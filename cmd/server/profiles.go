package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/sasuke39/open-warp/internal/config"
)

// profileNameRE restricts profile names to letters, digits, '-', '_' and
// Chinese characters. Since '/', '\\', '.' and whitespace are all rejected,
// path traversal is impossible by construction (names like ".." or "a/b"
// never match).
var profileNameRE = regexp.MustCompile(`^[A-Za-z0-9_\-\p{Han}]+$`)

const maxProfileNameRunes = 64

func validProfileName(name string) bool {
	if name == "" || utf8.RuneCountInString(name) > maxProfileNameRunes {
		return false
	}
	return profileNameRE.MatchString(name)
}

// configJSON is the wire representation of config.Config for the settings
// API. It mirrors the YAML key names (snake_case) so the JSON API stays
// consistent with config.yaml. config.Config itself intentionally has no
// JSON tags; using a DTO here keeps POST /settings decoding behavior
// untouched.
type configJSON struct {
	Provider         string           `json:"provider"`
	BaseURL          string           `json:"base_url"`
	APIKey           string           `json:"api_key"`
	Model            string           `json:"model"`
	MaxTokens        int              `json:"max_tokens"`
	ThinkingDisabled bool             `json:"thinking_disabled"`
	Server           serverConfigJSON `json:"server"`
	Memory           memoryConfigJSON `json:"memory"`
}

type serverConfigJSON struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type memoryConfigJSON struct {
	Enabled             *bool  `json:"enabled"`
	BaseDir             string `json:"base_dir"`
	SessionEnabled      *bool  `json:"session_enabled"`
	AutoEnabled         *bool  `json:"auto_enabled"`
	MaxProjectMemory    int    `json:"max_project_memories"`
	StaleAfterHours     int    `json:"stale_after_hours"`
	ContextWindowTokens int    `json:"context_window_tokens"`
}

func configToJSON(cfg *config.Config) configJSON {
	return configJSON{
		Provider:         cfg.Provider,
		BaseURL:          cfg.BaseURL,
		APIKey:           cfg.APIKey,
		Model:            cfg.Model,
		MaxTokens:        cfg.MaxTokens,
		ThinkingDisabled: cfg.ThinkingDisabled,
		Server: serverConfigJSON{
			Host: cfg.Server.Host,
			Port: cfg.Server.Port,
		},
		Memory: memoryConfigJSON{
			Enabled:             cfg.Memory.Enabled,
			BaseDir:             cfg.Memory.BaseDir,
			SessionEnabled:      cfg.Memory.SessionEnabled,
			AutoEnabled:         cfg.Memory.AutoEnabled,
			MaxProjectMemory:    cfg.Memory.MaxProjectMemory,
			StaleAfterHours:     cfg.Memory.StaleAfterHours,
			ContextWindowTokens: cfg.Memory.ContextWindowTokens,
		},
	}
}

func (c *configJSON) toConfig() *config.Config {
	return &config.Config{
		Provider:         c.Provider,
		BaseURL:          c.BaseURL,
		APIKey:           c.APIKey,
		Model:            c.Model,
		MaxTokens:        c.MaxTokens,
		ThinkingDisabled: c.ThinkingDisabled,
		Server: config.ServerConfig{
			Host: c.Server.Host,
			Port: c.Server.Port,
		},
		Memory: config.MemoryConfig{
			Enabled:             c.Memory.Enabled,
			BaseDir:             c.Memory.BaseDir,
			SessionEnabled:      c.Memory.SessionEnabled,
			AutoEnabled:         c.Memory.AutoEnabled,
			MaxProjectMemory:    c.Memory.MaxProjectMemory,
			StaleAfterHours:     c.Memory.StaleAfterHours,
			ContextWindowTokens: c.Memory.ContextWindowTokens,
		},
	}
}

