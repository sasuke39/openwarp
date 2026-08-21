# DeepSeek Harness Runtime Bridge

This package adapts DeepSeek Harness to the versioned OpenWarp Agent Runtime
protocol. DSH owns its loop, sessions, retries, compaction, skills, jobs, todos,
and subagents. Workspace-facing tools are proxy definitions that execute in the
active Warp terminal through the parent Go adapter.

DSH's high-level tools stay enabled: skills, goals, todo, jobs, fresh and forked
subagents, subagent control/reporting, workflows, and Ralph loops. Its bash and
filesystem implementations are replaced by same-name proxy tools so they act
on the active Warp terminal (including managed SSH), not the adapter's Mac.

```bash
npm install
npm run build
```

DSH requires Node `^22.19` or `>=24`; Node 23 is intentionally unsupported by
upstream. Point `command` at a compatible Node binary when several are installed.

Configure the adapter with:

```yaml
agent_runtime:
  driver: deepseek-harness
  command: node
  args:
    - /absolute/path/to/integrations/deepseek-harness/dist/main.js
```

The sidecar receives provider configuration from `DEEPSEEK_API_KEY`,
`DEEPSEEK_BASE_URL`, `DSH_MODEL`, and `DSH_MAX_TOKENS`. The wire protocol is
transport-neutral NDJSON; another framework can implement the same envelopes
without importing DSH packages.
