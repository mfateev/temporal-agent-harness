// Package handlers contains tool handler implementations.
//
// mcp_handler.go provides the MCPHandler, a single handler registered under
// the name "mcp" that routes tool calls to MCP server processes via McpStore.
//
// Maps to: codex-rs/core/src/tools/handlers/mcp.rs MCPHandler
package handlers

import (
	"context"
	"fmt"
	"strings"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mfateev/temporal-agent-harness/internal/mcp"
	"github.com/mfateev/temporal-agent-harness/internal/tools"
)

// MCPHandler routes MCP tool calls to the appropriate MCP server via McpStore.
// A single handler is registered under the name "mcp". The ExecuteTool activity
// detects mcp__ prefix tool names and routes to this handler, passing McpToolRef
// for server/tool resolution.
//
// Maps to: codex-rs/core/src/tools/handlers/mcp.rs MCPHandler
type MCPHandler struct {
	store *mcp.McpStore
}

// NewMCPHandler creates a new MCPHandler backed by the given store.
func NewMCPHandler(store *mcp.McpStore) *MCPHandler {
	return &MCPHandler{store: store}
}

func (h *MCPHandler) Name() string {
	return "mcp"
}

func (h *MCPHandler) Kind() tools.ToolKind {
	return tools.ToolKindMcp
}

// IsMutating checks MCP tool annotations to determine if the tool is read-only.
// If the tool has ReadOnlyHint=true in its annotations, it is not mutating.
// Otherwise, defaults to true (conservative: assume mutating).
//
// Maps to: codex-rs/core/src/mcp_tool_call.rs tool_approval_from_annotations
func (h *MCPHandler) IsMutating(invocation *tools.ToolInvocation) bool {
	if invocation.McpToolRef == nil {
		return true // conservative default
	}

	mgr := h.store.Get(invocation.SessionID)
	if mgr == nil {
		return true // conservative: no manager means we can't check
	}

	// Look up the tool info by server+tool name
	info, ok := mgr.GetToolInfoByRef(invocation.McpToolRef.ServerName, invocation.McpToolRef.ToolName)
	if !ok {
		return true
	}

	// Check if the MCP Tool has annotations
	if tool, ok := info.Tool.(*gomcp.Tool); ok && tool.Annotations != nil {
		if tool.Annotations.ReadOnlyHint {
			return false
		}
	}

	return true // default: assume mutating
}

// Handle dispatches a tool call to the MCP server via the connection manager.
//
// Maps to: codex-rs/core/src/tools/handlers/mcp.rs MCPHandler::handle
func (h *MCPHandler) Handle(ctx context.Context, invocation *tools.ToolInvocation) (*tools.ToolOutput, error) {
	if invocation.McpToolRef == nil {
		return nil, fmt.Errorf("MCPHandler: missing McpToolRef in invocation")
	}

	mgr := h.store.Get(invocation.SessionID)
	if mgr == nil {
		// Auto-reconnect: worker restarted, re-initialize from config
		if invocation.McpServers == nil {
			success := false
			return &tools.ToolOutput{
				Content: "MCP server not connected and no config available for reconnection",
				Success: &success,
			}, nil
		}

		servers, ok := invocation.McpServers.(map[string]mcp.McpServerConfig)
		if !ok {
			success := false
			return &tools.ToolOutput{
				Content: "MCP server config has unexpected type for reconnection",
				Success: &success,
			}, nil
		}

		mgr = h.store.GetOrCreate(invocation.SessionID)
		_, err := mgr.Initialize(ctx, servers)
		if err != nil {
			success := false
			return &tools.ToolOutput{
				Content: fmt.Sprintf("MCP server failed to reconnect: %v", err),
				Success: &success,
			}, nil
		}
	}

	result, err := mgr.CallTool(ctx, invocation.McpToolRef.ServerName, invocation.McpToolRef.ToolName, invocation.Arguments)
	if err != nil {
		success := false
		return &tools.ToolOutput{
			Content: fmt.Sprintf("MCP tool call failed: %v", err),
			Success: &success,
		}, nil
	}

	// Convert CallToolResult to ToolOutput
	return convertCallToolResult(result), nil
}

// convertCallToolResult converts an MCP CallToolResult to a ToolOutput.
func convertCallToolResult(result *gomcp.CallToolResult) *tools.ToolOutput {
	var sb strings.Builder
	for i, content := range result.Content {
		if i > 0 {
			sb.WriteString("\n")
		}
		switch c := content.(type) {
		case *gomcp.TextContent:
			sb.WriteString(c.Text)
		case *gomcp.ImageContent:
			sb.WriteString("[image: ")
			sb.WriteString(c.MIMEType)
			sb.WriteString("]")
		default:
			sb.WriteString("[unsupported content type]")
		}
	}

	success := !result.IsError
	return &tools.ToolOutput{
		Content: sb.String(),
		Success: &success,
	}
}
