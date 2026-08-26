package main

import (
	"strings"
	"testing"

	"github.com/sasuke39/open-warp/internal/llm"
	pb "github.com/sasuke39/open-warp/internal/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestExternalRejectedToolInputsReadsTaskContextCancellation(t *testing.T) {
	server := &Server{}
	server.setExternalPending("task-1", []llm.ToolCall{{ID: "pending-call"}, {ID: "other-call"}})
	request := &pb.Request{TaskContext: &pb.Request_TaskContext{Tasks: []*pb.Task{{
		Id: "task-1",
		Messages: []*pb.Message{{Message: &pb.Message_ToolCallResult_{
			ToolCallResult: &pb.Message_ToolCallResult{
				ToolCallId: "pending-call",
				Result:     &pb.Message_ToolCallResult_Cancel{Cancel: &emptypb.Empty{}},
			},
		}}},
	}}}}

	inputs := server.externalRejectedToolInputs(request, "task-1")
	if len(inputs) != 1 {
		t.Fatalf("rejected inputs = %d, want 1", len(inputs))
	}
	if inputs[0].ToolCallID != "pending-call" || inputs[0].Status != "rejected" {
		t.Fatalf("unexpected rejected input: %+v", inputs[0])
	}
}

func TestCompleteExternalToolInputsOrdersAndFillsMissingResults(t *testing.T) {
	server := &Server{}
	server.setExternalPending("task-1", []llm.ToolCall{
		{ID: "shell-call", Name: "run_shell_command"},
		{ID: "read-call", Name: "read_files"},
	})

	inputs := server.completeExternalToolInputs("task-1", []input{{
		Kind: "tool_result", ToolCallID: "shell-call", Content: "zip contents",
	}})
	if len(inputs) != 2 {
		t.Fatalf("completed inputs = %d, want 2", len(inputs))
	}
	if inputs[0].ToolCallID != "shell-call" || inputs[0].Status != "success" {
		t.Fatalf("first result = %+v", inputs[0])
	}
	if inputs[1].ToolCallID != "read-call" || inputs[1].Status != "error" {
		t.Fatalf("missing result = %+v", inputs[1])
	}
	if !strings.Contains(inputs[1].Content, "did not return a result") {
		t.Fatalf("missing result explanation = %q", inputs[1].Content)
	}
}

func TestCompleteExternalToolInputsSubmitsBatchOnlyOnce(t *testing.T) {
	server := &Server{}
	server.setExternalPending("task-1", []llm.ToolCall{{ID: "call-1", Name: "read_files"}})

	first := server.completeExternalToolInputs("task-1", []input{{
		Kind: "tool_result", ToolCallID: "call-1", Content: "first",
	}})
	second := server.completeExternalToolInputs("task-1", []input{{
		Kind: "tool_result", ToolCallID: "call-1", Content: "late duplicate",
	}})
	if len(first) != 1 || first[0].Content != "first" {
		t.Fatalf("first submission = %+v", first)
	}
	if len(second) != 0 {
		t.Fatalf("duplicate submission must be ignored: %+v", second)
	}
}

func TestCompleteExternalToolInputsRestoresOriginalOrder(t *testing.T) {
	server := &Server{}
	server.setExternalPending("task-1", []llm.ToolCall{
		{ID: "first", Name: "run_shell_command"},
		{ID: "second", Name: "read_files"},
	})

	inputs := server.completeExternalToolInputs("task-1", []input{
		{Kind: "tool_result", ToolCallID: "second", Content: "two"},
		{Kind: "tool_result", ToolCallID: "first", Content: "one"},
	})
	if len(inputs) != 2 || inputs[0].ToolCallID != "first" || inputs[1].ToolCallID != "second" {
		t.Fatalf("ordered results = %+v", inputs)
	}
}

func TestCompleteExternalToolInputsIgnoresLateResultAfterBatchCleanup(t *testing.T) {
	server := &Server{}
	server.setExternalPending("task-1", []llm.ToolCall{{ID: "call-1", Name: "read_files"}})
	server.completeExternalToolInputs("task-1", []input{{
		Kind: "tool_result", ToolCallID: "call-1", Content: "first",
	}})
	server.finishExternalPending("task-1")

	late := server.completeExternalToolInputs("task-1", []input{{
		Kind: "tool_result", ToolCallID: "call-1", Content: "late",
	}})
	if len(late) != 0 {
		t.Fatalf("late result must be ignored after cleanup: %+v", late)
	}
}

func TestCompleteExternalToolInputsPreservesRejection(t *testing.T) {
	server := &Server{}
	server.setExternalPending("task-1", []llm.ToolCall{{ID: "call-1", Name: "run_shell_command"}})

	inputs := server.completeExternalToolInputs("task-1", []input{{
		Kind: "tool_result", ToolCallID: "call-1", Status: "rejected", Content: "no",
	}})
	if len(inputs) != 1 || inputs[0].Status != "rejected" {
		t.Fatalf("rejected result = %+v", inputs)
	}
}

func TestExternalRejectedToolInputsIgnoresOldCancellation(t *testing.T) {
	server := &Server{}
	server.setExternalPending("task-1", []llm.ToolCall{{ID: "current-call"}})
	request := &pb.Request{TaskContext: &pb.Request_TaskContext{Tasks: []*pb.Task{{
		Id: "task-1",
		Messages: []*pb.Message{{Message: &pb.Message_ToolCallResult_{
			ToolCallResult: &pb.Message_ToolCallResult{
				ToolCallId: "old-call",
				Result:     &pb.Message_ToolCallResult_Cancel{Cancel: &emptypb.Empty{}},
			},
		}}},
	}}}}

	if inputs := server.externalRejectedToolInputs(request, "task-1"); len(inputs) != 0 {
		t.Fatalf("old cancellation must not be replayed: %+v", inputs)
	}
}
