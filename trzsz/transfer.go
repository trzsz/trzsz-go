/*
MIT License

Copyright (c) 2022 Lonny Wong

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
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/term"
)

type TrzszTransfer struct {
	buffer          *TrzszBuffer
	writer          PtyIO
	stopped         bool
	tmuxOutputJunk  bool
	lastInputTime   int64
	cleanTimeout    time.Duration
	maxChunkTime    time.Duration
	transferConfig  map[string]interface{}
	protocolNewline string
	stdinState      *term.State
	fileNameMap     map[int]string
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

func NewTransfer(writer PtyIO, stdinState *term.State) *TrzszTransfer {
	return &TrzszTransfer{
		NewTrzszBuffer(),
		writer,
		false,
		false,
		0,
		100 * time.Millisecond,
		0,
		make(map[string]interface{}),
		"\n",
		stdinState,
		make(map[int]string),
	}
}

func (t *TrzszTransfer) addReceivedData(buf []byte) {
	if !t.stopped {
		t.buffer.addBuffer(buf)
	}
	atomic.StoreInt64(&t.lastInputTime, time.Now().UnixMilli())
}

func (t *TrzszTransfer) stopTransferringFiles() {
	t.cleanTimeout = maxDuration(t.maxChunkTime*2, 500*time.Millisecond)
	t.stopped = true
	t.buffer.stopBuffer()
}

func (t *TrzszTransfer) cleanInput(timeoutDuration time.Duration) {
	t.stopped = true
	t.buffer.drainBuffer()
	atomic.StoreInt64(&t.lastInputTime, time.Now().UnixMilli())
	for {
		sleepDuration := timeoutDuration - time.Now().Sub(time.UnixMilli(atomic.LoadInt64(&t.lastInputTime)))
		if sleepDuration <= 0 {
			return
		}
		time.Sleep(sleepDuration)
	}
}

func (t *TrzszTransfer) writeAll(buf []byte) error {
	if gTrzszArgs != nil && gTrzszArgs.TraceLog {
		writeTraceLog(buf, false)
	}
	written := 0
	length := len(buf)
	for written < length {
		n, err := t.writer.Write(buf[written:])
		if err != nil {
			return err
		}
		written += n
	}
	return nil
}

func (t *TrzszTransfer) sendLine(typ string, buf string) error {
	return t.writeAll([]byte(fmt.Sprintf("#%s:%s%s", typ, buf, t.protocolNewline)))
}

func (t *TrzszTransfer) recvLine(expectType string, mayHasJunk bool, timeout <-chan time.Time) ([]byte, error) {
	if t.stopped {
		return nil, newTrzszError("Stopped")
	}

	if IsWindows() {
		line, err := t.buffer.readLineOnWindows(timeout)
		if err != nil {
			return nil, err
		}
		if t.tmuxOutputJunk || mayHasJunk {
			idx := bytes.LastIndex(line, []byte("#"+expectType+":"))
			if idx >= 0 {
				line = line[idx:]
			}
		}
		return line, nil
	}

	line, err := t.buffer.readLine(timeout)
	if err != nil {
		return nil, err
	}

	if t.tmuxOutputJunk || mayHasJunk {
		if len(line) > 0 {
			buf := make([]byte, len(line))
			copy(buf, line)
			for buf[len(buf)-1] == '\r' {
				line, err := t.buffer.readLine(timeout)
				if err != nil {
					return nil, err
				}
				buf = append(buf[:len(buf)-1], line...)
			}
			line = buf
		}
		idx := bytes.LastIndex(line, []byte("#"+expectType+":"))
		if idx >= 0 {
			line = line[idx:]
		}
	}

	return line, nil
}

func (t *TrzszTransfer) recvCheck(expectType string, mayHasJunk bool, timeout <-chan time.Time) (string, error) {
	line, err := t.recvLine(expectType, mayHasJunk, timeout)
	if err != nil {
		return "", err
	}

	idx := bytes.IndexByte(line, ':')
	if idx < 1 {
		return "", NewTrzszError(encodeBytes(line), "colon", true)
	}

	typ := string(line[1:idx])
	buf := string(line[idx+1:])
	if typ != expectType {
		return "", NewTrzszError(buf, typ, true)
	}

	return buf, nil
}

func (t *TrzszTransfer) sendInteger(typ string, val int64) error {
	return t.sendLine(typ, strconv.FormatInt(val, 10))
}

func (t *TrzszTransfer) recvInteger(typ string, mayHasJunk bool) (int64, error) {
	buf, err := t.recvCheck(typ, mayHasJunk, nil)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(buf, 10, 64)
}

func (t *TrzszTransfer) checkInteger(expect int64) error {
	result, err := t.recvInteger("SUCC", false)
	if err != nil {
		return err
	}
	if result != expect {
		return NewTrzszError(fmt.Sprintf("[%d] <> [%d]", result, expect), "", true)
	}
	return nil
}

func (t *TrzszTransfer) sendString(typ string, str string) error {
	return t.sendLine(typ, encodeString(str))
}

func (t *TrzszTransfer) recvString(typ string, mayHasJunk bool) (string, error) {
	buf, err := t.recvCheck(typ, mayHasJunk, nil)
	if err != nil {
		return "", err
	}
	b, err := decodeString(buf)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (t *TrzszTransfer) checkString(expect string) error {
	result, err := t.recvString("SUCC", false)
	if err != nil {
		return err
	}
	if result != expect {
		return NewTrzszError(fmt.Sprintf("[%s] <> [%s]", result, expect), "", true)
	}
	return nil
}

func (t *TrzszTransfer) sendBinary(typ string, buf []byte) error {
	return t.sendLine(typ, encodeBytes(buf))
}

func (t *TrzszTransfer) recvBinary(typ string, mayHasJunk bool, timeout <-chan time.Time) ([]byte, error) {
	buf, err := t.recvCheck(typ, mayHasJunk, timeout)
	if err != nil {
		return nil, err
	}
	return decodeString(buf)
}

func (t *TrzszTransfer) checkBinary(expect []byte) error {
	result, err := t.recvBinary("SUCC", false, nil)
	if err != nil {
		return err
	}
	if bytes.Compare(result, expect) != 0 {
		return NewTrzszError(fmt.Sprintf("[%v] <> [%v]", result, expect), "", true)
	}
	return nil
}

func (t *TrzszTransfer) sendData(data []byte, binary bool, escapeCodes [][]byte) error {
	if !binary {
		return t.sendBinary("DATA", data)
	}
	buf := escapeData(data, escapeCodes)
	if err := t.writeAll([]byte(fmt.Sprintf("#DATA:%d\n", len(buf)))); err != nil {
		return err
	}
	return t.writeAll(buf)
}

func (t *TrzszTransfer) recvData(binary bool, escapeCodes [][]byte, timeout time.Duration) ([]byte, error) {
	var timeoutChan <-chan time.Time
	if timeout > 0 {
		timeoutChan = time.NewTimer(timeout).C
	}
	if !binary {
		return t.recvBinary("DATA", false, timeoutChan)
	}
	size, err := t.recvInteger("DATA", false)
	if err != nil {
		return nil, err
	}
	data, err := t.buffer.readBinary(int(size), timeoutChan)
	if err != nil {
		return nil, err
	}
	return unescapeData(data, escapeCodes), nil
}

func (t *TrzszTransfer) sendAction(confirm, remoteIsWindows bool) error {
	actMap := map[string]interface{}{
		"lang":        "go",
		"confirm":     confirm,
		"version":     kTrzszVersion,
		"support_dir": true,
	}
	if IsWindows() {
		actMap["binary"] = false
		actMap["newline"] = "!\n"
	}
	actStr, err := json.Marshal(actMap)
	if err != nil {
		return err
	}
	if remoteIsWindows {
		t.protocolNewline = "!\n"
	}
	return t.sendString("ACT", string(actStr))
}

func (t *TrzszTransfer) recvAction() (map[string]interface{}, error) {
	actStr, err := t.recvString("ACT", false)
	if err != nil {
		return nil, err
	}
	var actMap map[string]interface{}
	if err := json.Unmarshal([]byte(actStr), &actMap); err != nil {
		return nil, err
	}
	if v, ok := actMap["newline"].(string); ok {
		t.protocolNewline = v
	}
	return actMap, nil
}

func (t *TrzszTransfer) sendConfig(args *Args, escapeChars [][]unicode, tmuxMode TmuxMode, tmuxPaneWidth int) error {
	cfgMap := map[string]interface{}{
		"lang": "go",
	}
	if args.Quiet {
		cfgMap["quiet"] = true
	}
	if args.Binary {
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
	if tmuxMode == TmuxNormalMode {
		cfgMap["tmux_output_junk"] = true
		cfgMap["tmux_pane_width"] = tmuxPaneWidth
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

func (t *TrzszTransfer) recvConfig() (map[string]interface{}, error) {
	cfgStr, err := t.recvString("CFG", true)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(cfgStr), &t.transferConfig); err != nil {
		return nil, err
	}
	if v, ok := t.transferConfig["tmux_output_junk"].(bool); ok {
		t.tmuxOutputJunk = v
	}
	return t.transferConfig, nil
}

func (t *TrzszTransfer) clientExit(msg string) error {
	return t.sendString("EXIT", msg)
}

func (t *TrzszTransfer) recvExit() (string, error) {
	return t.recvString("EXIT", false)
}

func (t *TrzszTransfer) serverExit(msg string) {
	t.cleanInput(500 * time.Millisecond)
	if t.stdinState != nil {
		term.Restore(int(os.Stdin.Fd()), t.stdinState)
	}
	os.Stdout.WriteString("\x1b8\x1b[0J")
	os.Stdout.WriteString(msg)
	os.Stdout.WriteString("\n")
}

func (t *TrzszTransfer) clientError(err error) {
	t.cleanInput(t.cleanTimeout)

	trace := true
	if e, ok := err.(*TrzszError); ok {
		trace = e.isTraceBack()
		if e.isRemoteExit() || e.isRemoteFail() {
			return
		}
	}

	typ := "fail"
	if trace {
		typ = "FAIL"
	}
	_ = t.sendString(typ, err.Error())
}

func (t *TrzszTransfer) serverError(err error) {
	t.cleanInput(t.cleanTimeout)

	trace := true
	if e, ok := err.(*TrzszError); ok {
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

func (t *TrzszTransfer) sendFileNum(num int64, progress ProgressCallback) error {
	if err := t.sendInteger("NUM", num); err != nil {
		return err
	}
	if err := t.checkInteger(num); err != nil {
		return err
	}
	if progress != nil && !reflect.ValueOf(progress).IsNil() {
		progress.onNum(num)
	}
	return nil
}

func (t *TrzszTransfer) sendFileName(f *TrzszFile, directory bool, progress ProgressCallback) (*os.File, string, error) {
	var fileName string
	if directory {
		jsonName, err := json.Marshal(f)
		if err != nil {
			return nil, "", err
		}
		fileName = string(jsonName)
	} else {
		fileName = f.RelPath[0]
	}
	if err := t.sendString("NAME", fileName); err != nil {
		return nil, "", err
	}
	remoteName, err := t.recvString("SUCC", false)
	if err != nil {
		return nil, "", err
	}
	if progress != nil && !reflect.ValueOf(progress).IsNil() {
		progress.onName(f.RelPath[len(f.RelPath)-1])
	}
	if f.IsDir {
		return nil, remoteName, nil
	}
	file, err := os.Open(f.AbsPath)
	if err != nil {
		return nil, "", err
	}
	return file, remoteName, nil
}

func (t *TrzszTransfer) sendFileSize(file *os.File, progress ProgressCallback) (int64, error) {
	stat, err := file.Stat()
	if err != nil {
		return 0, err
	}
	size := stat.Size()
	if err := t.sendInteger("SIZE", size); err != nil {
		return 0, err
	}
	if err := t.checkInteger(size); err != nil {
		return 0, err
	}
	if progress != nil && !reflect.ValueOf(progress).IsNil() {
		progress.onSize(size)
	}
	return size, nil
}

func (t *TrzszTransfer) sendFileMD5(digest []byte, progress ProgressCallback) error {
	if err := t.sendBinary("MD5", digest); err != nil {
		return err
	}
	if err := t.checkBinary(digest); err != nil {
		return err
	}
	if progress != nil && !reflect.ValueOf(progress).IsNil() {
		progress.onDone()
	}
	return nil
}

func (t *TrzszTransfer) sendFiles(files []*TrzszFile, progress ProgressCallback) ([]string, error) {
	binary := false
	if v, ok := t.transferConfig["binary"].(bool); ok {
		binary = v
	}
	directory := false
	if v, ok := t.transferConfig["directory"].(bool); ok {
		directory = v
	}
	maxBufSize := int64(10 * 1024 * 1024)
	if v, ok := t.transferConfig["bufsize"].(float64); ok {
		maxBufSize = int64(v)
	}
	escapeCodes := [][]byte{}
	if v, ok := t.transferConfig["escape_chars"].([]interface{}); ok {
		var err error
		escapeCodes, err = escapeCharsToCodes(v)
		if err != nil {
			return nil, err
		}
	}

	if err := t.sendFileNum(int64(len(files)), progress); err != nil {
		return nil, err
	}

	bufSize := int64(1024)
	buffer := make([]byte, bufSize)
	var remoteNames []string
	for _, f := range files {
		file, remoteName, err := t.sendFileName(f, directory, progress)
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

		fileSize, err := t.sendFileSize(file, progress)
		if err != nil {
			return nil, err
		}

		step := int64(0)
		hasher := md5.New()
		for step < fileSize {
			beginTime := time.Now()
			n, err := file.Read(buffer)
			if err != nil {
				return nil, err
			}
			size := int64(n)
			data := buffer[:n]
			if err := t.sendData(data, binary, escapeCodes); err != nil {
				return nil, err
			}
			if _, err := hasher.Write(data); err != nil {
				return nil, err
			}
			if err := t.checkInteger(size); err != nil {
				return nil, err
			}
			step += size
			if progress != nil && !reflect.ValueOf(progress).IsNil() {
				progress.onStep(step)
			}
			chunkTime := time.Now().Sub(beginTime)
			if size == bufSize && chunkTime < 500*time.Millisecond && bufSize < maxBufSize {
				bufSize = minInt64(bufSize*2, maxBufSize)
				buffer = make([]byte, bufSize)
			}
			if chunkTime > t.maxChunkTime {
				t.maxChunkTime = chunkTime
			}
		}

		if err := t.sendFileMD5(hasher.Sum(nil), progress); err != nil {
			return nil, err
		}
	}

	return remoteNames, nil
}

func (t *TrzszTransfer) recvFileNum(progress ProgressCallback) (int64, error) {
	num, err := t.recvInteger("NUM", false)
	if err != nil {
		return 0, err
	}
	if err := t.sendInteger("SUCC", num); err != nil {
		return 0, err
	}
	if progress != nil && !reflect.ValueOf(progress).IsNil() {
		progress.onNum(num)
	}
	return num, nil
}

func doCreateFile(path string) (*os.File, error) {
	file, err := os.Create(path)
	if err != nil {
		if e, ok := err.(*fs.PathError); ok {
			if errno, ok := e.Unwrap().(syscall.Errno); ok {
				if (!IsWindows() && errno == 13) || (IsWindows() && errno == 5) {
					return nil, newTrzszError(fmt.Sprintf("No permission to write: %s", path))
				} else if (!IsWindows() && errno == 21) || (IsWindows() && errno == 0x2000002a) {
					return nil, newTrzszError(fmt.Sprintf("Is a directory: %s", path))
				}
			}
		}
		return nil, newTrzszError(fmt.Sprintf("%v", err))
	}
	return file, nil
}

func doCreateDirectory(path string) error {
	stat, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(path, 0755)
	} else if err != nil {
		return err
	}
	if !stat.IsDir() {
		return newTrzszError(fmt.Sprintf("Not a directory: %s", path))
	}
	return nil
}

func (t *TrzszTransfer) createFile(path, fileName string, overwrite bool) (*os.File, string, error) {
	var localName string
	if overwrite {
		localName = fileName
	} else {
		var err error
		localName, err = getNewName(path, fileName)
		if err != nil {
			return nil, "", err
		}
	}
	file, err := doCreateFile(filepath.Join(path, localName))
	if err != nil {
		return nil, "", err
	}
	return file, localName, nil
}

func (t *TrzszTransfer) createDirOrFile(path, name string, overwrite bool) (*os.File, string, string, error) {
	var f TrzszFile
	if err := json.Unmarshal([]byte(name), &f); err != nil {
		return nil, "", "", err
	}
	if len(f.RelPath) < 1 {
		return nil, "", "", newTrzszError(fmt.Sprintf("Invalid name: %s", name))
	}

	fileName := f.RelPath[len(f.RelPath)-1]

	var localName string
	if overwrite {
		localName = f.RelPath[0]
	} else {
		if v, ok := t.fileNameMap[f.PathID]; ok {
			localName = v
		} else {
			var err error
			localName, err = getNewName(path, f.RelPath[0])
			if err != nil {
				return nil, "", "", err
			}
			t.fileNameMap[f.PathID] = localName
		}
	}

	var fullPath string
	if len(f.RelPath) > 1 {
		p := filepath.Join(append([]string{path, localName}, f.RelPath[1:len(f.RelPath)-1]...)...)
		if err := doCreateDirectory(p); err != nil {
			return nil, "", "", err
		}
		fullPath = filepath.Join(p, fileName)
	} else {
		fullPath = filepath.Join(path, localName)
	}

	if f.IsDir {
		if err := doCreateDirectory(fullPath); err != nil {
			return nil, "", "", err
		}
		return nil, localName, fileName, nil
	}

	file, err := doCreateFile(fullPath)
	if err != nil {
		return nil, "", "", err
	}
	return file, localName, fileName, nil
}

func (t *TrzszTransfer) recvFileName(path string, directory, overwrite bool, progress ProgressCallback) (*os.File, string, error) {
	fileName, err := t.recvString("NAME", false)
	if err != nil {
		return nil, "", err
	}

	var file *os.File
	var localName string
	if directory {
		file, localName, fileName, err = t.createDirOrFile(path, fileName, overwrite)
	} else {
		file, localName, err = t.createFile(path, fileName, overwrite)
	}
	if err != nil {
		return nil, "", err
	}

	if err := t.sendString("SUCC", localName); err != nil {
		return nil, "", err
	}
	if progress != nil && !reflect.ValueOf(progress).IsNil() {
		progress.onName(fileName)
	}

	return file, localName, nil
}

func (t *TrzszTransfer) recvFileSize(progress ProgressCallback) (int64, error) {
	size, err := t.recvInteger("SIZE", false)
	if err != nil {
		return 0, err
	}
	if err := t.sendInteger("SUCC", size); err != nil {
		return 0, err
	}
	if progress != nil && !reflect.ValueOf(progress).IsNil() {
		progress.onSize(size)
	}
	return size, nil
}

func (t *TrzszTransfer) recvFileMD5(digest []byte, progress ProgressCallback) error {
	expectDigest, err := t.recvBinary("MD5", false, nil)
	if err != nil {
		return err
	}
	if bytes.Compare(digest, expectDigest) != 0 {
		return newTrzszError("Check MD5 failed")
	}
	if err := t.sendBinary("SUCC", digest); err != nil {
		return err
	}
	if progress != nil && !reflect.ValueOf(progress).IsNil() {
		progress.onDone()
	}
	return nil
}

func (t *TrzszTransfer) recvFiles(path string, progress ProgressCallback) ([]string, error) {
	binary := false
	if v, ok := t.transferConfig["binary"].(bool); ok {
		binary = v
	}
	directory := false
	if v, ok := t.transferConfig["directory"].(bool); ok {
		directory = v
	}
	overwrite := false
	if v, ok := t.transferConfig["overwrite"].(bool); ok {
		overwrite = v
	}
	timeout := 100 * time.Second
	if v, ok := t.transferConfig["timeout"].(float64); ok {
		timeout = time.Duration(v) * time.Second
	}
	escapeCodes := [][]byte{}
	if v, ok := t.transferConfig["escape_chars"].([]interface{}); ok {
		var err error
		escapeCodes, err = escapeCharsToCodes(v)
		if err != nil {
			return nil, err
		}
	}

	num, err := t.recvFileNum(progress)
	if err != nil {
		return nil, err
	}

	var localNames []string
	for i := int64(0); i < num; i++ {
		file, localName, err := t.recvFileName(path, directory, overwrite, progress)
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

		fileSize, err := t.recvFileSize(progress)
		if err != nil {
			return nil, err
		}

		step := int64(0)
		hasher := md5.New()
		for step < fileSize {
			beginTime := time.Now()
			data, err := t.recvData(binary, escapeCodes, timeout)
			if err != nil {
				return nil, err
			}
			if _, err := file.Write(data); err != nil {
				return nil, err
			}
			size := int64(len(data))
			step += size
			if progress != nil && !reflect.ValueOf(progress).IsNil() {
				progress.onStep(step)
			}
			if err := t.sendInteger("SUCC", size); err != nil {
				return nil, err
			}
			if _, err := hasher.Write(data); err != nil {
				return nil, err
			}
			chunkTime := time.Now().Sub(beginTime)
			if chunkTime > t.maxChunkTime {
				t.maxChunkTime = chunkTime
			}
		}

		if err := t.recvFileMD5(hasher.Sum(nil), progress); err != nil {
			return nil, err
		}
	}

	return localNames, nil
}
