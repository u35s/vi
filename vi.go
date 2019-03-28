package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"syscall"
)

const (
	MAX_TABSTOP = 32 // sanity limit
	// User input len. Need not be extra big.
	// Lines in file being edited *can* be bigger than this.
	MAX_INPUT_LEN = 128
	// Sanity limits. We have only one buffer of this size.
	MAX_SCR_COLS = 4096
	MAX_SCR_ROWS = 4096
)

const ESC = "\033"

/* Clear-to-end-of-line */
const ESC_CLEAR2EOL = ESC + "[K"

/* Clear-to-end-of-screen.
 * (We use default param here.
 * Full sequence is "ESC [ <num> J",
 * <num> is 0/1/2 = "erase below/above/all".)
 */
const ESC_CLEAR2EOS = ESC + "[J"

/* Cursor to given coordinate (1,1: top left) */
const ESC_SET_CURSOR_POS = ESC + "[%d;%dH"

type globals struct {
	screen        []byte
	text          []byte
	editing       int
	rows, columns int // the terminal screen is this size
	crow, ccol    int // cursor is on Crow x Ccol

	screenbegin int
	end         int
	tabstop     int
	dot         int
	cmd_mode    int

	last_input_char byte

	term_orig syscall.Termios

	scr_out_buf [MAX_SCR_COLS + MAX_TABSTOP*2]byte
	readbuffer  [KEYCODE_BUFFER_SIZE]byte
}

func (g *globals) init() {
}

//----- Terminal Drawing ---------------------------------------
// The terminal is made up of 'rows' line of 'columns' columns.
// classically this would be 24 x 80.
//  screen coordinates
//  0,0     ...     0,79
//  1,0     ...     1,79
//  .       ...     .
//  .       ...     .
//  22,0    ...     22,79
//  23,0    ...     23,79   <- status line

//----- Move the cursor to row x col (count from 0, not 1) -------
func (g *globals) place_cursor(row, col int) {
	row = TernaryInt(row < 0, 0, row)
	row = TernaryInt(row >= g.rows, g.rows-1, row)
	col = TernaryInt(col < 0, 0, col)
	col = TernaryInt(col >= g.columns, g.columns-1, col)
	fmt.Printf(ESC_SET_CURSOR_POS, row+1, col+1)
}

//----- Erase from cursor to end of screen -----------------------
func (g *globals) clear_to_eos() {
	fmt.Printf(ESC_CLEAR2EOS)
}

//----- Force refresh of all Lines -----------------------------
func (g *globals) redraw(full_screen bool) {
	g.place_cursor(0, 0)
	g.clear_to_eos()
	g.screen_erase()
	g.refresh(full_screen)
}

func (g *globals) format_line(src int) []byte {
	dest := g.scr_out_buf
	var c byte = '~'
	var co int = 0
	for co < g.columns+g.tabstop {
		if src < g.end {
			c = g.text[src]
			src++
			if c == '\n' {
				break
			}
		}
		dest[co] = c
		co++
		if src >= g.end {
			break
		}
	}
	return dest[:co]
}

func (g *globals) begin_line(d int) int { // return index to first char for cur line
	if d > 0 && d < len(g.text) {
		n := strings.LastIndex(string(g.text[:d]), "\n")
		if n < 0 {
			return 0
		}
		return n + 1
	}
	return d
}

func (g *globals) end_line(p int) int {
	if p >= 0 && p < g.end {
		n := strings.Index(string(g.text[p:g.end]), "\n")
		if n < 0 {
			return g.end
		}
		return p + n
	}
	return p
}
func (g *globals) next_line(p int) int {
	p = g.end_line(p)
	log.Printf("end line p %d, g.end %d", p, g.end)
	if p < g.end && g.text[p] == '\n' {
		p++
	}
	return p
}

//----- Synchronize the cursor to Dot --------------------------
func (g *globals) sync_cursor(d int, row, col *int) {
	var co, ro = 0, 0
	beg_cur := g.begin_line(d)
	log.Printf("sync cursor beg_cur %d, screenbegin %d",
		beg_cur, g.screenbegin)
	if beg_cur < g.screenbegin {
	} else {
	}
	tp := g.screenbegin
	for ro = 0; ro < g.rows-1; ro++ {
		log.Printf("sync cursor tp %d, beg_cur %d", tp, beg_cur)
		if tp == beg_cur {
			break
		}
		tp = g.next_line(tp)
	}

	// find out what col "d" is on
	for tp < d {
		co++
		tp++
	}
	*row = ro
	*col = co
	log.Printf("sync cursor row %d,col %d", ro, co)
}

