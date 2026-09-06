#!/usr/bin/env bash
set -euo pipefail
# Real client executor + MCP stdio + loopback OpenSSH; never user profiles.
adapter_repo=$(cd "$(dirname "$0")/.." && pwd)
warp_repo=${WARPLOCAL_WARP_REPO:-"$adapter_repo/../warp-v0.2026.04.29.08.56.stable_00-src/warp-0.2026.04.29.08.56.stable_00"}
fixture=$(mktemp -d /tmp/warplocal-mcp-ssh.XXXXXX)
sshd_pid=''
cleanup() {
  if [[ -n "$sshd_pid" ]]; then kill "$sshd_pid" 2>/dev/null || true; wait "$sshd_pid" 2>/dev/null || true; fi
  case "$fixture" in /tmp/warplocal-mcp-ssh.*) rm -rf -- "$fixture";; esac
}
trap cleanup EXIT
port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
ssh-keygen -q -t ed25519 -N '' -f "$fixture/host_key"
ssh-keygen -q -t ed25519 -N '' -f "$fixture/client_key"
/usr/sbin/sshd -D -e -h "$fixture/host_key" -p "$port" \
  -o ListenAddress=127.0.0.1 -o "PidFile=$fixture/sshd.pid" \
  -o "AuthorizedKeysFile=$fixture/client_key.pub" -o PasswordAuthentication=no \
  -o KbdInteractiveAuthentication=no -o UsePAM=no -o StrictModes=no \
  -o 'Subsystem=sftp internal-sftp' >"$fixture/sshd.log" 2>&1 &
sshd_pid=$!
ready=false
for attempt in {1..30}; do
  if ssh -p "$port" -i "$fixture/client_key" -o IdentitiesOnly=yes \
     -o "UserKnownHostsFile=$fixture/known_hosts" -o StrictHostKeyChecking=accept-new \
     -o BatchMode=yes 127.0.0.1 true 2>/dev/null; then ready=true; break; fi
  kill -0 "$sshd_pid" 2>/dev/null || { tail -30 "$fixture/sshd.log"; exit 1; }
  sleep 0.1
done
$ready || { tail -30 "$fixture/sshd.log"; exit 1; }
export WARPLOCAL_E2E_SSH_TARGET="$(id -un)@127.0.0.1"
export WARPLOCAL_E2E_SSH_KEY="$fixture/client_key"
export WARPLOCAL_E2E_SSH_PORT="$port"
export WARPLOCAL_E2E_SSH_KNOWN_HOSTS="$fixture/known_hosts"
export WARPLOCAL_MCP_ADAPTER_REPO="$adapter_repo"
(cd "$adapter_repo" && go test ./...)
(cd "$warp_repo" && cargo test -p warp --lib --features skip_firebase_anonymous_user,ssh_drag_and_drop terminal::ssh::tool_ipc -- --nocapture)
