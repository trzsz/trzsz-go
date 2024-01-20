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
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func newTransferWithEscape(t *testing.T, transfer *trzszTransfer, escapeAll bool) *trzszTransfer {
	t.Helper()
	cfgMap := map[string]interface{}{
		"escape_chars": getEscapeChars(escapeAll),
	}
	cfgStr, err := json.Marshal(cfgMap)
	assert.Nil(t, err)
	if transfer == nil {
		transfer = newTransfer(nil, nil, false, nil)
	}
	err = json.Unmarshal([]byte(cfgStr), &transfer.transferConfig)
	assert.Nil(t, err)
	return transfer
}

type mockReader struct {
	buf [][]byte
	idx int
}

func (r *mockReader) Read(p []byte) (int, error) {
	if r.idx >= len(r.buf) {
		return 0, io.EOF
	}
	n := copy(p, r.buf[r.idx])
	if n < len(r.buf[r.idx]) {
		r.buf[r.idx] = r.buf[r.idx][n:]
	} else {
		r.idx++
	}
	return n, nil
}

func newMockReader(buf [][]byte) io.Reader {
	return &mockReader{buf, 0}
}

type mockWriter struct {
	buf    bytes.Buffer
	closed bool
}

func (w *mockWriter) Write(p []byte) (int, error) {
	return w.buf.Write(p)
}

func (w *mockWriter) Close() error {
	w.closed = true
	return nil
}

func newPipelineContext() *pipelineContext {
	c, cancel := context.WithCancelCause(context.Background())
	return &pipelineContext{c, cancel, make(chan struct{}, 1)}
}

func assertChannelClosed(t *testing.T, ch any) {
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
				return
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
				return
			}
		}
	}
	if c, ok := ch.(<-chan trzszAck); ok {
		for {
			select {
			case _, ok := <-c:
				if !ok {
					return
				}
			case <-time.After(time.Second):
				assert.Fail(t, "Channel not closed")
				return
			}
		}
	}
	assert.Fail(t, "Unknown channel type")
}

