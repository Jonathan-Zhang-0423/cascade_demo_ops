package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMigrateCreativeSourceBundleV1RequiresExplicitLineage(t *testing.T) {
	legacy := CreativeSourceBundleV1{
		SchemaVersion: CreativeSourceBundleLegacySchemaVersion,
		BundleID:      "bundle-v1", SourcePackageID: "package-v1", CreatedAt: time.Unix(10, 0).UTC(),
		NecessaryFacts:    []CreativeFact{{ID: "fact-1", Statement: "workflow completed", Required: true}},
		EvidenceArtifacts: []CreativeEvidenceArtifact{{ID: "recording", URI: "asset://recording", Kind: "video", Role: "fact_baseline", Immutable: true}},
		ImmutableFields:   []string{"necessary_facts", "evidence_artifacts", "source_digest"},
		DegradationPolicy: CreativeDegradationPolicy{AllowProviderFallback: true, AllowDeterministicBroll: true, AllowFactBaseline: true},
	}
	if _, err := MigrateCreativeSourceBundleV1(legacy, CreativeSourceLineage{}, nil); err == nil {
		t.Fatal("expected migration to reject missing lineage")
	}
}

func TestMigrateCreativeSourceBundleV1ProducesCanonicalV2Artifact(t *testing.T) {
	legacy := CreativeSourceBundleV1{
		SchemaVersion: CreativeSourceBundleLegacySchemaVersion,
		BundleID:      "bundle-v1", SourcePackageID: "package-v1", CreatedAt: time.Unix(10, 0).UTC(), SourceDigest: "sha256:legacy",
		NecessaryFacts: []CreativeFact{{ID: "fact-1", Statement: "workflow completed", Required: true}},
		EvidenceArtifacts: []CreativeEvidenceArtifact{
			{ID: "remote", URI: "https://cdn.example/recording.mp4", Kind: "video", Role: "fact_baseline", Immutable: true},
			{ID: "local", URI: "C:/server/private/recording.mp4", Kind: "video", Role: "fact_baseline", Immutable: true},
		},
		ImmutableFields:      []string{"necessary_facts", "evidence_artifacts", "source_digest"},
		AllowedCreativeScope: []string{"presentation_only"},
		DegradationPolicy:    CreativeDegradationPolicy{AllowProviderFallback: true, AllowDeterministicBroll: true, AllowFactBaseline: true},
	}
	lineage := CreativeSourceLineage{SourceBindingDecision: "confirm_mixed", SourceBindingAssessmentHash: "sha256:assessment", SourceSnapshotDigests: []string{"sha256:snapshot"}, RecordingResultPackageID: "recording-pkg"}
	bundle, err := MigrateCreativeSourceBundleV1(legacy, lineage, []CreativeActionCoverage{{ActionID: "submit", Action: "submit", Required: true, TargetSelector: "#submit", Status: "verified"}})
	if err != nil {
		t.Fatal(err)
	}
	if bundle.SchemaVersion != CreativeSourceBundleSchemaVersion || !reflect.DeepEqual(bundle.Lineage, lineage) || !strings.HasPrefix(bundle.SourceDigest, "sha256:") {
		t.Fatalf("unexpected migrated bundle: %+v", bundle)
	}
	if bundle.EvidenceArtifacts[0].ProviderReference != "https://cdn.example/recording.mp4" || bundle.EvidenceArtifacts[1].ProviderReference != "" {
		t.Fatalf("provider reference boundary was not enforced: %+v", bundle.EvidenceArtifacts)
	}
	if err := ValidateCreativeSourceBundle(bundle); err != nil {
		t.Fatal(err)
	}
}

