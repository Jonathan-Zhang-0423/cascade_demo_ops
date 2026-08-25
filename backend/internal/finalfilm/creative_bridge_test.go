package finalfilm

import (
	"testing"
	"time"

	"cascade-demoops/backend/internal/creative"
	"cascade-demoops/backend/internal/model"
)

func TestCompileCreativeDirectorPlanUsesExistingFinalFilmBoundary(t *testing.T) {
	bundle := creativeTestBundle()
	job := model.FinalFilmJob{JobID: "job_1", Constraints: model.StoryboardConstraintSet{ConstraintSetID: "constraints_1", AllowedArtifactIDs: []string{"recording"}}}
	shots := []model.CreativeShotIntent{{SchemaVersion: model.CreativeShotIntentSchemaVersion, IntentID: "intro_1", Role: model.CreativeShotRoleEstablishing, Prompt: "abstract enterprise intro", DurationSec: 5, AspectRatio: "16:9", ReferenceArtifactIDs: []string{"recording"}, AllowedProviders: []string{"minimax-h3"}, FallbackPolicy: model.CreativeShotFallbackContinue, PresentationOnly: true}}
	pkg, err := creative.BuildCreativeDirectorPackage(bundle, shots, "director_pkg_1", time.Unix(3, 0))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := CompileCreativeDirectorPlan(pkg, job)
	if err != nil {
		t.Fatal(err)
	}
	if plan.JobID != job.JobID || len(plan.Specs) != 1 || plan.Specs[0].Provider != "" || plan.Specs[0].PromptSHA256 == "" {
		t.Fatalf("unexpected bridged plan: %+v", plan)
	}
}

func creativeTestBundle() model.CreativeSourceBundle {
	bundle := model.CreativeSourceBundle{SchemaVersion: model.CreativeSourceBundleSchemaVersion, BundleID: "bundle", SourcePackageID: "pkg", CreatedAt: time.Unix(1, 0), Lineage: model.CreativeSourceLineage{SourceBindingDecision: "confirmed", SourceBindingAssessmentHash: "sha256:assessment", RecordingResultPackageID: "pkg"}, NecessaryFacts: []model.CreativeFact{{ID: "fact", Statement: "done"}}, EvidenceArtifacts: []model.CreativeEvidenceArtifact{{ID: "recording", URI: "asset://recording", ProviderReference: "asset://recording", Kind: "raw_recording", Role: "fact", Immutable: true}}, DegradationPolicy: model.CreativeDegradationPolicy{AllowProviderFallback: true, AllowDeterministicBroll: true, AllowFactBaseline: true}}
	bundle.SourceDigest, _ = model.ComputeCreativeSourceDigest(bundle)
	return bundle
}
