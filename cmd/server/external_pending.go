package main

import (
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/sasuke39/open-warp/internal/llm"
	pb "github.com/sasuke39/open-warp/internal/proto"
)

// externalToolBatch is the completion barrier for one tool-call batch. Warp
// returns the batch only after its actions have reached a terminal state, so a
// missing result at that boundary must become an explicit error instead of
// leaving the external runtime waiting forever.
type externalToolBatch struct {
	mu        sync.Mutex
	tools     []externalPendingTool
	terminal  map[string]input
	submitted bool
}

type externalPendingTool struct {
	ID   string
	Name string
}

const externalFinishedToolLimit = 256

type externalToolResultHistory struct {
	mu    sync.Mutex
	ids   map[string]struct{}
	order []string
}

func (s *Server) setExternalPending(taskID string, calls []llm.ToolCall) {
	batch := &externalToolBatch{terminal: make(map[string]input)}
	for _, call := range calls {
		if id := strings.TrimSpace(call.ID); id != "" {
			batch.tools = append(batch.tools, externalPendingTool{ID: id, Name: call.Name})
		}
	}
	if len(batch.tools) == 0 {
		s.externalPending.Delete(taskID)
		return
	}
	s.externalPending.Store(taskID, batch)
}

// completeExternalToolInputs returns one ordered, complete result set. Results
// that Warp omitted are synthesized as errors, duplicates are ignored, and a
// submitted batch can never resume the framework a second time.
func (s *Server) completeExternalToolInputs(taskID string, inputs []input) []input {
	value, ok := s.externalPending.Load(taskID)
	if !ok {
		return s.filterFinishedExternalToolInputs(taskID, inputs)
	}
	batch, ok := value.(*externalToolBatch)
	if !ok {
		return inputs
	}

	batch.mu.Lock()
	defer batch.mu.Unlock()

	known := make(map[string]externalPendingTool, len(batch.tools))
	for _, tool := range batch.tools {
		known[tool.ID] = tool
	}

	passthrough := make([]input, 0, len(inputs))
	matched := false
	for _, item := range inputs {
		if item.Kind != "tool_result" {
			passthrough = append(passthrough, item)
			continue
		}
		if _, belongs := known[item.ToolCallID]; !belongs {
			// This is a duplicate or late result from an older batch. Feeding it
			// into the current framework turn would corrupt tool/result pairing.
			continue
		}
		matched = true
		if batch.submitted {
			continue
		}
		if _, duplicate := batch.terminal[item.ToolCallID]; duplicate {
			continue
		}
		if strings.TrimSpace(item.Status) == "" {
			item.Status = "success"
		}
		batch.terminal[item.ToolCallID] = item
	}

	if !matched || batch.submitted {
		return passthrough
	}

	results := make([]input, 0, len(batch.tools)+len(passthrough))
	for _, tool := range batch.tools {
		result, found := batch.terminal[tool.ID]
		if !found {
			result = input{
				Kind:       "tool_result",
				ToolCallID: tool.ID,
				Status:     "error",
				Content: fmt.Sprintf(
					"Tool %s did not return a result at the end of the client tool batch. Treat it as failed and choose another approach.",
					tool.Name,
				),
			}
			batch.terminal[tool.ID] = result
			log.Printf("[RUNTIME] completing missing tool result task=%s tool_call_id=%s tool=%s status=error", taskID, tool.ID, tool.Name)
		}
		results = append(results, result)
	}
	batch.submitted = true
	s.rememberFinishedExternalTools(taskID, batch.tools)
	return append(results, passthrough...)
}

func (s *Server) finishExternalPending(taskID string) {
	value, ok := s.externalPending.Load(taskID)
	if !ok {
		return
	}
	batch, ok := value.(*externalToolBatch)
	if !ok {
		s.externalPending.Delete(taskID)
		return
	}
	batch.mu.Lock()
	batch.submitted = true
	tools := append([]externalPendingTool(nil), batch.tools...)
	batch.mu.Unlock()
	s.rememberFinishedExternalTools(taskID, tools)
	s.externalPending.Delete(taskID)
}

func (s *Server) rememberFinishedExternalTools(taskID string, tools []externalPendingTool) {
	if len(tools) == 0 {
		return
	}
	value, _ := s.externalFinished.LoadOrStore(taskID, &externalToolResultHistory{ids: make(map[string]struct{})})
	history := value.(*externalToolResultHistory)
	history.mu.Lock()
	defer history.mu.Unlock()
	for _, tool := range tools {
		if _, exists := history.ids[tool.ID]; exists {
			continue
		}
		history.ids[tool.ID] = struct{}{}
		history.order = append(history.order, tool.ID)
	}
	for len(history.order) > externalFinishedToolLimit {
		oldest := history.order[0]
		history.order = history.order[1:]
		delete(history.ids, oldest)
	}
}

func (s *Server) filterFinishedExternalToolInputs(taskID string, inputs []input) []input {
	value, ok := s.externalFinished.Load(taskID)
	if !ok {
		return inputs
	}
	history := value.(*externalToolResultHistory)
	history.mu.Lock()
	defer history.mu.Unlock()
	filtered := make([]input, 0, len(inputs))
	for _, item := range inputs {
		if item.Kind == "tool_result" {
			if _, finished := history.ids[item.ToolCallID]; finished {
				continue
			}
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func (s *Server) hasExternalPending(taskID string) bool {
	value, ok := s.externalPending.Load(taskID)
	if !ok {
		return false
	}
	batch, ok := value.(*externalToolBatch)
	if !ok {
		return false
	}
	batch.mu.Lock()
	defer batch.mu.Unlock()
	return !batch.submitted && len(batch.tools) > 0
}

func (s *Server) externalRejectedToolInputs(request *pb.Request, taskID string) []input {
	value, ok := s.externalPending.Load(taskID)
	if !ok {
		return nil
	}
	batch, ok := value.(*externalToolBatch)
	if !ok {
		return nil
	}
	batch.mu.Lock()
	defer batch.mu.Unlock()
	if batch.submitted {
		return nil
	}
	pending := make(map[string]struct{}, len(batch.tools))
	for _, tool := range batch.tools {
		if _, finished := batch.terminal[tool.ID]; !finished {
			pending[tool.ID] = struct{}{}
		}
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
