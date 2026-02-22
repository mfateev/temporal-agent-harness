# Feature Gap Analysis: tcx vs Codex

Comprehensive comparison of all features between tcx (temporal-agent-harness, Go/Temporal) and Codex (codex-rs, Rust). Covers tools, LLM integration, configuration, security, MCP, memory, CLI/TUI, session management, auth, and infrastructure.

**Last updated:** 2026-02-20

---

## Legend

| Status | Meaning |
|--------|---------|
| **Implemented** | Feature exists in tcx with rough parity to Codex |
| **Partial** | Core functionality exists but missing significant sub-features |
| **Not Started** | Feature exists in Codex but is absent from tcx |
| **Not Needed** | Temporal provides this natively or the feature is N/A |
| **Temporal-Only** | Feature unique to tcx with no Codex equivalent |

---

## 1. Tool Implementations

| Tool | Codex | tcx | Status | Gap Details |
|------|-------|-----|--------|-------------|
| `shell` / `shell_command` | Array + string modes | Array + string modes | **Implemented** | — |
| `read_file` (slice + indentation) | Both modes | Both modes | **Implemented** | — |
| `write_file` | Yes | Yes | **Implemented** | — |
| `list_dir` | Yes | Yes | **Implemented** | — |
| `grep_files` | Yes (via `rg` subprocess) | Yes (Go native) | **Implemented** | Different implementation strategy |
| `apply_patch` | Yes (Lark grammar) | Yes (custom parser) | **Implemented** | — |
| `exec_command` / `write_stdin` | PTY + pipe, 64 sessions | PTY + pipe, 64 sessions | **Implemented** | — |
| `update_plan` | Yes | Yes (intercepted by workflow) | **Implemented** | — |
| `request_user_input` | Yes | Yes | **Implemented** | — |
| `spawn_agent` / collab tools | Thread-based multi-agent | Temporal child workflows | **Implemented** | Different mechanism, same semantics |
| `view_image` | Injects base64 image into context | — | **Not Started** | No multimodal image input support |
| `search_tool_bm25` | BM25 search over MCP tool registry | — | **Not Started** | No MCP tools → no tool search needed yet |
| `js_repl` / `js_repl_reset` | Persistent Node.js kernel | — | **Not Started** | Experimental in Codex (feature-gated) |
| MCP tool dispatch (`mcp__*`) | Full MCP client | — | **Not Started** | See §5 (MCP) |
| MCP resource tools | `list_resources`, `read_resource`, etc. | — | **Not Started** | Depends on MCP client |
| Dynamic tools (`DynamicToolHandler`) | For apps/connectors | — | **Not Started** | Depends on apps/connectors |
| `resume_agent` | Supported | Placeholder only | **Partial** | Spec exists but handler not implemented |

---

## 2. LLM / Model Features

| Feature | Codex | tcx | Status | Gap Details |
|---------|-------|-----|--------|-------------|
| OpenAI Responses API | Yes | Yes | **Implemented** | — |
| Anthropic Claude support | No (OpenAI-only) | Yes (with prompt caching) | **Temporal-Only** | tcx has dual-provider; Codex is OpenAI-only |
| PreviousResponseID chaining | Yes | Yes (OpenAI only) | **Implemented** | — |
| Anthropic prompt caching | N/A | Yes (ephemeral breakpoints) | **Temporal-Only** | — |
| Streaming responses | Yes (with retry + idle timeout) | No | **Not Started** | Harness uses buffered activity calls; no token-level streaming to CLI |
| Model switching (mid-session) | Yes | Yes (UpdateModel handler) | **Implemented** | — |
| Model switch + compaction fixes | Yes (5 PRs) | Yes (ported) | **Implemented** | — |
| Remote model discovery | Yes (5-min cache, `ModelsManager`) | Yes (`FetchAvailableModels`) | **Implemented** | — |
| Reasoning effort controls | `reasoning_effort` + `reasoning_summary` + `verbosity` | `reasoning_effort` only | **Partial** | Missing `reasoning_summary` and `verbosity` |
| Hide/show agent reasoning | Yes | No | **Not Started** | — |
| Web search (cached/live) | Yes | Yes | **Implemented** | — |
| Context compaction (auto) | Yes (auto + `/compact`) | Yes (auto + manual) | **Implemented** | — |
| Remote compaction (OpenAI) | Yes (`/responses/compact`) | Yes | **Implemented** | — |
| Local compaction (Anthropic) | N/A | Yes | **Temporal-Only** | — |
| Request body compression (zstd) | Yes | No | **Not Started** | Minor optimization |
| Stream idle timeout / retry | Yes (configurable) | N/A (no streaming) | **Not Started** | Blocked by streaming gap |
| Ollama local provider | Yes (with model pull) | No | **Not Started** | — |
| LM Studio local provider | Yes (with model download) | No | **Not Started** | — |
| WebSocket transport | Experimental | No | **Not Started** | Low priority; experimental upstream |
| Model deprecation/migration prompts | Yes | No | **Not Started** | — |
| Post-turn suggestions | Yes | Yes (ghost text) | **Implemented** | — |

