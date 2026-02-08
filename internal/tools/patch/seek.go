// Package patch implements the apply_patch tool: parsing, fuzzy matching, and application.
//
// Corresponds to: codex-rs/apply-patch/src/seek_sequence.rs
package patch

import "strings"

// seekSequence attempts to find the sequence of pattern lines within lines
// beginning at or after start. Returns the starting index of the match or -1 if
// not found.
//
// Matches are attempted with decreasing strictness (4 passes):
//  1. Exact match
//  2. Right-trim whitespace
//  3. Both-trim whitespace
//  4. Unicode normalisation (smart quotes → ASCII, em-dash → hyphen, etc.)
//
// When eof is true, the search starts from the end of the file first (so that
// patterns intended to match file endings are applied at the end), and falls
// back to searching from start if needed.
//
// Maps to: codex-rs/apply-patch/src/seek_sequence.rs seek_sequence
func seekSequence(lines []string, pattern []string, start int, eof bool) int {
	if len(pattern) == 0 {
		return start
	}

	// When the pattern is longer than the available input there is no possible
	// match.
	if len(pattern) > len(lines) {
		return -1
	}

	searchStart := start
	if eof && len(lines) >= len(pattern) {
		searchStart = len(lines) - len(pattern)
	}

	last := len(lines) - len(pattern)

	// Pass 1: Exact match.
	for i := searchStart; i <= last; i++ {
		if matchExact(lines, pattern, i) {
			return i
		}
	}

	// Pass 2: Right-trim whitespace.
	for i := searchStart; i <= last; i++ {
		if matchRTrim(lines, pattern, i) {
			return i
		}
	}

	// Pass 3: Trim both sides.
	for i := searchStart; i <= last; i++ {
		if matchTrim(lines, pattern, i) {
			return i
		}
	}

	// Pass 4: Unicode normalisation.
	for i := searchStart; i <= last; i++ {
		if matchNormalised(lines, pattern, i) {
			return i
		}
	}

	return -1
}

func matchExact(lines, pattern []string, start int) bool {
	for j, p := range pattern {
		if lines[start+j] != p {
			return false
		}
	}
	return true
}

func matchRTrim(lines, pattern []string, start int) bool {
	for j, p := range pattern {
		if strings.TrimRight(lines[start+j], " \t") != strings.TrimRight(p, " \t") {
			return false
		}
	}
	return true
}

func matchTrim(lines, pattern []string, start int) bool {
	for j, p := range pattern {
		if strings.TrimSpace(lines[start+j]) != strings.TrimSpace(p) {
			return false
		}
	}
	return true
}

func matchNormalised(lines, pattern []string, start int) bool {
	for j, p := range pattern {
		if normalise(lines[start+j]) != normalise(p) {
			return false
		}
	}
	return true
}

// normalise replaces common Unicode punctuation with their ASCII equivalents
// and trims whitespace.
//
// Maps to: codex-rs/apply-patch/src/seek_sequence.rs normalise (inline fn)
func normalise(s string) string {
	trimmed := strings.TrimSpace(s)
	var b strings.Builder
	b.Grow(len(trimmed))
	for _, c := range trimmed {
		switch c {
		// Various dash / hyphen code-points → ASCII '-'
		case '\u2010', '\u2011', '\u2012', '\u2013', '\u2014', '\u2015', '\u2212':
			b.WriteByte('-')
		// Fancy single quotes → '\''
		case '\u2018', '\u2019', '\u201A', '\u201B':
			b.WriteByte('\'')
		// Fancy double quotes → '"'
		case '\u201C', '\u201D', '\u201E', '\u201F':
			b.WriteByte('"')
		// Non-breaking space and other odd spaces → normal space
		case '\u00A0', '\u2002', '\u2003', '\u2004', '\u2005', '\u2006',
			'\u2007', '\u2008', '\u2009', '\u200A', '\u202F', '\u205F',
			'\u3000':
			b.WriteByte(' ')
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}
