// Package workspacetools is the canonical workspace-tool translation used by
// bundled harnesses and the local MCP bridge. It never executes commands.
package workspacetools

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/sasuke39/open-warp/internal/agentruntime"
	"github.com/sasuke39/open-warp/internal/llm"
	"mvdan.cc/sh/v3/syntax"
	"strings"
)

func Translate(call agentruntime.ToolCall, managedSSH bool) (llm.ToolCall, error) {
	marshal := func(name string, value any) (llm.ToolCall, error) {
		raw, err := json.Marshal(value)
		if err != nil {
			return llm.ToolCall{}, err
		}
		return llm.ToolCall{ID: call.ID, Name: name, Args: raw}, nil
	}
	switch call.Name {
	case agentruntime.ToolWorkspaceShell:
		var args struct {
			Command         string `json:"command"`
			Workdir         string `json:"workdir"`
			CommandID       string `json:"commandId"`
			ExecutionMode   string `json:"executionMode"`
			ExecutionModeV1 string `json:"execution_mode"`
			RunInBackground *bool  `json:"run_in_background"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return llm.ToolCall{}, fmt.Errorf("decode external bash call: %w", err)
		}
		if err := ValidateShellSyntax(args.Command); err != nil {
			return llm.ToolCall{}, err
		}
		command := args.Command
		if strings.TrimSpace(args.Workdir) != "" {
			command = "cd -- " + ShellQuote(args.Workdir) + " && " + command
		}
		executionMode := strings.TrimSpace(args.ExecutionMode)
		if executionMode == "" {
			executionMode = strings.TrimSpace(args.ExecutionModeV1)
		}
		if executionMode == "" && args.RunInBackground != nil {
			if *args.RunInBackground {
				executionMode = "background"
			} else {
				executionMode = "foreground"
			}
		}
		if executionMode == "" {
			return llm.ToolCall{}, fmt.Errorf("workspace.shell requires an explicit execution mode: foreground or background")
		}
		var waitUntilComplete bool
		switch executionMode {
		case "foreground":
			if ShellCommandStartsBackgroundJob(args.Command) {
				return llm.ToolCall{}, fmt.Errorf("foreground workspace.shell must not contain shell background operators; use executionMode=background without nohup, &, or disown")
			}
			// Foreground means that the tool call waits for the command's real result.
			// For managed SSH, the App uses its independent session executor for this
			// path so stdout is returned directly instead of reconstructed from a
			// hidden terminal block. Local terminals still render through their PTY.
			waitUntilComplete = true
		case "background":
			// The client starts the original command in its managed executor and
			// immediately returns the resulting command_id. Keeping the wrapper
			// client-side preserves the original command in the UI.
			waitUntilComplete = false
		default:
			return llm.ToolCall{}, fmt.Errorf("unsupported workspace.shell execution mode %q", executionMode)
		}
		return marshal("run_shell_command", map[string]any{
			"command": command, "is_read_only": false, "is_risky": false, "risk_category": "",
			"wait_until_complete": waitUntilComplete,
		})
	case agentruntime.ToolWorkspaceProcessRead,
		agentruntime.ToolWorkspaceProcessWrite,
		agentruntime.ToolWorkspaceProcessCancel:
		var args struct {
			CommandID string `json:"commandId"`
			Input     string `json:"input"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return llm.ToolCall{}, fmt.Errorf("decode external process call: %w", err)
		}
		if !ValidBackgroundCommandID(args.CommandID) {
			return llm.ToolCall{}, fmt.Errorf("external process tool requires a valid commandId")
		}
		var command string
		switch call.Name {
		case agentruntime.ToolWorkspaceProcessRead:
			command = ManagedBackgroundReadCommand(args.CommandID)
		case agentruntime.ToolWorkspaceProcessWrite:
			command = ManagedBackgroundWriteCommand(args.CommandID, args.Input)
		case agentruntime.ToolWorkspaceProcessCancel:
			command = ManagedBackgroundCancelCommand(args.CommandID)
		}
		return marshal("run_shell_command", map[string]any{
			"command": command, "is_read_only": call.Name == agentruntime.ToolWorkspaceProcessRead,
			"is_risky": false, "risk_category": "", "wait_until_complete": true,
		})
	case agentruntime.ToolWorkspaceReadFile:
		var args struct {
			FilePath string `json:"file_path"`
			Offset   int    `json:"offset"`
			Limit    int    `json:"limit"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return llm.ToolCall{}, fmt.Errorf("decode external read call: %w", err)
		}
		if args.Offset <= 0 {
			args.Offset = 1
		}
		if args.Limit <= 0 {
			args.Limit = 200
		}
		if managedSSH {
			end := args.Offset + args.Limit - 1
			command := fmt.Sprintf("sed -n '%d,%dp' < %s", args.Offset, end, ShellQuote(args.FilePath))
			return marshal("run_shell_command", map[string]any{
				"command": command, "is_read_only": true, "is_risky": false, "risk_category": "",
			})
		}
		return marshal("read_files", map[string]any{"files": []any{map[string]any{
			"name": args.FilePath, "line_ranges": []any{map[string]int{"start": args.Offset, "end": args.Offset + args.Limit - 1}},
		}}})
	case agentruntime.ToolWorkspaceWriteFile:
		var args struct {
			FilePath string `json:"file_path"`
			Content  string `json:"content"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return llm.ToolCall{}, fmt.Errorf("decode external write call: %w", err)
		}
		return marshal("apply_file_diffs", map[string]any{
			"summary":   "External agent write " + args.FilePath,
			"new_files": []any{map[string]string{"file_path": args.FilePath, "content": args.Content}},
		})
	case agentruntime.ToolWorkspaceEditFile:
		var args struct {
			FilePath  string `json:"file_path"`
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return llm.ToolCall{}, fmt.Errorf("decode external edit call: %w", err)
		}
		return marshal("apply_file_diffs", map[string]any{
			"summary": "External agent edit " + args.FilePath,
			"diffs":   []any{map[string]string{"file_path": args.FilePath, "search": args.OldString, "replace": args.NewString}},
		})
	case agentruntime.ToolWorkspaceGlob:
		var args struct {
			Pattern string `json:"pattern"`
			Path    string `json:"path"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return llm.ToolCall{}, fmt.Errorf("decode external glob call: %w", err)
		}
		return marshal("file_glob_v2", map[string]any{"patterns": []string{args.Pattern}, "search_dir": args.Path, "max_matches": 200})
	case agentruntime.ToolWorkspaceGrep:
		var args struct {
			Pattern string `json:"pattern"`
			Path    string `json:"path"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return llm.ToolCall{}, fmt.Errorf("decode external grep call: %w", err)
		}
		return marshal("grep", map[string]any{"queries": []string{args.Pattern}, "path": args.Path})
	default:
		return llm.ToolCall{}, fmt.Errorf("external workspace tool %q has no Warp mapping", call.Name)
	}
}

