// Package instructions handles loading and merging instruction sources
// (AGENTS.md, base prompts, developer context) for the agentic session.
//
// Corresponds to: codex-rs/core/src/config_profile.rs (project doc discovery)
package instructions

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AgentsFileNames lists the agent instruction file names in priority order.
// At each directory level, the first file found wins.
// AGENTS.override.md takes precedence over AGENTS.md and CLAUDE.md.
var AgentsFileNames = []string{"AGENTS.override.md", "AGENTS.md", "CLAUDE.md"}

// SupplementaryFileNames lists additional documentation files loaded alongside
// the primary agent instruction file. These are always additive — they don't
// compete with AgentsFileNames but are appended if found at the same directory level.
var SupplementaryFileNames []string

// MaxProjectDocsBytes is the maximum total size of concatenated project docs.
const MaxProjectDocsBytes = 512 * 1024 // 512KB

// FindGitRoot walks up from dir looking for a .git directory.
// Returns the directory containing .git, or empty string if not found.
// Pure Go implementation — no subprocess.
func FindGitRoot(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("cannot resolve path: %w", err)
	}

	for {
		gitPath := filepath.Join(dir, ".git")
		info, err := os.Stat(gitPath)
		if err == nil {
			// .git can be a directory (normal repo) or a file (worktree)
			if info.IsDir() || info.Mode().IsRegular() {
				return dir, nil
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root without finding .git
			return "", nil
		}
		dir = parent
	}
}

// LoadProjectDocs discovers instruction files from rootDir down to targetDir.
//
// At each directory level between rootDir and targetDir (inclusive), it checks
// AgentsFileNames in priority order. If AGENTS.override.md exists at a level,
// only that file is used for that level. Files are concatenated with labeled
// separators. Stops if total exceeds MaxProjectDocsBytes.
//
// Returns empty string if no files found (not an error).
func LoadProjectDocs(rootDir, targetDir string) (string, error) {
	rootDir, err := filepath.Abs(rootDir)
	if err != nil {
		return "", fmt.Errorf("cannot resolve rootDir: %w", err)
	}
	targetDir, err = filepath.Abs(targetDir)
	if err != nil {
		return "", fmt.Errorf("cannot resolve targetDir: %w", err)
	}

	// Compute the directory path from rootDir to targetDir
	dirs, err := pathSegments(rootDir, targetDir)
	if err != nil {
		return "", err
	}

	var parts []string
	totalSize := 0

	for _, dir := range dirs {
		// Load primary agent instruction file (first match wins)
		content, filename, err := findInstructionFile(dir)
		if err != nil {
			return "", err
		}
		if content != "" {
			relPath, _ := filepath.Rel(rootDir, filepath.Join(dir, filename))
			if relPath == "" {
				relPath = filename
			}
			separator := fmt.Sprintf("--- %s ---", relPath)
			entrySize := len(separator) + 1 + len(content)

			if totalSize+entrySize > MaxProjectDocsBytes {
				break
			}

			parts = append(parts, separator+"\n"+content)
			totalSize += entrySize
		}

		// Load supplementary files (additive, don't compete with agent instructions)
		for _, name := range SupplementaryFileNames {
			path := filepath.Join(dir, name)
			data, err := os.ReadFile(path)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return "", fmt.Errorf("error reading %s: %w", path, err)
			}
			supContent := string(data)
			if supContent == "" {
				continue
			}

			relPath, _ := filepath.Rel(rootDir, path)
			if relPath == "" {
				relPath = name
			}
			separator := fmt.Sprintf("--- %s ---", relPath)
			entrySize := len(separator) + 1 + len(supContent)

			if totalSize+entrySize > MaxProjectDocsBytes {
				break
			}

			parts = append(parts, separator+"\n"+supContent)
			totalSize += entrySize
		}
	}

	return strings.Join(parts, "\n\n"), nil
}

// pathSegments returns all directories from rootDir to targetDir inclusive.
// rootDir must be a prefix of targetDir.
func pathSegments(rootDir, targetDir string) ([]string, error) {
	// Normalise paths
	rootDir = filepath.Clean(rootDir)
	targetDir = filepath.Clean(targetDir)

	if !strings.HasPrefix(targetDir, rootDir) {
		return nil, fmt.Errorf("targetDir %q is not under rootDir %q", targetDir, rootDir)
	}

	var dirs []string
	dirs = append(dirs, rootDir)

	if rootDir == targetDir {
		return dirs, nil
	}

	// Get the relative path and split into segments
	rel, err := filepath.Rel(rootDir, targetDir)
	if err != nil {
		return nil, fmt.Errorf("cannot compute relative path: %w", err)
	}

	segments := strings.Split(rel, string(filepath.Separator))
	current := rootDir
	for _, seg := range segments {
		if seg == "." {
			continue
		}
		current = filepath.Join(current, seg)
		dirs = append(dirs, current)
	}

	return dirs, nil
}

// findInstructionFile checks AgentsFileNames in priority order at dir.
// Returns file content and filename, or empty strings if nothing found.
func findInstructionFile(dir string) (string, string, error) {
	for _, name := range AgentsFileNames {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", "", fmt.Errorf("error reading %s: %w", path, err)
		}
		return string(data), name, nil
	}
	return "", "", nil
}
