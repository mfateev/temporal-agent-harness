package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validCrewTOML = `
name = "bug-fixer"
description = "Fixes bugs from GitHub issues"
mode = "autonomous"
initial_prompt = "Fix the bug described in {issue_url}"
main_agent = "coordinator"

[inputs.issue_url]
description = "GitHub issue URL"
required = true

[inputs.branch]
description = "Target branch"
required = false
default = "main"

[agents.coordinator]
model = "o3"
instructions = "You coordinate bug fixing for {issue_url} on branch {branch}."
available_agents = ["investigator", "fixer"]

[agents.investigator]
role = "explorer"
description = "Investigates the bug's root cause"

[agents.fixer]
role = "worker"
description = "Implements the fix"
available_agents = ["investigator"]
`

func TestParseCrewType_Valid(t *testing.T) {
	crew, err := ParseCrewType([]byte(validCrewTOML))
	require.NoError(t, err)

	assert.Equal(t, "bug-fixer", crew.Name)
	assert.Equal(t, "Fixes bugs from GitHub issues", crew.Description)
	assert.Equal(t, CrewModeAutonomous, crew.Mode)
	assert.Equal(t, "Fix the bug described in {issue_url}", crew.InitialPrompt)
	assert.Equal(t, "coordinator", crew.MainAgent)

	// Inputs
	require.Len(t, crew.Inputs, 2)
	assert.True(t, crew.Inputs["issue_url"].IsRequired())
	assert.False(t, crew.Inputs["branch"].IsRequired())
	assert.Equal(t, "main", crew.Inputs["branch"].Default)

	// Agents
	require.Len(t, crew.Agents, 3)

	coord := crew.Agents["coordinator"]
	assert.Equal(t, "o3", coord.Model)
	assert.Equal(t, []string{"investigator", "fixer"}, coord.AvailableAgents)

	inv := crew.Agents["investigator"]
	assert.Equal(t, "explorer", inv.Role)
	assert.Equal(t, "Investigates the bug's root cause", inv.Description)
	assert.Empty(t, inv.AvailableAgents)

	fixer := crew.Agents["fixer"]
	assert.Equal(t, "worker", fixer.Role)
	assert.Equal(t, []string{"investigator"}, fixer.AvailableAgents)
}

