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
	"fmt"
	"os"
	"strings"
)

func detectDragFiles(buf []byte) ([]string, bool, bool) {
	if len(buf) > 5 && bytes.Contains(buf, []byte("\x1b[20")) {
		buf = bytes.ReplaceAll(buf, []byte("\x1b[200~"), []byte(""))
		buf = bytes.ReplaceAll(buf, []byte("\x1b[201~"), []byte(""))
		if len(buf) == 0 {
			return nil, false, true
		}
	}
	if isRunningOnLinux() {
		return detectDragFilesOnLinux(buf)
	} else if isRunningOnMacOS() {
		return detectDragFilesOnMacOS(buf)
	} else if isRunningOnWindows() {
		return detectDragFilesOnWindows(buf)
	}
	return nil, false, false
}

func detectFilePath(path string, dragFiles *[]string, hasDir *bool) bool {
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

func detectDragFilesOnLinux(buf []byte) ([]string, bool, bool) {
	length := len(buf)
	if length < 3 || !(buf[0] == '\'' && buf[1] == '/' || buf[0] == '/') || buf[length-1] != ' ' {
		return nil, false, false
	}
	hasDir := false
	var dragFiles []string
	var i int
	var path string
	for idx := 0; idx < length; idx += i {
		path, i = nextLinuxPath(buf[idx:])
		if path == "" {
			return nil, false, false
		}
		if !detectFilePath(path, &dragFiles, &hasDir) {
			return nil, false, false
		}
	}
	return dragFiles, hasDir, false
}

func nextLinuxPath(buf []byte) (string, int) {
	length := len(buf)
	if length < 3 {
		return "", 0
	}
	if buf[0] == '\'' && buf[1] == '/' {
		idx := bytes.IndexByte(buf[1:], '\'')
		if idx < 0 {
			return "", 0
		}
		idx++
		if idx+1 >= length || buf[idx+1] != ' ' {
			return "", 0
		}
		return string(buf[1:idx]), idx + 2
	} else if buf[0] == '/' {
		idx := bytes.IndexByte(buf, ' ')
		if idx < 0 {
			return "", 0
		}
		return string(buf[:idx]), idx + 1
	}
	return "", 0
}

func detectDragFilesOnMacOS(buf []byte) ([]string, bool, bool) {
	length := len(buf)
	if isWarpTerminal() {
		if len(buf) > 1 && buf[0] == '\x10' {
			if buf[1] != '/' {
				return nil, false, false
			}
			buf = bytes.TrimSpace(buf[1:])
			length = len(buf)
		}
		if length < 3 || buf[0] != '/' {
			return nil, false, false
		}
		if length > 1 && (buf[length-1] != ' ' || buf[length-2] == '\\') {
			buf = append(buf, ' ')
			length = len(buf)
		}
	}
	if length < 3 || buf[0] != '/' || buf[length-1] != ' ' || buf[length-2] == '\\' {
		return nil, false, false
	}
	hasDir := false
	var dragFiles []string
	pathBuf := new(bytes.Buffer)
	for i := 0; i < length; i++ {
		if buf[i] == ' ' {
			path := pathBuf.String()
			if !detectFilePath(path, &dragFiles, &hasDir) {
				return nil, false, false
			}
			pathBuf.Reset()
		} else if buf[i] == '\\' {
			i++
			if i < length {
				pathBuf.WriteByte(buf[i])
			}
		} else {
			pathBuf.WriteByte(buf[i])
		}
	}
	if pathBuf.Len() != 0 {
		return nil, false, false
	}
	return dragFiles, hasDir, false
}

func detectDragFilesOnWindows(buf []byte) ([]string, bool, bool) {
	length := len(buf)
	if length < 4 {
		return nil, false, false
	}
	hasDir := false
	var dragFiles []string
	if buf[length-1] == '"' && buf[0] >= 'A' && buf[0] <= 'Z' && buf[1] == ':' && buf[2] == '\\' &&
		bytes.IndexByte(buf[:length-1], '"') < 0 {
		// Cmd & PowerShell may lost the first `"`, and supports one path only.
		if detectFilePath(string(buf[:length-1]), &dragFiles, &hasDir) {
			return dragFiles, hasDir, false
		}
	}
	isWinPath, isMsysPath, isCygPath := false, false, false
	if (buf[0] == '"' && buf[1] >= 'A' && buf[1] <= 'Z' && buf[2] == ':' && buf[3] == '\\') ||
		(buf[0] >= 'A' && buf[0] <= 'Z' && buf[1] == ':' && buf[2] == '\\') {
		isWinPath = true
	} else if (buf[0] == '\'' && buf[1] == '/' && buf[2] >= 'a' && buf[2] <= 'z' && buf[3] == '/') ||
		(buf[0] == '/' && buf[1] >= 'a' && buf[1] <= 'z' && buf[2] == '/') {
		isMsysPath = true
	} else if (length > 13 && string(buf[:11]) == "'/cygdrive/" && buf[11] >= 'a' && buf[11] <= 'z' && buf[12] == '/') ||
		(length > 12 && string(buf[:10]) == "/cygdrive/" && buf[10] >= 'a' && buf[10] <= 'z' && buf[11] == '/') {
		isCygPath = true
	} else {
		return nil, false, false
	}
	var i int
	var path string
	for idx := 0; idx < length; idx += i {
		if isWinPath {
			path, i = nextWinPath(buf[idx:])
		} else if isMsysPath {
			path, i = nextMsysPath(buf[idx:])
		} else if isCygPath {
			path, i = nextCygPath(buf[idx:])
		}
		if path == "" {
			return nil, false, false
		}
		if !detectFilePath(path, &dragFiles, &hasDir) {
			return nil, false, false
		}
	}
	return dragFiles, hasDir, false
}

func nextWinPath(buf []byte) (string, int) {
	length := len(buf)
	if length < 4 {
		return "", 0
	}
	if buf[0] == '"' && buf[1] >= 'A' && buf[1] <= 'Z' && buf[2] == ':' && buf[3] == '\\' {
		idx := bytes.IndexByte(buf[1:], '"')
		if idx < 0 {
			return "", 0
		}
		idx++
		if idx+1 < length && buf[idx+1] != ' ' {
			return "", 0
		}
		return string(buf[1:idx]), idx + 2
	} else if buf[0] >= 'A' && buf[0] <= 'Z' && buf[1] == ':' && buf[2] == '\\' {
		idx := bytes.IndexByte(buf, ' ')
		if idx < 0 {
			return string(buf), length
		}
		return string(buf[:idx]), idx + 1
	}
	return "", 0
}

func nextMsysPath(buf []byte) (string, int) {
	length := len(buf)
	if length < 4 {
		return "", 0
	}
	if buf[0] == '\'' && buf[1] == '/' && buf[2] >= 'a' && buf[2] <= 'z' && buf[3] == '/' {
		idx := bytes.IndexByte(buf[1:], '\'')
		if idx < 0 {
			return "", 0
		}
		idx++
		if idx+1 < length && buf[idx+1] != ' ' {
			return "", 0
		}
		return unixPathToWinPath(buf[1:idx]), idx + 2
	} else if buf[0] == '/' && buf[1] >= 'a' && buf[1] <= 'z' && buf[2] == '/' {
		idx := bytes.IndexByte(buf, ' ')
		if idx < 0 {
			return unixPathToWinPath(buf), length
		}
		return unixPathToWinPath(buf[:idx]), idx + 1
	}
	return "", 0
}

func nextCygPath(buf []byte) (string, int) {
	length := len(buf)
	if length < 13 {
		return "", 0
	}
	if string(buf[:11]) == "'/cygdrive/" && buf[11] >= 'a' && buf[11] <= 'z' && buf[12] == '/' {
		idx := bytes.IndexByte(buf[1:], '\'')
		if idx < 0 {
			return "", 0
		}
		idx++
		if idx+1 < length && buf[idx+1] != ' ' {
			return "", 0
		}
		return unixPathToWinPath(buf[10:idx]), idx + 2
	} else if string(buf[:10]) == "/cygdrive/" && buf[10] >= 'a' && buf[10] <= 'z' && buf[11] == '/' {
		idx := bytes.IndexByte(buf, ' ')
		if idx < 0 {
			return unixPathToWinPath(buf[9:]), length
		}
		return unixPathToWinPath(buf[9:idx]), idx + 1
	}
	return "", 0
}

func unixPathToWinPath(buf []byte) string {
	return fmt.Sprintf("%c:%s", buf[1], strings.ReplaceAll(string(buf[2:]), "/", "\\"))
}
