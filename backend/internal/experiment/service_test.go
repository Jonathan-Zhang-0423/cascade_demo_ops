package experiment

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreateRunFreezesTwoLegsAndIsIdempotent(t *testing.T) {
	service := testService(t)
	request := testCreateRequest()
	run, err := service.CreateRun(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunStateQueued || run.Phase != "product_spec_frozen" || len(run.Legs) != 2 || run.Legs[0].Kind != "main" || run.Legs[1].Kind != "recovery" {
		t.Fatalf("unexpected frozen run: %+v", run)
	}
	if run.Legs[0].ProjectName == run.Legs[1].ProjectName || run.Legs[0].BuildPrompt != request.UserGoal || run.Legs[0].BuildPrompt != run.Legs[1].BuildPrompt {
		t.Fatalf("main and recovery identities were not kept distinct: %+v", run.Legs)
	}
	if !strings.Contains(run.Legs[0].ProjectName, "·") || run.UserGoal != request.UserGoal {
		t.Fatalf("real-run project identity and one-sentence goal were not kept separate: %+v", run.Legs[0])
	}
	for _, term := range targetPromptForbiddenTerms {
		if strings.Contains(strings.ToLower(run.Legs[0].BuildPrompt), strings.ToLower(term)) {
			t.Fatalf("target prompt leaked internal term %q: %s", term, run.Legs[0].BuildPrompt)
		}
	}
	again, err := service.CreateRun(context.Background(), request)
	if err != nil || again.RunID != run.RunID {
		t.Fatalf("idempotent create returned a new run: run=%+v err=%v", again, err)
	}
	request.TargetURL = "https://other.example.test/app"
	if _, err := service.CreateRun(context.Background(), request); err == nil {
		t.Fatal("idempotency key reuse for a different target must fail")
	}
}

func TestConfirmedOnceEffectResumesObserveOnlyWithoutReplay(t *testing.T) {
	service := testService(t)
	run := mustCreateRun(t, service)
	run = mustTransitionLeg(t, service, run, run.Legs[0].LegID, RunStateRunning, "creating_target")
	legID := run.Legs[0].LegID
	started, err := service.BeginOnceEffect(context.Background(), run.RunID, run.Revision, legID, "submit_build", "target_submission", "idem-submit-001")
	if err != nil {
		t.Fatal(err)
	}
	committed, err := service.CommitOnceEffect(context.Background(), run.RunID, CommitOnceEffectRequest{
		ExpectedRevision: started.Revision, LegID: legID, EffectID: "submit_build", StateFingerprintRef: "artifact://fingerprint/main", ResultEntryRef: "artifact://result/entry",
		EvidenceRefs: []string{"evidence_submit_result"}, SegmentRefs: []ArtifactRef{{ArtifactID: "artifact_segment_before", Revision: 1, Role: "recording_segment"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if committed.Legs[0].TargetSubmissions != 1 || committed.Legs[0].Checkpoint.OnceEffects[0].Status != "confirmed" {
		t.Fatalf("once-effect was not committed: %+v", committed.Legs[0])
	}
	if _, err := service.BeginOnceEffect(context.Background(), run.RunID, committed.Revision, legID, "submit_build", "target_submission", "idem-submit-001"); err == nil {
		t.Fatal("confirmed once-effect must never start again")
	}
	resumed, err := service.Resume(context.Background(), run.RunID, committed.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Legs[0].Phase != "resume_observe_only" || resumed.Legs[0].BrowserAttempt != 2 || resumed.Legs[0].TargetSubmissions != 1 {
		t.Fatalf("confirmed recovery replayed or lost the checkpoint: %+v", resumed.Legs[0])
	}
}

func TestConfirmedCheckpointAdvancesObservationEntryWithoutCountingAnotherSubmission(t *testing.T) {
	service := testService(t)
	run := mustCreateRun(t, service)
	run = mustTransitionLeg(t, service, run, run.Legs[0].LegID, RunStateRunning, "creating_target")
	legID := run.Legs[0].LegID
	started, err := service.BeginOnceEffect(t.Context(), run.RunID, run.Revision, legID, "target_submit", "target_submission", "idem-advance-entry")
	if err != nil {
		t.Fatal(err)
	}
	committed, err := service.CommitOnceEffect(t.Context(), run.RunID, CommitOnceEffectRequest{
		ExpectedRevision: started.Revision, LegID: legID, EffectID: "target_submit", StateFingerprintRef: "result:initial",
		ResultEntryRef: "direct:project-one:job-initial", EvidenceRefs: []string{"job-initial"},
	})
	if err != nil {
		t.Fatal(err)
	}
	advanced, err := service.AdvanceCheckpointResultEntry(t.Context(), run.RunID, committed.Revision, legID, "direct:project-one:job-observe-two", []string{"job-initial", "job-observe-two"})
	if err != nil {
		t.Fatal(err)
	}
	if advanced.Legs[0].Checkpoint.ResultEntryRef != "direct:project-one:job-observe-two" || advanced.Legs[0].TargetSubmissions != 1 || advanced.Legs[0].Checkpoint.OnceEffects[0].Status != "confirmed" {
		t.Fatalf("observation entry advance changed once-effect semantics: %+v", advanced.Legs[0])
	}
}

func TestUncertainOnceEffectDefersInsteadOfReplaying(t *testing.T) {
	service := testService(t)
	run := mustCreateRun(t, service)
	run = mustTransitionLeg(t, service, run, run.Legs[0].LegID, RunStateRunning, "creating_target")
	started, err := service.BeginOnceEffect(context.Background(), run.RunID, run.Revision, run.Legs[0].LegID, "submit_build", "target_submission", "idem-submit-002")
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := service.Resume(context.Background(), run.RunID, started.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.State != RunStateWaitingInput || resumed.Phase != "once_effect_uncertain" || resumed.Waiting == nil || resumed.Legs[0].TargetSubmissions != 0 {
		t.Fatalf("uncertain effect did not fail closed: %+v", resumed)
	}
}

func TestResumeBoundExternalTaskSelectsWaitingMainLeg(t *testing.T) {
	service := testService(t)
	run := mustCreateRun(t, service)
	run = mustTransitionLeg(t, service, run, run.Legs[0].LegID, RunStateRunning, "target_submission")
	legID := run.Legs[0].LegID
	started, err := service.BeginOnceEffect(context.Background(), run.RunID, run.Revision, legID, "target_submit", "target_submission", "idem-bound-001")
	if err != nil {
		t.Fatal(err)
	}
	bound, err := service.BindOnceEffectExternalTask(context.Background(), run.RunID, started.Revision, legID, "target_submit", "direct:project-one:job-one", []string{"job-one"})
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := service.TransitionLeg(context.Background(), run.RunID, LegTransitionRequest{ExpectedRevision: bound.Revision, LegID: legID, State: RunStateWaitingInput, Phase: "observation_deferred", EventType: "module_interrupted", Summary: "waiting for external result"})
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := service.Resume(context.Background(), run.RunID, waiting.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Legs[0].State != RunStateQueued || resumed.Legs[0].Phase != "resume_external_task" || resumed.Legs[0].BrowserAttempt != 2 {
		t.Fatalf("bound main leg was not resumed: %+v", resumed.Legs)
	}
	if resumed.Legs[1].State != RunStateCreated || resumed.Legs[1].Phase != "awaiting_main_completion" {
		t.Fatalf("unreached recovery leg was scheduled instead of main: %+v", resumed.Legs)
	}
}

func TestExplicitAdaptiveReconcileRefreshesObservationBudgetForWaitingRun(t *testing.T) {
	service := testService(t)
	loaded, err := LoadDefinition(service.definitionRoot, "2048-v2")
	if err != nil || loaded.ObservationPlan.DeferAfterMS != 600_000 {
		t.Fatalf("updated observation definition did not load: plan=%+v err=%v", loaded.ObservationPlan, err)
	}
	request := testCreateRequest()
	request.HarnessProfile = HarnessProfileAdaptiveBusinessV1
	run, err := service.CreateRun(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	run = mustTransitionLeg(t, service, run, run.Legs[0].LegID, RunStateRunning, "target_submission")
	legID := run.Legs[0].LegID
	started, err := service.BeginOnceEffect(t.Context(), run.RunID, run.Revision, legID, "target_submit", "target_submission", "idem-refresh-plan")
	if err != nil {
		t.Fatal(err)
	}
	bound, err := service.BindOnceEffectExternalTask(t.Context(), run.RunID, started.Revision, legID, "target_submit", "direct:project-one:job-one", []string{"job-one"})
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := service.TransitionLeg(t.Context(), run.RunID, LegTransitionRequest{ExpectedRevision: bound.Revision, LegID: legID, State: RunStateWaitingInput, Phase: "observation_deferred", EventType: "module_interrupted", Summary: "waiting"})
	if err != nil {
		t.Fatal(err)
	}
	stale := waiting
	stale.ObservationPlan.DeferAfterMS = 300_000
	stale.Revision++
	stale.UpdatedAt = service.now().UTC()
	if err := service.store.Transition(t.Context(), run.RunID, waiting.Revision, stale, service.event(stale, "stale_plan_fixture", "simulate an older frozen plan", legID, nil)); err != nil {
		t.Fatal(err)
	}
	resumed, err := service.Resume(t.Context(), run.RunID, stale.Revision, "reconcile_observed_state")
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ObservationPlan.DeferAfterMS != 600_000 || resumed.Legs[0].Phase != "resume_external_task" {
		t.Fatalf("explicit adaptive reconciliation did not refresh the observation plan: %+v", resumed.ObservationPlan)
	}
}

func TestReconcileObservedStateCreatesRevisionFromFailedBoundRun(t *testing.T) {
	service := testService(t)
	run := mustCreateRun(t, service)
	run = mustTransitionLeg(t, service, run, run.Legs[0].LegID, RunStateRunning, "target_submission")
	legID := run.Legs[0].LegID
	started, err := service.BeginOnceEffect(t.Context(), run.RunID, run.Revision, legID, "target_submit", "target_submission", "idem-reconcile-001")
	if err != nil {
		t.Fatal(err)
	}
	bound, err := service.BindOnceEffectExternalTask(t.Context(), run.RunID, started.Revision, legID, "target_submit", "direct:project-one:job-one", []string{"job-one"})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := service.TransitionLeg(t.Context(), run.RunID, LegTransitionRequest{ExpectedRevision: bound.Revision, LegID: legID, State: RunStateFailed, Phase: "terminal_failed", EventType: "module_failed", Summary: "fixed-step harness failed after successor navigation"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Resume(t.Context(), run.RunID, failed.Revision); err == nil {
		t.Fatal("a failed run must remain terminal without an explicit reconciliation strategy")
	}
	reconciled, err := service.Resume(t.Context(), run.RunID, failed.Revision, "reconcile_observed_state")
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.State != RunStateQueued || reconciled.Phase != "reconcile_observed_state" || reconciled.Legs[0].State != RunStateQueued || reconciled.Legs[0].BrowserAttempt != 2 {
		t.Fatalf("failed externally-bound run was not revisioned for observation-only reconciliation: %+v", reconciled)
	}
	if reconciled.Legs[0].Checkpoint.OnceEffects[0].ExternalTaskRef != "direct:project-one:job-one" || reconciled.Legs[0].TargetSubmissions != 0 {
		t.Fatalf("reconciliation lost the existing external binding or fabricated a second submission: %+v", reconciled.Legs[0])
	}
	if reconciled.HarnessProfile != HarnessProfileAdaptiveBusinessV1 || reconciled.Budget.VisualCallsPerRun != 2 || len(reconciled.InteractionPlan.Steps) != 7 {
		t.Fatalf("reconciliation did not refresh the versioned adaptive contract: profile=%q budget=%+v steps=%d", reconciled.HarnessProfile, reconciled.Budget, len(reconciled.InteractionPlan.Steps))
	}
}

func TestCancelTerminatesRunWithoutCreatingEffects(t *testing.T) {
	service := testService(t)
	run := mustCreateRun(t, service)
	run = mustTransitionLeg(t, service, run, run.Legs[0].LegID, RunStateRunning, "plan_review")
	run = mustTransitionLeg(t, service, run, run.Legs[0].LegID, RunStateWaitingExternal, "plan_review")

	canceled, err := service.Cancel(t.Context(), run.RunID, run.Revision, "pre-submit package gate failed")
	if err != nil {
		t.Fatal(err)
	}
	if canceled.State != RunStateCanceled || canceled.Phase != "canceled" || canceled.Legs[0].State != RunStateCanceled || canceled.Legs[1].State != RunStateCanceled {
		t.Fatalf("run was not fully canceled: %+v", canceled)
	}
	if canceled.Legs[0].Checkpoint != nil || canceled.Legs[0].TargetSubmissions != 0 || canceled.ProviderCallsUsed != 0 || canceled.Report == nil || canceled.Report.Valid {
		t.Fatalf("cancel created or concealed experiment effects: %+v", canceled)
	}
	if _, err := service.Resume(t.Context(), canceled.RunID, canceled.Revision); err == nil {
		t.Fatal("canceled run must not be resumable")
	}
}

func TestVisualCallBudgetIsHardBound(t *testing.T) {
	service := testService(t)
	run := mustCreateRun(t, service)
	legID := run.Legs[0].LegID
	for index := 0; index < run.Budget.VisualCallsPerRun; index++ {
		var err error
		run, err = service.RecordVisualCall(context.Background(), run.RunID, run.Revision, legID, []string{"evidence_visual"})
		if err != nil {
			t.Fatalf("visual call %d failed: %v", index+1, err)
		}
	}
	if _, err := service.RecordVisualCall(context.Background(), run.RunID, run.Revision, legID, []string{"evidence_over_budget"}); err == nil {
		t.Fatal("visual call beyond the frozen budget must fail")
	}
}

func TestFinalFilmBindingIsUniqueAndProviderConsumptionIsMonotonic(t *testing.T) {
	service := testService(t)
	run := mustCreateRun(t, service)
	bound, err := service.BindFinalFilm(t.Context(), run.RunID, run.Revision, "film-main", 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.BindFinalFilm(t.Context(), run.RunID, bound.Revision, "film-other", 1, 2); err == nil {
		t.Fatal("a second FinalFilm job must be rejected")
	}
	if _, err := service.BindFinalFilm(t.Context(), run.RunID, bound.Revision, "film-main", 4, 1); err == nil {
		t.Fatal("provider consumption must not decrease")
	}
	updated, err := service.BindFinalFilm(t.Context(), run.RunID, bound.Revision, "film-main", 4, 4)
	if err != nil || updated.ProviderCallsUsed != 4 || updated.FinalFilm.Revision != 4 {
		t.Fatalf("same job progress binding failed: run=%+v err=%v", updated, err)
	}
}

func TestOutOfBandHumanActionInvalidatesAttribution(t *testing.T) {
	service := testService(t)
	run := mustCreateRun(t, service)
	invalid, err := service.RecordHumanIntervention(t.Context(), run.RunID, run.Revision, run.Legs[0].LegID, "manual_browser_input", false)
	if err != nil {
		t.Fatal(err)
	}
	if invalid.State != RunStateFailed || invalid.Report == nil || invalid.Report.Valid || invalid.Report.Automation.OutOfBandBrowserActions != 1 {
		t.Fatalf("out-of-band input did not invalidate the run: %+v", invalid)
	}
}

func TestLegArtifactsAreVersionedAndDeduplicated(t *testing.T) {
	service := testService(t)
	run := mustCreateRun(t, service)
	artifact := ArtifactRef{ArtifactID: "artifact-keyframe-1", Revision: 1, Role: "temporal_keyframe"}
	first, err := service.AppendLegArtifacts(t.Context(), run.RunID, run.Revision, run.Legs[0].LegID, []ArtifactRef{artifact}, "keyframe_materialized", "keyframe stored")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.AppendLegArtifacts(t.Context(), run.RunID, first.Revision, run.Legs[0].LegID, []ArtifactRef{artifact}, "keyframe_reobserved", "keyframe already stored")
	if err != nil || len(second.Legs[0].ArtifactRefs) != 1 {
		t.Fatalf("artifact append was not idempotent: run=%+v err=%v", second, err)
	}
}

func TestFileStoreRejectsOversizedInlineEvent(t *testing.T) {
	service := testService(t)
	run := mustCreateRun(t, service)
	next := run
	next.Revision++
	next.UpdatedAt = next.UpdatedAt.Add(time.Second)
	event := Event{SchemaVersion: EventSchemaVersion, EventID: "event_oversized", RunID: run.RunID, Type: "bad_inline_evidence", State: run.State, Phase: run.Phase, Summary: strings.Repeat("x", MaxEventBodyBytes), CreatedAt: next.UpdatedAt}
	if err := service.store.Transition(context.Background(), run.RunID, run.Revision, next, event); err == nil {
		t.Fatal("oversized event body must be rejected")
	}
}

func testService(t *testing.T) *Service {
	t.Helper()
	nextID := 0
	service, err := NewService(ServiceOptions{
		Store: NewFileStore(t.TempDir()), DefinitionRoot: filepath.Join("..", "..", "..", "experiments"),
		Now: func() time.Time { return time.Date(2026, 8, 21, 10, 0, nextID, 0, time.UTC) },
		NewID: func(prefix string) (string, error) {
			nextID++
			return prefix + "_test_" + strings.Repeat("x", nextID), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func testCreateRequest() CreateRunRequest {
	return CreateRunRequest{DefinitionRef: "2048-v2", UserGoal: "Create a polished responsive product from this single sentence.", TargetURL: "https://target.example.test/app", CredentialRef: "secret://demo/account", AuthorizationRef: "approval://experiment/start", IdempotencyKey: "experiment-idem-001"}
}

func mustCreateRun(t *testing.T, service *Service) Run {
	t.Helper()
	run, err := service.CreateRun(context.Background(), testCreateRequest())
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func mustTransitionLeg(t *testing.T, service *Service, run Run, legID string, state RunState, phase string) Run {
	t.Helper()
	next, err := service.TransitionLeg(context.Background(), run.RunID, LegTransitionRequest{ExpectedRevision: run.Revision, LegID: legID, State: state, Phase: phase, EventType: "test_transition", Summary: "test transition"})
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func TestStoreRevisionConflict(t *testing.T) {
	service := testService(t)
	run := mustCreateRun(t, service)
	_, err := service.Resume(context.Background(), run.RunID, run.Revision+1)
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("expected revision conflict, got %v", err)
	}
}
