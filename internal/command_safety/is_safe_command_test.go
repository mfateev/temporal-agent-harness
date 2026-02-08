package command_safety

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Maps to: codex-rs/core/src/command_safety/is_safe_command.rs tests

func TestKnownSafeExamples(t *testing.T) {
	assert.True(t, isSafeToCallWithExec([]string{"ls"}))
	assert.True(t, isSafeToCallWithExec([]string{"git", "status"}))
	assert.True(t, isSafeToCallWithExec([]string{"git", "branch"}))
	assert.True(t, isSafeToCallWithExec([]string{"git", "branch", "--show-current"}))
	assert.True(t, isSafeToCallWithExec([]string{"base64"}))
	assert.True(t, isSafeToCallWithExec([]string{"sed", "-n", "1,5p", "file.txt"}))
	assert.True(t, isSafeToCallWithExec([]string{"nl", "-nrz", "Cargo.toml"}))
	// Safe `find` command (no unsafe options).
	assert.True(t, isSafeToCallWithExec([]string{"find", ".", "-name", "file.txt"}))
	// Linux-only commands (we're on Linux)
	assert.True(t, isSafeToCallWithExec([]string{"numfmt", "1000"}))
	assert.True(t, isSafeToCallWithExec([]string{"tac", "Cargo.toml"}))
}

func TestGitBranchMutatingFlagsAreNotSafe(t *testing.T) {
	assert.False(t, IsKnownSafeCommand([]string{"git", "branch", "-d", "feature"}))
	assert.False(t, IsKnownSafeCommand([]string{"git", "branch", "new-branch"}))
}

func TestGitBranchGlobalOptionsRespectSafetyRules(t *testing.T) {
	assert.True(t, IsKnownSafeCommand([]string{"git", "-C", ".", "branch", "--show-current"}))
	assert.False(t, IsKnownSafeCommand([]string{"git", "-C", ".", "branch", "-d", "feature"}))
	assert.False(t, IsKnownSafeCommand([]string{"bash", "-lc", "git -C . branch -d feature"}))
}

func TestGitFirstPositionalIsTheSubcommand(t *testing.T) {
	// In git, the first non-option token is the subcommand. Later positional
	// args (like branch names) must not be treated as subcommands.
	assert.False(t, IsKnownSafeCommand([]string{"git", "checkout", "status"}))
}

func TestGitOutputAndConfigOverrideFlagsAreNotSafe(t *testing.T) {
	assert.False(t, IsKnownSafeCommand([]string{"git", "log", "--output=/tmp/git-log-out-test", "-n", "1"}))
	assert.False(t, IsKnownSafeCommand([]string{"git", "diff", "--output", "/tmp/git-diff-out-test"}))
	assert.False(t, IsKnownSafeCommand([]string{"git", "show", "--output=/tmp/git-show-out-test", "HEAD"}))
	assert.False(t, IsKnownSafeCommand([]string{"git", "-c", "core.pager=cat", "log", "-n", "1"}))
	assert.False(t, IsKnownSafeCommand([]string{"git", "-ccore.pager=cat", "status"}))
}

func TestCargoCheckIsNotSafe(t *testing.T) {
	assert.False(t, IsKnownSafeCommand([]string{"cargo", "check"}))
}

func TestZshLcSafeCommandSequence(t *testing.T) {
	assert.True(t, IsKnownSafeCommand([]string{"zsh", "-lc", "ls"}))
}

func TestUnknownOrPartial(t *testing.T) {
	assert.False(t, isSafeToCallWithExec([]string{"foo"}))
	assert.False(t, isSafeToCallWithExec([]string{"git", "fetch"}))
	assert.False(t, isSafeToCallWithExec([]string{"sed", "-n", "xp", "file.txt"}))

	// Unsafe `find` commands.
	unsafeFindCommands := [][]string{
		{"find", ".", "-name", "file.txt", "-exec", "rm", "{}", ";"},
		{"find", ".", "-name", "*.py", "-execdir", "python3", "{}", ";"},
		{"find", ".", "-name", "file.txt", "-ok", "rm", "{}", ";"},
		{"find", ".", "-name", "*.py", "-okdir", "python3", "{}", ";"},
		{"find", ".", "-delete", "-name", "file.txt"},
		{"find", ".", "-fls", "/etc/passwd"},
		{"find", ".", "-fprint", "/etc/passwd"},
		{"find", ".", "-fprint0", "/etc/passwd"},
		{"find", ".", "-fprintf", "/root/suid.txt", "%#m %u %p\n"},
	}
	for _, args := range unsafeFindCommands {
		assert.False(t, isSafeToCallWithExec(args), "expected %v to be unsafe", args)
	}
}