func (g *globals) refresh(full_screen bool) {
	g.sync_cursor(g.dot, &g.crow, &g.ccol)
	tp := g.screenbegin
	for li := 0; li < g.rows-1; li++ {
		out_buf := g.format_line(tp)
		if tp < g.end {
			n := strings.Index(string(g.text[tp:g.end]), "\n")
			if n < 0 {
				n = g.end - 1
			}
			tp = n + 1
		}

		changed := false
		var cs = 0
		ce := g.columns - 1
		sp := g.screen[li*g.columns:]

		// compare newly formatted buffer with virtual screen
		// look backward for last difference between out_buf and screen
		for ; cs <= ce; cs++ {
			if cs < len(out_buf) && out_buf[cs] != sp[cs] {
				changed = true
				break
			}
		}

		// look backward for last difference between out_buf and screen
		for ; ce >= cs; ce-- {
			if ce < len(out_buf) && out_buf[ce] != sp[ce] {
				changed = true
				break
			}
		}
		log.Printf("%s,%d,%d,li:%d\n", out_buf, cs, ce, li)
		if changed {
			copy(sp[cs:], out_buf[cs:ce+1])
			g.place_cursor(li, cs)
			fmt.Printf("%s", sp[cs:ce+1])
		}
	}
	g.place_cursor(g.crow, g.ccol)
}

func (g *globals) screen_erase() {
	for i := range g.screen {
		g.screen[i] = ' '
	}
}

func (g *globals) new_screen(row, col int) {
	g.screen = make([]byte, row*col+8)
	for li := 1; li < row-1; li++ {
		g.screen[li*col] = '~'
	}
}

func (g *globals) get_one_char() int {
	var c [1]byte
	n, err := os.Stdin.Read(c[:])
	if err != nil || n != 1 {
		log.Printf("read n %v, err %v", n, err)
		g.cookmode()
		os.Exit(1)
	}
	return int(c[0])
}

func (g *globals) do_cmd(c int) {
	log.Printf("do cmd %d", c)
	switch c {
	case
		KEYCODE_UP,
		KEYCODE_DOWN,
		KEYCODE_LEFT,
		KEYCODE_RIGHT,
		KEYCODE_HOME,
		KEYCODE_END,
		KEYCODE_PAGEUP,
		KEYCODE_PAGEDOWN,
		KEYCODE_DELETE:
		goto key_cmd_mode
	}
	if g.cmd_mode == 2 {
	}
	if g.cmd_mode == 1 {
		if 1 <= c || strconv.IsPrint(rune(c)) {
			g.dot = g.char_insert(g.dot, c)
		}
		goto dc1
	}
key_cmd_mode:
	switch c {
	case 27: // esc
		g.cmd_mode = 0
	case 'i', KEYCODE_INSERT: // i- insert before current char // Cursor Key Insert
		// dc_i:
		g.cmd_mode = 1 // start inserting
	}
dc1:
}

func (g *globals) text_hole_make(p int, size int) int {
	var bias int = 0
	if size <= 0 {
		return bias
	}
	g.end += size
	log.Printf("g.end - %d", g.end)
	if g.end >= len(g.text) {
	}
	copy(g.text[p+size:], g.text[p:g.end-size])
	for i := 0; i < size; i++ {
		g.text[p+i] = ' '
	}
	return bias
}

func (g *globals) stupid_insert(p int, c int) int {
	bias := g.text_hole_make(p, 1)
	p += bias
	g.text[p] = byte(c)
	return bias
}

func (g *globals) char_insert(p int, c int) int {
	if c == 27 { // Is this an ESC?
		g.cmd_mode = 0
	} else {
		if c == '\r' {
			c = '\n'
		}
		p += 1 + g.stupid_insert(p, c)
	}
	return p
}

func (g *globals) init_text_buffer(f string) {
	g.text = make([]byte, 10240)
	g.screenbegin = 0
	g.end = 0
}

func (g *globals) edit_file(f string) {
	g.editing = 1 // 0 = exit, 1 = one file, 2 = multiple files
	g.rawmode()
	g.rows = 24
	g.columns = 80
	g.new_screen(g.rows, g.columns)
	g.init_text_buffer(f)

	g.crow = 0
	g.ccol = 0
	g.cmd_mode = 0 // 0=command  1=insert  2='R'eplace
	g.tabstop = 8
	g.redraw(false)

	var c int
	for g.editing > 0 {
		c = g.get_one_char()
		g.last_input_char = byte(c)
		g.do_cmd(c)
		if g.readbuffer[0] == 0 {
			g.refresh(false)
		}
	}
	g.cookmode()
}

//----- Set terminal attributes --------------------------------
func (g *globals) rawmode() error {
	SetTermiosToRaw(int(os.Stdin.Fd()), &g.term_orig, TERMIOS_RAW_CRNL)
	return nil
}

func (g *globals) cookmode() {
	SetTermios(os.Stdin.Fd(), &g.term_orig)
}

func main() {
	file, err := os.Create("./vi.log")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer file.Close()
	log.SetOutput(file)
	// syscall.Dup3(int(file.Fd()), int(os.Stderr.Fd()), 0)
	// var c int
	var g globals
	g.init()

	//----- This is the main file handling loop --------------
	// "Save cursor, use alternate screen buffer, clear screen"
	fmt.Printf(ESC + "[?1049h")
	if len(os.Args) > 1 {
		for i := 1; i < len(os.Args); i++ {
			g.edit_file(os.Args[i])
		}
	} else {
		g.edit_file("")
	}
	// "Use normal screen buffer, restore cursor"
	fmt.Printf(ESC + "[?1049l")
	//-----------------------------------------------------------
	os.Exit(0)
}
