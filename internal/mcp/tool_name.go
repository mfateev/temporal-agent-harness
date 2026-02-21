package mcp

import (
	"crypto/sha1"
	"fmt"
	"log"
)

// Tool naming constants matching Codex conventions.
// Maps to: codex-rs/core/src/mcp_connection_manager.rs constants
const (
	// McpToolNameDelimiter separates "mcp", server name, and tool name.
	McpToolNameDelimiter = "__"

	// McpToolNamePrefix is the prefix for all MCP tool names.
	McpToolNamePrefix = "mcp"

	// MaxToolNameLength is the maximum length for a qualified tool name.
	// OpenAI requires tool names to match ^[a-zA-Z0-9_-]+$ and be <= 64 chars.
	MaxToolNameLength = 64
)

// ToolInfo holds metadata about a single MCP tool, including the original
// server and tool names needed for dispatch.
//
// Maps to: codex-rs/core/src/mcp_connection_manager.rs ToolInfo
type ToolInfo struct {
	ServerName string
	ToolName   string
	// Tool holds the raw MCP tool definition (schema, description, annotations).
	Tool interface{}
}

// SanitizeName replaces characters not in [a-zA-Z0-9_-] with underscore.
// Returns "_" if the input is empty after sanitization.
//
// Maps to: codex-rs/core/src/mcp_connection_manager.rs sanitize_responses_api_tool_name
func SanitizeName(name string) string {
	sanitized := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			sanitized = append(sanitized, c)
		} else {
			sanitized = append(sanitized, '_')
		}
	}
	if len(sanitized) == 0 {
		return "_"
	}
	return string(sanitized)
}

// sha1Hex returns the hex-encoded SHA1 hash of s.
func sha1Hex(s string) string {
	h := sha1.New()
	h.Write([]byte(s))
	return fmt.Sprintf("%x", h.Sum(nil))
}

// QualifyToolName creates a qualified MCP tool name from server and tool names.
// Format: mcp__<sanitized_server>__<sanitized_tool>
// If the result exceeds MaxToolNameLength, it is truncated and a SHA1 suffix appended.
func QualifyToolName(serverName, toolName string) string {
	raw := McpToolNamePrefix + McpToolNameDelimiter + serverName + McpToolNameDelimiter + toolName
	qualified := SanitizeName(raw)

	if len(qualified) > MaxToolNameLength {
		hash := sha1Hex(raw)
		prefixLen := MaxToolNameLength - len(hash)
		qualified = qualified[:prefixLen] + hash
	}

	return qualified
}

// QualifyTools takes a list of ToolInfo items, qualifies their names, deduplicates,
// and returns a map from qualified name to ToolInfo.
//
// Behavior matches Codex:
//   - Raw qualified names (before sanitization) are deduplicated; duplicates are skipped with a warning.
//   - Sanitized names that collide after sanitization are also skipped.
//   - Long names are truncated with a SHA1 suffix.
//
// Maps to: codex-rs/core/src/mcp_connection_manager.rs qualify_tools
func QualifyTools(tools []ToolInfo) map[string]ToolInfo {
	usedNames := make(map[string]bool)
	seenRawNames := make(map[string]bool)
	qualifiedTools := make(map[string]ToolInfo)

	for _, tool := range tools {
		rawName := McpToolNamePrefix + McpToolNameDelimiter + tool.ServerName + McpToolNameDelimiter + tool.ToolName

		// Skip duplicates based on raw (unsanitized) name
		if seenRawNames[rawName] {
			log.Printf("mcp: skipping duplicated tool %s", rawName)
			continue
		}
		seenRawNames[rawName] = true

		// Sanitize for OpenAI API compatibility
		qualifiedName := SanitizeName(rawName)

		// Enforce length constraint
		if len(qualifiedName) > MaxToolNameLength {
			hash := sha1Hex(rawName)
			prefixLen := MaxToolNameLength - len(hash)
			qualifiedName = qualifiedName[:prefixLen] + hash
		}

		// Skip if sanitized name collides with an existing one
		if usedNames[qualifiedName] {
			log.Printf("mcp: skipping duplicated tool %s", qualifiedName)
			continue
		}

		usedNames[qualifiedName] = true
		qualifiedTools[qualifiedName] = tool
	}

	return qualifiedTools
}

// FilterTools filters a list of ToolInfo items using the given ToolFilter.
//
// Maps to: codex-rs/core/src/mcp_connection_manager.rs filter_tools
func FilterTools(tools []ToolInfo, filter ToolFilter) []ToolInfo {
	filtered := make([]ToolInfo, 0, len(tools))
	for _, tool := range tools {
		if filter.Allows(tool.ToolName) {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}
