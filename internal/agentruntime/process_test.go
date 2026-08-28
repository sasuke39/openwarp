package agentruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestProcessDriverExchangesEvents(t *testing.T) {
	driver, err := NewProcessDriver(ProcessConfig{
		Name: "fake-runtime", Command: os.Args[0], Args: []string{"-test.run=TestProcessDriverHelper"},
		Env: append(os.Environ(), "OPEN_WARP_RUNTIME_HELPER=1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close(context.Background())

	var eventTypes []EventType
	err = driver.Exchange(context.Background(), TurnRequest{
		ConversationID: "conv", TurnID: "turn", TaskID: "task", RequestID: "request",
		Inputs: []Input{{Kind: InputUserMessage, Content: "hello"}},
	}, func(event Event) error {
		eventTypes = append(eventTypes, event.Type)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join([]string{string(eventTypes[0]), string(eventTypes[1])}, ","); got != "assistant.delta,turn.completed" {
		t.Fatalf("events = %s", got)
	}
}

func TestProcessDriverCancelAcknowledgedBeforeNextTurn(t *testing.T) {
	driver, err := NewProcessDriver(ProcessConfig{
		Name: "fake-runtime", Command: os.Args[0], Args: []string{"-test.run=TestProcessDriverHelper"},
		Env: append(os.Environ(), "OPEN_WARP_RUNTIME_HELPER=1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close(context.Background())

	request := TurnRequest{
		ConversationID: "conv", TurnID: "turn", TaskID: "task", RequestID: "request",
		Inputs: []Input{{Kind: InputUserMessage, Content: "wait"}},
	}
	if err := driver.Exchange(context.Background(), request, func(Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := driver.Cancel(ctx, TurnControl{ConversationID: request.ConversationID, TurnID: request.TurnID, TaskID: request.TaskID}); err != nil {
		t.Fatalf("cancel acknowledgement: %v", err)
	}

	request.RequestID = "request-2"
	request.TurnID = "turn-2"
	request.Inputs[0].Content = "next"
	if err := driver.Exchange(context.Background(), request, func(Event) error { return nil }); err != nil {
		t.Fatalf("next turn after cancellation: %v", err)
	}
}

func TestProcessDriverCancelReleasesRunningExchange(t *testing.T) {
	driver, err := NewProcessDriver(ProcessConfig{
		Name: "fake-runtime", Command: os.Args[0], Args: []string{"-test.run=TestProcessDriverHelper"},
		Env: append(os.Environ(), "OPEN_WARP_RUNTIME_HELPER=1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close(context.Background())

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- driver.Exchange(context.Background(), TurnRequest{
			ConversationID: "conv", TurnID: "running-turn", TaskID: "running-task", RequestID: "request",
			Inputs: []Input{{Kind: InputUserMessage, Content: "block"}},
		}, func(event Event) error {
			if event.Type == EventDiagnostic {
				close(started)
			}
			return nil
		})
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("running exchange did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := driver.Cancel(ctx, TurnControl{ConversationID: "conv", TurnID: "running-turn", TaskID: "running-task"}); err != nil {
		t.Fatalf("cancel running exchange: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancelled exchange returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel acknowledgement did not release the running exchange")
	}
}

func TestProcessDriverResumesWithRejectedToolAndSteer(t *testing.T) {
	driver, err := NewProcessDriver(ProcessConfig{
		Name: "fake-runtime", Command: os.Args[0], Args: []string{"-test.run=TestProcessDriverHelper"},
		Env: append(os.Environ(), "OPEN_WARP_RUNTIME_HELPER=1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close(context.Background())

	err = driver.Exchange(context.Background(), TurnRequest{
		ConversationID: "conv", TurnID: "turn", TaskID: "task", RequestID: "request",
		Inputs: []Input{
			{Kind: InputToolResult, ToolCallID: "call-1", Status: "rejected", Content: "rejected"},
			{Kind: InputUserSteer, Content: "use a mirror instead"},
		},
	}, func(Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
}

func TestProcessDriverSteersCanonicalConversationForActiveTask(t *testing.T) {
	driver, err := NewProcessDriver(ProcessConfig{
		Name: "fake-runtime", Command: os.Args[0], Args: []string{"-test.run=TestProcessDriverHelper"},
		Env: append(os.Environ(), "OPEN_WARP_RUNTIME_HELPER=1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close(context.Background())

	if err := driver.Exchange(context.Background(), TurnRequest{
		ConversationID: "runtime-conversation", TurnID: "runtime-turn", TaskID: "active-task", RequestID: "request",
		Inputs: []Input{{Kind: InputUserMessage, Content: "wait"}},
	}, func(Event) error { return nil }); err != nil {
		t.Fatal(err)
	}

	var steeredIdentity string
	if err := driver.Exchange(context.Background(), TurnRequest{
		ConversationID: "ui-conversation", TurnID: "ui-turn", TaskID: "active-task", RequestID: "steer-request",
		Inputs: []Input{{Kind: InputUserSteer, Content: "use the queued guidance"}},
	}, func(event Event) error {
		if event.Type == EventDiagnostic {
			steeredIdentity = event.Text
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if steeredIdentity != "runtime-conversation|runtime-turn" {
		t.Fatalf("steer identity = %q, want canonical runtime Turn", steeredIdentity)
	}
}

func TestProcessDriverHelper(t *testing.T) {
	if os.Getenv("OPEN_WARP_RUNTIME_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var frame Envelope
		if json.Unmarshal(scanner.Bytes(), &frame) != nil {
			os.Exit(2)
		}
		switch frame.Type {
		case "turn.start", "turn.resume":
			var request TurnRequest
			_ = json.Unmarshal(frame.Payload, &request)
			if hasToolResult(request.Inputs) && frame.Type != "turn.resume" {
				out, _ := NewEnvelope(frame.ExchangeID, "event", Event{Type: EventTurnFailed, Error: "tool result did not use turn.resume"})
				_ = encoder.Encode(out)
				continue
			}
			if len(request.Inputs) > 0 && request.Inputs[0].Content == "wait" {
				out, _ := NewEnvelope(frame.ExchangeID, "event", Event{Type: EventTurnAwaiting})
				_ = encoder.Encode(out)
				continue
			}
			if len(request.Inputs) > 0 && request.Inputs[0].Content == "block" {
				out, _ := NewEnvelope(frame.ExchangeID, "event", Event{Type: EventDiagnostic, Text: "started"})
				_ = encoder.Encode(out)
				continue
			}
			for _, event := range []Event{{Type: EventAssistantDelta, Text: "ok"}, {Type: EventTurnCompleted}} {
				out, _ := NewEnvelope(frame.ExchangeID, "event", event)
				_ = encoder.Encode(out)
			}
		case "turn.steer":
			var request TurnRequest
			_ = json.Unmarshal(frame.Payload, &request)
			diagnostic, _ := NewEnvelope(frame.ExchangeID, "event", Event{Type: EventDiagnostic, Text: request.ConversationID + "|" + request.TurnID})
			_ = encoder.Encode(diagnostic)
			steerID := ""
			if len(request.Inputs) > 0 {
				steerID = request.Inputs[0].SteerID
			}
			out, _ := NewEnvelope(frame.ExchangeID, "event", Event{Type: EventSteerAccepted, SteerID: steerID})
			_ = encoder.Encode(out)
		case "turn.cancel":
			for _, event := range []Event{{Type: EventTurnCancelling}, {Type: EventTurnCancelled}} {
				out, _ := NewEnvelope(frame.ExchangeID, "event", event)
				_ = encoder.Encode(out)
			}
		case "runtime.shutdown":
			return
		}
	}
}
