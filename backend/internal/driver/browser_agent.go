package driver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"cascade-demoops/backend/internal/model"
)

type BrowserAgentWorker struct {
	NodeBinary  string
	WorkerPath  string
	Environment map[string]string
}

type BrowserAgentWorkerBrowser struct {
	Engine      string                     `json:"engine,omitempty"`
	Headless    bool                       `json:"headless"`
	Viewport    BrowserAgentWorkerViewport `json:"viewport"`
	RecordVideo bool                       `json:"record_video"`
}

type BrowserAgentWorkerViewport struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type BrowserAgentWorkerOpenRequest struct {
	SessionID             string                    `json:"session_id,omitempty"`
	OutputDir             string                    `json:"output_dir"`
	InitialURL            string                    `json:"initial_url,omitempty"`
	Browser               BrowserAgentWorkerBrowser `json:"browser"`
	AllowedDomains        []string                  `json:"allowed_domains"`
	AllowedOrigins        []string                  `json:"allowed_origins,omitempty"`
	AllowedRoutes         []string                  `json:"allowed_routes,omitempty"`
	ForbiddenPages        []string                  `json:"forbidden_pages,omitempty"`
	ForbiddenPathPrefixes []string                  `json:"forbidden_path_prefixes,omitempty"`
	ForbiddenKeywords     []string                  `json:"forbidden_keywords,omitempty"`
	MaskSelectors         []string                  `json:"mask_selectors,omitempty"`
	RecordingSensitive    *bool                     `json:"recording_sensitive,omitempty"`
	// RecordTrace may be disabled only by the local dev-visible login handoff.
	// It prevents credentials entered manually in that isolated window from
	// being persisted in a Playwright trace.
	RecordTrace *bool                             `json:"record_trace,omitempty"`
	TaskSecrets map[string]BrowserAgentTaskSecret `json:"task_secrets,omitempty"`
}

type BrowserAgentTaskSecret struct {
	Username          string    `json:"username"`
	Password          string    `json:"password"`
	ExpiresAt         time.Time `json:"expires_at"`
	AllowedDomains    []string  `json:"allowed_domains"`
	AllowedOperations []string  `json:"allowed_operations"`
}

type BrowserAgentWorkerOpenResult struct {
	SessionID       string            `json:"session_id"`
	RuntimeVersions map[string]string `json:"runtime_versions,omitempty"`
}

type BrowserAgentWorkerStage struct {
	ID                                string                              `json:"id"`
	Order                             int                                 `json:"order"`
	NodeID                            string                              `json:"node_id"`
	StageKind                         model.BusinessStageKind             `json:"stage_kind,omitempty"`
	Objective                         string                              `json:"objective,omitempty"`
	EntryRoute                        string                              `json:"entry_route,omitempty"`
	Route                             string                              `json:"route,omitempty"`
	URL                               string                              `json:"url,omitempty"`
	TargetRouteTemplate               string                              `json:"target_route_template,omitempty"`
	ExpectedRouteAfterAction          string                              `json:"expected_route_after_action,omitempty"`
	RuntimeRouteVerificationRequired  bool                                `json:"runtime_route_verification_required,omitempty"`
	TargetContract                    model.BrowserAgentTargetContract    `json:"target_contract"`
	InteractionContract               *model.InteractionContract          `json:"interaction_contract,omitempty"`
	Components                        []model.BrowserAgentComponentTarget `json:"components,omitempty"`
	Interactions                      []model.BrowserAgentInteraction     `json:"interactions"`
	WaitConditions                    []string                            `json:"wait_conditions,omitempty"`
	CapturePlan                       *model.BrowserAgentCapturePlan      `json:"capture_plan,omitempty"`
	SuccessState                      string                              `json:"success_state,omitempty"`
	DurationMS                        int                                 `json:"duration_ms,omitempty"`
	Validations                       []model.ValidationSpec              `json:"validations,omitempty"`
	PreferredSelectorAlternative      *model.SelectorCandidate            `json:"preferred_selector_alternative,omitempty"`
	EvidenceBoundSelectorAlternatives []model.SelectorCandidate           `json:"evidence_bound_selector_alternatives,omitempty"`
	SuggestedWaitCondition            string                              `json:"suggested_wait_condition,omitempty"`
}

type BrowserAgentWorkerStageRequest struct {
	SessionID string                  `json:"session_id"`
	Stage     BrowserAgentWorkerStage `json:"stage"`
	// Secrets is an ephemeral RPC-only map keyed by an approved opaque
	// secret_ref. It must never be copied into stage data, logs, or artifacts.
	Secrets map[string]string `json:"secrets,omitempty"`
}

type BrowserAgentWorkerStageResult struct {
	Observation                  model.RuntimeObservation `json:"observation"`
	EvidenceRefs                 []model.EvidenceRef      `json:"evidence_refs"`
	Artifacts                    []model.ArtifactRef      `json:"artifacts,omitempty"`
	TargetResolved               bool                     `json:"target_resolved,omitempty"`
	PreferredSelectorAlternative *model.SelectorCandidate `json:"preferred_selector_alternative,omitempty"`
	SuggestedWaitCondition       string                   `json:"suggested_wait_condition,omitempty"`
}

