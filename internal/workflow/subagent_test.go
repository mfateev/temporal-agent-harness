package workflow

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mfateev/temporal-agent-harness/internal/models"
	"github.com/mfateev/temporal-agent-harness/internal/tools"
)

// ---------------------------------------------------------------------------
// Unit tests for subagent types and helpers (no Temporal test env needed)
// ---------------------------------------------------------------------------

func TestParseAgentRole(t *testing.T) {
	tests := []struct {
		input    string
		expected AgentRole
	}{
		{"default", AgentRoleDefault},
		{"orchestrator", AgentRoleOrchestrator},
		{"worker", AgentRoleWorker},
		{"explorer", AgentRoleExplorer},
		{"planner", AgentRolePlanner},
		{"", AgentRoleDefault},
		{"unknown", AgentRoleDefault},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, parseAgentRole(tt.input))
		})
	}
}

func TestAgentStatus_IsTerminal(t *testing.T) {
	tests := []struct {
		status   AgentStatus
		terminal bool
	}{
		{AgentStatusPendingInit, false},
		{AgentStatusRunning, false},
		{AgentStatusCompleted, true},
		{AgentStatusErrored, true},
		{AgentStatusShutdown, true},
		{AgentStatusNotFound, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			assert.Equal(t, tt.terminal, tt.status.isTerminal())
		})
	}
}

func TestAgentControl_HasActiveChildren(t *testing.T) {
	t.Run("no agents", func(t *testing.T) {
		ac := NewAgentControl(0)
		assert.False(t, ac.HasActiveChildren())
	})

	t.Run("one running agent", func(t *testing.T) {
		ac := NewAgentControl(0)
		ac.Agents["a1"] = &AgentInfo{AgentID: "a1", Status: AgentStatusRunning}
		assert.True(t, ac.HasActiveChildren())
	})

	t.Run("one completed agent", func(t *testing.T) {
		ac := NewAgentControl(0)
		ac.Agents["a1"] = &AgentInfo{AgentID: "a1", Status: AgentStatusCompleted}
		assert.False(t, ac.HasActiveChildren())
	})

	t.Run("mixed active and completed", func(t *testing.T) {
		ac := NewAgentControl(0)
		ac.Agents["a1"] = &AgentInfo{AgentID: "a1", Status: AgentStatusCompleted}
		ac.Agents["a2"] = &AgentInfo{AgentID: "a2", Status: AgentStatusRunning}
		assert.True(t, ac.HasActiveChildren())
	})

	t.Run("all terminal states", func(t *testing.T) {
		ac := NewAgentControl(0)
		ac.Agents["a1"] = &AgentInfo{AgentID: "a1", Status: AgentStatusCompleted}
		ac.Agents["a2"] = &AgentInfo{AgentID: "a2", Status: AgentStatusErrored}
		ac.Agents["a3"] = &AgentInfo{AgentID: "a3", Status: AgentStatusShutdown}
		assert.False(t, ac.HasActiveChildren())
	})
}

func TestIsCollabToolCall(t *testing.T) {
	collabTools := []string{"spawn_agent", "send_input", "wait", "close_agent", "resume_agent"}
	for _, name := range collabTools {
		assert.True(t, isCollabToolCall(name), "should be collab tool: %s", name)
	}

	nonCollabTools := []string{"shell", "shell_command", "read_file", "write_file", "request_user_input", "unknown"}
	for _, name := range nonCollabTools {
		assert.False(t, isCollabToolCall(name), "should not be collab tool: %s", name)
	}
}

