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
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/term"
)

const (
	kProtocolVersion2 = 2
	kProtocolVersion3 = 3
	kProtocolVersion4 = 4
	kProtocolVersion  = kProtocolVersion4

	kLastChunkTimeCount = 10
)

type transferAction struct {
	Lang             string `json:"lang"`
	Version          string `json:"version"`
	Confirm          bool   `json:"confirm"`
	Newline          string `json:"newline"`
	Protocol         int    `json:"protocol"`
	SupportBinary    bool   `json:"binary"`
	SupportDirectory bool   `json:"support_dir"`
	TunnelConnected  bool   `json:"tunnel"`
	SupportFork      bool   `json:"fork"`
}

type transferConfig struct {
	Quiet           bool         `json:"quiet"`
	Binary          bool         `json:"binary"`
	Directory       bool         `json:"directory"`
	Overwrite       bool         `json:"overwrite"`
	Timeout         int          `json:"timeout"`
	Newline         string       `json:"newline"`
	Protocol        int          `json:"protocol"`
	MaxBufSize      int64        `json:"bufsize"`
	EscapeTable     *escapeTable `json:"escape_chars"`
	TmuxPaneColumns int32        `json:"tmux_pane_width"`
	TmuxOutputJunk  bool         `json:"tmux_output_junk"`
	CompressType    compressType `json:"compress"`
	Fork            bool         `json:"fork"`
}

type trzszTransfer struct {
	buffer           *trzszBuffer
	writer           io.Writer
	stopped          atomic.Bool
	stopAndDelete    atomic.Bool
	pausing          atomic.Bool
	pauseIdx         atomic.Uint32
	pauseBeginTime   atomic.Int64
	resumeBeginTime  atomic.Pointer[time.Time]
	lastInputTime    atomic.Int64
	cleanTimeout     time.Duration
	lastChunkTimeArr [kLastChunkTimeCount]time.Duration
	lastChunkTimeIdx int
	stdinState       *term.State
	fileNameMap      map[int]string
	windowsProtocol  bool
	flushInTime      bool
	bufInitWG        sync.WaitGroup
	bufInitPhase     atomic.Bool
	bufferSize       atomic.Int64
	savedSteps       atomic.Int64
	transferConfig   transferConfig
	logger           *traceLogger
	createdFiles     []string
	tunnelConnected  bool
	tunnelConn       atomic.Pointer[net.Conn]
	tunnelInitWG     sync.WaitGroup
	bgChan           chan struct{}
	termReseted      atomic.Bool
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func newTransfer(writer io.Writer, stdinState *term.State, flushInTime bool, logger *traceLogger) *trzszTransfer {
	t := &trzszTransfer{
		buffer:       newTrzszBuffer(),
		writer:       writer,
		cleanTimeout: 100 * time.Millisecond,
		stdinState:   stdinState,
		fileNameMap:  make(map[int]string),
		flushInTime:  flushInTime,
		transferConfig: transferConfig{
			Timeout:    20,
			Newline:    "\n",
			MaxBufSize: 10 * 1024 * 1024,
		},
		logger: logger,
		bgChan: make(chan struct{}, 1),
	}
	t.bufInitPhase.Store(true)
	t.bufferSize.Store(10240)
	return t
}

func getHelloConstant(uniqueID string, port int) (string, string) {
	uid := uniqueID
	if len(uid) > 2 {
		uid = uid[:len(uid)-2]
	}
	clientHello := fmt.Sprintf("::TRZSZ::CLIENT::HELLO::%s:%d", uid, port)
	serverHello := fmt.Sprintf("::TRZSZ::SERVER::HELLO::%s:%d", uid, port)
	return clientHello, serverHello
}

func (t *trzszTransfer) acceptOnTunnel(listener net.Listener, uniqueID string, port int) {
	go func() {
		defer listener.Close()
		clientHello, serverHello := getHelloConstant(uniqueID, port)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			if t.tunnelConn.Load() != nil {
				conn.Close()
				return
			}
			go func(conn net.Conn) {
				buf := make([]byte, 100)
				n, err := conn.Read(buf)
				if err != nil || string(buf[:n]) != clientHello {
					conn.Close()
					return
				}
				if _, err := conn.Write([]byte(serverHello)); err != nil {
					conn.Close()
					return
				}
				if t.tunnelConn.CompareAndSwap(nil, &conn) {
					wrapTransferInput(t, conn, true)
					listener.Close()
				}
			}(conn)
		}
	}()
}

