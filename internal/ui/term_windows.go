package ui

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

const (
	enableProcessedInput            = 0x0001
	enableLineInput                 = 0x0002
	enableEchoInput                 = 0x0004
	enableVirtualTerminalInput      = 0x0200
	enableVirtualTerminalProcessing = 0x0004 // output handles
)

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procSetConsoleMode             = kernel32.NewProc("SetConsoleMode")
	procGetConsoleScreenBufferInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")
)

func consoleMode(f *os.File) (uint32, bool) {
	var mode uint32
	err := syscall.GetConsoleMode(syscall.Handle(f.Fd()), &mode)
	return mode, err == nil
}

func setConsoleMode(f *os.File, mode uint32) bool {
	r, _, _ := procSetConsoleMode.Call(f.Fd(), uintptr(mode))
	return r != 0
}

// IsTerminal reports whether f is a console.
func IsTerminal(f *os.File) bool {
	_, ok := consoleMode(f)
	return ok
}

func colorTerminal(f *os.File) bool { return EnableVT(f) }

// EnableVT makes the console behind the output f understand ANSI escape
// codes (Windows 10+) and reports whether it does.
func EnableVT(f *os.File) bool {
	mode, ok := consoleMode(f)
	if !ok {
		return false
	}
	return mode&enableVirtualTerminalProcessing != 0 || setConsoleMode(f, mode|enableVirtualTerminalProcessing)
}

// MakeRaw makes the console behind the input f deliver each key as it is
// pressed, without echo, with arrow keys as ANSI sequences and Ctrl+C as
// byte 3. restore puts the previous mode back.
func MakeRaw(f *os.File) (restore func(), err error) {
	mode, ok := consoleMode(f)
	if !ok {
		return nil, errors.New("não é um terminal")
	}
	raw := mode&^(enableEchoInput|enableProcessedInput|enableLineInput) | enableVirtualTerminalInput
	if !setConsoleMode(f, raw) {
		return nil, errors.New("o console não aceita o modo interativo")
	}
	return func() { setConsoleMode(f, mode) }, nil
}

type coord struct{ X, Y int16 }

type smallRect struct{ Left, Top, Right, Bottom int16 }

type screenBufferInfo struct {
	Size          coord
	Cursor        coord
	Attributes    uint16
	Window        smallRect
	MaxWindowSize coord
}

// Width returns the number of columns of the console behind f, or 0.
func Width(f *os.File) int {
	var info screenBufferInfo
	if r, _, _ := procGetConsoleScreenBufferInfo.Call(f.Fd(), uintptr(unsafe.Pointer(&info))); r == 0 {
		return 0
	}
	return int(info.Window.Right-info.Window.Left) + 1
}
