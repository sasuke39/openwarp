// Package remotemcp exposes workspace tools over MCP stdio, forwarding execution
// to the running desktop client. No SSH credentials or model runtime live here.
package remotemcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

const ProtocolVersion = 1
const MaxMessageBytes = 1 << 20

// Request keeps control operations separate from new command execution.
type Request struct {
	ClientID   string `json:"client_id"`
	Version    int    `json:"version"`
	RequestID  string `json:"request_id"`
	Operation  string `json:"operation"`
	ServerID   string `json:"server_id,omitempty"`
	CommandID  string `json:"command_id,omitempty"`
	Command    string `json:"command,omitempty"`
	Workdir    string `json:"workdir,omitempty"`
	Background bool   `json:"background"`
	LocalPath  string `json:"local_path,omitempty"`
	RemotePath string `json:"remote_path,omitempty"`
}

type Response struct {
	Version   int            `json:"version"`
	RequestID string         `json:"request_id"`
	Error     string         `json:"error,omitempty"`
	Result    map[string]any `json:"result,omitempty"`
}

type Transport interface {
	Call(context.Context, Request) (map[string]any, error)
}

type IPC struct {
	Socket   string
	ClientID string
}

func (ipc IPC) Call(ctx context.Context, req Request) (map[string]any, error) {
	req.Version = ProtocolVersion
	req.ClientID = ipc.ClientID
	if req.ClientID == "" {
		req.ClientID = "external"
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if len(payload) >= MaxMessageBytes {
		return nil, errors.New("tool request too large")
	}
	ctx, cancel := context.WithTimeout(ctx, 150*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", ipc.Socket)
	if err != nil {
		return nil, fmt.Errorf("WarpLocal IPC unavailable; start the authorized desktop client: %w", err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return nil, err
	}
	frame, err := bufio.NewReader(io.LimitReader(conn, MaxMessageBytes)).ReadBytes('\n')
	if err != nil || len(frame) >= MaxMessageBytes {
		return nil, fmt.Errorf("incomplete desktop IPC response: request_id=%s received_bytes=%d; execution outcome unknown; do not rerun with a new request_id (read error: %v)", req.RequestID, len(frame), err)
	}
	var response Response
	if err := json.Unmarshal(frame, &response); err != nil {
		return nil, fmt.Errorf("invalid desktop response: request_id=%s received_bytes=%d; execution outcome unknown; do not rerun with a new request_id: %w", req.RequestID, len(frame), err)
	}
	if response.Version != ProtocolVersion || response.RequestID != req.RequestID {
		return nil, errors.New("desktop IPC response version or request_id mismatch")
	}
	if response.Error != "" {
		return nil, errors.New(response.Error)
	}
	if response.Result == nil {
		return nil, errors.New("desktop IPC returned no result")
	}
	return response.Result, nil
}
