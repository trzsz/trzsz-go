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
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

var isLinux bool = (runtime.GOOS == "linux")
var isMacOS bool = (runtime.GOOS == "darwin")
var isWindows bool = (runtime.GOOS == "windows")

func isRunningOnLinux() bool {
	return isLinux
}

func isRunningOnMacOS() bool {
	return isMacOS
}

func isRunningOnWindows() bool {
	return isWindows
}

type progressCallback interface {
	onNum(num int64)
	onName(name string)
	onSize(size int64)
	onStep(step int64)
	onDone()
}

type bufferSize struct {
	Size int64
}

type baseArgs struct {
	Quiet     bool       `arg:"-q" help:"quiet (hide progress bar)"`
	Overwrite bool       `arg:"-y" help:"yes, overwrite existing file(s)"`
	Binary    bool       `arg:"-b" help:"binary transfer mode, faster for binary files"`
	Escape    bool       `arg:"-e" help:"escape all known control characters"`
	Directory bool       `arg:"-d" help:"transfer directories and files"`
	Bufsize   bufferSize `arg:"-B" placeholder:"N" default:"10M" help:"max buffer chunk size (1K<=N<=1G). (default: 10M)"`
	Timeout   int        `arg:"-t" placeholder:"N" default:"20" help:"timeout ( N seconds ) for each buffer chunk.\nN <= 0 means never timeout. (default: 20)"`
}

var sizeRegexp = regexp.MustCompile(`(?i)^(\d+)(b|k|m|g|kb|mb|gb)?$`)

func (b *bufferSize) UnmarshalText(buf []byte) error {
	str := string(buf)
	match := sizeRegexp.FindStringSubmatch(str)
	if len(match) < 2 {
		return fmt.Errorf("invalid size %s", str)
	}
	sizeValue, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return fmt.Errorf("invalid size %s", str)
	}
	if len(match) > 2 {
		unitSuffix := strings.ToLower(match[2])
		if len(unitSuffix) == 0 || unitSuffix == "b" {
			// sizeValue *= 1
		} else if unitSuffix == "k" || unitSuffix == "kb" {
			sizeValue *= 1024
		} else if unitSuffix == "m" || unitSuffix == "mb" {
			sizeValue *= 1024 * 1024
		} else if unitSuffix == "g" || unitSuffix == "gb" {
			sizeValue *= 1024 * 1024 * 1024
		} else {
			return fmt.Errorf("invalid size %s", str)
		}
	}
	if sizeValue < 1024 {
		return fmt.Errorf("less than 1K")
	}
	if sizeValue > 1024*1024*1024 {
		return fmt.Errorf("greater than 1G")
	}
	b.Size = sizeValue
	return nil
}

