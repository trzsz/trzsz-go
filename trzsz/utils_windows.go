/*
MIT License

Copyright (c) 2022-2026 The Trzsz Authors.

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
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"golang.org/x/sys/windows"
)

var user32 = windows.NewLazyDLL("user32.dll")

func getParentWindowID() uintptr {
	hwnd, _, _ := user32.NewProc("GetForegroundWindow").Call()
	return hwnd
}

var isWarpTerminal = func() bool {
	return false
}

func getSysProcAttr() *syscall.SysProcAttr {
	return nil
}

var getCygpath = func() func() string {
	var cygpathOnce sync.Once
	var cygpathPath string
	return func() string {
		cygpathOnce.Do(func() {
			handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(os.Getppid()))
			if err != nil {
				return
			}
			defer windows.CloseHandle(handle)

			var path [windows.MAX_PATH]uint16
			var pathLen uint32 = uint32(len(path))
			if err := windows.QueryFullProcessImageName(handle, 0, &path[0], &pathLen); err != nil {
				return
			}

			dir := filepath.Dir(windows.UTF16ToString(path[:pathLen]))
			cygpath := filepath.Join(dir, "cygpath.exe")
			if _, err := os.Stat(cygpath); err == nil {
				cygpathPath = cygpath
			}
		})
		return cygpathPath
	}
}()
