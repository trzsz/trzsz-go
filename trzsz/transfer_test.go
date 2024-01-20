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
	"golang.org/x/text/encoding/charmap"
)

func TestTransferAction(t *testing.T) {
	SetAffectedByWindows(false) // test as on Linux
	defer func() {
		SetAffectedByWindows(isRunningOnWindows())
	}()

	assert := assert.New(t)
	writer := newTestWriter(t)
	clientTransfer := newTransfer(writer, nil, false, nil)
	serverTransfer := newTransfer(writer, nil, false, nil)

	// compatible with older versions
	serverTransfer.addReceivedData(
		[]byte("#ACT:eJyrVspJzEtXslJQKqhU0lFQSs7PS8ssygUKlBSVpgIFylKLijPz80AqDPUM9AxAiopLCwryi0riUzKLEAoLivJL8pPzc4AiBrUAlAQbEA==\n"),
		false)
	action, err := serverTransfer.recvAction()
	assert.Nil(err)
	assert.Equal(&transferAction{
		Lang:             "py",
		Version:          "1.0.0",
		Confirm:          true,
		Newline:          "\n",
		Protocol:         0,
		SupportBinary:    true,
		SupportDirectory: true,
	}, action)
	assert.False(serverTransfer.windowsProtocol)
	assert.Equal("\n", serverTransfer.transferConfig.Newline)

	// client and server are Linux
	SetAffectedByWindows(false)
	err = clientTransfer.sendAction(true, nil, false)
	assert.Nil(err)
	writer.assertBufferCount(1)
	assert.False(clientTransfer.windowsProtocol)
	assert.Equal("\n", clientTransfer.transferConfig.Newline)

	SetAffectedByWindows(false)
	serverTransfer.addReceivedData([]byte(writer.buffer[0]), false)
	action, err = serverTransfer.recvAction()
	assert.Nil(err)
	assert.Equal("\n", action.Newline)
	assert.True(action.SupportBinary)
	assert.False(serverTransfer.windowsProtocol)
	assert.Equal("\n", serverTransfer.transferConfig.Newline)
	assert.Equal(kProtocolVersion, action.Protocol)

	// client is Windows, server is Linux
	SetAffectedByWindows(true)
	err = clientTransfer.sendAction(true, nil, false)
	assert.Nil(err)
	writer.assertBufferCount(2)
	assert.False(clientTransfer.windowsProtocol)
	assert.Equal("\n", clientTransfer.transferConfig.Newline)

	SetAffectedByWindows(false)
	serverTransfer.addReceivedData([]byte(writer.buffer[1]), false)
	action, err = serverTransfer.recvAction()
	assert.Nil(err)
	assert.Equal("!\n", action.Newline)
	assert.False(action.SupportBinary)
	assert.False(serverTransfer.windowsProtocol)
	assert.Equal("!\n", serverTransfer.transferConfig.Newline)
	assert.Equal(kProtocolVersion, action.Protocol)

	// client is Linux, server is Windows
	SetAffectedByWindows(false)
	err = clientTransfer.sendAction(true, nil, true)
	assert.Nil(err)
	writer.assertBufferCount(3)
	assert.True(clientTransfer.windowsProtocol)
	assert.Equal("!\n", clientTransfer.transferConfig.Newline)

	SetAffectedByWindows(true)
	serverTransfer.addReceivedData([]byte(writer.buffer[2]), false)
	action, err = serverTransfer.recvAction()
	assert.Nil(err)
	assert.Equal("!\n", action.Newline)
	assert.False(action.SupportBinary)
	assert.True(isWindowsEnvironment() || serverTransfer.windowsProtocol)
	assert.Equal("!\n", serverTransfer.transferConfig.Newline)
	assert.Equal(kProtocolVersion, action.Protocol)

	// client and server are Windows
	SetAffectedByWindows(true)
	err = clientTransfer.sendAction(true, nil, true)
	assert.Nil(err)
	writer.assertBufferCount(4)
	assert.True(clientTransfer.windowsProtocol)
	assert.Equal("!\n", clientTransfer.transferConfig.Newline)

	SetAffectedByWindows(true)
	serverTransfer.addReceivedData([]byte(writer.buffer[3]), false)
	action, err = serverTransfer.recvAction()
	assert.Nil(err)
	assert.Equal("!\n", action.Newline)
	assert.False(action.SupportBinary)
	assert.True(isWindowsEnvironment() || serverTransfer.windowsProtocol)
	assert.Equal("!\n", serverTransfer.transferConfig.Newline)
	assert.Equal(kProtocolVersion, action.Protocol)
}

