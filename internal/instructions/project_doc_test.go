package instructions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- FindGitRoot tests ---

func TestFindGitRoot_NormalRepo(t *testing.T) {
	// Create a temp dir with .git directory
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))

	root, err := FindGitRoot(dir)
	require.NoError(t, err)
	assert.Equal(t, dir, root)
}

func TestFindGitRoot_Subdirectory(t *testing.T) {
	// .git is at root, search from subdirectory
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))

	sub := filepath.Join(dir, "a", "b", "c")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	root, err := FindGitRoot(sub)
	require.NoError(t, err)
	assert.Equal(t, dir, root)
}

func TestFindGitRoot_Worktree(t *testing.T) {
	// .git is a file (worktree indicator)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /some/path"), 0o644))

	root, err := FindGitRoot(dir)
	require.NoError(t, err)
	assert.Equal(t, dir, root)
}

func TestFindGitRoot_NoGit(t *testing.T) {
	dir := t.TempDir()
	root, err := FindGitRoot(dir)
	require.NoError(t, err)
	assert.Empty(t, root)
}

// --- LoadProjectDocs tests ---

func TestLoadProjectDocs_SingleFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("root instructions"), 0o644))

	docs, err := LoadProjectDocs(dir, dir)
	require.NoError(t, err)
	assert.Contains(t, docs, "root instructions")
	assert.Contains(t, docs, "AGENTS.md")
}

func TestLoadProjectDocs_NestedDirs(t *testing.T) {
	// root/AGENTS.md + root/sub/AGENTS.md
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("root docs"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "AGENTS.md"), []byte("sub docs"), 0o644))

	docs, err := LoadProjectDocs(dir, sub)
	require.NoError(t, err)
	assert.Contains(t, docs, "root docs")
	assert.Contains(t, docs, "sub docs")

	// Root should come before sub
	rootIdx := strings.Index(docs, "root docs")
	subIdx := strings.Index(docs, "sub docs")
	assert.Less(t, rootIdx, subIdx, "root docs should appear before sub docs")
}

func TestLoadProjectDocs_OverridePrecedence(t *testing.T) {
	// AGENTS.override.md should take precedence over AGENTS.md at same level
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("normal"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.override.md"), []byte("override"), 0o644))

	docs, err := LoadProjectDocs(dir, dir)
	require.NoError(t, err)
	assert.Contains(t, docs, "override")
	assert.NotContains(t, docs, "normal")
}

func TestLoadProjectDocs_CLAUDEmd(t *testing.T) {
	// Falls back to CLAUDE.md when no AGENTS files exist
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("claude instructions"), 0o644))

	docs, err := LoadProjectDocs(dir, dir)
	require.NoError(t, err)
	assert.Contains(t, docs, "claude instructions")
}

func TestLoadProjectDocs_AGENTSBeforeCLAUDE(t *testing.T) {
	// AGENTS.md should be preferred over CLAUDE.md at same level
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("agents"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("claude"), 0o644))

	docs, err := LoadProjectDocs(dir, dir)
	require.NoError(t, err)
	assert.Contains(t, docs, "agents")
	assert.NotContains(t, docs, "claude")
}

func TestLoadProjectDocs_NoFiles(t *testing.T) {
	dir := t.TempDir()
	docs, err := LoadProjectDocs(dir, dir)
	require.NoError(t, err)
	assert.Empty(t, docs)
}

func TestLoadProjectDocs_SizeCap(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	// Create a large file that fills most of the cap.
	// The separator "--- AGENTS.md ---\n" adds ~18 bytes overhead, so the
	// first file's entrySize is bigContent + 18. After including it, any
	// second entry would push total past MaxProjectDocsBytes.
	bigContent := strings.Repeat("x", MaxProjectDocsBytes-20)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(bigContent), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "AGENTS.md"), []byte("should be skipped"), 0o644))

	docs, err := LoadProjectDocs(dir, sub)
	require.NoError(t, err)
	// First file is included (big content)
	assert.True(t, len(docs) > 1000, "should include the large first file")
	// Second file should NOT be included (would exceed cap)
	assert.NotContains(t, docs, "should be skipped")
}

func TestLoadProjectDocs_DeeplyNested(t *testing.T) {
	// root/a/b/c — files at root and c only
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b", "c")
	require.NoError(t, os.MkdirAll(deep, 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("root level"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(deep, "AGENTS.md"), []byte("deep level"), 0o644))

	docs, err := LoadProjectDocs(dir, deep)
	require.NoError(t, err)
	assert.Contains(t, docs, "root level")
	assert.Contains(t, docs, "deep level")
}

// --- Supplementary file tests ---
// SupplementaryFileNames is now empty, so CONTRIBUTING.md and README.md
// are no longer loaded into the system context.

func TestLoadProjectDocs_ContributingNotLoaded(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("agent rules"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "CONTRIBUTING.md"), []byte("how to contribute"), 0o644))

	docs, err := LoadProjectDocs(dir, dir)
	require.NoError(t, err)
	assert.Contains(t, docs, "agent rules")
	assert.NotContains(t, docs, "how to contribute")
}

func TestLoadProjectDocs_ReadmeNotLoaded(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("agent rules"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("project overview"), 0o644))

	docs, err := LoadProjectDocs(dir, dir)
	require.NoError(t, err)
	assert.Contains(t, docs, "agent rules")
	assert.NotContains(t, docs, "project overview")
}

func TestLoadProjectDocs_OnlyAgentsFiles(t *testing.T) {
	// Only AGENTS.md/CLAUDE.md are loaded, not CONTRIBUTING.md or README.md
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("agent rules"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "CONTRIBUTING.md"), []byte("contrib guide"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("project overview"), 0o644))

	docs, err := LoadProjectDocs(dir, dir)
	require.NoError(t, err)
	assert.Contains(t, docs, "agent rules")
	assert.NotContains(t, docs, "contrib guide")
	assert.NotContains(t, docs, "project overview")
}

// --- pathSegments tests ---

func TestPathSegments_SameDir(t *testing.T) {
	dirs, err := pathSegments("/a/b", "/a/b")
	require.NoError(t, err)
	assert.Equal(t, []string{"/a/b"}, dirs)
}

func TestPathSegments_Nested(t *testing.T) {
	dirs, err := pathSegments("/a", "/a/b/c")
	require.NoError(t, err)
	assert.Equal(t, []string{"/a", "/a/b", "/a/b/c"}, dirs)
}

func TestPathSegments_NotPrefix(t *testing.T) {
	_, err := pathSegments("/a/b", "/c/d")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not under")
}
