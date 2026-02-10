// Package execenv provides environment variable filtering for shell command execution.
//
// Corresponds to: codex-rs/core/src/exec_env.rs + codex-rs/core/src/config/types.rs (ShellEnvironmentPolicy)
package execenv

import (
	"os"
	"strings"
)

// Inherit controls which environment variables are included as the starting set.
//
// Maps to: codex-rs/core/src/config/types.rs ShellEnvironmentPolicyInherit
type Inherit string

const (
	// InheritAll inherits the full environment from the parent process (default).
	InheritAll Inherit = "all"
	// InheritNone starts with an empty environment.
	InheritNone Inherit = "none"
	// InheritCore keeps only core platform variables (HOME, PATH, SHELL, etc.).
	InheritCore Inherit = "core"
)

// coreVars are the platform-essential variables kept by InheritCore.
//
// Maps to: codex-rs/core/src/exec_env.rs CORE_VARS
var coreVars = map[string]bool{
	"HOME":     true,
	"LOGNAME":  true,
	"PATH":     true,
	"SHELL":    true,
	"USER":     true,
	"USERNAME": true,
	"TMPDIR":   true,
	"TEMP":     true,
	"TMP":      true,
}

// ShellEnvironmentPolicy configures how environment variables are filtered
// before being passed to a spawned process.
//
// The derivation follows a 5-step algorithm:
//  1. Create initial map based on Inherit strategy.
//  2. If IgnoreDefaultExcludes is false, filter out *KEY*, *SECRET*, *TOKEN*.
//  3. Apply custom Exclude patterns.
//  4. Insert Set overrides.
//  5. If IncludeOnly is non-empty, keep only matching vars.
//
// Maps to: codex-rs/core/src/config/types.rs ShellEnvironmentPolicy
type ShellEnvironmentPolicy struct {
	// Inherit controls the starting set. Default: InheritAll.
	Inherit Inherit `json:"inherit,omitempty"`

	// IgnoreDefaultExcludes when true skips filtering of *KEY*/*SECRET*/*TOKEN*.
	// Default: true (matching Codex default — keeps sensitive vars unless explicitly excluded).
	IgnoreDefaultExcludes bool `json:"ignore_default_excludes"`

	// Exclude is a list of wildcard patterns for variable names to remove.
	// Patterns support * (any chars) and ? (single char), case-insensitive.
	Exclude []string `json:"exclude,omitempty"`

	// Set provides explicit key=value overrides inserted after filtering.
	Set map[string]string `json:"set,omitempty"`

	// IncludeOnly, if non-empty, keeps only variables matching these patterns.
	// Applied last, after all other steps.
	IncludeOnly []string `json:"include_only,omitempty"`
}

// DefaultShellEnvironmentPolicy returns the default policy: inherit all, no filtering.
// Matches Codex default: ignore_default_excludes=true, inherit=All.
func DefaultShellEnvironmentPolicy() ShellEnvironmentPolicy {
	return ShellEnvironmentPolicy{
		Inherit:               InheritAll,
		IgnoreDefaultExcludes: true,
	}
}

// CreateEnv builds a filtered environment map from the current process environment.
//
// Maps to: codex-rs/core/src/exec_env.rs create_env
func CreateEnv(policy *ShellEnvironmentPolicy) map[string]string {
	if policy == nil {
		p := DefaultShellEnvironmentPolicy()
		policy = &p
	}

	vars := make([]envVar, 0, 64)
	for _, entry := range os.Environ() {
		if k, v, ok := strings.Cut(entry, "="); ok {
			vars = append(vars, envVar{k, v})
		}
	}

	return populateEnv(vars, policy)
}

// CreateEnvFrom builds a filtered environment map from the given variables.
// Useful for testing or when environment is provided externally.
func CreateEnvFrom(vars map[string]string, policy *ShellEnvironmentPolicy) map[string]string {
	if policy == nil {
		p := DefaultShellEnvironmentPolicy()
		policy = &p
	}

	entries := make([]envVar, 0, len(vars))
	for k, v := range vars {
		entries = append(entries, envVar{k, v})
	}

	return populateEnv(entries, policy)
}

type envVar struct {
	key, value string
}

// populateEnv implements the 5-step filtering algorithm.
//
// Maps to: codex-rs/core/src/exec_env.rs populate_env
func populateEnv(vars []envVar, policy *ShellEnvironmentPolicy) map[string]string {
	// Step 1: Determine starting set based on inherit strategy.
	envMap := make(map[string]string)

	inherit := policy.Inherit
	if inherit == "" {
		inherit = InheritAll
	}

	switch inherit {
	case InheritAll:
		for _, v := range vars {
			envMap[v.key] = v.value
		}
	case InheritNone:
		// Empty map
	case InheritCore:
		for _, v := range vars {
			if coreVars[v.key] {
				envMap[v.key] = v.value
			}
		}
	}

	// Step 2: Apply default excludes if not disabled.
	if !policy.IgnoreDefaultExcludes {
		defaultExcludes := []string{"*KEY*", "*SECRET*", "*TOKEN*"}
		for k := range envMap {
			if matchesAny(k, defaultExcludes) {
				delete(envMap, k)
			}
		}
	}

	// Step 3: Apply custom excludes.
	if len(policy.Exclude) > 0 {
		for k := range envMap {
			if matchesAny(k, policy.Exclude) {
				delete(envMap, k)
			}
		}
	}

	// Step 4: Apply user-provided overrides.
	for k, v := range policy.Set {
		envMap[k] = v
	}

	// Step 5: If include_only is non-empty, keep only matching vars.
	if len(policy.IncludeOnly) > 0 {
		for k := range envMap {
			if !matchesAny(k, policy.IncludeOnly) {
				delete(envMap, k)
			}
		}
	}

	return envMap
}

// matchesAny returns true if name matches any of the wildcard patterns (case-insensitive).
func matchesAny(name string, patterns []string) bool {
	nameLower := strings.ToLower(name)
	for _, pattern := range patterns {
		if wildcardMatch(nameLower, strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}

// wildcardMatch performs simple wildcard matching with * (any chars) and ? (single char).
// Both inputs should be pre-lowercased for case-insensitive matching.
//
// Maps to: codex-rs WildMatchPattern behavior
func wildcardMatch(s, pattern string) bool {
	return wildcardMatchRecursive(s, pattern, 0, 0)
}

func wildcardMatchRecursive(s, pattern string, si, pi int) bool {
	for pi < len(pattern) {
		if si >= len(s) {
			// Remaining pattern must be all *'s
			for pi < len(pattern) {
				if pattern[pi] != '*' {
					return false
				}
				pi++
			}
			return true
		}

		switch pattern[pi] {
		case '*':
			// Try matching * with zero or more characters
			// Skip consecutive *'s
			for pi < len(pattern) && pattern[pi] == '*' {
				pi++
			}
			if pi == len(pattern) {
				return true // trailing * matches everything
			}
			// Try each possible starting position
			for si <= len(s) {
				if wildcardMatchRecursive(s, pattern, si, pi) {
					return true
				}
				si++
			}
			return false

		case '?':
			// Match exactly one character
			si++
			pi++

		default:
			if s[si] != pattern[pi] {
				return false
			}
			si++
			pi++
		}
	}

	return si == len(s)
}

// EnvMapToSlice converts a map to a slice of "KEY=VALUE" strings suitable for exec.Cmd.Env.
func EnvMapToSlice(env map[string]string) []string {
	result := make([]string, 0, len(env))
	for k, v := range env {
		result = append(result, k+"="+v)
	}
	return result
}
