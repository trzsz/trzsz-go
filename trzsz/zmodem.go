/*
MIT License

Copyright (c) 2022-2026 The Trzsz Authors.

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
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
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
	redrawScreen    *func()
	autoRename      bool
	outputWG        sync.WaitGroup
	recvFiles       []string
	sendFiles       []string
	skipFiles       []string
}

var zmodemOverAndOut = []byte("OO\x08\x08")
var zmodemCanNotOpenFile = []byte("cannot open ")
var zmodemCancelSubSequence = []byte("\x18\x18\x18\x18\x18")
var zmodemCancelFullSequence = []byte("\x18\x18\x18\x18\x18\x18\x18\x18\x18\x18\x08\x08\x08\x08\x08\x08\x08\x08\x08\x08")

var (
	initZmodemRegexpOnce sync.Once
	zmodemInitRegexp     *regexp.Regexp
	zmodemFinishRegexp   *regexp.Regexp
	zmodemRecvFileRegexp *regexp.Regexp
	zmodemSendFileRegexp *regexp.Regexp
	zmodemSkipFileRegexp *regexp.Regexp
)

func initZmodemRegexp() {
	initZmodemRegexpOnce.Do(func() {
		zmodemInitRegexp = regexp.MustCompile(`\*\*\x18B0(0|1)[0-9a-f]{12}`)
		zmodemFinishRegexp = regexp.MustCompile(`\*\*\x18B08[0-9a-f]{12}`)
		zmodemRecvFileRegexp = regexp.MustCompile(`Receiving:\s*([^\r]+)`)
		zmodemSendFileRegexp = regexp.MustCompile(`Sending:\s*([^\r]+)`)
		zmodemSkipFileRegexp = regexp.MustCompile(`(?:sz:\s*skipped|file exists,\s*skipped):\s*([^\r]+?)(?:\s*Skipped|[\r]|$)`)
	})
}

func detectZmodem(buf []byte, autoRename bool) *zmodemTransfer {
	initZmodemRegexp()

	match := zmodemInitRegexp.FindSubmatch(buf)
	if len(match) < 2 || bytes.Contains(buf, zmodemCancelSubSequence) || bytes.Contains(buf, zmodemCanNotOpenFile) {
		return nil
	}

	if match[1][0] == '1' {
		return &zmodemTransfer{upload: true, autoRename: autoRename}
	} else {
		return &zmodemTransfer{upload: false, autoRename: autoRename}
	}
}

func (z *zmodemTransfer) writeMessage(msg string) {
	_ = writeAll(z.clientOut, fmt.Appendf(nil, "\r\x1b[2K%s\r\n", msg))
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
	_ = writeAll(z.clientOut, fmt.Appendf(nil, "\r\x1b[2KTransferred %s, Speed %s.",
		convertSizeToString(float64(z.transferredSize)), z.getSpeed(now)))
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
		if z.redrawScreen != nil {
			(*z.redrawScreen)()
		} else {
			_, _ = z.serverIn.Write([]byte("\r")) // enter for shell prompt
		}
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
	z.outputWG.Add(1)
	go z.checkClientExited(cmd)

	defer func() {
		_ = z.stdout.Close()
		z.outputWG.Done()
	}()

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
	z.outputWG.Wait()
	z.stopped.Store(true)

	if z.serverTimer != nil {
		z.serverTimer.Stop()
	}

	_ = z.stdin.Close()

	z.updateProgress(nil) // nil means finished

	if z.logger != nil {
		z.logger.writeTraceLog([]byte("zmodem end"), "debug")
	}

	if code := cmd.ProcessState.ExitCode(); code != 0 {
		z.writeMessage(fmt.Sprintf("client exit with %d", code))
	} else {
		z.writeMessage("\033[1;32mSuccess!!\033[0m")

		if len(z.recvFiles) > 0 {
			_, _ = z.clientOut.Write(fmt.Appendf(nil, "\r\n%s\r\n", joinFileNames("Saved files:", z.recvFiles)))
		} else if len(z.sendFiles) > 0 {
			var files []string
			for _, name := range z.sendFiles {
				if !slices.Contains(z.skipFiles, name) {
					files = append(files, name)
				}
			}
			if len(files) > 0 {
				_, _ = z.clientOut.Write(fmt.Appendf(nil, "\r\n%s\r\n", joinFileNames("Transferred files:", files)))
			}
		}
		if len(z.skipFiles) > 0 {
			_, _ = z.clientOut.Write(fmt.Appendf(nil, "\r\n%s\r\n", joinFileNames("Skipped files:", z.skipFiles)))
		}
	}

	z.resetCleanupTimer()

	// make sure the server exit
	_ = writeAll(z.serverIn, zmodemCancelFullSequence)
}

func extractLastMatch(line string, re *regexp.Regexp) string {
	if matches := re.FindAllStringSubmatch(line, -1); len(matches) > 0 {
		lastMatch := matches[len(matches)-1]
		if len(lastMatch) > 1 {
			return strings.TrimSpace(lastMatch[1])
		}
	}
	return ""
}

func (z *zmodemTransfer) handleZmodemStderr(stderr io.ReadCloser) {
	z.outputWG.Go(func() {
		defer func() { _ = stderr.Close() }()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			if name := extractLastMatch(line, zmodemRecvFileRegexp); name != "" {
				z.recvFiles = append(z.recvFiles, name)
				continue
			}
			if name := extractLastMatch(line, zmodemSendFileRegexp); name != "" {
				z.sendFiles = append(z.sendFiles, name)
				continue
			}
			if name := extractLastMatch(line, zmodemSkipFileRegexp); name != "" {
				z.skipFiles = append(z.skipFiles, name)
				continue
			}
		}
		if err := scanner.Err(); err != nil && z.logger != nil {
			z.logger.writeTraceLog(fmt.Appendf(nil, "handle zmodem stderr failed: %v", err), "error")
		}
	})
}

func (z *zmodemTransfer) launchZmodemCmd(dir, name string, args ...string) (*exec.Cmd, error) {
	var err error
	cmd := exec.Command(name, args...)
	cmd.Dir = dir

	z.stdin, err = cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("cmd stdin pipe failed: %v", err)
	}

	var stdoutWriter *os.File
	z.stdout, stdoutWriter, err = os.Pipe()
	if err != nil {
		_ = z.stdin.Close()
		return nil, fmt.Errorf("cmd stdout pipe failed: %v", err)
	}

	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		_ = z.stdin.Close()
		_ = z.stdout.Close()
		_ = stdoutWriter.Close()
		return nil, fmt.Errorf("cmd stderr pipe failed: %v", err)
	}

	// The write sides are inherited by the child process. Close the copies held
	// by the parent process in all cases; otherwise the read sides won't reach
	// EOF and the file descriptors will leak if the command fails to start.
	defer func() {
		_ = stdoutWriter.Close()
		_ = stderrWriter.Close()
	}()

	cmd.Stdout, cmd.Stderr = stdoutWriter, stderrWriter

	if err := cmd.Start(); err != nil {
		_ = z.stdin.Close()
		_ = z.stdout.Close()
		_ = stderrReader.Close()
		return nil, err
	}

	z.handleZmodemStderr(stderrReader)

	return cmd, nil
}

func (z *zmodemTransfer) uploadFiles(files []string) {
	workDir := ""
	for i := range files {
		fileDir := filepath.Dir(files[i])
		if i == 0 {
			workDir = fileDir
		}
		if fileDir == workDir {
			files[i] = filepath.Base(files[i])
		}
	}
	args := []string{"-v", "-e", "-b", "-B", "32768"}
	if z.autoRename {
		args = append(args, "-E")
	}
	args = append(args, files...)
	cmd, err := z.launchZmodemCmd(workDir, "sz", args...)
	if err != nil {
		z.handleZmodemError(fmt.Sprintf("run sz client failed: %v", err))
		return
	}
	z.handleZmodemStream(cmd)
}

func (z *zmodemTransfer) downloadFiles(path string) {
	args := []string{"-v", "-e", "-b", "-B", "32768"}
	if z.autoRename {
		args = append(args, "-E")
	}
	cmd, err := z.launchZmodemCmd(path, "rz", args...)
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
