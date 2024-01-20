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
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
)

const kAckChanBufferSize = 5

type pipelineContext struct {
	context.Context
	cancel context.CancelCauseFunc
	succ   chan struct{}
}

type trzszData struct {
	data   []byte
	buffer []byte
	index  int
}

type trzszAck struct {
	begin  time.Time
	length int64
}

type readCloser interface {
	io.Reader
	Close()
}

type writeCloseFlusher interface {
	io.WriteCloser
	Flush() error
}

type recvDataReader struct {
	dataChan <-chan []byte
	ctx      *pipelineContext
	buf      []byte
	eof      bool
}

func (r *recvDataReader) Read(p []byte) (int, error) {
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

func newRecvDataReader(ctx *pipelineContext, dataChan <-chan []byte) *recvDataReader {
	return &recvDataReader{ctx: ctx, dataChan: dataChan}
}

type sendDataWriter struct {
	transfer     *trzszTransfer
	ctx          *pipelineContext
	sendDataChan chan<- trzszData
	buffer       *bytes.Buffer
	bufSize      int64
}

func (b *sendDataWriter) deliver(data []byte) bool {
	buffer := bytes.NewBuffer(make([]byte, 0, len(data)+0x20))
	buffer.Write([]byte("#DATA:"))
	if b.transfer.transferConfig.Binary {
		buffer.Write([]byte(strconv.Itoa(len(data))))
		buffer.Write([]byte(b.transfer.transferConfig.Newline))
		buffer.Write(data)
	} else {
		buffer.Write(data)
		buffer.Write([]byte(b.transfer.transferConfig.Newline))
	}
	select {
	case b.sendDataChan <- trzszData{data, buffer.Bytes(), 0}:
		return true
	case <-b.ctx.Done():
		return false
	}
}

func (b *sendDataWriter) Write(p []byte) (int, error) {
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

		if b.transfer.bufInitPhase.Load() {
			b.transfer.bufInitWG.Add(1)
		}
		if !b.deliver(b.buffer.Bytes()) {
			return 0, b.ctx.Err()
		}

		if b.transfer.bufInitPhase.Load() {
			b.transfer.bufInitWG.Wait()
		}
		b.bufSize = b.transfer.bufferSize.Load()
		b.buffer = bytes.NewBuffer(make([]byte, 0, b.bufSize))
		p = p[n:]
		m += n
	}
}

func (b *sendDataWriter) Close() error {
	b.transfer.bufInitPhase.Store(false)
	if b.buffer.Len() > 0 {
		if !b.deliver(b.buffer.Bytes()) {
			return b.ctx.Err()
		}
	}
	b.deliver([]byte{}) // send the finish flag
	return nil
}

func newSendDataWriter(transfer *trzszTransfer, ctx *pipelineContext, sendDataChan chan<- trzszData) *sendDataWriter {
	bufSize := transfer.bufferSize.Load()
	buffer := bytes.NewBuffer(make([]byte, 0, bufSize))
	return &sendDataWriter{transfer, ctx, sendDataChan, buffer, bufSize}
}

type base64Reader struct {
	decoder io.Reader
}

func (b *base64Reader) Read(p []byte) (n int, err error) {
	return b.decoder.Read(p)
}

func (b *base64Reader) Close() {
}

func newBase64Reader(reader io.Reader) readCloser {
	decoder := base64.NewDecoder(base64.StdEncoding, reader)
	return &base64Reader{decoder}
}

type base64Writer struct {
	encoder io.WriteCloser
	writer  io.WriteCloser
}

func (b *base64Writer) Write(p []byte) (int, error) {
	return b.encoder.Write(p)
}

func (b *base64Writer) Close() error {
	if err := b.encoder.Close(); err != nil {
		return err
	}
	return b.writer.Close()
}

func (b *base64Writer) Flush() error {
	return nil
}

func newBase64Writer(writer io.WriteCloser) writeCloseFlusher {
	encoder := base64.NewEncoder(base64.StdEncoding, writer)
	return &base64Writer{encoder, writer}
}

type escapeReader struct {
	table  *escapeTable
	reader io.Reader
	buffer []byte
}