// BrowserAgentWorkerStatus is deliberately limited to redacted page metadata.
// It never returns storage state, cookies, page HTML, or form values.
type BrowserAgentWorkerStatus struct {
	URL   string `json:"url,omitempty"`
	Title string `json:"title,omitempty"`
}

// BrowserAgentWorkerExecutionPolicy narrows the initial local login scope to
// the App-approved execution scope before any Browser Agent stage can run.
type BrowserAgentWorkerExecutionPolicy struct {
	AllowedOrigins        []string `json:"allowed_origins"`
	AllowedRoutes         []string `json:"allowed_routes"`
	ForbiddenPages        []string `json:"forbidden_pages,omitempty"`
	ForbiddenPathPrefixes []string `json:"forbidden_path_prefixes,omitempty"`
	ForbiddenKeywords     []string `json:"forbidden_keywords,omitempty"`
	MaskSelectors         []string `json:"mask_selectors,omitempty"`
}

type BrowserAgentWorkerCloseResult struct {
	RecordingPath   string              `json:"recording_path,omitempty"`
	TracePath       string              `json:"trace_path,omitempty"`
	Artifacts       []model.ArtifactRef `json:"artifacts,omitempty"`
	RuntimeVersions map[string]string   `json:"runtime_versions,omitempty"`
}

type BrowserAgentWorkerSession struct {
	mu        sync.Mutex
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	decoder   *json.Decoder
	stderr    bytes.Buffer
	nextID    int
	closed    bool
	waited    bool
	sessionID string
}

func NewBrowserAgentWorker(nodeBinary string, workerPath string, environment ...map[string]string) *BrowserAgentWorker {
	if nodeBinary == "" {
		nodeBinary = "node"
	}
	return &BrowserAgentWorker{NodeBinary: nodeBinary, WorkerPath: workerPath, Environment: firstEnvironment(environment)}
}

