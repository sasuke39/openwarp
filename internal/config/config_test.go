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
