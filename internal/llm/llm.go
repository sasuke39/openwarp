package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/shared"
	"github.com/sasuke39/open-warp/internal/config"
)

type ToolCall struct {
	ID   string
	Name string
	Args json.RawMessage
}

// StreamResult holds the collected output from a streaming LLM response.
type StreamResult struct {
	Text             string
	ReasoningContent string
	ToolCalls        []ToolCall
	IsToolCall       bool
	// FinishReason 记录流中出现的最后一个非空 finish_reason(如 "stop"、
	// "length"、"tool_calls")。调用方用它区分"推理耗尽输出预算"(length)
	// 与普通空响应。
	FinishReason string
}

type Client struct {
	client *openai.Client
	model  string
	tools  []openai.ChatCompletionToolParam
	// maxTokens caps completion output when > 0; see config.Config.MaxTokens.
	maxTokens int
	// thinkingDisabled disables model reasoning when true.
	thinkingDisabled bool
	// streamStallTimeout is the watchdog gap limit for streaming calls.
	// Defaults to DefaultStreamStallTimeout; override in tests via
	// SetStreamStallTimeout.
	streamStallTimeout time.Duration
}

func NewClient(cfg *config.Config) *Client {
	return newClient(cfg, nil)
}

// NewClientWithHTTPClient creates a client with a caller-provided HTTP client.
// This is primarily intended for integration tests that need to mock the LLM API.
func NewClientWithHTTPClient(cfg *config.Config, httpClient *http.Client) *Client {
	return newClient(cfg, httpClient)
}

