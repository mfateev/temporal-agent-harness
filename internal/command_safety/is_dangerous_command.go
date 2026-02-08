package command_safety

import (
	"path/filepath"
	"strings"
)

// CommandMightBeDangerous returns true if the command is potentially destructive.
//
// Maps to: codex-rs/core/src/command_safety/is_dangerous_command.rs command_might_be_dangerous
func CommandMightBeDangerous(command []string) bool {
	if isDangerousToCallWithExec(command) {
		return true
	}

	// Support `bash -lc "<script>"` where any part of the script might contain a dangerous command.
	if allCommands := ParseShellLcPlainCommands(command); allCommands != nil {
		for _, cmd := range allCommands {
			if isDangerousToCallWithExec(cmd) {
				return true
			}
		}
	}

	return false
}

// FindGitSubcommand finds the first matching git subcommand, skipping global options.
// Shared between safe and dangerous command checks.
//
// Maps to: codex-rs/core/src/command_safety/is_dangerous_command.rs find_git_subcommand
func FindGitSubcommand(command []string, subcommands []string) (idx int, name string, found bool) {
	if len(command) == 0 {
		return 0, "", false
	}

	cmd0 := command[0]
	base := filepath.Base(cmd0)
	if base != "git" {
		return 0, "", false
	}

	skipNext := false
	for i := 1; i < len(command); i++ {
		if skipNext {
			skipNext = false
			continue
		}

		arg := command[i]

		if isGitGlobalOptionWithInlineValue(arg) {
			continue
		}

		if isGitGlobalOptionWithValue(arg) {
			skipNext = true
			continue
		}

		if arg == "--" || strings.HasPrefix(arg, "-") {
			continue
		}

		for _, sub := range subcommands {
			if arg == sub {
				return i, arg, true
			}
		}

		// In git, the first non-option token is the subcommand. If it isn't
		// one of the subcommands we're looking for, we must stop scanning to
		// avoid misclassifying later positional args (e.g., branch names).
		return 0, "", false
	}

	return 0, "", false
}

func isGitGlobalOptionWithValue(arg string) bool {
	switch arg {
	case "-C", "-c", "--config-env", "--exec-path", "--git-dir", "--namespace", "--super-prefix", "--work-tree":
		return true
	}
	return false
}

func isGitGlobalOptionWithInlineValue(arg string) bool {
	if strings.HasPrefix(arg, "--config-env=") ||
		strings.HasPrefix(arg, "--exec-path=") ||
		strings.HasPrefix(arg, "--git-dir=") ||
		strings.HasPrefix(arg, "--namespace=") ||
		strings.HasPrefix(arg, "--super-prefix=") ||
		strings.HasPrefix(arg, "--work-tree=") {
		return true
	}
	// -C<value> or -c<value> (len > 2 means inline value)
	if (strings.HasPrefix(arg, "-C") || strings.HasPrefix(arg, "-c")) && len(arg) > 2 {
		return true
	}
	return false
}

func isDangerousToCallWithExec(command []string) bool {
	if len(command) == 0 {
		return false
	}

	cmd0 := command[0]
	base := filepath.Base(cmd0)

	switch {
	case base == "git":
		idx, subcommand, found := FindGitSubcommand(command, []string{"reset", "rm", "branch", "push", "clean"})
		if !found {
			return false
		}

		switch subcommand {
		case "reset", "rm":
			return true
		case "branch":
			return gitBranchIsDelete(command[idx+1:])
		case "push":
			return gitPushIsDangerous(command[idx+1:])
		case "clean":
			return gitCleanIsForce(command[idx+1:])
		default:
			return false
		}

	case cmd0 == "rm":
		if len(command) > 1 {
			arg1 := command[1]
			if arg1 == "-f" || arg1 == "-rf" {
				return true
			}
		}
		return false

	case cmd0 == "sudo":
		if len(command) > 1 {
			return isDangerousToCallWithExec(command[1:])
		}
		return false

	default:
		return false
	}
}

func gitBranchIsDelete(branchArgs []string) bool {
	for _, arg := range branchArgs {
		if arg == "-d" || arg == "-D" || arg == "--delete" || strings.HasPrefix(arg, "--delete=") {
			return true
		}
		if shortFlagGroupContains(arg, 'd') || shortFlagGroupContains(arg, 'D') {
			return true
		}
	}
	return false
}

// shortFlagGroupContains checks if a short-flag group like "-dv" contains the target char.
func shortFlagGroupContains(arg string, target byte) bool {
	if !strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "--") {
		return false
	}
	for i := 1; i < len(arg); i++ {
		if arg[i] == target {
			return true
		}
	}
	return false
}

func gitPushIsDangerous(pushArgs []string) bool {
	for _, arg := range pushArgs {
		switch arg {
		case "--force", "--force-with-lease", "--force-if-includes", "--delete", "-f", "-d":
			return true
		}
		if strings.HasPrefix(arg, "--force-with-lease=") ||
			strings.HasPrefix(arg, "--force-if-includes=") ||
			strings.HasPrefix(arg, "--delete=") {
			return true
		}
		if shortFlagGroupContains(arg, 'f') || shortFlagGroupContains(arg, 'd') {
			return true
		}
		if gitPushRefspecIsDangerous(arg) {
			return true
		}
	}
	return false
}

func gitPushRefspecIsDangerous(arg string) bool {
	// `+<refspec>` forces updates and `:<dst>` deletes remote refs.
	return (strings.HasPrefix(arg, "+") || strings.HasPrefix(arg, ":")) && len(arg) > 1
}

func gitCleanIsForce(cleanArgs []string) bool {
	for _, arg := range cleanArgs {
		if arg == "--force" || arg == "-f" || strings.HasPrefix(arg, "--force=") {
			return true
		}
		if shortFlagGroupContains(arg, 'f') {
			return true
		}
	}
	return false
}
