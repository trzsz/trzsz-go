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
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"slices"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/google/shlex"
)

type dragFilesInfo struct {
	files      []string
	hasDir     bool
	prefix     bool
	tmuxPaneID string
	tmuxBlocks int
}

func detectDragFiles(buf []byte) (dragInfo dragFilesInfo) {
	if len(buf) < 2 { // fast return for keystrokes
		return
	}

	realInput, rawInput := buf, buf
	defer func() {
		if dragInfo.files != nil || dragInfo.prefix {
			text, _ := clipboard.ReadAll()
			if text == string(realInput) {
				dragInfo.files, dragInfo.prefix = nil, false
			}
		}
		if dragInfo.files != nil && dragInfo.tmuxPaneID != "" {
			for _, b := range rawInput {
				if b == ';' || b == '\r' {
					dragInfo.tmuxBlocks++
				}
			}
		}
	}()

	if buf[0] == 's' && len(buf) > 15 && slices.ContainsFunc(bytes.Split(buf, []byte("\r")), hasTmuxSendPrefix) { // tmux -CC
		buf, dragInfo.tmuxPaneID = decodeTmuxInput(buf)
		if len(buf) < 2 {
			return
		}
		realInput = buf
	}

	if len(buf) > 5 && bytes.Contains(buf, []byte("\x1b[20")) {
		buf = bytes.ReplaceAll(buf, []byte("\x1b[200~"), []byte(""))
		buf = bytes.ReplaceAll(buf, []byte("\x1b[201~"), []byte(""))
		if len(buf) == 0 {
			return
		}
		realInput = buf
	}

	if buf[0] == '\x10' { // for old warp terminal
		buf = buf[1:]
		if len(buf) < 2 {
			return
		}
	}
	if buf[0] == '\x1b' && len(buf) > 4 && buf[1] == 'i' && buf[2] == '\x10' { // for new warp terminal
		buf = buf[3:]
	}

	if isRunningOnWindows() {
		detectDragFilesOnWindows(buf, &dragInfo)
		return
	}

	paths, err := shlex.Split(string(buf))
	if err != nil {
		return
	}
	for _, path := range paths {
		if len(path) < 2 || path[0] != '/' { // not absolute path
			dragInfo.files = nil
			return
		}
		if !detectFilePath(path, &dragInfo.files, &dragInfo.hasDir) {
			dragInfo.files = nil
			if !dragInfo.prefix && isDirPrefix(path) {
				dragInfo.prefix = true
			}
			return
		}
		dragInfo.prefix = true
	}
	return
}

