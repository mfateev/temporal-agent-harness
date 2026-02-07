# Codex-Temporal-Go Architecture

> A Temporal-friendly reimplementation of Codex's agentic loop in Go

**Version:** 0.1.0
**Date:** 2026-02-07
**Status:** Design Phase

---

## Table of Contents

1. [Overview](#overview)
2. [Codex Architecture Analysis](#codex-architecture-analysis)
3. [Architectural Decisions](#architectural-decisions)
4. [Temporal Workflow Design](#temporal-workflow-design)
5. [Implementation Guide](#implementation-guide)
6. [Future Enhancements](#future-enhancements)
7. [References](#references)

---

## Overview

### Project Goal

Create a clean Temporal-friendly reimplementation of Codex's agentic loop in Go, leveraging Temporal's durable execution model while maintaining the core functionality of LLM-driven tool calling.

### Key Principles

- **Durable Execution First:** Leverage Temporal's built-in durability, retries, and state management
- **Simplicity:** Start with a simplified, working implementation before adding complexity
- **Extensibility:** Design interfaces that allow future enhancements without breaking changes
- **Temporal-Native:** Use Temporal patterns and idioms, not just port Rust code
- **Structural Alignment (CRITICAL):** Follow the original Codex repository structure as closely as makes sense for Go/Temporal to enable future maintenance and incorporation of upstream changes

> **See:** `../CLAUDE.md` for detailed guidance on maintaining structural alignment with Codex

---

## Codex Architecture Analysis

### The Agentic Loop (3 Layers)

Based on exploration of the Codex Rust codebase (`codex-rs/core/src/codex.rs`):

#### Layer 1: `run_turn()` - Top-level Turn Execution
**Location:** `codex.rs:3267-3484`

```
run_turn()
  ├─ Initialize Session & TurnContext
  ├─ Load tools (built-in, MCP, dynamic)
  ├─ Build ToolRouter
  ├─ Record user input to history
  └─ Enter sampling loop (until no follow-up needed)
```

**Key Structures:**
- `Session`: Per-thread state manager (conversation history, event broadcasting)
- `TurnContext`: Per-turn configuration (tools, permissions, model client)

#### Layer 2: `run_sampling_request()` - Inner Loop
**Location:** `codex.rs:3600-3732`

```
run_sampling_request()
  ├─ Build ToolRouter with all available tools
  ├─ Construct Prompt from history + tool specs
  ├─ Call try_run_sampling_request() with retry logic
  ├─ Handle context window overflow
  └─ Return SamplingRequestResult
```

**Responsibilities:**
- Context window management
- Retry logic with exponential backoff
- Tool selection and filtering

#### Layer 3: `try_run_sampling_request()` - LLM Streaming Loop
**Location:** `codex.rs:4142-4421`

```
try_run_sampling_request()
  ├─ Start LLM streaming (ModelClientSession.stream())
  └─ Loop on stream events:
      ├─ OutputItemAdded → Mark item as active
      ├─ OutputItemDone →
      │   ├─ If tool call: Queue execution (add to in_flight futures)
      │   └─ If message: Emit completion
      ├─ OutputTextDelta → Stream text to client
      └─ Completed →
          ├─ drain_in_flight() - Wait for ALL tools
          └─ Break loop
```

**Key Behaviors:**
- **Parallel tool execution:** All tool calls from one LLM response execute concurrently
- **Sequential result collection:** Wait for ALL tools before next LLM call
- **Tool results feed back:** All results included in next prompt together

### Tool Execution Model

**Location:** `codex-rs/core/src/tools/`

#### Tool Categories
1. **Built-in Tools:** shell, apply_patch, read_file, write_file, etc.
2. **MCP Tools:** Dynamically loaded from Model Context Protocol servers
3. **Dynamic Tools:** User-defined custom tools

#### Execution Flow
```
ToolRouter.dispatch()
  ↓
ToolCallRuntime.handle_tool_call()
  ├─ tokio::spawn() - Launch as async task
  ├─ Acquire lock (read for parallel, write for sequential)
  ├─ Execute tool handler
  └─ Return result as Future

drain_in_flight()
  ├─ FuturesOrdered::next() - Wait for each tool
  ├─ Record result to history immediately
  └─ Continue until all complete
```

**Key Properties:**
- Tools declare `supports_parallel` flag
- Parallel tools: multiple concurrent (read lock)
- Sequential tools: one at a time (write lock)
- Cancellation via `CancellationToken`

### History Management

**Location:** `codex-rs/core/src/state/session.rs`

#### History Read Operations

1. **`clone_history().for_prompt()`** - Build LLM prompt
   - **When:** Every sampling iteration
   - **Purpose:** Construct prompt with full conversation context

2. **`clone_history().estimate_token_count()`** - Context window management
   - **When:** After history changes
   - **Purpose:** Check if history fits in model's context window

3. **`clone_history().drop_last_n_user_turns(N)`** - Rollback/undo
   - **When:** User requests undo
   - **Purpose:** Remove last N turns from conversation

4. **`clone_history().raw_items()`** - Direct access
   - **When:** Turn counting, history compaction
   - **Purpose:** Access raw conversation items for analysis

5. **`get_history_entry_request()`** - External queries
   - **When:** UI/client requests specific history
   - **Purpose:** Retrieve history from persistent storage

**Key Insight:** History is NOT just "write-only" - it's actively read for multiple purposes within the workflow.

### State Management

**SessionState** holds:
- Complete conversation history (all user/assistant messages, tool calls, tool results)
- Token usage tracking
- Pending input queue
- Configuration

**Atomicity:** Items recorded before/during execution, not after, ensuring consistency on failure.

---

## Architectural Decisions

### Decision 1: LLM Call Execution Model

**Choice:** **B) Buffered - LLM in Activity returns full response**

**Rationale:**
- LLM calls happen in Temporal Activities
- Activities return complete responses (not streaming)
- Simpler initial implementation
- Aligns with Temporal's activity model

**Future Enhancement:**
- **C) Hybrid streaming** - Activity streams to UI directly via callback/webhook while workflow tracks "LLM in progress"

**Implementation:**
```go
func ExecuteLLMActivity(ctx context.Context, input LLMActivityInput) (LLMActivityOutput, error) {
    // Call LLM API
    // Buffer entire response
    return LLMActivityOutput{
        Content: fullResponse,
        ToolCalls: detectedToolCalls,
    }, nil
}
```

---

### Decision 2: Tool Execution Mapping

**Choice:** **A) Each tool call = separate Activity**

**Rationale:**
- Fine-grained retries, timeouts, visibility per tool
- Each tool gets its own activity in Temporal UI
- Better observability and debugging
- Aligns with Temporal best practices

**Activities:**
- `ExecuteShellActivity(cmd string) → result`
- `ReadFileActivity(path string) → content`
- `WriteFileActivity(path, content string) → success`
- `ApplyPatchActivity(patch string) → success`

**Implementation:**
```go
type ToolActivity interface {
    Execute(ctx context.Context, input ToolInput) (ToolOutput, error)
}

type ShellActivity struct{}
func (a *ShellActivity) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
    // Execute shell command
    // Return result
}
```

---

### Decision 3: Parallel Tool Execution

**Choice:** **A) Parallel using Activity Futures, wait for ALL tools**

**Rationale:**
- `workflow.ExecuteActivity()` returns `workflow.Future` - naturally async
- No need for `workflow.Go()` - activities are already concurrent
- Wait for ALL tools to complete before next LLM call
- **Matches Codex behavior exactly**

**Pattern:**
```go
// Start all tool activities (parallel)
futures := []workflow.Future{}
for _, tool := range toolCalls {
    future := workflow.ExecuteActivity(ctx, ExecuteToolActivity, tool)
    futures = append(futures, future)
}

// Wait for ALL to complete
results := []ToolResult{}
for _, future := range futures {
    var result ToolResult
    if err := future.Get(ctx, &result); err != nil {
        // Handle error
    }
    results = append(results, result)
}

// Feed all results to next LLM call
```

---

### Decision 4: Loop Continuation & ContinueAsNew

**Choice:** **B) ContinueAsNew after N iterations**

**Rationale:**
- Simple loop in workflow up to N iterations (e.g., 10-20)
- Use `workflow.NewContinueAsNewError()` to start fresh
- Avoids workflow history bloat on long conversations
- Clean state handoff between executions

**Pattern:**
```go
type WorkflowState struct {
    History          ConversationHistory
    ToolRegistry     *ToolRegistry
    IterationCount   int
    MaxIterations    int
}

func AgenticWorkflow(ctx workflow.Context, state WorkflowState) error {
    for state.IterationCount < state.MaxIterations {
        // Execute iteration
        state.IterationCount++

        if !needsFollowUp {
            break
        }
    }

    // Continue as new if more iterations needed
    if needsFollowUp {
        state.IterationCount = 0 // Reset counter
        return workflow.NewContinueAsNewError(ctx, AgenticWorkflow, state)
    }

    return nil
}
```

---

### Decision 5: History Management

**Choice:** **Interface-based design with multiple implementations**

**Rationale:**
- Abstraction allows evolution without breaking workflow logic
- Start simple (in-memory), add complexity later (external storage)
- Interface makes it easy to test and mock

**Default Implementation:** In-memory (workflow state)
**Future Implementation:** External persistence (database, S3)

**Interface Design:**
```go
// ConversationHistory interface - pluggable storage backend
type ConversationHistory interface {
    // Core operations
    AddItem(item ConversationItem) error
    GetForPrompt() ([]ConversationItem, error)
    EstimateTokenCount() (int, error)

    // Admin operations
    DropLastNUserTurns(n int) error
    GetRawItems() ([]ConversationItem, error)

    // Query operations
    GetTurnCount() (int, error)
}

// Default: In-memory implementation
type InMemoryHistory struct {
    items []ConversationItem
}

func (h *InMemoryHistory) GetForPrompt() ([]ConversationItem, error) {
    return h.items, nil
}

// Future: External persistence implementation
type ExternalHistory struct {
    conversationID string
    client         HistoryStorageClient
}

func (h *ExternalHistory) GetForPrompt() ([]ConversationItem, error) {
    // Fetch from external storage
    return h.client.GetHistory(h.conversationID)
}
```

**TODO:** Research all uses of `raw_items()` in Codex to ensure they can be done in Activities when we move to external persistence. Current uses:
- Turn counting (query operation)
- History compaction (admin operation)
- Rollback operations (admin operation)
- Building compacted history (potentially expensive)

These operations may need to become Activities that read from external storage.

---

### Decision 6: Tool Registry Management

**Choice:** **A) Build once at workflow start**

**Rationale:**
- Simple initial implementation
- Tools consistent throughout conversation
- Passed through ContinueAsNew as part of state

**Future Enhancement (C):** Refresh on demand
- Signal handler to rebuild tool registry mid-conversation
- Use case: User adds new MCP server or custom tool

**Pattern:**
```go
type ToolRegistry struct {
    Tools map[string]Tool
}

func BuildToolRegistry(ctx context.Context) (*ToolRegistry, error) {
    registry := &ToolRegistry{Tools: make(map[string]Tool)}

    // Register built-in tools
    registry.Register("shell", &ShellTool{})
    registry.Register("read_file", &ReadFileTool{})
    registry.Register("write_file", &WriteFileTool{})

    // Discover MCP tools (Activity)
    var mcpTools []Tool
    err := workflow.ExecuteActivity(ctx, DiscoverMCPToolsActivity).Get(ctx, &mcpTools)
    if err != nil {
        return nil, err
    }

    for _, tool := range mcpTools {
        registry.Register(tool.Name(), tool)
    }

    return registry, nil
}
```

---

### Decision 7: Error Handling & Retries

**Choice:** **C) Hybrid - Temporal retries + custom error handling**

