// Crew system types and logic — reusable multi-agent session templates.
//
// A crew is a TOML template stored in ~/.codex/crews/<name>.toml that defines
// a main agent plus supporting agents, parameterized by user inputs with
// {placeholder} interpolation.
//
// Maps to: codex-temporal crew system (types, config_loader, session_workflow)
package models

import (
	"fmt"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// CrewMode controls whether the crew's initial prompt requires user input.
type CrewMode string

const (
	// CrewModeInteractive means the user provides the first message.
	CrewModeInteractive CrewMode = "interactive"
	// CrewModeAutonomous means initial_prompt generates the first message.
	CrewModeAutonomous CrewMode = "autonomous"
)

// CrewInputSpec describes a single input parameter for a crew template.
type CrewInputSpec struct {
	Description string `toml:"description" json:"description"`
	Required    *bool  `toml:"required" json:"required,omitempty"`   // Default: true
	Default     string `toml:"default" json:"default,omitempty"`
}

// IsRequired returns whether this input is required (default: true).
func (s CrewInputSpec) IsRequired() bool {
	if s.Required == nil {
		return true
	}
	return *s.Required
}

// CrewAgentDef describes a single agent within a crew template.
type CrewAgentDef struct {
	// Role is an optional base role (explorer/worker/orchestrator/planner/default).
	// When set, the agent inherits role overrides before applying crew-specific config.
	Role string `toml:"role" json:"role,omitempty"`

	// Model overrides the session model for this agent.
	Model string `toml:"model" json:"model,omitempty"`

	// Instructions are agent-specific instructions (supports {placeholder} interpolation).
	Instructions string `toml:"instructions" json:"instructions,omitempty"`

	// Description is shown in the spawn_agent tool spec.
	Description string `toml:"description" json:"description,omitempty"`

	// AvailableAgents lists which other crew agents this agent can spawn.
	// Empty/nil means the agent cannot spawn anyone.
	AvailableAgents []string `toml:"available_agents" json:"available_agents,omitempty"`
}

// CrewType is a crew template loaded from a TOML file.
type CrewType struct {
	// Name is the crew identifier (filename stem).
	Name string `toml:"name" json:"name"`

	// Description is a human-readable summary.
	Description string `toml:"description" json:"description,omitempty"`

	// Mode: interactive or autonomous.
	Mode CrewMode `toml:"mode" json:"mode"`

	// InitialPrompt is used in autonomous mode (supports {placeholder} interpolation).
	InitialPrompt string `toml:"initial_prompt" json:"initial_prompt,omitempty"`

	// MainAgent is the name of the entry-point agent in the Agents map.
	MainAgent string `toml:"main_agent" json:"main_agent"`

	// ApprovalPolicy overrides the session's approval mode when the crew is active.
	ApprovalPolicy string `toml:"approval_policy" json:"approval_policy,omitempty"`

	// Inputs defines the parameterized inputs for this crew.
	Inputs map[string]CrewInputSpec `toml:"inputs" json:"inputs,omitempty"`

	// Agents defines all agents in the crew (main + supporting).
	Agents map[string]CrewAgentDef `toml:"agents" json:"agents"`
}

// ParseCrewType parses a TOML-encoded crew definition.
func ParseCrewType(data []byte) (*CrewType, error) {
	var crew CrewType
	if err := toml.Unmarshal(data, &crew); err != nil {
		return nil, fmt.Errorf("invalid crew TOML: %w", err)
	}

	// Validate required fields.
	if crew.Name == "" {
		return nil, fmt.Errorf("crew missing required field: name")
	}
	if crew.MainAgent == "" {
		return nil, fmt.Errorf("crew %q missing required field: main_agent", crew.Name)
	}
	if len(crew.Agents) == 0 {
		return nil, fmt.Errorf("crew %q has no agents defined", crew.Name)
	}
	if _, ok := crew.Agents[crew.MainAgent]; !ok {
		return nil, fmt.Errorf("crew %q: main_agent %q not found in agents", crew.Name, crew.MainAgent)
	}

	// Validate mode.
	switch crew.Mode {
	case CrewModeInteractive, CrewModeAutonomous:
		// ok
	case "":
		crew.Mode = CrewModeInteractive // default
	default:
		return nil, fmt.Errorf("crew %q: invalid mode %q (must be interactive or autonomous)", crew.Name, crew.Mode)
	}

	// Validate that autonomous crews have an initial_prompt.
	if crew.Mode == CrewModeAutonomous && crew.InitialPrompt == "" {
		return nil, fmt.Errorf("crew %q: autonomous mode requires initial_prompt", crew.Name)
	}

	// Validate available_agents references.
	for agentName, def := range crew.Agents {
		for _, ref := range def.AvailableAgents {
			if _, ok := crew.Agents[ref]; !ok {
				return nil, fmt.Errorf("crew %q: agent %q references unknown agent %q in available_agents", crew.Name, agentName, ref)
			}
		}
	}

	return &crew, nil
}

// Interpolate replaces {key} placeholders in template with values from vars.
// Unknown placeholders are left as-is.
func Interpolate(template string, vars map[string]string) string {
	result := template
	for key, val := range vars {
		result = strings.ReplaceAll(result, "{"+key+"}", val)
	}
	return result
}

// ValidateInputs checks that all required inputs are provided.
func (c *CrewType) ValidateInputs(inputs map[string]string) error {
	var missing []string
	for name, spec := range c.Inputs {
		if !spec.IsRequired() {
			continue
		}
		if _, ok := inputs[name]; !ok {
			if spec.Default == "" {
				missing = append(missing, name)
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("missing required inputs: %s", strings.Join(missing, ", "))
	}
	return nil
}

// BuildVars merges user-provided inputs with defaults to produce the
// interpolation variable map.
func (c *CrewType) BuildVars(inputs map[string]string) map[string]string {
	vars := make(map[string]string, len(c.Inputs))
	// Start with defaults.
	for name, spec := range c.Inputs {
		if spec.Default != "" {
			vars[name] = spec.Default
		}
	}
	// Override with user-provided inputs.
	for key, val := range inputs {
		vars[key] = val
	}
	return vars
}

// ApplyCrewType validates inputs, interpolates templates, applies the main
// agent's overrides to cfg, and returns the map of non-main agents (interpolated)
// to carry in the workflow input.
//
// Maps to: codex-temporal config_loader.rs apply_crew_type()
func ApplyCrewType(crew *CrewType, inputs map[string]string, cfg *SessionConfiguration) (map[string]CrewAgentDef, error) {
	// 1. Validate required inputs.
	if err := crew.ValidateInputs(inputs); err != nil {
		return nil, err
	}

	// 2. Build interpolation vars.
	vars := crew.BuildVars(inputs)

	// 3. Apply approval_policy override.
	if crew.ApprovalPolicy != "" {
		cfg.Permissions.ApprovalMode = ApprovalMode(crew.ApprovalPolicy)
	}

	// 4. Apply main agent overrides.
	mainDef := crew.Agents[crew.MainAgent]
	if mainDef.Model != "" {
		cfg.Model.Model = mainDef.Model
	}
	if mainDef.Instructions != "" {
		cfg.DeveloperInstructions = Interpolate(mainDef.Instructions, vars)
	}

	// 5. Set user message from initial_prompt (autonomous mode).
	// The caller should use the returned string as the user message.
	// We store it in BaseInstructions as a side-channel; the caller
	// will extract it from the return value instead.

	// 6. Build non-main agent map with interpolated values.
	crewAgents := make(map[string]CrewAgentDef, len(crew.Agents)-1)
	for name, def := range crew.Agents {
		if name == crew.MainAgent {
			continue
		}
		interpolated := CrewAgentDef{
			Role:            def.Role,
			Model:           def.Model,
			Description:     Interpolate(def.Description, vars),
			Instructions:    Interpolate(def.Instructions, vars),
			AvailableAgents: def.AvailableAgents,
		}
		crewAgents[name] = interpolated
	}

	// Also include the main agent in the map so its available_agents can be
	// looked up during spawn scoping. Mark it with a special key convention.
	mainInterpolated := CrewAgentDef{
		Role:            mainDef.Role,
		Model:           mainDef.Model,
		Description:     Interpolate(mainDef.Description, vars),
		Instructions:    Interpolate(mainDef.Instructions, vars),
		AvailableAgents: mainDef.AvailableAgents,
	}
	crewAgents[crew.MainAgent] = mainInterpolated

	return crewAgents, nil
}

// InterpolatedInitialPrompt returns the interpolated initial_prompt for autonomous crews.
func (c *CrewType) InterpolatedInitialPrompt(inputs map[string]string) string {
	vars := c.BuildVars(inputs)
	return Interpolate(c.InitialPrompt, vars)
}

// CrewSummary is a lightweight view of a crew for listing.
type CrewSummary struct {
	Name        string   `json:"name"`
	Mode        CrewMode `json:"mode"`
	Description string   `json:"description"`
	Inputs      []string `json:"inputs"` // Required input names
}

// Summary returns a CrewSummary for display purposes.
func (c *CrewType) Summary() CrewSummary {
	var requiredInputs []string
	for name, spec := range c.Inputs {
		if spec.IsRequired() {
			requiredInputs = append(requiredInputs, name)
		}
	}
	sort.Strings(requiredInputs)
	return CrewSummary{
		Name:        c.Name,
		Mode:        c.Mode,
		Description: c.Description,
		Inputs:      requiredInputs,
	}
}
