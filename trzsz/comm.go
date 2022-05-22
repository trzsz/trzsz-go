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
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime/debug"
)

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
	if e.errType == "fail" {
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
	if fileInfo.Mode().Perm()&(1<<7) == 0 {
		return newTrzszError(fmt.Sprintf("No permission to write: %s", path))
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
		if fileInfo.Mode().Perm()&(1<<8) == 0 {
			return newTrzszError(fmt.Sprintf("No permission to read: %s", file))
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
