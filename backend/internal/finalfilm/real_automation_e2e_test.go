package finalfilm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// TestRealAutomatedFinalFilmE2E replays already-paid H3 and Seedance outputs
// through guided-demo-v1, the production Node/FFmpeg renderer, the automatic
// quality gate, deterministic EDL assembly, review-package creation, and the
// single final-review transition. It never creates a provider task.
func TestRealAutomatedFinalFilmE2E(t *testing.T) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("CASCADE_REAL_AUTOMATION_E2E_APPROVED")), "true") {
		t.Skip("set CASCADE_REAL_AUTOMATION_E2E_APPROVED=true only after visually reviewing the persisted provider candidates")
	}
	h3 := readRealH3HarnessResult(t, requiredRealAutomationEnv(t, "CASCADE_REAL_AUTOMATION_H3_RESULT"))
	seedance := readRealAutomationExecution(t, requiredRealAutomationEnv(t, "CASCADE_REAL_AUTOMATION_SEEDANCE_RESULT"))
	sourcePath := requiredRealAutomationEnv(t, "CASCADE_REAL_AUTOMATION_SOURCE")
	outputRoot := requiredRealAutomationEnv(t, "CASCADE_REAL_AUTOMATION_OUTPUT")

	renderer := driver.NewLocalDriver(
		requiredRealAutomationEnv(t, "CASCADE_REAL_AUTOMATION_NODE"),
		requiredRealAutomationEnv(t, "CASCADE_REAL_AUTOMATION_WORKER"),
		map[string]string{
			"CASCADE_FFMPEG_PATH":  requiredRealAutomationEnv(t, "CASCADE_REAL_AUTOMATION_FFMPEG"),
			"CASCADE_FFPROBE_PATH": requiredRealAutomationEnv(t, "CASCADE_REAL_AUTOMATION_FFPROBE"),
		},
	)
	probe, err := renderer.ProbeMedia(context.Background(), executor.MediaProbeRequest{Path: sourcePath})
	if err != nil || !probe.FFProbeAvailable || probe.DurationMS < 80_000 {
		t.Fatalf("real automation source is not a usable factual track: probe=%+v err=%v", probe, err)
	}

	registry := media.NewGeneratedShotProviderRegistry()
	if err := registry.Register(newPersistedAutomationAdapter(media.GeneratedShotProviderMiniMaxH3, media.MiniMaxH3Model, h3.GenerationTaskID, h3.Candidate)); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(newPersistedAutomationAdapter(media.GeneratedShotProviderSeedance25, media.Seedance25ServerModel, seedance.ProviderTaskID, seedance.Candidate)); err != nil {
		t.Fatal(err)
	}

	runs := 1
	if raw := strings.TrimSpace(os.Getenv("CASCADE_REAL_AUTOMATION_E2E_RUNS")); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed < 1 || parsed > 10 {
			t.Fatalf("CASCADE_REAL_AUTOMATION_E2E_RUNS must be 1-10, got %q", raw)
		}
		runs = parsed
	}
	for run := 1; run <= runs; run++ {
		runRealAutomatedFinalFilmE2E(t, renderer, registry, sourcePath, probe, outputRoot, run)
	}
}

