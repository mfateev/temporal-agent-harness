# Tools Gap Analysis: tcx vs Codex

Comprehensive comparison of tool implementations between tcx (temporal-agent-harness) and Codex (codex-rs). Identifies missing tools, partial implementations, and feature gaps within existing tools.

**Last updated:** 2026-02-14

---

## Summary

| Category | Codex Tools | tcx Tools | Gap |
|----------|-------------|-----------|-----|
| Shell execution | 4 (`exec_command`, `write_stdin`, `shell`, `shell_command`) | 1 (`shell`) | 3 missing |
| File & directory | 4 (`read_file`, `list_dir`, `grep_files`, `view_image`) | 3 (`read_file`, `list_dir`, `grep_files`) | 1 missing |
| Code editing | 2 (`apply_patch` freeform, `apply_patch` JSON) | 2 (`apply_patch` JSON, `write_file`) | Parity (different mix) |
| Collaboration | 5 (`spawn_agent`, `send_input`, `wait`, `close_agent`, `resume_agent`) | 5 (same) | Feature gaps within |
| User interaction | 1 (`request_user_input`) | 1 (`request_user_input`) | Minor schema gap |
| Planning | 1 (`update_plan`) | 0 | 1 missing |
| MCP | 4 (`list_mcp_resources`, `list_mcp_resource_templates`, `read_mcp_resource`, dynamic MCP tools) | 0 | 4 missing |
| Search | 2 (`search_tool_bm25`, `web_search`) | 0 | 2 missing |
| Testing | 1 (`test_sync_tool`) | 0 | Low priority |
| **Total** | **24** | **12** | **12 missing** |

---

## 1. Missing Tools (Not Implemented)

### 1.1 `exec_command` + `write_stdin` (Unified Exec) — HIGH PRIORITY

**Codex:** The unified exec system (`exec_command` + `write_stdin`) provides PTY-based interactive command execution with session management. This is the **default shell type** in Codex.

| Feature | Codex | tcx |
|---------|-------|-----|
| PTY allocation | `tty` parameter | Not supported |
| Session persistence | `write_stdin` sends input to running session | Not supported |
| Yield-based output | `yield_time_ms` returns partial output | Not supported |
| Output token limiting | `max_output_tokens` | Uses byte-based output limiter |
| Login shell control | `login` parameter | Not supported |
| Sandbox escalation | `sandbox_permissions` + `justification` | Has sandbox but no per-call escalation |

**Impact:** Without unified exec, tcx cannot support interactive commands (e.g., `python` REPL, `node` REPL, `gdb` sessions, long-running dev servers). The model must complete all interaction in a single shell invocation.

**Files to create:**
- `internal/tools/handlers/unified_exec.go` — PTY session manager + handler
- Update `internal/tools/spec.go` — `NewExecCommandToolSpec()`, `NewWriteStdinToolSpec()`
- Update `internal/models/config.go` — `ShellType` config field

### 1.2 `shell_command` — LOW PRIORITY

