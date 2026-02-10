package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetectProvider(t *testing.T) {
	tests := []struct {
		model    string
		expected string
	}{
		// OpenAI GPT models
		{"gpt-4o-mini", "openai"},
		{"gpt-4o", "openai"},
		{"gpt-4", "openai"},
		{"gpt-3.5-turbo", "openai"},
		{"GPT-4o", "openai"},

		// OpenAI o-series models
		{"o1-preview", "openai"},
		{"o1-mini", "openai"},
		{"o3-mini", "openai"},
		{"o4-mini", "openai"},

		// OpenAI chatgpt models
		{"chatgpt-4o-latest", "openai"},

		// Anthropic Claude models
		{"claude-3-opus-20240229", "anthropic"},
		{"claude-3-sonnet-20240229", "anthropic"},
		{"claude-3-haiku-20240307", "anthropic"},
		{"claude-3.5-sonnet", "anthropic"},
		{"Claude-3-opus", "anthropic"},

		// Google Gemini models
		{"gemini-pro", "google"},
		{"gemini-1.5-pro", "google"},

		// Default fallback
		{"unknown-model", "openai"},
		{"", "openai"},
		{"my-custom-model", "openai"},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			assert.Equal(t, tt.expected, DetectProvider(tt.model))
		})
	}
}
