package creative

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

// BuildSourceBundle creates the one-time creative handoff from the existing
// factual asset catalog. It records references and lineage only; it does not
// perform another Browser Agent or OutcomeVerifier run.
func BuildSourceBundle(bundleID, sourcePackageID string, catalog model.AssetTimelineCatalog, facts []model.CreativeFact, lineage model.CreativeSourceLineage, coverage []model.CreativeActionCoverage, now time.Time) (model.CreativeSourceBundle, error) {
	if strings.TrimSpace(bundleID) == "" || strings.TrimSpace(sourcePackageID) == "" {
		return model.CreativeSourceBundle{}, fmt.Errorf("bundle_id and source_package_id are required")
	}
	if len(facts) == 0 {
		return model.CreativeSourceBundle{}, fmt.Errorf("at least one necessary fact is required")
	}
	artifacts := make([]model.CreativeEvidenceArtifact, 0, len(catalog.Artifacts))
	for _, artifact := range catalog.Artifacts {
		if !artifact.IncludeInDemo || artifact.Sensitive {
			continue
		}
		role := strings.TrimSpace(artifact.AssetRole)
		if role == "" {
			role = "fact_evidence"
		}
		providerReference := ""
		uri := strings.TrimSpace(artifact.URI)
		if strings.HasPrefix(strings.ToLower(uri), "https://") || strings.HasPrefix(strings.ToLower(uri), "asset://") {
			providerReference = uri
		}
		artifacts = append(artifacts, model.CreativeEvidenceArtifact{ID: artifact.ID, URI: uri, ProviderReference: providerReference, Kind: artifact.Kind, MimeType: artifact.MimeType, SHA256: artifact.SHA256, Role: role, Sensitive: artifact.Sensitive, Immutable: true})
	}
	if len(artifacts) == 0 {
		return model.CreativeSourceBundle{}, fmt.Errorf("catalog has no non-sensitive demo artifacts")
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].ID < artifacts[j].ID })
	bundle := model.CreativeSourceBundle{
		SchemaVersion: model.CreativeSourceBundleSchemaVersion, BundleID: bundleID, SourcePackageID: sourcePackageID,
		CreatedAt: now.UTC(), Lineage: lineage, NecessaryFacts: append([]model.CreativeFact{}, facts...), EvidenceArtifacts: artifacts,
		RequiredActionCoverage: append([]model.CreativeActionCoverage{}, coverage...), ImmutableFields: []string{"lineage", "necessary_facts", "evidence_artifacts", "required_action_coverage", "source_digest"},
		AllowedCreativeScope: []string{"presentation_only", "camera_language", "pacing", "color_grade", "music_sync"},
		DegradationPolicy:    model.CreativeDegradationPolicy{AllowProviderFallback: true, AllowDeterministicBroll: true, AllowFactBaseline: true},
	}
	digest, err := model.ComputeCreativeSourceDigest(bundle)
	if err != nil {
		return model.CreativeSourceBundle{}, err
	}
	bundle.SourceDigest = digest
	if err := model.ValidateCreativeSourceBundle(bundle); err != nil {
		return model.CreativeSourceBundle{}, err
	}
	return bundle, nil
}
