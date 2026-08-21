package config

import (
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Provider string `yaml:"provider"`
	BaseURL  string `yaml:"base_url"`
	APIKey   string `yaml:"api_key"`
	Model    string `yaml:"model"`
	// MaxTokens caps completion output (reasoning included on thinking models).
	// 0 means "do not send the parameter" and use the provider default.
	// Some providers default to a small cap, which lets thinking models burn
	// the whole budget on reasoning and return empty content
	// (finish_reason=length). Set explicitly when using such models.
	MaxTokens int `yaml:"max_tokens"`
	// ThinkingDisabled turns off the model's internal reasoning/thinking when
	// true and the provider supports it (e.g. GLM/DeepSeek thinking parameter).
	// Reduces latency and prevents reasoning from consuming the output budget,
	// at the cost of reasoning quality on hard problems.
	ThinkingDisabled bool          `yaml:"thinking_disabled"`
	AgentRuntime     RuntimeConfig `yaml:"agent_runtime"`
	Server           ServerConfig  `yaml:"server"`
	Memory           MemoryConfig  `yaml:"memory"`
}

type RuntimeConfig struct {
	// Driver selects the agent control loop. "native" preserves the current Go
	// implementation; every other value resolves an external runtime driver.
	Driver string `yaml:"driver"`
	// Command and Args launch a newline-delimited JSON protocol sidecar.
	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`
}

type ServerConfig struct {
	Host                      string `yaml:"host"`
	Port                      int    `yaml:"port"`
	StreamStallTimeoutSeconds int    `yaml:"stream_stall_timeout_seconds"`
}

type MemoryConfig struct {
	Enabled          *bool  `yaml:"enabled"`
	BaseDir          string `yaml:"base_dir"`
	SessionEnabled   *bool  `yaml:"session_enabled"`
	AutoEnabled      *bool  `yaml:"auto_enabled"`
	MaxProjectMemory int    `yaml:"max_project_memories"`
	StaleAfterHours  int    `yaml:"stale_after_hours"`
	// ContextWindowTokens is the server-side base model context window used to
	// derive memory compaction thresholds. It defaults to 32k tokens.
	ContextWindowTokens int `yaml:"context_window_tokens"`
}

// IsEnabled returns whether memory is enabled, defaulting to true if not set.
func (m *MemoryConfig) IsEnabled() bool {
	if m.Enabled == nil {
		return true
	}
	return *m.Enabled
}

// IsSessionEnabled returns whether session memory is enabled, defaulting to true if not set.
func (m *MemoryConfig) IsSessionEnabled() bool {
	if m.SessionEnabled == nil {
		return true
	}
	return *m.SessionEnabled
}

// IsAutoEnabled returns whether auto-memory is enabled, defaulting to true if not set.
func (m *MemoryConfig) IsAutoEnabled() bool {
	if m.AutoEnabled == nil {
		return true
	}
	return *m.AutoEnabled
}

func Default() *Config {
	return &Config{
		AgentRuntime: RuntimeConfig{Driver: "native"},
		Server: ServerConfig{
			Host:                      "127.0.0.1",
			Port:                      18888,
			StreamStallTimeoutSeconds: 120,
		},
	}
}

func ApplyDefaults(cfg *Config) *Config {
	if cfg == nil {
		cfg = Default()
	}
	if strings.TrimSpace(cfg.AgentRuntime.Driver) == "" {
		cfg.AgentRuntime.Driver = "native"
	}
	if cfg.Server.Host == "" {
		cfg.Server.Host = "127.0.0.1"
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 18888
	}
	if cfg.Server.StreamStallTimeoutSeconds <= 0 {
		cfg.Server.StreamStallTimeoutSeconds = 120
	}
	if cfg.Memory.MaxProjectMemory == 0 {
		cfg.Memory.MaxProjectMemory = 5
	}
	if cfg.Memory.StaleAfterHours == 0 {
		cfg.Memory.StaleAfterHours = 24
	}
	if cfg.Memory.ContextWindowTokens == 0 {
		cfg.Memory.ContextWindowTokens = 32000
	}
	return cfg
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return ApplyDefaults(&cfg), nil
}

func LoadOrDefault(path string) (*Config, error) {
	cfg, err := Load(path)
	if err != nil {
		return Default(), err
	}
	return ApplyDefaults(cfg), nil
}

func Dump(cfg *Config) ([]byte, error) {
	cfg = ApplyDefaults(cfg)
	return yaml.Marshal(cfg)
}

func MissingRequiredFields(cfg *Config) []string {
	cfg = ApplyDefaults(cfg)
	var missing []string
	if strings.TrimSpace(cfg.Provider) == "" {
		missing = append(missing, "provider")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		missing = append(missing, "base_url")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		missing = append(missing, "api_key")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		missing = append(missing, "model")
	}
	if strings.TrimSpace(cfg.Server.Host) == "" {
		missing = append(missing, "server.host")
	}
	if cfg.Server.Port == 0 {
		missing = append(missing, "server.port")
	}
	if cfg.AgentRuntime.Driver != "native" && strings.TrimSpace(cfg.AgentRuntime.Command) == "" {
		missing = append(missing, "agent_runtime.command")
	}
	return missing
}

func IsConfigured(cfg *Config) bool {
	return len(MissingRequiredFields(cfg)) == 0
}
