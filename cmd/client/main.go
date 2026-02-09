// CLI client for codex-temporal-go workflows.
//
// Sub-commands:
//
//	start    --message "..."         Start a new workflow, print workflow ID
//	send     --workflow-id <id> --message "..."  Send a user_input Update
//	history  --workflow-id <id>      Query conversation history
//	interrupt --workflow-id <id>     Send interrupt Update
//	end      --workflow-id <id>      Send shutdown Update
package main

import (
	"context"
	"encoding/json"
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
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	subcommand := os.Args[1]
	switch subcommand {
	case "start":
		cmdStart(os.Args[2:])
	case "send":
		cmdSend(os.Args[2:])
	case "history":
		cmdHistory(os.Args[2:])
	case "interrupt":
		cmdInterrupt(os.Args[2:])
	case "end":
		cmdEnd(os.Args[2:])
	default:
		log.Fatalf("Unknown sub-command: %s\n\n", subcommand)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage: client <command> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  start      Start a new agentic workflow")
	fmt.Fprintln(os.Stderr, "  send       Send a user message to a running workflow")
	fmt.Fprintln(os.Stderr, "  history    Query conversation history")
	fmt.Fprintln(os.Stderr, "  interrupt  Interrupt the current turn")
	fmt.Fprintln(os.Stderr, "  end        Shutdown the workflow")
}

func dialTemporal() client.Client {
	c, err := client.Dial(client.Options{
		HostPort: client.DefaultHostPort,
	})
	if err != nil {
		log.Fatalf("Failed to create Temporal client: %v", err)
	}
	return c
}

// cmdStart starts a new agentic workflow.
func cmdStart(args []string) {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	message := fs.String("message", "", "User message to send to the agent (required)")
	model := fs.String("model", "gpt-4o-mini", "LLM model to use")
	enableShell := fs.Bool("enable-shell", true, "Enable shell tool")
	enableReadFile := fs.Bool("enable-read-file", true, "Enable read_file tool")
	fs.Parse(args)

	if *message == "" {
		log.Fatal("Error: --message is required\n\nUsage: client start --message \"Your message here\"")
	}

	c := dialTemporal()
	defer c.Close()

	workflowID := fmt.Sprintf("codex-%s", uuid.New().String()[:8])

	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}

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

	log.Printf("Starting workflow: %s", workflowID)
	log.Printf("Message: %s", *message)

	ctx := context.Background()
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: TaskQueue,
	}, "AgenticWorkflow", input)
	if err != nil {
		log.Fatalf("Failed to start workflow: %v", err)
	}

	log.Printf("Workflow started successfully")
	log.Printf("Workflow ID: %s", workflowID)
	log.Printf("Run ID: %s", run.GetRunID())
	log.Printf("Temporal UI: http://localhost:8233/namespaces/default/workflows/%s", workflowID)

	// Print workflow ID on stdout for scripting
	fmt.Println(workflowID)
}

// cmdSend sends a user_input Update to a running workflow.
func cmdSend(args []string) {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	workflowID := fs.String("workflow-id", "", "Workflow ID (required)")
	message := fs.String("message", "", "User message (required)")
	fs.Parse(args)

	if *workflowID == "" || *message == "" {
		log.Fatal("Error: --workflow-id and --message are required")
	}

	c := dialTemporal()
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   *workflowID,
		UpdateName:   workflow.UpdateUserInput,
		Args:         []interface{}{workflow.UserInput{Content: *message}},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	if err != nil {
		log.Fatalf("Failed to send user input: %v", err)
	}

	var accepted workflow.UserInputAccepted
	if err := updateHandle.Get(ctx, &accepted); err != nil {
		log.Fatalf("Update failed: %v", err)
	}

	log.Printf("Message accepted, turn ID: %s", accepted.TurnID)
	fmt.Println(accepted.TurnID)
}

// cmdHistory queries the conversation history.
func cmdHistory(args []string) {
	fs := flag.NewFlagSet("history", flag.ExitOnError)
	workflowID := fs.String("workflow-id", "", "Workflow ID (required)")
	fs.Parse(args)

	if *workflowID == "" {
		log.Fatal("Error: --workflow-id is required")
	}

	c := dialTemporal()
	defer c.Close()

	resp, err := c.QueryWorkflow(context.Background(), *workflowID, "", workflow.QueryGetConversationItems)
	if err != nil {
		log.Fatalf("Failed to query history: %v", err)
	}

	var items []models.ConversationItem
	if err := resp.Get(&items); err != nil {
		log.Fatalf("Failed to decode history: %v", err)
	}

	// Print items as JSON
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal history: %v", err)
	}
	fmt.Println(string(data))
}

// cmdInterrupt sends an interrupt Update.
func cmdInterrupt(args []string) {
	fs := flag.NewFlagSet("interrupt", flag.ExitOnError)
	workflowID := fs.String("workflow-id", "", "Workflow ID (required)")
	fs.Parse(args)

	if *workflowID == "" {
		log.Fatal("Error: --workflow-id is required")
	}

	c := dialTemporal()
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   *workflowID,
		UpdateName:   workflow.UpdateInterrupt,
		Args:         []interface{}{workflow.InterruptRequest{}},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	if err != nil {
		log.Fatalf("Failed to send interrupt: %v", err)
	}

	var resp workflow.InterruptResponse
	if err := updateHandle.Get(ctx, &resp); err != nil {
		log.Fatalf("Interrupt failed: %v", err)
	}

	log.Printf("Interrupt acknowledged: %v", resp.Acknowledged)
}

// cmdEnd sends a shutdown Update.
func cmdEnd(args []string) {
	fs := flag.NewFlagSet("end", flag.ExitOnError)
	workflowID := fs.String("workflow-id", "", "Workflow ID (required)")
	reason := fs.String("reason", "", "Shutdown reason (optional)")
	fs.Parse(args)

	if *workflowID == "" {
		log.Fatal("Error: --workflow-id is required")
	}

	c := dialTemporal()
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   *workflowID,
		UpdateName:   workflow.UpdateShutdown,
		Args:         []interface{}{workflow.ShutdownRequest{Reason: *reason}},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	if err != nil {
		log.Fatalf("Failed to send shutdown: %v", err)
	}

	var resp workflow.ShutdownResponse
	if err := updateHandle.Get(ctx, &resp); err != nil {
		log.Fatalf("Shutdown failed: %v", err)
	}

	log.Printf("Shutdown acknowledged: %v", resp.Acknowledged)
}
