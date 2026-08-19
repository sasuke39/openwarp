package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sasuke39/open-warp/internal/config"
)

func newProfileTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	disabled := false
	cfg := &config.Config{
		Provider: "openai",
		BaseURL:  "http://initial.local/v1",
		APIKey:   "initial-key",
		Model:    "initial-model",
		Memory:   config.MemoryConfig{Enabled: &disabled},
	}
	return NewServer(cfg, filepath.Join(dir, "config.yaml"))
}

func profileRequest(t *testing.T, s *Server, method, target, name string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, target, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	if name != "" {
		req.SetPathValue("name", name)
	}
	rec := httptest.NewRecorder()
	switch {
	case strings.HasSuffix(target, "/activate"):
		s.handleProfileActivate(rec, req)
	case name != "":
		s.handleProfile(rec, req)
	default:
		s.handleProfiles(rec, req)
	}
	return rec
}

const glmProfileJSON = `{
  "provider": "glm",
  "base_url": "https://open.bigmodel.cn/api/paas/v4",
  "api_key": "glm-secret-key",
  "model": "glm-4.6",
  "max_tokens": 8192,
  "thinking_disabled": true
}`

func TestProfilesCRUD(t *testing.T) {
	s := newProfileTestServer(t)

	// Empty state: profiles dir does not exist yet → empty array, not error.
	rec := profileRequest(t, s, http.MethodGet, "/settings/profiles", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for empty list, got %d: %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Profiles []profileInfo `json:"profiles"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if list.Profiles == nil || len(list.Profiles) != 0 {
		t.Fatalf("expected empty (non-nil) profiles array, got %+v", list.Profiles)
	}

	// Create.
	rec = profileRequest(t, s, http.MethodPost, "/settings/profiles/glm", "glm", glmProfileJSON)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on create, got %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(s.profilePath("glm")); err != nil {
		t.Fatalf("expected profile file to exist: %v", err)
	}

	// Create with Chinese name.
	rec = profileRequest(t, s, http.MethodPost, "/settings/profiles/备用配置", "备用配置", glmProfileJSON)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for Chinese profile name, got %d: %s", rec.Code, rec.Body.String())
	}

	// Overwrite with a different model.
	updated := strings.Replace(glmProfileJSON, "glm-4.6", "glm-4.5-air", 1)
	rec = profileRequest(t, s, http.MethodPost, "/settings/profiles/glm", "glm", updated)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on overwrite, got %d: %s", rec.Code, rec.Body.String())
	}
	loaded, err := config.Load(s.profilePath("glm"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Model != "glm-4.5-air" || loaded.Provider != "glm" || loaded.APIKey != "glm-secret-key" {
		t.Fatalf("unexpected profile content after overwrite: %+v", loaded)
	}
	if !loaded.ThinkingDisabled || loaded.MaxTokens != 8192 {
		t.Fatalf("expected thinking_disabled/max_tokens to round-trip: %+v", loaded)
	}

	// Get one profile as JSON.
	rec = profileRequest(t, s, http.MethodGet, "/settings/profiles/glm", "glm", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on get, got %d: %s", rec.Code, rec.Body.String())
	}
	var got configJSON
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Provider != "glm" || got.BaseURL != "https://open.bigmodel.cn/api/paas/v4" ||
		got.APIKey != "glm-secret-key" || got.Model != "glm-4.5-air" {
		t.Fatalf("unexpected profile JSON: %+v", got)
	}

	// List: two profiles, none active, sorted by name.
	rec = profileRequest(t, s, http.MethodGet, "/settings/profiles", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on list, got %d", rec.Code)
	}
	list.Profiles = nil
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Profiles) != 2 || list.Profiles[0].Name != "glm" || list.Profiles[1].Name != "备用配置" {
		t.Fatalf("unexpected profile list: %+v", list.Profiles)
	}
	for _, p := range list.Profiles {
		if p.Active {
			t.Fatalf("no profile should be active yet: %+v", p)
		}
	}

	// Delete.
	rec = profileRequest(t, s, http.MethodDelete, "/settings/profiles/glm", "glm", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on delete, got %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(s.profilePath("glm")); !os.IsNotExist(err) {
		t.Fatalf("expected profile file removed, stat err=%v", err)
	}

	// 404s after delete.
	rec = profileRequest(t, s, http.MethodGet, "/settings/profiles/glm", "glm", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on get after delete, got %d", rec.Code)
	}
	rec = profileRequest(t, s, http.MethodDelete, "/settings/profiles/glm", "glm", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on second delete, got %d", rec.Code)
	}
}

func TestProfileActivate(t *testing.T) {
	s := newProfileTestServer(t)

	// Activating a missing profile → 404.
	rec := profileRequest(t, s, http.MethodPost, "/settings/profiles/glm/activate", "glm", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 activating missing profile, got %d", rec.Code)
	}

	profileRequest(t, s, http.MethodPost, "/settings/profiles/glm", "glm", glmProfileJSON)
	rec = profileRequest(t, s, http.MethodPost, "/settings/profiles/glm/activate", "glm", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on activate, got %d: %s", rec.Code, rec.Body.String())
	}

	// Response is the new settings status.
	var status settingsStatus
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if !status.OK || !status.Configured {
		t.Fatalf("expected configured status after activate, got %+v", status)
	}

	// config.yaml now holds the profile content.
	activeCfg, err := config.Load(s.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if activeCfg.Provider != "glm" || activeCfg.APIKey != "glm-secret-key" ||
		activeCfg.Model != "glm-4.6" || activeCfg.BaseURL != "https://open.bigmodel.cn/api/paas/v4" {
		t.Fatalf("config.yaml does not match activated profile: %+v", activeCfg)
	}

	// Active marker recorded.
	if got := s.activeProfile(); got != "glm" {
		t.Fatalf("expected active marker %q, got %q", "glm", got)
	}

	// Running server picked the config up via reloadConfig().
	s.mu.RLock()
	if s.cfg.Model != "glm-4.6" || s.cfg.Provider != "glm" {
		s.mu.RUnlock()
		t.Fatalf("server config not hot-reloaded: %+v", s.cfg)
	}
	s.mu.RUnlock()

	// List marks glm active.
	rec = profileRequest(t, s, http.MethodGet, "/settings/profiles", "", "")
	var list struct {
		Profiles []profileInfo `json:"profiles"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Profiles) != 1 || !list.Profiles[0].Active || list.Profiles[0].Name != "glm" {
		t.Fatalf("expected glm flagged active, got %+v", list.Profiles)
	}

	// Switching to a second profile updates config.yaml and the marker.
	second := strings.Replace(glmProfileJSON, "glm-4.6", "deepseek-chat", 1)
	profileRequest(t, s, http.MethodPost, "/settings/profiles/deepseek", "deepseek", second)
	rec = profileRequest(t, s, http.MethodPost, "/settings/profiles/deepseek/activate", "deepseek", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 switching profile, got %d", rec.Code)
	}
	if got := s.activeProfile(); got != "deepseek" {
		t.Fatalf("expected active marker to switch to deepseek, got %q", got)
	}
	activeCfg, err = config.Load(s.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if activeCfg.Model != "deepseek-chat" {
		t.Fatalf("expected config.yaml to follow new profile, got model=%q", activeCfg.Model)
	}
}

func TestProfileNameValidation(t *testing.T) {
	s := newProfileTestServer(t)

	bad := []string{
		"../evil", "..", "a/b", `a\b`, ".active", ".", " ",
		"name with space", "name.yaml", "name!", "glm/config",
	}
	for _, name := range bad {
		rec := profileRequest(t, s, http.MethodPost, "/settings/profiles/x", name, glmProfileJSON)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for name %q, got %d", name, rec.Code)
		}
		rec = profileRequest(t, s, http.MethodDelete, "/settings/profiles/x", name, "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 on DELETE for name %q, got %d", name, rec.Code)
		}
		rec = profileRequest(t, s, http.MethodPost, "/settings/profiles/x/activate", name, "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 on activate for name %q, got %d", name, rec.Code)
		}
	}

	// Nothing may have been written outside the (still nonexistent) profiles dir.
	if _, err := os.Stat(s.profilesDir()); !os.IsNotExist(err) {
		t.Fatalf("profiles dir should not have been created by rejected names, err=%v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(s.configPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), "evil") {
			t.Fatalf("path traversal wrote a file: %s", e.Name())
		}
	}

	// Valid names accepted: ASCII, digits, dash, underscore, Chinese.
	for _, name := range []string{"glm", "GLM4", "glm_4", "glm-4", "配置", "备用-1_号"} {
		rec := profileRequest(t, s, http.MethodPost, "/settings/profiles/x", name, glmProfileJSON)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 for valid name %q, got %d: %s", name, rec.Code, rec.Body.String())
		}
	}
}

