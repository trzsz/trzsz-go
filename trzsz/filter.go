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
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/atotto/clipboard"
	"github.com/google/shlex"
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
	// EnableZmodem enable zmodem lrzsz ( rz / sz ) feature.
	EnableZmodem bool
	// EnableOSC52 enable OSC52 clipboard feature.
	EnableOSC52 bool
}

// TrzszFilter is a filter that supports trzsz ( trz / tsz ).
type TrzszFilter struct {
	clientIn              io.Reader
	clientOut             io.WriteCloser
	serverIn              io.WriteCloser
	serverOut             io.Reader
	options               TrzszOptions
	transfer              atomic.Pointer[trzszTransfer]
	zmodem                atomic.Pointer[zmodemTransfer]
	progress              atomic.Pointer[textProgressBar]
	trigger               *trzszTrigger
	dragFiles             atomic.Pointer[[]string]
	dragInputBuffer       *bytes.Buffer
	dragBufferMutex       sync.Mutex
	interrupting          atomic.Bool
	skipUploadCommand     atomic.Bool
	uploadCommandIsNotTrz atomic.Bool
	logger                *traceLogger
	defaultUploadPath     atomic.Pointer[string]
	defaultDownloadPath   atomic.Pointer[string]
	dragFileUploadCommand atomic.Pointer[string]
	currentUploadCommand  atomic.Pointer[string]
	tunnelConnector       atomic.Pointer[func(int) net.Conn]
	redrawScreenFunc      atomic.Pointer[func()]
	transferStateCallback atomic.Pointer[func(bool)]
	osc52Sequence         *bytes.Buffer
	progressColorPair     atomic.Pointer[string]
	oneTimeUploadFiles    []string
	oneTimeUploadResult   chan error
	hidingCursor          bool
	closed                atomic.Bool
}

// NewTrzszFilter create a TrzszFilter to support trzsz ( trz / tsz ).
//
// ┌────────┐   ClientIn   ┌─────────────┐   ServerIn   ┌────────┐
// │        ├─────────────►│             ├─────────────►│        │
// │ Client │              │ TrzszFilter │              │ Server │
// │        │◄─────────────┤             │◄─────────────┤        │
// └────────┘   ClientOut  └─────────────┘   ServerOut  └────────┘
//
// Please specify the columns of the terminal in options.TerminalColumns.
//
// Note that if you pass os.Stdout directly as clientOut,
// os.Stdout will be closed when serverOut is closed,
// and you will no longer be able to use os.Stdout to output anything else.
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

// SetTerminalColumns sets the latest columns of the terminal.
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
	if filter.dragFiles.Load() != nil {
		return simpleTrzszError("Is dragging files to upload")
	}
	go filter.uploadDragFiles(&dragFilesInfo{files: filePaths, hasDir: hasDir})
	return nil
}

// OneTimeUpload upload one time while the server is already running trz / rz.
func (filter *TrzszFilter) OneTimeUpload(filePaths []string) (<-chan error, error) {
	if len(filePaths) == 0 {
		return nil, fmt.Errorf("nothing to upload")
	}
	for _, path := range filePaths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if _, err := checkPathsReadable([]string{path}, info.IsDir()); err != nil {
			return nil, err
		}
	}
	filter.oneTimeUploadFiles = filePaths
	filter.oneTimeUploadResult = make(chan error, 1)
	go func() {
		time.Sleep(10 * time.Second)
		if filter.oneTimeUploadFiles != nil {
			filter.oneTimeUploadResult <- fmt.Errorf(
				"the upload did not start, possibly because trzsz is not installed or trz is not found on the server")
		}
	}()
	return filter.oneTimeUploadResult, nil
}

// ResetTerminal reset the terminal settings.
func (filter *TrzszFilter) ResetTerminal() {
	if filter.hidingCursor {
		showCursor(filter.clientOut)
		filter.hidingCursor = false
	}
}

// SetDefaultUploadPath sets the default open path while choosing upload files.
func (filter *TrzszFilter) SetDefaultUploadPath(path string) {
	if path == "" {
		filter.defaultUploadPath.Store(&path)
		return
	}
	path = resolveHomeDir(path)
	if !strings.HasSuffix(path, string(os.PathSeparator)) {
		path += string(os.PathSeparator)
	}
	filter.defaultUploadPath.Store(&path)
}

// SetDefaultDownloadPath sets the path to automatically save while downloading files.
func (filter *TrzszFilter) SetDefaultDownloadPath(path string) {
	if path == "" {
		filter.defaultDownloadPath.Store(&path)
		return
	}
	path = resolveHomeDir(path)
	filter.defaultDownloadPath.Store(&path)
}