func newClient(cfg *config.Config, httpClient *http.Client) *Client {
	cfg = config.ApplyDefaults(cfg)
	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	opts = append(opts, option.WithBaseURL(baseURL))
	if httpClient != nil {
		opts = append(opts, option.WithHTTPClient(httpClient))
	}
	log.Printf("[LLM] NewClient: base_url=%s model=%s key_len=%d", baseURL, cfg.Model, len(cfg.APIKey))

	client := openai.NewClient(opts...)

	tools := []openai.ChatCompletionToolParam{
		{
			Function: shared.FunctionDefinitionParam{
				Name:        "read_files",
				Description: param.NewOpt("Read the contents of files. Provide file paths and optional line ranges."),
				Parameters: shared.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"files": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"name": map[string]any{"type": "string", "description": "File path"},
									"line_ranges": map[string]any{
										"type": "array",
										"items": map[string]any{
											"type": "object",
											"properties": map[string]any{
												"start": map[string]any{"type": "integer"},
												"end":   map[string]any{"type": "integer"},
											},
										},
									},
								},
								"required": []string{"name"},
							},
						},
					},
					"required": []string{"files"},
				},
			},
		},
		{
			Function: shared.FunctionDefinitionParam{
				Name:        "grep",
				Description: param.NewOpt("Search for patterns in files using regular expressions."),
				Parameters: shared.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"queries": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "Search patterns (regex supported)",
						},
						"path": map[string]any{
							"type":        "string",
							"description": "Directory or file to search in",
						},
					},
					"required": []string{"queries"},
				},
			},
		},
		{
			Function: shared.FunctionDefinitionParam{
				Name:        "file_glob",
				Description: param.NewOpt("Find files matching glob patterns."),
				Parameters: shared.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"patterns": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": `Glob patterns to match (e.g. ["*.go", "src/**/*.rs"])`,
						},
						"search_dir": map[string]any{
							"type":        "string",
							"description": "Directory to search in",
						},
					},
					"required": []string{"patterns"},
				},
			},
		},
		{
			Function: shared.FunctionDefinitionParam{
				Name:        "run_shell_command",
				Description: param.NewOpt("Execute a shell command in the terminal. Use this to run build commands, tests, git operations, or any CLI tool."),
				Parameters: shared.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"command": map[string]any{
							"type":        "string",
							"description": "The shell command to execute",
						},
						"is_read_only": map[string]any{
							"type":        "boolean",
							"description": "Whether this command is read-only (no side effects)",
						},
						"is_risky": map[string]any{
							"type":        "boolean",
							"description": "Whether this command is potentially risky/destructive",
						},
						"risk_category": map[string]any{
							"type":        "string",
							"enum":        []string{"RISK_CATEGORY_READ_ONLY", "RISK_CATEGORY_TRIVIAL_LOCAL_CHANGE", "RISK_CATEGORY_NONTRIVIAL_LOCAL_CHANGE", "RISK_CATEGORY_EXTERNAL_CHANGE", "RISK_CATEGORY_RISKY"},
							"description": "Risk classification: READ_ONLY (no changes), TRIVIAL_LOCAL_CHANGE (minor file edit), NONTRIVIAL_LOCAL_CHANGE (significant file edit), EXTERNAL_CHANGE (network/external effects), RISKY (destructive)",
						},
					},
					"required": []string{"command"},
				},
			},
		},
		{
			Function: shared.FunctionDefinitionParam{
				Name:        "read_shell_command_output",
				Description: param.NewOpt("Continue waiting for a previously started long-running shell command, or fetch more output from it. Use the command_id from a prior 'Command still running' result."),
				Parameters: shared.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"command_id": map[string]any{
							"type":        "string",
							"description": "The command ID from a previous long-running shell command result",
						},
						"wait_for_completion": map[string]any{
							"type":        "boolean",
							"description": "Whether to wait until the command completes before returning",
						},
						"duration_seconds": map[string]any{
							"type":        "integer",
							"description": "If not waiting for completion, poll again after this many seconds",
						},
					},
					"required": []string{"command_id"},
				},
			},
		},
		{
			Function: shared.FunctionDefinitionParam{
				Name:        "transfer_shell_command_control_to_user",
				Description: param.NewOpt("Hand control of a still-running shell command back to the user when the command is interactive or needs manual intervention."),
				Parameters: shared.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"reason": map[string]any{
							"type":        "string",
							"description": "Why the user should take over this running command",
						},
					},
					"required": []string{"reason"},
				},
			},
		},
		{
			Function: shared.FunctionDefinitionParam{
				Name:        "apply_file_diffs",
				Description: param.NewOpt("Apply changes to files: create new files, edit existing ones, or delete files. Use this to implement code changes."),
				Parameters: shared.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"summary": map[string]any{
							"type":        "string",
							"description": "A short summary of what these changes accomplish",
						},
						"diffs": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"file_path": map[string]any{"type": "string", "description": "Path to the file to edit"},
									"search":    map[string]any{"type": "string", "description": "Content to search for and replace"},
									"replace":   map[string]any{"type": "string", "description": "Replacement content"},
								},
								"required": []string{"file_path", "search", "replace"},
							},
							"description": "List of file edits (search/replace pairs)",
						},
						"new_files": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"file_path": map[string]any{"type": "string", "description": "Path for the new file"},
									"content":   map[string]any{"type": "string", "description": "Full contents of the new file"},
								},
								"required": []string{"file_path", "content"},
							},
							"description": "List of new files to create",
						},
						"deleted_files": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"file_path": map[string]any{"type": "string", "description": "Path to the file to delete"},
								},
								"required": []string{"file_path"},
							},
							"description": "List of files to delete",
						},
					},
					"required": []string{"summary"},
				},
			},
		},
		{
			Function: shared.FunctionDefinitionParam{
				Name:        "file_glob_v2",
				Description: param.NewOpt("Find files matching glob patterns with advanced options like max depth and max matches."),
				Parameters: shared.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"patterns": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "Glob patterns to match (e.g. [\"*.go\", \"src/**/*.rs\"])",
						},
						"search_dir": map[string]any{
							"type":        "string",
							"description": "Directory to search in",
						},
						"max_matches": map[string]any{
							"type":        "integer",
							"description": "Maximum number of matches to return (0 = unlimited)",
						},
						"max_depth": map[string]any{
							"type":        "integer",
							"description": "Maximum directory depth to search (0 = unlimited)",
						},
						"min_depth": map[string]any{
							"type":        "integer",
							"description": "Minimum directory depth to search (0 = unlimited)",
						},
					},
					"required": []string{"patterns"},
				},
			},
		},
		{
			Function: shared.FunctionDefinitionParam{
				Name:        "search_codebase",
				Description: param.NewOpt("Semantically search the codebase for relevant code. Use this for high-level questions about how the code works."),
				Parameters: shared.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "Natural language query describing what you're looking for",
						},
						"path_filters": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "Optional file path patterns to filter by",
						},
						"codebase_path": map[string]any{
							"type":        "string",
							"description": "Optional absolute path to the codebase root",
						},
					},
					"required": []string{"query"},
				},
			},
		},
		{
			// update_task_list 是服务端执行的规划工具:adapter 校验后把任务
			// 列表存进会话内存状态,不转发给客户端执行。每轮请求时把当前
			// 任务进度注入 system prompt 尾部(见 cmd/server/tasklist.go)。
			Function: shared.FunctionDefinitionParam{
				Name: "update_task_list",
				Description: param.NewOpt("Update the task list for this work session. Use this to plan your approach, track progress, and stay focused. Call it:\n" +
					"- At the start of any non-trivial task to outline your steps\n" +
					"- After completing each step to mark it done and move to the next\n" +
					"- When you realize your plan needs to change\n\n" +
					"Rules:\n" +
					"- Only one task can be in_progress at a time\n" +
					"- Always pass the complete list (replaces previous state)\n" +
					"- Keep tasks concrete and actionable\n" +
					"- When all tasks are completed, you may finish your response"),
				Parameters: shared.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"tasks": map[string]any{
							"type":        "array",
							"description": "Complete task list, replacing any previous list. Each task must have a unique id.",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"id": map[string]any{
										"type":        "string",
										"description": "Unique task identifier (e.g. 'step-1', 'build', 'test')",
									},
									"content": map[string]any{
										"type":        "string",
										"description": "What this task does, one sentence",
									},
									"status": map[string]any{
										"type":        "string",
										"enum":        []string{"pending", "in_progress", "completed", "cancelled"},
										"description": "pending=not started, in_progress=currently working on, completed=done, cancelled=abandoned",
									},
									"priority": map[string]any{
										"type":        "string",
										"enum":        []string{"high", "medium", "low"},
										"description": "high=must do, medium=should do, low=nice to have",
									},
								},
								"required": []string{"id", "content", "status"},
							},
						},
					},
					"required": []string{"tasks"},
				},
			},
		},
	}

	return &Client{
		client:             &client,
		model:              cfg.Model,
		tools:              tools,
		maxTokens:          cfg.MaxTokens,
		thinkingDisabled:   cfg.ThinkingDisabled,
		streamStallTimeout: time.Duration(cfg.Server.StreamStallTimeoutSeconds) * time.Second,
	}
}

