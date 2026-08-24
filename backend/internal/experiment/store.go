package experiment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var ErrRevisionConflict = errors.New("experiment run revision conflict")

type Store interface {
	Create(context.Context, Run, Event) error
	Get(context.Context, string) (Run, error)
	FindByIdempotencyKey(context.Context, string) (Run, bool, error)
	ListRuns(context.Context) ([]Run, error)
	Transition(context.Context, string, int, Run, Event) error
	ListEvents(context.Context, string) ([]Event, error)
}

func (s *FileStore) ListRuns(ctx context.Context) ([]Run, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return []Run{}, nil
	}
	if err != nil {
		return nil, err
	}
	runs := make([]Run, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		value, _, readErr := s.readUnlocked(entry.Name())
		if readErr != nil {
			return nil, readErr
		}
		runs = append(runs, value.Run)
	}
	return runs, nil
}

type envelope struct {
	Run    Run     `json:"run"`
	Events []Event `json:"events"`
}

type FileStore struct {
	root string
	mu   sync.Mutex
}

func NewFileStore(root string) *FileStore {
	return &FileStore{root: filepath.Clean(root)}
}

func (s *FileStore) Create(ctx context.Context, run Run, event Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateRun(run); err != nil {
		return err
	}
	if event.RunID != run.RunID || strings.TrimSpace(event.EventID) == "" {
		return errors.New("initial experiment event must bind the run")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.runPath(run.RunID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return errors.New("experiment run already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	event.Sequence = 1
	if err := validateEventSize(event); err != nil {
		return err
	}
	return s.write(path, envelope{Run: run, Events: []Event{event}})
}

func (s *FileStore) Get(ctx context.Context, runID string) (Run, error) {
	if err := ctx.Err(); err != nil {
		return Run{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, _, err := s.readUnlocked(runID)
	return value.Run, err
}

func (s *FileStore) FindByIdempotencyKey(ctx context.Context, key string) (Run, bool, error) {
	if err := ctx.Err(); err != nil {
		return Run{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		value, _, readErr := s.readUnlocked(entry.Name())
		if readErr != nil {
			return Run{}, false, readErr
		}
		if value.Run.IdempotencyKey == key {
			return value.Run, true, nil
		}
	}
	return Run{}, false, nil
}

func (s *FileStore) Transition(ctx context.Context, runID string, expectedRevision int, next Run, event Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateRun(next); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, path, err := s.readUnlocked(runID)
	if err != nil {
		return err
	}
	if value.Run.Revision != expectedRevision {
		return fmt.Errorf("%w: expected %d current %d", ErrRevisionConflict, expectedRevision, value.Run.Revision)
	}
	if next.RunID != runID || next.Revision != expectedRevision+1 || event.RunID != runID || strings.TrimSpace(event.EventID) == "" {
		return errors.New("experiment transition must preserve identity, increment revision once, and include a bound event")
	}
	for _, existing := range value.Events {
		if existing.EventID == event.EventID {
			return nil
		}
	}
	event.Sequence = len(value.Events) + 1
	if err := validateEventSize(event); err != nil {
		return err
	}
	value.Run = next
	value.Events = append(value.Events, event)
	return s.write(path, value)
}

func (s *FileStore) ListEvents(ctx context.Context, runID string) ([]Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, _, err := s.readUnlocked(runID)
	if err != nil {
		return nil, err
	}
	return append([]Event{}, value.Events...), nil
}

func (s *FileStore) readUnlocked(runID string) (envelope, string, error) {
	path, err := s.runPath(runID)
	if err != nil {
		return envelope{}, "", err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return envelope{}, path, err
	}
	var value envelope
	if err := json.Unmarshal(payload, &value); err != nil {
		return envelope{}, path, err
	}
	return value, path, nil
}

func (s *FileStore) write(path string, value envelope) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(payload, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(path)
		if retryErr := os.Rename(temporary, path); retryErr != nil {
			_ = os.Remove(temporary)
			return retryErr
		}
	}
	return nil
}

func (s *FileStore) runPath(runID string) (string, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" || strings.ContainsAny(runID, `/\\`) || strings.Contains(runID, "..") || strings.TrimSpace(s.root) == "" || s.root == "." {
		return "", errors.New("invalid experiment run id or store root")
	}
	return filepath.Join(s.root, runID, "run.json"), nil
}

func validateEventSize(event Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if len(payload) > MaxEventBodyBytes {
		return errors.New("experiment event exceeds 512 KiB; store large evidence as an artifact reference")
	}
	return nil
}
