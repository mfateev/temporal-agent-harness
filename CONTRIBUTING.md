# Contributing

## Structure

This project ports [codex-rs](https://github.com/openai/codex/tree/main/codex-rs) to Go + Temporal. Maintain structural alignment with the original Rust codebase.

When adding features:
1. Locate the corresponding Codex Rust source
2. Use the same type/function names (adapted to Go conventions)
3. Document the mapping with `// Maps to: codex-rs/...` comments
4. Port all associated tests from the Rust test suite

## Development

```bash
temporal server start-dev              # Terminal 1
export OPENAI_API_KEY=sk-...
go run cmd/worker/main.go              # Terminal 2
go run cmd/tcx/main.go                 # Terminal 3
```

## Testing

All changes must pass before pushing:

```bash
go build ./...
go vet ./...
go test -short ./...
```

E2E tests require Temporal + OpenAI:
```bash
go test -v ./e2e/...
```

## Guidelines

- **Tests required** for all new code. Port Rust tests when porting features.
- **No untested commits**. If services are unavailable, note it — don't push.
- **Minimal changes**. Fix what's asked, don't refactor surroundings.
- **Document divergences**. If Go/Temporal idioms require a different approach, add a comment.
- **Don't commit secrets**. No API keys, tokens, or credentials in code.

## Packages

| Package | What goes here |
|---------|---------------|
| `internal/workflow/` | Temporal workflow logic (agentic loop, state, handlers) |
| `internal/activities/` | Temporal activities (LLM calls, tool dispatch, instruction loading) |
| `internal/tools/handlers/` | Tool implementations (shell, file ops, grep) |
| `internal/tools/` | Tool registry, specs, types |
| `internal/execpolicy/` | Starlark exec policy engine |
| `internal/sandbox/` | OS sandbox (macOS Seatbelt, Linux bwrap) |
| `internal/command_safety/` | Shell command classification |
| `internal/execenv/` | Environment variable filtering |
| `internal/cli/` | Interactive REPL (binary: `tcx`) |
| `internal/instructions/` | AGENTS.md discovery, prompt construction |
| `internal/models/` | Shared types (config, conversation, errors) |
| `internal/history/` | Conversation history management |
| `internal/llm/` | LLM client (OpenAI) |

## Adding a tool

1. Create handler in `internal/tools/handlers/` implementing `tools.ToolHandler`
2. Add spec in `internal/tools/spec.go` with `NewXxxToolSpec()`
3. Add `EnableXxx` to `models.ToolsConfig`
4. Wire in `workflow/agentic.go` `buildToolSpecs()` and `cmd/worker/main.go`
5. Add unit tests + E2E test
