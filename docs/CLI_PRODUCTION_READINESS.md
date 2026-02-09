# CLI Production Readiness

Status: **Not production-ready**
Last reviewed: 2026-02-09
Compared against: Codex Rust CLI/TUI (`codex-rs/cli/`, `codex-rs/tui/`)

This document tracks what must be fixed, added, and improved before the
interactive CLI (`cmd/cli/`) can be considered production-ready.

---

## Phase A: Critical Bug Fixes

These cause crashes, hangs, or data loss. Must fix before any user testing.

### A1. Spinner race condition

**File:** `internal/cli/spinner.go`

`Stop()` closes `stopCh` and `Start()` creates a new one, but the old
`run()` goroutine may still be reading from the closed channel. Rapid
Start/Stop cycles (e.g., during interrupt handling when items render
between polls) will eventually panic.

**Fix:** Use a single long-lived `done` channel per spinner lifetime, or
switch to `context.Context` cancellation. Add a `sync.WaitGroup` so
`Stop()` blocks until the goroutine exits before `Start()` launches a new
one.

**Test:** `go test -race` with rapid Start/Stop/Start cycles.

---

### A2. Input goroutine leak

**File:** `internal/cli/app.go` — `startInput()`

`startInput()` launches a goroutine that blocks on `rl.Readline()`. If
called again before the previous goroutine returns (e.g., on error
recovery), the old goroutine is leaked — it blocks forever waiting for
terminal input that will never come. Over time this accumulates dangling
goroutines.

**Fix:** Track the current input goroutine. Before starting a new one,
cancel the old one (readline supports `rl.Close()` to unblock) or wait
for it via `inputDone`. Never have two concurrent `readInput()` calls.

---

### A3. Signal channel blocks

**File:** `internal/cli/app.go` — `readInput()`

When readline catches Ctrl+C, it returns `readline.ErrInterrupt`. The
handler sends to `a.sigCh` which has buffer size 1. If a signal is
already pending in the channel (e.g., OS-level SIGINT arrived
simultaneously), this send blocks forever, freezing the CLI.

**Fix:** Use a non-blocking send:
```go
select {
case a.sigCh <- syscall.SIGINT:
default: // signal already pending
}
```

---

### A4. Workflow completion detection is too broad

**File:** `internal/cli/app.go` — `isWorkflowCompleted()`

Current implementation:
```go
return strings.Contains(errStr, "workflow execution already completed") ||
    strings.Contains(errStr, "not found")
```

The string `"not found"` matches transient DNS errors, Temporal namespace
issues, and other unrelated failures. The CLI exits prematurely thinking
the workflow is done.

**Fix:** Use typed error checking from the Temporal SDK:
```go
var notFoundErr *serviceerror.NotFound
if errors.As(err, &notFoundErr) { ... }
```
Also check for `serviceerror.WorkflowNotReady` (during ContinueAsNew)
and retry instead of failing.

---

### A5. ContinueAsNew breaks Seq tracking

**File:** `internal/cli/app.go` — `lastRenderedSeq`

When the workflow triggers ContinueAsNew, it restarts with a fresh
execution. The history is preserved in the new execution via
`HistoryItems`, but `Seq` numbers are re-assigned starting from 0 in
`initHistory()`. The CLI's `lastRenderedSeq` still holds the old value,
so all items in the new execution are skipped (their Seq < lastRenderedSeq).

**Fix:** Detect ContinueAsNew (query returns fewer items than expected, or
a transient "workflow not ready" error). On detection, reset
`lastRenderedSeq = -1` and re-render from the latest turn boundary to
avoid duplicates. Alternatively, make `Seq` globally monotonic across
ContinueAsNew by persisting the counter in `SessionState`.

---

## Phase B: Core Missing Features

These are required for the CLI to be usable by real users.

### B1. Tool approval flow

**Priority:** High (security requirement)
**Codex equivalent:** `codex-rs/tui/src/bottom_pane/approval_overlay.rs`

Currently all tool calls are auto-approved. For production use, dangerous
commands (identified by `command_safety`) must prompt the user before
execution. This requires:

1. A new Temporal signal/update for approval requests from the workflow
2. CLI renders the pending command and waits for user input (y/n/edit)
3. Approval response sent back to the workflow via signal/update
4. Configurable approval policy: `--full-auto`, `--suggest`, `--auto-edit`

