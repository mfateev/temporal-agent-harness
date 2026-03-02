// Collaboration tool specifications for subagent orchestration.
//
// Maps to: codex-rs/core/src/tools/spec.rs (collaboration tool definitions)
// See also: codex-rs/core/src/agent/collab.rs, codex-rs/core/src/agent/control.rs
package tools

import (
	"fmt"
	"sort"
	"strings"
)

func init() {
	for _, e := range []SpecEntry{
		{Name: "spawn_agent", Constructor: NewSpawnAgentToolSpec, Group: "collab"},
		{Name: "send_input", Constructor: NewSendInputToolSpec, Group: "collab"},
		{Name: "wait", Constructor: NewWaitToolSpec, Group: "collab"},
		{Name: "close_agent", Constructor: NewCloseAgentToolSpec, Group: "collab"},
		{Name: "resume_agent", Constructor: NewResumeAgentToolSpec, Group: "collab"},
	} {
		RegisterSpec(e)
	}
}

// collabInputItemsSchema is the JSON schema for the items parameter shared by
// spawn_agent and send_input. Each item is an object with a type discriminator.
var collabInputItemsSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"type": map[string]interface{}{
			"type":        "string",
			"description": "Input item type: text, image, local_image, skill, or mention.",
		},
		"text": map[string]interface{}{
			"type":        "string",
			"description": "Text content when type is text.",
		},
		"image_url": map[string]interface{}{
			"type":        "string",
			"description": "Image URL when type is image.",
		},
		"path": map[string]interface{}{
			"type":        "string",
			"description": "Path when type is local_image/skill, or mention target such as app://<connector-id> when type is mention.",
		},
		"name": map[string]interface{}{
			"type":        "string",
			"description": "Display name when type is skill or mention.",
		},
	},
}

// NewSpawnAgentToolSpec creates the specification for the spawn_agent tool.
// This tool is intercepted by the workflow (not dispatched as an activity).
//
// Maps to: codex-rs/core/src/tools/spec.rs create_spawn_agent_tool
func NewSpawnAgentToolSpec() ToolSpec {
	return ToolSpec{
		Name:        "spawn_agent",
		Description: `Spawn a sub-agent for a well-scoped task. Returns the agent id to use to communicate with this agent.`,
		Parameters: []ToolParameter{
			{
				Name:        "message",
				Type:        "string",
				Description: "Initial plain-text task for the new agent. Use either message or items.",
				Required:    false,
			},
			{
				Name:        "items",
				Type:        "array",
				Description: "Structured content items for the new agent's task. Use either message or items.",
				Required:    false,
				Items:       collabInputItemsSchema,
			},
			{
				Name: "agent_type",
				Type: "string",
				Description: "The type of agent to spawn. Options: " +
					"'explorer' — Use explorer for all codebase questions, searches, reading files, and understanding code. Explorers are fast and cheap. " +
					"'worker' — Use for execution and production work: writing code, running tests, creating files, and making commits. " +
					"'orchestrator' — Use for coordination of multiple sub-agents. " +
					"'default' — Inherits parent configuration. " +
					"Default: 'default'.",
				Required: false,
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
		Name:        "send_input",
		Description: `Send a message to an existing agent. Use interrupt=true to redirect work immediately.`,
		Parameters: []ToolParameter{
			{
				Name:        "id",
				Type:        "string",
				Description: "Agent id to message (from spawn_agent).",
				Required:    true,
			},
			{
				Name:        "message",
				Type:        "string",
				Description: "Plain-text message to send to the agent. Use either message or items.",
				Required:    false,
			},
			{
				Name:        "items",
				Type:        "array",
				Description: "Structured content items to send to the agent. Use either message or items.",
				Required:    false,
				Items:       collabInputItemsSchema,
			},
			{
				Name:        "interrupt",
				Type:        "boolean",
				Description: "When true, stop the agent's current task and handle this immediately. When false (default), queue this message.",
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
		Name:        "wait",
		Description: `Wait for agents to reach a final status. Completed statuses may include the agent's final message. Returns empty status when timed out.`,
		Parameters: []ToolParameter{
			{
				Name:        "ids",
				Type:        "array",
				Description: "Agent ids to wait on. Pass multiple ids to wait for whichever finishes first.",
				Required:    true,
				Items: map[string]interface{}{
					"type": "string",
				},
			},
			{
				Name:        "timeout_ms",
				Type:        "number",
				Description: "Maximum time to wait in milliseconds. Min: 10000, Max: 300000, Default: 30000. Prefer longer waits (minutes) to avoid busy polling.",
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
		Name:        "close_agent",
		Description: `Close an agent when it is no longer needed and return its last known status.`,
		Parameters: []ToolParameter{
			{
				Name:        "id",
				Type:        "string",
				Description: "Agent id to close (from spawn_agent).",
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
		Name:        "resume_agent",
		Description: `Resume a previously closed agent by id so it can receive send_input and wait calls.`,
		Parameters: []ToolParameter{
			{
				Name:        "id",
				Type:        "string",
				Description: "Agent id to resume.",
				Required:    true,
			},
		},
	}
}

// CrewAgentSummary is a lightweight description of a crew agent for tool spec generation.
// This avoids importing the models package from tools (keeping tools dependency-free).
type CrewAgentSummary struct {
	Name        string
	Description string
}

// UpdateSpawnAgentSpecWithCrewRoles extends the spawn_agent tool spec's agent_type
// parameter description with crew-defined agent names and descriptions.
// If crewAgents is empty, the specs are returned unchanged.
// If crewAgents is non-empty but the agent has no available_agents (empty list),
// collab tools are removed entirely.
func UpdateSpawnAgentSpecWithCrewRoles(specs []ToolSpec, crewAgents []CrewAgentSummary) []ToolSpec {
	if len(crewAgents) == 0 {
		return specs
	}

	// Build the crew roles description string.
	// Sort by name for deterministic output.
	sorted := make([]CrewAgentSummary, len(crewAgents))
	copy(sorted, crewAgents)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})

	var parts []string
	for _, agent := range sorted {
		desc := agent.Description
		if desc == "" {
			desc = "Crew-defined agent"
		}
		parts = append(parts, fmt.Sprintf("'%s' — %s", agent.Name, desc))
	}
	crewDesc := " Crew agents: " + strings.Join(parts, " ")

	// Find and modify the spawn_agent spec.
	result := make([]ToolSpec, len(specs))
	copy(result, specs)
	for i, spec := range result {
		if spec.Name != "spawn_agent" {
			continue
		}
		// Clone parameters to avoid mutating the original.
		params := make([]ToolParameter, len(spec.Parameters))
		copy(params, spec.Parameters)
		for j, p := range params {
			if p.Name == "agent_type" {
				params[j].Description += crewDesc
				break
			}
		}
		result[i].Parameters = params
		break
	}

	return result
}

// RemoveCollabSpecs removes all collab tool specs from the list.
// Used when an agent has no available_agents and cannot spawn sub-agents.
func RemoveCollabSpecs(specs []ToolSpec) []ToolSpec {
	collabNames := map[string]bool{
		"spawn_agent":  true,
		"send_input":   true,
		"wait":         true,
		"close_agent":  true,
		"resume_agent": true,
	}
	var result []ToolSpec
	for _, spec := range specs {
		if !collabNames[spec.Name] {
			result = append(result, spec)
		}
	}
	return result
}
