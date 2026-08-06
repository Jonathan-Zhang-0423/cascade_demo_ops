package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	MiniMaxH3AdmissionAccepted             = "accepted"
	MiniMaxH3AdmissionReplay               = "idempotent_replay"
	MiniMaxH3AdmissionInvalidPolicy        = "h3_admission_policy_invalid"
	MiniMaxH3AdmissionInvalidRequest       = "h3_admission_request_invalid"
	MiniMaxH3AdmissionIdempotencyRequired  = "h3_idempotency_key_required"
	MiniMaxH3AdmissionIdempotencyConflict  = "h3_idempotency_key_conflict"
	MiniMaxH3AdmissionIdempotencyInFlight  = "h3_idempotency_in_progress"
	MiniMaxH3AdmissionConcurrentLimit      = "h3_concurrent_limit_exceeded"
	MiniMaxH3AdmissionRequestQuota         = "h3_request_quota_exceeded"
	MiniMaxH3AdmissionOutputSecondsQuota   = "h3_output_seconds_quota_exceeded"
	MiniMaxH3AdmissionEstimatedCostQuota   = "h3_estimated_cost_quota_exceeded"
	MiniMaxH3AdmissionProviderCallFailed   = "h3_provider_call_failed"
	MiniMaxH3AdmissionProviderTaskMissing  = "h3_provider_task_missing"
	MiniMaxH3AdmissionRequestTimeout       = "h3_provider_request_timeout"
	MiniMaxH3AdmissionRequestCanceled      = "h3_provider_request_canceled"
	miniMaxH3AdmissionIdempotencyKeyMaxLen = 160
)

type MiniMaxH3TaskCreator interface {
	CreateContentGenerationTask(ctx context.Context, request ContentGenerationTaskRequest) (ContentGenerationTaskResult, error)
}

// MiniMaxH3AdmissionPolicy is Server-owned configuration. Monetary values use
// integer micros so budget decisions do not depend on floating-point math.
// No provider price is embedded in code: operations must configure the current
// verified rate before a governed submitter can be constructed.
type MiniMaxH3AdmissionPolicy struct {
	MaxConcurrent                      int           `json:"max_concurrent"`
	Window                             time.Duration `json:"window"`
	MaxRequestsPerWindow               int           `json:"max_requests_per_window"`
	MaxOutputSecondsPerWindow          int           `json:"max_output_seconds_per_window"`
	EstimatedCostMicrosPerOutputSecond int64         `json:"estimated_cost_micros_per_output_second"`
	MaxEstimatedCostMicrosPerWindow    int64         `json:"max_estimated_cost_micros_per_window"`
	ProviderRequestTimeout             time.Duration `json:"provider_request_timeout"`
	IdempotencyTTL                     time.Duration `json:"idempotency_ttl"`
}

type MiniMaxH3GovernedSubmitRequest struct {
	Scope          string                       `json:"scope"`
	IdempotencyKey string                       `json:"idempotency_key"`
	Request        ContentGenerationTaskRequest `json:"request"`
}

type MiniMaxH3AdmissionSnapshot struct {
	WindowStartedAt             time.Time `json:"window_started_at"`
	WindowEndsAt                time.Time `json:"window_ends_at"`
	Concurrent                  int       `json:"concurrent"`
	RequestsUsed                int       `json:"requests_used"`
	OutputSecondsReserved       int       `json:"output_seconds_reserved"`
	EstimatedCostMicrosReserved int64     `json:"estimated_cost_micros_reserved"`
}

