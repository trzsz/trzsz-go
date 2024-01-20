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
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

func getParentWindowID() any {
	pid := getParentPid()
	for i := 0; i < 1000; i++ {
		kinfo, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
		if err != nil {
			return 0
		}
		ppid := kinfo.Eproc.Ppid
		switch ppid {
		case 0:
			return 0
		case 1:
			name := kinfo.Proc.P_comm[:]
			idx := bytes.IndexByte(name, '\x00')
			if idx > 0 && bytes.HasPrefix(name[:idx], []byte("iTerm")) {
				return "iTerm2"
			}
			return pid
		default:
			pid = int(ppid)
		}
	}
	return 0
}

func getParentPid() int {
	if _, tmux := os.LookupEnv("TMUX"); !tmux {
		return os.Getppid()
	}

	cmd := exec.Command("tmux", "display-message", "-p", "#{client_pid}")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		return os.Getppid()
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return os.Getppid()
	}

	return pid
}

var (
	warpOnce     sync.Once
	warpTerminal bool
)

func isWarpTerminal() bool {
	warpOnce.Do(func() {
		pid := os.Getppid()
		for i := 0; i < 1000; i++ {
			kinfo, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
			if err != nil {
				return
			}
			ppid := kinfo.Eproc.Ppid
			switch ppid {
			case 0:
				return
			case 1:
				cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=")
				out, err := cmd.Output()
				if err == nil && bytes.Contains(out, []byte("/Warp.app/")) {
					warpTerminal = true
				}
				return
			default:
				pid = int(ppid)
			}
		}
	})
	return warpTerminal
}
