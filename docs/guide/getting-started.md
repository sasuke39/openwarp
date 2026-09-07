# Getting Started

`open-warp` lets you use your own OpenAI-compatible LLM provider inside Warp through the `OpenWarp.app` bundle.

## Install from release

Download the latest `OpenWarp.app.zip` from:

https://github.com/sasuke39/openwarp/releases

Unzip it, move `OpenWarp.app` to `/Applications`, then open it.

If macOS says the app is damaged, clear the quarantine attribute:

```bash
xattr -cr /Applications/OpenWarp.app
```

The install script also handles this automatically:

```bash
sh ./install.sh
```

## Configure your provider

Open `OpenWarp.app`, then choose **Settings → Agent Engine**.

Fill in the fields shown in the settings window:

- provider name
- base URL
- API key
- model name

Then click save. The adapter reloads the configuration without a full app restart.

The local HTTP settings URL is still available for debugging, but normal users do not need to open it manually.

## Start using AI in WarpLocal

Open `OpenWarp.app`, press `Cmd+K`, and ask a natural-language question.

Examples:

```text
Explain the current directory.
Find the server entry point.
Create a simple Go HTTP handler and run the tests.
```

Chinese, Japanese, and Korean input is detected as natural language and should not fall through to the shell.
