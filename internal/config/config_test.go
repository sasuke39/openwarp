package config

import "testing"

func TestApplyDefaultsSetsMemoryContextWindowTokens(t *testing.T) {
	cfg := ApplyDefaults(&Config{})
	if cfg.Memory.ContextWindowTokens != 32000 {
		t.Fatalf("expected default memory context window 32000, got %d", cfg.Memory.ContextWindowTokens)
	}
}

func TestApplyDefaultsPreservesMemoryContextWindowTokens(t *testing.T) {
	cfg := ApplyDefaults(&Config{Memory: MemoryConfig{ContextWindowTokens: 128000}})
	if cfg.Memory.ContextWindowTokens != 128000 {
		t.Fatalf("expected configured memory context window to be preserved, got %d", cfg.Memory.ContextWindowTokens)
	}
}

func TestApplyDefaultsSetsAndPreservesStreamStallTimeout(t *testing.T) {
	defaulted := ApplyDefaults(&Config{})
	if defaulted.Server.StreamStallTimeoutSeconds != 120 {
		t.Fatalf("expected default stream stall timeout 120, got %d", defaulted.Server.StreamStallTimeoutSeconds)
	}

	configured := ApplyDefaults(&Config{Server: ServerConfig{StreamStallTimeoutSeconds: 180}})
	if configured.Server.StreamStallTimeoutSeconds != 180 {
		t.Fatalf("expected configured stream stall timeout 180, got %d", configured.Server.StreamStallTimeoutSeconds)
	}
}

func TestApplyDefaultsSelectsNativeRuntime(t *testing.T) {
	cfg := ApplyDefaults(&Config{})
	if cfg.AgentRuntime.Driver != "native" {
		t.Fatalf("default runtime = %q, want native", cfg.AgentRuntime.Driver)
	}
}

func TestExternalRuntimeRequiresCommand(t *testing.T) {
	cfg := ApplyDefaults(&Config{
		Provider: "openai",
		BaseURL:  "http://localhost/v1",
		APIKey:   "test-key",
		Model:    "test-model",
		AgentRuntime: RuntimeConfig{
			Driver: "deepseek-harness",
		},
	})
	missing := MissingRequiredFields(cfg)
	if len(missing) != 1 || missing[0] != "agent_runtime.command" {
		t.Fatalf("missing fields = %#v", missing)
	}
}
