package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sasuke39/open-warp/internal/agent"
	"github.com/sasuke39/open-warp/internal/config"
	"github.com/sasuke39/open-warp/internal/llm"
	"github.com/sasuke39/open-warp/internal/memory"
	pb "github.com/sasuke39/open-warp/internal/proto"

	"github.com/openai/openai-go"
	"google.golang.org/protobuf/proto"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type finishOrderingWriter struct {
	t              *testing.T
	beforeFinished *bool
}

func (w *finishOrderingWriter) Write(p []byte) (int, error) {
	if !*w.beforeFinished {
		w.t.Error("StreamFinished was written before durable enqueue callback")
	}
	return len(p), nil
}

func (*finishOrderingWriter) Flush() {}

func TestFinishEventRunsDurableEnqueueFirst(t *testing.T) {
	committed := false
	ctx := context.WithValue(context.Background(), beforeAgentFinishKey{}, func() { committed = true })
	w := &finishOrderingWriter{t: t, beforeFinished: &committed}
	if ok := (&Server{}).finishSuccessfulAgentLoop(ctx, w, w); !ok || !committed {
		t.Fatalf("finished=%v committed=%v", ok, committed)
	}
}

func TestNormalizeConversationHistory_PrunesDanglingAssistantToolCallBeforeUserQuery(t *testing.T) {
	history := []openai.ChatCompletionMessageParamUnion{
		llm.MakeUserMessage("A"),
		llm.MakeAssistantToolCallMessage([]llm.ToolCall{
			{ID: "call-1", Name: "run_shell_command", Args: []byte(`{"command":"echo a"}`)},
		}, ""),
		llm.MakeUserMessage("B"),
	}

	normalized, changed := normalizeConversationHistory(history)
	if !changed {
		t.Fatalf("expected history normalization to report changes")
	}
	if got := len(normalized); got != 2 {
		t.Fatalf("expected 2 messages after pruning, got %d", got)
	}
	if normalized[0].OfUser == nil || normalized[1].OfUser == nil {
		t.Fatalf("expected remaining messages to be user messages")
	}
}

func TestNormalizeConversationHistory_PrunesIncompleteAssistantToolCallAndPartialToolResults(t *testing.T) {
	history := []openai.ChatCompletionMessageParamUnion{
		llm.MakeUserMessage("start"),
		llm.MakeAssistantToolCallMessage([]llm.ToolCall{
			{ID: "call-1", Name: "run_shell_command", Args: []byte(`{"command":"a"}`)},
			{ID: "call-2", Name: "run_shell_command", Args: []byte(`{"command":"b"}`)},
		}, ""),
		llm.MakeToolResultMessage("call-1", "ok"),
		llm.MakeUserMessage("next"),
	}

	normalized, changed := normalizeConversationHistory(history)
	if !changed {
		t.Fatalf("expected history normalization to report changes")
	}
	if got := len(normalized); got != 2 {
		t.Fatalf("expected 2 messages after pruning incomplete round, got %d", got)
	}
	if normalized[0].OfUser == nil || normalized[1].OfUser == nil {
		t.Fatalf("expected remaining messages to be user messages")
	}
}

func TestNormalizeConversationHistory_LeavesValidToolRoundIntact(t *testing.T) {
	history := []openai.ChatCompletionMessageParamUnion{
		llm.MakeUserMessage("start"),
		llm.MakeAssistantToolCallMessage([]llm.ToolCall{
			{ID: "call-1", Name: "run_shell_command", Args: []byte(`{"command":"a"}`)},
		}, ""),
		llm.MakeToolResultMessage("call-1", "ok"),
		llm.MakeUserMessage("next"),
	}

	normalized, changed := normalizeConversationHistory(history)
	if changed {
		t.Fatalf("expected valid history to remain unchanged")
	}
	if got := len(normalized); got != len(history) {
		t.Fatalf("expected %d messages, got %d", len(history), got)
	}
}

