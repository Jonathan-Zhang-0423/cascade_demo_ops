package finalfilm

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

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
