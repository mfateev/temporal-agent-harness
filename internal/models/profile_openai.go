package models

// openaiProfile is the provider-wide profile for OpenAI models.
// No CLAUDE.md for OpenAI — only AGENTS files.
var openaiProfile = ModelProfile{
	Provider:        "openai",
	AgentsFileNames: []string{"AGENTS.override.md", "AGENTS.md"},
}

// openaiReasoningProfile applies to OpenAI reasoning models (o1, o3, o4, codex).
var openaiReasoningProfile = ModelProfile{
	Provider:     "openai",
	ModelPattern: `^(o1|o3|o4|codex)-`,
}

// builtinProfiles returns all built-in profiles in resolution order.
// Default first, then provider-wide, then model-specific.
func builtinProfiles() []ModelProfile {
	return []ModelProfile{
		defaultProfile,
		anthropicProfile,
		openaiProfile,
		openaiReasoningProfile,
	}
}
