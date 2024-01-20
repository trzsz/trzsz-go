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

func TestTrzszVersion(t *testing.T) {
	assert := assert.New(t)
	assertTrzszVersion := func(vstr string, ver *trzszVersion) {
		t.Helper()
		v, err := parseTrzszVersion(vstr)
		if ver != nil {
			assert.Nil(err)
			assert.Equal(ver, v)
			assert.Equal(0, v.compare(ver))
			assert.Equal(0, ver.compare(v))
		} else {
			assert.NotNil(err)
		}
	}

	assertTrzszVersion("0.0.0", &trzszVersion{})
	assertTrzszVersion("1.0.0", &trzszVersion{1})
	assertTrzszVersion("1.2.0", &trzszVersion{1, 2})
	assertTrzszVersion("1.2.3", &trzszVersion{1, 2, 3})
	assertTrzszVersion("3.0.0", &trzszVersion{3, 0, 0})

	assertTrzszVersion("1", nil)
	assertTrzszVersion("1.", nil)
	assertTrzszVersion("1.0", nil)
	assertTrzszVersion("1.0.", nil)
	assertTrzszVersion("1.0.a", nil)
	assertTrzszVersion("1.-1.0", nil)
	assertTrzszVersion("0.0.4294967296", nil)

	assert.Greater((&trzszVersion{2, 1, 1}).compare(&trzszVersion{1, 1, 2}), 0)
	assert.Equal((&trzszVersion{3, 2, 1}).compare(&trzszVersion{3, 2, 1}), 0)
	assert.Less((&trzszVersion{1, 1, 1}).compare(&trzszVersion{1, 2}), 0)

	assert.True((&trzszVersion{1, 1, 3}).compare(&trzszVersion{1, 1, 3}) <= 0)
	assert.True((&trzszVersion{1, 1, 2}).compare(&trzszVersion{1, 1, 3}) <= 0)
	assert.True((&trzszVersion{1, 1, 1}).compare(&trzszVersion{1, 1, 3}) <= 0)
	assert.True((&trzszVersion{1, 1, 0}).compare(&trzszVersion{1, 1, 3}) <= 0)

	assert.True((&trzszVersion{1, 1, 0}).compare(&trzszVersion{1, 1, 0}) >= 0)
	assert.True((&trzszVersion{1, 1, 1}).compare(&trzszVersion{1, 1, 0}) >= 0)
	assert.True((&trzszVersion{1, 1, 2}).compare(&trzszVersion{1, 1, 0}) >= 0)
	assert.True((&trzszVersion{1, 1, 3}).compare(&trzszVersion{1, 1, 0}) >= 0)

	assert.False((&trzszVersion{1, 1, 4}).compare(&trzszVersion{1, 1, 3}) <= 0)
	assert.False((&trzszVersion{1, 1, 10}).compare(&trzszVersion{1, 1, 3}) <= 0)
	assert.False((&trzszVersion{1, 2, 0}).compare(&trzszVersion{1, 1, 3}) <= 0)
	assert.False((&trzszVersion{2, 0, 0}).compare(&trzszVersion{1, 1, 3}) <= 0)
	assert.False((&trzszVersion{0, 2, 100}).compare(&trzszVersion{1, 1, 0}) >= 0)
}

