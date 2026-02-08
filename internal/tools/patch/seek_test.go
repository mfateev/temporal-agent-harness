package patch

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Tests ported from: codex-rs/apply-patch/src/seek_sequence.rs mod tests

func TestSeekSequence_ExactMatchFindsSequence(t *testing.T) {
	lines := []string{"foo", "bar", "baz"}
	pattern := []string{"bar", "baz"}
	assert.Equal(t, 1, seekSequence(lines, pattern, 0, false))
}

func TestSeekSequence_RStripMatchIgnoresTrailingWhitespace(t *testing.T) {
	lines := []string{"foo   ", "bar\t\t"}
	// Pattern omits trailing whitespace.
	pattern := []string{"foo", "bar"}
	assert.Equal(t, 0, seekSequence(lines, pattern, 0, false))
}

func TestSeekSequence_TrimMatchIgnoresLeadingAndTrailingWhitespace(t *testing.T) {
	lines := []string{"    foo   ", "   bar\t"}
	// Pattern omits any additional whitespace.
	pattern := []string{"foo", "bar"}
	assert.Equal(t, 0, seekSequence(lines, pattern, 0, false))
}

func TestSeekSequence_PatternLongerThanInputReturnsNone(t *testing.T) {
	lines := []string{"just one line"}
	pattern := []string{"too", "many", "lines"}
	// Should not panic – must return -1 when pattern cannot possibly fit.
	assert.Equal(t, -1, seekSequence(lines, pattern, 0, false))
}

func TestSeekSequence_EmptyPatternReturnsStart(t *testing.T) {
	lines := []string{"foo", "bar"}
	assert.Equal(t, 0, seekSequence(lines, []string{}, 0, false))
	assert.Equal(t, 1, seekSequence(lines, []string{}, 1, false))
}

func TestSeekSequence_StartOffset(t *testing.T) {
	lines := []string{"a", "b", "a", "b"}
	pattern := []string{"a", "b"}
	// With start=0, should find at 0.
	assert.Equal(t, 0, seekSequence(lines, pattern, 0, false))
	// With start=1, should skip the first "a","b" and find at 2.
	assert.Equal(t, 2, seekSequence(lines, pattern, 1, false))
}

func TestSeekSequence_EOFSearchesFromEnd(t *testing.T) {
	lines := []string{"a", "b", "c", "a", "b"}
	pattern := []string{"a", "b"}
	// With eof=true, should start searching from end and find at 3.
	assert.Equal(t, 3, seekSequence(lines, pattern, 0, true))
}

func TestSeekSequence_NoMatch(t *testing.T) {
	lines := []string{"foo", "bar", "baz"}
	pattern := []string{"xxx", "yyy"}
	assert.Equal(t, -1, seekSequence(lines, pattern, 0, false))
}

func TestSeekSequence_UnicodeNormalisation(t *testing.T) {
	// EN DASH (\u2013) and NON-BREAKING HYPHEN (\u2011) in lines.
	lines := []string{"import asyncio  # local import \u2013 avoids top\u2011level dep"}
	// Pattern uses plain ASCII dash / hyphen.
	pattern := []string{"import asyncio  # local import - avoids top-level dep"}
	assert.Equal(t, 0, seekSequence(lines, pattern, 0, false))
}

func TestSeekSequence_SmartQuotesNormalised(t *testing.T) {
	// Fancy double quotes in lines.
	lines := []string{"say \u201CHello\u201D"}
	pattern := []string{"say \"Hello\""}
	assert.Equal(t, 0, seekSequence(lines, pattern, 0, false))
}

func TestNormalise(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"  hello  ", "hello"},
		{"em\u2014dash", "em-dash"},
		{"\u201Cquoted\u201D", "\"quoted\""},
		{"\u2018single\u2019", "'single'"},
		{"non\u00A0breaking", "non breaking"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, normalise(tt.input), "normalise(%q)", tt.input)
	}
}
