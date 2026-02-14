package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolve_Default(t *testing.T) {
	registry := NewDefaultRegistry()
	profile := registry.Resolve("unknown-provider", "unknown-model")

	// Should get default AgentsFileNames
	assert.Equal(t, []string{"AGENTS.override.md", "AGENTS.md", "CLAUDE.md"}, profile.AgentsFileNames)
	// No tool overrides
	assert.Nil(t, profile.Tools)
	// No model parameter overrides
	assert.Nil(t, profile.Temperature)
	assert.Nil(t, profile.MaxTokens)
	assert.Nil(t, profile.ContextWindow)
}

func TestResolve_Anthropic(t *testing.T) {
	registry := NewDefaultRegistry()
	profile := registry.Resolve("anthropic", "claude-sonnet-4-5-20250929")

	// Anthropic: CLAUDE.md first
	assert.Equal(t, []string{"CLAUDE.md", "AGENTS.override.md", "AGENTS.md"}, profile.AgentsFileNames)
	// Should have Anthropic-specific prompt suffix
	assert.Contains(t, profile.PromptSuffix, "sequential")
}

func TestResolve_OpenAI(t *testing.T) {
	registry := NewDefaultRegistry()
	profile := registry.Resolve("openai", "gpt-4o")

	// OpenAI: no CLAUDE.md
	assert.Equal(t, []string{"AGENTS.override.md", "AGENTS.md"}, profile.AgentsFileNames)
	// No prompt suffix for standard OpenAI models
	assert.Empty(t, profile.PromptSuffix)
}

func TestResolve_OpenAIReasoning(t *testing.T) {
	registry := NewDefaultRegistry()

	for _, model := range []string{"o1-preview", "o3-mini", "o4-mini", "codex-mini"} {
		profile := registry.Resolve("openai", model)
		// Should match the reasoning profile (currently no extra overrides,
		// but should still get the openai provider base)
		assert.Equal(t, []string{"AGENTS.override.md", "AGENTS.md"}, profile.AgentsFileNames,
			"model %s should use OpenAI file names", model)
	}
}

func TestResolve_InheritNil(t *testing.T) {
	// A profile with only a PromptSuffix should inherit AgentsFileNames from default
	registry := &ProfileRegistry{
		profiles: []ModelProfile{
			{
				AgentsFileNames: []string{"DEFAULT.md"},
			},
			{
				Provider:     "test",
				PromptSuffix: "extra guidance",
			},
		},
	}

	profile := registry.Resolve("test", "any-model")
	// AgentsFileNames inherited from default
	assert.Equal(t, []string{"DEFAULT.md"}, profile.AgentsFileNames)
	// PromptSuffix from provider
	assert.Equal(t, "extra guidance", profile.PromptSuffix)
}

func TestResolve_PromptSuffixAdditive(t *testing.T) {
	registry := &ProfileRegistry{
		profiles: []ModelProfile{
			{
				PromptSuffix: "layer1",
			},
			{
				Provider:     "test",
				PromptSuffix: "layer2",
			},
			{
				Provider:     "test",
				ModelPattern:  "^special-",
				PromptSuffix: "layer3",
			},
		},
	}

	profile := registry.Resolve("test", "special-model")
	// All three suffixes should be concatenated
	assert.Contains(t, profile.PromptSuffix, "layer1")
	assert.Contains(t, profile.PromptSuffix, "layer2")
	assert.Contains(t, profile.PromptSuffix, "layer3")
}

func TestResolve_ToolDisable(t *testing.T) {
	registry := &ProfileRegistry{
		profiles: []ModelProfile{
			{
				Tools: &ToolOverrides{Disable: []string{"tool_a"}},
			},
			{
				Provider: "test",
				Tools:    &ToolOverrides{Disable: []string{"tool_b"}},
			},
		},
	}

	profile := registry.Resolve("test", "any-model")
	require.NotNil(t, profile.Tools)
	assert.Contains(t, profile.Tools.Disable, "tool_a")
	assert.Contains(t, profile.Tools.Disable, "tool_b")
}

func TestResolve_ModelPatternNoMatch(t *testing.T) {
	registry := &ProfileRegistry{
		profiles: []ModelProfile{
			{
				AgentsFileNames: []string{"DEFAULT.md"},
			},
			{
				Provider:        "test",
				ModelPattern:    "^special-",
				AgentsFileNames: []string{"SPECIAL.md"},
			},
		},
	}

	// Non-matching model should not get the model-specific override
	profile := registry.Resolve("test", "regular-model")
	assert.Equal(t, []string{"DEFAULT.md"}, profile.AgentsFileNames)

	// Matching model should get the override
	profile = registry.Resolve("test", "special-model")
	assert.Equal(t, []string{"SPECIAL.md"}, profile.AgentsFileNames)
}

func TestResolve_TemperatureOverride(t *testing.T) {
	temp := 0.3
	registry := &ProfileRegistry{
		profiles: []ModelProfile{
			{},
			{
				Provider:    "test",
				Temperature: &temp,
			},
		},
	}

	profile := registry.Resolve("test", "any-model")
	require.NotNil(t, profile.Temperature)
	assert.Equal(t, 0.3, *profile.Temperature)

	// Default profile should have nil temperature
	profile = registry.Resolve("other", "any-model")
	assert.Nil(t, profile.Temperature)
}

func TestResolve_BasePromptOverride(t *testing.T) {
	custom := "custom system prompt"
	registry := &ProfileRegistry{
		profiles: []ModelProfile{
			{},
			{
				Provider:   "test",
				BasePrompt: &custom,
			},
		},
	}

	profile := registry.Resolve("test", "any-model")
	assert.Equal(t, "custom system prompt", profile.BasePrompt)

	// Default should be empty (no BasePrompt set)
	profile = registry.Resolve("other", "any-model")
	assert.Empty(t, profile.BasePrompt)
}

func TestMergeProfiles_NilOverlay(t *testing.T) {
	base := ModelProfile{
		AgentsFileNames: []string{"BASE.md"},
		PromptSuffix:    "base suffix",
	}
	overlay := ModelProfile{} // all nil/zero values

	result := mergeProfiles(base, overlay)
	assert.Equal(t, []string{"BASE.md"}, result.AgentsFileNames)
	assert.Equal(t, "base suffix", result.PromptSuffix)
}

func TestMergeProfiles_OverlayReplaces(t *testing.T) {
	base := ModelProfile{
		AgentsFileNames: []string{"BASE.md"},
	}
	overlay := ModelProfile{
		AgentsFileNames: []string{"OVERLAY.md"},
	}

	result := mergeProfiles(base, overlay)
	assert.Equal(t, []string{"OVERLAY.md"}, result.AgentsFileNames)
}

func TestNewDefaultRegistry(t *testing.T) {
	registry := NewDefaultRegistry()
	assert.NotNil(t, registry)
	assert.True(t, len(registry.profiles) > 0, "should have built-in profiles")
}
