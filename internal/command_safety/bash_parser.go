// Package command_safety classifies shell commands as safe, dangerous, or unknown.
//
// Corresponds to: codex-rs/core/src/command_safety/ + codex-rs/core/src/bash.rs
package command_safety

import (
	"path/filepath"
	"strings"
)

// ParseShellLcPlainCommands parses bash/zsh -lc "..." into individual commands.
// Returns nil if not a bash/zsh -lc invocation or if script has unsafe constructs.
//
// Maps to: codex-rs/core/src/bash.rs parse_shell_lc_plain_commands
func ParseShellLcPlainCommands(command []string) [][]string {
	_, script := extractBashCommand(command)
	if script == "" {
		return nil
	}
	return parseWordOnlyCommandsSequence(script)
}

// extractBashCommand extracts (shell, script) from ["bash", "-lc", "script"] or
// ["zsh", "-lc", "script"] or ["sh", "-lc", "script"] patterns.
// Also accepts "-c" flag.
//
// Maps to: codex-rs/core/src/bash.rs extract_bash_command
func extractBashCommand(command []string) (shell, script string) {
	if len(command) != 3 {
		return "", ""
	}
	shell = command[0]
	flag := command[1]
	script = command[2]

	if flag != "-lc" && flag != "-c" {
		return "", ""
	}

	base := filepath.Base(shell)
	switch base {
	case "bash", "zsh", "sh":
		return shell, script
	default:
		return "", ""
	}
}

// parseWordOnlyCommandsSequence parses a bash script into individual commands.
// Only accepts word-only commands joined by &&, ||, ;, |.
// Rejects redirections, subshells, variable expansion, command substitution,
// background jobs, and other unsafe constructs.
// Returns nil if the script contains any unsafe constructs.
//
// This is a custom single-pass scanner replacing tree-sitter-bash.
// Maps to: codex-rs/core/src/bash.rs try_parse_word_only_commands_sequence
func parseWordOnlyCommandsSequence(script string) [][]string {
	p := &parser{
		src: script,
		pos: 0,
	}
	return p.parse()
}

type parser struct {
	src string
	pos int
}

func (p *parser) parse() [][]string {
	var commands [][]string
	var currentWords []string
	needCommand := false // true after an operator, expecting a following command

	for p.pos < len(p.src) {
		p.skipWhitespace()
		if p.pos >= len(p.src) {
			break
		}

		ch := p.src[p.pos]

		// Comment: skip to end of line
		if ch == '#' {
			for p.pos < len(p.src) && p.src[p.pos] != '\n' {
				p.pos++
			}
			continue
		}

		// Reject unsafe characters
		if ch == '>' || ch == '<' || ch == '(' || ch == ')' || ch == '`' || ch == '$' {
			return nil
		}

		// Handle operators: &&, ||, ;, |
		if ch == '&' {
			if p.pos+1 < len(p.src) && p.src[p.pos+1] == '&' {
				// && operator
				if len(currentWords) == 0 {
					return nil // operator with no preceding command
				}
				commands = append(commands, currentWords)
				currentWords = nil
				needCommand = true
				p.pos += 2
				continue
			}
			// Bare & (background job) - reject
			return nil
		}

		if ch == '|' {
			if p.pos+1 < len(p.src) && p.src[p.pos+1] == '|' {
				// || operator
				if len(currentWords) == 0 {
					return nil
				}
				commands = append(commands, currentWords)
				currentWords = nil
				needCommand = true
				p.pos += 2
				continue
			}
			// | pipe operator
			if len(currentWords) == 0 {
				return nil
			}
			commands = append(commands, currentWords)
			currentWords = nil
			needCommand = true
			p.pos++
			continue
		}

		if ch == ';' {
			if len(currentWords) == 0 {
				return nil
			}
			commands = append(commands, currentWords)
			currentWords = nil
			needCommand = true
			p.pos++
			continue
		}

		// Parse a word (possibly concatenated with quotes)
		word := p.parseWord()
		if word == nil {
			return nil // parse error
		}

		// Reject variable assignment: if this is the first word of a command
		// and it contains '=', it's a variable assignment like FOO=bar.
		if len(currentWords) == 0 && strings.Contains(*word, "=") {
			return nil
		}

		currentWords = append(currentWords, *word)
		needCommand = false
	}

	// Reject trailing operator (e.g. "ls &&")
	if needCommand {
		return nil
	}

	// Emit the last command if any
	if len(currentWords) > 0 {
		commands = append(commands, currentWords)
	}

	if len(commands) == 0 {
		return nil
	}

	return commands
}

