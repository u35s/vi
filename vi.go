package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"unicode"
	"unsafe"
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

/* Inverse/Normal text */
const ESC_BOLD_TEXT = ESC + "[7m"
const ESC_NORM_TEXT = ESC + "[m"

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

	screenbegin       int
	end               int
	tabstop           int
	dot               int
	cmd_mode          int
	cmdcnt            int
	line_number_width int
	modified_count    int
	erase_char        int
	last_input_char   byte

	term_orig syscall.Termios

	scr_out_buf         [MAX_SCR_COLS + MAX_TABSTOP*2]byte
	readbuffer          [KEYCODE_BUFFER_SIZE]byte
	get_input_line__buf [MAX_INPUT_LEN]byte
	current_filename    string
	status_buffer       bytes.Buffer
	last_search_pattern string
}

func (g *globals) init() {
	c := make(chan os.Signal)
	signal.Notify(c, syscall.SIGWINCH)
	go func() {
		for range c {
			g.query_screen_dimensions()
			g.new_screen(g.rows, g.columns)
			g.redraw(true)
		}
	}()
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

//----- Erase from cursor to end of line -----------------------
func (g *globals) clear_to_eol() {
	fmt.Printf(ESC_CLEAR2EOL)
}

func (g *globals) go_bottom_and_clear_to_eol() {
	g.place_cursor(g.rows-1, 0)
	g.clear_to_eol()
}

//----- Erase from cursor to end of screen -----------------------
func (g *globals) clear_to_eos() {
	fmt.Printf(ESC_CLEAR2EOS)
}

//----- Force refresh of all Lines -----------------------------
func (g *globals) redraw(full_screen bool) {
	g.place_cursor(0, 0+g.line_number_width)
	g.clear_to_eos()
	g.screen_erase()
	g.refresh(full_screen)
}

//----- Draw the status line at bottom of the screen -------------
func (g *globals) show_status_line() {
	if g.status_buffer.Len() == 0 {
	}
	if g.status_buffer.Len() > 0 {
		g.go_bottom_and_clear_to_eol()
		fmt.Printf("%s", g.status_buffer.Bytes())
		g.status_buffer.Reset()
		g.place_cursor(g.crow, g.ccol)
	}
}

func (g *globals) find_line(li int) int {
	var dot = 0
	for ; li > 1; li-- {
		dot = g.next_line(dot)
	}
	return dot
}

func (g *globals) format_line_number(src int) []byte {
	cnt := g.count_lines(g.text[:src])
	lastcnt := g.count_lines(g.text[src:])
	if lastcnt == 0 {
		g.line_number_width = 0
		return nil
	}
	wd := len(fmt.Sprintf("%d", cnt+lastcnt))
	g.line_number_width = wd + 1

	bts := StrToBytes(fmt.Sprintf("%"+fmt.Sprintf("%d", wd)+"d ", cnt+1))
	return bts
}

func (g *globals) format_line(src int) []byte {
	dest := g.scr_out_buf
	bts := g.format_line_number(src)
	copy(dest[:], bts)

	var c byte = '~'
	var co int = g.line_number_width
	for co < g.columns+g.tabstop {
		if src < g.end {
			c = g.text[src]
			src++
			if c == '\n' {
				break
			}
			if c < ' ' || c == 0x7f {
				if c == '\t' {
					c = ' '
					for (co-g.line_number_width)%g.tabstop != g.tabstop-1 {
						dest[co] = c
						co++
					}
				}
			}
		}
		dest[co] = c
		co++
		if src >= g.end {
			break
		}
	}
	// log.Printf("format line start %v, %s, co %v", src, dest[:co], co)
	if co < g.columns {
		for i := co; i < g.columns; i++ {
			dest[i] = ' '
		}
	}
	return dest[:]
}

func (g *globals) begin_line(d int) int { // return index to first char for cur line
	if d > 0 && d < len(g.text) {
		n := strings.LastIndexByte(BytesToStr(g.text[:d]), '\n')
		if n < 0 {
			return 0
		}
		return n + 1
	}
	return d
}

func (g *globals) end_line(p int) int {
	if p >= 0 && p < g.end {
		n := strings.IndexByte(BytesToStr(g.text[p:g.end]), '\n')
		if n < 0 {
			return g.end
		}
		return p + n
	}
	return p
}

func (g *globals) prev_line(p int) int {
	p = g.begin_line(p)
	if p > 0 && p <= g.end && g.text[p-1] == '\n' {
		p--
	}
	p = g.begin_line(p)
	return p
}

func (g *globals) next_line(p int) int {
	p = g.end_line(p)
	if p < g.end && g.text[p] == '\n' {
		p++
	}
	return p
}

func (g *globals) end_screen() int {
	q := g.screenbegin
	for cnt := 0; cnt < g.rows-2; cnt++ {
		q = g.next_line(q)
	}
	q = g.end_line(q)
	return q
}

//----- Synchronize the cursor to Dot --------------------------
func (g *globals) sync_cursor(d int, row, col *int) {
	var co, ro = 0, 0
	beg_cur := g.begin_line(d)
	//log.Printf("sync cursor beg_cur %d, screenbegin %d",
	// 	beg_cur, g.screenbegin)
	if beg_cur < g.screenbegin {
		// cnt := g.count_lines(g.text[beg_cur:g.screenbegin])
		g.screenbegin = beg_cur
	} else {
		end_scr := g.end_screen()
		if beg_cur > end_scr {
			cnt := g.count_lines(g.text[end_scr:beg_cur])
			log.Printf("sync cursor update screenbegin %v,%v", d, g.screenbegin)
			for ro = 0; ro < cnt; ro++ {
				g.screenbegin = g.next_line(g.screenbegin)
			}
		}
	}
	tp := g.screenbegin
	for ro = 0; ro < g.rows-1; ro++ {
		// log.Printf("sync cursor tp %d, beg_cur %d", tp, beg_cur)
		if tp == beg_cur {
			break
		}
		tp = g.next_line(tp)
	}

	// find out what col "d" is on
	for tp < d {
		if g.text[tp] == '\n' {
			break
		} else if g.text[tp] == '\t' {
			co = g.next_tabstop(co)
		} else if g.text[tp] < ' ' || g.text[tp] == 0x7f {
			co++ // display as ^X, use 2 columns
		}
		log.Printf("tp %d,co %d,d %d", tp, co, d)
		co++
		tp++
	}

	if g.text[d] == '\t' {
		co = co + (g.tabstop - 1)
	}
	*row = ro
	*col = co
	// log.Printf("sync cursor row %d,col %d", ro, co)
}

func (g *globals) next_tabstop(col int) int {
	return col + ((g.tabstop - 1) - (col % g.tabstop))
}

func (g *globals) refresh(full_screen bool) {
	g.sync_cursor(g.dot, &g.crow, &g.ccol)
	tp := g.screenbegin
	for li := 0; li < g.rows-1; li++ {
		out_buf := g.format_line(tp)
		if tp < g.end {
			n := strings.IndexByte(BytesToStr(g.text[tp:g.end]), '\n')
			if n < 0 {
				n = g.end - 1
			}
			tp += n + 1
		}
		// log.Printf("refresh tp %d", tp)

		changed := false
		var cs = 0
		ce := g.columns - 1
		sp := g.screen[li*g.columns:]
		if full_screen {
			changed = true
		} else {
			// compare newly formatted buffer with virtual screen
			// look backward for last difference between out_buf and screen
			for ; cs <= ce; cs++ {
				if out_buf[cs] != sp[cs] {
					changed = true
					break
				}
			}

			// look backward for last difference between out_buf and screen
			for ; ce >= cs; ce-- {
				if out_buf[ce] != sp[ce] {
					changed = true
					break
				}
			}
		}

		cs = TernaryInt(cs < 0, 0, cs)
		ce = TernaryInt(ce > g.columns-1, g.columns-1, ce)
		if cs > ce {
			cs, ce = 0, g.columns-1
		}
		if changed {
			// log.Printf("li:%d,%2d,%2d,cnt:%s-%s\n", li, cs, ce, sp[:ce+1], out_buf[:ce+1])
			copy(sp[cs:], out_buf[cs:ce+1])
			g.place_cursor(li, cs)
			fmt.Printf("%s", sp[cs:ce+1])
		}
	}
	g.place_cursor(g.crow, g.ccol+g.line_number_width)
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

func (g *globals) dot_next() {
	g.dot = g.next_line(g.dot)
}

func (g *globals) dot_begin() {
	g.dot = g.begin_line(g.dot)
}

func (g *globals) char_search(p int, pat string, dir_and_range int) int {
	if p < 0 || p >= g.end {
		return -1
	}
	if dir_and_range > 0 {
		// log.Printf("char search:%v,pat:%v,text:%s", p, pat, g.text[p:g.end])
		n := strings.Index(BytesToStr(g.text[p:g.end]), pat)
		if n >= 0 {
			return g.dot + n
		}
	} else {
		n := strings.LastIndex(BytesToStr(g.text[0:p]), pat)
		if n >= 0 {
			return n
		}
	}
	return -1
}

func (g *globals) dot_left() {
	if g.dot > 0 && g.text[g.dot-1] != '\n' {
		g.dot--
	}
}

func (g *globals) dot_prev() {
	g.dot = g.prev_line(g.dot)
}

func (g *globals) dot_end() {
	g.dot = g.end_line(g.dot)
}

func (g *globals) move_to_col(p int, l int) int {
	var co int = 0
	p = g.begin_line(p)
	for co < l && p < g.end {
		if g.text[p] == '\n' {
			break
		}
		if g.text[p] == '\t' {
			co = g.next_tabstop(co)
		} else if g.text[p] < ' ' || g.text[p] == 127 {
			co++ // display as ^X, use 2 columns
		}
		co++
		p++
	}
	log.Printf("move to col %d,p %d", co, p)
	return p
}

func (g *globals) dot_right() {
	if g.dot < g.end-1 && g.text[g.dot+1] != '\n' {
		g.dot++
	}
}

func (g *globals) dot_scroll(cnt, dir int) {
	for ; cnt > 0; cnt-- {
		if dir < 0 {
			// scroll Backwards
			// ctrl-Y scroll up one line
			g.screenbegin = g.prev_line(g.screenbegin)
		} else {
			// scroll Forwards
			// ctrl-E scroll down one line
			g.screenbegin = g.next_line(g.screenbegin)
		}
	}
	// make sure "dot" stays on the screen so we dont scroll off
	if g.dot < g.screenbegin {
		g.dot = g.screenbegin
	}
	q := g.end_screen() // find new bottom line
	if g.dot > q {
		g.dot = g.begin_line(q) // is dot is below bottom line?
	}
	g.dot_skip_over_ws()
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
	case 2: // ctrl-b  scroll up full screen
		g.dot_scroll(g.rows-2, -1)
	case 4: // ctrl-D  scroll down half screen
		g.dot_scroll((g.rows-2)/2, 1)
	case 5: // ctrl-E  scroll down one line
		g.dot_scroll(1, 1)
	case 6: // ctrl-f  scroll down full screen
		g.dot_scroll(g.rows-2, 1)
	case '/', '?':
		s := g.get_input_line(string(c))
		if len(s) == 1 { // if no pat re-use old pat

		} else {
			g.last_search_pattern = s
			p := g.char_search(g.dot+1, s[1:], TernaryInt(c == '/', 1, -1))
			if p >= 0 {
				g.dot = p + 1
			}
		}
	case 'n', 'N':
		s := g.last_search_pattern
		log.Printf("%s %s,", string(byte(c)), s)
		if len(s) > 0 {
			p := g.char_search(g.dot+1, s[1:], TernaryInt(c == 'n', 1, -1))
			log.Printf("%s %s,cur %d p %d,end %d", string(byte(c)), s, g.dot, p, g.end)
			if p >= 0 {
				g.dot = p + 1
			}
		}
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		if c == '0' && g.cmdcnt < 1 {
			g.dot_begin()
		} else {
			g.cmdcnt = g.cmdcnt*10 + (c - '0')
		}
	case 27: // esc
		g.cmd_mode = 0
	case ':': //:- the colon mode commands
		p := g.get_input_line(":") // get input line- use "status line"
		g.colon(p)                 // execute the command
	case 'a':
		if g.text[g.dot] != '\n' {
			g.dot++
			g.cmd_mode = 1
		}
	case 'A':
		g.dot_end()
		g.cmd_mode = 1 // start inserting
	case '$':
		g.dot_end()
	case 'i', KEYCODE_INSERT: // i- insert before current char // Cursor Key Insert
		// dc_i:
		g.cmd_mode = 1 // start inserting
	case 'g', 'G':
		if c == 'g' {
			c1 := g.get_one_char()
			if c1 != 'g' {
				break
			}
			if g.cmdcnt == 0 {
				g.cmdcnt = 1
			}
		}
		g.dot = g.end - 1
		if g.cmdcnt > 0 {
			g.dot = g.find_line(g.cmdcnt)
		}
		g.dot_skip_over_ws()
	case 'h':
		DoWhile(g.dot_left, func() bool { g.cmdcnt--; return g.cmdcnt <= 0 })
	case 'j':
		DoWhile(func() {
			g.dot_next()
			g.dot = g.move_to_col(g.dot, g.ccol)
		}, func() bool { g.cmdcnt--; return g.cmdcnt <= 0 })
	case 'k':
		DoWhile(func() {
			g.dot_prev()
			g.dot = g.move_to_col(g.dot, g.ccol)
		}, func() bool { g.cmdcnt--; return g.cmdcnt <= 0 })
	case 'l':
		DoWhile(g.dot_right, func() bool { g.cmdcnt--; return g.cmdcnt <= 0 })
	case 'O':
		g.dot_begin()
		g.dot = g.char_insert(g.dot, '\n')
		g.dot_prev()
		g.cmd_mode = 1
	case 'o':
		g.dot_end()
		g.dot = g.char_insert(g.dot, '\n')
		g.cmd_mode = 1
	case 'r': // r- replace the current char with user input
		c1 := g.get_one_char() // get the replacement char
		if g.text[g.dot] != '\n' {
			g.text[g.dot] = byte(c1)
		}
	case '~': // ~- flip the case of letters   a-z -> A-Z
		DoWhile(func() {
			if unicode.IsLower(rune(g.text[g.dot])) {
				g.text[g.dot] = byte(unicode.ToUpper(rune(g.text[g.dot])))
			} else if unicode.IsUpper(rune(g.text[g.dot])) {
				g.text[g.dot] = byte(unicode.ToLower(rune(g.text[g.dot])))
			}
			g.dot_right()
		}, func() bool { g.cmdcnt--; return g.cmdcnt <= 0 })
	}
dc1:
	if !unicode.IsDigit(rune(c)) {
		g.cmdcnt = 0
	}
}

func (g *globals) dot_skip_over_ws() {
	b := g.text[g.dot]
	for unicode.IsSpace(rune(b)) && b != '\n' && g.dot < g.end-1 {
		g.dot++
		b = g.text[g.dot]
	}
}

func (g *globals) colon(c string) {
	if len(c) == 0 {
		return
	}
	if c[0] == ':' {
		c = c[1:]
	}
	if strings.HasPrefix(c, "quit") || strings.HasPrefix(c, "q!") {
		g.editing = 0
		return
	}
	if strings.HasPrefix(c, "write") || strings.HasPrefix(c, "wq") || c == "x" {
		var err error
		if g.modified_count != 0 || c != "x" {
			err = g.file_write(g.current_filename, g.text[:g.end])
		}
		if err != nil {
			g.status_line_bold("Write error: %v", err)
		} else {
			g.modified_count = 0
			g.status_line("%s %dL %dC written",
				g.current_filename, g.count_lines(g.text[:g.end]), g.end)
			if c == "x" || c[1] == 'q' {
				g.editing = 0
			}
		}
		return
	}
}

func (g *globals) count_lines(cnt []byte) int {
	return strings.Count(BytesToStr(cnt), "\n")
}

func (g *globals) status_line_bold(f string, a ...interface{}) {
	g.status_buffer.WriteString(ESC_BOLD_TEXT)
	fmt.Fprintf(&g.status_buffer, f, a...)
	g.status_buffer.WriteString(ESC_NORM_TEXT)
}

func (g *globals) status_line(f string, a ...interface{}) {
	fmt.Fprintf(&g.status_buffer, f, a...)
}

func (g *globals) file_write(f string, cnt []byte) error {
	return ioutil.WriteFile(f, cnt, 0666)
}

func (g *globals) query_screen_dimensions() {
	var winsize = &struct {
		Row    uint16
		Col    uint16
		Xpixel uint16
		Ypixel uint16
	}{}
	retCode, _, _ := syscall.Syscall(syscall.SYS_IOCTL,
		uintptr(syscall.Stdin),
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(winsize)))

	if int(retCode) == -1 {
	} else {
		g.rows = int(winsize.Row)
		g.columns = int(winsize.Col)
	}
}

