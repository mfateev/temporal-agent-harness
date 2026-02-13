# codex-temporal-go

A Go port of [OpenAI Codex](https://github.com/openai/codex) built on [Temporal](https://temporal.io) for durable agentic execution. The CLI is called `tcx`.

## What it does

An LLM-driven coding agent that runs shell commands, reads/writes files, and searches codebases — with durable execution that survives crashes, restarts, and network failures.

- **Durable agentic loop** on Temporal (LLM call -> tool execution -> repeat)
- **Multi-provider LLM support**: OpenAI (GPT-4, GPT-4o) and Anthropic (Claude Opus, Sonnet, Haiku)
- **6 built-in tools**: shell, read_file, write_file, apply_patch, list_dir, grep_files
- **Parallel tool execution** via Temporal futures
- **Interactive REPL** (`tcx`) with markdown rendering, approval prompts, session resume
- **Shell security**: exec policy engine, command safety classification, OS sandbox (macOS Seatbelt / Linux bubblewrap), environment variable filtering
- **3 approval modes**: `unless-trusted`, `never`, `on-failure`
- **Temporal Cloud support** via envconfig (env vars, config files, TLS)

## Install

```bash
go install github.com/mfateev/codex-temporal-go/cmd/tcx@latest
go install github.com/mfateev/codex-temporal-go/cmd/worker@latest
```

Or build from source:
```bash
git clone https://github.com/mfateev/codex-temporal-go.git
cd codex-temporal-go
go build -o tcx ./cmd/tcx
go build -o worker ./cmd/worker
```

## Quick start

```bash
# 1. Start Temporal (terminal 1)
temporal server start-dev

# 2. Start worker (terminal 2)
# OpenAI:
export OPENAI_API_KEY=sk-...
# Or Anthropic:
export ANTHROPIC_API_KEY=sk-ant-...
# Or both (worker supports both providers)
./worker

# 3. Run tcx (terminal 3)
# OpenAI (default):
./tcx -m "List files in the current directory"
# Or Anthropic:
./tcx --provider anthropic --model claude-sonnet-4.5-20250929 -m "List files"
```

Or start an interactive session:
```bash
./tcx
```

## Interactive Mode

The TUI supports multi-line input for composing longer messages:

- **Enter** - Submit message
- **Shift+Enter** - Insert new line
- **Ctrl+C** - Interrupt (twice to disconnect)
- **Ctrl+D** - Disconnect
- **↑/↓, PgUp/PgDn** - Scroll viewport
- **/exit, /quit** - Exit session
- **/end** - End session gracefully
- **/model** - Switch model for the current session

The input area automatically expands up to 10 lines as you type.

## Connection

Temporal connection is configured via [envconfig](https://github.com/temporalio/samples-go/tree/main/external-env-conf):

```bash
# Local (default)
temporal server start-dev

# Temporal Cloud
export TEMPORAL_HOST_URL=your-namespace.tmprl.cloud:7233
export TEMPORAL_NAMESPACE=your-namespace
export TEMPORAL_TLS_CERT=/path/to/cert.pem
export TEMPORAL_TLS_KEY=/path/to/key.pem
```

Or use `--temporal-host` flag to override.

## CLI flags

```
tcx [flags]

  -m, --message string       Initial message
  --session string            Resume existing session
  --provider string           LLM provider: openai (default) | anthropic
  --model string              LLM model (default: gpt-4o-mini for OpenAI, claude-sonnet-4.5-20250929 for Anthropic)
  --approval-mode string      unless-trusted | never | on-failure
  --full-auto                 Alias for --approval-mode never
  --sandbox string            full-access | read-only | workspace-write
  --temporal-host string      Override Temporal server address
  --codex-home string         Config directory (default: ~/.codex)
  --no-markdown               Disable markdown rendering
  --no-color                  Disable colored output
```

### Supported Models

**OpenAI:**
- `gpt-4o` - GPT-4 Optimized
- `gpt-4o-mini` - Smaller, faster GPT-4 (default)
- `gpt-4-turbo` - GPT-4 Turbo
- `gpt-3.5-turbo` - GPT-3.5 Turbo

**Anthropic:**
- `claude-opus-4-6` - Claude Opus 4.6 (most capable, 200K context)
- `claude-opus-4-5` - Claude Opus 4.5
- `claude-sonnet-4.5-20250929` - Claude Sonnet 4.5 (balanced, default for Anthropic)
- `claude-sonnet-4-0` - Claude Sonnet 4.0
- `claude-3-7-sonnet-20250219` - Claude 3.7 Sonnet
- `claude-haiku-4.5-20251001` - Claude Haiku 4.5 (fastest, cheapest)
- `claude-3-5-haiku-20241022` - Claude 3.5 Haiku
- `claude-3-opus-20240229` - Claude 3 Opus (legacy)
- `claude-3-haiku-20240307` - Claude 3 Haiku (legacy)

## Testing

```bash
go test -short ./...                    # Unit tests (no services needed)
go test -v ./e2e/...                    # E2E tests (requires Temporal + OpenAI/Anthropic)
go test -race -short ./...              # Race detector
```

## Architecture

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

Ported from [codex-rs](https://github.com/openai/codex/tree/main/codex-rs) with structural alignment to the original Rust codebase. Key packages:

| Package | Maps to | Purpose |
|---------|---------|---------|
| `internal/workflow/` | `codex.rs` | Agentic loop, state, handlers |
| `internal/tools/handlers/` | `tools/handlers/` | Built-in tool implementations |
| `internal/execpolicy/` | `execpolicy/` | Starlark exec policy engine |
| `internal/sandbox/` | `sandboxing/` | OS-native sandboxing |
| `internal/command_safety/` | `command_safety/` | Shell command classification |
| `internal/cli/` | `cli/` | Interactive REPL (`tcx`) |

## License

MIT. See [LICENSE](LICENSE).

Derived from [OpenAI Codex](https://github.com/openai/codex) (Apache 2.0, Copyright 2025 OpenAI).
