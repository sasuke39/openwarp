package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/sasuke39/open-warp/internal/agent"
	"github.com/sasuke39/open-warp/internal/config"
	"github.com/sasuke39/open-warp/internal/llm"
	"github.com/sasuke39/open-warp/internal/memory"
	"github.com/sasuke39/open-warp/internal/tools"

	"github.com/openai/openai-go"
	pb "github.com/sasuke39/open-warp/internal/proto"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Conversation struct {
	mu                       sync.Mutex
	history                  []openai.ChatCompletionMessageParamUnion
	client                   *llm.Client
	CreatedAt                time.Time
	LastRequestID            string
	LastRunID                string
	LastLongRunningCommandID string
	ProjectKey               string
	// todoList 是 update_task_list 工具维护的任务清单。只存在于内存:
	// 不写 history、不持久化,会话结束即消失。每轮请求前格式化成
	// "## Current Task Progress" 追加到 system prompt 尾部。
	todoList []TaskItem
}

type Server struct {
	mu               sync.RWMutex
	conversations    map[string]*Conversation
	runningTasks     sync.Map // taskID → context.CancelFunc
	cfg              *config.Config
	configPath       string
	persistencePath  string
	lastConfigError  string
	memoryStore      *memory.Store
	memoryQueue      *memory.DurableQueue
	memoryWake       chan struct{}
	memoryCancel     context.CancelFunc
	memoryDone       chan struct{}
	persistWake      chan struct{}
	persistDone      chan struct{}
	stopCh           chan struct{}
	stopOnce         sync.Once
	memoryMutationMu sync.Mutex
	persistenceMu    sync.Mutex
}

type settingsStatus struct {
	OK            bool     `json:"ok"`
	Name          string   `json:"name"`
	Configured    bool     `json:"configured"`
	MissingFields []string `json:"missing_fields,omitempty"`
	Error         string   `json:"error,omitempty"`
}

const maxConversations = 100

var supportedTools = map[string]struct{}{
	"read_files":                             {},
	"grep":                                   {},
	"file_glob":                              {},
	"file_glob_v2":                           {},
	"run_shell_command":                      {},
	"read_shell_command_output":              {},
	"transfer_shell_command_control_to_user": {},
	"apply_file_diffs":                       {},
	"search_codebase":                        {},
	// update_task_list 服务端执行,不转发客户端;列在这里是为了不被
	// splitUnsupportedToolCalls 当成不支持的工具。
	"update_task_list": {},
}

func NewServer(cfg *config.Config, configPath string) *Server {
	server := &Server{
		conversations:   make(map[string]*Conversation),
		cfg:             config.ApplyDefaults(cfg),
		configPath:      configPath,
		persistencePath: filepath.Join(filepath.Dir(configPath), "conversations.json"),
		memoryWake:      make(chan struct{}, 1),
		persistWake:     make(chan struct{}, 1),
		persistDone:     make(chan struct{}),
		stopCh:          make(chan struct{}),
	}
	if err := server.loadConversations(); err != nil {
		log.Printf("Failed to load persisted conversations: %v", err)
	}
	// Initialize memory store only when enabled.
	if server.cfg.Memory.IsEnabled() {
		memCfg := memory.Config{
			BaseDir:          server.cfg.Memory.BaseDir,
			StaleAfterHours:  server.cfg.Memory.StaleAfterHours,
			SessionEnabled:   server.cfg.Memory.IsSessionEnabled(),
			AutoEnabled:      server.cfg.Memory.IsAutoEnabled(),
			MaxProjectMemory: server.cfg.Memory.MaxProjectMemory,
		}
		store, err := memory.NewStore(memCfg, configPath)
		if err != nil {
			log.Printf("[MEMORY] Failed to create store: %v", err)
		} else {
			if err := store.EnsureDirs(); err != nil {
				log.Printf("[MEMORY] Failed to ensure dirs: %v", err)
			}
			server.memoryStore = store
			log.Printf("[MEMORY] Store initialized at %s", store.BaseDir())
			queue, queueErr := memory.OpenDurableQueue(filepath.Join(store.BaseDir(), "memory-queue.db"))
			if queueErr != nil {
				log.Printf("[MEMORY] Failed to open durable queue: %v", queueErr)
			} else {
				server.memoryQueue = queue
				server.startMemoryWorker()
			}
		}
	} else {
		log.Printf("[MEMORY] Memory system is disabled by configuration")
	}
	go server.persistenceLoop()
	return server
}

func (s *Server) getOrCreateConversation(id string) *Conversation {
	s.mu.Lock()
	if conv, ok := s.conversations[id]; ok {
		s.mu.Unlock()
		return conv
	}
	conv := &Conversation{
		client:    llm.NewClient(s.cfg),
		CreatedAt: time.Now().UTC(),
	}
	s.conversations[id] = conv
	s.evictOldestLocked()
	s.mu.Unlock()

	// Persist after releasing s.mu. saveConversations() takes s.mu.RLock(),
	// so calling it while holding the write lock would deadlock on a brand-new
	// conversation before the request can even emit StreamInit.
	s.requestConversationSave()
	return conv
}

func (s *Server) evictOldestLocked() {
	for len(s.conversations) > maxConversations {
		var oldestID string
		var oldestTime time.Time
		first := true
		for id, conv := range s.conversations {
			createdAt := conv.CreatedAt
			if createdAt.IsZero() {
				createdAt = time.Unix(0, 0).UTC()
			}
			if first || createdAt.Before(oldestTime) {
				first = false
				oldestID = id
				oldestTime = createdAt
			}
		}
		if oldestID == "" {
			return
		}
		delete(s.conversations, oldestID)
		log.Printf("Evicted oldest conversation %s to enforce limit=%d", oldestID, maxConversations)
	}
}

func defaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err == nil {
		return filepath.Join(home, "Library", "Application Support", "WarpLocal", "config.yaml")
	}
	return "config.yaml"
}

func main() {
	configPath := flag.String("config", "", "Path to config.yaml (default: ~/Library/Application Support/WarpLocal/config.yaml)")
	flag.Parse()

	resolvedConfigPath := *configPath
	if resolvedConfigPath == "" {
		resolvedConfigPath = defaultConfigPath()
	}

	// Set up file logging to ~/Library/Application Support/WarpLocal/warplocal.log
	logDir := filepath.Dir(resolvedConfigPath)
	if err := os.MkdirAll(logDir, 0o755); err == nil {
		logPath := filepath.Join(logDir, "warplocal.log")
		if logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			log.SetOutput(io.MultiWriter(os.Stderr, logFile))
			defer logFile.Close()
		}
	}
	log.Printf("[SERVER] Starting warp-local-adapter, config=%s", resolvedConfigPath)

	cfg, err := config.LoadOrDefault(resolvedConfigPath)
	server := NewServer(cfg, resolvedConfigPath)
	if err != nil {
		server.lastConfigError = err.Error()
		log.Printf("[SERVER] Config load warning: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/ai/multi-agent", server.handleAgentRequest)
	mux.HandleFunc("/health", server.handleHealth)
	mux.HandleFunc("/signup/remote", server.handleSignupRemote)
	mux.HandleFunc("/login/remote", server.handleSignupRemote)
	mux.HandleFunc("/settings", server.handleSettings)
	mux.HandleFunc("/settings/status", server.handleSettingsStatus)
	mux.HandleFunc("/settings/reload", server.handleSettingsReload)
	mux.HandleFunc("/settings/config", server.handleSettingsConfig)
	mux.HandleFunc("/settings/profiles", server.handleProfiles)
	mux.HandleFunc("/settings/profiles/{name}", server.handleProfile)
	mux.HandleFunc("/settings/profiles/{name}/activate", server.handleProfileActivate)
	mux.HandleFunc("POST /agent/tasks/{task_id}/cancel", server.handleCancelTask)
	mux.HandleFunc("/settings/memory/status", server.handleMemoryStatus)
	mux.HandleFunc("POST /settings/memory/clear-session", server.handleMemoryClearSession)
	mux.HandleFunc("POST /settings/memory/clear-project", server.handleMemoryClearProject)

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	httpServer := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
	}()

	log.Printf("Local adapter listening on %s", addr)
	if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer closeCancel()
	if err := server.closeBackground(closeCtx); err != nil {
		log.Printf("Failed graceful background shutdown: %v", err)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(s.currentStatus())
}

func (s *Server) currentStatus() settingsStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	status := settingsStatus{
		OK:   true,
		Name: "warp-local-adapter",
	}
	if s.cfg == nil {
		status.Error = "config is not loaded"
		return status
	}
	status.MissingFields = config.MissingRequiredFields(s.cfg)
	status.Configured = len(status.MissingFields) == 0
	if s.lastConfigError != "" {
		status.Error = s.lastConfigError
	}
	return status
}

func (s *Server) isConfigured() bool {
	return s.currentStatus().Configured
}

func (s *Server) reloadConfig() settingsStatus {
	cfg, err := config.LoadOrDefault(s.configPath)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.cfg = config.ApplyDefaults(cfg)
	if err != nil {
		s.lastConfigError = err.Error()
	} else {
		s.lastConfigError = ""
	}

	for _, conv := range s.conversations {
		conv.mu.Lock()
		conv.client = llm.NewClient(s.cfg)
		conv.history = nil
		conv.LastRequestID = ""
		conv.LastRunID = ""
		conv.LastLongRunningCommandID = ""
		conv.CreatedAt = time.Now().UTC()
		conv.mu.Unlock()
	}

	status := settingsStatus{
		OK:            true,
		Name:          "warp-local-adapter",
		MissingFields: config.MissingRequiredFields(s.cfg),
	}
	status.Configured = len(status.MissingFields) == 0
	if s.lastConfigError != "" {
		status.Error = s.lastConfigError
	}
	log.Printf("[SERVER] reloadConfig configured=%v missing=%v error=%q", status.Configured, status.MissingFields, status.Error)
	return status
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method == http.MethodGet {
		s.renderSettingsHTML(w)
		return
	}

	if r.Method == http.MethodPost {
		var newCfg config.Config
		if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			if err := json.NewDecoder(r.Body).Decode(&newCfg); err != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
		} else {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "invalid form data", http.StatusBadRequest)
				return
			}
			newCfg = config.Config{
				Provider: r.FormValue("provider"),
				BaseURL:  r.FormValue("base_url"),
				APIKey:   r.FormValue("api_key"),
				Model:    r.FormValue("model"),
				Server: config.ServerConfig{
					Host: r.FormValue("host"),
				},
			}
			if port := r.FormValue("port"); port != "" {
				fmt.Sscanf(port, "%d", &newCfg.Server.Port)
			}
		}
		newCfg = *config.ApplyDefaults(&newCfg)
		data, err := config.Dump(&newCfg)
		if err != nil {
			http.Error(w, "failed to serialize config", http.StatusInternalServerError)
			return
		}
		if err := os.MkdirAll(filepath.Dir(s.configPath), 0o755); err != nil {
			http.Error(w, "failed to create config directory", http.StatusInternalServerError)
			return
		}
		if err := os.WriteFile(s.configPath, data, 0o644); err != nil {
			http.Error(w, "failed to write config", http.StatusInternalServerError)
			return
		}
		status := s.reloadConfig()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(status)
		return
	}

	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleSettingsStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.currentStatus())
}

func (s *Server) handleSettingsReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.reloadConfig())
}