func (t *trzszTransfer) connectToTunnel(connector func(int) net.Conn, uniqueID string, port int) {
	t.tunnelInitWG.Add(1)
	go func() {
		defer t.tunnelInitWG.Done()

		timeout := false
		connChan := make(chan net.Conn, 1)
		go func() {
			defer close(connChan)
			conn := connector(port)
			if conn == nil {
				connChan <- nil
				return
			}
			if timeout {
				conn.Close()
				connChan <- nil
				return
			}
			clientHello, serverHello := getHelloConstant(uniqueID, port)
			if _, err := conn.Write([]byte(clientHello)); err != nil || timeout {
				conn.Close()
				connChan <- nil
				return
			}
			buf := make([]byte, 100)
			n, err := conn.Read(buf)
			if err != nil || string(buf[:n]) != serverHello || timeout {
				conn.Close()
				connChan <- nil
				return
			}
			connChan <- conn
		}()

		select {
		case conn := <-connChan:
			if conn != nil {
				t.tunnelConn.Store(&conn)
				wrapTransferInput(t, conn, true)
			}
		case <-time.After(time.Second):
			timeout = true
		}
	}()
}

func (t *trzszTransfer) cleanup() {
	if conn := t.tunnelConn.Load(); conn != nil {
		(*conn).Close()
	}
}

func (t *trzszTransfer) background() <-chan struct{} {
	return t.bgChan
}

func (t *trzszTransfer) switchToBackground() {
	os.Stdin.Close()
	go func() {
		time.Sleep(500 * time.Millisecond) // wait for client switch to background
		t.resetTerm("Switch to transfer in background.", true)
		os.Stderr.Close()
	}()
}

func (t *trzszTransfer) addReceivedData(buf []byte, tunnel bool) {
	if t.tunnelConnected && !tunnel {
		if t.logger != nil {
			t.logger.writeTraceLog(buf, "ignout")
		}
		return
	}
	if t.logger != nil {
		t.logger.writeTraceLog(buf, "rcvbuf")
	}
	if !t.stopped.Load() {
		t.buffer.addBuffer(buf)
	}
	t.lastInputTime.Store(time.Now().UnixMilli())
}

func (t *trzszTransfer) stopTransferringFiles(stopAndDelete bool) {
	if !t.stopped.CompareAndSwap(false, true) {
		return
	}
	t.stopAndDelete.Store(stopAndDelete)
	t.buffer.stopBuffer()

	if !t.tunnelConnected {
		maxChunkTime := time.Duration(0)
		for _, chunkTime := range t.lastChunkTimeArr {
			if chunkTime > maxChunkTime {
				maxChunkTime = chunkTime
			}
		}
		waitTime := maxChunkTime * 2
		beginTime := t.pauseBeginTime.Load()
		if beginTime > 0 {
			waitTime -= time.Since(time.UnixMilli(beginTime))
		}
		t.cleanTimeout = maxDuration(waitTime, 500*time.Millisecond)
	}
}

func (t *trzszTransfer) pauseTransferringFiles() {
	t.pausing.Store(true)
	if t.pauseBeginTime.Load() == 0 {
		t.pauseIdx.Add(1)
		t.pauseBeginTime.CompareAndSwap(0, time.Now().UnixMilli())
	}
}

func (t *trzszTransfer) resumeTransferringFiles() {
	now := timeNowFunc()
	t.resumeBeginTime.Store(&now)
	t.pauseBeginTime.Store(0)
	t.buffer.setNewTimeout(t.getNewTimeout())
	t.pausing.Store(false)
}

func (t *trzszTransfer) checkStop() error {
	if t.stopAndDelete.Load() {
		return errStoppedAndDeleted
	}
	if t.stopped.Load() {
		return errStopped
	}
	return nil
}

func (t *trzszTransfer) setLastChunkTime(chunkTime time.Duration) {
	t.lastChunkTimeArr[t.lastChunkTimeIdx] = chunkTime
	t.lastChunkTimeIdx++
	if t.lastChunkTimeIdx >= kLastChunkTimeCount {
		t.lastChunkTimeIdx = 0
	}
}

func (t *trzszTransfer) cleanInput(timeoutDuration time.Duration) {
	t.stopped.Store(true)
	t.buffer.drainBuffer()
	t.lastInputTime.Store(time.Now().UnixMilli())
	for {
		sleepDuration := timeoutDuration - time.Since(time.UnixMilli(t.lastInputTime.Load()))
		if sleepDuration <= 0 {
			return
		}
		time.Sleep(sleepDuration)
	}
}

