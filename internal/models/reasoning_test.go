package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseReasoningEffort(t *testing.T) {
	tests := []struct {
		input string
		want  ReasoningEffort
		ok    bool
	}{
		{"low", ReasoningEffortLow, true},
		{"LOW", ReasoningEffortLow, true},
		{"Medium", ReasoningEffortMedium, true},
		{"med", ReasoningEffortMedium, true},
		{"high", ReasoningEffortHigh, true},
		{"xhigh", ReasoningEffortXHigh, true},
		{"x-high", ReasoningEffortXHigh, true},
		{"extra-high", ReasoningEffortXHigh, true},
		{"none", ReasoningEffortNone, true},
		{"minimal", ReasoningEffortMinimal, true},
		{"  high  ", ReasoningEffortHigh, true},
		{"invalid", "", false},
		{"", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, ok := ParseReasoningEffort(tt.input)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseReasoningSummary(t *testing.T) {
	tests := []struct {
		input string
		want  ReasoningSummary
		ok    bool
	}{
		{"auto", ReasoningSummaryAuto, true},
		{"AUTO", ReasoningSummaryAuto, true},
		{"concise", ReasoningSummaryConcise, true},
		{"detailed", ReasoningSummaryDetailed, true},
		{"none", ReasoningSummaryNone, true},
		{"  concise  ", ReasoningSummaryConcise, true},
		{"invalid", "", false},
		{"", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, ok := ParseReasoningSummary(tt.input)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}
