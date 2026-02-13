package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsOpenAIChatModel(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		// --- Should PASS: canonical chat/codex models ---
		{"gpt-4o", true},
		{"gpt-4o-mini", true},
		{"gpt-4-turbo", true},
		{"gpt-3.5-turbo", true},
		{"gpt-4", true},
		{"gpt-4.1", true},
		{"gpt-4.1-mini", true},
		{"gpt-4.1-nano", true},
		{"gpt-5", true},
		{"gpt-5-mini", true},
		{"gpt-5-nano", true},
		{"gpt-5-pro", true},
		{"gpt-5-codex", true},
		{"gpt-5.1", true},
		{"gpt-5.1-codex", true},
		{"gpt-5.1-codex-max", true},
		{"gpt-5.1-codex-mini", true},
		{"gpt-5.2", true},
		{"gpt-5.2-pro", true},
		{"gpt-5.2-codex", true},
		{"o1", true},
		{"o1-pro", true},
		{"o1-mini", true},
		{"o3", true},
		{"o3-mini", true},
		{"o4-mini", true},
		{"chatgpt-4o-latest", true},

		// --- Should FAIL: non-chat model families ---
		{"text-embedding-ada-002", false},
		{"text-embedding-3-small", false},
		{"text-embedding-3-large", false},
		{"dall-e-3", false},
		{"dall-e-2", false},
		{"whisper-1", false},
		{"tts-1", false},
		{"tts-1-hd", false},
		{"text-moderation-latest", false},
		{"text-moderation-stable", false},
		{"babbage-002", false},
		{"davinci-002", false},

		// --- Should FAIL: capability exclusions (non-chat gpt- models) ---
		{"gpt-4o-mini-tts", false},
		{"gpt-4o-mini-tts-2025-03-20", false},
		{"gpt-4o-realtime-preview", false},
		{"gpt-4o-realtime-preview-2024-12-17", false},
		{"gpt-4o-transcribe", false},
		{"gpt-4o-transcribe-diarize", false},
		{"gpt-3.5-turbo-instruct", false},
		{"gpt-3.5-turbo-instruct-0914", false},
		{"gpt-audio", false},
		{"gpt-audio-mini", false},
		{"gpt-audio-mini-2025-10-06", false},
		{"gpt-image-1", false},
		{"gpt-image-1-mini", false},
		{"gpt-image-1.5", false},
		{"gpt-realtime", false},
		{"gpt-realtime-mini", false},
		{"chatgpt-image-latest", false},
		{"ft:gpt-4o-mini:my-org:custom:abc123", false},
		{"ft:gpt-3.5-turbo:org:name:id", false},

		// --- Should FAIL: noise exclusions (date-pinned snapshots) ---
		{"gpt-4o-2024-05-13", false},
		{"gpt-4o-2024-08-06", false},
		{"gpt-4o-2024-11-20", false},
		{"gpt-4o-mini-2024-07-18", false},
		{"gpt-5-2025-08-07", false},
		{"gpt-5-mini-2025-08-07", false},
		{"gpt-5.1-2025-11-13", false},
		{"gpt-5.2-2025-12-11", false},
		{"gpt-5.2-pro-2025-12-11", false},
		{"gpt-4.1-2025-04-14", false},
		{"gpt-4.1-mini-2025-04-14", false},
		{"gpt-4.1-nano-2025-04-14", false},
		{"o1-2024-12-17", false},
		{"o1-pro-2025-03-19", false},
		{"o3-2025-04-16", false},
		{"o3-mini-2025-01-31", false},
		{"o4-mini-2025-04-16", false},
		{"gpt-4-0125-preview", false},
		{"gpt-4-0613", false},
		{"gpt-4-1106-preview", false},
		{"gpt-4-turbo-2024-04-09", false},

		// --- Should FAIL: noise exclusions (specialized variants) ---
		{"gpt-4-turbo-preview", false},
		{"gpt-4o-audio-preview", false},
		{"gpt-4o-audio-preview-2024-12-17", false},
		{"gpt-4o-mini-audio-preview", false},
		{"gpt-4o-mini-audio-preview-2024-12-17", false},
		{"gpt-4o-search-preview", false},
		{"gpt-4o-search-preview-2025-03-11", false},
		{"gpt-4o-mini-search-preview", false},
		{"gpt-4o-mini-search-preview-2025-03-11", false},
		{"gpt-5-search-api", false},
		{"gpt-5-search-api-2025-10-14", false},
		{"gpt-5-chat-latest", false},
		{"gpt-5.1-chat-latest", false},
		{"gpt-5.2-chat-latest", false},
		{"o4-mini-deep-research", false},
		{"o4-mini-deep-research-2025-06-26", false},
		{"gpt-3.5-turbo-16k", false},
		{"gpt-3.5-turbo-0125", false},
		{"gpt-3.5-turbo-1106", false},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			got := isOpenAIChatModel(tt.id)
			assert.Equal(t, tt.want, got, "isOpenAIChatModel(%q)", tt.id)
		})
	}
}
