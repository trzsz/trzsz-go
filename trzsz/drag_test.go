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
	dragFiles, hasDir, ignore, isWinPathPrefix := detectDragFiles([]byte(buf))
	assert.Equal(files, dragFiles)
	assert.False(hasDir)
	assert.False(ignore)
	assert.False(isWinPathPrefix)
}

func assertHasDragDirs(t *testing.T, buf string, files []string) {
	t.Helper()
	assert := assert.New(t)
	dragFiles, hasDir, ignore, isWinPathPrefix := detectDragFiles([]byte(buf))
	assert.Equal(files, dragFiles)
	assert.True(hasDir)
	assert.False(ignore)
	assert.False(isWinPathPrefix)
}

func assertNoDragFiles(t *testing.T, buf string) {
	t.Helper()
	assert := assert.New(t)
	dragFiles, hasDir, ignore, isWinPathPrefix := detectDragFiles([]byte(buf))
	assert.Nil(dragFiles)
	assert.False(hasDir)
	assert.False(ignore)
	assert.False(isWinPathPrefix)
}

func assertIgnoreDragFiles(t *testing.T, buf string) {
	t.Helper()
	assert := assert.New(t)
	dragFiles, hasDir, ignore, isWinPathPrefix := detectDragFiles([]byte(buf))
	assert.Nil(dragFiles)
	assert.False(hasDir)
	assert.True(ignore)
	assert.False(isWinPathPrefix)
}

func assertDragFilesPrefix(t *testing.T, buf string) {
	t.Helper()
	assert := assert.New(t)
	dragFiles, hasDir, ignore, isWinPathPrefix := detectDragFiles([]byte(buf))
	assert.Nil(dragFiles)
	assert.False(hasDir)
	assert.False(ignore)
	assert.True(isWinPathPrefix)
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
	assertNoDragFiles(t, "x")
	assertNoDragFiles(t, "xyz")
	assertNoDragFiles(t, "/tmp/xyz ")
	assertNoDragFiles(t, "/tmp/abc x ")
	assertNoDragFiles(t, "/tmp/abc xyz ")
	assertNoDragFiles(t, "/tmp/abc '/x ")
	assertNoDragFiles(t, "/tmp/abc '/x'a ")

	assertIgnoreDragFiles(t, "\x1b[200~\x1b[201~")
	assertIgnoreDragFiles(t, "\x1b[200~\x1b[201~\x1b[200~\x1b[201~")
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
	assertNoDragFiles(t, "x")
	assertNoDragFiles(t, "xyz")
	assertNoDragFiles(t, "/tmp/xyz ")
	assertNoDragFiles(t, "/tmp/abc\\")
	assertNoDragFiles(t, "/tmp/abc x ")
	assertNoDragFiles(t, "/tmp/abc xyz ")
	assertNoDragFiles(t, "/tmp/abc '/x ")
	assertNoDragFiles(t, "/tmp/abc '/x'a ")

	assertIgnoreDragFiles(t, "\x1b[200~\x1b[201~")
	assertIgnoreDragFiles(t, "\x1b[200~\x1b[201~\x1b[200~\x1b[201~")

	oriIsWarpTerminal := isWarpTerminal
	isWarpTerminal = func() bool { return true }
	defer func() { isWarpTerminal = oriIsWarpTerminal }()
	assertHasDragFiles(t, "\x10/tmp/abc ", []string{"/tmp/abc"})
	assertHasDragFiles(t, "\x10/tmp/abc", []string{"/tmp/abc"})
	assertHasDragFiles(t, "\x10/tmp/kkk\\ ", []string{"/tmp/kkk "})
	assertNoDragFiles(t, "\x10/")
	assertNoDragFiles(t, "\x10x")
	assertHasDragFiles(t, "\x1bi\x10/tmp/abc ", []string{"/tmp/abc"})
	assertHasDragFiles(t, "\x1bi\x10/tmp/abc", []string{"/tmp/abc"})
	assertHasDragFiles(t, "\x1bi\x10/tmp/kkk\\ ", []string{"/tmp/kkk "})
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
	assertNoDragFiles(t, "x")
	assertNoDragFiles(t, "xyzz")
	assertNoDragFiles(t, "/c/xyzz ")
	assertNoDragFiles(t, "/c/abc x ")
	assertNoDragFiles(t, "/c/abc xyzz ")
	assertNoDragFiles(t, "/c/abc '/x/x ")
	assertNoDragFiles(t, "/c/abc '/x/x'a ")

	assertIgnoreDragFiles(t, "\x1b[200~\x1b[201~")
	assertIgnoreDragFiles(t, "\x1b[200~\x1b[201~\x1b[200~\x1b[201~")
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
	assertNoDragFiles(t, "x")
	assertNoDragFiles(t, "xyz1234567890")
	assertNoDragFiles(t, "/cygdrive/c/xyz1234567890 ")
	assertNoDragFiles(t, "/cygdrive/c/abc x ")
	assertNoDragFiles(t, "/cygdrive/c/abc xyz1234567890 ")
	assertNoDragFiles(t, "/cygdrive/c/abc '/cygdrive/x/x ")
	assertNoDragFiles(t, "/cygdrive/c/abc '/cygdrive/x/x'a ")

	assertIgnoreDragFiles(t, "\x1b[200~\x1b[201~")
	assertIgnoreDragFiles(t, "\x1b[200~\x1b[201~\x1b[200~\x1b[201~")
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
	assertNoDragFiles(t, "x")
	assertNoDragFiles(t, "xyz")
	assertNoDragFiles(t, "C:\\xyz ")
	assertNoDragFiles(t, "C:\\abc x ")
	assertNoDragFiles(t, "C:\\abc xyz ")
	assertNoDragFiles(t, "C:\\abc \"X:\\x\"a ")

	assertNoDragFiles(t, "C:\\\\xyz ")
	assertNoDragFiles(t, "C:\\\\abc x ")
	assertNoDragFiles(t, "C:\\\\abc xyz ")
	assertNoDragFiles(t, "C:\\\\abc \"X:\\\\x ")

	assertDragFilesPrefix(t, "C:\\a")
	assertDragFilesPrefix(t, "C:\\abc D:\\1")
	assertDragFilesPrefix(t, "C:\\abc \"D:\\12")
	assertDragFilesPrefix(t, "C:\\abc \"D:\\12 ")
	assertDragFilesPrefix(t, "C:\\abc \"D:\\12 3")

	assertIgnoreDragFiles(t, "\x1b[200~\x1b[201~")
	assertIgnoreDragFiles(t, "\x1b[200~\x1b[201~\x1b[200~\x1b[201~")
}