func ShellCommandStartsBackgroundJob(command string) bool {
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return false
	}
	found := false
	syntax.Walk(file, func(node syntax.Node) bool {
		if stmt, ok := node.(*syntax.Stmt); ok && stmt.Background {
			found = true
			return false
		}
		if call, ok := node.(*syntax.CallExpr); ok && len(call.Args) > 0 && len(call.Args[0].Parts) == 1 {
			if literal, ok := call.Args[0].Parts[0].(*syntax.Lit); ok && (literal.Value == "nohup" || literal.Value == "disown") {
				found = true
				return false
			}
		}
		return !found
	})
	return found
}

func ValidateShellSyntax(command string) error {
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("workspace.shell command must not be empty")
	}
	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	if _, err := parser.Parse(strings.NewReader(command), "agent-command"); err != nil {
		return fmt.Errorf("workspace.shell command has incomplete or invalid Bash syntax: %w", err)
	}
	return nil
}

func ValidBackgroundCommandID(commandID string) bool {
	_, err := uuid.Parse(commandID)
	return err == nil
}

func ManagedBackgroundJobDir(commandID string) string {
	return "/tmp/warplocal-agent-jobs/" + commandID
}

func ManagedBackgroundStartCommand(commandID, command string) string {
	dir := ManagedBackgroundJobDir(commandID)
	// Opening the FIFO read/write avoids blocking command startup before the
	// first input arrives, while still allowing later process.write calls.
	worker := `trap '' HUP; exec 3<>"$1"; bash -lc "$2" <&3; code=$?; date +%s > "$4"; printf '%s\n' "$code" > "$3"`
	return "job_dir=" + ShellQuote(dir) +
		"; mkdir -p \"$job_dir\"; rm -f \"$job_dir/input\" \"$job_dir/exit\" \"$job_dir/finished_at\"; date +%s > \"$job_dir/started_at\"; mkfifo \"$job_dir/input\"; " +
		"if command -v setsid >/dev/null 2>&1; then nohup setsid sh -c " + ShellQuote(worker) +
		" sh \"$job_dir/input\" " + ShellQuote(command) + " \"$job_dir/exit\" \"$job_dir/finished_at\" " +
		">\"$job_dir/output\" 2>&1 </dev/null & pid=$!; printf '%s\n' \"$pid\" > \"$job_dir/pgid\"; " +
		"else sh -c " + ShellQuote(worker) + " sh \"$job_dir/input\" " + ShellQuote(command) + " \"$job_dir/exit\" \"$job_dir/finished_at\" >\"$job_dir/output\" 2>&1 </dev/null & pid=$!; rm -f \"$job_dir/pgid\"; fi; " +
		"printf '%s\n' \"$pid\" > \"$job_dir/pid\"; " +
		"started_at=$(cat \"$job_dir/started_at\"); printf 'command_id=%s pid=%s status=running started_at=%s finished_at= elapsed_seconds=0\\n' " + ShellQuote(commandID) + " \"$pid\" \"$started_at\""
}

