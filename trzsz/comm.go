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
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
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

	"github.com/klauspost/compress/zstd"
	"golang.org/x/term"
)

var onExitFuncs []func()

func cleanupOnExit() {
	for i := len(onExitFuncs) - 1; i >= 0; i-- {
		onExitFuncs[i]()
	}
}

var timeNowFunc = time.Now

var linuxRuntime bool = (runtime.GOOS == "linux")
var macosRuntime bool = (runtime.GOOS == "darwin")
var windowsRuntime bool = (runtime.GOOS == "windows")
var windowsEnvironment bool = (runtime.GOOS == "windows")

func isRunningOnLinux() bool {
	return linuxRuntime
}

func isRunningOnMacOS() bool {
	return macosRuntime
}

// isRunningOnWindows returns whether the runtime platform is Windows.
func isRunningOnWindows() bool {
	return windowsRuntime
}

// isWindowsEnvironment returns false if trzsz is not affected by Windows.
func isWindowsEnvironment() bool {
	return windowsEnvironment
}

// SetAffectedByWindows set whether trzsz is affected by Windows.
func SetAffectedByWindows(affected bool) {
	windowsEnvironment = affected
}

type progressCallback interface {
	onNum(num int64)
	onName(name string)
	onSize(size int64)
	onStep(step int64)
	onDone()
	setPreSize(size int64)
	setPause(pausing bool)
}

type bufferSize struct {
	Size int64
}

type compressType int

const (
	kCompressAuto = 0
	kCompressYes  = 1
	kCompressNo   = 2
)

