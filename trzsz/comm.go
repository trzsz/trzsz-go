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
	"compress/zlib"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
)

var isWindows bool = (runtime.GOOS == "windows")

func IsWindows() bool {
	return isWindows
}

type PtyIO interface {
	Read(b []byte) (n int, err error)
	Write(p []byte) (n int, err error)
	Close() error
}

type ProgressCallback interface {
	onNum(num int64)
	onName(name string)
	onSize(size int64)
	onStep(step int64)
	onDone(name string)
}

type BufferSize struct {
	Size int64
}

type Args struct {
	Quiet     bool       `arg:"-q" help:"quiet (hide progress bar)"`
	Overwrite bool       `arg:"-y" help:"yes, overwrite existing file(s)"`
	Binary    bool       `arg:"-b" help:"binary transfer mode, faster for binary files"`
	Escape    bool       `arg:"-e" help:"escape all known control characters"`
	Bufsize   BufferSize `arg:"-B" placeholder:"N" default:"10M" help:"max buffer chunk size (1K<=N<=1G). (default: 10M)"`
	Timeout   int        `arg:"-t" placeholder:"N" default:"100" help:"timeout ( N seconds ) for each buffer chunk.\nN <= 0 means never timeout. (default: 100)"`
}

var sizeRegexp = regexp.MustCompile("(?i)^(\\d+)(b|k|m|g|kb|mb|gb)?$")

func (b *BufferSize) UnmarshalText(buf []byte) error {
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
	var b bytes.Buffer
	z := zlib.NewWriter(&b)
	z.Write([]byte(buf))
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
	return ioutil.ReadAll(z)
}

type TrzszError struct {
	message string
	errType string
	trace   bool
}

func NewTrzszError(message string, errType string, trace bool) *TrzszError {
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
	err := &TrzszError{message, errType, trace}
	if err.isTraceBack() {
		err.message = fmt.Sprintf("%s\n%s", err.message, string(debug.Stack()))
	}
	return err
}

func newTrzszError(message string) *TrzszError {
	return NewTrzszError(message, "", false)
}

func (e *TrzszError) Error() string {
	return e.message
}

func (e *TrzszError) isTraceBack() bool {
	if e.errType == "fail" || e.errType == "EXIT" {
		return false
	}
	return e.trace
}

func (e *TrzszError) isRemoteExit() bool {
	return e.errType == "EXIT"
}

func (e *TrzszError) isRemoteFail() bool {
	return e.errType == "fail" || e.errType == "FAIL"
}

func checkPathWritable(path string) error {
	fileInfo, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return newTrzszError(fmt.Sprintf("No such directory: %s", path))
	}
	if !fileInfo.IsDir() {
		return newTrzszError(fmt.Sprintf("Not a directory: %s", path))
	}
	if !IsWindows() {
		if fileInfo.Mode().Perm()&(1<<7) == 0 {
			return newTrzszError(fmt.Sprintf("No permission to write: %s", path))
		}
	}
	return nil
}

func checkFilesReadable(files []string) error {
	for _, file := range files {
		fileInfo, err := os.Stat(file)
		if errors.Is(err, os.ErrNotExist) {
			return newTrzszError(fmt.Sprintf("No such file: %s", file))
		}
		if fileInfo.IsDir() {
			return newTrzszError(fmt.Sprintf("Is a directory: %s", file))
		}
		if !fileInfo.Mode().IsRegular() {
			return newTrzszError(fmt.Sprintf("Not a regular file: %s", file))
		}
		if !IsWindows() {
			if fileInfo.Mode().Perm()&(1<<8) == 0 {
				return newTrzszError(fmt.Sprintf("No permission to read: %s", file))
			}
		}
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
	return "", newTrzszError("Fail to assign new file name")
}

type TmuxMode int

const (
	NoTmux = iota
	TmuxNormalMode
	TmuxControlMode
)

func checkTmux() (TmuxMode, *os.File, int, error) {
	if _, tmux := os.LookupEnv("TMUX"); !tmux {
		return NoTmux, os.Stdout, -1, nil
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
		return TmuxControlMode, os.Stdout, -1, nil
	}
	if _, err := os.Stat(tmuxTty); errors.Is(err, os.ErrNotExist) {
		return TmuxControlMode, os.Stdout, -1, nil
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
	return TmuxNormalMode, tmuxStdout, tmuxPaneWidth, nil
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

func reverseString(s string) string {
	rns := []rune(s)
	for i, j := 0, len(rns)-1; i < j; i, j = i+1, j-1 {
		rns[i], rns[j] = rns[j], rns[i]
	}
	return string(rns)
}

func wrapStdinInput(transfer *TrzszTransfer) {
	const bufSize = 10240
	buffer := make([]byte, bufSize)
	for {
		n, err := os.Stdin.Read(buffer)
		if err == io.EOF {
			transfer.stopTransferringFiles()
		} else {
			buf := buffer[0:n]
			transfer.addReceivedData(buf)
			buffer = make([]byte, bufSize)
		}
	}
}

func handleServerSignal(transfer *TrzszTransfer) {
	sigstop := make(chan os.Signal, 1)
	signal.Notify(sigstop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigstop
		transfer.stopTransferringFiles()
	}()
}