func assertChannelFrontEqual(t *testing.T, expected any, ch any) {
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
	if c, ok := ch.(<-chan trzszAck); ok {
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

func assertChannelFinalEqual(t *testing.T, expected any, ch any) {
	t.Helper()
	if c, ok := ch.(<-chan []byte); ok {
		var actual []byte
	out:
		for {
			select {
			case buf, ok := <-c:
				if !ok {
					break out
				}
				actual = append(actual, buf...)
			case <-time.After(time.Second):
				assert.Fail(t, "Channel timeout")
			}
		}
		assert.Equal(t, expected, actual)
		return
	}
	assert.Fail(t, "Unknown channel type")
}

func TestRecvDataReader(t *testing.T) {
	var wg sync.WaitGroup
	assert := assert.New(t)
	dataChan := make(chan []byte, 100)
	base64Reader := newRecvDataReader(newPipelineContext(), dataChan)

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

func TestSendDataWriter(t *testing.T) {
	assert := assert.New(t)
	transfer := newTransfer(nil, nil, false, nil)
	transfer.bufInitPhase.Store(false)
	transfer.bufferSize.Store(4)
	dataChan := make(chan trzszData, 3)
	base64Writer := newSendDataWriter(transfer, newPipelineContext(), dataChan)
	assertWriteSucc := func(data []byte) {
		t.Helper()
		n, err := base64Writer.Write(data)
		assert.Equal(len(data), n)
		assert.Nil(err)
	}
	newTrzszData := func(data string) trzszData {
		return trzszData{
			data:   []byte(data),
			buffer: []byte(fmt.Sprintf("#DATA:%s\n", data)),
		}
	}

	// buffering
	assertWriteSucc([]byte{})
	assertWriteSucc([]byte("A"))
	assertWriteSucc([]byte("BC"))
	time.Sleep(100 * time.Millisecond)
	assert.Empty(dataChan)

	// buffer just complete
	assertWriteSucc([]byte("D"))
	assertChannelFrontEqual(t, newTrzszData("ABCD"), dataChan)

	// buffer complete and more data, and cancel
	assertWriteSucc([]byte("ABCD1234abcdZZ"))
	base64Writer.ctx.cancel(nil)
	n, err := base64Writer.Write([]byte("ZZ"))
	assert.Equal(0, n)
	assert.ErrorIs(err, context.Canceled)
	base64Writer.ctx = newPipelineContext()                                            // reset context
	base64Writer.buffer = bytes.NewBuffer(make([]byte, 0, transfer.bufferSize.Load())) // reset buffer
	assertChannelFrontEqual(t, newTrzszData("ABCD"), dataChan)
	assertChannelFrontEqual(t, newTrzszData("1234"), dataChan)
	assertChannelFrontEqual(t, newTrzszData("abcd"), dataChan)
	assert.Empty(dataChan)

	// change buffer size
	transfer.bufferSize.Store(5)
	assertWriteSucc([]byte("XY"))
	assert.Empty(dataChan)
	assertWriteSucc([]byte("MN"))
	assertChannelFrontEqual(t, newTrzszData("XYMN"), dataChan)
	assertWriteSucc([]byte("ABCDEF"))
	assertChannelFrontEqual(t, newTrzszData("ABCDE"), dataChan)

	// close writer
	assertWriteSucc([]byte("GH"))
	assert.Empty(dataChan)
	base64Writer.Close()
	assertChannelFrontEqual(t, newTrzszData("FGH"), dataChan)
	assertChannelFrontEqual(t, newTrzszData(""), dataChan)
}

func TestBase64Reader(t *testing.T) {
	assert := assert.New(t)
	reader := newBase64Reader(newMockReader([][]byte{
		[]byte("d"),
		[]byte("HJ"),
		[]byte("6c3"),
		[]byte("o"),
		[]byte("="),
	}))
	buf, err := io.ReadAll(reader)
	assert.Nil(err)
	assert.Equal([]byte("trzsz"), buf)
}

func TestBase64Writer(t *testing.T) {
	assert := assert.New(t)
	var out mockWriter
	writer := newBase64Writer(&out)
	assertWriteSucc := func(data string) {
		t.Helper()
		n, err := writer.Write([]byte(data))
		assert.Nil(err)
		assert.Equal(n, len(data))
	}
	assertWriteSucc("T")
	assertWriteSucc("rz")
	assertWriteSucc("sz")
	writer.Close()
	assert.True(out.closed)
	assert.Equal("VHJ6c3o=", out.buf.String())
}

func TestEscapeReader(t *testing.T) {
	assert := assert.New(t)
	transfer := newTransferWithEscape(t, nil, true)
	reader := newEscapeReader(transfer.transferConfig.EscapeTable, newMockReader([][]byte{
		[]byte("ABC"),
		[]byte("\xeeA\xeeB\xeeC\xeeDE"),
		[]byte("ABCDEFGXYX\xee"),
		[]byte("\xee123"),
	}))
	assertReadEqual := func(data string, max int) {
		t.Helper()
		buf := make([]byte, max)
		n, err := reader.Read(buf)
		assert.Nil(err)
		assert.Equal(n, len(data))
		assert.Equal([]byte(data), buf[:n])
	}
	assertReadEqual("ABC", 100)
	assertReadEqual("\x02", 1)
	assertReadEqual("\x0d", 1)
	assertReadEqual("\x10\x11", 2)
	assertReadEqual("E", 100)
	assertReadEqual("A", 1)
	assertReadEqual("BC", 2)
	assertReadEqual("DEF", 3)
	assertReadEqual("GXYX", 100)
	assertReadEqual("\xee123", 100)
}

func TestEscapeWriter(t *testing.T) {
	assert := assert.New(t)
	transfer := newTransferWithEscape(t, nil, true)
	var out mockWriter
	writer := newEscapeWriter(transfer.transferConfig.EscapeTable, &out)
	assertWriteSucc := func(data string) {
		t.Helper()
		n, err := writer.Write([]byte(data))
		assert.Nil(err)
		assert.Equal(n, len(data))
	}
	assertWriteSucc("AB\xeeC\x7e")
	assertWriteSucc("\x02\x0d\x10\x11")
	assertWriteSucc("\x13\x18\x1b\x1d")
	assertWriteSucc("\x8d\x90\x91\x93")
	assertWriteSucc("\x9dXZ")
	writer.Close()
	assert.True(out.closed)
	assert.Equal("AB\xee\xeeC\xee1"+
		"\xeeA\xeeB\xeeC\xeeD"+
		"\xeeE\xeeF\xeeG\xeeH"+
		"\xeeI\xeeJ\xeeK\xeeL"+
		"\xeeMXZ", out.buf.String())
}

func TestZstdReaderWriter(t *testing.T) {
	assert := assert.New(t)

	in, out := io.Pipe()
	writer, err := newZstdWriter(out)
	assert.Nil(err)
	reader, err := newZstdReader(in)
	assert.Nil(err)

	buf := make([]byte, 1337)
	for i := 0; i < len(buf); i++ {
		buf[i] = byte(i)
	}

	n, err := writer.Write(buf)
	assert.Nil(err)
	assert.Equal(len(buf), n)
	assert.Nil(writer.Close())

	dst, err := io.ReadAll(reader)
	assert.Nil(err)
	assert.Equal(buf, dst)
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
	assertChannelFrontEqual(t, digest, digestChan)
	assertChannelClosed(t, digestChan)
}

func TestPipelineReadData(t *testing.T) {
	assert := assert.New(t)

	N := 32 * 1024
	file, err := os.CreateTemp("", "TestPipelineReadData")
	assert.Nil(err)
	defer os.Remove(file.Name())
	_, _ = file.Write(bytes.Repeat([]byte{'A'}, N))
	_, _ = file.Write(bytes.Repeat([]byte{'B'}, N))
	_, _ = file.Write(bytes.Repeat([]byte{'C'}, 100))
	file.Close()
	f1, err := os.Open(file.Name())
	assert.Nil(err)
	defer f1.Close()

	// read success
	ctx := newPipelineContext()
	transfer := newTransfer(nil, nil, false, nil)
	fileDataChan, md5SourceChan := transfer.pipelineReadData(ctx, &simpleFileReader{f1, int64(N*2 + 100)})

	assertChannelFrontEqual(t, bytes.Repeat([]byte{'A'}, N), fileDataChan)
	assertChannelFrontEqual(t, bytes.Repeat([]byte{'B'}, N), fileDataChan)
	assertChannelFrontEqual(t, bytes.Repeat([]byte{'C'}, 100), fileDataChan)

	assertChannelFrontEqual(t, bytes.Repeat([]byte{'A'}, N), md5SourceChan)
	assertChannelFrontEqual(t, bytes.Repeat([]byte{'B'}, N), md5SourceChan)
	assertChannelFrontEqual(t, bytes.Repeat([]byte{'C'}, 100), md5SourceChan)

	assert.Nil(ctx.Err())
	assertChannelClosed(t, fileDataChan)
	assertChannelClosed(t, md5SourceChan)

	// growing size
	ctx = newPipelineContext()
	f2, err := os.Open(file.Name())
	assert.Nil(err)
	defer f2.Close()
	fileDataChan, md5SourceChan = transfer.pipelineReadData(ctx, &simpleFileReader{f2, 200})
	assertChannelFrontEqual(t, bytes.Repeat([]byte{'A'}, 200), fileDataChan)
	assertChannelFrontEqual(t, bytes.Repeat([]byte{'A'}, 200), md5SourceChan)

	// cancel read
	f3, err := os.Open(file.Name())
	assert.Nil(err)
	defer f3.Close()
	fileDataChan, md5SourceChan = transfer.pipelineReadData(ctx, &simpleFileReader{f2, int64(N*2 + 100)})
	assertChannelClosed(t, fileDataChan)
	assertChannelClosed(t, md5SourceChan)
}

func TestPipelineEncodeAndDecode(t *testing.T) {
	assert := assert.New(t)

	transfer := newTransfer(nil, nil, false, nil)
	transfer.bufInitPhase.Store(false)
	transfer.bufferSize.Store(4)

	testEncodeAndDecode := func(binary, compress bool) {
		t.Helper()
		transfer.transferConfig.Binary = binary
		ctx := newPipelineContext()
		srcDataChan := make(chan []byte, 100)
		sendDataChan := transfer.pipelineEncodeData(ctx, srcDataChan, compress)
		recvDataChan := make(chan []byte, 100)
		fileDataChan, md5SourceChan := transfer.pipelineDecodeData(ctx, recvDataChan, compress)

		go func() {
			for data := range sendDataChan {
				recvDataChan <- data.data
			}
			close(recvDataChan)
		}()

		buf := make([]byte, 1067)
		for i := 0; i < len(buf); i++ {
			buf[i] = byte(i)
		}
		srcDataChan <- buf
		close(srcDataChan)

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			assertChannelFinalEqual(t, buf, fileDataChan)
			wg.Done()
		}()
		assertChannelFinalEqual(t, buf, md5SourceChan)
		wg.Wait()
		assert.Nil(ctx.Err())
	}

	testEncodeAndDecode(false, false)
	testEncodeAndDecode(false, true)
	testEncodeAndDecode(true, false)
	testEncodeAndDecode(true, true)
}

func TestPipelineEscapeData(t *testing.T) {
	assert := assert.New(t)

	fileDataChan := make(chan []byte, 100)
	transfer := newTransfer(nil, nil, false, nil)
	transfer.bufInitPhase.Store(false)
	transfer.bufferSize.Store(4)
	transfer.transferConfig.Binary = true
	sendDataChan := transfer.pipelineEncodeData(newPipelineContext(), fileDataChan, false)
	newTrzszData := func(data string) trzszData {
		return trzszData{
			data:   []byte(data),
			buffer: []byte(fmt.Sprintf("#DATA:%d\n%s", len(data), data)),
		}
	}

	// buffering
	fileDataChan <- []byte{}
	fileDataChan <- []byte("A")
	fileDataChan <- []byte("BC")
	time.Sleep(100 * time.Millisecond)
	assert.Empty(sendDataChan)

	// buffer just complete
	fileDataChan <- []byte("D")
	assertChannelFrontEqual(t, newTrzszData("ABCD"), sendDataChan)

	// buffer complete and more data
	fileDataChan <- []byte("ABCD1234abcdZZ")
	assertChannelFrontEqual(t, newTrzszData("ABCD"), sendDataChan)
	assertChannelFrontEqual(t, newTrzszData("1234"), sendDataChan)
	assertChannelFrontEqual(t, newTrzszData("abcd"), sendDataChan)

	// change buffer size
	time.Sleep(100 * time.Millisecond)
	transfer.bufferSize.Store(5)
	fileDataChan <- []byte("ABCDEFGHI")
	assertChannelFrontEqual(t, newTrzszData("ZZAB"), sendDataChan)
	assertChannelFrontEqual(t, newTrzszData("CDEFG"), sendDataChan)

	// last buffer
	close(fileDataChan)
	assertChannelFrontEqual(t, newTrzszData("HI"), sendDataChan)
	assertChannelFrontEqual(t, newTrzszData(""), sendDataChan)
	assertChannelClosed(t, sendDataChan)

	// escape data
	transfer = newTransferWithEscape(t, transfer, true)
	ctx := newPipelineContext()
	fileDataChan = make(chan []byte, 100)
	sendDataChan = transfer.pipelineEncodeData(ctx, fileDataChan, false)
	fileDataChan <- []byte{0xee, 0xee, 0xee}
	assertChannelFrontEqual(t, newTrzszData("\xee\xee\xee\xee\xee"), sendDataChan)
	fileDataChan <- []byte{0x7e, 0x7e}
	assertChannelFrontEqual(t, newTrzszData("\xee\xee\x31\xee\x31"), sendDataChan)

	// cancel
	fileDataChan <- bytes.Repeat([]byte{0xee}, 32*1024)
	ctx.cancel(nil)
	assertChannelClosed(t, sendDataChan)
}

func TestPipelineUnescapeData(t *testing.T) {
	recvDataChan := make(chan []byte, 100)
	transfer := newTransferWithEscape(t, nil, true)
	transfer.transferConfig.Binary = true
	fileDataChan, md5SourceChan := transfer.pipelineDecodeData(newPipelineContext(), recvDataChan, false)

	// no need to escape
	recvDataChan <- []byte("123")
	assertChannelFrontEqual(t, []byte("123"), fileDataChan)
	assertChannelFrontEqual(t, []byte("123"), md5SourceChan)
	recvDataChan <- []byte("ABCDE")
	assertChannelFrontEqual(t, []byte("ABCDE"), fileDataChan)
	assertChannelFrontEqual(t, []byte("ABCDE"), md5SourceChan)
	recvDataChan <- bytes.Repeat([]byte("abc"), 1024)
	assertChannelFrontEqual(t, bytes.Repeat([]byte("abc"), 1024), fileDataChan)
	assertChannelFrontEqual(t, bytes.Repeat([]byte("abc"), 1024), md5SourceChan)

	// escape at the beginning
	recvDataChan <- []byte("\xee\xee\xee\x31ABC")
	assertChannelFrontEqual(t, []byte("\xee\x7eABC"), fileDataChan)
	assertChannelFrontEqual(t, []byte("\xee\x7eABC"), md5SourceChan)

	// escape in the middle
	recvDataChan <- []byte("ABC\xee\xee1\xee\x31ABC")
	assertChannelFrontEqual(t, []byte("ABC\xee1\x7eABC"), fileDataChan)
	assertChannelFrontEqual(t, []byte("ABC\xee1\x7eABC"), md5SourceChan)

	// escape at the end
	recvDataChan <- []byte("ABC\xee\x41\xee\x42")
	assertChannelFrontEqual(t, []byte("ABC\x02\x0d"), fileDataChan)
	assertChannelFrontEqual(t, []byte("ABC\x02\x0d"), md5SourceChan)

	// escaping across buffers
	recvDataChan <- []byte("ABC\xee\x41\xee")
	recvDataChan <- []byte("\x42DEF")
	assertChannelFrontEqual(t, []byte("ABC\x02"), fileDataChan)
	assertChannelFrontEqual(t, []byte("ABC\x02"), md5SourceChan)
	assertChannelFrontEqual(t, []byte("\x0dDEF"), fileDataChan)
	assertChannelFrontEqual(t, []byte("\x0dDEF"), md5SourceChan)

	// complex escaping
	recvDataChan <- []byte("ABC\xee\xee\xee\xee\xee")
	recvDataChan <- []byte("\xee\xee\xee\xee\xee")
	recvDataChan <- []byte("\xee\xee\xee\xee\xee")
	recvDataChan <- []byte("\xee\xee\xeeDEF\xee\xee")
	recvDataChan <- []byte("\xee\xee\xee\xee\xee")
	recvDataChan <- []byte("\xee\xee\xee\xee\xee")
	recvDataChan <- []byte("G\xee\xee\xee\xee\xee")
	recvDataChan <- []byte("\x31\xee\xee\xee\xee")
	recvDataChan <- []byte("\xeeA\xeeB\xeeC\xeeD")
	recvDataChan <- []byte("\xeeE\xeeF\xeeG\xeeH")
	recvDataChan <- []byte("\xeeI\xeeJ\xeeK\xeeL")
	recvDataChan <- []byte("\xeeM")
	close(recvDataChan)
	assertUnescapeEqual := func(dataChan <-chan []byte) {
		assertChannelFrontEqual(t, []byte("ABC\xee\xee"), dataChan)
		assertChannelFrontEqual(t, []byte("\xee\xee\xee"), dataChan)
		assertChannelFrontEqual(t, []byte("\xee\xee"), dataChan)
		assertChannelFrontEqual(t, []byte("\xee\xeeDEF\xee"), dataChan)
		assertChannelFrontEqual(t, []byte("\xee\xee"), dataChan)
		assertChannelFrontEqual(t, []byte("\xee\xee\xee"), dataChan)
		assertChannelFrontEqual(t, []byte("G\xee\xee"), dataChan)
		assertChannelFrontEqual(t, []byte("\x7e\xee\xee"), dataChan)
		assertChannelFrontEqual(t, []byte("\x02\x0d\x10\x11"), dataChan)
		assertChannelFrontEqual(t, []byte("\x13\x18\x1b\x1d"), dataChan)
		assertChannelFrontEqual(t, []byte("\x8d\x90\x91\x93"), dataChan)
		assertChannelFrontEqual(t, []byte("\x9d"), dataChan)
	}
	assertUnescapeEqual(fileDataChan)
	assertUnescapeEqual(md5SourceChan)

	// cancel
	recvDataChan = make(chan []byte, 100)
	ctx := newPipelineContext()
	fileDataChan, md5SourceChan = transfer.pipelineDecodeData(ctx, recvDataChan, false)
	ctx.cancel(nil)
	recvDataChan <- []byte("123")
	assertChannelClosed(t, fileDataChan)
	assertChannelClosed(t, md5SourceChan)
}

func TestPipelineSendData(t *testing.T) {
	writer := newTestWriter(t)
	sendDataChan := make(chan trzszData, 100)
	transfer := newTransfer(writer, nil, false, nil)
	transfer.bufferSize.Store(3)
	ctx := newPipelineContext()
	ackChan := transfer.pipelineSendData(ctx, sendDataChan)
	mockTimeNow([]int64{}, 2096)
	newTrzszAck := func(length int64) trzszAck {
		return trzszAck{begin: time.UnixMilli(2096), length: length}
	}

	// send all base64 at once
	sendDataChan <- trzszData{data: []byte("ABC"), buffer: []byte("xyz123")}
	assertChannelFrontEqual(t, newTrzszAck(3), ackChan)
	writer.assertLastBufferEqual("xyz123")

	// split base64 and send
	sendDataChan <- trzszData{data: []byte("abcdefg"), buffer: []byte("331")}
	assertChannelFrontEqual(t, newTrzszAck(3), ackChan)
	assertChannelFrontEqual(t, newTrzszAck(3), ackChan)
	assertChannelFrontEqual(t, newTrzszAck(1), ackChan)
	writer.assertBase64DataEqual([]string{"abc", "def", "g"})

	// send all binary at once
	transfer.transferConfig.Binary = true
	transfer.bufferSize.Store(3)
	sendDataChan <- trzszData{data: []byte("123"), buffer: []byte("ABCDEF")}
	assertChannelFrontEqual(t, newTrzszAck(3), ackChan)
	writer.assertLastBufferEqual("ABCDEF")

	// split binary and send
	sendDataChan <- trzszData{data: []byte("12345678"), buffer: []byte("332")}
	assertChannelFrontEqual(t, newTrzszAck(3), ackChan)
	assertChannelFrontEqual(t, newTrzszAck(3), ackChan)
	assertChannelFrontEqual(t, newTrzszAck(2), ackChan)
	writer.assertBinaryDataEqual([]string{"123", "456", "78"})

	// cancel
	ctx.cancel(nil)
	sendDataChan <- trzszData{data: []byte("111222333"), buffer: []byte("333")}
	assertChannelClosed(t, ackChan)
}