type MiniMaxH3GovernedSubmitResult struct {
	Status                   string                       `json:"status"`
	Scope                    string                       `json:"scope"`
	IdempotencyKey           string                       `json:"idempotency_key"`
	RequestFingerprintSHA256 string                       `json:"request_fingerprint_sha256"`
	EstimatedOutputSeconds   int                          `json:"estimated_output_seconds"`
	EstimatedCostMicros      int64                        `json:"estimated_cost_micros"`
	Replayed                 bool                         `json:"replayed"`
	ProviderResult           *ContentGenerationTaskResult `json:"provider_result,omitempty"`
	Admission                MiniMaxH3AdmissionSnapshot   `json:"admission"`
	ErrorCode                string                       `json:"error_code,omitempty"`
	ErrorMessage             string                       `json:"error_message,omitempty"`
}

type MiniMaxH3AdmissionError struct {
	Code    string
	Message string
}

func (e *MiniMaxH3AdmissionError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + ": " + e.Message
}

type miniMaxH3AdmissionRecord struct {
	fingerprint string
	createdAt   time.Time
	completedAt time.Time
	inFlight    bool
	result      MiniMaxH3GovernedSubmitResult
	err         error
}

type MiniMaxH3GovernedSubmitter struct {
	client MiniMaxH3TaskCreator
	policy MiniMaxH3AdmissionPolicy
	now    func() time.Time

	mu                      sync.Mutex
	windowStartedAt         time.Time
	concurrent              int
	requestsUsed            int
	outputSecondsReserved   int
	estimatedCostMicrosUsed int64
	records                 map[string]*miniMaxH3AdmissionRecord
}

func NewMiniMaxH3GovernedSubmitter(client MiniMaxH3TaskCreator, policy MiniMaxH3AdmissionPolicy, now func() time.Time) (*MiniMaxH3GovernedSubmitter, error) {
	if client == nil {
		return nil, &MiniMaxH3AdmissionError{Code: MiniMaxH3AdmissionInvalidPolicy, Message: "MiniMax-H3 task creator is required"}
	}
	if err := validateMiniMaxH3AdmissionPolicy(policy); err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	startedAt := now().UTC()
	return &MiniMaxH3GovernedSubmitter{
		client: client, policy: policy, now: now, windowStartedAt: startedAt,
		records: map[string]*miniMaxH3AdmissionRecord{},
	}, nil
}

func validateMiniMaxH3AdmissionPolicy(policy MiniMaxH3AdmissionPolicy) error {
	invalid := func(message string) error {
		return &MiniMaxH3AdmissionError{Code: MiniMaxH3AdmissionInvalidPolicy, Message: message}
	}
	if policy.MaxConcurrent <= 0 {
		return invalid("max_concurrent must be greater than zero")
	}
	if policy.Window <= 0 {
		return invalid("window must be greater than zero")
	}
	if policy.MaxRequestsPerWindow <= 0 {
		return invalid("max_requests_per_window must be greater than zero")
	}
	if policy.MaxOutputSecondsPerWindow <= 0 {
		return invalid("max_output_seconds_per_window must be greater than zero")
	}
	if policy.EstimatedCostMicrosPerOutputSecond <= 0 {
		return invalid("estimated_cost_micros_per_output_second must use a current verified provider rate")
	}
	if policy.MaxEstimatedCostMicrosPerWindow <= 0 {
		return invalid("max_estimated_cost_micros_per_window must be greater than zero")
	}
	if policy.ProviderRequestTimeout <= 0 {
		return invalid("provider_request_timeout must be greater than zero")
	}
	if policy.IdempotencyTTL <= 0 {
		return invalid("idempotency_ttl must be greater than zero")
	}
	return nil
}

