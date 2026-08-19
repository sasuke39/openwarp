package agent

import (
	"path/filepath"
	"strings"
)

// RedundantSSHHost returns the destination when command attempts to SSH back
// into the host that already owns the active managed SSH terminal.
func RedundantSSHHost(command string, target ManagedSSHTarget) (string, bool) {
	for _, destination := range sshDestinations(command) {
		host := sshDestinationHost(destination)
		if host == "" {
			continue
		}
		if isLoopbackHost(host) ||
			sameSSHHost(host, target.Host) ||
			sameSSHHost(host, target.SessionHostname) {
			return host, true
		}
	}
	return "", false
}

func sshDestinations(command string) []string {
	tokens := shellTokens(command)
	var destinations []string
	for i := 0; i < len(tokens); i++ {
		if filepath.Base(tokens[i]) != "ssh" {
			continue
		}
		if destination, next, ok := sshDestination(tokens, i+1); ok {
			destinations = append(destinations, destination)
			i = next - 1
		}
	}
	return destinations
}

func sshDestination(tokens []string, start int) (string, int, bool) {
	optionsWithValue := map[string]bool{
		"-B": true, "-b": true, "-c": true, "-D": true, "-E": true, "-e": true,
		"-F": true, "-I": true, "-i": true, "-J": true, "-L": true, "-l": true,
		"-m": true, "-O": true, "-o": true, "-P": true, "-p": true, "-Q": true,
		"-R": true, "-S": true, "-W": true, "-w": true,
	}
	for i := start; i < len(tokens); i++ {
		token := tokens[i]
		if isShellBoundary(token) {
			return "", i + 1, false
		}
		if token == "--" {
			if i+1 < len(tokens) && !isShellBoundary(tokens[i+1]) {
				return tokens[i+1], i + 2, true
			}
			return "", i + 1, false
		}
		if strings.HasPrefix(token, "-") {
			if optionsWithValue[token] && i+1 < len(tokens) {
				i++
			}
			continue
		}
		return token, i + 1, true
	}
	return "", len(tokens), false
}

func shellTokens(command string) []string {
	var tokens []string
	var current strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}

	for _, r := range command {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if strings.ContainsRune(";&|\n", r) {
			flush()
			tokens = append(tokens, string(r))
			continue
		}
		if r == ' ' || r == '\t' || r == '\r' {
			flush()
			continue
		}
		current.WriteRune(r)
	}
	if escaped {
		current.WriteRune('\\')
	}
	flush()
	return tokens
}

func sshDestinationHost(destination string) string {
	destination = strings.TrimSpace(destination)
	if at := strings.LastIndex(destination, "@"); at >= 0 {
		destination = destination[at+1:]
	}
	return normalizeSSHHost(destination)
}

func sameSSHHost(left, right string) bool {
	left = normalizeSSHHost(left)
	right = normalizeSSHHost(right)
	return left != "" && right != "" && left == right
}

func normalizeSSHHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimSuffix(host, ".")
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	return host
}

func isLoopbackHost(host string) bool {
	host = normalizeSSHHost(host)
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func isShellBoundary(token string) bool {
	return token == ";" || token == "&" || token == "|" || token == "\n"
}
