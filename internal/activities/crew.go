package activities

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mfateev/temporal-agent-harness/internal/models"
)

// CrewActivities contains crew-related activities.
type CrewActivities struct{}

// NewCrewActivities creates a new CrewActivities instance.
func NewCrewActivities() *CrewActivities {
	return &CrewActivities{}
}

// DiscoverCrewsInput is the input for the DiscoverCrews activity.
type DiscoverCrewsInput struct {
	CodexHome string `json:"codex_home"` // Path to codex config directory (e.g. ~/.codex)
}

// DiscoverCrewsOutput is the output from the DiscoverCrews activity.
type DiscoverCrewsOutput struct {
	Crews []models.CrewSummary `json:"crews"`
}

// DiscoverCrews scans {codex_home}/crews/*.toml and returns a sorted list of crew summaries.
func (a *CrewActivities) DiscoverCrews(ctx context.Context, input DiscoverCrewsInput) (DiscoverCrewsOutput, error) {
	crewDir := filepath.Join(input.CodexHome, "crews")

	entries, err := os.ReadDir(crewDir)
	if err != nil {
		if os.IsNotExist(err) {
			return DiscoverCrewsOutput{Crews: []models.CrewSummary{}}, nil
		}
		return DiscoverCrewsOutput{}, fmt.Errorf("failed to read crews directory %s: %w", crewDir, err)
	}

	var crews []models.CrewSummary
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(crewDir, entry.Name()))
		if err != nil {
			continue // skip unreadable files
		}

		crew, err := models.ParseCrewType(data)
		if err != nil {
			continue // skip invalid crews
		}

		crews = append(crews, crew.Summary())
	}

	sort.Slice(crews, func(i, j int) bool {
		return crews[i].Name < crews[j].Name
	})

	if crews == nil {
		crews = []models.CrewSummary{}
	}

	return DiscoverCrewsOutput{Crews: crews}, nil
}

// LoadCrewInput is the input for the LoadCrew activity.
type LoadCrewInput struct {
	CodexHome string `json:"codex_home"`
	Name      string `json:"name"`
}

// LoadCrewOutput is the output from the LoadCrew activity.
type LoadCrewOutput struct {
	Crew *models.CrewType `json:"crew"`
}

// LoadCrew loads a single crew by name from {codex_home}/crews/{name}.toml.
func (a *CrewActivities) LoadCrew(ctx context.Context, input LoadCrewInput) (LoadCrewOutput, error) {
	crewPath := filepath.Join(input.CodexHome, "crews", input.Name+".toml")

	data, err := os.ReadFile(crewPath)
	if err != nil {
		if os.IsNotExist(err) {
			return LoadCrewOutput{}, fmt.Errorf("crew %q not found at %s", input.Name, crewPath)
		}
		return LoadCrewOutput{}, fmt.Errorf("failed to read crew %q: %w", input.Name, err)
	}

	crew, err := models.ParseCrewType(data)
	if err != nil {
		return LoadCrewOutput{}, fmt.Errorf("failed to parse crew %q: %w", input.Name, err)
	}

	return LoadCrewOutput{Crew: crew}, nil
}

// ---------------------------------------------------------------------------
// ResolveCrewMain — resolves the main agent's config from a crew template.
// Called by SessionWorkflow to apply main agent overrides before profile resolution.
// ---------------------------------------------------------------------------

// ResolveCrewMainInput is the input for the ResolveCrewMain activity.
type ResolveCrewMainInput struct {
	CodexHome  string            `json:"codex_home"`
	CrewName   string            `json:"crew_name"`
	CrewInputs map[string]string `json:"crew_inputs"`
}

// ResolveCrewMainOutput is the output from the ResolveCrewMain activity.
type ResolveCrewMainOutput struct {
	MainAgentName  string              `json:"main_agent_name"`
	MainAgentDef   models.CrewAgentDef `json:"main_agent_def"`
	ApprovalPolicy string              `json:"approval_policy,omitempty"`
	Mode           models.CrewMode     `json:"mode"`
	InitialPrompt  string              `json:"initial_prompt,omitempty"`
}

