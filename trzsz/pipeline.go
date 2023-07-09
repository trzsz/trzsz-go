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
	"strconv"
	"strings"
	"time"
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
	case b.sendDataChan <- trzszData{data, buffer.Bytes(), 0}:
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

func (b *base64Writer) Close() error {
	b.transfer.bufInitPhase.Store(false)
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

func (t *trzszTransfer) pipelineReadData(ctx *pipelineContext, file *os.File, size int64) (<-chan []byte, <-chan []byte) {
	fileDataChan := make(chan []byte, 100)
	md5SourceChan := make(chan []byte, 100)
	go func() {
		defer close(fileDataChan)
		defer close(md5SourceChan)
		step := int64(0)
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
			if err != nil {
				ctx.cancel(newSimpleTrzszError(fmt.Sprintf("Read file error: %v", err)))
				return
			}
		}
	}()
	return fileDataChan, md5SourceChan
}

func (t *trzszTransfer) pipelineEncodeData(ctx *pipelineContext, fileDataChan <-chan []byte) <-chan trzszData {
	sendDataChan := make(chan trzszData, 5)
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
	sendDataChan := make(chan trzszData, 5)
	deliver := func(data []byte) bool {
		buffer := bytes.NewBuffer(make([]byte, 0, len(data)+0x20))
		buffer.Write([]byte(("#DATA:")))
		buffer.Write([]byte(strconv.Itoa(len(data))))
		buffer.Write([]byte(t.transferConfig.Newline))
		buffer.Write(data)
		select {
		case sendDataChan <- trzszData{data, buffer.Bytes(), 0}:
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
				if t.bufInitPhase.Load() {
					t.bufInitWG.Add(1)
				}

				b := buffer.Bytes()
				if !deliver(b[:bufSize]) {
					return
				}
				buffer = bytes.NewBuffer(b[bufSize:])

				if t.bufInitPhase.Load() {
					t.bufInitWG.Wait()
				}
				bufSize = int(t.bufferSize.Load())
			}
			if ctx.Err() != nil {
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		t.bufInitPhase.Store(false)
		if buffer.Len() > 0 {
			if !deliver(buffer.Bytes()) {
				return
			}
		}
		deliver([]byte{}) // send the finish flag
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
		return 0, 0, pause, newSimpleTrzszError(fmt.Sprintf("Response number is not 2 but %d", len(tokens)))
	}

	length, err := strconv.ParseInt(tokens[0], 10, 64)
	if err != nil {
		return 0, 0, pause, newSimpleTrzszError(fmt.Sprintf("Parse int from %s error: %v", tokens[0], err))
	}

	step, err := strconv.ParseInt(tokens[1], 10, 64)
	if err != nil {
		return 0, 0, pause, newSimpleTrzszError(fmt.Sprintf("Parse int from %s error: %v", tokens[1], err))
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
				ctx.cancel(newSimpleTrzszError(fmt.Sprintf("SendData length check [%d] <> [%d]", length, ack.length)))
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

	ackChan := t.pipelineSendData(ctx, sendDataChan)

	showProgress := progress != nil
	progressChan := t.pipelineRecvAck(ctx, size, ackChan, showProgress)

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
			buffer := make([]byte, 32*1024)
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

func (t *trzszTransfer) pipelineSaveData(ctx *pipelineContext, file *os.File, size int64,
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
		ackImmediatelyChan <- struct{}{}
	}()
	return progressChan
}

func (t *trzszTransfer) recvFileDataV2(file *os.File, size int64, progress progressCallback) ([]byte, error) {
	defer file.Close()
	c, cancel := context.WithCancelCause(context.Background())
	ctx := &pipelineContext{c, cancel, make(chan struct{}, 1)}
	defer ctx.cancel(nil)
	defer close(ctx.succ)

	ackChan, recvDataChan := t.pipelineRecvData(ctx)

	ackImmediatelyChan := t.pipelineSendAck(ctx, size, ackChan)

	var fileDataChan <-chan []byte
	var md5SourceChan <-chan []byte
	if t.transferConfig.Binary {
		fileDataChan, md5SourceChan = t.pipelineUnescapeData(ctx, recvDataChan)
	} else {
		fileDataChan, md5SourceChan = t.pipelineDecodeData(ctx, recvDataChan)
	}

	md5DigestChan := t.pipelineCalculateMD5(ctx, md5SourceChan)

	showProgress := progress != nil
	progressChan := t.pipelineSaveData(ctx, file, size, fileDataChan, ackImmediatelyChan, showProgress)

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