**Rationale:**
- Leverage Temporal's battle-tested retry policies for transient failures
- Custom error types for domain-specific handling (context overflow, rate limits)
- Best of both worlds: automatic retries + intelligent error handling

**Error Type Taxonomy:**
```go
type ActivityError struct {
    Type      ErrorType
    Retryable bool
    Message   string
    Details   map[string]interface{}
}

type ErrorType int
const (
    ErrorTypeTransient        ErrorType = iota // Network, timeout → Temporal retries
    ErrorTypeContextOverflow                   // Context window exceeded → ContinueAsNew
    ErrorTypeAPILimit                          // Rate limit → surface to user
    ErrorTypeToolFailure                       // Individual tool failed → continue workflow
    ErrorTypeFatal                            // Unrecoverable → stop workflow
)
```

**Activity Retry Policies:**
```go
// LLM Activity - moderate retries
llmActivityOptions := workflow.ActivityOptions{
    StartToCloseTimeout: 2 * time.Minute,
    RetryPolicy: &temporal.RetryPolicy{
        InitialInterval:    time.Second,
        BackoffCoefficient: 2.0,
        MaximumInterval:    30 * time.Second,
        MaximumAttempts:    3,
    },
}

// Tool Activity - more aggressive retries
toolActivityOptions := workflow.ActivityOptions{
    StartToCloseTimeout: 5 * time.Minute,
    RetryPolicy: &temporal.RetryPolicy{
        InitialInterval:    time.Second,
        BackoffCoefficient: 2.0,
        MaximumInterval:    time.Minute,
        MaximumAttempts:    5,
    },
}
```