func runRealAutomatedFinalFilmE2E(t *testing.T, renderer Renderer, registry *media.GeneratedShotProviderRegistry, sourcePath string, probe executor.MediaProbeResult, outputRoot string, run int) {
	t.Helper()
	runRoot := filepath.Join(outputRoot, fmt.Sprintf("run-%02d-%d", run, time.Now().UTC().UnixNano()))
	sequence := 0
	service, err := NewService(ServiceOptions{
		Store: NewFileStore(filepath.Join(runRoot, "jobs")), Renderer: renderer,
		OutputRoot: filepath.Join(runRoot, "outputs"), Providers: registry,
		Planner: &recordingDirectorPlanner{}, SkillRoot: filepath.Join("..", "..", "..", "skills", "final-film"),
		Now: func() time.Time { sequence++; return time.Unix(1_800_100_000+int64(sequence), 0).UTC() },
		NewID: func(prefix string) (string, error) {
			sequence++
			return fmt.Sprintf("%s_real_auto_%02d_%d", prefix, run, sequence), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog, plan := realAutomationFixture(sourcePath, probe, run)
	job, err := service.CreateJob(context.Background(), CreateJobRequest{
		EditorSessionID: fmt.Sprintf("editor_real_auto_%02d", run), EditorRevision: 1,
		SourcePackageID: fmt.Sprintf("package_real_auto_%02d", run), Catalog: catalog, BaselinePlan: plan,
		RenderProfile: finalFilmProfile(), AutomationProfile: model.FinalFilmAutomationProfileGuidedDemoV1,
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err = service.AuthorizeAutomation(context.Background(), job.JobID, job.Revision, fmt.Sprintf("real-auto-authorization-%02d", run), 8)
	if err != nil {
		t.Fatal(err)
	}
	job, err = service.ResumeAutomation(context.Background(), job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != model.FinalFilmJobAwaitingFinalReview || job.ReviewPackage == nil || job.FinalOutputValidation == nil || job.FinalOutputValidation.Status != "passed" {
		t.Fatalf("guided automation did not reach final review: state=%s validation=%+v package=%+v", job.State, job.FinalOutputValidation, job.ReviewPackage)
	}
	if job.RunAuthorization == nil || job.RunAuthorization.ProviderCallsUsed != 4 || len(job.ProviderAttempts) != 4 || len(job.QualityReports) != 4 {
		t.Fatalf("guided provider assignment or attempt accounting drifted: authorization=%+v attempts=%+v reports=%+v", job.RunAuthorization, job.ProviderAttempts, job.QualityReports)
	}
	for _, report := range job.QualityReports {
		if report.Decision != "accept" || !report.TechnicalPass {
			t.Fatalf("persisted real candidate failed the automatic quality gate: %+v", report)
		}
	}
	if _, err := os.Stat(job.ReviewPackage.ZIPPath); err != nil {
		t.Fatalf("review package ZIP is missing: %v", err)
	}
	accepted, err := service.RecordFinalReview(context.Background(), job.JobID, job.Revision, "accept", "", "real-automation-acceptance", job.ReviewPackage.PackageID)
	if err != nil || accepted.State != model.FinalFilmJobCompleted {
		t.Fatalf("single final review did not complete the job: state=%s err=%v", accepted.State, err)
	}
	t.Logf("REAL_AUTOMATION_FINAL_VIDEO=%s", job.FinalRender.VideoPath)
	t.Logf("REAL_AUTOMATION_REVIEW_PACKAGE=%s", job.ReviewPackage.ZIPPath)
}

type persistedAutomationAdapter struct {
	provider  string
	model     string
	taskID    string
	candidate media.GeneratedShotCandidate
	compiler  media.GeneratedShotProviderCompiler
}

func newPersistedAutomationAdapter(provider, modelName, taskID string, candidate *media.GeneratedShotCandidate) *persistedAutomationAdapter {
	compiler := media.GeneratedShotProviderCompiler(media.MiniMaxH3GeneratedShotCompiler{})
	if provider == media.GeneratedShotProviderSeedance25 {
		compiler = media.Seedance25GeneratedShotCompiler{}
	}
	value := media.GeneratedShotCandidate{}
	if candidate != nil {
		value = *candidate
	}
	return &persistedAutomationAdapter{provider: provider, model: modelName, taskID: taskID, candidate: value, compiler: compiler}
}

func (a *persistedAutomationAdapter) Descriptor() media.GeneratedShotProviderDescriptor {
	return media.GeneratedShotProviderDescriptor{Provider: a.provider, Model: a.model, ProfileVersion: a.compiler.Profile().ProfileVersion, Enabled: true, Reason: "persisted real-output replay; no provider request is performed"}
}

func (a *persistedAutomationAdapter) Profile() media.GeneratedShotCapabilityProfile {
	return a.compiler.Profile()
}

func (a *persistedAutomationAdapter) Preflight(_ context.Context, intent media.GeneratedShotIntent) media.GeneratedShotProviderPreflight {
	report := media.PreflightGeneratedShotIntent(intent, []media.GeneratedShotProviderCompiler{a.compiler}, []media.GeneratedShotProviderAvailability{{Provider: a.provider, Enabled: true}})
	if len(report.Providers) == 0 {
		return media.GeneratedShotProviderPreflight{Provider: a.provider, FailureCode: "persisted_preflight_missing", FailureMessage: "persisted provider preflight produced no result"}
	}
	return report.Providers[0]
}

func (a *persistedAutomationAdapter) Execute(_ context.Context, request media.GeneratedShotProviderExecutionRequest) (media.GeneratedShotProviderExecutionResult, error) {
	result := media.GeneratedShotProviderExecutionResult{SchemaVersion: media.GeneratedShotProviderExecutionSchemaVersion, Provider: a.provider, Model: a.model, IntentID: request.Intent.IntentID, ProviderTaskID: a.taskID, FailurePolicy: media.GeneratedShotFailureContinue}
	if !request.GenerationAuthorized || strings.TrimSpace(request.AuthorizationRef) == "" || strings.TrimSpace(request.IdempotencyKey) == "" {
		return result, errors.New("persisted provider replay requires the normal authorization and idempotency boundary")
	}
	if err := request.OnTaskSubmitted(a.taskID); err != nil {
		return result, err
	}
	candidate := a.candidate
	candidate.IntentID = request.Intent.IntentID
	candidate.CandidateID = strings.ReplaceAll(a.provider, ".", "_") + "_" + request.Intent.IntentID
	review := media.ReviewGeneratedShotCandidateStructure("structural_"+candidate.CandidateID, request.Intent, candidate)
	result.Status, result.Candidate, result.StructuralReview = media.GeneratedShotCandidateReadyForReview, &candidate, &review
	return result, nil
}

func (a *persistedAutomationAdapter) Cancel(context.Context, string) error { return nil }

func requiredRealAutomationEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required when CASCADE_REAL_AUTOMATION_E2E_APPROVED=true", name)
	}
	return filepath.Clean(value)
}

func readRealAutomationExecution(t *testing.T, path string) media.GeneratedShotProviderExecutionResult {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var result media.GeneratedShotProviderExecutionResult
	if err := json.Unmarshal(payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != media.GeneratedShotProviderExecutionSchemaVersion || result.Candidate == nil || strings.TrimSpace(result.ProviderTaskID) == "" {
		t.Fatalf("persisted Seedance result is incomplete: %+v", result)
	}
	return result
}

func realAutomationFixture(sourcePath string, probe executor.MediaProbeResult, run int) (model.AssetTimelineCatalog, model.DemoEditPlan) {
	// The persisted reference contains a deliberately blank 12.3-21.5s gap.
	// Acceptance uses factual, visible ranges on both sides of that gap so the
	// final black-frame gate is testing the harness instead of the fixture.
	ranges := []model.MillisecondRange{{0, 10_000}, {22_000, 34_000}, {34_000, 47_000}, {47_000, 62_000}, {62_000, 77_000}, {77_000, 92_000}}
	catalogID := fmt.Sprintf("catalog_real_automation_%02d", run)
	recordingID := fmt.Sprintf("recording_real_automation_%02d", run)
	catalog := model.AssetTimelineCatalog{
		SchemaVersion: model.AssetTimelineCatalogSchemaVersion, CatalogID: catalogID,
		WorkflowGraphID: fmt.Sprintf("graph_real_automation_%02d", run), GraphVersion: 1, RunID: fmt.Sprintf("run_real_automation_%02d", run),
		Source:      model.AssetTimelineSource{GeneratedAt: time.Unix(1_800_000_000, 0).UTC()},
		Constraints: model.AssetTimelineConstraints{SourceMaterialOnly: true, ProhibitNewImageOrVideoGeneration: true, ScriptIsPrimaryStoryline: true, AllowedEditOperations: append([]model.EditOperationType{}, model.DemoEditAllowedOperations...), ProhibitedPlanKeys: append([]string{}, model.DemoEditProhibitedPlanKeys...)},
		Timeline:    model.AssetTimelineInfo{DurationMS: probe.DurationMS, RecordingArtifactID: recordingID},
		Artifacts:   []model.TimelineArtifact{{ID: recordingID, Kind: "browser_recording", URI: sourcePath, LocalPath: sourcePath, MimeType: probe.MimeType, SHA256: probe.SHA256, SizeBytes: probe.SizeBytes, IncludeInDemo: true, DurationMS: probe.DurationMS}},
	}
	plan := model.DemoEditPlan{
		SchemaVersion: model.DemoEditPlanSchemaVersion, PlanID: fmt.Sprintf("plan_real_automation_%02d", run), CatalogID: catalogID,
		Objective:       "Present a verified cross-site product workflow with deterministic chapter packaging.",
		SourceAuthority: model.DemoEditSourceAuthorityServerLocalEditor, ModelRole: model.DemoEditModelRolePresentationOptimizerOnly,
		SourceMaterialPolicy: model.DemoEditSourceMaterialPolicyExistingAssetsOnly, ScriptOrderPolicy: model.DemoEditScriptOrderPolicyPreserveRequiredStepOrder,
		LockedFields: append([]string{}, model.DemoEditRequiredLockedFields...), ModelEditableFields: append([]string{}, model.DemoEditAllowedModelEditableFields...), TargetDurationMS: 80_000,
	}
	for index := range ranges {
		stepID := fmt.Sprintf("verified_step_%02d", index+1)
		actions := []string{"open", "fill", "submit", "inspect", "interact", "verify"}
		catalog.Steps = append(catalog.Steps, model.TimelineStep{StepID: stepID, Order: index + 1, Action: actions[index], Status: "passed", Required: true, StartMS: ranges[index][0], EndMS: ranges[index][1], DurationMS: ranges[index][1] - ranges[index][0], ExpectedOutcome: fmt.Sprintf("Verified state %d is visible", index+1), ObservedState: "verified state change", Artifacts: []string{recordingID}})
		rangeCopy := ranges[index]
		plan.Shots = append(plan.Shots, model.DemoEditShot{ID: fmt.Sprintf("fact_shot_%02d", index+1), SourceArtifactID: recordingID, SourceStepID: stepID, SourceTimeRangeMS: &rangeCopy, Purpose: fmt.Sprintf("Verified factual step %d", index+1)})
	}
	return catalog, plan
}
