package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"cascade-demoops/backend/internal/model"
)

const (
	defaultUploadTTL           = time.Hour
	defaultMaxEnvelopeBytes    = 2 << 20
	defaultMaxAttachmentBytes  = 512 << 20
	defaultServerPublicKeyID   = "local-dev/server-public-key"
	defaultServerPublicKeyAlg  = "x25519"
	defaultCascadeExecutionIP  = "127.0.0.1"
	defaultCloudJobIDPrefix    = "job"
	defaultExchangeIDPrefix    = "xpkg"
	defaultUploadIDPrefix      = "upload"
	defaultResultPackagePrefix = "result_pkg"
)

type ExchangeIntakeService struct {
	mu              sync.Mutex
	verifier        model.ExchangeSignatureVerifier
	now             func() time.Time
	uploads         map[string]exchangeUploadSession
	packages        map[string]*exchangePackageState
	packageByIdem   map[string]string
	resultByID      map[string]*recordingResultState
	resultByPackage map[string]string
	seenNonces      map[string]bool
}

type exchangeUploadSession struct {
	UploadID    string
	OrgID       string
	ProjectID   string
	PackageKind model.ExchangePackageKind
	Producer    model.ExchangeProducer
	ExpiresAt   time.Time
}

type exchangePackageState struct {
	ExchangePackageID string
	CloudJobID        string
	Status            model.ExchangePackageStatus
	Stage             string
	Message           string
	ProgressPercent   int
	Envelope          model.ExchangeEnvelope
	Payload           model.ClientExecutionPackage
	CreatedAt         time.Time
	UpdatedAt         time.Time
	Error             *model.AgentError
}

type recordingResultState struct {
	ResultPackageID   string
	ExchangePackageID string
	Result            model.RecordingResultPackage
	Retention         model.RetentionSpec
}

func NewExchangeIntakeService(verifier model.ExchangeSignatureVerifier) *ExchangeIntakeService {
	return &ExchangeIntakeService{
		verifier:        verifier,
		now:             func() time.Time { return time.Now().UTC() },
		uploads:         map[string]exchangeUploadSession{},
		packages:        map[string]*exchangePackageState{},
		packageByIdem:   map[string]string{},
		resultByID:      map[string]*recordingResultState{},
		resultByPackage: map[string]string{},
		seenNonces:      map[string]bool{},
	}
}