func TestBase64OutputOptionsAreUnsafe(t *testing.T) {
	unsafeCases := [][]string{
		{"base64", "-o", "out.bin"},
		{"base64", "--output", "out.bin"},
		{"base64", "--output=out.bin"},
		{"base64", "-ob64.txt"},
	}
	for _, args := range unsafeCases {
		assert.False(t, isSafeToCallWithExec(args), "expected %v to be unsafe due to output option", args)
	}
}

func TestRipgrepRules(t *testing.T) {
	// Safe ripgrep invocations
	assert.True(t, isSafeToCallWithExec([]string{"rg", "Cargo.toml", "-n"}))

	// Unsafe flags that do not take an argument
	unsafeNoArg := [][]string{
		{"rg", "--search-zip", "files"},
		{"rg", "-z", "files"},
	}
	for _, args := range unsafeNoArg {
		assert.False(t, isSafeToCallWithExec(args), "expected %v to be unsafe due to zip-search flag", args)
	}

	// Unsafe flags that expect a value
	unsafeWithArg := [][]string{
		{"rg", "--pre", "pwned", "files"},
		{"rg", "--pre=pwned", "files"},
		{"rg", "--hostname-bin", "pwned", "files"},
		{"rg", "--hostname-bin=pwned", "files"},
	}
	for _, args := range unsafeWithArg {
		assert.False(t, isSafeToCallWithExec(args), "expected %v to be unsafe due to external-command flag", args)
	}
}

func TestBashLcSafeExamples(t *testing.T) {
	assert.True(t, IsKnownSafeCommand([]string{"bash", "-lc", "ls"}))
	assert.True(t, IsKnownSafeCommand([]string{"bash", "-lc", "ls -1"}))
	assert.True(t, IsKnownSafeCommand([]string{"bash", "-lc", "git status"}))
	assert.True(t, IsKnownSafeCommand([]string{"bash", "-lc", `grep -R "Cargo.toml" -n`}))
	assert.True(t, IsKnownSafeCommand([]string{"bash", "-lc", "sed -n 1,5p file.txt"}))
	assert.True(t, IsKnownSafeCommand([]string{"bash", "-lc", "sed -n '1,5p' file.txt"}))
	assert.True(t, IsKnownSafeCommand([]string{"bash", "-lc", "find . -name file.txt"}))
}

func TestBashLcSafeExamplesWithOperators(t *testing.T) {
	assert.True(t, IsKnownSafeCommand([]string{"bash", "-lc", `grep -R "Cargo.toml" -n || true`}))
	assert.True(t, IsKnownSafeCommand([]string{"bash", "-lc", "ls && pwd"}))
	assert.True(t, IsKnownSafeCommand([]string{"bash", "-lc", "echo 'hi' ; ls"}))
	assert.True(t, IsKnownSafeCommand([]string{"bash", "-lc", "ls | wc -l"}))
}

func TestBashLcUnsafeExamples(t *testing.T) {
	assert.False(t, IsKnownSafeCommand([]string{"bash", "-lc", "git", "status"}),
		"Four arg version is not known to be safe.")
	assert.False(t, IsKnownSafeCommand([]string{"bash", "-lc", "'git status'"}),
		"The extra quoting around 'git status' makes it a program named 'git status' and is therefore unsafe.")

	assert.False(t, IsKnownSafeCommand([]string{"bash", "-lc", "find . -name file.txt -delete"}),
		"Unsafe find option should not be auto-approved.")

	// Disallowed because of unsafe command in sequence.
	assert.False(t, IsKnownSafeCommand([]string{"bash", "-lc", "ls && rm -rf /"}),
		"Sequence containing unsafe command must be rejected")

	// Disallowed because of parentheses / subshell.
	assert.False(t, IsKnownSafeCommand([]string{"bash", "-lc", "(ls)"}),
		"Parentheses (subshell) are not provably safe with the current parser")
	assert.False(t, IsKnownSafeCommand([]string{"bash", "-lc", "ls || (pwd && echo hi)"}),
		"Nested parentheses are not provably safe with the current parser")

	// Disallowed redirection.
	assert.False(t, IsKnownSafeCommand([]string{"bash", "-lc", "ls > out.txt"}),
		"> redirection should be rejected")
}
