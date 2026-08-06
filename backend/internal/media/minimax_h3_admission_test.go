package media

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cascade-demoops/backend/internal/config"
)

type stubMiniMaxH3TaskCreator struct {
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
	result  ContentGenerationTaskResult
	err     error
	once    sync.Once
}

func (s *stubMiniMaxH3TaskCreator) CreateContentGenerationTask(ctx context.Context, _ ContentGenerationTaskRequest) (ContentGenerationTaskResult, error) {
	s.calls.Add(1)
	if s.started != nil {
		s.once.Do(func() { close(s.started) })
	}
	if s.release != nil {
		select {
		case <-ctx.Done():
			return ContentGenerationTaskResult{}, ctx.Err()
		case <-s.release:
		}
	}
	return s.result, s.err
}

func validMiniMaxH3AdmissionPolicy() MiniMaxH3AdmissionPolicy {
	return MiniMaxH3AdmissionPolicy{
		MaxConcurrent: 1, Window: time.Minute, MaxRequestsPerWindow: 2,
		MaxOutputSecondsPerWindow: 10, EstimatedCostMicrosPerOutputSecond: 100,
		MaxEstimatedCostMicrosPerWindow: 1000, ProviderRequestTimeout: time.Second,
		IdempotencyTTL: time.Hour,
	}
}

func validMiniMaxH3GovernedRequest(key string, duration int) MiniMaxH3GovernedSubmitRequest {
	return MiniMaxH3GovernedSubmitRequest{
		Scope: "test-project", IdempotencyKey: key,
		Request: ContentGenerationTaskRequest{
			Content:  []ContentPart{{Type: "text", Text: "Create a presentation-only transition."}},
			Duration: duration, Ratio: "16:9",
		},
	}
}

func successfulMiniMaxH3CreateResult(taskID string) ContentGenerationTaskResult {
	return ContentGenerationTaskResult{
		Mode: config.ArkMediaModeReal, Provider: config.ModelProvider("minimax-h3"), Model: MiniMaxH3Model,
		Response: &ContentGenerationTaskResponse{ID: taskID, Status: MiniMaxH3TaskQueued, Model: MiniMaxH3Model},
	}
}

func TestMiniMaxH3GovernedSubmitterRejectsUnpricedPolicy(t *testing.T) {
	policy := validMiniMaxH3AdmissionPolicy()
	policy.EstimatedCostMicrosPerOutputSecond = 0
	_, err := NewMiniMaxH3GovernedSubmitter(&stubMiniMaxH3TaskCreator{}, policy, nil)
	var admissionErr *MiniMaxH3AdmissionError
	if !errors.As(err, &admissionErr) || admissionErr.Code != MiniMaxH3AdmissionInvalidPolicy {
		t.Fatalf("err = %v", err)
	}
}

