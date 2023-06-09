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
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"github.com/klauspost/compress/zstd"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
)

type pipelineContext struct {
	context.Context
	cancel context.CancelCauseFunc
	succ   chan struct{}
}

type trzszData struct {
	length int
	buffer []byte
}

type base64Reader struct {
	dataChan <-chan []byte
	ctx      *pipelineContext
	buf      []byte
	eof      bool
}

func (r *base64Reader) Read(p []byte) (int, error) {
	if r.eof {
		return 0, io.EOF
	}
	if len(r.buf) == 0 {
		select {
		case data, ok := <-r.dataChan:
			if !ok {
				r.eof = true
				return 0, io.EOF
			}
			r.buf = data
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		}
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

func newBase64Reader(ctx *pipelineContext, dataChan <-chan []byte) *base64Reader {
	return &base64Reader{ctx: ctx, dataChan: dataChan}
}

type base64Writer struct {
	transfer     *trzszTransfer
	ctx          *pipelineContext
	sendDataChan chan<- trzszData
	buffer       *bytes.Buffer
	bufSize      int64
}

func (b *base64Writer) deliver(data []byte) bool {
	buffer := bytes.NewBuffer(make([]byte, 0, len(data)+0x10))
	buffer.Write([]byte("#DATA:"))
	buffer.Write(data)
	buffer.Write([]byte(b.transfer.transferConfig.Newline))
	select {
	case b.sendDataChan <- trzszData{len(data), buffer.Bytes()}:
		return true
	case <-b.ctx.Done():
		return false
	}
}

func (b *base64Writer) Write(p []byte) (int, error) {
	m := 0
	for {
		space := b.buffer.Cap() - b.buffer.Len()

		if len(p) < space {
			n, err := b.buffer.Write(p)
			if err != nil {
				return 0, err
			}
			m += n
			return m, nil
		}

		n, err := b.buffer.Write(p[0:space])
		if err != nil {
			return 0, err
		}
		if !b.deliver(b.buffer.Bytes()) {
			return 0, b.ctx.Err()
		}

		b.bufSize = b.transfer.bufferSize.Load()
		b.buffer = bytes.NewBuffer(make([]byte, 0, b.bufSize))
		p = p[n:]
		m += n
	}
}

func (b *base64Writer) Close() error {
	if b.buffer.Len() > 0 {
		if !b.deliver(b.buffer.Bytes()) {
			return b.ctx.Err()
		}
	}
	b.deliver([]byte{}) // send the finish flag
	return nil
}

func newBase64Writer(transfer *trzszTransfer, ctx *pipelineContext, sendDataChan chan<- trzszData) *base64Writer {
	bufSize := transfer.bufferSize.Load()
	buffer := bytes.NewBuffer(make([]byte, 0, bufSize))
	return &base64Writer{transfer, ctx, sendDataChan, buffer, bufSize}
}

type compressedWriter struct {
	base64Writer  *base64Writer
	base64Encoder io.WriteCloser
}

func (c *compressedWriter) Write(p []byte) (int, error) {
	return c.base64Encoder.Write(p)
}

func (c *compressedWriter) Close() error {
	err := c.base64Encoder.Close()
	if err != nil {
		return err
	}
	return c.base64Writer.Close()
}

func newCompressedWriter(transfer *trzszTransfer, ctx *pipelineContext, sendDataChan chan<- trzszData) *compressedWriter {
	writer := newBase64Writer(transfer, ctx, sendDataChan)
	encoder := base64.NewEncoder(base64.StdEncoding, writer)
	return &compressedWriter{writer, encoder}
}

func (t *trzszTransfer) pipelineCalculateMD5(ctx *pipelineContext, md5SourceChan <-chan []byte) <-chan []byte {
	md5DigestChan := make(chan []byte, 1)
	go func() {
		defer close(md5DigestChan)
		hasher := md5.New()
		for buf := range md5SourceChan {
			if _, err := hasher.Write(buf); err != nil {
				ctx.cancel(newSimpleTrzszError(fmt.Sprintf("MD5 write error: %v", err)))
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		md5DigestChan <- hasher.Sum(nil)
	}()
	return md5DigestChan
}

func (t *trzszTransfer) pipelineReadData(ctx *pipelineContext, file *os.File, size int64) (<-chan []byte, <-chan []byte) {
	fileDataChan := make(chan []byte, 100)
	md5SourceChan := make(chan []byte, 100)
	go func() {
		defer close(fileDataChan)
		defer close(md5SourceChan)
		step := int64(0)
		bufSize := int64(4096)
		for step < size && ctx.Err() == nil {
			m := size - step
			if m > bufSize {
				m = bufSize
			}
			buffer := make([]byte, m)
			n, err := file.Read(buffer)
			if n > 0 {
				select {
				case fileDataChan <- buffer[:n]:
				case <-ctx.Done():
					return
				}
				select {
				case md5SourceChan <- buffer[:n]:
				case <-ctx.Done():
					return
				}
				step += int64(n)
			}
			if err != nil {
				ctx.cancel(newSimpleTrzszError(fmt.Sprintf("Read file error: %v", err)))
				return
			}
		}
	}()
	return fileDataChan, md5SourceChan
}

func (t *trzszTransfer) pipelineEncodeData(ctx *pipelineContext, fileDataChan <-chan []byte) <-chan trzszData {
	sendDataChan := make(chan trzszData, 1)
	go func() {
		defer close(sendDataChan)

		c := newCompressedWriter(t, ctx, sendDataChan)
		defer func() {
			err := c.Close()
			if err != nil {
				ctx.cancel(newSimpleTrzszError(fmt.Sprintf("Close compressed writer error: %v", err)))
			}
		}()

		z, err := zstd.NewWriter(c)
		if err != nil {
			ctx.cancel(newSimpleTrzszError(fmt.Sprintf("New zstd writer error: %v", err)))
			return
		}
		defer func() {
			err := z.Close()
			if err != nil {
				ctx.cancel(newSimpleTrzszError(fmt.Sprintf("Close zstd writer error: %v", err)))
			}
		}()

		for data := range fileDataChan {
			if err := writeAll(z, data); err != nil {
				ctx.cancel(newSimpleTrzszError(fmt.Sprintf("Write to zstd error: %v", err)))
				return
			}
			if t.flushInTime {
				if err := z.Flush(); err != nil {
					ctx.cancel(newSimpleTrzszError(fmt.Sprintf("Flush to zstd error: %v", err)))
					return
				}
			}
			if ctx.Err() != nil {
				return
			}
		}
	}()
	return sendDataChan
}

func (t *trzszTransfer) pipelineEscapeData(ctx *pipelineContext, fileDataChan <-chan []byte) <-chan trzszData {
	sendDataChan := make(chan trzszData, 1)
	deliver := func(data []byte) bool {
		buffer := bytes.NewBuffer(make([]byte, 0, len(data)+0x20))
		buffer.Write([]byte(fmt.Sprintf("#DATA:%d\n", len(data))))
		buffer.Write(data)
		select {
		case sendDataChan <- trzszData{len(data), buffer.Bytes()}:
			return true
		case <-ctx.Done():
			return false
		}
	}
	go func() {
		defer close(sendDataChan)
		bufSize := int(t.bufferSize.Load())
		buffer := new(bytes.Buffer)
		for data := range fileDataChan {
			buf := escapeData(data, t.transferConfig.EscapeCodes)
			if buffer.Len() == 0 {
				buffer = bytes.NewBuffer(buf)
			} else {
				buffer.Grow(bufSize)
				buffer.Write(buf)
			}
			for buffer.Len() >= bufSize {
				b := buffer.Bytes()
				if !deliver(b[:bufSize]) {
					return
				}
				buffer = bytes.NewBuffer(b[bufSize:])
				bufSize = int(t.bufferSize.Load())
			}
			if ctx.Err() != nil {
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		if buffer.Len() > 0 {
			if !deliver(buffer.Bytes()) {
				return
			}
		}
		deliver([]byte{}) // send the finish flag
	}()
	return sendDataChan
}

func (t *trzszTransfer) pipelineRecvCurrentAck() (int64, int64, error) {
	timeout := t.getNewTimeout()
	resp, err := t.recvCheck("SUCC", false, timeout)
	if err != nil {
		return 0, 0, err
	}

	tokens := strings.Split(resp, "/")
	if len(tokens) != 2 {
		return 0, 0, newSimpleTrzszError(fmt.Sprintf("Response number is not 2 but %d", len(tokens)))
	}

	length, err := strconv.ParseInt(tokens[0], 10, 64)
	if err != nil {
		return 0, 0, newSimpleTrzszError(fmt.Sprintf("Parse int from %s error: %v", tokens[0], err))
	}

	step, err := strconv.ParseInt(tokens[1], 10, 64)
	if err != nil {
		return 0, 0, newSimpleTrzszError(fmt.Sprintf("Parse int from %s error: %v", tokens[1], err))
	}

	return length, step, nil
}

func (t *trzszTransfer) pipelineRecvFinalAck(ctx *pipelineContext, size int64, progressChan chan<- int64) {
	for ctx.Err() == nil {
		timeout := t.getNewTimeout()
		step, err := t.recvInteger("SUCC", false, timeout)
		if err != nil {
			ctx.cancel(err)
			return
		}

		if step > size {
			ctx.cancel(newSimpleTrzszError(fmt.Sprintf("RecvFinalAck expected step %d but was %d", size, step)))
			return
		}

		if progressChan != nil {
			select {
			case progressChan <- step:
			case <-ctx.Done():
				return
			}
		}

		if step == size {
			if ctx.Err() == nil {
				ctx.succ <- struct{}{}
			}
			break
		}
	}
}

func (t *trzszTransfer) pipelineSendData(ctx *pipelineContext, size int64, sendDataChan <-chan trzszData, showProgress bool) <-chan int64 {
	var progressChan chan int64
	if showProgress {
		progressChan = make(chan int64, 100)
	}
	go func() {
		if showProgress {
			defer close(progressChan)
		}
		for data := range sendDataChan {
			beginTime := time.Now()
			if err := t.writeAll(data.buffer); err != nil {
				ctx.cancel(err)
				return
			}

			length, step, err := t.pipelineRecvCurrentAck()
			if err != nil {
				ctx.cancel(err)
				return
			}
			if length != int64(data.length) {
				ctx.cancel(newSimpleTrzszError(fmt.Sprintf("SendData length check [%d] <> [%d]", length, data.length)))
				return
			}

			if showProgress {
				select {
				case progressChan <- step:
				case <-ctx.Done():
					return
				}
			}

			chunkTime := time.Since(beginTime)
			bufSize := t.bufferSize.Load()
			if length == bufSize && chunkTime < 500*time.Millisecond && bufSize < t.transferConfig.MaxBufSize {
				t.bufferSize.Store(minInt64(bufSize*2, t.transferConfig.MaxBufSize))
			} else if chunkTime >= 2*time.Second && bufSize > 1024 {
				t.bufferSize.Store(1024)
			}
			if chunkTime > t.maxChunkTime {
				t.maxChunkTime = chunkTime
			}
			if ctx.Err() != nil {
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		t.pipelineRecvFinalAck(ctx, size, progressChan)
	}()
	return progressChan
}

func (t *trzszTransfer) pipelineShowProgress(ctx *pipelineContext, progress progressCallback, progressChan <-chan int64) {
	go func() {
		for step := range progressChan {
			progress.onStep(step)
			if ctx.Err() != nil {
				return
			}
		}
	}()
}

func (t *trzszTransfer) sendFileDataV2(file *os.File, size int64, progress progressCallback) ([]byte, error) {
	c, cancel := context.WithCancelCause(context.Background())
	ctx := &pipelineContext{c, cancel, make(chan struct{}, 1)}
	defer ctx.cancel(nil)
	defer close(ctx.succ)

	fileDataChan, md5SourceChan := t.pipelineReadData(ctx, file, size)

	md5DigestChan := t.pipelineCalculateMD5(ctx, md5SourceChan)

	var sendDataChan <-chan trzszData
	if t.transferConfig.Binary {
		sendDataChan = t.pipelineEscapeData(ctx, fileDataChan)
	} else {
		sendDataChan = t.pipelineEncodeData(ctx, fileDataChan)
	}

	showProgress := progress != nil && !reflect.ValueOf(progress).IsNil()
	progressChan := t.pipelineSendData(ctx, size, sendDataChan, showProgress)

	if showProgress {
		t.pipelineShowProgress(ctx, progress, progressChan)
	}

	select {
	case <-ctx.succ:
		return <-md5DigestChan, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

func (t *trzszTransfer) pipelineRecvBase64Data() ([]byte, error) {
	timeout := t.getNewTimeout()
	line, err := t.recvLine("DATA", false, timeout)
	if err != nil {
		return nil, err
	}

	idx := bytes.IndexByte(line, ':')
	if idx < 1 {
		return nil, newTrzszError(encodeBytes(line), "colon", true)
	}

	typ := line[1:idx]
	buf := line[idx+1:]
	if bytes.Compare(typ, []byte("DATA")) != 0 { // nolint:all
		return nil, newTrzszError(string(buf), string(typ), true)
	}

	return buf, nil
}

func (t *trzszTransfer) pipelineRecvBinaryData() ([]byte, error) {
	timeout := t.getNewTimeout()
	size, err := t.recvInteger("DATA", false, timeout)
	if err != nil {
		return nil, err
	}

	if size == 0 {
		return []byte{}, nil
	}

	return t.buffer.readBinary(int(size), timeout)
}

func (t *trzszTransfer) pipelineSendCurrentAck(length int) error {
	step := t.savedSteps.Load()
	return t.writeAll([]byte(fmt.Sprintf("#SUCC:%d/%d%s", length, step, t.transferConfig.Newline)))
}

func (t *trzszTransfer) pipelineSendFinalAck(ctx *pipelineContext, size int64, ackStepChan <-chan struct{}) {
	go func() {
		for ctx.Err() == nil {
			step := t.savedSteps.Load()
			if err := t.sendInteger("SUCC", step); err != nil {
				ctx.cancel(err)
				return
			}

			if step > size {
				ctx.cancel(newSimpleTrzszError(fmt.Sprintf("SendFinalAck expected step %d but was %d", size, step)))
				return
			}

			if step == size {
				if ctx.Err() == nil {
					ctx.succ <- struct{}{}
				}
				break
			}

			select {
			case <-ackStepChan:
			case <-time.After(200 * time.Millisecond):
			}
		}
	}()
}

func (t *trzszTransfer) pipelineRecvData(ctx *pipelineContext, size int64, ackStepChan <-chan struct{}) <-chan []byte {
	recvDataChan := make(chan []byte, 100)
	go func() {
		defer close(recvDataChan)
		t.savedSteps.Store(0)
		for ctx.Err() == nil {
			beginTime := time.Now()
			var err error
			var data []byte
			if t.transferConfig.Binary {
				data, err = t.pipelineRecvBinaryData()
			} else {
				data, err = t.pipelineRecvBase64Data()
			}
			if err != nil {
				ctx.cancel(err)
				return
			}

			if err := t.pipelineSendCurrentAck(len(data)); err != nil {
				ctx.cancel(err)
				return
			}

			chunkTime := time.Since(beginTime)
			if chunkTime > t.maxChunkTime {
				t.maxChunkTime = chunkTime
			}

			if len(data) == 0 {
				break
			}

			buf := make([]byte, len(data))
			copy(buf, data)
			select {
			case recvDataChan <- buf:
			case <-ctx.Done():
				return
			}
		}
		t.pipelineSendFinalAck(ctx, size, ackStepChan)
	}()
	return recvDataChan
}

func (t *trzszTransfer) pipelineDecodeData(ctx *pipelineContext, recvDataChan <-chan []byte) (<-chan []byte, <-chan []byte) {
	fileDataChan := make(chan []byte, 100)
	md5SourceChan := make(chan []byte, 100)
	go func() {
		defer close(fileDataChan)
		defer close(md5SourceChan)
		z, err := zstd.NewReader(base64.NewDecoder(base64.StdEncoding, newBase64Reader(ctx, recvDataChan)))
		if err != nil {
			ctx.cancel(newSimpleTrzszError(fmt.Sprintf("New zstd reader error: %v", err)))
			return
		}
		defer z.Close()
		for ctx.Err() == nil {
			buffer := make([]byte, 4096)
			n, err := z.Read(buffer)
			if n > 0 {
				select {
				case fileDataChan <- buffer[:n]:
				case <-ctx.Done():
					return
				}
				select {
				case md5SourceChan <- buffer[:n]:
				case <-ctx.Done():
					return
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				ctx.cancel(newSimpleTrzszError(fmt.Sprintf("Read from zstd error: %v", err)))
				return
			}
		}
	}()
	return fileDataChan, md5SourceChan
}

func (t *trzszTransfer) pipelineUnescapeData(ctx *pipelineContext, recvDataChan <-chan []byte) (<-chan []byte, <-chan []byte) {
	fileDataChan := make(chan []byte, 100)
	md5SourceChan := make(chan []byte, 100)
	deliver := func(data []byte) bool {
		buffer := unescapeData(data, t.transferConfig.EscapeCodes)
		select {
		case fileDataChan <- buffer:
		case <-ctx.Done():
			return false
		}
		select {
		case md5SourceChan <- buffer:
		case <-ctx.Done():
			return false
		}
		return true
	}
	go func() {
		defer close(fileDataChan)
		defer close(md5SourceChan)
		remainingBuf := new(bytes.Buffer)
		for data := range recvDataChan {
			if remainingBuf.Len() > 0 {
				idx := 0
				for idx < len(data) {
					b := data[idx]
					idx++
					remainingBuf.WriteByte(b)
					if !isEscapeByte(b) {
						if !deliver(remainingBuf.Bytes()) {
							return
						}
						remainingBuf.Reset()
						break
					}
				}
				data = data[idx:]
			}
			idx := len(data)
			for idx > 0 && isEscapeByte(data[idx-1]) {
				idx--
			}
			remainingBuf.Write(data[idx:])
			data = data[:idx]
			if len(data) == 0 {
				continue
			}
			if !deliver(data) {
				return
			}
			if ctx.Err() != nil {
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		if remainingBuf.Len() > 0 {
			deliver(remainingBuf.Bytes())
		}
	}()
	return fileDataChan, md5SourceChan
}

func (t *trzszTransfer) pipelineSaveData(ctx *pipelineContext, file *os.File, size int64, fileDataChan <-chan []byte, ackStepChan chan<- struct{}, showProgress bool) <-chan int64 {
	var progressChan chan int64
	if showProgress {
		progressChan = make(chan int64, 100)
	}
	go func() {
		defer close(ackStepChan)
		if showProgress {
			defer close(progressChan)
		}
		step := int64(0)
		for data := range fileDataChan {
			if _, err := file.Write(data); err != nil {
				ctx.cancel(newSimpleTrzszError(fmt.Sprintf("Write file error: %v", err)))
				return
			}
			step += int64(len(data))
			t.savedSteps.Store(step)
			if showProgress {
				select {
				case progressChan <- step:
				case <-ctx.Done():
					return
				}
			}
			if ctx.Err() != nil {
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		if step != size {
			ctx.cancel(newSimpleTrzszError(fmt.Sprintf("SaveFile expected step %d but was %d", size, step)))
			return
		}
		ackStepChan <- struct{}{}
	}()
	return progressChan
}

func (t *trzszTransfer) recvFileDataV2(file *os.File, size int64, progress progressCallback) ([]byte, error) {
	defer file.Close()
	c, cancel := context.WithCancelCause(context.Background())
	ctx := &pipelineContext{c, cancel, make(chan struct{}, 1)}
	defer ctx.cancel(nil)
	defer close(ctx.succ)

	ackStepChan := make(chan struct{}, 1)
	recvDataChan := t.pipelineRecvData(ctx, size, ackStepChan)

	var fileDataChan <-chan []byte
	var md5SourceChan <-chan []byte
	if t.transferConfig.Binary {
		fileDataChan, md5SourceChan = t.pipelineUnescapeData(ctx, recvDataChan)
	} else {
		fileDataChan, md5SourceChan = t.pipelineDecodeData(ctx, recvDataChan)
	}

	md5DigestChan := t.pipelineCalculateMD5(ctx, md5SourceChan)

	showProgress := progress != nil && !reflect.ValueOf(progress).IsNil()
	progressChan := t.pipelineSaveData(ctx, file, size, fileDataChan, ackStepChan, showProgress)

	if showProgress {
		t.pipelineShowProgress(ctx, progress, progressChan)
	}

	select {
	case <-ctx.succ:
		return <-md5DigestChan, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}
