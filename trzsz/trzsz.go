/*
MIT License

Copyright (c) 2023 Lonny Wong

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
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ncruces/zenity"
	"golang.org/x/term"
)

type TrzszArgs struct {
	Help     bool
	Version  bool
	Relay    bool
	TraceLog bool
	DragFile bool
	Name     string
	Args     []string
}

var gTrzszArgs TrzszArgs
var gTraceLogFile atomic.Pointer[os.File]
var gTraceLogChan atomic.Pointer[chan []byte]
var gDragging atomic.Bool
var gDragHasDir atomic.Bool
var gDragMutex sync.Mutex
var gDragFiles []string
var gInterrupting atomic.Bool
var gSkipTrzCommand atomic.Bool
var gTransfer atomic.Pointer[TrzszTransfer]
var gUniqueIDMap = make(map[string]int)
var parentWindowID = getParentWindowID()
var trzszRegexp = regexp.MustCompile("::TRZSZ:TRANSFER:([SRD]):(\\d+\\.\\d+\\.\\d+)(:\\d+)?")

func printVersion() {
	fmt.Printf("trzsz go %s\n", kTrzszVersion)
}

func printHelp() {
	fmt.Print("usage: trzsz [-h] [-v] [-r] [-t] [-d] command line\n\n" +
		"Wrapping command line to support trzsz ( trz / tsz ).\n\n" +
		"positional arguments:\n" +
		"  command line       the original command line\n\n" +
		"optional arguments:\n" +
		"  -h, --help         show this help message and exit\n" +
		"  -v, --version      show version number and exit\n" +
		"  -r, --relay        run as a trzsz relay server\n" +
		"  -t, --tracelog     eanble trace log for debugging\n" +
		"  -d, --dragfile     enable drag file(s) to upload\n")
}

func parseTrzszArgs() {
	var i int
	for i = 1; i < len(os.Args); i++ {
		if os.Args[i] == "-h" || os.Args[i] == "--help" {
			gTrzszArgs.Help = true
			return
		} else if os.Args[i] == "-v" || os.Args[i] == "--version" {
			gTrzszArgs.Version = true
			return
		} else if os.Args[i] == "-r" || os.Args[i] == "--relay" {
			gTrzszArgs.Relay = true
		} else if os.Args[i] == "-t" || os.Args[i] == "--tracelog" {
			gTrzszArgs.TraceLog = true
		} else if os.Args[i] == "-d" || os.Args[i] == "--dragfile" {
			gTrzszArgs.DragFile = true
		} else {
			break
		}
	}
	if i >= len(os.Args) {
		gTrzszArgs.Help = true
		return
	}
	gTrzszArgs.Name = os.Args[i]
	gTrzszArgs.Args = os.Args[i+1:]
}

func getTrzszConfig(name string) *string {
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

func detectTrzsz(output []byte) (*byte, bool) {
	if len(output) < 24 {
		return nil, false
	}
	idx := bytes.LastIndex(output, []byte("::TRZSZ:TRANSFER:"))
	if idx < 0 {
		return nil, false
	}
	match := trzszRegexp.FindSubmatch(output[idx:])
	if len(match) < 2 {
		return nil, false
	}
	uniqueID := ""
	if len(match) > 3 {
		uniqueID = string(match[3])
	}
	if len(uniqueID) >= 8 && (IsWindows() || !(len(uniqueID) == 14 && strings.HasSuffix(uniqueID, "00"))) {
		if _, ok := gUniqueIDMap[uniqueID]; ok {
			return nil, false
		}
		if len(gUniqueIDMap) > 100 {
			m := make(map[string]int)
			for k, v := range gUniqueIDMap {
				if v >= 50 {
					m[k] = v - 50
				}
			}
			gUniqueIDMap = m
		}
		gUniqueIDMap[uniqueID] = len(gUniqueIDMap)
	}
	remoteIsWindows := false
	if uniqueID == ":1" || (len(uniqueID) == 14 && strings.HasSuffix(uniqueID, "10")) {
		remoteIsWindows = true
	}
	return &match[1][0], remoteIsWindows
}

func chooseDownloadPath() (string, error) {
	savePath := getTrzszConfig("DefaultDownloadPath")
	if savePath != nil {
		return *savePath, nil
	}
	path, err := zenity.SelectFile(
		zenity.Title("Choose a folder to save file(s)"),
		zenity.Directory(),
		zenity.ShowHidden(),
		zenity.Attach(parentWindowID),
	)
	if err != nil {
		return "", err
	}
	if len(path) == 0 {
		return "", zenity.ErrCanceled
	}
	return path, nil
}

func chooseUploadPaths(directory bool) ([]string, error) {
	if gDragging.Load() == true {
		files := resetDragFiles()
		return files, nil
	}
	options := []zenity.Option{
		zenity.Title("Choose some files to send"),
		zenity.ShowHidden(),
		zenity.Attach(parentWindowID),
	}
	defaultPath := getTrzszConfig("DefaultUploadPath")
	if defaultPath != nil {
		options = append(options, zenity.Filename(*defaultPath))
	}
	if directory {
		options = append(options, zenity.Directory())
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

func newProgressBar(pty *TrzszPty, config *TransferConfig) (*TextProgressBar, error) {
	if config.Quiet {
		return nil, nil
	}
	columns, err := pty.GetColumns()
	if err != nil {
		return nil, err
	}
	return NewTextProgressBar(os.Stdout, columns, config.TmuxPaneColumns), nil
}

func downloadFiles(pty *TrzszPty, transfer *TrzszTransfer, remoteIsWindows bool) error {
	path, err := chooseDownloadPath()
	if err == zenity.ErrCanceled {
		return transfer.sendAction(false, remoteIsWindows)
	}
	if err != nil {
		return err
	}
	if err := checkPathWritable(path); err != nil {
		return err
	}

	if err := transfer.sendAction(true, remoteIsWindows); err != nil {
		return err
	}
	config, err := transfer.recvConfig()
	if err != nil {
		return err
	}

	progress, err := newProgressBar(pty, config)
	if err != nil {
		return err
	}
	if progress != nil && !reflect.ValueOf(progress).IsNil() {
		pty.OnResize(func(cols int) { progress.setTerminalColumns(cols) })
		defer pty.OnResize(nil)
	}

	localNames, err := transfer.recvFiles(path, progress)
	if err != nil {
		return err
	}

	return transfer.clientExit(fmt.Sprintf("Saved %s to %s", strings.Join(localNames, ", "), path))
}

func uploadFiles(pty *TrzszPty, transfer *TrzszTransfer, directory, remoteIsWindows bool) error {
	paths, err := chooseUploadPaths(directory)
	if err == zenity.ErrCanceled {
		return transfer.sendAction(false, remoteIsWindows)
	}
	if err != nil {
		return err
	}
	files, err := checkPathsReadable(paths, directory)
	if err != nil {
		return err
	}

	if err := transfer.sendAction(true, remoteIsWindows); err != nil {
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

	progress, err := newProgressBar(pty, config)
	if err != nil {
		return err
	}
	if progress != nil && !reflect.ValueOf(progress).IsNil() {
		pty.OnResize(func(cols int) { progress.setTerminalColumns(cols) })
		defer pty.OnResize(nil)
	}

	remoteNames, err := transfer.sendFiles(files, progress)
	if err != nil {
		return err
	}

	return transfer.clientExit(fmt.Sprintf("Received %s", strings.Join(remoteNames, ", ")))
}

func handleTrzsz(pty *TrzszPty, mode byte, remoteIsWindows bool) {
	transfer := NewTransfer(pty.Stdin, nil, IsWindows() || remoteIsWindows)

	gTransfer.Store(transfer)
	defer func() {
		gTransfer.Store(nil)
	}()

	defer func() {
		if err := recover(); err != nil {
			transfer.clientError(NewTrzszError(fmt.Sprintf("%v", err), "panic", true))
		}
	}()

	var err error
	switch mode {
	case 'S':
		err = downloadFiles(pty, transfer, remoteIsWindows)
	case 'R':
		err = uploadFiles(pty, transfer, false, remoteIsWindows)
	case 'D':
		err = uploadFiles(pty, transfer, true, remoteIsWindows)
	}
	if err != nil {
		transfer.clientError(err)
	}
}

func resetDragFiles() []string {
	if gDragging.Load() == false {
		return nil
	}
	gDragMutex.Lock()
	defer gDragMutex.Unlock()
	gDragging.Store(false)
	gDragHasDir.Store(false)
	dragFiles := gDragFiles
	gDragFiles = nil
	return dragFiles
}

func addDragFiles(dragFiles []string, hasDir bool) bool {
	gDragMutex.Lock()
	defer gDragMutex.Unlock()
	gDragging.Store(true)
	if hasDir {
		gDragHasDir.Store(true)
	}
	if gDragFiles == nil {
		gDragFiles = dragFiles
		return true
	}
	gDragFiles = append(gDragFiles, dragFiles...)
	return false
}

func uploadDragFiles(pty *TrzszPty) {
	time.Sleep(300 * time.Millisecond)
	if gDragging.Load() == false {
		return
	}
	gInterrupting.Store(true)
	writeAll(pty.Stdin, []byte{0x03})
	time.Sleep(200 * time.Millisecond)
	gInterrupting.Store(false)
	gSkipTrzCommand.Store(true)
	if gDragHasDir.Load() == true {
		writeAll(pty.Stdin, []byte("trz -d\r"))
	} else {
		writeAll(pty.Stdin, []byte("trz\r"))
	}
	time.Sleep(time.Second)
	resetDragFiles()
}

/**
 * ┌────────────────────┬───────────────────────────────────────────┬────────────────────────────────────────────┐
 * │                    │ Enable trace log                          │ Disable trace log                          │
 * ├────────────────────┼───────────────────────────────────────────┼────────────────────────────────────────────┤
 * │ Windows cmd        │ echo ^<ENABLE_TRZSZ_TRACE_LOG^>           │ echo ^<DISABLE_TRZSZ_TRACE_LOG^>           │
 * ├────────────────────┼───────────────────────────────────────────┼────────────────────────────────────────────┤
 * │ Windows PowerShell │ echo "<ENABLE_TRZSZ_TRACE_LOG$([char]62)" │ echo "<DISABLE_TRZSZ_TRACE_LOG$([char]62)" │
 * ├────────────────────┼───────────────────────────────────────────┼────────────────────────────────────────────┤
 * │ Linux and macOS    │ echo -e '<ENABLE_TRZSZ_TRACE_LOG\x3E'     │ echo -e '<DISABLE_TRZSZ_TRACE_LOG\x3E'     │
 * └────────────────────┴───────────────────────────────────────────┴────────────────────────────────────────────┘
 */