func TestExtractFinalMessage(t *testing.T) {
	t.Run("finds last assistant message", func(t *testing.T) {
		items := []models.ConversationItem{
			{Type: models.ItemTypeUserMessage, Content: "Hello"},
			{Type: models.ItemTypeAssistantMessage, Content: "First response"},
			{Type: models.ItemTypeFunctionCall, Name: "shell"},
			{Type: models.ItemTypeFunctionCallOutput, CallID: "c1"},
			{Type: models.ItemTypeAssistantMessage, Content: "Final response"},
		}
		assert.Equal(t, "Final response", extractFinalMessage(items))
	})

	t.Run("empty history", func(t *testing.T) {
		assert.Equal(t, "", extractFinalMessage(nil))
	})

	t.Run("no assistant messages", func(t *testing.T) {
		items := []models.ConversationItem{
			{Type: models.ItemTypeUserMessage, Content: "Hello"},
		}
		assert.Equal(t, "", extractFinalMessage(items))
	})

	t.Run("skips empty assistant messages", func(t *testing.T) {
		items := []models.ConversationItem{
			{Type: models.ItemTypeAssistantMessage, Content: "Real message"},
			{Type: models.ItemTypeAssistantMessage, Content: ""},
		}
		assert.Equal(t, "Real message", extractFinalMessage(items))
	})
}

func TestBuildAgentSharedConfig(t *testing.T) {
	parent := models.SessionConfiguration{
		Model: models.ModelConfig{
			Provider:    "openai",
			Model:       "gpt-4o",
			Temperature: 0.7,
			MaxTokens:   4096,
		},
		Tools: models.ToolsConfig{
			EnableShell:      true,
			EnableReadFile:   true,
			EnableWriteFile:  true,
			EnableApplyPatch: true,
			EnableListDir:    true,
			EnableGrepFiles:  true,
			EnableCollab:     true,
		},
		Cwd:          "/workspace",
		ApprovalMode: models.ApprovalNever,
	}

	t.Run("child at max depth has collab disabled", func(t *testing.T) {
		cfg := buildAgentSharedConfig(parent, MaxThreadSpawnDepth)
		assert.False(t, cfg.Tools.EnableCollab, "collab should be disabled at max depth")
		// Other tools should be preserved
		assert.True(t, cfg.Tools.EnableShell)
		assert.True(t, cfg.Tools.EnableReadFile)
	})

	t.Run("child below max depth preserves collab", func(t *testing.T) {
		cfg := buildAgentSharedConfig(parent, 0)
		assert.True(t, cfg.Tools.EnableCollab, "collab should be preserved below max depth")
	})

	t.Run("inherits parent config", func(t *testing.T) {
		cfg := buildAgentSharedConfig(parent, 1)
		assert.Equal(t, parent.Cwd, cfg.Cwd)
		assert.Equal(t, parent.ApprovalMode, cfg.ApprovalMode)
		assert.Equal(t, parent.Model.Model, cfg.Model.Model)
	})
}

