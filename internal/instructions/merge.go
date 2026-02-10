package instructions

import "strings"

// MergeInput collects all instruction sources for merging.
type MergeInput struct {
	// BaseOverride replaces the default base system prompt if non-empty.
	BaseOverride string

	// CLIProjectDocs contains AGENTS.md content discovered from the CLI's
	// local project directory. Used as fallback when worker docs are empty.
	CLIProjectDocs string

	// WorkerProjectDocs contains AGENTS.md content discovered from the
	// worker's root directory. Takes precedence over CLIProjectDocs.
	WorkerProjectDocs string

	// UserPersonalInstructions contains user preferences from
	// ~/.codex/instructions.md. Always appended if non-empty.
	UserPersonalInstructions string

	// ApprovalMode is the session's approval policy ("never", "unless-trusted").
	ApprovalMode string

	// Cwd is the session working directory.
	Cwd string
}

// MergedInstructions is the result of merging all instruction sources.
type MergedInstructions struct {
	// Base is the core system prompt (sent as system message).
	Base string

	// Developer contains working-directory context and approval mode
	// (sent as developer message).
	Developer string

	// User contains project docs and personal instructions
	// (appended to system message or sent as user context).
	User string
}

// MergeInstructions combines all instruction sources into the three-tier
// instruction hierarchy (Base, Developer, User).
//
// Merge rules:
//   - Base: GetBaseInstructions(BaseOverride)
//   - Developer: ComposeDeveloperInstructions(ApprovalMode, Cwd)
//   - User: WorkerProjectDocs (if non-empty, else CLIProjectDocs)
//     + UserPersonalInstructions (always appended)
func MergeInstructions(input MergeInput) MergedInstructions {
	base := GetBaseInstructions(input.BaseOverride)
	developer := ComposeDeveloperInstructions(input.ApprovalMode, input.Cwd)

	// Assemble user instructions: project docs + personal preferences
	var userParts []string

	// Project docs: worker-side is authoritative, CLI is fallback
	projectDocs := input.WorkerProjectDocs
	if projectDocs == "" {
		projectDocs = input.CLIProjectDocs
	}
	if projectDocs != "" {
		userParts = append(userParts, projectDocs)
	}

	// Personal instructions always appended
	if input.UserPersonalInstructions != "" {
		userParts = append(userParts, input.UserPersonalInstructions)
	}

	user := strings.Join(userParts, "\n\n")

	return MergedInstructions{
		Base:      base,
		Developer: developer,
		User:      user,
	}
}