func encodeBytes(buf []byte) string {
	b := bytes.NewBuffer(make([]byte, 0, len(buf)+0x10))
	z := zlib.NewWriter(b)
	_ = writeAll(z, []byte(buf))
	z.Close()
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

func encodeString(str string) string {
	return encodeBytes([]byte(str))
}

func decodeString(str string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(str)
	if err != nil {
		return nil, err
	}
	z, err := zlib.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer z.Close()
	buf := bytes.NewBuffer(make([]byte, 0, len(b)<<2))
	if _, err := io.Copy(buf, z); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type trzszError struct {
	message string
	errType string
	trace   bool
}

func newTrzszError(message string, errType string, trace bool) *trzszError {
	if errType == "fail" || errType == "FAIL" || errType == "EXIT" {
		msg, err := decodeString(message)
		if err != nil {
			message = fmt.Sprintf("decode [%s] error: %s", message, err)
		} else {
			message = string(msg)
		}
	} else if len(errType) > 0 {
		message = fmt.Sprintf("[TrzszError] %s: %s", errType, message)
	}
	err := &trzszError{message, errType, trace}
	if err.isTraceBack() {
		err.message = fmt.Sprintf("%s\n%s", err.message, string(debug.Stack()))
	}
	return err
}

func newSimpleTrzszError(message string) *trzszError {
	return newTrzszError(message, "", false)
}

func (e *trzszError) Error() string {
	return e.message
}

func (e *trzszError) isTraceBack() bool {
	if e.errType == "fail" || e.errType == "EXIT" {
		return false
	}
	return e.trace
}

func (e *trzszError) isRemoteExit() bool {
	return e.errType == "EXIT"
}

func (e *trzszError) isRemoteFail() bool {
	return e.errType == "fail" || e.errType == "FAIL"
}

func checkPathWritable(path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return newSimpleTrzszError(fmt.Sprintf("No such directory: %s", path))
	} else if err != nil {
		return err
	}
	if !info.IsDir() {
		return newSimpleTrzszError(fmt.Sprintf("Not a directory: %s", path))
	}
	if syscallAccessWok(path) != nil {
		return newSimpleTrzszError(fmt.Sprintf("No permission to write: %s", path))
	}
	return nil
}

type trzszFile struct {
	PathID  int      `json:"path_id"`
	AbsPath string   `json:"-"`
	RelPath []string `json:"path_name"`
	IsDir   bool     `json:"is_dir"`
}

func checkPathReadable(pathID int, path string, info os.FileInfo, list *[]*trzszFile, relPath []string, visitedDir map[string]bool) error {
	if !info.IsDir() {
		if !info.Mode().IsRegular() {
			return newSimpleTrzszError(fmt.Sprintf("Not a regular file: %s", path))
		}
		if syscallAccessRok(path) != nil {
			return newSimpleTrzszError(fmt.Sprintf("No permission to read: %s", path))
		}
		*list = append(*list, &trzszFile{pathID, path, relPath, false})
		return nil
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	if _, ok := visitedDir[realPath]; ok {
		return newSimpleTrzszError(fmt.Sprintf("Duplicate link: %s", path))
	}
	visitedDir[realPath] = true
	*list = append(*list, &trzszFile{pathID, path, relPath, true})
	f, err := os.Open(path)
	if err != nil {
		return newSimpleTrzszError(fmt.Sprintf("Open [%s] error: %v", path, err))
	}
	files, err := f.Readdir(-1)
	if err != nil {
		return newSimpleTrzszError(fmt.Sprintf("Readdir [%s] error: %v", path, err))
	}
	for _, file := range files {
		p := filepath.Join(path, file.Name())
		info, err := os.Stat(p)
		if err != nil {
			return err
		}
		r := make([]string, len(relPath))
		copy(r, relPath)
		r = append(r, file.Name())
		if err := checkPathReadable(pathID, p, info, list, r, visitedDir); err != nil {
			return err
		}
	}
	return nil
}

func checkPathsReadable(paths []string, directory bool) ([]*trzszFile, error) {
	var list []*trzszFile
	for i, p := range paths {
		path, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil, newSimpleTrzszError(fmt.Sprintf("No such file: %s", path))
		} else if err != nil {
			return nil, err
		}
		if !directory && info.IsDir() {
			return nil, newSimpleTrzszError(fmt.Sprintf("Is a directory: %s", path))
		}
		visitedDir := make(map[string]bool)
		if err := checkPathReadable(i, path, info, &list, []string{info.Name()}, visitedDir); err != nil {
			return nil, err
		}
	}
	return list, nil
}

func checkDuplicateNames(list []*trzszFile) error {
	m := make(map[string]bool)
	for _, f := range list {
		p := filepath.Join(f.RelPath...)
		if _, ok := m[p]; ok {
			return newSimpleTrzszError(fmt.Sprintf("Duplicate name: %s", p))
		}
		m[p] = true
	}
	return nil
}

func getNewName(path, name string) (string, error) {
	if _, err := os.Stat(filepath.Join(path, name)); errors.Is(err, os.ErrNotExist) {
		return name, nil
	}
	for i := 0; i < 1000; i++ {
		newName := fmt.Sprintf("%s.%d", name, i)
		if _, err := os.Stat(filepath.Join(path, newName)); errors.Is(err, os.ErrNotExist) {
			return newName, nil
		}
	}
	return "", newSimpleTrzszError("Fail to assign new file name")
}

type tmuxModeType int

const (
	noTmuxMode = iota
	tmuxNormalMode
	tmuxControlMode
)

func checkTmux() (tmuxModeType, *os.File, int, error) {
	if _, tmux := os.LookupEnv("TMUX"); !tmux {
		return noTmuxMode, os.Stdout, -1, nil
	}

	cmd := exec.Command("tmux", "display-message", "-p", "#{client_tty}:#{client_control_mode}:#{pane_width}")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		return 0, nil, -1, err
	}

	output := strings.TrimSpace(string(out))
	tokens := strings.Split(output, ":")
	if len(tokens) != 3 {
		return 0, nil, -1, fmt.Errorf("tmux unexpect output: %s", output)
	}
	tmuxTty, controlMode, paneWidth := tokens[0], tokens[1], tokens[2]

	if controlMode == "1" || tmuxTty[0] != '/' {
		return tmuxControlMode, os.Stdout, -1, nil
	}
	if _, err := os.Stat(tmuxTty); errors.Is(err, os.ErrNotExist) {
		return tmuxControlMode, os.Stdout, -1, nil
	}

	tmuxStdout, err := os.OpenFile(tmuxTty, os.O_WRONLY, 0)
	if err != nil {
		return 0, nil, -1, err
	}
	tmuxPaneWidth := -1
	if len(paneWidth) > 0 {
		tmuxPaneWidth, err = strconv.Atoi(paneWidth)
		if err != nil {
			return 0, nil, -1, err
		}
	}
	return tmuxNormalMode, tmuxStdout, tmuxPaneWidth, nil
}

