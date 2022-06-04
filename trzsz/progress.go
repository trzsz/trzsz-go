/*
MIT License

Copyright (c) 2022 Lonny Wong

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
	"math"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

func getDisplayLength(str string) int {
	length := 0
	for _, r := range []rune(str) {
		if utf8.RuneLen(r) == 1 {
			length += 1
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
	for _, r := range []rune(str) {
		if utf8.RuneLen(r) > 1 {
			if length+2 > max {
				b.WriteString("...")
				return b.String(), length + 3
			} else {
				length += 2
			}
		} else {
			if length+1 > max {
				b.WriteString("...")
				return b.String(), length + 3
			} else {
				length += 1
			}
		}
		b.WriteRune(r)
	}
	b.WriteString("...")
	return b.String(), length + 3
}

func convertSizeToString(size float64) string {
	if math.IsNaN(size) {
		return "NaN"
	}

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
		break
	}

	if size >= 100 {
		return fmt.Sprintf("%.0f%s", size, unit)
	} else if size >= 10 {
		return fmt.Sprintf("%.1f%s", size, unit)
	} else {
		return fmt.Sprintf("%.2f%s", size, unit)
	}
}

func convertTimeToString(seconds float64) string {
	if math.IsNaN(seconds) {
		return "NaN"
	}

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

type TextProgressBar struct {
	writer          *os.File
	columns         int
	tmuxPaneColumns int
	fileCount       int
	fileIdx         int
	fileName        string
	fileSize        int64
	fileStep        int64
	startTime       *time.Time
	lastUpdateTime  *time.Time
	firstWrite      bool
}

func NewTextProgressBar(writer *os.File, columns int, tmuxPaneColumns int) *TextProgressBar {
	if tmuxPaneColumns > 1 {
		columns = tmuxPaneColumns - 1 //  -1 to avoid messing up the tmux pane
	}
	return &TextProgressBar{writer, columns, tmuxPaneColumns, 0, 0, "", 0, 0, nil, nil, true}
}

func (p *TextProgressBar) setTerminalColumns(columns int) {
	p.columns = columns
	// resizing tmux panes is not supported
	if p.tmuxPaneColumns > 0 {
		p.tmuxPaneColumns = -1
	}
}

func (p *TextProgressBar) onNum(num int64) {
	p.fileCount = int(num)
}

func (p *TextProgressBar) onName(name string) {
	p.fileName = name
	p.fileIdx += 1
	now := time.Now()
	p.startTime = &now
}

func (p *TextProgressBar) onSize(size int64) {
	p.fileSize = size
}

func (p *TextProgressBar) onStep(step int64) {
	p.fileStep = step
	p.showProgress()
}

func (p *TextProgressBar) onDone(name string) {
}

func (p *TextProgressBar) showProgress() {
	now := time.Now()
	if p.lastUpdateTime != nil && now.Sub(*p.lastUpdateTime) < 500*time.Millisecond {
		return
	}
	p.lastUpdateTime = &now

	if p.fileSize == 0 {
		return
	}
	percentage := fmt.Sprintf("%.0f%%", float64(p.fileStep)*100.0/float64(p.fileSize))
	total := convertSizeToString(float64(p.fileStep))
	usedTime := float64(now.Sub(*p.startTime)) / float64(time.Second)
	speed := fmt.Sprintf("%s/s", convertSizeToString(float64(p.fileStep)/usedTime))
	leftTime := float64(p.fileSize-p.fileStep) * usedTime / float64(p.fileStep)
	eta := fmt.Sprintf("%s ETA", convertTimeToString(leftTime))
	progressText := p.getProgressText(percentage, total, speed, eta)

	if p.firstWrite {
		p.firstWrite = false
		p.writer.Write([]byte(progressText))
		return
	}

	if p.tmuxPaneColumns > 0 {
		p.writer.Write([]byte(fmt.Sprintf("\x1b[%dD%s", p.columns, progressText)))
	} else {
		p.writer.Write([]byte(fmt.Sprintf("\r%s", progressText)))
	}
}

func (p *TextProgressBar) getProgressText(percentage, total, speed, eta string) string {
	const barMinLength = 24

	left := p.fileName
	if p.fileCount > 1 {
		left = fmt.Sprintf("(%d/%d) %s", p.fileIdx, p.fileCount, p.fileName)
	}
	leftLength := getDisplayLength(left)
	right := fmt.Sprintf(" %s | %s | %s | %s", percentage, total, speed, eta)

	for {
		if p.columns-leftLength-len(right) >= barMinLength {
			break
		}
		if leftLength > 50 {
			left, leftLength = getEllipsisString(left, 50)
		}

		if p.columns-leftLength-len(right) >= barMinLength {
			break
		}
		if leftLength > 40 {
			left, leftLength = getEllipsisString(left, 40)
		}

		if p.columns-leftLength-len(right) >= barMinLength {
			break
		}
		right = fmt.Sprintf(" %s | %s | %s", percentage, speed, eta)

		if p.columns-leftLength-len(right) >= barMinLength {
			break
		}
		if leftLength > 30 {
			left, leftLength = getEllipsisString(left, 30)
		}

		if p.columns-leftLength-len(right) >= barMinLength {
			break
		}
		right = fmt.Sprintf(" %s | %s", percentage, eta)

		if p.columns-leftLength-len(right) >= barMinLength {
			break
		}
		right = fmt.Sprintf(" %s", percentage)

		if p.columns-leftLength-len(right) >= barMinLength {
			break
		}
		if leftLength > 20 {
			left, leftLength = getEllipsisString(left, 20)
		}

		if p.columns-leftLength-len(right) >= barMinLength {
			break
		}
		left = ""
		leftLength = 0
		break
	}

	barLength := p.columns - len(right)
	if leftLength > 0 {
		barLength -= (leftLength + 1)
		left += " "
	}

	return strings.TrimSpace(left + p.getProgressBar(barLength) + right)
}

func (p *TextProgressBar) getProgressBar(length int) string {
	if length < 12 {
		return ""
	}
	total := length - 2
	complete := int(math.Round((float64(total) * float64(p.fileStep)) / float64(p.fileSize)))
	return "[\u001b[36m" + strings.Repeat("\u2588", complete) + strings.Repeat("\u2591", total-complete) + "\u001b[0m]"
}
