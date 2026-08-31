package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sasuke39/open-warp/internal/agentruntime"
	pb "github.com/sasuke39/open-warp/internal/proto"
)

// mockTerminalChainDriver models the framework side of a complete background
// command turn. It intentionally uses the public runtime protocol rather than
// calling the command helpers directly.
type mockTerminalChainDriver struct {
	exchange int
	jobID    string
}

func (driver *mockTerminalChainDriver) Name() string { return "mock-terminal-chain" }

func (driver *mockTerminalChainDriver) Exchange(
	_ context.Context,
	request agentruntime.TurnRequest,
	emit func(agentruntime.Event) error,
) error {
	switch driver.exchange {
	case 0:
		if len(request.Inputs) != 1 || request.Inputs[0].Kind != agentruntime.InputUserMessage {
			return fmt.Errorf("first exchange must contain the user prompt: %+v", request.Inputs)
		}
		arguments, _ := json.Marshal(map[string]any{
			"command":       "printf mock-ready; sleep 10; printf mock-done",
			"executionMode": "background",
			"commandId":     driver.jobID,
		})
		driver.exchange++
		if err := emit(agentruntime.Event{Type: agentruntime.EventToolCallBatch, ToolCalls: []agentruntime.ToolCall{{
			ID: "start-call", Name: agentruntime.ToolWorkspaceShell, Arguments: arguments,
		}}}); err != nil {
			return err
		}
		return emit(agentruntime.Event{Type: agentruntime.EventTurnAwaiting})
	case 1:
		if !hasRuntimeToolResult(request, "start-call", "status=running") {
			return fmt.Errorf("background start result was not returned: %+v", request.Inputs)
		}
		driver.exchange++
		return emitProcessTool(driver.jobID, "read-call", agentruntime.ToolWorkspaceProcessRead, emit)
	case 2:
		if !hasRuntimeToolResult(request, "read-call", "mock-ready") ||
			!hasRuntimeToolResult(request, "read-call", "status=running") {
			return fmt.Errorf("background output/status was not returned: %+v", request.Inputs)
		}
		driver.exchange++
		return emitProcessTool(driver.jobID, "cancel-call", agentruntime.ToolWorkspaceProcessCancel, emit)
	case 3:
		if !hasRuntimeToolResult(request, "cancel-call", "command cancelled") {
			return fmt.Errorf("background cancellation was not returned: %+v", request.Inputs)
		}
		driver.exchange++
		if err := emit(agentruntime.Event{Type: agentruntime.EventAssistantFinal, Text: "mock terminal chain complete"}); err != nil {
			return err
		}
		return emit(agentruntime.Event{Type: agentruntime.EventTurnCompleted})
	case 4:
		if len(request.Inputs) != 1 || request.Inputs[0].Kind != agentruntime.InputUserMessage || request.Inputs[0].Content != "next turn" {
			return fmt.Errorf("next turn was not accepted after cancellation: %+v", request.Inputs)
		}
		driver.exchange++
		if err := emit(agentruntime.Event{Type: agentruntime.EventAssistantFinal, Text: "next turn accepted"}); err != nil {
			return err
		}
		return emit(agentruntime.Event{Type: agentruntime.EventTurnCompleted})
	default:
		return fmt.Errorf("unexpected exchange %d", driver.exchange)
	}
}

func (driver *mockTerminalChainDriver) Cancel(context.Context, agentruntime.TurnControl) error {
	return nil
}
func (driver *mockTerminalChainDriver) Close(context.Context) error { return nil }

func emitProcessTool(
	jobID string,
	callID string,
	toolName string,
	emit func(agentruntime.Event) error,
) error {
	arguments, _ := json.Marshal(map[string]any{"commandId": jobID})
	if err := emit(agentruntime.Event{Type: agentruntime.EventToolCallBatch, ToolCalls: []agentruntime.ToolCall{{
		ID: callID, Name: toolName, Arguments: arguments,
	}}}); err != nil {
		return err
	}
	return emit(agentruntime.Event{Type: agentruntime.EventTurnAwaiting})
}

func hasRuntimeToolResult(request agentruntime.TurnRequest, callID, content string) bool {
	for _, input := range request.Inputs {
		if input.Kind == agentruntime.InputToolResult && input.ToolCallID == callID && strings.Contains(input.Content, content) {
			return true
		}
	}
	return false
}

// simulatedTerminal executes the exact RunShellCommand forwarded to Warp. The
// independent counter represents Session::execute_command; visiblePTYWrites
// represents the user's interactive terminal and must remain untouched.
type simulatedTerminal struct {
	independentExecutions int
	visiblePTYWrites      int
	jobID                 string
}

func (terminal *simulatedTerminal) executeTool(t *testing.T, call *pb.Message_ToolCall) string {
	t.Helper()
	run := call.GetRunShellCommand()
	if run == nil {
		t.Fatalf("mock terminal only accepts RunShellCommand: %+v", call)
	}
	command := run.GetCommand()
	if !run.GetWaitUntilComplete() {
		// Warp owns the managed-process wrapper. The Adapter forwards only the
		// original command plus background intent so the UI never shows this
		// transport detail.
		command = managedBackgroundStartCommand(terminal.jobID, command)
	}
	terminal.independentExecutions++
	output, err := exec.Command("bash", "-lc", command).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("exit error: %v\n%s", err, output)
	}
	return string(output)
}

