package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

type projectActivityStore struct {
	root   string
	mu     sync.Mutex
	states map[string]*model.ProjectActivityState
	events map[string][]model.ProjectActivityEvent
	nextID int64
}

var projectActivityBearerPattern = regexp.MustCompile(`(?i)(authorization\s*:\s*)?bearer\s+[^\s,;]+`)

func newProjectActivityStore(root string) *projectActivityStore {
	return &projectActivityStore{root: filepath.Clean(root), states: map[string]*model.ProjectActivityState{}, events: map[string][]model.ProjectActivityEvent{}}
}

func (s *projectActivityStore) append(ctx context.Context, event model.ProjectActivityEvent) (model.ProjectActivityState, error) {
	if err := ctx.Err(); err != nil {
		return model.ProjectActivityState{}, err
	}
	if strings.TrimSpace(event.ProjectID) == "" {
		return model.ProjectActivityState{}, errors.New("project activity project ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked(event.ProjectID)
	previous := s.states[event.ProjectID]
	if event.Mode == model.ProjectCanvasModeBrowser && previous != nil && previous.Current != nil && previous.Current.Browser != nil {
		lastBrowser := *previous.Current.Browser
		if event.Browser == nil {
			event.Browser = &lastBrowser
		} else if event.Browser.FrameRef == "" {
			event.Browser.FrameRef = lastBrowser.FrameRef
		}
	}
	s.nextID++
	if event.ID == "" {
		event.ID = strconv.FormatInt(s.nextID, 10)
	}
	if event.RunID == "" {
		event.RunID = "run_" + event.ProjectID
	}
	if event.Mode == "" {
		event.Mode = model.ProjectCanvasModeAct
	}
	if event.Status == "" {
		event.Status = model.ProjectActivityRunning
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	event.Title = redactAssistantText(strings.TrimSpace(event.Title))
	event.Detail = projectActivityBearerPattern.ReplaceAllString(redactAssistantText(redactBridgeError(strings.TrimSpace(event.Detail))), "Authorization: Bearer [redacted]")
	if event.Browser != nil {
		event.Browser.URL = safeBrowserActivityURL(event.Browser.URL)
		event.Browser.Title = redactAssistantText(event.Browser.Title)
		event.Browser.Redacted = true
	}
	s.events[event.ProjectID] = append(s.events[event.ProjectID], event)
	if len(s.events[event.ProjectID]) > 200 {
		s.events[event.ProjectID] = append([]model.ProjectActivityEvent{}, s.events[event.ProjectID][len(s.events[event.ProjectID])-200:]...)
	}
	state := &model.ProjectActivityState{ProjectID: event.ProjectID, Mode: event.Mode, RunID: event.RunID, Status: event.Status, Current: &event, LastEventID: event.ID, UpdatedAt: event.OccurredAt}
	if previous != nil {
		state.EditorSessionID = previous.EditorSessionID
	}
	if event.EditorSessionID != "" {
		state.EditorSessionID = event.EditorSessionID
	}
	state.Recent = recentProjectActivities(s.events[event.ProjectID], 5)
	if event.Status == model.ProjectActivityCompleted && event.Mode == model.ProjectCanvasModeAct {
		state.Current = nil
		if state.EditorSessionID != "" {
			state.Mode = model.ProjectCanvasModeEditor
		} else {
			state.Mode = model.ProjectCanvasModeEmpty
		}
	}
	s.states[event.ProjectID] = state
	return *state, s.persistLocked(event.ProjectID)
}

func (s *projectActivityStore) state(ctx context.Context, projectID string) (model.ProjectActivityState, error) {
	if err := ctx.Err(); err != nil {
		return model.ProjectActivityState{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked(projectID)
	if state := s.states[projectID]; state != nil {
		copy := *state
		copy.Recent = append([]model.ProjectActivityEvent{}, state.Recent...)
		return copy, nil
	}
	return model.ProjectActivityState{ProjectID: projectID, Mode: model.ProjectCanvasModeEmpty}, nil
}

func (s *projectActivityStore) list(ctx context.Context, projectID, after string) ([]model.ProjectActivityEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked(projectID)
	result := make([]model.ProjectActivityEvent, 0)
	for _, event := range s.events[projectID] {
		if after == "" || numericEventID(event.ID) > numericEventID(after) {
			result = append(result, event)
		}
	}
	return result, nil
}

func (s *projectActivityStore) loadLocked(projectID string) {
	if s.states[projectID] != nil || s.root == "" || s.root == "." {
		return
	}
	data, err := os.ReadFile(s.path(projectID))
	if err != nil {
		return
	}
	var payload struct {
		State  model.ProjectActivityState   `json:"state"`
		Events []model.ProjectActivityEvent `json:"events"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return
	}
	s.states[projectID] = &payload.State
	s.events[projectID] = payload.Events
	for _, event := range payload.Events {
		if id := numericEventID(event.ID); id > s.nextID {
			s.nextID = id
		}
	}
}

func (s *projectActivityStore) persistLocked(projectID string) error {
	if s.root == "" || s.root == "." {
		return nil
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return err
	}
	payload := struct {
		State  *model.ProjectActivityState  `json:"state"`
		Events []model.ProjectActivityEvent `json:"events"`
	}{State: s.states[projectID], Events: s.events[projectID]}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	path := s.path(projectID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *projectActivityStore) path(projectID string) string {
	digest := sha256.Sum256([]byte(projectID))
	return filepath.Join(s.root, hex.EncodeToString(digest[:])+".json")
}

func recentProjectActivities(events []model.ProjectActivityEvent, limit int) []model.ProjectActivityEvent {
	result := append([]model.ProjectActivityEvent{}, events...)
	sort.SliceStable(result, func(i, j int) bool { return result[i].OccurredAt.After(result[j].OccurredAt) })
	if len(result) > limit {
		result = result[:limit]
	}
	return result
}

func numericEventID(value string) int64 {
	id, _ := strconv.ParseInt(value, 10, 64)
	return id
}

func safeBrowserActivityURL(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	for _, marker := range []string{"?", "#"} {
		if index := strings.Index(trimmed, marker); index >= 0 {
			trimmed = trimmed[:index]
		}
	}
	return redactBridgeError(trimmed)
}

func (s *Service) AppendProjectActivity(ctx context.Context, event model.ProjectActivityEvent) (model.ProjectActivityState, error) {
	if s == nil || s.activity == nil {
		return model.ProjectActivityState{ProjectID: event.ProjectID, Mode: event.Mode, Current: &event}, nil
	}
	return s.activity.append(ctx, event)
}

func (s *Service) GetProjectActivityState(ctx context.Context, projectID string) (model.ProjectActivityState, error) {
	if s == nil || s.activity == nil {
		return model.ProjectActivityState{ProjectID: projectID, Mode: model.ProjectCanvasModeEmpty}, nil
	}
	return s.activity.state(ctx, projectID)
}

func (s *Service) ListProjectActivityEvents(ctx context.Context, projectID, after string) ([]model.ProjectActivityEvent, error) {
	if s == nil || s.activity == nil {
		return []model.ProjectActivityEvent{}, nil
	}
	return s.activity.list(ctx, projectID, after)
}

func (s *Service) appendProgressActivity(ctx context.Context, projectID, runID string, progress orchestrator.ProgressEvent) {
	status := model.ProjectActivityRunning
	if progress.Level == orchestrator.ProgressLevelError {
		status = model.ProjectActivityFailed
	} else if progress.Level == orchestrator.ProgressLevelSuccess {
		status = model.ProjectActivityCompleted
	}
	_, _ = s.AppendProjectActivity(ctx, model.ProjectActivityEvent{
		ProjectID: projectID, RunID: runID, Mode: model.ProjectCanvasModeAct,
		Kind: string(progress.Node), Status: status, Title: progress.Message, Detail: progress.Detail,
	})
}

func (s *Service) appendCloudActivity(ctx context.Context, projectID, runID, stage, message string, progress int) {
	mode := model.ProjectCanvasModeAct
	status := model.ProjectActivityRunning
	kind := stage
	var capture *model.ProjectCaptureActivity
	switch stage {
	case "running_browser_agent", "running_script", "recording", "validating_runtime_stage":
		mode = model.ProjectCanvasModeBrowser
	case "material_validation", "packaging_recording":
		mode = model.ProjectCanvasModeBrowser
		capture = &model.ProjectCaptureActivity{Kind: "recording", Phase: "completed"}
	case "directing", "preparing_director_input", "applying_director_patch", "rendering", "quality_validation", "validating_post_execution":
		mode = model.ProjectCanvasModeEditor
	}
	if stage == "recording" || stage == "running_browser_agent" || stage == "running_script" {
		capture = &model.ProjectCaptureActivity{Kind: "recording", Phase: "active"}
	}
	_, _ = s.AppendProjectActivity(ctx, model.ProjectActivityEvent{
		ProjectID: projectID, RunID: runID, Mode: mode, Kind: kind, Status: status,
		Title: message, Progress: progress, Capture: capture,
	})
}
