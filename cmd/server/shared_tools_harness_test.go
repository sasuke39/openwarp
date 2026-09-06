package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/openai/openai-go"
	"github.com/sasuke39/open-warp/internal/agent"
	"github.com/sasuke39/open-warp/internal/agentruntime"
	"github.com/sasuke39/open-warp/internal/config"
	"github.com/sasuke39/open-warp/internal/llm"
	"github.com/sasuke39/open-warp/internal/remotemcp"
	"github.com/sasuke39/open-warp/internal/workspacetools"
)

func TestEveryHarnessToolRoundTripOverDesktopSSH(t *testing.T) {
	socket := os.Getenv("WARPLOCAL_MCP_TEST_SOCKET")
	if socket == "" {
		t.Skip("requires production desktop IPC and isolated local sshd")
	}
	for _, name := range config.BundledAgentDrivers {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				text := string(body)
				w.Header().Set("Content-Type", "text/event-stream")
				delta := map[string]any{"role": "assistant", "content": "TOOL_ROUND_OK"}
				finish := "stop"
				if strings.Contains(text, "LATER_TURN") {
					delta["content"] = "LATER_TURN_OK"
				} else if !strings.Contains(text, "SHARED_TOOL_OK_RESULT") {
					tool := "bash"
					args := `{"command":"printf SHARED_TOOL_OK","execution_mode":"foreground","run_in_background":false,"description":"Read-only shared SSH tool smoke test"}`
					if name == "native" {
						tool = "run_shell_command"
						args = `{"command":"printf SHARED_TOOL_OK","wait_until_complete":true,"is_read_only":true,"is_risky":false}`
					}
					delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "call-shared", "type": "function", "function": map[string]any{"name": tool, "arguments": args}}}}
					finish = "tool_calls"
				}
				for _, choice := range []map[string]any{{"index": 0, "delta": delta, "finish_reason": nil}, {"index": 0, "delta": map[string]any{}, "finish_reason": finish}} {
					payload, _ := json.Marshal(map[string]any{"id": "shared", "object": "chat.completion.chunk", "model": "contract-model", "choices": []any{choice}})
					fmt.Fprintf(w, "data: %s\n\n", payload)
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer model.Close()
			execute := func(command string) string {
				t.Helper()
				result, err := (remotemcp.IPC{Socket: socket}).Call(ctx, remotemcp.Request{RequestID: uuid.NewString(), Operation: "shell", ServerID: "local-test", Command: command})
				if err != nil || result["stdout"] != "SHARED_TOOL_OK" {
					t.Fatalf("actual client SSH: %v %v", result, err)
				}
				if id, ok := result["command_id"].(string); ok && workspacetools.ValidBackgroundCommandID(id) {
					t.Cleanup(func() { _ = os.RemoveAll(workspacetools.ManagedBackgroundJobDir(id)) })
				}
				return "SHARED_TOOL_OK_RESULT"
			}
			if name == "native" {
				disabled := false
				s := NewServer(&config.Config{Provider: "openai", BaseURL: model.URL + "/v1", APIKey: "fixture", Model: "contract-model", Memory: config.MemoryConfig{Enabled: &disabled}}, filepath.Join(t.TempDir(), "config.yaml"))
				defer s.closeBackground(ctx)
				conv := &Conversation{client: llm.NewClient(s.cfg), history: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("RUN_SHARED_TOOL")}}
				rec := httptest.NewRecorder()
				s.runAgentLoop(ctx, rec, rec, conv, "req1", "task1", false, "", conv.history, agent.ManagedSSHTarget{})
				call := onlyForwardedToolCall(t, rec)
				if call.GetRunShellCommand() == nil {
					t.Fatal("missing native shell call")
				}
				result := execute(call.GetRunShellCommand().GetCommand())
				conv.history = append(conv.history, openai.ToolMessage(result, call.GetToolCallId()))
				rec = httptest.NewRecorder()
				s.runAgentLoop(ctx, rec, rec, conv, "req2", "task1", true, "", conv.history, agent.ManagedSSHTarget{})
				if !responseContainsAgentText(t, rec, "TOOL_ROUND_OK") {
					t.Fatalf("native completion: %s", rec.Body.String())
				}
				conv.history = append(conv.history, openai.UserMessage("LATER_TURN"))
				rec = httptest.NewRecorder()
				s.runAgentLoop(ctx, rec, rec, conv, "req3", "task2", false, "", conv.history, agent.ManagedSSHTarget{})
				if !responseContainsAgentText(t, rec, "LATER_TURN_OK") {
					t.Fatal("native later Turn failed")
				}
				return
			}
			base := model.URL
			if name == "pi-agent" {
				base += "/v1"
			}
			harness := newProcessContractAgent(t, name, base).(*processContractAgent)
			harness.nextID = 1 // The manually driven first Turn below already uses turn1.
			defer harness.Close(ctx)
			req := agentruntime.TurnRequest{ConversationID: uuid.NewString(), TurnID: "turn1", TaskID: "task1", RequestID: "req1", Inputs: []agentruntime.Input{{Kind: agentruntime.InputUserMessage, Content: "RUN_SHARED_TOOL"}}}
			var calls []agentruntime.ToolCall
			err := harness.driver.Exchange(ctx, req, func(e agentruntime.Event) error {
				calls = append(calls, e.ToolCalls...)
				if e.Type == agentruntime.EventTurnFailed {
					return fmt.Errorf("%s", e.Error)
				}
				return nil
			})
			if err != nil || len(calls) != 1 {
				t.Fatalf("tool request: %v %v", calls, err)
			}
			translated, err := translateExternalToolCall(calls[0], true)
			if err != nil {
				t.Fatal(err)
			}
			var args struct {
				Command string `json:"command"`
			}
			json.Unmarshal(translated.Args, &args)
			result := execute(args.Command)
			req.RequestID = "req2"
			req.Inputs = []agentruntime.Input{{Kind: agentruntime.InputToolResult, ToolCallID: calls[0].ID, Content: result, Status: "success"}}
			text := ""
			done := false
			err = harness.driver.Exchange(ctx, req, func(e agentruntime.Event) error {
				text += e.Text
				done = done || e.Type == agentruntime.EventTurnCompleted
				return nil
			})
			if err != nil || !done || !strings.Contains(text, "TOOL_ROUND_OK") {
				t.Fatalf("completion: %q %v %v", text, done, err)
			}
			next := harness.Turn(ctx, req.ConversationID, "LATER_TURN")
			assertContractResult(t, next, "LATER_TURN_OK")
		})
	}
}
