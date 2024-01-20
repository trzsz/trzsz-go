/*
MIT License

Copyright (c) 2022-2024 The Trzsz Authors.

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
	"io"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/UserExistsError/conpty"
	"golang.org/x/sys/windows"
)

type trzszPty struct {
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	cpty      *conpty.ConPty
	width     int
	height    int
	closed    atomic.Bool
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

func enableVirtualTerminal() error {
	var inMode, outMode uint32
	inHandle, err := syscall.GetStdHandle(syscall.STD_INPUT_HANDLE)
	if err != nil {
		return err
	}
	if err := windows.GetConsoleMode(windows.Handle(inHandle), &inMode); err != nil {
		return err
	}
	onExitFuncs = append(onExitFuncs, func() {
		windows.SetConsoleMode(windows.Handle(inHandle), inMode)
	})
	if err := windows.SetConsoleMode(windows.Handle(inHandle), inMode|windows.ENABLE_VIRTUAL_TERMINAL_INPUT); err != nil {
		return err
	}

	outHandle, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil {
		return err
	}
	if err := windows.GetConsoleMode(windows.Handle(outHandle), &outMode); err != nil {
		return err
	}
	onExitFuncs = append(onExitFuncs, func() {
		windows.SetConsoleMode(windows.Handle(outHandle), outMode)
	})
	if err := windows.SetConsoleMode(windows.Handle(outHandle),
		outMode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING|windows.DISABLE_NEWLINE_AUTO_RETURN); err != nil {
		return err
	}

	return nil
}

func setupVirtualTerminal() error {
	// enable virtual terminal
	if err := enableVirtualTerminal(); err != nil {
		return err
	}

	// set code page to UTF8
	inCP := getConsoleCP()
	outCP := getConsoleOutputCP()
	setConsoleCP(CP_UTF8)
	setConsoleOutputCP(CP_UTF8)
	onExitFuncs = append(onExitFuncs, func() {
		setConsoleCP(inCP)
		setConsoleOutputCP(outCP)
	})

	return nil
}

func spawn(name string, args ...string) (*trzszPty, error) {
	// get pty size
	width, height, err := getConsoleSize()
	if err != nil {
		return nil, err
	}

	// spawn a pty
	var cmdLine strings.Builder
	cmdLine.WriteString(windows.EscapeArg(name))
	for _, arg := range args {
		cmdLine.WriteString(" ")
		cmdLine.WriteString(windows.EscapeArg(arg))
	}

	cpty, err := conpty.Start(cmdLine.String(), conpty.ConPtyDimensions(width, height))
	if err != nil {
		return nil, err
	}

	return &trzszPty{
		stdin:     cpty,
		stdout:    cpty,
		cpty:      cpty,
		width:     width,
		height:    height,
		startTime: time.Now(),
	}, nil
}

func (t *trzszPty) OnResize(setTerminalColumns func(int32)) {
	go func() {
		for {
			time.Sleep(1 * time.Second)
			if t.closed.Load() {
				break
			}
			width, height, err := getConsoleSize()
			if err != nil {
				continue
			}
			if t.width != width || t.height != height {
				t.width = width
				t.height = height
				t.cpty.Resize(width, height)
				if setTerminalColumns != nil {
					setTerminalColumns(int32(width))
				}
			}
		}
	}()
}

func (t *trzszPty) GetColumns() (int32, error) {
	return int32(t.width), nil
}

func (t *trzszPty) Close() {
	if t.closed.Load() {
		return
	}
	t.closed.Store(true)
	t.cpty.Close()
}

func (t *trzszPty) Wait() {
	code, _ := t.cpty.Wait(context.Background())
	t.exitCode = &code
}

func (t *trzszPty) Terminate() {
	t.Close()
}

func (t *trzszPty) ExitCode() int {
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

func setupConsoleOutput() {
	os.Stdout.WriteString("\x1b[?1049h\x1b[H\x1b[2J")

	logo := []string{
		"ooooooooooooo      ooooooooo.         oooooooooooo       .oooooo..o       oooooooooooo",
		"8'   888   '8      `888   `Y88.      d'''''''d888'      d8P'    `Y8      d'''''''d888'",
		"     888            888   .d88'            .888P        Y88bo.                 .888P  ",
		"     888            888ooo88P'            d888'          `'Y8888o.            d888'   ",
		"     888            888`88b.            .888P                `'Y88b         .888P     ",
		"     888            888  `88b.         d888'    .P      oo     .d8P        d888'    .P",
		"    o888o          o888o  o888o      .888d888d88P       d888d88P'        .888d888d88P ",
	}
	width, height, err := getConsoleSize()
	if err != nil {
		return
	}
	if width <= len(logo[0]) || height <= len(logo)+2 {
		return
	}
	pad := (width - len(logo[0])) / 2
	for _, s := range logo {
		os.Stdout.WriteString(strings.Repeat(" ", pad) + s + "\r\n")
	}
}
