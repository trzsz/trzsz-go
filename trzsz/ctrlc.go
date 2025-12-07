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
	"io"
	"time"

	"github.com/chzyer/readline"
	"github.com/trzsz/promptui"
)

type promptWriter struct {
	prefix string
	writer io.Writer
}

func (w *promptWriter) Write(p []byte) (int, error) {
	if len(p) == 1 && p[0] == readline.CharBell { // no bell ringing
		return 1, nil
	}
	if w.prefix == "" {
		return w.writer.Write(p)
	}
	if err := writeAll(w.writer, encodeTmuxOutput(w.prefix, p)); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *promptWriter) Close() error {
	return nil
}

func (t *trzszTransfer) handleInputCtrlC() {
	// `ctrl + c` to stop transferring files
	if t.trzszFilter.trigger.version.compare(&trzszVersion{1, 1, 3}) > 0 {
		t.confirmStopTransfer()
	} else {
		t.stopTransferringFiles(false)
	}
}

func (t *trzszTransfer) transformPromptInput(promptPipe *io.PipeWriter, buf []byte) {
	const keyPrev = '\x10'
	const keyNext = '\x0E'
	const keyEnter = '\r'
	moveNext := func() { _, _ = promptPipe.Write([]byte{keyNext}) }
	movePrev := func() { _, _ = promptPipe.Write([]byte{keyPrev}) }
	stop := func() { _, _ = promptPipe.Write([]byte{keyPrev, keyPrev, keyEnter}) }
	quit := func() { _, _ = promptPipe.Write([]byte{keyNext, keyNext, keyEnter}) }
	confirm := func() { _, _ = promptPipe.Write([]byte{keyEnter}) }

	if len(buf) == 3 && buf[0] == '\x1b' && buf[1] == '[' {
		switch buf[2] {
		case '\x42': // ↓ to Next
			moveNext()
		case '\x41', '\x5A': // ↑ Shift-TAB to Prev
			movePrev()
		}
	}

	if len(buf) == 1 {
		switch buf[0] {
		case '\x03': // Ctrl-C to Stop
			stop()
		case 'q', 'Q', '\x11': // q Ctrl-C Ctrl-Q to Quit
			quit()
		case '\t', '\x0E', 'j', 'J', '\x0A': // Tab ↓ j Ctrl-J to Next
			moveNext()
		case '\x10', 'k', 'K', '\x0B': // ↑ k Ctrl-K to Prev
			movePrev()
		case '\r': // Enter
			confirm()
		}
	}
}

func (t *trzszTransfer) confirmStopTransfer() {
	pipeReader, pipeWriter := io.Pipe()
	if !t.promptPipeWriter.CompareAndSwap(nil, pipeWriter) {
		_ = pipeReader.Close()
		_ = pipeWriter.Close()
		return
	}

	t.pauseTransferringFiles()

	go func() {
		defer func() {
			t.promptPipeWriter.Store(nil)
			_ = pipeWriter.Close()
			_ = pipeReader.Close()
		}()

		writer := &promptWriter{t.trzszFilter.trigger.tmuxPrefix, t.trzszFilter.clientOut}
		if progress := t.trzszFilter.progress.Load(); progress != nil {
			progress.setPause(true)
			defer func() {
				progress.setTerminalColumns(t.trzszFilter.options.TerminalColumns)
				progress.setPause(false)
			}()
			time.Sleep(50 * time.Millisecond)   // wait for the progress bar output
			_, _ = writer.Write([]byte("\r\n")) // keep the progress bar displayed
		}

		prompt := promptui.Select{
			Label: "Are you sure you want to stop transferring files",
			Items: []string{
				"Stop and keep transferred files",
				"Stop and delete transferred files",
				"Continue to transfer remaining files",
			},
			Stdin:  pipeReader,
			Stdout: writer,
			Templates: &promptui.SelectTemplates{
				Help: `{{ "Use ↓ ↑ j k <tab> to navigate" | faint }}`,
			},
		}

		idx, _, err := prompt.Run()

		if err != nil || idx == 2 {
			t.resumeTransferringFiles()
		} else if idx == 0 {
			t.stopTransferringFiles(false)
		} else if idx == 1 {
			t.stopTransferringFiles(true)
		}
	}()
}
