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

func newTszArgs(args baseArgs, files []string) tszArgs {
	if args.Bufsize.Size == 0 {
		args.Bufsize.Size = 10 * 1024 * 1024
	}
	if args.Timeout == 0 {
		args.Timeout = 20
	}
	return tszArgs{args, files}
}

func TestTszArgs(t *testing.T) {
	assert := assert.New(t)
	assertArgsEqual := func(cmdline string, expectedArg tszArgs) {
		t.Helper()
		var args tszArgs
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

	assertArgsEqual("a", newTszArgs(baseArgs{}, []string{"a"}))
	assertArgsEqual("-q a", newTszArgs(baseArgs{Quiet: true}, []string{"a"}))
	assertArgsEqual("-y a", newTszArgs(baseArgs{Overwrite: true}, []string{"a"}))
	assertArgsEqual("-b a", newTszArgs(baseArgs{Binary: true}, []string{"a"}))
	assertArgsEqual("-e a", newTszArgs(baseArgs{Escape: true}, []string{"a"}))
	assertArgsEqual("-d a", newTszArgs(baseArgs{Directory: true}, []string{"a"}))
	assertArgsEqual("-B 2k a", newTszArgs(baseArgs{Bufsize: bufferSize{2 * 1024}}, []string{"a"}))
	assertArgsEqual("-t 3 a", newTszArgs(baseArgs{Timeout: 3}, []string{"a"}))

	assertArgsEqual("--quiet a", newTszArgs(baseArgs{Quiet: true}, []string{"a"}))
	assertArgsEqual("--overwrite a", newTszArgs(baseArgs{Overwrite: true}, []string{"a"}))
	assertArgsEqual("--binary a", newTszArgs(baseArgs{Binary: true}, []string{"a"}))
	assertArgsEqual("--escape a", newTszArgs(baseArgs{Escape: true}, []string{"a"}))
	assertArgsEqual("--directory a", newTszArgs(baseArgs{Directory: true}, []string{"a"}))
	assertArgsEqual("--bufsize 2M a", newTszArgs(baseArgs{Bufsize: bufferSize{2 * 1024 * 1024}}, []string{"a"}))
	assertArgsEqual("--timeout 55 a", newTszArgs(baseArgs{Timeout: 55}, []string{"a"}))

	assertArgsEqual("-B1024 a", newTszArgs(baseArgs{Bufsize: bufferSize{1024}}, []string{"a"}))
	assertArgsEqual("-B1025b a", newTszArgs(baseArgs{Bufsize: bufferSize{1025}}, []string{"a"}))
	assertArgsEqual("-B 1026B a", newTszArgs(baseArgs{Bufsize: bufferSize{1026}}, []string{"a"}))
	assertArgsEqual("-B 1MB a", newTszArgs(baseArgs{Bufsize: bufferSize{1024 * 1024}}, []string{"a"}))
	assertArgsEqual("-B 2m a", newTszArgs(baseArgs{Bufsize: bufferSize{2 * 1024 * 1024}}, []string{"a"}))
	assertArgsEqual("-B1G a", newTszArgs(baseArgs{Bufsize: bufferSize{1024 * 1024 * 1024}}, []string{"a"}))
	assertArgsEqual("-B 1gb a", newTszArgs(baseArgs{Bufsize: bufferSize{1024 * 1024 * 1024}}, []string{"a"}))

	assertArgsEqual("-yq a", newTszArgs(baseArgs{Quiet: true, Overwrite: true}, []string{"a"}))
	assertArgsEqual("-bed a", newTszArgs(baseArgs{Binary: true, Escape: true, Directory: true}, []string{"a"}))
	assertArgsEqual("-yB 2096 a", newTszArgs(baseArgs{Overwrite: true, Bufsize: bufferSize{2096}}, []string{"a"}))
	assertArgsEqual("-ebt300 a", newTszArgs(baseArgs{Binary: true, Escape: true, Timeout: 300}, []string{"a"}))
	assertArgsEqual("-yqB3K -eb -t 9 -d a", newTszArgs(baseArgs{Quiet: true, Overwrite: true,
		Bufsize: bufferSize{3 * 1024}, Escape: true, Binary: true, Timeout: 9, Directory: true}, []string{"a"}))

	assertArgsEqual("/tmp/b", newTszArgs(baseArgs{}, []string{"/tmp/b"}))
	assertArgsEqual("-y -d a b c", newTszArgs(baseArgs{Overwrite: true, Directory: true}, []string{"a", "b", "c"}))
	assertArgsEqual("-eqt60 ./bb ../xx", newTszArgs(baseArgs{Escape: true, Quiet: true, Timeout: 60}, []string{"./bb", "../xx"}))

	assertArgsError := func(cmdline, errMsg string) {
		t.Helper()
		var args tszArgs
		p, err := arg.NewParser(arg.Config{}, &args)
		assert.Nil(err)
		err = p.Parse(strings.Split(cmdline, " "))
		assert.NotNil(err)
		assert.Contains(err.Error(), errMsg)
	}

	assertArgsError("-B 2gb a", "greater than 1G")
	assertArgsError("-B10 a", "less than 1K")
	assertArgsError("-B10x a", "invalid size 10x")
	assertArgsError("-Bb a", "invalid size b")
	assertArgsError("-tiii a", "iii")
	assertArgsError("-t --directory a", "missing value")
	assertArgsError("-x a", "unknown argument -x")
	assertArgsError("--kkk a", "unknown argument --kkk")
	assertArgsError("-y", "file is required")
	assertArgsError("-q -B 2k -et3", "file is required")
}
