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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/shlex"
	"github.com/trzsz/promptui"
)

// kNoGuiEnv is the environment variable to replace the GUI dialogs ( zenity )
// with text prompts on the client terminal. Set it to "1" to enable, e.g.
// `TRZSZ_NO_GUI=1 tssh xxx`. The value "0" disables it; any other non-empty
// value enables it.
const kNoGuiEnv = "TRZSZ_NO_GUI"

// noGuiEnabled returns whether the text prompts should be used instead of the
// GUI dialogs ( zenity ).
func noGuiEnabled() bool {
	v := strings.TrimSpace(os.Getenv(kNoGuiEnv))
	return v != "" && v != "0"
}

// The text prompts, prefixed with a stable greppable marker so that coding
// agents can detect them in the output.
const (
	kDownloadPrompt = "[trzsz] download path (absolute; an existing directory to save INTO; Ctrl+C to cancel):"
	kUploadPrompt   = "[trzsz] upload path(s) (absolute, space separated, existing file(s)/dir(s); Ctrl+C to cancel):"
	kEmptyPromptMsg = "please enter a path (or press Ctrl+C to cancel)"
)

var errNotAbsolutePath = func(path string) error {
	return fmt.Errorf("must be an absolute path: %s", path)
}

// textDialogWriter is the stdout of the text prompts. It is tmux control mode
// aware ( see promptWriter in ctrlc.go ), and suppresses the bell.
type textDialogWriter struct {
	promptWriter
}

func (w *textDialogWriter) Close() error {
	return nil
}

// newTextDialogWriter creates the writer for the text prompts. The optional
// tmuxPrefix is only known in trzsz transfers; zmodem has none.
func newTextDialogWriter(tmuxPrefix string, clientOut io.Writer) *textDialogWriter {
	return &textDialogWriter{promptWriter{tmuxPrefix, clientOut}}
}

// showTextPrompt shows a text prompt on the client terminal and returns the
// user's input. It re-prompts on empty or invalid input ( printing the
// validation error ) until a valid input is given, or the user presses
// Ctrl+C / Ctrl+D ( returns errUserCanceled ).
//
// The prompt's input pipe is installed once for the whole interaction, so the
// client input is always routed into the prompt ( see sendInput ) and never
// leaks to the server between re-prompts.
//
// Note: `validate` is called on every keystroke while typing ( promptui
// behavior ), so it must be cheap. Heavier checks should run in `confirm`
// which is called once per Enter.
func (filter *TrzszFilter) showTextPrompt(label string, validate func(string) error, confirm func(string) error) (string, error) {
	pipeReader, pipeWriter := io.Pipe()
	if !filter.textDialogPipe.CompareAndSwap(nil, pipeWriter) {
		// another text prompt is active ( shouldn't happen ); decline politely
		return "", errUserCanceled
	}
	defer func() {
		filter.textDialogPipe.Store(nil)
		_ = pipeWriter.Close()
		_ = pipeReader.Close()
	}()

	tmuxPrefix := ""
	if filter.trigger != nil {
		tmuxPrefix = filter.trigger.tmuxPrefix
	}
	writer := newTextDialogWriter(tmuxPrefix, filter.clientOut)

	prompt := promptui.Prompt{
		Label:  label,
		Stdin:  pipeReader,
		Stdout: writer,
		Validate: func(s string) error {
			if strings.TrimSpace(s) == "" {
				return fmt.Errorf("%s", kEmptyPromptMsg)
			}
			if validate != nil {
				return validate(s)
			}
			return nil
		},
	}

	for {
		result, err := prompt.Run()
		if err != nil {
			if err == promptui.ErrInterrupt || err == promptui.ErrEOF {
				return "", errUserCanceled
			}
			return "", err
		}
		result = strings.TrimSpace(result)
		if result == "" {
			continue // empty input re-prompts; never a silent default
		}
		if confirm != nil {
			if err := confirm(result); err != nil {
				_, _ = writer.Write([]byte(fmt.Sprintf("\r\n[trzsz] %s\r\n", err.Error())))
				continue
			}
		}
		return result, nil
	}
}

// chooseDownloadPathByPrompt asks the user for the local directory to save
// the downloaded files into, with a text prompt on the client terminal.
func (filter *TrzszFilter) chooseDownloadPathByPrompt() (string, error) {
	return filter.showTextPrompt(kDownloadPrompt, nil, func(path string) error {
		if !filepath.IsAbs(path) {
			return errNotAbsolutePath(path)
		}
		if err := checkPathWritable(path); err != nil {
			return fmt.Errorf("%v", err)
		}
		return nil
	})
}

// chooseUploadPathsByPrompt asks the user for the local file(s)/dir(s) to
// upload, with a text prompt on the client terminal.
func (filter *TrzszFilter) chooseUploadPathsByPrompt(directory bool) ([]string, error) {
	var paths []string
	_, err := filter.showTextPrompt(kUploadPrompt, nil, func(s string) error {
		// parse and validate once per Enter
		result, err := splitUploadPaths(s)
		if err != nil {
			return err
		}
		// check the file types ( regular file / directory ) as the GUI dialog does
		if _, err := checkPathsReadable(result, directory); err != nil {
			return fmt.Errorf("%v", err)
		}
		paths = result
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, errUserCanceled
	}
	return paths, nil
}

// splitUploadPaths splits the user's input into upload paths, validating
// that each path is absolute and exists.
func splitUploadPaths(s string) ([]string, error) {
	paths, err := shlex.Split(s)
	if err != nil {
		return nil, fmt.Errorf("invalid input: %v", err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("%s", kEmptyPromptMsg)
	}
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			return nil, errNotAbsolutePath(path)
		}
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("no such file or directory: %s", path)
			}
			return nil, err
		}
	}
	return paths, nil
}

// interceptTextDialog routes the client input into the active text dialog
// prompt's stdin pipe. Returns true if the input was consumed.
func (filter *TrzszFilter) interceptTextDialog(buf []byte) bool {
	pipe := filter.textDialogPipe.Load()
	if pipe == nil {
		return false
	}
	if _, err := pipe.Write(buf); err != nil {
		return false // pipe closed; let the input go its normal way
	}
	return true
}
