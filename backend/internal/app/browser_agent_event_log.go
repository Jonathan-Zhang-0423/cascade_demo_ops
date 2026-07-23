package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"cascade-demoops/backend/internal/model"
)

var timeNowUTC = func() time.Time { return time.Now().UTC() }

type StageExecutionEventSink interface {
	Append(context.Context, model.StageExecutionEvent) error
}

type stageEventAuditLog struct {
	mu           sync.Mutex
	path         string
	artifactID   string
	seenEventIDs map[string]bool
	lastSequence map[string]int64
	count        int
}

func newStageEventAuditLog(recordingOutputDir string, cloudJobID string) (*stageEventAuditLog, error) {
	if recordingOutputDir == "" {
		return nil, nil
	}
	if err := os.MkdirAll(recordingOutputDir, 0o700); err != nil {
		return nil, fmt.Errorf("create stage event audit directory: %w", err)
	}
	return &stageEventAuditLog{
		path:         filepath.Join(recordingOutputDir, "browser-agent-stage-events.jsonl"),
		artifactID:   "stage_event_log_" + safePathSegment(cloudJobID),
		seenEventIDs: map[string]bool{},
		lastSequence: map[string]int64{},
	}, nil
}

func (l *stageEventAuditLog) Append(ctx context.Context, event model.StageExecutionEvent) error {
	if l == nil {
		return errors.New("stage event audit log is not configured")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := event.Validate(); err != nil {
		return fmt.Errorf("invalid stage execution event: %w", err)
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seenEventIDs[event.EventID] {
		return nil
	}
	if event.Sequence <= l.lastSequence[event.RunID] {
		return fmt.Errorf("stage event sequence must increase for run %s: got %d after %d", event.RunID, event.Sequence, l.lastSequence[event.RunID])
	}
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open stage event audit log: %w", err)
	}
	writer := bufio.NewWriter(file)
	if _, err = writer.Write(append(data, '\n')); err == nil {
		err = writer.Flush()
	}
	closeErr := file.Close()
	if err != nil {
		return fmt.Errorf("write stage event audit log: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close stage event audit log: %w", closeErr)
	}
	l.seenEventIDs[event.EventID] = true
	l.lastSequence[event.RunID] = event.Sequence
	l.count++
	return nil
}

func (l *stageEventAuditLog) Count() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.count
}

func (l *stageEventAuditLog) ArtifactRef() (model.ArtifactRef, error) {
	if l == nil || l.Count() == 0 {
		return model.ArtifactRef{}, errors.New("stage event audit log is empty")
	}
	data, err := os.ReadFile(l.path)
	if err != nil {
		return model.ArtifactRef{}, err
	}
	return model.ArtifactRef{
		ID: l.artifactID, Kind: "browser_agent_stage_event_log", URI: localFileURI(l.path),
		MimeType: "application/x-ndjson", SHA256: model.SHA256Hex(data), SizeBytes: int64(len(data)), Sensitive: true,
	}, nil
}