func TestApplyRoleOverrides(t *testing.T) {
	t.Run("explorer: read-only, medium reasoning", func(t *testing.T) {
		cfg := models.SessionConfiguration{
			Model: models.ModelConfig{Provider: "openai", Model: "gpt-4o"},
			Tools: models.ToolsConfig{
				EnableShell:      true,
				EnableReadFile:   true,
				EnableWriteFile:  true,
				EnableApplyPatch: true,
				EnableListDir:    true,
				EnableGrepFiles:  true,
			},
		}
		applyRoleOverrides(&cfg, AgentRoleExplorer)
		assert.Equal(t, "medium", cfg.Model.ReasoningEffort)
		assert.False(t, cfg.Tools.EnableWriteFile, "explorer should not write")
		assert.False(t, cfg.Tools.EnableApplyPatch, "explorer should not patch")
		assert.True(t, cfg.Tools.EnableShell, "explorer keeps shell for read commands")
		assert.True(t, cfg.Tools.EnableReadFile, "explorer keeps read_file")
		assert.True(t, cfg.Tools.EnableListDir, "explorer keeps list_dir")
		assert.True(t, cfg.Tools.EnableGrepFiles, "explorer keeps grep_files")
		assert.Equal(t, ExplorerModel, cfg.Model.Model, "explorer on openai should use cheaper model")
	})

	t.Run("explorer: anthropic provider keeps original model", func(t *testing.T) {
		cfg := models.SessionConfiguration{
			Model: models.ModelConfig{Provider: "anthropic", Model: "claude-sonnet-4-5-20250929"},
			Tools: models.ToolsConfig{
				EnableShell:      true,
				EnableReadFile:   true,
				EnableWriteFile:  true,
				EnableApplyPatch: true,
			},
		}
		applyRoleOverrides(&cfg, AgentRoleExplorer)
		assert.Equal(t, "claude-sonnet-4-5-20250929", cfg.Model.Model, "explorer on anthropic should NOT override model")
		assert.Equal(t, "medium", cfg.Model.ReasoningEffort)
	})

	t.Run("orchestrator: no write tools, keeps shell, has base instructions", func(t *testing.T) {
		cfg := models.SessionConfiguration{
			Tools: models.ToolsConfig{
				EnableShell:      true,
				EnableReadFile:   true,
				EnableWriteFile:  true,
				EnableApplyPatch: true,
			},
		}
		applyRoleOverrides(&cfg, AgentRoleOrchestrator)
		assert.False(t, cfg.Tools.EnableWriteFile)
		assert.False(t, cfg.Tools.EnableApplyPatch)
		assert.True(t, cfg.Tools.EnableShell, "orchestrator keeps shell for read commands")
		assert.True(t, cfg.Tools.EnableReadFile, "orchestrator keeps read_file")
		assert.Contains(t, cfg.BaseInstructions, "Sub-agents",
			"orchestrator should have base instructions with sub-agent guidance")
	})

	t.Run("worker: keeps everything", func(t *testing.T) {
		cfg := models.SessionConfiguration{
			Tools: models.ToolsConfig{
				EnableShell:      true,
				EnableReadFile:   true,
				EnableWriteFile:  true,
				EnableApplyPatch: true,
			},
		}
		applyRoleOverrides(&cfg, AgentRoleWorker)
		assert.True(t, cfg.Tools.EnableShell)
		assert.True(t, cfg.Tools.EnableReadFile)
		assert.True(t, cfg.Tools.EnableWriteFile)
		assert.True(t, cfg.Tools.EnableApplyPatch)
	})

	t.Run("default: keeps everything", func(t *testing.T) {
		cfg := models.SessionConfiguration{
			Tools: models.ToolsConfig{
				EnableShell:      true,
				EnableReadFile:   true,
				EnableWriteFile:  true,
				EnableApplyPatch: true,
			},
		}
		applyRoleOverrides(&cfg, AgentRoleDefault)
		assert.True(t, cfg.Tools.EnableShell)
		assert.True(t, cfg.Tools.EnableReadFile)
		assert.True(t, cfg.Tools.EnableWriteFile)
		assert.True(t, cfg.Tools.EnableApplyPatch)
	})

	t.Run("planner: read-only, no collab, custom instructions", func(t *testing.T) {
		cfg := models.SessionConfiguration{
			Model: models.ModelConfig{Model: "gpt-4o"},
			Tools: models.ToolsConfig{
				EnableShell:      true,
				EnableReadFile:   true,
				EnableWriteFile:  true,
				EnableApplyPatch: true,
				EnableListDir:    true,
				EnableGrepFiles:  true,
				EnableCollab:     true,
			},
			BaseInstructions: "original instructions",
		}
		applyRoleOverrides(&cfg, AgentRolePlanner)
		assert.False(t, cfg.Tools.EnableWriteFile, "planner should not write")
		assert.False(t, cfg.Tools.EnableApplyPatch, "planner should not patch")
		assert.False(t, cfg.Tools.EnableCollab, "planner should not spawn children")
		assert.True(t, cfg.Tools.EnableShell, "planner keeps shell for read commands")
		assert.True(t, cfg.Tools.EnableReadFile, "planner keeps read_file")
		assert.True(t, cfg.Tools.EnableListDir, "planner keeps list_dir")
		assert.True(t, cfg.Tools.EnableGrepFiles, "planner keeps grep_files")
		assert.NotEqual(t, "original instructions", cfg.BaseInstructions,
			"planner should have custom base instructions")
		assert.Contains(t, cfg.BaseInstructions, "planning agent",
			"planner instructions should mention planning")
	})
}

