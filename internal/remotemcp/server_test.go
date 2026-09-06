package remotemcp

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sasuke39/open-warp/internal/workspacetools"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type recordingTransport struct{ requests []Request }

func (r *recordingTransport) Call(_ context.Context, request Request) (map[string]any, error) {
	r.requests = append(r.requests, request)
	return map[string]any{"status": "completed"}, nil
}
func TestMCPToolsValidateAndShareTranslation(t *testing.T) {
	ctx := context.Background()
	recorder := &recordingTransport{}
	a, b := mcp.NewInMemoryTransports()
	ss, err := NewServer(recorder).Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	list, err := cs.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 7 {
		t.Fatalf("tools: %v %v", list, err)
	}
	for _, dir := range []string{"", "/tmp/a b"} {
		args := map[string]any{"server_id": "test", "request_id": uuid.NewString(), "command": "echo ok", "execution_mode": "foreground"}
		if dir != "" {
			args["workdir"] = dir
		}
		result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "workspace_shell", Arguments: args})
		if err != nil || result.IsError {
			t.Fatalf("default directory rejected: %v %v", result, err)
		}
		if recorder.requests[len(recorder.requests)-1].Workdir != dir {
			t.Fatal("directory silently changed")
		}
	}
	before := len(recorder.requests)
	for _, command := range []string{"echo 'broken", "sleep 1 &", "nohup sleep 1"} {
		result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "workspace_shell", Arguments: map[string]any{"server_id": "test", "request_id": uuid.NewString(), "command": command, "execution_mode": "foreground"}})
		if err != nil || !result.IsError {
			t.Fatalf("invalid command accepted: %v %v", result, err)
		}
	}
	if len(recorder.requests) != before {
		t.Fatal("rejected commands reached IPC")
	}
	id := uuid.NewString()
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "process_read", Arguments: map[string]any{"server_id": "test", "command_id": id}})
	if err != nil || result.IsError {
		t.Fatalf("read: %v %v", result, err)
	}
	req := recorder.requests[len(recorder.requests)-1]
	if req.Operation != "process.read" || req.CommandID != id || !strings.Contains(req.Command, id) {
		t.Fatal("control was not preserved")
	}
}

