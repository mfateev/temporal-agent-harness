package instructions

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// --- GetBaseInstructions tests ---

func TestGetBaseInstructions_Default(t *testing.T) {
	result := GetBaseInstructions("")
	assert.Contains(t, result, "coding agent")
	assert.Contains(t, result, "Task execution")
}

func TestGetBaseInstructions_Override(t *testing.T) {
	result := GetBaseInstructions("custom system prompt")
	assert.Equal(t, "custom system prompt", result)
}

// --- ComposeDeveloperInstructions tests ---

func TestComposeDeveloperInstructions_WithCwdAndMode(t *testing.T) {
	result := ComposeDeveloperInstructions("unless-trusted", "/home/user/project")
	assert.Contains(t, result, "/home/user/project")
	assert.Contains(t, result, "unless-trusted")
}

func TestComposeDeveloperInstructions_NeverMode(t *testing.T) {
	result := ComposeDeveloperInstructions("never", "/tmp")
	assert.Contains(t, result, "full-auto")
}

func TestComposeDeveloperInstructions_EmptyMode(t *testing.T) {
	result := ComposeDeveloperInstructions("", "/tmp")
	assert.Contains(t, result, "/tmp")
	assert.NotContains(t, result, "Approval mode")
}

func TestComposeDeveloperInstructions_Empty(t *testing.T) {
	result := ComposeDeveloperInstructions("", "")
	assert.Empty(t, result)
}

// --- BuildEnvironmentContext tests ---

func TestBuildEnvironmentContext_Basic(t *testing.T) {
	result := BuildEnvironmentContext("/home/user/project", "zsh")
	assert.Contains(t, result, "<cwd>/home/user/project</cwd>")
	assert.Contains(t, result, "<shell>zsh</shell>")
	assert.Contains(t, result, "<environment_context>")
}

func TestBuildEnvironmentContext_DefaultShell(t *testing.T) {
	result := BuildEnvironmentContext("/tmp", "")
	assert.Contains(t, result, "<shell>bash</shell>")
}

// --- MergeInstructions tests ---

func TestMergeInstructions_WorkerDocsTakePrecedence(t *testing.T) {
	result := MergeInstructions(MergeInput{
		CLIProjectDocs:    "cli docs",
		WorkerProjectDocs: "worker docs",
	})
	assert.Contains(t, result.User, "worker docs")
	assert.NotContains(t, result.User, "cli docs")
}

func TestMergeInstructions_CLIDocsFallback(t *testing.T) {
	result := MergeInstructions(MergeInput{
		CLIProjectDocs:    "cli docs",
		WorkerProjectDocs: "",
	})
	assert.Contains(t, result.User, "cli docs")
}

func TestMergeInstructions_PersonalInstructionsAlwaysAppended(t *testing.T) {
	result := MergeInstructions(MergeInput{
		WorkerProjectDocs:        "project docs",
		UserPersonalInstructions: "personal prefs",
	})
	assert.Contains(t, result.User, "project docs")
	assert.Contains(t, result.User, "personal prefs")
}

func TestMergeInstructions_PersonalInstructionsAloneWhenNoDocs(t *testing.T) {
	result := MergeInstructions(MergeInput{
		UserPersonalInstructions: "personal prefs",
	})
	assert.Equal(t, "personal prefs", result.User)
}

func TestMergeInstructions_BaseOverride(t *testing.T) {
	result := MergeInstructions(MergeInput{
		BaseOverride: "custom base",
	})
	assert.Equal(t, "custom base", result.Base)
}

func TestMergeInstructions_DefaultBase(t *testing.T) {
	result := MergeInstructions(MergeInput{})
	assert.Contains(t, result.Base, "coding agent")
}

func TestMergeInstructions_DeveloperPopulated(t *testing.T) {
	result := MergeInstructions(MergeInput{
		ApprovalMode: "never",
		Cwd:          "/home/user/project",
	})
	assert.Contains(t, result.Developer, "/home/user/project")
	assert.Contains(t, result.Developer, "full-auto")
}

func TestMergeInstructions_AllEmpty(t *testing.T) {
	result := MergeInstructions(MergeInput{})
	// Base should have default prompt
	assert.NotEmpty(t, result.Base)
	// Developer and User should be empty with no inputs
	assert.Empty(t, result.Developer)
	assert.Empty(t, result.User)
}
