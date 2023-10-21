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
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	kRelayStandBy = iota
	kRelayHandshaking
	kRelayTransferring
)

// TrzszRelay is a relay that supports trzsz ( trz / tsz ).
type TrzszRelay struct {
	tmuxMode        tmuxModeType
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
	relayStatus     atomic.Int32
	logger          *traceLogger
	trigger         *trzszTrigger
	tunnelConnector func(int) net.Conn
	tunnelRelayPort int
	tunnelListener  atomic.Pointer[net.Listener]
	tunnelRelay     atomic.Pointer[tunnelRelay]
	tunnelConnected atomic.Bool
}

type tunnelRelay struct {
	relay         atomic.Pointer[TrzszRelay]
	logger        *traceLogger
	clientConn    net.Conn
	serverConn    net.Conn
	clientBufChan chan []byte
	serverBufChan chan []byte
}

// SetTunnelConnector set the connector for tunnel transferring.
func (r *TrzszRelay) SetTunnelConnector(connector func(int) net.Conn) {
	r.tunnelConnector = connector
}

func (r *TrzszRelay) listenForTunnel(buf []byte) []byte {
	if r.tunnelConnector == nil || r.trigger.tunnelPort == 0 {
		return buf
	}

	listener, port := listenForTunnel()
	if listener == nil {
		return buf
	}

	r.tunnelRelayPort = port
	if listener := r.tunnelListener.Load(); listener != nil {
		(*listener).Close()
	}
	r.tunnelListener.Store(&listener)
	r.acceptOnTunnel()

	return bytes.ReplaceAll(buf, []byte(fmt.Sprintf(":%s:%d", r.trigger.uniqueID, r.trigger.tunnelPort)),
		[]byte(fmt.Sprintf(":%s:%d", r.trigger.uniqueID, r.tunnelRelayPort)))
}

func (r *TrzszRelay) acceptOnTunnel() {
	go func() {
		defer func() {
			if listener := r.tunnelListener.Load(); listener != nil {
				(*listener).Close()
				r.tunnelListener.Store(nil)
			}
		}()
		for {
			listener := r.tunnelListener.Load()
			if listener == nil {
				return
			}
			clientConn, err := (*listener).Accept()
			if err != nil {
				return
			}
			if r.tunnelRelay.Load() != nil {
				clientConn.Close()
				return
			}
			go r.handleTunnelConn(clientConn)
		}
	}()
}

func (r *TrzszRelay) handleTunnelConn(clientConn net.Conn) {
	clientHello1, serverHello4 := getHelloConstant(r.trigger.uniqueID, r.tunnelRelayPort)
	clientHello2, serverHello3 := getHelloConstant(r.trigger.uniqueID, r.trigger.tunnelPort)
	buf := make([]byte, 100)
	n, err := clientConn.Read(buf)
	if err != nil || string(buf[:n]) != clientHello1 {
		clientConn.Close()
		return
	}
	serverConn := r.tunnelConnector(r.trigger.tunnelPort)
	if serverConn == nil {
		clientConn.Close()
		return
	}
	if _, err := serverConn.Write([]byte(clientHello2)); err != nil {
		clientConn.Close()
		serverConn.Close()
		return
	}
	n, err = serverConn.Read(buf)
	if err != nil || string(buf[:n]) != serverHello3 {
		clientConn.Close()
		serverConn.Close()
		return
	}
	if _, err := clientConn.Write([]byte(serverHello4)); err != nil {
		clientConn.Close()
		serverConn.Close()
		return
	}
	tr := newTunnelRelay(r.logger, clientConn, serverConn)
	if r.tunnelRelay.CompareAndSwap(nil, tr) {
		tr.relay.Store(r)
		go tr.wrapInput()
		go tr.wrapOutput()
		if listener := r.tunnelListener.Load(); listener != nil {
			(*listener).Close()
			r.tunnelListener.Store(nil)
		}
	} else {
		close(tr.clientBufChan)
		close(tr.serverBufChan)
	}
}

