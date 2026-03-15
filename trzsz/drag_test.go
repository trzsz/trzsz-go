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
	"testing"

	"github.com/stretchr/testify/assert"
)

var mockFileMap map[string]bool

func addMockFile(path string, dir bool) {
	mockFileMap[path] = dir
}

var mockDetectFilePath = func(path string, dragFiles *[]string, hasDir *bool) bool {
	if dir, ok := mockFileMap[path]; ok {
		*hasDir = dir
		*dragFiles = append(*dragFiles, path)
		return true
	}
	return false
}

func setupTestRuntime(linux, macos, windows bool) func() {
	mockFileMap = make(map[string]bool)
	oriDetectFilePath := detectFilePath
	detectFilePath = mockDetectFilePath
	oriLinuxRuntime, oriMacosRuntime, oriWindowsRuntime := linuxRuntime, macosRuntime, windowsRuntime
	linuxRuntime, macosRuntime, windowsRuntime = linux, macos, windows
	return func() {
		detectFilePath, mockFileMap = oriDetectFilePath, nil
		linuxRuntime, macosRuntime, windowsRuntime = oriLinuxRuntime, oriMacosRuntime, oriWindowsRuntime
	}
}

func assertHasDragFiles(t *testing.T, buf string, files []string) {
	t.Helper()
	assert := assert.New(t)
	assert.Equal(dragFilesInfo{
		files:  files,
		prefix: true,
	}, detectDragFiles([]byte(buf)))
}

func assertHasDragDirs(t *testing.T, buf string, files []string) {
	t.Helper()
	assert := assert.New(t)
	assert.Equal(dragFilesInfo{
		files:  files,
		hasDir: true,
		prefix: true,
	}, detectDragFiles([]byte(buf)))
}

func assertNoDragFiles(t *testing.T, buf string, prefix bool) {
	t.Helper()
	assert := assert.New(t)
	assert.Equal(dragFilesInfo{
		prefix: prefix,
	}, detectDragFiles([]byte(buf)))
}

func assertDragFilesPrefix(t *testing.T, buf string) {
	t.Helper()
	assert := assert.New(t)
	assert.Equal(dragFilesInfo{
		prefix: true,
	}, detectDragFiles([]byte(buf)))
}

func assertTmuxDragFiles(t *testing.T, buf string, files []string, id string, count int) {
	t.Helper()
	assert := assert.New(t)
	assert.Equal(dragFilesInfo{
		files:      files,
		prefix:     true,
		tmuxPaneID: id,
		tmuxBlocks: count,
	}, detectDragFiles([]byte(buf)))
}

func TestDetectDragFilesOnLinux(t *testing.T) {
	resetRuntime := setupTestRuntime(true, false, false)
	defer resetRuntime()
	assert.New(t).True(isRunningOnLinux())

	addMockFile("/tmp/abc", false)
	addMockFile("/tmp/12 3", false)
	assertHasDragFiles(t, "/tmp/abc ", []string{"/tmp/abc"})
	assertHasDragFiles(t, "/tmp/abc '/tmp/12 3' ", []string{"/tmp/abc", "/tmp/12 3"})

	addMockFile("/x", true)
	assertHasDragDirs(t, "/tmp/abc '/tmp/12 3' /x ", []string{"/tmp/abc", "/tmp/12 3", "/x"})

	assertHasDragFiles(t, "\x1b[200~/tmp/abc \x1b[201~", []string{"/tmp/abc"})

	addMockFile("x", false)
	addMockFile("xyz", true)
	assertNoDragFiles(t, "x", false)
	assertNoDragFiles(t, "xyz", false)
	assertNoDragFiles(t, "/tmp/xyz ", false)
	assertNoDragFiles(t, "/tmp/abc x ", true)
	assertNoDragFiles(t, "/tmp/abc xyz ", true)
	assertNoDragFiles(t, "/tmp/abc '/x ", false)
	assertNoDragFiles(t, "/tmp/abc '/x'a ", true)
}

