package agent

import "testing"

func TestRedundantSSHHostBlocksManagedHost(t *testing.T) {
	target := ManagedSSHTarget{
		Host:            "192.0.2.10",
		SessionHostname: "demo-host",
	}
	commands := []string{
		`ssh root@192.0.2.10 "pwd"`,
		`env DISPLAY=x ssh -p 22 -o 'ServerAliveInterval=30' root@192.0.2.10`,
		`command ssh root@demo-host`,
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
