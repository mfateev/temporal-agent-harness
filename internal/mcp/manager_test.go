package mcp

import (
	"context"
	"testing"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startTestServer creates a test MCP server with the given tools and returns
// a connected ClientSession. The server runs on an InMemoryTransport.
func startTestServer(t *testing.T, ctx context.Context, tools map[string]gomcp.ToolHandler) *gomcp.ClientSession {
	t.Helper()

	server := gomcp.NewServer(&gomcp.Implementation{
		Name:    "test-server",
		Version: "1.0.0",
	}, nil)

	for name, handler := range tools {
		server.AddTool(&gomcp.Tool{
			Name:        name,
			Description: "Test tool: " + name,
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		}, handler)
	}

	serverTransport, clientTransport := gomcp.NewInMemoryTransports()

	// Start server in background
	go func() {
		_ = server.Run(ctx, serverTransport)
	}()

	client := gomcp.NewClient(&gomcp.Implementation{
		Name:    "test-client",
		Version: "1.0.0",
	}, nil)

	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)

	return session
}

func TestMcpConnectionManager_CallTool(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a test server with an echo tool
	session := startTestServer(t, ctx, map[string]gomcp.ToolHandler{
		"echo": func(ctx context.Context, req *gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
			return &gomcp.CallToolResult{
				Content: []gomcp.Content{&gomcp.TextContent{Text: "echoed"}},
			}, nil
		},
	})
	defer session.Close()

	// Create manager and manually inject the test session
	mgr := NewMcpConnectionManager()
	mgr.mu.Lock()
	mgr.clients["test_server"] = &managedClient{
		session: session,
		config:  McpServerConfig{},
	}
	mgr.tools["mcp__test_server__echo"] = ToolInfo{
		ServerName: "test_server",
		ToolName:   "echo",
	}
	mgr.mu.Unlock()

	// Call the tool
	result, err := mgr.CallTool(ctx, "test_server", "echo", map[string]interface{}{})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Content, 1)
	tc, ok := result.Content[0].(*gomcp.TextContent)
	require.True(t, ok)
	assert.Equal(t, "echoed", tc.Text)
}

func TestMcpConnectionManager_CallTool_ServerNotConnected(t *testing.T) {
	mgr := NewMcpConnectionManager()

	_, err := mgr.CallTool(context.Background(), "nonexistent", "tool", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not connected")
}

func TestMcpConnectionManager_GetToolInfo(t *testing.T) {
	mgr := NewMcpConnectionManager()
	mgr.tools["mcp__github__create_issue"] = ToolInfo{
		ServerName: "github",
		ToolName:   "create_issue",
	}

	info, ok := mgr.GetToolInfo("mcp__github__create_issue")
	assert.True(t, ok)
	assert.Equal(t, "github", info.ServerName)
	assert.Equal(t, "create_issue", info.ToolName)

	_, ok = mgr.GetToolInfo("nonexistent")
	assert.False(t, ok)
}

func TestMcpConnectionManager_GetToolInfoByRef(t *testing.T) {
	mgr := NewMcpConnectionManager()
	mgr.tools["mcp__github__create_issue"] = ToolInfo{
		ServerName: "github",
		ToolName:   "create_issue",
	}

	info, ok := mgr.GetToolInfoByRef("github", "create_issue")
	assert.True(t, ok)
	assert.Equal(t, "github", info.ServerName)

	_, ok = mgr.GetToolInfoByRef("github", "nonexistent")
	assert.False(t, ok)
}

func TestMcpConnectionManager_Close(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	session := startTestServer(t, ctx, map[string]gomcp.ToolHandler{
		"test": func(ctx context.Context, req *gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
			return &gomcp.CallToolResult{
				Content: []gomcp.Content{&gomcp.TextContent{Text: "ok"}},
			}, nil
		},
	})

	mgr := NewMcpConnectionManager()
	mgr.clients["test"] = &managedClient{session: session, config: McpServerConfig{}}
	mgr.tools["mcp__test__test"] = ToolInfo{ServerName: "test", ToolName: "test"}

	mgr.Close()

	assert.Empty(t, mgr.clients)
	assert.Empty(t, mgr.tools)
}

func TestMcpConnectionManager_InitializeWithInMemoryServer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a test server
	server := gomcp.NewServer(&gomcp.Implementation{
		Name:    "test-server",
		Version: "1.0.0",
	}, nil)

	server.AddTool(&gomcp.Tool{
		Name:        "greet",
		Description: "Greet someone",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
		},
	}, func(ctx context.Context, req *gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
		return &gomcp.CallToolResult{
			Content: []gomcp.Content{&gomcp.TextContent{Text: "Hello!"}},
		}, nil
	})

	server.AddTool(&gomcp.Tool{
		Name:        "farewell",
		Description: "Say goodbye",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}, func(ctx context.Context, req *gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
		return &gomcp.CallToolResult{
			Content: []gomcp.Content{&gomcp.TextContent{Text: "Goodbye!"}},
		}, nil
	})

	serverTransport, clientTransport := gomcp.NewInMemoryTransports()

	go func() {
		_ = server.Run(ctx, serverTransport)
	}()

	// Create a manager and manually connect via the in-memory transport
	mgr := NewMcpConnectionManager()

	client := gomcp.NewClient(&gomcp.Implementation{
		Name:    "test-client",
		Version: "1.0.0",
	}, nil)

	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)

	// Manually set up as if Initialize ran
	mgr.mu.Lock()
	mgr.clients["myserver"] = &managedClient{session: session, config: McpServerConfig{}}

	// List tools and qualify
	toolsResult, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	var allTools []ToolInfo
	for _, t := range toolsResult.Tools {
		allTools = append(allTools, ToolInfo{
			ServerName: "myserver",
			ToolName:   t.Name,
			Tool:       t,
		})
	}
	mgr.tools = QualifyTools(allTools)
	mgr.mu.Unlock()

	// Verify tools were discovered and qualified
	assert.Len(t, mgr.tools, 2)
	_, ok := mgr.tools["mcp__myserver__greet"]
	assert.True(t, ok)
	_, ok = mgr.tools["mcp__myserver__farewell"]
	assert.True(t, ok)

	// Call a tool
	result, err := mgr.CallTool(ctx, "myserver", "greet", map[string]interface{}{"name": "World"})
	require.NoError(t, err)
	require.Len(t, result.Content, 1)
	tc, ok := result.Content[0].(*gomcp.TextContent)
	require.True(t, ok)
	assert.Equal(t, "Hello!", tc.Text)
}
