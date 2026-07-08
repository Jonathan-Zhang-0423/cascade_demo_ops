package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"cascade-demoops/backend/internal/orchestrator"
)

type FileStateStore struct {
	root string
	mu   sync.Mutex
}

func NewFileStateStore(root string) *FileStateStore {
	return &FileStateStore{root: filepath.Clean(root)}
}

func (s *FileStateStore) Save(ctx context.Context, state *orchestrator.CascadeState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.root == "" || s.root == "." {
		return errors.New("state root is required")
	}
	if state == nil || state.ProjectID == "" {
		return errors.New("state with project ID is required")
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return err
	}
	path := s.statePath(state.ProjectID)
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(path)
		if retryErr := os.Rename(tmpPath, path); retryErr != nil {
			return retryErr
		}
	}
	return nil
}

func (s *FileStateStore) Load(ctx context.Context, projectID string) (*orchestrator.CascadeState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.root == "" || s.root == "." {
		return nil, errors.New("state root is required")
	}
	if projectID == "" {
		return nil, errors.New("project ID is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.statePath(projectID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("state not found")
		}
		return nil, err
	}
	var state orchestrator.CascadeState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (s *FileStateStore) statePath(projectID string) string {
	sum := sha256.Sum256([]byte(projectID))
	return filepath.Join(s.root, hex.EncodeToString(sum[:])+".json")
}
