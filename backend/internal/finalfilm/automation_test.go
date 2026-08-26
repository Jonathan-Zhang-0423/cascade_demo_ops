package finalfilm

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

func TestDirectorEvidencePaletteComesFromObservedScreenshots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observed.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	bitmap := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			bitmap.Set(x, y, color.RGBA{R: 0x22, G: 0x88, B: 0xee, A: 0xff})
		}
	}
	if err := png.Encode(file, bitmap); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	_ = file.Close()
	catalog, baseline := finalFilmFixture()
	catalog.Artifacts = append(catalog.Artifacts, model.TimelineArtifact{ID: "observed_shot", Kind: "step_screenshot", MimeType: "image/png", LocalPath: path})
	job := model.FinalFilmJob{JobID: "palette_job", Constraints: model.StoryboardConstraintSet{ConstraintSetID: "palette_constraints"}, Catalog: catalog, BaselinePlan: baseline}
	digest, err := buildDirectorEvidenceDigest(job, false, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(digest.VisualStyle.DominantColors) == 0 || digest.VisualStyle.DominantColors[0] != "#2288ee" {
		t.Fatalf("observed screenshot palette was not extracted: %+v", digest.VisualStyle)
	}
}

func TestDirectorStoryDoesNotTreatObservedProvenanceAsWaiting(t *testing.T) {
	catalog, baseline := finalFilmFixture()
	for index := range catalog.Steps {
		catalog.Steps[index].ObservedState = "url_observed; title_observed; assertion passed"
	}
	job := model.FinalFilmJob{JobID: "story_wait_classification", Constraints: model.StoryboardConstraintSet{ConstraintSetID: "constraints_story"}, Catalog: catalog, BaselinePlan: baseline, PresentationIntents: guidedDemoPresentationIntents(catalog)}
	digest, err := buildDirectorEvidenceDigest(job, false, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	story, err := buildDirectorStoryPlan(job, digest)
	if err != nil {
		t.Fatal(err)
	}
	if story.TargetDurationMS != 105_000 {
		t.Fatalf("guided story target=%d want 105000", story.TargetDurationMS)
	}
	for _, segment := range story.Timeline {
		if segment.Kind == "fact" && segment.Speed != 1 {
			t.Fatalf("interactive fact segment was compressed by provenance text: %+v", segment)
		}
	}
}

func TestFitAutomatedFactTrackReconcilesExactTargetWithoutInventedMedia(t *testing.T) {
	first, second, generated := model.MillisecondRange{0, 30_000}, model.MillisecondRange{30_000, 60_000}, model.MillisecondRange{0, 4_000}
	plan := model.DemoEditPlan{Shots: []model.DemoEditShot{
		{ID: "fact_1", SourceStepID: "step_1", SourceTimeRangeMS: &first},
		{ID: "fact_2", SourceStepID: "step_2", SourceTimeRangeMS: &second},
		{ID: "generated", SourceTimeRangeMS: &generated, OutputDurationMS: 4_000},
	}}
	fitAutomatedFactTrackToTarget(&plan, 90_000)
	if got := timelineDuration(plan); got != 90_000 {
		t.Fatalf("fitted timeline duration=%d want 90000", got)
	}
	for _, shot := range plan.Shots[:2] {
		if speed := existingShotSpeed(shot); speed < 0.5 || speed > 1 {
			t.Fatalf("source-derived pacing left supported range: %f", speed)
		}
	}
}

func TestRecoverFailedAutomationRetriesOnlyProviderRejectedBeforeTaskCreation(t *testing.T) {
	service, _ := newFinalFilmTestService(t)
	policy := model.DefaultFinalFilmAutomationPolicy()
	intent, _ := model.PresentationGenerationIntentDefaults("divider", media.GeneratedShotPurposeSectionDivider, nil)
	record := GeneratedTrackRecord{SchemaVersion: generatedTrackRecordSchemaVersion, DirectorPlanID: "director_recovery", Intents: []media.GeneratedShotIntent{{IntentID: intent.IntentID, Purpose: intent.Purpose}}}
	raw, _ := json.Marshal(record)
	job := model.FinalFilmJob{
		SchemaVersion: model.FinalFilmJobSchemaVersion, JobID: "recover_provider_rejection", Revision: 1,
		State: model.FinalFilmJobFailed, Phase: "automation_failed", AutomationProfile: model.FinalFilmAutomationProfileGuidedDemoV1,
		AutomationPolicy: &policy, RunAuthorization: &model.FinalFilmRunAuthorization{AuthorizationRef: "approved", MaxProviderCalls: 8, ProviderCallsUsed: 2}, GeneratedTrack: raw,
		ProviderAttempts: []model.FinalFilmProviderAttempt{
			{IntentID: intent.IntentID, Provider: media.GeneratedShotProviderSeedance25, Attempt: 1, Status: "retry"},
			{IntentID: intent.IntentID, Provider: media.GeneratedShotProviderSeedance25, Attempt: 2, Status: "fallback_fact_track"},
		},
		QualityReports: []model.CandidateQualityReport{
			{IntentID: intent.IntentID, Provider: media.GeneratedShotProviderSeedance25, Attempt: 1, Decision: "retry", Findings: []string{"InvalidParameter.TaskTypeConstraint: omni_reference_task_type"}},
			{IntentID: intent.IntentID, Provider: media.GeneratedShotProviderSeedance25, Attempt: 2, Decision: "fallback_fact_track", Findings: []string{"InvalidParameter.TaskTypeConstraint: omni_reference_task_type"}},
		},
		LastError: &model.FinalFilmJobError{Retryable: true, Message: "request compiler mismatch"},
	}
	if err := service.store.CreateJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	recovered, err := service.recoverFailedAutomation(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != model.FinalFilmJobGeneratingPresentation || recovered.RunAuthorization.ProviderCallsUsed != 0 || len(recovered.QualityReports) != 0 {
		t.Fatalf("provider-rejected attempts were not reconciled: %+v", recovered)
	}
	for _, attempt := range recovered.ProviderAttempts {
		if attempt.Status != "superseded_pre_admission" || attempt.RecoveryCount != 1 {
			t.Fatalf("pre-admission audit record was not retained: %+v", attempt)
		}
	}
}

func TestRecoverFailedAutomationRetriesCompositionWithoutProviderConsumption(t *testing.T) {
	service, _ := newFinalFilmTestService(t)
	policy := model.DefaultFinalFilmAutomationPolicy()
	intent := media.GeneratedShotIntent{IntentID: "intro", Purpose: media.GeneratedShotPurposeIntro}
	record := GeneratedTrackRecord{SchemaVersion: generatedTrackRecordSchemaVersion, DirectorPlanID: "director_composition_recovery", Intents: []media.GeneratedShotIntent{intent}}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	job := model.FinalFilmJob{
		SchemaVersion: model.FinalFilmJobSchemaVersion, JobID: "recover_composition", Revision: 1,
		State: model.FinalFilmJobFailed, Phase: "automation_failed", AutomationProfile: model.FinalFilmAutomationProfileGuidedDemoV1,
		AutomationPolicy: &policy, RunAuthorization: &model.FinalFilmRunAuthorization{AuthorizationRef: "approved", MaxProviderCalls: 8, ProviderCallsUsed: 3}, GeneratedTrack: raw,
		QualityReports: []model.CandidateQualityReport{{IntentID: intent.IntentID, Provider: media.GeneratedShotProviderMiniMaxH3, Attempt: 1, Decision: "accept"}},
		LastError:      &model.FinalFilmJobError{Retryable: true, Message: "final requirement satisfaction report is not satisfied"},
	}
	if err := service.store.CreateJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	recovered, err := service.recoverFailedAutomation(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != model.FinalFilmJobQualityGate || recovered.RunAuthorization.ProviderCallsUsed != 3 || recovered.LastError != nil {
		t.Fatalf("composition recovery changed provider budget or failed to resume deterministically: %+v", recovered)
	}
}

func TestAutomatedBookendBlackGateAllowsShortFadeOnly(t *testing.T) {
	intro := media.GeneratedShotIntent{Purpose: media.GeneratedShotPurposeIntro}
	divider := media.GeneratedShotIntent{Purpose: media.GeneratedShotPurposeSectionDivider}
	if automatedBlackDurationLimitMS(intro) != 750 || automatedBlackDurationLimitMS(divider) != 500 {
		t.Fatal("purpose-aware black-frame limits drifted")
	}
}

func TestAutomatedProviderDefaultWaitsForSlowSuccessfulGeneration(t *testing.T) {
	service, _ := newFinalFilmTestService(t)
	if service.providerTimeout != 30*time.Minute {
		t.Fatalf("provider timeout=%s want 30m", service.providerTimeout)
	}
}

func TestGuidedDemoLocksFinalDeliveryTo1080p30(t *testing.T) {
	profile := automatedFinalDeliveryProfile(model.EditorRenderProfile{Mode: "final", Width: 2560, Height: 1440, FPS: 25, Format: "mov"})
	if profile.Width != 1920 || profile.Height != 1080 || profile.FPS != 30 || profile.Format != "mp4" || profile.Preset != "medium" || profile.CRF != 18 {
		t.Fatalf("guided delivery profile drifted: %+v", profile)
	}
	job := model.FinalFilmJob{AutomationProfile: model.FinalFilmAutomationProfileGuidedDemoV1, FinalOutputValidation: &model.FinalFilmOutputValidation{Width: 2560, Height: 1440, FPS: 30}}
	if !needsAutomatedDeliveryProfileReconcile(job) {
		t.Fatal("legacy 1440p final output was not selected for deterministic 1080p reconciliation")
	}
	job.FinalOutputValidation.Width, job.FinalOutputValidation.Height = 1920, 1080
	if needsAutomatedDeliveryProfileReconcile(job) {
		t.Fatal("valid 1080p30 final output should remain waiting for human review")
	}
}

func TestAutomatedFactCaptionsHideInternalEvidenceVocabulary(t *testing.T) {
	start, end := 0, 2000
	plan := model.DemoEditPlan{Shots: []model.DemoEditShot{{
		ID: "fact", SourceStepID: "step", Purpose: "source=browser_assertion",
		Overlays: []model.EditOverlay{{Type: model.EditOverlayCaption, Text: "source=browser_assertion; assertion:required_numeric_increased=passed", StartMS: &start, EndMS: &end}},
	}}}
	catalog := model.AssetTimelineCatalog{Steps: []model.TimelineStep{{StepID: "step", Action: "inspect", ObservedState: "source=browser_assertion; assertion:required_numeric_increased=passed"}}}
	replaceAutomatedFactCaptions(&plan, catalog)
	if got := plan.Shots[0].Overlays[0].Text; got != "关键业务指标已确认增长" || automatedPlanContainsInternalCaption(&plan) {
		t.Fatalf("internal evidence vocabulary leaked into the final caption: %q", got)
	}
}

func TestAutomatedFactTrackAddsSiteNeutralAmbientMotion(t *testing.T) {
	rangeMS := model.MillisecondRange{0, 4000}
	plan := model.DemoEditPlan{Shots: []model.DemoEditShot{{ID: "fact", SourceStepID: "verified_step", SourceTimeRangeMS: &rangeMS}}}
	if !automatedPlanMissingAmbientMotion(&plan) {
		t.Fatal("long factual shot should request deterministic ambient motion")
	}
	addAutomatedAmbientMotion(&plan)
	if automatedPlanMissingAmbientMotion(&plan) || len(plan.Shots[0].Operations) != 1 || plan.Shots[0].Operations[0].Style != "ambient_motion" {
		t.Fatalf("ambient motion was not compiled: %+v", plan.Shots[0].Operations)
	}
	addAutomatedAmbientMotion(&plan)
	if len(plan.Shots[0].Operations) != 1 {
		t.Fatal("ambient motion compilation is not idempotent")
	}
}

func TestAutomatedFinalFreezeGateUsesBoundedDurationRatio(t *testing.T) {
	baseline := executor.MediaProbeResult{DurationMS: 65_556, FreezeDurationMS: 64_597}
	if got := automatedFinalFreezeAllowanceMS(baseline, 105_000); got != 87_000 {
		t.Fatalf("bounded freeze allowance=%d want 87000", got)
	}
	dynamicBaseline := executor.MediaProbeResult{DurationMS: 60_000, FreezeDurationMS: 12_000}
	if got := automatedFinalFreezeAllowanceMS(dynamicBaseline, 105_000); got != 24_000 {
		t.Fatalf("relative freeze allowance=%d want 24000", got)
	}
}

func TestReconcileAutomatedProviderAttemptAuditUsesMatchingQualityReport(t *testing.T) {
	checkedAt := time.Date(2026, 8, 26, 15, 0, 0, 0, time.UTC)
	attempts := []model.FinalFilmProviderAttempt{
		{IntentID: "outro", Provider: media.GeneratedShotProviderMiniMaxH3, Attempt: 2, Status: "submitted"},
		{IntentID: "divider", Provider: media.GeneratedShotProviderSeedance25, Attempt: 2, Status: "submitted"},
	}
	reports := []model.CandidateQualityReport{
		{IntentID: "outro", Provider: media.GeneratedShotProviderMiniMaxH3, Attempt: 2, Decision: "accept", CheckedAt: checkedAt},
		{IntentID: "divider", Provider: media.GeneratedShotProviderSeedance25, Attempt: 2, Decision: "fallback_fact_track", CheckedAt: checkedAt},
	}
	reconciled := reconcileAutomatedProviderAttemptAudit(attempts, reports)
	if reconciled[0].Status != "accept" || reconciled[1].Status != "fallback_fact_track" || reconciled[0].CompletedAt != checkedAt {
		t.Fatalf("provider attempt audit was not reconciled: %+v", reconciled)
	}
}

func TestPersistAutomatedProviderResultUpdatesMatchingAttemptAfterInterleavedResume(t *testing.T) {
	service, _ := newFinalFilmTestService(t)
	policy := model.DefaultFinalFilmAutomationPolicy()
	intent := media.GeneratedShotIntent{IntentID: "outro", Purpose: media.GeneratedShotPurposeOutro}
	record := GeneratedTrackRecord{SchemaVersion: generatedTrackRecordSchemaVersion, Intents: []media.GeneratedShotIntent{intent}}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	job := model.FinalFilmJob{
		SchemaVersion: model.FinalFilmJobSchemaVersion, JobID: "interleaved_resume", Revision: 1,
		State: model.FinalFilmJobGeneratingPresentation, AutomationPolicy: &policy, GeneratedTrack: raw,
		ProviderAttempts: []model.FinalFilmProviderAttempt{
			{IntentID: intent.IntentID, Provider: media.GeneratedShotProviderMiniMaxH3, Attempt: 2, Status: "submitted", ProviderTaskID: "h3_outro"},
			{IntentID: "divider", Provider: media.GeneratedShotProviderSeedance25, Attempt: 2, Status: "submitted", ProviderTaskID: "seedance_divider"},
		},
	}
	if err := service.store.CreateJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	result := media.GeneratedShotProviderExecutionResult{Provider: media.GeneratedShotProviderMiniMaxH3, ProviderTaskID: "h3_outro", ErrorMessage: "provider output rejected"}
	updated, err := service.persistAutomatedProviderResult(context.Background(), job, record, intent, 2, result)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ProviderAttempts[0].Status != "fallback_fact_track" || updated.ProviderAttempts[1].Status != "submitted" {
		t.Fatalf("matching interleaved attempt was not updated: %+v", updated.ProviderAttempts)
	}
}

func TestGuidedDemoCreatesServerOwnedSlotsAndFixedProviderWorkflow(t *testing.T) {
	service, _ := newFinalFilmTestService(t)
	catalog, baseline := finalFilmFixture()
	clientIntent, _ := model.PresentationGenerationIntentDefaults("client_chosen", "brand_atmosphere", nil)
	job, err := service.CreateJob(context.Background(), CreateJobRequest{
		EditorSessionID: "editor_auto", EditorRevision: 1, SourcePackageID: "package_auto",
		Catalog: catalog, BaselinePlan: baseline, Intents: []model.PresentationGenerationIntent{clientIntent},
		RenderProfile: finalFilmProfile(), AutomationProfile: model.FinalFilmAutomationProfileGuidedDemoV1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if job.AutomationPolicy == nil || len(job.PresentationIntents) != 3 {
		t.Fatalf("guided demo did not create intro/divider/outro slots: %+v", job)
	}
	wantPurposes := []string{"intro", "section_divider", "outro"}
	for index, intent := range job.PresentationIntents {
		if intent.Purpose != wantPurposes[index] || intent.RequestedSlot.PreferredDurationSec != 4 {
			t.Fatalf("unexpected server-owned intent %d: %+v", index, intent)
		}
	}
	policy := job.AutomationPolicy.ProviderPolicy
	if providerForAutomatedPurpose(media.GeneratedShotPurposeIntro, policy) != media.GeneratedShotProviderMiniMaxH3 || providerForAutomatedPurpose(media.GeneratedShotPurposeOutro, policy) != media.GeneratedShotProviderMiniMaxH3 || providerForAutomatedPurpose(media.GeneratedShotPurposeSectionDivider, policy) != media.GeneratedShotProviderSeedance25 {
		t.Fatalf("provider purpose mapping drifted: %+v", policy)
	}
}

func TestRepositoryDirectorSkillRuntimesLoadWithoutSiteBindings(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", "..", "skills", "final-film"))
	runtimes, err := LoadDirectorSkillRuntimes(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimes) != 5 || runtimes["final-film-director-harness"].Version != "1.1.0" {
		t.Fatalf("unexpected Director skill registry: %+v", runtimes)
	}
}

func TestReviewSupplementsRejectTraversalDuplicateAndReservedManifest(t *testing.T) {
	for name, supplements := range map[string][]model.FinalFilmReviewSupplement{
		"traversal":         {{Role: "product_spec", SourcePath: "source.json", RelativePath: "../source.json", Required: true}},
		"duplicate role":    {{Role: "plan", SourcePath: "a.json", RelativePath: "experiment/a.json"}, {Role: "plan", SourcePath: "b.json", RelativePath: "experiment/b.json"}},
		"reserved manifest": {{Role: "manifest", SourcePath: "manifest.json", RelativePath: "experiment/manifest.json"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateReviewSupplements(supplements); err == nil {
				t.Fatal("expected review supplement validation failure")
			}
		})
	}
	if err := validateReviewSupplements([]model.FinalFilmReviewSupplement{{Role: "product_spec", SourcePath: "source.json", RelativePath: "experiment/product-spec.json", Required: true}}); err != nil {
		t.Fatalf("valid review supplement rejected: %v", err)
	}
}

func TestAutomationPersistsEvidenceAttemptsQualityAndFactFallback(t *testing.T) {
	service, _ := newFinalFilmTestService(t)
	registry := media.NewGeneratedShotProviderRegistry()
	if err := registry.Register(&fakeGeneratedShotProvider{root: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	service.providers = registry
	service.planner = &recordingDirectorPlanner{}
	catalog, baseline := finalFilmFixture()
	job, err := service.CreateJob(context.Background(), CreateJobRequest{EditorSessionID: "editor_auto", EditorRevision: 1, SourcePackageID: "package_auto", Catalog: catalog, BaselinePlan: baseline, RenderProfile: finalFilmProfile(), AutomationProfile: model.FinalFilmAutomationProfileGuidedDemoV1})
	if err != nil {
		t.Fatal(err)
	}
	job, err = service.AuthorizeAutomation(context.Background(), job.JobID, job.Revision, "test-authorization", 8)
	if err != nil {
		t.Fatal(err)
	}
	_, resumeErr := service.ResumeAutomation(context.Background(), job.JobID)
	if resumeErr == nil {
		t.Fatal("short fixture should fail the 90-120 second final duration gate")
	}
	job, err = service.GetJob(context.Background(), job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.EvidenceDigest == nil || job.DirectorPlan == nil || job.DirectorPlan.StoryPlan == nil {
		t.Fatalf("automatic Director evidence/plan was not persisted: %+v", job)
	}
	if job.RunAuthorization.ProviderCallsUsed != 2 || len(job.ProviderAttempts) != 2 {
		t.Fatalf("only H3 intro/outro should consume calls when Seedance is unavailable: auth=%+v attempts=%+v", job.RunAuthorization, job.ProviderAttempts)
	}
	for _, attempt := range job.ProviderAttempts {
		if attempt.ProviderTaskID == "" || attempt.Status != "accept" {
			t.Fatalf("provider task submission checkpoint was not carried into the terminal attempt: %+v", attempt)
		}
	}
	seedanceReports, fallbackReports := 0, 0
	for _, report := range job.QualityReports {
		if report.Provider == media.GeneratedShotProviderSeedance25 {
			seedanceReports++
		}
		if report.Decision == "fallback_fact_track" {
			fallbackReports++
		}
	}
	if seedanceReports != 2 || fallbackReports != 1 {
		t.Fatalf("Seedance failure did not use exactly one retry then fact fallback: %+v", job.QualityReports)
	}
}

func TestReviewPackageAndFinalReviewAreRevisionBound(t *testing.T) {
	service, _ := newFinalFilmTestService(t)
	catalog, baseline := finalFilmFixture()
	root := t.TempDir()
	rawPath := filepath.Join(root, "raw.mp4")
	baselinePath := filepath.Join(root, "baseline.mp4")
	finalPath := filepath.Join(root, "final.mp4")
	manifestPath := filepath.Join(root, "render-manifest.json")
	supplementPath := filepath.Join(root, "experiment-plan.json")
	for path, data := range map[string]string{rawPath: "raw", baselinePath: "baseline", finalPath: "final", manifestPath: `{}`, supplementPath: `{"plan":"bounded"}`} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	catalog.Artifacts[0].LocalPath, catalog.Artifacts[0].URI = rawPath, rawPath
	job, err := service.CreateJob(context.Background(), CreateJobRequest{EditorSessionID: "editor_package", EditorRevision: 1, SourcePackageID: "package_review", Catalog: catalog, BaselinePlan: baseline, RenderProfile: finalFilmProfile(), AutomationProfile: model.FinalFilmAutomationProfileGuidedDemoV1})
	if err != nil {
		t.Fatal(err)
	}
	job.BaselineRender.VideoPath = baselinePath
	job.FinalRender.VideoPath, job.FinalRender.RenderManifestPath = finalPath, manifestPath
	job.FinalOutputValidation = &model.FinalFilmOutputValidation{VideoSHA256: "stored-output-digest"}
	job.FinalPlan = &baseline
	job.ReviewSupplements = []model.FinalFilmReviewSupplement{{Role: "experiment_observation_plan", SourcePath: supplementPath, RelativePath: "experiment/build-observation-plan.json", Required: true}}
	record := GeneratedTrackRecord{SchemaVersion: generatedTrackRecordSchemaVersion, DirectorPlanID: "director_package"}
	job.Revision++
	pkg, err := service.buildReviewPackage(context.Background(), job, record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pkg.ZIPPath); err != nil || len(pkg.Files) < 7 {
		t.Fatalf("review package is incomplete: %+v err=%v", pkg, err)
	}
	foundSupplement := false
	for _, file := range pkg.Files {
		foundSupplement = foundSupplement || file.Role == "experiment_observation_plan" && file.RelativePath == "experiment/build-observation-plan.json"
	}
	if !foundSupplement {
		t.Fatalf("experiment review supplement is missing: %+v", pkg.Files)
	}
	firstZIP, err := os.ReadFile(pkg.ZIPPath)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, err := service.buildReviewPackage(context.Background(), job, record)
	if err != nil {
		t.Fatal(err)
	}
	secondZIP, err := os.ReadFile(rebuilt.ZIPPath)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.PackageID != pkg.PackageID || string(firstZIP) != string(secondZIP) {
		t.Fatal("review package is not idempotent for the same job revision")
	}
	job.State, job.Phase, job.ReviewPackage = model.FinalFilmJobAwaitingFinalReview, "awaiting_final_review", &pkg
	if err := service.store.TransitionJob(context.Background(), job.JobID, job.Revision-1, job, service.event(job, job.Phase, "ready", nil)); err != nil {
		t.Fatal(err)
	}
	accepted, err := service.RecordFinalReview(context.Background(), job.JobID, job.Revision, "accept", "", "human-reviewer", pkg.PackageID)
	if err != nil || accepted.State != model.FinalFilmJobCompleted || accepted.FinalReview == nil {
		t.Fatalf("final review was not revision-bound: job=%+v err=%v", accepted, err)
	}
}
