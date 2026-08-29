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

func TestAutomatedFactTrackDoesNotUseSlowMotionToFillTarget(t *testing.T) {
	first, second, generated := model.MillisecondRange{0, 30_000}, model.MillisecondRange{30_000, 60_000}, model.MillisecondRange{0, 4_000}
	plan := model.DemoEditPlan{Shots: []model.DemoEditShot{
		{ID: "fact_1", SourceStepID: "step_1", SourceTimeRangeMS: &first},
		{ID: "fact_2", SourceStepID: "step_2", SourceTimeRangeMS: &second},
		{ID: "generated", SourceTimeRangeMS: &generated, OutputDurationMS: 4_000},
	}}
	if got := timelineDuration(plan); got != 64_000 {
		t.Fatalf("source timeline duration=%d want 64000", got)
	}
	for _, shot := range plan.Shots[:2] {
		if speed := existingShotSpeed(shot); speed != 1 {
			t.Fatalf("factual footage was slowed to fill time: %f", speed)
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
		AutomationPolicy: &policy, RunAuthorization: &model.FinalFilmRunAuthorization{AuthorizationRef: "approved", MaxProviderCalls: 6, ProviderCallsUsed: 2}, GeneratedTrack: raw,
		ProviderAttempts: []model.FinalFilmProviderAttempt{
			{IntentID: intent.IntentID, Provider: media.GeneratedShotProviderSeedance25, Attempt: 1, Status: "retry"},
			{IntentID: intent.IntentID, Provider: media.GeneratedShotProviderSeedance25, Attempt: 2, Status: "provider_revision_required"},
		},
		QualityReports: []model.CandidateQualityReport{
			{IntentID: intent.IntentID, Provider: media.GeneratedShotProviderSeedance25, Attempt: 1, Decision: "retry", Findings: []string{"InvalidParameter.TaskTypeConstraint: omni_reference_task_type"}},
			{IntentID: intent.IntentID, Provider: media.GeneratedShotProviderSeedance25, Attempt: 2, Decision: "provider_revision_required", Findings: []string{"InvalidParameter.TaskTypeConstraint: omni_reference_task_type"}},
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
		AutomationPolicy: &policy, RunAuthorization: &model.FinalFilmRunAuthorization{AuthorizationRef: "approved", MaxProviderCalls: 6, ProviderCallsUsed: 3}, GeneratedTrack: raw,
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

func TestCandidateVisualQualityGateReviewsOneAttemptAsOneBatch(t *testing.T) {
	service, _ := newFinalFilmTestService(t)
	policy := model.DefaultFinalFilmAutomationPolicy()
	intents := []media.GeneratedShotIntent{{IntentID: "intro", Purpose: media.GeneratedShotPurposeIntro}, {IntentID: "divider", Purpose: media.GeneratedShotPurposeSectionDivider}, {IntentID: "outro", Purpose: media.GeneratedShotPurposeOutro}}
	reports := []model.CandidateQualityReport{}
	attempts := []model.FinalFilmProviderAttempt{}
	for _, intent := range intents {
		provider := providerForAutomatedPurpose(intent.Purpose, policy.ProviderPolicy)
		reports = append(reports, model.CandidateQualityReport{SchemaVersion: model.CandidateQualityReportSchemaVersion, IntentID: intent.IntentID, CandidateID: "candidate_" + intent.IntentID, Provider: provider, Attempt: 1, TechnicalPass: true, TemporalPass: true, Decision: "awaiting_content_review", ContactSheetPath: "sheet_" + intent.IntentID + ".jpg"})
		attempts = append(attempts, model.FinalFilmProviderAttempt{IntentID: intent.IntentID, Provider: provider, Attempt: 1, Status: "awaiting_content_review"})
	}
	job := model.FinalFilmJob{SchemaVersion: model.FinalFilmJobSchemaVersion, JobID: "batch_review", Revision: 1, State: model.FinalFilmJobGeneratingPresentation, AutomationProfile: model.FinalFilmAutomationProfileGuidedDemoV1, AutomationPolicy: &policy, QualityReports: reports, ProviderAttempts: attempts}
	if err := service.store.CreateJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	pending := pendingAutomatedQualityBatch(intents, reports)
	if len(pending) != 3 {
		t.Fatalf("candidate batch was fragmented: %+v", pending)
	}
	updated, err := service.reviewAutomatedCandidateBatch(t.Context(), job, GeneratedTrackRecord{SchemaVersion: generatedTrackRecordSchemaVersion, Intents: intents}, pending)
	if err != nil {
		t.Fatal(err)
	}
	if updated.DirectorVisualCallsUsed != 1 {
		t.Fatalf("batch consumed %d visual calls", updated.DirectorVisualCallsUsed)
	}
	for _, report := range updated.QualityReports {
		if report.Decision != "accept" || !report.TextPass || !report.ContentPass {
			t.Fatalf("candidate did not pass four-part gate: %+v", report)
		}
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
		ID: "fact", SourceStepID: "step", SourceArtifactID: "visible_fact", Purpose: "source=browser_assertion",
		Overlays: []model.EditOverlay{{Type: model.EditOverlayCaption, Text: "source=browser_assertion; assertion:required_numeric_increased=passed", StartMS: &start, EndMS: &end}},
	}}}
	facts := []model.PublicNarrativeFact{{SchemaVersion: model.PublicNarrativeFactSchemaVersion, FactID: "fact_1", Chapter: "interaction", ApprovedCaptionVariants: []string{"真实操作后分数增加"}, VisibleEvidenceRefs: []string{"visible_fact"}, SourceKind: "verified_product_fact"}}
	catalog := model.AssetTimelineCatalog{Steps: []model.TimelineStep{{StepID: "step", Order: 1, Required: true, Artifacts: []string{"visible_fact"}}}}
	replaceAutomatedFactCaptions(&plan, facts, catalog)
	if got := plan.Shots[0].Overlays[0].Text; got != "真实操作后分数增加" || automatedPlanContainsInternalCaption(&plan) {
		t.Fatalf("internal evidence vocabulary leaked into the final caption: %q", got)
	}
}

func TestAutomatedFactTrackRejectsDynamicMotion(t *testing.T) {
	rangeMS := model.MillisecondRange{0, 4000}
	plan := model.DemoEditPlan{Shots: []model.DemoEditShot{{ID: "fact", SourceStepID: "verified_step", SourceTimeRangeMS: &rangeMS, Operations: []model.EditOperation{{Type: model.EditOperationZoomPan, Style: "ambient_motion"}}}}}
	if !automatedPlanContainsDynamicFactMotion(&plan) {
		t.Fatal("time-varying motion on factual footage must be rejected")
	}
	plan.Shots[0].Operations = nil
	if automatedPlanContainsDynamicFactMotion(&plan) {
		t.Fatal("unmodified factual footage must pass the motion boundary")
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
		{IntentID: "divider", Provider: media.GeneratedShotProviderSeedance25, Attempt: 2, Decision: "provider_revision_required", CheckedAt: checkedAt},
	}
	reconciled := reconcileAutomatedProviderAttemptAudit(attempts, reports)
	if reconciled[0].Status != "accept" || reconciled[1].Status != "provider_revision_required" || reconciled[0].CompletedAt != checkedAt {
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
	if updated.ProviderAttempts[0].Status != "provider_revision_required" || updated.ProviderAttempts[1].Status != "submitted" {
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
	paletteID := ""
	for _, artifact := range job.Catalog.Artifacts {
		if artifact.Kind == "generated_palette_reference" {
			paletteID = artifact.ID
			if artifact.Metadata["text_free"] != true || artifact.Metadata["ui_free"] != true || artifact.Metadata["timeline_insertable"] != false {
				t.Fatalf("palette reference is not explicitly text/UI free and non-timeline: %+v", artifact)
			}
		}
	}
	if paletteID == "" {
		t.Fatal("guided demo did not create a Server-owned palette reference")
	}
	for index, intent := range job.PresentationIntents {
		if intent.Purpose != wantPurposes[index] || intent.RequestedSlot.PreferredDurationSec != 4 || len(intent.ReferenceAssetRefs) != 1 || intent.ReferenceAssetRefs[0] != paletteID {
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
	if len(runtimes) != 5 || runtimes["final-film-director-harness"].Version != "2.0.0" {
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

func TestAutomationPersistsEvidenceAttemptsQualityAndBlocksRequiredProviderSlot(t *testing.T) {
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
	job, err = service.AuthorizeAutomation(context.Background(), job.JobID, job.Revision, "test-authorization", 6)
	if err != nil {
		t.Fatal(err)
	}
	_, resumeErr := service.ResumeAutomation(context.Background(), job.JobID)
	if resumeErr != nil {
		t.Fatal(resumeErr)
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
	seedanceReports, blockedReports := 0, 0
	for _, report := range job.QualityReports {
		if report.Provider == media.GeneratedShotProviderSeedance25 {
			seedanceReports++
		}
		if report.Decision == "provider_revision_required" {
			blockedReports++
		}
	}
	if seedanceReports != 2 || blockedReports != 1 || job.State != model.FinalFilmJobRevisionRequested || job.Phase != "provider_revision_required" {
		t.Fatalf("Seedance failure did not use exactly one retry then block delivery: %+v", job.QualityReports)
	}
}

func TestInterruptedBaselineRecoveryKeepsJobAndBudgetWhileReplacingFactPlan(t *testing.T) {
	service, _ := newFinalFilmTestService(t)
	catalog, baseline := finalFilmFixture()
	job, err := service.CreateJob(context.Background(), CreateJobRequest{EditorSessionID: "editor_interrupted", EditorRevision: 1, SourcePackageID: "package_interrupted", Catalog: catalog, BaselinePlan: baseline, RenderProfile: finalFilmProfile(), AutomationProfile: model.FinalFilmAutomationProfileGuidedDemoV1})
	if err != nil {
		t.Fatal(err)
	}
	job, err = service.AuthorizeAutomation(context.Background(), job.JobID, job.Revision, "test-authorization", 6)
	if err != nil {
		t.Fatal(err)
	}
	rendering := job
	rendering.State, rendering.Phase = model.FinalFilmJobRenderingBaseline, "rendering_fact_track"
	rendering.Revision++
	rendering.UpdatedAt = time.Now().UTC()
	if err := service.store.TransitionJob(context.Background(), job.JobID, job.Revision, rendering, service.event(rendering, rendering.Phase, "test interrupted render", nil)); err != nil {
		t.Fatal(err)
	}
	revised := baseline
	revised.PlanID += "_bounded"
	revised.TargetDurationMS = 90_000
	recovered, err := service.RecoverInterruptedBaseline(context.Background(), job.JobID, rendering.Revision, CreateJobRequest{
		EditorSessionID: "editor_recovered", EditorRevision: 2, SourcePackageID: "package_recovered",
		Catalog: catalog, BaselinePlan: revised, RenderProfile: finalFilmProfile(),
		PublicNarrativeFacts: rendering.PublicNarrativeFacts, MediaCoverage: rendering.MediaCoverage,
	})
	if err != nil {
		t.Fatal(err)
	}
	if recovered.JobID != job.JobID || recovered.State != model.FinalFilmJobAnalyzingEvidence || recovered.BaselinePlan.PlanID != revised.PlanID {
		t.Fatalf("interrupted baseline did not recover in-place: %+v", recovered)
	}
	if recovered.RunAuthorization == nil || recovered.RunAuthorization.ProviderCallsUsed != 0 || recovered.SourcePackageID != "package_recovered" {
		t.Fatalf("recovery changed authorization or failed to rebind source: %+v", recovered)
	}
	for _, intent := range recovered.PresentationIntents {
		if len(intent.ReferenceAssetRefs) != 1 {
			t.Fatalf("text-free provider reference was lost: %+v", intent)
		}
	}
}

func TestFailedDirectorPlacementRecoveryReturnsToPlanningWithoutProviderSpend(t *testing.T) {
	service, _ := newFinalFilmTestService(t)
	catalog, baseline := finalFilmFixture()
	job, err := service.CreateJob(context.Background(), CreateJobRequest{EditorSessionID: "editor_director_recovery", EditorRevision: 1, SourcePackageID: "package_director_recovery", Catalog: catalog, BaselinePlan: baseline, RenderProfile: finalFilmProfile(), AutomationProfile: model.FinalFilmAutomationProfileGuidedDemoV1})
	if err != nil {
		t.Fatal(err)
	}
	job, err = service.AuthorizeAutomation(context.Background(), job.JobID, job.Revision, "test-authorization", 6)
	if err != nil {
		t.Fatal(err)
	}
	failed := job
	failed.State, failed.Phase = model.FinalFilmJobFailed, "automation_failed"
	failed.EvidenceDigest = &model.DirectorEvidenceDigest{SchemaVersion: model.DirectorEvidenceDigestSchemaVersion, DigestID: "digest_recovery"}
	failed.LastError = &model.FinalFilmJobError{Code: "automation_failed", Message: "invalid generated placement for guided_section_divider_01", Retryable: true, OccurredAt: time.Now().UTC()}
	failed.Revision++
	failed.UpdatedAt = time.Now().UTC()
	if err := service.store.TransitionJob(context.Background(), job.JobID, job.Revision, failed, service.event(failed, failed.Phase, "test failed director", nil)); err != nil {
		t.Fatal(err)
	}
	recovered, err := service.RecoverFailedAutomation(context.Background(), job.JobID, failed.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != model.FinalFilmJobPlanning || recovered.Phase != "director_placement_reconciled" || recovered.LastError != nil {
		t.Fatalf("Director planning failure did not recover: %+v", recovered)
	}
	if recovered.RunAuthorization == nil || recovered.RunAuthorization.ProviderCallsUsed != 0 {
		t.Fatalf("Director planning recovery consumed provider budget: %+v", recovered.RunAuthorization)
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
	contactSheetPath := filepath.Join(root, "final-contact-sheet.jpg")
	for path, data := range map[string]string{rawPath: "raw", baselinePath: "baseline", finalPath: "final", manifestPath: `{}`, supplementPath: `{"plan":"bounded"}`, contactSheetPath: "contact sheet"} {
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
	job.FinalVisualQuality = &model.FinalVisualQualityReport{SchemaVersion: "demoops.final_visual_quality_report.v1", TemporalPass: true, TextPass: true, ContentPass: true, ContactSheetPath: contactSheetPath, CheckedAt: time.Now().UTC()}
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
	foundSupplement, foundPalette := false, false
	for _, file := range pkg.Files {
		foundSupplement = foundSupplement || file.Role == "experiment_observation_plan" && file.RelativePath == "experiment/build-observation-plan.json"
		foundPalette = foundPalette || file.Role == "provider_palette_reference"
	}
	if !foundSupplement || !foundPalette {
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