func TestTransferConfig(t *testing.T) {
	SetAffectedByWindows(false) // test as on Linux
	defer func() {
		SetAffectedByWindows(isRunningOnWindows())
	}()

	assert := assert.New(t)
	writer := newTestWriter(t)
	transfer := newTransfer(writer, nil, false, nil)

	escapeChars := getEscapeChars(true)
	err := transfer.sendConfig(&baseArgs{Quiet: true, Overwrite: true, Binary: true, Escape: true, Directory: true,
		Bufsize: bufferSize{1024}, Timeout: 10}, &transferAction{Protocol: 2}, escapeChars, tmuxNormalMode, 88)
	assert.Nil(err)
	writer.assertBufferCount(1)

	encoder := charmap.ISO8859_1.NewEncoder()
	table := &escapeTable{
		totalCount:    len(escapeChars),
		escapeCodes:   make([]*byte, 256),
		unescapeCodes: make([]*byte, 256),
	}
	for _, v := range escapeChars {
		b, err := encoder.Bytes([]byte(v[0]))
		assert.Nil(err)
		c, err := encoder.Bytes([]byte(v[1]))
		assert.Nil(err)
		assert.Equal(escapeLeaderByte, c[0])
		table.escapeCodes[b[0]] = &c[1]
		table.unescapeCodes[c[1]] = &b[0]
	}
	config := transferConfig{
		Quiet:           true,
		Binary:          true,
		Directory:       true,
		Overwrite:       true,
		Timeout:         10,
		Newline:         "\n",
		Protocol:        2,
		MaxBufSize:      1024,
		EscapeTable:     table,
		TmuxPaneColumns: 88,
		TmuxOutputJunk:  true,
	}
	assert.Equal(config, transfer.transferConfig)

	assertConfigEqual := func(cfgStr string) {
		t.Helper()
		transfer.addReceivedData([]byte(cfgStr), false)
		transferConfig, err := transfer.recvConfig()
		assert.Nil(err)
		assert.Equal(config, *transferConfig)
		assert.Equal(config, transfer.transferConfig)
	}

	cfgStr := "#CFG:eJxE0UtSwzAMBuC7/GsvnMAieMf7fYKQyTiJaA1tHByZ8hg4OxOmlXbSJ4/9j/WNLow+fcJxymTQ5ec5fBFcYctjgyEk" +
		"6jnqnObeT9T2a59muLrGU7aWCGZf7NvG1PgVLP77pbal4KniIHgmWFjBc8VC8ELxSPBSsRK8UuwErxX19RvBSvFW8EQj3SlqpHtFjfSgq" +
		"Hc+omkMNn5cwWEVYRDfKe1SYDp89JQixz5u4EqDtxyIDxMOW4qZlxUZ8DZ/tDHzlLl9yeOrHFp88iO1uzDwGq6qfv4CAAD//wzRkW0=\n"
	assertConfigEqual(cfgStr)
	assertConfigEqual(writer.buffer[0])
}

func TestStripTmuxStatusLine(t *testing.T) {
	assert := assert.New(t)
	writer := newTestWriter(t)
	transfer := newTransfer(writer, nil, false, nil)

	P := "\x1bP=1s\x1b\\\x1b[?25l\x1b[?12l\x1b[?25h\x1b[5 q\x1bP=2s\x1b\\"
	assert.Equal([]byte("ABC123"), transfer.stripTmuxStatusLine([]byte("ABC"+"123")))
	assert.Equal([]byte("ABC123"), transfer.stripTmuxStatusLine([]byte("ABC"+P+"123")))
	assert.Equal([]byte("ABC123XYZ"), transfer.stripTmuxStatusLine([]byte("ABC"+P+"123"+P+"XYZ")))
	assert.Equal([]byte("ABC123XYZ"), transfer.stripTmuxStatusLine([]byte("ABC"+P+"123"+P+P+P+"XYZ")))

	for i := 0; i < len(P)-2; i++ {
		assert.Equal([]byte("ABC123"), transfer.stripTmuxStatusLine([]byte("ABC"+P+"123"+P[:len(P)-i])))
	}
}
