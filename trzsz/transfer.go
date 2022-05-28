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
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"syscall"
	"time"
)

type TrzszTransfer struct {
	buffer         *TrzszBuffer
	writer         PtyIO
	stopped        bool
	tmuxOutputJunk bool
	lastInputTime  *time.Time
	cleanTimeout   time.Duration
	maxChunkTime   time.Duration
	transferConfig map[string]interface{}
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

func NewTransfer(writer PtyIO) *TrzszTransfer {
	return &TrzszTransfer{NewTrzszBuffer(), writer, false, false, nil, 100 * time.Millisecond, 0, make(map[string]interface{})}
}

func (t *TrzszTransfer) addReceivedData(buf []byte) {
	if !t.stopped {
		t.buffer.addBuffer(buf)
	}
	now := time.Now()
	t.lastInputTime = &now
}

func (t *TrzszTransfer) stopTransferringFiles() {
	t.cleanTimeout = maxDuration(t.maxChunkTime*2, 500*time.Millisecond)
	t.stopped = true
	t.buffer.stopBuffer()
}

func (t *TrzszTransfer) cleanInput(timeoutDuration time.Duration) {
	t.stopped = true
	t.buffer.drainBuffer()
	if t.lastInputTime == nil {
		return
	}
	for {
		sleepDuration := timeoutDuration - time.Now().Sub(*t.lastInputTime)
		if sleepDuration <= 0 {
			return
		}
		time.Sleep(sleepDuration)
	}
}

func (t *TrzszTransfer) writeAll(buf []byte) error {
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
	return t.writeAll([]byte(fmt.Sprintf("#%s:%s\n", typ, buf)))
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
				buf = append(buf, line...)
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
	timer := time.NewTimer(timeout)
	if !binary {
		return t.recvBinary("DATA", false, timer.C)
	}
	size, err := t.recvInteger("DATA", false)
	if err != nil {
		return nil, err
	}
	data, err := t.buffer.readBinary(int(size), timer.C)
	if err != nil {
		return nil, err
	}
	return unescapeData(data, escapeCodes), nil
}

func (t *TrzszTransfer) sendAction(confirm bool) error {
	actMap := map[string]interface{}{
		"lang":    "go",
		"confirm": confirm,
		"version": TrzszVersion,
	}
	if IsWindows() {
		actMap["binary"] = false
		actMap["newline"] = "!\n"
	}
	actStr, err := json.Marshal(actMap)
	if err != nil {
		return err
	}
	return t.sendString("ACT", string(actStr))
}

func (t *TrzszTransfer) recvAction() (map[string]interface{}, error) {
	actStr, err := t.recvString("ACT", false)
	if err != nil {
		return nil, err
	}
	var actMap map[string]interface{}
	err = json.Unmarshal([]byte(actStr), &actMap)
	if err != nil {
		return nil, err
	}
	return actMap, nil
}

func (t *TrzszTransfer) sendConfig() error {
	cfgMap := map[string]interface{}{
		"lang": "go",
	}
	cfgStr, err := json.Marshal(cfgMap)
	if err != nil {
		return err
	}
	return t.sendString("CFG", string(cfgStr))
}

func (t *TrzszTransfer) recvConfig() (map[string]interface{}, error) {
	cfgStr, err := t.recvString("CFG", true)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal([]byte(cfgStr), &t.transferConfig)
	if err != nil {
		return nil, err
	}
	if v, ok := t.transferConfig["tmux_output_junk"].(bool); ok {
		t.tmuxOutputJunk = v
	}
	return t.transferConfig, nil
}

func (t *TrzszTransfer) handleClientError(err error) {
	t.cleanInput(t.cleanTimeout)

	trace := true
	if e, ok := err.(*TrzszError); ok {
		trace = e.isTraceBack()
		if e.isRemoteExit() {
			return
		}
		if e.isRemoteFail() {
			return
		}
	}

	typ := "fail"
	if trace {
		typ = "FAIL"
	}
	_ = t.sendString(typ, err.Error())
}

func (t *TrzszTransfer) sendExit(msg string) error {
	t.cleanInput(200)
	return t.sendString("EXIT", msg)
}

func (t *TrzszTransfer) sendFiles(files []string, progress ProgressCallback) ([]string, error) {
	binary := false
	if v, ok := t.transferConfig["binary"].(bool); ok {
		binary = v
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

	num := int64(len(files))
	if err := t.sendInteger("NUM", num); err != nil {
		return nil, err
	}
	if err := t.checkInteger(num); err != nil {
		return nil, err
	}
	if progress != nil && !reflect.ValueOf(progress).IsNil() {
		progress.onNum(num)
	}

	bufSize := int64(1024)
	buffer := make([]byte, bufSize)
	remoteNames := make([]string, len(files))
	for i, file := range files {
		fileName := filepath.Base(file)
		if err := t.sendString("NAME", fileName); err != nil {
			return nil, err
		}
		remoteName, err := t.recvString("SUCC", false)
		if err != nil {
			return nil, err
		}
		if progress != nil && !reflect.ValueOf(progress).IsNil() {
			progress.onName(fileName)
		}

		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		stat, err := f.Stat()
		if err != nil {
			return nil, err
		}

		fileSize := stat.Size()
		if err := t.sendInteger("SIZE", fileSize); err != nil {
			return nil, err
		}
		if err := t.checkInteger(fileSize); err != nil {
			return nil, err
		}
		if progress != nil && !reflect.ValueOf(progress).IsNil() {
			progress.onSize(fileSize)
		}

		step := int64(0)
		hasher := md5.New()
		for step < fileSize {
			beginTime := time.Now()
			n, err := f.Read(buffer)
			if err != nil {
				return nil, err
			}
			buf := buffer[:n]
			if err := t.sendData(buf, binary, escapeCodes); err != nil {
				return nil, err
			}
			if _, err := hasher.Write(buf); err != nil {
				return nil, err
			}
			if err := t.checkInteger(int64(n)); err != nil {
				return nil, err
			}
			step += int64(n)
			if progress != nil && !reflect.ValueOf(progress).IsNil() {
				progress.onStep(step)
			}
			chunkTime := time.Now().Sub(beginTime)
			if chunkTime < time.Second && bufSize < maxBufSize {
				bufSize = minInt64(bufSize*2, maxBufSize)
				buffer = make([]byte, bufSize)
			}
			if chunkTime > t.maxChunkTime {
				t.maxChunkTime = chunkTime
			}
		}

		digest := hasher.Sum(nil)
		if err := t.sendBinary("MD5", digest); err != nil {
			return nil, err
		}
		if err := t.checkBinary(digest); err != nil {
			return nil, err
		}
		if progress != nil && !reflect.ValueOf(progress).IsNil() {
			progress.onDone(remoteName)
		}

		remoteNames[i] = remoteName
	}

	return remoteNames, nil
}

func (t *TrzszTransfer) recvFiles(path string, progress ProgressCallback) ([]string, error) {
	binary := false
	if v, ok := t.transferConfig["binary"].(bool); ok {
		binary = v
	}
	overwrite := false
	if v, ok := t.transferConfig["overwrite"].(bool); ok {
		overwrite = v
	}
	timeout := time.Duration(100) * time.Second
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

	num, err := t.recvInteger("NUM", false)
	if err != nil {
		return nil, err
	}
	if err := t.sendInteger("SUCC", num); err != nil {
		return nil, err
	}
	if progress != nil && !reflect.ValueOf(progress).IsNil() {
		progress.onNum(num)
	}

	localNames := make([]string, num)
	for i := int64(0); i < num; i++ {
		fileName, err := t.recvString("NAME", false)
		if err != nil {
			return nil, err
		}
		localName := fileName
		if !overwrite {
			localName, err = getNewName(path, fileName)
			if err != nil {
				return nil, err
			}
		}
		fullPath := filepath.Join(path, localName)
		f, err := os.Create(fullPath)
		if err != nil {
			if e, ok := err.(*fs.PathError); ok {
				if errno, ok := e.Err.(syscall.Errno); ok {
					if errno == 13 {
						return nil, newTrzszError(fmt.Sprintf("No permission to write: %s", fullPath))
					} else if errno == 21 {
						return nil, newTrzszError(fmt.Sprintf("Is a directory: %s", fullPath))
					}
				}
			}
			return nil, err
		}
		defer f.Close()
		if err := t.sendString("SUCC", localName); err != nil {
			return nil, err
		}
		if progress != nil && !reflect.ValueOf(progress).IsNil() {
			progress.onName(fileName)
		}

		fileSize, err := t.recvInteger("SIZE", false)
		if err != nil {
			return nil, err
		}
		if err := t.sendInteger("SUCC", fileSize); err != nil {
			return nil, err
		}
		if progress != nil && !reflect.ValueOf(progress).IsNil() {
			progress.onSize(fileSize)
		}

		step := int64(0)
		hasher := md5.New()
		for step < fileSize {
			beginTime := time.Now()
			data, err := t.recvData(binary, escapeCodes, timeout)
			if err != nil {
				return nil, err
			}
			if _, err := f.Write(data); err != nil {
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

		actualDigest := hasher.Sum(nil)
		expectDigest, err := t.recvBinary("MD5", false, nil)
		if err != nil {
			return nil, err
		}
		if bytes.Compare(actualDigest, expectDigest) != 0 {
			return nil, newTrzszError(fmt.Sprintf("Check MD5 of %s failed", fileName))
		}
		if err := t.sendBinary("SUCC", actualDigest); err != nil {
			return nil, err
		}
		if progress != nil && !reflect.ValueOf(progress).IsNil() {
			progress.onDone(localName)
		}

		localNames[i] = localName
	}

	return localNames, nil
}