func TestNormalizeConversationHistory_LeavesValidToolRoundWithReasoningIntact(t *testing.T) {
	history := []openai.ChatCompletionMessageParamUnion{
		llm.MakeUserMessage("start"),
		llm.MakeAssistantToolCallMessage([]llm.ToolCall{
			{ID: "call-1", Name: "run_shell_command", Args: []byte(`{"command":"ls -la"}`)},
		}, "先看一下目录"),
		llm.MakeToolResultMessage("call-1", "ok"),
	}

	normalized, changed := normalizeConversationHistory(history)
	if changed {
		t.Fatalf("expected valid history with reasoning_content to remain unchanged")
	}
	if got := len(normalized); got != len(history) {
		t.Fatalf("expected %d messages, got %d", len(history), got)
	}
	if normalized[1].OfAssistant == nil {
		t.Fatalf("expected assistant tool call message to be preserved")
	}
	if normalized[2].OfTool == nil || normalized[2].OfTool.ToolCallID != "call-1" {
		t.Fatalf("expected tool result to remain paired with call-1")
	}
}

func TestNormalizeConversationHistory_PrunesStrayToolMessage(t *testing.T) {
	history := []openai.ChatCompletionMessageParamUnion{
		llm.MakeUserMessage("start"),
		llm.MakeToolResultMessage("call-1", "orphan"),
		llm.MakeUserMessage("next"),
	}

	normalized, changed := normalizeConversationHistory(history)
	if !changed {
		t.Fatalf("expected stray tool message to be pruned")
	}
	if got := len(normalized); got != 2 {
		t.Fatalf("expected 2 messages after pruning stray tool message, got %d", got)
	}
	if normalized[0].OfUser == nil || normalized[1].OfUser == nil {
		t.Fatalf("expected remaining messages to be user messages")
	}
}

func TestMemoryStatusResponseIncludesSessionAndProjectDetails(t *testing.T) {
	dir := t.TempDir()
	enabled := true
	cfg := &config.Config{
		Memory: config.MemoryConfig{
			Enabled:        &enabled,
			SessionEnabled: &enabled,
			AutoEnabled:    &enabled,
			BaseDir:        dir,
		},
	}
	s := NewServer(cfg, filepath.Join(dir, "config.yaml"))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.closeBackground(ctx)
	})

	notes := memory.DefaultSessionNotes("Test Session")
	notes = strings.Replace(notes, "# Worklog\n\n", "# Worklog\n- status endpoint test\n\n", 1)
	if err := s.memoryStore.WriteSessionNotes("conv1", notes); err != nil {
		t.Fatal(err)
	}
	if err := s.memoryStore.InitProjectMemory("project1"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/settings/memory/status?conversation_id=conv1&project_key=project1", nil)
	rec := httptest.NewRecorder()
	s.handleMemoryStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got memoryStatusResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || !got.SessionEnabled || !got.AutoEnabled {
		t.Fatalf("expected memory flags to be enabled: %+v", got)
	}
	if !got.QueueAvailable {
		t.Fatalf("expected durable memory queue to be available: %+v", got)
	}
	if got.BaseDir != dir || got.CurrentProjectKey != "project1" {
		t.Fatalf("unexpected base/project: %+v", got)
	}
	if !got.Session.NotesExists || got.Session.NotesBytes == 0 {
		t.Fatalf("expected session notes details: %+v", got.Session)
	}
	if !got.Project.MemoryIndexExists || got.Project.MemoryCount != 4 {
		t.Fatalf("expected initialized project memory details: %+v", got.Project)
	}
	if got.ContextWindow.Tokens != 32000 || got.ContextWindow.CompactionAtChars != 89600 {
		t.Fatalf("expected default context window compaction details: %+v", got.ContextWindow)
	}
}

