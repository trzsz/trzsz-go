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
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/atotto/clipboard"
	"github.com/ncruces/zenity"
	"github.com/trzsz/promptui"
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
	// EnableZmodem enable zmodem lrzsz ( rz / sz ) feature.
	EnableZmodem bool
	// EnableOSC52 enable OSC52 clipboard feature.
	EnableOSC52 bool
}

// TrzszFilter is a filter that supports trzsz ( trz / tsz ).
type TrzszFilter struct {
	clientIn            io.Reader
	clientOut           io.WriteCloser
	serverIn            io.WriteCloser
	serverOut           io.Reader
	options             TrzszOptions
	transfer            atomic.Pointer[trzszTransfer]
	zmodem              atomic.Pointer[zmodemTransfer]
	progress            atomic.Pointer[textProgressBar]
	promptPipe          atomic.Pointer[io.PipeWriter]
	trigger             *trzszTrigger
	dragging            atomic.Bool
	dragHasDir          atomic.Bool
	dragMutex           sync.Mutex
	dragFiles           []string
	interrupting        atomic.Bool
	skipTrzCommand      atomic.Bool
	logger              *traceLogger
	defaultUploadPath   atomic.Pointer[string]
	defaultDownloadPath atomic.Pointer[string]
	tunnelConnector     atomic.Pointer[func(int) net.Conn]
	osc52Sequence       *bytes.Buffer
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

// SetDefaultUploadPath set the default open path while choosing upload files.
func (filter *TrzszFilter) SetDefaultUploadPath(uploadPath string) {
	filter.defaultUploadPath.Store(&uploadPath)
}

// SetDefaultDownloadPath set the path to automatically save while downloading files.
func (filter *TrzszFilter) SetDefaultDownloadPath(downloadPath string) {
	filter.defaultDownloadPath.Store(&downloadPath)
}

// SetTunnelConnector set the connector for tunnel transferring.
func (filter *TrzszFilter) SetTunnelConnector(connector func(int) net.Conn) {
	if connector == nil {
		filter.tunnelConnector.Store(nil)
		return
	}
	filter.tunnelConnector.Store(&connector)
}

func (filter *TrzszFilter) getTrzszConfig(name string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	file, err := os.Open(filepath.Join(home, ".trzsz.conf"))
	if err != nil {
		return ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	lowerName := strings.ToLower(name)
	for scanner.Scan() {
		line := scanner.Text()
		idx := strings.Index(line, "=")
		if idx < 0 {
			continue
		}
		if strings.ToLower(strings.TrimSpace(line[:idx])) == lowerName {
			return strings.TrimSpace(line[idx+1:])
		}
	}
	return ""
}

func (filter *TrzszFilter) getDefaultUploadPath() string {
	path := ""
	if p := filter.defaultUploadPath.Load(); p != nil {
		path = *p
	}
	if path == "" {
		path = filter.getTrzszConfig("DefaultUploadPath")
	}
	if path == "" {
		return ""
	}
	if !strings.HasSuffix(path, string(os.PathSeparator)) {
		path += string(os.PathSeparator)
	}
	return resolveHomeDir(path)
}

func (filter *TrzszFilter) getDefaultDownloadPath() string {
	path := ""
	if p := filter.defaultDownloadPath.Load(); p != nil {
		path = *p
	}
	if path == "" {
		path = filter.getTrzszConfig("DefaultDownloadPath")
	}
	if path == "" {
		return ""
	}
	return resolveHomeDir(path)
}

var errUserCanceled = fmt.Errorf("Cancelled")

var parentWindowID = getParentWindowID()

func zenityExecutable() bool {
	_, e := exec.LookPath("zenity")
	return e == nil
}

func zenityErrorWithTips(err error) error {
	if err == zenity.ErrCanceled {
		return errUserCanceled
	}
	if isRunningOnMacOS() || isRunningOnWindows() || zenityExecutable() {
		return fmt.Errorf("Open file dialog failed: %v", err)
	}
	tips := "'zenity' needs to be installed on your local Linux desktop."
	if os.Getenv("WSL_DISTRO_NAME") == "" {
		return simpleTrzszError(tips)
	}
	name := ""
	if len(os.Args) > 0 {
		name = filepath.Base(os.Args[0])
	}
	if !strings.HasSuffix(name, ".exe") {
		name += ".exe"
	}
	return simpleTrzszError("%s Or use the Windows version '%s' in WSL.", tips, name)
}

func (filter *TrzszFilter) chooseDownloadPath() (string, error) {
	savePath := filter.getDefaultDownloadPath()
	if savePath != "" {
		time.Sleep(50 * time.Millisecond) // wait for all output to show
		return savePath, nil
	}
	options := []zenity.Option{
		zenity.Title("Choose a folder to save file(s)"),
		zenity.Directory(),
		zenity.ShowHidden(),
	}
	if isRunningOnMacOS() || isRunningOnWindows() {
		options = append(options, zenity.Attach(parentWindowID))
	}
	path, err := zenity.SelectFile(options...)
	if err != nil {
		return "", zenityErrorWithTips(err)
	}
	if len(path) == 0 {
		return "", errUserCanceled
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
	defaultPath := filter.getDefaultUploadPath()
	if defaultPath != "" {
		options = append(options, zenity.Filename(defaultPath))
	}
	if directory {
		options = append(options, zenity.Directory())
	}
	if isRunningOnMacOS() || isRunningOnWindows() {
		options = append(options, zenity.Attach(parentWindowID))
	}
	files, err := zenity.SelectFileMultiple(options...)
	if err != nil {
		return nil, zenityErrorWithTips(err)
	}
	if len(files) == 0 {
		return nil, errUserCanceled
	}
	return files, nil
}

func (filter *TrzszFilter) createProgressBar(quiet bool, tmuxPaneColumns int32) {
	if quiet {
		filter.progress.Store(nil)
		return
	}
	filter.progress.Store(newTextProgressBar(filter.clientOut, filter.options.TerminalColumns,
		tmuxPaneColumns, filter.trigger.tmuxPrefix))
}

func (filter *TrzszFilter) resetProgressBar() {
	if progress := filter.progress.Load(); progress != nil {
		progress.showCursor()
	}
	filter.progress.Store(nil)
}

func (filter *TrzszFilter) downloadFiles(transfer *trzszTransfer) error {
	path, err := filter.chooseDownloadPath()
	if err == errUserCanceled {
		return transfer.sendAction(false, filter.trigger.version, filter.trigger.winServer)
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

	if err := transfer.sendAction(true, filter.trigger.version, filter.trigger.winServer); err != nil {
		return err
	}
	config, err := transfer.recvConfig()
	if err != nil {
		return err
	}

	filter.createProgressBar(config.Quiet, config.TmuxPaneColumns)
	defer filter.resetProgressBar()

	localNames, err := transfer.recvFiles(path, filter.progress.Load())
	if err != nil {
		return err
	}

	return transfer.clientExit(formatSavedFiles(localNames, path))
}

func (filter *TrzszFilter) uploadFiles(transfer *trzszTransfer, directory bool) error {
	paths, err := filter.chooseUploadPaths(directory)
	if err == errUserCanceled {
		return transfer.sendAction(false, filter.trigger.version, filter.trigger.winServer)
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

	if err := transfer.sendAction(true, filter.trigger.version, filter.trigger.winServer); err != nil {
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

	filter.createProgressBar(config.Quiet, config.TmuxPaneColumns)
	defer filter.resetProgressBar()

	remoteNames, err := transfer.sendFiles(files, filter.progress.Load())
	if err != nil {
		return err
	}
	return transfer.clientExit(formatSavedFiles(remoteNames, ""))
}

func (filter *TrzszFilter) handleTrzsz() {
	transfer := newTransfer(filter.serverIn, nil, isWindowsEnvironment() || filter.trigger.winServer, filter.logger)

	if connector := filter.tunnelConnector.Load(); connector != nil {
		transfer.connectToTunnel(*connector, filter.trigger.uniqueID, filter.trigger.tunnelPort)
	}

	defer func() {
		transfer.cleanup()
		filter.transfer.CompareAndSwap(transfer, nil)
	}()

	defer func() {
		if err := recover(); err != nil {
			transfer.clientError(newTrzszError(fmt.Sprintf("%v", err), "panic", true))
		}
	}()

	var err error
	switch filter.trigger.mode {
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
	time.Sleep(3 * time.Second)
	filter.resetDragFiles()
}

var tmuxInputRegexp = regexp.MustCompile(`send -(l?)t %\d+ (.*?)[;\r]`)

func (filter *TrzszFilter) transformPromptInput(promptPipe *io.PipeWriter, buf []byte) {
	if len(buf) > 6 {
		var input []byte
		for _, match := range tmuxInputRegexp.FindAllSubmatch(buf, -1) {
			if len(match) == 3 {
				if len(match[1]) == 1 {
					input = append(input, match[2]...)
					continue
				}
				for _, hex := range strings.Fields(string(match[2])) {
					if strings.HasPrefix(hex, "0x") {
						if char, err := strconv.ParseInt(hex[2:], 16, 32); err == nil {
							input = append(input, byte(char))
						}
					}
				}
			}
		}
		buf = input
	}

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

		writer := &promptWriter{filter.trigger.tmuxPrefix, filter.clientOut}
		if progress := filter.progress.Load(); progress != nil {
			progress.setPause(true)
			defer func() {
				progress.setTerminalColumns(filter.options.TerminalColumns)
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
			Stdin:  pipeIn,
			Stdout: writer,
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

var ctrlCRegexp = regexp.MustCompile(`^send -t %\d+ 0x3\r$`)

func (filter *TrzszFilter) sendInput(buf []byte, detectDragFile *atomic.Bool) {
	if filter.logger != nil {
		filter.logger.writeTraceLog(buf, "stdin")
	}
	if promptPipe := filter.promptPipe.Load(); promptPipe != nil {
		filter.transformPromptInput(promptPipe, buf)
		return
	}
	if transfer := filter.transfer.Load(); transfer != nil {
		if len(buf) == 1 && buf[0] == '\x03' || len(buf) > 14 && ctrlCRegexp.Match(buf) {
			// `ctrl + c` to stop transferring files
			if filter.trigger.version.compare(&trzszVersion{1, 1, 3}) > 0 {
				filter.confirmStopTransfer(transfer)
			} else {
				transfer.stopTransferringFiles(false)
			}
		}
		return
	}
	if filter.options.EnableZmodem {
		if zmodem := filter.zmodem.Load(); zmodem != nil {
			if len(buf) == 1 && buf[0] == '\x03' {
				zmodem.stopTransferringFiles() // `ctrl + c` to stop transferring files
			}
			if zmodem.isTransferringFiles() {
				return
			}
		}
	}
	if detectDragFile.Load() {
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
	var detectDragFile atomic.Bool
	if filter.options.DetectDragFile {
		go func() {
			if isWarpTerminal() {
				time.Sleep(time.Second)
			}
			detectDragFile.Store(true)
		}()
	}
	for {
		n, err := filter.clientIn.Read(buffer)
		if n > 0 {
			filter.sendInput(buffer[0:n], &detectDragFile)
		}
		if err == io.EOF {
			if isRunningOnWindows() {
				filter.sendInput([]byte{0x1A}, &detectDragFile) // ctrl + z
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
			if transfer := filter.transfer.Load(); transfer != nil {
				transfer.addReceivedData(buf, false)
				buffer = make([]byte, bufSize)
				continue
			}
			if filter.logger != nil {
				buf = filter.logger.writeTraceLog(buf, "svrout")
			}
			if filter.options.EnableZmodem {
				if zmodem := filter.zmodem.Load(); zmodem != nil {
					if zmodem.handleServerOutput(buf) {
						continue
					} else {
						showCursor(filter.clientOut)
						filter.zmodem.CompareAndSwap(zmodem, nil)
					}
				}
			}
			if filter.options.EnableOSC52 {
				filter.detectOSC52(buf)
			}

			var trigger *trzszTrigger
			buf, trigger = detector.detectTrzsz(buf, filter.tunnelConnector.Load() != nil)
			if trigger != nil {
				_ = writeAll(filter.clientOut, buf)
				filter.trigger = trigger
				go filter.handleTrzsz()
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

			if filter.options.EnableZmodem {
				if zmodem := detectZmodem(buf); zmodem != nil {
					_ = writeAll(filter.clientOut, buf)
					if filter.zmodem.CompareAndSwap(nil, zmodem) {
						hideCursor(filter.clientOut)
						go zmodem.handleZmodemEvent(filter.logger, filter.serverIn, filter.clientOut,
							func() ([]string, error) {
								return filter.chooseUploadPaths(false)
							},
							filter.chooseDownloadPath)
						continue
					}
				}
			}

			_ = writeAll(filter.clientOut, buf)
		}
		if err == io.EOF {
			continue // ignore output EOF
		}
	}
}

func (filter *TrzszFilter) detectOSC52(buf []byte) {
	if filter.osc52Sequence == nil {
		pos := bytes.Index(buf, []byte("\x1b]52;c;"))
		if pos < 0 {
			return
		}
		buf = buf[pos+7:]
		pos = bytes.IndexByte(buf, '\a')
		if pos < 0 {
			filter.osc52Sequence = bytes.NewBuffer(nil)
			filter.osc52Sequence.Write(buf)
			return
		}
		writeToClipboard(buf[:pos])
		buf = buf[pos+1:]
		if len(buf) > 0 {
			filter.detectOSC52(buf)
		}
		return
	}

	pos := bytes.IndexByte(buf, '\a')
	if pos < 0 {
		filter.osc52Sequence.Write(buf)
		if filter.osc52Sequence.Len() > 100000 {
			// something went wrong, just ignore it
			filter.osc52Sequence = nil
		}
		return
	}

	filter.osc52Sequence.Write(buf[:pos])
	writeToClipboard(filter.osc52Sequence.Bytes())
	filter.osc52Sequence = nil

	buf = buf[pos+1:]
	if len(buf) > 0 {
		filter.detectOSC52(buf)
	}
}

func writeToClipboard(buf []byte) {
	text, err := base64.StdEncoding.DecodeString(string(buf))
	if err != nil {
		return
	}
	_ = clipboard.WriteAll(string(text))
}