**Workflow Error Handling:**
```go
func AgenticWorkflow(ctx workflow.Context, state WorkflowState) error {
    // Execute LLM activity
    var llmResult LLMActivityOutput
    err := workflow.ExecuteActivity(llmCtx, ExecuteLLMActivity, input).Get(ctx, &llmResult)

    if err != nil {
        var activityErr *ActivityError
        if errors.As(err, &activityErr) {
            switch activityErr.Type {
            case ErrorTypeContextOverflow:
                // Trigger ContinueAsNew
                return workflow.NewContinueAsNewError(ctx, AgenticWorkflow, state)
            case ErrorTypeAPILimit:
                // Wait and retry
                workflow.Sleep(ctx, time.Minute)
                continue
            case ErrorTypeFatal:
                // Stop workflow
                return err
            }
        }
        // Temporal will retry transient errors automatically
        return err
    }
}
```

---

### Decision 8: Cancellation Handling

**Choice:** **2) Immediate cancellation with context**

**Rationale:**
- User cancels → workflow context cancelled immediately
- In-flight activities receive cancellation signal
- Activities exit as soon as possible
- Responsive user experience

**Implementation:**
```go
// Workflow sets up cancellation
ctx, cancel := workflow.WithCancel(ctx)
defer cancel()

// Activities check context
func ExecuteLLMActivity(ctx context.Context, input LLMActivityInput) (LLMActivityOutput, error) {
    select {
    case <-ctx.Done():
        return LLMActivityOutput{}, ctx.Err()
    default:
        // Continue with LLM call
    }

    // During long operations, periodically check
    if ctx.Err() != nil {
        return LLMActivityOutput{}, ctx.Err()
    }
}

// Tool activities respect cancellation
func ExecuteShellActivity(ctx context.Context, input ShellInput) (ShellOutput, error) {
    cmd := exec.CommandContext(ctx, "bash", "-c", input.Command)
    output, err := cmd.CombinedOutput()

    if ctx.Err() != nil {
        return ShellOutput{}, ctx.Err()
    }

    return ShellOutput{Output: string(output)}, err
}
```