func (t *trzszTransfer) writeAll(buf []byte) error {
	if t.logger != nil {
		t.logger.writeTraceLog(buf, "tosvr")
	}
	return writeAll(t.writer, buf)
}

func (t *trzszTransfer) sendLine(typ string, buf string) error {
	return t.writeAll([]byte(fmt.Sprintf("#%s:%s%s", typ, buf, t.transferConfig.Newline)))
}

func (t *trzszTransfer) stripTmuxStatusLine(buf []byte) []byte {
	for {
		beginIdx := bytes.Index(buf, []byte("\x1bP="))
		if beginIdx < 0 {
			return buf
		}
		bufIdx := beginIdx + 3
		midIdx := bytes.Index(buf[bufIdx:], []byte("\x1bP="))
		if midIdx < 0 {
			return buf[:beginIdx]
		}
		bufIdx += midIdx + 3
		endIdx := bytes.Index(buf[bufIdx:], []byte("\x1b\\"))
		if endIdx < 0 {
			return buf[:beginIdx]
		}
		bufIdx += endIdx + 2
		b := bytes.NewBuffer(make([]byte, 0, len(buf)-(bufIdx-beginIdx)))
		b.Write(buf[:beginIdx])
		b.Write(buf[bufIdx:])
		buf = b.Bytes()
	}
}

func (t *trzszTransfer) recvLine(expectType string, mayHasJunk bool, timeout <-chan time.Time) ([]byte, error) {
	if err := t.checkStop(); err != nil {
		return nil, err
	}

	if !t.tunnelConnected && (isWindowsEnvironment() || t.windowsProtocol) {
		line, err := t.buffer.readLineOnWindows(timeout)
		if err != nil {
			if e := t.checkStop(); e != nil {
				return nil, e
			}
			return nil, err
		}
		idx := bytes.LastIndex(line, []byte("#"+expectType+":"))
		if idx >= 0 {
			line = line[idx:]
		} else {
			idx = bytes.LastIndexByte(line, '#')
			if idx > 0 {
				line = line[idx:]
			}
		}
		return line, nil
	}

	if t.tunnelConnected {
		mayHasJunk = false
	} else if t.transferConfig.TmuxOutputJunk {
		mayHasJunk = true
	}

	line, err := t.buffer.readLine(mayHasJunk, timeout)
	if err != nil {
		if e := t.checkStop(); e != nil {
			return nil, e
		}
		return nil, err
	}

	if mayHasJunk {
		idx := bytes.LastIndex(line, []byte("#"+expectType+":"))
		if idx >= 0 {
			line = line[idx:]
		} else {
			idx = bytes.LastIndexByte(line, '#')
			if idx > 0 {
				line = line[idx:]
			}
		}
		line = t.stripTmuxStatusLine(line)
	}

	return line, nil
}

func (t *trzszTransfer) recvCheck(expectType string, mayHasJunk bool, timeout <-chan time.Time) (string, error) {
	line, err := t.recvLine(expectType, mayHasJunk, timeout)
	if err != nil {
		return "", err
	}

	idx := bytes.IndexByte(line, ':')
	if idx < 1 {
		return "", newTrzszError(encodeBytes(line), "colon", true)
	}

	typ := string(line[1:idx])
	buf := string(line[idx+1:])
	if typ != expectType {
		return "", newTrzszError(buf, typ, true)
	}

	return buf, nil
}

func (t *trzszTransfer) sendInteger(typ string, val int64) error {
	return t.sendLine(typ, strconv.FormatInt(val, 10))
}

func (t *trzszTransfer) recvInteger(typ string, mayHasJunk bool, timeout <-chan time.Time) (int64, error) {
	buf, err := t.recvCheck(typ, mayHasJunk, timeout)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(buf, 10, 64)
}

func (t *trzszTransfer) checkInteger(expect int64, timeout <-chan time.Time) error {
	result, err := t.recvInteger("SUCC", false, timeout)
	if err != nil {
		return err
	}
	if result != expect {
		return newTrzszError(fmt.Sprintf("Integer check [%d] <> [%d]", result, expect), "", true)
	}
	return nil
}

func (t *trzszTransfer) sendString(typ string, str string) error {
	return t.sendLine(typ, encodeString(str))
}

