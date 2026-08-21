package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultSessionNotes(t *testing.T) {
	notes := DefaultSessionNotes("My Task")
	if err := ValidateSessionNotes(notes); err != nil {
		t.Fatalf("default notes should be valid: %v", err)
	}
	if !contains(notes, "My Task") {
		t.Error("expected title in notes")
	}
}

func TestValidateSessionNotes_MissingHeading(t *testing.T) {
	notes := "# Session Title\n\n# Current State\n\n"
	if err := ValidateSessionNotes(notes); err == nil {
		t.Error("expected error for missing headings")
	}
}

func TestShouldUpdateSessionMemory_FirstCreate(t *testing.T) {
	if !ShouldUpdateSessionMemory(nil, SessionStats{MessageCount: 4}) {
		t.Error("should update when nil meta and >=4 messages")
	}
}

func TestShouldUpdateSessionMemory_TooEarly(t *testing.T) {
	if ShouldUpdateSessionMemory(nil, SessionStats{MessageCount: 3}) {
		t.Error("should not update when nil meta and <4 messages")
	}
}

func TestShouldUpdateSessionMemory_EnoughCharsAndTools(t *testing.T) {
	meta := &SessionMeta{LastHistoryChars: 0, LastToolCallCount: 0}
	stats := SessionStats{
		HistoryChars:             12000,
		ToolCallCount:            3,
		LastAssistantHasToolCall: true,
	}
	if !ShouldUpdateSessionMemory(meta, stats) {
		t.Error("should update when 12000 chars and 3 tools since last")
	}
}

func TestShouldUpdateSessionMemory_NaturalBreakpoint(t *testing.T) {
	meta := &SessionMeta{LastHistoryChars: 0, LastToolCallCount: 0}
	stats := SessionStats{
		HistoryChars:             12000,
		ToolCallCount:            0,
		LastAssistantHasToolCall: false,
	}
	if !ShouldUpdateSessionMemory(meta, stats) {
		t.Error("should update at natural breakpoint (no last tool call)")
	}
}

func TestShouldUpdateSessionMemory_WaitingForTool(t *testing.T) {
	meta := &SessionMeta{LastHistoryChars: 0, LastToolCallCount: 0}
	stats := SessionStats{
		HistoryChars:             12000,
		ToolCallCount:            1,
		LastAssistantHasToolCall: true,
	}
	if ShouldUpdateSessionMemory(meta, stats) {
		t.Error("should not update while waiting for tool result")
	}
}

func TestSessionNotesRelPath(t *testing.T) {
	got := SessionNotesRelPath("abc")
	if got != "session/abc/notes.md" {
		t.Errorf("expected session/abc/notes.md, got %s", got)
	}
}

func TestSessionMetaRelPath(t *testing.T) {
	got := SessionMetaRelPath("abc")
	if got != "session/abc/meta.json" {
		t.Errorf("expected session/abc/meta.json, got %s", got)
	}
}

func TestWriteAndReadSessionMeta(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(Config{BaseDir: dir}, "")
	_ = s.EnsureDirs()
	meta := &SessionMeta{
		ConversationID:    "c1",
		ProjectKey:        "-Users-a-p",
		LastMessageIndex:  10,
		LastHistoryChars:  24000,
		LastToolCallCount: 3,
		UpdatedAt:         time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC),
	}
	if err := s.WriteSessionMeta(meta); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadSessionMeta("c1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ConversationID != "c1" || got.LastHistoryChars != 24000 {
		t.Errorf("unexpected meta: %+v", got)
	}
}

func TestWriteAndReadSessionNotes(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(Config{BaseDir: dir}, "")
	_ = s.EnsureDirs()
	notes := DefaultSessionNotes("Test")
	if err := s.WriteSessionNotes("c1", notes); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadSessionNotes("c1")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSessionNotes(got); err != nil {
		t.Errorf("read notes should be valid: %v", err)
	}
}

func TestWriteSessionNotes_Invalid(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(Config{BaseDir: dir}, "")
	_ = s.EnsureDirs()
	err := s.WriteSessionNotes("c1", "no headings")
	if err == nil {
		t.Error("expected error for invalid notes")
	}
}

func TestReadSessionMeta_NotFound(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(Config{BaseDir: dir}, "")
	_ = s.EnsureDirs()
	// Ensure dir exists but file doesn't
	os.MkdirAll(filepath.Join(dir, "session", "c1"), 0o755)
	_, err := s.ReadSessionMeta("c1")
	if err == nil {
		t.Error("expected error for missing meta")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(len(s) > 0 && len(sub) > 0 && findSubstr(s, sub)))
}

func findSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestIsEmptySessionNotes_Empty(t *testing.T) {
	notes := DefaultSessionNotes("Test")
	if !IsEmptySessionNotes(notes) {
		t.Error("default template should be empty")
	}
}

func TestIsEmptySessionNotes_WithContent(t *testing.T) {
	notes := DefaultSessionNotes("Test")
	// Insert content under the "Files And Functions" heading.
	notes = strings.Replace(notes, "# Files And Functions\n\n", "# Files And Functions\n- main.go: entry point\n\n", 1)
	if IsEmptySessionNotes(notes) {
		t.Error("notes with content should not be empty")
	}
}

func TestIsEmptySessionNotes_EmptyString(t *testing.T) {
	if !IsEmptySessionNotes("") {
		t.Error("empty string should be empty notes")
	}
}