// ResolveCrewMain loads the crew TOML, validates inputs, interpolates the main
// agent definition, and returns it for SessionWorkflow to apply as config overrides.
func (a *CrewActivities) ResolveCrewMain(ctx context.Context, input ResolveCrewMainInput) (ResolveCrewMainOutput, error) {
	crew, err := loadCrewByName(input.CodexHome, input.CrewName)
	if err != nil {
		return ResolveCrewMainOutput{}, err
	}

	if err := crew.ValidateInputs(input.CrewInputs); err != nil {
		return ResolveCrewMainOutput{}, fmt.Errorf("crew %q: %w", input.CrewName, err)
	}

	vars := crew.BuildVars(input.CrewInputs)
	mainDef := models.InterpolateAgentDef(crew.Agents[crew.MainAgent], vars)

	var initialPrompt string
	if crew.Mode == models.CrewModeAutonomous && crew.InitialPrompt != "" {
		initialPrompt = models.Interpolate(crew.InitialPrompt, vars)
	}

	return ResolveCrewMainOutput{
		MainAgentName:  crew.MainAgent,
		MainAgentDef:   mainDef,
		ApprovalPolicy: crew.ApprovalPolicy,
		Mode:           crew.Mode,
		InitialPrompt:  initialPrompt,
	}, nil
}

// ---------------------------------------------------------------------------
// ResolveCrewAgent — resolves a single agent's config + visible agents.
// Called by AgenticWorkflow at init (both main and children).
// ---------------------------------------------------------------------------

// CrewAgentSummary is a lightweight description of a visible crew agent.
type CrewAgentSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ResolveCrewAgentInput is the input for the ResolveCrewAgent activity.
type ResolveCrewAgentInput struct {
	CodexHome  string            `json:"codex_home"`
	CrewName   string            `json:"crew_name"`
	AgentName  string            `json:"agent_name"`
	CrewInputs map[string]string `json:"crew_inputs"`
}

// ResolveCrewAgentOutput is the output from the ResolveCrewAgent activity.
type ResolveCrewAgentOutput struct {
	AgentDef        models.CrewAgentDef `json:"agent_def"`
	AvailableAgents []CrewAgentSummary  `json:"available_agents"`
}

// ResolveCrewAgent loads the crew TOML, finds the named agent, interpolates it,
// and returns the agent definition along with its visible agents list.
func (a *CrewActivities) ResolveCrewAgent(ctx context.Context, input ResolveCrewAgentInput) (ResolveCrewAgentOutput, error) {
	crew, err := loadCrewByName(input.CodexHome, input.CrewName)
	if err != nil {
		return ResolveCrewAgentOutput{}, err
	}

	agentDef, ok := crew.Agents[input.AgentName]
	if !ok {
		return ResolveCrewAgentOutput{}, fmt.Errorf("crew %q: agent %q not found", input.CrewName, input.AgentName)
	}

	vars := crew.BuildVars(input.CrewInputs)
	interpolated := models.InterpolateAgentDef(agentDef, vars)

	// Build available agents list from this agent's AvailableAgents.
	var available []CrewAgentSummary
	for _, name := range agentDef.AvailableAgents {
		if peer, ok := crew.Agents[name]; ok {
			available = append(available, CrewAgentSummary{
				Name:        name,
				Description: models.Interpolate(peer.Description, vars),
			})
		}
	}

	return ResolveCrewAgentOutput{
		AgentDef:        interpolated,
		AvailableAgents: available,
	}, nil
}

// loadCrewByName is a shared helper that loads and parses a crew TOML by name.
func loadCrewByName(codexHome, crewName string) (*models.CrewType, error) {
	crewPath := filepath.Join(codexHome, "crews", crewName+".toml")
	data, err := os.ReadFile(crewPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("crew %q not found at %s", crewName, crewPath)
		}
		return nil, fmt.Errorf("failed to read crew %q: %w", crewName, err)
	}
	crew, err := models.ParseCrewType(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse crew %q: %w", crewName, err)
	}
	return crew, nil
}
