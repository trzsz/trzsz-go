/*
MIT License

Copyright (c) 2022 Lonny Wong

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package trzsz

import (
	"context"
	"strings"
	"syscall"

	"github.com/UserExistsError/conpty"
	"golang.org/x/sys/windows"
)

type TrzszPty struct {
	Stdin    PtyIO
	Stdout   PtyIO
	cpty     *conpty.ConPty
	inMode   uint32
	outMode  uint32
	width    int
	height   int
	closed   bool
	exitCode *uint32
}

func getConsoleSize() (int, int, error) {
	handle, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil {
		return 0, 0, err
	}
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(windows.Handle(handle), &info); err != nil {
		return 0, 0, err
	}
	return int(info.Window.Right - info.Window.Left), int(info.Window.Bottom - info.Window.Top), nil
}

func enableVirtualTerminal() (uint32, uint32, error) {
	var inMode, outMode uint32
	inHandle, err := syscall.GetStdHandle(syscall.STD_INPUT_HANDLE)
	if err != nil {
		return 0, 0, err
	}
	if err := windows.GetConsoleMode(windows.Handle(inHandle), &inMode); err != nil {
		return 0, 0, err
	}
	if err := windows.SetConsoleMode(windows.Handle(inHandle), inMode|windows.ENABLE_VIRTUAL_TERMINAL_INPUT); err != nil {
		return 0, 0, err
	}

	outHandle, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil {
		return 0, 0, err
	}
	if err := windows.GetConsoleMode(windows.Handle(outHandle), &outMode); err != nil {
		return 0, 0, err
	}
	if err := windows.SetConsoleMode(windows.Handle(outHandle), outMode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
		return 0, 0, err
	}

	return inMode, outMode, nil
}

func resetVirtualTerminal(inMode, outMode uint32) error {
	inHandle, err := syscall.GetStdHandle(syscall.STD_INPUT_HANDLE)
	if err != nil {
		return err
	}
	if err := windows.SetConsoleMode(windows.Handle(inHandle), inMode); err != nil {
		return err
	}

	outHandle, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil {
		return err
	}
	if err := windows.SetConsoleMode(windows.Handle(outHandle), outMode); err != nil {
		return err
	}

	return nil
}

func Spawn(name string, args ...string) (*TrzszPty, error) {
	// get pty size
	width, height, err := getConsoleSize()
	if err != nil {
		return nil, err
	}

	// enable virtual terminal
	inMode, outMode, err := enableVirtualTerminal()
	if err != nil {
		return nil, err
	}

	// spawn a pty
	var cmdLine strings.Builder
	cmdLine.WriteString(strings.ReplaceAll(name, "\"", "\"\"\""))
	for _, arg := range args {
		cmdLine.WriteString(" \"")
		cmdLine.WriteString(strings.ReplaceAll(arg, "\"", "\"\"\""))
		cmdLine.WriteString("\"")
	}
	cpty, err := conpty.Start(cmdLine.String(), conpty.ConPtyDimensions(width, height))
	if err != nil {
		resetVirtualTerminal(inMode, outMode)
		return nil, err
	}

	return &TrzszPty{cpty, cpty, cpty, inMode, outMode, width, height, false, nil}, nil
}

func (t *TrzszPty) OnResize(cb func(int)) {
}

func (t *TrzszPty) GetColumns() (int, error) {
	return t.width, nil
}

func (t *TrzszPty) Close() {
	if t.closed {
		return
	}
	t.cpty.Close()
	resetVirtualTerminal(t.inMode, t.outMode)
	t.closed = true
}

func (t *TrzszPty) Wait() {
	code, _ := t.cpty.Wait(context.Background())
	t.exitCode = &code
}

func (t *TrzszPty) Terminate() {
	t.Close()
}

func (t *TrzszPty) ExitCode() int {
	if t.exitCode == nil {
		return 0
	}
	return int(*t.exitCode)
}
