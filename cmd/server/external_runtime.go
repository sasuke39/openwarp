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
	"github.com/sasuke39/open-warp/internal/workspacetools"
)

func (s *Server) runExternalAgent(
	ctx context.Context,
	driver agentruntime.Driver,
	w io.Writer,
	flusher interface{ Flush() },
	conv *Conversation,
	conversationID, turnID, requestID, taskID string,
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
		TurnID:         turnID,
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
		case agentruntime.EventSteerAccepted:
			sawSteered = true
		case agentruntime.EventSteerApplied:
			s.steers.update(event.SteerID, steerApplied, "")
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
			if cancelErr := driver.Cancel(cancelCtx, agentruntime.TurnControl{
				ConversationID: conversationID,
				TurnID:         turnID,
				TaskID:         taskID,
			}); cancelErr != nil {
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
	return workspacetools.Translate(call, managedSSH)
}

func shellCommandStartsBackgroundJob(command string) bool {
	return workspacetools.ShellCommandStartsBackgroundJob(command)
}

func validateShellSyntax(command string) error {
	return workspacetools.ValidateShellSyntax(command)
}

func validBackgroundCommandID(commandID string) bool {
	return workspacetools.ValidBackgroundCommandID(commandID)
}

func managedBackgroundJobDir(commandID string) string {
	return workspacetools.ManagedBackgroundJobDir(commandID)
}

func managedBackgroundStartCommand(commandID, command string) string {
	return workspacetools.ManagedBackgroundStartCommand(commandID, command)
}

func managedBackgroundReadCommand(commandID string) string {
	return workspacetools.ManagedBackgroundReadCommand(commandID)
}

func managedBackgroundWriteCommand(commandID, input string) string {
	return workspacetools.ManagedBackgroundWriteCommand(commandID, input)
}

func managedBackgroundCancelCommand(commandID string) string {
	return workspacetools.ManagedBackgroundCancelCommand(commandID)
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
