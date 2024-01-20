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
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
)

const kPrefixHashStep = 10 * 1024 * 1024

type prefixHash struct {
	Step int64  `json:"step"`
	Hash string `json:"hash"`
	Over bool   `json:"over"`
}

type prefixHashAck struct {
	Step  int64 `json:"step"`
	Match bool  `json:"match"`
}

func (t *trzszTransfer) sendHash(hash *prefixHash) error {
	jstr, err := json.Marshal(hash)
	if err != nil {
		return err
	}
	return t.sendString("HASH", string(jstr))
}

func (t *trzszTransfer) recvHash() (*prefixHash, error) {
	line, err := t.recvString("HASH", false, t.getNewTimeout())
	if err != nil {
		return nil, err
	}
	var hash prefixHash
	if err := json.Unmarshal([]byte(line), &hash); err != nil {
		return nil, err
	}
	return &hash, nil
}

func (t *trzszTransfer) sendHashAck(hashAck *prefixHashAck) error {
	jstr, err := json.Marshal(hashAck)
	if err != nil {
		return err
	}
	return t.sendString("SUCC", string(jstr))
}

func (t *trzszTransfer) recvHashAck() (*prefixHashAck, error) {
	line, err := t.recvString("SUCC", false, t.getNewTimeout())
	if err != nil {
		return nil, err
	}
	var hashAck prefixHashAck
	if err := json.Unmarshal([]byte(line), &hashAck); err != nil {
		return nil, err
	}
	return &hashAck, nil
}

