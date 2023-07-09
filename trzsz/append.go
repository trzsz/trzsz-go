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
				cancel(newSimpleTrzszError(fmt.Sprintf("Hash step check [%d] > [%d]", matchStep, size)))
				return
			}
		}
	}()
	return matchChan
}

func (t *trzszTransfer) sendPrefixHash(file *os.File, tgtFile *targetFile, progress progressCallback) (int64, error) {
	stat, err := file.Stat()
	if err != nil {
		return 0, err
	}

	if tgtFile.Size <= 0 {
		return stat.Size(), nil
	}

	var stopNow atomic.Bool
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	size := minInt64(tgtFile.Size, stat.Size())
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

	if _, err = file.Seek(matchStep, io.SeekStart); err != nil {
		return 0, err
	}
	if progress != nil {
		progress.setPreSize(matchStep)
	}
	return stat.Size() - matchStep, nil
}

func (t *trzszTransfer) sendFileNameV3(srcFile *sourceFile, progress progressCallback) (*os.File, int64, string, error) {
	source, err := srcFile.marshalSourceFile()
	if err != nil {
		return nil, 0, "", err
	}
	if err := t.sendString("NAME", source); err != nil {
		return nil, 0, "", err
	}

	target, err := t.recvString("SUCC", false, t.getNewTimeout())
	if err != nil {
		return nil, 0, "", err
	}
	tgtFile, err := unmarshalTargetFile(target)
	if err != nil {
		return nil, 0, "", err
	}

	if progress != nil {
		progress.onName(srcFile.getFileName())
	}

	if srcFile.IsDir {
		return nil, 0, tgtFile.Name, nil
	}

	file, err := os.Open(srcFile.AbsPath)
	if err != nil {
		return nil, 0, "", err
	}

	size, err := t.sendPrefixHash(file, tgtFile, progress)
	if err != nil {
		file.Close()
		return nil, 0, "", err
	}

	return file, size, tgtFile.Name, nil
}

func (t *trzszTransfer) recvPrefixHash(file *os.File, tgtFile *targetFile, progress progressCallback) error {
	if tgtFile.Size <= 0 {
		return nil
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

func (t *trzszTransfer) recvFileNameV3(path string, progress progressCallback) (*os.File, string, error) {
	jsonName, err := t.recvString("NAME", false, t.getNewTimeout())
	if err != nil {
		return nil, "", err
	}

	file, localName, fileName, err := t.createDirOrFile(path, jsonName, false)
	if err != nil {
		return nil, "", err
	}

	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, "", err
	}
	tgtFile := &targetFile{Name: localName, Size: stat.Size()}
	target, err := tgtFile.marshalTargetFile()
	if err != nil {
		file.Close()
		return nil, "", err
	}

	if err := t.sendString("SUCC", target); err != nil {
		file.Close()
		return nil, "", err
	}

	if progress != nil {
		progress.onName(fileName)
	}

	if err := t.recvPrefixHash(file, tgtFile, progress); err != nil {
		file.Close()
		return nil, "", err
	}

	return file, localName, nil
}
