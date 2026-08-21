package agentruntime

import (
	"context"
	"encoding/json"
	"testing"
)

func TestEnvelopeRoundTrip(t *testing.T) {
	envelope, err := NewEnvelope("exchange-1", "turn.start", TurnRequest{
		ConversationID: "conversation-1",
		TaskID:         "task-1",
		RequestID:      "request-1",
		Inputs:         []Input{{Kind: InputUserMessage, Content: "hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Envelope
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatal(err)
	}
	if decoded.Type != "turn.start" || decoded.ExchangeID != "exchange-1" {
		t.Fatalf("unexpected envelope: %+v", decoded)
	}
}

func TestValidateTurnRequest(t *testing.T) {
	valid := TurnRequest{
		ConversationID: "conversation-1",
		TaskID:         "task-1",
		RequestID:      "request-1",
		Inputs:         []Input{{Kind: InputToolResult, ToolCallID: "call-1", Content: "ok"}},
	}
	if err := validateTurnRequest(valid); err != nil {
		t.Fatal(err)
	}
	valid.Inputs[0].ToolCallID = ""
	if err := validateTurnRequest(valid); err == nil {
		t.Fatal("expected missing tool call id to fail")
	}
}

type fakeDriver struct{}

func (fakeDriver) Name() string { return "fake" }
func (fakeDriver) Exchange(_ context.Context, _ TurnRequest, emit func(Event) error) error {
	return emit(Event{Type: EventTurnCompleted})
}
func (fakeDriver) Cancel(context.Context, string) error { return nil }
func (fakeDriver) Close(context.Context) error          { return nil }

func TestDriverContract(t *testing.T) {
	var driver Driver = fakeDriver{}
	if driver.Name() != "fake" {
		t.Fatalf("unexpected driver name %q", driver.Name())
	}
}