func (g *globals) text_hole_make(p int, size int) int {
	var bias int = 0
	if size <= 0 {
		return bias
	}
	g.end += size
	log.Printf("g.end - %d", g.end)
	if g.end >= len(g.text) {
		new_text := make([]byte, g.end+10240)
		copy(new_text, g.text)
		g.text = new_text
	}
	copy(g.text[p+size:], g.text[p:g.end-size])
	/*for i := 0; i < size; i++ {
		g.text[p+i] = ' '
	}*/
	return bias
}

func (g *globals) stupid_insert(p int, c int) int {
	bias := g.text_hole_make(p, 1)
	p += bias
	g.text[p] = byte(c)
	return bias
}

func (g *globals) file_insert(f string, p int, initial bool) int {
	var cnt int = -1
	var err error
	var file *os.File

	if p < 0 {
		p = 0
	}
	if p > g.end {
		p = g.end
	}
	file, err = os.Open(f)
	if err != nil {
		if !initial {
		}
		return cnt
	}
	defer file.Close()
	stat, _ := file.Stat()
	var size = int(stat.Size())
	n := g.text_hole_make(p, size)
	cnt, err = file.Read(g.text[p : p+size])
	if err != nil {
	} else if cnt < n {
	}
	return cnt
}

func (g *globals) char_insert(p int, c int) int {
	if c == 27 { // Is this an ESC?
		g.cmd_mode = 0
		g.cmdcnt = 0
	} else {
		if c == '\r' {
			c = '\n'
		}
		g.modified_count++
		p += 1 + g.stupid_insert(p, c)
	}
	return p
}

