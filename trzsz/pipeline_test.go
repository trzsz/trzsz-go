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
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func newPipelineContext() *pipelineContext {
	c, cancel := context.WithCancelCause(context.Background())
	return &pipelineContext{c, cancel, make(chan struct{}, 1)}
}

func assertClosed(t *testing.T, ch any) {
	t.Helper()
	if c, ok := ch.(<-chan []byte); ok {
		for {
			select {
			case _, ok := <-c:
				if !ok {
					return
				}
			case <-time.After(time.Second):
				assert.Fail(t, "Channel not closed")
			}
		}
	}
	if c, ok := ch.(<-chan trzszData); ok {
		for {
			select {
			case _, ok := <-c:
				if !ok {
					return
				}
			case <-time.After(time.Second):
				assert.Fail(t, "Channel not closed")
			}
		}
	}
	assert.Fail(t, "Unknown channel type")
}

func assertChannel(t *testing.T, expected any, ch any) {
	t.Helper()
	if c, ok := ch.(<-chan []byte); ok {
		select {
		case actual, ok := <-c:
			assert.True(t, ok)
			assert.Equal(t, expected, actual)
		case <-time.After(time.Second):
			assert.Fail(t, "Channel timeout")
		}
		return
	}
	if c, ok := ch.(<-chan trzszData); ok {
		select {
		case actual, ok := <-c:
			assert.True(t, ok)
			assert.Equal(t, expected, actual)
		case <-time.After(time.Second):
			assert.Fail(t, "Channel timeout")
		}
		return
	}
	if c, ok := ch.(chan trzszData); ok {
		select {
		case actual, ok := <-c:
			assert.True(t, ok)
			assert.Equal(t, expected, actual)
		case <-time.After(time.Second):
			assert.Fail(t, "Channel timeout")
		}
		return
	}
	assert.Fail(t, "Unknown channel type")
}

func TestBase64Reader(t *testing.T) {
	var wg sync.WaitGroup
	assert := assert.New(t)
	dataChan := make(chan []byte, 100)
	base64Reader := newBase64Reader(newPipelineContext(), dataChan)

	// block until new data
	wg.Add(1)
	beginTime := time.Now()
	go func() {
		buf := make([]byte, 2)
		n, err := base64Reader.Read(buf)
		assert.Nil(err)
		assert.Equal(2, n)
		assert.Equal([]byte{1, 3}, buf)
		assert.GreaterOrEqual(time.Since(beginTime), 100*time.Millisecond)
		wg.Done()
	}()
	time.Sleep(100 * time.Millisecond)
	dataChan <- []byte{1, 3}
	wg.Wait()

	// read multiple times from one data
	dataChan <- []byte{1, 2, 3, 4, 5, 6}
	assertReadSucc := func(data []byte) {
		t.Helper()
		buf := make([]byte, len(data))
		n, err := base64Reader.Read(buf)
		assert.Nil(err)
		assert.Equal(len(data), n)
		assert.Equal(data, buf)
	}
	assertReadSucc([]byte{})
	assertReadSucc([]byte{1})
	assertReadSucc([]byte{2, 3, 4})
	assertReadSucc([]byte{5, 6})

	// not enough data to read
	dataChan <- []byte{7, 8, 9}
	buf := make([]byte, 10)
	n, err := base64Reader.Read(buf)
	assert.Nil(err)
	assert.Equal(3, n)
	assert.Equal([]byte{7, 8, 9}, buf[:n])

	// test cancel
	base64Reader.ctx.cancel(nil)
	_, err = base64Reader.Read(buf)
	assert.NotNil(err)
	assert.ErrorIs(err, context.Canceled)
	base64Reader.ctx = newPipelineContext() // reset context

	// test end of file
	dataChan <- []byte{10, 11}
	close(dataChan)
	assertReadSucc([]byte{10, 11})
	for i := 0; i < 3; i++ {
		n, err = base64Reader.Read(buf)
		assert.Equal(0, n)
		assert.ErrorIs(err, io.EOF)
	}
}

