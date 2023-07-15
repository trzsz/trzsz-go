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
	serverTransfer.addReceivedData([]byte(
		"#ACT:eJyrVspJzEtXslJQKqhU0lFQSs7PS8ssygUKlBSVpgIFylKLijPz80AqDPUM9AxAiopLCwryi0riUzKLEAoLivJL8pPzc4AiBrUAlAQbEA==\n"))
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
	serverTransfer.addReceivedData([]byte(writer.buffer[0]))
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
	serverTransfer.addReceivedData([]byte(writer.buffer[1]))
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
	serverTransfer.addReceivedData([]byte(writer.buffer[2]))
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
	serverTransfer.addReceivedData([]byte(writer.buffer[3]))
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
	escapeCodes := make([][]byte, len(escapeChars))
	for i, v := range escapeChars {
		b, err := encoder.Bytes([]byte(v[0]))
		assert.Nil(err)
		c, err := encoder.Bytes([]byte(v[1]))
		assert.Nil(err)
		escapeCodes[i] = make([]byte, 3)
		escapeCodes[i][0] = b[0]
		escapeCodes[i][1] = c[0]
		escapeCodes[i][2] = c[1]
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
		EscapeCodes:     escapeCodes,
		TmuxPaneColumns: 88,
		TmuxOutputJunk:  true,
	}
	assert.Equal(config, transfer.transferConfig)

	assertConfigEqual := func(cfgStr string) {
		t.Helper()
		transfer.addReceivedData([]byte(cfgStr))
		transferConfig, err := transfer.recvConfig()
		assert.Nil(err)
		assert.Equal(config, *transferConfig)
		assert.Equal(config, transfer.transferConfig)
	}

	cfgStr := "#CFG:eJxFz0sSgjAQBNC7zDqLQLnA7PydAikqhBGiSGKYiJ/SswsWhF3369nMGwrdSvcEQc4jg8KfOv1CEBGPVwxK7VCR" +
		"WXbslLSYq1q6DkSawtFzjghsClPNWArfgNG/j5nHATcBIx5wu2ARcLdgGXAfcL3gAbKMQSPbCgRUZnBzR9c7TTg/YJ0ho0wDImZw8" +
		"xppXkhf0XgaXx/K1T/yoVlP+dm3l3A0upUt5r0uqQaRJJ8f2dNlYw==\n"
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
