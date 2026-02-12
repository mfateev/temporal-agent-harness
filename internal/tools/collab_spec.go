// Collaboration tool specifications for subagent orchestration.
//
// Maps to: codex-rs/core/src/tools/spec.rs (collaboration tool definitions)
// See also: codex-rs/core/src/agent/collab.rs, codex-rs/core/src/agent/control.rs
package tools

// NewSpawnAgentToolSpec creates the specification for the spawn_agent tool.
// This tool is intercepted by the workflow (not dispatched as an activity).
//
// Maps to: codex-rs/core/src/tools/spec.rs create_spawn_agent_tool
func NewSpawnAgentToolSpec() ToolSpec {
	return ToolSpec{
		Name: "spawn_agent",
		Description: `Spawn a new child agent to work on a task. The child runs independently ` +
			`with its own conversation history. Use this when a subtask can be worked on in parallel ` +
			`or when you want to delegate focused work (e.g., code exploration, research).`,
		Parameters: []ToolParameter{
			{
				Name:        "message",
				Type:        "string",
				Description: "The task message to give to the child agent.",
				Required:    true,
			},
			{
				Name:        "agent_type",
				Type:        "string",
				Description: "The type of agent to spawn. Options: 'default', 'orchestrator', 'worker', 'explorer'. Default: 'default'.",
				Required:    false,
			},
		},
	}
}

// NewSendInputToolSpec creates the specification for the send_input tool.
// This tool is intercepted by the workflow (not dispatched as an activity).
//
// Maps to: codex-rs/core/src/tools/spec.rs create_send_input_tool
func NewSendInputToolSpec() ToolSpec {
	return ToolSpec{
		Name: "send_input",
		Description: `Send a message to a running child agent. The message is delivered ` +
			`as a new user input to the child's conversation.`,
		Parameters: []ToolParameter{
			{
				Name:        "id",
				Type:        "string",
				Description: "The agent ID returned by spawn_agent.",
				Required:    true,
			},
			{
				Name:        "message",
				Type:        "string",
				Description: "The message to send to the child agent.",
				Required:    true,
			},
			{
				Name:        "interrupt",
				Type:        "boolean",
				Description: "If true, interrupt the child's current turn before delivering the message.",
				Required:    false,
			},
		},
	}
}

// NewWaitToolSpec creates the specification for the wait tool.
// This tool is intercepted by the workflow (not dispatched as an activity).
//
// Maps to: codex-rs/core/src/tools/spec.rs create_wait_tool
func NewWaitToolSpec() ToolSpec {
	return ToolSpec{
		Name: "wait",
		Description: `Wait for one or more child agents to reach a terminal state (completed, errored, shutdown). ` +
			`Returns the status of each requested agent. Times out if agents don't finish within the timeout.`,
		Parameters: []ToolParameter{
			{
				Name:        "ids",
				Type:        "array",
				Description: "Array of agent IDs to wait for.",
				Required:    true,
				Items: map[string]interface{}{
					"type": "string",
				},
			},
			{
				Name:        "timeout_ms",
				Type:        "number",
				Description: "Maximum time to wait in milliseconds. Range: 10000-300000. Default: 30000.",
				Required:    false,
			},
		},
	}
}

// NewCloseAgentToolSpec creates the specification for the close_agent tool.
// This tool is intercepted by the workflow (not dispatched as an activity).
//
// Maps to: codex-rs/core/src/tools/spec.rs create_close_agent_tool
func NewCloseAgentToolSpec() ToolSpec {
	return ToolSpec{
		Name: "close_agent",
		Description: `Shut down a running child agent. Sends a shutdown signal and waits briefly ` +
			`for the child to complete. Returns the child's final status.`,
		Parameters: []ToolParameter{
			{
				Name:        "id",
				Type:        "string",
				Description: "The agent ID to shut down.",
				Required:    true,
			},
		},
	}
}

// NewResumeAgentToolSpec creates the specification for the resume_agent tool.
// This tool is intercepted by the workflow (not dispatched as an activity).
//
// Maps to: codex-rs/core/src/tools/spec.rs create_resume_agent_tool
func NewResumeAgentToolSpec() ToolSpec {
	return ToolSpec{
		Name: "resume_agent",
		Description: `Resume a previously completed or shut down agent with its conversation history preserved. ` +
			`Note: This feature is not yet implemented.`,
		Parameters: []ToolParameter{
			{
				Name:        "id",
				Type:        "string",
				Description: "The agent ID to resume.",
				Required:    true,
			},
		},
	}
}
