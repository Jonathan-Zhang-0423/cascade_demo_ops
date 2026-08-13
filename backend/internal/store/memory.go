package store

import (
	"context"
	"errors"
	"sync"
	"time"

	"cascade-demoops/backend/internal/orchestrator"
)

var ErrStateNotFound = errors.New("state not found")

type StateStore interface {
	Save(ctx context.Context, state *orchestrator.CascadeState) error
	Load(ctx context.Context, projectID string) (*orchestrator.CascadeState, error)
	List(ctx context.Context) ([]*orchestrator.CascadeState, error)
	Archive(ctx context.Context, projectID string, archivedAt time.Time) error
	Delete(ctx context.Context, projectID string) error
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
		return nil, ErrStateNotFound
	}
	return state, nil
}

func (s *MemoryStateStore) List(ctx context.Context) ([]*orchestrator.CascadeState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	states := make([]*orchestrator.CascadeState, 0, len(s.states))
	for _, state := range s.states {
		states = append(states, state)
	}
	return states, nil
}

func (s *MemoryStateStore) Archive(ctx context.Context, projectID string, archivedAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.states[projectID]
	if !ok {
		return errors.New("state not found")
	}
	state.ArchivedAt = &archivedAt
	if state.ProjectContext != nil {
		state.ProjectContext.UpdatedAt = archivedAt
	}
	return nil
}

func (s *MemoryStateStore) Delete(ctx context.Context, projectID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.states[projectID]; !ok {
		return errors.New("state not found")
	}
	delete(s.states, projectID)
	return nil
}