// parseWord parses one "word" which may be a plain token, a quoted string,
// or a concatenation of these (e.g. -g"*.py" or "/usr"'/'"local"/bin).
// Returns nil on error (unsafe construct encountered).
func (p *parser) parseWord() *string {
	var result strings.Builder
	gotAny := false

	for p.pos < len(p.src) {
		ch := p.src[p.pos]

		// Whitespace or operator terminates the word
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			break
		}
		if ch == '&' || ch == '|' || ch == ';' || ch == '#' {
			break
		}

		// Reject unsafe characters
		if ch == '>' || ch == '<' || ch == '(' || ch == ')' || ch == '`' || ch == '$' {
			return nil
		}

		// Reject = in first word position (variable assignment)
		if ch == '=' && !gotAny {
			return nil
		}
		// = in the middle of a word is fine (e.g. --flag=value) only if
		// we already consumed something. But if we have words already on
		// the command (this is an argument, not a command name), = is
		// fine too. However, parseWord doesn't know its position in the
		// command. The caller handles variable-assignment rejection at
		// the top level by checking for = before we get here.
		// Actually, we need to handle FOO=bar which the Rust version
		// rejects via tree-sitter's variable_assignment node. We handle
		// it differently: the top-level parse loop rejects bare = at
		// command start. In a word context, = is fine (e.g. --key=value).

		if ch == '\'' {
			// Single-quoted string
			s := p.parseSingleQuoted()
			if s == nil {
				return nil
			}
			result.WriteString(*s)
			gotAny = true
			continue
		}

		if ch == '"' {
			// Double-quoted string
			s := p.parseDoubleQuoted()
			if s == nil {
				return nil
			}
			result.WriteString(*s)
			gotAny = true
			continue
		}

		// Plain character - part of unquoted word
		result.WriteByte(ch)
		p.pos++
		gotAny = true
	}

	if !gotAny {
		return nil
	}

	s := result.String()
	return &s
}

// parseSingleQuoted parses a single-quoted string 'content'.
// Returns the content without quotes, or nil on error.
func (p *parser) parseSingleQuoted() *string {
	if p.pos >= len(p.src) || p.src[p.pos] != '\'' {
		return nil
	}
	p.pos++ // skip opening '

	var result strings.Builder
	for p.pos < len(p.src) {
		ch := p.src[p.pos]
		if ch == '\'' {
			p.pos++ // skip closing '
			s := result.String()
			return &s
		}
		result.WriteByte(ch)
		p.pos++
	}

	// Unterminated single quote
	return nil
}

// parseDoubleQuoted parses a double-quoted string "content".
// Rejects any $ or ` inside (no expansion/substitution allowed).
// Returns the content without quotes, or nil on error.
func (p *parser) parseDoubleQuoted() *string {
	if p.pos >= len(p.src) || p.src[p.pos] != '"' {
		return nil
	}
	p.pos++ // skip opening "

	var result strings.Builder
	for p.pos < len(p.src) {
		ch := p.src[p.pos]
		if ch == '"' {
			p.pos++ // skip closing "
			s := result.String()
			return &s
		}
		// Reject expansions and substitutions inside double quotes
		if ch == '$' || ch == '`' {
			return nil
		}
		result.WriteByte(ch)
		p.pos++
	}

	// Unterminated double quote
	return nil
}

func (p *parser) skipWhitespace() {
	for p.pos < len(p.src) {
		ch := p.src[p.pos]
		if ch != ' ' && ch != '\t' && ch != '\n' && ch != '\r' {
			break
		}
		p.pos++
	}
}
