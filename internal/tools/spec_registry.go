// spec_registry.go provides a dynamic registry for tool specifications.
//
// Each tool registers a SpecEntry via init(). The registry is keyed by
// internal name; the LLM-facing name may differ (see SpecEntry.LLMName).
// Groups allow multiple tools to be referenced by a single name
// (e.g. "collab" expands to spawn_agent, send_input, wait, …).
package tools

import "sync"

// SpecEntry is the registry unit for a single tool.
type SpecEntry struct {
	Name        string         // Internal name: "shell_command", "patch_gpt"
	LLMName     string         // LLM-facing name (defaults to Name if empty)
	Constructor func() ToolSpec // Returns the spec (spec.Name == LLM name)
	Group       string         // Optional group: "collab"
}

// resolvedLLMName returns LLMName if set, otherwise Name.
func (e SpecEntry) resolvedLLMName() string {
	if e.LLMName != "" {
		return e.LLMName
	}
	return e.Name
}

var (
	mu           sync.RWMutex
	specRegistry = map[string]SpecEntry{}
	toolGroups   = map[string][]string{}
)

// RegisterSpec adds a SpecEntry to the global registry.
// If the entry belongs to a group, it is also added to that group.
func RegisterSpec(entry SpecEntry) {
	mu.Lock()
	defer mu.Unlock()
	specRegistry[entry.Name] = entry
	if entry.Group != "" {
		toolGroups[entry.Group] = append(toolGroups[entry.Group], entry.Name)
	}
}

// GetEntry returns the SpecEntry for the given internal name.
func GetEntry(internalName string) (SpecEntry, bool) {
	mu.RLock()
	defer mu.RUnlock()
	e, ok := specRegistry[internalName]
	return e, ok
}

// BuildSpecs constructs ToolSpec values for the given internal names.
// Group names (e.g. "collab") are expanded first. Unknown names are skipped.
func BuildSpecs(internalNames []string) []ToolSpec {
	expanded := ExpandGroups(internalNames)

	mu.RLock()
	defer mu.RUnlock()

	specs := make([]ToolSpec, 0, len(expanded))
	for _, name := range expanded {
		entry, ok := specRegistry[name]
		if !ok {
			continue // unknown tool — skip gracefully
		}
		specs = append(specs, entry.Constructor())
	}
	return specs
}

// ExpandGroups replaces group names with their member tool names.
// Non-group names pass through unchanged. Duplicates are preserved
// (callers should deduplicate if needed).
func ExpandGroups(names []string) []string {
	mu.RLock()
	defer mu.RUnlock()

	var out []string
	for _, name := range names {
		if members, ok := toolGroups[name]; ok {
			out = append(out, members...)
		} else {
			out = append(out, name)
		}
	}
	return out
}

// DefaultEnabledTools returns the internal tool names enabled by default.
func DefaultEnabledTools() []string {
	return []string{
		"shell_command",
		"read_file",
		"write_file",
		"list_dir",
		"grep_files",
		"apply_patch",
		"request_user_input",
		"update_plan",
	}
}