func TestMemoryClearSessionRejectsInvalidIDAndAppendsEvent(t *testing.T) {
	dir := t.TempDir()
	enabled := true
	cfg := &config.Config{Memory: config.MemoryConfig{Enabled: &enabled, BaseDir: dir}}
	s := NewServer(cfg, filepath.Join(dir, "config.yaml"))
	notes := memory.DefaultSessionNotes("Clear Test")
	if err := s.memoryStore.WriteSessionNotes("conv1", notes); err != nil {
		t.Fatal(err)
	}

	badReq := httptest.NewRequest(http.MethodPost, "/settings/memory/clear-session", bytes.NewBufferString(`{"conversation_id":"../conv1"}`))
	badRec := httptest.NewRecorder()
	s.handleMemoryClearSession(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid id to return 400, got %d", badRec.Code)
	}

	req := httptest.NewRequest(http.MethodPost, "/settings/memory/clear-session", bytes.NewBufferString(`{"conversation_id":"conv1"}`))
	rec := httptest.NewRecorder()
	s.handleMemoryClearSession(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, memory.SessionNotesRelPath("conv1"))); !os.IsNotExist(err) {
		t.Fatalf("expected notes to be removed, stat err=%v", err)
	}
	events, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(events), "session_memory_cleared") {
		t.Fatalf("expected clear event, got %s", string(events))
	}
}

func TestResolveProjectKeyFromRequestPrefersProjectRulesRoot(t *testing.T) {
	root := t.TempDir()
	s := &Server{}
	req := &pb.Request{
		Input: &pb.Request_Input{
			Context: &pb.InputContext{
				ProjectRules: []*pb.InputContext_ProjectRules{{RootPath: root}},
			},
		},
	}
	got := s.resolveProjectKeyFromRequest(req, "conv1")
	want := memory.ProjectKeyFromRoot(root)
	if got != want {
		t.Fatalf("expected project key %q, got %q", want, got)
	}
}

func TestWorkspaceRootFromRequestIgnoresManagedSSHRuntimeContext(t *testing.T) {
	req := &pb.Request{
		Input: &pb.Request_Input{
			Context: &pb.InputContext{
				ProjectRules: []*pb.InputContext_ProjectRules{
					{RootPath: agent.ManagedSSHContextRoot},
					{RootPath: "/srv/project"},
				},
			},
		},
	}

	if got := workspaceRootFromRequest(req); got != "/srv/project" {
		t.Fatalf("expected actual project root, got %q", got)
	}
}

func TestEnforceManagedSSHPolicyBlocksSameHostAndAllowsDifferentHost(t *testing.T) {
	target := agent.ManagedSSHTarget{
		Host:            "47.115.32.237",
		SessionHostname: "iZwz94kqmvp7aaxi22dsh5Z",
	}
	toolCalls := []llm.ToolCall{
		{
			ID:   "same-host",
			Name: "run_shell_command",
			Args: json.RawMessage(`{"command":"ssh root@47.115.32.237 'pwd'"}`),
		},
		{
			ID:   "different-host",
			Name: "run_shell_command",
			Args: json.RawMessage(`{"command":"ssh app@10.0.2.15"}`),
		},
	}

	allowed, denied := enforceManagedSSHPolicy(toolCalls, target, "", nil)

	if len(allowed) != 1 || allowed[0].ID != "different-host" {
		t.Fatalf("expected only different-host call to pass, got %+v", allowed)
	}
	if len(denied) != 1 || denied[0].ID != "same-host" {
		t.Fatalf("expected same-host call to be denied, got %+v", denied)
	}
}

func TestEnforceManagedSSHPolicyBlocksTransferForPendingSameHostSSH(t *testing.T) {
	target := agent.ManagedSSHTarget{Host: "47.115.32.237"}
	history := []openai.ChatCompletionMessageParamUnion{
		llm.MakeAssistantToolCallMessage([]llm.ToolCall{
			{
				ID:   "shell-call",
				Name: "run_shell_command",
				Args: json.RawMessage(`{"command":"ssh root@47.115.32.237 'pwd'"}`),
			},
		}, ""),
	}
	transfer := []llm.ToolCall{
		{
			ID:   "transfer-call",
			Name: "transfer_shell_command_control_to_user",
			Args: json.RawMessage(`{"command_id":"precmd-1","reason":"password"}`),
		},
	}

	allowed, denied := enforceManagedSSHPolicy(transfer, target, "precmd-1", history)

	if len(allowed) != 0 {
		t.Fatalf("pending redundant SSH transfer must not be allowed: %+v", allowed)
	}
	if len(denied) != 1 || denied[0].ID != "transfer-call" {
		t.Fatalf("expected transfer denial, got %+v", denied)
	}
}