---

## 3. Configuration System

| Feature | Codex | tcx | Status | Gap Details |
|---------|-------|-----|--------|-------------|
| TOML config file (`~/.codex/config.toml`) | Yes (rich, ~50+ fields) | No config file | **Not Started** | tcx uses CLI flags only; no persistent config |
| Config profiles (`[profiles.<name>]`) | Yes | No | **Not Started** | — |
| Config layer stack (cloud → user → profile → CLI) | Yes | No | **Not Started** | — |
| ConfigEdit operations (programmatic) | Yes | No | **Not Started** | — |
| JSON Schema for config | Yes | No | **Not Started** | — |
| `CODEX_HOME` env var / `--codex-home` flag | Yes | Yes | **Implemented** | — |
| Personality setting | Yes (`/personality` command) | No | **Not Started** | — |
| `project_doc_max_bytes` / fallback filenames | Yes (configurable) | Hardcoded | **Partial** | No configurable limits |
| Session source tracking (CLI/VSCode/MCP/exec) | Yes | Partial (`harness` vs `direct`) | **Partial** | — |
| File opener integration (VSCode, Cursor, etc.) | Yes | No | **Not Started** | — |
| Analytics / telemetry config (Statsig, OTLP) | Yes | No | **Not Started** | — |
| Notification config (OSC-9, BEL) | Yes | No | **Not Started** | — |
| `--output-schema` (structured output for exec) | Yes | No | **Not Started** | — |
| `--json` JSONL event stream (exec mode) | Yes | No | **Not Started** | — |
| `--image` flag (multimodal input) | Yes | No | **Not Started** | — |
| `/debug-config` introspection | Yes | No | **Not Started** | — |
| Feature flags system | Yes (granular per-feature) | No | **Not Started** | — |

---

## 4. Security Features

| Feature | Codex | tcx | Status | Gap Details |
|---------|-------|-----|--------|-------------|
| Approval policy: `never` | Yes | Yes | **Implemented** | — |
| Approval policy: `unless-trusted` | Yes | Yes | **Implemented** | — |
| Approval policy: `on-failure` | Yes (deprecated upstream) | Yes | **Implemented** | Should deprecate to match upstream |
| Approval policy: `on-request` | Yes | No | **Not Started** | Auto-approve model-marked safe commands |
| `--full-auto` shortcut | Yes | Yes | **Implemented** | — |
| `--yolo` (bypass all safety) | Yes | No | **Not Started** | — |
| macOS Seatbelt sandbox | Yes (embedded SBPL) | Yes | **Implemented** | — |
| Linux bubblewrap sandbox | Yes (vendored binary) | Yes | **Implemented** | — |
| Linux Landlock + seccomp | Yes (in-process kernel) | No | **Not Started** | Extra kernel-level hardening layer |
| Windows sandbox | Yes (restricted token, ACL, firewall) | No | **Not Started** | No Windows support at all |
| Exec policy engine (`.rules` files) | Yes | Yes | **Implemented** | — |
| Env var filtering (5-step policy) | Yes | Yes | **Implemented** | — |
| Network proxy (domain allow/deny) | Yes (`network-proxy` crate) | No | **Not Started** | — |
| Patch safety assessment | Yes (`assess_patch_safety`) | No explicit equivalent | **Not Started** | — |
| Configurable sandbox read access | Yes (`ReadOnlyAccess` enum) | No | **Not Started** | — |
| Banned prefix suggestions (exec policy) | Yes | No | **Not Started** | — |
| `codex execpolicy check` debug command | Yes | No | **Not Started** | — |
| Permissions struct consolidation | Yes (upstream) | No | **Not Started** | High priority upstream change |
| Git commands safe by default | Yes (upstream) | No | **Not Started** | High priority upstream change |
| Empty command list validation | Yes (upstream) | No | **Not Started** | High priority upstream change |
| Structured network approvals | Yes (upstream) | No | **Not Started** | — |
| Process hardening (seccomp capabilities) | Yes | No | **Not Started** | — |

