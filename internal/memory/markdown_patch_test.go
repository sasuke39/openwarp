package memory

import (
	"strings"
	"testing"
)

func TestParseMemoryPatch_InvalidJSON(t *testing.T) {
	_, err := ParseMemoryPatch([]byte("{"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseMemoryPatch_Valid(t *testing.T) {
	raw := `{"updates":[{"path":"known_issues.md","mode":"append_or_replace_section","section":"Tool call pairing","content":"some content"}]}`
	p, err := ParseMemoryPatch([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(p.Updates))
	}
	if p.Updates[0].Path != "known_issues.md" {
		t.Errorf("expected known_issues.md, got %s", p.Updates[0].Path)
	}
}

func TestValidateMemoryPatch_PathTraversal(t *testing.T) {
	p := MemoryPatch{Updates: []MemoryUpdate{{Path: "../x.md", Mode: "replace_file"}}}
	if err := ValidateMemoryPatch(p); err == nil {
		t.Error("expected error for path traversal")
	}
}

func TestValidateMemoryPatch_NonMarkdown(t *testing.T) {
	p := MemoryPatch{Updates: []MemoryUpdate{{Path: "x.json", Mode: "replace_file"}}}
	if err := ValidateMemoryPatch(p); err == nil {
		t.Error("expected error for non-md path")
	}
}

func TestValidateMemoryPatch_AbsolutePath(t *testing.T) {
	p := MemoryPatch{Updates: []MemoryUpdate{{Path: "/tmp/workflows.md", Mode: "replace_file"}}}
	if err := ValidateMemoryPatch(p); err == nil {
		t.Error("expected error for absolute path")
	}
}

func TestValidateMemoryPatch_DisallowedMemoryFile(t *testing.T) {
	p := MemoryPatch{Updates: []MemoryUpdate{{Path: "random.md", Mode: "replace_file"}}}
	if err := ValidateMemoryPatch(p); err == nil {
		t.Error("expected error for disallowed memory file")
	}
}

func TestValidateMemoryPatch_Secret(t *testing.T) {
	p := MemoryPatch{Updates: []MemoryUpdate{{Path: "known_issues.md", Mode: "append_or_replace_section", Content: "api_key: sk-" + "abc123def456ghi789jkl012"}}}
	if err := ValidateMemoryPatch(p); err == nil {
		t.Error("expected error for secret")
	}
}

func TestValidateMemoryPatch_InvalidMode(t *testing.T) {
	p := MemoryPatch{Updates: []MemoryUpdate{{Path: "known_issues.md", Mode: "delete_everything", Section: "s", Content: "c"}}}
	if err := ValidateMemoryPatch(p); err == nil {
		t.Error("expected error for invalid mode")
	}
}

func TestValidateMemoryPatch_OK(t *testing.T) {
	p := MemoryPatch{Updates: []MemoryUpdate{{Path: "known_issues.md", Mode: "append_or_replace_section", Section: "s", Content: "safe content"}}}
	if err := ValidateMemoryPatch(p); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestApplySectionPatch_ReplaceSection(t *testing.T) {
	md := "# Title\n\nold content\n\n# Other\n\nother\n"
	u := MemoryUpdate{Mode: "append_or_replace_section", Section: "Title", Content: "new content"}
	result, err := ApplySectionPatch(md, u)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(result, "new content") {
		t.Errorf("expected 'new content' in result, got %q", result)
	}
	if contains(result, "old content") {
		t.Errorf("expected 'old content' to be replaced, got %q", result)
	}
}

func TestApplySectionPatch_AppendSection(t *testing.T) {
	md := "# Existing\n\ntext\n"
	u := MemoryUpdate{Mode: "append_or_replace_section", Section: "New", Content: "added"}
	result, err := ApplySectionPatch(md, u)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(result, "# New") {
		t.Errorf("expected new section heading, got %q", result)
	}
}

func TestApplySectionPatch_AppendBullet(t *testing.T) {
	md := "# Issues\n\n- existing bug\n"
	u := MemoryUpdate{Mode: "append_bullet", Section: "Issues", Content: "new bug"}
	result, err := ApplySectionPatch(md, u)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(result, "- new bug") {
		t.Errorf("expected new bullet, got %q", result)
	}
}

func TestApplySectionPatch_ReplaceFile(t *testing.T) {
	u := MemoryUpdate{Mode: "replace_file", Content: "entirely new"}
	result, err := ApplySectionPatch("old", u)
	if err != nil {
		t.Fatal(err)
	}
	if result != "entirely new" {
		t.Errorf("expected 'entirely new', got %q", result)
	}
}

func TestContainsSecret_True(t *testing.T) {
	if !ContainsSecret("api_key: sk-" + "abc123def456ghi789jkl012mno") {
		t.Error("expected secret detection")
	}
}

func TestContainsSecret_False(t *testing.T) {
	if ContainsSecret("just a normal line of text") {
		t.Error("expected no secret detection")
	}
}

func TestRedactSecrets(t *testing.T) {
	input := "api_key: sk-" + "abc123def456ghi789jkl012mno"
	got := RedactSecrets(input)
	if ContainsSecret(got) || !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("secret was not redacted: %q", got)
	}
}

func TestPreparedProjectWritesAreIdempotentAndComposeSameFileUpdates(t *testing.T) {
	store, err := NewStore(Config{BaseDir: t.TempDir()}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InitProjectMemory("project-a"); err != nil {
		t.Fatal(err)
	}
	patch := MemoryPatch{Updates: []MemoryUpdate{
		{Path: "workflows.md", Mode: "append_bullet", Section: "Build", Content: "run go build ./..."},
		{Path: "workflows.md", Mode: "append_bullet", Section: "Test", Content: "run go test ./..."},
	}}
	writes, err := store.PrepareProjectWrites("project-a", patch)
	if err != nil {
		t.Fatal(err)
	}
	if len(writes) != 1 {
		t.Fatalf("writes=%d, want one composed file", len(writes))
	}
	for i := 0; i < 2; i++ {
		if err := store.WritePreparedProject("project-a", writes); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.ReadFile("projects/project-a/memory/workflows.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"run go build ./...", "run go test ./..."} {
		if strings.Count(string(got), want) != 1 {
			t.Fatalf("content %q count != 1 in:\n%s", want, got)
		}
	}
}