func TestDeleteActiveProfileClearsMarkerOnly(t *testing.T) {
	s := newProfileTestServer(t)

	profileRequest(t, s, http.MethodPost, "/settings/profiles/glm", "glm", glmProfileJSON)
	if rec := profileRequest(t, s, http.MethodPost, "/settings/profiles/glm/activate", "glm", ""); rec.Code != http.StatusOK {
		t.Fatalf("activate failed: %d", rec.Code)
	}
	configBefore, err := os.ReadFile(s.configPath)
	if err != nil {
		t.Fatal(err)
	}

	rec := profileRequest(t, s, http.MethodDelete, "/settings/profiles/glm", "glm", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 deleting active profile, got %d: %s", rec.Code, rec.Body.String())
	}

	// Marker cleared.
	if got := s.activeProfile(); got != "" {
		t.Fatalf("expected active marker cleared, got %q", got)
	}
	if _, err := os.Stat(s.activeProfilePath()); !os.IsNotExist(err) {
		t.Fatalf("expected .active file removed, err=%v", err)
	}

	// config.yaml untouched.
	configAfter, err := os.ReadFile(s.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(configBefore, configAfter) {
		t.Fatalf("config.yaml must stay untouched when deleting the active profile")
	}

	// List is empty again (file gone, no active).
	rec = profileRequest(t, s, http.MethodGet, "/settings/profiles", "", "")
	var list struct {
		Profiles []profileInfo `json:"profiles"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Profiles) != 0 {
		t.Fatalf("expected no profiles left, got %+v", list.Profiles)
	}
}

func TestSettingsConfigJSON(t *testing.T) {
	s := newProfileTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/settings/config", nil)
	rec := httptest.NewRecorder()
	s.handleSettingsConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected JSON content type, got %q", ct)
	}

	// Raw map check: snake_case keys, api_key present (local service, unredacted).
	var raw map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if raw["provider"] != "openai" || raw["base_url"] != "http://initial.local/v1" ||
		raw["api_key"] != "initial-key" || raw["model"] != "initial-model" {
		t.Fatalf("unexpected config JSON: %v", raw)
	}
	server, ok := raw["server"].(map[string]any)
	if !ok || server["host"] != "127.0.0.1" {
		t.Fatalf("expected nested server config with defaults applied: %v", raw["server"])
	}
}
