package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

const exchangeStateFileName = "exchange_state.json"

type exchangeSnapshotStore interface {
	Save(ctx context.Context, snapshot exchangeSnapshot) error
	Load(ctx context.Context) (exchangeSnapshot, error)
}

type fileExchangeSnapshotStore struct {
	path string
	mu   sync.Mutex
}

type exchangeSnapshot struct {
	Uploads         map[string]exchangeUploadSession `json:"uploads,omitempty"`
	Packages        map[string]*exchangePackageState `json:"packages,omitempty"`
	PackageByIdem   map[string]string                `json:"package_by_idem,omitempty"`
	ResultByID      map[string]*recordingResultState `json:"result_by_id,omitempty"`
	ResultByPackage map[string]string                `json:"result_by_package,omitempty"`
	SeenNonces      map[string]bool                  `json:"seen_nonces,omitempty"`
}

func newFileExchangeSnapshotStore(root string) *fileExchangeSnapshotStore {
	return &fileExchangeSnapshotStore{path: filepath.Join(filepath.Clean(root), exchangeStateFileName)}
}

func (s *fileExchangeSnapshotStore) Save(ctx context.Context, snapshot exchangeSnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.path == "" {
		return errors.New("exchange snapshot path is required")
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		_ = os.Remove(s.path)
		if retryErr := os.Rename(tmpPath, s.path); retryErr != nil {
			return retryErr
		}
	}
	return nil
}

func (s *fileExchangeSnapshotStore) Load(ctx context.Context) (exchangeSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return exchangeSnapshot{}, err
	}
	if s == nil || s.path == "" {
		return exchangeSnapshot{}, errors.New("exchange snapshot path is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return exchangeSnapshot{}, nil
		}
		return exchangeSnapshot{}, err
	}
	var snapshot exchangeSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return exchangeSnapshot{}, err
	}
	return snapshot, nil
}
