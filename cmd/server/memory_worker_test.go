package main

import (
	"testing"

	"github.com/sasuke39/open-warp/internal/memory"
)

func TestShouldUpdateProjectMemoryUsesIncrementalCursor(t *testing.T) {
	tests := []struct {
		name   string
		cursor memory.QueueCursor
		stats  memory.SessionStats
		want   bool
	}{
		{"initial too small", memory.QueueCursor{}, memory.SessionStats{MessageCount: 5, HistoryChars: 9000}, false},
		{"initial threshold", memory.QueueCursor{}, memory.SessionStats{MessageCount: 6, HistoryChars: 8000}, true},
		{"increment too small", memory.QueueCursor{MessageIndex: 6, HistoryChars: 8000}, memory.SessionStats{MessageCount: 9, HistoryChars: 19000}, false},
		{"increment at breakpoint", memory.QueueCursor{MessageIndex: 6, HistoryChars: 8000}, memory.SessionStats{MessageCount: 10, HistoryChars: 20000}, true},
		{"increment waiting tool", memory.QueueCursor{MessageIndex: 6, HistoryChars: 8000, ToolCalls: 1}, memory.SessionStats{MessageCount: 10, HistoryChars: 20000, ToolCallCount: 2, LastAssistantHasToolCall: true}, false},
		{"increment enough tools", memory.QueueCursor{MessageIndex: 6, HistoryChars: 8000, ToolCalls: 1}, memory.SessionStats{MessageCount: 10, HistoryChars: 20000, ToolCallCount: 4, LastAssistantHasToolCall: true}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldUpdateProjectMemory(tt.cursor, tt.stats); got != tt.want {
				t.Fatalf("got=%v want=%v", got, tt.want)
			}
		})
	}
}
