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

	"cascade-demoops/backend/internal/model"
)

type AssistantStore interface {
	Save(context.Context, *model.AssistantSession) error
	Load(context.Context, string) (*model.AssistantSession, error)
}

type FileAssistantStore struct {
	root string
	mu   sync.Mutex
}

func NewFileAssistantStore(root string) *FileAssistantStore {
	return &FileAssistantStore{root: filepath.Clean(root)}
}

func (s *FileAssistantStore) Save(ctx context.Context, session *model.AssistantSession) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.root == "" || s.root == "." {
		return errors.New("assistant state root is required")
	}
	if session == nil || session.ID == "" {
		return errors.New("assistant session ID is required")
	}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return err
	}
	path := s.path(session.ID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(path)
		return os.Rename(tmp, path)
	}
	return nil
}

func (s *FileAssistantStore) Load(ctx context.Context, sessionID string) (*model.AssistantSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if sessionID == "" {
		return nil, errors.New("assistant session ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path(sessionID))
	if err != nil {
		return nil, err
	}
	var session model.AssistantSession
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func (s *FileAssistantStore) path(sessionID string) string {
	digest := sha256.Sum256([]byte(sessionID))
	return filepath.Join(s.root, hex.EncodeToString(digest[:])+".json")
}

type MemoryAssistantStore struct {
	mu       sync.RWMutex
	sessions map[string]*model.AssistantSession
}

func NewMemoryAssistantStore() *MemoryAssistantStore {
	return &MemoryAssistantStore{sessions: map[string]*model.AssistantSession{}}
}

func (s *MemoryAssistantStore) Save(ctx context.Context, session *model.AssistantSession) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if session == nil || session.ID == "" {
		return errors.New("assistant session ID is required")
	}
	data, err := json.Marshal(session)
	if err != nil {
		return err
	}
	var copy model.AssistantSession
	if err := json.Unmarshal(data, &copy); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[session.ID] = &copy
	return nil
}

func (s *MemoryAssistantStore) Load(ctx context.Context, sessionID string) (*model.AssistantSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return nil, errors.New("assistant session not found")
	}
	data, err := json.Marshal(session)
	if err != nil {
		return nil, err
	}
	var copy model.AssistantSession
	if err := json.Unmarshal(data, &copy); err != nil {
		return nil, err
	}
	return &copy, nil
}
