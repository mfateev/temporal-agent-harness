package patch

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ApplyError is returned when a parsed patch cannot be applied to the filesystem.
type ApplyError struct {
	Message string
}

func (e *ApplyError) Error() string {
	return e.Message
}

// AffectedPaths tracks which files were added, modified, or deleted.
//
// Maps to: codex-rs/apply-patch/src/lib.rs AffectedPaths
type AffectedPaths struct {
	Added    []string
	Modified []string
	Deleted  []string
}

// Apply parses a patch string and applies it to the filesystem under cwd.
// Returns a human-readable summary on success.
//
// Maps to: codex-rs/apply-patch/src/lib.rs apply_patch + apply_hunks
func Apply(patchText string, cwd string) (string, error) {
	p, err := Parse(patchText)
	if err != nil {
		return "", err
	}

	if len(p.Hunks) == 0 {
		return "", &ApplyError{Message: "empty patch"}
	}

	// Resolve relative paths against cwd and verify before applying.
	resolved, err := resolveAndVerify(p, cwd)
	if err != nil {
		return "", err
	}

	// Apply all hunks.
	affected, err := applyHunks(resolved)
	if err != nil {
		return "", err
	}

	return formatSummary(affected), nil
}

// resolvedHunk is a hunk with absolute paths ready for application.
type resolvedHunk struct {
	Hunk
	absPath     string
	absMovePath string
}

// resolveAndVerify resolves all paths and performs pre-flight checks.
func resolveAndVerify(p *Patch, cwd string) ([]resolvedHunk, error) {
	result := make([]resolvedHunk, len(p.Hunks))
	for i, h := range p.Hunks {
		absPath := resolvePath(cwd, h.Path)
		var absMovePath string
		if h.MovePath != "" {
			absMovePath = resolvePath(cwd, h.MovePath)
		}

		// For UpdateFile and DeleteFile, verify the source file exists.
		switch h.Type {
		case HunkUpdate:
			if _, err := os.Stat(absPath); err != nil {
				return nil, &ApplyError{
					Message: fmt.Sprintf("Failed to read file to update %s: %v", h.Path, err),
				}
			}
		case HunkDelete:
			info, err := os.Stat(absPath)
			if err != nil {
				return nil, &ApplyError{
					Message: fmt.Sprintf("Failed to read file to delete %s: %v", h.Path, err),
				}
			}
			if info.IsDir() {
				return nil, &ApplyError{
					Message: fmt.Sprintf("Failed to read file to delete %s: is a directory", h.Path),
				}
			}
		}

		result[i] = resolvedHunk{
			Hunk:        h,
			absPath:     absPath,
			absMovePath: absMovePath,
		}
	}
	return result, nil
}

func resolvePath(cwd, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(cwd, path)
}

// applyHunks applies each hunk to the filesystem.
//
// Maps to: codex-rs/apply-patch/src/lib.rs apply_hunks_to_files
func applyHunks(hunks []resolvedHunk) (*AffectedPaths, error) {
	affected := &AffectedPaths{}

	for _, rh := range hunks {
		switch rh.Type {
		case HunkAdd:
			if err := applyAddFile(rh.absPath, rh.Contents); err != nil {
				return nil, err
			}
			affected.Added = append(affected.Added, rh.Path)

		case HunkDelete:
			if err := os.Remove(rh.absPath); err != nil {
				return nil, &ApplyError{
					Message: fmt.Sprintf("Failed to delete file %s: %v", rh.Path, err),
				}
			}
			affected.Deleted = append(affected.Deleted, rh.Path)

		case HunkUpdate:
			newContents, err := deriveNewContents(rh.absPath, rh.Chunks)
			if err != nil {
				return nil, err
			}

			dest := rh.absPath
			if rh.absMovePath != "" {
				dest = rh.absMovePath
			}

			// Create parent directories if needed.
			if dir := filepath.Dir(dest); dir != "" {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return nil, &ApplyError{
						Message: fmt.Sprintf("Failed to create parent directories for %s: %v", dest, err),
					}
				}
			}

			if err := os.WriteFile(dest, []byte(newContents), 0o644); err != nil {
				return nil, &ApplyError{
					Message: fmt.Sprintf("Failed to write file %s: %v", dest, err),
				}
			}

			// If moving, remove the original file.
			if rh.absMovePath != "" && rh.absPath != rh.absMovePath {
				if err := os.Remove(rh.absPath); err != nil {
					return nil, &ApplyError{
						Message: fmt.Sprintf("Failed to remove original %s: %v", rh.Path, err),
					}
				}
			}

			if rh.absMovePath != "" {
				affected.Modified = append(affected.Modified, rh.MovePath)
			} else {
				affected.Modified = append(affected.Modified, rh.Path)
			}
		}
	}

	return affected, nil
}

func applyAddFile(absPath, contents string) error {
	dir := filepath.Dir(absPath)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return &ApplyError{
				Message: fmt.Sprintf("Failed to create parent directories for %s: %v", absPath, err),
			}
		}
	}
	if err := os.WriteFile(absPath, []byte(contents), 0o644); err != nil {
		return &ApplyError{
			Message: fmt.Sprintf("Failed to write file %s: %v", absPath, err),
		}
	}
	return nil
}

