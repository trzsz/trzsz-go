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
	"time"
)

type TrzszBuffer struct {
	bufCh   chan []byte
	stopCh  chan bool
	nextBuf []byte
	nextIdx int
	readBuf *bytes.Buffer
}

func NewTrzszBuffer() *TrzszBuffer {
	return &TrzszBuffer{make(chan []byte, 10), make(chan bool, 1), nil, 0, new(bytes.Buffer)}
}

func (b *TrzszBuffer) addBuffer(buf []byte) {
	b.bufCh <- buf
}

func (b *TrzszBuffer) stopBuffer() {
	select {
	case b.stopCh <- true:
	default:
	}
}

func (b *TrzszBuffer) drainBuffer() {
	for {
		select {
		case <-b.bufCh:
		default:
			return
		}
	}
}

func (b *TrzszBuffer) nextBuffer(timeout <-chan time.Time) ([]byte, error) {
	if b.nextBuf != nil && b.nextIdx < len(b.nextBuf) {
		return b.nextBuf[b.nextIdx:], nil
	}
	select {
	case b.nextBuf = <-b.bufCh:
		b.nextIdx = 0
		return b.nextBuf, nil
	case <-b.stopCh:
		return nil, newTrzszError("Stopped")
	case <-timeout:
		return nil, newTrzszError("Receive data timeout")
	}
}

func (b *TrzszBuffer) readLine(timeout <-chan time.Time) ([]byte, error) {
	b.readBuf.Reset()
	for {
		buf, err := b.nextBuffer(timeout)
		if err != nil {
			return nil, err
		}
		newLineIdx := bytes.IndexByte(buf, '\n')
		if newLineIdx >= 0 {
			b.nextIdx += newLineIdx + 1 // +1 to ignroe the '\n'
			buf = buf[0:newLineIdx]
		} else {
			b.nextIdx += len(buf)
		}
		if bytes.IndexByte(buf, '\x03') >= 0 { // `ctrl + c` to interrupt
			return nil, newTrzszError("Interrupted")
		}
		b.readBuf.Write(buf)
		if newLineIdx >= 0 {
			return b.readBuf.Bytes(), nil
		}
	}
}

func (b *TrzszBuffer) readBinary(size int, timeout <-chan time.Time) ([]byte, error) {
	b.readBuf.Reset()
	if b.readBuf.Cap() < size {
		b.readBuf.Grow(size)
	}
	for b.readBuf.Len() < size {
		buf, err := b.nextBuffer(timeout)
		if err != nil {
			return nil, err
		}
		left := size - b.readBuf.Len()
		if len(buf) > left {
			b.nextIdx += left
			buf = buf[0:left]
		} else {
			b.nextIdx += len(buf)
		}
		b.readBuf.Write(buf)
	}
	return b.readBuf.Bytes(), nil
}

func isVT100End(b byte) bool {
	if 'a' <= b && b <= 'z' {
		return true
	}
	if 'A' <= b && b <= 'Z' {
		return true
	}
	return false
}

func isTrzszLetter(b byte) bool {
	if 'a' <= b && b <= 'z' {
		return true
	}
	if 'A' <= b && b <= 'Z' {
		return true
	}
	if '0' <= b && b <= '9' {
		return true
	}
	if b == '#' || b == ':' || b == '+' || b == '/' || b == '=' {
		return true
	}
	return false
}

func (b *TrzszBuffer) readLineOnWindows(timeout <-chan time.Time) ([]byte, error) {
	b.readBuf.Reset()
	skipVT100 := false
	for {
		buf, err := b.nextBuffer(timeout)
		if err != nil {
			return nil, err
		}
		newLineIdx := bytes.IndexByte(buf, '!')
		if newLineIdx >= 0 {
			b.nextIdx += newLineIdx + 1 // +1 to ignroe the newline
			buf = buf[0:newLineIdx]
		} else {
			b.nextIdx += len(buf)
		}
		if bytes.IndexByte(buf, '\x03') >= 0 { // `ctrl + c` to interrupt
			return nil, newTrzszError("Interrupted")
		}
		for _, c := range buf {
			if skipVT100 {
				if isVT100End(c) {
					skipVT100 = false
				}
			} else if c == '\x1b' {
				skipVT100 = true
			} else if isTrzszLetter(c) {
				b.readBuf.WriteByte(c)
			}
		}
		if newLineIdx >= 0 {
			return b.readBuf.Bytes(), nil
		}
	}
}
