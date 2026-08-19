package tools

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Decision represents a path policy ruling.
type Decision string

const (
	Allow   Decision = "allow"
	Deny    Decision = "deny"
	Confirm Decision = "confirm"
)

// PathPolicy controls which files can be written.
type PathPolicy struct {
	WorkspaceRoot   string
	TaskScope       string
	Allow           []string
	Deny            []string
	RequireConfirm  []string
}

// DefaultPathPolicy returns a standard policy for the given workspace.
func DefaultPathPolicy(workspaceRoot, taskScope string) PathPolicy {
	return PathPolicy{
		WorkspaceRoot: workspaceRoot,
		TaskScope:     taskScope,
		Allow: []string{
			"记忆系统设计方案/**",
			"记忆系统设计方案.md",
		},
		Deny: []string{
			"**/.git/**",
			"**/node_modules/**",
			"**/.gocache/**",
			"**/.gotmp/**",
			"**/config.yaml",
			"**/conversations.json",
		},
		RequireConfirm: []string{
			"warp-v0.*-src/**",
			"warp-*-src/**",
		},
	}
}

// CanWrite determines whether a path can be written.
func CanWrite(path string, policy PathPolicy) Decision {
	abs := cleanPath(path, policy.WorkspaceRoot)
	rel := relPath(abs, policy.WorkspaceRoot)

	for _, pattern := range policy.Deny {
		if matchGlob(rel, pattern) {
			return Deny
		}
	}
	for _, pattern := range policy.Allow {
		if matchGlob(rel, pattern) {
			return Allow
		}
	}
	for _, pattern := range policy.RequireConfirm {
		if matchGlob(rel, pattern) {
			return Confirm
		}
	}
	return Confirm
}

// IsMarkdownLineLimitOK checks if a markdown file is within the line limit.
func IsMarkdownLineLimitOK(path string, maxLines int) (bool, error) {
	if !strings.HasSuffix(path, ".md") {
		return true, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	defer f.Close()
	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		count++
		if count > maxLines {
			return false, nil
		}
	}
	return true, scanner.Err()
}

func cleanPath(path, root string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	if root != "" {
		return filepath.Join(root, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

func relPath(abs, root string) string {
	if root == "" {
		return abs
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return abs
	}
	return rel
}

func matchGlob(path, pattern string) bool {
	// filepath.Match doesn't support **, so we handle it.
	if strings.Contains(pattern, "**") {
		return matchDoublestar(path, pattern)
	}
	matched, _ := filepath.Match(pattern, path)
	return matched
}

func matchDoublestar(path, pattern string) bool {
	// Split pattern on ** and match segment by segment.
	parts := strings.SplitN(pattern, "**", 2)
	if len(parts) != 2 {
		return false
	}
	prefix := parts[0]
	suffix := strings.TrimPrefix(parts[1], "/")

	// prefix must match the start of path.
	if prefix != "" {
		pathPrefix := path
		if len(path) > len(prefix) {
			pathPrefix = path[:len(prefix)]
		}
		if pathPrefix != prefix && pathPrefix != prefix[:len(prefix)-1] {
			// Also try matching the directory portion
			if !strings.HasPrefix(path, strings.TrimSuffix(prefix, "/")) {
				return false
			}
		}
	}

	// suffix must match the end.
	if suffix == "" {
		return true
	}
	matched, _ := filepath.Match(suffix, filepath.Base(path))
	if matched {
		return true
	}
	// Try matching suffix against trailing path segments.
	pathParts := strings.Split(path, "/")
	for i := len(pathParts) - 1; i >= 0; i-- {
		trial := strings.Join(pathParts[i:], "/")
		if m, _ := filepath.Match(suffix, trial); m {
			return true
		}
	}
	return false
}