func TestCreativeSourceBundleV2JSONRejectsDigestTampering(t *testing.T) {
	bundle := CreativeSourceBundle{
		SchemaVersion: CreativeSourceBundleSchemaVersion, BundleID: "bundle", SourcePackageID: "package", CreatedAt: time.Unix(1, 0).UTC(),
		Lineage:           CreativeSourceLineage{SourceBindingDecision: "confirmed", SourceBindingAssessmentHash: "sha256:assessment"},
		NecessaryFacts:    []CreativeFact{{ID: "fact", Statement: "done"}},
		EvidenceArtifacts: []CreativeEvidenceArtifact{{ID: "recording", URI: "asset://recording", ProviderReference: "asset://recording", Kind: "video", Role: "fact_baseline", Immutable: true}},
		DegradationPolicy: CreativeDegradationPolicy{AllowProviderFallback: true, AllowDeterministicBroll: true, AllowFactBaseline: true},
	}
	bundle.SourceDigest, _ = ComputeCreativeSourceDigest(bundle)
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	var tampered CreativeSourceBundle
	if err := json.Unmarshal(raw, &tampered); err != nil {
		t.Fatal(err)
	}
	tampered.NecessaryFacts[0].Statement = "changed"
	if err := ValidateCreativeSourceBundle(tampered); err == nil {
		t.Fatal("expected digest validation to reject tampering")
	}
}

func TestCreativeSourceBundleRejectsProviderReferenceOverreach(t *testing.T) {
	bundle := CreativeSourceBundle{
		SchemaVersion: CreativeSourceBundleSchemaVersion, BundleID: "bundle", SourcePackageID: "package", CreatedAt: time.Unix(1, 0).UTC(),
		Lineage:           CreativeSourceLineage{SourceBindingDecision: "confirmed", SourceBindingAssessmentHash: "sha256:assessment"},
		NecessaryFacts:    []CreativeFact{{ID: "fact", Statement: "done"}},
		EvidenceArtifacts: []CreativeEvidenceArtifact{{ID: "recording", URI: "asset://recording", ProviderReference: "file:///private/recording.mp4", Kind: "video", Role: "fact_baseline", Immutable: true}},
		DegradationPolicy: CreativeDegradationPolicy{AllowProviderFallback: true, AllowDeterministicBroll: true, AllowFactBaseline: true},
	}
	bundle.SourceDigest, _ = ComputeCreativeSourceDigest(bundle)
	if err := ValidateCreativeSourceBundle(bundle); err == nil {
		t.Fatal("expected provider reference validation to reject private URI")
	}
}

func TestCreativeSourceDigestCanonicalizesUnorderedCollections(t *testing.T) {
	base := CreativeSourceBundle{
		SchemaVersion: CreativeSourceBundleSchemaVersion, BundleID: "bundle", SourcePackageID: "package", CreatedAt: time.Unix(1, 0).UTC(),
		Lineage:           CreativeSourceLineage{SourceBindingDecision: "confirmed", SourceBindingAssessmentHash: "sha256:assessment", SourceSnapshotDigests: []string{"sha256:z", "sha256:a"}},
		NecessaryFacts:    []CreativeFact{{ID: "fact", Statement: "done", EvidenceIDs: []string{"e2", "e1"}}},
		EvidenceArtifacts: []CreativeEvidenceArtifact{{ID: "recording", URI: "asset://recording", ProviderReference: "asset://recording", Kind: "video", Role: "fact_baseline", Immutable: true}},
		ImmutableFields:   []string{"source_digest", "lineage"}, AllowedCreativeScope: []string{"color_grade", "presentation_only"},
		DegradationPolicy: CreativeDegradationPolicy{AllowProviderFallback: true, AllowDeterministicBroll: true, AllowFactBaseline: true},
	}
	reordered := base
	reordered.Lineage.SourceSnapshotDigests = []string{"sha256:a", "sha256:z"}
	reordered.NecessaryFacts = []CreativeFact{{ID: "fact", Statement: "done", EvidenceIDs: []string{"e1", "e2"}}}
	reordered.ImmutableFields = []string{"lineage", "source_digest"}
	reordered.AllowedCreativeScope = []string{"presentation_only", "color_grade"}
	first, err := ComputeCreativeSourceDigest(base)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ComputeCreativeSourceDigest(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("digest was not canonical: %s != %s", first, second)
	}
}
