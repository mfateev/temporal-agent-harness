package activities

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/mfateev/temporal-agent-harness/internal/instructions"
)

// LoadWorkerInstructionsInput is the input for the LoadWorkerInstructions activity.
type LoadWorkerInstructionsInput struct {
	Cwd             string   `json:"cwd"`
	AgentsFileNames []string `json:"agents_file_names,omitempty"`
}

// LoadWorkerInstructionsOutput is the output from the LoadWorkerInstructions activity.
type LoadWorkerInstructionsOutput struct {
	ProjectDocs string `json:"project_docs,omitempty"`
	GitRoot     string `json:"git_root,omitempty"`
}

// InstructionActivities contains instruction-loading activities.
type InstructionActivities struct{}

// NewInstructionActivities creates a new InstructionActivities instance.
func NewInstructionActivities() *InstructionActivities {
	return &InstructionActivities{}
}

// LoadWorkerInstructions discovers and loads AGENTS.md files from the
// worker's file system. Runs on the session task queue so it executes
// on the same machine where tools run.
func (a *InstructionActivities) LoadWorkerInstructions(
	ctx context.Context, input LoadWorkerInstructionsInput,
) (LoadWorkerInstructionsOutput, error) {
	if input.Cwd == "" {
		return LoadWorkerInstructionsOutput{}, nil
	}

	gitRoot, err := instructions.FindGitRoot(input.Cwd)
	if err != nil {
		return LoadWorkerInstructionsOutput{}, nil // non-fatal
	}

	if gitRoot == "" {
		// Not in a git repo — no project docs to load
		return LoadWorkerInstructionsOutput{}, nil
	}

	projectDocs, err := instructions.LoadProjectDocs(gitRoot, input.Cwd, input.AgentsFileNames)
	if err != nil {
		return LoadWorkerInstructionsOutput{}, nil // non-fatal
	}

	return LoadWorkerInstructionsOutput{
		ProjectDocs: projectDocs,
		GitRoot:     gitRoot,
	}, nil
}

// LoadExecPolicyInput is the input for the LoadExecPolicy activity.
type LoadExecPolicyInput struct {
	CodexHome string `json:"codex_home"`
}

// LoadExecPolicyOutput is the output from the LoadExecPolicy activity.
type LoadExecPolicyOutput struct {
	// RulesSource is the concatenated content of all *.rules files.
	// Transported as text so the workflow can parse it deterministically.
	RulesSource string `json:"rules_source,omitempty"`
}

// LoadExecPolicy reads exec policy rules from the worker's filesystem.
// Similar to LoadWorkerInstructions — runs on session task queue.
func (a *InstructionActivities) LoadExecPolicy(
	_ context.Context, input LoadExecPolicyInput,
) (LoadExecPolicyOutput, error) {
	if input.CodexHome == "" {
		return LoadExecPolicyOutput{}, nil
	}

	rulesDir := filepath.Join(input.CodexHome, "rules")
	entries, err := os.ReadDir(rulesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return LoadExecPolicyOutput{}, nil
		}
		return LoadExecPolicyOutput{}, nil // non-fatal
	}

	var parts []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".rules") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(rulesDir, entry.Name()))
		if err != nil {
			continue // skip unreadable files
		}
		parts = append(parts, string(data))
	}

	return LoadExecPolicyOutput{
		RulesSource: strings.Join(parts, "\n"),
	}, nil
}
