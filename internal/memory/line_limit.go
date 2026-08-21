package memory

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// PreparedProjectWrite is a complete file image produced before committing a
// project-memory job. Reapplying it is safe after a crash because it overwrites
// the same file content instead of replaying append operations.
type PreparedProjectWrite struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// DefaultProjectMemoryFiles returns the initial memory files for a new project.
func DefaultProjectMemoryFiles(now time.Time) map[string]string {
	dateStr := now.Format("2006-01-02")
	return map[string]string{
		"MEMORY.md": DefaultMemoryIndex(),
		"project_context.md": fmt.Sprintf(`---
name: Project Context
description: Architecture, scope, and key constraints of this project.
type: project
updated_at: %s
---

# Project Context

`, dateStr),
		"user_preferences.md": fmt.Sprintf(`---
name: User Preferences
description: User-facing choices and style preferences for this project.
type: preference
updated_at: %s
---

# User Preferences

`, dateStr),
		"workflows.md": fmt.Sprintf(`---
name: Workflows
description: Build, test, run, and verification commands for this project.
type: workflow
updated_at: %s
---

# Workflows

`, dateStr),
		"known_issues.md": fmt.Sprintf(`---
name: Known Issues
description: Repeated bugs, workarounds, and pitfalls encountered in this project.
type: issue
updated_at: %s
---

# Known Issues

`, dateStr),
	}
}

// InitProjectMemory creates the memory directory and default files for a project.
func (s *Store) InitProjectMemory(projectKey string) error {
	memDir := "projects/" + projectKey + "/memory"
	defaults := DefaultProjectMemoryFiles(time.Now())
	for name, content := range defaults {
		rel := memDir + "/" + name
		// Don't overwrite existing files.
		if _, err := s.ReadFile(rel); err == nil {
			continue
		}
		if err := s.AtomicWrite(rel, []byte(content)); err != nil {
			return fmt.Errorf("cannot init %s: %w", name, err)
		}
	}
	return nil
}

// ApplyAndWritePatch validates and applies a MemoryPatch to project memory files.
func (s *Store) ApplyAndWritePatch(projectKey string, p MemoryPatch) error {
	writes, err := s.PrepareProjectWrites(projectKey, p)
	if err != nil {
		return err
	}
	return s.WritePreparedProject(projectKey, writes)
}

// PrepareProjectWrites applies every patch in memory and returns a deterministic
// complete-file write set. Multiple updates to one file are composed in order.
func (s *Store) PrepareProjectWrites(projectKey string, p MemoryPatch) ([]PreparedProjectWrite, error) {
	if err := ValidateMemoryPatch(p); err != nil {
		return nil, err
	}
	contents := make(map[string]string)
	for _, u := range p.Updates {
		existing, ok := contents[u.Path]
		if !ok {
			rel := "projects/" + projectKey + "/memory/" + u.Path
			data, err := s.ReadFile(rel)
			if err != nil {
				existing = "# " + u.Section + "\n\n"
			} else {
				existing = string(data)
			}
		}
		updated, err := ApplySectionPatch(existing, u)
		if err != nil {
			return nil, fmt.Errorf("cannot apply patch to %s: %w", u.Path, err)
		}
		lines := strings.Count(updated, "\n") + 1
		if lines > 70 {
			return nil, fmt.Errorf("result for %s exceeds 70 lines (%d); split into sub-documents", u.Path, lines)
		}
		contents[u.Path] = updated
	}
	paths := make([]string, 0, len(contents))
	for path := range contents {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	writes := make([]PreparedProjectWrite, 0, len(paths))
	for _, path := range paths {
		writes = append(writes, PreparedProjectWrite{Path: path, Content: contents[path]})
	}
	return writes, nil
}

// WritePreparedProject validates and atomically overwrites a prepared write set.
func (s *Store) WritePreparedProject(projectKey string, writes []PreparedProjectWrite) error {
	safeKey := SanitizeKey(projectKey)
	if safeKey != projectKey {
		return fmt.Errorf("invalid project key %q", projectKey)
	}
	for _, write := range writes {
		if !allowedMemoryPatchPaths[write.Path] || ContainsSecret(write.Content) {
			return fmt.Errorf("invalid prepared project write %q", write.Path)
		}
		if lines := strings.Count(write.Content, "\n") + 1; lines > 70 {
			return fmt.Errorf("result for %s exceeds 70 lines (%d)", write.Path, lines)
		}
		rel := "projects/" + projectKey + "/memory/" + write.Path
		if err := s.AtomicWrite(rel, []byte(write.Content)); err != nil {
			return fmt.Errorf("cannot write %s: %w", write.Path, err)
		}
	}
	return nil
}
