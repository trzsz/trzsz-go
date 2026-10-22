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
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNoGuiEnabled(t *testing.T) {
	cases := []struct {
		value  string
		expect bool
	}{
		{"", false},
		{"0", false},
		{"1", true},
		{"yes", true},
		{" 1 ", true},
		{" 0 ", false},
	}
	for _, c := range cases {
		t.Setenv(kNoGuiEnv, c.value)
		if got := noGuiEnabled(); got != c.expect {
			t.Errorf("noGuiEnabled(%q) = %v, expect %v", c.value, got, c.expect)
		}
	}
}

func TestSplitUploadPaths(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	spacedDir, err := os.MkdirTemp("", "trzsz with space")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(spacedDir) }()

	cases := []struct {
		input   string
		expect  []string
		wantErr string
	}{
		{"", nil, kEmptyPromptMsg},
		{"   ", nil, kEmptyPromptMsg},
		{file + " " + dir, []string{file, dir}, ""},
		{file + "  " + dir, []string{file, dir}, ""},
		{`"` + spacedDir + `" ` + file, []string{spacedDir, file}, ""},
		{`'` + spacedDir + `'`, []string{spacedDir}, ""},
		{"relative/path", nil, "must be an absolute path"},
		{"~/home/path", nil, "must be an absolute path"},
		{dir + " relative/path", nil, "must be an absolute path"},
		{file, []string{file}, ""},
		{dir, []string{dir}, ""},
		{"/abs/path /abs/path2", nil, "no such file or directory"},
	}
	for _, c := range cases {
		paths, err := splitUploadPaths(c.input)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("splitUploadPaths(%q) error = %v, expect containing %q", c.input, err, c.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("splitUploadPaths(%q) unexpected error: %v", c.input, err)
			continue
		}
		if len(paths) != len(c.expect) {
			t.Errorf("splitUploadPaths(%q) = %v, expect %v", c.input, paths, c.expect)
			continue
		}
		for i := range paths {
			if paths[i] != c.expect[i] {
				t.Errorf("splitUploadPaths(%q)[%d] = %q, expect %q", c.input, i, paths[i], c.expect[i])
			}
		}
	}
}

// textDialogFixture wires a TrzszFilter with a text dialog prompt, a pipe for
// the client input, and a buffer capturing the client output.
type textDialogFixture struct {
	filter      *TrzszFilter
	clientInput chan []byte
	output      *bytes.Buffer
}

func newTextDialogFixture() *textDialogFixture {
	f := &textDialogFixture{
		filter:      &TrzszFilter{},
		clientInput: make(chan []byte, 100),
		output:      &bytes.Buffer{},
	}
	// simulate wrapInput: send the client input to sendInput
	go func() {
		for buf := range f.clientInput {
			detectDragFile := &atomic.Bool{}
			f.filter.sendInput(append([]byte(nil), buf...), detectDragFile)
		}
	}()
	// sendInput writes to serverIn when no prompt is active; give it a pipe
	// so the fixture works before/after the prompt ( and doesn't crash ).
	_, serverIn := io.Pipe()
	f.filter.serverIn = serverIn
	f.filter.clientOut = &nopWriteCloser{f.output}
	return f
}

type nopWriteCloser struct {
	io.Writer
}

func (w *nopWriteCloser) Write(p []byte) (int, error) { return w.Writer.Write(p) }
func (w *nopWriteCloser) Close() error                { return nil }

// type simulates typing into the prompt: each string is written as one input
// chunk, then Enter.
func (f *textDialogFixture) typeInput(s string) {
	f.clientInput <- []byte(s)
}

func (f *textDialogFixture) pressEnter() {
	f.clientInput <- []byte("\r")
}

func (f *textDialogFixture) pressCtrlC() {
	f.clientInput <- []byte("\x03")
}

