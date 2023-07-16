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
	"fmt"
	"io"
	"math"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

func getDisplayLength(str string) int {
	length := 0
	for _, r := range []rune(str) { // nolint:all
		if utf8.RuneLen(r) == 1 {
			length++
		} else {
			length += 2
		}
	}
	return length
}

func getEllipsisString(str string, max int) (string, int) {
	var b strings.Builder
	b.Grow(max)
	max -= 3
	length := 0
	for _, r := range []rune(str) { // nolint:all
		if utf8.RuneLen(r) > 1 {
			if length+2 > max {
				b.WriteString("...")
				return b.String(), length + 3
			}
			length += 2
		} else {
			if length+1 > max {
				b.WriteString("...")
				return b.String(), length + 3
			}
			length++
		}
		b.WriteRune(r)
	}
	b.WriteString("...")
	return b.String(), length + 3
}

func convertSizeToString(size float64) string {
	unit := "B"
	for {
		if size < 1024 {
			break
		}
		size = size / 1024
		unit = "KB"

		if size < 1024 {
			break
		}
		size = size / 1024
		unit = "MB"

		if size < 1024 {
			break
		}
		size = size / 1024
		unit = "GB"

		if size < 1024 {
			break
		}
		size = size / 1024
		unit = "TB"
		break // nolint:all
	}

	if size >= 100 {
		return fmt.Sprintf("%.0f %s", size, unit)
	} else if size >= 10 {
		return fmt.Sprintf("%.1f %s", size, unit)
	} else {
		return fmt.Sprintf("%.2f %s", size, unit)
	}
}

func convertTimeToString(seconds float64) string {
	var b strings.Builder
	if seconds >= 3600 {
		hour := math.Floor(seconds / 3600)
		b.WriteString(fmt.Sprintf("%.0f:", hour))
		seconds -= (hour * 3600)
	}

	minute := math.Floor(seconds / 60)
	if minute >= 10 {
		b.WriteString(fmt.Sprintf("%.0f:", minute))
	} else {
		b.WriteString(fmt.Sprintf("0%.0f:", minute))
	}

	second := seconds - (minute * 60)
	if second >= 10 {
		b.WriteString(fmt.Sprintf("%.0f", second))
	} else {
		b.WriteString(fmt.Sprintf("0%.0f", second))
	}

	return b.String()
}

const kSpeedArraySize = 30

type textProgressBar struct {
	writer          io.Writer
	columns         atomic.Int32
	tmuxPaneColumns atomic.Int32
	fileCount       int
	fileIdx         int
	fileName        string
	preSize         int64
	fileSize        int64
	fileStep        int64
	startTime       *time.Time
	lastUpdateTime  *time.Time
	firstWrite      bool
	speedCnt        int
	speedIdx        int
	timeArray       [kSpeedArraySize]*time.Time
	stepArray       [kSpeedArraySize]int64
	pausing         atomic.Bool
}

func newTextProgressBar(writer io.Writer, columns int32, tmuxPaneColumns int32) *textProgressBar {
	if tmuxPaneColumns > 1 {
		columns = tmuxPaneColumns - 1 //  -1 to avoid messing up the tmux pane
	}
	progress := &textProgressBar{writer: writer, firstWrite: true}
	progress.columns.Store(columns)
	progress.tmuxPaneColumns.Store(tmuxPaneColumns)
	return progress
}

func (p *textProgressBar) setTerminalColumns(columns int32) {
	if p == nil {
		return
	}
	p.columns.Store(columns)
	// resizing tmux panes is not supported
	if p.tmuxPaneColumns.Load() > 0 {
		p.tmuxPaneColumns.Store(0)
	}
}

func (p *textProgressBar) onNum(num int64) {
	if p == nil {
		return
	}
	p.fileCount = int(num)
}

func (p *textProgressBar) onName(name string) {
	if p == nil {
		return
	}
	p.fileName = name
	p.fileIdx++
	now := timeNowFunc()
	p.startTime = &now
	p.timeArray[0] = p.startTime
	p.stepArray[0] = 0
	p.speedCnt = 1
	p.speedIdx = 1
	p.preSize = 0
	p.fileStep = -1
}

func (p *textProgressBar) onSize(size int64) {
	if p == nil {
		return
	}
	p.fileSize = p.preSize + size
}

func (p *textProgressBar) onStep(step int64) {
	if p == nil {
		return
	}
	step += p.preSize
	if step <= p.fileStep {
		return
	}
	p.fileStep = step
	if !p.pausing.Load() {
		p.showProgress()
	}
}

func (p *textProgressBar) onDone() {
	if p == nil {
		return
	}
	if !p.firstWrite {
		if p.tmuxPaneColumns.Load() > 0 {
			_ = writeAll(p.writer, []byte(fmt.Sprintf("\x1b[%dD", p.columns.Load())))
		} else {
			_ = writeAll(p.writer, []byte("\r"))
		}
		p.firstWrite = true
	}
}

