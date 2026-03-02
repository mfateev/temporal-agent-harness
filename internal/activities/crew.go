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
