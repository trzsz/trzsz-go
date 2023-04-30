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
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/alexflint/go-arg"
	"golang.org/x/term"
)

type TszArgs struct {
	Args
	File []string `arg:"positional,required" help:"file(s) to be sent"`
}

func (TszArgs) Description() string {
	return "Send file(s), similar to sz and compatible with tmux.\n"
}

func (TszArgs) Version() string {
	return fmt.Sprintf("tsz (trzsz) go %s", kTrzszVersion)
}

func sendFiles(transfer *TrzszTransfer, files []*TrzszFile, args *TszArgs, tmuxMode TmuxMode, tmuxPaneWidth int) error {
	action, err := transfer.recvAction()
	if err != nil {
		return err
	}

	if !action.Confirm {
		transfer.serverExit("Cancelled")
		return nil
	}

	// check if the client doesn't support binary mode
	if args.Binary && !action.SupportBinary {
		args.Binary = false
	}

	// check if the client doesn't support transfer directory
	if args.Directory && !action.SupportDirectory {
		return newTrzszError("The client doesn't support transfer directory")
	}

	var escapeChars [][]unicode
	if err := transfer.sendConfig(&args.Args, action, escapeChars, tmuxMode, tmuxPaneWidth); err != nil {
		return err
	}

	if _, err := transfer.sendFiles(files, nil); err != nil {
		return err
	}

	msg, err := transfer.recvExit()
	if err != nil {
		return err
	}

	transfer.serverExit(msg)
	return nil
}

// TszMain entry of send files to client
func TszMain() int {
	var args TszArgs
	arg.MustParse(&args)

	files, err := checkPathsReadable(args.File, args.Directory)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return -1
	}
	if args.Overwrite {
		if err := checkDuplicateNames(files); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return -2
		}
	}

	tmuxMode, realStdout, tmuxPaneWidth, err := checkTmux()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return -3
	}

	if args.Binary && tmuxMode == TmuxControlMode {
		os.Stdout.WriteString("Binary download in tmux control mode is slower, auto switch to base64 mode.\n")
		args.Binary = false
	}
	if args.Binary && IsWindows() {
		os.Stdout.WriteString("Binary download on Windows is not supported, auto switch to base64 mode.\n")
		args.Binary = false
	}

	uniqueID := strconv.FormatInt(time.Now().UnixMilli()%10e10, 10)
	if IsWindows() {
		if inMode, outMode, err := enableVirtualTerminal(); err == nil {
			defer resetVirtualTerminal(inMode, outMode)
		}
		setupConsoleOutput()
		uniqueID += "10"
	} else if tmuxMode == TmuxNormalMode {
		columns := getTerminalColumns()
		if columns > 0 && columns < 40 {
			os.Stdout.WriteString("\n\n\x1b[2A\x1b[0J")
		} else {
			os.Stdout.WriteString("\n\x1b[1A\x1b[0J")
		}
		uniqueID += "20"
	} else {
		uniqueID += "00"
	}

	os.Stdout.WriteString(fmt.Sprintf("\x1b7\x07::TRZSZ:TRANSFER:S:%s:%s\r\n", kTrzszVersion, uniqueID))
	os.Stdout.Sync()

	state, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return -4
	}
	defer func() { _ = term.Restore(int(os.Stdin.Fd()), state) }()

	transfer := NewTransfer(realStdout, state, false)
	defer func() {
		if err := recover(); err != nil {
			transfer.serverError(NewTrzszError(fmt.Sprintf("%v", err), "panic", true))
		}
	}()

	go wrapStdinInput(transfer)
	handleServerSignal(transfer)

	if err := sendFiles(transfer, files, &args, tmuxMode, tmuxPaneWidth); err != nil {
		transfer.serverError(err)
	}

	return 0
}