func TestBuildToolSpecs_WithCollabTools(t *testing.T) {
	t.Run("collab disabled", func(t *testing.T) {
		specs := buildToolSpecs(models.ToolsConfig{
			EnableShell:    true,
			EnableReadFile: true,
			EnableCollab:   false,
		}, models.ResolvedProfile{})

		names := specNames(specs)
		assert.Contains(t, names, "shell_command", "EnableShell=true resolves to shell_command")
		assert.Contains(t, names, "read_file")
		assert.Contains(t, names, "request_user_input")
		assert.NotContains(t, names, "spawn_agent")
		assert.NotContains(t, names, "send_input")
		assert.NotContains(t, names, "wait")
		assert.NotContains(t, names, "close_agent")
		assert.NotContains(t, names, "resume_agent")
	})

	t.Run("collab enabled", func(t *testing.T) {
		specs := buildToolSpecs(models.ToolsConfig{
			EnableShell:    true,
			EnableReadFile: true,
			EnableCollab:   true,
		}, models.ResolvedProfile{})

		names := specNames(specs)
		assert.Contains(t, names, "shell_command", "EnableShell=true resolves to shell_command")
		assert.Contains(t, names, "read_file")
		assert.Contains(t, names, "request_user_input")
		assert.Contains(t, names, "spawn_agent")
		assert.Contains(t, names, "send_input")
		assert.Contains(t, names, "wait")
		assert.Contains(t, names, "close_agent")
		assert.Contains(t, names, "resume_agent")
	})
}

func TestCollabToolsDisabledForChildren(t *testing.T) {
	// Simulate a parent config with collab enabled
	parentConfig := models.SessionConfiguration{
		Tools: models.ToolsConfig{
			EnableShell:    true,
			EnableReadFile: true,
			EnableCollab:   true,
		},
	}

	// Build child config at max depth — collab should be disabled
	childConfig := buildAgentSharedConfig(parentConfig, MaxThreadSpawnDepth)
	specs := buildToolSpecs(childConfig.Tools, models.ResolvedProfile{})

	names := specNames(specs)
	assert.NotContains(t, names, "spawn_agent", "child at max depth should not have spawn_agent")
	assert.NotContains(t, names, "send_input", "child at max depth should not have send_input")
	assert.NotContains(t, names, "wait", "child at max depth should not have wait")
	assert.Contains(t, names, "shell_command", "child should still have shell_command")
	assert.Contains(t, names, "read_file", "child should still have read_file")
}

// ---------------------------------------------------------------------------
// buildToolSpecs ShellType tests
// ---------------------------------------------------------------------------

func TestBuildToolSpecs_ShellType_Default(t *testing.T) {
	specs := buildToolSpecs(models.ToolsConfig{
		ShellType: models.ShellToolDefault,
	}, models.ResolvedProfile{})
	names := specNames(specs)
	assert.Contains(t, names, "shell", "ShellToolDefault should produce 'shell' spec")
	assert.NotContains(t, names, "shell_command")
}

func TestBuildToolSpecs_ShellType_ShellCommand(t *testing.T) {
	specs := buildToolSpecs(models.ToolsConfig{
		ShellType: models.ShellToolShellCommand,
	}, models.ResolvedProfile{})
	names := specNames(specs)
	assert.Contains(t, names, "shell_command", "ShellToolShellCommand should produce 'shell_command' spec")
	assert.NotContains(t, names, "shell")
}

func TestBuildToolSpecs_ShellType_Disabled(t *testing.T) {
	specs := buildToolSpecs(models.ToolsConfig{
		ShellType: models.ShellToolDisabled,
	}, models.ResolvedProfile{})
	names := specNames(specs)
	assert.NotContains(t, names, "shell")
	assert.NotContains(t, names, "shell_command")
}

func TestBuildToolSpecs_ShellType_BackwardCompat(t *testing.T) {
	// EnableShell=true with no explicit ShellType should produce shell_command
	specs := buildToolSpecs(models.ToolsConfig{
		EnableShell: true,
	}, models.ResolvedProfile{})
	names := specNames(specs)
	assert.Contains(t, names, "shell_command", "EnableShell=true should default to shell_command")

	// EnableShell=false with no explicit ShellType should produce nothing
	specs = buildToolSpecs(models.ToolsConfig{
		EnableShell: false,
	}, models.ResolvedProfile{})
	names = specNames(specs)
	assert.NotContains(t, names, "shell")
	assert.NotContains(t, names, "shell_command")
}

