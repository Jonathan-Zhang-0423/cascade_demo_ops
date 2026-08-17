package finalfilm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/driver"
	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

// TestRealH3FinalFilmE2E replays an already paid, persisted H3 provider result
// through the production FinalFilm state machine and the real Node/FFmpeg
// renderer. It never creates a provider task. The separate approval variable
// makes the human content-review decision explicit and keeps normal test runs
// hermetic and free of provider cost.
func TestRealH3FinalFilmE2E(t *testing.T) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("CASCADE_REAL_H3_E2E_APPROVED")), "true") {
		t.Skip("set CASCADE_REAL_H3_E2E_APPROVED=true only after visually reviewing the persisted H3 candidate")
	}
	harnessPath := requiredRealH3E2EEnv(t, "CASCADE_REAL_H3_HARNESS_RESULT")
	baselinePath := requiredRealH3E2EEnv(t, "CASCADE_REAL_H3_BASELINE")
	outputRoot := requiredRealH3E2EEnv(t, "CASCADE_REAL_H3_E2E_OUTPUT")
	nodePath := requiredRealH3E2EEnv(t, "CASCADE_REAL_H3_NODE")
	workerPath := requiredRealH3E2EEnv(t, "CASCADE_REAL_H3_WORKER")
	ffmpegPath := requiredRealH3E2EEnv(t, "CASCADE_REAL_H3_FFMPEG")
	ffprobePath := requiredRealH3E2EEnv(t, "CASCADE_REAL_H3_FFPROBE")

	harness := readRealH3HarnessResult(t, harnessPath)
	if harness.Status != "awaiting_human_content_review" || harness.Candidate == nil || harness.StructuralReview == nil {
		t.Fatalf("persisted H3 result is not review-ready: status=%q", harness.Status)
	}
	if harness.Provider != media.GeneratedShotProviderMiniMaxH3 || harness.Model != media.MiniMaxH3Model || strings.TrimSpace(harness.GenerationTaskID) == "" {
		t.Fatalf("persisted result lost its H3 provider/model/task binding: provider=%q model=%q task=%q", harness.Provider, harness.Model, harness.GenerationTaskID)
	}
	if err := media.ValidateGeneratedShotCandidate(*harness.Candidate); err != nil {
		t.Fatalf("validate persisted real H3 candidate: %v", err)
	}
	if !harness.StructuralReview.StructurallyEligible || harness.StructuralReview.CandidateID != harness.Candidate.CandidateID {
		t.Fatal("persisted real H3 structural review is missing, ineligible, or bound to another candidate")
	}
	if info, err := os.Stat(harness.Candidate.NormalizedArtifact.Path); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("persisted normalized H3 candidate is unavailable: %v", err)
	}

	renderer := driver.NewLocalDriver(nodePath, workerPath, map[string]string{
		"CASCADE_FFMPEG_PATH":  ffmpegPath,
		"CASCADE_FFPROBE_PATH": ffprobePath,
	})
	baselineProbe, err := renderer.ProbeMedia(context.Background(), executor.MediaProbeRequest{Path: baselinePath})
	if err != nil {
		t.Fatalf("probe baseline fixture: %v", err)
	}
	if !baselineProbe.FFProbeAvailable || baselineProbe.DurationMS < 4000 || baselineProbe.Width != 1920 || baselineProbe.Height != 1080 {
		t.Fatalf("baseline fixture does not satisfy the E2E profile: %+v", baselineProbe)
	}

	registry := media.NewGeneratedShotProviderRegistry()
	if err := registry.Register(&persistedRealH3Adapter{harness: harness}); err != nil {
		t.Fatal(err)
	}
	runRoot := filepath.Join(outputRoot, "run_"+strconv.FormatInt(time.Now().UTC().UnixNano(), 10))
	sequence := 0
	service, err := NewService(ServiceOptions{
		Store: NewFileStore(filepath.Join(runRoot, "jobs")), Renderer: renderer,
		OutputRoot: filepath.Join(runRoot, "outputs"), Providers: registry,
		Now: func() time.Time {
			sequence++
			return time.Unix(1_800_000_000+int64(sequence), 0).UTC()
		},
		NewID: func(prefix string) (string, error) {
			sequence++
			return prefix + "_real_h3_" + strconv.Itoa(sequence), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	catalog, plan := realH3E2EFixture(baselinePath, baselineProbe)
	intent, err := model.PresentationGenerationIntentDefaults(harness.IntentID, media.GeneratedShotPurposeIntro, nil)
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.CreateJob(context.Background(), CreateJobRequest{
		EditorSessionID: "editor_real_h3_e2e", EditorRevision: 1, SourcePackageID: "package_real_h3_e2e",
		Catalog: catalog, BaselinePlan: plan, Intents: []model.PresentationGenerationIntent{intent}, RenderProfile: finalFilmProfile(),
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err = service.RunBaseline(context.Background(), job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != model.FinalFilmJobAwaitingGenerationApproval || strings.TrimSpace(job.BaselineRender.VideoPath) == "" {
		t.Fatalf("real baseline did not reach the approval gate: %+v", job)
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
	if err != nil {
		t.Fatal(err)
	}
	if job.State != model.FinalFilmJobAwaitingContentReview {
		t.Fatalf("persisted H3 candidate bypassed or failed the content-review gate: state=%s", job.State)
	}
	record, err := decodeGeneratedTrack(job.GeneratedTrack)
	if err != nil || len(record.Executions) != 1 || len(record.Candidates) != 1 || len(record.StructuralReviews) != 1 {
		t.Fatalf("real H3 execution audit is incomplete: record=%+v err=%v", record, err)
	}
	if record.Executions[0].ProviderTaskID != harness.GenerationTaskID || record.Executions[0].Candidate.CandidateID != harness.Candidate.CandidateID {
		t.Fatal("real H3 provider task or candidate identity was not preserved")
	}

	candidate := record.Candidates[0]
	structural := record.StructuralReviews[0]
	job, err = service.RecordGeneratedContentReview(context.Background(), job.JobID, job.Revision, media.GeneratedShotContentReviewDecision{
		ReviewID: "content_review_real_h3", StructuralReviewID: structural.ReviewID,
		CandidateID: candidate.CandidateID, IntentID: intent.IntentID,
		ReviewerID: "operator_real_h3_e2e", ReviewerKind: "human", ReviewedAt: time.Now().UTC(),
		Decision: media.GeneratedShotContentDecisionApprove,
		Assertions: media.GeneratedShotContentSafetyAssertions{
			PurposeMatchesIntent: true, NoCapturedUIReplacement: true, NoBusinessFactClaims: true,
			NoUnverifiedTextOrNumbers: true, NoReferenceFactMutation: true,
		},
		EvidenceRefs: []string{"file://review-contact-sheet-small.png"},
		Reason:       "operator visually reviewed the real H3 contact sheet; abstract presentation-only intro contains no UI, text, or business facts",
	})
	if err != nil {
		t.Fatal(err)
	}
	record, _ = decodeGeneratedTrack(job.GeneratedTrack)
	job, err = service.RecordGeneratedSelection(context.Background(), job.JobID, job.Revision, media.GeneratedShotSelectionDecision{
		SelectionID: "selection_real_h3", SetID: record.CandidateSets[0].SetID, CandidateID: candidate.CandidateID,
		SelectorID: "operator_real_h3_e2e", SelectorKind: "human", SelectedAt: time.Now().UTC(),
		EvidenceRefs: []string{"file://review-contact-sheet-small.png"}, Reason: "selected the reviewed real H3 candidate",
	})
	if err != nil {
		t.Fatal(err)
	}
	record, _ = decodeGeneratedTrack(job.GeneratedTrack)
	job, err = service.RecordGeneratedEditorApproval(context.Background(), job.JobID, job.Revision, media.GeneratedShotEditorApprovalDecision{
		ApprovalID: "editor_approval_real_h3", SelectionID: record.Selections[0].SelectionID, CandidateID: candidate.CandidateID,
		ApproverID: "editor_real_h3_e2e", ApproverKind: "human", ApprovedAt: time.Now().UTC(),
		TargetPlanID: plan.PlanID, ExpectedPlanRevision: 1, Placement: "before_first_required_step",
		EvidenceRefs: []string{"finalfilm://real-h3/e2e/editor-approval"}, Reason: "approved as an optional non-factual intro before the locked fact track",
	})
	if err != nil {
		t.Fatal(err)
	}
	record, _ = decodeGeneratedTrack(job.GeneratedTrack)
	job, err = service.ApplyGeneratedPatches(context.Background(), job.JobID, job.Revision, []string{record.PatchProposals[0].PatchID})
	if err != nil {
		t.Fatal(err)
	}
	if job.State != model.FinalFilmJobCompleted || job.FinalOutputValidation == nil || job.FinalOutputValidation.Status != "passed" {
		t.Fatalf("real H3 final render did not complete validation: state=%s validation=%+v fallback=%q", job.State, job.FinalOutputValidation, job.GenerationSkipReason)
	}
	if job.FinalRender.VideoPath == job.BaselineRender.VideoPath || len(job.AppliedGeneratedPatchIDs) != 1 || job.FinalPlan == nil || len(job.FinalPlan.Shots) != 3 {
		t.Fatalf("real H3 candidate was not deterministically applied: %+v", job)
	}
	if _, err := os.Stat(job.FinalRender.VideoPath); err != nil {
		t.Fatalf("stat completed real H3 final video: %v", err)
	}
	events, err := service.ListEvents(context.Background(), job.JobID)
	if err != nil || len(events) < 10 {
		t.Fatalf("real H3 E2E audit trail is incomplete: events=%d err=%v", len(events), err)
	}
	t.Logf("REAL_H3_E2E_FINAL_VIDEO=%s", job.FinalRender.VideoPath)
	t.Logf("REAL_H3_E2E_JOB_ROOT=%s", runRoot)
}

type persistedRealH3Adapter struct {
	harness media.MiniMaxH3HarnessResult
}

func (a *persistedRealH3Adapter) Descriptor() media.GeneratedShotProviderDescriptor {
	return media.GeneratedShotProviderDescriptor{
		Provider: media.GeneratedShotProviderMiniMaxH3, Model: media.MiniMaxH3Model,
		ProfileVersion: (media.MiniMaxH3GeneratedShotCompiler{}).Profile().ProfileVersion, Enabled: true,
		Reason: "replay a persisted real H3 result; no provider request is performed",
	}
}

func (a *persistedRealH3Adapter) Profile() media.GeneratedShotCapabilityProfile {
	return (media.MiniMaxH3GeneratedShotCompiler{}).Profile()
}

func (a *persistedRealH3Adapter) Preflight(_ context.Context, intent media.GeneratedShotIntent) media.GeneratedShotProviderPreflight {
	report := media.PreflightGeneratedShotIntent(intent, []media.GeneratedShotProviderCompiler{media.MiniMaxH3GeneratedShotCompiler{}}, []media.GeneratedShotProviderAvailability{{
		Provider: media.GeneratedShotProviderMiniMaxH3, Enabled: true,
	}})
	if len(report.Providers) == 0 {
		return media.GeneratedShotProviderPreflight{Provider: media.GeneratedShotProviderMiniMaxH3, FailureCode: "persisted_h3_preflight_missing", FailureMessage: "persisted H3 replay preflight produced no result"}
	}
	return report.Providers[0]
}

func (a *persistedRealH3Adapter) Execute(_ context.Context, request media.GeneratedShotProviderExecutionRequest) (media.GeneratedShotProviderExecutionResult, error) {
	result := media.GeneratedShotProviderExecutionResult{
		SchemaVersion: media.GeneratedShotProviderExecutionSchemaVersion, Provider: media.GeneratedShotProviderMiniMaxH3,
		Model: media.MiniMaxH3Model, IntentID: request.Intent.IntentID, Status: media.GeneratedShotFailureContinue,
		FailurePolicy: media.GeneratedShotFailureContinue, ProviderTaskID: a.harness.GenerationTaskID,
	}
	if !request.GenerationAuthorized || strings.TrimSpace(request.AuthorizationRef) == "" {
		return result, errors.New("persisted real H3 replay still requires explicit FinalFilm generation authorization")
	}
	if request.Intent.IntentID != a.harness.IntentID || a.harness.Candidate == nil || a.harness.StructuralReview == nil {
		return result, errors.New("persisted real H3 result does not match the authorized intent")
	}
	result.Status = a.harness.Status
	result.Candidate = a.harness.Candidate
	result.StructuralReview = a.harness.StructuralReview
	return result, nil
}

func (a *persistedRealH3Adapter) Cancel(_ context.Context, _ string) error {
	return errors.New("persisted H3 replay has no live provider task to cancel")
}

func requiredRealH3E2EEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required when CASCADE_REAL_H3_E2E_APPROVED=true", name)
	}
	return filepath.Clean(value)
}

func readRealH3HarnessResult(t *testing.T, path string) media.MiniMaxH3HarnessResult {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read persisted real H3 harness result: %v", err)
	}
	var result media.MiniMaxH3HarnessResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decode persisted real H3 harness result: %v", err)
	}
	if result.SchemaVersion != media.MiniMaxH3HarnessSchemaVersion {
		t.Fatalf("unsupported persisted real H3 harness schema %q", result.SchemaVersion)
	}
	return result
}

func realH3E2EFixture(baselinePath string, probe executor.MediaProbeResult) (model.AssetTimelineCatalog, model.DemoEditPlan) {
	catalog, plan := finalFilmFixture()
	catalog.CatalogID = "catalog_real_h3_e2e"
	catalog.WorkflowGraphID = "graph_real_h3_e2e"
	catalog.RunID = "run_real_h3_e2e"
	catalog.Timeline.RecordingArtifactID = "recording_real_h3_e2e"
	catalog.Timeline.DurationMS = 4000
	catalog.Steps[0].Artifacts = []string{"recording_real_h3_e2e"}
	catalog.Steps[1].Artifacts = []string{"recording_real_h3_e2e"}
	catalog.Artifacts = []model.TimelineArtifact{{
		ID: "recording_real_h3_e2e", Kind: "browser_recording", URI: "file://" + filepath.ToSlash(baselinePath),
		LocalPath: baselinePath, MimeType: probe.MimeType, SHA256: probe.SHA256, SizeBytes: probe.SizeBytes,
		IncludeInDemo: true, DurationMS: 4000,
	}}
	plan.PlanID = "plan_real_h3_e2e"
	plan.CatalogID = catalog.CatalogID
	plan.TargetDurationMS = 4000
	for index := range plan.Shots {
		plan.Shots[index].SourceArtifactID = "recording_real_h3_e2e"
	}
	return catalog, plan
}