// Invoked by the Rust client integration test with its production IPC listener
// and a real, isolated local sshd. Never uses the user's saved profiles.
func TestDesktopSSHIntegration(t *testing.T) {
	socket := os.Getenv("WARPLOCAL_MCP_TEST_SOCKET")
	if socket == "" {
		t.Skip("requires Rust desktop IPC + isolated local SSH fixture")
	}
	root := os.Getenv("WARPLOCAL_MCP_TEST_DIR")
	binary := filepath.Join(t.TempDir(), "warp-local-adapter")
	build := exec.Command("go", "build", "-o", binary, "./cmd/server")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", output, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 210*time.Second)
	defer cancel()
	connect := func() *mcp.ClientSession {
		session, err := mcp.NewClient(&mcp.Implementation{Name: "integration", Version: "1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: exec.Command(binary, "mcp", "--socket", socket)}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	session := connect()
	defer func() { session.Close() }()
	owned := map[string]bool{}
	defer func() {
		for id := range owned {
			dir := workspacetools.ManagedBackgroundJobDir(id)
			if _, err := os.Stat(filepath.Join(dir, "exit")); os.IsNotExist(err) {
				cleanup := exec.Command("ssh", "-p", os.Getenv("WARPLOCAL_E2E_SSH_PORT"), "-i", os.Getenv("WARPLOCAL_E2E_SSH_KEY"), "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile="+os.Getenv("WARPLOCAL_E2E_SSH_KNOWN_HOSTS"), "127.0.0.1", workspacetools.ManagedBackgroundCancelCommand(id))
				_ = cleanup.Run()
			}
			_ = os.RemoveAll(dir) // Only UUIDs returned by this isolated local SSH fixture.
		}
	}()
	call := func(name string, args map[string]any, fail bool) map[string]any {
		t.Helper()
		r, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if r.IsError != fail {
			encoded, _ := json.Marshal(r)
			t.Fatalf("%s unexpected error=%v: %s", name, r.IsError, encoded)
		}
		bytes, _ := json.Marshal(r.StructuredContent)
		var result map[string]any
		_ = json.Unmarshal(bytes, &result)
		if name == "workspace_shell" {
			if id, ok := result["command_id"].(string); ok && workspacetools.ValidBackgroundCommandID(id) {
				owned[id] = true
			}
		}
		return result
	}
	shell := func(command, mode, dir string, fail bool) map[string]any {
		t.Helper()
		args := map[string]any{"server_id": "local-test", "request_id": uuid.NewString(), "command": command, "execution_mode": mode}
		if dir != "" {
			args["workdir"] = dir
		}
		return call("workspace_shell", args, fail)
	}
	t.Run("large stdout and stderr survive IPC buffers", func(t *testing.T) {
		r := shell("awk 'BEGIN {for(i=0;i<500;i++) print \"MCP_LARGE_abcdefghijklmnopqrstuvwxyz\"; print \"MCP_LARGE_END\"}'; awk 'BEGIN {for(i=0;i<500;i++) print \"MCP_ERR_abcdefghijklmnopqrstuvwxyz\"; print \"MCP_ERR_END\"}' >&2", "foreground", "", false)
		out, stderr := r["stdout"].(string), r["stderr"].(string)
		combined := out + stderr // The managed job may merge stderr into its output file.
		if strings.Count(combined, "MCP_LARGE_abcdefghijklmnopqrstuvwxyz") != 500 || !strings.Contains(combined, "MCP_LARGE_END") || strings.Count(combined, "MCP_ERR_abcdefghijklmnopqrstuvwxyz") != 500 || !strings.Contains(combined, "MCP_ERR_END") {
			t.Fatalf("truncated response: stdout=%d stderr=%d", len(out), len(stderr))
		}
	})
	t.Run("saved identity and directory", func(t *testing.T) {
		result := call("servers_list", map[string]any{}, false)
		servers := result["servers"].([]any)
		if len(servers) != 3 {
			t.Fatalf("expected every authorized server, including unsupported bastion: %v", servers)
		}
		for _, value := range servers {
			server := value.(map[string]any)
			if server["server_id"] == "interactive-test" && server["execution_supported"] != false {
				t.Fatal("interactive bastion must be listed as unsupported")
			}
		}
		bytes, _ := json.Marshal(result)
		if !strings.Contains(string(bytes), "username") || !strings.Contains(string(bytes), "saved project") || strings.Contains(string(bytes), "identity_file") {
			t.Fatalf("server context: %s", bytes)
		}
		r := shell("pwd; id -un", "foreground", "", false)
		if !strings.Contains(r["stdout"].(string), filepath.Join(root, "saved project")) {
			t.Fatal(r)
		}
		r = shell("pwd", "foreground", filepath.Join(root, "override"), false)
		if !strings.Contains(r["stdout"].(string), "override") {
			t.Fatal(r)
		}
		r = call("workspace_shell", map[string]any{"server_id": "local-home", "request_id": uuid.NewString(), "command": "pwd", "execution_mode": "foreground"}, false)
		home, _ := os.UserHomeDir()
		if strings.TrimSpace(r["stdout"].(string)) != home {
			t.Fatal(r)
		}
	})
	t.Run("bad directory never executes", func(t *testing.T) {
		marker := filepath.Join(root, "must-not-exist")
		shell("touch '"+marker+"'", "foreground", filepath.Join(root, "missing"), true)
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatal("command ran after failed cd")
		}
		shell("echo failure; exit 7", "foreground", "", true)
	})
	t.Run("retry and permission", func(t *testing.T) {
		args := map[string]any{"server_id": "local-test", "request_id": uuid.NewString(), "command": "echo once >> once.txt; cat once.txt", "execution_mode": "foreground"}
		first := call("workspace_shell", args, false)
		again := call("workspace_shell", args, false)
		if first["stdout"] != again["stdout"] {
			t.Fatal("duplicate executed")
		}
		args["command"] = "echo different"
		call("workspace_shell", args, true)
		args["request_id"] = uuid.NewString()
		args["server_id"] = "not-authorized"
		call("workspace_shell", args, true)
	})
	t.Run("background input polling reconnect cancellation later command", func(t *testing.T) {
		r := shell("read value; printf 'received=%s\\n' \"$value\"; sleep 20", "background", "", false)
		id := r["command_id"].(string)
		defer func() { call("process_cancel", map[string]any{"server_id": "local-test", "command_id": id}, false) }()
		call("process_write", map[string]any{"server_id": "local-test", "request_id": uuid.NewString(), "command_id": id, "input": "hello\n"}, false)
		session.Close()
		session = connect()
		for i := 0; i < 5; i++ {
			r = call("process_read", map[string]any{"server_id": "local-test", "command_id": id}, false)
			if strings.Contains(r["stdout"].(string), "received=hello") {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !strings.Contains(r["stdout"].(string), "received=hello") {
			t.Fatal(r)
		}
		call("process_cancel", map[string]any{"server_id": "local-test", "command_id": id}, false)
		shell("echo LATER_OK", "foreground", "", false)
	})
	t.Run("foreground observation read finish later command", func(t *testing.T) {
		r := shell("echo START; sleep 32; echo END", "foreground", "", false)
		if r["status"] != "running" {
			t.Fatal(r)
		}
		id := r["command_id"].(string)
		for i := 0; i < 15; i++ {
			r = call("process_read", map[string]any{"server_id": "local-test", "command_id": id}, false)
			if r["status"] == "exited" {
				break
			}
			time.Sleep(time.Second)
		}
		if r["status"] != "exited" || !strings.Contains(r["stdout"].(string), "END") {
			t.Fatal(r)
		}
		shell("echo NEXT_TURN_OK", "foreground", "", false)
	})
	t.Run("sftp upload download integrity and local path denial", func(t *testing.T) {
		source := filepath.Join(root, "source file")
		remote := filepath.Join(root, "remote file")
		dest := filepath.Join(root, "download file")
		if err := os.WriteFile(source, []byte("MCP SFTP 内容\n"), 0600); err != nil {
			t.Fatal(err)
		}
		args := map[string]any{"server_id": "local-test", "request_id": uuid.NewString(), "local_path": source, "remote_path": remote}
		call("sftp_upload", args, false)
		args["request_id"] = uuid.NewString()
		args["local_path"] = dest
		call("sftp_download", args, false)
		got, err := os.ReadFile(dest)
		if err != nil || string(got) != "MCP SFTP 内容\n" {
			t.Fatalf("integrity: %q %v", got, err)
		}
		args["request_id"] = uuid.NewString()
		args["local_path"] = "/etc/hosts"
		call("sftp_upload", args, true)
	})
}
