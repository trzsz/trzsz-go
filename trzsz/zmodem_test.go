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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectZmodem(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	upload := true
	download := false
	assertDetectZmodem := func(buf string, expect *bool) {
		t.Helper()
		zmodem := detectZmodem([]byte(buf))
		if expect == nil {
			assert.Nil(zmodem)
		} else {
			require.NotNil(zmodem)
			assert.Equal(*expect, zmodem.upload)
		}
	}

	assertDetectZmodem("**\x18B0100000023be50", &upload)
	assertDetectZmodem("**\x18B0100000063f694", &upload)
	assertDetectZmodem("**\x18B00000000000000", &download)
	assertDetectZmodem("rz\x0d**\x18B00000000000000", &download)

	assertDetectZmodem("**\x18B0100000023be50\x0d\x8a\x11", &upload)
	assertDetectZmodem("**\x18B0100000063f694\x0d\x8a\x11", &upload)
	assertDetectZmodem("**\x18B00000000000000\x0d\x8a\x11", &download)
	assertDetectZmodem("rz\x0d**\x18B00000000000000\x0d\x8a\x11", &download)

	assertDetectZmodem("**\x19B0100000023be50\x0d\x8a\x11", nil)
	assertDetectZmodem("**\x18B0100000023BE50\x0d\x8a\x11", nil)
	assertDetectZmodem(" *\x18B0100000023be50\x0d\x8a\x11", nil)
	assertDetectZmodem("**\x18B0100000023be5", nil)

	assertDetectZmodem("**\x18B0100000023be50"+string(zmodemCanNotOpenFile), nil)
	assertDetectZmodem("**\x18B0100000063f694\x0d\x8a\x11"+string(zmodemCanNotOpenFile), nil)
	assertDetectZmodem("**\x18B0100000023be50"+string(zmodemCancelFullSequence), nil)
	assertDetectZmodem("**\x18B0100000063f694\x0d\x8a\x11"+string(zmodemCancelFullSequence), nil)
}
