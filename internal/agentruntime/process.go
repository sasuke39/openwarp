package agentruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type ProcessConfig struct {
	Name            string
	Command         string
	Args            []string
	Env             []string
	ShutdownTimeout time.Duration
}

type ProcessDriver struct {
	cfg ProcessConfig

	startMu sync.Mutex
	writeMu sync.Mutex
	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	pending map[string]chan processResult
	closed  bool
	done    chan struct{}
}

type processResult struct {
	event Event
	err   error
}

func NewProcessDriver(cfg ProcessConfig) (*ProcessDriver, error) {
	if strings.TrimSpace(cfg.Name) == "" {
		return nil, fmt.Errorf("runtime driver name is required")
	}
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, fmt.Errorf("runtime driver command is required")
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 5 * time.Second
	}
	return &ProcessDriver{cfg: cfg, pending: make(map[string]chan processResult)}, nil
}

func (d *ProcessDriver) Name() string { return d.cfg.Name }

func (d *ProcessDriver) Exchange(ctx context.Context, request TurnRequest, emit func(Event) error) error {
	if err := validateTurnRequest(request); err != nil {
		return err
	}
	if emit == nil {
		return fmt.Errorf("event sink is required")
	}
	if err := d.ensureStarted(); err != nil {
		return err
	}

	exchangeID := uuid.NewString()
	results := make(chan processResult, 32)
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return fmt.Errorf("agent runtime driver is closed")
	}
	d.pending[exchangeID] = results
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		delete(d.pending, exchangeID)
		d.mu.Unlock()
	}()

	frameType := "turn.start"
	if isToolResultOnly(request.Inputs) {
		frameType = "turn.resume"
	}
	envelope, err := NewEnvelope(exchangeID, frameType, request)
	if err != nil {
		return err
	}
	if err := d.writeEnvelope(envelope); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			_ = d.Cancel(context.Background(), request.TaskID)
			return ctx.Err()
		case result := <-results:
			if result.err != nil {
				return result.err
			}
			if err := emit(result.event); err != nil {
				return err
			}
			if result.event.IsExchangeTerminal() {
				if result.event.Type == EventTurnFailed {
					return errors.New(result.event.Error)
				}
				return nil
			}
		}
	}
}

func (d *ProcessDriver) Cancel(ctx context.Context, taskID string) error {
	if strings.TrimSpace(taskID) == "" {
		return fmt.Errorf("task id is required")
	}
	if err := d.ensureStarted(); err != nil {
		return err
	}
	envelope, err := NewEnvelope(uuid.NewString(), "turn.cancel", map[string]string{"task_id": taskID})
	if err != nil {
		return err
	}
	return d.writeEnvelope(envelope)
}

func (d *ProcessDriver) Close(ctx context.Context) error {
	d.startMu.Lock()
	defer d.startMu.Unlock()

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	cmd := d.cmd
	done := d.done
	d.mu.Unlock()
	if cmd == nil {
		return nil
	}

	envelope, _ := NewEnvelope(uuid.NewString(), "runtime.shutdown", struct{}{})
	_ = d.writeEnvelope(envelope)
	waitCtx, cancel := context.WithTimeout(ctx, d.cfg.ShutdownTimeout)
	defer cancel()
	select {
	case <-done:
		return nil
	case <-waitCtx.Done():
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
		return waitCtx.Err()
	}
}

func (d *ProcessDriver) ensureStarted() error {
	d.startMu.Lock()
	defer d.startMu.Unlock()

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return fmt.Errorf("agent runtime driver is closed")
	}
	if d.cmd != nil {
		d.mu.Unlock()
		return nil
	}
	d.mu.Unlock()

	cmd := exec.Command(d.cfg.Command, d.cfg.Args...)
	cmd.Env = d.cfg.Env
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open %s stdin: %w", d.cfg.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open %s stdout: %w", d.cfg.Name, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("open %s stderr: %w", d.cfg.Name, err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s runtime: %w", d.cfg.Name, err)
	}

	d.mu.Lock()
	d.cmd = cmd
	d.stdin = stdin
	d.done = make(chan struct{})
	done := d.done
	d.mu.Unlock()
	go d.readStdout(stdout)
	go d.readStderr(stderr)
	go d.wait(cmd, done)
	return nil
}

