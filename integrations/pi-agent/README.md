# Pi Agent Runtime Bridge

This sidecar embeds `@earendil-works/pi-coding-agent` behind OpenWarp's
versioned external runtime protocol. Pi owns the agent loop, persistent session
tree, retry policy, compaction, skills, prompt templates, and cancellation.

Pi's `bash`, `read`, `write`, `edit`, `grep`, `find`, and `ls` definitions
override its local built-ins. Each call is suspended and forwarded to Warp, so
commands execute in the active terminal, including managed SSH sessions.

```bash
npm install
npm run build
```

Configure OpenWarp with Node `>=22.19`:

```yaml
agent_runtime:
  driver: pi-agent
  command: node
  args:
    - /absolute/path/to/integrations/pi-agent/dist/main.js
```

The adapter passes its provider settings through `AGENT_RUNTIME_API_KEY`,
`AGENT_RUNTIME_BASE_URL`, `AGENT_RUNTIME_MODEL`, and
`AGENT_RUNTIME_MAX_TOKENS`. The endpoint must support OpenAI Chat Completions;
the configured memory context window is also forwarded to Pi's model metadata.

Sessions persist below `PI_SESSION_ROOT` (a temporary application-state
directory by default). Global Pi skills and prompt templates remain available.
Extensions are disabled by default because extension tools run in the sidecar
process; set `PI_ENABLE_EXTENSIONS=true` only for trusted extensions.