// SetDragFileUploadCommand sets the command to execute while dragging files to upload.
func (filter *TrzszFilter) SetDragFileUploadCommand(command string) {
	filter.uploadCommandIsNotTrz.Store(false)
	if command != "" {
		tokens, err := shlex.Split(command)
		if err == nil && len(tokens) > 0 {
			name := filepath.Base(tokens[0])
			if name != "trz" && name != "trz.exe" {
				filter.uploadCommandIsNotTrz.Store(true)
			}
		}
	}
	filter.dragFileUploadCommand.Store(&command)
}

// SetProgressColorPair sets the color pair for the progress bar.
func (filter *TrzszFilter) SetProgressColorPair(colorPair string) {
	filter.progressColorPair.Store(&colorPair)
}

// SetTunnelConnector sets the connector for tunnel transferring.
func (filter *TrzszFilter) SetTunnelConnector(connector func(int) net.Conn) {
	if connector == nil {
		filter.tunnelConnector.Store(nil)
		return
	}
	filter.tunnelConnector.Store(&connector)
}

// SetRedrawScreenFunc sets the RedrawScreen function for transfer completed.
func (filter *TrzszFilter) SetRedrawScreenFunc(redrawScreenFunc func()) {
	if redrawScreenFunc == nil {
		filter.redrawScreenFunc.Store(nil)
		return
	}
	filter.redrawScreenFunc.Store(&redrawScreenFunc)
}

// SetTransferStateCallback sets the callback for starting and ending the transfer.
func (filter *TrzszFilter) SetTransferStateCallback(transferStateCallback func(transferring bool)) {
	if transferStateCallback == nil {
		filter.transferStateCallback.Store(nil)
		return
	}
	filter.transferStateCallback.Store(&transferStateCallback)
}

// Close to let the filter gracefully exit.
func (filter *TrzszFilter) Close() {
	filter.closed.Store(true)
}

func (filter *TrzszFilter) readTrzszConfig() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	file, err := os.Open(filepath.Join(home, ".trzsz.conf"))
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		idx := strings.Index(line, "#")
		if idx >= 0 {
			line = line[:idx]
		}
		idx = strings.Index(line, "=")
		if idx < 0 {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(line[:idx]))
		value := strings.TrimSpace(line[idx+1:])
		if name == "" || value == "" {
			continue
		}
		switch {
		case name == "defaultuploadpath" && filter.defaultUploadPath.Load() == nil:
			filter.SetDefaultUploadPath(value)
		case name == "defaultdownloadpath" && filter.defaultDownloadPath.Load() == nil:
			filter.SetDefaultDownloadPath(value)
		case name == "dragfileuploadcommand" && filter.dragFileUploadCommand.Load() == nil:
			filter.SetDragFileUploadCommand(value)
		case name == "progresscolorpair" && filter.progressColorPair.Load() == nil:
			filter.SetProgressColorPair(value)
		}
	}
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
		return fmt.Errorf("open file dialog failed: %v", err)
	}
	tips := "'zenity' needs to be installed on your local Linux desktop."
	if os.Getenv("WSL_DISTRO_NAME") == "" {
		return simpleTrzszError("%s", tips)
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
	savePath := ""
	if path := filter.defaultDownloadPath.Load(); path != nil {
		savePath = *path
	}
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
	if len(filter.oneTimeUploadFiles) > 0 {
		files := filter.oneTimeUploadFiles
		filter.oneTimeUploadFiles = nil
		return files, nil
	}
	if files := filter.dragFiles.Swap(nil); files != nil {
		return *files, nil
	}
	options := []zenity.Option{
		zenity.Title("Choose some files to send"),
		zenity.ShowHidden(),
	}
	defaultPath := ""
	if path := filter.defaultUploadPath.Load(); path != nil {
		defaultPath = *path
	}
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
	colorPair := ""
	if color := filter.progressColorPair.Load(); color != nil {
		colorPair = *color
	}
	filter.progress.Store(newTextProgressBar(filter.clientOut, filter.options.TerminalColumns,
		tmuxPaneColumns, filter.trigger.tmuxPrefix, colorPair))
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
	transfer := newTransfer(filter.serverIn, nil)
	transfer.trzszFilter = filter
	transfer.flushInTime = isWindowsEnvironment() || filter.trigger.winServer
	if filter.trigger.tmuxPaneID != "" {
		transfer.tmuxPaneID = []byte(filter.trigger.tmuxPaneID)
		transfer.tmuxInputAckChan = make(chan bool, 100)
	}

	if !filter.transfer.CompareAndSwap(nil, transfer) {
		return
	}
	if callback := filter.transferStateCallback.Load(); callback != nil {
		go (*callback)(true)
	}
	defer func() {
		if filter.transfer.CompareAndSwap(transfer, nil) {
			if callback := filter.transferStateCallback.Load(); callback != nil {
				go (*callback)(false)
			}
		}
	}()

	if connector := filter.tunnelConnector.Load(); connector != nil {
		transfer.connectToTunnel(*connector, filter.trigger.uniqueID, filter.trigger.tunnelPort)
	}

	done := make(chan struct{}, 1)
	go func() {
		defer close(done)
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
			filter.setOneTimeUploadResult(err)
		case 'D':
			err = filter.uploadFiles(transfer, true)
			filter.setOneTimeUploadResult(err)
		}
		if err != nil {
			transfer.clientError(err)
		}
		transfer.cleanup()
		done <- struct{}{}
	}()

	select {
	case <-done:
	case <-transfer.background():
	}
	transfer.tmuxAckWaitGroup.Wait()
}

