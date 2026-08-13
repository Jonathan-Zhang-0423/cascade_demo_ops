package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"cascade-demoops/backend/internal/model"
)

type AccountStore interface {
	Load(context.Context) (*model.AccountState, error)
	Save(context.Context, *model.AccountState) error
}

type FileAccountStore struct {
	path string
	mu   sync.Mutex
}

func NewFileAccountStore(path string) *FileAccountStore {
	return &FileAccountStore{path: filepath.Clean(path)}
}

func (s *FileAccountStore) Load(ctx context.Context) (*model.AccountState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.path == "" || s.path == "." {
		return nil, errors.New("account state path is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	var state model.AccountState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return cloneAccountState(&state)
}

func (s *FileAccountStore) Save(ctx context.Context, state *model.AccountState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.path == "" || s.path == "." {
		return errors.New("account state path is required")
	}
	if state == nil || state.Profile.ID == "" {
		return errors.New("account profile is required")
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(s.path)
		return os.Rename(tmp, s.path)
	}
	return nil
}

type MemoryAccountStore struct {
	mu    sync.RWMutex
	state *model.AccountState
}

func NewMemoryAccountStore() *MemoryAccountStore {
	return &MemoryAccountStore{}
}

func (s *MemoryAccountStore) Load(ctx context.Context) (*model.AccountState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.state == nil {
		return nil, os.ErrNotExist
	}
	return cloneAccountState(s.state)
}

func (s *MemoryAccountStore) Save(ctx context.Context, state *model.AccountState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	copy, err := cloneAccountState(state)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.state = copy
	s.mu.Unlock()
	return nil
}

func cloneAccountState(state *model.AccountState) (*model.AccountState, error) {
	if state == nil {
		return nil, errors.New("account state is required")
	}
	data, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	var copy model.AccountState
	if err := json.Unmarshal(data, &copy); err != nil {
		return nil, err
	}
	return &copy, nil
}
