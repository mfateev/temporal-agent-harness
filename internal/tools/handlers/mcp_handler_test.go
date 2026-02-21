package handlers

import (
	"context"
	"testing"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mfateev/temporal-agent-harness/internal/mcp"
	"github.com/mfateev/temporal-agent-harness/internal/tools"
)

func TestMCPHandler_Name(t *testing.T) {
	store := mcp.NewMcpStore()
	handler := NewMCPHandler(store)
	assert.Equal(t, "mcp", handler.Name())
}

func TestMCPHandler_Kind(t *testing.T) {
	store := mcp.NewMcpStore()
	handler := NewMCPHandler(store)
	assert.Equal(t, tools.ToolKindMcp, handler.Kind())
}

func TestMCPHandler_IsMutating_NoRef(t *testing.T) {
	store := mcp.NewMcpStore()
	handler := NewMCPHandler(store)

	inv := &tools.ToolInvocation{ToolName: "mcp__test__tool"}
	assert.True(t, handler.IsMutating(inv), "should default to mutating when no McpToolRef")
}

func TestMCPHandler_IsMutating_NoManager(t *testing.T) {
	store := mcp.NewMcpStore()
	handler := NewMCPHandler(store)

	inv := &tools.ToolInvocation{
		ToolName:  "mcp__test__tool",
		SessionID: "session-unknown",
		McpToolRef: &tools.McpToolRef{
			ServerName: "test",
			ToolName:   "tool",
		},
	}
	assert.True(t, handler.IsMutating(inv), "should default to mutating when no manager")
}

func TestMCPHandler_IsMutating_ReadOnlyTool(t *testing.T) {
	store := mcp.NewMcpStore()
	handler := NewMCPHandler(store)

	// Set up a manager with a read-only tool
	mgr := store.GetOrCreate("session-1")
	mgr.SetToolInfo("mcp__test__read_tool", mcp.ToolInfo{
		ServerName: "test",
		ToolName:   "read_tool",
		Tool: &gomcp.Tool{
			Name: "read_tool",
			Annotations: &gomcp.ToolAnnotations{
				ReadOnlyHint: true,
			},
		},
	})

	inv := &tools.ToolInvocation{
		ToolName:  "mcp__test__read_tool",
		SessionID: "session-1",
		McpToolRef: &tools.McpToolRef{
			ServerName: "test",
			ToolName:   "read_tool",
		},
	}
	assert.False(t, handler.IsMutating(inv), "read-only tool should not be mutating")
}

func TestMCPHandler_IsMutating_MutatingTool(t *testing.T) {
	store := mcp.NewMcpStore()
	handler := NewMCPHandler(store)

	mgr := store.GetOrCreate("session-1")
	mgr.SetToolInfo("mcp__test__write_tool", mcp.ToolInfo{
		ServerName: "test",
		ToolName:   "write_tool",
		Tool: &gomcp.Tool{
			Name: "write_tool",
			Annotations: &gomcp.ToolAnnotations{
				ReadOnlyHint: false,
			},
		},
	})

	inv := &tools.ToolInvocation{
		ToolName:  "mcp__test__write_tool",
		SessionID: "session-1",
		McpToolRef: &tools.McpToolRef{
			ServerName: "test",
			ToolName:   "write_tool",
		},
	}
	assert.True(t, handler.IsMutating(inv), "non-readonly tool should be mutating")
}

func TestMCPHandler_Handle_MissingRef(t *testing.T) {
	store := mcp.NewMcpStore()
	handler := NewMCPHandler(store)

	inv := &tools.ToolInvocation{ToolName: "mcp__test__tool"}
	_, err := handler.Handle(context.Background(), inv)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing McpToolRef")
}

func TestMCPHandler_Handle_NoManagerNoConfig(t *testing.T) {
	store := mcp.NewMcpStore()
	handler := NewMCPHandler(store)

	inv := &tools.ToolInvocation{
		ToolName:  "mcp__test__tool",
		SessionID: "session-unknown",
		McpToolRef: &tools.McpToolRef{
			ServerName: "test",
			ToolName:   "tool",
		},
	}
	output, err := handler.Handle(context.Background(), inv)
	require.NoError(t, err)
	require.NotNil(t, output)
	assert.False(t, *output.Success)
	assert.Contains(t, output.Content, "not connected")
}

func TestMCPHandler_Handle_CallsTool(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := mcp.NewMcpStore()
	handler := NewMCPHandler(store)

	// Set up a real test server with InMemoryTransport
	server := gomcp.NewServer(&gomcp.Implementation{
		Name:    "test-server",
		Version: "1.0.0",
	}, nil)

	server.AddTool(&gomcp.Tool{
		Name:        "greet",
		Description: "Say hello",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}, func(ctx context.Context, req *gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
		return &gomcp.CallToolResult{
			Content: []gomcp.Content{&gomcp.TextContent{Text: "Hello from MCP!"}},
		}, nil
	})

	serverTransport, clientTransport := gomcp.NewInMemoryTransports()
	go func() { _ = server.Run(ctx, serverTransport) }()

	client := gomcp.NewClient(&gomcp.Implementation{
		Name:    "test-client",
		Version: "1.0.0",
	}, nil)

	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)

	// Inject the session into the store's manager
	mgr := store.GetOrCreate("session-1")
	mgr.InjectSession("test_server", session, mcp.McpServerConfig{})
	mgr.SetToolInfo("mcp__test_server__greet", mcp.ToolInfo{
		ServerName: "test_server",
		ToolName:   "greet",
	})

	inv := &tools.ToolInvocation{
		ToolName:  "mcp__test_server__greet",
		SessionID: "session-1",
		Arguments: map[string]interface{}{},
		McpToolRef: &tools.McpToolRef{
			ServerName: "test_server",
			ToolName:   "greet",
		},
	}

	output, err := handler.Handle(ctx, inv)
	require.NoError(t, err)
	require.NotNil(t, output)
	assert.True(t, *output.Success)
	assert.Equal(t, "Hello from MCP!", output.Content)
}

func TestConvertCallToolResult_TextContent(t *testing.T) {
	result := &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: "line1"},
			&gomcp.TextContent{Text: "line2"},
		},
		IsError: false,
	}

	output := convertCallToolResult(result)
	assert.Equal(t, "line1\nline2", output.Content)
	assert.True(t, *output.Success)
}

func TestConvertCallToolResult_Error(t *testing.T) {
	result := &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: "something went wrong"},
		},
		IsError: true,
	}

	output := convertCallToolResult(result)
	assert.Equal(t, "something went wrong", output.Content)
	assert.False(t, *output.Success)
}

func TestConvertCallToolResult_Empty(t *testing.T) {
	result := &gomcp.CallToolResult{
		Content: []gomcp.Content{},
		IsError: false,
	}

	output := convertCallToolResult(result)
	assert.Equal(t, "", output.Content)
	assert.True(t, *output.Success)
}
