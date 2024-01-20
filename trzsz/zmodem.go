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
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync/atomic"
	"time"
)

// zmodem escape leader char
const kZDLE byte = 030

type zmodemTransfer struct {
	upload          bool
	logger          *traceLogger
	serverIn        io.Writer
	clientOut       io.Writer
	cmd             atomic.Pointer[exec.Cmd]
	stdin           io.WriteCloser
	stdout          io.ReadCloser
	clientFinished  atomic.Bool
	serverFinished  atomic.Bool
	errorOccurred   atomic.Bool
	stopped         atomic.Bool
	cleaned         atomic.Bool
	cleanupTimer    *time.Timer
	clientTimer     *time.Timer
	serverTimer     *time.Timer
	lastUpdateTime  *time.Time
	transferredSize int64
	recentSpeed     recentSpeed
}

var zmodemOverAndOut = []byte("OO\x08\x08")
var zmodemCanNotOpenFile = []byte("cannot open ")
var zmodemCancelSubSequence = []byte("\x18\x18\x18\x18\x18")
var zmodemCancelFullSequence = []byte("\x18\x18\x18\x18\x18\x18\x18\x18\x18\x18\x08\x08\x08\x08\x08\x08\x08\x08\x08\x08")

var zmodemInitRegexp = regexp.MustCompile(`\*\*\x18B0(0|1)[0-9a-f]{12}`)
var zmodemFinishRegexp = regexp.MustCompile(`\*\*\x18B08[0-9a-f]{12}`)

func detectZmodem(buf []byte) *zmodemTransfer {
	match := zmodemInitRegexp.FindSubmatch(buf)
	if len(match) < 2 || bytes.Contains(buf, zmodemCancelSubSequence) || bytes.Contains(buf, zmodemCanNotOpenFile) {
		return nil
	}
	if match[1][0] == '1' {
		return &zmodemTransfer{upload: true}
	} else {
		return &zmodemTransfer{upload: false}
	}
}

func (z *zmodemTransfer) writeMessage(msg string) {
	_ = writeAll(z.clientOut, []byte(fmt.Sprintf("\r\x1b[2K%s\r\n", msg)))
}

func (z *zmodemTransfer) updateProgress(buf []byte) {
	if buf == nil {
		now := timeNowFunc()
		z.showProgress(&now)
		_ = writeAll(z.clientOut, []byte("\r\n"))
		return
	}
	// rough estimate of the size currently transferred
	for _, c := range buf {
		if c != kZDLE {
			z.transferredSize++
		}
	}
	now := timeNowFunc()
	if z.lastUpdateTime != nil && now.Sub(*z.lastUpdateTime) < 200*time.Millisecond {
		return
	}
	z.lastUpdateTime = &now
	z.showProgress(&now)
}

func (z *zmodemTransfer) showProgress(now *time.Time) {
	_ = writeAll(z.clientOut, []byte(fmt.Sprintf("\r\x1b[2KTransferred %s, Speed %s.",
		convertSizeToString(float64(z.transferredSize)), z.getSpeed(now))))
}

func (z *zmodemTransfer) getSpeed(now *time.Time) string {
	if z.recentSpeed.speedCnt == 0 {
		z.recentSpeed.initFirstStep(now)
		return "N/A"
	}

	speed := z.recentSpeed.getSpeed(z.transferredSize, now)
	if speed < 0 {
		return "N/A"
	}
	return convertSizeToString(speed) + "/s"
}

func (z *zmodemTransfer) resetCleanupTimer() {
	if z.cleanupTimer != nil {
		z.cleanupTimer.Stop()
	}
	z.cleanupTimer = time.AfterFunc(500*time.Millisecond, func() {
		z.cleaned.Store(true)
		_, _ = z.serverIn.Write([]byte("\r")) // enter for shell prompt
	})
}

func (z *zmodemTransfer) resetClientTimer() {
	if !z.upload {
		return
	}
	if z.clientTimer != nil {
		z.clientTimer.Stop()
	}
	z.clientTimer = time.AfterFunc(20*time.Second, func() {
		z.handleZmodemError("client timeout")
	})
}

func (z *zmodemTransfer) resetServerTimer() {
	if z.upload {
		return
	}
	if z.serverTimer != nil {
		z.serverTimer.Stop()
	}
	z.serverTimer = time.AfterFunc(20*time.Second, func() {
		z.handleZmodemError("server timeout")
	})
}

func (z *zmodemTransfer) isTransferringFiles() bool {
	return !z.stopped.Load() || !z.cleaned.Load()
}

func (z *zmodemTransfer) stopTransferringFiles() {
	z.handleZmodemError("Stopped")
}

func (z *zmodemTransfer) handleZmodemError(msg string) {
	if !z.stopped.CompareAndSwap(false, true) {
		return
	}

	z.errorOccurred.Store(true)

	if z.logger != nil {
		z.logger.writeTraceLog([]byte(msg), "debug")
	}

	_ = writeAll(z.serverIn, zmodemCancelFullSequence)

	if cmd := z.cmd.Load(); cmd != nil {
		_ = writeAll(z.stdin, zmodemCancelFullSequence)
		z.ensureClientExit(cmd)
	}

	z.writeMessage(msg)
}

