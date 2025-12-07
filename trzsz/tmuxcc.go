/*
MIT License

Copyright (c) 2022-2025 The Trzsz Authors.

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
	"fmt"
	"strconv"
	"time"
)

func encodeTmuxOutput(prefix string, output []byte) []byte {
	buffer := bytes.NewBuffer(make([]byte, 0, len(prefix)+len(output)<<2+2))
	buffer.Write([]byte(prefix))
	for _, b := range output {
		if b < ' ' || b == '\\' || b > '~' {
			fmt.Fprintf(buffer, "\\%03o", b)
		} else {
			buffer.WriteByte(b)
		}
	}
	buffer.Write([]byte("\r\n"))
	return buffer.Bytes()
}

func canSendAsLiteralCharacter(b byte) bool {
	if b < 0x21 || b >= 0x7f {
		return false
	}
	switch b {
	case '"', '#', '\'', ';', '\\', '{', '}':
		return false
	default:
		return true
	}
}

func convertToHexStrings(data []byte) []byte {
	var buf bytes.Buffer
	for i, b := range data {
		if i > 0 {
			buf.WriteByte(' ')
		}
		if b == 0 {
			buf.WriteString("C-Space")
		} else {
			buf.WriteString(fmt.Sprintf("%#x", b))
		}
	}
	return buf.Bytes()
}

func (t *trzszTransfer) tmuxccSendKeys(data []byte, asLiteralCharacters bool) error {
	if !asLiteralCharacters {
		data = convertToHexStrings(data)
	}
	var buf bytes.Buffer
	buf.Grow(9 + len(t.tmuxPaneID) + len(data))
	buf.WriteString("send")
	if asLiteralCharacters {
		buf.WriteString(" -lt")
	} else {
		buf.WriteString(" -t")
	}
	buf.Write(t.tmuxPaneID)
	buf.Write(data)
	buf.WriteByte('\r')
	t.writeTraceLog(buf.Bytes(), "ttosvr")

	t.tmuxInputMutex.Lock()
	defer t.tmuxInputMutex.Unlock()
	t.tmuxInputAckChan <- true
	t.tmuxAckWaitGroup.Add(1)
	err := writeAll(t.writer, buf.Bytes())
	return err
}

func (t *trzszTransfer) tmuxccWriteAll(data []byte) error {
	if len(data) == 0 {
		return nil
	}

	const kMaxLiteralCharacters = 1000                  // commands larger than 1024 bytes crash tmux 1.8.
	const kMaxHexCharacters = kMaxLiteralCharacters / 8 // len(' C-Space') = 8

	start := 0
	asLiteralCharacters := canSendAsLiteralCharacter(data[0])
	for i := 1; i < len(data); i++ {
		currentAsLiteral := canSendAsLiteralCharacter(data[i])

		if asLiteralCharacters != currentAsLiteral {
			if err := t.tmuxccSendKeys(data[start:i], asLiteralCharacters); err != nil {
				return err
			}
			start = i
			asLiteralCharacters = currentAsLiteral
			continue
		}

		currentLength := i - start
		if asLiteralCharacters && currentLength >= kMaxLiteralCharacters || !asLiteralCharacters && currentLength >= kMaxHexCharacters {
			if err := t.tmuxccSendKeys(data[start:i], asLiteralCharacters); err != nil {
				return err
			}
			start = i
		}
	}

	if start < len(data) {
		if err := t.tmuxccSendKeys(data[start:], asLiteralCharacters); err != nil {
			return err
		}
	}

	return nil
}

func (t *trzszTransfer) tmuxccAddBuffer(data []byte) {
	var buf bytes.Buffer
	buf.Grow(len(data))
	i, length := 0, len(data)
	for i < length {
		c := data[i]

		if c < ' ' {
			i++
			continue
		}

		if c == '\\' {
			if i+3 >= length {
				t.buffer.addBuffer(fmt.Appendf(nil, "|invalid octal in tmux cc output: %s", encodeBytes(data[max(0, i-10):min(len(data), i+10)])))
			}
			a, b, c := data[i+1], data[i+2], data[i+3]
			if a < '0' || a > '7' || b < '0' || b > '7' || c < '0' || c > '7' {
				t.buffer.addBuffer(fmt.Appendf(nil, "|invalid octal in tmux cc output: %s", encodeBytes(data[max(0, i-10):min(len(data), i+10)])))
			}
			buf.WriteByte((a-'0')*64 + (b-'0')*8 + (c - '0'))
			i += 4
			continue
		}

		buf.WriteByte(c)
		i++
	}
	if t.stopped.Load() {
		return
	}
	t.writeTraceLog(buf.Bytes(), "rctbuf")
	t.buffer.addBuffer(buf.Bytes())
	t.lastInputTime.Store(time.Now().UnixMilli())
}

func (t *trzszTransfer) tmuxccClientCommand(cmd []byte, promptInput bool) []byte {
	var input []byte
	if len(cmd) == 0 {
		return input
	}

	if bytes.HasPrefix(cmd, []byte("send ")) { // user input
		pos := bytes.IndexByte(cmd[5:], ' ')
		if pos > 0 && bytes.HasPrefix(cmd[5+pos:], t.tmuxPaneID) { // current pane
			data := cmd[5+pos+len(t.tmuxPaneID):]
			if promptInput {
				if string(cmd[5:5+pos]) == "-lt" {
					input = data
				} else {
					for hex := range bytes.FieldsSeq(data) {
						if bytes.HasPrefix(hex, []byte("0x")) {
							if char, err := strconv.ParseInt(string(hex[2:]), 16, 32); err == nil {
								input = append(input, byte(char))
							}
						}
					}
				}
			} else if bytes.Equal(data, []byte("0x3")) { // ctrl + c
				t.handleInputCtrlC()
			}
			// ack and don't send to the server
			now := time.Now().Unix()
			data = fmt.Appendf(nil, "%%begin %d 1 1\r\n%%end %d 1 1\r\n", now, now)
			t.writeTraceLog(data, "totcli")
			_ = writeAll(t.trzszFilter.clientOut, data)
			return input
		}
		// other pane send to the server
	}
	// other command send to the server

	t.tmuxInputMutex.Lock()
	defer t.tmuxInputMutex.Unlock()
	t.tmuxInputAckChan <- false
	_ = writeAll(t.trzszFilter.serverIn, append(cmd, '\r'))
	return input
}

func (t *trzszTransfer) handleClientInput(buf []byte) {
	if len(t.tmuxPaneID) == 0 {
		if promptPipe := t.promptPipeWriter.Load(); promptPipe != nil {
			t.transformPromptInput(promptPipe, buf)
			return
		}
		if len(buf) == 1 && buf[0] == '\x03' { // ctrl + c
			t.handleInputCtrlC()
		}
		return
	}

	t.writeTraceLog(buf, "tinput")
	promptPipe := t.promptPipeWriter.Load()
	promptInput := promptPipe != nil

	data := buf
	if len(t.tmuxInputBuf) > 0 {
		data = append(t.tmuxInputBuf, buf...)
	}
	parts := bytes.Split(data, []byte{'\r'})

	var input []byte
	n := len(parts) - 1
	for i := range n {
		for cmd := range bytes.SplitSeq(parts[i], []byte{';'}) {
			in := t.tmuxccClientCommand(bytes.TrimSpace(cmd), promptInput)
			if promptInput && len(in) > 0 {
				input = append(input, in...)
			}
		}
	}

	if promptInput {
		t.transformPromptInput(promptPipe, input)
	}

	last := parts[n]
	if len(last) > 0 {
		t.tmuxInputBuf = append([]byte(nil), last...)
	} else {
		t.tmuxInputBuf = nil
	}
}

func (t *trzszTransfer) isAckForTransfer(buf []byte) bool {
	if t.tmuxBeginBuffer == nil { // unpaired %begin %end
		return false
	}

	var flag uint64
	parts := bytes.Fields(buf)
	if len(parts) == 3 {
		flag = 1 // default 1 in old tmux
	} else if len(parts) > 3 {
		var err error
		flag, err = strconv.ParseUint(string(parts[3]), 10, 64)
		if err != nil { // invalid
			return false
		}
	}
	if flag&1 == 0 { // not ack for client input
		return false
	}

	select {
	case ack := <-t.tmuxInputAckChan:
		return ack
	default:
		return false
	}
}

func (t *trzszTransfer) tmuxccServerOutput(buf []byte) {
	if len(buf) == 0 {
		return
	}

	// %begin %end
	if bytes.HasPrefix(buf, []byte("%begin ")) {
		t.tmuxBeginBuffer = buf
		return
	}
	if bytes.HasPrefix(buf, []byte("%end ")) || bytes.HasPrefix(buf, []byte("%error ")) {
		if t.isAckForTransfer(buf) { // ignore acks of current pane
			t.tmuxAckWaitGroup.Done()
			t.tmuxBeginBuffer = nil
			return
		}
		// ack for other panes
		data := t.tmuxBeginBuffer
		if len(data) > 0 {
			data = append(data, '\n')
		}
		data = append(data, buf...)
		data = append(data, '\n')
		t.writeTraceLog(data, "totcli")
		_ = writeAll(t.trzszFilter.clientOut, data)
		t.tmuxBeginBuffer = nil
		return
	}
	if t.tmuxBeginBuffer != nil {
		t.tmuxBeginBuffer = append(t.tmuxBeginBuffer, '\n')
		t.tmuxBeginBuffer = append(t.tmuxBeginBuffer, buf...)
		return
	}

	// %output %extended-output
	var output []byte
	if bytes.HasPrefix(buf, []byte("%output ")) {
		output = buf[7:]
	} else if bytes.HasPrefix(buf, []byte("%extended-output ")) {
		output = buf[16:]
	}
	if output != nil && bytes.HasPrefix(output, t.tmuxPaneID) { // current pane
		if t.tunnelConnected { // ignore current pane output
			t.writeTraceLog(buf, "igtout")
			return
		}

		output = output[len(t.tmuxPaneID):]
		if buf[1] == 'e' { // %extended-output
			if pos := bytes.IndexByte(output, ':'); pos >= 0 {
				output = output[pos+2:]
			}
		}
		t.tmuxccAddBuffer(output)
		return
	}

	// other commands or other panes
	data := append(buf, '\n')
	t.writeTraceLog(data, "totcli")
	_ = writeAll(t.trzszFilter.clientOut, data)
}

func (t *trzszTransfer) handleTmuxOutput(buf []byte) {
	t.writeTraceLog(buf, "rctout")

	data := buf
	if len(t.tmuxOutputBuf) > 0 {
		data = append(t.tmuxOutputBuf, buf...)
	}
	parts := bytes.Split(data, []byte{'\n'})

	n := len(parts) - 1
	for i := range n {
		t.tmuxccServerOutput(parts[i])
	}

	last := parts[n]
	if len(last) > 0 {
		t.tmuxOutputBuf = last // the next read has used a new buffer
	} else {
		t.tmuxOutputBuf = nil
	}
}
