package execpolicy

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mfateev/codex-temporal-go/internal/command_safety"
	"github.com/mfateev/codex-temporal-go/internal/tools"
)

// ExecPolicyManager loads and evaluates exec policy rules.
//
// Maps to: codex-rs/execpolicy combined policy manager
type ExecPolicyManager struct {
	policy *Policy
	mu     sync.RWMutex
}

// NewExecPolicyManager creates a manager with a pre-built policy.
func NewExecPolicyManager(policy *Policy) *ExecPolicyManager {
	return &ExecPolicyManager{policy: policy}
}

// LoadExecPolicy reads all *.rules files from {codexHome}/rules/ and parses them
// into an ExecPolicyManager.
//
// Maps to: codex-rs/execpolicy loader
func LoadExecPolicy(codexHome string) (*ExecPolicyManager, error) {
	rulesDir := filepath.Join(codexHome, "rules")

	entries, err := os.ReadDir(rulesDir)
	if err != nil {
		if os.IsNotExist(err) {
			// No rules directory — return empty policy
			return NewExecPolicyManager(NewPolicy()), nil
		}
		return nil, err
	}

	merged := NewPolicy()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".rules") {
			continue
		}
		path := filepath.Join(rulesDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		p, err := ParsePolicy(path, string(data))
		if err != nil {
			return nil, err
		}
		merged.Merge(p)
	}

	return NewExecPolicyManager(merged), nil
}

// LoadExecPolicyFromSource parses a raw rules source string into a manager.
// Used when rules are transported via Temporal activity (serialized as text).
func LoadExecPolicyFromSource(source string) (*ExecPolicyManager, error) {
	if source == "" {
		return NewExecPolicyManager(NewPolicy()), nil
	}

	p, err := ParsePolicy("inline-rules", source)
	if err != nil {
		return nil, err
	}
	return NewExecPolicyManager(p), nil
}

// EvaluateCommand evaluates a shell command against the policy.
//
// The approvalMode determines the heuristic fallback when no rules match:
//   - "unless-trusted": IsKnownSafeCommand → Allow, else Prompt
//   - "never":          Allow (auto-approve everything)
//   - "on-failure":     Allow (runs in sandbox, escalate on failure)
//
// Maps to: codex-rs/execpolicy/src/lib.rs Policy::check + heuristic
func (m *ExecPolicyManager) EvaluateCommand(cmd []string, approvalMode string) tools.ExecApprovalRequirement {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Parse bash -lc "..." into individual commands
	subCommands := command_safety.ParseShellLcPlainCommands(cmd)
	if subCommands == nil {
		// Can't parse — treat the whole command as a single unit
		subCommands = [][]string{cmd}
	}

	// Build heuristic fallback based on approval mode
	fallback := m.heuristicFallback(approvalMode)

	eval := m.policy.CheckMultiple(subCommands, fallback)
	return decisionToApprovalRequirement(eval.Decision)
}

// EvaluateShellCommand is a convenience method that wraps a shell command
// string as ["bash", "-c", command] before evaluating.
func (m *ExecPolicyManager) EvaluateShellCommand(command, approvalMode string) tools.ExecApprovalRequirement {
	return m.EvaluateCommand([]string{"bash", "-c", command}, approvalMode)
}

// GetEvaluation returns the full evaluation (including justification) for a command.
func (m *ExecPolicyManager) GetEvaluation(cmd []string, approvalMode string) Evaluation {
	m.mu.RLock()
	defer m.mu.RUnlock()

	subCommands := command_safety.ParseShellLcPlainCommands(cmd)
	if subCommands == nil {
		subCommands = [][]string{cmd}
	}

	fallback := m.heuristicFallback(approvalMode)
	return m.policy.CheckMultiple(subCommands, fallback)
}

// AppendAndReload appends a prefix rule to the rules file and reloads the policy.
func (m *ExecPolicyManager) AppendAndReload(codexHome string, prefix []string) error {
	rulesFile := filepath.Join(codexHome, "rules", "default.rules")
	if err := AppendAllowPrefixRule(rulesFile, prefix); err != nil {
		return err
	}

	// Reload
	newManager, err := LoadExecPolicy(codexHome)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.policy = newManager.policy
	return nil
}

// heuristicFallback returns the fallback function for the given approval mode.
func (m *ExecPolicyManager) heuristicFallback(approvalMode string) func([]string) Decision {
	switch approvalMode {
	case "never", "":
		return func(cmd []string) Decision {
			return DecisionAllow
		}
	case "on-failure":
		return func(cmd []string) Decision {
			return DecisionAllow
		}
	case "unless-trusted":
		return func(cmd []string) Decision {
			if command_safety.IsKnownSafeCommand(cmd) {
				return DecisionAllow
			}
			return DecisionPrompt
		}
	default:
		// Unknown mode — default to prompt
		return func(cmd []string) Decision {
			return DecisionPrompt
		}
	}
}

// decisionToApprovalRequirement maps a Decision to ExecApprovalRequirement.
func decisionToApprovalRequirement(d Decision) tools.ExecApprovalRequirement {
	switch d {
	case DecisionAllow:
		return tools.ApprovalSkip
	case DecisionPrompt:
		return tools.ApprovalNeeded
	case DecisionForbidden:
		return tools.ApprovalForbidden
	default:
		return tools.ApprovalNeeded
	}
}