func (w *BrowserAgentWorker) Open(ctx context.Context, request BrowserAgentWorkerOpenRequest) (*BrowserAgentWorkerSession, BrowserAgentWorkerOpenResult, error) {
	if w == nil || w.WorkerPath == "" {
		return nil, BrowserAgentWorkerOpenResult{}, errors.New("node worker path is required")
	}
	cmd := exec.Command(w.NodeBinary, w.WorkerPath)
	cmd.Env = childProcessEnvironment(w.Environment)
	configureProcessTree(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, BrowserAgentWorkerOpenResult{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, BrowserAgentWorkerOpenResult{}, err
	}
	session := &BrowserAgentWorkerSession{cmd: cmd, stdin: stdin, decoder: json.NewDecoder(bufio.NewReader(stdout))}
	cmd.Stderr = &session.stderr
	if err := cmd.Start(); err != nil {
		return nil, BrowserAgentWorkerOpenResult{}, err
	}
	var result BrowserAgentWorkerOpenResult
	if err := session.call(ctx, "browser_agent_open", request, &result); err != nil {
		_ = session.Abort()
		return nil, BrowserAgentWorkerOpenResult{}, err
	}
	if result.SessionID == "" {
		_ = session.Abort()
		return nil, BrowserAgentWorkerOpenResult{}, errors.New("browser-agent worker did not return session_id")
	}
	session.sessionID = result.SessionID
	return session, result, nil
}

func (s *BrowserAgentWorkerSession) Observe(ctx context.Context, stage BrowserAgentWorkerStage) (BrowserAgentWorkerStageResult, error) {
	var result BrowserAgentWorkerStageResult
	err := s.call(ctx, "browser_agent_observe", BrowserAgentWorkerStageRequest{SessionID: s.sessionID, Stage: stage}, &result)
	return result, err
}

func (s *BrowserAgentWorkerSession) Execute(ctx context.Context, stage BrowserAgentWorkerStage) (BrowserAgentWorkerStageResult, error) {
	var result BrowserAgentWorkerStageResult
	err := s.call(ctx, "browser_agent_execute", BrowserAgentWorkerStageRequest{SessionID: s.sessionID, Stage: stage}, &result)
	return result, err
}

func (s *BrowserAgentWorkerSession) ExecuteWithSecrets(ctx context.Context, stage BrowserAgentWorkerStage, secrets map[string]string) (BrowserAgentWorkerStageResult, error) {
	var result BrowserAgentWorkerStageResult
	err := s.call(ctx, "browser_agent_execute", BrowserAgentWorkerStageRequest{SessionID: s.sessionID, Stage: stage, Secrets: secrets}, &result)
	return result, err
}

// Revalidate waits for the approved capture window and observes the outcome
// again without repeating click/fill/select or any other business action.
func (s *BrowserAgentWorkerSession) Revalidate(ctx context.Context, stage BrowserAgentWorkerStage) (BrowserAgentWorkerStageResult, error) {
	var result BrowserAgentWorkerStageResult
	err := s.call(ctx, "browser_agent_revalidate", BrowserAgentWorkerStageRequest{SessionID: s.sessionID, Stage: stage}, &result)
	return result, err
}

func (s *BrowserAgentWorkerSession) Status(ctx context.Context) (BrowserAgentWorkerStatus, error) {
	var result BrowserAgentWorkerStatus
	err := s.call(ctx, "browser_agent_status", map[string]string{"session_id": s.sessionID}, &result)
	return result, err
}

func (s *BrowserAgentWorkerSession) ApplyExecutionPolicy(ctx context.Context, policy BrowserAgentWorkerExecutionPolicy) error {
	var result struct{}
	return s.call(ctx, "browser_agent_apply_execution_policy", map[string]any{"session_id": s.sessionID, "policy": policy}, &result)
}

// NavigateDevVisible is restricted to the local dev-visible login handoff. It
// navigates without capturing an action screenshot or reading page data.
func (s *BrowserAgentWorkerSession) NavigateDevVisible(ctx context.Context, targetURL string) (BrowserAgentWorkerStatus, error) {
	var result BrowserAgentWorkerStatus
	err := s.call(ctx, "browser_agent_dev_visible_navigate", map[string]string{"session_id": s.sessionID, "target_url": targetURL}, &result)
	return result, err
}

// AutoLoginDevVisible is a local test-only handoff. The credentials are sent
// over the private Worker stdin RPC and the Worker returns only redacted page
// metadata; callers must not persist or log the arguments.
func (s *BrowserAgentWorkerSession) AutoLoginDevVisible(ctx context.Context, email, password string) (BrowserAgentWorkerStatus, error) {
	var result BrowserAgentWorkerStatus
	err := s.call(ctx, "browser_agent_dev_visible_auto_login", map[string]string{
		"session_id": s.sessionID,
		"email":      email,
		"password":   password,
	}, &result)
	return result, err
}

// BeginExecutionRecording starts the trace only after any local login handoff
// has completed. This keeps login values out of the Playwright trace.
func (s *BrowserAgentWorkerSession) BeginExecutionRecording(ctx context.Context) error {
	var result struct{}
	return s.call(ctx, "browser_agent_dev_visible_begin_recording", map[string]string{"session_id": s.sessionID}, &result)
}

func (s *BrowserAgentWorkerSession) Close(ctx context.Context) (BrowserAgentWorkerCloseResult, error) {
	var result BrowserAgentWorkerCloseResult
	if err := s.call(ctx, "browser_agent_close", map[string]string{"session_id": s.sessionID}, &result); err != nil {
		_ = s.Abort()
		return BrowserAgentWorkerCloseResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	_ = s.stdin.Close()
	if err := s.waitLocked(); err != nil {
		return BrowserAgentWorkerCloseResult{}, fmt.Errorf("browser-agent worker failed: %w; stderr=%s", err, s.stderr.String())
	}
	return result, nil
}

func (s *BrowserAgentWorkerSession) Abort() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed && s.waited {
		return nil
	}
	s.closed = true
	_ = s.stdin.Close()
	_ = terminateProcessTree(s.cmd)
	return s.waitLocked()
}

func (s *BrowserAgentWorkerSession) call(ctx context.Context, method string, params any, result any) error {
	if s == nil {
		return errors.New("browser-agent worker session is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("browser-agent worker session is closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.nextID++
	request := rpcRequest{JSONRPC: "2.0", ID: s.nextID, Method: method, Params: params}
	if err := json.NewEncoder(s.stdin).Encode(request); err != nil {
		return err
	}
	type decodeResult struct {
		response rpcResponse
		err      error
	}
	done := make(chan decodeResult, 1)
	go func() {
		var response rpcResponse
		err := s.decoder.Decode(&response)
		done <- decodeResult{response: response, err: err}
	}()
	select {
	case <-ctx.Done():
		s.closed = true
		_ = s.stdin.Close()
		_ = terminateProcessTree(s.cmd)
		_ = s.waitLocked()
		return ctx.Err()
	case decoded := <-done:
		if decoded.err != nil {
			return fmt.Errorf("decode browser-agent worker response: %w; stderr=%s", decoded.err, s.stderr.String())
		}
		if decoded.response.Error != nil {
			return errors.New(decoded.response.Error.Message)
		}
		if decoded.response.ID != s.nextID {
			return fmt.Errorf("browser-agent worker response id mismatch: got %d want %d", decoded.response.ID, s.nextID)
		}
		return json.Unmarshal(decoded.response.Result, result)
	}
}

func (s *BrowserAgentWorkerSession) waitLocked() error {
	if s.waited {
		return nil
	}
	s.waited = true
	return s.cmd.Wait()
}