func (t *trzszTransfer) recvString(typ string, mayHasJunk bool, timeout <-chan time.Time) (string, error) {
	buf, err := t.recvCheck(typ, mayHasJunk, timeout)
	if err != nil {
		return "", err
	}
	b, err := decodeString(buf)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (t *trzszTransfer) checkString(expect string, timeout <-chan time.Time) error { // nolint:all
	result, err := t.recvString("SUCC", false, timeout)
	if err != nil {
		return err
	}
	if result != expect {
		return newTrzszError(fmt.Sprintf("String check [%s] <> [%s]", result, expect), "", true)
	}
	return nil
}

func (t *trzszTransfer) sendBinary(typ string, buf []byte) error {
	return t.sendLine(typ, encodeBytes(buf))
}

func (t *trzszTransfer) recvBinary(typ string, mayHasJunk bool, timeout <-chan time.Time) ([]byte, error) {
	buf, err := t.recvCheck(typ, mayHasJunk, timeout)
	if err != nil {
		return nil, err
	}
	return decodeString(buf)
}

func (t *trzszTransfer) checkBinary(expect []byte, timeout <-chan time.Time) error {
	result, err := t.recvBinary("SUCC", false, timeout)
	if err != nil {
		return err
	}
	if bytes.Compare(result, expect) != 0 { // nolint:all
		return newTrzszError(fmt.Sprintf("Binary check [%v] <> [%v]", result, expect), "", true)
	}
	return nil
}

func (t *trzszTransfer) sendData(data []byte) error {
	if err := t.checkStop(); err != nil {
		return err
	}
	if !t.transferConfig.Binary {
		return t.sendBinary("DATA", data)
	}
	buf := escapeData(data, t.transferConfig.EscapeTable)
	if err := t.writeAll([]byte(fmt.Sprintf("#DATA:%d\n", len(buf)))); err != nil {
		return err
	}
	return t.writeAll(buf)
}

func (t *trzszTransfer) getNewTimeout() <-chan time.Time {
	if t.transferConfig.Timeout > 0 {
		return time.NewTimer(time.Duration(t.transferConfig.Timeout) * time.Second).C
	}
	return nil
}

func (t *trzszTransfer) recvData() ([]byte, error) {
	timeout := t.getNewTimeout()
	if !t.transferConfig.Binary {
		return t.recvBinary("DATA", false, timeout)
	}
	size, err := t.recvInteger("DATA", false, timeout)
	if err != nil {
		return nil, err
	}
	data, err := t.buffer.readBinary(int(size), timeout)
	if err != nil {
		if e := t.checkStop(); e != nil {
			return nil, e
		}
		return nil, err
	}
	buf, remaining, err := unescapeData(data, t.transferConfig.EscapeTable, nil)
	if err != nil {
		return nil, err
	}
	if len(remaining) != 0 {
		return nil, simpleTrzszError("Unescape has bytes remaining: %v", remaining)
	}
	return buf, nil
}

func (t *trzszTransfer) sendAction(confirm bool, serverVersion *trzszVersion, remoteIsWindows bool) error {
	protocol := kProtocolVersion
	if serverVersion != nil &&
		serverVersion.compare(&trzszVersion{1, 1, 3}) <= 0 && serverVersion.compare(&trzszVersion{1, 1, 0}) >= 0 {
		protocol = 2 // compatible with older versions
	}
	action := &transferAction{
		Lang:             "go",
		Version:          kTrzszVersion,
		Confirm:          confirm,
		Newline:          "\n",
		Protocol:         protocol,
		SupportBinary:    true,
		SupportDirectory: true,
	}

	t.tunnelInitWG.Wait()
	if conn := t.tunnelConn.Load(); conn != nil {
		t.writer = *conn
		t.tunnelConnected = true
		action.TunnelConnected = true
		action.SupportFork = true
	}

	if !t.tunnelConnected && (isWindowsEnvironment() || remoteIsWindows) {
		action.Newline = "!\n"
		action.SupportBinary = false
	}
	actStr, err := json.Marshal(action)
	if err != nil {
		return err
	}
	if remoteIsWindows {
		t.windowsProtocol = true
		t.transferConfig.Newline = "!\n"
	}
	if err := t.sendString("ACT", string(actStr)); err != nil {
		return err
	}
	if t.tunnelConnected {
		t.transferConfig.Newline = "\n"
	}
	return nil
}

func (t *trzszTransfer) recvAction() (*transferAction, error) {
	actStr, err := t.recvString("ACT", true, nil)
	if err != nil {
		return nil, err
	}
	action := &transferAction{
		Newline:       "\n",
		SupportBinary: true,
	}
	if err := json.Unmarshal([]byte(actStr), action); err != nil {
		return nil, err
	}
	if action.TunnelConnected {
		t.tunnelConnected = true
		if conn := t.tunnelConn.Load(); conn != nil {
			t.writer = *conn
		} else {
			return nil, simpleTrzszError("The tunnel connection is nil")
		}
	}
	t.transferConfig.Newline = action.Newline
	return action, nil
}

func (t *trzszTransfer) sendConfig(args *baseArgs, action *transferAction, escapeChars [][]unicode, tmuxMode tmuxModeType, tmuxPaneWidth int32) error {
	cfgMap := map[string]interface{}{
		"lang": "go",
	}
	if args.Quiet {
		cfgMap["quiet"] = true
	}
	if action.TunnelConnected {
		cfgMap["binary"] = true
		if args.Fork {
			t.switchToBackground()
			cfgMap["fork"] = true
			cfgMap["quiet"] = true
		}
	} else if args.Binary {
		cfgMap["binary"] = true
		cfgMap["escape_chars"] = escapeChars
	}
	if args.Directory {
		cfgMap["directory"] = true
	}
	cfgMap["bufsize"] = args.Bufsize.Size
	cfgMap["timeout"] = args.Timeout
	if args.Overwrite {
		cfgMap["overwrite"] = true
	}
	if tmuxMode == tmuxNormalMode {
		cfgMap["tmux_output_junk"] = true
	}
	if tmuxPaneWidth > 0 {
		cfgMap["tmux_pane_width"] = tmuxPaneWidth
	}
	if action.Protocol > 0 {
		cfgMap["protocol"] = minInt(action.Protocol, kProtocolVersion)
	}
	if args.Compress != kCompressAuto {
		cfgMap["compress"] = args.Compress
	}
	cfgStr, err := json.Marshal(cfgMap)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(cfgStr), &t.transferConfig); err != nil {
		return err
	}
	return t.sendString("CFG", string(cfgStr))
}

