/*
MIT License

Copyright (c) 2023 [Trzsz](https://github.com/trzsz)

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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mockTimeNow(times []int64, defaultTime int64) *int {
	idx := 0
	timeNowFunc = func() time.Time {
		if idx >= len(times) {
			return time.UnixMilli(defaultTime)
		}
		t := time.UnixMilli(times[idx])
		idx++
		return t
	}
	return &idx
}

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

func (w *testWriter) assertBufferEqual(idx int, expected string) {
	w.t.Helper()
	require.Less(w.t, idx, len(w.buffer))
	assert.Equal(w.t, expected, w.buffer[idx])
}

func (w *testWriter) assertLastBufferEqual(expected string) {
	w.t.Helper()
	require.Less(w.t, 0, len(w.buffer))
	w.assertBufferEqual(len(w.buffer)-1, expected)
}

func (w *testWriter) assertBase64DataEqual(expected []string) {
	w.t.Helper()
	require.Less(w.t, 0, len(expected)*3)
	for i := 0; i < len(expected); i++ {
		j := len(w.buffer) - (len(expected)-i)*3
		w.assertBufferEqual(j, "#DATA:")
		w.assertBufferEqual(j+1, expected[i])
		w.assertBufferEqual(j+2, "\n")
	}
}

func (w *testWriter) assertBinaryDataEqual(expected []string) {
	w.t.Helper()
	require.Less(w.t, 0, len(expected)*2)
	for i := 0; i < len(expected); i++ {
		j := len(w.buffer) - (len(expected)-i)*2
		w.assertBufferEqual(j, fmt.Sprintf("#DATA:%d\n", len(expected[i])))
		w.assertBufferEqual(j+1, expected[i])
	}
}

func newTestWriter(t *testing.T) *testWriter {
	return &testWriter{t, nil}
}

func TestTrzszDetector(t *testing.T) {
	assert := assert.New(t)
	detector := newTrzszDetector(false, false)
	assertDetectTrzsz := func(output string, mode *byte, win bool) {
		t.Helper()
		buf, m, w := detector.detectTrzsz([]byte(output))
		if mode == nil {
			assert.Equal([]byte(output), buf)
		} else {
			assert.Equal(bytes.ReplaceAll([]byte(output), []byte("TRZSZ"), []byte("TRZSZGO")), buf)
		}
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
	assertDetectTrzsz("\x1b7\x07::TRZSZ:TRANSFER:"+"S:1.0.0:1:1234", &S, true)
	assertDetectTrzsz("XYX\x1b7\x07::TRZSZ:TRANSFER:"+"S:1.0.0:1:7890", &S, true)
	assertDetectTrzsz("\x1b7\x07::TRZSZ:TRANSFER:"+"S:1.0.0:1:1234ABC\n", &S, true)
	assertDetectTrzsz("XYX\x1b7\x07::TRZSZ:TRANSFER:"+"S:1.0.0:1:7890EFG\r\n", &S, true)

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

func TestRelayDetector(t *testing.T) {
	assert := assert.New(t)
	detector := newTrzszDetector(true, true)
	R := byte('R')
	prefix := "\x1b7\x07::TRZSZ:TRANSFER:R:1.0.0"
	assertRewriteEqual := func(output, expected string, mode *byte, win bool) {
		t.Helper()
		detector.uniqueIDMap = make(map[string]int) // ignore unique check
		buf, m, w := detector.detectTrzsz([]byte(prefix + output))
		assert.Equal([]byte(prefix+expected), buf)
		assert.Equal(mode, m)
		assert.Equal(win, w)
	}

	assertRewriteEqual(":0", ":0#R", &R, false)
	assertRewriteEqual(":1", ":1#R", &R, true)
	assertRewriteEqual(":0\n", ":0#R\n", &R, false)
	assertRewriteEqual(":1\r\n", ":1#R\r\n", &R, true)

	assertRewriteEqual(":1234567890110", ":1234567890110#R", &R, true)
	assertRewriteEqual(":9876543210210", ":9876543210210#R", &R, true)
	assertRewriteEqual(":1234567890110\n", ":1234567890110#R\n", &R, true)
	assertRewriteEqual(":9876543210210\r\n", ":9876543210210#R\r\n", &R, true)
	assertRewriteEqual(":1234567890110#R\n", ":1234567890110#R#R\n", &R, true)
	assertRewriteEqual(":9876543210210#R\r\n", ":9876543210210#R#R\r\n", &R, true)

	assertRewriteEqual(":123456789\n0100", ":123456789#R\n0100", &R, false)
	assertRewriteEqual(":123456789\r\n0200", ":123456789#R\r\n0200", &R, false)
	assertRewriteEqual(":123456789\n0100\n", ":123456789#R\n0100\n", &R, false)
	assertRewriteEqual(":123456789\r\n0200\r\n", ":123456789#R\r\n0200\r\n", &R, false)

	assertRewriteEqual(":1234567890100", ":1234567890120#R", &R, false)
	assertRewriteEqual(":9876543210200", ":9876543210220#R", &R, false)
	assertRewriteEqual(":1234567890100\n", ":1234567890120#R\n", &R, false)
	assertRewriteEqual(":9876543210200\r\n", ":9876543210220#R\r\n", &R, false)
	assertRewriteEqual(":1234567890100#R\n", ":1234567890120#R#R\n", &R, false)
	assertRewriteEqual(":9876543210200#R\r\n", ":9876543210220#R#R\r\n", &R, false)

	assertRewriteEqual(":1234567890100\n"+prefix+":9876543210200\r\n",
		":1234567890120\n"+prefix+":9876543210220#R\r\n", &R, false)

	assertRewriteEqual(":1234567890100\n"+prefix+":9876543210200\r\n::TRZSZ:TRANSFER:R:",
		":1234567890120\n"+prefix+":9876543210220\r\n::TRZSZ:TRANSFER:R:", nil, false)
}

func TestFormatSavedFileNames(t *testing.T) {
	assert := assert.New(t)
	type args struct {
		files   []string
		dstPath string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "nodstPath",
			args: args{
				dstPath: "",
				files:   []string{"a.jpg", "b.jpg", "c.jpg"},
			},
			want: "Saved 3 files/directories\r\n" +
				"- a.jpg\r\n" +
				"- b.jpg\r\n" +
				"- c.jpg",
		},
		{
			name: "dstPath",
			args: args{
				dstPath: "/root",
				files:   []string{"a.jpg", "b.jpg", "c.jpg"},
			},
			want: "Saved 3 files/directories to /root\r\n" +
				"- a.jpg\r\n" +
				"- b.jpg\r\n" +
				"- c.jpg",
		},
		{
			name: "dstPath",
			args: args{
				dstPath: "/root",
				files:   []string{"a.jpg", "b.jpg", "c.jpg"},
			},
			want: "Saved 3 files/directories to /root\r\n" +
				"- a.jpg\r\n" +
				"- b.jpg\r\n" +
				"- c.jpg",
		},
		{
			name: "dstPath",
			args: args{
				dstPath: "/root",
				files:   []string{"a.jpg"},
			},
			want: "Saved 1 file/directory to /root\r\n" +
				"- a.jpg",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatSavedFileNames(tt.args.files, tt.args.dstPath)
			assert.Equal(tt.want, got)
		})
	}
}
