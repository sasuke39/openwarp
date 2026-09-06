package remotemcp

import (
	"context"
	"errors"
	"flag"
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Run only uses stdout for MCP protocol frames. The caller sends diagnostics to stderr.
func Run(ctx context.Context, args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("mcp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	socket := flags.String("socket", "", "Private Unix socket exposed by the running WarpLocal desktop client")
	clientID := flags.String("client-id", "external", "Stable caller context, e.g. codex or claude-code; not an authentication credential")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *socket == "" || flags.NArg() != 0 {
		return errors.New("usage: warp-local-adapter mcp --socket /absolute/private/directory/tools.sock")
	}
	return NewServer(IPC{Socket: *socket, ClientID: *clientID}).Run(ctx, &mcp.StdioTransport{})
}
