package memory

import (
	"fmt"
	"strings"
	"time"
)

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
	if err := ValidateMemoryPatch(p); err != nil {
		return err
	}
	for _, u := range p.Updates {
		rel := "projects/" + projectKey + "/memory/" + u.Path
		existing, err := s.ReadFile(rel)
		if err != nil {
			// File doesn't exist yet; create with section content.
			existing = []byte("# " + u.Section + "\n\n" + u.Content + "\n")
		}
		updated, err := ApplySectionPatch(string(existing), u)
		if err != nil {
			return fmt.Errorf("cannot apply patch to %s: %w", u.Path, err)
		}
		// Check line limit.
		lines := strings.Count(updated, "\n") + 1
		if lines > 70 {
			return fmt.Errorf("result for %s exceeds 70 lines (%d); split into sub-documents", u.Path, lines)
		}
		if err := s.AtomicWrite(rel, []byte(updated)); err != nil {
			return fmt.Errorf("cannot write %s: %w", u.Path, err)
		}
	}
	return nil
}
