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

// isOpenAIChatModel returns true for model IDs suitable for a coding-agent
// model selector. It applies two stages of filtering:
//
//  1. Capability filter — exclude non-chat model families (embeddings, image,
//     audio, TTS, realtime, transcription, moderation, fine-tunes).
//  2. Noise filter — exclude date-pinned snapshots, preview aliases, and
//     specialized variants (search, deep-research, audio-preview) that
//     clutter the selector. Codex (coding) variants are kept.
func isOpenAIChatModel(id string) bool {
	// --- Stage 1: capability exclusions ---

	if strings.HasPrefix(id, "ft:") {
		return false
	}

	// Non-chat substrings (anywhere in the ID)
	for _, sub := range []string{
		"-tts",        // text-to-speech (gpt-4o-mini-tts)
		"-realtime",   // realtime/voice (gpt-4o-realtime-preview)
		"-transcribe", // transcription (gpt-4o-transcribe)
		"-instruct",   // completion, not chat (gpt-3.5-turbo-instruct)
	} {
		if strings.Contains(id, sub) {
			return false
		}
	}

	// Non-chat prefixes
	for _, prefix := range []string{
		"gpt-audio",     // audio models (gpt-audio, gpt-audio-mini)
		"gpt-image",     // image generation (gpt-image-1)
		"chatgpt-image", // image generation (chatgpt-image-latest)
	} {
		if strings.HasPrefix(id, prefix) {
			return false
		}
	}

	// Must match a known chat prefix to proceed
	isChatModel := false
	for _, prefix := range []string{"gpt-", "o1", "o3", "o4", "chatgpt-"} {
		if strings.HasPrefix(id, prefix) {
			isChatModel = true
			break
		}
	}
	if !isChatModel {
		return false
	}

	// --- Stage 2: noise exclusions (keep selector concise) ---

	// Exclude date-pinned snapshots (e.g. gpt-4o-2024-05-13, o3-2025-04-16).
	// These contain "-YYYY-" or end with "-YYYYMMDD".
	if hasDateSuffix(id) {
		return false
	}

	// Exclude specialized / redundant variants
	for _, sub := range []string{
		"-preview",       // old previews (gpt-4-turbo-preview)
		"-audio-",        // audio-preview variants (gpt-4o-audio-preview)
		"-search",        // web-search models (gpt-4o-search-preview, gpt-5-search-api)
		"-deep-research", // specialized (o4-mini-deep-research)
		"-chat-latest",   // redundant alias (gpt-5-chat-latest)
	} {
		if strings.Contains(id, sub) {
			return false
		}
	}

	// Exclude legacy sized variants (base alias already present)
	if id == "gpt-3.5-turbo-16k" {
		return false
	}

	return true
}

// hasDateSuffix returns true if the model ID contains a date stamp.
// Matches both full dates like "-2024-05-13" (pattern "-20XX-") and
// legacy short formats like "-0613", "-0125", "-1106" (trailing 4+ digits).
func hasDateSuffix(id string) bool {
	// Full date: "-20XX-" anywhere in the ID
	for i := 0; i < len(id)-5; i++ {
		if id[i] == '-' && id[i+1] == '2' && id[i+2] == '0' &&
			isDigit(id[i+3]) && isDigit(id[i+4]) && id[i+5] == '-' {
			return true
		}
	}
	// Legacy short date: ends with "-" followed by 4+ digits (e.g. -0613, -0125)
	lastDash := strings.LastIndex(id, "-")
	if lastDash >= 0 && lastDash < len(id)-3 {
		suffix := id[lastDash+1:]
		if len(suffix) >= 4 && allDigits(suffix) {
			return true
		}
	}
	return false
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return len(s) > 0
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
