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
	"bytes"
	"testing"
	"time"
)

func TestTerminalInputWaitsForTunnelDecision(t *testing.T) {
	for _, connected := range []bool{true, false} {
		name := "fallback"
		if connected {
			name = "connected"
		}
		t.Run(name, func(t *testing.T) {
			transfer := newTransfer(nil, nil)
			transfer.tunnelInitWG.Add(1)
			terminalData := []byte("\x1b[?2026hbackground redraw\n")
			done := make(chan struct{})
			go func() {
				transfer.addReceivedData(terminalData, false)
				close(done)
			}()
			select {
			case <-done:
				transfer.tunnelInitWG.Done()
				t.Fatal("terminal data was consumed before the tunnel decision")
			case <-time.After(50 * time.Millisecond):
			}

			// Tunnel input must remain readable during connection setup.
			tunnelData := []byte("#CFG:test\n")
			transfer.addReceivedData(tunnelData, true)
			if got := transfer.buffer.popBuffer(); !bytes.Equal(got, tunnelData) {
				t.Errorf("tunnel input = %q, want %q", got, tunnelData)
			}
			transfer.tunnelConnected = connected
			transfer.tunnelInitWG.Done()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("terminal input did not resume after the tunnel decision")
			}
			got := transfer.buffer.popBuffer()
			if connected && got != nil {
				t.Fatalf("terminal redraw polluted tunnel input: %q", got)
			}
			if !connected && !bytes.Equal(got, terminalData) {
				t.Fatalf("fallback input = %q, want %q", got, terminalData)
			}
		})
	}
}
