package command_safety

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Maps to: codex-rs/core/src/command_safety/is_dangerous_command.rs tests

func TestGitResetIsDangerous(t *testing.T) {
	assert.True(t, CommandMightBeDangerous([]string{"git", "reset"}))
}

func TestBashGitResetIsDangerous(t *testing.T) {
	assert.True(t, CommandMightBeDangerous([]string{"bash", "-lc", "git reset --hard"}))
}

func TestZshGitResetIsDangerous(t *testing.T) {
	assert.True(t, CommandMightBeDangerous([]string{"zsh", "-lc", "git reset --hard"}))
}

func TestGitStatusIsNotDangerous(t *testing.T) {
	assert.False(t, CommandMightBeDangerous([]string{"git", "status"}))
}

func TestBashGitStatusIsNotDangerous(t *testing.T) {
	assert.False(t, CommandMightBeDangerous([]string{"bash", "-lc", "git status"}))
}

func TestSudoGitResetIsDangerous(t *testing.T) {
	assert.True(t, CommandMightBeDangerous([]string{"sudo", "git", "reset", "--hard"}))
}

func TestUsrBinGitIsDangerous(t *testing.T) {
	assert.True(t, CommandMightBeDangerous([]string{"/usr/bin/git", "reset", "--hard"}))
}

func TestGitBranchDeleteIsDangerous(t *testing.T) {
	assert.True(t, CommandMightBeDangerous([]string{"git", "branch", "-d", "feature"}))
	assert.True(t, CommandMightBeDangerous([]string{"git", "branch", "-D", "feature"}))
	assert.True(t, CommandMightBeDangerous([]string{"bash", "-lc", "git branch --delete feature"}))
}

func TestGitBranchDeleteWithStackedShortFlagsIsDangerous(t *testing.T) {
	assert.True(t, CommandMightBeDangerous([]string{"git", "branch", "-dv", "feature"}))
	assert.True(t, CommandMightBeDangerous([]string{"git", "branch", "-vd", "feature"}))
	assert.True(t, CommandMightBeDangerous([]string{"git", "branch", "-vD", "feature"}))
	assert.True(t, CommandMightBeDangerous([]string{"git", "branch", "-Dvv", "feature"}))
}

func TestGitBranchDeleteWithGlobalOptionsIsDangerous(t *testing.T) {
	assert.True(t, CommandMightBeDangerous([]string{"git", "-C", ".", "branch", "-d", "feature"}))
	assert.True(t, CommandMightBeDangerous([]string{"git", "-c", "color.ui=false", "branch", "-D", "feature"}))
	assert.True(t, CommandMightBeDangerous([]string{"bash", "-lc", "git -C . branch -d feature"}))
}

func TestGitCheckoutResetIsNotDangerous(t *testing.T) {
	// The first non-option token is "checkout", so later positional args
	// like branch names must not be treated as subcommands.
	assert.False(t, CommandMightBeDangerous([]string{"git", "checkout", "reset"}))
}

func TestGitPushForceIsDangerous(t *testing.T) {
	assert.True(t, CommandMightBeDangerous([]string{"git", "push", "--force", "origin", "main"}))
	assert.True(t, CommandMightBeDangerous([]string{"git", "push", "-f", "origin", "main"}))
	assert.True(t, CommandMightBeDangerous([]string{"git", "-C", ".", "push", "--force-with-lease", "origin", "main"}))
}

func TestGitPushPlusRefspecIsDangerous(t *testing.T) {
	assert.True(t, CommandMightBeDangerous([]string{"git", "push", "origin", "+main"}))
	assert.True(t, CommandMightBeDangerous([]string{"git", "push", "origin", "+refs/heads/main:refs/heads/main"}))
}

func TestGitPushDeleteFlagIsDangerous(t *testing.T) {
	assert.True(t, CommandMightBeDangerous([]string{"git", "push", "--delete", "origin", "feature"}))
	assert.True(t, CommandMightBeDangerous([]string{"git", "push", "-d", "origin", "feature"}))
}

func TestGitPushDeleteRefspecIsDangerous(t *testing.T) {
	assert.True(t, CommandMightBeDangerous([]string{"git", "push", "origin", ":feature"}))
	assert.True(t, CommandMightBeDangerous([]string{"bash", "-lc", "git push origin :feature"}))
}

func TestGitPushWithoutForceIsNotDangerous(t *testing.T) {
	assert.False(t, CommandMightBeDangerous([]string{"git", "push", "origin", "main"}))
}

func TestGitCleanForceIsDangerousEvenWhenFIsNotFirstFlag(t *testing.T) {
	assert.True(t, CommandMightBeDangerous([]string{"git", "clean", "-fdx"}))
	assert.True(t, CommandMightBeDangerous([]string{"git", "clean", "-xdf"}))
	assert.True(t, CommandMightBeDangerous([]string{"git", "clean", "--force"}))
}

func TestRmRfIsDangerous(t *testing.T) {
	assert.True(t, CommandMightBeDangerous([]string{"rm", "-rf", "/"}))
}

func TestRmFIsDangerous(t *testing.T) {
	assert.True(t, CommandMightBeDangerous([]string{"rm", "-f", "/"}))
}