func TestDetectDragFilesOnMacOS(t *testing.T) {
	resetRuntime := setupTestRuntime(false, true, false)
	defer resetRuntime()

	addMockFile("/tmp/abc", false)
	addMockFile("/tmp/12 3", false)
	addMockFile("/tmp/kkk ", false)
	assertHasDragFiles(t, "/tmp/abc ", []string{"/tmp/abc"})
	assertHasDragFiles(t, "/tmp/abc /tmp/12\\ 3 ", []string{"/tmp/abc", "/tmp/12 3"})
	assertHasDragFiles(t, "/tmp/abc /tmp/kkk\\  ", []string{"/tmp/abc", "/tmp/kkk "})

	addMockFile("/x", true)
	assertHasDragDirs(t, "/tmp/abc /tmp/12\\ 3 /x ", []string{"/tmp/abc", "/tmp/12 3", "/x"})

	assertHasDragFiles(t, "\x1b[200~/tmp/abc \x1b[201~", []string{"/tmp/abc"})

	addMockFile("x", false)
	addMockFile("xyz", true)
	assertNoDragFiles(t, "x", false)
	assertNoDragFiles(t, "xyz", false)
	assertNoDragFiles(t, "/tmp/xyz ", false)
	assertNoDragFiles(t, "/tmp/abc\\", false)
	assertNoDragFiles(t, "/tmp/abc x ", true)
	assertNoDragFiles(t, "/tmp/abc xyz ", true)
	assertNoDragFiles(t, "/tmp/abc '/x ", false)
	assertNoDragFiles(t, "/tmp/abc '/x'a ", true)

	oriIsWarpTerminal := isWarpTerminal
	isWarpTerminal = func() bool { return true }
	defer func() { isWarpTerminal = oriIsWarpTerminal }()
	assertHasDragFiles(t, "\x10/tmp/abc ", []string{"/tmp/abc"})
	assertHasDragFiles(t, "\x10/tmp/abc", []string{"/tmp/abc"})
	assertHasDragFiles(t, "\x10/tmp/kkk\\ ", []string{"/tmp/kkk "})
	assertNoDragFiles(t, "\x10/", false)
	assertNoDragFiles(t, "\x10x", false)
	assertHasDragFiles(t, "\x1bi\x10/tmp/abc ", []string{"/tmp/abc"})
	assertHasDragFiles(t, "\x1bi\x10/tmp/abc", []string{"/tmp/abc"})
	assertHasDragFiles(t, "\x1bi\x10/tmp/kkk\\ ", []string{"/tmp/kkk "})
}

func TestDetectDragFilesOnTmuxCC(t *testing.T) {
	resetRuntime := setupTestRuntime(false, true, false)
	defer resetRuntime()

	addMockFile("/tmp/abc", false)
	addMockFile("/tmp/12 3", false)
	addMockFile("/tmp/kkk ", false)

	assertTmuxDragFiles(t, "send -lt %26 /tmp/abc; send -t %26 0x20\r",
		[]string{"/tmp/abc"}, "26", 2)

	assertTmuxDragFiles(t, "select-window -t @92\rsend -lt %251 /tmp/abc; send -t %251 0x20; send -lt %251 /tmp/12; "+
		"send -t %251 0x5c 0x20; send -lt %251 3; send -t %251 0x20\r",
		[]string{"/tmp/abc", "/tmp/12 3"}, "251", 7)

	assertTmuxDragFiles(t, "send -t %237 0x1b 0x5b; send -lt %237 200; send -t %237 0x7e; "+
		"send -lt %237 /tmp/abc; send -t %237 0x20; send -lt %237 /tmp/kkk; send -t %237 "+
		"0x5c 0x20 0x20 0x1b 0x5b; send -lt %237 201; send -t %237 0x7e\r",
		[]string{"/tmp/abc", "/tmp/kkk "}, "237", 9)
}

func TestDetectDragFilesOnMSYS2(t *testing.T) {
	resetRuntime := setupTestRuntime(false, false, true)
	defer resetRuntime()

	addMockFile("c:\\abc", false)
	addMockFile("d:\\12 3", false)
	assertHasDragFiles(t, "/c/abc ", []string{"c:\\abc"})
	assertHasDragFiles(t, "'/c/abc'", []string{"c:\\abc"})
	assertHasDragFiles(t, "/c/abc '/d/12 3' ", []string{"c:\\abc", "d:\\12 3"})

	addMockFile("x:\\x", true)
	assertHasDragDirs(t, "/c/abc '/d/12 3' /x/x", []string{"c:\\abc", "d:\\12 3", "x:\\x"})

	assertHasDragFiles(t, "\x1b[200~/c/abc \x1b[201~", []string{"c:\\abc"})

	addMockFile("x", false)
	addMockFile("xyzz", true)
	assertNoDragFiles(t, "x", false)
	assertNoDragFiles(t, "xyzz", false)
	assertNoDragFiles(t, "/c/xyzz ", false)
	assertNoDragFiles(t, "/c/abc x ", true)
	assertNoDragFiles(t, "/c/abc xyzz ", true)
	assertNoDragFiles(t, "/c/abc '/x/x ", false)
	assertNoDragFiles(t, "/c/abc '/x/x'a ", true)
}

