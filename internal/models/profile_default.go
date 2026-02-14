package models

// defaultProfile is the base layer of the profile resolution chain.
// It defines the default behavior when no provider-specific profile matches.
var defaultProfile = ModelProfile{
	// AgentsFileNames: override priority order for project doc discovery.
	// AGENTS.override.md > AGENTS.md > CLAUDE.md
	AgentsFileNames: []string{"AGENTS.override.md", "AGENTS.md", "CLAUDE.md"},
}