---

## 5. MCP (Model Context Protocol)

| Feature | Codex | tcx | Status | Gap Details |
|---------|-------|-----|--------|-------------|
| MCP client (stdio transport) | Yes | No | **Not Started** | Major gap — entire subsystem missing |
| MCP client (HTTP transport) | Yes | No | **Not Started** | — |
| MCP server config (`mcp_servers` map) | Yes (per-server enabled/required/timeout) | No | **Not Started** | — |
| MCP OAuth support | Yes (keyring/file credential store) | No | **Not Started** | — |
| MCP tool namespacing (`mcp__server__tool`) | Yes | No | **Not Started** | — |
| MCP resource protocol (list/read) | Yes | No | **Not Started** | — |
| Codex as MCP server (`codex mcp-server`) | Yes | No | **Not Started** | — |
| Apps/connectors MCP gateway | Yes | No | **Not Started** | — |
| MCP tool search (`search_tool_bm25`) | Yes (BM25 over tool registry) | No | **Not Started** | — |
| Skill dependency auto-install | Yes | No | **Not Started** | Depends on skills + MCP |

---

## 6. Memory / Persistence

| Feature | Codex | tcx | Status | Gap Details |
|---------|-------|-----|--------|-------------|
| Session rollout (JSONL persistence) | Yes (`~/.codex/sessions/`) | Temporal workflow history | **Not Needed** | Temporal provides durable history natively |
| Session resume | Yes (by UUID/name/`--last`) | Yes (`--session`, picker) | **Implemented** | — |
| Session fork | Yes (`codex fork`, `/fork`) | No | **Not Started** | — |
| Session archive/unarchive | Yes | No | **Not Started** | — |
| Thread naming (`/rename`) | Yes | No | **Not Started** | — |
| Ephemeral mode (`--ephemeral`) | Yes | No | **Not Started** | — |
| Memory v2 (extraction + consolidation) | Yes (28 commits, two-phase pipeline) | No | **Not Started** | Large effort; entirely new subsystem |
| Shell snapshot (env capture/restore) | Yes (3-day retention) | No | **Not Started** | — |
| Ghost commit / undo | Yes (`/undo`) | No | **Not Started** | — |
| SQLite state DB | Experimental | No | **Not Started** | — |

---

## 7. CLI / TUI Features