// SetStreamStallTimeout overrides the watchdog stall timeout for streaming
// calls. Production configuration should normally use
// server.stream_stall_timeout_seconds instead.
func (c *Client) SetStreamStallTimeout(d time.Duration) {
	c.streamStallTimeout = d
}

func (c *Client) StreamChat(ctx context.Context, systemPrompt string, history []openai.ChatCompletionMessageParamUnion) *WatchdogStream {
	msgs := make([]openai.ChatCompletionMessageParamUnion, 0, len(history)+1)
	msgs = append(msgs, openai.SystemMessage(systemPrompt))
	msgs = append(msgs, history...)

	log.Printf("[LLM] StreamChat: model=%s msg_count=%d tools=%d", c.model, len(msgs), len(c.tools))
	// Child context so the watchdog can abort a stalled HTTP request without
	// cancelling the caller's request context.
	streamCtx, cancel := context.WithCancel(ctx)
	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(c.model),
		Messages: msgs,
		Tools:    c.tools,
	}
	if c.maxTokens > 0 {
		params.MaxTokens = openai.Int(int64(c.maxTokens))
	}
	// 火山/GLM 等支持 thinking 参数的提供商:关闭推理可防止推理吃光输出预算。
	// 用 WithJSONSet 注入,不依赖 SDK 暴露该字段。
	var extraOpts []option.RequestOption
	if c.thinkingDisabled {
		extraOpts = append(extraOpts, option.WithJSONSet("thinking", map[string]string{"type": "disabled"}))
		log.Printf("[LLM] thinking=disabled")
	}
	inner := c.client.Chat.Completions.NewStreaming(streamCtx, params, extraOpts...)
	return newWatchdogStream(inner, cancel, c.streamStallTimeout)
}

// CompleteText sends a non-streaming, no-tools completion request and returns
// the assistant text. Used by memory extractors that must not call tools.
func (c *Client) CompleteText(ctx context.Context, systemPrompt string, history []openai.ChatCompletionMessageParamUnion) (string, error) {
	msgs := make([]openai.ChatCompletionMessageParamUnion, 0, len(history)+1)
	msgs = append(msgs, openai.SystemMessage(systemPrompt))
	msgs = append(msgs, history...)

	log.Printf("[LLM] CompleteText: model=%s msg_count=%d (no tools)", c.model, len(msgs))
	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(c.model),
		Messages: msgs,
	}
	if c.maxTokens > 0 {
		params.MaxTokens = openai.Int(int64(c.maxTokens))
	}
	var extraOpts []option.RequestOption
	if c.thinkingDisabled {
		extraOpts = append(extraOpts, option.WithJSONSet("thinking", map[string]string{"type": "disabled"}))
	}
	resp, err := c.client.Chat.Completions.New(ctx, params, extraOpts...)
	if err != nil {
		return "", fmt.Errorf("CompleteText: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("CompleteText: empty response")
	}
	text := resp.Choices[0].Message.Content
	if text == "" {
		return "", fmt.Errorf("CompleteText: empty text in response")
	}
	return text, nil
}

func MakeUserMessage(content string) openai.ChatCompletionMessageParamUnion {
	return openai.UserMessage(content)
}