func isDirPrefix(absPath string) bool {
	var isDir bool
	var files []string
	if !detectFilePath(path.Dir(absPath), &files, &isDir) {
		return false
	}
	return isDir
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

func hasTmuxSendPrefix(buf []byte) bool {
	if !isRunningOnMacOS() {
		return false
	}

	i := 0
	n := len(buf)
	match := func(s string) bool {
		if n-i < len(s) {
			return false
		}
		for j := 0; j < len(s); j++ {
			if buf[i+j] != s[j] {
				return false
			}
		}
		i += len(s)
		return true
	}
	skipNum := func() bool {
		start := i
		for i < n && buf[i] >= '0' && buf[i] <= '9' {
			i++
		}
		return i > start
	}

	// send -lt %N /
	if match("send -lt %") && skipNum() && match(" /") && !match("\r") {
		return true
	}

	i = 0
	// send -t %N 0x1b 0x5b;
	if !match("send -t %") || !skipNum() || !match(" 0x1b 0x5b; ") {
		return false
	}
	// send -lt %N 200;
	if !match("send -lt %") || !skipNum() || !match(" 200; ") {
		return false
	}
	// send -t %N 0x7e;
	if !match("send -t %") || !skipNum() || !match(" 0x7e; ") {
		return false
	}
	// send -lt %N /
	if !match("send -lt %") || !skipNum() || !match(" /") || match("\r") {
		return false
	}

	return true
}

func detectDragFilesOnWindows(buf []byte, dragInfo *dragFilesInfo) {
	length := len(buf)

	if length > 1 && ((buf[0] == '\'' && buf[1] == '/') || buf[0] == '/') {
		if cygpath := getCygpath(); cygpath != "" {
			detectDragFilesWithCygpath(cygpath, buf, dragInfo)
			return
		}
	}

	if length < 4 {
		return
	}

	if (buf[0] == '\'' && buf[1] == '/' && buf[2] >= 'a' && buf[2] <= 'z' && buf[3] == '/') ||
		(buf[0] == '/' && buf[1] >= 'a' && buf[1] <= 'z' && buf[2] == '/') {
		detectDragFilesOnMSYS(buf, dragInfo)
		return
	}

	if (length > 13 && string(buf[:11]) == "'/cygdrive/" && buf[11] >= 'a' && buf[11] <= 'z' && buf[12] == '/') ||
		(length > 12 && string(buf[:10]) == "/cygdrive/" && buf[10] >= 'a' && buf[10] <= 'z' && buf[11] == '/') {
		detectDragFilesOnCygwin(buf, dragInfo)
		return
	}

	if buf[length-1] == '"' && buf[0] >= 'A' && buf[0] <= 'Z' && buf[1] == ':' && buf[2] == '\\' &&
		bytes.IndexByte(buf[:length-1], '"') < 0 {
		// Cmd & PowerShell may lost the first `"`, and supports one path only.
		if detectFilePath(string(buf[:length-1]), &dragInfo.files, &dragInfo.hasDir) {
			dragInfo.prefix = true
			return
		}
	}

	if length > 4 && buf[0] >= 'A' && buf[0] <= 'Z' && buf[1] == ':' && buf[2] == '\\' && buf[3] == '\\' {
		paths, err := shlex.Split(string(buf))
		if err != nil {
			return
		}
		for _, path := range paths {
			if len(path) < 4 || path[0] < 'A' || path[0] > 'Z' || path[1] != ':' || path[2] != '\\' { // not absolute path
				dragInfo.files = nil
				return
			}
			if !detectFilePath(path, &dragInfo.files, &dragInfo.hasDir) {
				dragInfo.files = nil
				return
			}
			dragInfo.prefix = true
		}
		return
	}

	for idx := 0; idx < length; {
		path, inc, isPrefix := nextWinPath(buf[idx:])
		if path == "" {
			dragInfo.files = nil
			dragInfo.prefix = dragInfo.prefix || isPrefix
			return
		}
		if !detectFilePath(path, &dragInfo.files, &dragInfo.hasDir) {
			dragInfo.files = nil
			dragInfo.prefix = dragInfo.prefix || isPrefix
			return
		}
		dragInfo.prefix = true
		idx += inc
	}
	return
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

func detectDragFilesOnMSYS(buf []byte, dragInfo *dragFilesInfo) {
	paths, err := shlex.Split(string(buf))
	if err != nil || len(paths) < 1 {
		return
	}
	for _, path := range paths {
		if len(path) < 4 || path[0] != '/' || path[1] < 'a' || path[1] > 'z' || path[2] != '/' { // not absolute path
			dragInfo.files = nil
			return
		}
		if !detectFilePath(unixPathToWinPath(path), &dragInfo.files, &dragInfo.hasDir) {
			dragInfo.files = nil
			return
		}
		dragInfo.prefix = true
	}
	return
}

func detectDragFilesOnCygwin(buf []byte, dragInfo *dragFilesInfo) {
	paths, err := shlex.Split(string(buf))
	if err != nil || len(paths) < 1 {
		return
	}
	for _, path := range paths {
		if len(path) < 13 || path[:10] != "/cygdrive/" || path[10] < 'a' || path[10] > 'z' || path[11] != '/' { // not absolute path
			dragInfo.files = nil
			return
		}
		if !detectFilePath(unixPathToWinPath(path[9:]), &dragInfo.files, &dragInfo.hasDir) {
			dragInfo.files = nil
			return
		}
		dragInfo.prefix = true
	}
	return
}

func unixPathToWinPath(buf string) string {
	return fmt.Sprintf("%c:%s", buf[1], strings.ReplaceAll(string(buf[2:]), "/", "\\"))
}

func detectDragFilesWithCygpath(cygpath string, buf []byte, dragInfo *dragFilesInfo) {
	paths, err := shlex.Split(string(buf))
	if err != nil || len(paths) < 1 {
		return
	}
	for _, path := range paths {
		cmd := exec.Command(cygpath, "-w", path)
		out, err := cmd.Output()
		if err != nil {
			dragInfo.files = nil
			return
		}
		if !detectFilePath(strings.TrimSpace(string(out)), &dragInfo.files, &dragInfo.hasDir) {
			dragInfo.files = nil
			return
		}
		dragInfo.prefix = true
	}
	return
}