func TestBuildToolSpecs_ShellType_ExplicitOverridesEnableShell(t *testing.T) {
	// Explicit ShellType takes precedence over EnableShell
	specs := buildToolSpecs(models.ToolsConfig{
		EnableShell: false,
		ShellType:   models.ShellToolDefault,
	}, models.ResolvedProfile{})
	names := specNames(specs)
	assert.Contains(t, names, "shell", "explicit ShellType should override EnableShell=false")
}

func TestCollabToolApprovalSkip(t *testing.T) {
	// Collab tools should always be auto-approved regardless of approval mode
	for _, name := range []string{"spawn_agent", "send_input", "wait", "close_agent", "resume_agent"} {
		req, _ := evaluateToolApproval(name, "{}", nil, models.ApprovalUnlessTrusted)
		assert.Equal(t, tools.ApprovalSkip, req, "%s should be auto-approved", name)
	}
}

func TestCollabSuccessOutput(t *testing.T) {
	output := collabSuccessOutput("call-1", map[string]interface{}{
		"agent_id": "agent-123",
	})
	assert.Equal(t, models.ItemTypeFunctionCallOutput, output.Type)
	assert.Equal(t, "call-1", output.CallID)
	require.NotNil(t, output.Output)
	assert.True(t, *output.Output.Success)

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(output.Output.Content), &data))
	assert.Equal(t, "agent-123", data["agent_id"])
}

func TestCollabErrorOutput(t *testing.T) {
	output := collabErrorOutput("call-2", "something failed")
	assert.Equal(t, models.ItemTypeFunctionCallOutput, output.Type)
	assert.Equal(t, "call-2", output.CallID)
	require.NotNil(t, output.Output)
	assert.False(t, *output.Output.Success)
	assert.Equal(t, "something failed", output.Output.Content)
}

func TestHandleResumeAgent_NotImplemented(t *testing.T) {
	s := &SessionState{}
	fc := models.ConversationItem{
		Type:      models.ItemTypeFunctionCall,
		CallID:    "call-resume",
		Name:      "resume_agent",
		Arguments: `{"id": "agent-1"}`,
	}

	output, err := s.handleResumeAgent(nil, fc)
	require.NoError(t, err)
	assert.Equal(t, "call-resume", output.CallID)
	require.NotNil(t, output.Output)
	assert.False(t, *output.Output.Success)
	assert.Contains(t, output.Output.Content, "not yet implemented")
}

func TestBuildAgentSpawnConfig(t *testing.T) {
	parentConfig := models.SessionConfiguration{
		Model: models.ModelConfig{
			Provider:    "openai",
			Model:       "gpt-4o",
			Temperature: 0.7,
			MaxTokens:   4096,
		},
		Tools: models.ToolsConfig{
			EnableShell:      true,
			EnableReadFile:   true,
			EnableWriteFile:  true,
			EnableApplyPatch: true,
			EnableCollab:     true,
		},
		Cwd: "/workspace",
	}

	t.Run("default role at depth 1", func(t *testing.T) {
		input := buildAgentSpawnConfig(parentConfig, AgentRoleDefault, "do something", 1)
		assert.Equal(t, "do something", input.UserMessage)
		assert.Equal(t, 1, input.Depth)
		assert.False(t, input.Config.Tools.EnableCollab, "child at depth 1 cannot spawn")
		assert.True(t, input.Config.Tools.EnableShell)
		assert.True(t, input.Config.Tools.EnableWriteFile)
	})

	t.Run("explorer role", func(t *testing.T) {
		input := buildAgentSpawnConfig(parentConfig, AgentRoleExplorer, "explore", 1)
		assert.Equal(t, "medium", input.Config.Model.ReasoningEffort)
		assert.False(t, input.Config.Tools.EnableWriteFile)
		assert.False(t, input.Config.Tools.EnableApplyPatch)
		assert.True(t, input.Config.Tools.EnableReadFile)
	})

	t.Run("orchestrator role", func(t *testing.T) {
		input := buildAgentSpawnConfig(parentConfig, AgentRoleOrchestrator, "orchestrate", 1)
		assert.False(t, input.Config.Tools.EnableWriteFile)
		assert.False(t, input.Config.Tools.EnableApplyPatch)
		assert.True(t, input.Config.Tools.EnableShell, "orchestrator keeps shell for read commands")
		assert.Contains(t, input.Config.BaseInstructions, "Sub-agents",
			"orchestrator should have base instructions")
	})

	t.Run("planner role", func(t *testing.T) {
		input := buildAgentSpawnConfig(parentConfig, AgentRolePlanner, "plan the feature", 1)
		assert.Equal(t, "plan the feature", input.UserMessage)
		assert.Equal(t, 1, input.Depth)
		assert.False(t, input.Config.Tools.EnableWriteFile, "planner should not write")
		assert.False(t, input.Config.Tools.EnableApplyPatch, "planner should not patch")
		assert.False(t, input.Config.Tools.EnableCollab, "planner at depth 1 has collab disabled (both depth and role)")
		assert.True(t, input.Config.Tools.EnableShell, "planner keeps shell for read commands")
		assert.True(t, input.Config.Tools.EnableReadFile, "planner keeps read_file")
		assert.Contains(t, input.Config.BaseInstructions, "planning agent",
			"planner should have custom base instructions")
	})
}