func (g *globals) init_text_buffer(f string) {
	g.text = make([]byte, 10240)
	g.screenbegin = 0
	g.dot = 0
	g.end = 0
	if f != g.current_filename {
		g.current_filename = f
	}
	rc := g.file_insert(f, g.dot, true)
	if rc < 0 {
		g.char_insert(g.dot, '\n')
	}
	g.modified_count = 0
}

func (g *globals) edit_file(f string) {
	g.editing = 1 // 0 = exit, 1 = one file, 2 = multiple files
	g.rawmode()
	g.rows = 24
	g.columns = 80
	g.query_screen_dimensions()
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
			g.show_status_line()
		}
	}
	g.cookmode()
}

//----- IO Routines --------------------------------------------
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

// Get input line (uses "status line" area)
func (g *globals) get_input_line(prompt string) string {
	buf := g.get_input_line__buf
	copy(buf[:], prompt)
	g.go_bottom_and_clear_to_eol()
	fmt.Printf(prompt)

	var c int
	i := len(prompt)
	for i < MAX_INPUT_LEN {
		c = g.get_one_char()
		if c == '\n' || c == '\r' || c == 27 {
			break
		}
		if c == g.erase_char || c == 8 || c == 127 {
			i--
			buf[i] = ' '
			fmt.Printf("\b \b")
			if i <= 0 {
				break
			}
		} else if c > 0 && c < 256 {
			buf[i] = byte(c)
			i++
			fmt.Printf("%s", string(byte(c)))
		}
	}
	g.refresh(false)
	return string(buf[:i])
}

//----- Set terminal attributes --------------------------------
func (g *globals) rawmode() error {
	SetTermiosToRaw(int(os.Stdin.Fd()), &g.term_orig, TERMIOS_RAW_CRNL)
	g.erase_char = int(g.term_orig.Cc[syscall.VERASE])
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
