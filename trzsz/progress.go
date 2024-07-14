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
	"fmt"
	"io"
	"math"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/lucasb-eyer/go-colorful"
	"github.com/mattn/go-runewidth"
)

func getEllipsisString(str string, max int) (string, int) {
	var b strings.Builder
	b.Grow(max)
	max -= 3
	length := 0
	for _, r := range []rune(str) { // nolint:all
		rlen := runewidth.RuneWidth(r)
		if length+rlen > max {
			b.WriteString("...")
			return b.String(), length + 3
		}
		length += rlen
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

type recentSpeed struct {
	speedCnt  int
	speedIdx  int
	timeArray [kSpeedArraySize]*time.Time
	stepArray [kSpeedArraySize]int64
}

func (s *recentSpeed) initFirstStep(now *time.Time) {
	s.timeArray[0] = now
	s.stepArray[0] = 0
	s.speedCnt = 1
	s.speedIdx = 1
}

func (s *recentSpeed) getSpeed(step int64, now *time.Time) float64 {
	var speed float64
	if s.speedCnt <= kSpeedArraySize {
		s.speedCnt++
		speed = float64(step-s.stepArray[0]) / (float64(now.Sub(*s.timeArray[0])) / float64(time.Second))
	} else {
		speed = float64(step-s.stepArray[s.speedIdx]) / (float64(now.Sub(*s.timeArray[s.speedIdx])) / float64(time.Second))
	}

	s.timeArray[s.speedIdx] = now
	s.stepArray[s.speedIdx] = step

	s.speedIdx++
	if s.speedIdx >= kSpeedArraySize {
		s.speedIdx = 0
	}

	if math.IsNaN(speed) {
		return -1
	}
	return speed
}

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
	recentSpeed     recentSpeed
	pausing         atomic.Bool
	tmuxPrefix      string
	colorA          *colorful.Color
	colorB          *colorful.Color
}

func newTextProgressBar(writer io.Writer, columns int32, tmuxPaneColumns int32,
	tmuxPrefix, colorPair string) *textProgressBar {
	if tmuxPaneColumns > 1 {
		columns = tmuxPaneColumns - 1 //  -1 to avoid messing up the tmux pane
	}
	progress := &textProgressBar{writer: writer, firstWrite: true, tmuxPrefix: tmuxPrefix}
	progress.columns.Store(columns)
	progress.tmuxPaneColumns.Store(tmuxPaneColumns)
	colors := strings.Fields(colorPair)
	if len(colors) == 2 {
		if colorA, err := colorful.Hex("#" + colors[0]); err == nil {
			progress.colorA = &colorA
		}
		if colorB, err := colorful.Hex("#" + colors[1]); err == nil {
			progress.colorB = &colorB
		}
	}
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
	p.hideCursor()
}

func (p *textProgressBar) onName(name string) {
	if p == nil {
		return
	}
	p.fileName = name
	p.fileIdx++
	now := timeNowFunc()
	p.startTime = &now
	p.recentSpeed.initFirstStep(&now)
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
	if p.fileSize == 0 {
		return
	}
	p.fileStep = p.fileSize
	p.lastUpdateTime = nil
	p.showProgress()
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
	if !pausing {
		p.hideCursor()
	}
	p.pausing.Store(pausing)
}

func (p *textProgressBar) hideCursor() {
	p.writeProgress("\x1b[?25l")
}

func (p *textProgressBar) showCursor() {
	p.writeProgress("\x1b[?25h")
}

func (p *textProgressBar) writeProgress(progress string) {
	data := []byte(progress)
	if p.tmuxPrefix != "" {
		data = encodeTmuxOutput(p.tmuxPrefix, data)
	}
	_ = writeAll(p.writer, data)
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
	speed := p.recentSpeed.getSpeed(p.fileStep, &now)
	speedStr := "--- B/s"
	etaStr := "--- ETA"
	if speed > 0 {
		speedStr = fmt.Sprintf("%s/s", convertSizeToString(speed))
		etaStr = fmt.Sprintf("%s ETA", convertTimeToString(math.Round(float64(p.fileSize-p.fileStep)/speed)))
	}
	progressText := p.getProgressText(percentage, total, speedStr, etaStr)

	if p.firstWrite {
		p.firstWrite = false
		p.writeProgress(progressText)
		return
	}

	if p.tmuxPaneColumns.Load() > 0 {
		p.writeProgress(fmt.Sprintf("\x1b[%dD%s", p.columns.Load(), progressText))
	} else {
		p.writeProgress(fmt.Sprintf("\r%s", progressText))
	}
}

func (p *textProgressBar) getProgressText(percentage, total, speed, eta string) string {
	const barMinLength = 24

	left := p.fileName
	if p.fileCount > 1 {
		left = fmt.Sprintf("(%d/%d) %s", p.fileIdx, p.fileCount, p.fileName)
	}
	leftLength := runewidth.StringWidth(left)
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
	totalSize := length - 2
	fullSize := totalSize
	if p.fileSize != 0 {
		fullSize = int(math.Round((float64(totalSize) * float64(p.fileStep)) / float64(p.fileSize)))
	}
	emptySize := totalSize - fullSize
	if p.colorA == nil || p.colorB == nil {
		return fmt.Sprintf("[\x1b[36m%s%s\x1b[0m]",
			strings.Repeat("\u2588", fullSize), strings.Repeat("\u2591", emptySize))
	}
	var buf strings.Builder
	buf.WriteString("[")
	for i := 0; i < fullSize; i++ {
		color := p.colorA.BlendLuv(*p.colorB, float64(i)/float64(totalSize))
		render := lipgloss.NewStyle().Foreground(lipgloss.Color(color.Hex()))
		buf.WriteString(render.Render("\u2588"))
	}
	buf.WriteString(strings.Repeat("\u2591", emptySize))
	buf.WriteString("]")
	return buf.String()
}