---

## Temporal Workflow Design

### Core Workflow Structure

```go
package workflow

import (
    "go.temporal.io/sdk/workflow"
)

// WorkflowInput is the initial input to start a conversation
type WorkflowInput struct {
    ConversationID string
    UserMessage    string
    ModelConfig    ModelConfig
    ToolsConfig    ToolsConfig
}

// WorkflowState is passed through ContinueAsNew
type WorkflowState struct {
    ConversationID string
    History        ConversationHistory
    ToolRegistry   *ToolRegistry
    ModelConfig    ModelConfig

    // Iteration tracking
    IterationCount int
    MaxIterations  int
}

// AgenticWorkflow is the main durable agentic loop
func AgenticWorkflow(ctx workflow.Context, input WorkflowInput) error {
    // Initialize state on first run
    state := WorkflowState{
        ConversationID: input.ConversationID,
        History:        NewInMemoryHistory(),
        ModelConfig:    input.ModelConfig,
        MaxIterations:  20,
    }

    // Build tool registry
    registry, err := BuildToolRegistry(ctx, input.ToolsConfig)
    if err != nil {
        return err
    }
    state.ToolRegistry = registry

    // Add initial user message to history
    state.History.AddItem(ConversationItem{
        Type:    ItemTypeUserMessage,
        Content: input.UserMessage,
    })

    // Run the agentic loop
    return runAgenticLoop(ctx, state)
}

// AgenticWorkflowContinued handles ContinueAsNew
func AgenticWorkflowContinued(ctx workflow.Context, state WorkflowState) error {
    return runAgenticLoop(ctx, state)
}

// runAgenticLoop is the main loop logic
func runAgenticLoop(ctx workflow.Context, state WorkflowState) error {
    for state.IterationCount < state.MaxIterations {
        // Get history for prompt
        historyItems, err := state.History.GetForPrompt()
        if err != nil {
            return err
        }

        // Call LLM Activity
        llmInput := LLMActivityInput{
            History:     historyItems,
            ModelConfig: state.ModelConfig,
            Tools:       state.ToolRegistry.GetToolSpecs(),
        }

        var llmResult LLMActivityOutput
        err = workflow.ExecuteActivity(
            workflow.WithActivityOptions(ctx, getLLMActivityOptions()),
            ExecuteLLMActivity,
            llmInput,
        ).Get(ctx, &llmResult)

        if err != nil {
            return handleError(ctx, state, err)
        }

        // Add LLM response to history
        state.History.AddItem(ConversationItem{
            Type:      ItemTypeAssistantMessage,
            Content:   llmResult.Content,
            ToolCalls: llmResult.ToolCalls,
        })

        // Execute tools if present
        if len(llmResult.ToolCalls) > 0 {
            toolResults, err := executeTools(ctx, state, llmResult.ToolCalls)
            if err != nil {
                return err
            }

            // Add all tool results to history
            for _, result := range toolResults {
                state.History.AddItem(ConversationItem{
                    Type:    ItemTypeToolResult,
                    ToolID:  result.ToolID,
                    Content: result.Output,
                })
            }
        }

        state.IterationCount++

        // Check if we need follow-up
        if !llmResult.NeedsFollowUp {
            break
        }

        // Check token count and consider ContinueAsNew
        tokenCount, _ := state.History.EstimateTokenCount()
        if tokenCount > state.ModelConfig.ContextWindow*0.8 {
            state.IterationCount = 0
            return workflow.NewContinueAsNewError(ctx, AgenticWorkflowContinued, state)
        }
    }

    // Max iterations reached, continue as new
    if state.IterationCount >= state.MaxIterations {
        state.IterationCount = 0
        return workflow.NewContinueAsNewError(ctx, AgenticWorkflowContinued, state)
    }

    return nil
}

// executeTools runs all tool activities in parallel and waits for all
func executeTools(ctx workflow.Context, state WorkflowState, toolCalls []ToolCall) ([]ToolResult, error) {
    futures := make([]workflow.Future, len(toolCalls))

    // Start all tool activities (parallel execution)
    for i, toolCall := range toolCalls {
        toolActivity := state.ToolRegistry.GetActivity(toolCall.Name)
        futures[i] = workflow.ExecuteActivity(
            workflow.WithActivityOptions(ctx, getToolActivityOptions()),
            toolActivity,
            toolCall.Input,
        )
    }

    // Wait for ALL tools to complete
    results := make([]ToolResult, len(toolCalls))
    for i, future := range futures {
        var result ToolResult
        if err := future.Get(ctx, &result); err != nil {
            // Individual tool failure - record error as result
            results[i] = ToolResult{
                ToolID: toolCalls[i].ID,
                Error:  err.Error(),
            }
        } else {
            results[i] = result
        }
    }

    return results, nil
}
```

