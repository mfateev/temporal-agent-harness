package cli

import "strings"

// DetectProvider returns the provider name inferred from a model name string.
// Returns "openai" for GPT/o-series models, "anthropic" for Claude models,
// and "openai" as the fallback default.
func DetectProvider(model string) string {
	m := strings.ToLower(model)

	// OpenAI models
	if strings.HasPrefix(m, "gpt-") {
		return "openai"
	}
	if strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4") {
		return "openai"
	}
	if strings.HasPrefix(m, "chatgpt-") {
		return "openai"
	}

	// Anthropic models
	if strings.HasPrefix(m, "claude-") {
		return "anthropic"
	}

	// Google models
	if strings.HasPrefix(m, "gemini-") {
		return "google"
	}

	// Default to openai
	return "openai"
}