| Feature | Codex | tcx | Status | Gap Details |
|---------|-------|-----|--------|-------------|
| Interactive TUI | Full `ratatui` TUI | Bubbletea TUI | **Implemented** | Different frameworks, similar UX |
| Markdown rendering | Yes | Yes (glamour) | **Implemented** | — |
| Streaming display (incremental tokens) | Yes (real-time chunks) | No (full response at once) | **Not Started** | Major UX gap |
| Diff rendering (`/diff`) | Yes | No | **Not Started** | — |
| Syntax highlighting | Yes (shell commands) | No | **Not Started** | — |
| File search (`@` mention) | Yes (fuzzy inline) | No | **Not Started** | — |
| External editor integration | Yes | No | **Not Started** | — |
| Pager overlay (scrollable long content) | Yes | No | **Not Started** | — |
| Terminal notifications (OSC-9/BEL) | Yes | No | **Not Started** | — |
| Onboarding flow (first-run setup) | Yes | No | **Not Started** | — |
| Terminal palette detection | Yes | No | **Not Started** | — |
| Paste burst detection | Yes | Yes | **Implemented** | — |
| Plan rendering | Yes | Yes | **Implemented** | — |
| Suggestion ghost text | Yes | Yes | **Implemented** | — |
| Session picker | Yes (by name/UUID/CWD) | Yes (Temporal visibility) | **Implemented** | — |
| Approval prompt (with preview boxes) | Yes | Yes | **Implemented** | — |
| Interactive selector (arrow keys) | Yes | Yes | **Implemented** | — |
| Slash commands | ~30+ commands | ~4 (`/exit`, `/end`, `/compact`, `/model`) | **Partial** | Most slash commands missing |
| `codex exec` (non-interactive) | Yes (rich: `--output-schema`, `--json`, `--image`) | `-m` flag only | **Partial** | Missing structured output, JSONL stream, image input |
| `codex review` (code review mode) | Yes | No | **Not Started** | — |
| `codex apply` (git apply latest diff) | Yes | No | **Not Started** | — |
| `codex cloud` (cloud task browser) | Yes | No | **Not Started** | — |
| Shell completion (bash/zsh/fish) | Yes | No | **Not Started** | — |
| Rate limit display (TUI status bar) | Yes | No | **Not Started** | — |
| Alternate screen mode (auto/always/never) | Yes | No | **Not Started** | — |
| Frame rate limiter | Yes | No | **Not Started** | — |
| Status line config (`/statusline`) | Yes | No | **Not Started** | — |

---

## 8. Session Management

| Feature | Codex | tcx | Status | Gap Details |
|---------|-------|-----|--------|-------------|
| Multi-turn conversation | Yes | Yes | **Implemented** | — |
| Interrupt (mid-turn) | Yes | Yes | **Implemented** | — |
| Shutdown (graceful) | Yes | Yes | **Implemented** | — |
| Turn lifecycle markers | Yes | Yes | **Implemented** | — |
| ThreadManager (multi-session) | Yes (start/resume/fork/list/remove) | HarnessWorkflow (per-cwd) | **Partial** | Per-directory management but no fork/archive |
| Sub-agent concurrency cap | Yes (`agent_max_threads=6`) | No explicit cap | **Not Started** | Could spawn unlimited children |
| Max thread spawn depth | Yes | Yes (`MaxThreadSpawnDepth=1`) | **Implemented** | — |
| Collaboration modes (plan/default) | Yes (`/plan`, `/collab`) | Yes (planner role) | **Partial** | Less flexible; no runtime mode switching |
| Rate limit tracking | Yes (per-session) | No | **Not Started** | — |
| Token usage display | Yes (detailed breakdown) | Yes (basic total) | **Partial** | Missing cached tokens breakdown |
| Steer (mid-turn input) | Yes (Enter submits immediately) | No | **Not Started** | — |
| Context manager normalization | Yes | No | **Not Started** | — |

---

## 9. Auth & Infrastructure

| Feature | Codex | tcx | Status | Gap Details |
|---------|-------|-----|--------|-------------|
| ChatGPT OAuth login (device code) | Yes | No | **Not Started** | API key env var only |
| Keyring credential storage | Yes (auto/file/keyring) | No | **Not Started** | — |
| OpenTelemetry (metrics/traces/logs) | Yes (Statsig, OTLP exporters) | No | **Not Started** | — |
| Sleep prevention (macOS) | Yes | No | **Not Started** | — |
| macOS desktop app launcher | Yes (`codex app`) | No | **Not Started** | — |
| App server (JSON-RPC for IDE extensions) | Yes (WebSocket + stdio) | No | **Not Started** | Future phase |
| Skills system (TOML packages) | Yes | No | **Not Started** | — |
| Responses API proxy (debug) | Yes | No | **Not Started** | — |

---

