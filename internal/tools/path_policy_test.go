package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultPathPolicy(t *testing.T) {
	p := DefaultPathPolicy("/workspace", "memory-design")
	if p.WorkspaceRoot != "/workspace" {
		t.Errorf("expected /workspace, got %s", p.WorkspaceRoot)
	}
	if len(p.Deny) == 0 {
		t.Error("expected deny patterns")
	}
}

func TestCanWrite_TopicDoc(t *testing.T) {
	p := DefaultPathPolicy("/workspace", "memory-design")
	if d := CanWrite("记忆系统设计方案/a.md", p); d != Allow {
		t.Errorf("expected allow for topic doc, got %s", d)
	}
}

func TestCanWrite_Git(t *testing.T) {
	p := DefaultPathPolicy("/workspace", "memory-design")
	if d := CanWrite(".git/config", p); d != Deny {
		t.Errorf("expected deny for .git, got %s", d)
	}
}

func TestCanWrite_NodeModules(t *testing.T) {
	p := DefaultPathPolicy("/workspace", "memory-design")
	if d := CanWrite("node_modules/a", p); d != Deny {
		t.Errorf("expected deny for node_modules, got %s", d)
	}
}

func TestCanWrite_Config(t *testing.T) {
	p := DefaultPathPolicy("/workspace", "memory-design")
	if d := CanWrite("config.yaml", p); d != Deny {
		t.Errorf("expected deny for config.yaml, got %s", d)
	}
}

func TestCanWrite_Upstream(t *testing.T) {
	p := DefaultPathPolicy("/workspace", "memory-design")
	if d := CanWrite("warp-v0.2026.04.29.08.56.stable_00-src/a", p); d != Confirm {
		t.Errorf("expected confirm for upstream source, got %s", d)
	}
}

func TestCanWrite_UnknownRoot(t *testing.T) {
	p := DefaultPathPolicy("/workspace", "memory-design")
	if d := CanWrite("new.md", p); d != Confirm {
		t.Errorf("expected confirm for unknown root file, got %s", d)
	}
}

func TestCanWrite_ConversationsJson(t *testing.T) {
	p := DefaultPathPolicy("/workspace", "memory-design")
	if d := CanWrite("conversations.json", p); d != Deny {
		t.Errorf("expected deny for conversations.json, got %s", d)
	}
}

func TestIsMarkdownLineLimitOK(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, err := IsMarkdownLineLimitOK(path, 10)
	if err != nil || !ok {
		t.Errorf("expected ok for 3 lines with limit 10, got ok=%v err=%v", ok, err)
	}
	ok, err = IsMarkdownLineLimitOK(path, 2)
	if err != nil || ok {
		t.Errorf("expected not ok for 3 lines with limit 2, got ok=%v err=%v", ok, err)
	}
}

func TestIsMarkdownLineLimitOK_NonMarkdown(t *testing.T) {
	ok, err := IsMarkdownLineLimitOK("test.go", 10)
	if err != nil || !ok {
		t.Errorf("non-markdown should always be ok, got ok=%v err=%v", ok, err)
	}
}

func TestIsMarkdownLineLimitOK_NotExists(t *testing.T) {
	ok, err := IsMarkdownLineLimitOK("/nonexistent/file.md", 10)
	if err != nil || !ok {
		t.Errorf("nonexistent file should be ok, got ok=%v err=%v", ok, err)
	}
}
