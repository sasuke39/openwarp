# External Agent Runtime Protocol

`Driver` isolates Warp transport from an agent framework. The built-in process
driver launches any sidecar that speaks versioned NDJSON on stdin/stdout. DSH is
the first implementation; later runtimes use the same contract.

Every frame has `version`, `exchange_id`, `type`, and `payload`. Warp sends:

- `turn.start`: a user input starts or continues a framework-owned session.
- `turn.resume`: tool results resume the suspended framework loop.
- `turn.cancel`: cancel the task and release framework resources.
- `runtime.shutdown`: flush state and terminate cleanly.

The sidecar emits `event` envelopes whose payload type is one of:

- `assistant.delta` / `assistant.final`
- `tool.call.batch`
- `todo.changed`
- `turn.awaiting_tool`
- `turn.completed` / `turn.failed`
- `diagnostic`

`turn.awaiting_tool`, `turn.completed`, and `turn.failed` terminate one HTTP
exchange. The framework session may remain alive after `turn.awaiting_tool`.
Warp later sends matching `tool.result` inputs in `turn.resume`.

Framework-specific tools belong in the sidecar plugin. They should preserve
the framework's schemas and semantics while forwarding workspace operations to
Warp. The Go adapter translates accepted external tool names to protobuf tool
cards; unknown names fail closed instead of silently running on the adapter host.

Canonical workspace calls are `workspace.shell`, `workspace.read_file`,
`workspace.write_file`, `workspace.edit_file`, `workspace.glob`, and
`workspace.grep`. A framework plugin maps its native tool names and arguments
to these calls; the Go transport therefore has no dependency on that framework.
