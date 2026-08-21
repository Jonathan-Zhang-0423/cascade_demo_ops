package experiment

import (
	"context"
	"errors"
	"testing"
)

type scriptedLegAdapter struct {
	mode        string
	submissions *int
}

func (a scriptedLegAdapter) DescribeCapabilities(context.Context) (ModuleManifest, error) {
	return ModuleManifest{ModuleID: "execution-capture", Version: "1.0.0", Capabilities: []string{"async_build", "temporal_observation", "segmented_recording"}, SupportedReplay: []ReplayPolicy{ReplayObserveOnly, ReplayIdempotentWrite, ReplayOnceEffect}, SupportsSegmentedCapture: true}, nil
}

func (a scriptedLegAdapter) ExecuteLeg(_ context.Context, request LegExecutionRequest, emit func(LegExecutionUpdate) error) error {
	if request.Checkpoint != nil && confirmedEffect(request.Checkpoint, "submit") {
		return emit(LegExecutionUpdate{Kind: "artifacts", Summary: "resumed from verified segments", Artifacts: []ArtifactRef{{ArtifactID: "fact-track-resumed", Revision: 1, Role: "fact_track"}}})
	}
	if err := emit(LegExecutionUpdate{Kind: "phase", Phase: "request_submitted", Summary: "request prepared"}); err != nil {
		return err
	}
	if a.mode == "visual_unavailable" {
		return &AdapterError{Code: "visual_model_unavailable", Phase: "visual_observation_deferred", State: RunStateWaitingExternal, Retryable: true, Cause: errors.New("provider unavailable")}
	}
	if a.mode == "deadline" {
		return &AdapterError{Code: "observation_deadline_reached", Phase: "observation_deferred", State: RunStateWaitingInput, Retryable: true}
	}
	if a.mode == "build_failed" {
		return &AdapterError{Code: "explicit_terminal_build_failure", Phase: "terminal_failed", State: RunStateFailed, Retryable: false}
	}
	if err := emit(LegExecutionUpdate{Kind: "once_effect_started", EffectID: "submit", EffectKind: "target_submission", IdempotencyKey: "submit-idempotency-001"}); err != nil {
		return err
	}
	*a.submissions++
	if a.mode == "unknown_after_click" {
		return &AdapterError{Code: "once_effect_result_unknown", EffectID: "submit", EvidenceRefs: []string{"click-evidence"}, Cause: errors.New("connection lost before result")}
	}
	if err := emit(LegExecutionUpdate{Kind: "once_effect_committed", EffectID: "submit", StateFingerprintRef: "artifact:fingerprint:after", ResultEntryRef: "artifact:result:entry", EvidenceRefs: []string{"result-evidence"}, SegmentRefs: []ArtifactRef{{ArtifactID: "segment-before-restart", Revision: 1, Role: "recording_segment"}}}); err != nil {
		return err
	}
	if a.mode == "worker_restart" {
		return &AdapterError{Code: "worker_restart_injected", Phase: "worker_restart_recovery", State: RunStateWaitingExternal, Retryable: true}
	}
	return emit(LegExecutionUpdate{Kind: "artifacts", Artifacts: []ArtifactRef{{ArtifactID: "fact-track", Revision: 1, Role: "fact_track"}}})
}