func newTunnelRelay(logger *traceLogger, clientConn, serverConn net.Conn) *tunnelRelay {
	clientBufChan := make(chan []byte, 10)
	go func() {
		defer serverConn.Close()
		for buffer := range clientBufChan {
			if logger != nil {
				logger.writeTraceLog(buffer, "ttosvr")
			}
			_ = writeAll(serverConn, buffer)
		}
	}()

	serverBufChan := make(chan []byte, 10)
	go func() {
		defer clientConn.Close()
		for buffer := range serverBufChan {
			if logger != nil {
				logger.writeTraceLog(buffer, "ttocli")
			}
			_ = writeAll(clientConn, buffer)
		}
	}()

	return &tunnelRelay{
		logger:        logger,
		clientConn:    clientConn,
		serverConn:    serverConn,
		clientBufChan: clientBufChan,
		serverBufChan: serverBufChan,
	}
}

func (r *TrzszRelay) addHandshakeBuffer(buffer *trzszBuffer, data []byte, tunnel bool) (int32, bool) {
	r.bufferLock.Lock()
	defer r.bufferLock.Unlock()
	status := r.relayStatus.Load()
	if status != kRelayHandshaking || !tunnel && r.tunnelConnected.Load() {
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
		if t := r.tunnelRelay.Load(); t != nil && r.tunnelConnected.Load() {
			t.clientBufChan <- buf
		} else {
			r.osStdinChan <- buf
		}
	}

	for {
		buf := r.stdoutBuffer.popBuffer()
		if buf == nil {
			break
		}
		if t := r.tunnelRelay.Load(); t != nil && r.tunnelConnected.Load() {
			t.serverBufChan <- buf
		} else {
			if confirm {
				r.bypassTmuxChan <- buf
			} else {
				r.osStdoutChan <- buf
			}
		}
	}

	if confirm {
		r.relayStatus.Store(kRelayTransferring)
	} else {
		r.resetToStandby(kRelayHandshaking)
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

func (r *TrzszRelay) recvStringFromClient(expectType string) (string, error) {
	if r.trigger.winServer && !r.tunnelConnected.Load() {
		return recvStringForWindows(r.stdinBuffer, expectType)
	}
	return recvStringFromBuffer(r.stdinBuffer, expectType, true)
}

func (r *TrzszRelay) recvStringFromServer(expectType string) (string, error) {
	if (r.clientIsWindows || r.trigger.winServer) && !r.tunnelConnected.Load() {
		return recvStringForWindows(r.stdoutBuffer, expectType)
	}
	return recvStringFromBuffer(r.stdoutBuffer, expectType, true)
}

func (r *TrzszRelay) sendStringToClient(typ string, str string) error {
	newline := "\n"
	if (r.clientIsWindows || r.trigger.winServer) && !r.tunnelConnected.Load() {
		newline = "!\n"
	}
	buffer := []byte(fmt.Sprintf("#%s:%s%s", typ, encodeString(str), newline))
	if t := r.tunnelRelay.Load(); t != nil && r.tunnelConnected.Load() {
		t.serverBufChan <- buffer
	} else {
		r.bypassTmuxChan <- buffer
	}
	return nil
}

func (r *TrzszRelay) sendStringToServer(typ string, str string) error {
	newline := "\n"
	if r.trigger.winServer && (!r.tunnelConnected.Load() || typ == "ACT") {
		newline = "!\n"
	}
	buffer := []byte(fmt.Sprintf("#%s:%s%s", typ, encodeString(str), newline))
	if t := r.tunnelRelay.Load(); t != nil && r.tunnelConnected.Load() {
		t.clientBufChan <- buffer
	} else {
		r.osStdinChan <- buffer
	}
	return nil
}

func (r *TrzszRelay) recvAction() (*transferAction, error) {
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

func (r *TrzszRelay) sendAction(action *transferAction) error {
	actStr, err := json.Marshal(action)
	if err != nil {
		return err
	}
	return r.sendStringToServer("ACT", string(actStr))
}

func (r *TrzszRelay) recvConfig() (*transferConfig, error) {
	cfgStr, err := r.recvStringFromServer("CFG")
	if err != nil {
		return nil, err
	}
	config := &transferConfig{
		Timeout:    20,
		Newline:    "\n",
		MaxBufSize: 10 * 1024 * 1024,
	}
	if r.trigger.winServer && !r.tunnelConnected.Load() {
		config.Newline = "!\n"
	}
	if err := json.Unmarshal([]byte(cfgStr), config); err != nil {
		return nil, err
	}
	return config, nil
}

func (r *TrzszRelay) sendConfig(config *transferConfig) error {
	cfgStr, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return r.sendStringToClient("CFG", string(cfgStr))
}

func (r *TrzszRelay) sendError(err error) {
	_ = r.sendStringToClient("FAIL", err.Error())
	_ = r.sendStringToServer("FAIL", err.Error())
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
		err = simpleTrzszError("Relay recv action error: %v", err)
		return
	}
	r.tunnelConnected.Store(action.TunnelConnected)
	r.clientIsWindows = action.Newline == "!\n"

	if !action.TunnelConnected {
		action.SupportBinary = false
	}
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

	if r.tmuxMode == tmuxNormalMode {
		config.TmuxOutputJunk = true
	}
	if config.TmuxPaneColumns <= 0 && r.tmuxPaneWidth > 0 {
		config.TmuxPaneColumns = r.tmuxPaneWidth
	}
	if e := r.sendConfig(config); e != nil {
		err = simpleTrzszError("Relay send config error: %v", e)
		return
	}

	confirm = true
}

func (r *TrzszRelay) resetToStandby(status int32) {
	if !r.relayStatus.CompareAndSwap(status, kRelayStandBy) {
		return
	}
	if listener := r.tunnelListener.Load(); listener != nil {
		(*listener).Close()
		r.tunnelListener.Store(nil)
	}
	if t := r.tunnelRelay.Load(); t != nil {
		t.relay.Store(nil)
		r.tunnelRelay.Store(nil)
	}
	r.tunnelConnected.Store(false)
	tmuxRefreshClient()
}

func (r *TrzszRelay) wrapInput() {
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
				status, ok = r.addHandshakeBuffer(r.stdinBuffer, buf, false)
				if ok {
					continue
				}
			}

			r.osStdinChan <- buf

			if status == kRelayTransferring {
				if len(buf) == 1 && buf[0] == '\x03' { // `ctrl + c` to stop
					r.resetToStandby(kRelayTransferring)
				} else if bytes.Contains(buf, []byte("#EXIT:")) { // transfer exit
					r.resetToStandby(kRelayTransferring)
				} else if bytes.Contains(buf, []byte("#FAIL:")) || bytes.Contains(buf, []byte("#fail:")) { // transfer error
					r.resetToStandby(kRelayTransferring)
				}
			}
		}
		if err == io.EOF {
			if isRunningOnWindows() {
				r.osStdinChan <- []byte{0x1A} // ctrl + z
				continue
			}
			break
		}
	}
}

