package memory

import (
	"fmt"
	"os/exec"
	"strings"
)

// ProjectKeyFromRoot computes a project key from a workspace root directory.
// It prefers git root if inside a git repo, otherwise uses the root as-is.
func ProjectKeyFromRoot(root string) string {
	if root == "" {
		return "unknown"
	}
	// Try to resolve git root.
	if gitRoot, err := gitRoot(root); err == nil && gitRoot != "" {
		return SanitizePathKey(gitRoot)
	}
	return SanitizePathKey(root)
}

// gitRoot returns the top-level git directory for the given path.
func gitRoot(dir string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not a git repo: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ShortID returns a safe abbreviated ID that won't panic on short strings.
func ShortID(id string) string {
	if id == "" {
		return "unknown"
	}
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
