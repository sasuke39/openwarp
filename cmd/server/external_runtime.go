package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/google/uuid"
	"github.com/sasuke39/open-warp/internal/agent"
	"github.com/sasuke39/open-warp/internal/agentruntime"
	"github.com/sasuke39/open-warp/internal/llm"
	pb "github.com/sasuke39/open-warp/internal/proto"
)

func (s *Server) runExternalAgent(
	ctx context.Context,
	driver agentruntime.Driver,
	w io.Writer,
	flusher interface{ Flush() },
	conv *Conversation,
	conversationID, requestID, taskID string,
	taskAlreadyExists bool,
	inputs []input,
	executionContext *pb.InputContext,
) bool {
	// Keep the external-runtime event sequence identical to the native agent
	// loop. When Warp did not provide a task in TaskContext, the generated task
	// ID is unknown to the client until CreateTask arrives. Sending output first
	// makes Warp reject every subsequent action with TaskNotFound.
	if !taskAlreadyExists {
		s.sendCreateTask(w, flusher, taskID)
	}

	runtimeInputs := make([]agentruntime.Input, 0, len(inputs))
	for _, in := range inputs {
		if in.LongRunningCommandID != "" {
			conv.LastLongRunningCommandID = in.LongRunningCommandID
		}
		if in.ShellCommandCompleted {
			conv.LastLongRunningCommandID = ""
		}
		item := agentruntime.Input{Content: in.Content, ToolCallID: in.ToolCallID}
		switch in.Kind {
		case "user_query":
			item.Kind = agentruntime.InputUserMessage
		case "tool_result":
			item.Kind = agentruntime.InputToolResult
		default:
			continue
		}
		runtimeInputs = append(runtimeInputs, item)
	}
	request := agentruntime.TurnRequest{
		ConversationID: conversationID,
		TaskID:         taskID,
		RequestID:      requestID,
		SystemPrompt:   agent.WithExecutionContext(agent.SystemPrompt, executionContext),
		Inputs:         runtimeInputs,
		Metadata:       map[string]string{"driver": driver.Name(), "project_key": conv.ProjectKey},
	}

	outputMessageID := uuid.NewString()
	sawText := false
	var pending []llm.ToolCall
	emit := func(event agentruntime.Event) error {
		switch event.Type {
		case agentruntime.EventAssistantDelta, agentruntime.EventAssistantFinal:
			if event.Text == "" {
				return nil
			}
			if !sawText {
				s.sendFirstTextChunk(w, flusher, taskID, requestID, outputMessageID, event.Text)
				sawText = true
			} else {
				s.sendAppendText(w, flusher, taskID, outputMessageID, event.Text)
			}
		case agentruntime.EventToolCallBatch:
			for _, call := range event.ToolCalls {
				translated, err := translateExternalToolCall(call)
				if err != nil {
					return err
				}
				pending = append(pending, translated)
			}
		case agentruntime.EventTodoChanged:
			if next, ok := decodeExternalTodos(event.Data); ok {
				previous := conv.todoList
				conv.todoList = next
				s.sendTodoListUpdate(w, flusher, taskID, previous, next)
			}
		case agentruntime.EventTurnAwaiting:
			if len(pending) == 0 {
				return fmt.Errorf("%s runtime suspended without a tool call", driver.Name())
			}
			if err := s.sendToolCalls(w, flusher, conv, taskID, pending); err != nil {
				return err
			}
		case agentruntime.EventDiagnostic:
			log.Printf("[RUNTIME:%s] %s", driver.Name(), event.Text)
		}
		return nil
	}

	log.Printf("[RUNTIME:%s] exchange conv=%s task=%s inputs=%d", driver.Name(), conversationID, taskID, len(runtimeInputs))
	if err := driver.Exchange(ctx, request, emit); err != nil {
		if ctx.Err() != nil {
			s.sendFinishError(w, flusher, "Agent task was cancelled")
		} else {
			s.sendFinishError(w, flusher, err.Error())
		}
		return false
	}
	s.sendEvent(w, flusher, finishEvent(&pb.ResponseEvent_StreamFinished_Done{}))
	return true
}