type profileInfo struct {
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

func (s *Server) profilesDir() string {
	return filepath.Join(filepath.Dir(s.configPath), "profiles")
}

func (s *Server) profilePath(name string) string {
	return filepath.Join(s.profilesDir(), name+".yaml")
}

func (s *Server) activeProfilePath() string {
	return filepath.Join(s.profilesDir(), ".active")
}

// activeProfile returns the name of the currently activated profile, or ""
// when no profile has been activated (or the marker file is unreadable).
func (s *Server) activeProfile() string {
	data, err := os.ReadFile(s.activeProfilePath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func setProfilesCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// handleSettingsConfig serves GET /settings/config: the currently effective
// configuration as JSON, api_key included (local-only service, same exposure
// as the existing settings HTML page).
func (s *Server) handleSettingsConfig(w http.ResponseWriter, r *http.Request) {
	setProfilesCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.mu.RLock()
	cfg := *s.cfg
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, configToJSON(&cfg))
}

// handleProfiles serves GET /settings/profiles: the list of saved profiles
// with the active one flagged. A missing profiles directory yields an empty
// list rather than an error.
func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	setProfilesCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	profiles := make([]profileInfo, 0)
	entries, err := os.ReadDir(s.profilesDir())
	if err != nil {
		if !os.IsNotExist(err) {
			writeJSONError(w, http.StatusInternalServerError, "failed to list profiles")
			return
		}
	} else {
		active := s.activeProfile()
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
				continue
			}
			name := strings.TrimSuffix(entry.Name(), ".yaml")
			profiles = append(profiles, profileInfo{Name: name, Active: name == active})
		}
		sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": profiles})
}

// handleProfile serves GET/POST/DELETE /settings/profiles/{name}.
func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	setProfilesCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	name := r.PathValue("name")
	if !validProfileName(name) {
		writeJSONError(w, http.StatusBadRequest, "invalid profile name")
		return
	}
	path := s.profilePath(name)

	switch r.Method {
	case http.MethodGet:
		cfg, err := config.Load(path)
		if err != nil {
			if os.IsNotExist(err) {
				writeJSONError(w, http.StatusNotFound, "profile not found")
			} else {
				writeJSONError(w, http.StatusInternalServerError, "failed to load profile")
			}
			return
		}
		writeJSON(w, http.StatusOK, configToJSON(cfg))

	case http.MethodPost:
		var body configJSON
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		data, err := config.Dump(body.toConfig())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to serialize config")
			return
		}
		if err := os.MkdirAll(s.profilesDir(), 0o755); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to create profiles directory")
			return
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to write profile")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": name})

	case http.MethodDelete:
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				writeJSONError(w, http.StatusNotFound, "profile not found")
			} else {
				writeJSONError(w, http.StatusInternalServerError, "failed to delete profile")
			}
			return
		}
		// Deleting the active profile only clears the active marker;
		// config.yaml itself is left untouched.
		if s.activeProfile() == name {
			if err := os.Remove(s.activeProfilePath()); err != nil && !os.IsNotExist(err) {
				writeJSONError(w, http.StatusInternalServerError, "failed to clear active profile marker")
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": name})

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleProfileActivate serves POST /settings/profiles/{name}/activate: the
// profile is written to config.yaml, hot-reloaded via reloadConfig(), and
// recorded as active. Returns the same status shape as POST /settings.
func (s *Server) handleProfileActivate(w http.ResponseWriter, r *http.Request) {
	setProfilesCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	name := r.PathValue("name")
	if !validProfileName(name) {
		writeJSONError(w, http.StatusBadRequest, "invalid profile name")
		return
	}
	cfg, err := config.Load(s.profilePath(name))
	if err != nil {
		if os.IsNotExist(err) {
			writeJSONError(w, http.StatusNotFound, "profile not found")
		} else {
			writeJSONError(w, http.StatusInternalServerError, "failed to load profile")
		}
		return
	}
	data, err := config.Dump(cfg)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to serialize config")
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.configPath), 0o755); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to create config directory")
		return
	}
	if err := os.WriteFile(s.configPath, data, 0o644); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to write config")
		return
	}
	if err := os.WriteFile(s.activeProfilePath(), []byte(name+"\n"), 0o644); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to record active profile")
		return
	}
	writeJSON(w, http.StatusOK, s.reloadConfig())
}
