// Package tools provides tool registry, routing, and handler specifications.
//
// Corresponds to: codex-rs/core/src/tools/
package tools

// ToolKind classifies the type of tool handler.
//
// Maps to: codex-rs/core/src/tools/registry.rs ToolKind
type ToolKind int

const (
	ToolKindFunction ToolKind = iota // Standard function tool
	ToolKindMcp                      // MCP server tool (future)
)

// ToolOutput represents the result of tool execution.
//
// Maps to: codex-rs/core/src/tools/router.rs ToolOutput::Function
type ToolOutput struct {
	Content string `json:"content"`
	Success *bool  `json:"success,omitempty"`
}

// McpToolRef carries routing metadata for MCP tool dispatch.
// Stored in ToolActivityInput and ToolInvocation for MCP tool calls.
//
// Maps to: MCP tool routing (server + original tool name)
type McpToolRef struct {
	ServerName string `json:"server_name"`
	ToolName   string `json:"tool_name"`
}

// ToolInvocation provides context for tool execution.
//
// Maps to: codex-rs/core/src/tools/context.rs ToolInvocation
type ToolInvocation struct {
	CallID    string                 `json:"call_id"`
	ToolName  string                 `json:"tool_name"`
	Arguments map[string]interface{} `json:"arguments"`
	Cwd       string                 `json:"cwd,omitempty"` // Working directory for tool execution

	// SandboxPolicy, if set, restricts the execution environment.
	// Populated from workflow config and passed through activity input.
	SandboxPolicy *SandboxPolicyRef `json:"sandbox_policy,omitempty"`

	// EnvPolicy, if set, filters environment variables before execution.
	EnvPolicy *EnvPolicyRef `json:"env_policy,omitempty"`

	// Heartbeat, if set, is called periodically during long-running tool
	// execution to keep the Temporal activity alive. Set by the activity
	// layer; nil in unit tests.
	Heartbeat func(details ...interface{}) `json:"-"`

	// MCP fields — populated for mcp__* tool calls.

	// McpToolRef, if set, routes this call to the named MCP server + tool.
	McpToolRef *McpToolRef `json:"mcp_tool_ref,omitempty"`

	// SessionID identifies the workflow session for MCP store lookup.
	SessionID string `json:"session_id,omitempty"`

	// McpServers carries the session's MCP server configs for auto-reconnect.
	// Typed as interface{} to avoid circular imports; the MCPHandler
	// type-asserts to map[string]mcp.McpServerConfig.
	McpServers interface{} `json:"-"`
}

// SandboxPolicyRef is a serializable reference to a sandbox policy.
// Stored separately from internal/sandbox to avoid circular imports.
type SandboxPolicyRef struct {
	Mode          string   `json:"mode"`
	WritableRoots []string `json:"writable_roots,omitempty"`
	NetworkAccess bool     `json:"network_access"`
}

// EnvPolicyRef is a serializable reference to a shell environment policy.
// Stored separately from internal/execenv to avoid circular imports.
type EnvPolicyRef struct {
	Inherit               string            `json:"inherit,omitempty"`                // "all", "none", "core"
	IgnoreDefaultExcludes bool              `json:"ignore_default_excludes"`
	Exclude               []string          `json:"exclude,omitempty"`
	Set                   map[string]string `json:"set,omitempty"`
	IncludeOnly           []string          `json:"include_only,omitempty"`
}

// ExecApprovalRequirement classifies what approval a command needs before execution.
// Foundation type for the future approval system (not wired yet).
//
// Maps to: codex-rs/core/src/tools/context.rs (approval concepts)
type ExecApprovalRequirement int

const (
	// ApprovalSkip means the command is safe and no approval is needed.
	ApprovalSkip ExecApprovalRequirement = iota
	// ApprovalNeeded means the command requires user approval before execution.
	ApprovalNeeded
	// ApprovalForbidden means the command is forbidden and must not be executed.
	ApprovalForbidden
)

// CommandSafetyClassification holds the result of classifying a command's safety.
type CommandSafetyClassification struct {
	Requirement ExecApprovalRequirement
	Reason      string
	IsSafe      bool
	IsDangerous bool
}
