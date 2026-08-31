package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sasuke39/open-warp/internal/agentruntime"
)

// TestManagedBackgroundCommandOverRealSSH is opt-in because it requires an SSH
// server. It runs the production background command strings through the real
// OpenSSH client and a real remote login shell; only the host is configurable.
func TestManagedBackgroundCommandOverRealSSH(t *testing.T) {
	runner := configuredRealSSHRunner(t)

	foregroundCall, err := translateExternalToolCall(agentruntime.ToolCall{

		ID:        "foreground-over-ssh",
		Name:      agentruntime.ToolWorkspaceShell,
		Arguments: json.RawMessage(`{"command":"printf ssh-short-ok","executionMode":"foreground"}`),
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	var foregroundArgs struct {
		Command           string `json:"command"`
		WaitUntilComplete *bool  `json:"wait_until_complete"`
	}
	if err := json.Unmarshal(foregroundCall.Args, &foregroundArgs); err != nil {
		t.Fatal(err)
	}
	if foregroundArgs.WaitUntilComplete == nil || !*foregroundArgs.WaitUntilComplete {
		t.Fatal("managed SSH foreground command must wait for direct command output")
	}
	if output := runner.run(t, foregroundArgs.Command); output != "ssh-short-ok" {
		t.Fatalf("foreground SSH command output = %q", output)
	}

	failureCall, err := translateExternalToolCall(agentruntime.ToolCall{
		ID:        "foreground-failure-over-ssh",
		Name:      agentruntime.ToolWorkspaceShell,
		Arguments: json.RawMessage(`{"command":"printf ssh-failure-output; exit 7","executionMode":"foreground"}`),
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	var failureArgs struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(failureCall.Args, &failureArgs); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	failureOutput, failureErr := runner.command(ctx, failureArgs.Command).CombinedOutput()
	cancel()
	exitErr, ok := failureErr.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 7 || string(failureOutput) != "ssh-failure-output" {
		t.Fatalf("foreground SSH failure = err %v, output %q", failureErr, failureOutput)
	}
	visiblePTY := startPersistentSSHPTY(t, runner)
	visiblePTY.run(t, "printf visible-pty-before")

	jobID := uuid.NewString()
	t.Cleanup(func() { _ = runner.runAllowError(managedBackgroundCancelCommand(jobID)) })
	t.Cleanup(func() { _ = runner.runAllowError("rm -rf -- " + shellQuote(managedBackgroundJobDir(jobID))) })

	startTime := time.Now()
	startCommand := managedBackgroundStartCommand(
		jobID,
		`read value; printf 'received=%s\n' "$value"; sleep 30`,
	)
	startOutput := runner.run(t, startCommand)
	if elapsed := time.Since(startTime); elapsed > 3*time.Second {
		t.Fatalf("background launcher blocked for %s", elapsed)
	}
	if !strings.Contains(startOutput, "status=running") {
		t.Fatalf("background launcher output = %q", startOutput)
	}

	// Keep one interactive SSH PTY open for the whole test, just like the
	// terminal tab. Independent Agent commands must not block it.
	visiblePTY.run(t, "printf visible-pty-during-background")

	if output := runner.run(t, managedBackgroundWriteCommand(jobID, "hello-over-ssh\n")); !strings.Contains(output, "input delivered") {
		t.Fatalf("background stdin result = %q", output)
	}
	waitForRemoteOutput(t, runner, jobID, "received=hello-over-ssh")

	readOutput := runner.run(t, managedBackgroundReadCommand(jobID))
	if !strings.Contains(readOutput, "status=running") || !strings.Contains(readOutput, "received=hello-over-ssh") {
		t.Fatalf("background read result = %q", readOutput)
	}
	if output := runner.run(t, managedBackgroundCancelCommand(jobID)); !strings.Contains(output, "command cancelled") {
		t.Fatalf("background cancel result = %q", output)
	}
	waitForRemoteStopped(t, runner, jobID)

	// The same SSH target must remain usable after stopping the background job.
	if output := runner.run(t, "printf next-turn-ok"); output != "next-turn-ok" {
		t.Fatalf("SSH command after cancellation = %q", output)
	}
	visiblePTY.run(t, "printf visible-pty-after-cancel")
}

func TestSteerActiveTurnWhileRealSSHCommandRuns(t *testing.T) {
	runner := configuredRealSSHRunner(t)
	commandDone := make(chan struct {
		output string
		err    error
	}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		output, err := runner.command(ctx, "sleep 1; printf ssh-tool-finished").CombinedOutput()
		commandDone <- struct {
			output string
			err    error
		}{string(output), err}
	}()
	time.Sleep(100 * time.Millisecond)

	driver := &steerRecordingDriver{}
	server := &Server{}
	turn := server.activeTurns.begin("ssh-warp-task", "runtime-conversation", driver)
	mux := http.NewServeMux()
	registerAgentControlRoutes(mux, server)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/agent/tasks/ssh-warp-task/steer",
		bytes.NewBufferString(`{"conversation_id":"ui-conversation","prompt":"inspect logs next"}`),
	)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("steer during SSH command status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	snapshot := turn.snapshot()
	if driver.request.ConversationID != snapshot.ConversationID || driver.request.TurnID != snapshot.TurnID {
		t.Fatalf("steer used non-canonical Agent Turn: %+v", driver.request)
	}
	result := <-commandDone
	if result.err != nil || result.output != "ssh-tool-finished" {
		t.Fatalf("SSH command after steer = output %q, err %v", result.output, result.err)
	}
	if output := runner.run(t, "printf ssh-next-exchange-ok"); output != "ssh-next-exchange-ok" {
		t.Fatalf("SSH next exchange output = %q", output)
	}
}

func configuredRealSSHRunner(t *testing.T) realSSHRunner {
	t.Helper()
	target := os.Getenv("WARPLOCAL_E2E_SSH_TARGET")
	key := os.Getenv("WARPLOCAL_E2E_SSH_KEY")
	portText := os.Getenv("WARPLOCAL_E2E_SSH_PORT")
	if target == "" || key == "" || portText == "" {
		t.Skip("set WARPLOCAL_E2E_SSH_TARGET, WARPLOCAL_E2E_SSH_KEY and WARPLOCAL_E2E_SSH_PORT")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		t.Fatalf("invalid SSH port %q", portText)
	}

	return realSSHRunner{target: target, key: key, port: port}
}

type realSSHRunner struct {
	target string
	key    string
	port   int
}

type persistentSSHPTY struct {
	stdin  io.WriteCloser
	lines  chan string
	cancel context.CancelFunc
	cmd    *exec.Cmd
}

func startPersistentSSHPTY(t *testing.T, runner realSSHRunner) *persistentSSHPTY {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, "ssh",
		"-tt",
		"-p", strconv.Itoa(runner.port),
		"-i", runner.key,
		"-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-o", "ConnectTimeout=3",
		runner.target,
	)
	stdin, err := command.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	pty := &persistentSSHPTY{stdin: stdin, lines: make(chan string, 128), cancel: cancel, cmd: command}
	forwardScanner := func(reader io.Reader) {
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			pty.lines <- scanner.Text()
		}
	}
	go forwardScanner(stdout)
	go forwardScanner(stderr)
	// Disable terminal echo so a marker is observed only after the remote shell
	// actually executes the command, rather than when the PTY echoes input.
	_, _ = io.WriteString(stdin, "stty -echo\n")
	time.Sleep(100 * time.Millisecond)
	pty.run(t, "printf visible-pty-ready")
	t.Cleanup(func() {
		_, _ = io.WriteString(stdin, "exit\n")
		_ = stdin.Close()
		done := make(chan struct{})
		go func() {
			_ = command.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			cancel()
			<-done
		}
	})
	return pty
}

func (pty *persistentSSHPTY) run(t *testing.T, command string) {
	t.Helper()
	marker := "__WARPLOCAL_PTY_" + strings.ReplaceAll(uuid.NewString(), "-", "") + "__"
	if _, err := fmt.Fprintf(pty.stdin, "%s; printf '\\n%s\\n'\n", command, marker); err != nil {
		t.Fatalf("write persistent SSH PTY: %v", err)
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case line := <-pty.lines:
			if strings.Contains(line, marker) {
				return
			}
		case <-deadline.C:
			t.Fatalf("persistent SSH PTY did not execute %q", command)
		}
	}
}

func (runner realSSHRunner) command(ctx context.Context, remoteCommand string) *exec.Cmd {
	return exec.CommandContext(ctx, "ssh",
		"-p", strconv.Itoa(runner.port),
		"-i", runner.key,
		"-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-o", "ConnectTimeout=3",
		runner.target,
		remoteCommand,
	)
}

func (runner realSSHRunner) run(t *testing.T, remoteCommand string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	output, err := runner.command(ctx, remoteCommand).CombinedOutput()
	if err != nil {
		t.Fatalf("SSH command failed: %v\ncommand: %s\noutput: %s", err, remoteCommand, output)
	}
	return string(output)
}

func (runner realSSHRunner) runAllowError(remoteCommand string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	output, err := runner.command(ctx, remoteCommand).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, output)
	}
	return nil
}

func waitForRemoteOutput(t *testing.T, runner realSSHRunner, jobID, expected string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		output := runner.run(t, managedBackgroundReadCommand(jobID))
		if strings.Contains(output, expected) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("remote background output %q did not appear: %s", expected, output)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func waitForRemoteStopped(t *testing.T, runner realSSHRunner, jobID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		output := runner.run(t, managedBackgroundReadCommand(jobID))
		if !strings.Contains(output, "status=running") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("remote background job did not stop: %s", output)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
