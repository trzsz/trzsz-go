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
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/UserExistsError/conpty"
	"golang.org/x/sys/windows"
)

type TrzszPty struct {
	Stdin     PtyIO
	Stdout    PtyIO
	cpty      *conpty.ConPty
	inCP      uint32
	outCP     uint32
	inMode    uint32
	outMode   uint32
	width     int
	height    int
	closed    bool
	exitCode  *uint32
	startTime time.Time
}

const CP_UTF8 uint32 = 65001

var kernel32 = windows.NewLazyDLL("kernel32.dll")

func getConsoleCP() uint32 {
	result, _, _ := kernel32.NewProc("GetConsoleCP").Call()
	return uint32(result)
}

func getConsoleOutputCP() uint32 {
	result, _, _ := kernel32.NewProc("GetConsoleOutputCP").Call()
	return uint32(result)
}

func setConsoleCP(cp uint32) {
	kernel32.NewProc("SetConsoleCP").Call(uintptr(cp))
}

func setConsoleOutputCP(cp uint32) {
	kernel32.NewProc("SetConsoleOutputCP").Call(uintptr(cp))
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
	return int(info.Window.Right-info.Window.Left) + 1, int(info.Window.Bottom-info.Window.Top) + 1, nil
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
	if err := windows.SetConsoleMode(windows.Handle(outHandle),
		outMode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING|windows.DISABLE_NEWLINE_AUTO_RETURN); err != nil {
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

	// set code page to UTF8
	inCP := getConsoleCP()
	outCP := getConsoleOutputCP()
	setConsoleCP(CP_UTF8)
	setConsoleOutputCP(CP_UTF8)

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
		setConsoleCP(inCP)
		setConsoleOutputCP(outCP)
		resetVirtualTerminal(inMode, outMode)
		return nil, err
	}

	return &TrzszPty{cpty, cpty, cpty, inCP, outCP, inMode, outMode, width, height, false, nil, time.Now()}, nil
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
	t.closed = true
	t.cpty.Close()
	setConsoleCP(t.inCP)
	setConsoleOutputCP(t.outCP)
	resetVirtualTerminal(t.inMode, t.outMode)
	if time.Now().Sub(t.startTime) > 10*time.Second {
		time.Sleep(100 * time.Millisecond)
		cmd := exec.Command("cmd", "/c", "cls")
		cmd.Stdout = os.Stdout
		cmd.Run()
	}
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

func syscallAccessWok(path string) error {
	return nil
}

func syscallAccessRok(path string) error {
	return nil
}