func (t *trzszTransfer) recvConfig() (*transferConfig, error) {
	cfgStr, err := t.recvString("CFG", true, t.getNewTimeout())
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(cfgStr), &t.transferConfig); err != nil {
		return nil, err
	}
	if t.transferConfig.Fork {
		t.bgChan <- struct{}{}
	}
	return &t.transferConfig, nil
}

func (t *trzszTransfer) clientExit(msg string) error {
	return t.sendString("EXIT", msg)
}

func (t *trzszTransfer) recvExit() (string, error) {
	return t.recvString("EXIT", false, t.getNewTimeout())
}

func (t *trzszTransfer) serverExit(msg string) {
	t.cleanInput(500 * time.Millisecond)
	t.resetTerm(msg, false)
}

func (t *trzszTransfer) resetTerm(msg string, ignorable bool) {
	if !t.termReseted.CompareAndSwap(false, true) {
		if !ignorable {
			os.Stdout.WriteString(fmt.Sprintf("\x1b7\r\n%s\r\n\x1b8", msg))
		}
		return
	}
	if t.stdinState != nil {
		_ = term.Restore(int(os.Stdin.Fd()), t.stdinState)
	}
	if isRunningOnWindows() {
		msg = strings.ReplaceAll(msg, "\n", "\r\n")
		os.Stdout.WriteString("\x1b[H\x1b[2J\x1b[?1049l")
	} else {
		os.Stdout.WriteString("\x1b8\x1b[0J")
	}
	os.Stdout.WriteString(msg)
	os.Stdout.WriteString("\r\n")
	showCursor(os.Stdout)
	if t.transferConfig.TmuxOutputJunk {
		tmuxRefreshClient()
	}
}

func (t *trzszTransfer) deleteCreatedFiles() []string {
	var deletedFiles []string
	for _, path := range t.createdFiles {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		}
		if err := os.RemoveAll(path); err == nil {
			deletedFiles = append(deletedFiles, path)
		}
	}
	return deletedFiles
}

func (t *trzszTransfer) clientError(err error) {
	t.cleanInput(t.cleanTimeout)

	trace := true
	if e, ok := err.(*trzszError); ok {
		trace = e.isTraceBack()
		if e.isRemoteExit() || e.isRemoteFail() {
			return
		}
	}

	if t.stopAndDelete.Load() {
		deletedFiles := t.deleteCreatedFiles()
		if len(deletedFiles) > 0 {
			_ = t.sendString("fail", joinFileNames(err.Error()+":", deletedFiles))
			return
		}
	}

	typ := "fail"
	if trace {
		typ = "FAIL"
	}
	_ = t.sendString(typ, err.Error())
}

