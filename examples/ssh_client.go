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

package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"

	"github.com/kevinburke/ssh_config"
	"github.com/trzsz/trzsz-go/trzsz"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

func main() {
	// parse ssh alias
	if len(os.Args) != 2 {
		fmt.Printf("Usage: %s ssh_alias\n", os.Args[0])
		return
	}
	alias := os.Args[1]
	host := ssh_config.Get(alias, "HostName")
	port := ssh_config.Get(alias, "Port")
	user := ssh_config.Get(alias, "User")
	if host == "" || port == "" || user == "" {
		fmt.Printf("ssh alias [%s] invalid: host=[%s] port=[%s] user=[%s]\n", alias, host, port, user)
		return
	}

	// read private key
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("user home dir failed: %s\n", err)
		return
	}
	var signers []ssh.Signer
	for _, name := range []string{"id_rsa", "id_ecdsa", "id_ecdsa_sk", "id_ed25519", "id_ed25519_sk", "id_dsa"} {
		path := filepath.Join(home, ".ssh", name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		}
		privateKey, err := os.ReadFile(path)
		if err != nil {
			fmt.Printf("read private key [%s] failed: %s\n", path, err)
			return
		}
		signer, err := ssh.ParsePrivateKey(privateKey)
		if err != nil {
			fmt.Printf("parse private key [%s] failed: %s\n", path, err)
			return
		}
		signers = append(signers, signer)
	}

	// ssh login
	config := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signers...)},
		Timeout:         3 * time.Second,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // TODO should not be used for production code
	}
	client, err := ssh.Dial("tcp", net.JoinHostPort(host, port), config)
	if err != nil {
		fmt.Printf("ssh dial tcp [%s:%s] failed: %s\n", host, port, err)
		return
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		fmt.Printf("ssh new session failed: %s\n", err)
		return
	}
	defer session.Close()

	// make stdin to raw
	fd := int(os.Stdin.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		fmt.Printf("term make raw failed: %s\n", err)
		return
	}
	defer term.Restore(fd, state) // nolint:all

	// request a pty session
	width, height, err := term.GetSize(fd)
	if err != nil {
		if runtime.GOOS != "windows" {
			fmt.Printf("term get size failed: %s\n", err)
			return
		}
		// TODO find another way to get size on Windows
		width, height = 80, 40
	}
	if err := session.RequestPty("xterm-256color", height, width, ssh.TerminalModes{}); err != nil {
		fmt.Printf("session request pty failed: %s\n", err)
		return
	}

	// session input and output
	serverIn, err := session.StdinPipe()
	if err != nil {
		fmt.Printf("session stdin pipe failed: %s\n", err)
		return
	}
	serverOut, err := session.StdoutPipe()
	if err != nil {
		fmt.Printf("session stdout pipe failed: %s\n", err)
		return
	}

	var trzszFilter *trzsz.TrzszFilter
	// want to do something with stdin and stdout ?
	var wantToDoSomethingWithStdinAndStdout bool
	if !wantToDoSomethingWithStdinAndStdout {
		// create a TrzszFilter to support trzsz, no need to control stdin and stdout.
		//
		//   os.Stdin  ┌────────┐   os.Stdin   ┌─────────────┐   ServerIn   ┌────────┐
		// ───────────►│        ├─────────────►│             ├─────────────►│        │
		//             │        │              │ TrzszFilter │              │        │
		// ◄───────────│ Client │◄─────────────┤             │◄─────────────┤ Server │
		//   os.Stdout │        │   os.Stdout  └─────────────┘   ServerOut  │        │
		// ◄───────────│        │◄──────────────────────────────────────────┤        │
		//   os.Stderr └────────┘                  stderr                   └────────┘
		trzszFilter = trzsz.NewTrzszFilter(os.Stdin, os.Stdout, serverIn, serverOut,
			trzsz.TrzszOptions{TerminalColumns: int32(width)})
		session.Stderr = os.Stderr
	} else {
		// create a TrzszFilter to support trzsz, with stdin and stdout controllable.
		//
		//             ┌──────────────────────────────────────────┐
		//             │                 Client                   │
		//             │                                          │
		//   os.Stdin  │ ┌──────────┐  stdinPipe  ┌───────────┐   │ ClientIn   ┌─────────────┐   ServerIn   ┌────────┐
		// ────────────┼►│          ├────────────►│           ├───┼───────────►│             ├─────────────►│        │
		//             │ │  Custom  │             │  io.Pipe  │   │            │ TrzszFilter │              │        │
		// ◄───────────┼─┤          │◄────────────┤           │◄──┼────────────┤             │◄─────────────┤ Server │
		//   os.Stdout │ └──────────┘  stdoutPipe └───────────┘   │ ClientOut  └─────────────┘   ServerOut  │        │
		// ◄───────────│                                          │◄────────────────────────────────────────┤        │
		//   os.Stderr └──────────────────────────────────────────┘                stderr                   └────────┘
		clientIn, stdinPipe := io.Pipe()   // You can treat stdinPipe as session.StdinPipe()
		stdoutPipe, clientOut := io.Pipe() // You can treat stdoutPipe as session.StdoutPipe()
		trzszFilter = trzsz.NewTrzszFilter(clientIn, clientOut, serverIn, serverOut,
			trzsz.TrzszOptions{TerminalColumns: int32(width)})
		// TODO implement your function with stdin, stdout and stderr
		go io.Copy(stdinPipe, os.Stdin)   // nolint:all
		go io.Copy(os.Stdout, stdoutPipe) // nolint:all
		session.Stderr = os.Stderr
	}

	// connect to linux directly is not affected by Windows
	trzsz.SetAffectedByWindows(false)

	// reset terminal columns on resize
	ch := make(chan os.Signal, 1)
	// signal.Notify(ch, syscall.SIGWINCH) // TODO find another way to do this on Windows
	go func() {
		for range ch {
			width, height, err := term.GetSize(fd)
			if err != nil {
				fmt.Printf("term get size failed: %s\n", err)
				continue
			}
			_ = session.WindowChange(height, width)
			trzszFilter.SetTerminalColumns(int32(width))
		}
	}()
	defer func() { signal.Stop(ch); close(ch) }()

	go func() {
		// call TrzszFilter to upload some files and directories as you want
		// trzszFilter.UploadFiles([]string{"/path/to/file", "/path/to/directory"})

		// tell TrzszFilter to stop transferring files if necessary
		// trzszFilter.StopTransferringFiles()
	}()

	// start shell
	if err := session.Shell(); err != nil {
		fmt.Printf("session shell failed: %s\n", err)
		return
	}

	// wait for exit
	session.Wait() // nolint:all
}