func (terminal *simulatedTerminal) executeVisible(t *testing.T, command string) string {
	t.Helper()
	terminal.visiblePTYWrites++
	output, err := exec.Command("bash", "-lc", command).CombinedOutput()
	if err != nil {
		t.Fatalf("visible terminal command failed: %v\n%s", err, output)
	}
	return string(output)
}

func TestExternalRuntimeBackgroundCommandThroughSimulatedTerminal(t *testing.T) {
	jobID := uuid.NewString()
	t.Cleanup(func() { _ = os.RemoveAll(managedBackgroundJobDir(jobID)) })
	driver := &mockTerminalChainDriver{jobID: jobID}
	server := &Server{}
	conversation := &Conversation{}
	terminal := &simulatedTerminal{jobID: jobID}

	runExchange := func(inputs []input, existingTask bool) (*httptest.ResponseRecorder, bool) {
		recorder := httptest.NewRecorder()
		ok, active := server.runExternalAgent(
			context.Background(), driver, recorder, recorder,
			conversation, "conversation-mock", "turn-mock", uuid.NewString(), "task-mock", existingTask,
			inputs, nil,
		)
		if !ok {
			t.Fatalf("exchange failed: %s", recorder.Body.String())
		}
		return recorder, active
	}

	first, active := runExchange([]input{{Kind: "user_query", Content: "start service"}}, false)
	if !active {
		t.Fatal("background start must suspend for a tool result")
	}
	startCall := onlyForwardedToolCall(t, first)
	startResult := terminal.executeTool(t, startCall)
	if !strings.Contains(startResult, "status=running") {
		t.Fatalf("background launcher did not return a running job: %s", startResult)
	}

	// The background job is still sleeping, but the user's visible terminal is
	// independent and must remain immediately usable.
	if output := terminal.executeVisible(t, "printf visible-pty-ok"); output != "visible-pty-ok" {
		t.Fatalf("visible terminal was not usable: %q", output)
	}
	waitForMockJobOutput(t, jobID, "mock-ready")

	second, active := runExchange([]input{{Kind: "tool_result", ToolCallID: "start-call", Content: startResult}}, true)
	if !active {
		t.Fatal("output read must suspend for a tool result")
	}
	readResult := terminal.executeTool(t, onlyForwardedToolCall(t, second))

	third, active := runExchange([]input{{Kind: "tool_result", ToolCallID: "read-call", Content: readResult}}, true)
	if !active {
		t.Fatal("cancel must suspend for a tool result")
	}
	cancelResult := terminal.executeTool(t, onlyForwardedToolCall(t, third))

	final, active := runExchange([]input{{Kind: "tool_result", ToolCallID: "cancel-call", Content: cancelResult}}, true)
	if active {
		t.Fatal("final assistant response must release the turn")
	}
	if driver.exchange != 4 || terminal.independentExecutions != 3 || terminal.visiblePTYWrites != 1 {
		t.Fatalf("unexpected chain state: exchanges=%d independent=%d visible=%d", driver.exchange, terminal.independentExecutions, terminal.visiblePTYWrites)
	}
	if !responseContainsAgentText(t, final, "mock terminal chain complete") {
		t.Fatal("final assistant response was not forwarded")
	}

	next, active := runExchange([]input{{Kind: "user_query", Content: "next turn"}}, true)
	if active || driver.exchange != 5 {
		t.Fatalf("same conversation did not complete its next turn: active=%v exchanges=%d", active, driver.exchange)
	}
	if !responseContainsAgentText(t, next, "next turn accepted") {
		t.Fatal("same conversation did not forward the next turn response")
	}
}

func onlyForwardedToolCall(t *testing.T, recorder *httptest.ResponseRecorder) *pb.Message_ToolCall {
	t.Helper()
	calls := collectForwardedToolCalls(t, decodeResponseEvents(t, recorder.Body.String()))
	if len(calls) != 1 {
		t.Fatalf("forwarded tool calls = %d, want 1", len(calls))
	}
	return calls[0]
}

func waitForMockJobOutput(t *testing.T, jobID, expected string) {
	t.Helper()
	path := managedBackgroundJobDir(jobID) + "/output"
	deadline := time.Now().Add(3 * time.Second)
	for {
		content, _ := os.ReadFile(path)
		if strings.Contains(string(content), expected) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("background output %q did not appear in %s", expected, path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func responseContainsAgentText(t *testing.T, recorder *httptest.ResponseRecorder, expected string) bool {
	t.Helper()
	for _, event := range decodeResponseEvents(t, recorder.Body.String()) {
		for _, action := range event.GetClientActions().GetActions() {
			for _, message := range action.GetAddMessagesToTask().GetMessages() {
				if strings.Contains(message.GetAgentOutput().GetText(), expected) {
					return true
				}
			}
		}
	}
	return false
}