// Submit is deliberately not wired into App, Director, Executor, comparison,
// or fallback routes. It validates capability boundaries before reserving
// quota, then reserves concurrency, request, output-second, and estimated-cost
// budgets before the provider call. The reservation is conservative: once a
// provider call is attempted, window budgets remain consumed even on error.
func (s *MiniMaxH3GovernedSubmitter) Submit(ctx context.Context, request MiniMaxH3GovernedSubmitRequest) (MiniMaxH3GovernedSubmitResult, error) {
	if s == nil || s.client == nil {
		return MiniMaxH3GovernedSubmitResult{}, &MiniMaxH3AdmissionError{Code: MiniMaxH3AdmissionInvalidPolicy, Message: "governed submitter is not initialized"}
	}
	request.Scope = strings.TrimSpace(request.Scope)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if request.Scope == "" {
		return MiniMaxH3GovernedSubmitResult{}, &MiniMaxH3AdmissionError{Code: MiniMaxH3AdmissionInvalidRequest, Message: "scope is required"}
	}
	if request.IdempotencyKey == "" {
		return MiniMaxH3GovernedSubmitResult{}, &MiniMaxH3AdmissionError{Code: MiniMaxH3AdmissionIdempotencyRequired, Message: "idempotency_key is required"}
	}
	if len(request.IdempotencyKey) > miniMaxH3AdmissionIdempotencyKeyMaxLen {
		return MiniMaxH3GovernedSubmitResult{}, &MiniMaxH3AdmissionError{Code: MiniMaxH3AdmissionInvalidRequest, Message: "idempotency_key is too long"}
	}
	if strings.ContainsAny(request.Scope, "\x00\r\n") || strings.ContainsAny(request.IdempotencyKey, "\x00\r\n") {
		return MiniMaxH3GovernedSubmitResult{}, &MiniMaxH3AdmissionError{Code: MiniMaxH3AdmissionInvalidRequest, Message: "scope and idempotency_key cannot contain control separators"}
	}
	if err := validateMiniMaxH3Request(request.Request); err != nil {
		code := MiniMaxH3AdmissionInvalidRequest
		if errors.Is(err, ErrMiniMaxH3LastFrameOnlyNotEnabled) || errors.Is(err, ErrMiniMaxH3ReferenceAudioNotEnabled) {
			code = "provider_capability_not_enabled"
		}
		return MiniMaxH3GovernedSubmitResult{}, &MiniMaxH3AdmissionError{Code: code, Message: err.Error()}
	}
	fingerprint, err := miniMaxH3AdmissionFingerprint(request.Request)
	if err != nil {
		return MiniMaxH3GovernedSubmitResult{}, &MiniMaxH3AdmissionError{Code: MiniMaxH3AdmissionInvalidRequest, Message: err.Error()}
	}
	estimatedSeconds := request.Request.Duration
	estimatedCost, err := checkedMiniMaxH3EstimatedCost(estimatedSeconds, s.policy.EstimatedCostMicrosPerOutputSecond)
	if err != nil {
		return MiniMaxH3GovernedSubmitResult{}, &MiniMaxH3AdmissionError{Code: MiniMaxH3AdmissionInvalidPolicy, Message: err.Error()}
	}
	recordKey := request.Scope + "\x00" + request.IdempotencyKey
	now := s.now().UTC()

	s.mu.Lock()
	s.advanceWindowLocked(now)
	s.purgeIdempotencyLocked(now)
	if existing, ok := s.records[recordKey]; ok {
		if existing.fingerprint != fingerprint {
			snapshot := s.snapshotLocked(now)
			s.mu.Unlock()
			return rejectedMiniMaxH3GovernedResult(request, fingerprint, estimatedSeconds, estimatedCost, snapshot, MiniMaxH3AdmissionIdempotencyConflict, "idempotency_key was already used for a different request")
		}
		if existing.inFlight {
			snapshot := s.snapshotLocked(now)
			s.mu.Unlock()
			return rejectedMiniMaxH3GovernedResult(request, fingerprint, estimatedSeconds, estimatedCost, snapshot, MiniMaxH3AdmissionIdempotencyInFlight, "an identical request is already in progress")
		}
		result := existing.result
		result.Status = MiniMaxH3AdmissionReplay
		result.Replayed = true
		result.Admission = s.snapshotLocked(now)
		replayErr := existing.err
		s.mu.Unlock()
		return result, replayErr
	}
	if s.concurrent >= s.policy.MaxConcurrent {
		snapshot := s.snapshotLocked(now)
		s.mu.Unlock()
		return rejectedMiniMaxH3GovernedResult(request, fingerprint, estimatedSeconds, estimatedCost, snapshot, MiniMaxH3AdmissionConcurrentLimit, "MiniMax-H3 concurrent request limit reached")
	}
	if s.requestsUsed+1 > s.policy.MaxRequestsPerWindow {
		snapshot := s.snapshotLocked(now)
		s.mu.Unlock()
		return rejectedMiniMaxH3GovernedResult(request, fingerprint, estimatedSeconds, estimatedCost, snapshot, MiniMaxH3AdmissionRequestQuota, "MiniMax-H3 request quota reached for the current window")
	}
	if s.outputSecondsReserved+estimatedSeconds > s.policy.MaxOutputSecondsPerWindow {
		snapshot := s.snapshotLocked(now)
		s.mu.Unlock()
		return rejectedMiniMaxH3GovernedResult(request, fingerprint, estimatedSeconds, estimatedCost, snapshot, MiniMaxH3AdmissionOutputSecondsQuota, "MiniMax-H3 output-second quota reached for the current window")
	}
	if estimatedCost > s.policy.MaxEstimatedCostMicrosPerWindow || s.estimatedCostMicrosUsed > s.policy.MaxEstimatedCostMicrosPerWindow-estimatedCost {
		snapshot := s.snapshotLocked(now)
		s.mu.Unlock()
		return rejectedMiniMaxH3GovernedResult(request, fingerprint, estimatedSeconds, estimatedCost, snapshot, MiniMaxH3AdmissionEstimatedCostQuota, "MiniMax-H3 estimated cost quota reached for the current window")
	}

	s.concurrent++
	s.requestsUsed++
	s.outputSecondsReserved += estimatedSeconds
	s.estimatedCostMicrosUsed += estimatedCost
	s.records[recordKey] = &miniMaxH3AdmissionRecord{fingerprint: fingerprint, createdAt: now, inFlight: true}
	acceptedSnapshot := s.snapshotLocked(now)
	s.mu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, s.policy.ProviderRequestTimeout)
	providerResult, providerErr := s.client.CreateContentGenerationTask(requestCtx, request.Request)
	requestErr := requestCtx.Err()
	cancel()

	result := MiniMaxH3GovernedSubmitResult{
		Status: MiniMaxH3AdmissionAccepted, Scope: request.Scope, IdempotencyKey: request.IdempotencyKey,
		RequestFingerprintSHA256: fingerprint, EstimatedOutputSeconds: estimatedSeconds,
		EstimatedCostMicros: estimatedCost, ProviderResult: &providerResult, Admission: acceptedSnapshot,
	}
	var finalErr error
	if requestErr != nil {
		result.Status = "rejected"
		result.ErrorCode = MiniMaxH3AdmissionRequestCanceled
		if errors.Is(requestErr, context.DeadlineExceeded) {
			result.ErrorCode = MiniMaxH3AdmissionRequestTimeout
		}
		result.ErrorMessage = requestErr.Error()
		finalErr = &MiniMaxH3AdmissionError{Code: result.ErrorCode, Message: result.ErrorMessage}
	} else if providerErr != nil {
		result.Status = "rejected"
		result.ErrorCode = MiniMaxH3AdmissionProviderCallFailed
		result.ErrorMessage = providerErr.Error()
		finalErr = &MiniMaxH3AdmissionError{Code: result.ErrorCode, Message: result.ErrorMessage}
	} else if providerResult.Response == nil || strings.TrimSpace(providerResult.Response.ID) == "" {
		result.Status = "rejected"
		result.ErrorCode = MiniMaxH3AdmissionProviderTaskMissing
		result.ErrorMessage = "provider response is missing task_id"
		finalErr = &MiniMaxH3AdmissionError{Code: result.ErrorCode, Message: result.ErrorMessage}
	}

	completedAt := s.now().UTC()
	s.mu.Lock()
	if s.concurrent > 0 {
		s.concurrent--
	}
	s.advanceWindowLocked(completedAt)
	s.purgeIdempotencyLocked(completedAt)
	result.Admission = s.snapshotLocked(completedAt)
	if record := s.records[recordKey]; record != nil {
		record.inFlight = false
		record.completedAt = completedAt
		record.result = result
		record.err = finalErr
	}
	s.mu.Unlock()
	return result, finalErr
}

