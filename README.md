# ScreenLens

[![Build](https://github.com/th1nk-er/ScreenLens/actions/workflows/build.yml/badge.svg)](https://github.com/th1nk-er/ScreenLens/actions/workflows/build.yml) [![Go](https://img.shields.io/badge/Go-1.26.3%2B-00ADD8?logo=go)](https://go.dev/) [![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

ScreenLens is a local-first screen analysis assistant written in Go. Press a global hotkey, use the system tray, or send `/screen` through Telegram; ScreenLens captures the selected display, sends the image to a configured analyzer, and returns a concise answer.

The default analyzer is a locally installed CLI agent. Codex, Claude Code, and OpenCode are supported without MCP integration or an approval workflow. Hosted vision APIs remain available as an explicit alternative.

## Why ScreenLens

- Local-first by default: use the agent already installed and authenticated on your machine.
- One configuration model: prompts, execution limits, local agents, and hosted APIs live under `analysis`.
- Provider-specific image transport: native CLI arguments are used instead of asking a model to discover a file by guesswork.
- Safe automation boundary: short-lived private image artifacts, read-only defaults, bounded output, cancellation, timeouts, and process-tree cleanup.
- Operational controls: Telegram authorization, `/status`, `/reload`, `/cancel`, rotating logs, and single-instance protection.
- Extensible core: new analyzers implement one provider-neutral interface and do not change the workflow layer.

## Architecture

```text
hotkey / tray / Telegram
            |
            v
      capture workflow
            |
      analysis workflow
       (ordered steps)
       +----+-----------+
       |                |
   step analyzer    step analyzer
       |                |
       +----+-----------+
            v
     Telegram response
```

The main modules are deliberately separated:

| Module | Responsibility |
| --- | --- |
| `internal/screenshot` | Capture, resize, encode, and size-limit screenshots |
| `internal/workflow` | Serialize capture requests, cancellation, delivery, and status |
| `internal/pipeline` | Execute named multi-step analysis workflows in order and pass prior text between steps |
| `internal/analyzer` | Stable analyzer contract and hosted-API adapter |
| `internal/agent` | Local CLI process execution, image staging, retries, sessions, and output parsing |
| `internal/artifact` | Private temporary screenshot files and cleanup |
| `internal/vision` | OpenAI Chat, OpenAI Responses, and Anthropic Messages protocols |
| `internal/telegram` | Telegram transport, commands, and authorization |

## Supported analyzers

| Profile/provider | Default invocation | Image delivery | Notes |
| --- | --- | --- | --- |
| `codex` / Codex CLI | `codex exec --json` | Native `--image <path>` | Read-only sandbox; repository trust check is skipped for temporary artifacts |
| `claude` / Claude Code | `claude -p ... --output-format json` | `@<path>` attachment, with restricted `Read` fallback | MCP is disabled; `Read` is the only allowed tool |
| `opencode` / OpenCode | `opencode --pure run --format json` | Native `--file <path>` | External plugins and MCP are disabled; native attachment plus read-only file access |
| `vision` | Configured HTTP protocol | Base64 image payload | Opt-in hosted API mode |

For local agents, ScreenLens stages the screenshot into a private temporary directory. The artifact is removed after the process finishes, including failure paths. The staged path is also exposed as `SCREENLENS_IMAGE_PATH` for custom wrappers.

The built-in OpenCode adapter sends screenshots through OpenCode's native `--file` attachment and defaults to denying workspace-discovery tools while retaining read access for the attachment. This keeps a visual-recognition step focused on the image instead of searching the working directory for words from the prompt.

The built-in arguments follow each tool's documented non-interactive CLI interface. See the official references for [Codex CLI](https://github.com/openai/codex), [Claude Code CLI](https://code.claude.com/docs/en/cli-usage), [Claude Code image workflows](https://code.claude.com/docs/en/common-workflows), and [OpenCode CLI](https://dev.opencode.ai/docs/cli/).

## Requirements

- Go 1.26.3 or newer for source builds.
- A Telegram bot token and target chat ID.
- Either an installed and authenticated Codex, Claude Code, or OpenCode CLI, or a configured vision API profile.
- Platform dependencies:
  - Windows: a working C compiler for cgo.
  - macOS: Xcode Command Line Tools; grant Screen Recording and Accessibility permissions when requested.
  - Linux: GCC, GTK 3, AppIndicator, X11 development libraries, and an X11 session for global hotkeys.

ScreenLens does not install, authenticate, or update third-party agents. Verify each CLI independently with `codex --version`, `claude --version`, or `opencode --version`.

## Installation

### Option A: Download a prebuilt release

The recommended path for end users is the [latest GitHub Release](https://github.com/th1nk-er/ScreenLens/releases/latest). Choose the archive for your platform:

| Platform | Release asset |
| --- | --- |
| Windows amd64 | `screenlens-windows-amd64.zip` |
| Linux amd64 | `screenlens-linux-amd64.tar.gz` |
| macOS Intel | `screenlens-macos-amd64.tar.gz` |
| macOS Apple Silicon | `screenlens-macos-arm64.tar.gz` |

Each archive contains GUI and console binaries, `config.example.yaml`, this README, and the license files. Extract the archive, then continue with the configuration steps below. Run the binary from the extracted directory or pass an explicit `-config` path.

### Option B: Build from source

```sh
git clone https://github.com/th1nk-er/ScreenLens.git
cd ScreenLens
```

```sh
go build -trimpath -o bin/screenlens ./cmd/screenlens
```

For the Windows GUI build:

```powershell
go build -tags gui -ldflags "-H=windowsgui" -o bin/screenlens.exe ./cmd/screenlens
```

### Configure and start

Windows PowerShell:

```powershell
Copy-Item config.example.yaml config.yaml
$env:TELEGRAM_BOT_TOKEN = "<bot-token>"
$env:TELEGRAM_CHAT_ID = "<chat-id>"
```

macOS/Linux:

```sh
cp config.example.yaml config.yaml
export TELEGRAM_BOT_TOKEN='<bot-token>'
export TELEGRAM_CHAT_ID='<chat-id>'
```

The example uses `analysis.mode: local-agent` and `analysis.profile: auto`. Auto selection checks Codex, Claude Code, then OpenCode. Set `analysis.profile` to a provider name for deterministic selection.

`analysis.prompt` is required for legacy single-step mode. When multi-step workflows are configured, each step supplies its own prompt and the top-level prompt may be omitted.

Start an extracted release binary:

```sh
./screenlens -config config.yaml
```

Start a source build:

```sh
./bin/screenlens -config config.yaml
```

On Windows PowerShell:

```powershell
.\screenlens.exe -config config.yaml
```

For a source build on Windows, use `.\bin\screenlens.exe -config config.yaml`.

When using the source tree directly, `go run ./cmd/screenlens -config config.yaml` is also supported. The Makefile provides platform-aware build and run shortcuts.

## Configuration

The complete reference is [`config.example.yaml`](config.example.yaml). The format is intentionally strict: unknown YAML fields are rejected, and the previous top-level `vision` configuration is not supported by this major version.

```yaml
analysis:
  mode: local-agent
  profile: auto
  prompt: |
    Analyze the screenshot and respond in concise Markdown.
    State uncertainty explicitly.
  timeout: 5m
  max_output_bytes: 2097152
  session: ephemeral
  profiles:
    codex:
      type: local-agent
      local_agent:
        provider: codex
    claude:
      type: local-agent
      local_agent:
        provider: claude
        max_output_tokens: 8192
    opencode:
      type: local-agent
      local_agent:
        provider: opencode
    vision:
      type: vision-api
      vision:
        protocol: openai-chat-completions
        endpoint: https://api.openai.com/v1/chat/completions
        model: your-vision-model
        api_key: ${OPENAI_API_KEY}
```

### Multi-step analysis workflows

For a chain of models, configure `analysis.workflow` and one or more named entries under `analysis.workflows`. Steps run strictly in YAML order, and only the final step is sent as the user-facing answer. A step may reuse an existing `analysis.profiles.<name>` entry:

```yaml
analysis:
  profiles:
    deepseek:
      type: local-agent
      local_agent:
        provider: deepseek
        command: deepseek
  workflow: screen-solution
  workflows:
    screen-solution:
      timeout: 10m
      steps:
        - name: inspect
          profile: codex
          input: screenshot
          prompt: Inspect the screenshot and list factual findings.
        - name: reason
          profile: deepseek
          input: previous
          prompt: Analyze the previous findings and identify likely causes.
        - name: solution
          profile: claude
          input: previous
          prompt: Turn the previous reasoning into a concrete solution.
```

It may also define a one-off profile directly on a step with `profile_config`, without adding it to the shared profile registry:

```yaml
- name: reason
  profile_config:
    type: local-agent
    local_agent:
      provider: deepseek
      command: deepseek
  input: previous
  prompt: Analyze the previous findings.
```

`input` defaults to `both`. Use `screenshot` when a step should independently inspect the image, or `previous` when the model only supports text and should receive the prior step's output. In the latter mode the screenshot is omitted from CLI/API transport. `{previous_output}` can be placed explicitly in a prompt; otherwise ScreenLens appends a clearly marked previous-output section for steps after the first. `analysis.timeout` (or a workflow-specific `timeout`) bounds the complete chain; provider/profile timeouts still bound individual calls.

### Local-agent profiles

`local_agent.command` is an executable name or path, not a shell command. `args` is an argument array, not a shell string. Empty `command` and `args` select the built-in adapter for the profile's `provider`; explicit values override the adapter's command construction. The adapter boundary owns provider-specific image transport, output parsing, safe defaults, and platform compatibility, while the core execution loop remains provider-neutral. These placeholders are expanded per capture:

- `{prompt}` — configured prompt plus staged-image instructions.
- `{image_path}` — private staged image path.
- `{workdir}` — configured working directory, or the temporary artifact directory.
- `{session_id}` — previous provider session ID for persistent sessions.

`local_agent.image_transport` controls how a custom CLI receives the screenshot: `auto` uses the built-in adapter behavior, `path` adds the staged path to the prompt, and `native` suppresses the path fallback when custom `args` already attach `{image_path}` through the provider's native option. Built-in Codex, Claude, and OpenCode adapters keep their native attachment rules in the adapter registry.

When overriding arguments, preserve the provider's image contract:

```yaml
analysis:
  profiles:
    opencode:
      type: local-agent
      local_agent:
        provider: opencode
        args: [--pure, run, --format, json, "{prompt}", --file, "{image_path}"]
```

OpenCode's `--file` is an array option, so the prompt must appear before it. ScreenLens also repairs a missing Codex image value and appends the native image attachment when custom Codex arguments omit it.

When `analysis.profile: auto` is used, `analysis.auto_profiles` controls discovery order. The default is `codex`, `claude`, then `opencode`; configure a different ordered list when the deployment has a preferred local backend.

`max_output_tokens` is primarily useful for Claude Code and Anthropic-compatible gateways. It is exported as `CLAUDE_CODE_MAX_OUTPUT_TOKENS` unless the profile already supplies that variable. This prevents a smaller third-party gateway from receiving an unnecessarily large output request.

### Hosted vision profiles

Set `analysis.mode: vision` and select a `type: vision-api` profile. Supported protocols are `openai-chat-completions`, `openai-responses`, and `anthropic-messages`. Endpoints, models, headers, token fields, proxies, and retry counts are profile-specific, so compatible third-party gateways can be configured without vendor-specific code.

### Secrets and environment variables

YAML values support `${ENV_VAR}` expansion. Keep tokens and API keys in the environment, keep `config.yaml` out of version control, and never paste secrets into issue reports or logs. The repository ignores `config.yaml`, `.env`, binaries, and logs by default.

## Interaction

Region screenshots use the last two recorded mouse positions and do not show a selection overlay.

- `ALT+SHIFT+S` — set the region start point.
- `ALT+SHIFT+E` — set the region end point.
- `ALT+SHIFT+C` — capture the selected region.
- `CTRL+SHIFT+S` — capture using the configured hotkey.
- `MOUSE_X1` / `MOUSE_X2` — optional side-button capture aliases.
- `/screen` — capture with the active profile.
- `/screen codex` — use a profile for one capture.
- `/screen workflow:screen-solution` — run a configured workflow for one capture.
- `/agent` — list configured profiles.
- `/workflow` — list configured workflows and the active workflow.
- `/cancel` — cancel the active capture/analysis.
- `/status` — show runtime and last-request status.
- `/reload` — reload supported capture, analysis, and hotkey settings.
- `/help` — show the command list.

Telegram authorization is fail-closed. Messages must come from the configured chat. In group chats, configure `telegram.allowed_user_ids`; without an explicit allowlist, only a private chat whose sender matches the chat is accepted.

The screenshot is optionally sent to Telegram immediately after capture. The analysis response is then sent as a reply to that photo. Set `telegram.send_image: false` to return only the analysis text.

Telegram credentials, proxy settings, polling, timeouts, and authorization changes require a restart. Other validated runtime settings can be applied with `/reload`.

## Safety and reliability

- No MCP servers are configured or loaded by ScreenLens.
- No interactive approval flow is used. Local calls are non-interactive and constrained to read-oriented behavior.
- Codex runs with a read-only sandbox and ignores user config for deterministic automation.
- Claude Code exposes only `Read`, auto-allows that tool, disables session persistence by default, and uses strict MCP isolation without an MCP config.
- OpenCode runs in pure mode, disables default plugins, supplies an explicit permission policy, and disables MCP through inline configuration.
- Local artifacts use private temporary directories and restrictive file permissions, then are cleaned up.
- Process output is capped; timeouts and cancellation terminate the launched process tree.
- Transient local-agent failures are retried according to the profile; authentication, invalid flags, context-limit, and other permanent failures are not retried.
- Persistent sessions retain a provider session ID only after a successful response. A failed session is cleared before the next attempt.

These controls protect ScreenLens's execution boundary; they do not make a third-party CLI or model trustworthy. Review the installed agent's own configuration and credentials before enabling it.

## Troubleshooting

### `local agent ... is not available`

Install the selected CLI and verify it is on `PATH`, or set `analysis.profiles.<name>.local_agent.command` to its executable path. Use `/agent` and `/status` to confirm the selected profile.

### Codex reports an untrusted directory

ScreenLens uses `--skip-git-repo-check` and stages the image in a temporary directory. If custom `args` remove that flag, restore it or set a suitable `work_dir`.

### Claude says it cannot see the screenshot

ScreenLens passes the image as an `@<image_path>` attachment and also allows the read-only `Read` tool. Check the installed Claude Code version, keep `--allowed-tools Read` in custom arguments, and verify that the third-party gateway preserves image content. For a constrained gateway, lower `local_agent.max_output_tokens` and shorten `analysis.prompt`.

### Claude returns a context-length 400 error

The gateway may receive a large input plus the CLI's output budget. Set `max_output_tokens` to a value the gateway accepts, reduce the prompt, and keep screenshots within `capture.max_bytes`. ScreenLens does not assume that a third-party Anthropic-compatible service has Anthropic's context limits.

### OpenCode says the file is missing or does not analyze the image

Use native `--file <path>` and put `{prompt}` before `--file`. Do not put the screenshot path inside the prompt as the only image transport. Check that the installed OpenCode version supports the `run --format json --file` combination.

### Telegram does not respond

Check the bot token, chat ID, network/proxy settings, and `telegram.allowed_user_ids`. Run the console build and inspect `screenlens.log`; do not include the token when sharing diagnostics.

## Development

```sh
make check       # format, tidy, test, vet
make race        # race detector
make build       # platform-default binary
make build-gui
make build-console
```

Without Make on Windows:

```powershell
gofmt -w cmd internal
go mod tidy
go test ./...
go test -race ./...
go vet ./...
go build -o bin/screenlens-console.exe ./cmd/screenlens
go build -tags gui -ldflags "-H=windowsgui" -o bin/screenlens.exe ./cmd/screenlens
```

Linux CI runs native tests under Xvfb when no display is available. Pull requests build console and GUI variants on Ubuntu, macOS, and Windows.

When adding an analyzer, implement `analyzer.Analyzer`, keep provider-specific process or HTTP details in its package, add parser/transport and failure-mode tests, and avoid coupling the workflow to a vendor name.

## Releases

Download prebuilt packages from the [latest GitHub Release](https://github.com/th1nk-er/ScreenLens/releases/latest). Releases are built by GitHub Actions from semantic version tags:

```sh
git tag -a vX.Y.Z -m "Release vX.Y.Z"
git push origin vX.Y.Z
```

Packages are produced for Linux amd64, macOS amd64, macOS arm64, and Windows amd64. Each archive includes console and GUI binaries, `config.example.yaml`, documentation, license files, and `SHA256SUMS`.

## Contributing

Bug reports and pull requests are welcome. Include the operating system, ScreenLens version, third-party CLI versions, reproduction steps, and redacted logs. Do not attach screenshots, bot tokens, API keys, or private prompts unless sanitized.

Before opening a pull request, run `make check` and `make race`. Changes that affect a local-agent command should include tests for argument order, image delivery, process diagnostics, and cleanup.

## License

ScreenLens is released under the [Apache License 2.0](LICENSE). Third-party attribution details are available in [NOTICE](NOTICE).
