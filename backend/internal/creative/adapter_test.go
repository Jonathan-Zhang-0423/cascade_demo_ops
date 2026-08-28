package creative

import (
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func testBundle() model.CreativeSourceBundle {
	bundle := model.CreativeSourceBundle{
		SchemaVersion: model.CreativeSourceBundleSchemaVersion,
		BundleID:      "bundle_1", SourcePackageID: "pkg_1", CreatedAt: time.Unix(1, 0).UTC(),
		Lineage:              model.CreativeSourceLineage{SourceBindingDecision: "confirmed", SourceBindingAssessmentHash: "sha256:assessment", RecordingResultPackageID: "pkg_1"},
		NecessaryFacts:       []model.CreativeFact{{ID: "fact_1", Statement: "The recorded workflow completed."}},
		EvidenceArtifacts:    []model.CreativeEvidenceArtifact{{ID: "recording", URI: "asset://recording", ProviderReference: "asset://recording", Kind: "raw_recording", Role: "fact_baseline", Immutable: true}},
		AllowedCreativeScope: []string{"intro", "transition"},
		DegradationPolicy:    model.CreativeDegradationPolicy{AllowProviderFallback: true, AllowDeterministicBroll: true, AllowFactBaseline: true},
	}
	bundle.SourceDigest, _ = model.ComputeCreativeSourceDigest(bundle)
	return bundle
}

func TestAdaptToPresentationIntentsKeepsProviderNeutralBoundary(t *testing.T) {
	shots := []model.CreativeShotIntent{{SchemaVersion: model.CreativeShotIntentSchemaVersion, IntentID: "shot_1", Role: model.CreativeShotRoleEstablishing, Prompt: "abstract enterprise light", DurationSec: 5, AspectRatio: "16:9", ReferenceArtifactIDs: []string{"recording"}, AllowedProviders: []string{"minimax-h3", "seedance-2.5"}, FallbackPolicy: model.CreativeShotFallbackContinue, PresentationOnly: true}}
	intents, err := AdaptToPresentationIntents(testBundle(), shots)
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 1 || intents[0].Provider != "" || intents[0].Model != "" || intents[0].SourceStepID != "" {
		t.Fatalf("adapter leaked provider or fact bindings: %+v", intents)
	}
}

func TestCompileDemoEditPlanUsesDeterministicRoleOperations(t *testing.T) {
	plan, err := CompileDemoEditPlan(testBundle(), []EditShotInput{{ID: "hero", SourceArtifactID: "recording", Role: model.CreativeShotRoleHeroTransition, Purpose: "hero transition"}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.SourceMaterialPolicy != model.DemoEditSourceMaterialPolicyExistingAssetsOnly || len(plan.Shots) != 1 || len(plan.Shots[0].Operations) != 1 {
		t.Fatalf("unexpected deterministic plan: %+v", plan)
	}
}

func TestCompileDemoEditPlanRequiresVerifiedGeometryBeforeZoom(t *testing.T) {
	bundle := testBundle()
	bundle.EvidenceArtifacts = append(bundle.EvidenceArtifacts, model.CreativeEvidenceArtifact{ID: "geometry", URI: "asset://geometry", ProviderReference: "asset://geometry", Kind: "screenshot", Role: "target_geometry", Immutable: true})
	bundle.SourceDigest, _ = model.ComputeCreativeSourceDigest(bundle)
	plan, err := CompileDemoEditPlan(bundle, []EditShotInput{{ID: "hero", SourceArtifactID: "recording", GeometryArtifactID: "geometry", TargetGeometryVerified: true, Role: model.CreativeShotRoleHeroTransition, Purpose: "hero transition"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Shots[0].Operations) != 2 || plan.Shots[0].Operations[1].Type != model.EditOperationZoomPan {
		t.Fatalf("verified geometry should enable zoom only after evidence validation: %+v", plan.Shots[0].Operations)
	}
}

func TestBuildSourceBundleDerivesStableDigestFromCatalog(t *testing.T) {
	catalog := model.AssetTimelineCatalog{Artifacts: []model.TimelineArtifact{{ID: "recording", URI: "asset://recording", Kind: "raw_recording", IncludeInDemo: true, SHA256: "sha256:1"}}}
	bundle, err := BuildSourceBundle("bundle_2", "pkg_2", catalog, []model.CreativeFact{{ID: "fact", Statement: "done"}}, model.CreativeSourceLineage{SourceBindingDecision: "confirmed", SourceBindingAssessmentHash: "sha256:assessment", RecordingResultPackageID: "pkg_2"}, nil, time.Unix(2, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(bundle.SourceDigest, "sha256:") || len(bundle.EvidenceArtifacts) != 1 {
		t.Fatalf("unexpected source bundle: %+v", bundle)
	}
}

func TestAdaptToGeneratedShotIntentsCarriesProviderAllowlistAsPolicy(t *testing.T) {
	shots := []model.CreativeShotIntent{{SchemaVersion: model.CreativeShotIntentSchemaVersion, IntentID: "shot_2", Role: model.CreativeShotRoleHeroTransition, Prompt: "abstract", DurationSec: 5, AspectRatio: "16:9", AllowedProviders: []string{"minimax-h3", "seedance-2.5"}, FallbackPolicy: model.CreativeShotFallbackContinue, PresentationOnly: true}}
	intents, err := AdaptToGeneratedShotIntents(testBundle(), shots)
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 1 || len(intents[0].AllowedProviders) != 2 {
		t.Fatalf("provider pool policy was lost: %+v", intents)
	}
}
