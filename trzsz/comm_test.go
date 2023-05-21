/*
MIT License

Copyright (c) 2023 Lonny Wong

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
	"testing"

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

func (w *testWriter) assertBufferEqual(idx int, expected string) {
	w.t.Helper()
	require.Less(w.t, idx, len(w.buffer))
	assert.Equal(w.t, expected, w.buffer[idx])
}

func newTestWriter(t *testing.T) *testWriter {
	return &testWriter{t, nil}
}
