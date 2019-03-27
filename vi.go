package main

import (
	"fmt"
	"os"
	"syscall"
	"time"
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
	editing   int
	term_orig syscall.Termios
}

func (g *globals) init() {
}

func main() {
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

func (g *globals) edit_file(f string) {
	g.editing = 1 // 0 = exit, 1 = one file, 2 = multiple files
	g.rawmode()
	for g.editing > 0 {
		fmt.Printf("hello world!")
		fmt.Printf(ESC_SET_CURSOR_POS, 1, 1)
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