func TestBase64Writer(t *testing.T) {
	assert := assert.New(t)
	transfer := newTransfer(nil, nil, false, nil)
	transfer.bufferSize.Store(4)
	dataChan := make(chan trzszData, 3)
	base64Writer := newBase64Writer(transfer, newPipelineContext(), dataChan)
	assertWriteSucc := func(data []byte) {
		t.Helper()
		n, err := base64Writer.Write(data)
		assert.Equal(len(data), n)
		assert.Nil(err)
	}

	// buffering
	assertWriteSucc([]byte{})
	assertWriteSucc([]byte("A"))
	assertWriteSucc([]byte("BC"))
	time.Sleep(100 * time.Millisecond)
	assert.Empty(dataChan)

	// buffer just complete
	assertWriteSucc([]byte("D"))
	assertChannel(t, trzszData{4, []byte("#DATA:ABCD\n")}, dataChan)

	// buffer complete and more data, and cancel
	assertWriteSucc([]byte("ABCD1234abcdZZ"))
	base64Writer.ctx.cancel(nil)
	n, err := base64Writer.Write([]byte("ZZ"))
	assert.Equal(0, n)
	assert.ErrorIs(err, context.Canceled)
	base64Writer.ctx = newPipelineContext()                                            // reset context
	base64Writer.buffer = bytes.NewBuffer(make([]byte, 0, transfer.bufferSize.Load())) // reset buffer
	assertChannel(t, trzszData{4, []byte("#DATA:ABCD\n")}, dataChan)
	assertChannel(t, trzszData{4, []byte("#DATA:1234\n")}, dataChan)
	assertChannel(t, trzszData{4, []byte("#DATA:abcd\n")}, dataChan)
	assert.Empty(dataChan)

	// change buffer size
	transfer.bufferSize.Store(5)
	assertWriteSucc([]byte("XY"))
	assert.Empty(dataChan)
	assertWriteSucc([]byte("MN"))
	assertChannel(t, trzszData{4, []byte("#DATA:XYMN\n")}, dataChan)
	assertWriteSucc([]byte("ABCDEF"))
	assertChannel(t, trzszData{5, []byte("#DATA:ABCDE\n")}, dataChan)

	// close writer
	assertWriteSucc([]byte("GH"))
	assert.Empty(dataChan)
	base64Writer.Close()
	assertChannel(t, trzszData{3, []byte("#DATA:FGH\n")}, dataChan)
	assertChannel(t, trzszData{0, []byte("#DATA:\n")}, dataChan)
}

func TestPipelineCalculateMD5(t *testing.T) {
	assert := assert.New(t)
	transfer := newTransfer(nil, nil, false, nil)
	sourceChan := make(chan []byte, 10)
	digestChan := transfer.pipelineCalculateMD5(newPipelineContext(), sourceChan)
	sourceChan <- []byte("123")
	sourceChan <- []byte("456")
	sourceChan <- []byte("abc")
	sourceChan <- []byte("XYZ")
	assert.Empty(digestChan)
	close(sourceChan)
	digest, _ := hex.DecodeString("b0fd1d438fe1c86d176717a0c93bc673")
	assertChannel(t, digest, digestChan)
	assertClosed(t, digestChan)
}

func TestPipelineReadData(t *testing.T) {
	assert := assert.New(t)

	file, err := os.CreateTemp("", "TestPipelineReadData")
	assert.Nil(err)
	defer os.Remove(file.Name())
	_, _ = file.Write(bytes.Repeat([]byte{'A'}, 4096))
	_, _ = file.Write(bytes.Repeat([]byte{'B'}, 4096))
	_, _ = file.Write(bytes.Repeat([]byte{'C'}, 100))
	file.Close()
	f1, err := os.Open(file.Name())
	assert.Nil(err)
	defer f1.Close()

	// read success
	ctx := newPipelineContext()
	transfer := newTransfer(nil, nil, false, nil)
	fileDataChan, md5SourceChan := transfer.pipelineReadData(ctx, f1, 4096*2+100)

	assertChannel(t, bytes.Repeat([]byte{'A'}, 4096), fileDataChan)
	assertChannel(t, bytes.Repeat([]byte{'B'}, 4096), fileDataChan)
	assertChannel(t, bytes.Repeat([]byte{'C'}, 100), fileDataChan)

	assertChannel(t, bytes.Repeat([]byte{'A'}, 4096), md5SourceChan)
	assertChannel(t, bytes.Repeat([]byte{'B'}, 4096), md5SourceChan)
	assertChannel(t, bytes.Repeat([]byte{'C'}, 100), md5SourceChan)

	assert.Nil(ctx.Err())
	assertClosed(t, fileDataChan)
	assertClosed(t, md5SourceChan)

	// size incorrect
	ctx = newPipelineContext()
	f2, err := os.Open(file.Name())
	assert.Nil(err)
	defer f2.Close()
	transfer.pipelineReadData(ctx, f2, 4096*2+300)
	select {
	case <-ctx.Done():
		assert.ErrorIs(ctx.Err(), context.Canceled)
		assert.EqualError(context.Cause(ctx), "File size 8492 but read 8292")
	case <-time.After(time.Second):
		assert.Fail("Context timeout")
	}

	// cancel read
	f3, err := os.Open(file.Name())
	assert.Nil(err)
	defer f3.Close()
	fileDataChan, md5SourceChan = transfer.pipelineReadData(ctx, f2, 4096*2+100)
	assertClosed(t, fileDataChan)
	assertClosed(t, md5SourceChan)
}

