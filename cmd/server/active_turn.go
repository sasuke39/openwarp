package main

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sasuke39/open-warp/internal/agentruntime"
)

type agentTurnState string

const (
	agentTurnRunning      agentTurnState = "RUNNING"
	agentTurnAwaitingTool agentTurnState = "AWAITING_TOOL"
	agentTurnCancelling   agentTurnState = "CANCELLING"
)

// activeTurn is the Adapter-owned lifecycle identity. WarpTaskID is only the
// UI bridge key; ConversationID and TurnID are the canonical Agent identities.
type activeTurn struct {
	mu             sync.RWMutex
	TurnID         string
	ConversationID string
	WarpTaskID     string
	Driver         agentruntime.Driver
	State          agentTurnState
	CreatedAt      time.Time
}

type activeTurnSnapshot struct {
	TurnID         string
	ConversationID string
	WarpTaskID     string
	Driver         agentruntime.Driver
	State          agentTurnState
	CreatedAt      time.Time
}

func (turn *activeTurn) snapshot() activeTurnSnapshot {
	turn.mu.RLock()
	defer turn.mu.RUnlock()
	return activeTurnSnapshot{
		TurnID:         turn.TurnID,
		ConversationID: turn.ConversationID,
		WarpTaskID:     turn.WarpTaskID,
		Driver:         turn.Driver,
		State:          turn.State,
		CreatedAt:      turn.CreatedAt,
	}
}

func (turn *activeTurn) setState(state agentTurnState) {
	turn.mu.Lock()
	turn.State = state
	turn.mu.Unlock()
}

type activeTurnRegistry struct {
	mu             sync.RWMutex
	byWarpTask     map[string]*activeTurn
	byConversation map[string]*activeTurn
}

func (registry *activeTurnRegistry) begin(warpTaskID, conversationID string, driver agentruntime.Driver) *activeTurn {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if turn := registry.byWarpTask[warpTaskID]; turn != nil {
		turn.mu.Lock()
		turn.Driver = driver
		turn.mu.Unlock()
		return turn
	}
	if registry.byWarpTask == nil {
		registry.byWarpTask = make(map[string]*activeTurn)
		registry.byConversation = make(map[string]*activeTurn)
	}
	turn := &activeTurn{
		TurnID:         uuid.NewString(),
		ConversationID: conversationID,
		WarpTaskID:     warpTaskID,
		Driver:         driver,
		State:          agentTurnRunning,
		CreatedAt:      time.Now().UTC(),
	}
	registry.byWarpTask[warpTaskID] = turn
	registry.byConversation[conversationID] = turn
	return turn
}

func (registry *activeTurnRegistry) loadByWarpTask(warpTaskID string) (*activeTurn, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	turn := registry.byWarpTask[warpTaskID]
	return turn, turn != nil
}

func (registry *activeTurnRegistry) finish(warpTaskID string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	turn := registry.byWarpTask[warpTaskID]
	if turn == nil {
		return
	}
	delete(registry.byWarpTask, warpTaskID)
	turn.mu.RLock()
	conversationID := turn.ConversationID
	turn.mu.RUnlock()
	if registry.byConversation[conversationID] == turn {
		delete(registry.byConversation, conversationID)
	}
}
