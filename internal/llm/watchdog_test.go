package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sasuke39/open-warp/internal/config"
)

const testChunk = `data: {"id":"chatcmpl-x","object":"chat.completion.chunk","created":0,"model":"test-model","choices":[{"index":0,"delta":{"content":%q},"finish_reason":null}]}` + "\n\n"

const testDoneChunk = `data: {"id":"chatcmpl-x","object":"chat.completion.chunk","created":0,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
	"data: [DONE]\n\n"

func newTestClient(t *testing.T, srv *httptest.Server, stallTimeout time.Duration) *Client {
	t.Helper()
	cfg := &config.Config{Provider: "openai", BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "test-model"}
	c := NewClientWithHTTPClient(cfg, srv.Client())
	if stallTimeout > 0 {
		c.SetStreamStallTimeout(stallTimeout)
	}
	return c
}

// (a) A healthy stream must flow through the watchdog untouched.
func TestWatchdogStream_NormalStreamUnaffected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, testChunk, "Hello, ")
		fmt.Fprintf(w, testChunk, "world!")
		fmt.Fprint(w, testDoneChunk)
	}))
	defer srv.Close()

	c := newTestClient(t, srv, 200*time.Millisecond)
	stream := c.StreamChat(context.Background(), "sys", nil)

	var chunks int
	for stream.Next() {
		chunks++
		_ = stream.Current()
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if chunks != 3 {
		t.Fatalf("expected 3 chunks, got %d", chunks)
	}
	if stream.Chunks() != 3 {
		t.Fatalf("expected Chunks()=3, got %d", stream.Chunks())
	}
}

// (b) A stream that goes silent longer than the stall timeout must fail with
// ErrStreamStall, and the watchdog must cancel the underlying request so the
// reader goroutine can exit.
func TestWatchdogStream_StallDetected(t *testing.T) {
	serverSawCancel := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, testChunk, "partial")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Go silent: only unblock when the client aborts the request.
		select {
		case <-r.Context().Done():
			close(serverSawCancel)
		case <-time.After(10 * time.Second):
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv, 150*time.Millisecond)
	stream := c.StreamChat(context.Background(), "sys", nil)

	if !stream.Next() {
		t.Fatalf("expected first chunk, got Next()=false err=%v", stream.Err())
	}

	start := time.Now()
	if stream.Next() {
		t.Fatalf("expected stall on second Next(), got a chunk")
	}
	elapsed := time.Since(start)
	if !errors.Is(stream.Err(), ErrStreamStall) {
		t.Fatalf("expected ErrStreamStall, got %v", stream.Err())
	}
	if elapsed > 5*time.Second {
		t.Fatalf("stall detection took too long: %s", elapsed)
	}

	select {
	case <-serverSawCancel:
	case <-time.After(5 * time.Second):
		t.Fatalf("watchdog did not cancel the underlying request")
	}
}

// (c) Next/Err semantics must match the raw SDK stream: false + nil error on
// clean EOF, stable false afterwards, non-stall errors pass through.
func TestWatchdogStream_ErrNextSemantics(t *testing.T) {
	t.Run("clean EOF then stable false", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, testChunk, "x")
			fmt.Fprint(w, testDoneChunk)
		}))
		defer srv.Close()

		stream := newTestClient(t, srv, 0).StreamChat(context.Background(), "sys", nil)
		for stream.Next() {
		}
		if err := stream.Err(); err != nil {
			t.Fatalf("expected nil error after clean EOF, got %v", err)
		}
		if stream.Next() {
			t.Fatalf("expected Next() to stay false after EOF")
		}
	})

	t.Run("stable false after stall", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			// Never send a chunk; unblock only when the client aborts.
			<-r.Context().Done()
		}))
		defer srv.Close()

		stream := newTestClient(t, srv, 100*time.Millisecond).StreamChat(context.Background(), "sys", nil)
		if stream.Next() {
			t.Fatalf("expected stall, got chunk")
		}
		if !errors.Is(stream.Err(), ErrStreamStall) {
			t.Fatalf("expected ErrStreamStall, got %v", stream.Err())
		}
		if stream.Next() {
			t.Fatalf("expected Next() to stay false after stall")
		}
		if !errors.Is(stream.Err(), ErrStreamStall) {
			t.Fatalf("expected Err() to keep reporting ErrStreamStall, got %v", stream.Err())
		}
	})

	t.Run("HTTP error passes through unchanged", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":{"message":"boom","type":"server_error"}}`)
		}))
		defer srv.Close()

		stream := newTestClient(t, srv, 0).StreamChat(context.Background(), "sys", nil)
		if stream.Next() {
			t.Fatalf("expected no chunks on HTTP 500")
		}
		err := stream.Err()
		if err == nil {
			t.Fatalf("expected error on HTTP 500")
		}
		if errors.Is(err, ErrStreamStall) {
			t.Fatalf("HTTP error must not be reported as ErrStreamStall: %v", err)
		}
	})
}
