package models

import "strings"

// ReasoningEffort controls the level of reasoning effort for reasoning models.
//
// Maps to: codex-rs/core/src/config_types.rs ReasoningEffort
type ReasoningEffort string

const (
	ReasoningEffortNone    ReasoningEffort = "none"
	ReasoningEffortMinimal ReasoningEffort = "minimal"
	ReasoningEffortLow     ReasoningEffort = "low"
	ReasoningEffortMedium  ReasoningEffort = "medium"
	ReasoningEffortHigh    ReasoningEffort = "high"
	ReasoningEffortXHigh   ReasoningEffort = "xhigh"
)

// ParseReasoningEffort parses a string into a ReasoningEffort.
// Returns the effort and true if valid, or ("", false) if unrecognized.
func ParseReasoningEffort(s string) (ReasoningEffort, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "none":
		return ReasoningEffortNone, true
	case "minimal":
		return ReasoningEffortMinimal, true
	case "low":
		return ReasoningEffortLow, true
	case "medium", "med":
		return ReasoningEffortMedium, true
	case "high":
		return ReasoningEffortHigh, true
	case "xhigh", "x-high", "extra-high":
		return ReasoningEffortXHigh, true
	default:
		return "", false
	}
}

// ReasoningEffortPreset describes a supported reasoning effort level with a
// human-readable description for the TUI selector.
type ReasoningEffortPreset struct {
	Effort      ReasoningEffort
	Description string
}

// ReasoningSummary controls the level of reasoning summary in responses.
//
// Maps to: codex-rs/core/src/config_types.rs ReasoningSummary
type ReasoningSummary string

const (
	ReasoningSummaryAuto     ReasoningSummary = "auto"
	ReasoningSummaryConcise  ReasoningSummary = "concise"
	ReasoningSummaryDetailed ReasoningSummary = "detailed"
	ReasoningSummaryNone     ReasoningSummary = "none"
)

// ParseReasoningSummary parses a string into a ReasoningSummary.
// Returns the summary and true if valid, or ("", false) if unrecognized.
func ParseReasoningSummary(s string) (ReasoningSummary, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "auto":
		return ReasoningSummaryAuto, true
	case "concise":
		return ReasoningSummaryConcise, true
	case "detailed":
		return ReasoningSummaryDetailed, true
	case "none":
		return ReasoningSummaryNone, true
	default:
		return "", false
	}
}