// deriveNewContents reads the file at path, computes replacements from chunks,
// and returns the new file contents.
//
// Maps to: codex-rs/apply-patch/src/lib.rs derive_new_contents_from_chunks
func deriveNewContents(path string, chunks []UpdateChunk) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", &ApplyError{
			Message: fmt.Sprintf("Failed to read file to update %s: %v", path, err),
		}
	}

	originalContents := string(data)
	originalLines := strings.Split(originalContents, "\n")

	// Drop the trailing empty element that results from the final newline so
	// that line counts match the behaviour of standard diff.
	if len(originalLines) > 0 && originalLines[len(originalLines)-1] == "" {
		originalLines = originalLines[:len(originalLines)-1]
	}

	replacements, err := computeReplacements(originalLines, path, chunks)
	if err != nil {
		return "", err
	}

	newLines := applyReplacements(originalLines, replacements)

	// Ensure the file ends with a newline.
	if len(newLines) == 0 || newLines[len(newLines)-1] != "" {
		newLines = append(newLines, "")
	}

	return strings.Join(newLines, "\n"), nil
}

// replacement describes a single region to replace in the file.
type replacement struct {
	index    int      // starting line index
	count    int      // number of old lines to remove
	newLines []string // lines to insert
}

// computeReplacements determines the set of replacements needed to transform
// originalLines according to the given chunks.
//
// Maps to: codex-rs/apply-patch/src/lib.rs compute_replacements
func computeReplacements(originalLines []string, path string, chunks []UpdateChunk) ([]replacement, error) {
	var replacements []replacement
	lineIndex := 0

	for _, chunk := range chunks {
		// If a chunk has a ChangeContext, seek forward to find it.
		if chunk.ChangeContext != "" {
			idx := seekSequence(originalLines, []string{chunk.ChangeContext}, lineIndex, false)
			if idx < 0 {
				return nil, &ApplyError{
					Message: fmt.Sprintf("Failed to find context '%s' in %s", chunk.ChangeContext, path),
				}
			}
			lineIndex = idx + 1
		}

		if len(chunk.OldLines) == 0 {
			// Pure addition. Insert at end (or just before trailing empty line).
			insertionIdx := len(originalLines)
			if len(originalLines) > 0 && originalLines[len(originalLines)-1] == "" {
				insertionIdx = len(originalLines) - 1
			}
			replacements = append(replacements, replacement{
				index:    insertionIdx,
				count:    0,
				newLines: chunk.NewLines,
			})
			continue
		}

		// Try to match old_lines in the file.
		pattern := chunk.OldLines
		found := seekSequence(originalLines, pattern, lineIndex, chunk.IsEOF)
		newSlice := chunk.NewLines

		// If not found and pattern ends with empty string, retry without it.
		if found < 0 && len(pattern) > 0 && pattern[len(pattern)-1] == "" {
			pattern = pattern[:len(pattern)-1]
			if len(newSlice) > 0 && newSlice[len(newSlice)-1] == "" {
				newSlice = newSlice[:len(newSlice)-1]
			}
			found = seekSequence(originalLines, pattern, lineIndex, chunk.IsEOF)
		}

		if found >= 0 {
			replacements = append(replacements, replacement{
				index:    found,
				count:    len(pattern),
				newLines: copyStrings(newSlice),
			})
			lineIndex = found + len(pattern)
		} else {
			return nil, &ApplyError{
				Message: fmt.Sprintf(
					"Failed to find expected lines in %s:\n%s",
					path,
					strings.Join(chunk.OldLines, "\n"),
				),
			}
		}
	}

	// Sort by index.
	sort.Slice(replacements, func(i, j int) bool {
		return replacements[i].index < replacements[j].index
	})

	return replacements, nil
}

// applyReplacements applies replacements in reverse order to avoid index shifts.
//
// Maps to: codex-rs/apply-patch/src/lib.rs apply_replacements
func applyReplacements(lines []string, replacements []replacement) []string {
	result := make([]string, len(lines))
	copy(result, lines)

	// Apply in reverse order so earlier indices remain valid.
	for i := len(replacements) - 1; i >= 0; i-- {
		r := replacements[i]
		idx := r.index
		count := r.count

		// Remove old lines.
		head := result[:idx]
		var tail []string
		if idx+count < len(result) {
			tail = result[idx+count:]
		}

		newResult := make([]string, 0, len(head)+len(r.newLines)+len(tail))
		newResult = append(newResult, head...)
		newResult = append(newResult, r.newLines...)
		newResult = append(newResult, tail...)
		result = newResult
	}

	return result
}

func formatSummary(affected *AffectedPaths) string {
	var b strings.Builder
	b.WriteString("Success. Updated the following files:\n")
	for _, p := range affected.Added {
		fmt.Fprintf(&b, "A %s\n", p)
	}
	for _, p := range affected.Modified {
		fmt.Fprintf(&b, "M %s\n", p)
	}
	for _, p := range affected.Deleted {
		fmt.Fprintf(&b, "D %s\n", p)
	}
	return b.String()
}

func copyStrings(s []string) []string {
	if s == nil {
		return nil
	}
	c := make([]string, len(s))
	copy(c, s)
	return c
}
