# codex-temporal-go

A Go port of [OpenAI Codex](https://github.com/openai/codex) built on [Temporal](https://temporal.io) for durable agentic execution. The CLI is called `tcx`.

## What it does

An LLM-driven coding agent that runs shell commands, reads/writes files, and searches codebases — with durable execution that survives crashes, restarts, and network failures.

- **Durable agentic loop** on Temporal (LLM call -> tool execution -> repeat)
- **6 built-in tools**: shell, read_file, write_file, apply_patch, list_dir, grep_files
- **Parallel tool execution** via Temporal futures
- **Interactive REPL** (`tcx`) with markdown rendering, approval prompts, session resume
- **Shell security**: exec policy engine, command safety classification, OS sandbox (macOS Seatbelt / Linux bubblewrap), environment variable filtering
- **3 approval modes**: `unless-trusted`, `never`, `on-failure`
- **Temporal Cloud support** via envconfig (env vars, config files, TLS)

## Quick start

```bash
# 1. Start Temporal (terminal 1)
temporal server start-dev

# 2. Start worker (terminal 2)
export OPENAI_API_KEY=sk-...
go run cmd/worker/main.go

# 3. Run tcx (terminal 3)
go run cmd/tcx/main.go -m "List files in the current directory"
```

Or start an interactive session:
```bash
go run cmd/tcx/main.go
```

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
  --model string              LLM model (default: gpt-4o-mini)
  --approval-mode string      unless-trusted | never | on-failure
  --full-auto                 Alias for --approval-mode never
  --sandbox string            full-access | read-only | workspace-write
  --temporal-host string      Override Temporal server address
  --codex-home string         Config directory (default: ~/.codex)
  --no-markdown               Disable markdown rendering
  --no-color                  Disable colored output
```

## Testing

```bash
go test -short ./...                    # Unit tests (no services needed)
go test -v ./e2e/...                    # E2E tests (requires Temporal + OpenAI)
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