func (r *TrzszRelay) wrapOutput() {
	defer close(r.osStdoutChan)
	if r.bypassTmuxChan != r.osStdoutChan {
		defer close(r.bypassTmuxChan)
	}
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
				status, ok = r.addHandshakeBuffer(r.stdoutBuffer, buf, false)
				if ok {
					continue
				}
			}

			if status == kRelayTransferring {
				r.bypassTmuxChan <- buf

				if bytes.Contains(buf, []byte("#EXIT:")) { // transfer exit
					r.resetToStandby(kRelayTransferring)
				} else if bytes.Contains(buf, []byte("#FAIL:")) || bytes.Contains(buf, []byte("#fail:")) { // transfer error
					r.resetToStandby(kRelayTransferring)
				}
				continue
			}

			var trigger *trzszTrigger
			buf, trigger = detector.detectTrzsz(buf, r.tunnelConnector != nil)
			if trigger != nil {
				r.relayStatus.Store(kRelayHandshaking) // store status before send to client
				r.trigger = trigger
				buf = r.listenForTunnel(buf)
				go r.handshake()
			}

			r.osStdoutChan <- buf
		}
		if err == io.EOF {
			break
		}
	}
}

func (t *tunnelRelay) wrapInput() {
	defer close(t.clientBufChan)
	for {
		buffer := make([]byte, 32*1024)
		n, err := t.clientConn.Read(buffer)
		if n > 0 {
			buf := buffer[:n]
			if t.logger != nil {
				t.logger.writeTraceLog(buf, "tunin")
			}

			if r := t.relay.Load(); r != nil {
				status := r.relayStatus.Load()
				if status == kRelayHandshaking {
					var ok bool
					status, ok = r.addHandshakeBuffer(r.stdinBuffer, buf, true)
					if ok {
						continue
					}
				}
				if status == kRelayTransferring {
					if bytes.Contains(buf, []byte("#EXIT:")) { // transfer exit
						r.resetToStandby(kRelayTransferring)
					} else if bytes.Contains(buf, []byte("#FAIL:")) || bytes.Contains(buf, []byte("#fail:")) { // transfer error
						r.resetToStandby(kRelayTransferring)
					}
				}
			}

			t.clientBufChan <- buf
		}
		if err == io.EOF {
			for t.relay.Load() != nil { // wait for reset
				time.Sleep(50 * time.Millisecond)
			}
			break
		}
	}
}

