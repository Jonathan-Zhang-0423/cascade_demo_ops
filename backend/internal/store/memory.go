package store

import (
	"context"
	"errors"
	"sync"

	"cascade-demoops/backend/internal/orchestrator"
)

type StateStore interface {
	Save(ctx context.Context, state *orchestrator.CascadeState) error
	Load(ctx context.Context, projectID string) (*orchestrator.CascadeState, error)
}

type MemoryStateStore struct {
	mu     sync.RWMutex
	states map[string]*orchestrator.CascadeState
}

func NewMemoryStateStore() *MemoryStateStore {
	return &MemoryStateStore{states: map[string]*orchestrator.CascadeState{}}
}

func (s *MemoryStateStore) Save(ctx context.Context, state *orchestrator.CascadeState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if state == nil || state.ProjectID == "" {
		return errors.New("state with project ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[state.ProjectID] = state
	return nil
}

func (s *MemoryStateStore) Load(ctx context.Context, projectID string) (*orchestrator.CascadeState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.states[projectID]
	if !ok {
		return nil, errors.New("state not found")
	}
	return state, nil
}
