package activities

import (
	"context"
	"fmt"

	"github.com/mfateev/temporal-agent-harness/internal/mcp"
	"github.com/mfateev/temporal-agent-harness/internal/tools"
)

// McpActivities contains MCP-related Temporal activities.
type McpActivities struct {
	store *mcp.McpStore
}

// NewMcpActivities creates a new McpActivities instance.
func NewMcpActivities(store *mcp.McpStore) *McpActivities {
	return &McpActivities{store: store}
}

// InitializeMcpServersInput is the input for the InitializeMcpServers activity.
type InitializeMcpServersInput struct {
	SessionID  string                         `json:"session_id"`
	McpServers map[string]mcp.McpServerConfig `json:"mcp_servers"`
}

// InitializeMcpServersOutput is the output from the InitializeMcpServers activity.
type InitializeMcpServersOutput struct {
	// ToolSpecs contains the discovered MCP tool specifications (with RawJSONSchema).
	ToolSpecs []tools.ToolSpec `json:"tool_specs"`
	// McpToolLookup maps qualified tool names to their server/tool routing info.
	McpToolLookup map[string]tools.McpToolRef `json:"mcp_tool_lookup"`
	// Failures records servers that failed to initialize (server name → error).
	Failures map[string]string `json:"failures"`
}

// InitializeMcpServers starts all MCP server connections for a session,
// discovers their tools, and returns tool specs + routing info.
//
// This activity runs on the worker and creates entries in the McpStore.
// The workflow calls this once before the first turn when McpServers is configured.
func (a *McpActivities) InitializeMcpServers(ctx context.Context, input InitializeMcpServersInput) (InitializeMcpServersOutput, error) {
	mgr := a.store.GetOrCreate(input.SessionID)

	result, err := mgr.Initialize(ctx, input.McpServers)
	if err != nil {
		return InitializeMcpServersOutput{}, fmt.Errorf("MCP initialization failed: %w", err)
	}

	// Convert MCP tool specs to tools.ToolSpec with RawJSONSchema
	var toolSpecs []tools.ToolSpec
	mcpToolLookup := make(map[string]tools.McpToolRef)

	for _, mcpSpec := range result.ToolSpecs {
		toolSpecs = append(toolSpecs, tools.ToolSpec{
			Name:             mcpSpec.QualifiedName,
			Description:      mcpSpec.Description,
			RawJSONSchema:    mcpSpec.InputSchema,
			DefaultTimeoutMs: int64(mcp.DefaultToolTimeout.Milliseconds()),
		})

		mcpToolLookup[mcpSpec.QualifiedName] = tools.McpToolRef{
			ServerName: mcpSpec.ServerName,
			ToolName:   mcpSpec.ToolName,
		}
	}

	return InitializeMcpServersOutput{
		ToolSpecs:     toolSpecs,
		McpToolLookup: mcpToolLookup,
		Failures:      result.Failures,
	}, nil
}

// CleanupMcpServersInput is the input for the CleanupMcpServers activity.
type CleanupMcpServersInput struct {
	SessionID string `json:"session_id"`
}

// CleanupMcpServers closes all MCP connections for a session.
// Called when the workflow completes.
func (a *McpActivities) CleanupMcpServers(ctx context.Context, input CleanupMcpServersInput) error {
	a.store.Remove(input.SessionID)
	return nil
}
