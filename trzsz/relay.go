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
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
)

const (
	kRelayStandBy = iota
	kRelayHandshaking
	kRelayTransferring
)

type TrzszRelay struct {
	pty             *TrzszPty
	osStdinChan     chan []byte
	osStdoutChan    chan []byte
	bypassTmuxChan  chan []byte
	bufferLock      sync.Mutex
	stdinBuffer     *TrzszBuffer
	stdoutBuffer    *TrzszBuffer
	tmuxPaneWidth   int
	clientIsWindows bool
	serverIsWindows bool
	relayStatus     atomic.Int32
}

func (r *TrzszRelay) addHandshakeBuffer(buffer *TrzszBuffer, data []byte) (int32, bool) {
	r.bufferLock.Lock()
	defer r.bufferLock.Unlock()
	status := r.relayStatus.Load()
	if status != kRelayHandshaking {
		return status, false
	}
	buffer.addBuffer(data)
	return status, true
}

func (r *TrzszRelay) flushHandshakeBuffer(confirm bool) {
	r.bufferLock.Lock()
	defer r.bufferLock.Unlock()

	for {
		buf := r.stdinBuffer.popBuffer()
		if buf == nil {
			break
		}
		r.osStdinChan <- buf
	}

	for {
		buf := r.stdoutBuffer.popBuffer()
		if buf == nil {
			break
		}
		if confirm {
			r.bypassTmuxChan <- buf
		} else {
			r.osStdoutChan <- buf
		}
	}

	if confirm {
		r.relayStatus.Store(kRelayTransferring)
	} else {
		r.relayStatus.Store(kRelayStandBy)
	}
}