### Query Handlers

```go
// GetHistoryQuery returns current conversation history
func (w *AgenticWorkflow) GetHistory(ctx workflow.Context) ([]ConversationItem, error) {
    return w.state.History.GetForPrompt()
}

// GetStatusQuery returns current workflow status
func (w *AgenticWorkflow) GetStatus(ctx workflow.Context) (WorkflowStatus, error) {
    return WorkflowStatus{
        ConversationID:  w.state.ConversationID,
        IterationCount:  w.state.IterationCount,
        MessageCount:    w.state.History.GetTurnCount(),
        LastActivity:    workflow.Now(ctx),
    }, nil
}
```

### Signal Handlers

```go
// AddUserMessageSignal adds a new user message mid-workflow
func (w *AgenticWorkflow) AddUserMessage(ctx workflow.Context, message string) error {
    return w.state.History.AddItem(ConversationItem{
        Type:    ItemTypeUserMessage,
        Content: message,
    })
}

// Future: RefreshToolsSignal reloads tool registry
func (w *AgenticWorkflow) RefreshTools(ctx workflow.Context) error {
    // TODO: Implement tool registry refresh
    return nil
}
```

---

## Implementation Guide

### Project Structure

```
codex-temporal-go/
├── cmd/
│   ├── worker/          # Temporal worker executable
│   └── client/          # CLI client for starting workflows
├── internal/
│   ├── workflow/        # Workflow definitions
│   │   ├── agentic.go
│   │   └── state.go
│   ├── activities/      # Activity implementations
│   │   ├── llm.go
│   │   ├── tools/
│   │   │   ├── shell.go
│   │   │   ├── file.go
│   │   │   └── patch.go
│   │   └── mcp.go
│   ├── history/         # History interface & implementations
│   │   ├── interface.go
│   │   ├── memory.go
│   │   └── external.go  # Future
│   ├── tools/           # Tool registry
│   │   ├── registry.go
│   │   ├── spec.go
│   │   └── types.go
│   ├── models/          # Shared types
│   │   ├── conversation.go
│   │   ├── errors.go
│   │   └── config.go
│   └── llm/             # LLM client
│       ├── client.go
│       └── openai.go    # Using github.com/openai/openai-go
├── docs/
│   ├── ARCHITECTURE.md  # This document
│   └── API.md           # API documentation
├── go.mod
└── README.md
```

