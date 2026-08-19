package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultMemoryIndex(t *testing.T) {
	idx := DefaultMemoryIndex()
	if !containsLower(idx, "memory index") {
		t.Error("expected 'Memory Index' in default index")
	}
}

func TestParseMemoryHeader_Valid(t *testing.T) {
	content := []byte("---\nname: WarpLocal workflows\ndescription: Build and test workflows.\ntype: workflow\nupdated_at: 2026-05-13\n---\n\n# Workflows\n")
	h, err := ParseMemoryHeader("workflows.md", content)
	if err != nil {
		t.Fatal(err)
	}
	if h.Name != "WarpLocal workflows" {
		t.Errorf("expected 'WarpLocal workflows', got %q", h.Name)
	}
	if h.Type != "workflow" {
		t.Errorf("expected 'workflow', got %q", h.Type)
	}
}

func TestParseMemoryHeader_NoFrontmatter(t *testing.T) {
	content := []byte("# Just a file\nno frontmatter\n")
	_, err := ParseMemoryHeader("test.md", content)
	if err == nil {
		t.Error("expected error for missing frontmatter")
	}
}

func TestScoreMemory_QueryWorkflow(t *testing.T) {
	h := MemoryHeader{Name: "Workflows", Type: "workflow", Description: "Build, test, packaging"}
	in := SelectInput{Query: "build app", Now: time.Now()}
	score := ScoreMemory(h, in)
	if score < 3 {
		t.Errorf("expected high score for workflow query, got %d", score)
	}
}

func TestScoreMemory_QueryIssue(t *testing.T) {
	h := MemoryHeader{Name: "Known Issues", Type: "issue", Description: "Repeated bugs"}
	in := SelectInput{Query: "tool repeats error", Now: time.Now()}
	score := ScoreMemory(h, in)
	if score < 3 {
		t.Errorf("expected high score for issue query, got %d", score)
	}
}

func TestSelectMemories_Limit(t *testing.T) {
	headers := make([]MemoryHeader, 10)
	for i := range headers {
		headers[i] = MemoryHeader{Name: "mem", Type: "project", Description: "desc"}
	}
	in := SelectInput{Query: "test", Limit: 5, Now: time.Now()}
	selected := SelectMemories(headers, in)
	if len(selected) != 5 {
		t.Errorf("expected 5, got %d", len(selected))
	}
}

func TestSelectMemories_Stale(t *testing.T) {
	h := MemoryHeader{
		Name:      "Old",
		Type:      "project",
		UpdatedAt: time.Now().Add(-40 * 24 * time.Hour),
	}
	in := SelectInput{Query: "something", Limit: 5, Now: time.Now()}
	selected := SelectMemories([]MemoryHeader{h}, in)
	if len(selected) == 0 {
		t.Fatal("expected 1 result")
	}
	if selected[0].FreshnessWarning == "" {
		t.Error("expected freshness warning for stale memory")
	}
}

func TestScanMemoryHeaders(t *testing.T) {
	dir := t.TempDir()
	content := []byte("---\nname: Test\ndescription: A test memory\ntype: project\nupdated_at: 2026-05-13\n---\n\n# Test\n")
	if err := os.WriteFile(filepath.Join(dir, "test.md"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	headers, err := ScanMemoryHeaders(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(headers) != 1 {
		t.Errorf("expected 1 header, got %d", len(headers))
	}
}

func TestScanMemoryHeaders_SkipsMemoryIndex(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("# Index\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	headers, err := ScanMemoryHeaders(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(headers) != 0 {
		t.Errorf("MEMORY.md should be skipped, got %d", len(headers))
	}
}
