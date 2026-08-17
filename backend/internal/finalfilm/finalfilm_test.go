package finalfilm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

func TestCompileStoryboardConstraintsLocksRequiredFactTrack(t *testing.T) {
	catalog, plan := finalFilmFixture()
	intent, err := model.PresentationGenerationIntentDefaults("intro_1", "intro", []string{"shot_1"})
	if err != nil {
		t.Fatal(err)
	}
	set, err := CompileStoryboardConstraints(ConstraintCompileInput{
		SourcePackageID: "package_1", Catalog: catalog, BaselinePlan: plan, Intents: []model.PresentationGenerationIntent{intent},
		Canvas: model.RenderCanvas{Width: 1920, Height: 1080, FPS: 30, Format: "mp4"}, Now: time.Unix(100, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.RequiredStepOrder) != 2 || set.RequiredStepOrder[0] != "step_1" || set.RequiredStepOrder[1] != "step_2" {
		t.Fatalf("unexpected required order: %+v", set.RequiredStepOrder)
	}
	if set.RequiredStepCoverage["step_1"].SourceTimeRangeMS == nil || set.RequiredStepCoverage["step_1"].SourceTimeRangeMS[1] != 2000 {
		t.Fatalf("missing locked timing: %+v", set.RequiredStepCoverage["step_1"])
	}
	if len(set.PresentationSlots) != 1 || set.PresentationSlots[0].Required || !set.GeneratedTrackPolicy.Optional {
		t.Fatalf("unsafe presentation slot: %+v", set.PresentationSlots)
	}
	if err := model.ValidateStoryboardConstraintSet(set); err != nil {
		t.Fatal(err)
	}
}

func TestCompileStoryboardConstraintsRejectsMissingRequiredPlanShot(t *testing.T) {
	catalog, plan := finalFilmFixture()
	plan.Shots = plan.Shots[:1]
	_, err := CompileStoryboardConstraints(ConstraintCompileInput{SourcePackageID: "package_1", Catalog: catalog, BaselinePlan: plan, Now: time.Now()})
	if err == nil {
		t.Fatal("expected required-step coverage rejection")
	}
}

func TestFinalFilmBaselineFirstWaitsForExplicitGenerationApproval(t *testing.T) {
	service, renderer := newFinalFilmTestService(t)
	catalog, plan := finalFilmFixture()
	intent, _ := model.PresentationGenerationIntentDefaults("intro_1", "intro", []string{"shot_1"})
	job, err := service.CreateJob(context.Background(), CreateJobRequest{
		EditorSessionID: "editor_1", EditorRevision: 3, SourcePackageID: "package_1", Catalog: catalog, BaselinePlan: plan,
		Intents: []model.PresentationGenerationIntent{intent}, RenderProfile: finalFilmProfile(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if renderer.renderCalls != 0 {
		t.Fatal("job creation must not render or call a provider")
	}
	job, err = service.RunBaseline(context.Background(), job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != model.FinalFilmJobAwaitingGenerationApproval || job.GenerationAuthorized || job.BaselineRender.VideoPath == "" || job.FinalRender.VideoPath != "" {
		t.Fatalf("unexpected baseline-first state: %+v", job)
	}
	if renderer.renderCalls != 1 {
		t.Fatalf("render calls = %d", renderer.renderCalls)
	}
	events, err := service.ListEvents(context.Background(), job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("events = %+v", events)
	}
	for index, event := range events {
		if event.Sequence != index+1 {
			t.Fatalf("event sequence is not contiguous: %+v", events)
		}
	}
}

func TestFinalFilmNoGenerationIntentCompletesFromBaseline(t *testing.T) {
	service, renderer := newFinalFilmTestService(t)
	catalog, plan := finalFilmFixture()
	job, err := service.CreateJob(context.Background(), CreateJobRequest{
		EditorSessionID: "editor_1", EditorRevision: 1, SourcePackageID: "package_1", Catalog: catalog, BaselinePlan: plan, RenderProfile: finalFilmProfile(),
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err = service.RunBaseline(context.Background(), job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != model.FinalFilmJobCompleted || job.FinalRender.VideoPath != job.BaselineRender.VideoPath || renderer.renderCalls != 1 {
		t.Fatalf("unexpected fact-only completion: %+v", job)
	}
}

func TestFinalFilmGenerationFailureFallsBackToDurableBaseline(t *testing.T) {
	service, _ := newFinalFilmTestService(t)
	catalog, plan := finalFilmFixture()
	intent, _ := model.PresentationGenerationIntentDefaults("intro_1", "intro", nil)
	job, err := service.CreateJob(context.Background(), CreateJobRequest{
		EditorSessionID: "editor_1", EditorRevision: 1, SourcePackageID: "package_1", Catalog: catalog, BaselinePlan: plan,
		Intents: []model.PresentationGenerationIntent{intent}, RenderProfile: finalFilmProfile(),
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err = service.RunBaseline(context.Background(), job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	job, err = service.SubmitDirectorPlan(context.Background(), job.JobID, job.Revision, finalFilmDirectorPlan(job, intent))
	if err != nil {
		t.Fatal(err)
	}
	job, err = service.DecideGeneration(context.Background(), job.JobID, job.Revision, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != model.FinalFilmJobGeneratingCandidates || !job.GenerationAuthorized {
		t.Fatalf("generation was not explicitly authorized: %+v", job)
	}
	job, err = service.RunGeneratedCandidates(context.Background(), job.JobID, job.Revision, media.GeneratedShotProviderMiniMaxH3)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != model.FinalFilmJobCompletedWithoutGenerated || job.FinalRender.VideoPath != job.BaselineRender.VideoPath || job.GenerationSkipReason == "" {
		t.Fatalf("baseline fallback failed: %+v", job)
	}
	record, err := decodeGeneratedTrack(job.GeneratedTrack)
	if err != nil || len(record.Executions) != 1 || record.Executions[0].ErrorClass != "provider_selection_failed" {
		t.Fatalf("provider failure audit was not persisted: record=%+v err=%v", record, err)
	}
}

func TestGenerationApprovalRejectsMissingDirectorPlan(t *testing.T) {
	service, _ := newFinalFilmTestService(t)
	catalog, plan := finalFilmFixture()
	intent, _ := model.PresentationGenerationIntentDefaults("intro_1", "intro", nil)
	job, err := service.CreateJob(context.Background(), CreateJobRequest{
		EditorSessionID: "editor_1", EditorRevision: 1, SourcePackageID: "package_1", Catalog: catalog, BaselinePlan: plan,
		Intents: []model.PresentationGenerationIntent{intent}, RenderProfile: finalFilmProfile(),
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err = service.RunBaseline(context.Background(), job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.DecideGeneration(context.Background(), job.JobID, job.Revision, true, ""); err == nil {
		t.Fatal("expected approval without a persisted Director plan to fail")
	}
}

func TestPlanDirectorGeneratedShotsPersistsPlannerOutputAtExpectedRevision(t *testing.T) {
	service, _ := newFinalFilmTestService(t)
	catalog, plan := finalFilmFixture()
	intent, _ := model.PresentationGenerationIntentDefaults("intro_planner", "intro", nil)
	job, err := service.CreateJob(context.Background(), CreateJobRequest{
		EditorSessionID: "editor_planner", EditorRevision: 1, SourcePackageID: "package_planner", Catalog: catalog, BaselinePlan: plan,
		Intents: []model.PresentationGenerationIntent{intent}, RenderProfile: finalFilmProfile(),
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err = service.RunBaseline(context.Background(), job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	planner := &recordingDirectorPlanner{}
	service.planner = planner
	previousRevision := job.Revision
	job, err = service.PlanDirectorGeneratedShots(context.Background(), job.JobID, previousRevision)
	if err != nil {
		t.Fatal(err)
	}
	if planner.calls != 1 || planner.request.JobID != job.JobID || planner.request.Constraints.ConstraintSetID != job.Constraints.ConstraintSetID {
		t.Fatalf("planner did not receive the locked job inputs: %+v", planner)
	}
	if job.Revision != previousRevision+1 || job.DirectorPlan == nil || job.Phase != "director_generated_shot_plan_ready" {
		t.Fatalf("planner output was not persisted through the normal Director-plan transition: %+v", job)
	}
	if _, err := service.PlanDirectorGeneratedShots(context.Background(), job.JobID, previousRevision); err == nil {
		t.Fatal("expected stale Director planning revision to be rejected")
	}
}

func TestGeneratedCandidateRequiresHumanReviewSelectionAndEditorApprovalBeforePatch(t *testing.T) {
	service, _ := newFinalFilmTestService(t)
	adapter := &fakeGeneratedShotProvider{root: t.TempDir()}
	registry := media.NewGeneratedShotProviderRegistry()
	if err := registry.Register(adapter); err != nil {
		t.Fatal(err)
	}
	service.providers = registry
	catalog, plan := finalFilmFixture()
	intent, _ := model.PresentationGenerationIntentDefaults("intro_review", "intro", nil)
	job, err := service.CreateJob(context.Background(), CreateJobRequest{
		EditorSessionID: "editor_review", EditorRevision: 1, SourcePackageID: "package_review", Catalog: catalog, BaselinePlan: plan,
		Intents: []model.PresentationGenerationIntent{intent}, RenderProfile: finalFilmProfile(),
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err = service.RunBaseline(context.Background(), job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	job, err = service.SubmitDirectorPlan(context.Background(), job.JobID, job.Revision, finalFilmDirectorPlan(job, intent))
	if err != nil {
		t.Fatal(err)
	}
	job, err = service.DecideGeneration(context.Background(), job.JobID, job.Revision, true, "")
	if err != nil {
		t.Fatal(err)
	}
	job, err = service.RunGeneratedCandidates(context.Background(), job.JobID, job.Revision, media.GeneratedShotProviderMiniMaxH3)
	if err != nil || job.State != model.FinalFilmJobAwaitingContentReview {
		t.Fatalf("candidate generation did not stop at content review: job=%+v err=%v", job, err)
	}
	record, _ := decodeGeneratedTrack(job.GeneratedTrack)
	candidate := record.Candidates[0]
	structural := record.StructuralReviews[0]
	mediaPath, err := service.CandidateMediaPath(context.Background(), job.JobID, candidate.CandidateID)
	if err != nil || mediaPath != candidate.NormalizedArtifact.Path {
		t.Fatalf("persisted candidate media was not safely resolved: path=%q err=%v", mediaPath, err)
	}
	if _, err := service.CandidateMediaPath(context.Background(), job.JobID, "../../arbitrary.mp4"); err == nil {
		t.Fatal("unpersisted candidate media path was accepted")
	}
	job, err = service.RecordGeneratedContentReview(context.Background(), job.JobID, job.Revision, media.GeneratedShotContentReviewDecision{
		ReviewID: "content_review_1", StructuralReviewID: structural.ReviewID, CandidateID: candidate.CandidateID, IntentID: intent.IntentID,
		ReviewerID: "reviewer_1", ReviewerKind: "human", ReviewedAt: time.Unix(300, 0), Decision: media.GeneratedShotContentDecisionApprove,
		Assertions:   media.GeneratedShotContentSafetyAssertions{PurposeMatchesIntent: true, NoCapturedUIReplacement: true, NoBusinessFactClaims: true, NoUnverifiedTextOrNumbers: true, NoReferenceFactMutation: true},
		EvidenceRefs: []string{"review://content/1"}, Reason: "presentation-only content verified",
	})
	if err != nil || job.State != model.FinalFilmJobAwaitingCandidateSelection {
		t.Fatalf("content review did not stop at selection: job=%+v err=%v", job, err)
	}
	record, _ = decodeGeneratedTrack(job.GeneratedTrack)
	set := record.CandidateSets[0]
	job, err = service.RecordGeneratedSelection(context.Background(), job.JobID, job.Revision, media.GeneratedShotSelectionDecision{
		SelectionID: "selection_1", SetID: set.SetID, CandidateID: candidate.CandidateID,
		SelectorID: "selector_1", SelectorKind: "human", SelectedAt: time.Unix(301, 0), EvidenceRefs: []string{"review://selection/1"}, Reason: "selected after visual comparison",
	})
	if err != nil || job.State != model.FinalFilmJobAwaitingEditorApproval {
		t.Fatalf("selection did not stop at editor approval: job=%+v err=%v", job, err)
	}
	record, _ = decodeGeneratedTrack(job.GeneratedTrack)
	selection := record.Selections[0]
	job, err = service.RecordGeneratedEditorApproval(context.Background(), job.JobID, job.Revision, media.GeneratedShotEditorApprovalDecision{
		ApprovalID: "editor_approval_1", SelectionID: selection.SelectionID, CandidateID: candidate.CandidateID,
		ApproverID: "editor_1", ApproverKind: "human", ApprovedAt: time.Unix(302, 0), TargetPlanID: plan.PlanID,
		ExpectedPlanRevision: 1, Placement: "before_first_required_step", EvidenceRefs: []string{"review://editor/1"}, Reason: "approved as non-factual intro",
	})
	if err != nil || job.State != model.FinalFilmJobAwaitingPatchApply {
		t.Fatalf("editor approval did not create a pending patch: job=%+v err=%v", job, err)
	}
	record, _ = decodeGeneratedTrack(job.GeneratedTrack)
	if len(record.PatchProposals) != 1 || record.PatchProposals[0].AutoApply || record.PatchProposals[0].IncludeInDemo || !record.PatchProposals[0].RequiresExplicitOptIn {
		t.Fatalf("unsafe generated patch proposal: %+v", record.PatchProposals)
	}
	job, err = service.ApplyGeneratedPatch(context.Background(), job.JobID, job.Revision, record.PatchProposals[0].PatchID)
	if err != nil || job.State != model.FinalFilmJobCompleted || job.FinalPlan == nil || job.FinalCatalog == nil || job.AppliedGeneratedPatchID == "" {
		t.Fatalf("explicit generated patch was not rendered: job=%+v err=%v", job, err)
	}
	if service.renderer.(*fakeFinalFilmRenderer).renderCalls != 2 || job.FinalPlan.Shots[0].SourceStepID != "" || job.FinalPlan.Shots[1].SourceStepID != "step_1" || job.FinalPlan.Shots[2].SourceStepID != "step_2" {
		t.Fatalf("final plan did not preserve the locked fact track: %+v", job.FinalPlan.Shots)
	}
}

func TestGeneratedPatchBatchAtomicallyPlacesIntroDividerAndOutro(t *testing.T) {
	service, renderer := newFinalFilmTestService(t)
	registry := media.NewGeneratedShotProviderRegistry()
	if err := registry.Register(&fakeGeneratedShotProvider{root: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	service.providers = registry
	catalog, plan := finalFilmFixture()
	intro, _ := model.PresentationGenerationIntentDefaults("intro_batch", "intro", nil)
	divider, _ := model.PresentationGenerationIntentDefaults("divider_batch", "section_divider", nil)
	outro, _ := model.PresentationGenerationIntentDefaults("outro_batch", "outro", nil)
	intents := []model.PresentationGenerationIntent{intro, divider, outro}
	job, err := service.CreateJob(context.Background(), CreateJobRequest{
		EditorSessionID: "editor_batch", EditorRevision: 1, SourcePackageID: "package_batch", Catalog: catalog, BaselinePlan: plan,
		Intents: intents, RenderProfile: finalFilmProfile(),
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err = service.RunBaseline(context.Background(), job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	job, err = service.SubmitDirectorPlan(context.Background(), job.JobID, job.Revision, finalFilmDirectorPlanForIntents(job, intents))
	if err != nil {
		t.Fatal(err)
	}
	job, err = service.DecideGeneration(context.Background(), job.JobID, job.Revision, true, "")
	if err != nil {
		t.Fatal(err)
	}
	job, err = service.RunGeneratedCandidates(context.Background(), job.JobID, job.Revision, media.GeneratedShotProviderMiniMaxH3)
	if err != nil {
		t.Fatal(err)
	}
	record, _ := decodeGeneratedTrack(job.GeneratedTrack)
	if len(record.Candidates) != 3 || len(record.StructuralReviews) != 3 {
		t.Fatalf("expected three generated candidates: %+v", record)
	}
	for index, candidate := range record.Candidates {
		structural := record.StructuralReviews[index]
		job, err = service.RecordGeneratedContentReview(context.Background(), job.JobID, job.Revision, media.GeneratedShotContentReviewDecision{
			ReviewID: fmt.Sprintf("content_batch_%d", index), StructuralReviewID: structural.ReviewID,
			CandidateID: candidate.CandidateID, IntentID: candidate.IntentID, ReviewerID: "reviewer_batch", ReviewerKind: "human",
			ReviewedAt: time.Unix(int64(400+index), 0), Decision: media.GeneratedShotContentDecisionApprove,
			Assertions:   media.GeneratedShotContentSafetyAssertions{PurposeMatchesIntent: true, NoCapturedUIReplacement: true, NoBusinessFactClaims: true, NoUnverifiedTextOrNumbers: true, NoReferenceFactMutation: true},
			EvidenceRefs: []string{fmt.Sprintf("review://batch/content/%d", index)}, Reason: "approved presentation-only candidate",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if job.State != model.FinalFilmJobAwaitingCandidateSelection {
		t.Fatalf("batch review state = %s", job.State)
	}
	record, _ = decodeGeneratedTrack(job.GeneratedTrack)
	for index, set := range record.CandidateSets {
		job, err = service.RecordGeneratedSelection(context.Background(), job.JobID, job.Revision, media.GeneratedShotSelectionDecision{
			SelectionID: fmt.Sprintf("selection_batch_%d", index), SetID: set.SetID, CandidateID: set.Candidates[0].CandidateID,
			SelectorID: "selector_batch", SelectorKind: "human", SelectedAt: time.Unix(int64(410+index), 0),
			EvidenceRefs: []string{fmt.Sprintf("review://batch/selection/%d", index)}, Reason: "selected approved candidate",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if job.State != model.FinalFilmJobAwaitingEditorApproval {
		t.Fatalf("batch selection state = %s", job.State)
	}
	record, _ = decodeGeneratedTrack(job.GeneratedTrack)
	for index, selection := range record.Selections {
		placement := "before_first_required_step"
		anchorAfterStepID := ""
		if selection.IntentID == outro.IntentID {
			placement = "after_last_required_step"
		} else if selection.IntentID == divider.IntentID {
			placement = "between_sections"
			anchorAfterStepID = "step_1"
		}
		job, err = service.RecordGeneratedEditorApproval(context.Background(), job.JobID, job.Revision, media.GeneratedShotEditorApprovalDecision{
			ApprovalID: fmt.Sprintf("approval_batch_%d", index), SelectionID: selection.SelectionID, CandidateID: selection.SelectedCandidateID,
			ApproverID: "editor_batch", ApproverKind: "human", ApprovedAt: time.Unix(int64(420+index), 0),
			TargetPlanID: plan.PlanID, ExpectedPlanRevision: 1, Placement: placement, AnchorAfterStepID: anchorAfterStepID,
			EvidenceRefs: []string{fmt.Sprintf("review://batch/editor/%d", index)}, Reason: "approved deterministic placement",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if job.State != model.FinalFilmJobAwaitingPatchApply {
		t.Fatalf("batch approval state = %s", job.State)
	}
	record, _ = decodeGeneratedTrack(job.GeneratedTrack)
	patchIDs := []string{record.PatchProposals[0].PatchID, record.PatchProposals[1].PatchID, record.PatchProposals[2].PatchID}
	job, err = service.ApplyGeneratedPatches(context.Background(), job.JobID, job.Revision, patchIDs)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != model.FinalFilmJobCompleted || len(job.AppliedGeneratedPatchIDs) != 3 || renderer.renderCalls != 2 {
		t.Fatalf("batch was not rendered atomically: %+v", job)
	}
	shots := job.FinalPlan.Shots
	if len(shots) != 5 || shots[0].SourceStepID != "" || shots[1].SourceStepID != "step_1" || shots[2].SourceStepID != "" || shots[3].SourceStepID != "step_2" || shots[4].SourceStepID != "" {
		t.Fatalf("intro/fact/divider/fact/outro order is unsafe: %+v", shots)
	}
}

func TestFinalFilmInvalidConstraintDoesNotPersistJob(t *testing.T) {
	root := t.TempDir()
	renderer := &fakeFinalFilmRenderer{validation: model.DemoEditPlanValidationReport{Valid: true}}
	service, err := NewService(ServiceOptions{Store: NewFileStore(filepath.Join(root, "jobs")), Renderer: renderer, OutputRoot: filepath.Join(root, "outputs")})
	if err != nil {
		t.Fatal(err)
	}
	catalog, plan := finalFilmFixture()
	plan.Shots[0].SourceArtifactID = "missing"
	_, err = service.CreateJob(context.Background(), CreateJobRequest{
		EditorSessionID: "editor_1", EditorRevision: 1, SourcePackageID: "package_1", Catalog: catalog, BaselinePlan: plan, RenderProfile: finalFilmProfile(),
	})
	if err == nil {
		t.Fatal("expected invalid constraint rejection")
	}
	entries, readErr := filepath.Glob(filepath.Join(root, "jobs", "*"))
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("invalid job was persisted: entries=%v err=%v", entries, readErr)
	}
}

func TestFileStoreRejectsStaleRevision(t *testing.T) {
	service, _ := newFinalFilmTestService(t)
	catalog, plan := finalFilmFixture()
	job, err := service.CreateJob(context.Background(), CreateJobRequest{
		EditorSessionID: "editor_1", EditorRevision: 1, SourcePackageID: "package_1", Catalog: catalog, BaselinePlan: plan, RenderProfile: finalFilmProfile(),
	})
	if err != nil {
		t.Fatal(err)
	}
	next := job
	next.Revision++
	store := service.store
	if err := store.CompareAndSwapJob(context.Background(), job.JobID, job.Revision+1, next); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("expected revision conflict, got %v", err)
	}
}

type fakeFinalFilmRenderer struct {
	validation    model.DemoEditPlanValidationReport
	validationErr error
	renderResult  executor.RenderResult
	renderErr     error
	validateCalls int
	renderCalls   int
}

type fakeGeneratedShotProvider struct{ root string }

func (f *fakeGeneratedShotProvider) Descriptor() media.GeneratedShotProviderDescriptor {
	return media.GeneratedShotProviderDescriptor{Provider: media.GeneratedShotProviderMiniMaxH3, Model: media.MiniMaxH3Model, ProfileVersion: f.Profile().ProfileVersion, Enabled: true}
}

func (f *fakeGeneratedShotProvider) Profile() media.GeneratedShotCapabilityProfile {
	return (media.MiniMaxH3GeneratedShotCompiler{}).Profile()
}

func (f *fakeGeneratedShotProvider) Preflight(_ context.Context, intent media.GeneratedShotIntent) media.GeneratedShotProviderPreflight {
	report := media.PreflightGeneratedShotIntent(intent, []media.GeneratedShotProviderCompiler{media.MiniMaxH3GeneratedShotCompiler{}}, []media.GeneratedShotProviderAvailability{{Provider: media.GeneratedShotProviderMiniMaxH3, Enabled: true}})
	return report.Providers[0]
}

func (f *fakeGeneratedShotProvider) Execute(_ context.Context, request media.GeneratedShotProviderExecutionRequest) (media.GeneratedShotProviderExecutionResult, error) {
	originalDigest, normalizedDigest := strings.Repeat("a", 64), strings.Repeat("b", 64)
	if request.Intent.Purpose == media.GeneratedShotPurposeOutro {
		originalDigest, normalizedDigest = strings.Repeat("d", 64), strings.Repeat("e", 64)
	}
	taskID := "task_" + request.Intent.IntentID
	candidate := media.GeneratedShotCandidate{
		SchemaVersion: media.GeneratedShotCandidateSchemaVersion, CandidateID: "candidate_" + request.Intent.IntentID,
		IntentID: request.Intent.IntentID, Provider: media.GeneratedShotProviderMiniMaxH3, ProviderTaskID: taskID,
		Status: media.GeneratedShotCandidateReadyForReview, FailurePolicy: media.GeneratedShotFailureContinue,
		NonAuthoritative: true, PresentationOnly: true, RequiresExplicitReview: true,
		OriginalArtifact:   media.GeneratedShotCandidateArtifact{Role: "original", Path: filepath.Join(f.root, request.Intent.IntentID+"_original.mp4"), MimeType: "video/mp4", SHA256: originalDigest, SizeBytes: 100, Probe: media.GeneratedShotMediaProbe{DurationSec: 5}},
		NormalizedArtifact: media.GeneratedShotCandidateArtifact{Role: "normalized", Path: filepath.Join(f.root, request.Intent.IntentID+"_normalized.mp4"), MimeType: "video/mp4", SHA256: normalizedDigest, SizeBytes: 90, NormalizationStatus: "ok", NormalizationProfile: media.GeneratedShotNormalizationProfile, Probe: media.GeneratedShotMediaProbe{Format: "mp4", VideoCodec: "h264", PixelFormat: "yuv420p", Width: 1920, Height: 1080, FPS: 30, CFR: true, DurationSec: 5}},
	}
	if err := os.WriteFile(candidate.OriginalArtifact.Path, []byte("original candidate"), 0o600); err != nil {
		return media.GeneratedShotProviderExecutionResult{}, err
	}
	if err := os.WriteFile(candidate.NormalizedArtifact.Path, []byte("normalized candidate"), 0o600); err != nil {
		return media.GeneratedShotProviderExecutionResult{}, err
	}
	review := media.ReviewGeneratedShotCandidateStructure("structural_"+request.Intent.IntentID, request.Intent, candidate)
	return media.GeneratedShotProviderExecutionResult{SchemaVersion: media.GeneratedShotProviderExecutionSchemaVersion, Provider: media.GeneratedShotProviderMiniMaxH3, Model: media.MiniMaxH3Model, IntentID: request.Intent.IntentID, Status: media.GeneratedShotCandidateReadyForReview, ProviderTaskID: taskID, Candidate: &candidate, StructuralReview: &review, FailurePolicy: media.GeneratedShotFailureContinue}, nil
}

func (f *fakeGeneratedShotProvider) Cancel(context.Context, string) error { return nil }

type recordingDirectorPlanner struct {
	calls   int
	request DirectorPlanRequest
}

func (p *recordingDirectorPlanner) PlanGeneratedShots(_ context.Context, request DirectorPlanRequest) (model.FinalFilmDirectorPlan, error) {
	p.calls++
	p.request = request
	job := model.FinalFilmJob{JobID: request.JobID, Constraints: request.Constraints}
	return finalFilmDirectorPlanForIntents(job, request.Intents), nil
}

func (f *fakeFinalFilmRenderer) ValidateEditPlan(_ context.Context, _ executor.EditPlanValidationRequest) (model.DemoEditPlanValidationReport, error) {
	f.validateCalls++
	return f.validation, f.validationErr
}

func (f *fakeFinalFilmRenderer) ProbeMedia(_ context.Context, request executor.MediaProbeRequest) (executor.MediaProbeResult, error) {
	return executor.MediaProbeResult{
		Path: request.Path, SizeBytes: 1000, SHA256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		MimeType: "video/mp4", Format: "mp4", DurationMS: 9000, VideoCodec: "h264", Width: 1920, Height: 1080,
		FPS: 30, PixelFormat: "yuv420p", FFProbeAvailable: true,
	}, nil
}

func (f *fakeFinalFilmRenderer) Render(_ context.Context, request executor.RenderRequest) (executor.RenderResult, error) {
	f.renderCalls++
	if request.ModelExecution == nil {
		return executor.RenderResult{}, errors.New("render model audit is missing")
	}
	if request.ModelExecution.PlanSource == "validated_fact_track_baseline" {
		if request.ModelExecution.Invoked || request.ModelExecution.ProviderOutputAdopted {
			return executor.RenderResult{}, errors.New("baseline render model audit is unsafe")
		}
	} else if !request.ModelExecution.Invoked || !request.ModelExecution.ProviderOutputAdopted || !request.ModelExecution.PatchApplied {
		return executor.RenderResult{}, errors.New("generated final render model audit is incomplete")
	}
	return f.renderResult, f.renderErr
}

func newFinalFilmTestService(t *testing.T) (*Service, *fakeFinalFilmRenderer) {
	t.Helper()
	root := t.TempDir()
	renderer := &fakeFinalFilmRenderer{
		validation:   model.DemoEditPlanValidationReport{SchemaVersion: model.DemoEditPlanValidationSchemaVersion, Valid: true},
		renderResult: executor.RenderResult{VideoPath: filepath.Join(root, "baseline.mp4"), RenderManifestPath: filepath.Join(root, "manifest.json")},
	}
	if err := os.WriteFile(renderer.renderResult.RenderManifestPath, []byte(`{"compositor":{"quality_status":"ok","ffmpeg_available":true,"fallback_reason":"","planned_operations":["trim"],"applied_operations":["trim"],"skipped_operations":[]},"requirement_satisfaction_report":{"status":"satisfied"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sequence := 0
	registry := media.NewGeneratedShotProviderRegistry()
	disabled, err := media.NewMiniMaxH3ProviderAdapter(media.MiniMaxH3ProviderAdapterOptions{Enabled: false, DisabledReason: "disabled for final-film test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(disabled); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(ServiceOptions{
		Store: NewFileStore(filepath.Join(root, "jobs")), Renderer: renderer, OutputRoot: filepath.Join(root, "outputs"),
		Providers: registry,
		Now:       func() time.Time { sequence++; return time.Unix(int64(100+sequence), 0) },
		NewID: func(prefix string) (string, error) {
			sequence++
			return prefix + "_test_" + time.Unix(int64(sequence), 0).Format("150405"), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, renderer
}

func finalFilmDirectorPlan(job model.FinalFilmJob, intent model.PresentationGenerationIntent) model.FinalFilmDirectorPlan {
	return finalFilmDirectorPlanForIntents(job, []model.PresentationGenerationIntent{intent})
}

func finalFilmDirectorPlanForIntents(job model.FinalFilmJob, intents []model.PresentationGenerationIntent) model.FinalFilmDirectorPlan {
	plan := model.FinalFilmDirectorPlan{
		SchemaVersion: model.FinalFilmDirectorPlanSchemaVersion, PlanID: "director_plan_1", JobID: job.JobID,
		ConstraintSetID: job.Constraints.ConstraintSetID, DirectorRunID: "director_run_1", GeneratedAt: time.Unix(200, 0),
	}
	for index, intent := range intents {
		prompt := "Cinematic abstract brand atmosphere with soft light and geometric motion; no product UI, facts, numbers, or readable text. Purpose: " + intent.Purpose
		plan.Specs = append(plan.Specs, model.FinalFilmGeneratedSpec{
			SpecID: fmt.Sprintf("generated_spec_%d", index+1), IntentID: intent.IntentID, Purpose: intent.Purpose,
			Prompt: prompt, PromptSHA256: model.FinalFilmPromptSHA256(prompt), DurationSec: intent.RequestedSlot.PreferredDurationSec,
			AspectRatio: intent.RequestedSlot.AspectRatio, ReferenceArtifactIDs: append([]string{}, intent.ReferenceAssetRefs...),
			ContentPolicy: model.FinalFilmGeneratedSpecContentPolicy{PresentationOnly: true, NoCapturedUIRecreation: true, NoBusinessFactClaims: true, NoUnverifiedText: true, RequiresExplicitReview: true},
			FailurePolicy: model.PresentationGenerationFailureContinue,
		})
	}
	return plan
}

func finalFilmFixture() (model.AssetTimelineCatalog, model.DemoEditPlan) {
	catalog := model.AssetTimelineCatalog{
		SchemaVersion: model.AssetTimelineCatalogSchemaVersion, CatalogID: "catalog_1", WorkflowGraphID: "graph_1", GraphVersion: 1, RunID: "run_1",
		Source:      model.AssetTimelineSource{GeneratedAt: time.Unix(10, 0)},
		Constraints: model.AssetTimelineConstraints{SourceMaterialOnly: true, ProhibitNewImageOrVideoGeneration: true, ScriptIsPrimaryStoryline: true, AllowedEditOperations: append([]model.EditOperationType{}, model.DemoEditAllowedOperations...), ProhibitedPlanKeys: append([]string{}, model.DemoEditProhibitedPlanKeys...)},
		Timeline:    model.AssetTimelineInfo{DurationMS: 4000, RecordingArtifactID: "recording_1"},
		Steps: []model.TimelineStep{
			{StepID: "step_1", Order: 1, Action: "open", Status: "passed", Required: true, StartMS: 0, EndMS: 2000, DurationMS: 2000, ExpectedOutcome: "page visible", ObservedState: "visible", Artifacts: []string{"recording_1", "shot_1"}},
			{StepID: "step_2", Order: 2, Action: "submit", Status: "passed", Required: true, StartMS: 2000, EndMS: 4000, DurationMS: 2000, ExpectedOutcome: "result visible", ObservedState: "visible", Artifacts: []string{"recording_1"}},
		},
		Artifacts: []model.TimelineArtifact{
			{ID: "recording_1", Kind: "browser_recording", URI: "file:///recording.mp4", MimeType: "video/mp4", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SizeBytes: 100, IncludeInDemo: true, DurationMS: 4000, LocalPath: filepath.Clean("C:/fixtures/recording.mp4")},
			{ID: "shot_1", Kind: "screenshot", URI: "file:///shot.png", MimeType: "image/png", SHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", SizeBytes: 10, IncludeInDemo: true, LocalPath: filepath.Clean("C:/fixtures/shot.png")},
		},
	}
	first := model.MillisecondRange{0, 2000}
	second := model.MillisecondRange{2000, 4000}
	plan := model.DemoEditPlan{
		SchemaVersion: model.DemoEditPlanSchemaVersion, PlanID: "plan_1", CatalogID: catalog.CatalogID,
		SourceAuthority: model.DemoEditSourceAuthorityServerLocalEditor, ModelRole: model.DemoEditModelRolePresentationOptimizerOnly,
		SourceMaterialPolicy: model.DemoEditSourceMaterialPolicyExistingAssetsOnly, ScriptOrderPolicy: model.DemoEditScriptOrderPolicyPreserveRequiredStepOrder,
		LockedFields: append([]string{}, model.DemoEditRequiredLockedFields...), ModelEditableFields: append([]string{}, model.DemoEditAllowedModelEditableFields...), TargetDurationMS: 4000,
		Shots: []model.DemoEditShot{
			{ID: "shot_fact_1", SourceArtifactID: "recording_1", SourceStepID: "step_1", SourceTimeRangeMS: &first, Purpose: "required step 1"},
			{ID: "shot_fact_2", SourceArtifactID: "recording_1", SourceStepID: "step_2", SourceTimeRangeMS: &second, Purpose: "required step 2"},
		},
	}
	return catalog, plan
}

func finalFilmProfile() model.EditorRenderProfile {
	return model.EditorRenderProfile{Mode: "final", Width: 1920, Height: 1080, FPS: 30, Format: "mp4", Preset: "medium", CRF: 18}
}