func TestTrzszDetector(t *testing.T) {
	assert := assert.New(t)
	detector := newTrzszDetector(false, false)
	assertDetectTrzsz := func(output string, tunnel bool, trigger *trzszTrigger) {
		t.Helper()
		buf, t := detector.detectTrzsz([]byte(output), tunnel)
		assert.Equal(trigger, t)
		if trigger == nil {
			assert.Equal([]byte(output), buf)
		} else {
			assert.Equal(bytes.ReplaceAll([]byte(output), []byte("TRZSZ"), []byte("TRZSZGO")), buf)
		}
	}

	assertDetectTrzsz("", false, nil)
	assertDetectTrzsz("ABC", false, nil)
	assertDetectTrzsz(strings.Repeat("A::", 10), false, nil)
	assertDetectTrzsz("::TRZSZ:TRANSFER:R:", false, nil)

	// normal trzsz trigger
	newTrigger100 := func(mode byte, uid string, win bool, port int) *trzszTrigger {
		return &trzszTrigger{mode, &trzszVersion{1, 0, 0}, uid, win, port, ""}
	}
	assertDetectTrzsz("::TRZSZ:TRANSFER:"+"R:1.0.0:0", false,
		newTrigger100('R', "0", false, 0))
	assertDetectTrzsz("ABC::TRZSZ:TRANSFER:"+"D:1.0.0:123", false,
		newTrigger100('D', "123", false, 0))
	assertDetectTrzsz("\x1b7\x07::TRZSZ:TRANSFER:"+"S:1.0.0:1", false,
		newTrigger100('S', "1", true, 0))
	assertDetectTrzsz("\x1b7\x07::TRZSZ:TRANSFER:"+"S:1.0.0:1:1234", false,
		newTrigger100('S', "1", true, 1234))
	assertDetectTrzsz("XYX\x1b7\x07::TRZSZ:TRANSFER:"+"S:1.0.0:1:7890", false,
		newTrigger100('S', "1", true, 7890))
	assertDetectTrzsz("\x1b7\x07::TRZSZ:TRANSFER:"+"S:1.0.0:1:1337:1234", false,
		newTrigger100('S', "1", true, 1337))
	assertDetectTrzsz("XYX\x1b7\x07::TRZSZ:TRANSFER:"+"S:1.0.0:1:1337:7890", false,
		newTrigger100('S', "1", true, 1337))
	assertDetectTrzsz("\x1b7\x07::TRZSZ:TRANSFER:"+"S:1.0.0:1:1337:1234ABC\n", false,
		newTrigger100('S', "1", true, 1337))
	assertDetectTrzsz("XYX\x1b7\x07::TRZSZ:TRANSFER:"+"S:1.0.0:1:1337:7890EFG\r\n", false,
		newTrigger100('S', "1", true, 1337))

	// repeated trigger
	uniqueID := time.Now().UnixMilli() % 10e10
	newTrigger110 := func(mode byte, uid int64, win bool, port int) *trzszTrigger {
		return &trzszTrigger{mode, &trzszVersion{1, 1, 0}, fmt.Sprintf("%013d", uid), win, port, ""}
	}
	assertDetectTrzsz(fmt.Sprintf("::TRZSZ:TRANSFER:R:1.1.0:%013d", uniqueID*100+10), false,
		newTrigger110('R', uniqueID*100+10, true, 0))
	for i := 0; i <= 100; i++ {
		assertDetectTrzsz(fmt.Sprintf("::TRZSZ:TRANSFER:R:1.1.0:%013d", i*100+10), false,
			newTrigger110('R', int64(i*100+10), true, 0))
		assertDetectTrzsz(fmt.Sprintf("::TRZSZ:TRANSFER:R:1.1.0:%013d", i*100+10), false, nil)
		if i > 0 {
			assertDetectTrzsz(fmt.Sprintf("::TRZSZ:TRANSFER:R:1.1.0:%013d", (i-1)*100+10), false, nil)
		}
	}
	for i := 0; i < 49; i++ {
		assertDetectTrzsz(fmt.Sprintf("::TRZSZ:TRANSFER:R:1.1.0:%013d", i*100+10), false,
			newTrigger110('R', int64(i*100+10), true, 0))
		assertDetectTrzsz(fmt.Sprintf("::TRZSZ:TRANSFER:R:1.1.0:%013d", i*100+10), false, nil)
		if i > 0 {
			assertDetectTrzsz(fmt.Sprintf("::TRZSZ:TRANSFER:R:1.1.0:%013d", (i-1)*100+10), false, nil)
		}
	}

	// tmux control mode
	newTriggerTMUX := func(prefix string, port int) *trzszTrigger {
		return &trzszTrigger{'R', &trzszVersion{1, 0, 0}, "0", false, port, prefix}
	}
	assertDetectTrzsz("%output %1 \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", false, nil)
	assertDetectTrzsz("%output %23 \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", false, nil)
	assertDetectTrzsz("%extended-output %0 0 : \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", false, nil)
	assertDetectTrzsz("%extended-output %10 0 : \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", false, nil)

	assertDetectTrzsz("%output %1 \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0:1337ABC", false, nil)
	assertDetectTrzsz("%extended-output %0 0 : \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0:1337ABC", false, nil)
	assertDetectTrzsz("%output %1 \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0:1337ABC", true,
		newTriggerTMUX("%output %1 ", 1337))
	assertDetectTrzsz("%extended-output %0 0 : \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0:1337ABC", true,
		newTriggerTMUX("%extended-output %0 0 : ", 1337))

	tmuxTrigger := newTriggerTMUX("", 0)
	assertDetectTrzsz("%output %x \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", false, tmuxTrigger)
	assertDetectTrzsz("%output 1 \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", false, tmuxTrigger)
	assertDetectTrzsz("%output % \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", false, tmuxTrigger)
	assertDetectTrzsz("output %1 \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", false, tmuxTrigger)

	assertDetectTrzsz("%extended-output %a 0 : \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", false, tmuxTrigger)
	assertDetectTrzsz("%extended-output %0 b : \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", false, tmuxTrigger)
	assertDetectTrzsz("extended-output %0 0 : \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", false, tmuxTrigger)
	assertDetectTrzsz("%extended-output 0 0 : \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", false, tmuxTrigger)
	assertDetectTrzsz("%extended-output % 0 : \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", false, tmuxTrigger)
	assertDetectTrzsz("%extended-output %0 0 \x1b7\x07::TRZSZ:TRANSFER:"+"R:1.0.0:0ABC", false, tmuxTrigger)
}

