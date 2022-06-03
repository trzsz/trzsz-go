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

	"github.com/alexflint/go-arg"
)

type TszArgs struct {
	Quiet     bool       `arg:"-q" help:"quiet (hide progress bar)"`
	Overwrite bool       `arg:"-y" help:"yes, overwrite existing file(s)"`
	Binary    bool       `arg:"-b" help:"binary transfer mode, faster for binary files"`
	Escape    bool       `arg:"-e" help:"escape all known control characters"`
	Bufsize   BufferSize `arg:"-B" placeholder:"N" default:"10M" help:"max buffer chunk size (1K<=N<=1G). (default: 10M)"`
	Timeout   int        `arg:"-t" placeholder:"N" default:"100" help:"timeout ( N seconds ) for each buffer chunk.\nN <= 0 means never timeout. (default: 100)"`
	File      []string   `arg:"positional,required" help:"file(s) to be sent"`
}

func (TszArgs) Description() string {
	return "Send file(s), similar to sz and compatible with tmux.\n"
}

func (TszArgs) Version() string {
	return fmt.Sprintf("tsz (trzsz) go %s", kTrzszVersion)
}

// TszMain entry of send files to client
func TszMain() int {
	var args TszArgs
	arg.MustParse(&args)

	fmt.Printf("%#v\n", args)
	return 0
}
