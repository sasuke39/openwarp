package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sasuke39/open-warp/internal/agent"
	"github.com/sasuke39/open-warp/internal/llm"
	pb "github.com/sasuke39/open-warp/internal/proto"
)

// ---------- 校验规则 ----------

func TestValidateTaskList(t *testing.T) {
	longID := strings.Repeat("x", maxTaskIDLen+1)
	longContent := strings.Repeat("x", maxTaskContentLen+1)
	tests := []struct {
		name    string
		tasks   []TaskItem
		wantErr string // 空串表示应通过
	}{
		{
			name: "valid list passes",
			tasks: []TaskItem{
				{ID: "step-1", Content: "read code", Status: "completed", Priority: "high"},
				{ID: "step-2", Content: "write fix", Status: "in_progress"},
				{ID: "step-3", Content: "run tests", Status: "pending", Priority: "low"},
			},
		},
		{
			name:    "empty list passes",
			tasks:   nil,
			wantErr: "",
		},
		{
			name: "duplicate id rejected",
			tasks: []TaskItem{
				{ID: "step-1", Content: "a", Status: "pending"},
				{ID: "step-1", Content: "b", Status: "completed"},
			},
			wantErr: `duplicate task id "step-1"`,
		},
		{
			name: "multiple in_progress rejected",
			tasks: []TaskItem{
				{ID: "step-1", Content: "a", Status: "pending"},
				{ID: "step-2", Content: "b", Status: "in_progress"},
				{ID: "step-3", Content: "c", Status: "in_progress"},
			},
			wantErr: "multiple in_progress tasks (step-2, step-3). Only one task can be in_progress at a time.",
		},
		{
			name: "empty id rejected",
			tasks: []TaskItem{
				{ID: "", Content: "a", Status: "pending"},
			},
			wantErr: "task id must not be empty",
		},
		{
			name: "id too long rejected",
			tasks: []TaskItem{
				{ID: longID, Content: "a", Status: "pending"},
			},
			wantErr: "exceeds 50 characters",
		},
		{
			name: "empty content rejected",
			tasks: []TaskItem{
				{ID: "step-1", Content: "", Status: "pending"},
			},
			wantErr: `task "step-1" content must not be empty`,
		},
		{
			name: "content too long rejected",
			tasks: []TaskItem{
				{ID: "step-1", Content: longContent, Status: "pending"},
			},
			wantErr: "exceeds 200 characters",
		},
		{
			name: "invalid status rejected",
			tasks: []TaskItem{
				{ID: "step-1", Content: "a", Status: "doing"},
			},
			wantErr: `task "step-1" has invalid status "doing"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTaskList(tt.tasks)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}

// ---------- 执行与覆盖语义 ----------

func mustToolCall(t *testing.T, argsJSON string) llm.ToolCall {
	t.Helper()
	return llm.ToolCall{ID: "call-1", Name: updateTaskListToolName, Args: json.RawMessage(argsJSON)}
}

func TestExecuteUpdateTaskListSuccessResult(t *testing.T) {
	conv := &Conversation{}
	result := executeUpdateTaskList(conv, mustToolCall(t, `{"tasks":[
		{"id":"step-1","content":"read code","status":"completed"},
		{"id":"step-2","content":"implement the C renderer","status":"in_progress","priority":"high"},
		{"id":"step-3","content":"run tests","status":"pending"}
	]}`))

	var res taskListResult
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("result is not valid JSON: %v (%s)", err, result)
	}
	if !res.OK {
		t.Fatalf("expected ok=true, got %s", result)
	}
	if res.ActiveTask != "step-2: implement the C renderer" {
		t.Fatalf("unexpected active_task: %q", res.ActiveTask)
	}
	if res.Completed != 1 || res.Pending != 1 || res.Total != 3 {
		t.Fatalf("unexpected counts: %+v", res)
	}
	if len(conv.todoList) != 3 || conv.todoList[1].Priority != "high" {
		t.Fatalf("todoList not updated correctly: %+v", conv.todoList)
	}
}

func TestExecuteUpdateTaskListReplacesEntireState(t *testing.T) {
	conv := &Conversation{}
	executeUpdateTaskList(conv, mustToolCall(t, `{"tasks":[
		{"id":"a","content":"first","status":"pending"},
		{"id":"b","content":"second","status":"pending"}
	]}`))
	executeUpdateTaskList(conv, mustToolCall(t, `{"tasks":[
		{"id":"c","content":"only survivor","status":"in_progress"}
	]}`))
	if len(conv.todoList) != 1 || conv.todoList[0].ID != "c" {
		t.Fatalf("expected full replacement with only task c, got %+v", conv.todoList)
	}
}

func TestExecuteUpdateTaskListValidationFailureKeepsState(t *testing.T) {
	conv := &Conversation{todoList: []TaskItem{
		{ID: "keep", Content: "original", Status: "pending"},
	}}
	result := executeUpdateTaskList(conv, mustToolCall(t, `{"tasks":[
		{"id":"x","content":"a","status":"in_progress"},
		{"id":"y","content":"b","status":"in_progress"}
	]}`))
	var errObj struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(result), &errObj); err != nil || errObj.Error == "" {
		t.Fatalf("expected error JSON, got %s", result)
	}
	if !strings.HasPrefix(errObj.Error, "validation failed:") {
		t.Fatalf("expected 'validation failed:' prefix, got %q", errObj.Error)
	}
	if len(conv.todoList) != 1 || conv.todoList[0].ID != "keep" {
		t.Fatalf("todoList must stay unchanged on validation failure, got %+v", conv.todoList)
	}
}

func TestExecuteUpdateTaskListInvalidArgs(t *testing.T) {
	conv := &Conversation{}
	result := executeUpdateTaskList(conv, mustToolCall(t, `{not json`))
	if !strings.Contains(result, `"error"`) {
		t.Fatalf("expected error JSON for invalid args, got %s", result)
	}
}

// ---------- 注入格式化 ----------

func TestFormatTaskProgress(t *testing.T) {
	if got := formatTaskProgress(nil); got != "" {
		t.Fatalf("empty list must produce no injection, got %q", got)
	}
	out := formatTaskProgress([]TaskItem{
		{ID: "step-1", Content: "读代码", Status: "completed"},
		{ID: "step-2", Content: "写修复", Status: "in_progress"},
		{ID: "step-3", Content: "跑测试", Status: "pending"},
		{ID: "step-4", Content: "放弃的", Status: "cancelled"},
	})
	for _, want := range []string{
		"## Current Task Progress",
		"- [x] step-1: 读代码 (completed)",
		"- [>] step-2: 写修复 (in_progress)",
		"- [ ] step-3: 跑测试 (pending)",
		"- [-] step-4: 放弃的 (cancelled)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("formatted progress missing %q:\n%s", want, out)
		}
	}
}

func TestSplitServerSideToolCalls(t *testing.T) {
	server, client := splitServerSideToolCalls([]llm.ToolCall{
		{ID: "1", Name: "read_files"},
		{ID: "2", Name: updateTaskListToolName},
		{ID: "3", Name: "run_shell_command"},
	})
	if len(server) != 1 || server[0].ID != "2" {
		t.Fatalf("expected only update_task_list server-side, got %+v", server)
	}
	if len(client) != 2 || client[0].ID != "1" || client[1].ID != "3" {
		t.Fatalf("expected read_files+run_shell_command client-side, got %+v", client)
	}
}

// ---------- 集成:agent loop 全链路 ----------

// sseToolCallResponse 构造带 tool_calls 的 SSE 响应(finish_reason=tool_calls)。
func sseToolCallResponse(t *testing.T, calls ...llm.ToolCall) string {
	t.Helper()
	var sb strings.Builder
	for i, c := range calls {
		idJSON, _ := json.Marshal(c.ID)
		nameJSON, _ := json.Marshal(c.Name)
		argsJSON, _ := json.Marshal(string(c.Args))
		sb.WriteString(fmt.Sprintf("data: {\"id\":\"chatcmpl-x\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":%d,\"id\":%s,\"type\":\"function\",\"function\":{\"name\":%s,\"arguments\":%s}}]},\"finish_reason\":null}]}\n\n",
			i, idJSON, nameJSON, argsJSON))
	}
	sb.WriteString("data: {\"id\":\"chatcmpl-x\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
	sb.WriteString("data: [DONE]\n\n")
	return sb.String()
}

type chatRequest struct {
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

// chatRequestMessage 用于把 conv.history 里的消息 marshal 后再解码,
// 避免直接匹配转义后的内层 JSON 引号。
type chatRequestMessage struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolCallID string `json:"tool_call_id"`
}

// systemPromptOf 从请求体里取出 system prompt 文本。
func systemPromptOf(t *testing.T, body string) string {
	t.Helper()
	var req chatRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("failed to decode request body: %v", err)
	}
	if len(req.Messages) == 0 || req.Messages[0].Role != "system" {
		t.Fatalf("first message must be system, got %+v", req.Messages)
	}
	return req.Messages[0].Content
}

// collectForwardedToolCalls 从响应事件里收集所有转发给客户端的 ToolCall 消息。
func collectForwardedToolCalls(t *testing.T, events []*pb.ResponseEvent) []*pb.Message_ToolCall {
	t.Helper()
	var out []*pb.Message_ToolCall
	for _, ev := range events {
		for _, action := range ev.GetClientActions().GetActions() {
			for _, msg := range action.GetAddMessagesToTask().GetMessages() {
				if tc := msg.GetToolCall(); tc != nil {
					out = append(out, tc)
				}
			}
		}
	}
	return out
}

func newTaskListTestServer(t *testing.T, roundTrip roundTripperFunc) (*Server, *Conversation) {
	t.Helper()
	return newWatchdogTestServer(t, roundTrip, 0)
}

// 三轮脚本:update_task_list -> update_task_list -> 最终文本。
// 断言 todoList 状态、每轮 system prompt 注入、工具结果、history 不被污染。
func TestRunAgentLoop_UpdateTaskListEndToEnd(t *testing.T) {
	var streamCalls int32
	var requestBodies []string
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		requestBodies = append(requestBodies, string(body))
		call := atomic.AddInt32(&streamCalls, 1)
		header := http.Header{"Content-Type": []string{"text/event-stream"}}
		var payload string
		switch call {
		case 1:
			payload = sseToolCallResponse(t, llm.ToolCall{
				ID:   "call-todo-1",
				Name: updateTaskListToolName,
				Args: json.RawMessage(`{"tasks":[
					{"id":"step-1","content":"read code","status":"in_progress","priority":"high"},
					{"id":"step-2","content":"write fix","status":"pending"}
				]}`),
			})
		case 2:
			payload = sseToolCallResponse(t, llm.ToolCall{
				ID:   "call-todo-2",
				Name: updateTaskListToolName,
				Args: json.RawMessage(`{"tasks":[
					{"id":"step-1","content":"read code","status":"completed"},
					{"id":"step-2","content":"write fix","status":"in_progress"}
				]}`),
			})
		default:
			payload = sseChunkPayload("All done.") + sseDonePayload
		}
		return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(payload)), Request: r}, nil
	})

	s, conv := newTaskListTestServer(t, rt)
	rec := httptest.NewRecorder()
	s.runAgentLoop(context.Background(), rec, rec, conv, "req-1", "task-1", false, "sys", nil, agent.ManagedSSHTarget{})

	if got := atomic.LoadInt32(&streamCalls); got != 3 {
		t.Fatalf("expected 3 stream calls (2 tool rounds + final text), got %d", got)
	}

	// 第 1 轮:没有 todoList,不注入。
	if strings.Contains(systemPromptOf(t, requestBodies[0]), "Current Task Progress") {
		t.Fatal("first request must not contain task progress section")
	}
	// 第 2 轮:注入 step-1 in_progress / step-2 pending。
	sp2 := systemPromptOf(t, requestBodies[1])
	for _, want := range []string{"## Current Task Progress", "[>] step-1: read code (in_progress)", "[ ] step-2: write fix (pending)"} {
		if !strings.Contains(sp2, want) {
			t.Fatalf("second request system prompt missing %q:\n%s", want, sp2)
		}
	}
	// 注入位置:必须在 system prompt 尾部(以注入段结尾)。
	if !strings.HasPrefix(sp2, "sys\n\n## Current Task Progress") {
		t.Fatalf("task progress must be appended after base prompt, got:\n%s", sp2)
	}
	// 第 3 轮:注入 step-1 completed / step-2 in_progress。
	sp3 := systemPromptOf(t, requestBodies[2])
	for _, want := range []string{"[x] step-1: read code (completed)", "[>] step-2: write fix (in_progress)"} {
		if !strings.Contains(sp3, want) {
			t.Fatalf("third request system prompt missing %q:\n%s", want, sp3)
		}
	}

	// conv.todoList 是最后一次全量覆盖的结果。
	if len(conv.todoList) != 2 || conv.todoList[0].Status != "completed" || conv.todoList[1].Status != "in_progress" {
		t.Fatalf("unexpected final todoList: %+v", conv.todoList)
	}

	// 工具结果正确写入 conv.history,且 history 不含注入段。
	var sawToolResult bool
	for idx, msg := range conv.history {
		raw, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("marshal history[%d]: %v", idx, err)
		}
		if strings.Contains(string(raw), "Current Task Progress") {
			t.Fatalf("conv.history[%d] must not contain injected task progress", idx)
		}
		var hm chatRequestMessage
		if err := json.Unmarshal(raw, &hm); err != nil {
			continue // assistant tool_call 消息结构不同,跳过
		}
		if hm.Role == "tool" && strings.Contains(hm.Content, `"ok":true`) && strings.Contains(hm.Content, "step-2: write fix") {
			sawToolResult = true
		}
	}
	if !sawToolResult {
		t.Fatalf("expected a tool result message with ok=true and active task in history")
	}

	// update_task_list 不转发给客户端:整个响应里不应有 ToolCall 客户端动作。
	events := decodeResponseEvents(t, rec.Body.String())
	if forwarded := collectForwardedToolCalls(t, events); len(forwarded) != 0 {
		t.Fatalf("update_task_list must not be forwarded to client, got %d tool call messages", len(forwarded))
	}
	done, errMsgs := finishOutcome(events)
	if !done || len(errMsgs) != 0 {
		t.Fatalf("expected clean Done finish, done=%v errors=%v", done, errMsgs)
	}
}

// update_task_list 与客户端工具混在一轮:服务端工具先执行,
// 客户端工具照常转发,请求正常结束(等客户端回结果)。
func TestRunAgentLoop_UpdateTaskListMixedWithClientTool(t *testing.T) {
	var streamCalls int32
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&streamCalls, 1)
		header := http.Header{"Content-Type": []string{"text/event-stream"}}
		payload := sseToolCallResponse(t,
			llm.ToolCall{
				ID:   "call-todo",
				Name: updateTaskListToolName,
				Args: json.RawMessage(`{"tasks":[{"id":"step-1","content":"read code","status":"in_progress"}]}`),
			},
			llm.ToolCall{
				ID:   "call-read",
				Name: "read_files",
				Args: json.RawMessage(`{"files":[{"name":"main.go"}]}`),
			},
		)
		return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(payload)), Request: r}, nil
	})

	s, conv := newTaskListTestServer(t, rt)
	rec := httptest.NewRecorder()
	s.runAgentLoop(context.Background(), rec, rec, conv, "req-1", "task-1", false, "sys", nil, agent.ManagedSSHTarget{})

	if got := atomic.LoadInt32(&streamCalls); got != 1 {
		t.Fatalf("expected 1 stream call (loop ends when client tools are forwarded), got %d", got)
	}
	if len(conv.todoList) != 1 || conv.todoList[0].ID != "step-1" {
		t.Fatalf("server-side update_task_list must run before forwarding: %+v", conv.todoList)
	}

	// 只有 read_files 被转发,update_task_list 不转发。
	events := decodeResponseEvents(t, rec.Body.String())
	forwarded := collectForwardedToolCalls(t, events)
	if len(forwarded) != 1 {
		t.Fatalf("expected exactly 1 forwarded tool call, got %d", len(forwarded))
	}
	if forwarded[0].GetToolCallId() != "call-read" || forwarded[0].GetReadFiles() == nil {
		t.Fatalf("forwarded tool call must be read_files(call-read), got %+v", forwarded[0])
	}

	// history: assistant tool_call 消息 + update_task_list 的服务端结果;
	// read_files 的结果等客户端回传,不在此轮 history 里。
	var sawServerResult bool
	for _, msg := range conv.history {
		raw, _ := json.Marshal(msg)
		var hm chatRequestMessage
		if err := json.Unmarshal(raw, &hm); err != nil {
			continue
		}
		if hm.Role == "tool" && hm.ToolCallID == "call-todo" && strings.Contains(hm.Content, `"ok":true`) {
			sawServerResult = true
		}
		if hm.Role == "tool" && hm.ToolCallID == "call-read" {
			t.Fatalf("read_files result must not be in history yet: %s", hm.Content)
		}
	}
	if !sawServerResult {
		t.Fatal("expected server-side tool result for call-todo in history")
	}
	done, errMsgs := finishOutcome(events)
	if !done || len(errMsgs) != 0 {
		t.Fatalf("expected clean Done finish, done=%v errors=%v", done, errMsgs)
	}
}

// 校验失败:错误结果喂回模型,conv.todoList 不更新,下一轮不注入任务段。
func TestRunAgentLoop_UpdateTaskListValidationErrorFedBack(t *testing.T) {
	var streamCalls int32
	var requestBodies []string
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, string(body))
		call := atomic.AddInt32(&streamCalls, 1)
		header := http.Header{"Content-Type": []string{"text/event-stream"}}
		var payload string
		if call == 1 {
			payload = sseToolCallResponse(t, llm.ToolCall{
				ID:   "call-bad",
				Name: updateTaskListToolName,
				Args: json.RawMessage(`{"tasks":[
					{"id":"a","content":"x","status":"in_progress"},
					{"id":"b","content":"y","status":"in_progress"}
				]}`),
			})
		} else {
			payload = sseChunkPayload("Fixed my plan.") + sseDonePayload
		}
		return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(payload)), Request: r}, nil
	})

	s, conv := newTaskListTestServer(t, rt)
	rec := httptest.NewRecorder()
	s.runAgentLoop(context.Background(), rec, rec, conv, "req-1", "task-1", false, "sys", nil, agent.ManagedSSHTarget{})

	if got := atomic.LoadInt32(&streamCalls); got != 2 {
		t.Fatalf("expected 2 stream calls, got %d", got)
	}
	if len(conv.todoList) != 0 {
		t.Fatalf("todoList must stay empty after validation failure, got %+v", conv.todoList)
	}
	// 第二轮不注入任务段。
	if strings.Contains(systemPromptOf(t, requestBodies[1]), "Current Task Progress") {
		t.Fatal("no task progress injection expected after failed validation")
	}
	// history 里有带 validation failed 的工具结果。
	var sawError bool
	for _, msg := range conv.history {
		raw, _ := json.Marshal(msg)
		var hm chatRequestMessage
		if err := json.Unmarshal(raw, &hm); err != nil {
			continue
		}
		if hm.Role == "tool" && hm.ToolCallID == "call-bad" && strings.Contains(hm.Content, "multiple in_progress") {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("expected validation error tool result for call-bad in history")
	}
	done, errMsgs := finishOutcome(decodeResponseEvents(t, rec.Body.String()))
	if !done || len(errMsgs) != 0 {
		t.Fatalf("expected clean Done finish, done=%v errors=%v", done, errMsgs)
	}
}
