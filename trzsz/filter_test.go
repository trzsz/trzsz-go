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
	"strings"
	"testing"
)

func TestDetectOSC52(t *testing.T) {
	oriWriteToClipboard := writeToClipboard
	defer func() {
		writeToClipboard = oriWriteToClipboard
	}()

	writer := newTestWriter(t)
	writeToClipboard = func(buf []byte) {
		_, _ = writer.Write(buf)
	}

	filter := &TrzszFilter{}

	filter.detectOSC52([]byte("ABC"))
	writer.assertBufferCount(0)

	filter.detectOSC52([]byte("\x1b]52;"))
	writer.assertBufferCount(0)

	filter.detectOSC52([]byte("\x1b]52;a;"))
	writer.assertBufferCount(0)

	filter.detectOSC52([]byte("\x1b]52;a;A\a"))
	writer.assertBufferCount(0)

	filter.detectOSC52([]byte("\x1b]52;a;A\a\x1b]52;c;X\aZ"))
	writer.assertBufferEqual(0, "X")

	writer.clearBuffer()
	filter.detectOSC52([]byte("\x1b]52;a;A\a\x1b]52;b;B\a\x1b]52;c;X\aZ"))
	writer.assertBufferEqual(0, "X")

	writer.clearBuffer()
	filter.detectOSC52([]byte("\x1b]52;c;ABC"))
	writer.assertBufferCount(0)
	filter.detectOSC52([]byte("xyz\a"))
	writer.assertBufferEqual(0, "ABCxyz")

	writer.clearBuffer()
	filter.detectOSC52([]byte("\x1b]52;c;ABC"))
	writer.assertBufferCount(0)
	filter.detectOSC52([]byte("\x1bxyz\a"))
	writer.assertBufferEqual(0, "ABC")

	writer.clearBuffer()
	filter.detectOSC52([]byte("11\x1b]52;c;ABC\a\x1b]52;p;xyz\x1b00"))
	writer.assertBufferEqual(0, "ABC")
	writer.assertBufferEqual(1, "xyz")

	writer.clearBuffer()
	filter.detectOSC52([]byte("\x1b]52;c;ABC"))
	writer.assertBufferCount(0)
	filter.detectOSC52([]byte("DEFG"))
	writer.assertBufferCount(0)
	filter.detectOSC52([]byte("HIJ\a111\x1b]52;p;xyz\x1b00\x1b]52;c;123"))
	writer.assertBufferCount(2)
	writer.assertBufferEqual(0, "ABCDEFGHIJ")
	writer.assertBufferEqual(1, "xyz")
	filter.detectOSC52([]byte("\a"))
	writer.assertBufferEqual(2, "123")

	writer.clearBuffer()
	filter.detectOSC52([]byte("\x1b]52;c;ABC"))
	for i := 0; i < 2000; i++ {
		filter.detectOSC52([]byte("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"))
	}
	filter.detectOSC52([]byte("==\x1b"))
	writer.assertBufferEqual(0, "ABC"+
		strings.Repeat("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/", 2000)+"==")

	writer.clearBuffer()
	filter.detectOSC52([]byte("\x1b]52;c;ABC"))
	for i := 0; i < 2000; i++ {
		filter.detectOSC52([]byte("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"))
	}
	filter.detectOSC52([]byte("==$=="))
	writer.assertBufferCount(0)
	filter.detectOSC52([]byte("\x1b]52;c;ABC\a"))
	writer.assertBufferEqual(0, "ABC")
}
