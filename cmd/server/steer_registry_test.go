package main

import "testing"

func TestSteerRegistryIsIdempotent(t *testing.T) {
	var registry steerRegistry
	first, fresh, err := registry.reserve("steer-1", "task-1", "use mirror")
	if err != nil || !fresh || first.Status != steerSubmitting {
		t.Fatalf("first reserve = %+v, fresh=%v, err=%v", first, fresh, err)
	}
	registry.update("steer-1", steerAccepted, "")
	second, fresh, err := registry.reserve("steer-1", "task-1", "use mirror")
	if err != nil || fresh || second.Status != steerAccepted {
		t.Fatalf("repeat reserve = %+v, fresh=%v, err=%v", second, fresh, err)
	}
	if _, _, err := registry.reserve("steer-1", "task-1", "different"); err == nil {
		t.Fatal("same steer id with different prompt must conflict")
	}
}

func TestSteerRegistryTracksAppliedAndCancelled(t *testing.T) {
	var registry steerRegistry
	_, _, _ = registry.reserve("applied", "task-1", "one")
	registry.update("applied", steerApplied, "")
	got, ok := registry.load("task-1", "applied")
	if !ok || got.Status != steerApplied {
		t.Fatalf("applied record = %+v, ok=%v", got, ok)
	}
	_, _, _ = registry.reserve("cancelled", "task-1", "two")
	registry.cancelTask("task-1")
	got, ok = registry.load("task-1", "cancelled")
	if !ok || got.Status != steerCancelled {
		t.Fatalf("cancelled record = %+v, ok=%v", got, ok)
	}
}