func writeTraceLog(buf []byte, typ string) []byte {
	if ch, file := gTraceLogChan.Load(), gTraceLogFile.Load(); ch != nil && file != nil {
		if typ == "svrout" && bytes.Contains(buf, []byte("<DISABLE_TRZSZ_TRACE_LOG>")) {
			msg := fmt.Sprintf("Closed trace log at %s", file.Name())
			close(*ch)
			gTraceLogChan.Store(nil)
			gTraceLogFile.Store(nil)
			return bytes.ReplaceAll(buf, []byte("<DISABLE_TRZSZ_TRACE_LOG>"), []byte(msg))
		}
		*ch <- []byte(fmt.Sprintf("[%s]%s\n", typ, encodeBytes(buf)))
		return buf
	}
	if typ == "svrout" && bytes.Contains(buf, []byte("<ENABLE_TRZSZ_TRACE_LOG>")) {
		var msg string
		file, err := os.CreateTemp("", "trzsz_*.log")
		if err != nil {
			msg = fmt.Sprintf("Create log file error: %v", err)
		} else {
			msg = fmt.Sprintf("Writing trace log to %s", file.Name())
		}
		ch := make(chan []byte, 10000)
		gTraceLogChan.Store(&ch)
		gTraceLogFile.Store(file)
		go func() {
			for {
				select {
				case buf, ok := <-ch:
					if !ok {
						file.Close()
						return
					}
					writeAll(file, buf)
				case <-time.After(3 * time.Second):
					file.Sync()
				}
			}
		}()
		return bytes.ReplaceAll(buf, []byte("<ENABLE_TRZSZ_TRACE_LOG>"), []byte(msg))
	}
	return buf
}

