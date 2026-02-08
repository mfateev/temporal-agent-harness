package command_safety

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Maps to: codex-rs/core/src/bash.rs tests

func TestAcceptsSingleSimpleCommand(t *testing.T) {
	cmds := parseWordOnlyCommandsSequence("ls -1")
	require.NotNil(t, cmds)
	assert.Equal(t, [][]string{{"ls", "-1"}}, cmds)
}

func TestAcceptsMultipleCommandsWithAllowedOperators(t *testing.T) {
	src := "ls && pwd; echo 'hi there' | wc -l"
	cmds := parseWordOnlyCommandsSequence(src)
	require.NotNil(t, cmds)
	expected := [][]string{
		{"ls"},
		{"pwd"},
		{"echo", "hi there"},
		{"wc", "-l"},
	}
	assert.Equal(t, expected, cmds)
}

func TestExtractsDoubleAndSingleQuotedStrings(t *testing.T) {
	cmds := parseWordOnlyCommandsSequence(`echo "hello world"`)
	require.NotNil(t, cmds)
	assert.Equal(t, [][]string{{"echo", "hello world"}}, cmds)

	cmds2 := parseWordOnlyCommandsSequence("echo 'hi there'")
	require.NotNil(t, cmds2)
	assert.Equal(t, [][]string{{"echo", "hi there"}}, cmds2)
}

func TestAcceptsDoubleQuotedStringsWithNewlines(t *testing.T) {
	cmds := parseWordOnlyCommandsSequence("git commit -m \"line1\nline2\"")
	require.NotNil(t, cmds)
	assert.Equal(t, [][]string{{"git", "commit", "-m", "line1\nline2"}}, cmds)
}

func TestAcceptsMixedQuoteConcatenation(t *testing.T) {
	cmds := parseWordOnlyCommandsSequence(`echo "/usr"'/'"local"/bin`)
	require.NotNil(t, cmds)
	assert.Equal(t, [][]string{{"echo", "/usr/local/bin"}}, cmds)

	cmds2 := parseWordOnlyCommandsSequence(`echo '/usr'"/"'local'/bin`)
	require.NotNil(t, cmds2)
	assert.Equal(t, [][]string{{"echo", "/usr/local/bin"}}, cmds2)
}

func TestRejectsDoubleQuotedStringsWithExpansions(t *testing.T) {
	assert.Nil(t, parseWordOnlyCommandsSequence(`echo "hi ${USER}"`))
	assert.Nil(t, parseWordOnlyCommandsSequence(`echo "$HOME"`))
}

func TestAcceptsNumbersAsWords(t *testing.T) {
	cmds := parseWordOnlyCommandsSequence("echo 123 456")
	require.NotNil(t, cmds)
	assert.Equal(t, [][]string{{"echo", "123", "456"}}, cmds)
}

func TestRejectsParenthesesAndSubshells(t *testing.T) {
	assert.Nil(t, parseWordOnlyCommandsSequence("(ls)"))
	assert.Nil(t, parseWordOnlyCommandsSequence("ls || (pwd && echo hi)"))
}

func TestRejectsRedirectionsAndUnsupportedOperators(t *testing.T) {
	assert.Nil(t, parseWordOnlyCommandsSequence("ls > out.txt"))
	assert.Nil(t, parseWordOnlyCommandsSequence("echo hi & echo bye"))
}

func TestRejectsCommandAndProcessSubstitutionsAndExpansions(t *testing.T) {
	assert.Nil(t, parseWordOnlyCommandsSequence("echo $(pwd)"))
	assert.Nil(t, parseWordOnlyCommandsSequence("echo `pwd`"))
	assert.Nil(t, parseWordOnlyCommandsSequence("echo $HOME"))
	assert.Nil(t, parseWordOnlyCommandsSequence(`echo "hi $USER"`))
}

func TestRejectsVariableAssignmentPrefix(t *testing.T) {
	assert.Nil(t, parseWordOnlyCommandsSequence("FOO=bar ls"))
}

func TestRejectsTrailingOperatorParseError(t *testing.T) {
	assert.Nil(t, parseWordOnlyCommandsSequence("ls &&"))
}

func TestParseZshLcPlainCommands(t *testing.T) {
	command := []string{"zsh", "-lc", "ls"}
	parsed := ParseShellLcPlainCommands(command)
	require.NotNil(t, parsed)
	assert.Equal(t, [][]string{{"ls"}}, parsed)
}

func TestAcceptsConcatenatedFlagAndValue(t *testing.T) {
	cmds := parseWordOnlyCommandsSequence(`rg -n "foo" -g"*.py"`)
	require.NotNil(t, cmds)
	assert.Equal(t, [][]string{{"rg", "-n", "foo", "-g*.py"}}, cmds)
}

func TestAcceptsConcatenatedFlagWithSingleQuotes(t *testing.T) {
	cmds := parseWordOnlyCommandsSequence("grep -n 'pattern' -g'*.txt'")
	require.NotNil(t, cmds)
	assert.Equal(t, [][]string{{"grep", "-n", "pattern", "-g*.txt"}}, cmds)
}

func TestRejectsConcatenationWithVariableSubstitution(t *testing.T) {
	assert.Nil(t, parseWordOnlyCommandsSequence(`rg -g"$VAR" pattern`))
	assert.Nil(t, parseWordOnlyCommandsSequence(`rg -g"${VAR}" pattern`))
}

func TestRejectsConcatenationWithCommandSubstitution(t *testing.T) {
	assert.Nil(t, parseWordOnlyCommandsSequence(`rg -g"$(pwd)" pattern`))
	assert.Nil(t, parseWordOnlyCommandsSequence(`rg -g"$(echo '*.py')" pattern`))
}