## 10. Temporal-Only Features (Not in Codex)

These features exist in tcx but have no Codex equivalent. They represent advantages of the Temporal architecture.

| Feature | Description |
|---------|-------------|
| **Durable execution** | Workflow state survives worker crashes; transparent recovery with no data loss |
| **ContinueAsNew** | Prevents unbounded event history growth; automatic at 100 iterations |
| **HarnessWorkflow** | Long-lived per-directory orchestrator; survives CLI disconnects; sessions accumulate |
| **Blocking long-poll** | `UpdateGetStateUpdate` handler provides efficient server-side blocking, replacing client-side polling |
| **Activity retry with backoff** | Temporal-native exponential backoff retry for LLM calls and tool execution |
| **Worker version tracking** | CLI status bar displays worker version hash for deployment skew detection |
| **Dual LLM provider** | OpenAI + Anthropic support; Codex is OpenAI-only |
| **Anthropic prompt caching** | Ephemeral cache breakpoints on instructions, tool definitions, and conversation for cost reduction |
| **Anthropic local compaction** | History summarization without requiring a remote compact API endpoint |
| **Temporal visibility API** | Session listing and filtering via Temporal's built-in search attributes |
| **Child workflow isolation** | Sub-agents run as independent child workflows with their own event histories |

---

## Summary: Gap Counts

| Category | Implemented | Partial | Not Started | Not Needed | Temporal-Only |
|----------|-------------|---------|-------------|------------|---------------|
| Tools (§1) | 10 | 1 | 6 | 0 | 0 |
| LLM/Model (§2) | 9 | 1 | 8 | 0 | 3 |
| Configuration (§3) | 1 | 2 | 13 | 0 | 0 |
| Security (§4) | 8 | 0 | 12 | 0 | 0 |
| MCP (§5) | 0 | 0 | 10 | 0 | 0 |
| Memory (§6) | 1 | 0 | 7 | 1 | 0 |
| CLI/TUI (§7) | 8 | 2 | 17 | 0 | 0 |
| Session Mgmt (§8) | 5 | 3 | 4 | 0 | 0 |
| Auth/Infra (§9) | 0 | 0 | 8 | 0 | 0 |
| **Total** | **42** | **9** | **85** | **1** | **11+** |

---

## Priority Recommendations

### Tier 1 — High Impact (core user experience)

| # | Gap | Effort | Rationale |
|---|-----|--------|-----------|
| 1 | **Streaming responses** | Large | Single biggest UX gap; users see nothing until full LLM response completes. Codex streams tokens incrementally. Requires rethinking activity pattern (possibly workflow-side SSE or chunked updates). |
| 2 | **MCP client support** | Large | Entire ecosystem of third-party tools unavailable. Blocks IDE integration and tool extensibility. Requires stdio + HTTP transports, tool namespacing, server lifecycle management. |
| 3 | **Persistent config file** | Medium | Everything must be passed as CLI flags today. Need `config.toml` (or equivalent), profiles, and config layer stack for usability parity. |
| 4 | **Memory v2 system** | Large | No cross-session learning. Codex's two-phase extraction + consolidation pipeline is a major productivity feature (28 upstream commits). Could implement a Temporal-native variant using workflow-based extraction. |
| 5 | **Slash commands** | Medium | Only ~4 vs Codex's ~30+. Missing `/diff`, `/review`, `/init`, `/rename`, `/fork`, `/plan`, `/collab`, `/mention`, `/personality`, `/mcp`, `/skills`, `/status`, `/debug-config`, `/ps`, `/clean`. |

### Tier 2 — Medium Impact (power users and completeness)