func TestRelayDetector(t *testing.T) {
	assert := assert.New(t)
	detector := newTrzszDetector(true, true)
	prefix := "\x1b7\x07::TRZSZ:TRANSFER:R:1.0.0"
	assertRewriteEqual := func(output, expected string, trigger *trzszTrigger) {
		t.Helper()
		detector.uniqueIDMap = make(map[string]int) // ignore unique check
		buf, t := detector.detectTrzsz([]byte(prefix+output), false)
		assert.Equal([]byte(prefix+expected), buf)
		assert.Equal(t, trigger)
	}
	newTrigger := func(uid string, win bool, port int) *trzszTrigger {
		return &trzszTrigger{'R', &trzszVersion{1, 0, 0}, uid, win, port, ""}
	}

	assertRewriteEqual(":0", ":0#R", newTrigger("0", false, 0))
	assertRewriteEqual(":1", ":1#R", newTrigger("1", true, 0))
	assertRewriteEqual(":0\n", ":0#R\n", newTrigger("0", false, 0))
	assertRewriteEqual(":1\r\n", ":1#R\r\n", newTrigger("1", true, 0))

	assertRewriteEqual(":0:1234", ":0:1234#R", newTrigger("0", false, 1234))
	assertRewriteEqual(":1:1234", ":1:1234#R", newTrigger("1", true, 1234))
	assertRewriteEqual(":0:1337\n", ":0:1337#R\n", newTrigger("0", false, 1337))
	assertRewriteEqual(":1:1337\r\n", ":1:1337#R\r\n", newTrigger("1", true, 1337))

	assertRewriteEqual(":1234567890110", ":1234567890110#R",
		newTrigger("1234567890110", true, 0))
	assertRewriteEqual(":9876543210210", ":9876543210210#R",
		newTrigger("9876543210210", true, 0))
	assertRewriteEqual(":1234567890110\n", ":1234567890110#R\n",
		newTrigger("1234567890110", true, 0))
	assertRewriteEqual(":9876543210210\r\n", ":9876543210210#R\r\n",
		newTrigger("9876543210210", true, 0))
	assertRewriteEqual(":1234567890110#R\n", ":1234567890110#R#R\n",
		newTrigger("1234567890110", true, 0))
	assertRewriteEqual(":9876543210210#R\r\n", ":9876543210210#R#R\r\n",
		newTrigger("9876543210210", true, 0))

	assertRewriteEqual(":1234567890110:12345", ":1234567890110:12345#R",
		newTrigger("1234567890110", true, 12345))
	assertRewriteEqual(":9876543210210:12345", ":9876543210210:12345#R",
		newTrigger("9876543210210", true, 12345))
	assertRewriteEqual(":1234567890110:12345\n", ":1234567890110:12345#R\n",
		newTrigger("1234567890110", true, 12345))
	assertRewriteEqual(":9876543210210:12345\r\n", ":9876543210210:12345#R\r\n",
		newTrigger("9876543210210", true, 12345))
	assertRewriteEqual(":1234567890110:12345#R\n", ":1234567890110:12345#R#R\n",
		newTrigger("1234567890110", true, 12345))
	assertRewriteEqual(":9876543210210:12345#R\r\n", ":9876543210210:12345#R#R\r\n",
		newTrigger("9876543210210", true, 12345))

	assertRewriteEqual(":123456789\n0100", ":123456789#R\n0100",
		newTrigger("123456789", false, 0))
	assertRewriteEqual(":123456789\r\n0200", ":123456789#R\r\n0200",
		newTrigger("123456789", false, 0))
	assertRewriteEqual(":123456789\n0100\n", ":123456789#R\n0100\n",
		newTrigger("123456789", false, 0))
	assertRewriteEqual(":123456789\r\n0200\r\n", ":123456789#R\r\n0200\r\n",
		newTrigger("123456789", false, 0))

	assertRewriteEqual(":123456789:1223\n0100", ":123456789:1223#R\n0100",
		newTrigger("123456789", false, 1223))
	assertRewriteEqual(":123456789:1223\r\n0200", ":123456789:1223#R\r\n0200",
		newTrigger("123456789", false, 1223))
	assertRewriteEqual(":123456789:1223\n0100\n", ":123456789:1223#R\n0100\n",
		newTrigger("123456789", false, 1223))
	assertRewriteEqual(":123456789:1223\r\n0200\r\n", ":123456789:1223#R\r\n0200\r\n",
		newTrigger("123456789", false, 1223))

	assertRewriteEqual(":1234567890100", ":1234567890120#R",
		newTrigger("1234567890120", false, 0))
	assertRewriteEqual(":9876543210200", ":9876543210220#R",
		newTrigger("9876543210220", false, 0))
	assertRewriteEqual(":1234567890100\n", ":1234567890120#R\n",
		newTrigger("1234567890120", false, 0))
	assertRewriteEqual(":9876543210200\r\n", ":9876543210220#R\r\n",
		newTrigger("9876543210220", false, 0))
	assertRewriteEqual(":1234567890100#R\n", ":1234567890120#R#R\n",
		newTrigger("1234567890120", false, 0))
	assertRewriteEqual(":9876543210200#R\r\n", ":9876543210220#R#R\r\n",
		newTrigger("9876543210220", false, 0))

	assertRewriteEqual(":1234567890100:333", ":1234567890120:333#R",
		newTrigger("1234567890120", false, 333))
	assertRewriteEqual(":9876543210200:333", ":9876543210220:333#R",
		newTrigger("9876543210220", false, 333))
	assertRewriteEqual(":1234567890100:333\n", ":1234567890120:333#R\n",
		newTrigger("1234567890120", false, 333))
	assertRewriteEqual(":9876543210200:333\r\n", ":9876543210220:333#R\r\n",
		newTrigger("9876543210220", false, 333))
	assertRewriteEqual(":1234567890100:333#R\n", ":1234567890120:333#R#R\n",
		newTrigger("1234567890120", false, 333))
	assertRewriteEqual(":9876543210200:333#R\r\n", ":9876543210220:333#R#R\r\n",
		newTrigger("9876543210220", false, 333))

	assertRewriteEqual(":1234567890100\n"+prefix+":9876543210200\r\n",
		":1234567890120\n"+prefix+":9876543210220#R\r\n", newTrigger("9876543210220", false, 0))
	assertRewriteEqual(":1234567890100\n"+prefix+":9876543210200:8\r\n",
		":1234567890120\n"+prefix+":9876543210220:8#R\r\n", newTrigger("9876543210220", false, 8))

	assertRewriteEqual(":1234567890100\n"+prefix+":9876543210200\r\n::TRZSZ:TRANSFER:R:",
		":1234567890120\n"+prefix+":9876543210220\r\n::TRZSZ:TRANSFER:R:", nil)
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
			got := formatSavedFiles(tt.args.files, tt.args.dstPath)
			assert.Equal(tt.want, got)
		})
	}
}
