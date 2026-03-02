package activities

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testCrewTOML = `
name = "bug-fixer"
description = "Fixes bugs from GitHub issues"
mode = "autonomous"
initial_prompt = "Fix the bug described in {issue_url}"
main_agent = "coordinator"
approval_policy = "never"

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
description = "Coordinates the bug fix"
available_agents = ["investigator", "fixer"]

[agents.investigator]
role = "explorer"
description = "Investigates the bug at {issue_url}"

[agents.fixer]
role = "worker"
description = "Implements the fix"
available_agents = ["investigator"]
`

// setupTestCrew writes a crew TOML to a temp directory and returns the codex home path.
func setupTestCrew(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	crewDir := filepath.Join(dir, "crews")
	require.NoError(t, os.MkdirAll(crewDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(crewDir, name+".toml"), []byte(content), 0o644))
	return dir
}

func TestResolveCrewMain_Basic(t *testing.T) {
	codexHome := setupTestCrew(t, "bug-fixer", testCrewTOML)
	a := NewCrewActivities()

	out, err := a.ResolveCrewMain(context.Background(), ResolveCrewMainInput{
		CodexHome:  codexHome,
		CrewName:   "bug-fixer",
		CrewInputs: map[string]string{"issue_url": "https://github.com/org/repo/issues/42"},
	})
	require.NoError(t, err)

	assert.Equal(t, "coordinator", out.MainAgentName)
	assert.Equal(t, "o3", out.MainAgentDef.Model)
	assert.Equal(t, "never", out.ApprovalPolicy)
	assert.Equal(t, "Coordinates the bug fix", out.MainAgentDef.Description)
}

func TestResolveCrewMain_InterpolatesFields(t *testing.T) {
	codexHome := setupTestCrew(t, "bug-fixer", testCrewTOML)
	a := NewCrewActivities()

	out, err := a.ResolveCrewMain(context.Background(), ResolveCrewMainInput{
		CodexHome:  codexHome,
		CrewName:   "bug-fixer",
		CrewInputs: map[string]string{"issue_url": "https://example.com/1"},
	})
	require.NoError(t, err)

	// Instructions should be interpolated with both user input and default values.
	assert.Contains(t, out.MainAgentDef.Instructions, "https://example.com/1")
	assert.Contains(t, out.MainAgentDef.Instructions, "branch main") // default

	// InitialPrompt should be interpolated for autonomous mode.
	assert.Equal(t, "Fix the bug described in https://example.com/1", out.InitialPrompt)
}

func TestResolveCrewMain_MissingCrew(t *testing.T) {
	dir := t.TempDir()
	a := NewCrewActivities()

	_, err := a.ResolveCrewMain(context.Background(), ResolveCrewMainInput{
		CodexHome:  dir,
		CrewName:   "nonexistent",
		CrewInputs: nil,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestResolveCrewMain_MissingInputs(t *testing.T) {
	codexHome := setupTestCrew(t, "bug-fixer", testCrewTOML)
	a := NewCrewActivities()

	_, err := a.ResolveCrewMain(context.Background(), ResolveCrewMainInput{
		CodexHome:  codexHome,
		CrewName:   "bug-fixer",
		CrewInputs: map[string]string{}, // missing required "issue_url"
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing required inputs")
}

func TestResolveCrewAgent_Basic(t *testing.T) {
	codexHome := setupTestCrew(t, "bug-fixer", testCrewTOML)
	a := NewCrewActivities()

	out, err := a.ResolveCrewAgent(context.Background(), ResolveCrewAgentInput{
		CodexHome:  codexHome,
		CrewName:   "bug-fixer",
		AgentName:  "coordinator",
		CrewInputs: map[string]string{"issue_url": "https://example.com/1"},
	})
	require.NoError(t, err)

	assert.Equal(t, "o3", out.AgentDef.Model)
	assert.Contains(t, out.AgentDef.Instructions, "https://example.com/1")

	// Coordinator can see investigator and fixer.
	require.Len(t, out.AvailableAgents, 2)
	names := make(map[string]string)
	for _, a := range out.AvailableAgents {
		names[a.Name] = a.Description
	}
	assert.Contains(t, names, "investigator")
	assert.Contains(t, names, "fixer")
	// Descriptions should be interpolated.
	assert.Contains(t, names["investigator"], "https://example.com/1")
}

func TestResolveCrewAgent_NoAvailableAgents(t *testing.T) {
	codexHome := setupTestCrew(t, "bug-fixer", testCrewTOML)
	a := NewCrewActivities()

	out, err := a.ResolveCrewAgent(context.Background(), ResolveCrewAgentInput{
		CodexHome:  codexHome,
		CrewName:   "bug-fixer",
		AgentName:  "investigator",
		CrewInputs: map[string]string{"issue_url": "https://example.com/1"},
	})
	require.NoError(t, err)

	assert.Equal(t, "explorer", out.AgentDef.Role)
	assert.Empty(t, out.AvailableAgents) // investigator has no available_agents
}

func TestResolveCrewAgent_UnknownAgent(t *testing.T) {
	codexHome := setupTestCrew(t, "bug-fixer", testCrewTOML)
	a := NewCrewActivities()

	_, err := a.ResolveCrewAgent(context.Background(), ResolveCrewAgentInput{
		CodexHome:  codexHome,
		CrewName:   "bug-fixer",
		AgentName:  "nonexistent",
		CrewInputs: map[string]string{"issue_url": "https://example.com/1"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent \"nonexistent\" not found")
}

func TestResolveCrewMain_AvailableAgents(t *testing.T) {
	// Verify that main agent's available agents are in the output when
	// resolving via ResolveCrewAgent (not ResolveCrewMain — main uses
	// ResolveCrewAgent at init too).
	codexHome := setupTestCrew(t, "bug-fixer", testCrewTOML)
	a := NewCrewActivities()

	out, err := a.ResolveCrewAgent(context.Background(), ResolveCrewAgentInput{
		CodexHome:  codexHome,
		CrewName:   "bug-fixer",
		AgentName:  "coordinator",
		CrewInputs: map[string]string{"issue_url": "https://example.com/1"},
	})
	require.NoError(t, err)

	// Coordinator has available_agents = ["investigator", "fixer"]
	require.Len(t, out.AvailableAgents, 2)
	var agentNames []string
	for _, a := range out.AvailableAgents {
		agentNames = append(agentNames, a.Name)
	}
	assert.Contains(t, agentNames, "investigator")
	assert.Contains(t, agentNames, "fixer")

	// Each has a description
	for _, a := range out.AvailableAgents {
		assert.NotEmpty(t, a.Description, "agent %s should have a description", a.Name)
	}
}
