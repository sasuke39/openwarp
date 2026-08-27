package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sasuke39/open-warp/internal/agent"
	"github.com/sasuke39/open-warp/internal/agentruntime"
	"github.com/sasuke39/open-warp/internal/llm"
	pb "github.com/sasuke39/open-warp/internal/proto"
	"mvdan.cc/sh/v3/syntax"
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
) (ok bool, turnActive bool) {
	// Keep the external-runtime event sequence identical to the native agent
	// loop. When Warp did not provide a task in TaskContext, the generated task
	// ID is unknown to the client until CreateTask arrives. Sending output first
	// makes Warp reject every subsequent action with TaskNotFound.
	if !taskAlreadyExists {
		s.sendCreateTask(w, flusher, taskID)
	}

	inputs = s.completeExternalToolInputs(taskID, inputs)
	runtimeInputs := make([]agentruntime.Input, 0, len(inputs))
	for _, in := range inputs {
		if in.LongRunningCommandID != "" {
			conv.LastLongRunningCommandID = in.LongRunningCommandID
		}
		if in.ShellCommandCompleted {
			conv.LastLongRunningCommandID = ""
		}
		item := agentruntime.Input{Content: in.Content, ToolCallID: in.ToolCallID, Status: in.Status}
		switch in.Kind {
		case "user_query":
			item.Kind = agentruntime.InputUserMessage
		case "user_steer":
			item.Kind = agentruntime.InputUserSteer
		case "tool_result":
			item.Kind = agentruntime.InputToolResult
		default:
			continue
		}
		runtimeInputs = append(runtimeInputs, item)
	}
	if len(runtimeInputs) == 0 {
		s.sendEvent(w, flusher, finishEvent(&pb.ResponseEvent_StreamFinished_Done{}))
		return true, s.hasExternalPending(taskID)
	}
	request := agentruntime.TurnRequest{
		ConversationID: conversationID,
		TaskID:         taskID,
		RequestID:      requestID,
		SystemPrompt:   agent.WithExecutionContext(agent.SystemPrompt, executionContext),
		WorkingDir:     externalRuntimeWorkingDir(executionContext),
		Inputs:         runtimeInputs,
		Metadata:       map[string]string{"driver": driver.Name(), "project_key": conv.ProjectKey},
	}
	outputMessageID := uuid.NewString()
	sawText := false
	sawAwaitingTool := false
	sawSteered := false
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
			_, managedSSH := agent.ManagedSSHTargetFromInput(executionContext)
			for _, call := range event.ToolCalls {
				translated, err := translateExternalToolCall(call, managedSSH)
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
			sawAwaitingTool = true
			if len(pending) == 0 {
				return fmt.Errorf("%s runtime suspended without a tool call", driver.Name())
			}
			if err := s.sendToolCalls(w, flusher, conv, taskID, pending); err != nil {
				return err
			}
			s.setExternalPending(taskID, pending)
		case agentruntime.EventTurnSteered:
			sawSteered = true
		case agentruntime.EventDiagnostic:
			log.Printf("[RUNTIME:%s] %s", driver.Name(), event.Text)
		}
		return nil
	}

	log.Printf("[RUNTIME:%s] exchange conv=%s task=%s inputs=%d", driver.Name(), conversationID, taskID, len(runtimeInputs))
	exchangeCtx, cancelExchange := context.WithTimeout(ctx, s.externalRuntimeExchangeTimeout())
	defer cancelExchange()
	if err := driver.Exchange(exchangeCtx, request, emit); err != nil {
		if exchangeCtx.Err() == context.DeadlineExceeded {
			cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if cancelErr := driver.Cancel(cancelCtx, taskID); cancelErr != nil {
				log.Printf("[RUNTIME:%s] cancel timed-out exchange task=%s: %v", driver.Name(), taskID, cancelErr)
			}
			cancel()
			s.sendFinishError(w, flusher, fmt.Sprintf("%s runtime did not finish within %s", driver.Name(), s.externalRuntimeExchangeTimeout()))
			return false, false
		}
		if ctx.Err() != nil {
			s.sendFinishError(w, flusher, "Agent task was cancelled")
		} else {
			s.sendFinishError(w, flusher, err.Error())
		}
		return false, false
	}
	s.sendEvent(w, flusher, finishEvent(&pb.ResponseEvent_StreamFinished_Done{}))
	return true, sawAwaitingTool || sawSteered
}

func (s *Server) externalRuntimeExchangeTimeout() time.Duration {
	if s != nil && s.cfg != nil && s.cfg.Server.StreamStallTimeoutSeconds > 0 {
		return time.Duration(s.cfg.Server.StreamStallTimeoutSeconds) * time.Second
	}
	return 120 * time.Second
}