func (s *MiniMaxH3GovernedSubmitter) Snapshot() MiniMaxH3AdmissionSnapshot {
	if s == nil {
		return MiniMaxH3AdmissionSnapshot{}
	}
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.advanceWindowLocked(now)
	s.purgeIdempotencyLocked(now)
	return s.snapshotLocked(now)
}

func (s *MiniMaxH3GovernedSubmitter) advanceWindowLocked(now time.Time) {
	if s.windowStartedAt.IsZero() {
		s.windowStartedAt = now
	}
	if now.Before(s.windowStartedAt.Add(s.policy.Window)) {
		return
	}
	s.windowStartedAt = now
	s.requestsUsed = 0
	s.outputSecondsReserved = 0
	s.estimatedCostMicrosUsed = 0
}

func (s *MiniMaxH3GovernedSubmitter) purgeIdempotencyLocked(now time.Time) {
	for key, record := range s.records {
		if record == nil || record.inFlight {
			continue
		}
		reference := record.completedAt
		if reference.IsZero() {
			reference = record.createdAt
		}
		if !now.Before(reference.Add(s.policy.IdempotencyTTL)) {
			delete(s.records, key)
		}
	}
}

func (s *MiniMaxH3GovernedSubmitter) snapshotLocked(_ time.Time) MiniMaxH3AdmissionSnapshot {
	return MiniMaxH3AdmissionSnapshot{
		WindowStartedAt:             s.windowStartedAt,
		WindowEndsAt:                s.windowStartedAt.Add(s.policy.Window),
		Concurrent:                  s.concurrent,
		RequestsUsed:                s.requestsUsed,
		OutputSecondsReserved:       s.outputSecondsReserved,
		EstimatedCostMicrosReserved: s.estimatedCostMicrosUsed,
	}
}

