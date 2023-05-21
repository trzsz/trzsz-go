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
	"sync"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

type trzszPty struct {
	Stdin  io.ReadWriteCloser
	Stdout io.ReadWriteCloser
	ptmx   *os.File
	cmd    *exec.Cmd
	ch     chan os.Signal
	resize func(int)
	mutex  sync.Mutex
	closed bool
}

func spawn(name string, arg ...string) (*trzszPty, error) {
	// spawn a pty
	cmd := exec.Command(name, arg...)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}

	// handle pty size
	ch := make(chan os.Signal, 1)
	tPty := &trzszPty{Stdin: ptmx, Stdout: ptmx, ptmx: ptmx, cmd: cmd, ch: ch}
	signal.Notify(ch, syscall.SIGWINCH)
	go func() {
		for range ch {
			_ = tPty.Resize()
		}
	}()
	ch <- syscall.SIGWINCH

	return tPty, nil
}

func (t *trzszPty) OnResize(cb func(int)) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.resize = cb
}

func (t *trzszPty) Resize() error {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if t.closed {
		return nil
	}
	size, err := pty.GetsizeFull(os.Stdin)
	if err != nil {
		return err
	}
	if err := pty.Setsize(t.ptmx, size); err != nil {
		return err
	}
	if t.resize != nil {
		t.resize(int(size.Cols))
	}
	return nil
}

func (t *trzszPty) GetColumns() (int, error) {
	size, err := pty.GetsizeFull(os.Stdin)
	if err != nil {
		return 0, err
	}
	return int(size.Cols), nil
}

func (t *trzszPty) Close() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if t.closed {
		return
	}
	t.closed = true
	signal.Stop(t.ch)
	close(t.ch)
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