func (s *Server) renderSettingsHTML(w http.ResponseWriter) {
	status := s.currentStatus()
	s.mu.RLock()
	cfg := *config.ApplyDefaults(s.cfg)
	s.mu.RUnlock()
	statusJSON, _ := json.Marshal(status)
	warningHTML := func() string {
		var parts []string
		if status.Error != "" {
			parts = append(parts, "<div class=\"warning-item\"><strong>Config warning</strong><span>"+html.EscapeString(status.Error)+"</span></div>")
		}
		if len(status.MissingFields) > 0 {
			parts = append(parts, "<div class=\"warning-item\"><strong>Missing fields</strong><span>"+html.EscapeString(strings.Join(status.MissingFields, ", "))+"</span></div>")
		}
		return strings.Join(parts, "")
	}()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>WarpLocal Settings</title>
<style>
:root{
  --bg:#0b1020;
  --panel:#121a2d;
  --panel-soft:rgba(255,255,255,0.03);
  --border:rgba(148,163,184,0.18);
  --text:#e5ecf6;
  --muted:#95a3b8;
  --good:#3ddc84;
  --bad:#ff6b6b;
  --accent-1:#8b5cf6;
  --accent-2:#2563eb;
}
*{box-sizing:border-box}
body{
  font-family:-apple-system,BlinkMacSystemFont,"SF Pro Text",sans-serif;
  background:
    radial-gradient(circle at top left, rgba(139,92,246,0.2), transparent 28%%),
    radial-gradient(circle at top right, rgba(37,99,235,0.16), transparent 26%%),
    var(--bg);
  color:var(--text);
  margin:0;
  min-height:100vh;
}
a{color:#b9cbff;text-decoration:none}
a:hover{text-decoration:underline}
.wrap{max-width:1080px;margin:0 auto;padding:32px 24px 48px}
.hero{
  display:flex;
  justify-content:space-between;
  gap:24px;
  align-items:flex-start;
  margin-bottom:24px;
}
.hero-copy{max-width:700px}
.eyebrow{
  display:inline-flex;
  align-items:center;
  gap:8px;
  border:1px solid rgba(99,102,241,0.35);
  background:rgba(99,102,241,0.12);
  color:#cdd7ff;
  border-radius:999px;
  padding:7px 12px;
  font-size:12px;
  font-weight:700;
  letter-spacing:.02em;
  text-transform:uppercase;
}
h1{font-size:40px;line-height:1.05;margin:14px 0 12px}
.lead{color:var(--muted);font-size:18px;line-height:1.6;margin:0}
.hero-meta{
  display:grid;
  gap:10px;
  min-width:280px;
}
.meta-chip{
  padding:14px 16px;
  border-radius:16px;
  border:1px solid var(--border);
  background:rgba(15,23,42,0.78);
}
.meta-chip strong{display:block;font-size:13px;color:#c8d3e3;margin-bottom:4px}
.meta-chip span{font-size:14px;color:var(--muted)}
.layout{
  display:grid;
  grid-template-columns:minmax(0,1.3fr) minmax(300px,.9fr);
  gap:20px;
  align-items:start;
}
.card{
  background:linear-gradient(180deg, rgba(18,26,45,0.96), rgba(12,18,32,0.98));
  border:1px solid var(--border);
  border-radius:24px;
  padding:24px;
  box-shadow:0 18px 48px rgba(0,0,0,.24);
}
.section-title{font-size:22px;margin:0 0 8px}
.section-copy{color:var(--muted);line-height:1.6;margin:0 0 18px}
.status-badge{
  display:inline-flex;
  align-items:center;
  gap:8px;
  padding:9px 14px;
  border-radius:999px;
  border:1px solid rgba(61,220,132,.24);
  background:rgba(61,220,132,.1);
  font-weight:700;
  font-size:13px;
}
.status-badge.bad{
  border-color:rgba(255,107,107,.24);
  background:rgba(255,107,107,.1);
}
.status-grid{display:grid;gap:14px;margin-top:18px}
.status-panel{
  border-radius:18px;
  border:1px solid var(--border);
  background:var(--panel-soft);
  padding:16px;
}
.status-panel strong{display:block;font-size:15px;margin-bottom:6px}
.status-panel p{margin:0;color:var(--muted);line-height:1.5}
.status-list{display:grid;gap:10px;margin-top:12px}
.warning-item{
  display:grid;
  gap:4px;
  padding:12px 14px;
  border-radius:14px;
  border:1px solid rgba(255,107,107,.18);
  background:rgba(255,107,107,.08);
}
.warning-item strong{font-size:13px;color:#ffd0d0}
.warning-item span{font-size:13px;color:#ffc1c1}
label{display:block;margin:18px 0 8px;font-weight:600;color:#dbe6f4}
input,select{
  width:100%%;
  padding:14px 15px;
  border-radius:14px;
  border:1px solid rgba(148,163,184,0.22);
  background:#0d1527;
  color:var(--text);
  font-size:15px;
  outline:none;
}
input:focus,select:focus{
  border-color:rgba(96,165,250,0.9);
  box-shadow:0 0 0 3px rgba(59,130,246,0.18);
}
.field-help{font-size:13px;color:var(--muted);margin-top:7px}
.row{display:grid;grid-template-columns:1fr 1fr;gap:16px}
.password-row{position:relative}
.toggle-secret{
  position:absolute;
  right:12px;
  top:50%%;
  transform:translateY(-50%%);
  border:none;
  border-radius:10px;
  padding:8px 10px;
  background:#162036;
  color:#cbd5e1;
  cursor:pointer;
}
.actions{display:flex;gap:12px;flex-wrap:wrap;margin-top:26px}
button{
  border:none;
  border-radius:14px;
  padding:13px 18px;
  font-weight:700;
  font-size:14px;
  cursor:pointer;
}
.primary{background:linear-gradient(135deg,var(--accent-1),var(--accent-2));color:white}
.secondary{background:#1b2437;color:#e6edf3;border:1px solid rgba(148,163,184,0.18)}
.ghost{background:transparent;border:1px solid rgba(148,163,184,0.18);color:#d7dfeb}
.hint{
  font-size:13px;
  color:var(--muted);
  line-height:1.6;
  margin:18px 0 0;
}
code{
  background:#0b1222;
  padding:2px 6px;
  border-radius:8px;
  color:#dbe6ff;
}
.endpoint-list{display:grid;gap:10px}
.endpoint-item{
  padding:14px;
  border:1px solid var(--border);
  border-radius:16px;
  background:rgba(255,255,255,0.02);
}
.endpoint-item strong{display:block;font-size:14px;margin-bottom:6px}
.endpoint-item span{color:var(--muted);font-size:13px;line-height:1.5}
.save-note{
  min-height:22px;
  margin-top:12px;
  font-size:13px;
  color:#a5b4fc;
}
.footer{
  margin-top:22px;
  padding-top:18px;
  border-top:1px solid rgba(148,163,184,0.16);
  color:var(--muted);
  font-size:14px;
  line-height:1.7;
}
@media (max-width: 900px){
  .hero,.layout{grid-template-columns:1fr;display:grid}
  .hero{align-items:start}
}
@media (max-width: 640px){
  .wrap{padding:24px 16px 40px}
  h1{font-size:32px}
  .row{grid-template-columns:1fr}
  .actions button{width:100%%}
}
</style>
</head>
<body>
<div class="wrap">
  <section class="hero">
    <div class="hero-copy">
      <div class="eyebrow">WarpLocal Control Center</div>
      <h1>Run Warp against your own LLM stack.</h1>
      <p class="lead">Configure your provider, check adapter health, and hot-reload the local backend without leaving WarpLocal.</p>
    </div>
    <div class="hero-meta">
      <div class="meta-chip">
        <strong>Config file</strong>
        <span><code>%s</code></span>
      </div>
      <div class="meta-chip">
        <strong>Project</strong>
        <span><a href="https://github.com/sasuke39/open-warp" target="_blank" rel="noreferrer">sasuke39/open-warp</a></span>
      </div>
    </div>
  </section>

  <section class="layout">
    <form id="settings-form" class="card" method="post" action="/settings">
      <h2 class="section-title">Connection Settings</h2>
      <p class="section-copy">These values are stored locally and used by the helper service inside <code>WarpLocal.app</code>.</p>

      <label>Provider</label>
      <select name="provider" id="provider">
        %s
      </select>
      <div class="field-help">Pick a preset to prefill the most common base URL and model pair.</div>

      <label>Base URL</label>
      <input type="url" name="base_url" id="base_url" value="%s" placeholder="https://api.openai.com/v1">

      <label>API Key</label>
      <div class="password-row">
        <input type="password" name="api_key" id="api_key" value="%s" placeholder="sk-...">
        <button class="toggle-secret" type="button" id="toggle-secret">Show</button>
      </div>

      <label>Model</label>
      <input type="text" name="model" id="model" value="%s" placeholder="gpt-4.1-mini">

      <div class="row">
        <div>
          <label>Host</label>
          <input type="text" name="host" value="%s" placeholder="127.0.0.1">
        </div>
        <div>
          <label>Port</label>
          <input type="number" name="port" value="%d" placeholder="18888">
        </div>
      </div>

      <div class="actions">
        <button class="primary" type="submit">Save & Reload</button>
        <button class="secondary" type="button" id="refresh-status">Refresh Status</button>
        <button class="ghost" type="button" id="open-health">Open Health JSON</button>
      </div>
      <div class="save-note" id="save-note"></div>
      <p class="hint">Saving writes <code>config.yaml</code> and triggers <code>POST /settings/reload</code> so the running helper picks up the new provider immediately.</p>
    </form>

    <aside class="card">
      <h2 class="section-title">Adapter Status</h2>
      <p class="section-copy">A quick read on whether WarpLocal is ready to accept agent requests.</p>
      <div id="status-badge" class="status-badge">Checking configuration…</div>
      <div class="status-grid">
        <div class="status-panel">
          <strong id="status-title">Waiting for status</strong>
          <p id="status-copy">Refresh the adapter state after editing settings or switching providers.</p>
        </div>
        <div class="status-panel">
          <strong>Useful endpoints</strong>
          <div class="endpoint-list">
            <div class="endpoint-item">
              <strong><code>GET /health</code></strong>
              <span>Basic liveliness check for scripts, packaging smoke tests, and release verification.</span>
            </div>
            <div class="endpoint-item">
              <strong><code>GET /settings/status</code></strong>
              <span>Returns the currently loaded configuration state and any missing required fields.</span>
            </div>
          </div>
        </div>
        <div id="status-issues" class="status-list">%s</div>
      </div>
      <div class="footer">
        If WarpLocal is helpful, please <a href="https://github.com/sasuke39/open-warp" target="_blank" rel="noreferrer">star the project on GitHub</a>. That little nudge helps us keep polishing the settings experience and add more coding tools.
      </div>
    </aside>
  </section>
</div>
<script>
const initialStatus = %s;
const presets = {
  "OpenAI": { base_url: "https://api.openai.com/v1", model: "gpt-4.1-mini" },
  "DeepSeek": { base_url: "https://api.deepseek.com", model: "deepseek-chat" },
  "Ollama": { base_url: "http://localhost:11434/v1", model: "llama3" },
  "Custom": { base_url: "", model: "" }
};
document.getElementById("provider").addEventListener("change", (e) => {
  const preset = presets[e.target.value];
  if (preset) {
    document.getElementById("base_url").value = preset.base_url;
    document.getElementById("model").value = preset.model;
  }
});

function renderStatus(data) {
  const badge = document.getElementById("status-badge");
  const title = document.getElementById("status-title");
  const copy = document.getElementById("status-copy");
  const issues = document.getElementById("status-issues");

  badge.textContent = data.configured ? "Configured and ready" : "Needs attention";
  badge.className = "status-badge" + (data.configured ? "" : " bad");

  if (data.configured) {
    title.textContent = "Local adapter is ready";
    copy.textContent = "WarpLocal should be able to start agent conversations with the provider configured on this page.";
  } else {
    title.textContent = "Configuration is incomplete";
    copy.textContent = "Fill the missing fields below, save, and WarpLocal will reload the helper without a full restart.";
  }

  const issueBlocks = [];
  if (data.error) {
    issueBlocks.push('<div class="warning-item"><strong>Config warning</strong><span>' + escapeHtml(data.error) + '</span></div>');
  }
  if (data.missing_fields && data.missing_fields.length) {
    issueBlocks.push('<div class="warning-item"><strong>Missing fields</strong><span>' + escapeHtml(data.missing_fields.join(", ")) + '</span></div>');
  }
  issues.innerHTML = issueBlocks.join("");
}

function escapeHtml(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

async function fetchStatus() {
  const resp = await fetch("/settings/status");
  const data = await resp.json();
  renderStatus(data);
}

document.getElementById("refresh-status").addEventListener("click", async () => {
  await fetchStatus();
  document.getElementById("save-note").textContent = "Status refreshed from /settings/status.";
});

document.getElementById("open-health").addEventListener("click", () => {
  window.open("/health", "_blank");
});

document.getElementById("toggle-secret").addEventListener("click", () => {
  const field = document.getElementById("api_key");
  const isPassword = field.type === "password";
  field.type = isPassword ? "text" : "password";
  document.getElementById("toggle-secret").textContent = isPassword ? "Hide" : "Show";
});

document.getElementById("settings-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const form = event.currentTarget;
  const payload = new URLSearchParams(new FormData(form));
  const resp = await fetch("/settings", {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded;charset=UTF-8" },
    body: payload.toString()
  });
  const data = await resp.json();
  renderStatus(data);
  document.getElementById("save-note").textContent = data.configured
    ? "Saved. The local adapter reloaded successfully."
    : "Saved, but the adapter still needs a few required fields.";
});

renderStatus(initialStatus);
if (!initialStatus.configured) {
  document.getElementById("save-note").textContent = "Add your provider settings, then save to activate the local adapter.";
}
</script>
</body>
</html>`,
		html.EscapeString(s.configPath),
		renderProviderOptions(cfg.Provider),
		html.EscapeString(cfg.BaseURL),
		html.EscapeString(cfg.APIKey),
		html.EscapeString(cfg.Model),
		html.EscapeString(cfg.Server.Host),
		cfg.Server.Port,
		warningHTML,
		string(statusJSON),
	)
}

func renderProviderOptions(selected string) string {
	providers := []string{"OpenAI", "DeepSeek", "Ollama", "Custom"}
	var b strings.Builder
	for _, provider := range providers {
		if provider == selected {
			fmt.Fprintf(&b, `<option selected value="%s">%s</option>`, provider, provider)
		} else {
			fmt.Fprintf(&b, `<option value="%s">%s</option>`, provider, provider)
		}
	}
	return b.String()
}

func (s *Server) handleSignupRemote(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	scheme := r.URL.Query().Get("scheme")
	if scheme == "" {
		scheme = "warplocal"
	}
	redirectURL := fmt.Sprintf("%s://auth/desktop_redirect?refresh_token=local&state=%s", scheme, state)

	log.Printf("[/signup/remote] scheme=%s state=%s → redirecting to %s", scheme, state, redirectURL)

	// Return an HTML page that redirects via JavaScript.
	// Browsers may block 302 redirects to custom URL schemes, but allow
	// user-initiated or JS-triggered navigations.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><meta charset="utf-8"><title>Warp Local</title></head>
<body>
<p>Logging in to local Warp adapter...</p>
<p>If nothing happens, <a href="%s">click here</a>.</p>
<script>window.location.href = "%s";</script>
</body>
</html>`, redirectURL, redirectURL)
}

func (s *Server) handleCancelTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("task_id")
	if taskID == "" {
		http.Error(w, "missing task_id", http.StatusBadRequest)
		return
	}
	if cancel, ok := s.runningTasks.Load(taskID); ok {
		if fn, ok := cancel.(context.CancelFunc); ok {
			fn()
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleAgentRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("[REQ] Failed to read body: %v", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	log.Printf("[REQ] body_size=%d", len(body))

	var req pb.Request
	if err := proto.Unmarshal(body, &req); err != nil {
		log.Printf("[REQ] Failed to unmarshal: %v", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	convID := req.GetMetadata().GetConversationId()
	if convID == "" {
		convID = uuid.New().String()
	}

	// Extract task ID from the request — must match what the client sent
	// in TaskContext.tasks, otherwise AddMessagesToTask won't find the task.
	taskID := "task-" + uuid.New().String()
	taskIDFromClient := false
	if tc := req.GetTaskContext(); tc != nil {
		if tasks := tc.GetTasks(); len(tasks) > 0 {
			taskID = tasks[0].GetId()
			taskIDFromClient = true
			log.Printf("[REQ] using task_id from request: %s (found %d tasks)", taskID, len(tasks))
		} else {
			log.Printf("[REQ] WARNING: TaskContext has no tasks, using generated task_id=%s", taskID)
		}
	} else {
		log.Printf("[REQ] WARNING: no TaskContext in request, using generated task_id=%s", taskID)
	}

	conv := s.getOrCreateConversation(convID)
	conv.ProjectKey = s.resolveProjectKeyFromRequest(&req, convID)

	// Extract user inputs from request
	inputs := extractInputs(&req)
	isFollowUp := len(inputs) > 0 && inputs[0].Kind == "tool_result"

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	if !s.isConfigured() {
		log.Printf("[REQ] Local adapter is not configured")
		s.sendEvent(w, flusher, &pb.ResponseEvent{
			Type: &pb.ResponseEvent_Init{
				Init: &pb.ResponseEvent_StreamInit{
					ConversationId: convID,
					RequestId:      uuid.New().String(),
					RunId:          uuid.New().String(),
				},
			},
		})
		s.sendFinishError(w, flusher, "Local Adapter is not configured. Open Settings → Local Adapter to configure your LLM provider.")
		return
	}

	conv.mu.Lock()
	if conv.CreatedAt.IsZero() {
		conv.CreatedAt = time.Now().UTC()
	}
	requestID := uuid.New().String()
	runID := uuid.New().String()
	if isFollowUp && conv.LastRequestID != "" && conv.LastRunID != "" {
		requestID = conv.LastRequestID
		runID = conv.LastRunID
	} else {
		conv.LastRequestID = requestID
		conv.LastRunID = runID
	}

	log.Printf("[REQ] conv=%s req=%s run=%s history_len=%d", convID, requestID, runID, len(conv.history))

	// Send StreamInit
	s.sendEvent(w, flusher, &pb.ResponseEvent{
		Type: &pb.ResponseEvent_Init{
			Init: &pb.ResponseEvent_StreamInit{
				ConversationId: convID,
				RequestId:      requestID,
				RunId:          runID,
			},
		},
	})

	if len(inputs) == 0 {
		conv.mu.Unlock()
		log.Printf("[REQ] No inputs found in request, sending empty finish")
		s.sendEvent(w, flusher, finishEvent(&pb.ResponseEvent_StreamFinished_Done{}))
		return
	}

	for i, in := range inputs {
		contentPreview := in.Content
		if len(contentPreview) > 200 {
			contentPreview = contentPreview[:200] + "..."
		}
		log.Printf("[REQ] input[%d] kind=%s tool_call_id=%s content=%q", i, in.Kind, in.ToolCallID, contentPreview)
	}

	// Feed inputs into conversation history
	for _, in := range inputs {
		if in.LongRunningCommandID != "" {
			conv.LastLongRunningCommandID = in.LongRunningCommandID
		}
		if in.ShellCommandCompleted {
			conv.LastLongRunningCommandID = ""
		}
		switch in.Kind {
		case "user_query":
			conv.history = append(conv.history, llm.MakeUserMessage(in.Content))
		case "tool_result":
			conv.history = append(conv.history, llm.MakeToolResultMessage(in.ToolCallID, in.Content))
		}
	}

	log.Printf("[REQ] history now has %d messages, calling LLM", len(conv.history))

	// Normalize only after this request's inputs are present. A just-emitted
	// assistant tool_call is valid while we wait for the client to return the
	// matching tool_result; pruning it before the result arrives causes the agent
	// to forget completed tools and repeat the same command forever.
	if normalized, changed := normalizeConversationHistory(conv.history); changed {
		log.Printf("[HISTORY] normalized conversation %s after appending request inputs: %d -> %d messages", convID, len(conv.history), len(normalized))
		conv.history = normalized
	}

	// Build memory-augmented system prompt and check compaction.
	systemPrompt := agent.SystemPrompt
	var historyForLLM []openai.ChatCompletionMessageParamUnion

	if s.memoryStore != nil && s.cfg.Memory.IsEnabled() {
		systemPrompt, historyForLLM = s.buildMemoryContext(conv, convID)
	}
	systemPrompt = agent.WithExecutionContext(systemPrompt, req.GetInput().GetContext())
	managedSSHTarget, _ := agent.ManagedSSHTargetFromInput(req.GetInput().GetContext())
	if historyForLLM == nil {
		historyForLLM = conv.history
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	ctx = context.WithValue(ctx, beforeAgentFinishKey{}, func() {
		s.enqueueMemoryUpdates(conv, convID)
	})

	// Register the cancel function so the /agent/tasks/{task_id}/cancel
	// endpoint can stop a running agent loop.
	s.runningTasks.Store(taskID, cancel)
	defer s.runningTasks.Delete(taskID)

	s.runAgentLoop(ctx, w, flusher, conv, requestID, taskID, isFollowUp || taskIDFromClient, systemPrompt, historyForLLM, managedSSHTarget)
	conv.mu.Unlock()
	s.requestConversationSave()
}

type input struct {
	Kind                  string // "user_query" or "tool_result"
	Content               string
	ToolCallID            string
	LongRunningCommandID  string
	ShellCommandCompleted bool
}

func extractInputs(req *pb.Request) []input {
	var inputs []input

	switch v := req.GetInput().GetType().(type) {
	case *pb.Request_Input_UserQuery_:
		inputs = append(inputs, input{
			Kind:    "user_query",
			Content: v.UserQuery.GetQuery(),
		})
	case *pb.Request_Input_UserInputs_:
		for _, u := range v.UserInputs.GetInputs() {
			switch ui := u.GetInput().(type) {
			case *pb.Request_Input_UserInputs_UserInput_UserQuery:
				inputs = append(inputs, input{
					Kind:    "user_query",
					Content: ui.UserQuery.GetQuery(),
				})
			case *pb.Request_Input_UserInputs_UserInput_ToolCallResult:
				inputs = append(inputs, extractToolResult(ui.ToolCallResult))
			}
		}
	}
	return inputs
}

func extractToolResult(tc *pb.Request_Input_ToolCallResult) input {
	ui := input{
		Kind:       "tool_result",
		ToolCallID: tc.GetToolCallId(),
	}
	if result := tc.GetRunShellCommand(); result != nil {
		if snapshot := result.GetLongRunningCommandSnapshot(); snapshot != nil {
			ui.LongRunningCommandID = snapshot.GetCommandId()
		}
		if result.GetCommandFinished() != nil {
			ui.ShellCommandCompleted = true
		}
	}
	if result := tc.GetReadShellCommandOutput(); result != nil {
		if snapshot := result.GetLongRunningCommandSnapshot(); snapshot != nil {
			ui.LongRunningCommandID = snapshot.GetCommandId()
		}
		if result.GetCommandFinished() != nil {
			ui.ShellCommandCompleted = true
		}
	}
	if result := tc.GetTransferShellCommandControlToUser(); result != nil {
		if snapshot := result.GetLongRunningCommandSnapshot(); snapshot != nil {
			ui.LongRunningCommandID = snapshot.GetCommandId()
		}
		if result.GetCommandFinished() != nil {
			ui.ShellCommandCompleted = true
		}
	}
	ui.Content = summarizeToolCallResult(tc)
	return ui
}

type persistedConversation struct {
	History                  []json.RawMessage `json:"history"`
	LastRequestID            string            `json:"last_request_id"`
	LastRunID                string            `json:"last_run_id"`
	LastLongRunningCommandID string            `json:"last_long_running_command_id,omitempty"`
	CreatedAt                time.Time         `json:"created_at"`
}

type storedMessage struct {
	Role             string                    `json:"role"`
	Content          string                    `json:"content,omitempty"`
	ReasoningContent string                    `json:"reasoning_content,omitempty"`
	ToolCallID       string                    `json:"tool_call_id,omitempty"`
	ToolCalls        []storedAssistantToolCall `json:"tool_calls,omitempty"`
}

type storedAssistantToolCall struct {
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (s *Server) loadConversations() error {
	data, err := os.ReadFile(s.persistencePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var persisted map[string]persistedConversation
	if err := json.Unmarshal(data, &persisted); err != nil {
		return err
	}

	for id, item := range persisted {
		history, err := deserializeHistory(item.History)
		if err != nil {
			log.Printf("Skipping persisted conversation %s: %v", id, err)
			continue
		}
		createdAt := item.CreatedAt
		if createdAt.IsZero() {
			createdAt = time.Now().UTC()
		}
		s.conversations[id] = &Conversation{
			history:                  history,
			client:                   llm.NewClient(s.cfg),
			CreatedAt:                createdAt,
			LastRequestID:            item.LastRequestID,
			LastRunID:                item.LastRunID,
			LastLongRunningCommandID: item.LastLongRunningCommandID,
		}
	}

	s.evictOldestLocked()
	log.Printf("Loaded %d persisted conversations from %s", len(s.conversations), s.persistencePath)
	return nil
}

func (s *Server) saveConversations() error {
	s.persistenceMu.Lock()
	defer s.persistenceMu.Unlock()
	// 只持短读锁拷贝会话快照。此前版本在 RLock 内逐个 conv.mu.Lock(),
	// 而 handleAgentRequest 在整个 agent 循环期间(可能数分钟)持有 conv.mu:
	// 一旦有新会话在 s.mu.Lock 上排队(Go RWMutex 写者优先),所有 RLock
	// 全部堵死,整个 server 形成 convoy 死锁。
	type convEntry struct {
		id   string
		conv *Conversation
	}
	s.mu.RLock()
	entries := make([]convEntry, 0, len(s.conversations))
	for id, conv := range s.conversations {
		entries = append(entries, convEntry{id, conv})
	}
	s.mu.RUnlock()
	sort.Slice(entries, func(i, j int) bool { return entries[i].id < entries[j].id })

	// Preserve the last durable snapshot for conversations that are currently
	// busy. Skipping their mutex must not erase them from conversations.json.
	persisted := make(map[string]persistedConversation, len(entries))
	if previous, err := os.ReadFile(s.persistencePath); err == nil {
		_ = json.Unmarshal(previous, &persisted)
	}
	active := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		active[entry.id] = struct{}{}
	}
	for id := range persisted {
		if _, ok := active[id]; !ok {
			delete(persisted, id)
		}
	}
	for _, e := range entries {
		// 正在跑 agent 循环的会话本轮跳过不存(下一次保存会覆盖),
		// 绝不能在这里阻塞等待,否则又会把存盘路径和活跃会话耦合成死锁。
		if !e.conv.mu.TryLock() {
			continue
		}
		history, err := serializeHistory(e.conv.history)
		if err != nil {
			e.conv.mu.Unlock()
			return fmt.Errorf("serialize conversation %s: %w", e.id, err)
		}
		createdAt := e.conv.CreatedAt
		if createdAt.IsZero() {
			createdAt = time.Now().UTC()
		}
		persisted[e.id] = persistedConversation{
			History:                  history,
			LastRequestID:            e.conv.LastRequestID,
			LastRunID:                e.conv.LastRunID,
			LastLongRunningCommandID: e.conv.LastLongRunningCommandID,
			CreatedAt:                createdAt,
		}
		e.conv.mu.Unlock()
	}

	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(s.persistencePath), 0o755); err != nil {
		return err
	}
	return atomicWritePersistence(s.persistencePath, data)
}

func atomicWritePersistence(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "conversations-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if parent, err := os.Open(dir); err == nil {
			err = parent.Sync()
			_ = parent.Close()
			return err
		}
	}
	return nil
}

func serializeHistory(history []openai.ChatCompletionMessageParamUnion) ([]json.RawMessage, error) {
	items := make([]json.RawMessage, 0, len(history))
	for _, msg := range history {
		b, err := json.Marshal(msg)
		if err != nil {
			return nil, err
		}
		items = append(items, json.RawMessage(b))
	}
	return items, nil
}

func deserializeHistory(rawMessages []json.RawMessage) ([]openai.ChatCompletionMessageParamUnion, error) {
	history := make([]openai.ChatCompletionMessageParamUnion, 0, len(rawMessages))
	for _, raw := range rawMessages {
		var msg storedMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			return nil, err
		}

		switch msg.Role {
		case "user":
			history = append(history, llm.MakeUserMessage(msg.Content))
		case "tool":
			history = append(history, llm.MakeToolResultMessage(msg.ToolCallID, msg.Content))
		case "assistant":
			if len(msg.ToolCalls) > 0 {
				toolCalls := make([]llm.ToolCall, 0, len(msg.ToolCalls))
				for _, tc := range msg.ToolCalls {
					toolCalls = append(toolCalls, llm.ToolCall{
						ID:   tc.ID,
						Name: tc.Function.Name,
						Args: json.RawMessage(tc.Function.Arguments),
					})
				}
				history = append(history, llm.MakeAssistantToolCallMessage(toolCalls, msg.ReasoningContent))
				continue
			}
			history = append(history, llm.MakeAssistantMessageWithReasoning(msg.Content, msg.ReasoningContent))
		default:
			log.Printf("Skipping unsupported persisted message role=%q", msg.Role)
		}
	}
	return history, nil
}

func summarizeToolCallResult(tc *pb.Request_Input_ToolCallResult) string {
	switch {
	case tc.GetRunShellCommand() != nil:
		return summarizeRunShellCommandResult(tc.GetRunShellCommand())
	case tc.GetReadFiles() != nil:
		return summarizeReadFilesResult(tc.GetReadFiles())
	case tc.GetSearchCodebase() != nil:
		return summarizeSearchCodebaseResult(tc.GetSearchCodebase())
	case tc.GetApplyFileDiffs() != nil:
		return summarizeApplyFileDiffsResult(tc.GetApplyFileDiffs())
	case tc.GetGrep() != nil:
		return summarizeGrepResult(tc.GetGrep())
	case tc.GetFileGlob() != nil:
		return summarizeFileGlobResult(tc.GetFileGlob())
	case tc.GetFileGlobV2() != nil:
		return summarizeFileGlobV2Result(tc.GetFileGlobV2())
	case tc.GetReadShellCommandOutput() != nil:
		return summarizeReadShellCommandOutputResult(tc.GetReadShellCommandOutput())
	case tc.GetTransferShellCommandControlToUser() != nil:
		return summarizeTransferShellCommandControlToUserResult(tc.GetTransferShellCommandControlToUser())
	default:
		if b, err := json.Marshal(tc.GetResult()); err == nil {
			return string(b)
		}
		return "tool result received"
	}
}

func summarizeRunShellCommandResult(result *pb.RunShellCommandResult) string {
	if finished := result.GetCommandFinished(); finished != nil {
		output := strings.TrimSpace(finished.GetOutput())
		if output == "" {
			output = "(no output)"
		}
		return fmt.Sprintf("Command: %s\nExit Code: %d\nOutput:\n%s", result.GetCommand(), finished.GetExitCode(), output)
	}
	if snapshot := result.GetLongRunningCommandSnapshot(); snapshot != nil {
		output := strings.TrimSpace(snapshot.GetOutput())
		if output == "" {
			output = "(no output yet)"
		}
		return fmt.Sprintf("Command still running: %s\nCommand ID: %s\nCurrent Output:\n%s", result.GetCommand(), snapshot.GetCommandId(), output)
	}
	if denied := result.GetPermissionDenied(); denied != nil {
		return fmt.Sprintf("Command denied: %s\nReason: %s", result.GetCommand(), summarizePermissionDenied(denied))
	}
	if result.GetOutput() != "" || result.GetExitCode() != 0 {
		return fmt.Sprintf("Command: %s\nExit Code: %d\nOutput:\n%s", result.GetCommand(), result.GetExitCode(), strings.TrimSpace(result.GetOutput()))
	}
	return fmt.Sprintf("Command finished: %s", result.GetCommand())
}

func summarizePermissionDenied(denied *pb.PermissionDenied) string {
	switch denied.GetReason().(type) {
	case *pb.PermissionDenied_DenylistedCommand:
		return "command is denylisted"
	default:
		return "permission denied"
	}
}

func summarizeReadFilesResult(result *pb.ReadFilesResult) string {
	if success := result.GetTextFilesSuccess(); success != nil {
		return joinFileContents(success.GetFiles())
	}
	if success := result.GetAnyFilesSuccess(); success != nil {
		var sections []string
		for _, file := range success.GetFiles() {
			if text := file.GetTextContent(); text != nil {
				sections = append(sections, formatFileContent(text))
				continue
			}
			if binary := file.GetBinaryContent(); binary != nil {
				sections = append(sections, fmt.Sprintf("File: %s\n<binary content: %d bytes>", binary.GetFilePath(), len(binary.GetData())))
			}
		}
		if len(sections) == 0 {
			return "Read files succeeded with no readable content."
		}
		return strings.Join(sections, "\n\n")
	}
	if errResult := result.GetError(); errResult != nil {
		return "Read files failed: " + errResult.GetMessage()
	}
	return "Read files completed."
}

func summarizeSearchCodebaseResult(result *pb.SearchCodebaseResult) string {
	if success := result.GetSuccess(); success != nil {
		return joinFileContents(success.GetFiles())
	}
	if errResult := result.GetError(); errResult != nil {
		return "Search codebase failed: " + errResult.GetMessage()
	}
	return "Search codebase completed."
}

func summarizeApplyFileDiffsResult(result *pb.ApplyFileDiffsResult) string {
	if success := result.GetSuccess(); success != nil {
		var parts []string
		for _, file := range success.GetUpdatedFilesV2() {
			section := formatFileContent(file.GetFile())
			if file.GetWasEditedByUser() {
				section += "\nNote: file includes user edits."
			}
			parts = append(parts, section)
		}
		for _, file := range success.GetUpdatedFiles() {
			parts = append(parts, formatFileContent(file))
		}
		for _, deleted := range success.GetDeletedFiles() {
			parts = append(parts, fmt.Sprintf("Deleted file: %s", deleted.GetFilePath()))
		}
		if len(parts) == 0 {
			return "Apply file diffs succeeded with no file details."
		}
		return strings.Join(parts, "\n\n")
	}
	if errResult := result.GetError(); errResult != nil {
		return "Apply file diffs failed: " + errResult.GetMessage()
	}
	return "Apply file diffs completed."
}

func summarizeGrepResult(result *pb.GrepResult) string {
	if success := result.GetSuccess(); success != nil {
		var parts []string
		for _, file := range success.GetMatchedFiles() {
			lines := make([]string, 0, len(file.GetMatchedLines()))
			for _, line := range file.GetMatchedLines() {
				lines = append(lines, fmt.Sprintf("%d", line.GetLineNumber()))
			}
			parts = append(parts, fmt.Sprintf("%s: lines %s", file.GetFilePath(), strings.Join(lines, ", ")))
		}
		if len(parts) == 0 {
			return "Grep succeeded with no matches."
		}
		return strings.Join(parts, "\n")
	}
	if errResult := result.GetError(); errResult != nil {
		return "Grep failed: " + errResult.GetMessage()
	}
	return "Grep completed."
}

func summarizeFileGlobResult(result *pb.FileGlobResult) string {
	if success := result.GetSuccess(); success != nil {
		matches := strings.TrimSpace(success.GetMatchedFiles())
		if matches == "" {
			return "File glob succeeded with no matches."
		}
		return "Matched files:\n" + matches
	}
	if errResult := result.GetError(); errResult != nil {
		return "File glob failed: " + errResult.GetMessage()
	}
	return "File glob completed."
}

func summarizeFileGlobV2Result(result *pb.FileGlobV2Result) string {
	if success := result.GetSuccess(); success != nil {
		lines := make([]string, 0, len(success.GetMatchedFiles()))
		for _, match := range success.GetMatchedFiles() {
			lines = append(lines, match.GetFilePath())
		}
		if warnings := strings.TrimSpace(success.GetWarnings()); warnings != "" {
			lines = append(lines, "Warnings: "+warnings)
		}
		if len(lines) == 0 {
			return "File glob v2 succeeded with no matches."
		}
		return "Matched files:\n" + strings.Join(lines, "\n")
	}
	if errResult := result.GetError(); errResult != nil {
		return "File glob v2 failed: " + errResult.GetMessage()
	}
	return "File glob v2 completed."
}

func summarizeReadShellCommandOutputResult(result *pb.ReadShellCommandOutputResult) string {
	if finished := result.GetCommandFinished(); finished != nil {
		output := strings.TrimSpace(finished.GetOutput())
		if output == "" {
			output = "(no output)"
		}
		return fmt.Sprintf("Command finished.\nExit Code: %d\nOutput:\n%s", finished.GetExitCode(), output)
	}
	if snapshot := result.GetLongRunningCommandSnapshot(); snapshot != nil {
		output := strings.TrimSpace(snapshot.GetOutput())
		if output == "" {
			output = "(no output yet)"
		}
		return fmt.Sprintf("Command still running.\nCommand ID: %s\nCurrent Output:\n%s", snapshot.GetCommandId(), output)
	}
	if errResult := result.GetError(); errResult != nil {
		return "Reading shell command output failed: " + summarizeShellCommandError(errResult)
	}
	return "Shell command output fetched."
}

func summarizeTransferShellCommandControlToUserResult(result *pb.TransferShellCommandControlToUserResult) string {
	if finished := result.GetCommandFinished(); finished != nil {
		output := strings.TrimSpace(finished.GetOutput())
		if output == "" {
			output = "(no output)"
		}
		return fmt.Sprintf("Command finished after handing control to user.\nExit Code: %d\nOutput:\n%s", finished.GetExitCode(), output)
	}
	if snapshot := result.GetLongRunningCommandSnapshot(); snapshot != nil {
		output := strings.TrimSpace(snapshot.GetOutput())
		if output == "" {
			output = "(no output yet)"
		}
		return fmt.Sprintf("Command handed off to user.\nCommand ID: %s\nCurrent Output:\n%s", snapshot.GetCommandId(), output)
	}
	if errResult := result.GetError(); errResult != nil {
		return "Transfer shell command control failed: " + summarizeShellCommandError(errResult)
	}
	return "Shell command control transferred to user."
}

func summarizeShellCommandError(errResult *pb.ShellCommandError) string {
	switch errResult.GetType().(type) {
	case *pb.ShellCommandError_CommandNotFound:
		return "command not found"
	default:
		return "unknown shell command error"
	}
}

func joinFileContents(files []*pb.FileContent) string {
	if len(files) == 0 {
		return "No file content returned."
	}
	sections := make([]string, 0, len(files))
	for _, file := range files {
		sections = append(sections, formatFileContent(file))
	}
	return strings.Join(sections, "\n\n")
}

func formatFileContent(file *pb.FileContent) string {
	if file == nil {
		return "<missing file content>"
	}
	header := "File: " + file.GetFilePath()
	if lineRange := file.GetLineRange(); lineRange != nil {
		header = fmt.Sprintf("%s (lines %d-%d)", header, lineRange.GetStart(), lineRange.GetEnd())
	}
	content := strings.TrimSpace(file.GetContent())
	if content == "" {
		content = "(empty)"
	}
	return header + "\n" + content
}

// reasoningExhaustedNudge 是"推理耗尽输出预算"型空响应(finish_reason=
// length 且 content 为空)重试时追加在请求历史尾部的用户消息。它只进入
// 发给 LLM 的请求历史,不写进 conv.history(见 runAgentLoop 内注释)。
const reasoningExhaustedNudge = "Your previous response was cut off because it exceeded the output token limit. Skip the detailed analysis and directly provide your best answer or the next tool call. Be concise."

func (s *Server) runAgentLoop(ctx context.Context, w io.Writer, flusher http.Flusher, conv *Conversation, requestID, taskID string, isFollowUp bool, systemPrompt string, historyForLLM []openai.ChatCompletionMessageParamUnion, managedSSHTarget agent.ManagedSSHTarget) bool {
	// Only send CreateTask on the first request — it upgrades the client's
	// optimistic task. On follow-up requests (tool results), the task already exists.
	if !isFollowUp {
		s.sendCreateTask(w, flusher, taskID)
	}

	const maxLoops = 5
	// maxStreamRetries bounds how many times one loop iteration re-issues the
	// same StreamChat request after a watchdog stall or an empty response, so
	// each loop iteration gets 1 + maxStreamRetries chances in total.
	const maxStreamRetries = 2
	for i := 0; i < maxLoops; i++ {
		// Check if client disconnected before starting a new loop iteration.
		if ctx.Err() != nil {
			log.Printf("[LLM] loop=%d context cancelled before stream: %v", i, ctx.Err())
			return false
		}

		// 每轮请求前把当前任务清单注入 system prompt 尾部(WithExecutionContext
		// 已在调用方拼好,这里再往尾巴上加)。空列表时 formatTaskProgress 返回
		// 空串,不注入。todoList 只进本轮请求的 system prompt,不写 conv.history。
		effectiveSystemPrompt := systemPrompt + formatTaskProgress(conv.todoList)

		log.Printf("[LLM] loop=%d starting stream, task_id=%s history_len=%d", i, taskID, len(historyForLLM))

		var chunks []openai.ChatCompletionChunk
		var result llm.StreamResult
		var chunkCount int

		// Retry loop for this iteration's completion. An empty stream can be
		// retried safely. Once any SSE chunk has arrived, however, replaying the
		// request is unsafe: text may already be visible to the client and tool
		// calls may already be partially assembled. A later retry can duplicate
		// either, so report that partial-stream failure instead.
		//
		// requestHistory 是本轮迭代的请求历史,初始等于 historyForLLM。
		// 当检测到"推理耗尽输出预算"型空响应(finish_reason=length)时,会在
		// requestHistory 尾部追加一条 nudge 再重试。nudge 只存在于这个局部
		// 变量里:conv.history 的追加发生在重试循环之外(assistant 消息或
		// tool_call 消息),因此 nudge 永远不会写入正式对话历史;即使重试
		// 成功入库,历史里也看不到这条提示。
		requestHistory := historyForLLM
		nudgeAdded := false
		for attempt := 0; ; {
			stream := conv.client.StreamChat(ctx, effectiveSystemPrompt, requestHistory)

			chunks = nil
			chunkCount = 0
			textChars := 0

			// Fixed message ID so AppendToMessageContent can target the same message
			outputMsgID := uuid.New().String()
			var firstSent bool

			for stream.Next() {
				// Check for client disconnect. Without this, a dead connection
				// stays in CLOSE_WAIT until the handler unwinds.
				if ctx.Err() != nil {
					log.Printf("[LLM] loop=%d client disconnected mid-stream, aborting", i)
					return false
				}

				chunk := stream.Current()
				chunks = append(chunks, chunk)
				chunkCount++

				for _, choice := range chunk.Choices {
					// Debug: log raw delta JSON on early chunks to inspect reasoning_content
					if chunkCount <= 3 {
						rawDelta := choice.Delta.RawJSON()
						if len(rawDelta) > 500 {
							rawDelta = rawDelta[:500] + "..."
						}
						log.Printf("[LLM] loop=%d chunk=%d delta_raw=%s", i, chunkCount, rawDelta)
					}
					if choice.Delta.Content != "" {
						textChars += len(choice.Delta.Content)
						if !firstSent {
							s.sendFirstTextChunk(w, flusher, taskID, requestID, outputMsgID, choice.Delta.Content)
							firstSent = true
						} else {
							s.sendAppendText(w, flusher, taskID, outputMsgID, choice.Delta.Content)
						}
					}
				}
			}

			log.Printf("[LLM] loop=%d stream done: chunks=%d text_chars=%d", i, chunkCount, textChars)

			if err := stream.Err(); err != nil {
				if errors.Is(err, llm.ErrStreamStall) && ctx.Err() == nil {
					if chunkCount == 0 && attempt < maxStreamRetries {
						attempt++
						log.Printf("[LLM-WATCHDOG] loop=%d stream stalled before first chunk, retrying request (%d/%d)", i, attempt, maxStreamRetries)
						continue
					}
					if chunkCount > 0 {
						err = fmt.Errorf("%w; partial response (%d chunks) was not retried to avoid duplicate output or tool calls", err, chunkCount)
						log.Printf("[LLM-WATCHDOG] loop=%d stream stalled after %d chunks; not retrying partial response", i, chunkCount)
					} else {
						log.Printf("[LLM-WATCHDOG] loop=%d stream stalled before first chunk; retries exhausted", i)
					}
				}
				log.Printf("[LLM] loop=%d STREAM ERROR: %v", i, err)
				// If the client disconnected, don't try to write an error event —
				// the connection is already closed.
				if ctx.Err() != nil {
					log.Printf("[LLM] loop=%d context also cancelled, skipping error event", i)
					return false
				}
				s.sendFinishError(w, flusher, err.Error())
				return false
			}

			result = llm.CollectStreamResult(chunks)
			rcPreview := result.ReasoningContent
			if len(rcPreview) > 200 {
				rcPreview = rcPreview[:200] + "..."
			}
			log.Printf("[LLM] loop=%d result: text_len=%d reasoning_len=%d is_tool=%v reasoning_preview=%q", i, len(result.Text), len(result.ReasoningContent), result.IsToolCall, rcPreview)

			if len(chunks) > 0 {
				last := chunks[len(chunks)-1]
				for _, choice := range last.Choices {
					log.Printf("[LLM] loop=%d finish_reason=%q content_len=%d", i, choice.FinishReason, len(choice.Delta.Content))
				}
			}

			// Empty non-tool responses (observed after long DeepSeek reasoning)
			// are retried like stalls instead of failing the task immediately.
			if result.Text == "" && !result.IsToolCall && ctx.Err() == nil {
				// finish_reason=length 说明推理模型把输出预算(max_tokens)
				// 全部耗在推理上,content 被截断为空。原样重试大概率再次
				// 长推理、再次撞顶(评测实锤白烧预算),因此改为在请求历史
				// 尾部追加 nudge,要求模型跳过逐步分析直接作答。
				reasoningExhausted := result.FinishReason == "length"
				if attempt < maxStreamRetries {
					attempt++
					if reasoningExhausted {
						log.Printf("[LLM-WATCHDOG] loop=%d reasoning exhausted output budget (finish=length, reasoning_len=%d, chunks=%d), retrying with brevity nudge (%d/%d)", i, len(result.ReasoningContent), chunkCount, attempt, maxStreamRetries)
						if !nudgeAdded {
							// 拷贝一份新切片再追加,避免共享底层数组影响
							// historyForLLM;nudge 只进本次及后续重试请求。
							nudged := make([]openai.ChatCompletionMessageParamUnion, 0, len(requestHistory)+1)
							nudged = append(nudged, requestHistory...)
							nudged = append(nudged, llm.MakeUserMessage(reasoningExhaustedNudge))
							requestHistory = nudged
							nudgeAdded = true
						}
					} else {
						log.Printf("[LLM-WATCHDOG] loop=%d empty non-tool response (chunks=%d), retrying same request (%d/%d)", i, chunkCount, attempt, maxStreamRetries)
					}
					continue
				}
				if reasoningExhausted {
					log.Printf("[LLM-WATCHDOG] loop=%d reasoning exhausted output budget (finish=length, reasoning_len=%d, chunks=%d), retries exhausted", i, len(result.ReasoningContent), chunkCount)
				} else {
					log.Printf("[LLM-WATCHDOG] loop=%d empty non-tool response (chunks=%d), retries exhausted", i, chunkCount)
				}
			}
			break
		}

		// Check if LLM wants to call tools
		if result.IsToolCall {
			for j, tc := range result.ToolCalls {
				log.Printf("[LLM] loop=%d tool_call[%d] name=%s id=%s args=%s", i, j, tc.Name, tc.ID, string(tc.Args))
			}
			supportedCalls, unsupportedResults := splitUnsupportedToolCalls(result.ToolCalls)
			for _, dr := range unsupportedResults {
				log.Printf("[LLM] loop=%d unsupported tool call, feeding synthetic result: %s", i, dr.Message)
			}
			// update_task_list 在服务端执行:校验 -> 更新 conv.todoList -> 生成
			// 工具结果,不转发给客户端。先于所有客户端工具处理——它和读工具并行
			// 无冲突,但必须赶在写工具生效前更新计划状态。
			serverCalls, clientCalls := splitServerSideToolCalls(supportedCalls)
			serverResults := make([]serverToolResult, 0, len(serverCalls))
			for _, tc := range serverCalls {
				prevTodoList := conv.todoList
				msg := executeUpdateTaskList(conv, tc)
				serverResults = append(serverResults, serverToolResult{ID: tc.ID, Message: msg})
				log.Printf("[LLM] loop=%d %s executed server-side: %s", i, tc.Name, msg)
				// 校验通过且状态变化时,推 todo 状态给客户端刷新 UI 面板
				if !taskListEqual(prevTodoList, conv.todoList) {
					s.sendTodoListUpdate(w, flusher, taskID, prevTodoList, conv.todoList)
				}
			}
			sshAllowed, sshDeniedResults := enforceManagedSSHPolicy(
				clientCalls,
				managedSSHTarget,
				conv.LastLongRunningCommandID,
				conv.history,
			)
			// Enforce path policy on file-write tool calls that passed the SSH guard.
			allowed, deniedResults := s.enforcePathPolicy(sshAllowed)
			// Always append the full assistant tool_call message with ALL IDs first
			// to preserve tool_call/tool_result pairing.
			conv.history = append(conv.history, llm.MakeAssistantToolCallMessage(result.ToolCalls, result.ReasoningContent))
			if len(allowed) == 0 && len(serverResults)+len(deniedResults)+len(sshDeniedResults)+len(unsupportedResults) > 0 {
				// Nothing to forward to the client (all calls were server-side or
				// denied). Append results for all and continue the loop.
				for _, sr := range serverResults {
					conv.history = append(conv.history, llm.MakeToolResultMessage(sr.ID, sr.Message))
				}
				for _, dr := range unsupportedResults {
					conv.history = append(conv.history, llm.MakeToolResultMessage(dr.ID, dr.Message))
				}
				for _, dr := range sshDeniedResults {
					conv.history = append(conv.history, llm.MakeToolResultMessage(dr.ID, dr.Message))
				}
				for _, dr := range deniedResults {
					conv.history = append(conv.history, llm.MakeToolResultMessage(dr.ID, dr.Message))
				}
				log.Printf("[LLM] loop=%d no client-bound tool calls (server_results=%d denied=%d), continuing loop", i, len(serverResults), len(deniedResults)+len(sshDeniedResults)+len(unsupportedResults))
				historyForLLM = conv.history
				continue
			}
			for _, sr := range serverResults {
				conv.history = append(conv.history, llm.MakeToolResultMessage(sr.ID, sr.Message))
			}
			for _, dr := range unsupportedResults {
				conv.history = append(conv.history, llm.MakeToolResultMessage(dr.ID, dr.Message))
			}
			for _, dr := range sshDeniedResults {
				conv.history = append(conv.history, llm.MakeToolResultMessage(dr.ID, dr.Message))
				log.Printf("[SSH-GUARD] %s", dr.Message)
			}
			if len(deniedResults) > 0 {
				// Some calls denied: append synthetic results for denied calls.
				for _, dr := range deniedResults {
					conv.history = append(conv.history, llm.MakeToolResultMessage(dr.ID, dr.Message))
					log.Printf("[PATH-POLICY] Denied write to %s: %s", dr.Path, dr.Message)
				}
			}
			if err := s.sendToolCalls(w, flusher, conv, taskID, allowed); err != nil {
				log.Printf("[LLM] loop=%d failed to send tool calls: %v", i, err)
				s.sendFinishError(w, flusher, err.Error())
				return false
			}
			return s.finishSuccessfulAgentLoop(ctx, w, flusher)
		}

		textPreview := result.Text
		if len(textPreview) > 300 {
			textPreview = textPreview[:300] + "..."
		}
		log.Printf("[LLM] loop=%d final_text len=%d preview=%q", i, len(result.Text), textPreview)

		if result.Text == "" {
			log.Printf("[LLM] loop=%d empty response, sending error", i)
			s.sendFinishError(w, flusher, "LLM returned empty response")
			return false
		}

		conv.history = append(conv.history, llm.MakeAssistantMessageWithReasoning(result.Text, result.ReasoningContent))

		log.Printf("[LLM] loop=%d sending Done finish event", i)
		return s.finishSuccessfulAgentLoop(ctx, w, flusher)
	}

	log.Printf("[LLM] Max tool loops reached")
	s.sendFinishError(w, flusher, "Max tool call loops exceeded")
	return false
}

type beforeAgentFinishKey struct{}

func (s *Server) finishSuccessfulAgentLoop(ctx context.Context, w io.Writer, flusher http.Flusher) bool {
	if beforeFinish, ok := ctx.Value(beforeAgentFinishKey{}).(func()); ok {
		// Commit the immutable extraction job before StreamFinished. The callback
		// never performs extraction or waits for a memory-model response.
		beforeFinish()
	}
	s.sendEvent(w, flusher, finishEvent(&pb.ResponseEvent_StreamFinished_Done{}))
	return true
}

func (s *Server) sendCreateTask(w io.Writer, flusher http.Flusher, taskID string) {
	log.Printf("[LLM] sending CreateTask for task_id=%s", taskID)
	s.sendEvent(w, flusher, &pb.ResponseEvent{
		Type: &pb.ResponseEvent_ClientActions_{
			ClientActions: &pb.ResponseEvent_ClientActions{
				Actions: []*pb.ClientAction{
					{
						Action: &pb.ClientAction_CreateTask_{
							CreateTask: &pb.ClientAction_CreateTask{
								Task: &pb.Task{Id: taskID},
							},
						},
					},
				},
			},
		},
	})
}

// sendFirstTextChunk sends the first text delta via AddMessagesToTask, creating
// the message that subsequent AppendToMessageContent calls will append to.
func (s *Server) sendFirstTextChunk(w io.Writer, flusher http.Flusher, taskID, requestID, msgID, delta string) {
	msg := &pb.Message{
		Id:        msgID,
		TaskId:    taskID,
		RequestId: requestID,
		Timestamp: timestamppb.Now(),
		Message: &pb.Message_AgentOutput_{
			AgentOutput: &pb.Message_AgentOutput{
				Text: delta,
			},
		},
	}
	s.sendEvent(w, flusher, &pb.ResponseEvent{
		Type: &pb.ResponseEvent_ClientActions_{
			ClientActions: &pb.ResponseEvent_ClientActions{
				Actions: []*pb.ClientAction{
					{
						Action: &pb.ClientAction_AddMessagesToTask_{
							AddMessagesToTask: &pb.ClientAction_AddMessagesToTask{
								TaskId:   taskID,
								Messages: []*pb.Message{msg},
							},
						},
					},
				},
			},
		},
	})
}

// sendAppendText appends a text delta to an existing AgentOutput message.
func (s *Server) sendAppendText(w io.Writer, flusher http.Flusher, taskID, msgID, delta string) {
	msg := &pb.Message{
		Id: msgID,
		Message: &pb.Message_AgentOutput_{
			AgentOutput: &pb.Message_AgentOutput{
				Text: delta,
			},
		},
	}
	s.sendEvent(w, flusher, &pb.ResponseEvent{
		Type: &pb.ResponseEvent_ClientActions_{
			ClientActions: &pb.ResponseEvent_ClientActions{
				Actions: []*pb.ClientAction{
					{
						Action: &pb.ClientAction_AppendToMessageContent_{
							AppendToMessageContent: &pb.ClientAction_AppendToMessageContent{
								TaskId:  taskID,
								Message: msg,
								Mask: &fieldmaskpb.FieldMask{
									Paths: []string{"agent_output.text"},
								},
							},
						},
					},
				},
			},
		},
	})
}

func (s *Server) sendFinishError(w io.Writer, flusher http.Flusher, message string) {
	s.sendEvent(w, flusher, &pb.ResponseEvent{
		Type: &pb.ResponseEvent_Finished{
			Finished: &pb.ResponseEvent_StreamFinished{
				Reason: &pb.ResponseEvent_StreamFinished_InternalError_{
					InternalError: &pb.ResponseEvent_StreamFinished_InternalError{
						Message: message,
					},
				},
			},
		},
	})
}

func (s *Server) sendIncrementalText(w io.Writer, flusher http.Flusher, taskID, requestID, delta string) {
	msg := &pb.Message{
		Id:        uuid.New().String(),
		TaskId:    taskID,
		RequestId: requestID,
		Timestamp: timestamppb.Now(),
		Message: &pb.Message_AgentOutput_{
			AgentOutput: &pb.Message_AgentOutput{
				Text: delta,
			},
		},
	}

	s.sendEvent(w, flusher, &pb.ResponseEvent{
		Type: &pb.ResponseEvent_ClientActions_{
			ClientActions: &pb.ResponseEvent_ClientActions{
				Actions: []*pb.ClientAction{
					{
						Action: &pb.ClientAction_AddMessagesToTask_{
							AddMessagesToTask: &pb.ClientAction_AddMessagesToTask{
								TaskId:   taskID,
								Messages: []*pb.Message{msg},
							},
						},
					},
				},
			},
		},
	})
}

// sendTodoListUpdate 把 todo 状态推给客户端刷新 UI 面板。
// 通过 ClientAction AddMessagesToTask 发 UpdateTodos 消息。
func (s *Server) sendTodoListUpdate(w io.Writer, flusher http.Flusher, taskID string, prev, curr []TaskItem) {
	ops := todoListToProto(prev, curr)
	if len(ops) == 0 {
		return
	}
	msgs := make([]*pb.Message, 0, len(ops))
	for _, op := range ops {
		msgs = append(msgs, &pb.Message{
			Id:     uuid.New().String(),
			TaskId: taskID,
			Message: &pb.Message_UpdateTodos_{
				UpdateTodos: op,
			},
		})
	}
	s.sendEvent(w, flusher, &pb.ResponseEvent{
		Type: &pb.ResponseEvent_ClientActions_{
			ClientActions: &pb.ResponseEvent_ClientActions{
				Actions: []*pb.ClientAction{
					{
						Action: &pb.ClientAction_AddMessagesToTask_{
							AddMessagesToTask: &pb.ClientAction_AddMessagesToTask{
								TaskId:   taskID,
								Messages: msgs,
							},
						},
					},
				},
			},
		},
	})
}

// unsupportedToolDenial describes a tool call the adapter cannot execute.
// A synthetic tool result is fed back to the LLM so it can recover with
// supported tools instead of aborting the whole stream.
type unsupportedToolDenial struct {
	ID      string
	Message string
}

// splitUnsupportedToolCalls partitions tool calls into ones the adapter can
// execute and ones it cannot. The Warp client system prompt advertises tools
// this adapter does not implement (e.g. run_scheduler_command), so models
// occasionally call them; failing the stream is worse than telling the model.
func splitUnsupportedToolCalls(toolCalls []llm.ToolCall) ([]llm.ToolCall, []unsupportedToolDenial) {
	var supported []llm.ToolCall
	var denied []unsupportedToolDenial
	for _, tc := range toolCalls {
		if _, ok := supportedTools[tc.Name]; ok {
			supported = append(supported, tc)
			continue
		}
		denied = append(denied, unsupportedToolDenial{
			ID: tc.ID,
			Message: fmt.Sprintf(
				"Tool %q is not supported by this local adapter and was not executed. Supported tools: read_files, grep, file_glob, file_glob_v2, run_shell_command, read_shell_command_output, transfer_shell_command_control_to_user, apply_file_diffs, search_codebase, update_task_list. Continue using the supported tools, or explain the limitation to the user. Do not call %q again.",
				tc.Name, tc.Name,
			),
		})
	}
	return supported, denied
}

func (s *Server) sendToolCalls(w io.Writer, flusher http.Flusher, conv *Conversation, taskID string, toolCalls []llm.ToolCall) error {
	msgs := make([]*pb.Message, 0, len(toolCalls))
	for _, tc := range toolCalls {
		tcMsg := &pb.Message_ToolCall{
			ToolCallId: tc.ID,
		}
		// Build tool variant inline since isMessage_ToolCall_Tool is unexported.
		switch tc.Name {
		case "read_files":
			var args struct {
				Files []struct {
					Name       string `json:"name"`
					LineRanges []struct {
						Start int `json:"start"`
						End   int `json:"end"`
					} `json:"line_ranges"`
				} `json:"files"`
			}
			json.Unmarshal(tc.Args, &args)
			files := make([]*pb.Message_ToolCall_ReadFiles_File, 0, len(args.Files))
			for _, f := range args.Files {
				ranges := make([]*pb.FileContentLineRange, 0, len(f.LineRanges))
				for _, lr := range f.LineRanges {
					ranges = append(ranges, &pb.FileContentLineRange{
						Start: uint32(lr.Start),
						End:   uint32(lr.End),
					})
				}
				files = append(files, &pb.Message_ToolCall_ReadFiles_File{
					Name:       f.Name,
					LineRanges: ranges,
				})
			}
			tcMsg.Tool = &pb.Message_ToolCall_ReadFiles_{
				ReadFiles: &pb.Message_ToolCall_ReadFiles{Files: files},
			}

		case "grep":
			var args struct {
				Queries []string `json:"queries"`
				Path    string   `json:"path"`
			}
			json.Unmarshal(tc.Args, &args)
			tcMsg.Tool = &pb.Message_ToolCall_Grep_{
				Grep: &pb.Message_ToolCall_Grep{
					Queries: args.Queries,
					Path:    args.Path,
				},
			}

		case "file_glob":
			var args struct {
				Patterns  []string `json:"patterns"`
				Path      string   `json:"path"`
				SearchDir string   `json:"search_dir"`
			}
			json.Unmarshal(tc.Args, &args)
			path := args.Path
			if path == "" {
				path = args.SearchDir
			}
			tcMsg.Tool = &pb.Message_ToolCall_FileGlob_{
				FileGlob: &pb.Message_ToolCall_FileGlob{
					Patterns: args.Patterns,
					Path:     path,
				},
			}

		case "file_glob_v2":
			var args struct {
				Patterns   []string `json:"patterns"`
				SearchDir  string   `json:"search_dir"`
				MaxMatches int32    `json:"max_matches"`
				MaxDepth   int32    `json:"max_depth"`
				MinDepth   int32    `json:"min_depth"`
			}
			json.Unmarshal(tc.Args, &args)
			tcMsg.Tool = &pb.Message_ToolCall_FileGlobV2_{
				FileGlobV2: &pb.Message_ToolCall_FileGlobV2{
					Patterns:   args.Patterns,
					SearchDir:  args.SearchDir,
					MaxMatches: args.MaxMatches,
					MaxDepth:   args.MaxDepth,
					MinDepth:   args.MinDepth,
				},
			}

		case "run_shell_command":
			var args struct {
				Command      string `json:"command"`
				IsReadOnly   bool   `json:"is_read_only"`
				IsRisky      bool   `json:"is_risky"`
				RiskCategory string `json:"risk_category"`
			}
			json.Unmarshal(tc.Args, &args)
			if strings.TrimSpace(args.Command) == "wait" && conv.LastLongRunningCommandID != "" {
				tcMsg.Tool = &pb.Message_ToolCall_ReadShellCommandOutput_{
					ReadShellCommandOutput: &pb.Message_ToolCall_ReadShellCommandOutput{
						CommandId: conv.LastLongRunningCommandID,
						Delay: &pb.Message_ToolCall_ReadShellCommandOutput_OnCompletion{
							OnCompletion: &emptypb.Empty{},
						},
					},
				}
				break
			}
			tcMsg.Tool = &pb.Message_ToolCall_RunShellCommand_{
				RunShellCommand: &pb.Message_ToolCall_RunShellCommand{
					Command:      isolateShellCommand(args.Command),
					IsReadOnly:   args.IsReadOnly,
					IsRisky:      args.IsRisky,
					RiskCategory: parseRiskCategory(args.RiskCategory),
				},
			}

		case "read_shell_command_output":
			var args struct {
				CommandID         string `json:"command_id"`
				WaitForCompletion *bool  `json:"wait_for_completion"`
				DurationSeconds   *int64 `json:"duration_seconds"`
			}
			json.Unmarshal(tc.Args, &args)
			commandID := args.CommandID
			if commandID == "" {
				commandID = conv.LastLongRunningCommandID
			}
			read := &pb.Message_ToolCall_ReadShellCommandOutput{
				CommandId: commandID,
			}
			if args.WaitForCompletion == nil || *args.WaitForCompletion {
				read.Delay = &pb.Message_ToolCall_ReadShellCommandOutput_OnCompletion{
					OnCompletion: &emptypb.Empty{},
				}
			} else {
				seconds := int64(1)
				if args.DurationSeconds != nil && *args.DurationSeconds > 0 {
					seconds = *args.DurationSeconds
				}
				read.Delay = &pb.Message_ToolCall_ReadShellCommandOutput_Duration{
					Duration: durationpb.New(time.Duration(seconds) * time.Second),
				}
			}
			tcMsg.Tool = &pb.Message_ToolCall_ReadShellCommandOutput_{
				ReadShellCommandOutput: read,
			}

		case "transfer_shell_command_control_to_user":
			var args struct {
				Reason string `json:"reason"`
			}
			json.Unmarshal(tc.Args, &args)
			tcMsg.Tool = &pb.Message_ToolCall_TransferShellCommandControlToUser_{
				TransferShellCommandControlToUser: &pb.Message_ToolCall_TransferShellCommandControlToUser{
					Reason: args.Reason,
				},
			}

		case "apply_file_diffs":
			var args struct {
				Summary string `json:"summary"`
				Diffs   []struct {
					FilePath string `json:"file_path"`
					Search   string `json:"search"`
					Replace  string `json:"replace"`
				} `json:"diffs"`
				NewFiles []struct {
					FilePath string `json:"file_path"`
					Content  string `json:"content"`
				} `json:"new_files"`
				DeletedFiles []struct {
					FilePath string `json:"file_path"`
				} `json:"deleted_files"`
			}
			json.Unmarshal(tc.Args, &args)
			pbDiffs := make([]*pb.Message_ToolCall_ApplyFileDiffs_FileDiff, 0, len(args.Diffs))
			for _, d := range args.Diffs {
				pbDiffs = append(pbDiffs, &pb.Message_ToolCall_ApplyFileDiffs_FileDiff{
					FilePath: d.FilePath,
					Search:   d.Search,
					Replace:  d.Replace,
				})
			}
			pbNewFiles := make([]*pb.Message_ToolCall_ApplyFileDiffs_NewFile, 0, len(args.NewFiles))
			for _, nf := range args.NewFiles {
				pbNewFiles = append(pbNewFiles, &pb.Message_ToolCall_ApplyFileDiffs_NewFile{
					FilePath: nf.FilePath,
					Content:  nf.Content,
				})
			}
			pbDeleted := make([]*pb.Message_ToolCall_ApplyFileDiffs_DeleteFile, 0, len(args.DeletedFiles))
			for _, df := range args.DeletedFiles {
				pbDeleted = append(pbDeleted, &pb.Message_ToolCall_ApplyFileDiffs_DeleteFile{
					FilePath: df.FilePath,
				})
			}
			tcMsg.Tool = &pb.Message_ToolCall_ApplyFileDiffs_{
				ApplyFileDiffs: &pb.Message_ToolCall_ApplyFileDiffs{
					Summary:      args.Summary,
					Diffs:        pbDiffs,
					NewFiles:     pbNewFiles,
					DeletedFiles: pbDeleted,
				},
			}

		case "search_codebase":
			var args struct {
				Query        string   `json:"query"`
				PathFilters  []string `json:"path_filters"`
				CodebasePath string   `json:"codebase_path"`
			}
			json.Unmarshal(tc.Args, &args)
			tcMsg.Tool = &pb.Message_ToolCall_SearchCodebase_{
				SearchCodebase: &pb.Message_ToolCall_SearchCodebase{
					Query:        args.Query,
					PathFilters:  args.PathFilters,
					CodebasePath: args.CodebasePath,
				},
			}
		default:
			return fmt.Errorf("tool %s is not supported by this local adapter", tc.Name)
		}

		msg := &pb.Message{
			Id:        uuid.New().String(),
			TaskId:    taskID,
			Timestamp: timestamppb.Now(),
			Message: &pb.Message_ToolCall_{
				ToolCall: tcMsg,
			},
		}
		msgs = append(msgs, msg)
	}

	s.sendEvent(w, flusher, &pb.ResponseEvent{
		Type: &pb.ResponseEvent_ClientActions_{
			ClientActions: &pb.ResponseEvent_ClientActions{
				Actions: []*pb.ClientAction{
					{
						Action: &pb.ClientAction_AddMessagesToTask_{
							AddMessagesToTask: &pb.ClientAction_AddMessagesToTask{
								TaskId:   taskID,
								Messages: msgs,
							},
						},
					},
				},
			},
		},
	})
	return nil
}

// isolateShellCommand executes every model-generated command in a subshell.
// Warp reuses an interactive shell for tool calls, so commands such as set -x,
// set -e, cd, export, alias, and trap must not leak into Warp's shell hooks or
// later tool calls. The subshell preserves stdout, stderr, and the exit status.
func isolateShellCommand(command string) string {
	return "(\n" + command + "\n)"
}

func parseRiskCategory(s string) pb.RiskCategory {
	switch s {
	case "RISK_CATEGORY_READ_ONLY":
		return pb.RiskCategory_RISK_CATEGORY_READ_ONLY
	case "RISK_CATEGORY_TRIVIAL_LOCAL_CHANGE":
		return pb.RiskCategory_RISK_CATEGORY_TRIVIAL_LOCAL_CHANGE
	case "RISK_CATEGORY_NONTRIVIAL_LOCAL_CHANGE":
		return pb.RiskCategory_RISK_CATEGORY_NONTRIVIAL_LOCAL_CHANGE
	case "RISK_CATEGORY_EXTERNAL_CHANGE":
		return pb.RiskCategory_RISK_CATEGORY_EXTERNAL_CHANGE
	case "RISK_CATEGORY_RISKY":
		return pb.RiskCategory_RISK_CATEGORY_RISKY
	default:
		return pb.RiskCategory_RISK_CATEGORY_UNSPECIFIED
	}
}

func finishEvent(done *pb.ResponseEvent_StreamFinished_Done) *pb.ResponseEvent {
	return &pb.ResponseEvent{
		Type: &pb.ResponseEvent_Finished{
			Finished: &pb.ResponseEvent_StreamFinished{
				Reason: &pb.ResponseEvent_StreamFinished_Done_{
					Done: done,
				},
			},
		},
	}
}

func (s *Server) sendEvent(w io.Writer, flusher http.Flusher, event *pb.ResponseEvent) {
	data, err := proto.Marshal(event)
	if err != nil {
		log.Printf("[EVENT] Failed to marshal event: %v", err)
		return
	}
	encoded := base64.URLEncoding.EncodeToString(data)
	fmt.Fprintf(w, "data: %s\n\n", encoded)
	flusher.Flush()

	// Log event type for debugging
	switch event.Type.(type) {
	case *pb.ResponseEvent_Init:
		init := event.GetInit()
		log.Printf("[EVENT] StreamInit conv=%s req=%s run=%s", init.GetConversationId(), init.GetRequestId(), init.GetRunId())
	case *pb.ResponseEvent_ClientActions_:
		actions := event.GetClientActions()
		for _, a := range actions.GetActions() {
			switch a.Action.(type) {
			case *pb.ClientAction_AddMessagesToTask_:
				amt := a.GetAddMessagesToTask()
				for _, m := range amt.GetMessages() {
					switch m.Message.(type) {
					case *pb.Message_AgentOutput_:
						log.Printf("[EVENT] ClientAction AddMessagesToTask AgentOutput task=%s len=%d", amt.GetTaskId(), len(m.GetAgentOutput().GetText()))
					case *pb.Message_ToolCall_:
						tc := m.GetToolCall()
						log.Printf("[EVENT] ClientAction AddMessagesToTask ToolCall task=%s tool=%T", amt.GetTaskId(), tc.GetTool())
					default:
						log.Printf("[EVENT] ClientAction AddMessagesToTask task=%s msg_type=%T", amt.GetTaskId(), m.Message)
					}
				}
			default:
				log.Printf("[EVENT] ClientAction type=%T", a.Action)
			}
		}
	case *pb.ResponseEvent_Finished:
		fin := event.GetFinished()
		log.Printf("[EVENT] StreamFinished reason=%T", fin.GetReason())
	default:
		log.Printf("[EVENT] unknown type=%T", event.Type)
	}
}

// normalizeConversationHistory removes malformed tool-call history so strict
// providers like DeepSeek never receive:
// 1. assistant tool_calls messages without matching tool_result messages
// 2. stray tool_result messages with no preceding assistant tool_calls
//
// This is the key recovery path for "interrupt A, then immediately do B":
// once the previous tool round is abandoned, we must drop that incomplete
// assistant/tool sequence before sending the next user query upstream.
func normalizeConversationHistory(history []openai.ChatCompletionMessageParamUnion) ([]openai.ChatCompletionMessageParamUnion, bool) {
	if len(history) == 0 {
		return history, false
	}

	normalized := make([]openai.ChatCompletionMessageParamUnion, 0, len(history))
	changed := false

	for i := 0; i < len(history); i++ {
		msg := history[i]

		if msg.OfTool != nil {
			log.Printf("[HISTORY] pruning stray tool_result message tool_call_id=%q", msg.OfTool.ToolCallID)
			changed = true
			continue
		}

		if toolCallIDs, ok := assistantToolCallIDs(msg); ok {
			expected := make(map[string]struct{}, len(toolCallIDs))
			malformedToolCallIDs := false
			for _, id := range toolCallIDs {
				if id == "" {
					malformedToolCallIDs = true
					continue
				}
				expected[id] = struct{}{}
			}

			j := i + 1
			toolMsgs := make([]openai.ChatCompletionMessageParamUnion, 0)
			matched := make(map[string]struct{}, len(expected))
			for j < len(history) && history[j].OfTool != nil {
				toolMsg := history[j]
				toolMsgs = append(toolMsgs, toolMsg)
				if _, ok := expected[toolMsg.OfTool.ToolCallID]; ok {
					matched[toolMsg.OfTool.ToolCallID] = struct{}{}
				}
				j++
			}

			if malformedToolCallIDs || len(expected) == 0 || len(matched) != len(expected) {
				log.Printf(
					"[HISTORY] pruning incomplete assistant tool_calls message expected=%d matched=%d trailing_tool_results=%d malformed_ids=%v",
					len(expected),
					len(matched),
					len(toolMsgs),
					malformedToolCallIDs,
				)
				changed = true
				i = j - 1
				continue
			}

			normalized = append(normalized, msg)
			normalized = append(normalized, toolMsgs...)
			i = j - 1
			continue
		}

		normalized = append(normalized, msg)
	}

	return normalized, changed
}

func assistantToolCallIDs(msg openai.ChatCompletionMessageParamUnion) ([]string, bool) {
	if msg.OfAssistant == nil {
		return nil, false
	}

	if len(msg.OfAssistant.ToolCalls) > 0 {
		ids := make([]string, 0, len(msg.OfAssistant.ToolCalls))
		for _, tc := range msg.OfAssistant.ToolCalls {
			ids = append(ids, tc.ID)
		}
		return ids, true
	}

	raw, err := json.Marshal(msg)
	if err != nil {
		return nil, false
	}

	var payload struct {
		Role      string `json:"role"`
		ToolCalls []struct {
			ID string `json:"id"`
		} `json:"tool_calls"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, false
	}
	if payload.Role != "assistant" || len(payload.ToolCalls) == 0 {
		return nil, false
	}

	ids := make([]string, 0, len(payload.ToolCalls))
	for _, tc := range payload.ToolCalls {
		ids = append(ids, tc.ID)
	}
	return ids, true
}

var _ = json.RawMessage{}

type memoryStatusResponse struct {
	Enabled           bool   `json:"enabled"`
	SessionEnabled    bool   `json:"session_enabled"`
	AutoEnabled       bool   `json:"auto_enabled"`
	BaseDir           string `json:"base_dir"`
	CurrentProjectKey string `json:"current_project_key,omitempty"`
	ContextWindow     struct {
		Tokens            int `json:"tokens"`
		CompactionAtChars int `json:"compaction_at_chars"`
		KeepRecentChars   int `json:"keep_recent_chars"`
		MaxRecentMessages int `json:"max_recent_messages"`
	} `json:"context_window"`
	Session struct {
		NotesExists bool `json:"notes_exists"`
		NotesBytes  int  `json:"notes_bytes"`
	} `json:"session"`
	Project struct {
		MemoryCount       int  `json:"memory_count"`
		MemoryIndexExists bool `json:"memory_index_exists"`
	} `json:"project"`
	QueueAvailable bool              `json:"queue_available"`
	Queue          memory.QueueStats `json:"queue"`
}

func (s *Server) handleMemoryStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	var resp memoryStatusResponse
	if s.memoryStore == nil {
		resp.Enabled = false
		_ = json.NewEncoder(w).Encode(resp)
		return
	}
	resp.Enabled = s.cfg.Memory.IsEnabled()
	resp.SessionEnabled = s.cfg.Memory.IsSessionEnabled()
	resp.AutoEnabled = s.cfg.Memory.IsAutoEnabled()
	resp.BaseDir = s.memoryStore.BaseDir()
	if s.memoryQueue != nil {
		resp.QueueAvailable = true
		resp.Queue, _ = s.memoryQueue.Stats(r.Context())
	}
	compactionCfg := memory.CompactionConfigForContextWindow(s.cfg.Memory.ContextWindowTokens)
	resp.ContextWindow.Tokens = compactionCfg.ContextWindowTokens
	resp.ContextWindow.CompactionAtChars = compactionCfg.MaxHistoryChars
	resp.ContextWindow.KeepRecentChars = compactionCfg.MinRecentChars
	resp.ContextWindow.MaxRecentMessages = compactionCfg.MaxRecentMessages
	resp.CurrentProjectKey = r.URL.Query().Get("project_key")
	if resp.CurrentProjectKey != "" && memory.SanitizeKey(resp.CurrentProjectKey) != resp.CurrentProjectKey {
		http.Error(w, "project_key contains invalid characters", http.StatusBadRequest)
		return
	}
	if resp.CurrentProjectKey == "" {
		resp.CurrentProjectKey = s.resolveProjectKey("")
	}
	if convID := r.URL.Query().Get("conversation_id"); convID != "" {
		if info, err := os.Stat(filepath.Join(s.memoryStore.BaseDir(), memory.SessionNotesRelPath(convID))); err == nil {
			resp.Session.NotesExists = true
			resp.Session.NotesBytes = int(info.Size())
		}
	}
	if resp.CurrentProjectKey != "" {
		memDir := filepath.Join(s.memoryStore.BaseDir(), "projects", resp.CurrentProjectKey, "memory")
		if _, err := os.Stat(filepath.Join(memDir, "MEMORY.md")); err == nil {
			resp.Project.MemoryIndexExists = true
		}
		if entries, err := os.ReadDir(memDir); err == nil {
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") && e.Name() != "MEMORY.md" {
					resp.Project.MemoryCount++
				}
			}
		}
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleMemoryClearSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if s.memoryStore == nil {
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "memory not initialized"})
		return
	}
	var body struct {
		ConversationID string `json:"conversation_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ConversationID == "" {
		http.Error(w, "conversation_id required", http.StatusBadRequest)
		return
	}
	if memory.SanitizeKey(body.ConversationID) != body.ConversationID {
		http.Error(w, "conversation_id contains invalid characters", http.StatusBadRequest)
		return
	}
	s.memoryMutationMu.Lock()
	defer s.memoryMutationMu.Unlock()
	if s.memoryQueue != nil {
		if err := s.memoryQueue.ClearSession(r.Context(), body.ConversationID); err != nil {
			http.Error(w, "failed to clear queued session memory", http.StatusInternalServerError)
			return
		}
	}
	if err := s.memoryStore.ClearSession(body.ConversationID); err != nil {
		log.Printf("[MEMORY] Failed to clear session conv=%s: %v", body.ConversationID, err)
		http.Error(w, "failed to clear session", http.StatusInternalServerError)
		return
	}
	_ = s.memoryStore.AppendEvent(memory.Event{
		Type:           "session_memory_cleared",
		ConversationID: body.ConversationID,
	})
	log.Printf("[MEMORY] Cleared session memory for conv=%s", body.ConversationID)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "cleared"})
}

func (s *Server) handleMemoryClearProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if s.memoryStore == nil {
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "memory not initialized"})
		return
	}
	var body struct {
		ProjectKey string `json:"project_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ProjectKey == "" {
		http.Error(w, "project_key required", http.StatusBadRequest)
		return
	}
	if memory.SanitizeKey(body.ProjectKey) != body.ProjectKey {
		http.Error(w, "project_key contains invalid characters", http.StatusBadRequest)
		return
	}
	s.memoryMutationMu.Lock()
	defer s.memoryMutationMu.Unlock()
	if s.memoryQueue != nil {
		if err := s.memoryQueue.ClearProject(r.Context(), body.ProjectKey); err != nil {
			http.Error(w, "failed to clear queued project memory", http.StatusInternalServerError)
			return
		}
	}
	if err := s.memoryStore.ClearProject(body.ProjectKey); err != nil {
		log.Printf("[MEMORY] Failed to clear project key=%s: %v", body.ProjectKey, err)
		http.Error(w, "failed to clear project", http.StatusInternalServerError)
		return
	}
	_ = s.memoryStore.AppendEvent(memory.Event{
		Type:       "project_memory_cleared",
		ProjectKey: body.ProjectKey,
	})
	log.Printf("[MEMORY] Cleared project memory for key=%s", body.ProjectKey)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "cleared"})
}

// buildMemoryContext augments the system prompt with auto-memories and session notes,
// and returns a potentially compacted history for the LLM.
func (s *Server) buildMemoryContext(conv *Conversation, convID string) (string, []openai.ChatCompletionMessageParamUnion) {
	systemPrompt := agent.SystemPrompt
	history := conv.history

	// 1. Inject auto-memories into system prompt.
	if s.cfg.Memory.IsAutoEnabled() {
		projectKey := conv.ProjectKey
		if projectKey == "" {
			projectKey = s.resolveProjectKey(convID)
		}
		memDir := filepath.Join(s.memoryStore.BaseDir(), "projects", projectKey, "memory")
		if headers, err := memory.ScanMemoryHeaders(memDir); err == nil && len(headers) > 0 {
			in := memory.SelectInput{
				Query: s.lastUserQuery(conv),
				Limit: s.cfg.Memory.MaxProjectMemory,
				Now:   time.Now(),
			}
			selected := memory.SelectMemories(headers, in)
			if len(selected) > 0 {
				var sb strings.Builder
				sb.WriteString(systemPrompt)
				sb.WriteString("\n\nProject memories selected for this request:\n")
				for _, m := range selected {
					sb.WriteString(fmt.Sprintf("<memory path=%q type=%q updated_at=%q>\n", m.Header.Path, m.Header.Type, m.Header.UpdatedAt.Format("2006-01-02")))
					if data, err := s.memoryStore.ReadFile("projects/" + projectKey + "/memory/" + m.Header.Path); err == nil {
						content := string(data)
						// Strip frontmatter for injection.
						if idx := strings.Index(content, "\n---\n"); idx >= 0 {
							content = content[idx+5:]
						}
						sb.WriteString(content)
					}
					sb.WriteString("\n</memory>\n")
					if m.FreshnessWarning != "" {
						sb.WriteString(fmt.Sprintf("(warning: %s)\n", m.FreshnessWarning))
					}
				}
				systemPrompt = sb.String()
				log.Printf("[MEMORY] Injected %d auto-memories for conv=%s", len(selected), convID)
			}
		}
	}

	// 2. Inject session notes and check compaction.
	if s.cfg.Memory.IsSessionEnabled() {
		notes, err := s.memoryStore.ReadSessionNotes(convID)
		if err == nil && notes != "" {
			if memory.IsEmptySessionNotes(notes) {
				log.Printf("[MEMORY] Skipping compaction for conv=%s: notes are empty template", convID)
			} else {
				compactionMessages := s.historyToCompactionMsgs(history)
				cfg := memory.CompactionConfigForContextWindow(s.cfg.Memory.ContextWindowTokens)
				result := memory.ShouldCompact(compactionMessages, notes, cfg)
				if result.ShouldCompact {
					summaryMsg := llm.MakeUserMessage("[Session Memory Summary]\n" + notes + "\n[End of Session Memory. Continue from here with the recent messages below.]")
					if result.StartIndex < len(history) {
						recent := make([]openai.ChatCompletionMessageParamUnion, 0, len(history)-result.StartIndex+1)
						recent = append(recent, summaryMsg)
						recent = append(recent, history[result.StartIndex:]...)
						history = recent
						log.Printf("[MEMORY] Compacted history for conv=%s: boundary=%d original=%d kept=%d", convID, result.StartIndex, len(conv.history), len(recent))
					}
				}
			}
		}
	}

	return systemPrompt, history
}

// summarizeDelta creates a text summary of new messages for the extractor.
func (s *Server) summarizeDelta(delta []openai.ChatCompletionMessageParamUnion) string {
	var sb strings.Builder
	for i, msg := range delta {
		raw, err := json.Marshal(msg)
		if err != nil {
			continue
		}
		var payload struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		if json.Unmarshal(raw, &payload) != nil {
			continue
		}
		if payload.Content == "" {
			continue
		}
		// Truncate very long messages to keep prompt manageable.
		content := payload.Content
		if len(content) > 2000 {
			content = content[:2000] + "...(truncated)"
		}
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", payload.Role, content))
		if i > 30 {
			sb.WriteString("...(more messages truncated)\n")
			break
		}
	}
	return sb.String()
}

// lastUserQuery extracts the last user message from history for memory selection.
func (s *Server) lastUserQuery(conv *Conversation) string {
	for i := len(conv.history) - 1; i >= 0; i-- {
		msg := conv.history[i]
		if msg.OfUser != nil {
			raw, err := json.Marshal(msg)
			if err == nil {
				var payload struct {
					Content string `json:"content"`
				}
				if json.Unmarshal(raw, &payload) == nil && payload.Content != "" {
					return payload.Content
				}
			}
		}
	}
	return ""
}

// historyToCompactionMsgs converts openai history to simplified Msg for compaction.
func (s *Server) historyToCompactionMsgs(history []openai.ChatCompletionMessageParamUnion) []memory.Msg {
	msgs := make([]memory.Msg, 0, len(history))
	for _, h := range history {
		raw, err := json.Marshal(h)
		if err != nil {
			continue
		}
		var payload struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				ID string `json:"id"`
			} `json:"tool_calls"`
			ToolCallID string `json:"tool_call_id"`
		}
		if json.Unmarshal(raw, &payload) != nil {
			continue
		}
		m := memory.Msg{
			Role:         payload.Role,
			Content:      payload.Content,
			ToolResultID: payload.ToolCallID,
		}
		for _, tc := range payload.ToolCalls {
			m.ToolCallIDs = append(m.ToolCallIDs, tc.ID)
		}
		msgs = append(msgs, m)
	}
	return msgs
}

func estimateHistoryChars(history []openai.ChatCompletionMessageParamUnion) int {
	total := 0
	for _, h := range history {
		raw, err := json.Marshal(h)
		if err == nil {
			total += len(raw)
		}
	}
	return total
}

func countToolCalls(history []openai.ChatCompletionMessageParamUnion) int {
	count := 0
	for _, h := range history {
		if ids, ok := assistantToolCallIDs(h); ok {
			count += len(ids)
		}
	}
	return count
}

func lastAssistantHasToolCall(history []openai.ChatCompletionMessageParamUnion) bool {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].OfAssistant != nil {
			ids, ok := assistantToolCallIDs(history[i])
			return ok && len(ids) > 0
		}
	}
	return false
}

type pathPolicyDenial struct {
	ID      string
	Path    string
	Message string
}

type managedSSHPolicyDenial struct {
	ID      string
	Message string
}

func enforceManagedSSHPolicy(
	toolCalls []llm.ToolCall,
	target agent.ManagedSSHTarget,
	longRunningCommandID string,
	history []openai.ChatCompletionMessageParamUnion,
) ([]llm.ToolCall, []managedSSHPolicyDenial) {
	if target.Host == "" {
		return toolCalls, nil
	}

	var allowed []llm.ToolCall
	var denied []managedSSHPolicyDenial
	pendingRedundantSSH := longRunningCommandID != "" && historyHasRedundantSSH(history, target)

	for _, tc := range toolCalls {
		if tc.Name == "transfer_shell_command_control_to_user" && pendingRedundantSSH {
			denied = append(denied, managedSSHPolicyDenial{
				ID:      tc.ID,
				Message: "Blocked control transfer: the pending command is a redundant SSH connection to the host this terminal is already connected to. Do not ask for its password. Tell the user to cancel that stale SSH command once, then run subsequent commands directly in the current remote terminal.",
			})
			continue
		}
		if tc.Name != "run_shell_command" {
			allowed = append(allowed, tc)
			continue
		}

		var args struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(tc.Args, &args) != nil {
			allowed = append(allowed, tc)
			continue
		}
		host, redundant := agent.RedundantSSHHost(args.Command, target)
		if !redundant {
			allowed = append(allowed, tc)
			continue
		}
		denied = append(denied, managedSSHPolicyDenial{
			ID: tc.ID,
			Message: fmt.Sprintf(
				"Blocked redundant SSH to %q: this Warp terminal is already connected to that managed SSH host. Run the intended command directly in the current terminal without ssh. Nested SSH is permitted only for a different destination host.",
				host,
			),
		})
	}
	return allowed, denied
}

func historyHasRedundantSSH(
	history []openai.ChatCompletionMessageParamUnion,
	target agent.ManagedSSHTarget,
) bool {
	for i := len(history) - 1; i >= 0; i-- {
		raw, err := json.Marshal(history[i])
		if err != nil {
			continue
		}
		var message storedMessage
		if json.Unmarshal(raw, &message) != nil {
			continue
		}
		for j := len(message.ToolCalls) - 1; j >= 0; j-- {
			toolCall := message.ToolCalls[j]
			if toolCall.Function.Name != "run_shell_command" {
				continue
			}
			var args struct {
				Command string `json:"command"`
			}
			if json.Unmarshal([]byte(toolCall.Function.Arguments), &args) != nil {
				return false
			}
			_, redundant := agent.RedundantSSHHost(args.Command, target)
			return redundant
		}
	}
	return false
}

type pathCheck struct {
	Path      string
	Content   string
	IsNewFile bool
	Search    string
	Replace   string
	IsDiff    bool
}

// enforcePathPolicy checks file paths in apply_file_diffs tool calls.
// It returns the allowed tool calls and synthetic denial results for denied paths.
// Both Deny and Confirm decisions block the write in automatic mode.
func (s *Server) enforcePathPolicy(toolCalls []llm.ToolCall) ([]llm.ToolCall, []pathPolicyDenial) {
	var allowed []llm.ToolCall
	var denied []pathPolicyDenial

	// Use server CWD as workspace root.
	workspaceRoot, _ := os.Getwd()
	taskScope := ""
	policy := tools.DefaultPathPolicy(workspaceRoot, taskScope)

	for _, tc := range toolCalls {
		if tc.Name != "apply_file_diffs" {
			allowed = append(allowed, tc)
			continue
		}

		var args struct {
			Diffs []struct {
				FilePath string `json:"file_path"`
				Search   string `json:"search"`
				Replace  string `json:"replace"`
			} `json:"diffs"`
			NewFiles []struct {
				FilePath string `json:"file_path"`
				Content  string `json:"content"`
			} `json:"new_files"`
			DeletedFiles []struct {
				FilePath string `json:"file_path"`
			} `json:"deleted_files"`
		}
		if json.Unmarshal(tc.Args, &args) != nil {
			allowed = append(allowed, tc)
			continue
		}

		var checks []pathCheck
		for _, d := range args.Diffs {
			checks = append(checks, pathCheck{Path: d.FilePath, Search: d.Search, Replace: d.Replace, IsDiff: true})
		}
		for _, nf := range args.NewFiles {
			checks = append(checks, pathCheck{Path: nf.FilePath, Content: nf.Content, IsNewFile: true})
		}
		for _, df := range args.DeletedFiles {
			checks = append(checks, pathCheck{Path: df.FilePath})
		}

		hasDenied := false
		for _, c := range checks {
			decision := tools.CanWrite(c.Path, policy)
			if decision != tools.Allow {
				msg := ""
				if decision == tools.Deny {
					msg = fmt.Sprintf("Path policy denied write to %q: this path is in the deny list. Do not attempt to write to .git, node_modules, config.yaml, or conversations.json.", c.Path)
				} else {
					msg = fmt.Sprintf("Path policy requires explicit user confirmation for write to %q. In automatic mode, this path is not allowed. Write to an allowed directory instead.", c.Path)
				}
				denied = append(denied, pathPolicyDenial{
					ID:      tc.ID,
					Path:    c.Path,
					Message: msg,
				})
				hasDenied = true
				break
			}
			// Check markdown line limits for new files and simple search/replace diffs.
			if strings.HasSuffix(c.Path, ".md") {
				content, ok := proposedMarkdownContent(workspaceRoot, c)
				if ok {
					if maxLines, limited := markdownLineLimitForPath(c.Path); limited {
						lines := strings.Count(content, "\n") + 1
						if lines > maxLines {
							denied = append(denied, pathPolicyDenial{
								ID:      tc.ID,
								Path:    c.Path,
								Message: fmt.Sprintf("Markdown file %q exceeds %d line limit (%d lines). Split into sub-documents and add an index.", c.Path, maxLines, lines),
							})
							hasDenied = true
							break
						}
					}
				}
			}
		}
		if !hasDenied {
			allowed = append(allowed, tc)
		}
	}

	return allowed, denied
}

func markdownLineLimitForPath(path string) (int, bool) {
	if path == "记忆系统设计方案/implementation-spec/07-code-review-fix-implementation.md" {
		return 0, false
	}
	if filepath.Base(path) == "AGENTS.md" {
		return 50, true
	}
	return 70, true
}

func proposedMarkdownContent(workspaceRoot string, c pathCheck) (string, bool) {
	if c.IsNewFile {
		return c.Content, c.Content != ""
	}
	if !c.IsDiff {
		return "", false
	}
	target := c.Path
	if !filepath.IsAbs(target) && workspaceRoot != "" {
		target = filepath.Join(workspaceRoot, target)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return "", false
	}
	current := string(data)
	if c.Search == "" || !strings.Contains(current, c.Search) {
		return current, true
	}
	return strings.Replace(current, c.Search, c.Replace, 1), true
}

// resolveProjectKeyFromRequest determines the project key from request context.
// It prefers explicit project roots from the client, then falls back to server CWD.
func (s *Server) resolveProjectKeyFromRequest(req *pb.Request, convID string) string {
	if root := workspaceRootFromRequest(req); root != "" {
		return memory.ProjectKeyFromRoot(root)
	}
	return s.resolveProjectKey(convID)
}

func workspaceRootFromRequest(req *pb.Request) string {
	if req == nil || req.GetInput() == nil || req.GetInput().GetContext() == nil {
		return ""
	}
	ctx := req.GetInput().GetContext()
	for _, rules := range ctx.GetProjectRules() {
		if root := strings.TrimSpace(rules.GetRootPath()); root != "" && root != agent.ManagedSSHContextRoot {
			return root
		}
	}
	for _, codebase := range ctx.GetCodebases() {
		if root := strings.TrimSpace(codebase.GetPath()); root != "" {
			return root
		}
	}
	if dir := ctx.GetDirectory(); dir != nil {
		return strings.TrimSpace(dir.GetPwd())
	}
	return ""
}

// resolveProjectKey determines the project key when no request root is available.
// It prefers the server CWD, then falls back to conversation-based key with a warning.
func (s *Server) resolveProjectKey(convID string) string {
	// Use server working directory as workspace root.
	cwd, err := os.Getwd()
	if err == nil && cwd != "" {
		return memory.ProjectKeyFromRoot(cwd)
	}
	// Fallback: conversation-based key. This is not ideal for cross-session memory.
	log.Printf("[MEMORY] WARNING: using conversation-based project key for conv=%s; workspace root unavailable", convID)
	return memory.ProjectKey("", convID)
}