**IMPORTANT: Structural Alignment with Codex**

This project structure mirrors the original Codex repository (`codex-rs/core/src/`) as closely as possible:

| Codex (Rust) | codex-temporal-go (Go) | Purpose |
|--------------|------------------------|---------|
| `codex.rs` | `workflow/agentic.go` | Main agentic loop |
| `state/session.rs` | `workflow/state.go` | Session state management |
| `client.rs` | `activities/llm.go` + `llm/client.go` | LLM client integration |
| `tools/router.rs` | `tools/registry.go` | Tool dispatch |
| `tools/parallel.rs` | Workflow futures pattern | Parallel execution |
| `tools/handlers/*` | `activities/tools/*` | Tool implementations |
| `message_history.rs` | `history/manager.go` | History management |
| `mcp_connection_manager.rs` | `activities/mcp.go` | MCP integration |

**Rationale:** Maintaining structural alignment enables:
- Easy incorporation of future Codex changes
- Side-by-side code comparison during development
- Feature parity tracking
- Better long-term maintainability

**See:** `../CLAUDE.md` for comprehensive guidelines on maintaining structural alignment.

### Implementation Phases

#### Phase 1: Core Loop (MVP)
- [ ] Define workflow state structures
- [ ] Implement in-memory history
- [ ] Basic LLM activity (OpenAI API using github.com/openai/openai-go)
- [ ] Simple tool registry (shell, read_file)
- [ ] Main agentic loop workflow
- [ ] ContinueAsNew logic
- [ ] Basic error handling

#### Phase 2: Tool Execution
- [ ] Implement all built-in tools
  - [ ] Shell execution
  - [ ] File operations (read, write)
  - [ ] Apply patch
- [ ] Parallel tool execution with futures
- [ ] Tool-specific retry policies
- [ ] Tool error handling

#### Phase 3: Observability
- [ ] Query handlers (history, status)
- [ ] Signal handlers (add message)
- [ ] Logging and metrics
- [ ] Temporal UI dashboard

#### Phase 4: Production Readiness
- [ ] External history storage
- [ ] MCP tool discovery
- [ ] Advanced error recovery
- [ ] Performance optimization
- [ ] Integration tests

---

## Future Enhancements

### Streaming Support (From Decision 1C)

**Goal:** Stream LLM responses in real-time to UI while maintaining workflow durability

**Approach:**
- Activity calls LLM and streams via callback/webhook
- Workflow tracks "LLM call in progress"
- Activity returns final result for durability

**Implementation:**
```go
type LLMActivityInput struct {
    History       []ConversationItem
    ModelConfig   ModelConfig
    Tools         []ToolSpec
    StreamWebhook string // URL to stream deltas
}

func ExecuteLLMActivity(ctx context.Context, input LLMActivityInput) (LLMActivityOutput, error) {
    client := openai.NewClient(...)

    // Stream to webhook
    stream := client.Chat.Completions.NewStreaming(...)
    for delta := range stream {
        // POST delta to webhook (fire-and-forget)
        go postWebhook(input.StreamWebhook, delta)
    }

    // Return complete result for durability
    return LLMActivityOutput{
        Content:   fullContent,
        ToolCalls: detectedToolCalls,
    }, nil
}
```

