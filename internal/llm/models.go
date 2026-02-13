package llm

import (
	"context"
	"os"
	"sort"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicopt "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/openai/openai-go"
	openaiopt "github.com/openai/openai-go/option"
)

// AvailableModel describes a model returned by a provider's list-models API.
type AvailableModel struct {
	Provider    string // "openai" or "anthropic"
	ID          string // model identifier usable in API calls
	DisplayName string // human-readable name (Anthropic provides this; empty for OpenAI)
}

// FetchAvailableModels queries each provider's Models.List API and returns a
// merged, sorted list. Providers whose API key env-var is unset are silently
// skipped. If every provider fails or is skipped the function returns (nil, nil)
// to signal the caller should fall back to a hardcoded list.
func FetchAvailableModels(ctx context.Context) ([]AvailableModel, error) {
	var all []AvailableModel

	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		models, err := fetchOpenAIModels(ctx, key)
		if err == nil {
			all = append(all, models...)
		}
	}

	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		models, err := fetchAnthropicModels(ctx, key)
		if err == nil {
			all = append(all, models...)
		}
	}

	if len(all) == 0 {
		return nil, nil
	}

	// Sort: anthropic first, then alphabetical by ID within each provider.
	sort.Slice(all, func(i, j int) bool {
		if all[i].Provider != all[j].Provider {
			return all[i].Provider < all[j].Provider // "anthropic" < "openai"
		}
		return all[i].ID < all[j].ID
	})

	return all, nil
}

// fetchOpenAIModels calls the OpenAI Models.List API and returns only chat-
// capable models (filtering out embeddings, dall-e, whisper, tts, etc.).
func fetchOpenAIModels(ctx context.Context, apiKey string) ([]AvailableModel, error) {
	client := openai.NewClient(openaiopt.WithAPIKey(apiKey))

	page, err := client.Models.List(ctx)
	if err != nil {
		return nil, err
	}

	var result []AvailableModel
	for _, m := range page.Data {
		if isOpenAIChatModel(m.ID) {
			result = append(result, AvailableModel{
				Provider: "openai",
				ID:       m.ID,
			})
		}
	}
	return result, nil
}

// isOpenAIChatModel returns true for model IDs that are chat/completion models.
// It filters out embeddings, image generation, audio, moderation, and fine-tunes.
func isOpenAIChatModel(id string) bool {
	// Exclude fine-tuned models
	if strings.HasPrefix(id, "ft:") {
		return false
	}

	// Known chat-model prefixes
	chatPrefixes := []string{
		"gpt-",
		"o1",
		"o3",
		"o4",
		"chatgpt-",
	}
	for _, prefix := range chatPrefixes {
		if strings.HasPrefix(id, prefix) {
			return true
		}
	}
	return false
}

// fetchAnthropicModels calls the Anthropic Models.ListAutoPaging API.
// Anthropic only returns Claude models so no filtering is needed.
func fetchAnthropicModels(ctx context.Context, apiKey string) ([]AvailableModel, error) {
	client := anthropic.NewClient(anthropicopt.WithAPIKey(apiKey))

	iter := client.Models.ListAutoPaging(ctx, anthropic.ModelListParams{})

	var result []AvailableModel
	for iter.Next() {
		m := iter.Current()
		result = append(result, AvailableModel{
			Provider:    "anthropic",
			ID:          m.ID,
			DisplayName: m.DisplayName,
		})
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
