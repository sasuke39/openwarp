package memory

// Msg is a simplified message representation for compaction logic.
type Msg struct {
	Role         string
	Content      string
	ToolCallIDs  []string
	ToolResultID string
}

// CompactionConfig controls when and how compaction happens.
type CompactionConfig struct {
	MaxHistoryChars     int
	MinRecentChars      int
	MaxRecentMessages   int
	ContextWindowTokens int
}

// CompactionResult describes whether and how to compact.
type CompactionResult struct {
	StartIndex    int
	ShouldCompact bool
	Reason        string
}

const (
	defaultContextWindowTokens = 32000
	estimatedCharsPerToken     = 4
	minTriggerChars            = 32000
	minRecentWindowChars       = 8000
	maxRecentWindowChars       = 64000
)

// CompactionConfigForContextWindow derives character thresholds from the
// server-side model context window. History is measured in chars locally, so we
// use a conservative 4 chars/token estimate and compact before the window is
// close to full.
func CompactionConfigForContextWindow(contextWindowTokens int) CompactionConfig {
	if contextWindowTokens <= 0 {
		contextWindowTokens = defaultContextWindowTokens
	}
	contextChars := contextWindowTokens * estimatedCharsPerToken
	trigger := int(float64(contextChars) * 0.70)
	if trigger < minTriggerChars {
		trigger = minTriggerChars
	}
	recent := int(float64(contextChars) * 0.12)
	if recent < minRecentWindowChars {
		recent = minRecentWindowChars
	}
	if recent > maxRecentWindowChars {
		recent = maxRecentWindowChars
	}
	maxRecentMessages := contextWindowTokens / 800
	if maxRecentMessages < 20 {
		maxRecentMessages = 20
	}
	if maxRecentMessages > 120 {
		maxRecentMessages = 120
	}
	return CompactionConfig{
		MaxHistoryChars:     trigger,
		MinRecentChars:      recent,
		MaxRecentMessages:   maxRecentMessages,
		ContextWindowTokens: contextWindowTokens,
	}
}

// EstimateChars returns the total character count of all messages.
func EstimateChars(messages []Msg) int {
	total := 0
	for _, m := range messages {
		total += len(m.Content)
		for _, id := range m.ToolCallIDs {
			total += len(id)
		}
		total += len(m.ToolResultID)
	}
	return total
}

// HasUnpairedToolCall returns true if any assistant tool_call lacks a result.
func HasUnpairedToolCall(messages []Msg) bool {
	pending := map[string]struct{}{}
	for _, m := range messages {
		if m.Role == "assistant" {
			for _, id := range m.ToolCallIDs {
				pending[id] = struct{}{}
			}
		}
		if m.Role == "tool" {
			delete(pending, m.ToolResultID)
		}
	}
	return len(pending) > 0
}

// AdjustBoundaryForToolPairs moves start backward if it would split a tool pair.
func AdjustBoundaryForToolPairs(messages []Msg, start int) int {
	if start <= 0 || start >= len(messages) {
		return start
	}
	// If start lands on a tool result, roll back to include its assistant tool_call.
	if messages[start].Role == "tool" {
		resultID := messages[start].ToolResultID
		for i := start - 1; i >= 0; i-- {
			if messages[i].Role == "assistant" {
				for _, id := range messages[i].ToolCallIDs {
					if id == resultID {
						return i
					}
				}
			}
		}
	}
	// If start lands on a tool result that belongs to a multi-call assistant,
	// ensure all sibling results are included.
	if messages[start].Role == "tool" {
		for i := start - 1; i >= 0; i-- {
			if messages[i].Role == "assistant" && len(messages[i].ToolCallIDs) > 1 {
				return i
			}
		}
	}
	return start
}

// ShouldCompact determines whether history should be compacted.
func ShouldCompact(messages []Msg, notes string, cfg CompactionConfig) CompactionResult {
	totalChars := EstimateChars(messages)
	if totalChars < cfg.MaxHistoryChars {
		return CompactionResult{Reason: "below threshold"}
	}
	if notes == "" {
		return CompactionResult{Reason: "no session notes"}
	}

	// Determine the recent window boundary.
	recentChars := 0
	start := len(messages)
	for i := len(messages) - 1; i >= 0; i-- {
		recentChars += len(messages[i].Content)
		if recentChars >= cfg.MinRecentChars || (cfg.MaxRecentMessages > 0 && len(messages)-i >= cfg.MaxRecentMessages) {
			start = i
			break
		}
	}
	if start == len(messages) {
		start = 0
	}

	// Adjust for tool pairs.
	start = AdjustBoundaryForToolPairs(messages, start)

	// Check for unpaired tool calls in the kept window.
	window := messages[start:]
	if HasUnpairedToolCall(window) {
		return CompactionResult{
			StartIndex:    start,
			ShouldCompact: false,
			Reason:        "unpaired active tool call",
		}
	}

	return CompactionResult{
		StartIndex:    start,
		ShouldCompact: true,
		Reason:        "safe window",
	}
}
