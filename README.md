# codex-temporal-go

A Temporal-friendly reimplementation of Codex's agentic loop in Go, leveraging Temporal's durable execution model for LLM-driven tool calling.

## Overview

This project implements an agentic workflow system that:
- Executes LLM calls via OpenAI API
- Calls tools (shell, read_file) based on LLM responses
- Maintains conversation history
- Uses Temporal for durable execution with ContinueAsNew
- Survives worker restarts and failures

**Status:** Phase 1 (MVP) - Core agentic loop with basic tools

## Architecture

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for detailed architectural decisions and design.

**Key Features:**
- **Durable Execution**: Workflows survive process restarts and failures
- **Parallel Tool Execution**: Multiple tools execute concurrently
- **Context Management**: Automatic handling of context window limits via ContinueAsNew
- **Structural Alignment**: Follows original Codex repository structure for maintainability

## Prerequisites

1. **Go 1.21+**
2. **Temporal Server** - Install from https://docs.temporal.io/cli
3. **OpenAI API Key** - Get from https://platform.openai.com/api-keys

## Quick Start

### 1. Start Temporal Server

```bash
# In terminal 1
temporal server start-dev
```

This starts Temporal server on `localhost:7233` with Web UI on `http://localhost:8233`.

### 2. Set OpenAI API Key

```bash
export OPENAI_API_KEY=sk-your-key-here
```

### 3. Start Worker

```bash
# In terminal 2
go run cmd/worker/main.go
```

You should see:
```
Registered 2 tools:
  - shell: Execute a shell command and return the output...
  - read_file: Read the contents of a file...
Starting worker on task queue: codex-temporal
```

### 4. Run Client

```bash
# In terminal 3
go run cmd/client/main.go --message "Use shell to run 'ls -la' and tell me what you see"
```

The client will:
1. Start a workflow on Temporal
2. Print the workflow ID and Temporal UI link
3. Wait for completion
4. Show results (iterations, tokens used, tools executed)

### 5. View in Temporal UI

Open http://localhost:8233 to see:
- Workflow execution history
- Activity executions (LLM calls, tool executions)
- Input/output for each step
- Retry attempts and errors

## Examples

### Simple conversation (no tools)

```bash
go run cmd/client/main.go \
  --message "Say hello in exactly 3 words" \
  --enable-shell=false \
  --enable-read-file=false
```

### Shell command execution

```bash
go run cmd/client/main.go \
  --message "Use shell to check the current directory and list files"
```

### File reading

```bash
go run cmd/client/main.go \
  --message "Read the file README.md and summarize its contents"
```

### Multi-turn conversation

```bash
go run cmd/client/main.go \
  --message "Create a file /tmp/test.txt with 'Hello World', then read it back"
```

### Using a different model

```bash
go run cmd/client/main.go \
  --model gpt-4 \
  --message "Analyze this codebase structure"
```

## CLI Options

```bash
go run cmd/client/main.go [options]

Options:
  --message string           User message (required)
  --model string             LLM model (default: gpt-4o-mini)
  --enable-shell bool        Enable shell tool (default: true)
  --enable-read-file bool    Enable read_file tool (default: true)
```

## Testing

### Unit Tests (Fast)

```bash
# Run unit tests only (no external services required)
go test -short ./...
```

### E2E Tests (Real Services)

**Prerequisites:**
1. Temporal server running (`temporal server start-dev`)
2. Worker running (`go run cmd/worker/main.go`)
3. OpenAI API key set

```bash
# Run E2E tests
export OPENAI_API_KEY=sk-...
go test -v ./e2e/...
```

**E2E Tests:**
- `TestAgenticWorkflow_SingleTurn` - Simple conversation without tools
- `TestAgenticWorkflow_WithShellTool` - LLM calls shell tool
- `TestAgenticWorkflow_MultiTurn` - Multi-step task with multiple tools
- `TestAgenticWorkflow_ReadFile` - File reading with read_file tool

**Note:** E2E tests use real OpenAI API (costs money). Tests use `gpt-4o-mini` for cost efficiency.

## Project Structure

```
codex-temporal-go/
├── cmd/
│   ├── worker/          # Temporal worker executable
│   └── client/          # CLI client for starting workflows
├── internal/
│   ├── workflow/        # Workflow definitions (agentic loop)
│   ├── activities/      # Activity implementations (LLM, tools)
│   ├── history/         # Conversation history management
│   ├── tools/           # Tool registry and implementations
│   ├── models/          # Shared types
│   └── llm/             # LLM client (OpenAI)
├── e2e/                 # End-to-end tests
├── docs/
│   └── ARCHITECTURE.md  # Detailed architecture documentation
├── go.mod
└── README.md
```

