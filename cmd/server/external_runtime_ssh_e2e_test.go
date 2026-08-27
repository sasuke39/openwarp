package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestManagedBackgroundCommandOverRealSSH is opt-in because it requires an SSH
// server. It runs the production background command strings through the real
// OpenSSH client and a real remote login shell; only the host is configurable.
func TestManagedBackgroundCommandOverRealSSH(t *testing.T) {
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

	runner := realSSHRunner{target: target, key: key, port: port}
	if output := runner.run(t, "printf ssh-short-ok"); output != "ssh-short-ok" {
		t.Fatalf("short SSH command output = %q", output)
	}
	visiblePTY := startPersistentSSHPTY(t, runner)
	visiblePTY.run(t, "printf visible-pty-before")

	jobID := uuid.NewString()
	t.Cleanup(func() { _ = runner.runAllowError(managedBackgroundCancelCommand(jobID)) })
	t.Cleanup(func() { _ = runner.runAllowError("rm -rf -- " + shellQuote(managedBackgroundJobDir(jobID))) })

	startTime := time.Now()
	startOutput := runner.run(t, managedBackgroundStartCommand(
		jobID,
		`read value; printf 'received=%s\n' "$value"; sleep 30`,
	))
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