func TestMiniMaxH3GovernedSubmitterReservesBudgetAndReplaysIdempotently(t *testing.T) {
	creator := &stubMiniMaxH3TaskCreator{result: successfulMiniMaxH3CreateResult("task_1")}
	now := time.Date(2026, 8, 5, 6, 0, 0, 0, time.UTC)
	submitter, err := NewMiniMaxH3GovernedSubmitter(creator, validMiniMaxH3AdmissionPolicy(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	request := validMiniMaxH3GovernedRequest("idem-1", 5)
	first, err := submitter.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != MiniMaxH3AdmissionAccepted || first.EstimatedCostMicros != 500 || first.Admission.RequestsUsed != 1 || first.Admission.OutputSecondsReserved != 5 {
		t.Fatalf("first = %+v", first)
	}
	replay, err := submitter.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Status != MiniMaxH3AdmissionReplay || !replay.Replayed || creator.calls.Load() != 1 {
		t.Fatalf("replay=%+v provider_calls=%d", replay, creator.calls.Load())
	}
	if replay.Admission.RequestsUsed != 1 || replay.Admission.OutputSecondsReserved != 5 || replay.Admission.EstimatedCostMicrosReserved != 500 {
		t.Fatalf("replay admission = %+v", replay.Admission)
	}
}

func TestMiniMaxH3GovernedSubmitterRejectsIdempotencyConflict(t *testing.T) {
	creator := &stubMiniMaxH3TaskCreator{result: successfulMiniMaxH3CreateResult("task_1")}
	submitter, err := NewMiniMaxH3GovernedSubmitter(creator, validMiniMaxH3AdmissionPolicy(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := submitter.Submit(context.Background(), validMiniMaxH3GovernedRequest("idem-1", 4)); err != nil {
		t.Fatal(err)
	}
	result, err := submitter.Submit(context.Background(), validMiniMaxH3GovernedRequest("idem-1", 5))
	if err == nil || result.ErrorCode != MiniMaxH3AdmissionIdempotencyConflict || creator.calls.Load() != 1 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, creator.calls.Load())
	}
}

func TestMiniMaxH3GovernedSubmitterRejectsControlSeparators(t *testing.T) {
	creator := &stubMiniMaxH3TaskCreator{}
	submitter, err := NewMiniMaxH3GovernedSubmitter(creator, validMiniMaxH3AdmissionPolicy(), nil)
	if err != nil {
		t.Fatal(err)
	}
	request := validMiniMaxH3GovernedRequest("idem\x00other", 5)
	_, submitErr := submitter.Submit(context.Background(), request)
	var admissionErr *MiniMaxH3AdmissionError
	if !errors.As(submitErr, &admissionErr) || admissionErr.Code != MiniMaxH3AdmissionInvalidRequest || creator.calls.Load() != 0 {
		t.Fatalf("err=%v calls=%d", submitErr, creator.calls.Load())
	}
}

func TestMiniMaxH3GovernedSubmitterEnforcesConcurrentLimitBeforeProviderCall(t *testing.T) {
	creator := &stubMiniMaxH3TaskCreator{
		started: make(chan struct{}), release: make(chan struct{}), result: successfulMiniMaxH3CreateResult("task_1"),
	}
	submitter, err := NewMiniMaxH3GovernedSubmitter(creator, validMiniMaxH3AdmissionPolicy(), nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, submitErr := submitter.Submit(context.Background(), validMiniMaxH3GovernedRequest("idem-1", 4))
		done <- submitErr
	}()
	<-creator.started
	second, secondErr := submitter.Submit(context.Background(), validMiniMaxH3GovernedRequest("idem-2", 4))
	if secondErr == nil || second.ErrorCode != MiniMaxH3AdmissionConcurrentLimit || creator.calls.Load() != 1 {
		t.Fatalf("second=%+v err=%v calls=%d", second, secondErr, creator.calls.Load())
	}
	close(creator.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestMiniMaxH3GovernedSubmitterEnforcesOutputAndCostBudgets(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*MiniMaxH3AdmissionPolicy)
		duration  int
		errorCode string
	}{
		{
			name: "output seconds", duration: 6, errorCode: MiniMaxH3AdmissionOutputSecondsQuota,
			mutate: func(policy *MiniMaxH3AdmissionPolicy) { policy.MaxOutputSecondsPerWindow = 5 },
		},
		{
			name: "estimated cost", duration: 6, errorCode: MiniMaxH3AdmissionEstimatedCostQuota,
			mutate: func(policy *MiniMaxH3AdmissionPolicy) { policy.MaxEstimatedCostMicrosPerWindow = 100 },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			creator := &stubMiniMaxH3TaskCreator{result: successfulMiniMaxH3CreateResult("task_1")}
			policy := validMiniMaxH3AdmissionPolicy()
			test.mutate(&policy)
			submitter, err := NewMiniMaxH3GovernedSubmitter(creator, policy, nil)
			if err != nil {
				t.Fatal(err)
			}
			result, submitErr := submitter.Submit(context.Background(), validMiniMaxH3GovernedRequest("idem-1", test.duration))
			if submitErr == nil || result.ErrorCode != test.errorCode || creator.calls.Load() != 0 {
				t.Fatalf("result=%+v err=%v calls=%d", result, submitErr, creator.calls.Load())
			}
		})
	}
}

func TestMiniMaxH3GovernedSubmitterKeepsReservationAfterProviderFailure(t *testing.T) {
	creator := &stubMiniMaxH3TaskCreator{err: errors.New("provider unavailable")}
	policy := validMiniMaxH3AdmissionPolicy()
	policy.MaxRequestsPerWindow = 1
	submitter, err := NewMiniMaxH3GovernedSubmitter(creator, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, firstErr := submitter.Submit(context.Background(), validMiniMaxH3GovernedRequest("idem-1", 4))
	if firstErr == nil || first.ErrorCode != MiniMaxH3AdmissionProviderCallFailed {
		t.Fatalf("first=%+v err=%v", first, firstErr)
	}
	second, secondErr := submitter.Submit(context.Background(), validMiniMaxH3GovernedRequest("idem-2", 4))
	if secondErr == nil || second.ErrorCode != MiniMaxH3AdmissionRequestQuota || creator.calls.Load() != 1 {
		t.Fatalf("second=%+v err=%v calls=%d", second, secondErr, creator.calls.Load())
	}
}

func TestMiniMaxH3GovernedSubmitterRejectsCapabilityViolationBeforeReservation(t *testing.T) {
	creator := &stubMiniMaxH3TaskCreator{}
	submitter, err := NewMiniMaxH3GovernedSubmitter(creator, validMiniMaxH3AdmissionPolicy(), nil)
	if err != nil {
		t.Fatal(err)
	}
	request := validMiniMaxH3GovernedRequest("idem-1", 5)
	request.Request.Ratio = "adaptive"
	request.Request.Content = append(request.Request.Content, ContentPart{
		Type: "image_url", Role: "last_frame", ImageURL: &MediaURL{URL: "https://example.test/last.png"},
	})
	_, submitErr := submitter.Submit(context.Background(), request)
	var admissionErr *MiniMaxH3AdmissionError
	if !errors.As(submitErr, &admissionErr) || admissionErr.Code != "provider_capability_not_enabled" || creator.calls.Load() != 0 {
		t.Fatalf("err=%v calls=%d", submitErr, creator.calls.Load())
	}
	snapshot := submitter.Snapshot()
	if snapshot.RequestsUsed != 0 || snapshot.OutputSecondsReserved != 0 || snapshot.EstimatedCostMicrosReserved != 0 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestMiniMaxH3GovernedSubmitterTimesOutAndReleasesConcurrency(t *testing.T) {
	creator := &stubMiniMaxH3TaskCreator{release: make(chan struct{})}
	policy := validMiniMaxH3AdmissionPolicy()
	policy.ProviderRequestTimeout = time.Millisecond
	submitter, err := NewMiniMaxH3GovernedSubmitter(creator, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, submitErr := submitter.Submit(context.Background(), validMiniMaxH3GovernedRequest("idem-1", 4))
	if submitErr == nil || result.ErrorCode != MiniMaxH3AdmissionRequestTimeout {
		t.Fatalf("result=%+v err=%v", result, submitErr)
	}
	if snapshot := submitter.Snapshot(); snapshot.Concurrent != 0 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}
