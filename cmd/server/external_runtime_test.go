package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sasuke39/open-warp/internal/agentruntime"
	"github.com/sasuke39/open-warp/internal/config"
	"github.com/sasuke39/open-warp/internal/llm"
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
		{"bash", agentruntime.ToolWorkspaceShell, `{"command":"pwd","workdir":"/tmp/a b","executionMode":"foreground"}`, "run_shell_command", `/tmp/a b`},
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
			}, false)
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
	_, err := translateExternalToolCall(agentruntime.ToolCall{ID: "call-1", Name: "unknown", Arguments: json.RawMessage(`{}`)}, false)
	if err == nil {
		t.Fatal("expected unknown external tool to be rejected")
	}
}

func TestTranslateExternalShellExecutionModes(t *testing.T) {
	tests := []struct {
		name string
		args string
		wait bool
	}{
		{name: "background", args: `{"command":"sleep 30","executionMode":"background"}`, wait: true},
		{name: "foreground", args: `{"command":"echo done","executionMode":"foreground"}`, wait: true},
		{name: "dsh legacy background", args: `{"command":"sleep 30","run_in_background":true}`, wait: true},
		{name: "dsh legacy foreground", args: `{"command":"echo done","run_in_background":false}`, wait: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			translated, err := translateExternalToolCall(agentruntime.ToolCall{
				ID: "call-1", Name: agentruntime.ToolWorkspaceShell, Arguments: json.RawMessage(test.args),
			}, false)
			if err != nil {
				t.Fatal(err)
			}
			var args struct {
				WaitUntilComplete *bool `json:"wait_until_complete"`
			}
			if err := json.Unmarshal(translated.Args, &args); err != nil {
				t.Fatal(err)
			}
			if args.WaitUntilComplete == nil || *args.WaitUntilComplete != test.wait {
				t.Fatalf("wait_until_complete = %v, want %v", args.WaitUntilComplete, test.wait)
			}
		})
	}
}

func TestTranslateExternalShellRequiresExplicitExecutionMode(t *testing.T) {
	for _, args := range []string{
		`{"command":"pwd"}`,
		`{"command":"pwd","executionMode":"auto"}`,
	} {
		_, err := translateExternalToolCall(agentruntime.ToolCall{
			ID: "call-1", Name: agentruntime.ToolWorkspaceShell, Arguments: json.RawMessage(args),
		}, false)
		if err == nil {
			t.Fatalf("expected explicit execution mode error for %s", args)
		}
	}
}

func TestTranslateExternalShellRejectsUnknownExecutionMode(t *testing.T) {
	_, err := translateExternalToolCall(agentruntime.ToolCall{
		ID: "call-1", Name: agentruntime.ToolWorkspaceShell,
		Arguments: json.RawMessage(`{"command":"pwd","executionMode":"eventually"}`),
	}, false)
	if err == nil || !strings.Contains(err.Error(), "unsupported workspace.shell execution mode") {
		t.Fatalf("error = %v", err)
	}
}

func TestTranslateExternalShellRejectsIncompleteSyntax(t *testing.T) {
	_, err := translateExternalToolCall(agentruntime.ToolCall{
		ID: "call-incomplete", Name: agentruntime.ToolWorkspaceShell,
		Arguments: json.RawMessage(`{"command":"cat <<'EOF'\nmissing terminator","executionMode":"foreground"}`),
	}, true)
	if err == nil || !strings.Contains(err.Error(), "incomplete or invalid Bash syntax") {
		t.Fatalf("expected an actionable syntax error, got %v", err)
	}
}

