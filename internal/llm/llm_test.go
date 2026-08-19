package llm

import (
	"testing"

	"github.com/openai/openai-go"
)

func TestExtractToolCalls_UsesStableProviderIndex(t *testing.T) {
	chunks := []openai.ChatCompletionChunk{
		{
			Choices: []openai.ChatCompletionChunkChoice{
				{
					Delta: openai.ChatCompletionChunkChoiceDelta{
						ToolCalls: []openai.ChatCompletionChunkChoiceDeltaToolCall{
							{
								Index: 1,
								ID:    "call-b",
								Function: openai.ChatCompletionChunkChoiceDeltaToolCallFunction{
									Name:      "run_shell_command",
									Arguments: `{"command":"b`,
								},
							},
						},
					},
				},
			},
		},
		{
			Choices: []openai.ChatCompletionChunkChoice{
				{
					Delta: openai.ChatCompletionChunkChoiceDelta{
						ToolCalls: []openai.ChatCompletionChunkChoiceDeltaToolCall{
							{
								Index: 0,
								ID:    "call-a",
								Function: openai.ChatCompletionChunkChoiceDeltaToolCallFunction{
									Name:      "read_files",
									Arguments: `{"files":["a"]}`,
								},
							},
							{
								Index: 1,
								Function: openai.ChatCompletionChunkChoiceDeltaToolCallFunction{
									Arguments: `"}`,
								},
							},
						},
					},
				},
			},
		},
	}

	toolCalls := ExtractToolCalls(chunks)
	if len(toolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(toolCalls))
	}
	if toolCalls[0].ID != "call-b" || toolCalls[0].Name != "run_shell_command" || string(toolCalls[0].Args) != `{"command":"b"}` {
		t.Fatalf("unexpected first tool call: %+v", toolCalls[0])
	}
	if toolCalls[1].ID != "call-a" || toolCalls[1].Name != "read_files" || string(toolCalls[1].Args) != `{"files":["a"]}` {
		t.Fatalf("unexpected second tool call: %+v", toolCalls[1])
	}
}

// CollectStreamResult 应记录流中最后一个非空 finish_reason。
func TestCollectStreamResult_CapturesFinishReason(t *testing.T) {
	// 正常流:中间 chunk 无 finish_reason,末尾 chunk finish_reason=length
	chunks := []openai.ChatCompletionChunk{
		{
			Choices: []openai.ChatCompletionChunkChoice{
				{
					Delta: openai.ChatCompletionChunkChoiceDelta{
						Content: "partial",
					},
				},
			},
		},
		{
			Choices: []openai.ChatCompletionChunkChoice{
				{
					FinishReason: "length",
				},
			},
		},
	}
	result := CollectStreamResult(chunks)
	if result.Text != "partial" {
		t.Fatalf("expected text 'partial', got %q", result.Text)
	}
	if result.FinishReason != "length" {
		t.Fatalf("expected finish_reason 'length', got %q", result.FinishReason)
	}

	// 无 finish_reason 时应为空
	emptyChunks := []openai.ChatCompletionChunk{
		{
			Choices: []openai.ChatCompletionChunkChoice{
				{
					Delta: openai.ChatCompletionChunkChoiceDelta{Content: "x"},
				},
			},
		},
	}
	emptyResult := CollectStreamResult(emptyChunks)
	if emptyResult.FinishReason != "" {
		t.Fatalf("expected empty finish_reason, got %q", emptyResult.FinishReason)
	}

	// stop finish_reason 也应被记录
	stopChunks := []openai.ChatCompletionChunk{
		{
			Choices: []openai.ChatCompletionChunkChoice{
				{
					Delta:       openai.ChatCompletionChunkChoiceDelta{Content: "done"},
					FinishReason: "stop",
				},
			},
		},
	}
	stopResult := CollectStreamResult(stopChunks)
	if stopResult.FinishReason != "stop" {
		t.Fatalf("expected finish_reason 'stop', got %q", stopResult.FinishReason)
	}
}
