package finalfilm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"cascade-demoops/backend/internal/model"
)

var ErrRevisionConflict = errors.New("final film job revision conflict")

type Store interface {
	CreateJob(context.Context, model.FinalFilmJob) error
	CreateJobWithEvent(context.Context, model.FinalFilmJob, model.FinalFilmEvent) error
	GetJob(context.Context, string) (model.FinalFilmJob, error)
	CompareAndSwapJob(context.Context, string, int, model.FinalFilmJob) error
	TransitionJob(context.Context, string, int, model.FinalFilmJob, model.FinalFilmEvent) error
	AppendEvent(context.Context, model.FinalFilmEvent) error
	ListEvents(context.Context, string) ([]model.FinalFilmEvent, error)
	ListJobs(context.Context) ([]model.FinalFilmJob, error)
}

type fileEnvelope struct {
	Job    model.FinalFilmJob     `json:"job"`
	Events []model.FinalFilmEvent `json:"events"`
}

type FileStore struct {
	root string
	mu   sync.Mutex
}

func NewFileStore(root string) *FileStore {
	return &FileStore{root: filepath.Clean(root)}
}

func (s *FileStore) CreateJob(ctx context.Context, job model.FinalFilmJob) error {
	return s.createJob(ctx, job, nil)
}

func (s *FileStore) CreateJobWithEvent(ctx context.Context, job model.FinalFilmJob, event model.FinalFilmEvent) error {
	return s.createJob(ctx, job, &event)
}

func (s *FileStore) createJob(ctx context.Context, job model.FinalFilmJob, event *model.FinalFilmEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.jobPath(job.JobID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return errors.New("final film job already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	events := []model.FinalFilmEvent{}
	if event != nil {
		if event.JobID != job.JobID {
			return errors.New("initial final film event must bind the job")
		}
		event.Sequence = 1
		events = append(events, *event)
	}
	return s.write(path, fileEnvelope{Job: job, Events: events})
}

func (s *FileStore) TransitionJob(ctx context.Context, jobID string, expectedRevision int, next model.FinalFilmJob, event model.FinalFilmEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	envelope, path, err := s.readUnlocked(jobID)
	if err != nil {
		return err
	}
	if envelope.Job.Revision != expectedRevision {
		return fmt.Errorf("%w: expected %d current %d", ErrRevisionConflict, expectedRevision, envelope.Job.Revision)
	}
	if next.JobID != jobID || next.Revision != expectedRevision+1 || event.JobID != jobID || strings.TrimSpace(event.EventID) == "" {
		return errors.New("final film transition must preserve identity, increment revision once, and include a bound event")
	}
	for _, existing := range envelope.Events {
		if existing.EventID == event.EventID {
			return nil
		}
	}
	event.Sequence = len(envelope.Events) + 1
	envelope.Job = next
	envelope.Events = append(envelope.Events, event)
	return s.write(path, envelope)
}

func (s *FileStore) GetJob(ctx context.Context, jobID string) (model.FinalFilmJob, error) {
	if err := ctx.Err(); err != nil {
		return model.FinalFilmJob{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	envelope, _, err := s.readUnlocked(jobID)
	return envelope.Job, err
}

func (s *FileStore) CompareAndSwapJob(ctx context.Context, jobID string, expectedRevision int, next model.FinalFilmJob) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	envelope, path, err := s.readUnlocked(jobID)
	if err != nil {
		return err
	}
	if envelope.Job.Revision != expectedRevision {
		return fmt.Errorf("%w: expected %d current %d", ErrRevisionConflict, expectedRevision, envelope.Job.Revision)
	}
	if next.JobID != jobID || next.Revision != expectedRevision+1 {
		return errors.New("next final film job must preserve identity and increment revision once")
	}
	envelope.Job = next
	return s.write(path, envelope)
}

func (s *FileStore) AppendEvent(ctx context.Context, event model.FinalFilmEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	envelope, path, err := s.readUnlocked(event.JobID)
	if err != nil {
		return err
	}
	for _, existing := range envelope.Events {
		if existing.EventID == event.EventID {
			return nil
		}
	}
	if event.Sequence != len(envelope.Events)+1 {
		return errors.New("final film event sequence is not contiguous")
	}
	envelope.Events = append(envelope.Events, event)
	return s.write(path, envelope)
}

func (s *FileStore) ListEvents(ctx context.Context, jobID string) ([]model.FinalFilmEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	envelope, _, err := s.readUnlocked(jobID)
	if err != nil {
		return nil, err
	}
	return append([]model.FinalFilmEvent{}, envelope.Events...), nil
}

func (s *FileStore) ListJobs(ctx context.Context) ([]model.FinalFilmJob, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return []model.FinalFilmJob{}, nil
	}
	if err != nil {
		return nil, err
	}
	jobs := []model.FinalFilmJob{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path, pathErr := s.jobPath(entry.Name())
		if pathErr != nil {
			continue
		}
		payload, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		var envelope fileEnvelope
		if jsonErr := json.Unmarshal(payload, &envelope); jsonErr != nil {
			return nil, jsonErr
		}
		jobs = append(jobs, envelope.Job)
	}
	return jobs, nil
}

func (s *FileStore) readUnlocked(jobID string) (fileEnvelope, string, error) {
	path, err := s.jobPath(jobID)
	if err != nil {
		return fileEnvelope{}, "", err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return fileEnvelope{}, path, err
	}
	var envelope fileEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return fileEnvelope{}, path, err
	}
	return envelope, path, nil
}

func (s *FileStore) write(path string, envelope fileEnvelope) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(envelope, "", "  ")
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

func (s *FileStore) jobPath(jobID string) (string, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" || strings.ContainsAny(jobID, `/\\`) || strings.Contains(jobID, "..") {
		return "", errors.New("invalid final film job id")
	}
	if strings.TrimSpace(s.root) == "" || s.root == "." {
		return "", errors.New("final film store root is required")
	}
	return filepath.Join(s.root, jobID, "job.json"), nil
}