This is the single biggest gap between our CLI and Codex. Without it,
the CLI is unsafe for any environment where the LLM might run destructive
commands.

**Scope:** Workflow changes + CLI changes. Estimated: large.

---

### B2. Exec mode (non-interactive)

**Priority:** High
**Codex equivalent:** `codex exec [--json] [--quiet] "prompt"`

One-shot mode: start workflow, wait for completion, print result, exit.
Essential for scripting, CI/CD, and programmatic use.

```
cli exec -m "List all TODO comments" --json
```

**Implementation:**
- Skip readline setup entirely
- Start workflow, poll until first turn completes
- Send shutdown, wait for result
- Print result as JSON (`--json`) or plain text
- Exit with appropriate code (0 = success, 1 = error)

**Scope:** Small — mostly flag handling and a simplified main loop.

---

### B3. Markdown rendering

**Priority:** Medium
**Codex equivalent:** `codex-rs/tui/src/markdown_render.rs` (pulldown-cmark)

Assistant messages are currently plain text. Code blocks, headings, lists,
and links should be rendered with appropriate formatting. The plan already
identified `github.com/charmbracelet/glamour` as the Go library.

**Implementation:**
- Add glamour dependency
- In `renderAssistantMessage()`, pass content through glamour
- Respect `--no-markdown` flag for plain text fallback
- Handle glamour errors gracefully (fall back to plain text)

**Scope:** Small.

---

### B4. TTY detection

**Priority:** Medium

The spinner writes ANSI escape codes (`\r\033[K`) and braille characters
unconditionally. When output is piped to a file or a dumb terminal, this
produces garbage.

**Implementation:**
- Check `os.IsTerminal(fd)` for stdout and stderr
- If not a TTY: disable spinner, disable colors, use plain text markers
- Respect `NO_COLOR` environment variable (standard convention)
- Respect `TERM=dumb`

**Scope:** Small.

---

### B5. Debug logging

**Priority:** Medium

There is no way to debug CLI issues. Add a `--debug` flag or
`CODEX_DEBUG=1` environment variable that enables verbose logging to
stderr (or a file).

**Log points:**
- Temporal connection established/failed
- Workflow started/resumed (with ID)
- Each poll cycle: items received, status, errors
- State machine transitions
- Update sent/received (user_input, interrupt, shutdown)
- Spinner start/stop events

**Implementation:**
- Use `log/slog` with a conditional handler
- Debug logs go to stderr (or `--log-file` path)
- Normal operation: no log output

**Scope:** Small.

---

### B6. Resume retry with backoff

**Priority:** Medium

`resumeWorkflow()` makes a single poll attempt. If the Temporal server is
momentarily slow or there is a transient network issue, resume fails
permanently.

**Implementation:**
- Retry up to 3 times with exponential backoff (1s, 2s, 4s)
- Show "Connecting..." message during retries
- After all retries exhausted, show error with suggestion

**Scope:** Small.

---

### B7. Per-query timeout

**Priority:** Medium

`Poll()` uses the caller's context which may have no deadline. If the
Temporal server hangs, the CLI freezes.

**Implementation:**
- Wrap each query call with a 5-second timeout context
- On timeout, return error (poller will retry on next tick)

**Scope:** Small.

---

## Phase C: UX Polish

Nice-to-have improvements that make the CLI pleasant to use.

### C1. Help and slash commands

Add discoverable in-session commands:

| Command | Action |
|---------|--------|
| `/help` | Show available commands |
| `/exit`, `/quit` | Shutdown and exit (already implemented) |
| `/status` | Show workflow state, tokens, turn count |
| `/id` | Print workflow ID (for resume) |
| `/history` | Print full conversation history |

---

### C2. Multi-line input

**Codex equivalent:** Shift+Enter for newlines, Ctrl+E to open `$EDITOR`

Current readline only supports single-line input. For code snippets or
long prompts, users need multi-line support.

**Options:**
1. Use `chzyer/readline` multi-line mode (limited)
2. Switch to `charmbracelet/bubbletea` textarea component
3. Support Ctrl+E to open `$EDITOR` (simpler, high value)

---

### C3. Configuration file

**Codex equivalent:** `~/.codex/config.toml` with project overrides

Support a config file so users don't have to pass flags every time:

```toml
# ~/.codex-temporal/config.toml
model = "gpt-4o"
temporal_host = "localhost:7233"
enable_shell = true
enable_read_file = true
no_color = false
```

