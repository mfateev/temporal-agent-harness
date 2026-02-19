// Worker executable for temporal-agent-harness
//
// This starts a Temporal worker that executes workflows and activities.
package main

import (
	"log"
	"os"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/mfateev/temporal-agent-harness/internal/activities"
	"github.com/mfateev/temporal-agent-harness/internal/execsession"
	"github.com/mfateev/temporal-agent-harness/internal/llm"
	"github.com/mfateev/temporal-agent-harness/internal/temporalclient"
	"github.com/mfateev/temporal-agent-harness/internal/tools"
	"github.com/mfateev/temporal-agent-harness/internal/tools/handlers"
	"github.com/mfateev/temporal-agent-harness/internal/version"
	"github.com/mfateev/temporal-agent-harness/internal/workflow"
)

const (
	TaskQueue = "temporal-agent-harness"
)

func main() {
	// Check for at least one LLM provider API key
	hasOpenAI := os.Getenv("OPENAI_API_KEY") != ""
	hasAnthropic := os.Getenv("ANTHROPIC_API_KEY") != ""

	if !hasOpenAI && !hasAnthropic {
		log.Fatal("At least one LLM provider API key is required: OPENAI_API_KEY or ANTHROPIC_API_KEY")
	}

	if hasOpenAI {
		log.Println("OpenAI provider available")
	}
	if hasAnthropic {
		log.Println("Anthropic provider available")
	}

	// Load Temporal client options via envconfig (supports env vars, config files, TLS)
	opts := temporalclient.MustLoadClientOptions("", "")

	c, err := client.Dial(opts)
	if err != nil {
		log.Fatalf("Failed to create Temporal client: %v", err)
	}
	defer c.Close()

	// Create worker
	w := worker.New(c, TaskQueue, worker.Options{})

	// Register workflows
	w.RegisterWorkflow(workflow.AgenticWorkflow)
	w.RegisterWorkflow(workflow.AgenticWorkflowContinued)
	w.RegisterWorkflow(workflow.HarnessWorkflow)
	w.RegisterWorkflow(workflow.HarnessWorkflowContinued)

	// Create tool registry with handlers
	// Maps to: codex-rs/core/src/tools/registry.rs ToolRegistry setup
	toolRegistry := tools.NewToolRegistry()
	toolRegistry.Register(handlers.NewShellHandler())        // array-based "shell"
	toolRegistry.Register(handlers.NewShellCommandHandler()) // string-based "shell_command"
	toolRegistry.Register(handlers.NewReadFileTool())
	toolRegistry.Register(handlers.NewWriteFileTool())
	toolRegistry.Register(handlers.NewListDirTool())
	toolRegistry.Register(handlers.NewGrepFilesTool())
	toolRegistry.Register(handlers.NewApplyPatchTool())

	// Unified exec: interactive PTY/pipe sessions (exec_command + write_stdin)
	execStore := execsession.NewStore()
	toolRegistry.Register(handlers.NewExecCommandHandler(execStore))
	toolRegistry.Register(handlers.NewWriteStdinHandler(execStore))

	log.Printf("Registered %d tools", toolRegistry.ToolCount())

	// Create multi-provider LLM client (supports both OpenAI and Anthropic)
	llmClient := llm.NewMultiProviderClient()

	// Register activities
	llmActivities := activities.NewLLMActivities(llmClient)
	w.RegisterActivity(llmActivities.ExecuteLLMCall)
	w.RegisterActivity(llmActivities.ExecuteCompact)
	w.RegisterActivity(llmActivities.GenerateSuggestions)

	toolActivities := activities.NewToolActivities(toolRegistry)
	w.RegisterActivity(toolActivities.ExecuteTool)

	instructionActivities := activities.NewInstructionActivities()
	w.RegisterActivity(instructionActivities.LoadWorkerInstructions)
	w.RegisterActivity(instructionActivities.LoadPersonalInstructions)
	w.RegisterActivity(instructionActivities.LoadExecPolicy)

	// Start worker
	log.Printf("Worker version: %s", version.GitCommit)
	log.Printf("Starting worker on task queue: %s", TaskQueue)
	if opts.HostPort != "" {
		log.Printf("Temporal server: %s", opts.HostPort)
	}

	err = w.Run(worker.InterruptCh())
	if err != nil {
		log.Fatalf("Failed to start worker: %v", err)
	}

	log.Println("Worker stopped")
}
