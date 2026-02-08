// Package patch implements the apply_patch tool: parsing, fuzzy matching, and application.
//
// Corresponds to: codex-rs/apply-patch/src/parser.rs
package patch

import (
	"fmt"
	"strings"
)

// Marker constants matching the Codex patch grammar.
const (
	beginPatchMarker       = "*** Begin Patch"
	endPatchMarker         = "*** End Patch"
	addFileMarker          = "*** Add File: "
	deleteFileMarker       = "*** Delete File: "
	updateFileMarker       = "*** Update File: "
	moveToMarker           = "*** Move to: "
	eofMarker              = "*** End of File"
	changeContextMarker    = "@@ "
	emptyChangeCtxMarker   = "@@"
)

// Patch is the top-level result of parsing apply_patch input.
//
// Maps to: codex-rs/apply-patch/src/parser.rs (ApplyPatchArgs.hunks)
type Patch struct {
	Hunks []Hunk
}

// Hunk represents a single file operation in a patch.
//
// Maps to: codex-rs/apply-patch/src/parser.rs Hunk
type Hunk struct {
	Type     HunkType
	Path     string
	Contents string          // AddFile only: the file contents (with trailing newlines)
	MovePath string          // UpdateFile only: optional rename destination
	Chunks   []UpdateChunk   // UpdateFile only
}

// HunkType discriminates between add, delete, and update operations.
type HunkType int

const (
	HunkAdd    HunkType = iota
	HunkDelete
	HunkUpdate
)

// UpdateChunk is one contiguous diff region within an UpdateFile hunk.
//
// Maps to: codex-rs/apply-patch/src/parser.rs UpdateFileChunk
type UpdateChunk struct {
	// ChangeContext is an optional single line of context used to narrow down
	// the position of the chunk (usually a class, method, or function name).
	ChangeContext string

	// OldLines are the lines in the original file that should be replaced.
	OldLines []string

	// NewLines are the replacement lines.
	NewLines []string

	// IsEOF is true when *** End of File was present, meaning OldLines must
	// occur at the end of the source file.
	IsEOF bool
}

// ParseError is returned when a patch cannot be parsed.
type ParseError struct {
	Message    string
	LineNumber int // 0 means the error is patch-level, not line-level
}

func (e *ParseError) Error() string {
	if e.LineNumber > 0 {
		return fmt.Sprintf("invalid hunk at line %d, %s", e.LineNumber, e.Message)
	}
	return fmt.Sprintf("invalid patch: %s", e.Message)
}

// Parse parses the full patch text (*** Begin Patch ... *** End Patch).
//
// Maps to: codex-rs/apply-patch/src/parser.rs parse_patch
func Parse(input string) (*Patch, error) {
	lines := strings.Split(strings.TrimSpace(input), "\n")
	if err := checkPatchBoundaries(lines); err != nil {
		return nil, err
	}

	var hunks []Hunk
	lastLineIdx := len(lines) - 1
	remaining := lines[1:lastLineIdx] // strip Begin/End markers
	lineNumber := 2                   // 1-indexed; line 1 is "*** Begin Patch"

	for len(remaining) > 0 {
		hunk, consumed, err := parseOneHunk(remaining, lineNumber)
		if err != nil {
			return nil, err
		}
		hunks = append(hunks, hunk)
		lineNumber += consumed
		remaining = remaining[consumed:]
	}

	return &Patch{Hunks: hunks}, nil
}

// checkPatchBoundaries validates the *** Begin Patch / *** End Patch envelope.
func checkPatchBoundaries(lines []string) error {
	if len(lines) == 0 {
		return &ParseError{Message: "The first line of the patch must be '*** Begin Patch'"}
	}
	first := strings.TrimSpace(lines[0])
	if first != beginPatchMarker {
		return &ParseError{Message: "The first line of the patch must be '*** Begin Patch'"}
	}
	last := strings.TrimSpace(lines[len(lines)-1])
	if last != endPatchMarker {
		return &ParseError{Message: "The last line of the patch must be '*** End Patch'"}
	}
	return nil
}

