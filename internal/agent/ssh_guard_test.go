package agent

import "testing"

func TestRedundantSSHHostBlocksManagedHost(t *testing.T) {
	target := ManagedSSHTarget{
		Host:            "47.115.32.237",
		SessionHostname: "iZwz94kqmvp7aaxi22dsh5Z",
	}
	commands := []string{
		`ssh root@47.115.32.237 "pwd"`,
		`env DISPLAY=x ssh -p 22 -o 'ServerAliveInterval=30' root@47.115.32.237`,
		`command ssh root@iZwz94kqmvp7aaxi22dsh5Z`,
		`printf ok; /usr/bin/ssh localhost`,
	}
	for _, command := range commands {
		if _, blocked := RedundantSSHHost(command, target); !blocked {
			t.Fatalf("expected redundant SSH to be blocked: %s", command)
		}
	}
}

func TestRedundantSSHHostAllowsDifferentJumpTarget(t *testing.T) {
	target := ManagedSSHTarget{
		Host:            "bastion.example.com",
		SessionHostname: "bastion-01",
	}
	commands := []string{
		`ssh app@10.0.2.15`,
		`ssh -J root@bastion.example.com app@10.0.2.15`,
		`echo "ssh root@bastion.example.com"`,
	}
	for _, command := range commands {
		if host, blocked := RedundantSSHHost(command, target); blocked {
			t.Fatalf("different target should be allowed, blocked host %q in %s", host, command)
		}
	}
}