func (z *zmodemTransfer) handleServerOutput(buf []byte) bool {
	if z.stopped.Load() {
		if z.cleaned.Load() {
			return false
		}
		z.resetCleanupTimer()
		return true
	}

	// forward server output to the client
	if cmd := z.cmd.Load(); cmd != nil {
		z.resetServerTimer()
		if len(buf) < 50 && zmodemFinishRegexp.Match(buf) {
			if z.serverFinished.CompareAndSwap(false, true) {
				z.ensureOverAndOut()
			}
		}
		err := writeAll(z.stdin, buf)
		if err == nil && !z.upload {
			z.updateProgress(buf)
		}
		return true
	}

	// server canceled before the client startup
	if bytes.Contains(buf, zmodemCancelSubSequence) || bytes.Contains(buf, zmodemCanNotOpenFile) {
		z.cleaned.Store(true)
		z.stopped.Store(true)
		return false
	}

	// skip it and wait for the client to start
	return true
}

func (z *zmodemTransfer) handleZmodemStream(cmd *exec.Cmd) {
	if z.logger != nil {
		z.logger.writeTraceLog([]byte("zmodem begin"), "debug")
	}
	z.cmd.Store(cmd)
	z.resetClientTimer()
	z.resetServerTimer()

	// async check if the client has exited
	go z.checkClientExited(cmd)

	// forward client output to the server
	buffer := make([]byte, 32*1024)
	for {
		n, err := z.stdout.Read(buffer)
		z.resetClientTimer()
		if n > 0 {
			buf := buffer[:n]
			if z.logger != nil {
				z.logger.writeTraceLog(buf, "zmodem")
			}
			if z.errorOccurred.Load() || z.serverFinished.Load() && z.clientFinished.Load() {
				if z.logger != nil {
					z.logger.writeTraceLog([]byte("ignore zmodem output"), "debug")
				}
				break
			}
			if len(buf) < 50 && zmodemFinishRegexp.Match(buf) {
				if z.clientFinished.CompareAndSwap(false, true) {
					z.ensureOverAndOut()
				}
			}
			if err := writeAll(z.serverIn, buf); err != nil {
				z.handleZmodemError(fmt.Sprintf("write to server failed: %v", err))
				break
			}
			if z.upload {
				z.updateProgress(buf)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			z.handleZmodemError(fmt.Sprintf("read from client failed: %v", err))
			break
		}
	}

	if z.clientTimer != nil {
		z.clientTimer.Stop()
	}

	z.ensureClientExit(cmd)
}

func (z *zmodemTransfer) ensureOverAndOut() {
	if !z.serverFinished.Load() || !z.clientFinished.Load() {
		return
	}
	if z.logger != nil {
		z.logger.writeTraceLog([]byte("Over and Out"), "debug")
	}
	if z.upload {
		_ = writeAll(z.serverIn, zmodemOverAndOut)
	} else {
		_ = writeAll(z.stdin, zmodemOverAndOut)
	}
}

func (z *zmodemTransfer) ensureClientExit(cmd *exec.Cmd) {
	go func() {
		time.Sleep(500 * time.Millisecond)
		_ = cmd.Process.Kill()
	}()
}

func (z *zmodemTransfer) checkClientExited(cmd *exec.Cmd) {
	_ = cmd.Wait()
	z.stopped.Store(true)

	if z.serverTimer != nil {
		z.serverTimer.Stop()
	}

	z.updateProgress(nil) // nil means finished

	if z.logger != nil {
		z.logger.writeTraceLog([]byte("zmodem end"), "debug")
	}

	if code := cmd.ProcessState.ExitCode(); code != 0 {
		z.writeMessage(fmt.Sprintf("client exit with %d", code))
	} else {
		z.writeMessage("\033[1;32mSuccess!!\033[0m")
	}

	z.resetCleanupTimer()

	// make sure the server exit
	_ = writeAll(z.serverIn, zmodemCancelFullSequence)
}

func (z *zmodemTransfer) launchZmodemCmd(dir, name string, args ...string) (*exec.Cmd, error) {
	var err error
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	z.stdin, err = cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	z.stdout, err = cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

func (z *zmodemTransfer) uploadFiles(files []string) {
	workDir := ""
	for i := 0; i < len(files); i++ {
		fileDir := filepath.Dir(files[i])
		if i == 0 {
			workDir = fileDir
		}
		if fileDir == workDir {
			files[i] = filepath.Base(files[i])
		}
	}
	cmd, err := z.launchZmodemCmd(workDir, "sz", append([]string{"-e", "-b", "-B", "32768"}, files...)...)
	if err != nil {
		z.handleZmodemError(fmt.Sprintf("run sz client failed: %v", err))
		return
	}
	z.handleZmodemStream(cmd)
}

func (z *zmodemTransfer) downloadFiles(path string) {
	cmd, err := z.launchZmodemCmd(path, "rz", "-E", "-e", "-b", "-B", "32768")
	if err != nil {
		z.handleZmodemError(fmt.Sprintf("run rz client failed: %v", err))
		return
	}
	z.handleZmodemStream(cmd)
}

func (z *zmodemTransfer) handleZmodemEvent(logger *traceLogger, serverIn io.Writer, clientOut io.Writer,
	chooseUploadFiles func() ([]string, error), chooseDownloadPath func() (string, error)) {
	z.logger = logger
	z.serverIn = serverIn
	z.clientOut = clientOut

	// the server may fail immediately
	time.Sleep(100 * time.Millisecond)
	if z.stopped.Load() {
		return
	}

	if z.upload {
		files, err := chooseUploadFiles()
		if err != nil {
			z.handleZmodemError(err.Error())
			return
		}
		z.uploadFiles(files)
	} else {
		path, err := chooseDownloadPath()
		if err != nil {
			z.handleZmodemError(err.Error())
			return
		}
		z.downloadFiles(path)
	}
}