func translateExternalToolCall(call agentruntime.ToolCall) (llm.ToolCall, error) {
	marshal := func(name string, value any) (llm.ToolCall, error) {
		raw, err := json.Marshal(value)
		if err != nil {
			return llm.ToolCall{}, err
		}
		return llm.ToolCall{ID: call.ID, Name: name, Args: raw}, nil
	}
	switch call.Name {
	case agentruntime.ToolWorkspaceShell:
		var args struct {
			Command string `json:"command"`
			Workdir string `json:"workdir"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return llm.ToolCall{}, fmt.Errorf("decode DSH bash call: %w", err)
		}
		command := args.Command
		if strings.TrimSpace(args.Workdir) != "" {
			command = "cd -- " + shellQuote(args.Workdir) + " && " + command
		}
		return marshal("run_shell_command", map[string]any{
			"command": command, "is_read_only": false, "is_risky": false, "risk_category": "",
		})
	case agentruntime.ToolWorkspaceReadFile:
		var args struct {
			FilePath string `json:"file_path"`
			Offset   int    `json:"offset"`
			Limit    int    `json:"limit"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return llm.ToolCall{}, fmt.Errorf("decode DSH read call: %w", err)
		}
		if args.Offset <= 0 {
			args.Offset = 1
		}
		if args.Limit <= 0 {
			args.Limit = 200
		}
		return marshal("read_files", map[string]any{"files": []any{map[string]any{
			"name": args.FilePath, "line_ranges": []any{map[string]int{"start": args.Offset, "end": args.Offset + args.Limit - 1}},
		}}})
	case agentruntime.ToolWorkspaceWriteFile:
		var args struct {
			FilePath string `json:"file_path"`
			Content  string `json:"content"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return llm.ToolCall{}, fmt.Errorf("decode DSH write call: %w", err)
		}
		return marshal("apply_file_diffs", map[string]any{
			"summary":   "DSH write " + args.FilePath,
			"new_files": []any{map[string]string{"file_path": args.FilePath, "content": args.Content}},
		})
	case agentruntime.ToolWorkspaceEditFile:
		var args struct {
			FilePath  string `json:"file_path"`
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return llm.ToolCall{}, fmt.Errorf("decode DSH edit call: %w", err)
		}
		return marshal("apply_file_diffs", map[string]any{
			"summary": "DSH edit " + args.FilePath,
			"diffs":   []any{map[string]string{"file_path": args.FilePath, "search": args.OldString, "replace": args.NewString}},
		})
	case agentruntime.ToolWorkspaceGlob:
		var args struct {
			Pattern string `json:"pattern"`
			Path    string `json:"path"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return llm.ToolCall{}, fmt.Errorf("decode DSH glob call: %w", err)
		}
		return marshal("file_glob_v2", map[string]any{"patterns": []string{args.Pattern}, "search_dir": args.Path, "max_matches": 200})
	case agentruntime.ToolWorkspaceGrep:
		var args struct {
			Pattern string `json:"pattern"`
			Path    string `json:"path"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return llm.ToolCall{}, fmt.Errorf("decode DSH grep call: %w", err)
		}
		return marshal("grep", map[string]any{"queries": []string{args.Pattern}, "path": args.Path})
	default:
		return llm.ToolCall{}, fmt.Errorf("external workspace tool %q has no Warp mapping", call.Name)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func decodeExternalTodos(raw json.RawMessage) ([]TaskItem, bool) {
	var direct struct {
		Todos []TaskItem `json:"todos"`
		Tasks []TaskItem `json:"tasks"`
	}
	if err := json.Unmarshal(raw, &direct); err != nil {
		return nil, false
	}
	items := direct.Todos
	if len(items) == 0 {
		items = direct.Tasks
	}
	if len(items) == 0 || validateTaskList(items) != nil {
		return nil, false
	}
	return items, true
}
