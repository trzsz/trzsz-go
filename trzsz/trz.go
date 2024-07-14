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
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/trzsz/go-arg"
	"golang.org/x/term"
)

type trzArgs struct {
	baseArgs
	Path string `arg:"positional" default:"." help:"path to save file(s). (default: current directory)"`
}

func (trzArgs) Description() string {
	return "Receive file(s), similar to rz and compatible with tmux.\n"
}

func (trzArgs) Version() string {
	return fmt.Sprintf("trz (trzsz) go %s", kTrzszVersion)
}

func parseTrzArgs(osArgs []string) *trzArgs {
	var args trzArgs
	parser, err := arg.NewParser(arg.Config{Out: os.Stderr, Exit: os.Exit}, &args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(-1)
		return nil
	}
	var flags []string
	if len(osArgs) > 0 {
		flags = osArgs[1:]
	}
	parser.MustParse(flags)
	if args.Recursive {
		args.Directory = true
	}
	return &args
}

func recvFiles(transfer *trzszTransfer, args *trzArgs, tmuxMode tmuxModeType, tmuxPaneWidth int32) error {
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

	// check if the client doesn't support fork to background
	if args.Fork && !action.SupportFork {
		return simpleTrzszError("The client doesn't support fork to background")
	}

	// check if the client doesn't support transfer directory
	if args.Directory && !action.SupportDirectory {
		return simpleTrzszError("The client doesn't support transfer directory")
	}

	escapeChars := getEscapeChars(args.Escape)
	if err := transfer.sendConfig(&args.baseArgs, action, escapeChars, tmuxMode, tmuxPaneWidth); err != nil {
		return err
	}

	localNames, err := transfer.recvFiles(args.Path, nil)
	if err != nil {
		return err
	}

	if _, err := transfer.recvExit(); err != nil {
		return err
	}

	transfer.serverExit(formatSavedFiles(localNames, args.Path))
	return nil
}

// TrzMain is the main function of `trz` binary.
func TrzMain() int {
	args := parseTrzArgs(os.Args)

	// fork to background
	if args.Fork {
		parent, err := forkToBackground()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if parent {
			return 0
		}
	}

	// cleanup on exit
	defer cleanupOnExit()

	var err error
	args.Path, err = filepath.Abs(args.Path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Get absolute path of [%s] failed: %v\r\n", args.Path, err)
		return -1
	}
	if err := checkPathWritable(args.Path); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return -2
	}

	tmuxMode, realStdout, tmuxPaneWidth, err := checkTmux()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return -3
	}

	if args.Binary && tmuxMode != noTmuxMode {
		os.Stdout.WriteString("Binary upload in tmux is not supported, auto switch to base64 mode.\n")
		args.Binary = false
	}
	if args.Binary && isRunningOnWindows() {
		os.Stdout.WriteString("Binary upload on Windows is not supported, auto switch to base64 mode.\n")
		args.Binary = false
	}

	uniqueID := (time.Now().UnixMilli() % 10e10) * 100
	if isRunningOnWindows() {
		_ = setupVirtualTerminal()
		setupConsoleOutput()
		uniqueID += 10
	} else if tmuxMode == tmuxNormalMode {
		columns := getTerminalColumns()
		if columns > 0 && columns < 40 {
			os.Stdout.WriteString("\n\n\x1b[2A\x1b[0J")
		} else {
			os.Stdout.WriteString("\n\x1b[1A\x1b[0J")
		}
		uniqueID += 20
	}

	listener, port := listenForTunnel()

	mode := "R"
	if args.Directory {
		mode = "D"
	}
	os.Stdout.WriteString(fmt.Sprintf("\x1b7\x07::TRZSZ:TRANSFER:%s:%s:%013d:%d\r\n", mode, kTrzszVersion, uniqueID, port))
	os.Stdout.Sync()

	var state *term.State
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		state, err = term.MakeRaw(fd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Make stdin raw failed: %v\r\n", err)
			return -4
		}
		defer func() { _ = term.Restore(fd, state) }()
	}

	transfer := newTransfer(realStdout, state, false, nil)
	defer func() {
		if err := recover(); err != nil {
			transfer.serverError(newTrzszError(fmt.Sprintf("%v", err), "panic", true))
		}
	}()

	if listener != nil {
		defer listener.Close()
		transfer.acceptOnTunnel(listener, fmt.Sprintf("%013d", uniqueID), port)
	}
	wrapTransferInput(transfer, os.Stdin, false)
	handleServerSignal(transfer)

	done := make(chan struct{}, 1)
	go func() {
		defer close(done)
		if err := recvFiles(transfer, args, tmuxMode, tmuxPaneWidth); err != nil {
			transfer.serverError(err)
		}
		transfer.cleanup()
		done <- struct{}{}
	}()

	select {
	case <-done:
	case <-transfer.background():
	}

	return 0
}