func rejectedMiniMaxH3GovernedResult(request MiniMaxH3GovernedSubmitRequest, fingerprint string, seconds int, cost int64, snapshot MiniMaxH3AdmissionSnapshot, code string, message string) (MiniMaxH3GovernedSubmitResult, error) {
	result := MiniMaxH3GovernedSubmitResult{
		Status: "rejected", Scope: request.Scope, IdempotencyKey: request.IdempotencyKey,
		RequestFingerprintSHA256: fingerprint, EstimatedOutputSeconds: seconds,
		EstimatedCostMicros: cost, Admission: snapshot, ErrorCode: code, ErrorMessage: message,
	}
	return result, &MiniMaxH3AdmissionError{Code: code, Message: message}
}

func miniMaxH3AdmissionFingerprint(request ContentGenerationTaskRequest) (string, error) {
	normalized := request
	normalized.Model = MiniMaxH3Model
	normalized.Resolution = "2K"
	data, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("cannot fingerprint MiniMax-H3 request: %w", err)
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

func checkedMiniMaxH3EstimatedCost(seconds int, microsPerSecond int64) (int64, error) {
	if seconds <= 0 || microsPerSecond <= 0 {
		return 0, errors.New("estimated duration and cost rate must be greater than zero")
	}
	seconds64 := int64(seconds)
	const maxInt64 = int64(^uint64(0) >> 1)
	if microsPerSecond > maxInt64/seconds64 {
		return 0, errors.New("estimated MiniMax-H3 cost overflows int64")
	}
	return seconds64 * microsPerSecond, nil
}
