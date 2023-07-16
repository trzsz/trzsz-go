/*
MIT License

Copyright (c) 2023 [Trzsz](https://github.com/trzsz)

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
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/manifoldco/promptui"
	"github.com/ncruces/zenity"
)

// TrzszOptions specify the options to create a TrzszFilter.
type TrzszOptions struct {
	// TerminalColumns is the columns of the terminal.
	TerminalColumns int32
	// DetectDragFile is an optional feature.
	// If DetectDragFile is true, will detect the user input to determine whether user is dragging to upload.
	DetectDragFile bool
	// DetectTraceLog is for debugging.
	// If DetectTraceLog is true, will detect the server output to determine whether to enable trace logging.
	DetectTraceLog bool
}

// TrzszFilter is a filter that supports trzsz ( trz / tsz ).
type TrzszFilter struct {
	clientIn        io.Reader
	clientOut       io.WriteCloser
	serverIn        io.WriteCloser
	serverOut       io.Reader
	options         TrzszOptions
	transfer        atomic.Pointer[trzszTransfer]
	progress        atomic.Pointer[textProgressBar]
	promptPipe      atomic.Pointer[io.PipeWriter]
	serverVersion   *trzszVersion
	remoteIsWindows bool
	dragging        atomic.Bool
	dragHasDir      atomic.Bool
	dragMutex       sync.Mutex
	dragFiles       []string
	interrupting    atomic.Bool
	skipTrzCommand  atomic.Bool
	logger          *traceLogger
}

// NewTrzszFilter create a TrzszFilter to support trzsz ( trz / tsz ).
//
// ┌────────┐   ClientIn   ┌─────────────┐   ServerIn   ┌────────┐
// │        ├─────────────►│             ├─────────────►│        │
// │ Client │              │ TrzszFilter │              │ Server │
// │        │◄─────────────┤             │◄─────────────┤        │
// └────────┘   ClientOut  └─────────────┘   ServerOut  └────────┘
//
// Specify the columns of the terminal in options.TerminalColumns.
func NewTrzszFilter(clientIn io.Reader, clientOut io.WriteCloser,
	serverIn io.WriteCloser, serverOut io.Reader, options TrzszOptions) *TrzszFilter {
	filter := &TrzszFilter{
		clientIn:  clientIn,
		clientOut: clientOut,
		serverIn:  serverIn,
		serverOut: serverOut,
		options:   options,
	}
	if options.DetectTraceLog {
		filter.logger = newTraceLogger()
	}
	go filter.wrapInput()
	go filter.wrapOutput()
	return filter
}

// SetTerminalColumns set the latest columns of the terminal.
func (filter *TrzszFilter) SetTerminalColumns(columns int32) {
	filter.options.TerminalColumns = columns
	if progress := filter.progress.Load(); progress != nil {
		progress.setTerminalColumns(columns)
	}
}

// IsTransferringFiles returns whether trzsz is transferring files.
func (filter *TrzszFilter) IsTransferringFiles() bool {
	return filter.transfer.Load() != nil
}

// StopTransferringFiles tell trzsz to stop if it is transferring files.
func (filter *TrzszFilter) StopTransferringFiles(stopAndDelete bool) {
	if transfer := filter.transfer.Load(); transfer != nil {
		transfer.stopTransferringFiles(stopAndDelete)
	}
}

// UploadFiles try to upload the files and directories asynchronously.
//
// Returns nil means added to the upload queue, not mean that the upload is successful.
// Returns error if an error occurs before adding to the upload queue.
func (filter *TrzszFilter) UploadFiles(filePaths []string) error {
	hasDir := false
	for _, path := range filePaths {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if _, err := checkPathsReadable([]string{path}, info.IsDir()); err != nil {
			return err
		}
		if info.IsDir() {
			hasDir = true
		}
	}
	if filter.IsTransferringFiles() {
		return simpleTrzszError("Is transferring files now")
	}
	if filter.dragging.Load() {
		return simpleTrzszError("Is dragging files to upload")
	}
	filter.addDragFiles(filePaths, hasDir, false)
	return nil
}

func (filter *TrzszFilter) getTrzszConfig(name string) *string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	file, err := os.Open(filepath.Join(home, ".trzsz.conf"))
	if err != nil {
		return nil
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		idx := strings.Index(line, "=")
		if idx < 0 {
			continue
		}
		if strings.TrimSpace(line[0:idx]) == name {
			value := strings.TrimSpace(line[idx+1:])
			if len(value) == 0 {
				return nil
			}
			return &value
		}
	}
	return nil
}

var parentWindowID = getParentWindowID()

func (filter *TrzszFilter) chooseDownloadPath() (string, error) {
	savePath := filter.getTrzszConfig("DefaultDownloadPath")
	if savePath != nil {
		time.Sleep(50 * time.Millisecond) // wait for all output to show
		return *savePath, nil
	}
	options := []zenity.Option{
		zenity.Title("Choose a folder to save file(s)"),
		zenity.Directory(),
		zenity.ShowHidden(),
	}
	if !isRunningOnLinux() {
		options = append(options, zenity.Attach(parentWindowID))
	}
	path, err := zenity.SelectFile(options...)
	if err != nil {
		return "", err
	}
	if len(path) == 0 {
		return "", zenity.ErrCanceled
	}
	return path, nil
}

func (filter *TrzszFilter) chooseUploadPaths(directory bool) ([]string, error) {
	if filter.dragging.Load() {
		files := filter.resetDragFiles()
		return files, nil
	}
	options := []zenity.Option{
		zenity.Title("Choose some files to send"),
		zenity.ShowHidden(),
	}
	defaultPath := filter.getTrzszConfig("DefaultUploadPath")
	if defaultPath != nil {
		options = append(options, zenity.Filename(*defaultPath))
	}
	if directory {
		options = append(options, zenity.Directory())
	}
	if !isRunningOnLinux() {
		options = append(options, zenity.Attach(parentWindowID))
	}
	files, err := zenity.SelectFileMultiple(options...)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, zenity.ErrCanceled
	}
	return files, nil
}

func (filter *TrzszFilter) downloadFiles(transfer *trzszTransfer) error {
	path, err := filter.chooseDownloadPath()
	if err == zenity.ErrCanceled {
		return transfer.sendAction(false, filter.serverVersion, filter.remoteIsWindows)
	}
	if err != nil {
		return err
	}
	if err := checkPathWritable(path); err != nil {
		return err
	}

	if !filter.transfer.CompareAndSwap(nil, transfer) {
		return simpleTrzszError("Swap transfer failed")
	}

	if err := transfer.sendAction(true, filter.serverVersion, filter.remoteIsWindows); err != nil {
		return err
	}
	config, err := transfer.recvConfig()
	if err != nil {
		return err
	}

	filter.progress.Store(nil)
	if !config.Quiet {
		filter.progress.Store(newTextProgressBar(filter.clientOut, filter.options.TerminalColumns, config.TmuxPaneColumns))
		defer filter.progress.Store(nil)
	}

	localNames, err := transfer.recvFiles(path, filter.progress.Load())
	if err != nil {
		return err
	}

	return transfer.clientExit(formatSavedFiles(localNames, path))
}

func (filter *TrzszFilter) uploadFiles(transfer *trzszTransfer, directory bool) error {
	paths, err := filter.chooseUploadPaths(directory)
	if err == zenity.ErrCanceled {
		return transfer.sendAction(false, filter.serverVersion, filter.remoteIsWindows)
	}
	if err != nil {
		return err
	}
	files, err := checkPathsReadable(paths, directory)
	if err != nil {
		return err
	}

	if !filter.transfer.CompareAndSwap(nil, transfer) {
		return simpleTrzszError("Swap transfer failed")
	}

	if err := transfer.sendAction(true, filter.serverVersion, filter.remoteIsWindows); err != nil {
		return err
	}
	config, err := transfer.recvConfig()
	if err != nil {
		return err
	}

	if config.Overwrite {
		if err := checkDuplicateNames(files); err != nil {
			return err
		}
	}

	filter.progress.Store(nil)
	if !config.Quiet {
		filter.progress.Store(newTextProgressBar(filter.clientOut, filter.options.TerminalColumns, config.TmuxPaneColumns))
		defer filter.progress.Store(nil)
	}

	remoteNames, err := transfer.sendFiles(files, filter.progress.Load())
	if err != nil {
		return err
	}
	return transfer.clientExit(formatSavedFiles(remoteNames, ""))
}

func (filter *TrzszFilter) handleTrzsz(mode byte) {
	transfer := newTransfer(filter.serverIn, nil, isWindowsEnvironment() || filter.remoteIsWindows, filter.logger)

	defer func() {
		filter.transfer.CompareAndSwap(transfer, nil)
	}()

	defer func() {
		if err := recover(); err != nil {
			transfer.clientError(newTrzszError(fmt.Sprintf("%v", err), "panic", true))
		}
	}()

	var err error
	switch mode {
	case 'S':
		err = filter.downloadFiles(transfer)
	case 'R':
		err = filter.uploadFiles(transfer, false)
	case 'D':
		err = filter.uploadFiles(transfer, true)
	}
	if err != nil {
		transfer.clientError(err)
	}
}

func (filter *TrzszFilter) resetDragFiles() []string {
	if !filter.dragging.Load() {
		return nil
	}
	filter.dragMutex.Lock()
	defer filter.dragMutex.Unlock()
	filter.dragging.Store(false)
	filter.dragHasDir.Store(false)
	dragFiles := filter.dragFiles
	filter.dragFiles = nil
	return dragFiles
}

func (filter *TrzszFilter) addDragFiles(dragFiles []string, hasDir bool, delay bool) {
	filter.dragMutex.Lock()
	defer filter.dragMutex.Unlock()
	filter.dragging.Store(true)
	if hasDir {
		filter.dragHasDir.Store(true)
	}
	if filter.dragFiles == nil {
		filter.dragFiles = dragFiles
		go func() {
			if delay {
				time.Sleep(300 * time.Millisecond)
			}
			filter.uploadDragFiles()
		}()
	} else {
		filter.dragFiles = append(filter.dragFiles, dragFiles...)
	}
}

func (filter *TrzszFilter) uploadDragFiles() {
	if !filter.dragging.Load() {
		return
	}
	filter.interrupting.Store(true)
	_ = writeAll(filter.serverIn, []byte{0x03})
	time.Sleep(200 * time.Millisecond)
	filter.interrupting.Store(false)
	filter.skipTrzCommand.Store(true)
	if filter.dragHasDir.Load() {
		_ = writeAll(filter.serverIn, []byte("trz -d\r"))
	} else {
		_ = writeAll(filter.serverIn, []byte("trz\r"))
	}
	time.Sleep(time.Second)
	filter.resetDragFiles()
}

func (filter *TrzszFilter) transformPromptInput(promptPipe *io.PipeWriter, buf []byte) {
	const keyPrev = '\x10'
	const keyNext = '\x0E'
	n := len(buf)
	for i := 0; i < n; i++ {
		c := buf[i]
		if c == '\x1b' && n-i > 2 && buf[i+1] == '[' {
			switch buf[i+2] {
			case '\x42': // ↓ to Next
				c = keyNext
			case '\x41', '\x5A': // ↑ Shift-TAB to Prev
				c = keyPrev
			}
			i += 2
		} else {
			switch c {
			case '\x03': // Ctrl-C to Stop
				_, _ = promptPipe.Write([]byte{keyPrev, keyPrev, '\r'})
				return
			case 'q', 'Q', '\x11': // q Ctrl-C Ctrl-Q to Quit
				_, _ = promptPipe.Write([]byte{keyNext, keyNext, '\r'})
				return
			case '\t', '\x0E', 'j', 'J', '\x0A': // Tab ↓ j Ctrl-J to Next
				c = keyNext
			case '\x10', 'k', 'K', '\x0B': // ↑ k Ctrl-K to Prev
				c = keyPrev
			case '\r': // Enter
			default:
				continue
			}
		}
		_, _ = promptPipe.Write([]byte{c})
	}
}

func (filter *TrzszFilter) confirmStopTransfer(transfer *trzszTransfer) {
	pipeIn, pipeOut := io.Pipe()
	if !filter.promptPipe.CompareAndSwap(nil, pipeOut) {
		pipeIn.Close()
		pipeOut.Close()
		return
	}

	transfer.pauseTransferringFiles()

	go func() {
		defer pipeIn.Close()
		defer pipeOut.Close()
		defer filter.promptPipe.Store(nil)

		if progress := filter.progress.Load(); progress != nil {
			progress.setPause(true)
			defer func() {
				progress.setTerminalColumns(filter.options.TerminalColumns)
				progress.setPause(false)
			}()
			time.Sleep(50 * time.Millisecond)             // wait for the progress bar output
			_, _ = filter.clientOut.Write([]byte("\r\n")) // keep the progress bar displayed
		}

		prompt := promptui.Select{
			Label: "Are you sure you want to stop transferring files",
			Items: []string{
				"Stop and keep transferred files",
				"Stop and delete transferred files",
				"Continue to transfer remaining files",
			},
			Stdin:  pipeIn,
			Stdout: filter.clientOut,
			Templates: &promptui.SelectTemplates{
				Help: `{{ "Use ↓ ↑ j k <tab> to navigate" | faint }}`,
			},
		}

		idx, _, err := prompt.Run()

		if transfer := filter.transfer.Load(); transfer != nil {
			if err != nil || idx == 2 {
				transfer.resumeTransferringFiles()
			} else if idx == 0 {
				transfer.stopTransferringFiles(false)
			} else if idx == 1 {
				transfer.stopTransferringFiles(true)
			}
		}
	}()
}

func (filter *TrzszFilter) sendInput(buf []byte) {
	if filter.logger != nil {
		filter.logger.writeTraceLog(buf, "stdin")
	}
	if promptPipe := filter.promptPipe.Load(); promptPipe != nil {
		filter.transformPromptInput(promptPipe, buf)
		return
	}
	if transfer := filter.transfer.Load(); transfer != nil {
		if buf[0] == '\x03' { // `ctrl + c` to stop transferring files
			if filter.serverVersion.compare(&trzszVersion{1, 1, 3}) > 0 {
				filter.confirmStopTransfer(transfer)
			} else {
				transfer.stopTransferringFiles(false)
			}
		}
		return
	}
	if filter.options.DetectDragFile {
		dragFiles, hasDir, ignore := detectDragFiles(buf)
		if dragFiles != nil {
			filter.addDragFiles(dragFiles, hasDir, true)
			return // don't sent the file paths to server
		} else if !ignore {
			filter.resetDragFiles()
		}
	}
	_ = writeAll(filter.serverIn, buf)
}

func (filter *TrzszFilter) wrapInput() {
	buffer := make([]byte, 32*1024)
	for {
		n, err := filter.clientIn.Read(buffer)
		if n > 0 {
			filter.sendInput(buffer[0:n])
		}
		if err == io.EOF {
			if isRunningOnWindows() {
				filter.sendInput([]byte{0x1A}) // ctrl + z
				continue
			}
			filter.serverIn.Close()
			break
		}
	}
}

func (filter *TrzszFilter) wrapOutput() {
	const bufSize = 32 * 1024
	buffer := make([]byte, bufSize)
	detector := newTrzszDetector(false, false)
	for {
		n, err := filter.serverOut.Read(buffer)
		if n > 0 {
			buf := buffer[0:n]
			if filter.logger != nil {
				buf = filter.logger.writeTraceLog(buf, "svrout")
			}
			if transfer := filter.transfer.Load(); transfer != nil {
				transfer.addReceivedData(buf)
				buffer = make([]byte, bufSize)
				continue
			}
			var win bool
			var mode *byte
			var ver *trzszVersion
			buf, mode, ver, win = detector.detectTrzsz(buf)
			if mode != nil {
				_ = writeAll(filter.clientOut, buf)
				filter.serverVersion = ver
				filter.remoteIsWindows = win
				go filter.handleTrzsz(*mode)
				continue
			}
			if filter.interrupting.Load() {
				continue
			}
			if filter.skipTrzCommand.Load() {
				filter.skipTrzCommand.Store(false)
				output := strings.TrimRight(string(trimVT100(buf)), "\r\n")
				if output == "trz" || output == "trz -d" {
					_ = writeAll(filter.clientOut, []byte("\r\n"))
					continue
				}
			}
			_ = writeAll(filter.clientOut, buf)
		}
		if err == io.EOF {
			filter.clientOut.Close()
			break
		}
	}
}