**Load order:** defaults < config file < environment variables < CLI flags.

---

### C4. Session listing

**Codex equivalent:** `codex resume` with paginated picker

Add a `--list` flag or `list` subcommand that queries Temporal for
running workflows and displays them:

```
$ cli --list
  codex-433997ea  2m ago   "Remember: the secret word..."
  codex-b5713da7  5m ago   "What is 2+2?"
  codex-ed0f438b  8m ago   (completed)
```

Uses `client.ListWorkflow()` with a filter on task queue.

---

### C5. NO_COLOR and terminal fallbacks

- Respect the `NO_COLOR` environment variable (https://no-color.org/)
- Detect `TERM=dumb` and disable spinner + colors automatically
- Provide ASCII spinner fallback (`|`, `/`, `-`, `\`) for terminals
  that don't support braille characters

---

### C6. Configurable output truncation

The 20-line tool output limit and 200-character argument limit are
hard-coded. Make them configurable via flags or config:

```
--max-output-lines 50
--max-arg-chars 500
```

Or add a `/expand` command to show full output of the last tool call.

---

### C7. Show workflow ID prominently

Currently the workflow ID is printed once at startup to stderr. For
resume, users need to copy it. Add:

- Print workflow ID after each turn in the status line
- Add `/id` command
- Print resume command on exit: `Resume with: cli --workflow-id codex-xxx`

---

## Phase D: Advanced Features

Features from the Codex CLI that require significant implementation work.

### D1. Backtrack (undo turns)

**Codex equivalent:** Ctrl+Z

Undo the last agent turn and return to the previous state. Requires:

1. A new `undo` update handler in the workflow
2. History rollback (remove items since last TurnStarted)
3. CLI tracks undo-able boundaries

**Scope:** Large — workflow + CLI changes.

---

### D2. Transcript pager

**Codex equivalent:** Ctrl+T

Full-screen scrollable view of the conversation history. Could use
`charmbracelet/bubbletea` viewport or a simpler `less`-pipe approach.

**Scope:** Medium.

---

### D3. Image/file input

**Codex equivalent:** `codex -i image.png "explain this"`

Support attaching images or files to messages. Requires:

1. CLI flag: `-i file.png`
2. Base64 encoding and inclusion in the user message
3. Workflow/LLM activity support for multi-modal messages

**Scope:** Large — touches all layers.

---

### D4. MCP server integration

**Codex equivalent:** `codex mcp server add/list/remove`

Support Model Context Protocol servers for extensible tool availability.
The tool registry would dynamically include MCP-provided tools.

**Scope:** Large — new subsystem.

---

## Test Requirements

Before declaring production-ready, these test gaps must be filled:

### T1. Race detector coverage

Run all CLI code under `go test -race`. The spinner, signal handling,
and input goroutine issues (A1-A3) must not trigger data race warnings.

### T2. State machine transition tests

Unit tests for the App state machine:
- STARTUP -> INPUT (no message)
- STARTUP -> WATCHING (with message)
- INPUT -> WATCHING (user types message)
- WATCHING -> INPUT (turn completes)
- WATCHING -> WATCHING (interrupt, wait for turn_complete)
- INPUT -> SHUTDOWN (Ctrl+C)
- WATCHING -> SHUTDOWN (double Ctrl+C)
- Any state -> error recovery

### T3. Error injection tests

- Nil `Output` field on FunctionCallOutput items
- Empty content on AssistantMessage
- Malformed JSON in Arguments
- Very large output (>1MB)
- Unicode/emoji in all text fields
- Temporal query returning 0 items
- Temporal query error during ContinueAsNew

### T4. Integration tests

Test the full poll -> render -> input cycle with a mock Temporal client:
- Start workflow, see spinner, see response, type follow-up
- Resume workflow, see history replay
- Interrupt during WATCHING, see "Interrupting..."
- Workflow completes during WATCHING, CLI exits

---

## Summary

| Phase | Items | Effort | Blocks Production? |
|-------|-------|--------|--------------------|
| **A: Bug fixes** | 5 | Small | Yes |
| **B: Core features** | 7 | Medium-Large | Yes (B1 is security-critical) |
| **C: UX polish** | 7 | Medium | No (but improves adoption) |
| **D: Advanced** | 4 | Large | No |
| **T: Tests** | 4 | Medium | Yes |

**Minimum viable production release:** Phase A + B1 + B2 + B4 + T1.
Everything else can ship incrementally.
