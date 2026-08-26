package main

import (
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