// parseOneHunk parses a single file-level hunk and returns how many lines it consumed.
func parseOneHunk(lines []string, lineNumber int) (Hunk, int, error) {
	firstLine := strings.TrimSpace(lines[0])

	// *** Add File: <path>
	if path, ok := strings.CutPrefix(firstLine, addFileMarker); ok {
		var contents strings.Builder
		parsed := 1
		for _, line := range lines[1:] {
			if after, found := strings.CutPrefix(line, "+"); found {
				contents.WriteString(after)
				contents.WriteByte('\n')
				parsed++
			} else {
				break
			}
		}
		return Hunk{
			Type:     HunkAdd,
			Path:     path,
			Contents: contents.String(),
		}, parsed, nil
	}

	// *** Delete File: <path>
	if path, ok := strings.CutPrefix(firstLine, deleteFileMarker); ok {
		return Hunk{
			Type: HunkDelete,
			Path: path,
		}, 1, nil
	}

	// *** Update File: <path>
	if path, ok := strings.CutPrefix(firstLine, updateFileMarker); ok {
		remaining := lines[1:]
		parsed := 1

		// Optional: *** Move to: <newpath>
		var movePath string
		if len(remaining) > 0 {
			if mp, ok := strings.CutPrefix(remaining[0], moveToMarker); ok {
				movePath = mp
				remaining = remaining[1:]
				parsed++
			}
		}

		var chunks []UpdateChunk
		for len(remaining) > 0 {
			// Skip blank lines between chunks.
			if strings.TrimSpace(remaining[0]) == "" {
				parsed++
				remaining = remaining[1:]
				continue
			}

			// Stop if we hit another *** marker (next hunk).
			if strings.HasPrefix(remaining[0], "***") {
				break
			}

			chunk, consumed, err := parseUpdateChunk(remaining, lineNumber+parsed, len(chunks) == 0)
			if err != nil {
				return Hunk{}, 0, err
			}
			chunks = append(chunks, chunk)
			parsed += consumed
			remaining = remaining[consumed:]
		}

		if len(chunks) == 0 {
			return Hunk{}, 0, &ParseError{
				Message:    fmt.Sprintf("Update file hunk for path '%s' is empty", path),
				LineNumber: lineNumber,
			}
		}

		return Hunk{
			Type:     HunkUpdate,
			Path:     path,
			MovePath: movePath,
			Chunks:   chunks,
		}, parsed, nil
	}

	return Hunk{}, 0, &ParseError{
		Message: fmt.Sprintf(
			"'%s' is not a valid hunk header. Valid hunk headers: '*** Add File: {path}', '*** Delete File: {path}', '*** Update File: {path}'",
			firstLine,
		),
		LineNumber: lineNumber,
	}
}

// parseUpdateChunk parses a single @@ chunk within an UpdateFile hunk.
func parseUpdateChunk(lines []string, lineNumber int, allowMissingContext bool) (UpdateChunk, int, error) {
	if len(lines) == 0 {
		return UpdateChunk{}, 0, &ParseError{
			Message:    "Update hunk does not contain any lines",
			LineNumber: lineNumber,
		}
	}

	var changeContext string
	startIndex := 0

	if lines[0] == emptyChangeCtxMarker {
		// @@ with no context text
		startIndex = 1
	} else if after, ok := strings.CutPrefix(lines[0], changeContextMarker); ok {
		// @@ <context>
		changeContext = after
		startIndex = 1
	} else {
		if !allowMissingContext {
			return UpdateChunk{}, 0, &ParseError{
				Message:    fmt.Sprintf("Expected update hunk to start with a @@ context marker, got: '%s'", lines[0]),
				LineNumber: lineNumber,
			}
		}
		// First chunk may omit @@ and start directly with diff lines.
	}

	if startIndex >= len(lines) {
		return UpdateChunk{}, 0, &ParseError{
			Message:    "Update hunk does not contain any lines",
			LineNumber: lineNumber + 1,
		}
	}

	chunk := UpdateChunk{
		ChangeContext: changeContext,
	}
	parsedLines := 0

	for _, line := range lines[startIndex:] {
		if line == eofMarker {
			if parsedLines == 0 {
				return UpdateChunk{}, 0, &ParseError{
					Message:    "Update hunk does not contain any lines",
					LineNumber: lineNumber + 1,
				}
			}
			chunk.IsEOF = true
			parsedLines++
			break
		}

		if len(line) == 0 {
			// Empty line treated as unchanged context.
			chunk.OldLines = append(chunk.OldLines, "")
			chunk.NewLines = append(chunk.NewLines, "")
			parsedLines++
			continue
		}

		switch line[0] {
		case ' ':
			// Context line
			text := line[1:]
			chunk.OldLines = append(chunk.OldLines, text)
			chunk.NewLines = append(chunk.NewLines, text)
		case '+':
			chunk.NewLines = append(chunk.NewLines, line[1:])
		case '-':
			chunk.OldLines = append(chunk.OldLines, line[1:])
		default:
			if parsedLines == 0 {
				return UpdateChunk{}, 0, &ParseError{
					Message: fmt.Sprintf(
						"Unexpected line found in update hunk: '%s'. Every line should start with ' ' (context line), '+' (added line), or '-' (removed line)",
						line,
					),
					LineNumber: lineNumber + 1,
				}
			}
			// Assume this is the start of the next chunk.
			goto done
		}
		parsedLines++
	}

done:
	return chunk, parsedLines + startIndex, nil
}