func TestPipelineEncodeAndDecode(t *testing.T) {
	assert := assert.New(t)

	transfer := newTransfer(nil, nil, false, nil)
	transfer.bufferSize.Store(4)
	ctx := newPipelineContext()
	srcDataChan := make(chan []byte, 100)
	sendDataChan := transfer.pipelineEncodeData(ctx, srcDataChan)
	recvDataChan := make(chan []byte, 100)
	fileDataChan, md5SourceChan := transfer.pipelineDecodeData(ctx, recvDataChan)

	go func() {
		for data := range sendDataChan {
			recvDataChan <- data.buffer[6 : 6+data.length]
		}
		close(recvDataChan)
	}()

	buf := make([]byte, 0x100)
	for i := 0; i < 0x100; i++ {
		buf[i] = byte(i)
	}
	srcDataChan <- buf
	close(srcDataChan)

	assertChannel(t, buf, fileDataChan)
	assertChannel(t, buf, md5SourceChan)
	assert.Nil(ctx.Err())
}

func TestPipelineEscapeData(t *testing.T) {
	assert := assert.New(t)

	fileDataChan := make(chan []byte, 100)
	transfer := newTransfer(nil, nil, false, nil)
	transfer.bufferSize.Store(4)
	sendDataChan := transfer.pipelineEscapeData(newPipelineContext(), fileDataChan)

	// buffering
	fileDataChan <- []byte{}
	fileDataChan <- []byte("A")
	fileDataChan <- []byte("BC")
	time.Sleep(100 * time.Millisecond)
	assert.Empty(sendDataChan)

	// buffer just complete
	fileDataChan <- []byte("D")
	assertChannel(t, trzszData{4, []byte("#DATA:4\nABCD")}, sendDataChan)

	// buffer complete and more data
	fileDataChan <- []byte("ABCD1234abcdZZ")
	assertChannel(t, trzszData{4, []byte("#DATA:4\nABCD")}, sendDataChan)
	assertChannel(t, trzszData{4, []byte("#DATA:4\n1234")}, sendDataChan)
	assertChannel(t, trzszData{4, []byte("#DATA:4\nabcd")}, sendDataChan)

	// change buffer size
	time.Sleep(100 * time.Millisecond)
	transfer.bufferSize.Store(5)
	fileDataChan <- []byte("ABCDEFGHI")
	assertChannel(t, trzszData{4, []byte("#DATA:4\nZZAB")}, sendDataChan)
	assertChannel(t, trzszData{5, []byte("#DATA:5\nCDEFG")}, sendDataChan)

	// last buffer
	close(fileDataChan)
	assertChannel(t, trzszData{2, []byte("#DATA:2\nHI")}, sendDataChan)
	assertChannel(t, trzszData{0, []byte("#DATA:0\n")}, sendDataChan)
	assertClosed(t, sendDataChan)

	// escape data
	cfgMap := map[string]interface{}{
		"escape_chars": getEscapeChars(true),
	}
	cfgStr, err := json.Marshal(cfgMap)
	assert.Nil(err)
	err = json.Unmarshal([]byte(cfgStr), &transfer.transferConfig)
	assert.Nil(err)
	ctx := newPipelineContext()
	fileDataChan = make(chan []byte, 100)
	sendDataChan = transfer.pipelineEscapeData(ctx, fileDataChan)
	fileDataChan <- []byte{0xee, 0xee, 0xee}
	assertChannel(t, trzszData{5, []byte("#DATA:5\n\xee\xee\xee\xee\xee")}, sendDataChan)
	fileDataChan <- []byte{0x7e, 0x7e}
	assertChannel(t, trzszData{5, []byte("#DATA:5\n\xee\xee\x31\xee\x31")}, sendDataChan)

	// cancel
	fileDataChan <- bytes.Repeat([]byte{0xee}, 4096)
	ctx.cancel(nil)
	assertClosed(t, sendDataChan)
}