## How It Works

### Workflow Flow

1. **Start Workflow**: Client sends user message to workflow
2. **LLM Call**: Workflow executes LLM activity with conversation history
3. **Tool Calls**: If LLM requests tools, execute ALL in parallel
4. **Tool Results**: Add tool outputs to history
5. **Repeat**: Loop until LLM responds without tool calls
6. **ContinueAsNew**: If max iterations reached or context full, restart workflow with history

### Parallel Tool Execution

```
LLM Response: "Call tool1, tool2, tool3"
    ↓
Execute ALL tools in parallel (Temporal futures)
    ↓
Wait for ALL to complete
    ↓
Add ALL results to history together
    ↓
Next LLM call sees all tool results at once
```

This matches Codex behavior exactly.

### Error Handling

- **Transient errors** (network, timeouts): Temporal auto-retries
- **Context overflow**: Triggers ContinueAsNew
- **API rate limits**: Sleep and retry
- **Tool failures**: Recorded as tool error, workflow continues
- **Fatal errors**: Stop workflow

## Development

### Adding a New Tool

1. **Create tool handler** in `internal/tools/`:

```go
type MyTool struct{}

func (t *MyTool) Name() string { return "my_tool" }

func (t *MyTool) Execute(args map[string]interface{}) (string, error) {
    // Implementation
}
```

2. **Create tool spec** in `internal/tools/spec.go`:

```go
func NewMyToolSpec() ToolSpec {
    return ToolSpec{
        Name: "my_tool",
        Description: "What this tool does",
        Parameters: []ToolParameter{
            {Name: "arg1", Type: "string", Description: "...", Required: true},
        },
    }
}
```

3. **Register in worker** (`cmd/worker/main.go`):

```go
toolRegistry.Register(tools.NewMyTool(), tools.NewMyToolSpec())
```

4. **Add to config** (`internal/models/config.go`):

```go
type ToolsConfig struct {
    EnableMyTool bool `json:"enable_my_tool,omitempty"`
}
```

5. **Wire up in workflow** (`internal/workflow/agentic.go`):

```go
if config.EnableMyTool {
    specs = append(specs, tools.NewMyToolSpec())
}
```

### Running Locally

```bash
# Terminal 1: Temporal server
temporal server start-dev

# Terminal 2: Worker
export OPENAI_API_KEY=sk-...
go run cmd/worker/main.go

# Terminal 3: Test manually
export OPENAI_API_KEY=sk-...
go run cmd/client/main.go --message "Your test message"

# Or run E2E tests
go test -v ./e2e/...
```

## Troubleshooting

### "Failed to connect to Temporal server"

Make sure Temporal server is running:
```bash
temporal server start-dev
```

### "Workflow execution timeout"

Increase timeout in `cmd/client/main.go` or E2E tests.

### "No workers available"

Make sure worker is running:
```bash
go run cmd/worker/main.go
```

### "OPENAI_API_KEY not set"

Export your API key:
```bash
export OPENAI_API_KEY=sk-your-key-here
```

### E2E tests fail with rate limit

OpenAI rate limits may cause failures. Wait a minute and retry, or use a different API key.

## Roadmap

### Phase 1 (MVP) ✅
- [x] Core agentic loop workflow
- [x] LLM integration (OpenAI)
- [x] Basic tools (shell, read_file)
- [x] Parallel tool execution
- [x] ContinueAsNew for long conversations
- [x] E2E tests with real services

### Phase 2 (Next)
- [ ] Additional tools (write_file, apply_patch)
- [ ] Workflow queries and signals
- [ ] History compression strategies
- [ ] Better error recovery
- [ ] Worker restart tests

### Phase 3 (Future)
- [ ] External history storage (PostgreSQL/S3)
- [ ] MCP tool integration
- [ ] Streaming LLM responses via webhooks
- [ ] Advanced context management
- [ ] Multi-turn conversation optimization

## Contributing

This project follows the original Codex repository structure. When adding features:

1. Check corresponding Codex files in `codex-rs/core/src/`
2. Maintain structural alignment where possible
3. Document divergences with comments
4. Add E2E tests for new functionality

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for detailed guidelines.

## License

[To be determined - check with project owner]

## References

- [Temporal Documentation](https://docs.temporal.io/)
- [OpenAI API Documentation](https://platform.openai.com/docs)
- [Original Codex Project](https://github.com/anthropics/codex) (if public)

---

**Questions?** Open an issue or check the [architecture documentation](docs/ARCHITECTURE.md).
