package llm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/ssestream"
)

// DefaultStreamStallTimeout is the maximum allowed gap between two SSE chunks
// before the stream is considered stalled. Healthy chat streams emit chunks at
// sub-second intervals; even DeepSeek in slow periods keeps gaps within a few
// seconds. 45s sits far above any observed healthy gap and far below the
// 594~615s single-completion hangs seen in evaluation.
const DefaultStreamStallTimeout = 45 * time.Second

// ErrStreamStall is returned by WatchdogStream.Err when no chunk arrived
// within the stall timeout. Callers should treat it as retryable: re-issue
// the same request with a fresh stream.
var ErrStreamStall = errors.New("llm stream stalled: no new chunk within stall timeout")

// WatchdogStream wraps the SDK's SSE stream and detects stalls: if Next()
// waits longer than the stall timeout for the next chunk, Next returns false
// and Err reports ErrStreamStall.
//
// Next semantics match ssestream.Stream so existing consumers keep working:
// Next()==true -> Current() valid; Next()==false -> check Err().
//
// Goroutine safety: each Next() call runs the blocking inner Next() in a
// background goroutine that delivers its result on a buffered (cap 1)
// channel. On stall the wrapper cancels the stream's child context, which
// aborts the underlying HTTP request, so the goroutine's inner Next() returns
// promptly and the goroutine exits after a non-blocking send. In the worst
// case (transport ignores cancellation) one goroutine per stalled stream
// lingers until the connection dies — accepted trade-off, documented here.
type WatchdogStream struct {
	inner   *ssestream.Stream[openai.ChatCompletionChunk]
	cancel  context.CancelFunc
	timeout time.Duration

	mu     sync.Mutex
	cur    openai.ChatCompletionChunk
	err    error
	chunks int
}

func newWatchdogStream(inner *ssestream.Stream[openai.ChatCompletionChunk], cancel context.CancelFunc, timeout time.Duration) *WatchdogStream {
	if timeout <= 0 {
		timeout = DefaultStreamStallTimeout
	}
	return &WatchdogStream{inner: inner, cancel: cancel, timeout: timeout}
}

// Next advances the stream. It returns false when the stream ends, errors, or
// stalls (no chunk within the configured timeout).
func (w *WatchdogStream) Next() bool {
	w.mu.Lock()
	if w.err != nil {
		w.mu.Unlock()
		return false
	}
	w.mu.Unlock()

	done := make(chan bool, 1)
	go func() {
		done <- w.inner.Next()
	}()

	timer := time.NewTimer(w.timeout)
	defer timer.Stop()

	select {
	case ok := <-done:
		if !ok {
			w.setErr(w.inner.Err())
			// Stream is over; release the child context promptly instead of
			// waiting for the parent request context to end.
			w.cancel()
			return false
		}
		w.mu.Lock()
		w.cur = w.inner.Current()
		w.chunks++
		w.mu.Unlock()
		return true
	case <-timer.C:
		w.setErr(fmt.Errorf("%w (timeout=%s)", ErrStreamStall, w.timeout))
		// Abort the HTTP request so the background goroutine's inner.Next()
		// unblocks and exits. The buffered channel guarantees its final send
		// never blocks even if nobody reads it again.
		w.cancel()
		return false
	}
}

// Current returns the most recent chunk. Only valid after Next()==true.
func (w *WatchdogStream) Current() openai.ChatCompletionChunk {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cur
}

// Err returns the terminal error: nil on clean EOF, the underlying stream
// error, or an error wrapping ErrStreamStall on watchdog timeout.
func (w *WatchdogStream) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// Chunks returns how many chunks were successfully delivered, for logging.
func (w *WatchdogStream) Chunks() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.chunks
}

func (w *WatchdogStream) setErr(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.err = err
}