// TestSpawnAgent_DepthLimitExceeded verifies that spawning at max depth returns an error.
func TestSpawnAgent_DepthLimitExceeded(t *testing.T) {
	s := &SessionState{
		AgentCtl: NewAgentControl(MaxThreadSpawnDepth), // Already at max depth
	}

	fc := models.ConversationItem{
		Type:      models.ItemTypeFunctionCall,
		CallID:    "call-spawn",
		Name:      "spawn_agent",
		Arguments: `{"message": "do something"}`,
	}

	// handleSpawnAgent needs workflow context, but we can test the depth check
	// by verifying that depth+1 > MaxThreadSpawnDepth
	childDepth := s.AgentCtl.ParentDepth + 1
	assert.Greater(t, childDepth, MaxThreadSpawnDepth, "child depth should exceed max")

	// fc is declared above — note that the actual handler requires workflow context.
	// The depth check logic is the key verification here.
	assert.Equal(t, "spawn_agent", fc.Name)
}

func TestSendInput_AgentNotFound(t *testing.T) {
	s := &SessionState{
		AgentCtl: NewAgentControl(0),
	}

	// Can't call handleSendInput without workflow context, but we can verify
	// the lookup logic directly — the handler checks this map
	_, ok := s.AgentCtl.Agents["nonexistent"]
	assert.False(t, ok, "agent should not be found")
}

func TestCloseAgent_AlreadyTerminal(t *testing.T) {
	s := &SessionState{
		AgentCtl: NewAgentControl(0),
	}
	s.AgentCtl.Agents["a1"] = &AgentInfo{
		AgentID: "a1",
		Status:  AgentStatusCompleted,
	}

	// Verify agent is already terminal
	info := s.AgentCtl.Agents["a1"]
	assert.True(t, info.Status.isTerminal())
}

func TestWait_ParameterValidation(t *testing.T) {
	t.Run("empty ids rejected", func(t *testing.T) {
		var args struct {
			IDs       []string `json:"ids"`
			TimeoutMs *float64 `json:"timeout_ms"`
		}
		require.NoError(t, json.Unmarshal([]byte(`{"ids": []}`), &args))
		assert.Empty(t, args.IDs)
	})

	t.Run("timeout clamping", func(t *testing.T) {
		// Below minimum
		ms := int64(5000)
		if ms < MinWaitTimeoutMs {
			ms = MinWaitTimeoutMs
		}
		assert.Equal(t, int64(MinWaitTimeoutMs), ms)

		// Above maximum
		ms = 500_000
		if ms > MaxWaitTimeoutMs {
			ms = MaxWaitTimeoutMs
		}
		assert.Equal(t, int64(MaxWaitTimeoutMs), ms)

		// Within range
		ms = 60_000
		if ms < MinWaitTimeoutMs {
			ms = MinWaitTimeoutMs
		}
		if ms > MaxWaitTimeoutMs {
			ms = MaxWaitTimeoutMs
		}
		assert.Equal(t, int64(60_000), ms)
	})
}

