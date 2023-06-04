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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRewriteTrzszTrigger(t *testing.T) {
	assert := assert.New(t)
	relay := newTrzszRelay(nil, nil, 0, nil)
	prefix := "\x1b7\x07::TRZSZ:TRANSFER:R:1.0.0"
	assertRewriteEqual := func(output, expected string) {
		t.Helper()
		result := relay.rewriteTrzszTrigger([]byte(prefix + output))
		assert.Equal([]byte(prefix+expected), result)
	}

	assertRewriteEqual(":0", ":0")
	assertRewriteEqual(":1", ":1")
	assertRewriteEqual(":0\n", ":0\n")
	assertRewriteEqual(":1\r\n", ":1\r\n")

	assertRewriteEqual(":1234567890110", ":1234567890110")
	assertRewriteEqual(":9876543210210", ":9876543210210")
	assertRewriteEqual(":1234567890110\n", ":1234567890110\n")
	assertRewriteEqual(":9876543210210\r\n", ":9876543210210\r\n")

	assertRewriteEqual(":123456789\n0100", ":123456789\n0100")
	assertRewriteEqual(":123456789\r\n0200", ":123456789\r\n0200")
	assertRewriteEqual(":123456789\n0100\n", ":123456789\n0100\n")
	assertRewriteEqual(":123456789\r\n0200\r\n", ":123456789\r\n0200\r\n")

	assertRewriteEqual(":1234567890100", ":1234567890120")
	assertRewriteEqual(":9876543210200", ":9876543210220")
	assertRewriteEqual(":1234567890100\n", ":1234567890120\n")
	assertRewriteEqual(":9876543210200\r\n", ":9876543210220\r\n")

	assertRewriteEqual(":1234567890100\n"+prefix+":9876543210200\r\n", ":1234567890120\n"+prefix+":9876543210220\r\n")
}