type baseArgs struct {
	Quiet     bool         `arg:"-q" help:"quiet (hide progress bar)"`
	Overwrite bool         `arg:"-y" help:"yes, overwrite existing file(s)"`
	Binary    bool         `arg:"-b" help:"binary transfer mode, faster for binary files"`
	Escape    bool         `arg:"-e" help:"escape all known control characters"`
	Directory bool         `arg:"-d" help:"transfer directories and files"`
	Recursive bool         `arg:"-r" help:"transfer directories and files, same as -d"`
	Fork      bool         `arg:"-f" help:"fork to transfer in background (implies -q)"`
	Bufsize   bufferSize   `arg:"-B" placeholder:"N" default:"10M" help:"max buffer chunk size (1K<=N<=1G). (default: 10M)"`
	Timeout   int          `arg:"-t" placeholder:"N" default:"20" help:"timeout ( N seconds ) for each buffer chunk.\nN <= 0 means never timeout. (default: 20)"`
	Compress  compressType `arg:"-c" placeholder:"yes/no/auto" default:"auto" help:"compress type (default: auto)"`
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

func (c *compressType) UnmarshalText(buf []byte) error {
	str := strings.ToLower(strings.TrimSpace(string(buf)))
	switch str {
	case "auto":
		*c = kCompressAuto
		return nil
	case "yes":
		*c = kCompressYes
		return nil
	case "no":
		*c = kCompressNo
		return nil
	default:
		return fmt.Errorf("invalid compress type %s", str)
	}
}

func (c *compressType) UnmarshalJSON(data []byte) error {
	var compress int
	if err := json.Unmarshal(data, &compress); err != nil {
		return err
	}
	*c = compressType(compress)
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

var (
	errStopped            = simpleTrzszError("Stopped")
	errStoppedAndDeleted  = simpleTrzszError("Stopped and deleted")
	errReceiveDataTimeout = simpleTrzszError("Receive data timeout")
)

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

func simpleTrzszError(format string, a ...any) *trzszError {
	return newTrzszError(fmt.Sprintf(format, a...), "", false)
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

func (e *trzszError) isStopAndDelete() bool {
	if e == nil || e.errType != "fail" {
		return false
	}
	return e.message == errStoppedAndDeleted.message
}

func checkPathWritable(path string) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return simpleTrzszError("No such directory: %s", path)
	} else if err != nil {
		return err
	}
	if !info.IsDir() {
		return simpleTrzszError("Not a directory: %s", path)
	}
	if syscallAccessWok(path) != nil {
		return simpleTrzszError("No permission to write: %s", path)
	}
	return nil
}

type sourceFile struct {
	PathID   int           `json:"path_id"`
	AbsPath  string        `json:"-"`
	RelPath  []string      `json:"path_name"`
	IsDir    bool          `json:"is_dir"`
	Archive  bool          `json:"archive"`
	Size     int64         `json:"size"`
	Perm     *uint32       `json:"perm"`
	Header   string        `json:"-"`
	SubFiles []*sourceFile `json:"-"`
}

func (f *sourceFile) getFileName() string {
	if len(f.RelPath) == 0 {
		return ""
	}
	return f.RelPath[len(f.RelPath)-1]
}

func (f *sourceFile) marshalSourceFile() (string, error) {
	f.Archive = len(f.SubFiles) > 0
	jstr, err := json.Marshal(f)
	if err != nil {
		return "", err
	}
	return string(jstr), nil
}

func unmarshalSourceFile(source string) (*sourceFile, error) {
	var file sourceFile
	if err := json.Unmarshal([]byte(source), &file); err != nil {
		return nil, err
	}
	if len(file.RelPath) < 1 {
		return nil, simpleTrzszError("Invalid source file: %s", source)
	}
	return &file, nil
}

type targetFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

func (f *targetFile) marshalTargetFile() (string, error) {
	jstr, err := json.Marshal(f)
	if err != nil {
		return "", err
	}
	return string(jstr), nil
}

func unmarshalTargetFile(target string) (*targetFile, error) {
	var file targetFile
	if err := json.Unmarshal([]byte(target), &file); err != nil {
		return nil, err
	}
	if file.Size < 0 {
		return nil, simpleTrzszError("Invalid target file: %s", target)
	}
	return &file, nil
}

func checkPathReadable(pathID int, path string, info os.FileInfo, list *[]*sourceFile,
	relPath []string, visitedDir map[string]bool) error {
	perm := uint32(info.Mode().Perm())
	if !info.IsDir() {
		if !info.Mode().IsRegular() {
			return simpleTrzszError("Not a regular file: %s", path)
		}
		if syscallAccessRok(path) != nil {
			return simpleTrzszError("No permission to read: %s", path)
		}
		*list = append(*list, &sourceFile{PathID: pathID, AbsPath: path, RelPath: relPath, Size: info.Size(), Perm: &perm})
		return nil
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	if _, ok := visitedDir[realPath]; ok {
		return simpleTrzszError("Duplicate link: %s", path)
	}
	visitedDir[realPath] = true
	*list = append(*list, &sourceFile{PathID: pathID, AbsPath: path, RelPath: relPath, IsDir: true, Perm: &perm})
	fileObj, err := os.Open(path)
	if err != nil {
		return simpleTrzszError("Open [%s] error: %v", path, err)
	}
	files, err := fileObj.Readdir(-1)
	if err != nil {
		return simpleTrzszError("Readdir [%s] error: %v", path, err)
	}
	for _, file := range files {
		p := filepath.Join(path, file.Name())
		info, err := os.Stat(p)
		if err != nil {
			return simpleTrzszError("Stat [%s] error: %v", p, err)
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

func checkPathsReadable(paths []string, directory bool) ([]*sourceFile, error) {
	var list []*sourceFile
	for i, p := range paths {
		path, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			return nil, simpleTrzszError("No such file: %s", path)
		} else if err != nil {
			return nil, err
		}
		if !directory && info.IsDir() {
			return nil, simpleTrzszError("Is a directory: %s", path)
		}
		visitedDir := make(map[string]bool)
		if err := checkPathReadable(i, path, info, &list, []string{info.Name()}, visitedDir); err != nil {
			return nil, err
		}
	}
	return list, nil
}

func checkDuplicateNames(sourceFiles []*sourceFile) error {
	m := make(map[string]bool)
	for _, srcFile := range sourceFiles {
		p := filepath.Join(srcFile.RelPath...)
		if _, ok := m[p]; ok {
			return simpleTrzszError("Duplicate name: %s", p)
		}
		m[p] = true
	}
	return nil
}

func getNewName(path, name string) (string, error) {
	const maxNameLen = 255
	if len(name) > maxNameLen {
		return "", simpleTrzszError("File name too long: %s", name)
	}

	if _, err := os.Stat(filepath.Join(path, name)); os.IsNotExist(err) {
		return name, nil
	}
	for i := 0; i < 1000; i++ {
		newName := fmt.Sprintf("%s.%d", name, i)
		if _, err := os.Stat(filepath.Join(path, newName)); os.IsNotExist(err) {
			return newName, nil
		}
	}
	return "", simpleTrzszError("Fail to assign new file name to %s", name)
}

type tmuxModeType int

const (
	noTmuxMode = iota
	tmuxNormalMode
	tmuxControlMode
)

func checkTmux() (tmuxModeType, *os.File, int32, error) {
	if _, tmux := os.LookupEnv("TMUX"); !tmux {
		return noTmuxMode, os.Stdout, -1, nil
	}

	cmd := exec.Command("tmux", "display-message", "-p", "#{client_tty}:#{client_control_mode}:#{pane_width}")
	out, err := cmd.Output()
	if err != nil {
		return noTmuxMode, nil, -1, fmt.Errorf("Get tmux output failed: %v", err)
	}

	output := strings.TrimSpace(string(out))
	tokens := strings.Split(output, ":")
	if len(tokens) != 3 {
		return noTmuxMode, nil, -1, fmt.Errorf("Unexpect tmux output: %s", output)
	}
	tmuxTty, controlMode, paneWidth := tokens[0], tokens[1], tokens[2]

	tmuxPaneWidth := -1
	if len(paneWidth) > 0 {
		tmuxPaneWidth, err = strconv.Atoi(paneWidth)
		if err != nil {
			return 0, nil, -1, fmt.Errorf("Parse tmux pane width [%s] failed: %v", paneWidth, err)
		}
	}

	if controlMode == "1" || tmuxTty[0] != '/' {
		return tmuxControlMode, os.Stdout, int32(tmuxPaneWidth), nil
	}
	if _, err := os.Stat(tmuxTty); os.IsNotExist(err) {
		return tmuxControlMode, os.Stdout, int32(tmuxPaneWidth), nil
	}

	tmuxStdout, err := os.OpenFile(tmuxTty, os.O_WRONLY, 0)
	if err != nil {
		return 0, nil, -1, fmt.Errorf("Open tmux tty [%s] failed: %v", tmuxTty, err)
	}

	statusInterval := getTmuxStatusInterval()
	setTmuxStatusInterval("0")
	onExitFuncs = append(onExitFuncs, func() {
		setTmuxStatusInterval(statusInterval)
	})

	return tmuxNormalMode, tmuxStdout, int32(tmuxPaneWidth), nil
}

func tmuxRefreshClient() {
	cmd := exec.Command("tmux", "refresh-client")
	cmd.Stdout = os.Stdout
	_ = cmd.Run()
}

func getTmuxStatusInterval() string {
	cmd := exec.Command("tmux", "display-message", "-p", "#{status-interval}")
	out, err := cmd.Output()
	output := strings.TrimSpace(string(out))
	if err != nil || output == "" {
		return "15" // The default is 15 seconds
	}
	return output
}

func setTmuxStatusInterval(interval string) {
	if interval == "" {
		interval = "15" // The default is 15 seconds
	}
	cmd := exec.Command("tmux", "setw", "status-interval", interval)
	_ = cmd.Run()
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

func wrapTransferInput(transfer *trzszTransfer, reader io.Reader, tunnel bool) {
	go func() {
		const bufSize = 32 * 1024
		buffer := make([]byte, bufSize)
		for {
			n, err := reader.Read(buffer)
			if n > 0 {
				buf := buffer[0:n]
				transfer.addReceivedData(buf, tunnel)
				buffer = make([]byte, bufSize)
			}
			if err != nil {
				break
			}
		}
	}()
}

func handleServerSignal(transfer *trzszTransfer) {
	sigstop := make(chan os.Signal, 1)
	signal.Notify(sigstop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigstop
		transfer.stopTransferringFiles(false)
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

func hideCursor(writer io.Writer) {
	_ = writeAll(writer, []byte("\x1b[?25l"))
}

func showCursor(writer io.Writer) {
	_ = writeAll(writer, []byte("\x1b[?25h"))
}

type trzszVersion [3]uint32

func parseTrzszVersion(ver string) (*trzszVersion, error) {
	tokens := strings.Split(ver, ".")
	if len(tokens) != 3 {
		return nil, simpleTrzszError("Version [%s] invalid", ver)
	}
	var version trzszVersion
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseUint(tokens[i], 10, 32)
		if err != nil {
			return nil, simpleTrzszError("Version [%s] invalid", ver)
		}
		version[i] = uint32(v)
	}
	return &version, nil
}

func (v *trzszVersion) compare(ver *trzszVersion) int {
	for i := 0; i < 3; i++ {
		if v[i] < ver[i] {
			return -1
		}
		if v[i] > ver[i] {
			return 1
		}
	}
	return 0
}

type trzszTrigger struct {
	mode       byte
	version    *trzszVersion
	uniqueID   string
	winServer  bool
	tunnelPort int
	tmuxPrefix string
}

type trzszDetector struct {
	relay       bool
	tmux        bool
	uniqueIDMap map[string]int
}

func newTrzszDetector(relay, tmux bool) *trzszDetector {
	return &trzszDetector{relay, tmux, make(map[string]int)}
}

var trzszRegexp = regexp.MustCompile(`::TRZSZ:TRANSFER:([SRD]):(\d+\.\d+\.\d+)(:\d+)?(:\d+)?`)
var uniqueIDRegexp = regexp.MustCompile(`::TRZSZ:TRANSFER:[SRD]:\d+\.\d+\.\d+:(\d{13}\d*)`)
var tmuxControlModeRegexp = regexp.MustCompile(`((%output %\d+ )|(%extended-output %\d+ \d+ : )).*::TRZSZ:TRANSFER:`)

func (detector *trzszDetector) rewriteTrzszTrigger(buf []byte) []byte {
	for _, match := range uniqueIDRegexp.FindAllSubmatch(buf, -1) {
		if len(match) == 2 {
			uniqueID := match[1]
			if len(uniqueID) >= 13 && bytes.HasSuffix(uniqueID, []byte("00")) {
				newUniqueID := make([]byte, len(uniqueID))
				copy(newUniqueID, uniqueID)
				newUniqueID[len(uniqueID)-2] = '2'
				buf = bytes.ReplaceAll(buf, uniqueID, newUniqueID)
			}
		}
	}
	return buf
}

func (detector *trzszDetector) addRelaySuffix(output []byte, idx int) []byte {
	idx += 20
	if idx >= len(output) {
		return output
	}
	for ; idx < len(output); idx++ {
		c := output[idx]
		if c != ':' && c != '.' && !(c >= '0' && c <= '9') {
			break
		}
	}
	buf := bytes.NewBuffer(make([]byte, 0, len(output)+2))
	buf.Write(output[:idx])
	buf.Write([]byte("#R"))
	buf.Write(output[idx:])
	return buf.Bytes()
}

func (detector *trzszDetector) isRepeatedID(uniqueID string) bool {
	if len(uniqueID) > 6 && (isWindowsEnvironment() || !(len(uniqueID) == 13 && strings.HasSuffix(uniqueID, "00"))) {
		if _, ok := detector.uniqueIDMap[uniqueID]; ok {
			return true
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
	return false
}

func (detector *trzszDetector) detectTrzsz(output []byte, tunnel bool) ([]byte, *trzszTrigger) {
	if len(output) < 24 {
		return output, nil
	}
	idx := bytes.LastIndex(output, []byte("::TRZSZ:TRANSFER:"))
	if idx < 0 {
		return output, nil
	}

	if detector.relay && detector.tmux {
		output = detector.rewriteTrzszTrigger(output)
		idx = bytes.LastIndex(output, []byte("::TRZSZ:TRANSFER:"))
	}

	subOutput := output[idx:]
	match := trzszRegexp.FindSubmatch(subOutput)
	if len(match) < 3 {
		return output, nil
	}

	tmuxPrefix := ""
	tmuxMatch := tmuxControlModeRegexp.FindSubmatch(output)
	if len(tmuxMatch) > 1 {
		if !tunnel || len(match) < 5 || match[4] == nil {
			return output, nil
		}
		tmuxPrefix = string(tmuxMatch[1])
	}

	if len(subOutput) > 40 {
		for _, s := range []string{"#CFG:", "Saved", "Cancelled", "Stopped", "Interrupted"} {
			if bytes.Contains(subOutput[40:], []byte(s)) {
				return output, nil
			}
		}
	}

	mode := match[1][0]

	version, err := parseTrzszVersion(string(match[2]))
	if err != nil {
		return output, nil
	}

	uniqueID := ""
	if len(match) > 3 && match[3] != nil {
		uniqueID = string(match[3][1:])
	}
	if detector.isRepeatedID(uniqueID) {
		return output, nil
	}

	winServer := false
	if uniqueID == "1" || (len(uniqueID) == 13 && strings.HasSuffix(uniqueID, "10")) {
		winServer = true
	}

	port := 0
	if len(match) > 4 && match[4] != nil {
		if v, err := strconv.Atoi(string(match[4][1:])); err == nil {
			port = v
		}
	}

	if detector.relay {
		output = detector.addRelaySuffix(output, idx)
	} else {
		output = bytes.ReplaceAll(output, []byte("TRZSZ"), []byte("TRZSZGO"))
	}

	return output, &trzszTrigger{
		mode:       mode,
		version:    version,
		uniqueID:   uniqueID,
		winServer:  winServer,
		tunnelPort: port,
		tmuxPrefix: tmuxPrefix,
	}
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

const compressedBlockSize = 128 << 10

func isCompressedFileContent(file *os.File, pos int64) (bool, error) {
	if _, err := file.Seek(pos, io.SeekStart); err != nil {
		return false, err
	}

	buffer := make([]byte, compressedBlockSize)
	_, err := io.ReadFull(file, buffer)
	if err != nil {
		return false, err
	}

	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		return false, err
	}
	dst := make([]byte, 0, compressedBlockSize+0x20)
	return len(encoder.EncodeAll(buffer, dst)) > compressedBlockSize*98/100, nil
}

func isCompressionProfitable(reader fileReader) (bool, error) {
	file := reader.getFile()
	if file == nil {
		return true, nil
	}
	size := reader.getSize()

	pos, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return false, err
	}

	compressedCount := 0
	if size >= compressedBlockSize {
		compressed, err := isCompressedFileContent(file, pos)
		if err != nil {
			return false, err
		}
		if compressed {
			compressedCount++
		}
	}

	if size >= 2*compressedBlockSize {
		compressed, err := isCompressedFileContent(file, pos+size-compressedBlockSize)
		if err != nil {
			return false, err
		}
		if compressed {
			compressedCount++
		}
	}

	if size >= 3*compressedBlockSize {
		compressed, err := isCompressedFileContent(file, pos+(size/2)-(compressedBlockSize/2))
		if err != nil {
			return false, err
		}
		if compressed {
			compressedCount++
		}
	}

	if _, err := file.Seek(pos, io.SeekStart); err != nil {
		return false, err
	}

	return compressedCount < 2, nil
}

func formatSavedFiles(fileNames []string, destPath string) string {
	var builder strings.Builder
	builder.WriteString("Saved ")
	builder.WriteString(strconv.Itoa(len(fileNames)))
	if len(fileNames) > 1 {
		builder.WriteString(" files/directories")
	} else {
		builder.WriteString(" file/directory")
	}
	if len(destPath) > 0 {
		builder.WriteString(" to ")
		builder.WriteString(destPath)
	}
	for _, name := range fileNames {
		builder.WriteString("\r\n- ")
		builder.WriteString(name)
	}
	return builder.String()
}

func joinFileNames(header string, fileNames []string) string {
	var builder strings.Builder
	builder.WriteString(header)
	for _, name := range fileNames {
		builder.WriteString("\r\n- ")
		builder.WriteString(name)
	}
	return builder.String()
}

func resolveHomeDir(path string) string {
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}

func listenForTunnel() (net.Listener, int) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, 0
	}
	return listener, listener.Addr().(*net.TCPAddr).Port
}

func encodeTmuxOutput(prefix string, output []byte) []byte {
	buffer := bytes.NewBuffer(make([]byte, 0, len(prefix)+len(output)<<2+2))
	buffer.Write([]byte(prefix))
	for _, b := range output {
		if b >= '0' && b <= '9' || b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' {
			buffer.WriteByte(b)
			continue
		}
		buffer.Write([]byte(fmt.Sprintf("\\%03o", b)))
	}
	buffer.Write([]byte("\r\n"))
	return buffer.Bytes()
}

type promptWriter struct {
	prefix string
	writer io.Writer
}

func (w *promptWriter) Write(p []byte) (int, error) {
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

func forkToBackground() (bool, error) {
	if v := os.Getenv("TRZSZ-FORK-BACKGROUND"); v == "TRUE" {
		return false, nil
	}

	cmd := exec.Command(os.Args[0], os.Args[1:]...)
	cmd.Env = append(os.Environ(), "TRZSZ-FORK-BACKGROUND=TRUE")
	cmd.SysProcAttr = getSysProcAttr()
	cmd.Stdout = os.Stdout
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return true, fmt.Errorf("fork stdin pipe failed: %v", err)
	}
	defer stdin.Close()
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return true, fmt.Errorf("fork stderr pipe failed: %v", err)
	}
	defer stderr.Close()
	if err := cmd.Start(); err != nil {
		return true, fmt.Errorf("fork start failed: %v", err)
	}

	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		state, err := term.MakeRaw(fd)
		if err != nil {
			return true, fmt.Errorf("make stdin raw failed: %v\r\n", err)
		}
		defer func() { _ = term.Restore(fd, state) }()
	}
	go func() {
		_, _ = io.Copy(stdin, os.Stdin)
	}()

	_, _ = io.Copy(os.Stderr, stderr)
	return true, nil
}
