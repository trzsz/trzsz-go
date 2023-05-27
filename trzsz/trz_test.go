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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/trzsz/go-arg"
)

func newTrzArgs(args baseArgs, path string) trzArgs {
	if args.Bufsize.Size == 0 {
		args.Bufsize.Size = 10 * 1024 * 1024
	}
	if args.Timeout == 0 {
		args.Timeout = 20
	}
	return trzArgs{args, path}
}

func TestTrzArgs(t *testing.T) {
	assert := assert.New(t)
	assertArgsEqual := func(cmdline string, expectedArg trzArgs) {
		t.Helper()
		var args trzArgs
		p, err := arg.NewParser(arg.Config{}, &args)
		assert.Nil(err)
		if cmdline == "" {
			err = p.Parse(nil)
		} else {
			err = p.Parse(strings.Split(cmdline, " "))
		}
		assert.Nil(err)
		assert.Equal(expectedArg, args)
	}

	assertArgsEqual("", newTrzArgs(baseArgs{}, "."))
	assertArgsEqual("-q", newTrzArgs(baseArgs{Quiet: true}, "."))
	assertArgsEqual("-y", newTrzArgs(baseArgs{Overwrite: true}, "."))
	assertArgsEqual("-b", newTrzArgs(baseArgs{Binary: true}, "."))
	assertArgsEqual("-e", newTrzArgs(baseArgs{Escape: true}, "."))
	assertArgsEqual("-d", newTrzArgs(baseArgs{Directory: true}, "."))
	assertArgsEqual("-B 2k", newTrzArgs(baseArgs{Bufsize: bufferSize{2 * 1024}}, "."))
	assertArgsEqual("-t 3", newTrzArgs(baseArgs{Timeout: 3}, "."))

	assertArgsEqual("--quiet", newTrzArgs(baseArgs{Quiet: true}, "."))
	assertArgsEqual("--overwrite", newTrzArgs(baseArgs{Overwrite: true}, "."))
	assertArgsEqual("--binary", newTrzArgs(baseArgs{Binary: true}, "."))
	assertArgsEqual("--escape", newTrzArgs(baseArgs{Escape: true}, "."))
	assertArgsEqual("--directory", newTrzArgs(baseArgs{Directory: true}, "."))
	assertArgsEqual("--bufsize 2M", newTrzArgs(baseArgs{Bufsize: bufferSize{2 * 1024 * 1024}}, "."))
	assertArgsEqual("--timeout 55", newTrzArgs(baseArgs{Timeout: 55}, "."))

	assertArgsEqual("-B1024", newTrzArgs(baseArgs{Bufsize: bufferSize{1024}}, "."))
	assertArgsEqual("-B1025b", newTrzArgs(baseArgs{Bufsize: bufferSize{1025}}, "."))
	assertArgsEqual("-B 1026B", newTrzArgs(baseArgs{Bufsize: bufferSize{1026}}, "."))
	assertArgsEqual("-B 1MB", newTrzArgs(baseArgs{Bufsize: bufferSize{1024 * 1024}}, "."))
	assertArgsEqual("-B 2m", newTrzArgs(baseArgs{Bufsize: bufferSize{2 * 1024 * 1024}}, "."))
	assertArgsEqual("-B1G", newTrzArgs(baseArgs{Bufsize: bufferSize{1024 * 1024 * 1024}}, "."))
	assertArgsEqual("-B 1gb", newTrzArgs(baseArgs{Bufsize: bufferSize{1024 * 1024 * 1024}}, "."))

	assertArgsEqual("-yq", newTrzArgs(baseArgs{Quiet: true, Overwrite: true}, "."))
	assertArgsEqual("-bed", newTrzArgs(baseArgs{Binary: true, Escape: true, Directory: true}, "."))
	assertArgsEqual("-yB 2096", newTrzArgs(baseArgs{Overwrite: true, Bufsize: bufferSize{2096}}, "."))
	assertArgsEqual("-ebt300", newTrzArgs(baseArgs{Binary: true, Escape: true, Timeout: 300}, "."))
	assertArgsEqual("-yqB3K -eb -t 9 -d", newTrzArgs(baseArgs{Quiet: true, Overwrite: true,
		Bufsize: bufferSize{3 * 1024}, Escape: true, Binary: true, Timeout: 9, Directory: true}, "."))

	assertArgsEqual("/tmp", newTrzArgs(baseArgs{}, "/tmp"))
	assertArgsEqual("-y -d ../adir", newTrzArgs(baseArgs{Overwrite: true, Directory: true}, "../adir"))
	assertArgsEqual("-eqt60 ./bbb", newTrzArgs(baseArgs{Escape: true, Quiet: true, Timeout: 60}, "./bbb"))

	assertArgsError := func(cmdline, errMsg string) {
		t.Helper()
		var args trzArgs
		p, err := arg.NewParser(arg.Config{}, &args)
		assert.Nil(err)
		err = p.Parse(strings.Split(cmdline, " "))
		assert.NotNil(err)
		assert.Contains(err.Error(), errMsg)
	}

	assertArgsError("-B 2gb", "greater than 1G")
	assertArgsError("-B10", "less than 1K")
	assertArgsError("-B10x", "invalid size 10x")
	assertArgsError("-Bb", "invalid size b")
	assertArgsError("-tiii", "iii")
	assertArgsError("-t --directory", "missing value")
	assertArgsError("-x", "unknown argument -x")
	assertArgsError("--kkk", "unknown argument --kkk")
	assertArgsError("abc xyz", "too many positional arguments")
	assertArgsError("-q -B 2k -et3 abc xyz", "too many positional arguments")
}
