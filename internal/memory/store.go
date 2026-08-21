package memory

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Config holds memory store configuration.
type Config struct {
	BaseDir          string
	StaleAfterHours  int
	SessionEnabled   bool
	AutoEnabled      bool
	MaxProjectMemory int
}

// Store manages the memory filesystem.
type Store struct {
	baseDir string
	muMap   sync.Map // target path → *sync.Mutex for per-file write locking
}

// Event represents an entry in the memory event log.
type Event struct {
	TS             time.Time `json:"ts"`
	Type           string    `json:"type"`
	ProjectKey     string    `json:"project_key,omitempty"`
	ConversationID string    `json:"conversation_id,omitempty"`
	Path           string    `json:"path,omitempty"`
}

// NewStore creates a Store. If cfg.BaseDir is empty, it derives from configPath.
func NewStore(cfg Config, configPath string) (*Store, error) {
	baseDir := cfg.BaseDir
	if baseDir == "" {
		if configPath == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("memory: cannot determine home dir: %w", err)
			}
			baseDir = filepath.Join(home, "Library", "Application Support", "WarpLocal", "memory")
		} else {
			baseDir = filepath.Join(filepath.Dir(configPath), "memory")
		}
	}
	return &Store{baseDir: baseDir}, nil
}

// BaseDir returns the memory root directory.
func (s *Store) BaseDir() string {
	return s.baseDir
}

// EnsureDirs creates the memory directory tree.
func (s *Store) EnsureDirs() error {
	dirs := []string{
		s.baseDir,
		filepath.Join(s.baseDir, "session"),
		filepath.Join(s.baseDir, "projects"),
		filepath.Join(s.baseDir, "locks"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("memory: cannot create dir %s: %w", dir, err)
		}
	}
	return nil
}

// ProjectKey computes a stable key from cwd and conversationID.
func ProjectKey(cwd, conversationID string) string {
	if cwd != "" {
		return SanitizePathKey(cwd)
	}
	if conversationID != "" {
		return "conversation-" + conversationID
	}
	return "unknown"
}

// SanitizePathKey converts a filesystem path into a safe directory name.
func SanitizePathKey(path string) string {
	p := filepath.Clean(path)
	if vol := filepath.VolumeName(p); vol != "" {
		p = p[len(vol):]
	}
	p = strings.ReplaceAll(p, string(filepath.Separator), "-")
	// Request roots can contain spaces, dots, or platform-specific separators;
	// normalize the final key to the same alphabet accepted by all memory APIs.
	p = strings.ReplaceAll(p, "\\", "-")
	if p == "" {
		return "root"
	}
	return SanitizeKey(p)
}

// SanitizeKey sanitizes a user-supplied key (conversation_id or project_key)
// so it cannot contain path traversal sequences.
func SanitizeKey(key string) string {
	s := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, key)
	if s == "" {
		s = "unknown"
	}
	return s
}

// isWithinBaseDir checks that a resolved absolute path is within baseDir.
func (s *Store) isWithinBaseDir(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("memory: cannot resolve path: %w", err)
	}
	baseAbs, err := filepath.Abs(s.baseDir)
	if err != nil {
		return fmt.Errorf("memory: cannot resolve baseDir: %w", err)
	}
	if !strings.HasPrefix(abs+string(filepath.Separator), baseAbs+string(filepath.Separator)) && abs != baseAbs {
		return fmt.Errorf("memory: path %s escapes baseDir %s", path, s.baseDir)
	}
	return nil
}

// fileLock returns a per-target mutex for coordinating concurrent writes.
func (s *Store) fileLock(target string) *sync.Mutex {
	val, _ := s.muMap.LoadOrStore(target, &sync.Mutex{})
	return val.(*sync.Mutex)
}

// AtomicWrite writes data to rel path under baseDir atomically.
func (s *Store) AtomicWrite(rel string, data []byte) error {
	target := filepath.Join(s.baseDir, rel)
	if err := s.isWithinBaseDir(target); err != nil {
		return err
	}
	mu := s.fileLock(target)
	mu.Lock()
	defer mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("memory: cannot create parent dir: %w", err)
	}
	// Use unique tmp file to avoid collisions between concurrent writes to different targets.
	suffix, _ := randomHex(8)
	tmp := target + ".tmp." + suffix
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("memory: cannot create tmp file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("memory: cannot write tmp file: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("memory: cannot sync tmp file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("memory: cannot close tmp file: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("memory: cannot rename tmp to target: %w", err)
	}
	if runtime.GOOS != "windows" {
		if dir, err := os.Open(filepath.Dir(target)); err == nil {
			err = dir.Sync()
			_ = dir.Close()
			if err != nil {
				return fmt.Errorf("memory: cannot sync parent directory: %w", err)
			}
		}
	}
	return nil
}

// ReadFile reads a file relative to baseDir.
func (s *Store) ReadFile(rel string) ([]byte, error) {
	target := filepath.Join(s.baseDir, rel)
	if err := s.isWithinBaseDir(target); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return nil, fmt.Errorf("memory: cannot read %s: %w", rel, err)
	}
	return data, nil
}

// ClearSession removes session notes and meta for a conversation.
// The conversationID is sanitized and the resolved path is validated
// to stay within baseDir/session.
func (s *Store) ClearSession(conversationID string) error {
	safeID := SanitizeKey(conversationID)
	notesPath := filepath.Join(s.baseDir, "session", safeID, "notes.md")
	metaPath := filepath.Join(s.baseDir, "session", safeID, "meta.json")
	for _, p := range []string{notesPath, metaPath} {
		if err := s.isWithinBaseDir(p); err != nil {
			return err
		}
	}
	if err := os.Remove(notesPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("memory: cannot remove session notes: %w", err)
	}
	if err := os.Remove(metaPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("memory: cannot remove session meta: %w", err)
	}
	return nil
}

// ClearProject removes all memory files for a project.
// The projectKey is sanitized and the resolved path is validated
// to stay within baseDir/projects.
func (s *Store) ClearProject(projectKey string) error {
	safeKey := SanitizeKey(projectKey)
	projectDir := filepath.Join(s.baseDir, "projects", safeKey)
	if err := s.isWithinBaseDir(projectDir); err != nil {
		return err
	}
	if err := os.RemoveAll(projectDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("memory: cannot remove project dir: %w", err)
	}
	return nil
}

// AppendEvent appends one JSON line to events.jsonl.
func (s *Store) AppendEvent(e Event) error {
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("memory: cannot marshal event: %w", err)
	}
	eventsPath := filepath.Join(s.baseDir, "events.jsonl")
	f, err := os.OpenFile(eventsPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("memory: cannot open events file: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("memory: cannot write event: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("memory: cannot sync event: %w", err)
	}
	return nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
