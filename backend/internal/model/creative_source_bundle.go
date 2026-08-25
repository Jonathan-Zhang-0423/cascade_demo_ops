package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	CreativeSourceBundleSchemaVersion       = "demoops.creative_source_bundle.v2"
	CreativeSourceBundleLegacySchemaVersion = "demoops.creative_source_bundle.v1"
)

// CreativeSourceBundleV1 is the persisted pre-v2 shape. It is intentionally
// kept separate from CreativeSourceBundle so a legacy payload cannot be
// mistaken for a v2 artifact with silently missing lineage or action data.
type CreativeSourceBundleV1 struct {
	SchemaVersion        string                     `json:"schema_version"`
	BundleID             string                     `json:"bundle_id"`
	SourcePackageID      string                     `json:"source_package_id"`
	CreatedAt            time.Time                  `json:"created_at"`
	SourceDigest         string                     `json:"source_digest"`
	NecessaryFacts       []CreativeFact             `json:"necessary_facts"`
	EvidenceArtifacts    []CreativeEvidenceArtifact `json:"evidence_artifacts"`
	ImmutableFields      []string                   `json:"immutable_fields"`
	AllowedCreativeScope []string                   `json:"allowed_creative_scope"`
	DegradationPolicy    CreativeDegradationPolicy  `json:"degradation_policy"`
}

// MigrateCreativeSourceBundleV1 performs an explicit, auditable v1 -> v2
// migration. Lineage and required action coverage are supplied by the
// already-authorized source-binding/execution boundary; they are never
// inferred from creative input.
func MigrateCreativeSourceBundleV1(legacy CreativeSourceBundleV1, lineage CreativeSourceLineage, coverage []CreativeActionCoverage) (CreativeSourceBundle, error) {
	if legacy.SchemaVersion != CreativeSourceBundleLegacySchemaVersion {
		return CreativeSourceBundle{}, errors.New("unsupported legacy creative source bundle schema")
	}
	if strings.TrimSpace(legacy.SourceDigest) != "" && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(legacy.SourceDigest)), "sha256:") {
		return CreativeSourceBundle{}, errors.New("legacy source_digest must be a sha256: digest when present")
	}
	artifacts := make([]CreativeEvidenceArtifact, 0, len(legacy.EvidenceArtifacts))
	for _, artifact := range legacy.EvidenceArtifacts {
		artifact.ProviderReference = ""
		if providerReferenceAllowed(artifact.URI) {
			artifact.ProviderReference = strings.TrimSpace(artifact.URI)
		}
		artifacts = append(artifacts, artifact)
	}
	bundle := CreativeSourceBundle{
		SchemaVersion:          CreativeSourceBundleSchemaVersion,
		BundleID:               legacy.BundleID,
		SourcePackageID:        legacy.SourcePackageID,
		CreatedAt:              legacy.CreatedAt,
		Lineage:                lineage,
		NecessaryFacts:         append([]CreativeFact{}, legacy.NecessaryFacts...),
		EvidenceArtifacts:      artifacts,
		RequiredActionCoverage: append([]CreativeActionCoverage{}, coverage...),
		ImmutableFields:        append([]string{}, legacy.ImmutableFields...),
		AllowedCreativeScope:   append([]string{}, legacy.AllowedCreativeScope...),
		DegradationPolicy:      legacy.DegradationPolicy,
	}
	if len(bundle.ImmutableFields) == 0 {
		bundle.ImmutableFields = []string{"lineage", "necessary_facts", "evidence_artifacts", "required_action_coverage", "source_digest"}
	}
	if err := ValidateCreativeSourceBundleLineage(lineage); err != nil {
		return CreativeSourceBundle{}, err
	}
	digest, err := ComputeCreativeSourceDigest(bundle)
	if err != nil {
		return CreativeSourceBundle{}, err
	}
	bundle.SourceDigest = digest
	if err := ValidateCreativeSourceBundle(bundle); err != nil {
		return CreativeSourceBundle{}, fmt.Errorf("validate migrated creative source bundle: %w", err)
	}
	return bundle, nil
}

