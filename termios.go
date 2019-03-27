package main

import (
	"syscall"
	"unsafe"
)

const (
	TERMIOS_CLEAR_ISIG      = (1 << 0)
	TERMIOS_RAW_CRNL_INPUT  = (1 << 1)
	TERMIOS_RAW_CRNL_OUTPUT = (1 << 2)
	TERMIOS_RAW_CRNL        = (TERMIOS_RAW_CRNL_INPUT | TERMIOS_RAW_CRNL_OUTPUT)
	TERMIOS_RAW_INPUT       = (1 << 3)
)

func GetTermiosAndMakeRaw(fd int, newterm, oldterm *syscall.Termios, flags int) error {
	_, _, err := syscall.Syscall6(
		syscall.SYS_IOCTL, uintptr(fd), syscall.TCGETS,
		uintptr(unsafe.Pointer(oldterm)), 0, 0, 0)
	*newterm = *oldterm
	/* Turn off buffered input (ICANON)
	 * Turn off echoing (ECHO)
	 * and separate echoing of newline (ECHONL, normally off anyway)
	 */
	newterm.Lflag &= ^uint32((syscall.ICANON | syscall.ECHO | syscall.ECHONL))
	if flags&TERMIOS_CLEAR_ISIG > 0 {
		/* dont recognize INT/QUIT/SUSP chars */
		newterm.Lflag &= ^uint32(syscall.ISIG)
	}

	/* reads will block only if < 1 char is available */
	newterm.Cc[syscall.VMIN] = 1
	/* no timeout (reads block forever) */
	newterm.Cc[syscall.VTIME] = 0
	/* IXON, IXOFF, and IXANY:
	 * IXOFF=1: sw flow control is enabled on input queue:
	 * tty transmits a STOP char when input queue is close to full
	 * and transmits a START char when input queue is nearly empty.
	 * IXON=1: sw flow control is enabled on output queue:
	 * tty will stop sending if STOP char is received,
	 * and resume sending if START is received, or if any char
	 * is received and IXANY=1.
	 */
	if flags&TERMIOS_RAW_CRNL_INPUT > 0 {
		/* IXON=0: XON/XOFF chars are treated as normal chars (why we do this?) */
		/* dont convert CR to NL on input */
		newterm.Iflag &= ^uint32(syscall.IXON | syscall.ICRNL)
	}

	if flags&TERMIOS_RAW_CRNL_OUTPUT > 0 {
		/* dont convert NL to CR+NL on output */
		newterm.Iflag &= ^uint32(syscall.ONLCR)
	}
	if flags&TERMIOS_RAW_INPUT > 0 {
		/* IXOFF=0: disable sending XON/XOFF if input buf is full
		* IXON=0: input XON/XOFF chars are not special
		* BRKINT=0: dont send SIGINT on break
		* IMAXBEL=0: dont echo BEL on input line too long
		* INLCR,ICRNL,IUCLC: dont convert anything on input
		 */
		newterm.Iflag &= ^uint32(syscall.IXOFF | syscall.IXON |
			syscall.IXANY | syscall.BRKINT | syscall.INLCR |
			syscall.ICRNL | syscall.IUCLC | syscall.IMAXBEL)
	}

	return err
}

func SetTermios(fd uintptr, oldterm *syscall.Termios) error {
	_, _, err := syscall.Syscall6(
		syscall.SYS_IOCTL, fd, syscall.TCSETS,
		uintptr(unsafe.Pointer(oldterm)), 0, 0, 0)
	return err
}

func SetTermiosToRaw(fd int, oldterm *syscall.Termios, flags int) error {
	var newterm syscall.Termios
	GetTermiosAndMakeRaw(fd, &newterm, oldterm, flags)
	_, _, err := syscall.Syscall6(
		syscall.SYS_IOCTL, uintptr(fd), syscall.TCSETS,
		uintptr(unsafe.Pointer(&newterm)), 0, 0, 0)
	return err
}
