package memory

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// MemoryPatch is a set of updates from the extractor worker.
type MemoryPatch struct {
	Updates []MemoryUpdate `json:"updates"`
}

// MemoryUpdate describes one section-level change.
type MemoryUpdate struct {
	Path    string `json:"path"`
	Mode    string `json:"mode"`
	Section string `json:"section"`
	Content string `json:"content"`
}

var allowedModes = map[string]bool{
	"append_or_replace_section": true,
	"append_bullet":             true,
	"replace_file":              true,
}

var allowedMemoryPatchPaths = map[string]bool{
	"project_context.md":  true,
	"user_preferences.md": true,
	"workflows.md":        true,
	"known_issues.md":     true,
}

// secretPatterns match common secret formats.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`(?i)Authorization:\s*Bearer\s+\S+`),
	regexp.MustCompile(`(?i)api[_-]?key\s*[:=]\s*\S+`),
	regexp.MustCompile(`(?i)secret[_-]?key\s*[:=]\s*\S+`),
}

// ParseMemoryPatch parses raw JSON into a MemoryPatch.
func ParseMemoryPatch(data []byte) (MemoryPatch, error) {
	var p MemoryPatch
	if err := json.Unmarshal(data, &p); err != nil {
		return MemoryPatch{}, fmt.Errorf("patch: invalid JSON: %w", err)
	}
	return p, nil
}

// ValidateMemoryPatch checks all updates for safety.
func ValidateMemoryPatch(p MemoryPatch) error {
	for i, u := range p.Updates {
		if strings.Contains(u.Path, "..") {
			return fmt.Errorf("patch: path traversal in update[%d]: %s", i, u.Path)
		}
		if strings.HasPrefix(u.Path, "/") || strings.Contains(u.Path, "\\") {
			return fmt.Errorf("patch: invalid path in update[%d]: %s", i, u.Path)
		}
		if !strings.HasSuffix(u.Path, ".md") {
			return fmt.Errorf("patch: non-markdown path in update[%d]: %s", i, u.Path)
		}
		if !allowedMemoryPatchPaths[u.Path] {
			return fmt.Errorf("patch: path not allowed in update[%d]: %s", i, u.Path)
		}
		if !allowedModes[u.Mode] {
			return fmt.Errorf("patch: disallowed mode in update[%d]: %s", i, u.Mode)
		}
		if ContainsSecret(u.Content) {
			return fmt.Errorf("patch: secret detected in update[%d]", i)
		}
	}
	return nil
}

// ApplySectionPatch applies a single memory update to markdown content.
func ApplySectionPatch(markdown string, u MemoryUpdate) (string, error) {
	switch u.Mode {
	case "append_or_replace_section":
		sectionHeader := "# " + u.Section
		if strings.Contains(markdown, sectionHeader) {
			// Replace existing section.
			start := strings.Index(markdown, sectionHeader)
			if start < 0 {
				return markdown + "\n" + sectionHeader + "\n" + u.Content + "\n", nil
			}
			// Find the end of the section (next heading or EOF).
			afterHeader := markdown[start+len(sectionHeader):]
			nextHeading := findNextHeading(afterHeader)
			if nextHeading < 0 {
				return markdown[:start] + sectionHeader + "\n" + u.Content + "\n", nil
			}
			return markdown[:start] + sectionHeader + "\n" + u.Content + "\n" + afterHeader[nextHeading:], nil
		}
		// Section doesn't exist: append.
		return markdown + "\n" + sectionHeader + "\n" + u.Content + "\n", nil

	case "append_bullet":
		sectionHeader := "# " + u.Section
		if !strings.Contains(markdown, sectionHeader) {
			return markdown + "\n" + sectionHeader + "\n- " + u.Content + "\n", nil
		}
		idx := strings.Index(markdown, sectionHeader)
		afterHeader := markdown[idx+len(sectionHeader):]
		nextHeading := findNextHeading(afterHeader)
		var sectionBody string
		var afterSection string
		if nextHeading < 0 {
			sectionBody = afterHeader
		} else {
			sectionBody = afterHeader[:nextHeading]
			afterSection = afterHeader[nextHeading:]
		}
		sectionBody = strings.TrimRight(sectionBody, "\n") + "\n- " + u.Content + "\n"
		return markdown[:idx] + sectionHeader + sectionBody + afterSection, nil

	case "replace_file":
		return u.Content, nil

	default:
		return markdown, fmt.Errorf("patch: unsupported mode %s", u.Mode)
	}
}

// ContainsSecret checks whether content matches any known secret pattern.
func ContainsSecret(content string) bool {
	for _, p := range secretPatterns {
		if p.MatchString(content) {
			return true
		}
	}
	return false
}

// RedactSecrets removes known credential shapes before conversation evidence is
// persisted in the durable extraction queue.
func RedactSecrets(content string) string {
	redacted := content
	for _, pattern := range secretPatterns {
		redacted = pattern.ReplaceAllString(redacted, "[REDACTED]")
	}
	return redacted
}

// findNextHeading returns the index of the next markdown heading or -1.
func findNextHeading(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			rest := s[i+1:]
			if strings.HasPrefix(rest, "# ") {
				return i + 1
			}
		}
	}
	return -1
}
