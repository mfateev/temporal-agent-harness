package execenv

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func makeVars(pairs ...string) map[string]string {
	m := make(map[string]string, len(pairs)/2)
	for i := 0; i < len(pairs)-1; i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return m
}

// TestCoreInheritDefaults_KeepSensitiveVars matches Rust test:
// test_core_inherit_defaults_keep_sensitive_vars
func TestCoreInheritDefaults_KeepSensitiveVars(t *testing.T) {
	vars := makeVars(
		"PATH", "/usr/bin",
		"HOME", "/home/user",
		"API_KEY", "secret",
		"SECRET_TOKEN", "t",
	)

	policy := DefaultShellEnvironmentPolicy() // inherit All, default excludes ignored
	result := CreateEnvFrom(vars, &policy)

	assert.Equal(t, "/usr/bin", result["PATH"])
	assert.Equal(t, "/home/user", result["HOME"])
	assert.Equal(t, "secret", result["API_KEY"])
	assert.Equal(t, "t", result["SECRET_TOKEN"])
	assert.Len(t, result, 4)
}

// TestCoreInheritWithDefaultExcludesEnabled matches Rust test:
// test_core_inherit_with_default_excludes_enabled
func TestCoreInheritWithDefaultExcludesEnabled(t *testing.T) {
	vars := makeVars(
		"PATH", "/usr/bin",
		"HOME", "/home/user",
		"API_KEY", "secret",
		"SECRET_TOKEN", "t",
	)

	policy := ShellEnvironmentPolicy{
		Inherit:               InheritAll,
		IgnoreDefaultExcludes: false, // apply KEY/SECRET/TOKEN filter
	}
	result := CreateEnvFrom(vars, &policy)

	assert.Equal(t, "/usr/bin", result["PATH"])
	assert.Equal(t, "/home/user", result["HOME"])
	assert.NotContains(t, result, "API_KEY")
	assert.NotContains(t, result, "SECRET_TOKEN")
	assert.Len(t, result, 2)
}

// TestIncludeOnly matches Rust test: test_include_only
func TestIncludeOnly(t *testing.T) {
	vars := makeVars("PATH", "/usr/bin", "FOO", "bar")

	policy := ShellEnvironmentPolicy{
		Inherit:               InheritAll,
		IgnoreDefaultExcludes: true,
		IncludeOnly:           []string{"*PATH"},
	}
	result := CreateEnvFrom(vars, &policy)

	assert.Equal(t, "/usr/bin", result["PATH"])
	assert.NotContains(t, result, "FOO")
	assert.Len(t, result, 1)
}

// TestSetOverrides matches Rust test: test_set_overrides
func TestSetOverrides(t *testing.T) {
	vars := makeVars("PATH", "/usr/bin")

	policy := ShellEnvironmentPolicy{
		Inherit:               InheritAll,
		IgnoreDefaultExcludes: true,
		Set:                   map[string]string{"NEW_VAR": "42"},
	}
	result := CreateEnvFrom(vars, &policy)

	assert.Equal(t, "/usr/bin", result["PATH"])
	assert.Equal(t, "42", result["NEW_VAR"])
	assert.Len(t, result, 2)
}

// TestInheritAll matches Rust test: test_inherit_all
func TestInheritAll(t *testing.T) {
	vars := makeVars("PATH", "/usr/bin", "FOO", "bar")

	policy := ShellEnvironmentPolicy{
		Inherit:               InheritAll,
		IgnoreDefaultExcludes: true,
	}
	result := CreateEnvFrom(vars, &policy)

	assert.Equal(t, vars, result)
}

// TestInheritAllWithDefaultExcludes matches Rust test:
// test_inherit_all_with_default_excludes
func TestInheritAllWithDefaultExcludes(t *testing.T) {
	vars := makeVars("PATH", "/usr/bin", "API_KEY", "secret")

	policy := ShellEnvironmentPolicy{
		Inherit:               InheritAll,
		IgnoreDefaultExcludes: false,
	}
	result := CreateEnvFrom(vars, &policy)

	assert.Equal(t, "/usr/bin", result["PATH"])
	assert.NotContains(t, result, "API_KEY")
	assert.Len(t, result, 1)
}

// TestInheritNone matches Rust test: test_inherit_none
func TestInheritNone(t *testing.T) {
	vars := makeVars("PATH", "/usr/bin", "HOME", "/home")

	policy := ShellEnvironmentPolicy{
		Inherit:               InheritNone,
		IgnoreDefaultExcludes: true,
		Set:                   map[string]string{"ONLY_VAR": "yes"},
	}
	result := CreateEnvFrom(vars, &policy)

	assert.Equal(t, "yes", result["ONLY_VAR"])
	assert.NotContains(t, result, "PATH")
	assert.NotContains(t, result, "HOME")
	assert.Len(t, result, 1)
}

// TestInheritCore keeps only core platform variables.
func TestInheritCore(t *testing.T) {
	vars := makeVars(
		"PATH", "/usr/bin",
		"HOME", "/home/user",
		"USER", "testuser",
		"CUSTOM_VAR", "value",
		"API_KEY", "secret",
	)

	policy := ShellEnvironmentPolicy{
		Inherit:               InheritCore,
		IgnoreDefaultExcludes: true,
	}
	result := CreateEnvFrom(vars, &policy)

	assert.Equal(t, "/usr/bin", result["PATH"])
	assert.Equal(t, "/home/user", result["HOME"])
	assert.Equal(t, "testuser", result["USER"])
	assert.NotContains(t, result, "CUSTOM_VAR")
	assert.NotContains(t, result, "API_KEY")
	assert.Len(t, result, 3)
}

