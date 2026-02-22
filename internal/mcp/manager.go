package mcp

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"sync"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// managedClient wraps a single MCP SDK client session with its config metadata.
type managedClient struct {
	session *gomcp.ClientSession
	config  McpServerConfig
}

// InitResult is the outcome of initializing all MCP servers for a session.
type InitResult struct {
	// Tools maps qualified name → ToolInfo for all discovered tools.
	Tools map[string]ToolInfo
	// ToolSpecs contains extracted tool specifications ready for the workflow layer.
	ToolSpecs []McpToolSpec
	// Failures records servers that failed to initialize (server name → error message).
	Failures map[string]string
}

// McpConnectionManager manages MCP client connections for a single session.
// Each session gets its own manager with one Go MCP SDK client per configured server.
//
// Maps to: codex-rs/core/src/mcp_connection_manager.rs McpConnectionManager
type McpConnectionManager struct {
	mu      sync.Mutex
	clients map[string]*managedClient // server name → live client session
	tools   map[string]ToolInfo       // qualified name → tool metadata
}

// NewMcpConnectionManager creates a new empty manager.
func NewMcpConnectionManager() *McpConnectionManager {
	return &McpConnectionManager{
		clients: make(map[string]*managedClient),
		tools:   make(map[string]ToolInfo),
	}
}

// Initialize starts all enabled MCP servers, discovers their tools, applies
// filtering and name qualification, and returns the merged result.
//
// Servers are started in parallel. Required servers that fail cause an error
// to be returned. Optional servers that fail are logged and their tools skipped.
//
// Maps to: codex-rs McpConnectionManager::initialize
func (m *McpConnectionManager) Initialize(ctx context.Context, servers map[string]McpServerConfig) (*InitResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	type serverResult struct {
		name    string
		tools   []ToolInfo
		err     error
		session *gomcp.ClientSession
		config  McpServerConfig
	}

	// Collect enabled servers
	type enabledServer struct {
		name   string
		config McpServerConfig
	}
	var enabled []enabledServer
	for name, cfg := range servers {
		if cfg.IsEnabled() {
			enabled = append(enabled, enabledServer{name, cfg})
		}
	}

	if len(enabled) == 0 {
		return &InitResult{Tools: m.tools, Failures: map[string]string{}}, nil
	}

	// Start all servers in parallel
	results := make([]serverResult, len(enabled))
	var wg sync.WaitGroup
	for i, srv := range enabled {
		wg.Add(1)
		go func(idx int, serverName string, cfg McpServerConfig) {
			defer wg.Done()
			result := serverResult{name: serverName, config: cfg}

			// Create transport and connect
			session, err := m.connectToServer(ctx, serverName, cfg)
			if err != nil {
				result.err = err
				results[idx] = result
				return
			}
			result.session = session

			// List tools with startup timeout
			listCtx, cancel := context.WithTimeout(ctx, cfg.GetStartupTimeout())
			defer cancel()

			toolsResult, err := session.ListTools(listCtx, nil)
			if err != nil {
				result.err = fmt.Errorf("failed to list tools for %s: %w", serverName, err)
				_ = session.Close()
				results[idx] = result
				return
			}

			// Apply tool filter
			filter := NewToolFilter(cfg.EnabledTools, cfg.DisabledTools)
			var toolInfos []ToolInfo
			for _, t := range toolsResult.Tools {
				if filter.Allows(t.Name) {
					toolInfos = append(toolInfos, ToolInfo{
						ServerName: serverName,
						ToolName:   t.Name,
						Tool:       t,
					})
				}
			}

			result.tools = toolInfos
			results[idx] = result
		}(i, srv.name, srv.config)
	}
	wg.Wait()

	// Collect results
	failures := make(map[string]string)
	var allTools []ToolInfo
	for _, r := range results {
		if r.err != nil {
			failures[r.name] = r.err.Error()
			log.Printf("mcp: server %s failed: %v", r.name, r.err)
			continue
		}
		// Store the live client session
		m.clients[r.name] = &managedClient{
			session: r.session,
			config:  r.config,
		}
		allTools = append(allTools, r.tools...)
	}

	// Check required servers
	for name, cfg := range servers {
		if cfg.Required {
			if errMsg, failed := failures[name]; failed {
				return nil, fmt.Errorf("required MCP server %s failed to initialize: %s", name, errMsg)
			}
		}
	}

	// Qualify tool names
	m.tools = QualifyTools(allTools)

	// Extract tool specs for the workflow layer
	specs := extractToolSpecs(m.tools)

	return &InitResult{
		Tools:     m.tools,
		ToolSpecs: specs,
		Failures:  failures,
	}, nil
}

