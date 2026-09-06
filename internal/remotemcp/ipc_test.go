package remotemcp

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIPCResponseFraming(t *testing.T) {
	for _, scenario := range []string{"fragmented", "partial", "empty", "missing newline", "oversized"} {
		t.Run(scenario, func(t *testing.T) {
			dir, err := os.MkdirTemp("", "ipc-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			path := filepath.Join(dir, "s")
			listener, err := net.Listen("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				_, _ = bufio.NewReader(conn).ReadBytes('\n')
				body, _ := json.Marshal(Response{Version: 1, RequestID: "test-id", Result: map[string]any{"stdout": strings.Repeat("中文\n", 12000)}})
				switch scenario {
				case "partial":
					body = body[:8192]
				case "empty":
					body = nil
				case "oversized":
					body = []byte(strings.Repeat("x", MaxMessageBytes+1))
				case "fragmented":
					body = append(body, '\n')
				}
				for len(body) > 0 {
					n := min(len(body), 997)
					if _, err := conn.Write(body[:n]); err != nil {
						return
					}
					body = body[n:]
				}
			}()
			result, err := (IPC{Socket: path}).Call(context.Background(), Request{RequestID: "test-id"})
			<-done
			if scenario == "fragmented" {
				if err != nil || result["stdout"] != strings.Repeat("中文\n", 12000) {
					t.Fatalf("large fragmented response: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "execution outcome unknown") || !strings.Contains(err.Error(), "request_id=test-id") {
				t.Fatalf("expected actionable incomplete-frame error, got %v", err)
			}
		})
	}
}