func decodeRelayBufferString(expectType string, line []byte) (string, error) {
	idx := bytes.IndexByte(line, ':')
	if idx < 1 {
		return "", NewTrzszError(encodeBytes(line), "colon", true)
	}

	typ := string(line[1:idx])
	buf := string(line[idx+1:])
	if typ != expectType {
		return "", NewTrzszError(buf, typ, true)
	}

	data, err := decodeString(buf)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func recvStringFromBuffer(buffer *TrzszBuffer, expectType string, mayHasJunk bool) (string, error) {
	line, err := buffer.readLine(mayHasJunk, nil)
	if err != nil {
		return "", err
	}

	if mayHasJunk {
		idx := bytes.LastIndex(line, []byte("#"+expectType+":"))
		if idx >= 0 {
			line = line[idx:]
		}
	}

	return decodeRelayBufferString(expectType, line)
}

func recvStringForWindows(buffer *TrzszBuffer, expectType string) (string, error) {
	line, err := buffer.readLineOnWindows(nil)
	if err != nil {
		return "", err
	}
	idx := bytes.LastIndex(line, []byte("#"+expectType+":"))
	if idx >= 0 {
		line = line[idx:]
	}
	return decodeRelayBufferString(expectType, line)
}

func (r *TrzszRelay) recvStringFromClient(expectType string) (string, error) {
	if r.serverIsWindows {
		return recvStringForWindows(r.stdinBuffer, expectType)
	}
	return recvStringFromBuffer(r.stdinBuffer, expectType, false)
}

func (r *TrzszRelay) recvStringFromServer(expectType string) (string, error) {
	if r.clientIsWindows || r.serverIsWindows {
		return recvStringForWindows(r.stdoutBuffer, expectType)
	}
	return recvStringFromBuffer(r.stdoutBuffer, expectType, true)
}

func (r *TrzszRelay) sendStringToClient(typ string, str string) error {
	newline := "\n"
	if r.clientIsWindows || r.serverIsWindows {
		newline = "!\n"
	}
	r.bypassTmuxChan <- []byte(fmt.Sprintf("#%s:%s%s", typ, encodeString(str), newline))
	return nil
}

func (r *TrzszRelay) sendStringToServer(typ string, str string) error {
	newline := "\n"
	if r.serverIsWindows {
		newline = "!\n"
	}
	r.osStdinChan <- []byte(fmt.Sprintf("#%s:%s%s", typ, encodeString(str), newline))
	return nil
}

func (r *TrzszRelay) recvAction() (*TransferAction, error) {
	actStr, err := r.recvStringFromClient("ACT")
	if err != nil {
		return nil, err
	}
	action := &TransferAction{
		Newline:       "\n",
		SupportBinary: true,
	}
	if err := json.Unmarshal([]byte(actStr), action); err != nil {
		return nil, err
	}
	return action, nil
}

func (r *TrzszRelay) sendAction(action *TransferAction) error {
	actStr, err := json.Marshal(action)
	if err != nil {
		return err
	}
	return r.sendStringToServer("ACT", string(actStr))
}

func (r *TrzszRelay) recvConfig() (*TransferConfig, error) {
	cfgStr, err := r.recvStringFromServer("CFG")
	if err != nil {
		return nil, err
	}
	config := &TransferConfig{
		Timeout:    20,
		Newline:    "\n",
		MaxBufSize: 10 * 1024 * 1024,
	}
	if r.serverIsWindows {
		config.Newline = "!\n"
	}
	if err := json.Unmarshal([]byte(cfgStr), config); err != nil {
		return nil, err
	}
	return config, nil
}

func (r *TrzszRelay) sendConfig(config *TransferConfig) error {
	cfgStr, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return r.sendStringToClient("CFG", string(cfgStr))
}

func (r *TrzszRelay) sendError(err error) {
	r.sendStringToClient("FAIL", err.Error())
	r.sendStringToServer("FAIL", err.Error())
}

func (r *TrzszRelay) handshake() {
	confirm := false
	var err error = nil
	defer func() {
		if err != nil {
			r.sendError(err)
		}
		r.flushHandshakeBuffer(confirm)
	}()

	action, err := r.recvAction()
	if err != nil {
		err = newTrzszError(fmt.Sprintf("Relay recv action error: %v", err))
		return
	}
	r.clientIsWindows = action.Newline == "!\n"

	action.SupportBinary = false
	if action.Protocol > 2 {
		action.Protocol = 2
	}
	if err := r.sendAction(action); err != nil {
		err = newTrzszError(fmt.Sprintf("Relay send action error: %v", err))
		return
	}

	if !action.Confirm {
		return
	}

	config, err := r.recvConfig()
	if err != nil {
		err = newTrzszError(fmt.Sprintf("Relay recv config error: %v", err))
		return
	}

	config.TmuxOutputJunk = true
	if config.TmuxPaneColumns <= 0 {
		config.TmuxPaneColumns = r.tmuxPaneWidth
	}
	if err := r.sendConfig(config); err != nil {
		err = newTrzszError(fmt.Sprintf("Relay send config error: %v", err))
		return
	}

	confirm = true
}

func (r *TrzszRelay) wrapInput() {
	defer close(r.osStdinChan)
	for {
		buffer := make([]byte, 32*1024)
		n, err := os.Stdin.Read(buffer)
		if n > 0 {
			buf := buffer[:n]
			if gTrzszArgs.TraceLog {
				writeTraceLog(buf, "stdin")
			}

			status := r.relayStatus.Load()
			if status == kRelayHandshaking {
				var ok bool
				status, ok = r.addHandshakeBuffer(r.stdinBuffer, buf)
				if ok {
					continue
				}
			}

			r.osStdinChan <- buf
			if status == kRelayTransferring {
				if buf[0] == '\x03' { // `ctrl + c` to stop
					r.relayStatus.Store(kRelayStandBy)
				} else if bytes.Contains(buf, []byte("#EXIT:")) { // transfer exit
					r.relayStatus.Store(kRelayStandBy)
				} else if bytes.Contains(buf, []byte("#FAIL:")) || bytes.Contains(buf, []byte("#fail:")) { // transfer error
					r.relayStatus.Store(kRelayStandBy)
				}
			}
		}
		if err == io.EOF {
			break
		}
	}
}

func (r *TrzszRelay) wrapOutput() {
	defer close(r.osStdoutChan)
	defer close(r.bypassTmuxChan)
	for {
		buffer := make([]byte, 32*1024)
		n, err := r.pty.Stdout.Read(buffer)
		if n > 0 {
			buf := buffer[:n]
			if gTrzszArgs.TraceLog {
				buf = writeTraceLog(buf, "svrout")
			}

			status := r.relayStatus.Load()
			if status == kRelayHandshaking {
				var ok bool
				status, ok = r.addHandshakeBuffer(r.stdoutBuffer, buf)
				if ok {
					continue
				}
			}

			if status == kRelayTransferring {
				r.bypassTmuxChan <- buf
				if bytes.Contains(buf, []byte("#EXIT:")) { // transfer exit
					r.relayStatus.Store(kRelayStandBy)
				} else if bytes.Contains(buf, []byte("#FAIL:")) || bytes.Contains(buf, []byte("#fail:")) { // transfer error
					r.relayStatus.Store(kRelayStandBy)
				}
				continue
			}

			var mode *byte
			mode, r.serverIsWindows = detectTrzsz(buf)
			if mode != nil {
				r.relayStatus.Store(kRelayHandshaking) // store status before send to client
				go r.handshake()
			}

			r.osStdoutChan <- buf
		}
		if err == io.EOF {
			break
		}
	}
}

func NewTrzszRelay(pty *TrzszPty, bypassTmuxOut *os.File, tmuxPaneWidth int) *TrzszRelay {
	osStdinChan := make(chan []byte, 10)
	go func() {
		for buffer := range osStdinChan {
			if gTrzszArgs.TraceLog {
				writeTraceLog(buffer, "tosvr")
			}
			writeAll(pty.Stdin, buffer)
		}
	}()

	osStdoutChan := make(chan []byte, 10)
	go func() {
		for buffer := range osStdoutChan {
			if gTrzszArgs.TraceLog {
				writeTraceLog(buffer, "stdout")
			}
			writeAll(os.Stdout, buffer)
		}
	}()

	bypassTmuxChan := make(chan []byte, 10)
	go func() {
		for buffer := range bypassTmuxChan {
			if gTrzszArgs.TraceLog {
				writeTraceLog(buffer, "tocli")
			}
			writeAll(bypassTmuxOut, buffer)
		}
	}()

	return &TrzszRelay{
		pty:            pty,
		osStdinChan:    osStdinChan,
		osStdoutChan:   osStdoutChan,
		bypassTmuxChan: bypassTmuxChan,
		stdinBuffer:    NewTrzszBuffer(),
		stdoutBuffer:   NewTrzszBuffer(),
		tmuxPaneWidth:  tmuxPaneWidth,
	}
}

func asyncCopy(src PtyIO, dst PtyIO) {
	bufferChan := make(chan []byte, 10)
	go func() {
		defer close(bufferChan)
		for {
			buffer := make([]byte, 32*1024)
			n, err := src.Read(buffer)
			if n > 0 {
				bufferChan <- buffer[:n]
			}
			if err == io.EOF {
				break
			}
		}
	}()
	go func() {
		for buffer := range bufferChan {
			writeAll(dst, buffer)
		}
	}()
}

func runAsRelay(pty *TrzszPty) {
	tmuxMode, bypassTmuxOut, tmuxPaneWidth, _ := checkTmux()
	if tmuxMode != TmuxNormalMode {
		asyncCopy(os.Stdin, pty.Stdin)
		asyncCopy(pty.Stdout, os.Stdout)
		return
	}

	relay := NewTrzszRelay(pty, bypassTmuxOut, tmuxPaneWidth)
	go relay.wrapInput()
	go relay.wrapOutput()
}
