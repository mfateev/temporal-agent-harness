package models

// ModelProfile defines a layer in the profile resolution chain.
// Nil pointer fields mean "inherit from parent"; non-nil means "override".
//
// Resolution order: default → provider → model (regexp match).
type ModelProfile struct {
	// Provider matches a provider name ("openai", "anthropic").
	// Empty string means this is the default profile.
	Provider string

	// ModelPattern is a regexp that matches model names.
	// Empty string means this profile applies to all models for the provider.
	ModelPattern string

	// BasePrompt overrides the base system prompt. nil = inherit.
	BasePrompt *string

	// PromptSuffix is appended after the base prompt. Additive across layers.
	PromptSuffix string

	// AgentsFileNames overrides the project doc filename list. nil = inherit.
	AgentsFileNames []string

	// Tools overrides tool configuration. nil = inherit.
	Tools *ToolOverrides

	// Temperature overrides the default temperature. nil = inherit.
	Temperature *float64

	// MaxTokens overrides the default max tokens. nil = inherit.
	MaxTokens *int

	// ContextWindow overrides the default context window. nil = inherit.
	ContextWindow *int
}

// ToolOverrides configures tool-level overrides for a profile.
type ToolOverrides struct {
	// Disable lists tool names to remove from the spec set.
	Disable []string
}

// ResolvedProfile is a fully merged profile with no nil fields.
// Ready for direct use by the workflow.
type ResolvedProfile struct {
	BasePrompt      string
	PromptSuffix    string
	AgentsFileNames []string
	Tools           *ToolOverrides
	Temperature     *float64
	MaxTokens       *int
	ContextWindow   *int
}

// mergeProfiles merges overlay on top of base. Overlay's non-zero/non-nil
// fields take precedence. PromptSuffix is additive (concatenated).
// ToolOverrides.Disable lists are merged (union).
func mergeProfiles(base, overlay ModelProfile) ModelProfile {
	result := base

	if overlay.BasePrompt != nil {
		result.BasePrompt = overlay.BasePrompt
	}

	// PromptSuffix is additive across layers
	if overlay.PromptSuffix != "" {
		if result.PromptSuffix != "" {
			result.PromptSuffix = result.PromptSuffix + "\n\n" + overlay.PromptSuffix
		} else {
			result.PromptSuffix = overlay.PromptSuffix
		}
	}

	if overlay.AgentsFileNames != nil {
		result.AgentsFileNames = overlay.AgentsFileNames
	}

	// Tools: merge Disable lists (union)
	if overlay.Tools != nil {
		if result.Tools == nil {
			result.Tools = &ToolOverrides{
				Disable: append([]string{}, overlay.Tools.Disable...),
			}
		} else {
			merged := append([]string{}, result.Tools.Disable...)
			merged = append(merged, overlay.Tools.Disable...)
			result.Tools = &ToolOverrides{Disable: merged}
		}
	}

	if overlay.Temperature != nil {
		result.Temperature = overlay.Temperature
	}
	if overlay.MaxTokens != nil {
		result.MaxTokens = overlay.MaxTokens
	}
	if overlay.ContextWindow != nil {
		result.ContextWindow = overlay.ContextWindow
	}

	return result
}
