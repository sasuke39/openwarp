package agentruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
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
		ConversationID: "conv", TaskID: "task", RequestID: "request",
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
			for _, event := range []Event{{Type: EventAssistantDelta, Text: "ok"}, {Type: EventTurnCompleted}} {
				out, _ := NewEnvelope(frame.ExchangeID, "event", event)
				_ = encoder.Encode(out)
			}
		case "runtime.shutdown":
			return
		}
	}
}