func (p *textProgressBar) setPreSize(size int64) {
	if p == nil {
		return
	}
	p.preSize = size
}

func (p *textProgressBar) setPause(pausing bool) {
	if p == nil {
		return
	}
	p.pausing.Store(pausing)
}

func (p *textProgressBar) showProgress() {
	now := timeNowFunc()
	if p.lastUpdateTime != nil && now.Sub(*p.lastUpdateTime) < 200*time.Millisecond {
		return
	}
	p.lastUpdateTime = &now

	percentage := "100%"
	if p.fileSize != 0 {
		percentage = fmt.Sprintf("%.0f%%", math.Round(float64(p.fileStep)*100.0/float64(p.fileSize)))
	}
	total := convertSizeToString(float64(p.fileStep))
	speed := p.getSpeed(&now)
	speedStr := "--- B/s"
	etaStr := "--- ETA"
	if speed > 0 {
		speedStr = fmt.Sprintf("%s/s", convertSizeToString(speed))
		etaStr = fmt.Sprintf("%s ETA", convertTimeToString(math.Round(float64(p.fileSize-p.fileStep)/speed)))
	}
	progressText := p.getProgressText(percentage, total, speedStr, etaStr)

	if p.firstWrite {
		p.firstWrite = false
		_ = writeAll(p.writer, []byte(progressText))
		return
	}

	if p.tmuxPaneColumns.Load() > 0 {
		_ = writeAll(p.writer, []byte(fmt.Sprintf("\x1b[%dD%s", p.columns.Load(), progressText)))
	} else {
		_ = writeAll(p.writer, []byte(fmt.Sprintf("\r%s", progressText)))
	}
}

func (p *textProgressBar) getSpeed(now *time.Time) float64 {
	var speed float64
	if p.speedCnt <= kSpeedArraySize {
		p.speedCnt++
		speed = float64(p.fileStep-p.stepArray[0]) / (float64(now.Sub(*p.timeArray[0])) / float64(time.Second))
	} else {
		speed = float64(p.fileStep-p.stepArray[p.speedIdx]) / (float64(now.Sub(*p.timeArray[p.speedIdx])) / float64(time.Second))
	}

	p.timeArray[p.speedIdx] = now
	p.stepArray[p.speedIdx] = p.fileStep

	p.speedIdx++
	if p.speedIdx >= kSpeedArraySize {
		p.speedIdx %= kSpeedArraySize
	}

	if math.IsNaN(speed) {
		return -1
	}
	return speed
}

func (p *textProgressBar) getProgressText(percentage, total, speed, eta string) string {
	const barMinLength = 24

	left := p.fileName
	if p.fileCount > 1 {
		left = fmt.Sprintf("(%d/%d) %s", p.fileIdx, p.fileCount, p.fileName)
	}
	leftLength := getDisplayLength(left)
	right := fmt.Sprintf(" %s | %s | %s | %s", percentage, total, speed, eta)

	for {
		if int(p.columns.Load())-leftLength-len(right) >= barMinLength {
			break
		}
		if leftLength > 50 {
			left, leftLength = getEllipsisString(left, 50)
		}

		if int(p.columns.Load())-leftLength-len(right) >= barMinLength {
			break
		}
		if leftLength > 40 {
			left, leftLength = getEllipsisString(left, 40)
		}

		if int(p.columns.Load())-leftLength-len(right) >= barMinLength {
			break
		}
		right = fmt.Sprintf(" %s | %s | %s", percentage, speed, eta)

		if int(p.columns.Load())-leftLength-len(right) >= barMinLength {
			break
		}
		if leftLength > 30 {
			left, leftLength = getEllipsisString(left, 30)
		}

		if int(p.columns.Load())-leftLength-len(right) >= barMinLength {
			break
		}
		right = fmt.Sprintf(" %s | %s", percentage, eta)

		if int(p.columns.Load())-leftLength-len(right) >= barMinLength {
			break
		}
		right = fmt.Sprintf(" %s", percentage)

		if int(p.columns.Load())-leftLength-len(right) >= barMinLength {
			break
		}
		if leftLength > 20 {
			left, leftLength = getEllipsisString(left, 20)
		}

		if int(p.columns.Load())-leftLength-len(right) >= barMinLength {
			break
		}
		left = ""
		leftLength = 0
		break // nolint:all
	}

	barLength := int(p.columns.Load()) - len(right)
	if leftLength > 0 {
		barLength -= (leftLength + 1)
		left += " "
	}

	return strings.TrimSpace(left + p.getProgressBar(barLength) + right)
}

func (p *textProgressBar) getProgressBar(length int) string {
	if length < 12 {
		return ""
	}
	total := length - 2
	complete := total
	if p.fileSize != 0 {
		complete = int(math.Round((float64(total) * float64(p.fileStep)) / float64(p.fileSize)))
	}
	return "[\u001b[36m" + strings.Repeat("\u2588", complete) + strings.Repeat("\u2591", total-complete) + "\u001b[0m]"
}
