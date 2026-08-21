package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sasuke39/open-warp/internal/agentruntime"
	"github.com/sasuke39/open-warp/internal/config"
	pb "github.com/sasuke39/open-warp/internal/proto"
	"google.golang.org/protobuf/proto"
)

func TestTranslateExternalToolCall(t *testing.T) {
	tests := []struct {
		name     string
		tool     string
		args     string
		wantTool string
		contains string
	}{
		{"bash", agentruntime.ToolWorkspaceShell, `{"command":"pwd","workdir":"/tmp/a b"}`, "run_shell_command", `/tmp/a b`},
		{"read", agentruntime.ToolWorkspaceReadFile, `{"file_path":"main.go","offset":5,"limit":10}`, "read_files", `"end":14`},
		{"write", agentruntime.ToolWorkspaceWriteFile, `{"file_path":"new.txt","content":"hello"}`, "apply_file_diffs", `"new_files"`},
		{"edit", agentruntime.ToolWorkspaceEditFile, `{"file_path":"main.go","old_string":"a","new_string":"b"}`, "apply_file_diffs", `"search":"a"`},
		{"glob", agentruntime.ToolWorkspaceGlob, `{"pattern":"**/*.go","path":"src"}`, "file_glob_v2", `"search_dir":"src"`},
		{"grep", agentruntime.ToolWorkspaceGrep, `{"pattern":"TODO","path":"."}`, "grep", `"queries":["TODO"]`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			translated, err := translateExternalToolCall(agentruntime.ToolCall{
				ID: "call-1", Name: test.tool, Arguments: json.RawMessage(test.args),
			})
			if err != nil {
				t.Fatal(err)
			}
			if translated.Name != test.wantTool {
				t.Fatalf("tool = %q, want %q", translated.Name, test.wantTool)
			}
			if !strings.Contains(string(translated.Args), test.contains) {
				t.Fatalf("args %s do not contain %s", translated.Args, test.contains)
			}
		})
	}
}

func TestTranslateExternalToolCallRejectsUnknownTool(t *testing.T) {
	_, err := translateExternalToolCall(agentruntime.ToolCall{ID: "call-1", Name: "unknown", Arguments: json.RawMessage(`{}`)})
	if err == nil {
		t.Fatal("expected unknown external tool to be rejected")
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("a'b"); got != `'a'"'"'b'` {
		t.Fatalf("shellQuote = %q", got)
	}
}

type externalRuntimeTestDriver struct{}

func (externalRuntimeTestDriver) Name() string { return "test-runtime" }
func (externalRuntimeTestDriver) Exchange(_ context.Context, _ agentruntime.TurnRequest, emit func(agentruntime.Event) error) error {
	if err := emit(agentruntime.Event{Type: agentruntime.EventAssistantDelta, Text: "hello"}); err != nil {
		return err
	}
	return emit(agentruntime.Event{Type: agentruntime.EventTurnCompleted})
}
func (externalRuntimeTestDriver) Cancel(context.Context, string) error { return nil }
func (externalRuntimeTestDriver) Close(context.Context) error          { return nil }

func TestRunExternalAgentCreatesTaskBeforeFirstMessage(t *testing.T) {
	recorder := httptest.NewRecorder()
	server := &Server{}

	if ok := server.runExternalAgent(
		context.Background(), externalRuntimeTestDriver{}, recorder, recorder,
		&Conversation{}, "conversation-1", "request-1", "task-1", false,
		[]input{{Kind: "user_query", Content: "hello"}}, nil,
	); !ok {
		t.Fatal("expected external runtime turn to finish successfully")
	}

	events := decodeResponseEvents(t, recorder.Body.String())
	if len(events) < 3 {
		t.Fatalf("expected CreateTask, message, and finish events, got %d", len(events))
	}
	actions := events[0].GetClientActions().GetActions()
	if len(actions) != 1 || actions[0].GetCreateTask().GetTask().GetId() != "task-1" {
		t.Fatalf("first external runtime action must create task-1: %+v", actions)
	}
	if messages := events[1].GetClientActions().GetActions()[0].GetAddMessagesToTask().GetMessages(); len(messages) != 1 || messages[0].GetAgentOutput().GetText() != "hello" {
		t.Fatalf("second external runtime action must add the first message: %+v", messages)
	}
}

func TestRunExternalAgentDoesNotRecreateExistingTask(t *testing.T) {
	recorder := httptest.NewRecorder()
	server := &Server{}

	if ok := server.runExternalAgent(
		context.Background(), externalRuntimeTestDriver{}, recorder, recorder,
		&Conversation{}, "conversation-1", "request-1", "task-1", true,
		[]input{{Kind: "tool_result", ToolCallID: "call-1", Content: "ok"}}, nil,
	); !ok {
		t.Fatal("expected external runtime continuation to finish successfully")
	}

	for _, event := range decodeResponseEvents(t, recorder.Body.String()) {
		for _, action := range event.GetClientActions().GetActions() {
			if action.GetCreateTask() != nil {
				t.Fatal("existing task must not be created again")
			}
		}
	}
}

func TestHandleAgentRequestCreatesMissingTaskBeforeExternalOutput(t *testing.T) {
	disabled := false
	server := NewServer(&config.Config{
		Provider: "openai", BaseURL: "http://test.invalid/v1", APIKey: "test-key", Model: "test-model",
		Memory: config.MemoryConfig{Enabled: &disabled},
	}, filepath.Join(t.TempDir(), "config.yaml"))
	server.runtimeMu.Lock()
	server.runtimeDriver = externalRuntimeTestDriver{}
	server.runtimeMu.Unlock()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.closeBackground(ctx); err != nil {
			t.Errorf("close test server: %v", err)
		}
	})

	raw, err := proto.Marshal(&pb.Request{
		TaskContext: &pb.Request_TaskContext{},
		Input: &pb.Request_Input{Type: &pb.Request_Input_UserQuery_{
			UserQuery: &pb.Request_Input_UserQuery{Query: "hello"},
		}},
		Metadata: &pb.Request_Metadata{ConversationId: "conversation-http"},
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.handleAgentRequest(recorder, httptest.NewRequest(http.MethodPost, "/ai/multi-agent", bytes.NewReader(raw)))

	events := decodeResponseEvents(t, recorder.Body.String())
	createIndex, outputIndex := -1, -1
	for eventIndex, event := range events {
		for _, action := range event.GetClientActions().GetActions() {
			if action.GetCreateTask() != nil {
				createIndex = eventIndex
			}
			if action.GetAddMessagesToTask() != nil {
				outputIndex = eventIndex
			}
		}
	}
	if createIndex < 0 || outputIndex < 0 || createIndex >= outputIndex {
		t.Fatalf("HTTP event order must be StreamInit -> CreateTask -> output: create=%d output=%d events=%d", createIndex, outputIndex, len(events))
	}
}

var _ agentruntime.Driver = externalRuntimeTestDriver{}