**Codex:** Alternative shell tool that accepts `command` as a plain string (like tcx's current `shell`) with additional `login` parameter. Codex's `shell` tool takes `command` as an **array** of strings.

**tcx status:** tcx's `shell` tool accepts `command` as a string (matching `shell_command` semantics). The array-based `shell` variant is not implemented.

**Impact:** Minimal — tcx's string-based shell covers the common case. The array variant is mainly useful for bypassing shell interpretation.

### 1.3 `view_image` — MEDIUM PRIORITY

**Codex:** Reads a local image file and sends it as image content to the model (requires `supports_image_input` capability).

| Feature | Codex | tcx |
|---------|-------|-----|
| Image file reading | Local filesystem path → base64 image | Not supported |
| Multi-modal input | Sends image content to LLM | Not supported |
| Format support | PNG, JPEG, GIF, WebP | N/A |

**Impact:** Models cannot inspect screenshots, diagrams, or UI mockups from the filesystem. Limits usefulness for frontend/UI work.

**Files to create:**
- `internal/tools/handlers/view_image.go`
- Update `internal/tools/spec.go` — `NewViewImageToolSpec()`
- Update `internal/models/config.go` — `SupportsImageInput` flag

### 1.4 `update_plan` — MEDIUM PRIORITY

**Codex:** Allows the model to maintain a visible task plan with steps and statuses (`pending`, `in_progress`, `completed`). The plan is displayed in the UI and helps the user track progress.

| Feature | Codex | tcx |
|---------|-------|-----|
| Plan creation | `update_plan` tool with step list | Not supported |
| Step status tracking | `pending` / `in_progress` / `completed` | Not supported |
| Plan explanation | Optional explanation field | Not supported |
| UI display | Rendered in TUI as progress tracker | Not supported |

**Impact:** Without `update_plan`, the model cannot show structured progress to the user. For complex multi-step tasks, the user only sees raw tool calls and messages.

**Files to create:**
- `internal/tools/handlers/plan.go`
- Update `internal/tools/spec.go` — `NewUpdatePlanToolSpec()`
- Update `internal/workflow/state.go` — plan state fields
- Update `internal/cli/renderer.go` — plan rendering

### 1.5 MCP Tools — HIGH PRIORITY (for extensibility)

**Codex:** Full MCP (Model Context Protocol) integration with 3 built-in resource tools + dynamic tool registration from MCP servers.

| Feature | Codex | tcx |
|---------|-------|-----|
| `list_mcp_resources` | List resources from MCP servers | Not supported |
| `list_mcp_resource_templates` | List parameterized resource templates | Not supported |
| `read_mcp_resource` | Read specific resource by URI | Not supported |
| Dynamic MCP tools | Runtime tool registration from MCP servers | Not supported |
| MCP connection manager | Manages MCP server connections | Not supported |
| MCP tool search | `search_tool_bm25` for discovering tools | Not supported |

**Impact:** MCP is the primary extensibility mechanism in Codex. Without it, tcx cannot integrate with external tool providers (databases, APIs, custom tooling). This is likely the largest functional gap.

**Files to create:**
- `internal/mcp/` — New package for MCP connection management
- `internal/tools/handlers/mcp.go` — MCP tool call handler
- `internal/tools/handlers/mcp_resource.go` — Resource listing/reading
- Update `internal/tools/spec.go` — MCP tool specs
- Update `internal/models/config.go` — MCP server configuration

### 1.6 `search_tool_bm25` — LOW PRIORITY

**Codex:** BM25-based search over MCP tool metadata. When many MCP tools are available, this allows the model to discover relevant tools without sending all specs in every request.

**Impact:** Only relevant once MCP is implemented. Not needed with the current fixed tool set.

### 1.7 `web_search` — LOW PRIORITY

**Codex:** Built-in web search tool (not a function tool — uses OpenAI's native web search capability). Conditionally enabled based on `web_search_mode` config.

**Impact:** Low — this is an OpenAI-specific built-in tool, not a function tool. Can be added later as a config flag passed to the LLM client.

---

## 2. Feature Gaps Within Existing Tools

### 2.1 `shell` Tool

| Feature | Codex | tcx | Gap |
|---------|-------|-----|-----|
| Command format | Array of strings | Single string | Different (tcx matches `shell_command`) |
| `sandbox_permissions` param | Per-call escalation | Not in spec | Missing |
| `justification` param | Required for escalation | Not in spec | Missing |
| `prefix_rule` param | Suggested prefix pattern | Not in spec | Missing |
| `login` param | Login shell control | Not in spec | Missing |
| `workdir` param | Working directory | Present (`workdir` + `working_directory`) | Parity (tcx has two params — should deduplicate) |
| `timeout_ms` param | Timeout override | Present | Parity |
| Output limiting | Token-based (`max_output_tokens`) | Byte-based (1 MiB cap) | Different approach |

### 2.2 `read_file` Tool

| Feature | Codex | tcx | Gap |
|---------|-------|-----|-----|
| `file_path` param | Absolute path | Absolute path | Parity |
| `offset` param | 1-indexed line number | 1-indexed line number | Parity |
| `limit` param | Max lines to return | Max lines to return | Parity |
| `mode` param | `"slice"` or `"indentation"` | Not present | **Missing** |
| `indentation` param | Indentation-aware block mode | Not present | **Missing** |
| - `anchor_line` | Center lookup line | N/A | Missing |
| - `max_levels` | Parent indentation levels | N/A | Missing |
| - `include_siblings` | Include sibling blocks | N/A | Missing |
| - `include_header` | Include doc comments | N/A | Missing |
| - `max_lines` | Hard cap for indentation mode | N/A | Missing |

**Impact:** The indentation-aware mode lets the model read a function/class body by specifying an anchor line, without needing to know exact line ranges. This is valuable for navigating unfamiliar code.

### 2.3 `apply_patch` Tool

| Feature | Codex | tcx | Gap |
|---------|-------|-----|-----|
| JSON function variant | Present | Present | Parity |
| Freeform (Lark) variant | Present | Not present | **Missing** |
| Patch grammar | Same format | Same format | Parity |
| Fuzzy line matching | Multi-pass seek | Multi-pass seek | Parity |

**Impact:** Low — the JSON variant works with all models. The freeform variant is a Codex-specific optimization for models that support non-JSON tool output.

### 2.4 `request_user_input` Tool

| Feature | Codex | tcx | Gap |
|---------|-------|-----|-----|
| Questions array | 1-3 questions | 1-4 questions | tcx allows more |
| Question `id` | Required (snake_case) | Required | Parity |
| Question `header` | Required (12 chars max) | Not present | **Missing** |
| Question `question` | Required | Required | Parity |
| Options `label` | Required (1-5 words) | Required | Parity |
| Options `description` | Required | Optional in tcx | Minor gap |
| Plan mode restriction | Only available in Plan mode | Always available | Different design |

### 2.5 Collaboration Tools (`spawn_agent`, etc.)

| Feature | Codex | tcx | Gap |
|---------|-------|-----|-----|
| `spawn_agent` message/items | Both supported | Both supported | Parity (just fixed) |
| `spawn_agent` agent_type | Extensible (Engineer, Designer, etc.) | Fixed set (explorer, worker, orchestrator, planner) | Codex has more roles |
| `send_input` message/items | Both supported | Both supported | Parity (just fixed) |
| `resume_agent` | Implemented | Stub (returns error) | **Not implemented** |
| Role-based model override | Explorer uses cheaper model | Explorer uses `gpt-5.1-codex-mini` on OpenAI | Parity (just fixed) |
| Orchestrator instructions | Full orchestrator prompt | Full orchestrator prompt | Parity (just fixed) |
| Max spawn depth | 1 (no grandchildren) | 1 (no grandchildren) | Parity |

---

## 3. Configuration Gaps

### 3.1 `ToolsConfig` Flags

| Flag | Codex | tcx | Gap |
|------|-------|-----|-----|
| Shell type selection | `shell_type` (Default/Local/UnifiedExec/Disabled/ShellCommand) | `EnableShell` (bool) | Missing shell type variants |
| Apply patch type | `apply_patch_tool_type` (Freeform/Function) | `EnableApplyPatch` (bool) | Missing freeform variant |
| Web search mode | `web_search_mode` (Cached/Live/Disabled) | Not present | Missing |
| Image input support | `supports_image_input` (bool) | Not present | Missing |
| Search tool | `search_tool` (bool) | Not present | Missing |
| Collab tools | `collab_tools` (bool) | `EnableCollab` (bool) | Parity |
| Experimental tools | `experimental_supported_tools` (Vec) | Not present | Missing |
| Request rule | `request_rule_enabled` (bool) | Not present | Missing |

### 3.2 Per-Call Sandbox Escalation

Codex allows individual tool calls to request elevated sandbox permissions via `sandbox_permissions` and `justification` parameters on shell/exec tools. tcx has sandbox support but no per-call escalation mechanism — the sandbox mode is session-wide.

---

## 4. Priority Ranking

### P0 — Critical for feature parity
1. **MCP integration** — Primary extensibility mechanism. Blocks integration with external tools.
2. **`update_plan` tool** — Important for user visibility into agent progress.

### P1 — Important for advanced use cases
3. **Unified exec (`exec_command` + `write_stdin`)** — Required for interactive sessions (REPLs, debuggers, dev servers).
4. **`read_file` indentation mode** — Significant quality-of-life for code navigation.
5. **`resume_agent` implementation** — Currently a stub; needed for long-running subagent workflows.

### P2 — Nice to have
6. **`view_image` tool** — Needed for multi-modal workflows (UI dev, screenshots).
7. **Per-call sandbox escalation** — Finer-grained security for shell commands.
8. **Shell `login` parameter** — Minor UX improvement.

### P3 — Low priority
9. **`search_tool_bm25`** — Only relevant after MCP.
10. **`web_search`** — OpenAI built-in, simple config flag.
11. **Freeform `apply_patch`** — Optimization for specific model capabilities.
12. **`shell` array command variant** — Minimal practical impact.

---

## 5. Architectural Notes

### tcx Additions Not in Codex

| Tool/Feature | tcx | Codex |
|--------------|-----|-------|
| `write_file` tool | Dedicated tool | Routes through `apply_patch` |
| `planner` agent role | Separate role with custom instructions | Not a distinct role |
| Temporal workflow orchestration | Child workflows for subagents | In-process threads |

### Tool Dispatch Architecture Differences

| Aspect | Codex | tcx |
|--------|-------|-----|
| Execution model | In-process async (tokio) | Temporal activities (separate workers) |
| Parallel execution | `tokio::spawn` per tool | `workflow.ExecuteActivity` futures |
| Tool timeout | In-process cancellation | Temporal `StartToCloseTimeout` |
| State persistence | In-memory (lost on crash) | Temporal workflow history (durable) |
| Subagent isolation | Threads within process | Child workflows (separate execution) |

These architectural differences are intentional — tcx trades in-process speed for Temporal's durability, scalability, and fault tolerance guarantees.
