package main

import (
	"fmt"
	"log"
	"os"
	"syscall"
	"time"
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

	term_orig syscall.Termios

	scr_out_buf [MAX_SCR_COLS + MAX_TABSTOP*2]byte
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
		}
		dest[co] = c
		co++
		if src >= g.end {
			break
		}
	}
	return dest[:co]
}

func (g *globals) refresh(full_screen bool) {
	// g.sync_cursor(g.dot, &g.crow, &g.ccol)
	tp := g.screenbegin
	for li := 0; li < g.rows-1; li++ {
		out_buf := g.format_line(tp)

		changed := false
		var cs = 0
		ce := g.columns - 1
		sp := g.screen[li*g.columns:]

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
			if ce < len(out_buf) && out_buf[ce] != sp[ce] {
				changed = true
				break
			}
		}
		if changed {
			log.Printf("%s,%d,%d\n", out_buf, cs, ce)
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

	g.tabstop = 8
	g.redraw(false)

	for g.editing > 0 {
		time.Sleep(1e9 * 5)
		g.editing = 0
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