func (t *tunnelRelay) wrapOutput() {
	defer close(t.serverBufChan)
	for {
		buffer := make([]byte, 32*1024)
		n, err := t.serverConn.Read(buffer)
		if n > 0 {
			buf := buffer[:n]
			if t.logger != nil {
				buf = t.logger.writeTraceLog(buf, "tunout")
			}

			if r := t.relay.Load(); r != nil {
				status := r.relayStatus.Load()
				if status == kRelayHandshaking {
					var ok bool
					status, ok = r.addHandshakeBuffer(r.stdoutBuffer, buf, true)
					if ok {
						continue
					}
				}

				if status == kRelayTransferring {
					if bytes.Contains(buf, []byte("#EXIT:")) { // transfer exit
						r.resetToStandby(kRelayTransferring)
					} else if bytes.Contains(buf, []byte("#FAIL:")) || bytes.Contains(buf, []byte("#fail:")) { // transfer error
						r.resetToStandby(kRelayTransferring)
					}
				}
			}

			t.serverBufChan <- buf
		}
		if err == io.EOF {
			for t.relay.Load() != nil { // wait for reset
				time.Sleep(50 * time.Millisecond)
			}
			break
		}
	}
}

// NewTrzszRelay create a TrzszRelay to support trzsz through a jump server.
//
// ┌────────┐   ClientIn   ┌────────────┐   ServerIn   ┌────────┐
// │        ├─────────────►│            ├─────────────►│        │
// │ Client │              │ TrzszRelay │              │ Server │
// │        │◄─────────────┤            │◄─────────────┤        │
// └────────┘   ClientOut  └────────────┘   ServerOut  └────────┘
func NewTrzszRelay(clientIn io.Reader, clientOut io.WriteCloser,
	serverIn io.WriteCloser, serverOut io.Reader, options TrzszOptions) *TrzszRelay {

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

	bypassTmuxChan := osStdoutChan
	tmuxMode, bypassTmuxOut, tmuxPaneWidth, _ := checkTmux()
	if tmuxMode == tmuxNormalMode {
		bypassTmuxChan = make(chan []byte, 10)
		go func() {
			for buffer := range bypassTmuxChan {
				if logger != nil {
					logger.writeTraceLog(buffer, "tocli")
				}
				_ = writeAll(bypassTmuxOut, buffer)
			}
		}()
	}

	relay := &TrzszRelay{
		tmuxMode:       tmuxMode,
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

	return relay
}