func TestParseCrewType_MissingName(t *testing.T) {
	toml := `
main_agent = "a"
[agents.a]
description = "test"
`
	_, err := ParseCrewType([]byte(toml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing required field: name")
}

func TestParseCrewType_MissingMainAgent(t *testing.T) {
	toml := `
name = "test"
[agents.a]
description = "test"
`
	_, err := ParseCrewType([]byte(toml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing required field: main_agent")
}

func TestParseCrewType_MainAgentNotInAgents(t *testing.T) {
	toml := `
name = "test"
main_agent = "nonexistent"
[agents.a]
description = "test"
`
	_, err := ParseCrewType([]byte(toml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "main_agent \"nonexistent\" not found in agents")
}

func TestParseCrewType_NoAgents(t *testing.T) {
	toml := `
name = "test"
main_agent = "a"
`
	_, err := ParseCrewType([]byte(toml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no agents defined")
}

func TestParseCrewType_InvalidMode(t *testing.T) {
	toml := `
name = "test"
mode = "invalid"
main_agent = "a"
[agents.a]
description = "test"
`
	_, err := ParseCrewType([]byte(toml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid mode")
}

func TestParseCrewType_AutonomousWithoutPrompt(t *testing.T) {
	toml := `
name = "test"
mode = "autonomous"
main_agent = "a"
[agents.a]
description = "test"
`
	_, err := ParseCrewType([]byte(toml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "autonomous mode requires initial_prompt")
}

func TestParseCrewType_DefaultMode(t *testing.T) {
	toml := `
name = "test"
main_agent = "a"
[agents.a]
description = "test"
`
	crew, err := ParseCrewType([]byte(toml))
	require.NoError(t, err)
	assert.Equal(t, CrewModeInteractive, crew.Mode)
}

func TestParseCrewType_InvalidAvailableAgentsRef(t *testing.T) {
	toml := `
name = "test"
main_agent = "a"
[agents.a]
description = "test"
available_agents = ["nonexistent"]
`
	_, err := ParseCrewType([]byte(toml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "references unknown agent \"nonexistent\"")
}

func TestParseCrewType_InvalidTOML(t *testing.T) {
	_, err := ParseCrewType([]byte("not valid toml {{{{"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid crew TOML")
}

func TestInterpolate(t *testing.T) {
	tests := []struct {
		name     string
		template string
		vars     map[string]string
		expected string
	}{
		{
			name:     "single placeholder",
			template: "Fix {issue_url}",
			vars:     map[string]string{"issue_url": "https://github.com/org/repo/issues/42"},
			expected: "Fix https://github.com/org/repo/issues/42",
		},
		{
			name:     "multiple placeholders",
			template: "Fix {issue_url} on {branch}",
			vars:     map[string]string{"issue_url": "https://example.com/1", "branch": "main"},
			expected: "Fix https://example.com/1 on main",
		},
		{
			name:     "repeated placeholder",
			template: "{x} and {x}",
			vars:     map[string]string{"x": "hello"},
			expected: "hello and hello",
		},
		{
			name:     "unknown placeholder left as-is",
			template: "Hello {unknown}",
			vars:     map[string]string{},
			expected: "Hello {unknown}",
		},
		{
			name:     "empty vars",
			template: "no placeholders",
			vars:     map[string]string{},
			expected: "no placeholders",
		},
		{
			name:     "empty template",
			template: "",
			vars:     map[string]string{"x": "y"},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Interpolate(tt.template, tt.vars)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestValidateInputs_AllPresent(t *testing.T) {
	crew, err := ParseCrewType([]byte(validCrewTOML))
	require.NoError(t, err)

	err = crew.ValidateInputs(map[string]string{
		"issue_url": "https://github.com/org/repo/issues/42",
	})
	assert.NoError(t, err)
}

func TestValidateInputs_MissingRequired(t *testing.T) {
	crew, err := ParseCrewType([]byte(validCrewTOML))
	require.NoError(t, err)

	err = crew.ValidateInputs(map[string]string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "issue_url")
}

func TestValidateInputs_OptionalMissing(t *testing.T) {
	crew, err := ParseCrewType([]byte(validCrewTOML))
	require.NoError(t, err)

	// Only provide required input, skip optional "branch"
	err = crew.ValidateInputs(map[string]string{
		"issue_url": "https://github.com/org/repo/issues/42",
	})
	assert.NoError(t, err)
}

func TestBuildVars_WithDefaults(t *testing.T) {
	crew, err := ParseCrewType([]byte(validCrewTOML))
	require.NoError(t, err)

	vars := crew.BuildVars(map[string]string{
		"issue_url": "https://github.com/org/repo/issues/42",
	})
	assert.Equal(t, "https://github.com/org/repo/issues/42", vars["issue_url"])
	assert.Equal(t, "main", vars["branch"]) // default
}

func TestBuildVars_OverrideDefault(t *testing.T) {
	crew, err := ParseCrewType([]byte(validCrewTOML))
	require.NoError(t, err)

	vars := crew.BuildVars(map[string]string{
		"issue_url": "https://github.com/org/repo/issues/42",
		"branch":    "develop",
	})
	assert.Equal(t, "develop", vars["branch"]) // user override
}

func TestApplyCrewType_Basic(t *testing.T) {
	crew, err := ParseCrewType([]byte(validCrewTOML))
	require.NoError(t, err)

	cfg := DefaultSessionConfiguration()
	cfg.Model.Model = "gpt-4o"

	crewAgents, err := ApplyCrewType(crew, map[string]string{
		"issue_url": "https://github.com/org/repo/issues/42",
	}, &cfg)
	require.NoError(t, err)

	// Main agent model override
	assert.Equal(t, "o3", cfg.Model.Model)

	// Main agent instructions interpolated
	assert.Contains(t, cfg.DeveloperInstructions, "https://github.com/org/repo/issues/42")
	assert.Contains(t, cfg.DeveloperInstructions, "branch main") // default

	// Non-main agents in returned map
	require.Contains(t, crewAgents, "investigator")
	require.Contains(t, crewAgents, "fixer")

	// Main agent also in map (for available_agents lookup)
	require.Contains(t, crewAgents, "coordinator")

	inv := crewAgents["investigator"]
	assert.Equal(t, "explorer", inv.Role)
	assert.Equal(t, "Investigates the bug's root cause", inv.Description)

	fixer := crewAgents["fixer"]
	assert.Equal(t, "worker", fixer.Role)
	assert.Equal(t, []string{"investigator"}, fixer.AvailableAgents)
}

func TestApplyCrewType_MissingRequiredInput(t *testing.T) {
	crew, err := ParseCrewType([]byte(validCrewTOML))
	require.NoError(t, err)

	cfg := DefaultSessionConfiguration()
	_, err = ApplyCrewType(crew, map[string]string{}, &cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing required inputs")
}

func TestApplyCrewType_ApprovalPolicyOverride(t *testing.T) {
	toml := `
name = "auto-crew"
main_agent = "a"
approval_policy = "never"
[agents.a]
description = "test"
`
	crew, err := ParseCrewType([]byte(toml))
	require.NoError(t, err)

	cfg := DefaultSessionConfiguration()
	cfg.Permissions.ApprovalMode = ApprovalUnlessTrusted

	_, err = ApplyCrewType(crew, nil, &cfg)
	require.NoError(t, err)
	assert.Equal(t, ApprovalNever, cfg.Permissions.ApprovalMode)
}

func TestApplyCrewType_InterpolatesDescriptions(t *testing.T) {
	toml := `
name = "test"
mode = "autonomous"
initial_prompt = "Do {task}"
main_agent = "main"

[inputs.task]
description = "The task"

[agents.main]
instructions = "You do {task}"
available_agents = ["helper"]

[agents.helper]
description = "Helps with {task}"
instructions = "Help with {task}"
`
	crew, err := ParseCrewType([]byte(toml))
	require.NoError(t, err)

	cfg := DefaultSessionConfiguration()
	crewAgents, err := ApplyCrewType(crew, map[string]string{"task": "fixing bugs"}, &cfg)
	require.NoError(t, err)

	// Main agent instructions
	assert.Equal(t, "You do fixing bugs", cfg.DeveloperInstructions)

	// Helper description and instructions interpolated
	helper := crewAgents["helper"]
	assert.Equal(t, "Helps with fixing bugs", helper.Description)
	assert.Equal(t, "Help with fixing bugs", helper.Instructions)
}

func TestInterpolatedInitialPrompt(t *testing.T) {
	crew, err := ParseCrewType([]byte(validCrewTOML))
	require.NoError(t, err)

	prompt := crew.InterpolatedInitialPrompt(map[string]string{
		"issue_url": "https://example.com/1",
	})
	assert.Equal(t, "Fix the bug described in https://example.com/1", prompt)
}

func TestCrewSummary(t *testing.T) {
	crew, err := ParseCrewType([]byte(validCrewTOML))
	require.NoError(t, err)

	s := crew.Summary()
	assert.Equal(t, "bug-fixer", s.Name)
	assert.Equal(t, CrewModeAutonomous, s.Mode)
	assert.Equal(t, "Fixes bugs from GitHub issues", s.Description)
	assert.Equal(t, []string{"issue_url"}, s.Inputs) // only required inputs
}

func TestCrewInputSpec_DefaultRequired(t *testing.T) {
	// When Required is nil, it should default to true
	spec := CrewInputSpec{Description: "test"}
	assert.True(t, spec.IsRequired())
}

func TestCrewInputSpec_ExplicitFalse(t *testing.T) {
	f := false
	spec := CrewInputSpec{Description: "test", Required: &f}
	assert.False(t, spec.IsRequired())
}

func TestCrewInputSpec_ExplicitTrue(t *testing.T) {
	tr := true
	spec := CrewInputSpec{Description: "test", Required: &tr}
	assert.True(t, spec.IsRequired())
}

func TestParseCrewType_InteractiveMode(t *testing.T) {
	toml := `
name = "interactive-crew"
mode = "interactive"
main_agent = "a"
[agents.a]
description = "test"
`
	crew, err := ParseCrewType([]byte(toml))
	require.NoError(t, err)
	assert.Equal(t, CrewModeInteractive, crew.Mode)
	assert.Empty(t, crew.InitialPrompt) // no initial_prompt needed for interactive
}

func TestApplyCrewType_NoModelOverride(t *testing.T) {
	toml := `
name = "no-model"
main_agent = "a"
[agents.a]
instructions = "Do stuff"
`
	crew, err := ParseCrewType([]byte(toml))
	require.NoError(t, err)

	cfg := DefaultSessionConfiguration()
	originalModel := cfg.Model.Model

	_, err = ApplyCrewType(crew, nil, &cfg)
	require.NoError(t, err)
	assert.Equal(t, originalModel, cfg.Model.Model) // unchanged
}

func TestApplyCrewType_NoInstructionsOverride(t *testing.T) {
	toml := `
name = "no-instructions"
main_agent = "a"
[agents.a]
model = "gpt-4"
`
	crew, err := ParseCrewType([]byte(toml))
	require.NoError(t, err)

	cfg := DefaultSessionConfiguration()
	cfg.DeveloperInstructions = "original"

	_, err = ApplyCrewType(crew, nil, &cfg)
	require.NoError(t, err)
	assert.Equal(t, "original", cfg.DeveloperInstructions) // unchanged
}