func translateExternalToolCall(call agentruntime.ToolCall, managedSSH bool) (llm.ToolCall, error) {
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
			Command         string `json:"command"`
			Workdir         string `json:"workdir"`
			CommandID       string `json:"commandId"`
			ExecutionMode   string `json:"executionMode"`
			ExecutionModeV1 string `json:"execution_mode"`
			RunInBackground *bool  `json:"run_in_background"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return llm.ToolCall{}, fmt.Errorf("decode external bash call: %w", err)
		}
		if err := validateShellSyntax(args.Command); err != nil {
			return llm.ToolCall{}, err
		}
		command := args.Command
		if strings.TrimSpace(args.Workdir) != "" {
			command = "cd -- " + shellQuote(args.Workdir) + " && " + command
		}
		executionMode := strings.TrimSpace(args.ExecutionMode)
		if executionMode == "" {
			executionMode = strings.TrimSpace(args.ExecutionModeV1)
		}
		if executionMode == "" && args.RunInBackground != nil {
			if *args.RunInBackground {
				executionMode = "background"
			} else {
				executionMode = "foreground"
			}
		}
		if executionMode == "" {
			executionMode = "auto"
		}
		var waitUntilComplete bool
		switch executionMode {
		case "auto", "foreground":
			waitUntilComplete = true
		case "background":
			if strings.TrimSpace(args.CommandID) == "" {
				args.CommandID = uuid.NewString()
			}
			if !validBackgroundCommandID(args.CommandID) {
				return llm.ToolCall{}, fmt.Errorf("background workspace.shell requires a valid UUID commandId")
			}
			command = managedBackgroundStartCommand(args.CommandID, command)
			// The launcher itself is short-lived and runs through the independent
			// session executor. The managed child continues after this completes.
			waitUntilComplete = true
		default:
			return llm.ToolCall{}, fmt.Errorf("unsupported workspace.shell execution mode %q", executionMode)
		}
		return marshal("run_shell_command", map[string]any{
			"command": command, "is_read_only": false, "is_risky": false, "risk_category": "",
			"wait_until_complete": waitUntilComplete,
		})
	case agentruntime.ToolWorkspaceProcessRead,
		agentruntime.ToolWorkspaceProcessWrite,
		agentruntime.ToolWorkspaceProcessCancel:
		var args struct {
			CommandID string `json:"commandId"`
			Input     string `json:"input"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return llm.ToolCall{}, fmt.Errorf("decode external process call: %w", err)
		}
		if !validBackgroundCommandID(args.CommandID) {
			return llm.ToolCall{}, fmt.Errorf("external process tool requires a valid commandId")
		}
		var command string
		switch call.Name {
		case agentruntime.ToolWorkspaceProcessRead:
			command = managedBackgroundReadCommand(args.CommandID)
		case agentruntime.ToolWorkspaceProcessWrite:
			command = managedBackgroundWriteCommand(args.CommandID, args.Input)
		case agentruntime.ToolWorkspaceProcessCancel:
			command = managedBackgroundCancelCommand(args.CommandID)
		}
		return marshal("run_shell_command", map[string]any{
			"command": command, "is_read_only": call.Name == agentruntime.ToolWorkspaceProcessRead,
			"is_risky": false, "risk_category": "", "wait_until_complete": true,
		})
	case agentruntime.ToolWorkspaceReadFile:
		var args struct {
			FilePath string `json:"file_path"`
			Offset   int    `json:"offset"`
			Limit    int    `json:"limit"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return llm.ToolCall{}, fmt.Errorf("decode external read call: %w", err)
		}
		if args.Offset <= 0 {
			args.Offset = 1
		}
		if args.Limit <= 0 {
			args.Limit = 200
		}
		if managedSSH {
			end := args.Offset + args.Limit - 1
			command := fmt.Sprintf("sed -n '%d,%dp' < %s", args.Offset, end, shellQuote(args.FilePath))
			return marshal("run_shell_command", map[string]any{
				"command": command, "is_read_only": true, "is_risky": false, "risk_category": "",
			})
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
			return llm.ToolCall{}, fmt.Errorf("decode external write call: %w", err)
		}
		return marshal("apply_file_diffs", map[string]any{
			"summary":   "External agent write " + args.FilePath,
			"new_files": []any{map[string]string{"file_path": args.FilePath, "content": args.Content}},
		})
	case agentruntime.ToolWorkspaceEditFile:
		var args struct {
			FilePath  string `json:"file_path"`
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return llm.ToolCall{}, fmt.Errorf("decode external edit call: %w", err)
		}
		return marshal("apply_file_diffs", map[string]any{
			"summary": "External agent edit " + args.FilePath,
			"diffs":   []any{map[string]string{"file_path": args.FilePath, "search": args.OldString, "replace": args.NewString}},
		})
	case agentruntime.ToolWorkspaceGlob:
		var args struct {
			Pattern string `json:"pattern"`
			Path    string `json:"path"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return llm.ToolCall{}, fmt.Errorf("decode external glob call: %w", err)
		}
		return marshal("file_glob_v2", map[string]any{"patterns": []string{args.Pattern}, "search_dir": args.Path, "max_matches": 200})
	case agentruntime.ToolWorkspaceGrep:
		var args struct {
			Pattern string `json:"pattern"`
			Path    string `json:"path"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return llm.ToolCall{}, fmt.Errorf("decode external grep call: %w", err)
		}
		return marshal("grep", map[string]any{"queries": []string{args.Pattern}, "path": args.Path})
	default:
		return llm.ToolCall{}, fmt.Errorf("external workspace tool %q has no Warp mapping", call.Name)
	}
}

func validateShellSyntax(command string) error {
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("workspace.shell command must not be empty")
	}
	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	if _, err := parser.Parse(strings.NewReader(command), "agent-command"); err != nil {
		return fmt.Errorf("workspace.shell command has incomplete or invalid Bash syntax: %w", err)
	}
	return nil
}

func validBackgroundCommandID(commandID string) bool {
	_, err := uuid.Parse(commandID)
	return err == nil
}

func managedBackgroundJobDir(commandID string) string {
	return "/tmp/warplocal-agent-jobs/" + commandID
}

func managedBackgroundStartCommand(commandID, command string) string {
	dir := managedBackgroundJobDir(commandID)
	// Opening the FIFO read/write avoids blocking command startup before the
	// first input arrives, while still allowing later process.write calls.
	worker := `exec 3<>"$1"; bash -lc "$2" <&3; code=$?; printf '%s\n' "$code" > "$3"`
	return "job_dir=" + shellQuote(dir) +
		"; mkdir -p \"$job_dir\"; rm -f \"$job_dir/input\" \"$job_dir/exit\"; mkfifo \"$job_dir/input\"; " +
		"if command -v setsid >/dev/null 2>&1; then nohup setsid sh -c " + shellQuote(worker) +
		" sh \"$job_dir/input\" " + shellQuote(command) + " \"$job_dir/exit\"; " +
		"else nohup sh -c " + shellQuote(worker) + " sh \"$job_dir/input\" " + shellQuote(command) + " \"$job_dir/exit\"; fi " +
		">\"$job_dir/output\" 2>&1 </dev/null & pid=$!; printf '%s\n' \"$pid\" > \"$job_dir/pid\"; " +
		"printf 'command_id=%s pid=%s status=running\\n' " + shellQuote(commandID) + " \"$pid\""
}

func managedBackgroundReadCommand(commandID string) string {
	dir := managedBackgroundJobDir(commandID)
	return "job_dir=" + shellQuote(dir) +
		"; test -r \"$job_dir/pid\" || { echo 'unknown command_id'; exit 1; }; pid=$(cat \"$job_dir/pid\"); " +
		"if kill -0 \"$pid\" 2>/dev/null; then status=running; elif test -r \"$job_dir/exit\"; then status=exited:$(cat \"$job_dir/exit\"); else status=stopped; fi; " +
		"printf 'command_id=%s pid=%s status=%s\\n' " + shellQuote(commandID) + " \"$pid\" \"$status\"; tail -c 65536 \"$job_dir/output\" 2>/dev/null || true"
}

func managedBackgroundWriteCommand(commandID, input string) string {
	dir := managedBackgroundJobDir(commandID)
	return "job_dir=" + shellQuote(dir) +
		"; test -r \"$job_dir/pid\" || { echo 'unknown command_id'; exit 1; }; pid=$(cat \"$job_dir/pid\"); " +
		"kill -0 \"$pid\" 2>/dev/null || { echo 'command is not running'; exit 1; }; " +
		"printf '%s' " + shellQuote(input) + " > \"$job_dir/input\"; echo 'input delivered'"
}

func managedBackgroundCancelCommand(commandID string) string {
	dir := managedBackgroundJobDir(commandID)
	return "job_dir=" + shellQuote(dir) +
		"; test -r \"$job_dir/pid\" || { echo 'unknown command_id'; exit 1; }; pid=$(cat \"$job_dir/pid\"); " +
		"kill -TERM -- -\"$pid\" 2>/dev/null || kill -TERM \"$pid\" 2>/dev/null || true; " +
		"sleep 1; kill -0 \"$pid\" 2>/dev/null && { kill -KILL -- -\"$pid\" 2>/dev/null || kill -KILL \"$pid\" 2>/dev/null || true; }; echo 'command cancelled'"
}

func externalRuntimeWorkingDir(input *pb.InputContext) string {
	if input == nil || input.GetDirectory() == nil {
		return ""
	}
	return strings.TrimSpace(input.GetDirectory().GetPwd())
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