func (t *trzszTransfer) serverError(err error) {
	t.cleanInput(t.cleanTimeout)

	trace := true
	if e, ok := err.(*trzszError); ok {
		if e.isStopAndDelete() {
			deletedFiles := t.deleteCreatedFiles()
			if len(deletedFiles) > 0 {
				t.serverExit(joinFileNames(err.Error()+":", deletedFiles))
				return
			}
		}
		trace = e.isTraceBack()
		if e.isRemoteExit() || e.isRemoteFail() {
			t.serverExit(e.Error())
			return
		}
	}

	typ := "fail"
	if trace {
		typ = "FAIL"
	}
	_ = t.sendString(typ, err.Error())

	t.serverExit(err.Error())
}

func (t *trzszTransfer) sendFileNum(num int64, progress progressCallback) error {
	if err := t.sendInteger("NUM", num); err != nil {
		return err
	}
	if err := t.checkInteger(num, t.getNewTimeout()); err != nil {
		return err
	}
	if progress != nil {
		progress.onNum(num)
	}
	return nil
}

func (t *trzszTransfer) sendFileName(srcFile *sourceFile, progress progressCallback) (fileReader, string, error) {
	var fileName string
	if t.transferConfig.Directory {
		jsonName, err := srcFile.marshalSourceFile()
		if err != nil {
			return nil, "", err
		}
		fileName = jsonName
	} else {
		fileName = srcFile.getFileName()
	}
	if err := t.sendString("NAME", fileName); err != nil {
		return nil, "", err
	}

	remoteName, err := t.recvString("SUCC", false, t.getNewTimeout())
	if err != nil {
		return nil, "", err
	}

	if progress != nil {
		progress.onName(srcFile.getFileName())
	}

	if srcFile.IsDir {
		return nil, remoteName, nil
	}

	file, err := os.Open(srcFile.AbsPath)
	if err != nil {
		return nil, "", err
	}

	return &simpleFileReader{file, srcFile.Size}, remoteName, nil
}

func (t *trzszTransfer) sendFileSize(size int64, progress progressCallback) error {
	if err := t.sendInteger("SIZE", size); err != nil {
		return err
	}
	if err := t.checkInteger(size, t.getNewTimeout()); err != nil {
		return err
	}
	if progress != nil {
		progress.onSize(size)
	}
	return nil
}

func (t *trzszTransfer) sendFileData(file fileReader, progress progressCallback) ([]byte, error) {
	step := int64(0)
	if progress != nil {
		progress.onStep(step)
	}
	bufSize := int64(1024)
	buffer := make([]byte, bufSize)
	hasher := md5.New()
	size := file.getSize()
	for step < size {
		beginTime := time.Now()
		m := size - step
		if m < bufSize {
			buffer = buffer[:m]
		}
		n, err := file.Read(buffer)
		length := int64(n)
		if err == io.EOF {
			if length+step != size {
				return nil, simpleTrzszError("EOF but length [%d] + step [%d] <> size [%d]", length, step, size)
			}
		} else if err != nil {
			return nil, err
		}
		data := buffer[:n]
		if err := t.sendData(data); err != nil {
			return nil, err
		}
		if _, err := hasher.Write(data); err != nil {
			return nil, err
		}
		if err := t.checkInteger(length, t.getNewTimeout()); err != nil {
			return nil, err
		}
		step += length
		if progress != nil {
			progress.onStep(step)
		}
		chunkTime := time.Since(beginTime)
		if length == bufSize && chunkTime < 500*time.Millisecond && bufSize < t.transferConfig.MaxBufSize {
			bufSize = minInt64(bufSize*2, t.transferConfig.MaxBufSize)
			buffer = make([]byte, bufSize)
		} else if chunkTime >= 2*time.Second && bufSize > 1024 {
			bufSize = 1024
			buffer = make([]byte, bufSize)
		}
		t.setLastChunkTime(chunkTime)
	}
	return hasher.Sum(nil), nil
}

func (t *trzszTransfer) sendFileMD5(digest []byte, progress progressCallback) error {
	if err := t.sendBinary("MD5", digest); err != nil {
		return err
	}
	if err := t.checkBinary(digest, t.getNewTimeout()); err != nil {
		return err
	}
	if progress != nil {
		progress.onDone()
	}
	return nil
}