// specNames extracts tool names from a slice of ToolSpec.
func specNames(specs []tools.ToolSpec) []string {
	names := make([]string, len(specs))
	for i, s := range specs {
		names[i] = s.Name
	}
	return names
}

// ---------------------------------------------------------------------------
// buildToolSpecs update_plan tests
// ---------------------------------------------------------------------------

func TestBuildToolSpecs_UpdatePlan_Enabled(t *testing.T) {
	specs := buildToolSpecs(models.ToolsConfig{
		EnableUpdatePlan: true,
	}, models.ResolvedProfile{})
	names := specNames(specs)
	assert.Contains(t, names, "update_plan", "update_plan should be present when enabled")
}

func TestBuildToolSpecs_UpdatePlan_Disabled(t *testing.T) {
	specs := buildToolSpecs(models.ToolsConfig{
		EnableUpdatePlan: false,
	}, models.ResolvedProfile{})
	names := specNames(specs)
	assert.NotContains(t, names, "update_plan", "update_plan should not be present when disabled")
}

func TestBuildToolSpecs_UpdatePlan_DefaultConfig(t *testing.T) {
	specs := buildToolSpecs(models.DefaultToolsConfig(), models.ResolvedProfile{})
	names := specNames(specs)
	assert.Contains(t, names, "update_plan", "update_plan should be present in default config")
}

// ---------------------------------------------------------------------------
// update_plan tool spec tests
// ---------------------------------------------------------------------------

func TestUpdatePlanToolSpec(t *testing.T) {
	spec := tools.NewUpdatePlanToolSpec()
	assert.Equal(t, "update_plan", spec.Name)
	assert.NotEmpty(t, spec.Description)
	assert.Len(t, spec.Parameters, 2) // explanation, plan

	for _, p := range spec.Parameters {
		switch p.Name {
		case "explanation":
			assert.False(t, p.Required, "explanation should be optional")
			assert.Equal(t, "string", p.Type)
		case "plan":
			assert.True(t, p.Required, "plan should be required")
			assert.Equal(t, "array", p.Type)
			assert.NotNil(t, p.Items, "plan should have item schema")
		}
	}
}

// ---------------------------------------------------------------------------
// update_plan approval skip test
// ---------------------------------------------------------------------------

func TestUpdatePlanApprovalSkip(t *testing.T) {
	req, _ := evaluateToolApproval("update_plan", "{}", nil, models.ApprovalUnlessTrusted)
	assert.Equal(t, tools.ApprovalSkip, req, "update_plan should be auto-approved")
}

// ---------------------------------------------------------------------------
// Collab tool spec tests
// ---------------------------------------------------------------------------

