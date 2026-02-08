package command_safety

import (
	"path/filepath"
	"strings"
)

// IsKnownSafeCommand returns true if the command is read-only and can be auto-approved.
//
// Maps to: codex-rs/core/src/command_safety/is_safe_command.rs is_known_safe_command
func IsKnownSafeCommand(command []string) bool {
	// Normalize zsh → bash for consistent handling.
	normalized := make([]string, len(command))
	for i, s := range command {
		if s == "zsh" {
			normalized[i] = "bash"
		} else {
			normalized[i] = s
		}
	}

	if isSafeToCallWithExec(normalized) {
		return true
	}

	// Support `bash -lc "..."` where the script consists solely of one or more
	// "plain" commands combined with safe operators.
	if allCommands := ParseShellLcPlainCommands(normalized); allCommands != nil && len(allCommands) > 0 {
		allSafe := true
		for _, cmd := range allCommands {
			if !isSafeToCallWithExec(cmd) {
				allSafe = false
				break
			}
		}
		if allSafe {
			return true
		}
	}

	return false
}

func isSafeToCallWithExec(command []string) bool {
	if len(command) == 0 {
		return false
	}

	cmd0 := command[0]
	base := filepath.Base(cmd0)

	switch base {
	// Linux-only commands
	case "numfmt", "tac":
		return true

	// Unconditionally safe commands
	case "cat", "cd", "cut", "echo", "expr", "false", "grep", "head", "id",
		"ls", "nl", "paste", "pwd", "rev", "seq", "stat", "tail", "tr",
		"true", "uname", "uniq", "wc", "which", "whoami":
		return true

	case "base64":
		return base64IsSafe(command)

	case "find":
		return findIsSafe(command)

	case "rg":
		return rgIsSafe(command)

	case "git":
		return gitIsSafe(command)

	case "sed":
		return sedIsSafe(command)

	default:
		return false
	}
}

func base64IsSafe(command []string) bool {
	for _, arg := range command[1:] {
		if arg == "-o" || arg == "--output" {
			return false
		}
		if strings.HasPrefix(arg, "--output=") {
			return false
		}
		// -o<value> where the value is inline
		if strings.HasPrefix(arg, "-o") && arg != "-o" {
			return false
		}
	}
	return true
}

func findIsSafe(command []string) bool {
	unsafeOptions := []string{
		"-exec", "-execdir", "-ok", "-okdir",
		"-delete",
		"-fls", "-fprint", "-fprint0", "-fprintf",
	}
	for _, arg := range command {
		for _, opt := range unsafeOptions {
			if arg == opt {
				return false
			}
		}
	}
	return true
}

func rgIsSafe(command []string) bool {
	unsafeWithArgs := []string{"--pre", "--hostname-bin"}
	unsafeNoArgs := []string{"--search-zip", "-z"}

	for _, arg := range command {
		for _, opt := range unsafeNoArgs {
			if arg == opt {
				return false
			}
		}
		for _, opt := range unsafeWithArgs {
			if arg == opt || strings.HasPrefix(arg, opt+"=") {
				return false
			}
		}
	}
	return true
}

func gitIsSafe(command []string) bool {
	// Global config overrides like `-c core.pager=...` can force git
	// to execute arbitrary external commands.
	if gitHasConfigOverrideGlobalOption(command) {
		return false
	}

	idx, subcommand, found := FindGitSubcommand(command, []string{"status", "log", "diff", "show", "branch"})
	if !found {
		return false
	}

	subcommandArgs := command[idx+1:]

	switch subcommand {
	case "status", "log", "diff", "show":
		return gitSubcommandArgsAreReadOnly(subcommandArgs)
	case "branch":
		return gitSubcommandArgsAreReadOnly(subcommandArgs) && gitBranchIsReadOnly(subcommandArgs)
	default:
		return false
	}
}

func gitBranchIsReadOnly(branchArgs []string) bool {
	if len(branchArgs) == 0 {
		// `git branch` with no additional args lists branches.
		return true
	}

	sawReadOnlyFlag := false
	for _, arg := range branchArgs {
		switch arg {
		case "--list", "-l", "--show-current", "-a", "--all", "-r", "--remotes",
			"-v", "-vv", "--verbose":
			sawReadOnlyFlag = true
		default:
			if strings.HasPrefix(arg, "--format=") {
				sawReadOnlyFlag = true
			} else {
				// Any other flag or positional argument may create, rename, or delete branches.
				return false
			}
		}
	}

	return sawReadOnlyFlag
}

func gitHasConfigOverrideGlobalOption(command []string) bool {
	for _, arg := range command {
		if arg == "-c" || arg == "--config-env" {
			return true
		}
		if strings.HasPrefix(arg, "-c") && len(arg) > 2 {
			return true
		}
		if strings.HasPrefix(arg, "--config-env=") {
			return true
		}
	}
	return false
}

func gitSubcommandArgsAreReadOnly(args []string) bool {
	unsafeFlags := []string{"--output", "--ext-diff", "--textconv", "--exec", "--paginate"}
	for _, arg := range args {
		for _, flag := range unsafeFlags {
			if arg == flag {
				return false
			}
		}
		if strings.HasPrefix(arg, "--output=") || strings.HasPrefix(arg, "--exec=") {
			return false
		}
	}
	return true
}

// sedIsSafe handles the special case: `sed -n {N|M,N}p [file]`
func sedIsSafe(command []string) bool {
	if len(command) > 4 {
		return false
	}
	if len(command) < 3 {
		return false
	}
	if command[1] != "-n" {
		return false
	}
	return isValidSedNArg(command[2])
}

// isValidSedNArg returns true if arg matches /^(\d+,)?\d+p$/
func isValidSedNArg(arg string) bool {
	if !strings.HasSuffix(arg, "p") {
		return false
	}
	core := arg[:len(arg)-1]
	parts := strings.Split(core, ",")
	switch len(parts) {
	case 1:
		return len(parts[0]) > 0 && allDigits(parts[0])
	case 2:
		return len(parts[0]) > 0 && len(parts[1]) > 0 && allDigits(parts[0]) && allDigits(parts[1])
	default:
		return false
	}
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
