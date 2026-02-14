package models

import "regexp"

// ProfileRegistry holds ordered ModelProfile entries and resolves them
// against a provider/model pair.
type ProfileRegistry struct {
	profiles []ModelProfile
}

// NewDefaultRegistry returns a registry populated with built-in profiles
// from the provider files (default, anthropic, openai).
func NewDefaultRegistry() *ProfileRegistry {
	return &ProfileRegistry{
		profiles: builtinProfiles(),
	}
}

// Resolve walks the registry profiles, matches by provider then by model
// regexp, merges layers, and returns a fully resolved profile.
//
// Resolution order: default (no provider) → provider-wide → model-specific.
func (r *ProfileRegistry) Resolve(provider, model string) ResolvedProfile {
	merged := ModelProfile{}

	for _, p := range r.profiles {
		if !profileMatches(p, provider, model) {
			continue
		}
		merged = mergeProfiles(merged, p)
	}

	return toResolved(merged)
}

// profileMatches returns true if the profile applies to the given provider/model.
func profileMatches(p ModelProfile, provider, model string) bool {
	// Default profile (no provider): always matches
	if p.Provider == "" && p.ModelPattern == "" {
		return true
	}

	// Provider must match (case-sensitive)
	if p.Provider != "" && p.Provider != provider {
		return false
	}

	// Provider-wide profile (no model pattern): matches all models for this provider
	if p.ModelPattern == "" {
		return true
	}

	// Model pattern match
	matched, err := regexp.MatchString(p.ModelPattern, model)
	if err != nil {
		return false
	}
	return matched
}

// toResolved converts a merged ModelProfile into a ResolvedProfile.
// All nil fields are replaced with zero values.
func toResolved(p ModelProfile) ResolvedProfile {
	r := ResolvedProfile{
		PromptSuffix:    p.PromptSuffix,
		AgentsFileNames: p.AgentsFileNames,
		Tools:           p.Tools,
		Temperature:     p.Temperature,
		MaxTokens:       p.MaxTokens,
		ContextWindow:   p.ContextWindow,
	}

	if p.BasePrompt != nil {
		r.BasePrompt = *p.BasePrompt
	}

	return r
}
