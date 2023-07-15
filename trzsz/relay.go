/*
MIT License

Copyright (c) 2023 Lonny Wong <lonnywong@qq.com>

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
	"sync"
	"sync/atomic"
)

const (
	kRelayStandBy = iota
	kRelayHandshaking
	kRelayTransferring
)

type trzszRelay struct {
	clientIn        io.Reader
	clientOut       io.WriteCloser
	serverIn        io.WriteCloser
	serverOut       io.Reader
	osStdinChan     chan []byte
	osStdoutChan    chan []byte
	bypassTmuxChan  chan []byte
	bufferLock      sync.Mutex
	stdinBuffer     *trzszBuffer
	stdoutBuffer    *trzszBuffer
	tmuxPaneWidth   int32
	clientIsWindows bool
	serverIsWindows bool
	relayStatus     atomic.Int32
	logger          *traceLogger
}

func (r *trzszRelay) addHandshakeBuffer(buffer *trzszBuffer, data []byte) (int32, bool) {
	r.bufferLock.Lock()
	defer r.bufferLock.Unlock()
	status := r.relayStatus.Load()
	if status != kRelayHandshaking {
		return status, false
	}
	buffer.addBuffer(data)
	return status, true
}

func (r *trzszRelay) flushHandshakeBuffer(confirm bool) {
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
		return "", newTrzszError(encodeBytes(line), "colon", true)
	}

	typ := string(line[1:idx])
	buf := string(line[idx+1:])
	if typ != expectType {
		return "", newTrzszError(buf, typ, true)
	}

	data, err := decodeString(buf)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func recvStringFromBuffer(buffer *trzszBuffer, expectType string, mayHasJunk bool) (string, error) {
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

func recvStringForWindows(buffer *trzszBuffer, expectType string) (string, error) {
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

func (r *trzszRelay) recvStringFromClient(expectType string) (string, error) {
	if r.serverIsWindows {
		return recvStringForWindows(r.stdinBuffer, expectType)
	}
	return recvStringFromBuffer(r.stdinBuffer, expectType, false)
}

func (r *trzszRelay) recvStringFromServer(expectType string) (string, error) {
	if r.clientIsWindows || r.serverIsWindows {
		return recvStringForWindows(r.stdoutBuffer, expectType)
	}
	return recvStringFromBuffer(r.stdoutBuffer, expectType, true)
}

func (r *trzszRelay) sendStringToClient(typ string, str string) error {
	newline := "\n"
	if r.clientIsWindows || r.serverIsWindows {
		newline = "!\n"
	}
	r.bypassTmuxChan <- []byte(fmt.Sprintf("#%s:%s%s", typ, encodeString(str), newline))
	return nil
}

func (r *trzszRelay) sendStringToServer(typ string, str string) error {
	newline := "\n"
	if r.serverIsWindows {
		newline = "!\n"
	}
	r.osStdinChan <- []byte(fmt.Sprintf("#%s:%s%s", typ, encodeString(str), newline))
	return nil
}

func (r *trzszRelay) recvAction() (*transferAction, error) {
	actStr, err := r.recvStringFromClient("ACT")
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
	return action, nil
}

func (r *trzszRelay) sendAction(action *transferAction) error {
	actStr, err := json.Marshal(action)
	if err != nil {
		return err
	}
	return r.sendStringToServer("ACT", string(actStr))
}

func (r *trzszRelay) recvConfig() (*transferConfig, error) {
	cfgStr, err := r.recvStringFromServer("CFG")
	if err != nil {
		return nil, err
	}
	config := &transferConfig{
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

func (r *trzszRelay) sendConfig(config *transferConfig) error {
	cfgStr, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return r.sendStringToClient("CFG", string(cfgStr))
}

func (r *trzszRelay) sendError(err error) {
	_ = r.sendStringToClient("FAIL", err.Error())
	_ = r.sendStringToServer("FAIL", err.Error())
}

func (r *trzszRelay) handshake() {
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
		err = simpleTrzszError("Relay recv action error: %v", err)
		return
	}
	r.clientIsWindows = action.Newline == "!\n"

	action.SupportBinary = false
	if action.Protocol > kProtocolVersion {
		action.Protocol = kProtocolVersion
	}
	if e := r.sendAction(action); e != nil {
		err = simpleTrzszError("Relay send action error: %v", e)
		return
	}

	if !action.Confirm {
		return
	}

	config, err := r.recvConfig()
	if err != nil {
		err = simpleTrzszError("Relay recv config error: %v", err)
		return
	}

	config.TmuxOutputJunk = true
	if config.TmuxPaneColumns <= 0 {
		config.TmuxPaneColumns = r.tmuxPaneWidth
	}
	if e := r.sendConfig(config); e != nil {
		err = simpleTrzszError("Relay send config error: %v", e)
		return
	}

	confirm = true
}

func (r *trzszRelay) wrapInput() {
	defer close(r.osStdinChan)
	for {
		buffer := make([]byte, 32*1024)
		n, err := r.clientIn.Read(buffer)
		if n > 0 {
			buf := buffer[:n]
			if r.logger != nil {
				r.logger.writeTraceLog(buf, "stdin")
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

func (r *trzszRelay) wrapOutput() {
	defer close(r.osStdoutChan)
	defer close(r.bypassTmuxChan)
	detector := newTrzszDetector(true, true)
	for {
		buffer := make([]byte, 32*1024)
		n, err := r.serverOut.Read(buffer)
		if n > 0 {
			buf := buffer[:n]
			if r.logger != nil {
				buf = r.logger.writeTraceLog(buf, "svrout")
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
			var serverIsWindows bool
			buf, mode, _, serverIsWindows = detector.detectTrzsz(buf)
			if mode != nil {
				r.relayStatus.Store(kRelayHandshaking) // store status before send to client
				r.serverIsWindows = serverIsWindows
				go r.handshake()
			}

			r.osStdoutChan <- buf
		}
		if err == io.EOF {
			break
		}
	}
}

func asyncCopy(src io.Reader, dst io.Writer) {
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
			_ = writeAll(dst, buffer)
		}
	}()
}

// NewTrzszRelay create a TrzszRelay to support trzsz through a jump server.
//
// ┌────────┐   ClientIn   ┌────────────┐   ServerIn   ┌────────┐
// │        ├─────────────►│            ├─────────────►│        │
// │ Client │              │ TrzszRelay │              │ Server │
// │        │◄─────────────┤            │◄─────────────┤        │
// └────────┘   ClientOut  └────────────┘   ServerOut  └────────┘
func NewTrzszRelay(clientIn io.Reader, clientOut io.WriteCloser,
	serverIn io.WriteCloser, serverOut io.Reader, options TrzszOptions) {
	tmuxMode, bypassTmuxOut, tmuxPaneWidth, _ := checkTmux()
	if tmuxMode != tmuxNormalMode {
		asyncCopy(clientIn, serverIn)
		asyncCopy(serverOut, clientOut)
		return
	}

	var logger *traceLogger
	if options.DetectTraceLog {
		logger = newTraceLogger()
	}

	osStdinChan := make(chan []byte, 10)
	go func() {
		for buffer := range osStdinChan {
			if logger != nil {
				logger.writeTraceLog(buffer, "tosvr")
			}
			_ = writeAll(serverIn, buffer)
		}
	}()

	osStdoutChan := make(chan []byte, 10)
	go func() {
		for buffer := range osStdoutChan {
			if logger != nil {
				logger.writeTraceLog(buffer, "stdout")
			}
			_ = writeAll(clientOut, buffer)
		}
	}()

	bypassTmuxChan := make(chan []byte, 10)
	go func() {
		for buffer := range bypassTmuxChan {
			if logger != nil {
				logger.writeTraceLog(buffer, "tocli")
			}
			_ = writeAll(bypassTmuxOut, buffer)
		}
	}()

	relay := &trzszRelay{
		clientIn:       clientIn,
		clientOut:      clientOut,
		serverIn:       serverIn,
		serverOut:      serverOut,
		osStdinChan:    osStdinChan,
		osStdoutChan:   osStdoutChan,
		bypassTmuxChan: bypassTmuxChan,
		stdinBuffer:    newTrzszBuffer(),
		stdoutBuffer:   newTrzszBuffer(),
		tmuxPaneWidth:  tmuxPaneWidth,
		logger:         logger,
	}
	go relay.wrapInput()
	go relay.wrapOutput()
}
