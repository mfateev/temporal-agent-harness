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
		// Chat models — should pass
		{"gpt-4o", true},
		{"gpt-4o-mini", true},
		{"gpt-4-turbo", true},
		{"gpt-3.5-turbo", true},
		{"gpt-4-0613", true},
		{"o1-preview", true},
		{"o1-mini", true},
		{"o3-mini", true},
		{"o4-mini", true},
		{"chatgpt-4o-latest", true},

		// Non-chat models — should be filtered out
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

		// gpt- prefixed non-chat models — should be filtered out
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

		// Fine-tuned models — should be filtered out
		{"ft:gpt-4o-mini:my-org:custom:abc123", false},
		{"ft:gpt-3.5-turbo:org:name:id", false},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			got := isOpenAIChatModel(tt.id)
			assert.Equal(t, tt.want, got, "isOpenAIChatModel(%q)", tt.id)
		})
	}
}
