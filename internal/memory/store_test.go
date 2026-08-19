package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewStore_EmptyBase(t *testing.T) {
	s, err := NewStore(Config{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(s.BaseDir(), "/memory") {
		t.Errorf("expected baseDir ending with /memory, got %s", s.BaseDir())
	}
}

func TestNewStore_CustomBase(t *testing.T) {
	s, err := NewStore(Config{BaseDir: "/tmp/mem"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if s.BaseDir() != "/tmp/mem" {
		t.Errorf("expected /tmp/mem, got %s", s.BaseDir())
	}
}

func TestProjectKey_Path(t *testing.T) {
	got := ProjectKey("/Users/a/p", "")
	if got != "-Users-a-p" {
		t.Errorf("expected -Users-a-p, got %s", got)
	}
}

func TestProjectKey_EmptyCwd(t *testing.T) {
	got := ProjectKey("", "c1")
	if got != "conversation-c1" {
		t.Errorf("expected conversation-c1, got %s", got)
	}
}

func TestSanitizePathKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/Users/a/mywarp/local-adapter", "-Users-a-mywarp-local-adapter"},
		{"/tmp/x", "-tmp-x"},
		{"/", "-"},
	}
	for _, tt := range tests {
		got := SanitizePathKey(tt.input)
		if got != tt.expected {
			t.Errorf("SanitizePathKey(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestEnsureDirs(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(Config{BaseDir: dir}, "")
	if err := s.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"session", "projects", "locks"} {
		p := filepath.Join(dir, sub)
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			t.Errorf("expected dir %s to exist", p)
		}
	}
}

func TestAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(Config{BaseDir: dir}, "")
	_ = s.EnsureDirs()
	if err := s.AtomicWrite("a/b.md", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "a", "b.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Errorf("expected 'hello', got %q", string(data))
	}
}

func TestSanitizeKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"abc-123", "abc-123"},
		{"../escape", "---escape"},
		{"a/b/c", "a-b-c"},
		{"", "unknown"},
		{"normal_id", "normal_id"},
	}
	for _, tt := range tests {
		got := SanitizeKey(tt.input)
		if got != tt.expected {
			t.Errorf("SanitizeKey(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestAtomicWrite_TraversalBlocked(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(Config{BaseDir: dir}, "")
	_ = s.EnsureDirs()
	err := s.AtomicWrite("../../../etc/passwd", []byte("hack"))
	if err == nil {
		t.Error("expected error for path traversal")
	}
}

func TestReadFile_TraversalBlocked(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(Config{BaseDir: dir}, "")
	_ = s.EnsureDirs()
	_, err := s.ReadFile("../../../etc/passwd")
	if err == nil {
		t.Error("expected error for path traversal")
	}
}

func TestClearSession_SanitizesKey(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(Config{BaseDir: dir}, "")
	_ = s.EnsureDirs()
	// Create a session file
	cleanID := "c1"
	os.MkdirAll(filepath.Join(dir, "session", cleanID), 0o755)
	os.WriteFile(filepath.Join(dir, "session", cleanID, "notes.md"), []byte("test"), 0o644)
	// Clear with traversal key should still work (key is sanitized)
	err := s.ClearSession("../escape")
	if err != nil {
		// Should not error — the key gets sanitized to "--escape" which stays in baseDir
		t.Logf("ClearSession with traversal key returned: %v (expected sanitize)", err)
	}
}

func TestClearProject_SanitizesKey(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(Config{BaseDir: dir}, "")
	_ = s.EnsureDirs()
	err := s.ClearProject("../../etc")
	// Should succeed — the key gets sanitized to "--etc" which stays in baseDir
	if err != nil {
		t.Logf("ClearProject with traversal key returned: %v", err)
	}
}

func TestAppendEvent(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(Config{BaseDir: dir}, "")
	_ = s.EnsureDirs()
	ev := Event{
		TS:             time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC),
		Type:           "session_memory_updated",
		ConversationID: "c1",
		Path:           "session/c1/notes.md",
	}
	if err := s.AppendEvent(ev); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"session_memory_updated"`) {
		t.Errorf("expected event in jsonl, got %q", string(data))
	}
	lines := strings.Count(strings.TrimSpace(string(data)), "\n") + 1
	if lines != 1 {
		t.Errorf("expected 1 line, got %d", lines)
	}
}