func ManagedBackgroundReadCommand(commandID string) string {
	dir := ManagedBackgroundJobDir(commandID)
	return "job_dir=" + ShellQuote(dir) +
		"; test -r \"$job_dir/pid\" || { echo 'unknown command_id'; exit 1; }; pid=$(cat \"$job_dir/pid\"); " +
		"if test -r \"$job_dir/exit\"; then _warplocal_status=exited:$(cat \"$job_dir/exit\"); elif kill -0 \"$pid\" 2>/dev/null; then _warplocal_status=running; else _warplocal_status=stopped; fi; " +
		"started_at=$(cat \"$job_dir/started_at\" 2>/dev/null || date +%s); finished_at=$(cat \"$job_dir/finished_at\" 2>/dev/null || true); now=$(date +%s); end=${finished_at:-$now}; elapsed=$((end-started_at)); " +
		"printf 'command_id=%s pid=%s status=%s started_at=%s finished_at=%s elapsed_seconds=%s\\n' " + ShellQuote(commandID) + " \"$pid\" \"$_warplocal_status\" \"$started_at\" \"$finished_at\" \"$elapsed\"; tail -c 65536 \"$job_dir/output\" 2>/dev/null || true"
}

func ManagedBackgroundWriteCommand(commandID, input string) string {
	dir := ManagedBackgroundJobDir(commandID)
	return "job_dir=" + ShellQuote(dir) +
		"; test -r \"$job_dir/pid\" || { echo 'unknown command_id'; exit 1; }; pid=$(cat \"$job_dir/pid\"); " +
		"kill -0 \"$pid\" 2>/dev/null || { echo 'command is not running'; exit 1; }; " +
		"printf '%s' " + ShellQuote(input) + " > \"$job_dir/input\"; echo 'input delivered'"
}

func ManagedBackgroundCancelCommand(commandID string) string {
	dir := ManagedBackgroundJobDir(commandID)
	return "job_dir=" + ShellQuote(dir) +
		"; test -r \"$job_dir/pid\" || { echo 'unknown command_id'; exit 1; }; pid=$(cat \"$job_dir/pid\"); " +
		"pgid=$(cat \"$job_dir/pgid\" 2>/dev/null || printf '%s' \"$pid\"); kill -TERM -- -\"$pgid\" 2>/dev/null || kill -TERM \"$pid\" 2>/dev/null || true; " +
		"sleep 1; kill -0 \"$pid\" 2>/dev/null && { kill -KILL -- -\"$pgid\" 2>/dev/null || kill -KILL \"$pid\" 2>/dev/null || true; }; test -r \"$job_dir/finished_at\" || date +%s > \"$job_dir/finished_at\"; " +
		"started_at=$(cat \"$job_dir/started_at\" 2>/dev/null || date +%s); finished_at=$(cat \"$job_dir/finished_at\"); elapsed=$((finished_at-started_at)); printf 'command_id=%s pid=%s status=stopped started_at=%s finished_at=%s elapsed_seconds=%s\\ncommand cancelled\\n' " + ShellQuote(commandID) + " \"$pid\" \"$started_at\" \"$finished_at\" \"$elapsed\""
}

func ShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
