package main

import (
	"strings"

	"github.com/sasuke39/open-warp/internal/agentruntime"
	"github.com/sasuke39/open-warp/internal/llm"
	pb "github.com/sasuke39/open-warp/internal/proto"
)

func (s *Server) setExternalPending(taskID string, calls []llm.ToolCall) {
	ids := make([]string, 0, len(calls))
	for _, call := range calls {
		if strings.TrimSpace(call.ID) != "" {
			ids = append(ids, call.ID)
		}
	}
	if len(ids) == 0 {
		s.externalPending.Delete(taskID)
		return
	}
	s.externalPending.Store(taskID, ids)
}

func (s *Server) removeExternalPending(taskID string, inputs []agentruntime.Input) {
	value, ok := s.externalPending.Load(taskID)
	if !ok {
		return
	}
	ids, ok := value.([]string)
	if !ok {
		return
	}
	completed := make(map[string]struct{})
	for _, item := range inputs {
		if item.Kind == agentruntime.InputToolResult {
			completed[item.ToolCallID] = struct{}{}
		}
	}
	remaining := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, found := completed[id]; !found {
			remaining = append(remaining, id)
		}
	}
	if len(remaining) == 0 {
		s.externalPending.Delete(taskID)
	} else {
		s.externalPending.Store(taskID, remaining)
	}
}

func (s *Server) externalRejectedToolInputs(request *pb.Request, taskID string) []input {
	value, ok := s.externalPending.Load(taskID)
	if !ok {
		return nil
	}
	ids, ok := value.([]string)
	if !ok || len(ids) == 0 {
		return nil
	}
	pending := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		pending[id] = struct{}{}
	}
	seen := make(map[string]struct{})
	var rejected []input
	for _, task := range request.GetTaskContext().GetTasks() {
		if task.GetId() != taskID {
			continue
		}
		for _, message := range task.GetMessages() {
			result := message.GetToolCallResult()
			if result == nil || result.GetCancel() == nil {
				continue
			}
			id := result.GetToolCallId()
			if _, active := pending[id]; !active {
				continue
			}
			if _, duplicate := seen[id]; duplicate {
				continue
			}
			seen[id] = struct{}{}
			rejected = append(rejected, input{
				Kind: "tool_result", ToolCallID: id, Status: "rejected",
				Content: "The user rejected this tool call.",
			})
		}
	}
	return rejected
}
