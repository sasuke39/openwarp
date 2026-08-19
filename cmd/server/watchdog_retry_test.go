package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sasuke39/open-warp/internal/agent"
	"github.com/sasuke39/open-warp/internal/config"
	"github.com/sasuke39/open-warp/internal/llm"
	pb "github.com/sasuke39/open-warp/internal/proto"

	"google.golang.org/protobuf/proto"
)

// stallThenBlockBody serves prefix bytes, then blocks until the request
// context is cancelled — simulating an LLM stream that goes silent mid-way.
type stallThenBlockBody struct {
	prefix *strings.Reader
	done   <-chan struct{}
}

func (b *stallThenBlockBody) Read(p []byte) (int, error) {
	if b.prefix != nil {
		n, err := b.prefix.Read(p)
		if err == io.EOF {
			b.prefix = nil
			<-b.done
			return 0, context.Canceled
		}
		return n, err
	}
	<-b.done
	return 0, context.Canceled
}

func (b *stallThenBlockBody) Close() error { return nil }

func sseChunkPayload(text string) string {
	return "data: {\"id\":\"chatcmpl-x\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"" + text + "\"},\"finish_reason\":null}]}\n\n"
}

const sseDonePayload = "data: {\"id\":\"chatcmpl-x\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"

// sseLengthExhaustedPayload 构造"推理耗尽输出预算"的 SSE 响应:
// 只有 reasoning_content、无 content、finish_reason=length。
func sseLengthExhaustedPayload(reasoning string) string {
	return "data: {\"id\":\"chatcmpl-x\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"" + reasoning + "\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"chatcmpl-x\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"length\"}]}\n\n" +
		"data: [DONE]\n\n"
}

func decodeResponseEvents(t *testing.T, body string) []*pb.ResponseEvent {
	t.Helper()
	var events []*pb.ResponseEvent
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		raw, err := base64.URLEncoding.DecodeString(strings.TrimPrefix(line, "data: "))
		if err != nil {
			t.Fatalf("failed to decode event line %q: %v", line, err)
		}
		ev := &pb.ResponseEvent{}
		if err := proto.Unmarshal(raw, ev); err != nil {
			t.Fatalf("failed to unmarshal event: %v", err)
		}
		events = append(events, ev)
	}
	return events
}

// finishOutcome reports whether a Done finish was sent and collects any
// InternalError finish messages.
func finishOutcome(events []*pb.ResponseEvent) (done bool, errMsgs []string) {
	for _, ev := range events {
		fin := ev.GetFinished()
		if fin == nil {
			continue
		}
		if fin.GetDone() != nil {
			done = true
		}
		if ierr := fin.GetInternalError(); ierr != nil {
			errMsgs = append(errMsgs, ierr.GetMessage())
		}
	}
	return done, errMsgs
}

func newWatchdogTestServer(t *testing.T, roundTrip roundTripperFunc, stallTimeout time.Duration) (*Server, *Conversation) {
	t.Helper()
	cfg := &config.Config{
		Provider: "openai",
		BaseURL:  "http://mock-llm.local/v1",
		APIKey:   "test-key",
		Model:    "test-model",
	}
	s := NewServer(cfg, filepath.Join(t.TempDir(), "config.yaml"))
	client := llm.NewClientWithHTTPClient(cfg, &http.Client{Transport: roundTrip})
	if stallTimeout > 0 {
		client.SetStreamStallTimeout(stallTimeout)
	}
	conv := &Conversation{client: client, CreatedAt: time.Now().UTC()}
	return s, conv
}

func runLoopForTest(s *Server, conv *Conversation) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.runAgentLoop(context.Background(), rec, rec, conv, "req-1", "task-1", false, "sys", nil, agent.ManagedSSHTarget{})
	return rec
}

// First streaming attempt stalls mid-stream; the retry succeeds.
func TestRunAgentLoop_RetriesStalledStream(t *testing.T) {
	var streamCalls int32
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		if !strings.Contains(string(body), `"stream":true`) {
			return nil, fmt.Errorf("unexpected non-stream request: %s", string(body))
		}
		call := atomic.AddInt32(&streamCalls, 1)
		header := http.Header{"Content-Type": []string{"text/event-stream"}}
		var respBody io.ReadCloser
		if call == 1 {
			// Deliver one chunk, then go silent until the watchdog aborts us.
			respBody = &stallThenBlockBody{prefix: strings.NewReader(sseChunkPayload("stalled-partial")), done: r.Context().Done()}
		} else {
			respBody = io.NopCloser(strings.NewReader(sseChunkPayload("Recovered.") + sseDonePayload))
		}
		return &http.Response{StatusCode: 200, Header: header, Body: respBody, Request: r}, nil
	})

	s, conv := newWatchdogTestServer(t, rt, 150*time.Millisecond)
	rec := runLoopForTest(s, conv)

	if got := atomic.LoadInt32(&streamCalls); got != 2 {
		t.Fatalf("expected 2 stream attempts (1 stall + 1 retry), got %d", got)
	}
	done, errMsgs := finishOutcome(decodeResponseEvents(t, rec.Body.String()))
	if !done {
		t.Fatalf("expected Done finish after retry, errors=%v", errMsgs)
	}
	if len(errMsgs) != 0 {
		t.Fatalf("expected no finish errors, got %v", errMsgs)
	}
}