// CreativeSourceBundle is a read-only projection of already-authorized facts.
// It is not a second fact ledger and does not own a business lifecycle.
type CreativeSourceBundle struct {
	SchemaVersion          string                     `json:"schema_version"`
	BundleID               string                     `json:"bundle_id"`
	SourcePackageID        string                     `json:"source_package_id"`
	CreatedAt              time.Time                  `json:"created_at"`
	SourceDigest           string                     `json:"source_digest"`
	Lineage                CreativeSourceLineage      `json:"lineage"`
	NecessaryFacts         []CreativeFact             `json:"necessary_facts"`
	EvidenceArtifacts      []CreativeEvidenceArtifact `json:"evidence_artifacts"`
	RequiredActionCoverage []CreativeActionCoverage   `json:"required_action_coverage,omitempty"`
	ImmutableFields        []string                   `json:"immutable_fields"`
	AllowedCreativeScope   []string                   `json:"allowed_creative_scope"`
	DegradationPolicy      CreativeDegradationPolicy  `json:"degradation_policy"`
}

type CreativeSourceLineage struct {
	SourceBindingDecision       string   `json:"source_binding_decision"`
	SourceBindingAssessmentHash string   `json:"source_binding_assessment_hash"`
	SourceSnapshotDigests       []string `json:"source_snapshot_digests,omitempty"`
	RecordingResultPackageID    string   `json:"recording_result_package_id,omitempty"`
}

type CreativeActionCoverage struct {
	ActionID                 string `json:"action_id"`
	Action                   string `json:"action"`
	Required                 bool   `json:"required"`
	TargetSelector           string `json:"target_selector,omitempty"`
	SourceEvidenceArtifactID string `json:"source_evidence_artifact_id,omitempty"`
	Status                   string `json:"status"`
}

