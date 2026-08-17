package finalfilm

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"cascade-demoops/backend/internal/executor"
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
	intent, _ := model.PresentationGenerationIntentDefaults("intro_1", "intro", []string{"shot_1"})
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
	job, err = service.DecideGeneration(context.Background(), job.JobID, job.Revision, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != model.FinalFilmJobGeneratingCandidates || !job.GenerationAuthorized {
		t.Fatalf("generation was not explicitly authorized: %+v", job)
	}
	job, err = service.RecordGenerationFailure(context.Background(), job.JobID, job.Revision, "provider_timeout")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != model.FinalFilmJobCompletedWithoutGenerated || job.FinalRender.VideoPath != job.BaselineRender.VideoPath || job.GenerationSkipReason != "provider_timeout" {
		t.Fatalf("baseline fallback failed: %+v", job)
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

func (f *fakeFinalFilmRenderer) ValidateEditPlan(_ context.Context, _ executor.EditPlanValidationRequest) (model.DemoEditPlanValidationReport, error) {
	f.validateCalls++
	return f.validation, f.validationErr
}

func (f *fakeFinalFilmRenderer) Render(_ context.Context, request executor.RenderRequest) (executor.RenderResult, error) {
	f.renderCalls++
	if request.ModelExecution == nil || request.ModelExecution.Invoked || request.ModelExecution.ProviderOutputAdopted {
		return executor.RenderResult{}, errors.New("baseline render model audit is unsafe")
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
	sequence := 0
	service, err := NewService(ServiceOptions{
		Store: NewFileStore(filepath.Join(root, "jobs")), Renderer: renderer, OutputRoot: filepath.Join(root, "outputs"),
		Now: func() time.Time { sequence++; return time.Unix(int64(100+sequence), 0) },
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
