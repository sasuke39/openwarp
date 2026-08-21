package memory

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// SessionMeta tracks the last update cursor for session memory.
type SessionMeta struct {
	ConversationID    string    `json:"conversation_id"`
	ProjectKey        string    `json:"project_key"`
	LastMessageIndex  int       `json:"last_message_index"`
	LastHistoryChars  int       `json:"last_history_chars"`
	LastToolCallCount int       `json:"last_tool_call_count"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// SessionStats describes the current conversation state.
type SessionStats struct {
	MessageCount             int
	HistoryChars             int
	ToolCallCount            int
	LastAssistantHasToolCall bool
}

// requiredHeadings are the mandatory section headings in notes.md.
var requiredHeadings = []string{
	"Session Title",
	"Current State",
	"Task Specification",
	"Files And Functions",
	"Workflow",
	"Errors And Corrections",
	"Tool Results Worth Keeping",
	"Decisions",
	"Key Results",
	"Worklog",
}

// DefaultSessionNotes returns a template notes.md with all required headings.
func DefaultSessionNotes(title string) string {
	var sb strings.Builder
	for _, h := range requiredHeadings {
		if h == "Session Title" && title != "" {
			sb.WriteString("# " + h + "\n" + title + "\n\n")
		} else {
			sb.WriteString("# " + h + "\n\n")
		}
	}
	return sb.String()
}

// ValidateSessionNotes checks that notes contain all required headings.
func ValidateSessionNotes(markdown string) error {
	for _, h := range requiredHeadings {
		heading := "# " + h
		if !strings.Contains(markdown, heading) {
			return fmt.Errorf("session notes: missing heading %q", h)
		}
	}
	return nil
}

// ShouldUpdateSessionMemory decides whether to trigger a session memory update.
func ShouldUpdateSessionMemory(meta *SessionMeta, stats SessionStats) bool {
	if meta == nil {
		return stats.MessageCount >= 4
	}
	charsSince := stats.HistoryChars - meta.LastHistoryChars
	toolsSince := stats.ToolCallCount - meta.LastToolCallCount
	if charsSince < 12000 {
		return false
	}
	if toolsSince >= 3 {
		return true
	}
	if !stats.LastAssistantHasToolCall {
		return true
	}
	return false
}

// SessionNotesRelPath returns the relative path for session notes.
// The conversationID is sanitized to prevent path traversal.
func SessionNotesRelPath(conversationID string) string {
	safeID := SanitizeKey(conversationID)
	return filepath.Join("session", safeID, "notes.md")
}

// SessionMetaRelPath returns the relative path for session meta.
// The conversationID is sanitized to prevent path traversal.
func SessionMetaRelPath(conversationID string) string {
	safeID := SanitizeKey(conversationID)
	return filepath.Join("session", safeID, "meta.json")
}

// IsEmptySessionNotes returns true if notes contain only headings with no real content.
func IsEmptySessionNotes(notes string) bool {
	if notes == "" {
		return true
	}
	// Check if key content sections have any text between headings.
	contentSections := []string{"Files And Functions", "Decisions", "Worklog"}
	for _, section := range contentSections {
		header := "# " + section
		idx := strings.Index(notes, header)
		if idx < 0 {
			continue
		}
		afterHeader := notes[idx+len(header):]
		// Find next heading.
		nextIdx := strings.Index(afterHeader, "\n# ")
		var body string
		if nextIdx < 0 {
			body = afterHeader
		} else {
			body = afterHeader[:nextIdx]
		}
		body = strings.TrimSpace(body)
		if body != "" {
			return false
		}
	}
	return true
}

// ReadSessionMeta reads and parses session meta from the store.
func (s *Store) ReadSessionMeta(conversationID string) (*SessionMeta, error) {
	data, err := s.ReadFile(SessionMetaRelPath(conversationID))
	if err != nil {
		return nil, err
	}
	var meta SessionMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("session: cannot parse meta: %w", err)
	}
	return &meta, nil
}

// WriteSessionMeta writes session meta atomically.
func (s *Store) WriteSessionMeta(meta *SessionMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("session: cannot marshal meta: %w", err)
	}
	return s.AtomicWrite(SessionMetaRelPath(meta.ConversationID), data)
}

// ReadSessionNotes reads the session notes file.
func (s *Store) ReadSessionNotes(conversationID string) (string, error) {
	data, err := s.ReadFile(SessionNotesRelPath(conversationID))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WriteSessionNotes writes session notes after validation.
func (s *Store) WriteSessionNotes(conversationID string, notes string) error {
	if err := ValidateSessionNotes(notes); err != nil {
		return err
	}
	return s.AtomicWrite(SessionNotesRelPath(conversationID), []byte(notes))
}