func getTerminalColumns() int {
	cmd := exec.Command("stty", "size")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	output := strings.TrimSpace(string(out))
	tokens := strings.Split(output, " ")
	if len(tokens) != 2 {
		return 0
	}
	cols, _ := strconv.Atoi(tokens[1])
	return cols
}

func wrapStdinInput(transfer *trzszTransfer) {
	const bufSize = 32 * 1024
	buffer := make([]byte, bufSize)
	for {
		n, err := os.Stdin.Read(buffer)
		if n > 0 {
			buf := buffer[0:n]
			transfer.addReceivedData(buf)
			buffer = make([]byte, bufSize)
		}
		if err == io.EOF {
			transfer.stopTransferringFiles()
		}
	}
}

func handleServerSignal(transfer *trzszTransfer) {
	sigstop := make(chan os.Signal, 1)
	signal.Notify(sigstop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigstop
		transfer.stopTransferringFiles()
	}()
}

func isVT100End(b byte) bool {
	if 'a' <= b && b <= 'z' {
		return true
	}
	if 'A' <= b && b <= 'Z' {
		return true
	}
	return false
}

func trimVT100(buf []byte) []byte {
	b := new(bytes.Buffer)
	skipVT100 := false
	for _, c := range buf {
		if skipVT100 {
			if isVT100End(c) {
				skipVT100 = false
			}
		} else if c == '\x1b' {
			skipVT100 = true
		} else {
			b.WriteByte(c)
		}
	}
	return b.Bytes()
}

func containsString(elems []string, v string) bool {
	for _, s := range elems {
		if v == s {
			return true
		}
	}
	return false
}

func writeAll(dst io.Writer, data []byte) error {
	m := 0
	l := len(data)
	for m < l {
		n, err := dst.Write(data[m:])
		if err != nil {
			return newTrzszError(fmt.Sprintf("WriteAll error: %v", err), "", true)
		}
		m += n
	}
	return nil
}

type trzszDetector struct {
	uniqueIDMap map[string]int
}

func newTrzszDetector() *trzszDetector {
	return &trzszDetector{make(map[string]int)}
}

var trzszRegexp = regexp.MustCompile(`::TRZSZ:TRANSFER:([SRD]):(\d+\.\d+\.\d+)(:\d+)?`)

func (detector *trzszDetector) detectTrzsz(output []byte) (*byte, bool) {
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
	if len(uniqueID) >= 8 && (isRunningOnWindows() || !(len(uniqueID) == 14 && strings.HasSuffix(uniqueID, "00"))) {
		if _, ok := detector.uniqueIDMap[uniqueID]; ok {
			return nil, false
		}
		if len(detector.uniqueIDMap) > 100 {
			m := make(map[string]int)
			for k, v := range detector.uniqueIDMap {
				if v >= 50 {
					m[k] = v - 50
				}
			}
			detector.uniqueIDMap = m
		}
		detector.uniqueIDMap[uniqueID] = len(detector.uniqueIDMap)
	}
	remoteIsWindows := false
	if uniqueID == ":1" || (len(uniqueID) == 14 && strings.HasSuffix(uniqueID, "10")) {
		remoteIsWindows = true
	}
	return &match[1][0], remoteIsWindows
}

type traceLogger struct {
	traceLogFile atomic.Pointer[os.File]
	traceLogChan atomic.Pointer[chan []byte]
}

func newTraceLogger() *traceLogger {
	return &traceLogger{}
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
func (logger *traceLogger) writeTraceLog(buf []byte, typ string) []byte {
	if ch, file := logger.traceLogChan.Load(), logger.traceLogFile.Load(); ch != nil && file != nil {
		if typ == "svrout" && bytes.Contains(buf, []byte("<DISABLE_TRZSZ_TRACE_LOG>")) {
			msg := fmt.Sprintf("Closed trace log at %s", file.Name())
			close(*ch)
			logger.traceLogChan.Store(nil)
			logger.traceLogFile.Store(nil)
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
		logger.traceLogChan.Store(&ch)
		logger.traceLogFile.Store(file)
		go func() {
			for {
				select {
				case buf, ok := <-ch:
					if !ok {
						file.Close()
						return
					}
					_ = writeAll(file, buf)
				case <-time.After(3 * time.Second):
					_ = file.Sync()
				}
			}
		}()
		return bytes.ReplaceAll(buf, []byte("<ENABLE_TRZSZ_TRACE_LOG>"), []byte(msg))
	}
	return buf
}
