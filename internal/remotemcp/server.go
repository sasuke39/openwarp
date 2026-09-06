package remotemcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sasuke39/open-warp/internal/agentruntime"
	"github.com/sasuke39/open-warp/internal/workspacetools"
)

type ShellInput struct {
	ServerID      string `json:"server_id" jsonschema:"Authorized server ID from servers_list; never inferred from the active tab"`
	RequestID     string `json:"request_id" jsonschema:"Unique UUID per deliberate invocation. Reuse on transport retry; a new UUID means a new invocation"`
	Command       string `json:"command" jsonschema:"Original Bash command, without nohup, shell background operators or disown"`
	Workdir       string `json:"workdir,omitempty" jsonschema:"Optional absolute remote directory; omitted uses the saved SSH default directory, or remote HOME when none is configured"`
	ExecutionMode string `json:"execution_mode" jsonschema:"foreground or background; builds, deployments, servers and watchers normally use background"`
}

type ProcessInput struct {
	ServerID  string `json:"server_id"`
	CommandID string `json:"command_id" jsonschema:"Existing command_id returned by workspace_shell"`
}
type WriteInput struct {
	ProcessInput
	RequestID string `json:"request_id" jsonschema:"Unique UUID; reuse for delivery retries to avoid duplicate input"`
	Input     string `json:"input" jsonschema:"Exact stdin bytes, including newline if needed"`
}
type TransferInput struct {
	ServerID   string `json:"server_id"`
	RequestID  string `json:"request_id" jsonschema:"Unique UUID, reused on transport retry"`
	LocalPath  string `json:"local_path" jsonschema:"Absolute local path within an authorized transfer root"`
	RemotePath string `json:"remote_path" jsonschema:"Absolute remote path; upload and download may overwrite existing files"`
}

func NewServer(transport Transport) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "warplocal-tools", Version: "0.1.0"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "servers_list", Description: "List explicitly authorized saved SSH servers. Does not expose passwords or private keys."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, map[string]any, error) {
			result, err := transport.Call(ctx, Request{RequestID: uuid.NewString(), Operation: "servers.list"})
			return nil, result, err
		})
	mcp.AddTool(s, &mcp.Tool{Name: "workspace_shell", Description: "Run using WarpLocal's shared managed executor. Foreground waits up to 30 seconds, then returns an existing command_id for a decision, not permission to rerun. Read/cancel that job, or resubmit the exact original command in background to detach without restarting. For unfinished background jobs, inspect the conflict result before deliberately confirming a second invocation with a new request_id. A running result is not task completion."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ShellInput) (*mcp.CallToolResult, map[string]any, error) {
			if err := validateInvocation(in.ServerID, in.RequestID); err != nil {
				return nil, nil, err
			}
			if in.Workdir != "" && !absolutePath(in.Workdir) {
				return nil, nil, errors.New("workdir must be an explicit absolute remote path")
			}
			args, _ := json.Marshal(map[string]any{"command": in.Command, "execution_mode": in.ExecutionMode})
			command, err := translated(agentruntime.ToolWorkspaceShell, args)
			if err != nil {
				return nil, nil, err
			}
			result, err := transport.Call(ctx, Request{RequestID: in.RequestID, Operation: "shell", ServerID: in.ServerID, Command: command, Workdir: in.Workdir, Background: in.ExecutionMode == "background"})
			return toolResult(result, err)
		})
	for _, op := range []string{"read", "cancel"} {
		mcp.AddTool(s, &mcp.Tool{Name: "process_" + op, Description: "Operate on an existing managed job, never start a new command. Read returns status, timestamps and the latest output (a snapshot, not a delta). Cancel explicitly terminates the job."},
			func(ctx context.Context, _ *mcp.CallToolRequest, in ProcessInput) (*mcp.CallToolResult, map[string]any, error) {
				return process(ctx, transport, op, in, uuid.NewString(), "")
			})
	}
	mcp.AddTool(s, &mcp.Tool{Name: "process_write", Description: "Write stdin to the existing job. This does not start another shell or cancel the process; use process_cancel to stop it."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in WriteInput) (*mcp.CallToolResult, map[string]any, error) {
			if _, err := uuid.Parse(in.RequestID); err != nil {
				return nil, nil, errors.New("request_id must be a UUID")
			}
			return process(ctx, transport, "write", in.ProcessInput, in.RequestID, in.Input)
		})
	for _, op := range []string{"upload", "download"} {
		mcp.AddTool(s, &mcp.Tool{Name: "sftp_" + op, Description: "Transfer one file using the saved SSH profile; only explicitly allowed local transfer roots are accessible. Existing destination files may be overwritten. No credentials are returned."},
			func(ctx context.Context, _ *mcp.CallToolRequest, in TransferInput) (*mcp.CallToolResult, map[string]any, error) {
				if err := validateInvocation(in.ServerID, in.RequestID); err != nil {
					return nil, nil, err
				}
				if !absolutePath(in.LocalPath) || !absolutePath(in.RemotePath) {
					return nil, nil, errors.New("transfer paths must be absolute and contain no control characters")
				}
				result, err := transport.Call(ctx, Request{RequestID: in.RequestID, Operation: "sftp." + op, ServerID: in.ServerID, LocalPath: in.LocalPath, RemotePath: in.RemotePath})
				return toolResult(result, err)
			})
	}
	return s
}

func process(ctx context.Context, transport Transport, op string, in ProcessInput, requestID, input string) (*mcp.CallToolResult, map[string]any, error) {
	if strings.TrimSpace(in.ServerID) == "" {
		return nil, nil, errors.New("server_id is required")
	}
	args, _ := json.Marshal(map[string]string{"commandId": in.CommandID, "input": input})
	command, err := translated("workspace.process."+op, args)
	if err != nil {
		return nil, nil, err
	}
	result, err := transport.Call(ctx, Request{RequestID: requestID, Operation: "process." + op, ServerID: in.ServerID, CommandID: in.CommandID, Command: command})
	return toolResult(result, err)
}

func translated(name string, args json.RawMessage) (string, error) {
	call, err := workspacetools.Translate(agentruntime.ToolCall{Name: name, Arguments: args}, true)
	if err != nil {
		return "", err
	}
	var shell struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(call.Args, &shell); err != nil {
		return "", err
	}
	if call.Name != "run_shell_command" || shell.Command == "" {
		return "", fmt.Errorf("unsupported shared tool translation: %s", name)
	}
	return shell.Command, nil
}

func validateInvocation(serverID, requestID string) error {
	if strings.TrimSpace(serverID) == "" {
		return errors.New("server_id is required")
	}
	if _, err := uuid.Parse(requestID); err != nil {
		return errors.New("request_id must be a UUID; preserve it on retries")
	}
	return nil
}
func absolutePath(value string) bool {
	return strings.HasPrefix(value, "/") && !strings.ContainsAny(value, "\x00\r\n")
}
func toolResult(result map[string]any, err error) (*mcp.CallToolResult, map[string]any, error) {
	if err != nil {
		return nil, nil, err
	}
	failed, _ := result["is_error"].(bool)
	return &mcp.CallToolResult{IsError: failed}, result, nil
}
