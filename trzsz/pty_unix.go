//go:build !windows

/*
MIT License

Copyright (c) 2023 Lonny Wong

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
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

type trzszPty struct {
	Stdin  io.ReadWriteCloser
	Stdout io.ReadWriteCloser
	ptmx   *os.File
	cmd    *exec.Cmd
	closed bool
}

func spawn(name string, arg ...string) (*trzszPty, error) {
	// spawn a pty
	cmd := exec.Command(name, arg...)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	return &trzszPty{Stdin: ptmx, Stdout: ptmx, ptmx: ptmx, cmd: cmd}, nil
}

func (t *trzszPty) OnResize(setTerminalColumns func(int)) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	go func() {
		for range ch {
			size, err := pty.GetsizeFull(os.Stdin)
			if err != nil {
				continue
			}
			_ = pty.Setsize(t.ptmx, size)
			setTerminalColumns(int(size.Cols))
		}
	}()
}

func (t *trzszPty) GetColumns() (int, error) {
	size, err := pty.GetsizeFull(os.Stdin)
	if err != nil {
		return 0, err
	}
	return int(size.Cols), nil
}

func (t *trzszPty) Close() {
	if t.closed {
		return
	}
	t.closed = true
	t.ptmx.Close()
}

func (t *trzszPty) Wait() {
	_ = t.cmd.Wait()
}

func (t *trzszPty) Terminate() {
	_ = t.cmd.Process.Signal(syscall.SIGTERM)
}

func (t *trzszPty) ExitCode() int {
	return t.cmd.ProcessState.ExitCode()
}

func syscallAccessWok(path string) error {
	return syscall.Access(path, unix.W_OK)
}

func syscallAccessRok(path string) error {
	return syscall.Access(path, unix.R_OK)
}

func enableVirtualTerminal() (uint32, uint32, error) {
	return 0, 0, nil
}

func resetVirtualTerminal(inMode, outMode uint32) error {
	return nil
}

func setupConsoleOutput() {
}
