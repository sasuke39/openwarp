<p align="right"><a href="./README_CN.md">简体中文</a></p>
<p align="center"><img src="./docs/public/logo.svg" width="88" alt="open-warp logo"></p>
<h1 align="center">open-warp</h1>
<p align="center">Bring your own model and Agent harness to a local-first Warp experience.</p>
<p align="center">
  <a href="https://github.com/sasuke39/open-warp/releases/latest"><img alt="Latest release" src="https://img.shields.io/github/v/release/sasuke39/open-warp"></a>
  <img alt="macOS Apple Silicon" src="https://img.shields.io/badge/macOS-Apple%20Silicon-111111?logo=apple">
  <a href="./LICENSE"><img alt="MIT license" src="https://img.shields.io/badge/license-MIT-2ea44f"></a>
</p>

`open-warp` connects a patched Warp client to a local Go adapter and any OpenAI-compatible endpoint. The official Warp app can remain installed; `WarpLocal.app` uses separate local configuration and runtime state.

<table>
  <tr><td><img src="./docs/agent-unified-input/prototypes/warp-complete-agent.png" alt="Agent and terminal timeline"></td><td><img src="./docs/agent-unified-input/prototypes/warp-complete-terminal.png" alt="Terminal input mode"></td></tr>
  <tr><td align="center">Agent and terminal events in one timeline</td><td align="center">Terminal commands stay available during an Agent Turn</td></tr>
</table>

<p align="center"><sub>Sanitized UI preview — all hosts, users, paths, and model names are fictional.</sub></p>

## Core features

- **Bring your own backend** — OpenAI, DeepSeek, Ollama, OpenRouter, LM Studio, vLLM, or another OpenAI-compatible API.
- **Switchable Agent harnesses** — Native, Pi Agent, and DeepSeek Harness profiles with independently selected models.
- **Local and Managed SSH tools** — workspace-aware file search, reads, diffs, shell execution, and remote terminal context.
- **Managed command lifecycle** — explicit foreground/background execution, `command_id`, output polling, input, cancellation, exit status, and executor-measured duration.
- **Responsive Turns** — queue a follow-up or steer the active Turn without opening a second response stream.
- **Native configuration** — provider, model, context size, harness, and profile settings live inside WarpLocal.

## Install

Download **[WarpLocal.app.zip](https://github.com/sasuke39/open-warp/releases/latest/download/WarpLocal.app.zip)** from the latest release, unzip it, and move `WarpLocal.app` to `/Applications`.

```bash
xattr -cr /Applications/WarpLocal.app
open /Applications/WarpLocal.app
```

Current prebuilt releases target **Apple Silicon (arm64)** and use an ad-hoc development signature. A complete Windows installer is not available yet.

## Configure and use

1. Open **Settings → Local Adapter**.
2. Add an endpoint, API key, model, and context size; then choose an Agent harness.
3. Open a local or SSH terminal and submit an Agent instruction.
4. Switch the same input area back to terminal mode whenever you need direct shell control.

Configuration and runtime state stay under local WarpLocal Application Support. Prompts, tool context, and API credentials are sent only to the provider endpoint you configure. Diagnostics redact keys, tokens, email addresses, and home paths, but should still be reviewed before sharing.

## Project status

The core Agent loop and managed shell lifecycle are usable. MCP, subagents, computer use, passive suggestions, and full Warp cloud parity are not yet implemented. This is an independent community project and is not affiliated with Warp.

## Documentation

[Getting started](https://sasuke39.github.io/open-warp/guide/getting-started) · [Configuration](https://sasuke39.github.io/open-warp/guide/configuration) · [Supported tools](https://sasuke39.github.io/open-warp/guide/supported-tools) · [Troubleshooting](https://sasuke39.github.io/open-warp/guide/troubleshooting) · [Build guide](./WARP_CLIENT.md)

## Development

```bash
go test ./...
WARP_SRC=/path/to/warp-source sh ./build_and_bundle.sh
```

MIT licensed. See [LICENSE](./LICENSE).