func TestTranslateExternalBackgroundShellCreatesManagedJob(t *testing.T) {
	translated, err := translateExternalToolCall(agentruntime.ToolCall{
		ID: "background-1", Name: agentruntime.ToolWorkspaceShell,
		Arguments: json.RawMessage(`{"command":"./start.sh","executionMode":"background","commandId":"11111111-2222-4333-8444-555555555555"}`),
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	var args struct {
		Command           string `json:"command"`
		WaitUntilComplete bool   `json:"wait_until_complete"`
	}
	if err := json.Unmarshal(translated.Args, &args); err != nil {
		t.Fatal(err)
	}
	if !args.WaitUntilComplete {
		t.Fatal("background launcher must use the independent session executor")
	}
	if !strings.Contains(args.Command, "/tmp/warplocal-agent-jobs/11111111-2222-4333-8444-555555555555") ||
		!strings.Contains(args.Command, "status=running") {
		t.Fatalf("managed background launcher = %s", args.Command)
	}
	if err := validateShellSyntax(args.Command); err != nil {
		t.Fatalf("generated launcher must be valid Bash: %v\n%s", err, args.Command)
	}
}

func TestTranslateExternalProcessTools(t *testing.T) {
	const commandID = "11111111-2222-4333-8444-555555555555"
	tests := []struct {
		name     string
		tool     string
		args     string
		contains string
	}{
		{"read", agentruntime.ToolWorkspaceProcessRead, `{"commandId":"` + commandID + `"}`, "tail -c 65536"},
		{"write", agentruntime.ToolWorkspaceProcessWrite, `{"commandId":"` + commandID + `","input":"yes\\n"}`, "input delivered"},
		{"cancel", agentruntime.ToolWorkspaceProcessCancel, `{"commandId":"` + commandID + `"}`, "kill -TERM"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			translated, err := translateExternalToolCall(agentruntime.ToolCall{
				ID: "process-1", Name: test.tool, Arguments: json.RawMessage(test.args),
			}, true)
			if err != nil {
				t.Fatal(err)
			}
			var args struct {
				Command           string `json:"command"`
				WaitUntilComplete bool   `json:"wait_until_complete"`
			}
			if err := json.Unmarshal(translated.Args, &args); err != nil {
				t.Fatal(err)
			}
			if !args.WaitUntilComplete || !strings.Contains(args.Command, test.contains) {
				t.Fatalf("translated process command = %+v", args)
			}
			if err := validateShellSyntax(args.Command); err != nil {
				t.Fatalf("generated process command must be valid Bash: %v\n%s", err, args.Command)
			}
		})
	}
}

func TestManagedBackgroundCommandLifecycle(t *testing.T) {
	commandID := uuid.NewString()
	t.Cleanup(func() { _ = os.RemoveAll(managedBackgroundJobDir(commandID)) })
	start := exec.Command("bash", "-lc", managedBackgroundStartCommand(commandID, "printf hello; sleep 0.1; printf world"))
	output, err := start.CombinedOutput()
	if err != nil {
		t.Fatalf("start managed command: %v\n%s", err, output)
	}
	if !regexp.MustCompile(`status=running`).Match(output) {
		t.Fatalf("unexpected start output: %s", output)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		readOutput, readErr := exec.Command("bash", "-lc", managedBackgroundReadCommand(commandID)).CombinedOutput()
		if readErr != nil {
			t.Fatalf("read managed command: %v\n%s", readErr, readOutput)
		}
		if strings.Contains(string(readOutput), "status=exited:0") {
			if !strings.Contains(string(readOutput), "helloworld") {
				t.Fatalf("managed output was not captured: %s", readOutput)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("managed command did not finish: %s", readOutput)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestManagedBackgroundCommandAcceptsInput(t *testing.T) {
	commandID := uuid.NewString()
	t.Cleanup(func() { _ = os.RemoveAll(managedBackgroundJobDir(commandID)) })
	if output, err := exec.Command("bash", "-lc", managedBackgroundStartCommand(commandID, `read value; printf 'got:%s' "$value"`)).CombinedOutput(); err != nil {
		t.Fatalf("start managed command: %v\n%s", err, output)
	}
	if output, err := exec.Command("bash", "-lc", managedBackgroundWriteCommand(commandID, "ready\n")).CombinedOutput(); err != nil {
		t.Fatalf("write managed command: %v\n%s", err, output)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		output, err := exec.Command("bash", "-lc", managedBackgroundReadCommand(commandID)).CombinedOutput()
		if err != nil {
			t.Fatalf("read managed command: %v\n%s", err, output)
		}
		if strings.Contains(string(output), "status=exited:0") {
			if !strings.Contains(string(output), "got:ready") {
				t.Fatalf("managed command did not receive input: %s", output)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("managed command did not finish: %s", output)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestTranslateExternalReadUsesShellInManagedSSH(t *testing.T) {
	translated, err := translateExternalToolCall(agentruntime.ToolCall{
		ID: "read-1", Name: agentruntime.ToolWorkspaceReadFile,
		Arguments: json.RawMessage(`{"file_path":"/tmp/a b's.txt","offset":5,"limit":10}`),
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if translated.Name != "run_shell_command" {
		t.Fatalf("tool = %q, want run_shell_command", translated.Name)
	}
	args := string(translated.Args)
	if !strings.Contains(args, `sed -n '5,14p'`) || !strings.Contains(args, `/tmp/a b`) {
		t.Fatalf("managed SSH read args = %s", args)
	}
	if !strings.Contains(args, `"is_read_only":true`) {
		t.Fatalf("managed SSH read must be read-only: %s", args)
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("a'b"); got != `'a'"'"'b'` {
		t.Fatalf("shellQuote = %q", got)
	}
}

func TestExternalRuntimeWorkingDir(t *testing.T) {
	input := &pb.InputContext{Directory: &pb.InputContext_Directory{Pwd: "/opt/project"}}
	if got := externalRuntimeWorkingDir(input); got != "/opt/project" {
		t.Fatalf("working dir = %q, want /opt/project", got)
	}
	if got := externalRuntimeWorkingDir(nil); got != "" {
		t.Fatalf("nil working dir = %q, want empty", got)
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
func (externalRuntimeTestDriver) Cancel(context.Context, agentruntime.TurnControl) error {
	return nil
}
func (externalRuntimeTestDriver) Close(context.Context) error { return nil }

type recordingExternalRuntimeDriver struct {
	request agentruntime.TurnRequest
}

func (driver *recordingExternalRuntimeDriver) Name() string { return "recording-runtime" }
func (driver *recordingExternalRuntimeDriver) Exchange(_ context.Context, request agentruntime.TurnRequest, emit func(agentruntime.Event) error) error {
	driver.request = request
	return emit(agentruntime.Event{Type: agentruntime.EventTurnCompleted})
}
func (driver *recordingExternalRuntimeDriver) Cancel(context.Context, agentruntime.TurnControl) error {
	return nil
}
func (driver *recordingExternalRuntimeDriver) Close(context.Context) error { return nil }

func TestRunExternalAgentCreatesTaskBeforeFirstMessage(t *testing.T) {
	recorder := httptest.NewRecorder()
	server := &Server{}

	if ok, awaiting := server.runExternalAgent(
		context.Background(), externalRuntimeTestDriver{}, recorder, recorder,
		&Conversation{}, "conversation-1", "turn-1", "request-1", "task-1", false,
		[]input{{Kind: "user_query", Content: "hello"}}, nil,
	); !ok || awaiting {
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

	if ok, awaiting := server.runExternalAgent(
		context.Background(), externalRuntimeTestDriver{}, recorder, recorder,
		&Conversation{}, "conversation-1", "turn-1", "request-1", "task-1", true,
		[]input{{Kind: "tool_result", ToolCallID: "call-1", Content: "ok"}}, nil,
	); !ok || awaiting {
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

func TestRunExternalAgentCompletesPartialToolBatchBeforeResume(t *testing.T) {
	recorder := httptest.NewRecorder()
	server := &Server{}
	driver := &recordingExternalRuntimeDriver{}
	server.setExternalPending("task-1", []llm.ToolCall{
		{ID: "shell-call", Name: "run_shell_command"},
		{ID: "read-call", Name: "read_files"},
	})

	if ok, awaiting := server.runExternalAgent(
		context.Background(), driver, recorder, recorder,
		&Conversation{}, "conversation-1", "turn-1", "request-1", "task-1", true,
		[]input{{Kind: "tool_result", ToolCallID: "shell-call", Content: "ok"}}, nil,
	); !ok || awaiting {
		t.Fatal("expected completed external runtime continuation")
	}

	if len(driver.request.Inputs) != 2 {
		t.Fatalf("runtime inputs = %d, want complete batch of 2", len(driver.request.Inputs))
	}
	if driver.request.Inputs[0].ToolCallID != "shell-call" || driver.request.Inputs[0].Status != "success" {
		t.Fatalf("first runtime input = %+v", driver.request.Inputs[0])
	}
	if driver.request.Inputs[1].ToolCallID != "read-call" || driver.request.Inputs[1].Status != "error" {
		t.Fatalf("synthesized runtime input = %+v", driver.request.Inputs[1])
	}
}

type timeoutExternalRuntimeDriver struct {
	cancelled chan string
}

func (driver *timeoutExternalRuntimeDriver) Name() string { return "timeout-runtime" }
func (driver *timeoutExternalRuntimeDriver) Exchange(ctx context.Context, _ agentruntime.TurnRequest, _ func(agentruntime.Event) error) error {
	<-ctx.Done()
	return ctx.Err()
}
func (driver *timeoutExternalRuntimeDriver) Cancel(_ context.Context, control agentruntime.TurnControl) error {
	driver.cancelled <- control.TaskID
	return nil
}
func (driver *timeoutExternalRuntimeDriver) Close(context.Context) error { return nil }

func TestRunExternalAgentTimesOutAndCancelsFramework(t *testing.T) {
	recorder := httptest.NewRecorder()
	driver := &timeoutExternalRuntimeDriver{cancelled: make(chan string, 1)}
	server := &Server{cfg: &config.Config{Server: config.ServerConfig{StreamStallTimeoutSeconds: 1}}}

	if ok, active := server.runExternalAgent(
		context.Background(), driver, recorder, recorder,
		&Conversation{}, "conversation-1", "turn-timeout", "request-1", "task-timeout", true,
		[]input{{Kind: "user_query", Content: "hello"}}, nil,
	); ok || active {
		t.Fatal("timed-out external exchange must fail and become inactive")
	}
	select {
	case taskID := <-driver.cancelled:
		if taskID != "task-timeout" {
			t.Fatalf("cancelled task = %q", taskID)
		}
	default:
		t.Fatal("timed-out external exchange did not cancel the framework task")
	}
	_, errors := finishOutcome(decodeResponseEvents(t, recorder.Body.String()))
	if len(errors) != 1 || !strings.Contains(errors[0], "did not finish") {
		t.Fatalf("timeout errors = %+v", errors)
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

type cancelRecordingDriver struct {
	cancelled chan agentruntime.TurnControl
}

func (driver *cancelRecordingDriver) Name() string { return "cancel-recording-runtime" }
func (driver *cancelRecordingDriver) Exchange(context.Context, agentruntime.TurnRequest, func(agentruntime.Event) error) error {
	return nil
}
func (driver *cancelRecordingDriver) Cancel(_ context.Context, control agentruntime.TurnControl) error {
	driver.cancelled <- control
	return nil
}
func (driver *cancelRecordingDriver) Close(context.Context) error { return nil }

func TestHandleCancelTaskCancelsSuspendedExternalTurn(t *testing.T) {
	driver := &cancelRecordingDriver{cancelled: make(chan agentruntime.TurnControl, 1)}
	server := &Server{}
	turn := server.activeTurns.begin("task-suspended", "conversation-1", driver)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/agent/tasks/task-suspended/cancel", nil)
	request.SetPathValue("task_id", "task-suspended")
	server.handleCancelTask(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, want 200", recorder.Code)
	}
	select {
	case control := <-driver.cancelled:
		if control.TaskID != "task-suspended" || control.ConversationID != "conversation-1" || control.TurnID != turn.snapshot().TurnID {
			t.Fatalf("cancelled Turn = %+v", control)
		}
	case <-time.After(time.Second):
		t.Fatal("external runtime was not cancelled")
	}
	if _, ok := server.activeTurns.loadByWarpTask("task-suspended"); ok {
		t.Fatal("cancelled external Turn must be removed")
	}
}

type steerRecordingDriver struct {
	request   agentruntime.TurnRequest
	exchanges int
}

func (driver *steerRecordingDriver) Name() string { return "steer-recording-runtime" }
func (driver *steerRecordingDriver) Exchange(_ context.Context, request agentruntime.TurnRequest, emit func(agentruntime.Event) error) error {
	driver.request = request
	driver.exchanges++
	steerID := ""
	if len(request.Inputs) > 0 {
		steerID = request.Inputs[0].SteerID
	}
	return emit(agentruntime.Event{Type: agentruntime.EventSteerAccepted, SteerID: steerID})
}

func TestHandleSteerTaskIsIdempotentBySteerID(t *testing.T) {
	driver := &steerRecordingDriver{}
	server := &Server{}
	server.activeTurns.begin("task-active", "runtime-conversation", driver)
	body := `{"conversation_id":"ui-conversation","prompt":"use mirror","steer_id":"6e9b5b7f-a80f-4f7f-bb8b-a9328820bd3b"}`
	for attempt := 0; attempt < 2; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/agent/tasks/task-active/steer", bytes.NewBufferString(body))
		request.SetPathValue("task_id", "task-active")
		recorder := httptest.NewRecorder()
		server.handleSteerTask(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("attempt %d status = %d, body = %s", attempt, recorder.Code, recorder.Body.String())
		}
	}
	if driver.exchanges != 1 {
		t.Fatalf("runtime received %d steer exchanges, want 1", driver.exchanges)
	}
}
func (driver *steerRecordingDriver) Cancel(context.Context, agentruntime.TurnControl) error {
	return nil
}
func (driver *steerRecordingDriver) Close(context.Context) error { return nil }

func TestHandleSteerTaskInjectsGuidanceIntoActiveTurn(t *testing.T) {
	driver := &steerRecordingDriver{}
	server := &Server{}
	turn := server.activeTurns.begin("task-active", "runtime-conversation", driver)
	body := bytes.NewBufferString(`{"conversation_id":"ui-conversation","prompt":"use the mirror next"}`)
	request := httptest.NewRequest(http.MethodPost, "/agent/tasks/task-active/steer", body)
	request.SetPathValue("task_id", "task-active")
	recorder := httptest.NewRecorder()

	server.handleSteerTask(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("steer status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if driver.request.ConversationID != "runtime-conversation" || driver.request.TurnID != turn.snapshot().TurnID || driver.request.TaskID != "task-active" {
		t.Fatalf("steer request identity = %+v", driver.request)
	}
	if len(driver.request.Inputs) != 1 || driver.request.Inputs[0].Kind != agentruntime.InputUserSteer || driver.request.Inputs[0].Content != "use the mirror next" {
		t.Fatalf("steer inputs = %+v", driver.request.Inputs)
	}
}

func TestAgentControlRoutesAcceptWarpPublicAPIPrefix(t *testing.T) {
	for _, path := range []string{
		"/agent/tasks/task-active/steer",
		"/api/v1/agent/tasks/task-active/steer",
	} {
		t.Run(path, func(t *testing.T) {
			driver := &steerRecordingDriver{}
			server := &Server{}
			server.activeTurns.begin("task-active", "runtime-conversation", driver)
			mux := http.NewServeMux()
			registerAgentControlRoutes(mux, server)
			request := httptest.NewRequest(
				http.MethodPost,
				path,
				bytes.NewBufferString(`{"conversation_id":"conversation-1","prompt":"steer now"}`),
			)
			recorder := httptest.NewRecorder()

			mux.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusOK {
				t.Fatalf("steer route %s status = %d, body = %s", path, recorder.Code, recorder.Body.String())
			}
			if driver.request.TaskID != "task-active" || len(driver.request.Inputs) != 1 || driver.request.Inputs[0].Content != "steer now" {
				t.Fatalf("steer route %s request = %+v", path, driver.request)
			}
		})
	}
}

func TestHandleSteerTaskRejectsInactiveTurn(t *testing.T) {
	server := &Server{}
	body := bytes.NewBufferString(`{"conversation_id":"conversation-1","prompt":"continue"}`)
	request := httptest.NewRequest(http.MethodPost, "/agent/tasks/missing/steer", body)
	request.SetPathValue("task_id", "missing")
	recorder := httptest.NewRecorder()

	server.handleSteerTask(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("steer status = %d, want 409", recorder.Code)
	}
}

type blockingExternalDriver struct {
	started   chan struct{}
	cancelled chan string
	stopped   chan struct{}
	stopOnce  sync.Once
}

func (driver *blockingExternalDriver) Name() string { return "blocking-runtime" }
func (driver *blockingExternalDriver) Exchange(ctx context.Context, _ agentruntime.TurnRequest, _ func(agentruntime.Event) error) error {
	close(driver.started)
	select {
	case <-driver.stopped:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (driver *blockingExternalDriver) Cancel(_ context.Context, control agentruntime.TurnControl) error {
	driver.cancelled <- control.TaskID
	driver.stopOnce.Do(func() { close(driver.stopped) })
	return nil
}
func (driver *blockingExternalDriver) Close(context.Context) error { return nil }

func TestHandleCancelTaskAcknowledgesRunningExternalTurn(t *testing.T) {
	disabled := false
	driver := &blockingExternalDriver{
		started: make(chan struct{}), cancelled: make(chan string, 1), stopped: make(chan struct{}),
	}
	server := NewServer(&config.Config{
		Provider: "openai", BaseURL: "http://test.invalid/v1", APIKey: "test-key", Model: "test-model",
		Memory: config.MemoryConfig{Enabled: &disabled},
	}, filepath.Join(t.TempDir(), "config.yaml"))
	server.runtimeDriver = driver
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.closeBackground(ctx); err != nil {
			t.Errorf("close test server: %v", err)
		}
	})

	raw, err := proto.Marshal(&pb.Request{
		TaskContext: &pb.Request_TaskContext{Tasks: []*pb.Task{{Id: "task-running"}}},
		Input: &pb.Request_Input{Type: &pb.Request_Input_UserQuery_{
			UserQuery: &pb.Request_Input_UserQuery{Query: "keep working"},
		}},
		Metadata: &pb.Request_Metadata{ConversationId: "conversation-running"},
	})
	if err != nil {
		t.Fatal(err)
	}
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		server.handleAgentRequest(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/ai/multi-agent", bytes.NewReader(raw)))
	}()
	select {
	case <-driver.started:
	case <-time.After(time.Second):
		t.Fatal("external exchange did not start")
	}

	recorder := httptest.NewRecorder()
	cancelRequest := httptest.NewRequest(http.MethodPost, "/agent/tasks/task-running/cancel", nil)
	cancelRequest.SetPathValue("task_id", "task-running")
	server.handleCancelTask(recorder, cancelRequest)
	if recorder.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, want 200", recorder.Code)
	}
	select {
	case taskID := <-driver.cancelled:
		if taskID != "task-running" {
			t.Fatalf("cancelled task = %q", taskID)
		}
	case <-time.After(time.Second):
		t.Fatal("running external runtime was not cancelled")
	}
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("cancelled request did not finish")
	}
}