func TestShowTextPromptValidInput(t *testing.T) {
	f := newTextDialogFixture()
	dir := t.TempDir()

	resultCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := f.filter.showTextPrompt(kDownloadPrompt, nil, func(s string) error {
			if !filepath.IsAbs(s) {
				return errNotAbsolutePath(s)
			}
			return checkPathWritable(s)
		})
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	// wait for the prompt to become active
	waitForTextDialog(t, f.filter)

	// type the path and press Enter
	f.typeInput(dir)
	f.pressEnter()

	select {
	case result := <-resultCh:
		if result != dir {
			t.Errorf("showTextPrompt result = %q, expect %q", result, dir)
		}
	case err := <-errCh:
		t.Fatalf("showTextPrompt unexpected error: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("showTextPrompt timeout")
	}

	output := f.output.String()
	if !strings.Contains(output, "[trzsz]") {
		t.Errorf("output should contain the [trzsz] marker: %q", output)
	}
}

func TestShowTextPromptEmptyReprompts(t *testing.T) {
	f := newTextDialogFixture()
	dir := t.TempDir()

	resultCh := make(chan string, 1)
	go func() {
		result, err := f.filter.showTextPrompt(kDownloadPrompt, nil, func(s string) error {
			return checkPathWritable(s)
		})
		if err != nil {
			t.Errorf("showTextPrompt unexpected error: %v", err)
			return
		}
		resultCh <- result
	}()

	waitForTextDialog(t, f.filter)

	// empty input first: re-prompts, does not return
	f.pressEnter()

	select {
	case result := <-resultCh:
		t.Fatalf("showTextPrompt returned %q on empty input; should re-prompt", result)
	case <-time.After(300 * time.Millisecond):
		// expected: still prompting
	}

	// then valid input
	f.typeInput(dir)
	f.pressEnter()

	select {
	case result := <-resultCh:
		if result != dir {
			t.Errorf("showTextPrompt result = %q, expect %q", result, dir)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("showTextPrompt timeout after valid input")
	}

	output := f.output.String()
	if !strings.Contains(output, kEmptyPromptMsg) {
		t.Errorf("output should contain the empty-input hint: %q", output)
	}
}

func TestShowTextPromptInvalidReprompts(t *testing.T) {
	f := newTextDialogFixture()
	dir := t.TempDir()
	notDir := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(notDir, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	resultCh := make(chan string, 1)
	go func() {
		result, err := f.filter.showTextPrompt(kDownloadPrompt, nil, func(s string) error {
			if !filepath.IsAbs(s) {
				return errNotAbsolutePath(s)
			}
			return checkPathWritable(s)
		})
		if err != nil {
			t.Errorf("showTextPrompt unexpected error: %v", err)
			return
		}
		resultCh <- result
	}()

	waitForTextDialog(t, f.filter)

	// relative path: re-prompt with the reason
	f.typeInput("relative/path")
	f.pressEnter()
	// a file instead of a directory: re-prompt with the reason
	f.typeInput(notDir)
	f.pressEnter()
	// valid
	f.typeInput(dir)
	f.pressEnter()

	select {
	case result := <-resultCh:
		if result != dir {
			t.Errorf("showTextPrompt result = %q, expect %q", result, dir)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("showTextPrompt timeout")
	}

	output := f.output.String()
	if !strings.Contains(output, "must be an absolute path") {
		t.Errorf("output should contain the absolute-path error: %q", output)
	}
	if !strings.Contains(output, "Not a directory") {
		t.Errorf("output should contain the not-a-directory error: %q", output)
	}
}

func TestShowTextPromptCtrlC(t *testing.T) {
	f := newTextDialogFixture()

	errCh := make(chan error, 1)
	go func() {
		_, err := f.filter.showTextPrompt(kDownloadPrompt, nil, nil)
		errCh <- err
	}()

	waitForTextDialog(t, f.filter)
	f.pressCtrlC()

	select {
	case err := <-errCh:
		if err != errUserCanceled {
			t.Errorf("showTextPrompt Ctrl+C error = %v, expect errUserCanceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("showTextPrompt timeout after Ctrl+C")
	}
}

func TestInterceptTextDialog(t *testing.T) {
	f := newTextDialogFixture()

	// no prompt active: input goes the normal way ( returns false )
	if f.filter.interceptTextDialog([]byte("abc")) {
		t.Error("interceptTextDialog should return false when no prompt is active")
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := f.filter.showTextPrompt("test:", nil, nil)
		errCh <- err
	}()
	waitForTextDialog(t, f.filter)

	// prompt active: input is consumed ( returns true )
	if !f.filter.interceptTextDialog([]byte("a")) {
		t.Error("interceptTextDialog should return true when a prompt is active")
	}

	f.pressCtrlC()
	<-errCh

	// prompt closed: input goes the normal way again
	if f.filter.interceptTextDialog([]byte("abc")) {
		t.Error("interceptTextDialog should return false after the prompt closed")
	}
}

func TestChooseDownloadPathByPrompt(t *testing.T) {
	f := newTextDialogFixture()
	dir := t.TempDir()

	resultCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := f.filter.chooseDownloadPathByPrompt()
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	waitForTextDialog(t, f.filter)
	f.typeInput(dir)
	f.pressEnter()

	select {
	case result := <-resultCh:
		if result != dir {
			t.Errorf("chooseDownloadPathByPrompt = %q, expect %q", result, dir)
		}
	case err := <-errCh:
		t.Fatalf("chooseDownloadPathByPrompt unexpected error: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("chooseDownloadPathByPrompt timeout")
	}

	output := f.output.String()
	if !strings.Contains(output, "[trzsz] download path") {
		t.Errorf("output should contain the download prompt: %q", output)
	}
}

func TestChooseUploadPathsByPrompt(t *testing.T) {
	f := newTextDialogFixture()
	dir := t.TempDir()
	file1 := filepath.Join(dir, "file1.txt")
	file2 := filepath.Join(dir, "file2.txt")
	for _, file := range []string{file1, file2} {
		if err := os.WriteFile(file, []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	resultCh := make(chan []string, 1)
	errCh := make(chan error, 1)
	go func() {
		paths, err := f.filter.chooseUploadPathsByPrompt(false)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- paths
	}()

	waitForTextDialog(t, f.filter)

	// a directory is not acceptable for plain upload ( files only ): re-prompt
	f.typeInput(dir)
	f.pressEnter()
	// valid: two files, one with spaces to test shlex quoting
	f.typeInput(file1 + " " + file2)
	f.pressEnter()

	select {
	case paths := <-resultCh:
		if len(paths) != 2 || paths[0] != file1 || paths[1] != file2 {
			t.Errorf("chooseUploadPathsByPrompt = %v, expect [%s %s]", paths, file1, file2)
		}
	case err := <-errCh:
		t.Fatalf("chooseUploadPathsByPrompt unexpected error: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("chooseUploadPathsByPrompt timeout")
	}

	output := f.output.String()
	if !strings.Contains(output, "[trzsz] upload path(s)") {
		t.Errorf("output should contain the upload prompt: %q", output)
	}
	if !strings.Contains(output, "Is a directory") {
		t.Errorf("output should contain the is-a-directory error: %q", output)
	}
}

func TestChooseUploadPathsByPromptDirectory(t *testing.T) {
	f := newTextDialogFixture()
	dir := t.TempDir()

	resultCh := make(chan []string, 1)
	errCh := make(chan error, 1)
	go func() {
		paths, err := f.filter.chooseUploadPathsByPrompt(true)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- paths
	}()

	waitForTextDialog(t, f.filter)
	f.typeInput(dir)
	f.pressEnter()

	select {
	case paths := <-resultCh:
		if len(paths) != 1 || paths[0] != dir {
			t.Errorf("chooseUploadPathsByPrompt(directory) = %v, expect [%s]", paths, dir)
		}
	case err := <-errCh:
		t.Fatalf("chooseUploadPathsByPrompt unexpected error: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("chooseUploadPathsByPrompt timeout")
	}
}

// waitForTextDialog waits until the text dialog prompt becomes active.
func waitForTextDialog(t *testing.T, filter *TrzszFilter) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if filter.textDialogPipe.Load() != nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("text dialog prompt did not become active")
}