### Tool Registry Refresh (From Decision 6C)

**Goal:** Add/remove tools mid-conversation without restarting workflow

**Approach:**
- Signal handler to trigger registry rebuild
- Activity discovers new tools
- Update workflow state with new registry

**Implementation:**
```go
func (w *AgenticWorkflow) RefreshToolsSignal(ctx workflow.Context, config ToolsConfig) error {
    // Activity to discover tools
    var newRegistry *ToolRegistry
    err := workflow.ExecuteActivity(ctx, BuildToolRegistryActivity, config).Get(ctx, &newRegistry)
    if err != nil {
        return err
    }

    // Update state
    w.state.ToolRegistry = newRegistry
    return nil
}
```

### External History Storage

**Goal:** Scale to very long conversations without workflow state bloat

**Approach:**
- Implement `ExternalHistory` with database/S3 backend
- Workflow only stores conversation ID + metadata
- Activities load history on-demand

**TODO Items:**
- Research all uses of `raw_items()` in Codex
- Determine which operations must be Activities:
  - Turn counting → Activity
  - History compaction → Activity (expensive)
  - Rollback → Activity or workflow signal?
  - Building compacted history → Activity

**Interface (already designed):**
```go
type ExternalHistory struct {
    conversationID string
    client         HistoryStorageClient
}

func (h *ExternalHistory) GetForPrompt() ([]ConversationItem, error) {
    return h.client.GetHistory(ctx, h.conversationID)
}

func (h *ExternalHistory) AddItem(item ConversationItem) error {
    return h.client.AppendItem(ctx, h.conversationID, item)
}
```

### Advanced Error Recovery

**Enhancements:**
- Retry quotas per error type
- Circuit breakers for failing tools
- Fallback LLM models on rate limits
- Partial tool result handling

### MCP Tool Integration

**Goal:** Full Model Context Protocol support like Codex

**Components:**
- MCP server discovery activity
- MCP tool specification parsing
- Dynamic tool registration
- MCP connection lifecycle management

---

## References

### Codex Codebase Analysis

**Key Files Explored:**
- `codex-rs/core/src/codex.rs` - Main agentic loop (lines 3267-4421)
- `codex-rs/core/src/tools/router.rs` - Tool dispatch
- `codex-rs/core/src/tools/parallel.rs` - Parallel execution
- `codex-rs/core/src/state/session.rs` - Session state management
- `codex-rs/core/src/client.rs` - LLM client integration

**Agent Exploration Report:** See exploration agent output (agent ID: a40f74f)

### Temporal Documentation

- [Temporal Go SDK](https://docs.temporal.io/dev-guide/go)
- [Workflow Patterns](https://docs.temporal.io/develop/go/core-application)
- [Activity Best Practices](https://docs.temporal.io/activities)
- [ContinueAsNew](https://docs.temporal.io/workflows#continue-as-new)

### Related Projects

- [Codex (OpenAI)](https://github.com/openai/codex) - Original reference implementation
- [OpenAI Go SDK](https://github.com/openai/openai-go) - Official Go client for OpenAI API
- [Temporal Samples](https://github.com/temporalio/samples-go) - Example workflows

---

## Decision Summary

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| 1 | LLM Execution | Buffered in Activity | Simple, Temporal-native, streaming as future enhancement |
| 2 | Tool Mapping | Each tool = Activity | Fine-grained control, better observability |
| 3 | Parallel Execution | Activity Futures, wait for ALL | Matches Codex, leverages Temporal's async |
| 4 | Loop Continuation | ContinueAsNew after N iterations | Avoids history bloat, clean boundaries |
| 5 | History Storage | Interface: in-memory default, external future | Flexible, start simple, evolve later |
| 6 | Tool Registry | Build once at start, refresh as enhancement | Simple initial, extensible later |
| 7 | Error Handling | Hybrid Temporal + custom errors | Leverage platform + domain logic |
| 8 | Cancellation | Immediate with context | Responsive UX, graceful cleanup |

---

**Next Steps:**
1. Initialize Go module and dependencies
2. Implement Phase 1 (Core Loop MVP)
3. Write integration tests with Temporal test server
4. Iterate based on learnings

---

*Document maintained by: Codex-Temporal-Go Team*
*Last updated: 2026-02-07*
