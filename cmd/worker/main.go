// Worker executable for codex-temporal-go
//
// This starts a Temporal worker that executes workflows and activities.
package main

import (
	"log"
	"os"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/mfateev/codex-temporal-go/internal/activities"
	"github.com/mfateev/codex-temporal-go/internal/llm"
	"github.com/mfateev/codex-temporal-go/internal/tools"
	"github.com/mfateev/codex-temporal-go/internal/tools/handlers"
	"github.com/mfateev/codex-temporal-go/internal/workflow"
)

const (
	TaskQueue = "codex-temporal"
)

func main() {
	// Check for OpenAI API key
	if os.Getenv("OPENAI_API_KEY") == "" {
		log.Fatal("OPENAI_API_KEY environment variable is required")
	}

	// Create Temporal client
	c, err := client.Dial(client.Options{
		HostPort: client.DefaultHostPort, // localhost:7233
	})
	if err != nil {
		log.Fatalf("Failed to create Temporal client: %v", err)
	}
	defer c.Close()

	// Create worker
	w := worker.New(c, TaskQueue, worker.Options{})

	// Register workflows
	w.RegisterWorkflow(workflow.AgenticWorkflow)
	w.RegisterWorkflow(workflow.AgenticWorkflowContinued)

	// Create tool registry with handlers
	// Maps to: codex-rs/core/src/tools/registry.rs ToolRegistry setup
	toolRegistry := tools.NewToolRegistry()
	toolRegistry.Register(handlers.NewShellTool())
	toolRegistry.Register(handlers.NewReadFileTool())

	log.Printf("Registered %d tools", toolRegistry.ToolCount())

	// Create LLM client
	llmClient := llm.NewOpenAIClient()

	// Register activities
	llmActivities := activities.NewLLMActivities(llmClient)
	w.RegisterActivity(llmActivities.ExecuteLLMCall)

	toolActivities := activities.NewToolActivities(toolRegistry)
	w.RegisterActivity(toolActivities.ExecuteTool)

	// Start worker
	log.Printf("Starting worker on task queue: %s", TaskQueue)
	log.Printf("Temporal server: %s", client.DefaultHostPort)

	err = w.Run(worker.InterruptCh())
	if err != nil {
		log.Fatalf("Failed to start worker: %v", err)
	}

	log.Println("Worker stopped")
}