func (t *trzszTransfer) sendFiles(sourceFiles []*sourceFile, progress progressCallback) ([]string, error) {
	sourceFiles = t.archiveSourceFiles(sourceFiles)
	if err := t.sendFileNum(int64(len(sourceFiles)), progress); err != nil {
		return nil, err
	}

	var remoteNames []string
	for _, srcFile := range sourceFiles {
		var err error
		var file fileReader
		var remoteName string
		if t.transferConfig.Protocol >= kProtocolVersion3 {
			file, remoteName, err = t.sendFileNameV3(srcFile, progress)
		} else {
			file, remoteName, err = t.sendFileName(srcFile, progress)
		}
		if err != nil {
			return nil, err
		}

		if !containsString(remoteNames, remoteName) {
			remoteNames = append(remoteNames, remoteName)
		}

		if file == nil {
			continue
		}

		defer file.Close()

		if err := t.sendFileSize(file.getSize(), progress); err != nil {
			return nil, err
		}

		var digest []byte
		if t.transferConfig.Protocol >= kProtocolVersion2 {
			digest, err = t.sendFileDataV2(file, progress)
		} else {
			digest, err = t.sendFileData(file, progress)
		}
		if err != nil {
			return nil, err
		}

		if err := t.sendFileMD5(digest, progress); err != nil {
			return nil, err
		}
	}

	return remoteNames, nil
}

func (t *trzszTransfer) recvFileNum(progress progressCallback) (int64, error) {
	num, err := t.recvInteger("NUM", false, t.getNewTimeout())
	if err != nil {
		return 0, err
	}
	if err := t.sendInteger("SUCC", num); err != nil {
		return 0, err
	}
	if progress != nil {
		progress.onNum(num)
	}
	return num, nil
}

func (t *trzszTransfer) addCreatedFiles(path string) {
	t.createdFiles = append(t.createdFiles, path)
}

func (t *trzszTransfer) doCreateFile(path string, truncate bool, perm *uint32) (fileWriter, error) {
	flag := os.O_RDWR | os.O_CREATE
	if truncate {
		flag |= os.O_TRUNC
	}
	fileMode := fs.FileMode(0644)
	if perm != nil {
		fileMode = fs.FileMode(*perm) | 0600
	}
	file, err := os.OpenFile(path, flag, fileMode)
	if err != nil {
		if e, ok := err.(*fs.PathError); ok {
			if errno, ok := e.Unwrap().(syscall.Errno); ok {
				if (!isRunningOnWindows() && errno == 13) || (isRunningOnWindows() && errno == 5) {
					return nil, simpleTrzszError("No permission to write: %s", path)
				} else if (!isRunningOnWindows() && errno == 21) || (isRunningOnWindows() && errno == 0x2000002a) {
					return nil, simpleTrzszError("Is a directory: %s", path)
				}
			}
		}
		return nil, simpleTrzszError("Create file [%s] failed: %v", path, err)
	}
	t.addCreatedFiles(path)
	return &simpleFileWriter{file}, nil
}

func (t *trzszTransfer) doCreateDirectory(path string, perm *uint32) error {
	stat, err := os.Stat(path)
	if os.IsNotExist(err) {
		fileMode := fs.FileMode(0755)
		if perm != nil {
			fileMode = fs.FileMode(*perm) | 0700
		}
		err := os.MkdirAll(path, fileMode)
		if err != nil {
			return err
		}
		t.addCreatedFiles(path)
		return nil
	} else if err != nil {
		return err
	}
	if !stat.IsDir() {
		return simpleTrzszError("Not a directory: %s", path)
	}
	return nil
}

func (t *trzszTransfer) createFile(path, fileName string, truncate bool, perm *uint32) (fileWriter, string, error) {
	var localName string
	if t.transferConfig.Overwrite {
		localName = fileName
	} else {
		var err error
		localName, err = getNewName(path, fileName)
		if err != nil {
			return nil, "", err
		}
	}
	file, err := t.doCreateFile(filepath.Join(path, localName), truncate, perm)
	if err != nil {
		return nil, "", err
	}
	return file, localName, nil
}

func (t *trzszTransfer) createDirOrFile(path string, srcFile *sourceFile, truncate bool) (fileWriter, string, error) {
	var localName string
	if t.transferConfig.Overwrite {
		localName = srcFile.RelPath[0]
	} else {
		if v, ok := t.fileNameMap[srcFile.PathID]; ok {
			localName = v
		} else {
			var err error
			localName, err = getNewName(path, srcFile.RelPath[0])
			if err != nil {
				return nil, "", err
			}
			t.fileNameMap[srcFile.PathID] = localName
		}
	}

	var fullPath string
	if len(srcFile.RelPath) > 1 {
		p := filepath.Join(append([]string{path, localName}, srcFile.RelPath[1:len(srcFile.RelPath)-1]...)...)
		if err := t.doCreateDirectory(p, srcFile.Perm); err != nil {
			return nil, "", err
		}
		fullPath = filepath.Join(p, srcFile.getFileName())
	} else {
		fullPath = filepath.Join(path, localName)
	}

	if srcFile.Archive {
		file, err := t.newArchiveWriter(path, srcFile, fullPath)
		if err != nil {
			return nil, "", err
		}
		return file, localName, nil
	}

	if srcFile.IsDir {
		if err := t.doCreateDirectory(fullPath, srcFile.Perm); err != nil {
			return nil, "", err
		}
		return nil, localName, nil
	}

	file, err := t.doCreateFile(fullPath, truncate, srcFile.Perm)
	if err != nil {
		return nil, "", err
	}
	return file, localName, nil
}