func (t *trzszTransfer) pipelineSendHash(ctx context.Context, cancel context.CancelCauseFunc,
	stopNow *atomic.Bool, file *os.File, size int64) *sync.WaitGroup {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		step := int64(0)
		hasher := md5.New()
		buffer := make([]byte, kPrefixHashStep)
		for step < size && ctx.Err() == nil && !stopNow.Load() {
			buf := buffer
			m := size - step
			if m < kPrefixHashStep {
				buf = buffer[:m]
			}
			n, err := file.Read(buf)
			if err != nil {
				cancel(err)
				return
			}
			step += int64(n)
			hasher.Write(buf[:n])
			if err := t.sendHash(&prefixHash{Step: step, Hash: fmt.Sprintf("%x", hasher.Sum(nil))}); err != nil {
				cancel(err)
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		if err := t.sendHash(&prefixHash{Over: true}); err != nil {
			cancel(err)
			return
		}
	}()
	return &wg
}

func (t *trzszTransfer) pipelineRecvHashAck(ctx context.Context, cancel context.CancelCauseFunc,
	size int64, progress progressCallback) <-chan int64 {
	matchChan := make(chan int64, 1)
	go func() {
		defer close(matchChan)
		matchStep := int64(0)
		for ctx.Err() == nil {
			hashAck, err := t.recvHashAck()
			if err != nil {
				cancel(err)
				return
			}

			if !hashAck.Match {
				matchChan <- matchStep
				return
			}

			matchStep = hashAck.Step
			if progress != nil {
				progress.onStep(matchStep)
			}

			if matchStep == size {
				matchChan <- matchStep
				return
			} else if matchStep > size {
				cancel(simpleTrzszError("Hash step check [%d] > [%d]", matchStep, size))
				return
			}
		}
	}()
	return matchChan
}

func (t *trzszTransfer) sendPrefixHash(file *os.File, srcFile *sourceFile, tgtFile *targetFile,
	progress progressCallback) (int64, error) {
	if tgtFile.Size <= 0 || file == nil {
		return srcFile.Size, nil
	}

	if progress != nil {
		progress.onSize(srcFile.Size)
	}
	if t.transferConfig.Protocol < kProtocolVersion4 {
		if err := t.sendInteger("SIZE", srcFile.Size); err != nil {
			return 0, err
		}
	}

	var stopNow atomic.Bool
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	size := minInt64(srcFile.Size, tgtFile.Size)
	wg := t.pipelineSendHash(ctx, cancel, &stopNow, file, size)
	matchChan := t.pipelineRecvHashAck(ctx, cancel, size, progress)

	var matchStep int64
	select {
	case <-ctx.Done():
		return 0, context.Cause(ctx)
	case matchStep = <-matchChan:
	}

	stopNow.Store(true)
	wg.Wait()
	if ctx.Err() != nil {
		return 0, context.Cause(ctx)
	}

	if _, err := file.Seek(matchStep, io.SeekStart); err != nil {
		return 0, err
	}
	if progress != nil {
		progress.setPreSize(matchStep)
	}
	return srcFile.Size - matchStep, nil
}

func (t *trzszTransfer) sendFileNameV3(srcFile *sourceFile, progress progressCallback) (fileReader, string, error) {
	source, err := srcFile.marshalSourceFile()
	if err != nil {
		return nil, "", err
	}
	if err := t.sendString("NAME", source); err != nil {
		return nil, "", err
	}

	target, err := t.recvString("SUCC", false, t.getNewTimeout())
	if err != nil {
		return nil, "", err
	}
	tgtFile, err := unmarshalTargetFile(target)
	if err != nil {
		return nil, "", err
	}

	if progress != nil {
		progress.onName(srcFile.getFileName())
	}

	if len(srcFile.SubFiles) > 0 {
		file, err := t.newArchiveReader(srcFile)
		if err != nil {
			return nil, "", err
		}
		return file, tgtFile.Name, nil
	}

	if srcFile.IsDir {
		return nil, tgtFile.Name, nil
	}

	file, err := os.Open(srcFile.AbsPath)
	if err != nil {
		return nil, "", err
	}

	size, err := t.sendPrefixHash(file, srcFile, tgtFile, progress)
	if err != nil {
		file.Close()
		return nil, "", err
	}

	return &simpleFileReader{file, size}, tgtFile.Name, nil
}

func (t *trzszTransfer) recvPrefixHash(writer fileWriter, srcFile *sourceFile, tgtFile *targetFile,
	progress progressCallback) error {
	if tgtFile.Size <= 0 || writer == nil || writer.getFile() == nil {
		return nil
	}
	file := writer.getFile()

	var size int64
	if t.transferConfig.Protocol < kProtocolVersion4 {
		var err error
		size, err = t.recvInteger("SIZE", false, t.getNewTimeout())
		if err != nil {
			return err
		}
	} else {
		size = srcFile.Size
	}
	if progress != nil {
		progress.onSize(size)
	}

	match := true
	hasher := md5.New()
	matchStep := int64(0)
	for {
		hash, err := t.recvHash()
		if err != nil {
			return err
		}
		if hash.Over {
			break
		}
		if !match {
			continue
		}

		step := hash.Step - matchStep
		buffer := make([]byte, step)
		n, err := io.ReadFull(file, buffer)
		if err != nil {
			return err
		}
		hasher.Write(buffer[:n])

		match = hash.Hash == fmt.Sprintf("%x", hasher.Sum(nil))
		if match {
			matchStep = hash.Step
			if progress != nil {
				progress.onStep(matchStep)
			}
		}
		if err := t.sendHashAck(&prefixHashAck{Step: hash.Step, Match: match}); err != nil {
			return err
		}
	}

	if progress != nil {
		progress.setPreSize(matchStep)
	}
	if _, err := file.Seek(matchStep, io.SeekStart); err != nil {
		return err
	}
	if err := file.Truncate(matchStep); err != nil {
		return err
	}
	return nil
}

func (t *trzszTransfer) recvFileNameV3(path string, progress progressCallback) (fileWriter, string, error) {
	jsonName, err := t.recvString("NAME", false, t.getNewTimeout())
	if err != nil {
		return nil, "", err
	}
	srcFile, err := unmarshalSourceFile(jsonName)
	if err != nil {
		return nil, "", err
	}
	file, localName, err := t.createDirOrFile(path, srcFile, false)
	if err != nil {
		return nil, "", err
	}

	size := int64(0)
	if file != nil && file.getFile() != nil {
		stat, err := file.getFile().Stat()
		if err != nil {
			file.Close()
			return nil, "", err
		}
		size = stat.Size()
	}

	tgtFile := &targetFile{Name: localName, Size: size}
	target, err := tgtFile.marshalTargetFile()
	if err != nil {
		if file != nil {
			file.Close()
		}
		return nil, "", err
	}

	if err := t.sendString("SUCC", target); err != nil {
		if file != nil {
			file.Close()
		}
		return nil, "", err
	}

	if progress != nil {
		progress.onName(srcFile.getFileName())
	}

	if err := t.recvPrefixHash(file, srcFile, tgtFile, progress); err != nil {
		if file != nil {
			file.Close()
		}
		return nil, "", err
	}

	return file, localName, nil
}
