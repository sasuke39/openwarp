package main

import "testing"

func TestActiveTurnRegistryKeepsCanonicalConversationForWarpTask(t *testing.T) {
	var registry activeTurnRegistry
	turn := registry.begin("warp-task", "runtime-conversation", nil)
	again := registry.begin("warp-task", "ui-conversation", nil)
	if again != turn {
		t.Fatal("active Warp task created a second Agent Turn")
	}
	if got := again.snapshot().ConversationID; got != "runtime-conversation" {
		t.Fatalf("canonical conversation = %q", got)
	}
	if again.snapshot().TurnID == "" {
		t.Fatal("turn id was not generated")
	}
	registry.finish("warp-task")
	if _, ok := registry.loadByWarpTask("warp-task"); ok {
		t.Fatal("terminal Turn remained active")
	}
}