func (t *trzszTransfer) recvFileName(path string, progress progressCallback) (fileWriter, string, error) {
	fileName, err := t.recvString("NAME", false, t.getNewTimeout())
	if err != nil {
		return nil, "", err
	}

	var file fileWriter
	var localName string
	if t.transferConfig.Directory {
		var srcFile *sourceFile
		srcFile, err = unmarshalSourceFile(fileName)
		if err != nil {
			return nil, "", err
		}
		fileName = srcFile.getFileName()
		file, localName, err = t.createDirOrFile(path, srcFile, true)
	} else {
		file, localName, err = t.createFile(path, fileName, true, nil)
	}
	if err != nil {
		return nil, "", err
	}

	if err := t.sendString("SUCC", localName); err != nil {
		return nil, "", err
	}
	if progress != nil {
		progress.onName(fileName)
	}

	return file, localName, nil
}

func (t *trzszTransfer) recvFileSize(progress progressCallback) (int64, error) {
	size, err := t.recvInteger("SIZE", false, t.getNewTimeout())
	if err != nil {
		return 0, err
	}
	if err := t.sendInteger("SUCC", size); err != nil {
		return 0, err
	}
	if progress != nil {
		progress.onSize(size)
	}
	return size, nil
}

func (t *trzszTransfer) recvFileData(file fileWriter, size int64, progress progressCallback) ([]byte, error) {
	defer file.Close()
	step := int64(0)
	if progress != nil {
		progress.onStep(step)
	}
	hasher := md5.New()
	for step < size {
		beginTime := time.Now()
		data, err := t.recvData()
		if err != nil {
			return nil, err
		}
		if err := writeAll(file, data); err != nil {
			return nil, err
		}
		length := int64(len(data))
		step += length
		if progress != nil {
			progress.onStep(step)
		}
		if err := t.sendInteger("SUCC", length); err != nil {
			return nil, err
		}
		if _, err := hasher.Write(data); err != nil {
			return nil, err
		}
		t.setLastChunkTime(time.Since(beginTime))
	}
	return hasher.Sum(nil), nil
}

func (t *trzszTransfer) recvFileMD5(digest []byte, progress progressCallback) error {
	expectDigest, err := t.recvBinary("MD5", false, t.getNewTimeout())
	if err != nil {
		return err
	}
	if bytes.Compare(digest, expectDigest) != 0 { // nolint:all
		return simpleTrzszError("Check MD5 failed")
	}
	if err := t.sendBinary("SUCC", digest); err != nil {
		return err
	}
	if progress != nil {
		progress.onDone()
	}
	return nil
}

func (t *trzszTransfer) recvFiles(path string, progress progressCallback) ([]string, error) {
	num, err := t.recvFileNum(progress)
	if err != nil {
		return nil, err
	}

	var localNames []string
	for i := int64(0); i < num; i++ {
		var err error
		var file fileWriter
		var localName string
		if t.transferConfig.Protocol >= kProtocolVersion3 {
			file, localName, err = t.recvFileNameV3(path, progress)
		} else {
			file, localName, err = t.recvFileName(path, progress)
		}
		if err != nil {
			return nil, err
		}

		if !containsString(localNames, localName) {
			localNames = append(localNames, localName)
		}

		if file == nil {
			continue
		}

		defer file.Close()

		size, err := t.recvFileSize(progress)
		if err != nil {
			return nil, err
		}

		var digest []byte
		if t.transferConfig.Protocol >= kProtocolVersion2 {
			digest, err = t.recvFileDataV2(file, size, progress)
		} else {
			digest, err = t.recvFileData(file, size, progress)
		}
		if err != nil {
			return nil, err
		}

		if err := t.recvFileMD5(digest, progress); err != nil {
			return nil, err
		}
	}

	return localNames, nil
}