func (d *ProcessDriver) writeEnvelope(envelope Envelope) error {
	if err := envelope.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	d.mu.Lock()
	stdin := d.stdin
	d.mu.Unlock()
	if stdin == nil {
		return fmt.Errorf("%s runtime is not running", d.cfg.Name)
	}
	if _, err := stdin.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("write %s runtime frame: %w", d.cfg.Name, err)
	}
	return nil
}

func (d *ProcessDriver) readStdout(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 8*1024*1024)
	for scanner.Scan() {
		var envelope Envelope
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			log.Printf("[RUNTIME:%s] invalid JSON frame: %v", d.cfg.Name, err)
			continue
		}
		if err := envelope.Validate(); err != nil {
			log.Printf("[RUNTIME:%s] invalid protocol frame: %v", d.cfg.Name, err)
			continue
		}
		if envelope.Type != "event" {
			log.Printf("[RUNTIME:%s] unsupported frame type %q", d.cfg.Name, envelope.Type)
			continue
		}
		var event Event
		if err := json.Unmarshal(envelope.Payload, &event); err != nil {
			log.Printf("[RUNTIME:%s] invalid event payload: %v", d.cfg.Name, err)
			continue
		}
		d.deliver(envelope.ExchangeID, processResult{event: event})
	}
	err := scanner.Err()
	if err == nil {
		err = io.EOF
	}
	d.failPending(fmt.Errorf("%s runtime output closed: %w", d.cfg.Name, err))
}

func (d *ProcessDriver) readStderr(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		log.Printf("[RUNTIME:%s] %s", d.cfg.Name, scanner.Text())
	}
}

func (d *ProcessDriver) wait(cmd *exec.Cmd, done chan struct{}) {
	err := cmd.Wait()
	d.mu.Lock()
	if d.cmd == cmd {
		d.cmd = nil
		d.stdin = nil
	}
	d.mu.Unlock()
	if err != nil {
		d.failPending(fmt.Errorf("%s runtime exited: %w", d.cfg.Name, err))
	}
	close(done)
}

func (d *ProcessDriver) deliver(exchangeID string, result processResult) {
	d.mu.Lock()
	channel := d.pending[exchangeID]
	d.mu.Unlock()
	if channel == nil {
		log.Printf("[RUNTIME:%s] event for unknown exchange %s", d.cfg.Name, exchangeID)
		return
	}
	channel <- result
}

func (d *ProcessDriver) failPending(err error) {
	d.mu.Lock()
	channels := make([]chan processResult, 0, len(d.pending))
	for _, channel := range d.pending {
		channels = append(channels, channel)
	}
	d.mu.Unlock()
	for _, channel := range channels {
		select {
		case channel <- processResult{err: err}:
		default:
		}
	}
}

func validateTurnRequest(request TurnRequest) error {
	if strings.TrimSpace(request.ConversationID) == "" {
		return fmt.Errorf("conversation id is required")
	}
	if strings.TrimSpace(request.TaskID) == "" {
		return fmt.Errorf("task id is required")
	}
	if strings.TrimSpace(request.RequestID) == "" {
		return fmt.Errorf("request id is required")
	}
	if len(request.Inputs) == 0 {
		return fmt.Errorf("at least one runtime input is required")
	}
	for index, input := range request.Inputs {
		switch input.Kind {
		case InputUserMessage:
			if strings.TrimSpace(input.Content) == "" {
				return fmt.Errorf("input %d user message is empty", index)
			}
		case InputToolResult:
			if strings.TrimSpace(input.ToolCallID) == "" {
				return fmt.Errorf("input %d tool result has no call id", index)
			}
		default:
			return fmt.Errorf("input %d has unsupported kind %q", index, input.Kind)
		}
	}
	return nil
}

func isToolResultOnly(inputs []Input) bool {
	if len(inputs) == 0 {
		return false
	}
	for _, input := range inputs {
		if input.Kind != InputToolResult {
			return false
		}
	}
	return true
}