// connectToServer creates and connects an MCP client to the given server.
func (m *McpConnectionManager) connectToServer(ctx context.Context, serverName string, cfg McpServerConfig) (*gomcp.ClientSession, error) {
	transport := cfg.Transport

	client := gomcp.NewClient(&gomcp.Implementation{
		Name:    "temporal-agent-harness",
		Version: "1.0.0",
	}, nil)

	connectCtx, cancel := context.WithTimeout(ctx, cfg.GetStartupTimeout())
	defer cancel()

	if transport.IsStdio() {
		cmd := exec.CommandContext(connectCtx, transport.Command, transport.Args...)
		if transport.Cwd != "" {
			cmd.Dir = transport.Cwd
		}
		for k, v := range transport.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}

		cmdTransport := &gomcp.CommandTransport{Command: cmd}
		session, err := client.Connect(connectCtx, cmdTransport, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to MCP server %s (stdio): %w", serverName, err)
		}
		return session, nil
	}

	if transport.IsHTTP() {
		httpTransport := &gomcp.StreamableClientTransport{
			Endpoint: transport.URL,
		}
		session, err := client.Connect(connectCtx, httpTransport, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to MCP server %s (HTTP): %w", serverName, err)
		}
		return session, nil
	}

	return nil, fmt.Errorf("MCP server %s has neither command nor URL configured", serverName)
}

// CallTool dispatches a tool call to the appropriate MCP server.
//
// Maps to: codex-rs McpConnectionManager::call_tool
func (m *McpConnectionManager) CallTool(ctx context.Context, serverName, toolName string, args map[string]interface{}) (*gomcp.CallToolResult, error) {
	m.mu.Lock()
	mc, ok := m.clients[serverName]
	m.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("MCP server %q not connected", serverName)
	}

	// Apply per-tool timeout
	callCtx, cancel := context.WithTimeout(ctx, mc.config.GetToolTimeout())
	defer cancel()

	result, err := mc.session.CallTool(callCtx, &gomcp.CallToolParams{
		Name:      toolName,
		Arguments: args,
	})
	if err != nil {
		return nil, fmt.Errorf("MCP tool call %s/%s failed: %w", serverName, toolName, err)
	}

	return result, nil
}

// GetToolInfo returns the ToolInfo for a qualified tool name.
func (m *McpConnectionManager) GetToolInfo(qualifiedName string) (ToolInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	info, ok := m.tools[qualifiedName]
	return info, ok
}

// GetToolInfoByRef looks up a tool by server and tool name (iterates tools map).
func (m *McpConnectionManager) GetToolInfoByRef(serverName, toolName string) (ToolInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, info := range m.tools {
		if info.ServerName == serverName && info.ToolName == toolName {
			return info, true
		}
	}
	return ToolInfo{}, false
}

// extractToolSpecs converts the qualified tools map into McpToolSpec entries.
func extractToolSpecs(tools map[string]ToolInfo) []McpToolSpec {
	specs := make([]McpToolSpec, 0, len(tools))
	for qualifiedName, info := range tools {
		spec := McpToolSpec{
			QualifiedName: qualifiedName,
			ServerName:    info.ServerName,
			ToolName:      info.ToolName,
		}

		if tool, ok := info.Tool.(*gomcp.Tool); ok {
			spec.Description = tool.Description
			if tool.Annotations != nil && tool.Annotations.ReadOnlyHint {
				spec.ReadOnly = true
			}
			// Extract input schema as map[string]interface{}
			if tool.InputSchema != nil {
				if schema, ok := tool.InputSchema.(map[string]interface{}); ok {
					spec.InputSchema = schema
				} else if schema, ok := tool.InputSchema.(map[string]any); ok {
					spec.InputSchema = schema
				}
			}
		}

		specs = append(specs, spec)
	}
	return specs
}

// SetToolInfo adds or updates a tool entry in the manager's tool map.
// Used by tests to inject tool metadata without running full initialization.
func (m *McpConnectionManager) SetToolInfo(qualifiedName string, info ToolInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tools[qualifiedName] = info
}

// InjectSession adds a pre-connected client session to the manager.
// Used by tests to inject sessions created with InMemoryTransport.
func (m *McpConnectionManager) InjectSession(serverName string, session *gomcp.ClientSession, config McpServerConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clients[serverName] = &managedClient{
		session: session,
		config:  config,
	}
}

// Close shuts down all connected MCP client sessions.
func (m *McpConnectionManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, mc := range m.clients {
		if err := mc.session.Close(); err != nil {
			log.Printf("mcp: error closing session for %s: %v", name, err)
		}
	}
	m.clients = make(map[string]*managedClient)
	m.tools = make(map[string]ToolInfo)
}
