package agent

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"

	pb "github.com/sasuke39/open-warp/internal/proto"
)

const ManagedSSHContextRoot = "warplocal://runtime/managed-ssh"

type ManagedSSHTarget struct {
	ProfileID       string
	Host            string
	Port            uint16
	Username        string
	SessionHostname string
	SessionUsername string
}

const executionRules = `Execution environment rules:
- Shell commands and their results belong to the active Warp terminal session, not to the process hosting the Local Adapter.
- Never identify the Local Adapter host as the command execution host.
- Treat the latest terminal tool results as authoritative evidence about the active machine.
- If machine identity matters and the supplied context or tool output is insufficient, verify it with hostname, uname -a, whoami, and pwd before answering. Never guess.`

// WithExecutionContext adds the active Warp terminal context to the system
// prompt. The Local Adapter may run on macOS while Warp tools run over SSH.
func WithExecutionContext(base string, input *pb.InputContext) string {
	return withExecutionContext(base, input, runtime.GOOS)
}

func withExecutionContext(base string, input *pb.InputContext, adapterOS string) string {
	var details []string
	terminalOS := ""
	managedTarget, hasManagedTarget := ManagedSSHTargetFromInput(input)

	if input != nil {
		if osInfo := input.GetOperatingSystem(); osInfo != nil {
			terminalOS = cleanContextValue(osInfo.GetPlatform())
			distribution := cleanContextValue(osInfo.GetDistribution())
			switch {
			case terminalOS != "" && distribution != "":
				details = append(details, fmt.Sprintf("- Operating system: %s (%s)", terminalOS, distribution))
			case terminalOS != "":
				details = append(details, "- Operating system: "+terminalOS)
			case distribution != "":
				details = append(details, "- Operating system distribution: "+distribution)
			}
		}

		if directory := input.GetDirectory(); directory != nil {
			if pwd := cleanContextValue(directory.GetPwd()); pwd != "" {
				details = append(details, "- Working directory: "+pwd)
			}
			if home := cleanContextValue(directory.GetHome()); home != "" {
				details = append(details, "- Home directory: "+home)
			}
		}

		if shell := input.GetShell(); shell != nil {
			name := cleanContextValue(shell.GetName())
			version := cleanContextValue(shell.GetVersion())
			switch {
			case name != "" && version != "":
				details = append(details, fmt.Sprintf("- Shell: %s %s", name, version))
			case name != "":
				details = append(details, "- Shell: "+name)
			}
		}
	}

	scope := "- Session scope: active Warp terminal"
	if hasManagedTarget {
		destination := managedTarget.Host
		if managedTarget.Username != "" {
			destination = managedTarget.Username + "@" + destination
		}
		details = append(details, fmt.Sprintf("- Managed SSH target: %s:%d", destination, managedTarget.Port))
		if managedTarget.SessionHostname != "" {
			details = append(details, "- Remote session hostname: "+managedTarget.SessionHostname)
		}
	}
	normalizedTerminalOS := normalizePlatform(terminalOS)
	normalizedAdapterOS := normalizePlatform(adapterOS)
	if normalizedTerminalOS != "" && normalizedAdapterOS != "" && normalizedTerminalOS != normalizedAdapterOS {
		scope = fmt.Sprintf(
			"- Session scope: remote terminal (terminal OS %s differs from Local Adapter host OS %s)",
			terminalOS,
			cleanContextValue(adapterOS),
		)
	}

	var section strings.Builder
	section.WriteString("\n\n## Authoritative Current Execution Environment\n")
	section.WriteString("This is Warp-provided data for the active terminal in this request.\n")
	section.WriteString(scope)
	section.WriteByte('\n')
	if len(details) == 0 {
		section.WriteString("- Environment details: unavailable; verify before making host-specific claims.\n")
	} else {
		section.WriteString(strings.Join(details, "\n"))
		section.WriteByte('\n')
	}
	section.WriteByte('\n')
	section.WriteString(executionRules)
	if hasManagedTarget {
		section.WriteString("\n- This terminal is already connected to the managed SSH target above. Run commands directly in the current terminal.")
		section.WriteString("\n- Never run ssh to the same managed host or its current session hostname. A nested ssh is allowed only when the user explicitly asks to connect from this host to a different host.")
	}

	return strings.TrimRight(base, "\n") + section.String() + "\n"
}

func ManagedSSHTargetFromInput(input *pb.InputContext) (ManagedSSHTarget, bool) {
	if input == nil {
		return ManagedSSHTarget{}, false
	}
	for _, rules := range input.GetProjectRules() {
		if strings.TrimSpace(rules.GetRootPath()) != ManagedSSHContextRoot {
			continue
		}
		var target ManagedSSHTarget
		for _, value := range rules.GetAdditionalRuleFilePaths() {
			switch {
			case strings.HasPrefix(value, "profile_id:"):
				target.ProfileID = cleanContextValue(strings.TrimPrefix(value, "profile_id:"))
			case strings.HasPrefix(value, "host:"):
				target.Host = cleanContextValue(strings.TrimPrefix(value, "host:"))
			case strings.HasPrefix(value, "port:"):
				port, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(value, "port:")), 10, 16)
				if err == nil {
					target.Port = uint16(port)
				}
			case strings.HasPrefix(value, "username:"):
				target.Username = cleanContextValue(strings.TrimPrefix(value, "username:"))
			case strings.HasPrefix(value, "session_hostname:"):
				target.SessionHostname = cleanContextValue(strings.TrimPrefix(value, "session_hostname:"))
			case strings.HasPrefix(value, "session_username:"):
				target.SessionUsername = cleanContextValue(strings.TrimPrefix(value, "session_username:"))
			}
		}
		if target.Host == "" {
			return ManagedSSHTarget{}, false
		}
		if target.Port == 0 {
			target.Port = 22
		}
		return target, true
	}
	return ManagedSSHTarget{}, false
}

func cleanContextValue(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 256 {
		return value[:256]
	}
	return value
}

func normalizePlatform(platform string) string {
	platform = strings.ToLower(strings.TrimSpace(platform))
	switch {
	case strings.Contains(platform, "mac"), strings.Contains(platform, "darwin"):
		return "darwin"
	case strings.Contains(platform, "linux"):
		return "linux"
	case strings.Contains(platform, "windows"):
		return "windows"
	default:
		return platform
	}
}