func (filter *TrzszFilter) setOneTimeUploadResult(err error) {
	if filter.oneTimeUploadResult == nil {
		return
	}
	filter.oneTimeUploadResult <- err
	close(filter.oneTimeUploadResult)
	filter.oneTimeUploadResult = nil
}

func (filter *TrzszFilter) uploadDragFiles(dragInfo *dragFilesInfo) {
	if !filter.dragFiles.CompareAndSwap(nil, &dragInfo.files) {
		return
	}

	filter.interrupting.Store(true)
	if dragInfo.tmuxPaneID != "" {
		if count := dragInfo.tmuxBlocks - 1; count > 0 {
			now := time.Now().Unix()
			ack := fmt.Appendf(nil, "%%begin %d 1 1\r\n%%end %d 1 1\r\n", now, now)
			for range count {
				_, _ = os.Stderr.Write(ack)
			}
		}
		_ = writeAll(filter.serverIn, []byte(fmt.Sprintf("send -t %%%s 0x3\r", dragInfo.tmuxPaneID)))
		time.Sleep(300 * time.Millisecond) // sleep a bit longer to avoid iTerm2 receiving %begin/%end
	} else {
		_ = writeAll(filter.serverIn, []byte{0x03})
	}
	time.Sleep(200 * time.Millisecond)
	filter.interrupting.Store(false)

	filter.skipUploadCommand.Store(true)
	command := ""
	if cmd := filter.dragFileUploadCommand.Load(); cmd != nil {
		command = *cmd
	}
	if command == "" {
		command = "trz"
	}
	if dragInfo.hasDir && !filter.uploadCommandIsNotTrz.Load() {
		command += " -d"
	}
	filter.currentUploadCommand.Store(&command)

	command += "\r"
	buffer := []byte(command)
	if dragInfo.tmuxPaneID != "" {
		var buf bytes.Buffer
		buf.WriteString("send -t %")
		buf.WriteString(dragInfo.tmuxPaneID)
		buf.WriteByte(' ')
		buf.Write(convertToHexStrings(buffer))
		buf.WriteByte('\r')
		buffer = buf.Bytes()
	}

	if filter.logger != nil {
		filter.logger.writeTraceLog(buffer, "upload")
	}
	_ = writeAll(filter.serverIn, buffer)

	time.Sleep(3 * time.Second)
	filter.dragFiles.CompareAndSwap(&dragInfo.files, nil)
}

func (filter *TrzszFilter) sendInput(buf []byte, detectDragFile *atomic.Bool) {
	if filter.logger != nil {
		filter.logger.writeTraceLog(buf, "stdin")
	}

	if transfer := filter.transfer.Load(); transfer != nil {
		transfer.handleClientInput(buf)
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
		filter.dragBufferMutex.Lock()
		defer filter.dragBufferMutex.Unlock()
		if filter.dragInputBuffer != nil {
			filter.dragInputBuffer.Write(buf)
			return
		}
		if dragInfo := detectDragFiles(buf); dragInfo.files != nil || dragInfo.prefix {
			filter.dragInputBuffer = bytes.NewBuffer(nil)
			filter.dragInputBuffer.Write(buf)
			go func() {
				time.Sleep(300 * time.Millisecond)
				filter.dragBufferMutex.Lock()
				defer filter.dragBufferMutex.Unlock()
				buffer := filter.dragInputBuffer.Bytes()
				filter.dragInputBuffer = nil
				if dragInfo := detectDragFiles(buffer); dragInfo.files != nil {
					go filter.uploadDragFiles(&dragInfo)
					return // don't sent the file paths to server
				}
				_ = writeAll(filter.serverIn, buffer)
			}()
			return
		}
	}

	_ = writeAll(filter.serverIn, buf)
}