func TestPipelineUnescapeData(t *testing.T) {
	assert := assert.New(t)

	recvDataChan := make(chan []byte, 100)
	cfgMap := map[string]interface{}{
		"escape_chars": getEscapeChars(true),
	}
	cfgStr, err := json.Marshal(cfgMap)
	assert.Nil(err)
	transfer := newTransfer(nil, nil, false, nil)
	err = json.Unmarshal([]byte(cfgStr), &transfer.transferConfig)
	assert.Nil(err)
	fileDataChan, md5SourceChan := transfer.pipelineUnescapeData(newPipelineContext(), recvDataChan)

	// no need to escape
	recvDataChan <- []byte("123")
	assertChannel(t, []byte("123"), fileDataChan)
	assertChannel(t, []byte("123"), md5SourceChan)
	recvDataChan <- []byte("ABCDE")
	assertChannel(t, []byte("ABCDE"), fileDataChan)
	assertChannel(t, []byte("ABCDE"), md5SourceChan)
	recvDataChan <- bytes.Repeat([]byte("abc"), 1024)
	assertChannel(t, bytes.Repeat([]byte("abc"), 1024), fileDataChan)
	assertChannel(t, bytes.Repeat([]byte("abc"), 1024), md5SourceChan)

	// escape at the beginning
	recvDataChan <- []byte("\xee\xee\xee\x31ABC")
	assertChannel(t, []byte("\xee\x7eABC"), fileDataChan)
	assertChannel(t, []byte("\xee\x7eABC"), md5SourceChan)

	// escape in the middle
	recvDataChan <- []byte("ABC\xee\xee1\xee\x31ABC")
	assertChannel(t, []byte("ABC\xee1\x7eABC"), fileDataChan)
	assertChannel(t, []byte("ABC\xee1\x7eABC"), md5SourceChan)

	// escape at the end
	recvDataChan <- []byte("ABC\xee\x41\xee\x42")
	assertChannel(t, []byte("ABC\x02\x10"), fileDataChan)
	assertChannel(t, []byte("ABC\x02\x10"), md5SourceChan)

	// escaping across buffers
	recvDataChan <- []byte("ABC\xee\x41\xee")
	recvDataChan <- []byte("\x42DEF")
	assertChannel(t, []byte("ABC\x02"), fileDataChan)
	assertChannel(t, []byte("ABC\x02"), md5SourceChan)
	assertChannel(t, []byte("\x10"), fileDataChan)
	assertChannel(t, []byte("\x10"), md5SourceChan)
	assertChannel(t, []byte("DEF"), fileDataChan)
	assertChannel(t, []byte("DEF"), md5SourceChan)

	// complex escaping
	recvDataChan <- []byte("ABC\xee\xee\xee\xee\xee")
	recvDataChan <- []byte("\xee\xee\xee\xee\xee")
	recvDataChan <- []byte("\xee\xee\xee\xee\xee")
	recvDataChan <- []byte("\xee\xee\xeeDEF\xee\xee")
	recvDataChan <- []byte("\xee\xee\xee\xee\xee")
	recvDataChan <- []byte("\xee\xee\xee\xee\xee")
	recvDataChan <- []byte("G\xee\xee\xee\xee\xee")
	recvDataChan <- []byte("\x31\xee\xee\xee\xee")
	close(recvDataChan)
	assertChannel(t, []byte("ABC"), fileDataChan)
	assertChannel(t, []byte("ABC"), md5SourceChan)
	assertChannel(t, append(bytes.Repeat([]byte("\xee"), 9), 'D'), fileDataChan)
	assertChannel(t, append(bytes.Repeat([]byte("\xee"), 9), 'D'), md5SourceChan)
	assertChannel(t, []byte("EF"), fileDataChan)
	assertChannel(t, []byte("EF"), md5SourceChan)
	assertChannel(t, append(bytes.Repeat([]byte("\xee"), 6), 'G'), fileDataChan)
	assertChannel(t, append(bytes.Repeat([]byte("\xee"), 6), 'G'), md5SourceChan)
	assertChannel(t, []byte("\xee\xee\x7e"), fileDataChan)
	assertChannel(t, []byte("\xee\xee\x7e"), md5SourceChan)
	assertChannel(t, []byte("\xee\xee"), fileDataChan)
	assertChannel(t, []byte("\xee\xee"), md5SourceChan)
}
