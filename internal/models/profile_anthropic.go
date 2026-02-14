package models

// anthropicProfile is the provider-wide profile for Anthropic models.
// CLAUDE.md is listed first for Anthropic since it's their native format.
var anthropicProfile = ModelProfile{
	Provider:        "anthropic",
	AgentsFileNames: []string{"CLAUDE.md", "AGENTS.override.md", "AGENTS.md"},
	PromptSuffix:    "When using tools, prefer sequential calls when results depend on each other. Use parallel tool calls only for independent operations.",
}
