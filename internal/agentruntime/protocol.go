package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const ProtocolVersion = 1

type InputKind string

const (
	InputUserMessage InputKind = "user.message"
	InputToolResult  InputKind = "tool.result"
)

type Input struct {
	Kind       InputKind `json:"kind"`
	Content    string    `json:"content"`
	ToolCallID string    `json:"tool_call_id,omitempty"`
}

type TurnRequest struct {
	ConversationID string            `json:"conversation_id"`
	TaskID         string            `json:"task_id"`
	RequestID      string            `json:"request_id"`
	SystemPrompt   string            `json:"system_prompt,omitempty"`
	WorkingDir     string            `json:"working_dir,omitempty"`
	Inputs         []Input           `json:"inputs"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

const (
	ToolWorkspaceShell     = "workspace.shell"
	ToolWorkspaceReadFile  = "workspace.read_file"
	ToolWorkspaceWriteFile = "workspace.write_file"
	ToolWorkspaceEditFile  = "workspace.edit_file"
	ToolWorkspaceGlob      = "workspace.glob"
	ToolWorkspaceGrep      = "workspace.grep"
)

type EventType string

const (
	EventAssistantDelta EventType = "assistant.delta"
	EventAssistantFinal EventType = "assistant.final"
	EventToolCallBatch  EventType = "tool.call.batch"
	EventTodoChanged    EventType = "todo.changed"
	EventTurnAwaiting   EventType = "turn.awaiting_tool"
	EventTurnCompleted  EventType = "turn.completed"
	EventTurnFailed     EventType = "turn.failed"
	EventDiagnostic     EventType = "diagnostic"
)

type Event struct {
	Type      EventType       `json:"type"`
	Text      string          `json:"text,omitempty"`
	ToolCalls []ToolCall      `json:"tool_calls,omitempty"`
	Error     string          `json:"error,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

func (e Event) IsExchangeTerminal() bool {
	switch e.Type {
	case EventTurnAwaiting, EventTurnCompleted, EventTurnFailed:
		return true
	default:
		return false
	}
}

type Envelope struct {
	Version    int             `json:"version"`
	ExchangeID string          `json:"exchange_id"`
	Type       string          `json:"type"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

func NewEnvelope(exchangeID, frameType string, payload any) (Envelope, error) {
	if strings.TrimSpace(exchangeID) == "" {
		return Envelope{}, fmt.Errorf("exchange id is required")
	}
	if strings.TrimSpace(frameType) == "" {
		return Envelope{}, fmt.Errorf("frame type is required")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal %s payload: %w", frameType, err)
	}
	return Envelope{Version: ProtocolVersion, ExchangeID: exchangeID, Type: frameType, Payload: raw}, nil
}

func (e Envelope) Validate() error {
	if e.Version != ProtocolVersion {
		return fmt.Errorf("unsupported agent runtime protocol version %d", e.Version)
	}
	if strings.TrimSpace(e.ExchangeID) == "" {
		return fmt.Errorf("exchange id is required")
	}
	if strings.TrimSpace(e.Type) == "" {
		return fmt.Errorf("frame type is required")
	}
	return nil
}

type Driver interface {
	Name() string
	Exchange(context.Context, TurnRequest, func(Event) error) error
	Cancel(context.Context, string) error
	Close(context.Context) error
}
