package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

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
			return nil, ErrStateNotFound
		}
		return nil, err
	}
	var state orchestrator.CascadeState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (s *FileStateStore) List(ctx context.Context) ([]*orchestrator.CascadeState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.root == "" || s.root == "." {
		return nil, errors.New("state root is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return []*orchestrator.CascadeState{}, nil
	}
	if err != nil {
		return nil, err
	}
	states := make([]*orchestrator.CascadeState, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(s.root, entry.Name()))
		if readErr != nil {
			return nil, readErr
		}
		var state orchestrator.CascadeState
		if err := json.Unmarshal(data, &state); err != nil || state.ProjectID == "" {
			log.Printf("state_store skipping malformed state file=%s", entry.Name())
			continue
		}
		states = append(states, &state)
	}
	return states, nil
}

func (s *FileStateStore) Archive(ctx context.Context, projectID string, archivedAt time.Time) error {
	state, err := s.Load(ctx, projectID)
	if err != nil {
		return err
	}
	state.ArchivedAt = &archivedAt
	if state.ProjectContext != nil {
		state.ProjectContext.UpdatedAt = archivedAt
	}
	return s.Save(ctx, state)
}

func (s *FileStateStore) Delete(ctx context.Context, projectID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.root == "" || s.root == "." || projectID == "" {
		return errors.New("state root and project ID are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.statePath(projectID)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("state not found")
		}
		return err
	}
	return nil
}

func (s *FileStateStore) statePath(projectID string) string {
	sum := sha256.Sum256([]byte(projectID))
	return filepath.Join(s.root, hex.EncodeToString(sum[:])+".json")
}