func TestDetectDragFilesOnCygwin(t *testing.T) {
	resetRuntime := setupTestRuntime(false, false, true)
	defer resetRuntime()

	addMockFile("c:\\abc", false)
	addMockFile("d:\\12 3", false)
	assertHasDragFiles(t, "/cygdrive/c/abc ", []string{"c:\\abc"})
	assertHasDragFiles(t, "'/cygdrive/c/abc'", []string{"c:\\abc"})
	assertHasDragFiles(t, "/cygdrive/c/abc '/cygdrive/d/12 3' ", []string{"c:\\abc", "d:\\12 3"})

	addMockFile("x:\\x", true)
	assertHasDragDirs(t, "/cygdrive/c/abc '/cygdrive/d/12 3' /cygdrive/x/x", []string{"c:\\abc", "d:\\12 3", "x:\\x"})

	assertHasDragFiles(t, "\x1b[200~/cygdrive/c/abc \x1b[201~", []string{"c:\\abc"})

	addMockFile("x", false)
	addMockFile("xyz1234567890", true)
	assertNoDragFiles(t, "x", false)
	assertNoDragFiles(t, "xyz1234567890", false)
	assertNoDragFiles(t, "/cygdrive/c/xyz1234567890 ", false)
	assertNoDragFiles(t, "/cygdrive/c/abc x ", true)
	assertNoDragFiles(t, "/cygdrive/c/abc xyz1234567890 ", true)
	assertNoDragFiles(t, "/cygdrive/c/abc '/cygdrive/x/x ", false)
	assertNoDragFiles(t, "/cygdrive/c/abc '/cygdrive/x/x'a ", true)
}

func TestDetectDragFilesOnWindows(t *testing.T) {
	resetRuntime := setupTestRuntime(false, false, true)
	defer resetRuntime()

	addMockFile("C:\\abc", false)
	addMockFile("D:\\12 3", false)
	assertHasDragFiles(t, "C:\\abc", []string{"C:\\abc"})
	assertHasDragFiles(t, "C:\\abc ", []string{"C:\\abc"})
	assertHasDragFiles(t, "C:\\abc\"", []string{"C:\\abc"})
	assertHasDragFiles(t, "\"C:\\abc\"", []string{"C:\\abc"})
	assertHasDragFiles(t, "C:\\abc \"D:\\12 3\" ", []string{"C:\\abc", "D:\\12 3"})

	addMockFile("X:\\x", true)
	assertHasDragDirs(t, "C:\\abc \"D:\\12 3\" X:\\x", []string{"C:\\abc", "D:\\12 3", "X:\\x"})

	assertHasDragFiles(t, "\x1b[200~C:\\abc \x1b[201~", []string{"C:\\abc"})

	assertHasDragFiles(t, "C:\\\\abc", []string{"C:\\abc"})
	assertHasDragFiles(t, "C:\\\\abc D:\\\\12\\ 3 ", []string{"C:\\abc", "D:\\12 3"})

	addMockFile("x", false)
	addMockFile("xyz", true)
	assertNoDragFiles(t, "x", false)
	assertNoDragFiles(t, "xyz", false)
	assertNoDragFiles(t, "C:\\xyz ", false)
	assertNoDragFiles(t, "C:\\abc x ", true)
	assertNoDragFiles(t, "C:\\abc xyz ", true)
	assertNoDragFiles(t, "C:\\abc \"X:\\x\"a ", true)

	assertNoDragFiles(t, "C:\\\\xyz ", false)
	assertNoDragFiles(t, "C:\\\\abc x ", true)
	assertNoDragFiles(t, "C:\\\\abc xyz ", true)
	assertNoDragFiles(t, "C:\\\\abc \"X:\\\\x ", false)

	assertDragFilesPrefix(t, "C:\\a")
	assertDragFilesPrefix(t, "C:\\abc D:\\1")
	assertDragFilesPrefix(t, "C:\\abc \"D:\\12")
	assertDragFilesPrefix(t, "C:\\abc \"D:\\12 ")
	assertDragFilesPrefix(t, "C:\\abc \"D:\\12 3")
}
