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
	"encoding/json"
	"fmt"
)

type TrzszTransfer struct {
	ptyIn   PtyIO
	ptyOut  PtyIO
	stopped bool
}

func NewTransfer(ptyIn PtyIO, ptyOut PtyIO) *TrzszTransfer {
	return &TrzszTransfer{ptyIn, ptyOut, false}
}

func (t *TrzszTransfer) addReceivedData(buf []byte) {
	// TODO
}

func (t *TrzszTransfer) stopTransferringFiles() {
	// TODO
	t.stopped = true
}

func (t *TrzszTransfer) cleanInput(timeoutInMilliseconds int64) {
}

func (t *TrzszTransfer) sendLine(typ string, buf string) error {
	_, err := t.ptyOut.Write([]byte(fmt.Sprintf("#%s:%s\n", typ, buf)))
	return err
}

func (t *TrzszTransfer) recvLine(expectType string, mayHasJunk bool) (string, error) {
	if t.stopped {
		return "", fmt.Errorf("Stopped")
	}
	// TODO
	return "", nil
}

func (t *TrzszTransfer) sendString(typ string, str string) error {
	return t.sendLine(typ, encodeString(str))
}

func (t *TrzszTransfer) sendAction(confirm bool) error {
	actMap := map[string]interface{}{
		"lang":    "go",
		"confirm": confirm,
		"version": TrzszVersion,
	}
	actStr, err := json.Marshal(actMap)
	if err != nil {
		return err
	}
	return t.sendString("ACT", string(actStr))
}

func (t *TrzszTransfer) recvAction() error {
	// TODO
	return nil
}

func (t *TrzszTransfer) sendConfig() error {
	// TODO
	return nil
}

func (t *TrzszTransfer) recvConfig() (map[string]interface{}, error) {
	// TODO
	return nil, nil
}

func (t *TrzszTransfer) handleClientError(err error) {
	// TODO
}

func (t *TrzszTransfer) sendExit(msg string) error {
	t.cleanInput(200)
	return t.sendString("EXIT", msg)
}

func (t *TrzszTransfer) sendFiles(files []string, progress ProgressCallback) ([]string, error) {
	// TODO
	t.sendExit("Under development")
	return nil, nil
}

func (t *TrzszTransfer) recvFiles(path string, progress ProgressCallback) ([]string, error) {
	// TODO
	t.sendExit("Under development")
	return nil, nil
}