func sendInput(pty *TrzszPty, buf []byte) {
	if gTrzszArgs.TraceLog {
		writeTraceLog(buf, "stdin")
	}
	if transfer := gTransfer.Load(); transfer != nil {
		if buf[0] == '\x03' { // `ctrl + c` to stop transferring files
			transfer.stopTransferringFiles()
		}
		return
	}
	if gTrzszArgs.DragFile {
		dragFiles, hasDir, ignore := detectDragFiles(buf)
		if dragFiles != nil {
			if addDragFiles(dragFiles, hasDir) {
				go uploadDragFiles(pty)
			}
			return
		}
		if !ignore {
			resetDragFiles()
		}
	}
	writeAll(pty.Stdin, buf)

}

func wrapInput(pty *TrzszPty) {
	buffer := make([]byte, 32*1024)
	for {
		n, err := os.Stdin.Read(buffer)
		if n > 0 {
			sendInput(pty, buffer[0:n])
		}
		if err == io.EOF {
			if IsWindows() {
				sendInput(pty, []byte{0x1A}) // ctrl + z
				continue
			}
			pty.Stdin.Close()
			break
		}
	}
}

func wrapOutput(pty *TrzszPty) {
	const bufSize = 32 * 1024
	buffer := make([]byte, bufSize)
	for {
		n, err := pty.Stdout.Read(buffer)
		if n > 0 {
			buf := buffer[0:n]
			if gTrzszArgs.TraceLog {
				buf = writeTraceLog(buf, "svrout")
			}
			if transfer := gTransfer.Load(); transfer != nil {
				transfer.addReceivedData(buf)
				buffer = make([]byte, bufSize)
				continue
			}
			mode, remoteIsWindows := detectTrzsz(buf)
			if mode != nil {
				writeAll(os.Stdout, bytes.Replace(buf, []byte("TRZSZ"), []byte("TRZSZGO"), 1))
				go handleTrzsz(pty, *mode, remoteIsWindows)
				continue
			}
			if gInterrupting.Load() == true {
				continue
			}
			if gSkipTrzCommand.Load() == true {
				gSkipTrzCommand.Store(false)
				output := strings.TrimRight(string(trimVT100(buf)), "\r\n")
				if output == "trz" || output == "trz -d" {
					os.Stdout.WriteString("\r\n")
					continue
				}
			}
			writeAll(os.Stdout, buf)
		}
		if err == io.EOF {
			os.Stdout.Close()
			break
		}
	}
}

