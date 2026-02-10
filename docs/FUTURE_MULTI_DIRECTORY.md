# Future Enhancement: Multi-Directory Architecture

## Overview

The multi-directory enhancement extends the single-root session model to support
N directories per session, each with its own sandboxed worker and task queue.

## Current State (Single-Root)

- One `Cwd` per session
- One `SessionTaskQueue` (defaults to shared queue)
- One `LoadWorkerInstructions` call at session start
- Tools execute on the session task queue (or default)

## Proposed Architecture

### SessionDir Type

```go
type SessionDir struct {
    Label     string // Human label (e.g., "frontend", "backend")
    RootPath  string // Absolute path to root of this directory
    SubDir    string // Subdirectory within root (for cwd context)
    Mode      string // "rw" (read-write) or "ro" (read-only)
    TaskQueue string // Worker task queue for this directory
}
```

### Per-Directory Workers

Each directory gets its own sandboxed worker process and task queue:
- Worker for dir `frontend` listens on `session-abc-frontend`
- Worker for dir `backend` listens on `session-abc-backend`
- Orchestrator routes tool calls to the correct worker based on file path

### Tool Call Routing

```go
func routeToolCall(dirs []SessionDir, toolName string, args map[string]interface{}) string {
    // Match file paths in tool arguments to directory roots
    // Return the task queue for the matching directory
}
```

- `read_file("/app/frontend/src/App.tsx")` routes to `frontend` worker
- `shell("cd /app/backend && go test")` routes to `backend` worker
- Ambiguous calls default to the primary (first) directory

### Access Control

- Read-only directories reject `write_file`, `apply_patch`, and mutating shell commands
- Enforcement happens at the workflow level before dispatching to workers
- Denied operations return structured error to LLM

### Per-Directory Instructions

Each directory contributes its own AGENTS.md files:
```go
func MergeMultiDirInstructions(dirs []SessionDir, docsPerDir map[string]string) MergedInstructions
```

### Environment Context

```xml
<environment_context>
  <directories>
    <dir label="frontend" mode="rw">/app/frontend</dir>
    <dir label="backend" mode="ro">/app/backend</dir>
  </directories>
  <shell>bash</shell>
</environment_context>
```

### CLI Flag

```
cli --add-dir frontend:/app/frontend --add-dir backend:/app/backend:ro
```

## Building on Current Foundation

The single-root implementation provides all the building blocks:
- `SessionTaskQueue` generalizes to per-directory queues
- `LoadWorkerInstructions` generalizes to per-directory instruction loading
- `MergeInstructions` generalizes to N instruction sources
- `BuildEnvironmentContext` generalizes to multi-directory XML
- `executeToolsInParallel` already supports `sessionTaskQueue` parameter

## Future Enhancement: Re-evaluate AGENTS.md on `cd`

When the shell changes the working directory:

1. **Detect `cd`**: Parse shell tool output or track `working_directory` param
2. **Re-run `LoadWorkerInstructions`**: With new cwd
3. **Update instructions**: Re-merge with new worker-side project docs
4. **Update environment context**: New `<environment_context>` user message
5. **Track state**: Store effective cwd separately from initial `Config.Cwd`
