# ScreenLens

ScreenLens is a local, headless Go screen assistant. A global shortcut, a Telegram command, or an optional system-tray action captures the current screen, sends it to a configured vision model, and delivers the analysis back to Telegram.

## Features

- Headless operation with configuration-file driven runtime behavior.
- GUI and console builds with separate runtime behavior.
- Keyboard shortcuts such as `CTRL+SHIFT+S`.
- Mouse side buttons such as `MOUSE_X1` and `MOUSE_X2`.
- Immediate Telegram screenshot delivery followed by a reply to the screenshot message.
- Telegram RichMessage output with Markdown or HTML formatting.
- Telegram commands: `/screen`, `/reload`, `/status`, and `/help`.
- Protocol-oriented vision adapters:
  - OpenAI Chat Completions
  - OpenAI Responses
  - Anthropic Messages
- OpenAI-compatible third-party providers through configurable endpoints, models, headers, and token fields.
- Independent SOCKS5 or SOCKS5H proxies for vision and Telegram traffic.
- JPEG or PNG screenshots with display selection, downscaling, quality control, and size limits.
- Optional system-tray integration.
- Single-instance protection on Windows, macOS, and Linux.

## Requirements

- Go 1.26 or newer.
- A Telegram bot token and an authorized Telegram chat.
- Access to a vision-capable model that implements one of the supported protocols.

Platform build requirements:

- Windows: a C compiler configured for cgo.
- macOS: Xcode Command Line Tools and a C compiler. Screen recording and Accessibility permissions are required at runtime.
- Linux: GCC, GTK 3, AppIndicator, and X11 development libraries. Global hotkeys currently use X11, so an X11 session is the reliable Linux runtime.

## Quick start

1. Copy the example configuration.

   Windows PowerShell:

   ```powershell
   Copy-Item config.example.yaml config.yaml
   ```

   macOS/Linux:

   ```sh
   cp config.example.yaml config.yaml
   ```

2. Set the required environment variables.

   ```powershell
   $env:OPENAI_API_KEY = "your-api-key"
   $env:TELEGRAM_BOT_TOKEN = "your-bot-token"
   $env:TELEGRAM_CHAT_ID = "your-chat-id"
   ```

   macOS/Linux:

   ```sh
   export OPENAI_API_KEY="your-api-key"
   export TELEGRAM_BOT_TOKEN="your-bot-token"
   export TELEGRAM_CHAT_ID="your-chat-id"
   ```

3. Start ScreenLens:

   ```powershell
   go run ./cmd/screenlens -config config.yaml
   ```

To build a binary:

```powershell
go build -o bin/screenlens.exe ./cmd/screenlens
```

## Vision protocols

### OpenAI Chat Completions

```yaml
vision:
  protocol: openai-chat-completions
  endpoint: https://api.openai.com/v1/chat/completions
  model: gpt-5.6-sol
  api_key: ${OPENAI_API_KEY}
```

The current default output limit field is `max_completion_tokens`. For a legacy OpenAI-compatible provider that only accepts `max_tokens`, set:

```yaml
vision:
  max_tokens_field: max_tokens
```

### OpenAI Responses

```yaml
vision:
  protocol: openai-responses
  endpoint: https://api.openai.com/v1/responses
  model: gpt-5.6-sol
  api_key: ${OPENAI_API_KEY}
```

Responses uses `max_output_tokens` by default.

### Anthropic Messages

```yaml
vision:
  protocol: anthropic-messages
  endpoint: https://api.anthropic.com/v1/messages
  model: claude-sonnet-4-6
  api_key: ${ANTHROPIC_API_KEY}
```

Anthropic uses `x-api-key`, `anthropic-version`, and `max_tokens` by default.

## Configuration

See [config.example.yaml](config.example.yaml) for the complete configuration reference.

Important settings include:

- `hotkey.capture`: a keyboard combination or a mouse side-button alias.
- `app.log_file`: an optional log path. Empty uses `screenlens.log` next to the executable.
- `capture.monitor`: `primary` or a zero-based display index.
- `capture.format`: `jpeg` or `png`.
- `capture.max_width`, `capture.max_height`, and `capture.max_bytes`: screenshot limits.
- `vision.protocol`, `vision.endpoint`, and `vision.model`: the model protocol and target.
- `vision.headers`: additional provider-specific HTTP headers.
- `vision.proxy` and `telegram.proxy`: independent network proxies.
- `telegram.allowed_user_ids`: required when the configured Telegram chat is a group.

Environment variables are expanded in the YAML configuration. Keep `config.yaml` and secret files out of version control.

Logs are written next to the executable by default and rotate after reaching 10 MB. The default policy keeps five backups for up to 30 days. Console builds also mirror logs to stderr for interactive troubleshooting; the log file remains the durable record.

## Telegram commands

- `/screen` captures and analyzes the current screen.
- `/reload` reloads capture, vision, and hotkey settings.
- `/status` reports the current runtime state.
- `/help` displays the command list.

Telegram connection, proxy, polling, timeout, and authorization changes require a process restart. Other supported runtime settings can be reloaded with `/reload`.

## Development

Dependencies are managed with the Go module tooling. Common commands:

```powershell
make check
make race
make build
make build-console
make run CONFIG=config.yaml
```

On Windows, `make build` produces the GUI build without a console window. On macOS and Linux, `make build` produces the native console build. `make build-gui` selects GUI/tray behavior on every platform; Windows additionally uses the Windows GUI subsystem flag. `make build-console` produces a console build, where tray visibility follows `tray.enabled` in the configuration. `make run` runs in console mode.

Linux builds currently target X11 for global keyboard and mouse hooks. Wayland screenshot support depends on the desktop environment, but global hotkey capture under pure Wayland is not supported by the current hook backend.

The project uses a protocol adapter boundary so additional compatible providers can be added without changing the workflow layer.

## License

ScreenLens is licensed under the [Apache License 2.0](LICENSE). The project attribution notice is in [NOTICE](NOTICE).