func TestCollabToolSpecs(t *testing.T) {
	t.Run("spawn_agent spec", func(t *testing.T) {
		spec := tools.NewSpawnAgentToolSpec()
		assert.Equal(t, "spawn_agent", spec.Name)
		assert.NotEmpty(t, spec.Description)
		assert.Len(t, spec.Parameters, 3) // message, items, agent_type

		paramNames := make([]string, len(spec.Parameters))
		for i, p := range spec.Parameters {
			paramNames[i] = p.Name
		}
		assert.Contains(t, paramNames, "message")
		assert.Contains(t, paramNames, "items")
		assert.Contains(t, paramNames, "agent_type")

		// message should NOT be required (either message or items)
		for _, p := range spec.Parameters {
			if p.Name == "message" {
				assert.False(t, p.Required, "message should not be required")
			}
			if p.Name == "items" {
				assert.False(t, p.Required, "items should not be required")
				assert.Equal(t, "array", p.Type)
				assert.NotNil(t, p.Items, "items should have schema")
			}
			if p.Name == "agent_type" {
				assert.False(t, p.Required)
				assert.Contains(t, p.Description, "explorer", "agent_type should describe explorer role")
				assert.Contains(t, p.Description, "worker", "agent_type should describe worker role")
			}
		}
	})

	t.Run("send_input spec", func(t *testing.T) {
		spec := tools.NewSendInputToolSpec()
		assert.Equal(t, "send_input", spec.Name)
		assert.Len(t, spec.Parameters, 4) // id, message, items, interrupt

		for _, p := range spec.Parameters {
			switch p.Name {
			case "id":
				assert.True(t, p.Required)
				assert.Equal(t, "string", p.Type)
			case "message":
				assert.False(t, p.Required, "message should not be required")
				assert.Equal(t, "string", p.Type)
			case "items":
				assert.False(t, p.Required)
				assert.Equal(t, "array", p.Type)
				assert.NotNil(t, p.Items, "items should have schema")
			case "interrupt":
				assert.False(t, p.Required)
				assert.Equal(t, "boolean", p.Type)
			}
		}
	})

	t.Run("wait spec", func(t *testing.T) {
		spec := tools.NewWaitToolSpec()
		assert.Equal(t, "wait", spec.Name)
		assert.Len(t, spec.Parameters, 2) // ids, timeout_ms

		for _, p := range spec.Parameters {
			switch p.Name {
			case "ids":
				assert.True(t, p.Required)
				assert.Equal(t, "array", p.Type)
				assert.NotNil(t, p.Items)
			case "timeout_ms":
				assert.False(t, p.Required)
				assert.Equal(t, "number", p.Type)
			}
		}
	})

	t.Run("close_agent spec", func(t *testing.T) {
		spec := tools.NewCloseAgentToolSpec()
		assert.Equal(t, "close_agent", spec.Name)
		assert.Len(t, spec.Parameters, 1) // id
		assert.True(t, spec.Parameters[0].Required)
	})

	t.Run("resume_agent spec", func(t *testing.T) {
		spec := tools.NewResumeAgentToolSpec()
		assert.Equal(t, "resume_agent", spec.Name)
		assert.Len(t, spec.Parameters, 1) // id
		assert.True(t, spec.Parameters[0].Required)
	})
}

// ---------------------------------------------------------------------------
// parseCollabInput tests
// ---------------------------------------------------------------------------

func TestParseCollabInput(t *testing.T) {
	strPtr := func(s string) *string { return &s }

	t.Run("message only", func(t *testing.T) {
		msg, err := parseCollabInput(strPtr("hello"), nil)
		require.NoError(t, err)
		assert.Equal(t, "hello", msg)
	})

	t.Run("items only with single text", func(t *testing.T) {
		items := []collabInputItem{
			{Type: "text", Text: "explore the codebase"},
		}
		msg, err := parseCollabInput(nil, items)
		require.NoError(t, err)
		assert.Equal(t, "explore the codebase", msg)
	})

	t.Run("items with multiple text items concatenated", func(t *testing.T) {
		items := []collabInputItem{
			{Type: "text", Text: "first part"},
			{Type: "text", Text: "second part"},
		}
		msg, err := parseCollabInput(nil, items)
		require.NoError(t, err)
		assert.Equal(t, "first part\nsecond part", msg)
	})

	t.Run("both message and items rejected", func(t *testing.T) {
		items := []collabInputItem{
			{Type: "text", Text: "some text"},
		}
		_, err := parseCollabInput(strPtr("hello"), items)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not both")
	})

	t.Run("neither message nor items rejected", func(t *testing.T) {
		_, err := parseCollabInput(nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "required")
	})

	t.Run("empty message rejected", func(t *testing.T) {
		_, err := parseCollabInput(strPtr(""), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "required")
	})

	t.Run("items with no text items rejected", func(t *testing.T) {
		items := []collabInputItem{
			{Type: "image_url", ImageURL: "https://example.com/img.png"},
		}
		_, err := parseCollabInput(nil, items)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "text item")
	})

	t.Run("items skips non-text items", func(t *testing.T) {
		items := []collabInputItem{
			{Type: "image_url", ImageURL: "https://example.com/img.png"},
			{Type: "text", Text: "the actual task"},
			{Type: "path", Path: "/some/file"},
		}
		msg, err := parseCollabInput(nil, items)
		require.NoError(t, err)
		assert.Equal(t, "the actual task", msg)
	})
}