func handleSignal(pty *TrzszPty) {
	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGTERM)
	go func() {
		<-sigterm
		pty.Terminate()
	}()

	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, os.Interrupt)
	go func() {
		for {
			<-sigint
			if transfer := gTransfer.Load(); transfer != nil {
				transfer.stopTransferringFiles()
			}
		}
	}()
}

// TrzszMain entry of trzsz client
func TrzszMain() int {
	// parse command line arguments
	parseTrzszArgs()
	if gTrzszArgs.Help {
		printHelp()
		return 0
	}
	if gTrzszArgs.Version {
		printVersion()
		return 0
	}

	// spawn a pty
	pty, err := Spawn(gTrzszArgs.Name, gTrzszArgs.Args...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return -1
	}
	defer func() { pty.Close() }()

	// set stdin in raw mode
	if state, err := term.MakeRaw(int(os.Stdin.Fd())); err == nil {
		defer func() { _ = term.Restore(int(os.Stdin.Fd()), state) }()
	}

	if gTrzszArgs.Relay {
		// run as relay
		go runAsRelay(pty)
	} else {
		// wrap input and output
		go wrapInput(pty)
		go wrapOutput(pty)
		// handle signal
		go handleSignal(pty)
	}

	// wait for exit
	pty.Wait()
	return pty.ExitCode()
}
