package memory

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MemoryHeader represents the frontmatter of a memory file.
type MemoryHeader struct {
	Path        string
	Name        string
	Description string
	Type        string
	UpdatedAt   time.Time
}

// SelectInput is the query context for memory selection.
type SelectInput struct {
	Query       string
	RecentTools []string
	Now         time.Time
	Limit       int
}

// SelectedMemory is a scored memory returned by selection.
type SelectedMemory struct {
	Header           MemoryHeader
	Score            int
	FreshnessWarning string
}

// DefaultMemoryIndex returns the default MEMORY.md content.
func DefaultMemoryIndex() string {
	return `# WarpLocal Memory Index
- [Project Context](project_context.md) - Architecture and scope.
- [User Preferences](user_preferences.md) - User-facing choices.
- [Workflows](workflows.md) - Build, test, docs, and packaging.
- [Known Issues](known_issues.md) - Repeated bugs and fixes.
`
}

// ParseMemoryHeader extracts frontmatter from a memory markdown file.
func ParseMemoryHeader(path string, content []byte) (MemoryHeader, error) {
	h := MemoryHeader{Path: path}
	if !bytes.HasPrefix(content, []byte("---\n")) {
		return h, fmt.Errorf("automemory: no frontmatter in %s", path)
	}
	end := bytes.Index(content[4:], []byte("\n---"))
	if end < 0 {
		return h, fmt.Errorf("automemory: unclosed frontmatter in %s", path)
	}
	fm := string(content[4 : end+4])
	for _, line := range strings.Split(fm, "\n") {
		kv := strings.SplitN(line, ":", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		switch key {
		case "name":
			h.Name = val
		case "description":
			h.Description = val
		case "type":
			h.Type = val
		case "updated_at":
			if t, err := time.Parse("2006-01-02", val); err == nil {
				h.UpdatedAt = t
			}
		}
	}
	if h.Name == "" {
		h.Name = filepath.Base(path)
	}
	return h, nil
}

// ScanMemoryHeaders reads all memory files in a directory and parses frontmatter.
func ScanMemoryHeaders(dir string) ([]MemoryHeader, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var headers []MemoryHeader
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if e.Name() == "MEMORY.md" {
			continue
		}
		fullPath := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}
		h, err := ParseMemoryHeader(e.Name(), data)
		if err != nil {
			continue
		}
		headers = append(headers, h)
	}
	return headers, nil
}

// ScoreMemory computes a relevance score for a memory header given a query.
func ScoreMemory(h MemoryHeader, in SelectInput) int {
	score := 0
	q := strings.ToLower(in.Query)
	if containsLower(h.Path, q) {
		score += 5
	}
	if containsLower(h.Name, q) {
		score += 3
	}
	if containsLower(h.Description, q) {
		score += 2
	}
	for _, tool := range in.RecentTools {
		tl := strings.ToLower(tool)
		if containsLower(h.Type, tl) {
			score += 2
		}
	}
	if strings.Contains(q, "build") || strings.Contains(q, "test") || strings.Contains(q, "deploy") {
		if h.Type == "workflow" {
			score += 3
		}
	}
	if strings.Contains(q, "error") || strings.Contains(q, "bug") || strings.Contains(q, "issue") {
		if h.Type == "issue" {
			score += 3
		}
	}
	if !h.UpdatedAt.IsZero() && in.Now.Sub(h.UpdatedAt).Hours() > 24*30 {
		score -= 1
	}
	return score
}

// SelectMemories returns the top-scored memories up to the limit.
func SelectMemories(headers []MemoryHeader, in SelectInput) []SelectedMemory {
	if in.Limit <= 0 {
		in.Limit = 5
	}
	var scored []SelectedMemory
	for _, h := range headers {
		s := ScoreMemory(h, in)
		var warning string
		if !h.UpdatedAt.IsZero() && in.Now.Sub(h.UpdatedAt).Hours() > 24*30 {
			warning = "memory may be stale"
		}
		scored = append(scored, SelectedMemory{Header: h, Score: s, FreshnessWarning: warning})
	}
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})
	if len(scored) > in.Limit {
		scored = scored[:in.Limit]
	}
	return scored
}

// ReadMemoryLines reads the first N lines from a file.
func ReadMemoryLines(path string, maxLines int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() && len(lines) < maxLines {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

func containsLower(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), sub)
}