func TestRunnerRecoveryAfterCommittedWorkerRestartDoesNotResubmit(t *testing.T) {
	service := testService(t)
	runner, err := NewRunner(service)
	if err != nil {
		t.Fatal(err)
	}
	run := mustCreateRun(t, service)
	submissions := 0
	interrupted, runErr := runner.RunLeg(t.Context(), run.RunID, run.Legs[0].LegID, scriptedLegAdapter{mode: "worker_restart", submissions: &submissions})
	if runErr == nil || interrupted.State != RunStateWaitingExternal || submissions != 1 || interrupted.Legs[0].Checkpoint == nil {
		t.Fatalf("restart injection did not preserve a committed checkpoint: run=%+v submissions=%d err=%v", interrupted, submissions, runErr)
	}
	resumed, err := service.Resume(t.Context(), run.RunID, interrupted.Revision)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := runner.RunLeg(t.Context(), run.RunID, resumed.Legs[0].LegID, scriptedLegAdapter{mode: "normal", submissions: &submissions})
	if err != nil {
		t.Fatal(err)
	}
	if submissions != 1 || completed.Legs[0].TargetSubmissions != 1 || completed.Legs[0].State != RunStateSucceeded || completed.Legs[0].BrowserAttempt != 2 {
		t.Fatalf("resume replayed submission or lost continuity: run=%+v submissions=%d", completed, submissions)
	}
}

func TestStartedOnceEffectWithPersistedExternalTaskResumesSameTask(t *testing.T) {
	service := testService(t)
	run := mustCreateRun(t, service)
	legID := run.Legs[0].LegID
	run, err := service.TransitionLeg(t.Context(), run.RunID, LegTransitionRequest{ExpectedRevision: run.Revision, LegID: legID, State: RunStateRunning, Phase: "target_submission", EventType: "module_started", Summary: "started"})
	if err != nil {
		t.Fatal(err)
	}
	run, err = service.BeginOnceEffect(t.Context(), run.RunID, run.Revision, legID, "submit", "target_submission", "submit-idempotency-001")
	if err != nil {
		t.Fatal(err)
	}
	run, err = service.BindOnceEffectExternalTask(t.Context(), run.RunID, run.Revision, legID, "submit", "direct:project-one:job-one", []string{"job-one"})
	if err != nil {
		t.Fatal(err)
	}
	run, err = service.TransitionLeg(t.Context(), run.RunID, LegTransitionRequest{ExpectedRevision: run.Revision, LegID: legID, State: RunStateWaitingExternal, Phase: "worker_restarting", EventType: "module_interrupted", Summary: "restart"})
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := service.Resume(t.Context(), run.RunID, run.Revision)
	if err != nil {
		t.Fatal(err)
	}
	record := resumed.Legs[0].Checkpoint.OnceEffects[0]
	if resumed.State != RunStateQueued || record.Status != "started" || record.ExternalTaskRef != "direct:project-one:job-one" {
		t.Fatalf("resume must preserve and reuse the admitted external task: %+v", resumed)
	}
}

func TestRunnerFaultPoliciesAreDeterministic(t *testing.T) {
	for _, test := range []struct {
		name, mode string
		state      RunState
		phase      string
	}{
		{"unknown click", "unknown_after_click", RunStateWaitingInput, "once_effect_uncertain"},
		{"visual unavailable", "visual_unavailable", RunStateWaitingExternal, "visual_observation_deferred"},
		{"deadline", "deadline", RunStateWaitingInput, "observation_deferred"},
		{"terminal failure", "build_failed", RunStateFailed, "terminal_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := testService(t)
			runner, _ := NewRunner(service)
			run := mustCreateRun(t, service)
			submissions := 0
			result, err := runner.RunLeg(t.Context(), run.RunID, run.Legs[0].LegID, scriptedLegAdapter{mode: test.mode, submissions: &submissions})
			if err == nil || result.State != test.state || result.Phase != test.phase {
				t.Fatalf("fault projection mismatch: run=%+v err=%v", result, err)
			}
			if test.mode == "unknown_after_click" && (result.Legs[0].Checkpoint == nil || result.Legs[0].Checkpoint.OnceEffects[0].Status != "uncertain") {
				t.Fatalf("unknown result was not persisted as uncertain: %+v", result.Legs[0])
			}
		})
	}
}

func confirmedEffect(checkpoint *Checkpoint, effectID string) bool {
	if checkpoint == nil {
		return false
	}
	for _, effect := range checkpoint.OnceEffects {
		if effect.EffectID == effectID && effect.Status == "confirmed" {
			return true
		}
	}
	return false
}
