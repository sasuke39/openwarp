package memory

import "testing"

func TestEstimateChars(t *testing.T) {
	msgs := []Msg{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "world"},
	}
	if got := EstimateChars(msgs); got != 10 {
		t.Errorf("expected 10, got %d", got)
	}
}

func TestHasUnpairedToolCall_NoPairing(t *testing.T) {
	msgs := []Msg{
		{Role: "user", Content: "go"},
		{Role: "assistant", Content: "ok"},
	}
	if HasUnpairedToolCall(msgs) {
		t.Error("no tool calls, should not be unpaired")
	}
}

func TestHasUnpairedToolCall_Paired(t *testing.T) {
	msgs := []Msg{
		{Role: "assistant", ToolCallIDs: []string{"c1"}},
		{Role: "tool", ToolResultID: "c1"},
	}
	if HasUnpairedToolCall(msgs) {
		t.Error("paired tool call should not be unpaired")
	}
}

func TestHasUnpairedToolCall_Unpaired(t *testing.T) {
	msgs := []Msg{
		{Role: "assistant", ToolCallIDs: []string{"c1"}},
	}
	if !HasUnpairedToolCall(msgs) {
		t.Error("missing result should be unpaired")
	}
}

func TestAdjustBoundaryForToolPairs_OrphanResult(t *testing.T) {
	msgs := []Msg{
		{Role: "user", Content: "u1"},
		{Role: "assistant", ToolCallIDs: []string{"call_1"}},
		{Role: "tool", ToolResultID: "call_1"},
		{Role: "assistant", Content: "done"},
	}
	// If we start at index 2 (tool result), adjust back to index 1.
	got := AdjustBoundaryForToolPairs(msgs, 2)
	if got != 1 {
		t.Errorf("expected 1, got %d", got)
	}
}

func TestAdjustBoundaryForToolPairs_MultiCall(t *testing.T) {
	msgs := []Msg{
		{Role: "assistant", ToolCallIDs: []string{"c1", "c2"}},
		{Role: "tool", ToolResultID: "c1"},
		{Role: "tool", ToolResultID: "c2"},
	}
	// Start at index 2 (second tool result), should roll back to 0 (the assistant).
	got := AdjustBoundaryForToolPairs(msgs, 2)
	if got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestShouldCompact_BelowThreshold(t *testing.T) {
	cfg := CompactionConfig{MaxHistoryChars: 100000}
	msgs := []Msg{{Role: "user", Content: "hi"}}
	r := ShouldCompact(msgs, "notes", cfg)
	if r.ShouldCompact {
		t.Error("should not compact below threshold")
	}
}

func TestShouldCompact_NoNotes(t *testing.T) {
	cfg := CompactionConfig{MaxHistoryChars: 1}
	msgs := []Msg{{Role: "user", Content: "a very long message that exceeds threshold"}}
	r := ShouldCompact(msgs, "", cfg)
	if r.ShouldCompact {
		t.Error("should not compact without notes")
	}
}

func TestShouldCompact_UnpairedActiveCall(t *testing.T) {
	cfg := CompactionConfig{MaxHistoryChars: 1, MinRecentChars: 1, MaxRecentMessages: 100}
	msgs := []Msg{
		{Role: "user", Content: "long message to exceed threshold"},
		{Role: "assistant", ToolCallIDs: []string{"c1"}},
	}
	r := ShouldCompact(msgs, "notes", cfg)
	if r.ShouldCompact {
		t.Error("should not compact with unpaired tool call")
	}
}

func TestShouldCompact_SafeWindow(t *testing.T) {
	cfg := CompactionConfig{MaxHistoryChars: 1, MinRecentChars: 1, MaxRecentMessages: 100}
	msgs := []Msg{
		{Role: "user", Content: "long message to exceed threshold"},
		{Role: "assistant", Content: "response"},
	}
	r := ShouldCompact(msgs, "notes", cfg)
	if !r.ShouldCompact {
		t.Error("should compact with safe window")
	}
}

func TestCompactionConfigForContextWindow_Default(t *testing.T) {
	cfg := CompactionConfigForContextWindow(0)
	if cfg.ContextWindowTokens != 32000 {
		t.Fatalf("expected default 32000 tokens, got %d", cfg.ContextWindowTokens)
	}
	if cfg.MaxHistoryChars != 89600 {
		t.Fatalf("expected 70%% of default context chars, got %d", cfg.MaxHistoryChars)
	}
	if cfg.MinRecentChars != 15360 {
		t.Fatalf("expected 12%% recent window, got %d", cfg.MinRecentChars)
	}
	if cfg.MaxRecentMessages != 40 {
		t.Fatalf("expected 40 recent messages, got %d", cfg.MaxRecentMessages)
	}
}

func TestCompactionConfigForContextWindow_ScalesWithModelWindow(t *testing.T) {
	small := CompactionConfigForContextWindow(16000)
	large := CompactionConfigForContextWindow(128000)

	if small.MaxHistoryChars != 44800 {
		t.Fatalf("expected 16k model to compact at 44800 chars, got %d", small.MaxHistoryChars)
	}
	if small.MinRecentChars != 8000 {
		t.Fatalf("expected small model to keep minimum recent chars, got %d", small.MinRecentChars)
	}
	if small.MaxRecentMessages != 20 {
		t.Fatalf("expected small model to keep minimum recent messages, got %d", small.MaxRecentMessages)
	}
	if large.MaxHistoryChars <= small.MaxHistoryChars {
		t.Fatalf("expected larger model to have larger trigger: small=%d large=%d", small.MaxHistoryChars, large.MaxHistoryChars)
	}
	if large.MinRecentChars != 61440 {
		t.Fatalf("expected large model recent window to scale to 61440 chars, got %d", large.MinRecentChars)
	}
	if large.MaxRecentMessages != 120 {
		t.Fatalf("expected large model recent messages to cap at 120, got %d", large.MaxRecentMessages)
	}
}