| # | Gap | Effort | Rationale |
|---|-----|--------|-----------|
| 6 | **`view_image` tool** | Small | No multimodal image input. Required for model capabilities that accept images. |
| 7 | **Session fork** | Small | Can't branch conversations. Temporal makes this straightforward (start new workflow with partial history). |
| 8 | **Local providers (Ollama/LM Studio)** | Medium | Can't run models locally. Important for privacy-sensitive and offline use cases. |
| 9 | **Landlock + seccomp hardening** | Medium | Linux sandbox is bubblewrap-only. Codex adds in-process kernel restrictions as an extra layer. |
| 10 | **Network proxy** | Medium | Can't control outbound network access granularly. Domain allow/deny lists for sandboxed processes. |
| 11 | **Code review mode** | Medium | `codex review` has no equivalent. Separate model, structured findings output. |
| 12 | **Reasoning controls** | Small | Missing `reasoning_summary` and `verbosity`. Quick config plumbing. |
| 13 | **Upstream high-priority changes** | Small | Permissions struct consolidation, git-commands-safe, on-failure deprecation, empty command validation. All low effort individually. |
| 14 | **Structured network approvals** | Medium | Recent upstream addition. Extends existing approval infrastructure. |
| 15 | **Sub-agent concurrency cap** | Small | No `agent_max_threads` limit; could spawn unlimited children. |
| 16 | **`@` file mentions** | Medium | Fuzzy file search for attaching context to prompts. |

### Tier 3 — Low Impact (niche, experimental, or deferrable)

| # | Gap | Effort | Rationale |
|---|-----|--------|-----------|
| 17 | **JS REPL** | Large | Experimental in Codex, behind feature flag. |
| 18 | **WebSocket transport** | Large | Experimental upstream. |
| 19 | **Ghost commit / undo** | Medium | Nice-to-have git safety net. |
| 20 | **Shell snapshot** | Small | Environment capture/restore across sessions. |
| 21 | **macOS desktop app launcher** | Small | macOS only, niche. |
| 22 | **Sleep prevention** | Small | Minor QoL. |
| 23 | **App server / IDE integration** | Large | Future phase. Needed for VSCode extension but large scope. |
| 24 | **Skills system** | Medium | TOML-defined tool packages. Depends on MCP. |
| 25 | **OpenTelemetry** | Medium | Observability. Important for production but not user-facing. |
| 26 | **Auth (OAuth, keyring)** | Medium | ChatGPT login, keyring credential storage. API key works for now. |
| 27 | **Config JSON Schema** | Small | Developer tooling for config validation. |
| 28 | **Onboarding flow** | Small | First-run setup. |
| 29 | **Terminal notifications** | Small | OSC-9 / BEL when terminal unfocused. |
| 30 | **Windows sandbox** | Large | Entire platform. Low priority unless Windows support is required. |

---

## Appendix: Codex Source References

Key Codex source locations for porting reference:

| Feature | Codex Path |
|---------|------------|
| Tools (handlers) | `codex-rs/core/src/tools/handlers/` |
| Tool specs | `codex-rs/core/src/tools/tool_def_*.rs` |
| MCP client | `codex-rs/core/src/mcp/`, `mcp_connection_manager.rs` |
| MCP server | `codex-rs/mcp-server/src/` |
| Memory v2 | `codex-rs/core/src/memories/` |
| Config system | `codex-rs/config/src/` (extracted crate) |
| Exec policy | `codex-rs/execpolicy/src/` |
| Sandbox (macOS) | `codex-rs/core/src/seatbelt.rs`, `seatbelt_base_policy.sbpl` |
| Sandbox (Linux) | `codex-rs/linux-sandbox/` |
| Sandbox (Windows) | `codex-rs/windows-sandbox-rs/` |
| Network proxy | `codex-rs/network-proxy/` |
| Auth | `codex-rs/core/src/auth.rs` |
| TUI | `codex-rs/tui/src/` |
| CLI | `codex-rs/cli/src/` |
| Streaming | `codex-rs/core/src/client/streaming.rs` |
| Model providers | `codex-rs/core/src/model_provider_info.rs` |
| Skills | `codex-rs/core/src/skills/` |
| Ghost commit | `codex-rs/core/src/tasks/ghost_snapshot.rs` |
| Shell snapshot | `codex-rs/core/src/shell_snapshot.rs` |
| App server | `codex-rs/app-server/src/` |
| Ollama | `codex-rs/ollama/` |
| LM Studio | `codex-rs/lmstudio/` |
