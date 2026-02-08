// CLI client for starting codex-temporal-go workflows
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/client"

	"github.com/mfateev/codex-temporal-go/internal/models"
	"github.com/mfateev/codex-temporal-go/internal/workflow"
)

const (
	TaskQueue = "codex-temporal"
)

func main() {
	// Parse command-line flags
	message := flag.String("message", "", "User message to send to the agent (required)")
	model := flag.String("model", "gpt-4o-mini", "LLM model to use (default: gpt-4o-mini)")
	enableShell := flag.Bool("enable-shell", true, "Enable shell tool")
	enableReadFile := flag.Bool("enable-read-file", true, "Enable read_file tool")
	flag.Parse()

	if *message == "" {
		log.Fatal("Error: --message is required\n\nUsage: client --message \"Your message here\"")
	}

	// Create Temporal client
	c, err := client.Dial(client.Options{
		HostPort: client.DefaultHostPort, // localhost:7233
	})
	if err != nil {
		log.Fatalf("Failed to create Temporal client: %v", err)
	}
	defer c.Close()

	// Generate workflow ID
	workflowID := fmt.Sprintf("codex-%s", uuid.New().String()[:8])

	// Determine working directory
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}

	// Prepare workflow input
	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		UserMessage:    *message,
		Config: models.SessionConfiguration{
			Model: models.ModelConfig{
				Model:         *model,
				Temperature:   0.7,
				MaxTokens:     4096,
				ContextWindow: 128000,
			},
			Tools: models.ToolsConfig{
				EnableShell:    *enableShell,
				EnableReadFile: *enableReadFile,
			},
			Cwd:           cwd,
			SessionSource: "cli",
		},
	}

	// Start workflow
	log.Printf("Starting workflow: %s", workflowID)
	log.Printf("Message: %s", *message)
	log.Printf("Model: %s", *model)
	log.Printf("Tools enabled: shell=%v, read_file=%v", *enableShell, *enableReadFile)

	workflowOptions := client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: TaskQueue,
	}

	ctx := context.Background()
	run, err := c.ExecuteWorkflow(ctx, workflowOptions, "AgenticWorkflow", input)
	if err != nil {
		log.Fatalf("Failed to start workflow: %v", err)
	}

	log.Printf("Workflow started successfully")
	log.Printf("Workflow ID: %s", workflowID)
	log.Printf("Run ID: %s", run.GetRunID())
	log.Printf("Temporal UI: http://localhost:8233/namespaces/default/workflows/%s", workflowID)
	log.Println()
	log.Println("Waiting for workflow to complete...")

	// Wait for workflow to complete (with timeout)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	var result workflow.WorkflowResult
	err = run.Get(ctx, &result)
	if err != nil {
		log.Fatalf("Workflow execution failed: %v", err)
	}

	// Print results
	log.Println()
	log.Println("=== Workflow Completed ===")
	log.Printf("Conversation ID: %s", result.ConversationID)
	log.Printf("Total Iterations: %d", result.TotalIterations)
	log.Printf("Total Tokens: %d", result.TotalTokens)
	log.Printf("Tools Executed: %v", result.ToolCallsExecuted)
	log.Println()
	log.Printf("View full history in Temporal UI: http://localhost:8233/namespaces/default/workflows/%s", workflowID)
}
