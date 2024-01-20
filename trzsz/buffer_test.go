/*
MIT License

Copyright (c) 2022-2024 The Trzsz Authors.

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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBufferReadLine(t *testing.T) {
	assert := assert.New(t)
	tb := newTrzszBuffer()
	assertReadSucc := func(data []byte) {
		t.Helper()
		line, err := tb.readLine(false, nil)
		assert.Nil(err)
		assert.Equal(data, line)
	}

	// read line after add buffer
	tb.addBuffer([]byte("test message\n"))
	assertReadSucc([]byte("test message"))

	// read line before add buffer
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		beginTime := time.Now()
		assertReadSucc([]byte("test message"))
		assert.GreaterOrEqual(time.Since(beginTime), 100*time.Millisecond)
		wg.Done()
	}()
	time.Sleep(100 * time.Millisecond)
	tb.addBuffer([]byte("test message\n"))
	wg.Wait()

	// read line from two buffer
	tb.addBuffer([]byte("test "))
	time.Sleep(100 * time.Millisecond)
	tb.addBuffer([]byte("message\n"))
	assertReadSucc([]byte("test message"))

	// read lines from mix buffer
	tb.addBuffer([]byte("test\nmessage\n"))
	assertReadSucc([]byte("test"))
	assertReadSucc([]byte("message"))

	// read multiple lines
	tb.addBuffer([]byte("1test message1\n"))
	tb.addBuffer([]byte("2test message2\n"))
	assertReadSucc([]byte("1test message1"))
	assertReadSucc([]byte("2test message2"))

	// read a long line
	partA := bytes.Repeat([]byte("A"), 100)
	partB := bytes.Repeat([]byte("B"), 100)
	partC := bytes.Repeat([]byte("C"), 100)
	tb.addBuffer(partA)
	tb.addBuffer(partB)
	tb.addBuffer(partC)
	time.Sleep(100 * time.Millisecond)
	tb.addBuffer([]byte("D\n"))
	assertReadSucc(append(append(append(partA, partB...), partC...), 'D'))
}

func TestBufferReadJunk(t *testing.T) {
	assert := assert.New(t)
	tb := newTrzszBuffer()
	assertReadSucc := func(data []byte) {
		t.Helper()
		line, err := tb.readLine(true, nil)
		assert.Nil(err)
		assert.Equal(data, line)
	}

	// lines without junk
	tb.addBuffer([]byte("test\nmessage\n"))
	assertReadSucc([]byte("test"))
	assertReadSucc([]byte("message"))

	// lines with junks
	tb.addBuffer([]byte("test\r\n message\n"))
	assertReadSucc([]byte("test message"))
	tb.addBuffer([]byte("test\r\n"))
	tb.addBuffer([]byte(" test\r\n"))
	tb.addBuffer([]byte(" test\r\n"))
	time.Sleep(100 * time.Millisecond)
	tb.addBuffer([]byte(" message\n"))
	assertReadSucc([]byte("test test test message"))
}

func TestBufferReadBinary(t *testing.T) {
	assert := assert.New(t)
	tb := newTrzszBuffer()
	assertReadSucc := func(size int, data []byte) {
		t.Helper()
		buf, err := tb.readBinary(size, nil)
		assert.Nil(err)
		assert.Equal(data, buf)
	}

	// read binary after add buffer
	data := make([]byte, 300)
	for i := 0; i < len(data); i++ {
		data[i] = byte(i & 0xff)
	}
	tb.addBuffer(data)
	assertReadSucc(100, data[:100])
	assertReadSucc(200, data[100:])

	// read binary before add buffer
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		beginTime := time.Now()
		assertReadSucc(100, data[:100])
		assertReadSucc(200, data[100:])
		assert.GreaterOrEqual(time.Since(beginTime), 100*time.Millisecond)
		wg.Done()
	}()
	time.Sleep(100 * time.Millisecond)
	tb.addBuffer(data)
	wg.Wait()

	// read binary from two buffer
	tb.addBuffer(data[:100])
	time.Sleep(100 * time.Millisecond)
	tb.addBuffer(data[100:200])
	assertReadSucc(200, data[:200])
	tb.addBuffer(data[:200])
	assertReadSucc(200, data[:200])
}

func TestBufferReadOnWin(t *testing.T) {
	assert := assert.New(t)
	tb := newTrzszBuffer()
	assertReadSucc := func(data []byte) {
		t.Helper()
		line, err := tb.readLineOnWindows(nil)
		assert.Nil(err)
		assert.Equal(data, line)
	}

	tb.addBuffer([]byte("#DATA:test message\t1+/=!"))
	assertReadSucc([]byte("#DATA:testmessage1+/="))

	tb.addBuffer([]byte("\x1b[01;32mABC\x1b[01;34mdef!\x1b[00m"))
	assertReadSucc([]byte("ABCdef"))

	tb.addBuffer(append(append([]byte("\x1b[29CAAA\x1b[KBBB"), bytes.Repeat([]byte("C"), 200)...), '!'))
	assertReadSucc(append([]byte("AAABBB"), bytes.Repeat([]byte("C"), 200)...))

	tb.addBuffer([]byte("\r\n\x1b[90C"))
	tb.addBuffer([]byte("#SUCC:eJzy8XR29Qt21TMCBAAA//8"))
	tb.addBuffer([]byte("\r\n\x1b[25;119H8MnwJk!"))
	assertReadSucc([]byte("#SUCC:eJzy8XR29Qt21TMCBAAA//8MnwJk"))

	tb.addBuffer([]byte("\x1b[238X\x1b[238C\x1b[60;198H\x1b[?25h\x1b[H!\x1b[60;198H"))
	tb.addBuffer([]byte("\x1b[?25l#SUCC:65536! \x08\x1b[?25h\x1b[?25lSoft\x1b[!pReset!"))
	assertReadSucc([]byte("#SUCC:65536"))
	assertReadSucc([]byte("SoftReset"))

	tb.addBuffer([]byte("U4bz/o\x08\x1b[?25h\x1b[?25l\x1b[Hp\x1b[60;238H\x1b[?25h\x1b[?25l\r\np7bu8!"))
	assertReadSucc([]byte("U4bz/op7bu8"))

	tb.addBuffer([]byte("hnWwqzbHU\x1b[199X\x1b[199C\x1b[60;40H\x1b[?25h\x1b[?25lUUcczgV!"))
	assertReadSucc([]byte("hnWwqzbHUUUcczgV"))

	tb.addBuffer([]byte("8yOf8lh\x08\x1b[?25h\x1b[?25l\x1b[Hb\x1b[60;238H\x1b[?25h\x1b[?25l\r\ni2Czew!"))
	assertReadSucc([]byte("8yOf8lhi2Czew"))

	tb.addBuffer([]byte("BFjn6\x1b[30;1H\x1b[?25l\n\x1b[29;120H6jEF8aG!"))
	assertReadSucc([]byte("BFjn6jEF8aG"))

	tb.addBuffer([]byte("test1!\ntest2!\n"))
	assertReadSucc([]byte("test1"))
	assertReadSucc([]byte("test2"))

	tb.addBuffer([]byte("test\x03message"))
	_, err := tb.readLineOnWindows(nil)
	assert.EqualError(err, "Interrupted")

	tb.addBuffer([]byte("atestmessage!\n"))
	assertReadSucc([]byte("atestmessage"))
	assert.Nil(tb.popBuffer())
}

func TestBufferOthers(t *testing.T) {
	assert := assert.New(t)
	tb := newTrzszBuffer()
	assertReadSucc := func(data []byte) {
		t.Helper()
		line, err := tb.readLine(false, nil)
		assert.Nil(err)
		assert.Equal(data, line)
	}
	assertReadFail := func(msg string) {
		t.Helper()
		_, err := tb.readLine(false, nil)
		assert.EqualError(err, msg)
	}

	// read timeout
	timeout := time.NewTimer(100 * time.Millisecond).C
	beginTime := time.Now()
	_, err := tb.readLine(false, timeout)
	assert.EqualError(err, "Receive data timeout")
	assert.GreaterOrEqual(time.Since(beginTime), 100*time.Millisecond)

	// new timeout
	go func() {
		time.Sleep(50 * time.Millisecond)
		tb.setNewTimeout(time.NewTimer(200 * time.Millisecond).C)
		time.Sleep(100 * time.Millisecond)
		tb.addBuffer([]byte("test message\n"))
	}()
	line, err := tb.readLine(false, time.NewTimer(100*time.Millisecond).C)
	assert.Nil(err)
	assert.Equal([]byte("test message"), line)

	// read line interrupted
	tb.addBuffer([]byte("test\x03message\n"))
	assertReadFail("Interrupted")
	tb.addBuffer([]byte("test\nmessage\x03\n"))
	assertReadSucc([]byte("test"))
	assertReadFail("Interrupted")

	// stop while reading
	tb.stopBuffer()
	assertReadFail("Stopped")

	// drain buffer
	tb.addBuffer([]byte("old message\n"))
	tb.drainBuffer()
	tb.addBuffer([]byte("new message\n"))
	assertReadSucc([]byte("new message"))

	// pop buffer
	tb.addBuffer([]byte("test\nmessage\n"))
	tb.addBuffer([]byte("message without newline"))
	tb.addBuffer([]byte("message with newline\n"))
	assertReadSucc([]byte("test"))
	assert.Equal([]byte("message\n"), tb.popBuffer())
	assert.Equal([]byte("message without newline"), tb.popBuffer())
	assert.Equal([]byte("message with newline\n"), tb.popBuffer())
	assert.Nil(tb.popBuffer())
}