// First streaming attempt returns an empty completion; the retry succeeds.
// finish_reason=stop(非 length)的普通空响应必须走"原样重试",请求不变。
func TestRunAgentLoop_RetriesEmptyResponse(t *testing.T) {
	var streamCalls int32
	var requestBodies []string
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		requestBodies = append(requestBodies, string(body))
		atomic.AddInt32(&streamCalls, 1)
		header := http.Header{"Content-Type": []string{"text/event-stream"}}
		payload := sseDonePayload // empty: no content chunks at all
		if atomic.LoadInt32(&streamCalls) > 1 {
			payload = sseChunkPayload("Second try works.") + sseDonePayload
		}
		return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(payload)), Request: r}, nil
	})

	s, conv := newWatchdogTestServer(t, rt, 0)
	rec := runLoopForTest(s, conv)

	if got := atomic.LoadInt32(&streamCalls); got != 2 {
		t.Fatalf("expected 2 stream attempts (1 empty + 1 retry), got %d", got)
	}
	done, errMsgs := finishOutcome(decodeResponseEvents(t, rec.Body.String()))
	if !done {
		t.Fatalf("expected Done finish after retry, errors=%v", errMsgs)
	}
	// 非 length 空响应:重试请求不得携带 nudge。
	for idx, body := range requestBodies {
		if strings.Contains(body, reasoningExhaustedNudge) {
			t.Fatalf("request %d must not contain brevity nudge for non-length empty response", idx+1)
		}
	}
}

// Every attempt returns an empty completion: 1 initial + 2 retries, then fail.
func TestRunAgentLoop_EmptyResponseRetriesExhausted(t *testing.T) {
	var streamCalls int32
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&streamCalls, 1)
		header := http.Header{"Content-Type": []string{"text/event-stream"}}
		return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(sseDonePayload)), Request: r}, nil
	})

	s, conv := newWatchdogTestServer(t, rt, 0)
	rec := runLoopForTest(s, conv)

	if got := atomic.LoadInt32(&streamCalls); got != 3 {
		t.Fatalf("expected 3 stream attempts (1 initial + 2 retries), got %d", got)
	}
	done, errMsgs := finishOutcome(decodeResponseEvents(t, rec.Body.String()))
	if done {
		t.Fatalf("expected no Done finish when retries are exhausted")
	}
	if len(errMsgs) != 1 || !strings.Contains(errMsgs[0], "empty response") {
		t.Fatalf("expected 'empty response' finish error, got %v", errMsgs)
	}
}

// 第一次 finish=length 空响应(有长 reasoning),第二次正常 -> 成功。
// 断言:第二次请求 messages 尾部含 nudge;第一次不含;conv.history 不含 nudge。
func TestRunAgentLoop_RetriesReasoningExhaustedWithNudge(t *testing.T) {
	var streamCalls int32
	var requestBodies []string
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, string(body))
		call := atomic.AddInt32(&streamCalls, 1)
		header := http.Header{"Content-Type": []string{"text/event-stream"}}
		var payload string
		if call == 1 {
			// 推理耗尽:有 reasoning_content,无 content,finish_reason=length
			payload = sseLengthExhaustedPayload("Step by step analysis that consumed the entire output budget without producing a final answer.")
		} else {
			payload = sseChunkPayload("Direct answer after nudge.") + sseDonePayload
		}
		return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(payload)), Request: r}, nil
	})

	s, conv := newWatchdogTestServer(t, rt, 0)
	rec := runLoopForTest(s, conv)

	if got := atomic.LoadInt32(&streamCalls); got != 2 {
		t.Fatalf("expected 2 stream attempts (1 length-exhausted + 1 nudge retry), got %d", got)
	}
	done, errMsgs := finishOutcome(decodeResponseEvents(t, rec.Body.String()))
	if !done {
		t.Fatalf("expected Done finish after nudge retry, errors=%v", errMsgs)
	}

	// 第一次请求不应含 nudge
	if strings.Contains(requestBodies[0], reasoningExhaustedNudge) {
		t.Fatal("first request must not contain nudge")
	}
	// 第二次请求 messages 尾部应为 nudge user 消息
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(requestBodies[1]), &req); err != nil {
		t.Fatalf("failed to decode second request body: %v", err)
	}
	if len(req.Messages) == 0 {
		t.Fatal("second request has no messages")
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != "user" {
		t.Fatalf("expected last message role=user, got %s", last.Role)
	}
	if !strings.Contains(string(last.Content), "output token limit") {
		t.Fatalf("expected last message content to contain nudge, got %s", string(last.Content))
	}

	// conv.history 不应含 nudge:重试成功后只追加成功的 assistant 消息
	for idx, msg := range conv.history {
		raw, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("failed to marshal conv.history[%d]: %v", idx, err)
		}
		if strings.Contains(string(raw), reasoningExhaustedNudge) {
			t.Fatalf("conv.history[%d] must not contain nudge", idx)
		}
	}
	if len(conv.history) != 1 {
		t.Fatalf("expected conv.history to have 1 entry (successful assistant message), got %d", len(conv.history))
	}
}

// 连续 finish=length 空响应 -> 重试耗尽后报错路径不变。
func TestRunAgentLoop_ReasoningExhaustedRetriesExhausted(t *testing.T) {
	var streamCalls int32
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&streamCalls, 1)
		header := http.Header{"Content-Type": []string{"text/event-stream"}}
		payload := sseLengthExhaustedPayload("Long reasoning that exhausts the budget every time.")
		return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(payload)), Request: r}, nil
	})

	s, conv := newWatchdogTestServer(t, rt, 0)
	rec := runLoopForTest(s, conv)

	if got := atomic.LoadInt32(&streamCalls); got != 3 {
		t.Fatalf("expected 3 stream attempts (1 initial + 2 retries), got %d", got)
	}
	done, errMsgs := finishOutcome(decodeResponseEvents(t, rec.Body.String()))
	if done {
		t.Fatal("expected no Done finish when retries are exhausted")
	}
	if len(errMsgs) != 1 || !strings.Contains(errMsgs[0], "empty response") {
		t.Fatalf("expected 'empty response' finish error, got %v", errMsgs)
	}
}
