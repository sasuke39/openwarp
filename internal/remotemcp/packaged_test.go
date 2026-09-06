package remotemcp

import (
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"os"
	"os/exec"
	"testing"
	"time"
)

// Read-only smoke against a running packaged App. Never invokes shell/SFTP.
func TestPackagedServerList(t *testing.T) {
	binary, socket := os.Getenv("WARPLOCAL_PACKAGED_MCP_BIN"), os.Getenv("WARPLOCAL_PACKAGED_MCP_SOCKET")
	if binary == "" || socket == "" {
		t.Skip("requires running packaged App")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "packaged-list-smoke", Version: "1"}, nil).Connect(ctx,
		&mcp.CommandTransport{Command: exec.Command(binary, "mcp", "--socket", socket, "--client-id", "packaged-list-smoke")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 7 {
		t.Fatalf("tools discovery: %v %v", tools, err)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "servers_list", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	if result.IsError {
		t.Fatalf("servers_list error: %s", raw)
	}
	data, _ := json.Marshal(result.StructuredContent)
	var got struct {
		Servers []struct {
			ID        string `json:"server_id"`
			Name      string `json:"name"`
			Username  string `json:"username"`
			Directory string `json:"default_workdir"`
			Supported bool   `json:"execution_supported"`
		}
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	profiles, err := os.ReadFile(os.Getenv("WARPLOCAL_PACKAGED_SSH_PROFILES"))
	if err != nil {
		t.Fatal(err)
	}
	var expected struct {
		Profiles []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Username    string `json:"username"`
			Directory   string `json:"post_connect_directory"`
			Interactive bool   `json:"interactive_bastion"`
		}
	}
	if err := json.Unmarshal(profiles, &expected); err != nil {
		t.Fatal("invalid saved profiles")
	}
	if len(got.Servers) != len(expected.Profiles) {
		t.Fatalf("listed %d, saved %d", len(got.Servers), len(expected.Profiles))
	}
	for _, p := range expected.Profiles {
		found := false
		for _, s := range got.Servers {
			if s.ID == p.ID {
				found = true
				if s.Name != p.Name || s.Username != p.Username || s.Directory != p.Directory || s.Supported == p.Interactive {
					t.Fatalf("metadata mismatch for %s", p.ID)
				}
			}
		}
		if !found {
			t.Fatalf("missing server %s", p.ID)
		}
	}
	t.Logf("Actual MCP servers_list: %s", data)
}
