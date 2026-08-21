package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sasuke39/open-warp/internal/agentruntime"
)

func TestTranslateExternalToolCall(t *testing.T) {
	tests := []struct {
		name     string
		tool     string
		args     string
		wantTool string
		contains string
	}{
		{"bash", agentruntime.ToolWorkspaceShell, `{"command":"pwd","workdir":"/tmp/a b"}`, "run_shell_command", `/tmp/a b`},
		{"read", agentruntime.ToolWorkspaceReadFile, `{"file_path":"main.go","offset":5,"limit":10}`, "read_files", `"end":14`},
		{"write", agentruntime.ToolWorkspaceWriteFile, `{"file_path":"new.txt","content":"hello"}`, "apply_file_diffs", `"new_files"`},
		{"edit", agentruntime.ToolWorkspaceEditFile, `{"file_path":"main.go","old_string":"a","new_string":"b"}`, "apply_file_diffs", `"search":"a"`},
		{"glob", agentruntime.ToolWorkspaceGlob, `{"pattern":"**/*.go","path":"src"}`, "file_glob_v2", `"search_dir":"src"`},
		{"grep", agentruntime.ToolWorkspaceGrep, `{"pattern":"TODO","path":"."}`, "grep", `"queries":["TODO"]`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			translated, err := translateExternalToolCall(agentruntime.ToolCall{
				ID: "call-1", Name: test.tool, Arguments: json.RawMessage(test.args),
			})
			if err != nil {
				t.Fatal(err)
			}
			if translated.Name != test.wantTool {
				t.Fatalf("tool = %q, want %q", translated.Name, test.wantTool)
			}
			if !strings.Contains(string(translated.Args), test.contains) {
				t.Fatalf("args %s do not contain %s", translated.Args, test.contains)
			}
		})
	}
}

func TestTranslateExternalToolCallRejectsUnknownTool(t *testing.T) {
	_, err := translateExternalToolCall(agentruntime.ToolCall{ID: "call-1", Name: "unknown", Arguments: json.RawMessage(`{}`)})
	if err == nil {
		t.Fatal("expected unknown external tool to be rejected")
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("a'b"); got != `'a'"'"'b'` {
		t.Fatalf("shellQuote = %q", got)
	}
}