func MakeToolResultMessage(toolCallID, content string) openai.ChatCompletionMessageParamUnion {
	return openai.ToolMessage(content, toolCallID)
}

func MakeAssistantToolCallMessage(toolCalls []ToolCall, reasoningContent string) openai.ChatCompletionMessageParamUnion {
	tcs := make([]openai.ChatCompletionMessageToolCallParam, 0, len(toolCalls))
	for _, tc := range toolCalls {
		tcs = append(tcs, openai.ChatCompletionMessageToolCallParam{
			ID: tc.ID,
			Function: openai.ChatCompletionMessageToolCallFunctionParam{
				Name:      tc.Name,
				Arguments: string(tc.Args),
			},
		})
	}

	if reasoningContent != "" {
		msg := map[string]any{
			"role":              "assistant",
			"tool_calls":        tcs,
			"reasoning_content": reasoningContent,
		}
		raw, _ := json.Marshal(msg)
		overridden := param.Override[openai.ChatCompletionAssistantMessageParam](json.RawMessage(raw))
		return openai.ChatCompletionMessageParamUnion{
			OfAssistant: &overridden,
		}
	}

	return openai.ChatCompletionMessageParamUnion{
		OfAssistant: &openai.ChatCompletionAssistantMessageParam{
			ToolCalls: tcs,
		},
	}
}

// MakeAssistantMessageWithReasoning builds an assistant message that includes
// reasoning_content for DeepSeek thinking models. It uses raw JSON injection
// because the openai-go SDK doesn't support this field.
func MakeAssistantMessageWithReasoning(text, reasoningContent string) openai.ChatCompletionMessageParamUnion {
	if reasoningContent == "" {
		return openai.AssistantMessage(text)
	}
	// Build raw JSON with reasoning_content field, then use Override to inject it
	msg := map[string]any{
		"role":              "assistant",
		"content":           text,
		"reasoning_content": reasoningContent,
	}
	raw, _ := json.Marshal(msg)
	overridden := param.Override[openai.ChatCompletionAssistantMessageParam](json.RawMessage(raw))
	return openai.ChatCompletionMessageParamUnion{
		OfAssistant: &overridden,
	}
}

func IsToolCallFinish(chunks []openai.ChatCompletionChunk) bool {
	for _, chunk := range chunks {
		for _, choice := range chunk.Choices {
			if choice.FinishReason == "tool_calls" {
				return true
			}
		}
	}
	return false
}

func ExtractToolCalls(chunks []openai.ChatCompletionChunk) []ToolCall {
	calls := map[int64]*ToolCall{}
	order := []int64{}

	for _, chunk := range chunks {
		for _, choice := range chunk.Choices {
			for _, tc := range choice.Delta.ToolCalls {
				idx := tc.Index
				if _, ok := calls[idx]; !ok {
					calls[idx] = &ToolCall{}
					order = append(order, idx)
				}
				if tc.ID != "" {
					calls[idx].ID = tc.ID
				}
				if tc.Function.Name != "" {
					calls[idx].Name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					calls[idx].Args = append(calls[idx].Args, tc.Function.Arguments...)
				}
			}
		}
	}

	result := make([]ToolCall, 0, len(order))
	for _, idx := range order {
		result = append(result, *calls[idx])
	}
	return result
}

func CollectTextDeltas(chunks []openai.ChatCompletionChunk) string {
	var text string
	for _, chunk := range chunks {
		for _, choice := range chunk.Choices {
			text += choice.Delta.Content
		}
	}
	return text
}

func CollectStreamResult(chunks []openai.ChatCompletionChunk) StreamResult {
	var result StreamResult
	for _, chunk := range chunks {
		for _, choice := range chunk.Choices {
			result.Text += choice.Delta.Content
			if choice.FinishReason != "" {
				result.FinishReason = string(choice.FinishReason)
			}
			// DeepSeek reasoning models return reasoning_content in delta.
			// Try ExtraFields first, then fall back to RawJSON parsing.
			if f, ok := choice.Delta.JSON.ExtraFields["reasoning_content"]; ok && f.Valid() {
				var s string
				if err := json.Unmarshal([]byte(f.Raw()), &s); err == nil {
					result.ReasoningContent += s
				}
			} else if raw := choice.Delta.RawJSON(); raw != "" {
				var delta map[string]json.RawMessage
				if err := json.Unmarshal([]byte(raw), &delta); err == nil {
					if rc, ok := delta["reasoning_content"]; ok {
						var s string
						if json.Unmarshal(rc, &s) == nil {
							result.ReasoningContent += s
						}
					}
				}
			}
		}
	}
	if IsToolCallFinish(chunks) {
		result.IsToolCall = true
		result.ToolCalls = ExtractToolCalls(chunks)
	}
	return result
}
