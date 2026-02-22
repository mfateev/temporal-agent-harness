package mcp

import (
	"log"
	"sync"
)

// McpStore is a worker-scoped store of per-session MCP connection managers.
// Created once at worker startup, shared across activities.
//
// Follows the same pattern as execsession.Store.
//
// Maps to: worker-scoped MCP state (no direct Codex equivalent — Codex
// keeps MCP state in the Session which is already long-lived)
type McpStore struct {
	mu       sync.Mutex
	sessions map[string]*McpConnectionManager
}

// NewMcpStore creates a new empty store.
func NewMcpStore() *McpStore {
	return &McpStore{
		sessions: make(map[string]*McpConnectionManager),
	}
}

// GetOrCreate returns an existing manager for the session, or creates a new one.
func (s *McpStore) GetOrCreate(sessionID string) *McpConnectionManager {
	s.mu.Lock()
	defer s.mu.Unlock()

	if mgr, ok := s.sessions[sessionID]; ok {
		return mgr
	}
	mgr := NewMcpConnectionManager()
	s.sessions[sessionID] = mgr
	return mgr
}

// Get returns the manager for a session, or nil if not found.
func (s *McpStore) Get(sessionID string) *McpConnectionManager {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[sessionID]
}

// Remove closes and removes the manager for a session.
func (s *McpStore) Remove(sessionID string) {
	s.mu.Lock()
	mgr, ok := s.sessions[sessionID]
	if ok {
		delete(s.sessions, sessionID)
	}
	s.mu.Unlock()

	if ok {
		mgr.Close()
		log.Printf("mcp: cleaned up session %s", sessionID)
	}
}

// Count returns the number of active sessions.
func (s *McpStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}