func TestEnforcePathPolicyBlocksConfirmPaths(t *testing.T) {
	s := &Server{}
	args := []byte(`{"new_files":[{"file_path":"new.md","content":"hello"}]}`)
	allowed, denied := s.enforcePathPolicy([]llm.ToolCall{{ID: "call-1", Name: "apply_file_diffs", Args: args}})
	if len(allowed) != 0 {
		t.Fatalf("expected no allowed calls, got %d", len(allowed))
	}
	if len(denied) != 1 || !strings.Contains(denied[0].Message, "requires explicit user confirmation") {
		t.Fatalf("expected confirm denial, got %+v", denied)
	}
}

func TestEnforcePathPolicyAllowsLongReviewInstructionException(t *testing.T) {
	s := &Server{}
	content := strings.Repeat("line\n", 100)
	args, err := json.Marshal(map[string]any{
		"new_files": []map[string]string{{
			"file_path": "记忆系统设计方案/implementation-spec/07-code-review-fix-implementation.md",
			"content":   content,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	allowed, denied := s.enforcePathPolicy([]llm.ToolCall{{ID: "call-1", Name: "apply_file_diffs", Args: args}})
	if len(denied) != 0 || len(allowed) != 1 {
		t.Fatalf("expected exception file to be allowed, allowed=%d denied=%+v", len(allowed), denied)
	}
}

func TestEnforcePathPolicyRejectsDiffThatExceedsMarkdownLimit(t *testing.T) {
	dir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(oldWd); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("记忆系统设计方案", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("记忆系统设计方案/a.md", []byte("# A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(map[string]any{
		"diffs": []map[string]string{{
			"file_path": "记忆系统设计方案/a.md",
			"search":    "# A\n",
			"replace":   strings.Repeat("line\n", 71),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{}
	allowed, denied := s.enforcePathPolicy([]llm.ToolCall{{ID: "call-1", Name: "apply_file_diffs", Args: args}})
	if len(allowed) != 0 || len(denied) != 1 {
		t.Fatalf("expected markdown limit denial, allowed=%d denied=%+v", len(allowed), denied)
	}
	if !strings.Contains(denied[0].Message, "exceeds 70 line limit") {
		t.Fatalf("unexpected denial message: %s", denied[0].Message)
	}
}

func TestEndToEndAgentRequestUpdatesSessionAndProjectMemory(t *testing.T) {
	dir := t.TempDir()
	workspaceRoot := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	enabled := true
	cfg := &config.Config{
		Provider: "openai",
		BaseURL:  "http://mock-llm.local/v1",
		APIKey:   "test-key",
		Model:    "test-model",
		Memory: config.MemoryConfig{
			Enabled:          &enabled,
			SessionEnabled:   &enabled,
			AutoEnabled:      &enabled,
			BaseDir:          filepath.Join(dir, "memory"),
			MaxProjectMemory: 5,
			StaleAfterHours:  24,
		},
	}
	s := NewServer(cfg, filepath.Join(dir, "config.yaml"))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.closeBackground(ctx)
	})
	mockHTTP := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		raw := string(body)
		var payload string
		header := http.Header{}
		status := 200
		if strings.Contains(raw, `"stream":true`) {
			header.Set("Content-Type", "text/event-stream")
			payload = "data: {\"id\":\"chatcmpl-stream\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Memory verification response.\"},\"finish_reason\":null}]}\n\n" +
				"data: {\"id\":\"chatcmpl-stream\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: [DONE]\n\n"
		} else {
			header.Set("Content-Type", "application/json")
			switch {
			case strings.Contains(raw, "Extract durable project knowledge as a JSON patch"):
				payload = `{"id":"chatcmpl-project","object":"chat.completion","created":0,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"{\"updates\":[{\"path\":\"workflows.md\",\"mode\":\"append_or_replace_section\",\"section\":\"Build\",\"content\":\"- go test ./...\\n- go run ./cmd/server\"}]}"},"finish_reason":"stop"}]}`
			default:
				notes := "# Session Title\nE2E Session\n\n# Current State\nMemory verification response processed.\n\n# Task Specification\nVerify that session and project memory are written.\n\n# Files And Functions\ncmd/server/main.go\n\n# Workflow\nrequest -> stream -> extractor -> files\n\n# Errors And Corrections\nnone\n\n# Tool Results Worth Keeping\nstatus endpoint shows session/project fields\n\n# Decisions\nproject key should come from request root\n\n# Key Results\nsession notes and project memory updated\n\n# Worklog\n- mock llm completed end-to-end path\n"
				enc, _ := json.Marshal(notes)
				payload = `{"id":"chatcmpl-session","object":"chat.completion","created":0,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":` + string(enc) + `},"finish_reason":"stop"}]}`
			}
		}
		return &http.Response{
			StatusCode: status,
			Header:     header,
			Body:       io.NopCloser(strings.NewReader(payload)),
			Request:    r,
		}, nil
	})}
	s.conversations["conv-e2e"] = &Conversation{
		client:    llm.NewClientWithHTTPClient(cfg, mockHTTP),
		CreatedAt: time.Now().UTC(),
	}

	long := strings.Repeat("durable workflow detail for memory extraction. ", 60)
	inputs := make([]*pb.Request_Input_UserInputs_UserInput, 0, 6)
	for i := 0; i < 6; i++ {
		inputs = append(inputs, &pb.Request_Input_UserInputs_UserInput{
			Input: &pb.Request_Input_UserInputs_UserInput_UserQuery{
				UserQuery: &pb.Request_Input_UserQuery{Query: long},
			},
		})
	}
	reqBody := &pb.Request{
		Input: &pb.Request_Input{
			Context: &pb.InputContext{
				ProjectRules: []*pb.InputContext_ProjectRules{{RootPath: workspaceRoot}},
			},
			Type: &pb.Request_Input_UserInputs_{
				UserInputs: &pb.Request_Input_UserInputs{Inputs: inputs},
			},
		},
		Metadata: &pb.Request_Metadata{ConversationId: "conv-e2e"},
	}
	rawReq, err := proto.Marshal(reqBody)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/ai/multi-agent", bytes.NewReader(rawReq))
	rec := httptest.NewRecorder()
	s.handleAgentRequest(rec, req)
	if _, err := io.ReadAll(rec.Result().Body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	projectKey := memory.ProjectKeyFromRoot(workspaceRoot)
	notesPath := filepath.Join(cfg.Memory.BaseDir, memory.SessionNotesRelPath("conv-e2e"))
	workflowPath := filepath.Join(cfg.Memory.BaseDir, "projects", projectKey, "memory", "workflows.md")
	deadline := time.Now().Add(3 * time.Second)
	for {
		notes, notesErr := os.ReadFile(notesPath)
		workflow, workflowErr := os.ReadFile(workflowPath)
		if notesErr == nil && workflowErr == nil &&
			strings.Contains(string(notes), "project key should come from request root") &&
			strings.Contains(string(workflow), "go run ./cmd/server") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("asynchronous memory worker did not finish: notes=%v workflow=%v", notesErr, workflowErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	notesData, err := os.ReadFile(notesPath)
	if err != nil {
		t.Fatalf("expected session notes: %v", err)
	}
	if !strings.Contains(string(notesData), "project key should come from request root") {
		t.Fatalf("unexpected session notes content: %s", string(notesData))
	}

	workflowData, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("expected project workflows memory: %v", err)
	}
	if !strings.Contains(string(workflowData), "go run ./cmd/server") {
		t.Fatalf("unexpected workflows content: %s", string(workflowData))
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/settings/memory/status?conversation_id=conv-e2e&project_key="+projectKey, nil)
	statusRec := httptest.NewRecorder()
	s.handleMemoryStatus(statusRec, statusReq)
	var status memoryStatusResponse
	if err := json.NewDecoder(statusRec.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if !status.Session.NotesExists || !status.Project.MemoryIndexExists || status.Project.MemoryCount == 0 {
		t.Fatalf("unexpected memory status: %+v", status)
	}
	if status.ContextWindow.Tokens != 32000 || status.ContextWindow.KeepRecentChars == 0 {
		t.Fatalf("expected memory status to expose context-derived compaction settings: %+v", status.ContextWindow)
	}

	events, err := os.ReadFile(filepath.Join(cfg.Memory.BaseDir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(events), "session_memory_updated") || !strings.Contains(string(events), "project_memory_updated") {
		t.Fatalf("expected both update events, got: %s", string(events))
	}
}

func TestAgentResponseDoesNotWaitForMemoryExtractor(t *testing.T) {
	dir := t.TempDir()
	enabled, disabled := true, false
	cfg := &config.Config{
		Provider: "openai", BaseURL: "http://mock-llm.local/v1", APIKey: "test-key", Model: "test-model",
		Memory: config.MemoryConfig{Enabled: &enabled, SessionEnabled: &enabled, AutoEnabled: &disabled, BaseDir: filepath.Join(dir, "memory")},
	}
	s := NewServer(cfg, filepath.Join(dir, "config.yaml"))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.closeBackground(ctx)
	})
	mockHTTP := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"stream":true`) {
			payload := "data: {\"id\":\"stream\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":null}]}\n\n" +
				"data: {\"id\":\"stream\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(payload)), Request: r}, nil
		}
		time.Sleep(500 * time.Millisecond)
		notes := memory.DefaultSessionNotes("slow extractor")
		encoded, _ := json.Marshal(notes)
		payload := `{"id":"memory","object":"chat.completion","created":0,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":` + string(encoded) + `},"finish_reason":"stop"}]}`
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(payload)), Request: r}, nil
	})}
	s.conversations["conv-fast"] = &Conversation{client: llm.NewClientWithHTTPClient(cfg, mockHTTP), CreatedAt: time.Now().UTC()}

	inputs := make([]*pb.Request_Input_UserInputs_UserInput, 4)
	for i := range inputs {
		inputs[i] = &pb.Request_Input_UserInputs_UserInput{Input: &pb.Request_Input_UserInputs_UserInput_UserQuery{UserQuery: &pb.Request_Input_UserQuery{Query: "remember this verified workflow"}}}
	}
	request := &pb.Request{
		Input:    &pb.Request_Input{Context: &pb.InputContext{Directory: &pb.InputContext_Directory{Pwd: dir}}, Type: &pb.Request_Input_UserInputs_{UserInputs: &pb.Request_Input_UserInputs{Inputs: inputs}}},
		Metadata: &pb.Request_Metadata{ConversationId: "conv-fast"},
	}
	raw, err := proto.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	recorder := httptest.NewRecorder()
	s.handleAgentRequest(recorder, httptest.NewRequest(http.MethodPost, "/ai/multi-agent", bytes.NewReader(raw)))
	if elapsed := time.Since(start); elapsed >= 300*time.Millisecond {
		t.Fatalf("response waited for slow memory extractor: %s", elapsed)
	}
	if !strings.Contains(recorder.Body.String(), "StreamFinished") && recorder.Body.Len() == 0 {
		t.Fatal("expected streamed response")
	}
	stats, err := s.memoryQueue.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Pending+stats.Running == 0 {
		t.Fatalf("expected durable async job, stats=%+v", stats)
	}
}
