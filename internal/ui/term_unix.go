//go:build linux || darwin || freebsd

package ui

import (
	"os"
	"syscall"
	"unsafe"
)

func ioctl(f *os.File, req uintptr, arg unsafe.Pointer) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), req, uintptr(arg)); e != 0 {
		return e
	}
	return nil
}

func getTermios(f *os.File) (*syscall.Termios, error) {
	var t syscall.Termios
	if err := ioctl(f, ioctlGetTermios, unsafe.Pointer(&t)); err != nil {
		return nil, err
	}
	return &t, nil
}

// IsTerminal reports whether f is a terminal.
func IsTerminal(f *os.File) bool {
	_, err := getTermios(f)
	return err == nil
}

func colorTerminal(f *os.File) bool { return IsTerminal(f) }

// EnableVT reports whether the output f is a terminal (they all understand
// ANSI escape codes here).
func EnableVT(f *os.File) bool { return IsTerminal(f) }

// MakeRaw makes the terminal behind the input f deliver each key as it is
// pressed, without echo and with Ctrl+C as byte 3. restore puts the
// previous mode back.
func MakeRaw(f *os.File) (restore func(), err error) {
	old, err := getTermios(f)
	if err != nil {
		return nil, err
	}
	raw := *old
	raw.Iflag &^= syscall.ICRNL | syscall.IXON
	raw.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := ioctl(f, ioctlSetTermios, unsafe.Pointer(&raw)); err != nil {
		return nil, err
	}
	return func() { _ = ioctl(f, ioctlSetTermios, unsafe.Pointer(old)) }, nil
}

// Width returns the number of columns of the terminal behind f, or 0.
func Width(f *os.File) int {
	var ws struct{ Row, Col, X, Y uint16 }
	if err := ioctl(f, syscall.TIOCGWINSZ, unsafe.Pointer(&ws)); err != nil {
		return 0
	}
	return int(ws.Col)
}