// TestInheritCoreWithDefaultExcludes filters core vars through default excludes.
func TestInheritCoreWithDefaultExcludes(t *testing.T) {
	vars := makeVars(
		"PATH", "/usr/bin",
		"HOME", "/home/user",
		"SECRET_TOKEN", "hidden",
	)

	policy := ShellEnvironmentPolicy{
		Inherit:               InheritCore,
		IgnoreDefaultExcludes: false,
	}
	result := CreateEnvFrom(vars, &policy)

	assert.Equal(t, "/usr/bin", result["PATH"])
	assert.Equal(t, "/home/user", result["HOME"])
	// SECRET_TOKEN is not a core var, so it's not even in the starting set
	assert.NotContains(t, result, "SECRET_TOKEN")
}

// TestCustomExclude removes variables matching custom patterns.
func TestCustomExclude(t *testing.T) {
	vars := makeVars(
		"PATH", "/usr/bin",
		"AWS_ACCESS_KEY_ID", "AKIA...",
		"AWS_SECRET_ACCESS_KEY", "secret",
		"HOME", "/home/user",
	)

	policy := ShellEnvironmentPolicy{
		Inherit:               InheritAll,
		IgnoreDefaultExcludes: true, // don't use defaults
		Exclude:               []string{"AWS_*"},
	}
	result := CreateEnvFrom(vars, &policy)

	assert.Equal(t, "/usr/bin", result["PATH"])
	assert.Equal(t, "/home/user", result["HOME"])
	assert.NotContains(t, result, "AWS_ACCESS_KEY_ID")
	assert.NotContains(t, result, "AWS_SECRET_ACCESS_KEY")
	assert.Len(t, result, 2)
}

// TestSetOverridesExcluded verifies that Set inserts happen after excludes.
func TestSetOverridesExcluded(t *testing.T) {
	vars := makeVars("API_KEY", "old_secret")

	policy := ShellEnvironmentPolicy{
		Inherit:               InheritAll,
		IgnoreDefaultExcludes: false, // removes API_KEY
		Set:                   map[string]string{"API_KEY": "new_value"},
	}
	result := CreateEnvFrom(vars, &policy)

	// API_KEY was removed by default excludes, but Set re-inserts it
	assert.Equal(t, "new_value", result["API_KEY"])
}

// TestIncludeOnlyAfterSet verifies include_only applies after Set.
func TestIncludeOnlyAfterSet(t *testing.T) {
	vars := makeVars("PATH", "/usr/bin")

	policy := ShellEnvironmentPolicy{
		Inherit:               InheritAll,
		IgnoreDefaultExcludes: true,
		Set:                   map[string]string{"NEW_VAR": "42", "KEEP_ME": "yes"},
		IncludeOnly:           []string{"KEEP_*"},
	}
	result := CreateEnvFrom(vars, &policy)

	assert.Equal(t, "yes", result["KEEP_ME"])
	assert.NotContains(t, result, "PATH")
	assert.NotContains(t, result, "NEW_VAR")
	assert.Len(t, result, 1)
}

// TestNilPolicy uses default (inherit all, no filtering).
func TestNilPolicy(t *testing.T) {
	vars := makeVars("PATH", "/usr/bin", "API_KEY", "secret")
	result := CreateEnvFrom(vars, nil)

	assert.Equal(t, "/usr/bin", result["PATH"])
	assert.Equal(t, "secret", result["API_KEY"])
	assert.Len(t, result, 2)
}

// TestEnvMapToSlice converts map to KEY=VALUE slice.
func TestEnvMapToSlice(t *testing.T) {
	env := map[string]string{"FOO": "bar", "BAZ": "qux"}
	slice := EnvMapToSlice(env)
	assert.Len(t, slice, 2)
	assert.Contains(t, slice, "FOO=bar")
	assert.Contains(t, slice, "BAZ=qux")
}

// --- Wildcard matching tests ---

func TestWildcardMatch(t *testing.T) {
	tests := []struct {
		s       string
		pattern string
		want    bool
	}{
		// Exact match
		{"foo", "foo", true},
		{"foo", "bar", false},

		// * matches any sequence
		{"api_key", "*key*", true},
		{"API_KEY", "*key*", false}, // case-sensitive at this level
		{"secret_token", "*token*", true},
		{"path", "*key*", false},

		// * at edges
		{"foobar", "foo*", true},
		{"foobar", "*bar", true},
		{"foobar", "*", true},
		{"", "*", true},
		{"", "", true},

		// ? matches single char
		{"foo", "f?o", true},
		{"foo", "f??", true},
		{"fo", "f??", false},

		// Combined
		{"api_secret_key", "*secret*", true},
		{"my_token_123", "*token*", true},
		{"nothing_here", "*key*", false},
	}

	for _, tt := range tests {
		t.Run(tt.s+"_"+tt.pattern, func(t *testing.T) {
			assert.Equal(t, tt.want, wildcardMatch(tt.s, tt.pattern))
		})
	}
}

func TestMatchesAny_CaseInsensitive(t *testing.T) {
	patterns := []string{"*KEY*", "*SECRET*", "*TOKEN*"}

	assert.True(t, matchesAny("API_KEY", patterns))
	assert.True(t, matchesAny("api_key", patterns))
	assert.True(t, matchesAny("My_Secret_Value", patterns))
	assert.True(t, matchesAny("GITHUB_TOKEN", patterns))
	assert.True(t, matchesAny("github_token", patterns))
	assert.False(t, matchesAny("PATH", patterns))
	assert.False(t, matchesAny("HOME", patterns))
	assert.False(t, matchesAny("SHELL", patterns))
}
