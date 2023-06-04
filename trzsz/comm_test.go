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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testWriter struct {
	t      *testing.T
	buffer []string
}

func (w *testWriter) Write(text []byte) (n int, err error) {
	w.buffer = append(w.buffer, string(text))
	return len(text), nil
}

func (w *testWriter) assertBufferCount(count int) {
	w.t.Helper()
	require.Equal(w.t, count, len(w.buffer))
}

func (w *testWriter) assertBufferEqual(idx int, expected string) { // nolint:all
	w.t.Helper()
	require.Less(w.t, idx, len(w.buffer))
	assert.Equal(w.t, expected, w.buffer[idx])
}

func newTestWriter(t *testing.T) *testWriter {
	return &testWriter{t, nil}
}

func TestTrzszDetector(t *testing.T) {
	assert := assert.New(t)
	detector := newTrzszDetector()
	assertDetectTrzsz := func(output string, mode *byte, win bool) {
		t.Helper()
		m, w := detector.detectTrzsz([]byte(output))
		assert.Equal(mode, m)
		assert.Equal(win, w)
	}

	assertDetectTrzsz("", nil, false)
	assertDetectTrzsz("ABC", nil, false)
	assertDetectTrzsz(strings.Repeat("A::", 10), nil, false)
	assertDetectTrzsz("::TRZSZ:TRANSFER:R:", nil, false)

	// normal trzsz trigger
	R := byte('R')
	D := byte('D')
	S := byte('S')
	assertDetectTrzsz("::TRZSZ:TRANSFER:"+"R:1.0.0:0", &R, false)
	assertDetectTrzsz("ABC::TRZSZ:TRANSFER:"+"D:1.0.0:123", &D, false)
	assertDetectTrzsz("\x1b7\x07::TRZSZ:TRANSFER:"+"S:1.0.0:1", &S, true)
	assertDetectTrzsz("\x1b7\x07::TRZSZ:TRANSFER:"+"S:1.0.0:1ABC", &S, true)
	assertDetectTrzsz("XYX\x1b7\x07::TRZSZ:TRANSFER:"+"S:1.0.0:1ABC", &S, true)

	// repeated trigger
	uniqueID := time.Now().UnixMilli() % 10e10
	assertDetectTrzsz(fmt.Sprintf("::TRZSZ:TRANSFER:R:1.1.0:%013d", uniqueID*100+10), &R, true)
	for i := 0; i <= 100; i++ {
		assertDetectTrzsz(fmt.Sprintf("::TRZSZ:TRANSFER:R:1.1.0:%013d", i*100+10), &R, true)
		assertDetectTrzsz(fmt.Sprintf("::TRZSZ:TRANSFER:R:1.1.0:%013d", i*100+10), nil, false)
		if i > 0 {
			assertDetectTrzsz(fmt.Sprintf("::TRZSZ:TRANSFER:R:1.1.0:%013d", (i-1)*100+10), nil, false)
		}
	}
	for i := 0; i < 49; i++ {
		assertDetectTrzsz(fmt.Sprintf("::TRZSZ:TRANSFER:R:1.1.0:%013d", i*100+10), &R, true)
		assertDetectTrzsz(fmt.Sprintf("::TRZSZ:TRANSFER:R:1.1.0:%013d", i*100+10), nil, false)
		if i > 0 {
			assertDetectTrzsz(fmt.Sprintf("::TRZSZ:TRANSFER:R:1.1.0:%013d", (i-1)*100+10), nil, false)
		}
	}

	// ignore tmux control mode
	assertDetectTrzsz("%output %1 \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", nil, false)
	assertDetectTrzsz("%output %23 \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", nil, false)
	assertDetectTrzsz("%extended-output %0 0 : \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", nil, false)
	assertDetectTrzsz("%extended-output %10 0 : \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", nil, false)

	assertDetectTrzsz("%output %x \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", &R, false)
	assertDetectTrzsz("%output 1 \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", &R, false)
	assertDetectTrzsz("%output % \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", &R, false)
	assertDetectTrzsz("output %1 \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", &R, false)

	assertDetectTrzsz("%extended-output %a 0 : \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", &R, false)
	assertDetectTrzsz("%extended-output %0 b : \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", &R, false)
	assertDetectTrzsz("extended-output %0 0 : \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", &R, false)
	assertDetectTrzsz("%extended-output 0 0 : \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", &R, false)
	assertDetectTrzsz("%extended-output % 0 : \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", &R, false)
	assertDetectTrzsz("%extended-output %0 0 \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", &R, false)
}