type CreativeFact struct {
	ID          string   `json:"id"`
	Statement   string   `json:"statement"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
	Required    bool     `json:"required"`
}

type CreativeEvidenceArtifact struct {
	ID                string  `json:"id"`
	URI               string  `json:"uri"`
	ProviderReference string  `json:"provider_reference,omitempty"`
	Kind              string  `json:"kind"`
	MimeType          string  `json:"mime_type,omitempty"`
	SHA256            string  `json:"sha256,omitempty"`
	SourceTimeMS      *[2]int `json:"source_time_range_ms,omitempty"`
	Role              string  `json:"role"`
	Sensitive         bool    `json:"sensitive,omitempty"`
	Immutable         bool    `json:"immutable"`
}

type CreativeDegradationPolicy struct {
	AllowProviderFallback   bool `json:"allow_provider_fallback"`
	AllowDeterministicBroll bool `json:"allow_deterministic_broll"`
	AllowFactBaseline       bool `json:"allow_fact_baseline"`
}

func ValidateCreativeSourceBundle(bundle CreativeSourceBundle) error {
	if bundle.SchemaVersion != CreativeSourceBundleSchemaVersion {
		return errors.New("unsupported creative source bundle schema")
	}
	if strings.TrimSpace(bundle.BundleID) == "" || strings.TrimSpace(bundle.SourcePackageID) == "" || bundle.CreatedAt.IsZero() {
		return errors.New("bundle identity, source package, and created_at are required")
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(bundle.SourceDigest)), "sha256:") {
		return errors.New("source_digest must be a sha256: digest")
	}
	if err := ValidateCreativeSourceBundleLineage(bundle.Lineage); err != nil {
		return err
	}
	if len(bundle.NecessaryFacts) == 0 {
		return errors.New("at least one necessary fact is required")
	}
	facts := map[string]struct{}{}
	for i, fact := range bundle.NecessaryFacts {
		if strings.TrimSpace(fact.ID) == "" || strings.TrimSpace(fact.Statement) == "" {
			return fmt.Errorf("necessary_facts[%d] requires id and statement", i)
		}
		if _, exists := facts[fact.ID]; exists {
			return fmt.Errorf("necessary_facts[%d].id is duplicated", i)
		}
		facts[fact.ID] = struct{}{}
	}
	artifacts := map[string]struct{}{}
	for i, artifact := range bundle.EvidenceArtifacts {
		if strings.TrimSpace(artifact.ID) == "" || strings.TrimSpace(artifact.URI) == "" || strings.TrimSpace(artifact.Role) == "" {
			return fmt.Errorf("evidence_artifacts[%d] requires id, uri, and role", i)
		}
		if artifact.Sensitive || !artifact.Immutable {
			return fmt.Errorf("evidence_artifacts[%d] must be immutable and non-sensitive", i)
		}
		if reference := strings.TrimSpace(artifact.ProviderReference); reference != "" && !providerReferenceAllowed(reference) {
			return fmt.Errorf("evidence_artifacts[%d].provider_reference must be HTTPS or asset://", i)
		}
		if _, exists := artifacts[artifact.ID]; exists {
			return fmt.Errorf("evidence_artifacts[%d].id is duplicated", i)
		}
		artifacts[artifact.ID] = struct{}{}
	}
	for i, coverage := range bundle.RequiredActionCoverage {
		if strings.TrimSpace(coverage.ActionID) == "" || strings.TrimSpace(coverage.Action) == "" {
			return fmt.Errorf("required_action_coverage[%d] requires action_id and action", i)
		}
		switch coverage.Status {
		case "verified", "missing", "page_only":
		default:
			return fmt.Errorf("required_action_coverage[%d].status is unsupported", i)
		}
		if coverage.Required && coverage.Status == "verified" && strings.TrimSpace(coverage.TargetSelector) == "" {
			return fmt.Errorf("required_action_coverage[%d] verified required action needs target_selector", i)
		}
	}
	if !bundle.DegradationPolicy.AllowFactBaseline {
		return errors.New("fact baseline degradation must remain enabled")
	}
	computed, err := ComputeCreativeSourceDigest(bundle)
	if err != nil {
		return err
	}
	if !strings.EqualFold(computed, strings.TrimSpace(bundle.SourceDigest)) {
		return errors.New("source_digest does not match canonical bundle contents")
	}
	return nil
}

func ValidateCreativeSourceBundleLineage(lineage CreativeSourceLineage) error {
	if strings.TrimSpace(lineage.SourceBindingDecision) == "" || strings.TrimSpace(lineage.SourceBindingAssessmentHash) == "" {
		return errors.New("source binding decision and assessment hash are required in lineage")
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(lineage.SourceBindingAssessmentHash)), "sha256:") {
		return errors.New("source binding assessment hash must be a sha256: digest")
	}
	for _, digest := range lineage.SourceSnapshotDigests {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(digest)), "sha256:") {
			return errors.New("source snapshot digests must be sha256: digests")
		}
	}
	return nil
}

func ComputeCreativeSourceDigest(bundle CreativeSourceBundle) (string, error) {
	copyBundle := bundle
	copyBundle.Lineage.SourceSnapshotDigests = append([]string{}, bundle.Lineage.SourceSnapshotDigests...)
	copyBundle.SourceDigest = ""
	copyBundle.ImmutableFields = append([]string{}, bundle.ImmutableFields...)
	copyBundle.AllowedCreativeScope = append([]string{}, bundle.AllowedCreativeScope...)
	copyBundle.NecessaryFacts = make([]CreativeFact, len(bundle.NecessaryFacts))
	for index, fact := range bundle.NecessaryFacts {
		copyBundle.NecessaryFacts[index] = fact
		copyBundle.NecessaryFacts[index].EvidenceIDs = append([]string{}, fact.EvidenceIDs...)
	}
	copyBundle.EvidenceArtifacts = append([]CreativeEvidenceArtifact{}, bundle.EvidenceArtifacts...)
	copyBundle.RequiredActionCoverage = append([]CreativeActionCoverage{}, bundle.RequiredActionCoverage...)
	sort.Strings(copyBundle.Lineage.SourceSnapshotDigests)
	sort.Strings(copyBundle.ImmutableFields)
	sort.Strings(copyBundle.AllowedCreativeScope)
	for index := range copyBundle.NecessaryFacts {
		sort.Strings(copyBundle.NecessaryFacts[index].EvidenceIDs)
	}
	sort.Slice(copyBundle.NecessaryFacts, func(i, j int) bool { return copyBundle.NecessaryFacts[i].ID < copyBundle.NecessaryFacts[j].ID })
	sort.Slice(copyBundle.EvidenceArtifacts, func(i, j int) bool { return copyBundle.EvidenceArtifacts[i].ID < copyBundle.EvidenceArtifacts[j].ID })
	sort.Slice(copyBundle.RequiredActionCoverage, func(i, j int) bool {
		return copyBundle.RequiredActionCoverage[i].ActionID < copyBundle.RequiredActionCoverage[j].ActionID
	})
	raw, err := json.Marshal(copyBundle)
	if err != nil {
		return "", fmt.Errorf("marshal creative source digest input: %w", err)
	}
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func providerReferenceAllowed(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "asset://")
}