func (filter *TrzszFilter) wrapInput() {
	defer func() { _ = filter.serverIn.Close() }()
	buffer := make([]byte, 32*1024)
	var detectDragFile atomic.Bool
	if filter.options.DetectDragFile {
		go func() {
			if isWarpTerminal() {
				// for old warp terminal, if detect drag file too early may cause block feature to not work.
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
			if isRunningOnWindows() && !filter.closed.Load() {
				filter.sendInput([]byte{0x1A}, &detectDragFile) // ctrl + z
				time.Sleep(100 * time.Millisecond)              // give it a break just in case of real EOF
				continue
			}
			break
		} else if err != nil {
			break
		}
	}
}

func (filter *TrzszFilter) wrapOutput() {
	defer func() { _ = filter.clientOut.Close() }()
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
						filter.hidingCursor = false
						if filter.zmodem.CompareAndSwap(zmodem, nil) {
							if callback := filter.transferStateCallback.Load(); callback != nil {
								go (*callback)(false)
							}
						}
					}
				}
			}
			if filter.options.EnableOSC52 {
				filter.detectOSC52(buf)
			}

			var trigger *trzszTrigger
			buf, trigger = detector.detectTrzsz(buf, true)
			if trigger != nil {
				_ = writeAll(filter.clientOut, buf)
				filter.trigger = trigger
				go filter.handleTrzsz()
				continue
			}
			if filter.interrupting.Load() {
				continue
			}
			if filter.skipUploadCommand.Load() {
				filter.skipUploadCommand.Store(false)
				output := strings.TrimRight(string(trimVT100(buf)), "\r\n")
				if command := filter.currentUploadCommand.Load(); command != nil && *command == output {
					_ = writeAll(filter.clientOut, []byte("\r\n"))
					continue
				}
			}

			if filter.options.EnableZmodem {
				if zmodem := detectZmodem(buf); zmodem != nil {
					_ = writeAll(filter.clientOut, buf)
					zmodem.redrawScreen = filter.redrawScreenFunc.Load()
					if filter.zmodem.CompareAndSwap(nil, zmodem) {
						if callback := filter.transferStateCallback.Load(); callback != nil {
							go (*callback)(true)
						}
						hideCursor(filter.clientOut)
						filter.hidingCursor = true
						go zmodem.handleZmodemEvent(filter.logger, filter.serverIn, filter.clientOut,
							func() ([]string, error) {
								return filter.chooseUploadPaths(false)
							},
							filter.chooseDownloadPath)
						if filter.oneTimeUploadResult != nil {
							go func() {
								for zmodem.isTransferringFiles() {
									time.Sleep(100 * time.Millisecond)
								}
								filter.setOneTimeUploadResult(nil)
							}()
						}
						continue
					}
				}
			}

			_ = writeAll(filter.clientOut, buf)
		}
		if err != nil {
			break
		}
	}
}

func (filter *TrzszFilter) detectOSC52(buf []byte) {
	for len(buf) > 0 {
		if filter.osc52Sequence == nil {
			for {
				pos := bytes.Index(buf, []byte("\x1b]52;"))
				if pos < 0 {
					return
				}
				buf = buf[pos+5:]
				if len(buf) < 2 {
					return
				}
				if (buf[0] == 'c' || buf[0] == 'p') && buf[1] == ';' {
					buf = buf[2:]
					break
				}
				buf = buf[2:]
			}

			pos := bytes.IndexAny(buf, "\a\x1b")
			if pos < 0 {
				filter.osc52Sequence = bytes.NewBuffer(nil)
				filter.osc52Sequence.Write(buf)
				return
			}

			writeToClipboard(buf[:pos])
			buf = buf[pos+1:]
			continue
		}

		pos := bytes.IndexAny(buf, "\a\x1b")
		if pos < 0 {
			filter.osc52Sequence.Write(buf)
			if filter.osc52Sequence.Len() > 100000 {
				for _, b := range buf {
					if (b < 'A' || b > 'Z') && (b < 'a' || b > 'z') && (b < '0' || b > '9') && b != '+' && b != '/' && b != '=' {
						// something went wrong, just ignore it
						filter.osc52Sequence = nil
						return
					}
				}
			}
			return
		}

		filter.osc52Sequence.Write(buf[:pos])
		writeToClipboard(filter.osc52Sequence.Bytes())
		filter.osc52Sequence = nil
		buf = buf[pos+1:]
	}
}

var writeToClipboard = func(buf []byte) {
	text, err := base64.StdEncoding.DecodeString(string(buf))
	if err != nil {
		return
	}
	_ = clipboard.WriteAll(string(text))
}