func (s *ExchangeIntakeService) Init(ctx context.Context, request model.ExecutionPackageInitRequest) (model.ExecutionPackageInitResponse, error) {
	if err := ctx.Err(); err != nil {
		return model.ExecutionPackageInitResponse{}, err
	}
	if request.OrgID == "" || request.ProjectID == "" {
		return model.ExecutionPackageInitResponse{}, errors.New("org_id and project_id are required")
	}
	if request.PackageKind == "" {
		request.PackageKind = model.ExchangePackageKindClientExecution
	}
	if request.PackageKind != model.ExchangePackageKindClientExecution {
		return model.ExecutionPackageInitResponse{}, fmt.Errorf("unsupported package_kind %q", request.PackageKind)
	}

	now := s.now()
	response := model.ExecutionPackageInitResponse{
		UploadID:            newExchangeID(defaultUploadIDPrefix, now),
		ServerPublicKeyID:   defaultServerPublicKeyID,
		ServerPublicKeyAlg:  defaultServerPublicKeyAlg,
		CascadeExecutionIPs: []string{defaultCascadeExecutionIP},
		MaxEnvelopeBytes:    defaultMaxEnvelopeBytes,
		MaxAttachmentBytes:  defaultMaxAttachmentBytes,
		ExpiresAt:           now.Add(defaultUploadTTL),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.uploads[response.UploadID] = exchangeUploadSession{
		UploadID:    response.UploadID,
		OrgID:       request.OrgID,
		ProjectID:   request.ProjectID,
		PackageKind: request.PackageKind,
		Producer:    request.Producer,
		ExpiresAt:   response.ExpiresAt,
	}
	return response, nil
}

func (s *ExchangeIntakeService) Upload(ctx context.Context, request model.ExecutionPackageUploadRequest, payload model.ClientExecutionPackage) (model.ExecutionPackageUploadResponse, error) {
	if err := ctx.Err(); err != nil {
		return model.ExecutionPackageUploadResponse{}, err
	}
	if request.UploadID == "" {
		return model.ExecutionPackageUploadResponse{}, errors.New("upload_id is required")
	}

	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.uploads[request.UploadID]
	if !ok {
		return model.ExecutionPackageUploadResponse{}, errors.New("upload session not found")
	}
	if now.After(session.ExpiresAt) {
		return model.ExecutionPackageUploadResponse{}, errors.New("upload session expired")
	}
	if request.Envelope.OrgID != session.OrgID || request.Envelope.ProjectID != session.ProjectID || request.Envelope.PackageKind != session.PackageKind {
		return model.ExecutionPackageUploadResponse{}, errors.New("upload request envelope does not match initialized session")
	}
	if existingID := s.packageByIdem[idempotencyKey(request.Envelope.OrgID, request.Envelope.IdempotencyKey)]; existingID != "" {
		existing := s.packages[existingID]
		return model.ExecutionPackageUploadResponse{ExchangePackageID: existing.ExchangePackageID, CloudJobID: existing.CloudJobID, Status: existing.Status}, nil
	}
	if err := model.ValidateClientExecutionPackageIntake(&request.Envelope, &payload, now, s.seenNonces, s.verifier); err != nil {
		return model.ExecutionPackageUploadResponse{}, err
	}

	exchangePackageID := newExchangeID(defaultExchangeIDPrefix, now)
	cloudJobID := newExchangeID(defaultCloudJobIDPrefix, now)
	state := &exchangePackageState{
		ExchangePackageID: exchangePackageID,
		CloudJobID:        cloudJobID,
		Status:            model.ExchangePackageStatusAccepted,
		Stage:             "accepted",
		Message:           "Execution package accepted. Call the dev run endpoint to start recording and rendering.",
		ProgressPercent:   10,
		Envelope:          request.Envelope,
		Payload:           payload,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	s.packages[exchangePackageID] = state
	s.packageByIdem[idempotencyKey(request.Envelope.OrgID, request.Envelope.IdempotencyKey)] = exchangePackageID
	delete(s.uploads, request.UploadID)

	return model.ExecutionPackageUploadResponse{ExchangePackageID: exchangePackageID, CloudJobID: cloudJobID, Status: state.Status}, nil
}

func (s *ExchangeIntakeService) Status(ctx context.Context, orgID string, exchangePackageID string) (model.ExecutionPackageStatusResponse, error) {
	if err := ctx.Err(); err != nil {
		return model.ExecutionPackageStatusResponse{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.packageStateLocked(orgID, exchangePackageID)
	if err != nil {
		return model.ExecutionPackageStatusResponse{}, err
	}
	return s.statusResponseLocked(state), nil
}

func (s *ExchangeIntakeService) CompleteWithRecordingResult(ctx context.Context, orgID string, exchangePackageID string, result model.RecordingResultPackage) (model.ExecutionPackageStatusResponse, error) {
	if err := ctx.Err(); err != nil {
		return model.ExecutionPackageStatusResponse{}, err
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.packageStateLocked(orgID, exchangePackageID)
	if err != nil {
		return model.ExecutionPackageStatusResponse{}, err
	}
	if err := model.ValidateRecordingResultPackageForRender(&result, &state.Payload); err != nil {
		state.Status = model.ExchangePackageStatusFailed
		state.Stage = "failed"
		state.Message = "Recording result package was rejected by cloud-side validation."
		state.ProgressPercent = 100
		state.Error = &model.AgentError{Code: "recording_result_invalid", Message: err.Error()}
		state.UpdatedAt = now
		return s.statusResponseLocked(state), err
	}

	resultPackageID := newExchangeID(defaultResultPackagePrefix, now)
	retention := resultRetention(result, now)
	s.resultByID[resultPackageID] = &recordingResultState{
		ResultPackageID:   resultPackageID,
		ExchangePackageID: state.ExchangePackageID,
		Result:            result,
		Retention:         retention,
	}
	s.resultByPackage[state.ExchangePackageID] = resultPackageID
	state.Status = model.ExchangePackageStatusCompleted
	state.Stage = "completed"
	state.Message = "Recording and rendering completed. Result package is ready."
	state.ProgressPercent = 100
	state.UpdatedAt = now

	return s.statusResponseLocked(state), nil
}

func (s *ExchangeIntakeService) GetResultPackage(ctx context.Context, orgID string, resultPackageID string) (model.RecordingResultPackage, error) {
	if err := ctx.Err(); err != nil {
		return model.RecordingResultPackage{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	resultState, ok := s.resultByID[resultPackageID]
	if !ok {
		return model.RecordingResultPackage{}, errors.New("result package not found")
	}
	state, err := s.packageStateLocked(orgID, resultState.ExchangePackageID)
	if err != nil {
		return model.RecordingResultPackage{}, err
	}
	if state.Envelope.Policy.DeletePayloadAfterRun {
		state.Payload = model.ClientExecutionPackage{}
	}
	return resultState.Result, nil
}

func (s *ExchangeIntakeService) AckResultPackage(ctx context.Context, orgID string, request model.ResultPackageAckRequest) (model.ResultPackageAckResponse, error) {
	if err := ctx.Err(); err != nil {
		return model.ResultPackageAckResponse{}, err
	}
	if request.ResultPackageID == "" {
		return model.ResultPackageAckResponse{}, errors.New("result_package_id is required")
	}
	if request.AckedAt.IsZero() {
		request.AckedAt = s.now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	resultState, ok := s.resultByID[request.ResultPackageID]
	if !ok {
		return model.ResultPackageAckResponse{}, errors.New("result package not found")
	}
	state, err := s.packageStateLocked(orgID, resultState.ExchangePackageID)
	if err != nil {
		return model.ResultPackageAckResponse{}, err
	}
	resultState.Result.Status = model.RecordingResultStatusAcked
	resultState.Result.Delivery.AckedAt = request.AckedAt
	state.UpdatedAt = request.AckedAt
	return model.ResultPackageAckResponse{ResultPackageID: request.ResultPackageID, Status: model.RecordingResultStatusAcked, Retention: resultState.Retention}, nil
}

func (s *ExchangeIntakeService) packageState(orgID string, exchangePackageID string) (*exchangePackageState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.packageStateLocked(orgID, exchangePackageID)
}

func (s *ExchangeIntakeService) packageStateLocked(orgID string, exchangePackageID string) (*exchangePackageState, error) {
	if exchangePackageID == "" {
		return nil, errors.New("exchange_package_id is required")
	}
	state, ok := s.packages[exchangePackageID]
	if !ok {
		return nil, errors.New("exchange package not found")
	}
	if state.Envelope.OrgID != orgID {
		return nil, errors.New("exchange package org mismatch")
	}
	return state, nil
}

func resultRetention(result model.RecordingResultPackage, now time.Time) model.RetentionSpec {
	if !result.Delivery.ExpiresAt.IsZero() {
		return model.RetentionSpec{ExpiresAt: result.Delivery.ExpiresAt, Reason: "recording_result_delivery"}
	}
	return model.RetentionSpec{ExpiresAt: now.Add(24 * time.Hour), Reason: "default_recording_result_retention"}
}

func (s *ExchangeIntakeService) statusResponseLocked(state *exchangePackageState) model.ExecutionPackageStatusResponse {
	stage, message, progress := exchangeStatusView(state)
	response := model.ExecutionPackageStatusResponse{
		ExchangePackageID: state.ExchangePackageID,
		CloudJobID:        state.CloudJobID,
		Status:            state.Status,
		Stage:             stage,
		Message:           message,
		ProgressPercent:   progress,
		Error:             state.Error,
		UpdatedAt:         state.UpdatedAt,
	}
	if resultID := s.resultByPackage[state.ExchangePackageID]; resultID != "" {
		response.ResultPackageID = resultID
		if resultState := s.resultByID[resultID]; resultState != nil {
			response.ResultSummary = summarizeRecordingResult(resultState.Result)
		}
	}
	return response
}

func exchangeStatusView(state *exchangePackageState) (string, string, int) {
	if state.Stage != "" || state.Message != "" || state.ProgressPercent > 0 {
		return state.Stage, state.Message, state.ProgressPercent
	}
	switch state.Status {
	case model.ExchangePackageStatusAccepted:
		return "accepted", "Execution package accepted. Call the dev run endpoint to start recording and rendering.", 10
	case model.ExchangePackageStatusRunning:
		return "recording_rendering", "Recording and rendering are running.", 50
	case model.ExchangePackageStatusCompleted:
		return "completed", "Recording and rendering completed. Result package is ready.", 100
	case model.ExchangePackageStatusFailed:
		return "failed", "Execution failed.", 100
	default:
		return string(state.Status), "", 0
	}
}

func summarizeRecordingResult(result model.RecordingResultPackage) *model.ExecutionResultSummary {
	steps := result.StepResults
	if len(steps) == 0 && result.ExecutionTrace != nil {
		steps = result.ExecutionTrace.StepResults
	}
	assets := uniqueStatusArtifacts(append(append([]model.ArtifactRef{}, result.GeneratedAssets...), traceArtifacts(result.ExecutionTrace)...))
	summary := &model.ExecutionResultSummary{
		ResultID:            result.ResultID,
		ResultStatus:        result.Status,
		PassRate:            result.VerificationReport.PassRate,
		StepCount:           len(steps),
		GeneratedAssetCount: len(assets),
	}
	if summary.PassRate == 0 && result.ExecutionTrace != nil {
		summary.PassRate = result.ExecutionTrace.PassRate
	}
	for _, step := range steps {
		switch {
		case strings.EqualFold(step.Status, "passed"):
			summary.PassedStepCount++
		case strings.EqualFold(step.Status, "failed"):
			summary.FailedStepCount++
		}
	}
	for _, asset := range assets {
		switch strings.ToLower(asset.Kind) {
		case "demo_video":
			summary.DemoVideoCount++
			if summary.PrimaryDemoVideoURI == "" {
				summary.PrimaryDemoVideoURI = asset.URI
			}
		case "raw_recording":
			summary.RawRecordingCount++
			if summary.RawRecordingURI == "" {
				summary.RawRecordingURI = asset.URI
			}
		case "screenshot", "webpage_screenshot":
			summary.ScreenshotCount++
		case "browser_trace", "execution_trace":
			summary.TraceCount++
		}
	}
	return summary
}

func traceArtifacts(trace *model.ExecutionTrace) []model.ArtifactRef {
	if trace == nil {
		return nil
	}
	return trace.Artifacts
}

func uniqueStatusArtifacts(artifacts []model.ArtifactRef) []model.ArtifactRef {
	seen := map[string]bool{}
	out := []model.ArtifactRef{}
	for _, artifact := range artifacts {
		key := artifact.ID
		if key == "" {
			key = artifact.URI
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, artifact)
	}
	return out
}

func idempotencyKey(orgID string, key string) string {
	return orgID + "\x00" + key
}

func newExchangeID(prefix string, now time.Time) string {
	return fmt.Sprintf("%s_%d", prefix, now.UnixNano())
}
