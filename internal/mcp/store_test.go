package mcp

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMcpStore_GetOrCreate(t *testing.T) {
	store := NewMcpStore()

	mgr1 := store.GetOrCreate("session-1")
	require.NotNil(t, mgr1)

	// Same session returns same manager
	mgr2 := store.GetOrCreate("session-1")
	assert.Equal(t, mgr1, mgr2)

	// Different session returns different manager (pointer comparison)
	mgr3 := store.GetOrCreate("session-2")
	assert.True(t, mgr1 != mgr3, "different sessions should have different managers")

	assert.Equal(t, 2, store.Count())
}

func TestMcpStore_Get(t *testing.T) {
	store := NewMcpStore()

	// Non-existent session returns nil
	assert.Nil(t, store.Get("nonexistent"))

	// After creating, Get returns the manager
	mgr := store.GetOrCreate("session-1")
	assert.Equal(t, mgr, store.Get("session-1"))
}

func TestMcpStore_Remove(t *testing.T) {
	store := NewMcpStore()

	store.GetOrCreate("session-1")
	store.GetOrCreate("session-2")
	assert.Equal(t, 2, store.Count())

	store.Remove("session-1")
	assert.Equal(t, 1, store.Count())
	assert.Nil(t, store.Get("session-1"))
	assert.NotNil(t, store.Get("session-2"))

	// Remove non-existent is safe
	store.Remove("nonexistent")
	assert.Equal(t, 1, store.Count())
}

func TestMcpStore_ThreadSafety(t *testing.T) {
	store := NewMcpStore()
	var wg sync.WaitGroup

	// Concurrent GetOrCreate
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sessionID := "session"
			if id%2 == 0 {
				sessionID = "session-even"
			} else {
				sessionID = "session-odd"
			}
			mgr := store.GetOrCreate(sessionID)
			assert.NotNil(t, mgr)
		}(i)
	}
	wg.Wait()

	assert.Equal(t, 2, store.Count())

	// Concurrent Get + Remove
	wg = sync.WaitGroup{}
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if id%3 == 0 {
				store.Remove("session-even")
			} else {
				store.Get("session-odd")
			}
		}(i)
	}
	wg.Wait()
}
