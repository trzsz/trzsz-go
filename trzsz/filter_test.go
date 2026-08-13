/*
MIT License

Copyright (c) 2022-2026 The Trzsz Authors.

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
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectOSC52(t *testing.T) {
	oriWriteToClipboard := writeToClipboard
	defer func() {
		writeToClipboard = oriWriteToClipboard
	}()

	writer := newTestWriter(t)
	writeToClipboard = func(buf []byte) {
		_, _ = writer.Write(buf)
	}

	filter := &TrzszFilter{}

	filter.detectOSC52([]byte("ABC"))
	writer.assertBufferCount(0)

	filter.detectOSC52([]byte("\x1b]52;"))
	writer.assertBufferCount(0)

	filter.detectOSC52([]byte("\x1b]52;a;"))
	writer.assertBufferCount(0)

	filter.detectOSC52([]byte("\x1b]52;a;A\a"))
	writer.assertBufferCount(0)

	filter.detectOSC52([]byte("\x1b]52;a;A\a\x1b]52;c;X\aZ"))
	writer.assertBufferEqual(0, "X")

	writer.clearBuffer()
	filter.detectOSC52([]byte("\x1b]52;a;A\a\x1b]52;b;B\a\x1b]52;c;X\aZ"))
	writer.assertBufferEqual(0, "X")

	writer.clearBuffer()
	filter.detectOSC52([]byte("\x1b]52;c;ABC"))
	writer.assertBufferCount(0)
	filter.detectOSC52([]byte("xyz\a"))
	writer.assertBufferEqual(0, "ABCxyz")

	writer.clearBuffer()
	filter.detectOSC52([]byte("\x1b]52;c;ABC"))
	writer.assertBufferCount(0)
	filter.detectOSC52([]byte("\x1bxyz\a"))
	writer.assertBufferEqual(0, "ABC")

	writer.clearBuffer()
	filter.detectOSC52([]byte("11\x1b]52;c;ABC\a\x1b]52;p;xyz\x1b00"))
	writer.assertBufferEqual(0, "ABC")
	writer.assertBufferEqual(1, "xyz")

	writer.clearBuffer()
	filter.detectOSC52([]byte("\x1b]52;c;ABC"))
	writer.assertBufferCount(0)
	filter.detectOSC52([]byte("DEFG"))
	writer.assertBufferCount(0)
	filter.detectOSC52([]byte("HIJ\a111\x1b]52;p;xyz\x1b00\x1b]52;c;123"))
	writer.assertBufferCount(2)
	writer.assertBufferEqual(0, "ABCDEFGHIJ")
	writer.assertBufferEqual(1, "xyz")
	filter.detectOSC52([]byte("\a"))
	writer.assertBufferEqual(2, "123")

	writer.clearBuffer()
	filter.detectOSC52([]byte("\x1b]52;c;ABC"))
	for range 2000 {
		filter.detectOSC52([]byte("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"))
	}
	filter.detectOSC52([]byte("==\x1b"))
	writer.assertBufferEqual(0, "ABC"+
		strings.Repeat("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/", 2000)+"==")

	writer.clearBuffer()
	filter.detectOSC52([]byte("\x1b]52;c;ABC"))
	for range 2000 {
		filter.detectOSC52([]byte("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"))
	}
	filter.detectOSC52([]byte("==$=="))
	writer.assertBufferCount(0)
	filter.detectOSC52([]byte("\x1b]52;c;ABC\a"))
	writer.assertBufferEqual(0, "ABC")
}

func TestHandleLocalPathMessage(t *testing.T) {
	// Given: real files so os.Stat / checkPathsReadable pass
	tmp := t.TempDir()
	file1 := filepath.Join(tmp, "a.jar")
	file2 := filepath.Join(tmp, "b.jar")
	if err := os.WriteFile(file1, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file2, []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	filter := &TrzszFilter{}
	paths := file1 + "," + file2
	msg := "\x1b[s::TRZSZ:LOCALPATH:" + base64.StdEncoding.EncodeToString([]byte(paths)) + "\r\n"

	// When
	consumed, err := filter.handleLocalPathMessage([]byte(msg))

	// Then
	if err != nil {
		t.Fatalf("handleLocalPathMessage() error = %v", err)
	}
	if consumed != len(msg) {
		t.Fatalf("consumed = %d, want %d", consumed, len(msg))
	}
	if len(filter.oneTimeUploadFiles) != 2 || filter.oneTimeUploadFiles[0] != file1 || filter.oneTimeUploadFiles[1] != file2 {
		t.Fatalf("oneTimeUploadFiles = %v, want [%s %s]", filter.oneTimeUploadFiles, file1, file2)
	}
	filter.oneTimeUploadFiles = nil // stop the OneTimeUpload timeout goroutine side effect
}

func TestHandleLocalPathMessageErrors(t *testing.T) {
	emptyPaths := base64.StdEncoding.EncodeToString([]byte(" , "))
	tests := []struct {
		name string
		buf  string
	}{
		{"no prefix", "::TRZSZ:NOPE:xxx\r\n"},
		{"invalid base64", "\x1b[s::TRZSZ:LOCALPATH:!!!\r\n"},
		{"empty paths", "\x1b[s::TRZSZ:LOCALPATH:" + emptyPaths + "\r\n"},
		{"not terminated", "\x1b[s::TRZSZ:LOCALPATH:" + base64.StdEncoding.EncodeToString([]byte("/x"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When
			consumed, err := (&TrzszFilter{}).handleLocalPathMessage([]byte(tt.buf))
			// Then
			if err == nil {
				t.Fatalf("expected error for %q, got nil (consumed=%d)", tt.buf, consumed)
			}
		})
	}
}

func TestParseLocalPathMessage(t *testing.T) {
	paths := "/tmp/a.jar, /tmp/b.jar"
	msg := "\x1b[s::TRZSZ:LOCALPATH:" + base64.StdEncoding.EncodeToString([]byte(paths)) + "\r\n"

	// When
	consumed, parsed, err := (&TrzszFilter{}).parseLocalPathMessage([]byte(msg))

	// Then
	if err != nil {
		t.Fatalf("parseLocalPathMessage() error = %v", err)
	}
	if consumed != len(msg) {
		t.Errorf("consumed = %d, want %d", consumed, len(msg))
	}
	want := []string{"/tmp/a.jar", "/tmp/b.jar"}
	if len(parsed) != len(want) || parsed[0] != want[0] || parsed[1] != want[1] {
		t.Errorf("parsed = %v, want %v", parsed, want)
	}
}

func TestParseLocalPathMessageErrors(t *testing.T) {
	tests := []struct {
		name string
		buf  string
	}{
		{"no prefix", "::TRZSZ:NOPE:xxx\r\n"},
		{"invalid base64", "\x1b[s::TRZSZ:LOCALPATH:!!!\r\n"},
		{"empty paths", "\x1b[s::TRZSZ:LOCALPATH:" + base64.StdEncoding.EncodeToString([]byte(" , ")) + "\r\n"},
		{"not terminated", "\x1b[s::TRZSZ:LOCALPATH:" + base64.StdEncoding.EncodeToString([]byte("/x"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When
			consumed, parsed, err := (&TrzszFilter{}).parseLocalPathMessage([]byte(tt.buf))
			// Then
			if err == nil {
				t.Fatalf("expected error for %q, got nil (consumed=%d, parsed=%v)", tt.buf, consumed, parsed)
			}
		})
	}
}

func TestLoadTrzszConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	conf := filepath.Join(tmp, ".trzsz.conf")
	content := "# comment line\n\nlocal_path_upload = true\nclient_auto_upload=yes\nprogresscolorpair=blue\n=novalue\n"
	if err := os.WriteFile(conf, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got := loadTrzszConfig()
	if got["local_path_upload"] != "true" {
		t.Errorf("local_path_upload = %q, want %q", got["local_path_upload"], "true")
	}
	if got["client_auto_upload"] != "yes" {
		t.Errorf("client_auto_upload = %q, want %q", got["client_auto_upload"], "yes")
	}
	if got["progresscolorpair"] != "blue" {
		t.Errorf("progresscolorpair = %q, want %q", got["progresscolorpair"], "blue")
	}
	if _, ok := got[""]; ok {
		t.Error("empty key should be skipped")
	}
}

func TestLoadTrzszConfigMissingFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if got := loadTrzszConfig(); got != nil {
		t.Errorf("loadTrzszConfig() = %v, want nil for missing file", got)
	}
}

func TestReadTrzszConfigSwitches(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	conf := filepath.Join(tmp, ".trzsz.conf")
	content := "local_path_upload=false\nclient_auto_upload=1\n"
	if err := os.WriteFile(conf, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	filter := &TrzszFilter{}
	filter.readTrzszConfig()
	if filter.clientAutoUpload != true {
		t.Errorf("clientAutoUpload = %v, want true", filter.clientAutoUpload)
	}
	if filter.localPathUpload != false {
		t.Errorf("localPathUpload = %v, want false", filter.localPathUpload)
	}
}

func TestIsTruthy(t *testing.T) {
	truthy := []string{"true", "TRUE", "1", "yes", "on", " True "}
	falsy := []string{"false", "0", "no", "off", "", "banana"}
	for _, v := range truthy {
		if !isTruthy(v) {
			t.Errorf("isTruthy(%q) = false, want true", v)
		}
	}
	for _, v := range falsy {
		if isTruthy(v) {
			t.Errorf("isTruthy(%q) = true, want false", v)
		}
	}
}

func TestSetClientAutoUpload(t *testing.T) {
	filter := &TrzszFilter{}
	if filter.clientAutoUpload != false {
		t.Errorf("clientAutoUpload initial = %v, want false", filter.clientAutoUpload)
	}
	filter.SetClientAutoUpload(true)
	if filter.clientAutoUpload != true {
		t.Errorf("clientAutoUpload after Set(true) = %v, want true", filter.clientAutoUpload)
	}
	filter.SetClientAutoUpload(false)
	if filter.clientAutoUpload != false {
		t.Errorf("clientAutoUpload after Set(false) = %v, want false", filter.clientAutoUpload)
	}
}
