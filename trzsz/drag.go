/*
MIT License

Copyright (c) 2022-2025 The Trzsz Authors.

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
	"fmt"
	"os"
	"strings"

	"github.com/google/shlex"
)

func detectDragFiles(buf []byte) (dragFiles []string, hasDir bool, ignore bool, isWinPathPrefix bool) {
	if len(buf) < 2 { // fast return for keystrokes
		return nil, false, false, false
	}
	if len(buf) > 5 && bytes.Contains(buf, []byte("\x1b[20")) {
		buf = bytes.ReplaceAll(buf, []byte("\x1b[200~"), []byte(""))
		buf = bytes.ReplaceAll(buf, []byte("\x1b[201~"), []byte(""))
		if len(buf) == 0 {
			return nil, false, true, false
		}
	}

	if buf[0] == '\x10' { // for old warp terminal
		buf = buf[1:]
		if len(buf) < 2 {
			return nil, false, false, false
		}
	}
	if buf[0] == '\x1b' && len(buf) > 4 && buf[1] == 'i' && buf[2] == '\x10' { // for new warp terminal on MacOS
		buf = buf[3:]
	}

	if isRunningOnWindows() {
		return detectDragFilesOnWindows(buf)
	}

	paths, err := shlex.Split(string(buf))
	if err != nil {
		return nil, false, false, false
	}
	hasDir = false
	for _, path := range paths {
		if len(path) < 2 || path[0] != '/' { // not absolute path
			return nil, false, false, false
		}
		if !detectFilePath(path, &dragFiles, &hasDir) {
			return nil, false, false, false
		}
	}
	return dragFiles, hasDir, false, false
}

var detectFilePath = func(path string, dragFiles *[]string, hasDir *bool) bool {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false
	}
	if fileInfo.IsDir() {
		*hasDir = true
		*dragFiles = append(*dragFiles, path)
		return true
	}
	if fileInfo.Mode().IsRegular() {
		*dragFiles = append(*dragFiles, path)
		return true
	}
	return false
}

func detectDragFilesOnWindows(buf []byte) (dragFiles []string, hasDir bool, ignore bool, isWinPathPrefix bool) {
	length := len(buf)
	if length < 4 {
		return nil, false, false, false
	}

	if (buf[0] == '\'' && buf[1] == '/' && buf[2] >= 'a' && buf[2] <= 'z' && buf[3] == '/') ||
		(buf[0] == '/' && buf[1] >= 'a' && buf[1] <= 'z' && buf[2] == '/') {
		return detectDragFilesOnMSYS(buf)
	}

	if (length > 13 && string(buf[:11]) == "'/cygdrive/" && buf[11] >= 'a' && buf[11] <= 'z' && buf[12] == '/') ||
		(length > 12 && string(buf[:10]) == "/cygdrive/" && buf[10] >= 'a' && buf[10] <= 'z' && buf[11] == '/') {
		return detectDragFilesOnCygwin(buf)
	}

	if buf[length-1] == '"' && buf[0] >= 'A' && buf[0] <= 'Z' && buf[1] == ':' && buf[2] == '\\' &&
		bytes.IndexByte(buf[:length-1], '"') < 0 {
		// Cmd & PowerShell may lost the first `"`, and supports one path only.
		if detectFilePath(string(buf[:length-1]), &dragFiles, &hasDir) {
			return dragFiles, hasDir, false, false
		}
	}

	if length > 4 && buf[0] >= 'A' && buf[0] <= 'Z' && buf[1] == ':' && buf[2] == '\\' && buf[3] == '\\' {
		paths, err := shlex.Split(string(buf))
		if err != nil {
			return nil, false, false, false
		}
		for _, path := range paths {
			if len(path) < 4 || path[0] < 'A' || path[0] > 'Z' || path[1] != ':' || path[2] != '\\' { // not absolute path
				return nil, false, false, false
			}
			if !detectFilePath(path, &dragFiles, &hasDir) {
				return nil, false, false, false
			}
		}
		return dragFiles, hasDir, false, false
	}

	for idx := 0; idx < length; {
		path, inc, isPrefix := nextWinPath(buf[idx:])
		if path == "" {
			return nil, false, false, isPrefix
		}
		if !detectFilePath(path, &dragFiles, &hasDir) {
			return nil, false, false, isPrefix
		}
		idx += inc
	}
	return dragFiles, hasDir, false, false
}

func nextWinPath(buf []byte) (string, int, bool) {
	length := len(buf)
	if length < 4 {
		return "", 0, false
	}
	if buf[0] == '"' && buf[1] >= 'A' && buf[1] <= 'Z' && buf[2] == ':' && buf[3] == '\\' {
		idx := bytes.IndexByte(buf[1:], '"')
		if idx < 0 {
			return "", 0, true
		}
		idx++
		if idx+1 < length && buf[idx+1] != ' ' {
			return "", 0, false
		}
		return string(buf[1:idx]), idx + 2, false
	} else if buf[0] >= 'A' && buf[0] <= 'Z' && buf[1] == ':' && buf[2] == '\\' {
		idx := bytes.IndexByte(buf, ' ')
		if idx < 0 {
			return string(buf), length, true
		}
		return string(buf[:idx]), idx + 1, false
	}
	return "", 0, false
}

func detectDragFilesOnMSYS(buf []byte) (dragFiles []string, hasDir bool, ignore bool, isWinPathPrefix bool) {
	paths, err := shlex.Split(string(buf))
	if err != nil || len(paths) < 1 {
		return nil, false, false, false
	}
	for _, path := range paths {
		if len(path) < 4 || path[0] != '/' || path[1] < 'a' || path[1] > 'z' || path[2] != '/' { // not absolute path
			return nil, false, false, false
		}
		if !detectFilePath(unixPathToWinPath(path), &dragFiles, &hasDir) {
			return nil, false, false, false
		}
	}
	return dragFiles, hasDir, false, false
}

func detectDragFilesOnCygwin(buf []byte) (dragFiles []string, hasDir bool, ignore bool, isWinPathPrefix bool) {
	paths, err := shlex.Split(string(buf))
	if err != nil || len(paths) < 1 {
		return nil, false, false, false
	}
	for _, path := range paths {
		if len(path) < 13 || path[:10] != "/cygdrive/" || path[10] < 'a' || path[10] > 'z' || path[11] != '/' { // not absolute path
			return nil, false, false, false
		}
		if !detectFilePath(unixPathToWinPath(path[9:]), &dragFiles, &hasDir) {
			return nil, false, false, false
		}
	}
	return dragFiles, hasDir, false, false
}

func unixPathToWinPath(buf string) string {
	return fmt.Sprintf("%c:%s", buf[1], strings.ReplaceAll(string(buf[2:]), "/", "\\"))
}