func (e *escapeReader) Read(p []byte) (n int, err error) {
	if e.table == nil || e.table.totalCount == 0 {
		return e.reader.Read(p)
	}
	for {
		var idx int
		if len(e.buffer) > 0 {
			buf, remaining, err := unescapeData(e.buffer, e.table, p)
			if err != nil {
				return 0, err
			}
			if len(buf) > 0 {
				e.buffer = remaining
				return len(buf), nil
			}
			e.buffer = make([]byte, 32*1024)
			copy(e.buffer, remaining)
			idx = len(remaining)
		} else {
			e.buffer = make([]byte, 32*1024)
			idx = 0
		}
		n, err := e.reader.Read(e.buffer[idx:])
		if err != nil {
			return 0, err
		}
		e.buffer = e.buffer[:idx+n]
	}
}

func (e *escapeReader) Close() {
}

func newEscapeReader(table *escapeTable, reader io.Reader) readCloser {
	return &escapeReader{table, reader, nil}
}

type escapeWriter struct {
	table  *escapeTable
	writer io.WriteCloser
}

func (e *escapeWriter) Write(p []byte) (int, error) {
	buf := escapeData(p, e.table)
	if err := writeAll(e.writer, buf); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (e *escapeWriter) Close() error {
	return e.writer.Close()
}

func (e *escapeWriter) Flush() error {
	return nil
}

func newEscapeWriter(table *escapeTable, writer io.WriteCloser) writeCloseFlusher {
	return &escapeWriter{table, writer}
}

type zstdReader struct {
	decoder *zstd.Decoder
}

func (z *zstdReader) Read(p []byte) (n int, err error) {
	return z.decoder.Read(p)
}

func (z *zstdReader) Close() {
	z.decoder.Close()
}

func newZstdReader(reader io.Reader) (readCloser, error) {
	decoder, err := zstd.NewReader(reader)
	if err != nil {
		return nil, simpleTrzszError("New zstd reader error: %v", err)
	}
	return &zstdReader{decoder}, nil
}

type zstdWriter struct {
	encoder *zstd.Encoder
	writer  io.WriteCloser
}

func (z *zstdWriter) Write(p []byte) (int, error) {
	return z.encoder.Write(p)
}

func (c *zstdWriter) Close() error {
	if err := c.encoder.Close(); err != nil {
		return err
	}
	return c.writer.Close()
}

func (c *zstdWriter) Flush() error {
	return c.encoder.Flush()
}

func newZstdWriter(writer io.WriteCloser) (writeCloseFlusher, error) {
	encoder, err := zstd.NewWriter(writer)
	if err != nil {
		return nil, simpleTrzszError("New zstd writer error: %v", err)
	}
	return &zstdWriter{encoder, writer}, nil
}

func (t *trzszTransfer) checkStopAndPause(typ string) error {
	if t.transferConfig.Protocol >= kProtocolVersion3 {
		for t.pausing.Load() {
			if err := t.checkStop(); err != nil {
				return err
			}
			if err := t.writeAll([]byte(fmt.Sprintf("#%s:=%s", typ, t.transferConfig.Newline))); err != nil {
				return err
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	return t.checkStop()
}

func (t *trzszTransfer) recvCheckV2(expectType string) ([]byte, *time.Time, bool, error) {
	pause := false
	var pauseIdx uint32
	for {
		if t.transferConfig.Protocol >= kProtocolVersion3 {
			pauseIdx = t.pauseIdx.Load()
			for t.pausing.Load() {
				pause = true
				if err := t.checkStop(); err != nil {
					return nil, nil, pause, err
				}
				time.Sleep(100 * time.Millisecond)
			}
		}

		beginTime := timeNowFunc()
		line, err := t.recvLine(expectType, false, t.getNewTimeout())

		if t.transferConfig.Protocol >= kProtocolVersion3 &&
			err == errReceiveDataTimeout && pauseIdx < t.pauseIdx.Load() { // pause after read, read again
			pause = true
			continue
		}
		if err != nil {
			return nil, nil, pause, err
		}

		idx := bytes.IndexByte(line, ':')
		if idx < 1 {
			return nil, nil, pause, newTrzszError(encodeBytes(line), "colon", true)
		}
		typ := line[1:idx]
		buf := line[idx+1:]
		if string(typ) != expectType {
			return nil, nil, pause, newTrzszError(string(buf), string(typ), true)
		}

		if t.transferConfig.Protocol >= kProtocolVersion3 {
			if len(buf) == 1 && buf[0] == '=' { // client pausing, read again
				pause = true
				continue
			}
			if rbt := t.resumeBeginTime.Load(); rbt != nil && beginTime.Before(*rbt) {
				t.resumeBeginTime.CompareAndSwap(rbt, nil)
				beginTime = *rbt
				pause = true
			}
		}

		return buf, &beginTime, pause, nil
	}
}

func (t *trzszTransfer) sendDataV2(buffer []byte, length int, encoded bool) (*time.Time, error) {
	if err := t.checkStopAndPause("DATA"); err != nil {
		return nil, err
	}
	beginTime := timeNowFunc()
	if encoded {
		return &beginTime, t.writeAll(buffer)
	}
	if t.transferConfig.Binary {
		if err := t.writeAll([]byte(fmt.Sprintf("#DATA:%d%s", length, t.transferConfig.Newline))); err != nil {
			return nil, err
		}
		return &beginTime, t.writeAll(buffer)
	} else {
		if err := t.writeAll([]byte("#DATA:")); err != nil {
			return nil, err
		}
		if err := t.writeAll(buffer); err != nil {
			return nil, err
		}
		return &beginTime, t.writeAll([]byte(t.transferConfig.Newline))
	}
}

func (t *trzszTransfer) isCompressFixed(size int64) (fixed bool, compress bool) {
	if t.transferConfig.Protocol < kProtocolVersion3 {
		return true, !t.transferConfig.Binary
	}
	if t.transferConfig.CompressType == kCompressYes {
		return true, true
	}
	if t.transferConfig.CompressType == kCompressNo {
		return true, false
	}
	if size < 512 {
		return true, false
	}
	if size < 128*1024 {
		return true, true
	}
	return false, false
}

func (t *trzszTransfer) sendCompressFlag(file fileReader) (bool, error) {
	fixed, compress := t.isCompressFixed(file.getSize())
	if fixed {
		return compress, nil
	}
	compress, err := isCompressionProfitable(file)
	if err != nil {
		return false, fmt.Errorf("Compression detect failed: %v", err)
	}
	return compress, t.sendLine("COMP", strconv.FormatBool(compress))
}

func (t *trzszTransfer) recvCompressFlag(size int64) (bool, error) {
	fixed, compress := t.isCompressFixed(size)
	if fixed {
		return compress, nil
	}
	flag, err := t.recvCheck("COMP", false, t.getNewTimeout())
	if err != nil {
		return false, err
	}
	switch flag {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, simpleTrzszError("Unknown compress flag: %s", flag)
	}
}

func (t *trzszTransfer) pipelineCalculateMD5(ctx *pipelineContext, md5SourceChan <-chan []byte) <-chan []byte {
	md5DigestChan := make(chan []byte, 1)
	go func() {
		defer close(md5DigestChan)
		hasher := md5.New()
		for buf := range md5SourceChan {
			if _, err := hasher.Write(buf); err != nil {
				ctx.cancel(simpleTrzszError("MD5 write error: %v", err))
				return
			}
			if ctx.Err() != nil {
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

func (t *trzszTransfer) pipelineReadData(ctx *pipelineContext, file fileReader) (<-chan []byte, <-chan []byte) {
	fileDataChan := make(chan []byte, 100)
	md5SourceChan := make(chan []byte, 100)
	go func() {
		defer close(fileDataChan)
		defer close(md5SourceChan)
		step := int64(0)
		size := file.getSize()
		bufSize := int64(32 * 1024)
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
			if err == io.EOF {
				if step != size {
					ctx.cancel(simpleTrzszError("EOF but step [%d] <> size [%d]", step, size))
					return
				}
			} else if err != nil {
				ctx.cancel(simpleTrzszError("Read file error: %v", err))
				return
			}
		}
	}()
	return fileDataChan, md5SourceChan
}

func (t *trzszTransfer) pipelineEncodeData(ctx *pipelineContext, fileDataChan <-chan []byte, compress bool) <-chan trzszData {
	sendDataChan := make(chan trzszData, 5)
	go func() {
		defer close(sendDataChan)

		var err error
		var writer writeCloseFlusher
		if t.transferConfig.Binary {
			if compress {
				writer, err = newZstdWriter(newEscapeWriter(t.transferConfig.EscapeTable, newSendDataWriter(t, ctx, sendDataChan)))
			} else {
				writer = newEscapeWriter(t.transferConfig.EscapeTable, newSendDataWriter(t, ctx, sendDataChan))
			}
		} else {
			if compress {
				writer, err = newZstdWriter(newBase64Writer(newSendDataWriter(t, ctx, sendDataChan)))
			} else {
				writer = newBase64Writer(newSendDataWriter(t, ctx, sendDataChan))
			}
		}
		if err != nil {
			ctx.cancel(simpleTrzszError("New encode writer error: %v", err))
			return
		}

		defer func() {
			if err := writer.Close(); err != nil {
				ctx.cancel(simpleTrzszError("Close encode writer error: %v", err))
			}
		}()

		for data := range fileDataChan {
			if err := writeAll(writer, data); err != nil {
				ctx.cancel(simpleTrzszError("Write to encode writer error: %v", err))
				return
			}
			if t.flushInTime {
				if err := writer.Flush(); err != nil {
					ctx.cancel(simpleTrzszError("Flush to encode writer error: %v", err))
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

func (t *trzszTransfer) pipelineRecvCurrentAck() (int64, int64, bool, error) {
	resp, _, pause, err := t.recvCheckV2("SUCC")
	if err != nil {
		return 0, 0, pause, err
	}

	tokens := strings.Split(string(resp), "/")
	if len(tokens) != 2 {
		return 0, 0, pause, simpleTrzszError("Response number is not 2 but %d", len(tokens))
	}

	length, err := strconv.ParseInt(tokens[0], 10, 64)
	if err != nil {
		return 0, 0, pause, simpleTrzszError("Parse int from %s error: %v", tokens[0], err)
	}

	step, err := strconv.ParseInt(tokens[1], 10, 64)
	if err != nil {
		return 0, 0, pause, simpleTrzszError("Parse int from %s error: %v", tokens[1], err)
	}

	return length, step, pause, nil
}

func (t *trzszTransfer) pipelineRecvFinalAck(ctx *pipelineContext, size int64, progressChan chan<- int64) {
	for ctx.Err() == nil {
		resp, _, _, err := t.recvCheckV2("SUCC")
		if err != nil {
			ctx.cancel(err)
			return
		}
		step, err := strconv.ParseInt(string(resp), 10, 64)
		if err != nil {
			ctx.cancel(err)
			return
		}

		if step > size {
			ctx.cancel(simpleTrzszError("RecvFinalAck expected step %d but was %d", size, step))
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

func (t *trzszTransfer) pipelineSendData(ctx *pipelineContext, sendDataChan <-chan trzszData) <-chan trzszAck {
	ackChan := make(chan trzszAck, kAckChanBufferSize)
	deliver := func(buffer []byte, length int, encoded bool) error {
		beginTime, err := t.sendDataV2(buffer, length, encoded)
		if err != nil {
			return err
		}
		select {
		case ackChan <- trzszAck{*beginTime, int64(length)}:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	go func() {
		defer close(ackChan)
		for data := range sendDataChan {
			if ctx.Err() != nil {
				return
			}
			bufSize := int(t.bufferSize.Load())
			if len(data.data) <= bufSize { // send all at once
				if err := deliver(data.buffer, len(data.data), true); err != nil {
					ctx.cancel(err)
					return
				}
				continue
			}
			// split and send
			for data.index < len(data.data) {
				left := len(data.data) - data.index
				bufSize := int(t.bufferSize.Load())
				if bufSize > left {
					bufSize = left
				}
				nextIdx := data.index + bufSize
				if err := deliver(data.data[data.index:nextIdx], bufSize, false); err != nil {
					ctx.cancel(err)
					return
				}
				data.index = nextIdx
				if ctx.Err() != nil {
					return
				}
			}
		}
	}()
	return ackChan
}

func (t *trzszTransfer) pipelineRecvAck(ctx *pipelineContext, size int64, ackChan <-chan trzszAck, showProgress bool) <-chan int64 {
	var progressChan chan int64
	if showProgress {
		progressChan = make(chan int64, 100)
	}
	go func() {
		if showProgress {
			defer close(progressChan)
		}
		ignoreChunkTimeCount := 0
		for ack := range ackChan {
			length, step, pause, err := t.pipelineRecvCurrentAck()
			if err != nil {
				ctx.cancel(err)
				return
			}
			if length != ack.length {
				ctx.cancel(simpleTrzszError("SendData length check [%d] <> [%d]", length, ack.length))
				return
			}

			if showProgress {
				select {
				case progressChan <- step:
				case <-ctx.Done():
					return
				}
			}

			if pause {
				ignoreChunkTimeCount = kAckChanBufferSize + 2
			}

			if ignoreChunkTimeCount <= 0 || t.bufInitPhase.Load() {
				chunkTime := time.Since(ack.begin)
				bufSize := t.bufferSize.Load()

				if length == bufSize && chunkTime < 500*time.Millisecond && bufSize < t.transferConfig.MaxBufSize {
					t.bufferSize.Store(minInt64(bufSize*2, t.transferConfig.MaxBufSize))
					if t.bufInitPhase.Load() {
						t.bufInitWG.Done()
					}
				} else {
					if t.bufInitPhase.Load() {
						t.bufInitPhase.Store(false)
						t.bufInitWG.Done()
					}
					if chunkTime >= 2*time.Second && length <= bufSize {
						bufSize = bufSize / int64(chunkTime/time.Second)
						if bufSize < 1024 {
							bufSize = 1024
						}
						t.bufferSize.Store(bufSize)
					}
				}

				t.setLastChunkTime(chunkTime)
			} else {
				ignoreChunkTimeCount--
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

func (t *trzszTransfer) pipelineShowProgress(ctx *pipelineContext, progress progressCallback,
	progressChan <-chan int64) *sync.WaitGroup {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for step := range progressChan {
			progress.onStep(step)
			if ctx.Err() != nil {
				return
			}
		}
	}()
	return &wg
}

func (t *trzszTransfer) sendFileDataV2(file fileReader, progress progressCallback) ([]byte, error) {
	compress, err := t.sendCompressFlag(file)
	if err != nil {
		return nil, err
	}

	c, cancel := context.WithCancelCause(context.Background())
	ctx := &pipelineContext{c, cancel, make(chan struct{}, 1)}
	defer ctx.cancel(nil)
	defer close(ctx.succ)

	fileDataChan, md5SourceChan := t.pipelineReadData(ctx, file)

	md5DigestChan := t.pipelineCalculateMD5(ctx, md5SourceChan)

	sendDataChan := t.pipelineEncodeData(ctx, fileDataChan, compress)

	ackChan := t.pipelineSendData(ctx, sendDataChan)

	showProgress := progress != nil
	progressChan := t.pipelineRecvAck(ctx, file.getSize(), ackChan, showProgress)

	if showProgress {
		wg := t.pipelineShowProgress(ctx, progress, progressChan)
		defer wg.Wait()
	}

	select {
	case <-ctx.succ:
		return <-md5DigestChan, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

func (t *trzszTransfer) pipelineRecvBase64Data() ([]byte, *time.Time, error) {
	buf, beginTime, _, err := t.recvCheckV2("DATA")
	return buf, beginTime, err
}

func (t *trzszTransfer) pipelineRecvBinaryData() ([]byte, *time.Time, error) {
	buf, beginTime, _, err := t.recvCheckV2("DATA")
	if err != nil {
		return nil, nil, err
	}

	size, err := strconv.ParseInt(string(buf), 10, 64)
	if err != nil {
		return nil, nil, err
	}

	if size == 0 {
		return []byte{}, beginTime, nil
	}

	data, err := t.buffer.readBinary(int(size), t.getNewTimeout())
	if err != nil {
		if e := t.checkStop(); e != nil {
			return nil, nil, e
		}
		return nil, nil, err
	}
	return data, beginTime, nil
}

func (t *trzszTransfer) pipelineSendAck(ctx *pipelineContext, size int64, ackChan <-chan int) chan<- struct{} {
	ackImmediatelyChan := make(chan struct{}, 1)
	go func() {
		// send an ack for each step
		for length := range ackChan {
			if err := t.checkStopAndPause("SUCC"); err != nil {
				ctx.cancel(err)
				return
			}
			step := t.savedSteps.Load()
			if err := t.writeAll([]byte(fmt.Sprintf("#SUCC:%d/%d%s", length, step, t.transferConfig.Newline))); err != nil {
				ctx.cancel(err)
				return
			}
			if ctx.Err() != nil {
				return
			}
		}

		// send ack until all data is saved to disk
		for ctx.Err() == nil {
			if err := t.checkStopAndPause("SUCC"); err != nil {
				ctx.cancel(err)
				return
			}
			step := t.savedSteps.Load()
			if err := t.sendInteger("SUCC", step); err != nil {
				ctx.cancel(err)
				return
			}

			if step > size {
				ctx.cancel(simpleTrzszError("SendFinalAck expected step %d but was %d", size, step))
				return
			}

			if step == size {
				if ctx.Err() == nil {
					ctx.succ <- struct{}{}
				}
				break
			}

			select {
			case <-ackImmediatelyChan:
			case <-time.After(200 * time.Millisecond):
			}
		}
	}()
	return ackImmediatelyChan
}

func (t *trzszTransfer) pipelineRecvData(ctx *pipelineContext) (<-chan int, <-chan []byte) {
	ackChan := make(chan int, 100)
	recvDataChan := make(chan []byte, 100)
	go func() {
		defer close(ackChan)
		defer close(recvDataChan)
		t.savedSteps.Store(0)
		for ctx.Err() == nil {
			var err error
			var data []byte
			var beginTime *time.Time
			if t.transferConfig.Binary {
				data, beginTime, err = t.pipelineRecvBinaryData()
			} else {
				data, beginTime, err = t.pipelineRecvBase64Data()
			}
			if err != nil {
				ctx.cancel(err)
				return
			}

			select {
			case ackChan <- len(data):
			case <-ctx.Done():
				return
			}

			t.setLastChunkTime(time.Since(*beginTime))

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
	}()
	return ackChan, recvDataChan
}

func (t *trzszTransfer) pipelineDecodeData(ctx *pipelineContext, recvDataChan <-chan []byte, compress bool) (<-chan []byte, <-chan []byte) {
	fileDataChan := make(chan []byte, 100)
	md5SourceChan := make(chan []byte, 100)
	go func() {
		defer close(fileDataChan)
		defer close(md5SourceChan)
		var err error
		var reader readCloser
		if t.transferConfig.Binary {
			if compress {
				reader, err = newZstdReader(newEscapeReader(t.transferConfig.EscapeTable, newRecvDataReader(ctx, recvDataChan)))
			} else {
				reader = newEscapeReader(t.transferConfig.EscapeTable, newRecvDataReader(ctx, recvDataChan))
			}
		} else {
			if compress {
				reader, err = newZstdReader(newBase64Reader(newRecvDataReader(ctx, recvDataChan)))
			} else {
				reader = newBase64Reader(newRecvDataReader(ctx, recvDataChan))
			}
		}
		if err != nil {
			ctx.cancel(simpleTrzszError("New decode reader error: %v", err))
			return
		}
		defer reader.Close()
		for ctx.Err() == nil {
			buffer := make([]byte, 32*1024)
			n, err := reader.Read(buffer)
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
				ctx.cancel(simpleTrzszError("Read from decode reader error: %v", err))
				return
			}
		}
	}()
	return fileDataChan, md5SourceChan
}

func (t *trzszTransfer) pipelineSaveData(ctx *pipelineContext, file fileWriter, size int64,
	fileDataChan <-chan []byte, ackImmediatelyChan chan<- struct{}, showProgress bool) <-chan int64 {
	var progressChan chan int64
	if showProgress {
		progressChan = make(chan int64, 100)
	}
	go func() {
		defer close(ackImmediatelyChan)
		if showProgress {
			defer close(progressChan)
		}
		step := int64(0)
		for data := range fileDataChan {
			if err := writeAll(file, data); err != nil {
				ctx.cancel(simpleTrzszError("Write file error: %v", err))
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
			ctx.cancel(simpleTrzszError("SaveFile expected step %d but was %d", size, step))
			return
		}
		ackImmediatelyChan <- struct{}{}
	}()
	return progressChan
}

func (t *trzszTransfer) recvFileDataV2(file fileWriter, size int64, progress progressCallback) ([]byte, error) {
	defer file.Close()

	compress, err := t.recvCompressFlag(size)
	if err != nil {
		return nil, err
	}

	c, cancel := context.WithCancelCause(context.Background())
	ctx := &pipelineContext{c, cancel, make(chan struct{}, 1)}
	defer ctx.cancel(nil)
	defer close(ctx.succ)

	ackChan, recvDataChan := t.pipelineRecvData(ctx)

	ackImmediatelyChan := t.pipelineSendAck(ctx, size, ackChan)

	fileDataChan, md5SourceChan := t.pipelineDecodeData(ctx, recvDataChan, compress)

	md5DigestChan := t.pipelineCalculateMD5(ctx, md5SourceChan)

	showProgress := progress != nil
	progressChan := t.pipelineSaveData(ctx, file, size, fileDataChan, ackImmediatelyChan, showProgress)

	if showProgress {
		wg := t.pipelineShowProgress(ctx, progress, progressChan)
		defer wg.Wait()
	}

	select {
	case <-ctx.succ:
		return <-md5DigestChan, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}
