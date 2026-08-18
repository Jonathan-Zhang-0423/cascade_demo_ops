package executor

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

// NewSeedanceGeneratedShotCandidate converts one normalized Ark artifact into
// the provider-neutral review object consumed by the editor candidate flow.
// The result is deliberately non-authoritative and never carries timeline
// placement, approval, or auto-apply authority.
func NewSeedanceGeneratedShotCandidate(result model.ArkMediaGenerationResult, normalized model.ArtifactRef) (media.GeneratedShotCandidate, error) {
	if strings.TrimSpace(result.TaskID) == "" {
		return media.GeneratedShotCandidate{}, errors.New("seedance task id is required")
	}
	if result.Provider != "seedance" && result.Provider != "seedance-2.0" {
		return media.GeneratedShotCandidate{}, fmt.Errorf("unsupported generated candidate provider %q", result.Provider)
	}
	if result.Status != "candidate_artifacts_normalized" {
		return media.GeneratedShotCandidate{}, fmt.Errorf("seedance result is not normalized: %s", result.Status)
	}
	if !result.NonAuthoritative {
		return media.GeneratedShotCandidate{}, errors.New("seedance result must remain non-authoritative")
	}
	if !artifactMetadataBool(normalized.Metadata, "presentation_only") || artifactMetadataBool(normalized.Metadata, "approved_for_demo") || artifactMetadataBool(normalized.Metadata, "include_in_demo") {
		return media.GeneratedShotCandidate{}, errors.New("seedance normalized artifact has unsafe authority flags")
	}
	if artifactStringMetadata(normalized.Metadata, "artifact_variant") != "normalized" {
		return media.GeneratedShotCandidate{}, errors.New("seedance artifact is not marked normalized")
	}
	if !filepath.IsAbs(strings.TrimSpace(normalized.URI)) || strings.Contains(normalized.URI, "://") {
		return media.GeneratedShotCandidate{}, errors.New("seedance normalized artifact must use an absolute local path")
	}
	original, ok := findSeedanceOriginalArtifact(result.DownloadedArtifacts, normalized)
	if !ok {
		return media.GeneratedShotCandidate{}, errors.New("seedance normalized artifact has no matching original artifact")
	}
	probe, err := artifactMetadataProbe(normalized.Metadata, "normalized_media_probe")
	if err != nil {
		return media.GeneratedShotCandidate{}, err
	}
	candidate := media.GeneratedShotCandidate{
		SchemaVersion:    media.GeneratedShotCandidateSchemaVersion,
		CandidateID:      firstNonEmptyString(normalized.ID, result.ResultID+"_candidate"),
		IntentID:         firstNonEmptyString(result.SuggestionID, result.SourcePackageID),
		Provider:         media.GeneratedShotProviderSeedance20,
		ProviderTaskID:   result.TaskID,
		Status:           media.GeneratedShotCandidateReadyForReview,
		FailurePolicy:    media.GeneratedShotFailureContinue,
		NonAuthoritative: true, PresentationOnly: true, RequiresExplicitReview: true,
		ApprovedForDemo: false, IncludeInDemo: false,
		OriginalArtifact:   generatedShotArtifactFromArk(original, "original", nil),
		NormalizedArtifact: generatedShotArtifactFromArk(normalized, "normalized", &probe),
	}
	if err := media.ValidateGeneratedShotCandidate(candidate); err != nil {
		return media.GeneratedShotCandidate{}, err
	}
	return candidate, nil
}

func findSeedanceOriginalArtifact(artifacts []model.ArtifactRef, normalized model.ArtifactRef) (model.ArtifactRef, bool) {
	originalID := artifactStringMetadata(normalized.Metadata, "provider_original_artifact_id")
	for _, artifact := range artifacts {
		if artifact.ID == originalID && artifact.Metadata["artifact_variant"] == nil {
			return artifact, true
		}
	}
	return model.ArtifactRef{}, false
}

func generatedShotArtifactFromArk(artifact model.ArtifactRef, role string, probe *media.MiniMaxH3MediaProbe) media.GeneratedShotCandidateArtifact {
	return media.GeneratedShotCandidateArtifact{
		Role: role, Path: artifact.URI, MimeType: artifact.MimeType,
		SHA256: strings.ToLower(strings.TrimSpace(artifact.SHA256)), SizeBytes: artifact.SizeBytes,
		Probe:                generatedShotProbeFromMiniMax(probe),
		NormalizationStatus:  firstNonEmptyString(artifactStringMetadata(artifact.Metadata, "normalization_status"), ""),
		NormalizationProfile: artifactStringMetadata(artifact.Metadata, "normalization_profile"),
	}
}

func generatedShotProbeFromMiniMax(probe *media.MiniMaxH3MediaProbe) media.GeneratedShotMediaProbe {
	if probe == nil {
		return media.GeneratedShotMediaProbe{}
	}
	return media.GeneratedShotMediaProbe{Format: probe.Format, VideoCodec: probe.VideoCodec, PixelFormat: probe.PixelFormat, Width: probe.Width, Height: probe.Height, FPS: probe.FPS, CFR: probe.CFR, DurationSec: probe.DurationSec}
}

func artifactMetadataProbe(metadata map[string]any, key string) (media.MiniMaxH3MediaProbe, error) {
	value, ok := metadata[key]
	if !ok || value == nil {
		return media.MiniMaxH3MediaProbe{}, fmt.Errorf("artifact metadata %s is required", key)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return media.MiniMaxH3MediaProbe{}, fmt.Errorf("encode artifact metadata %s: %w", key, err)
	}
	var probe media.MiniMaxH3MediaProbe
	if err := json.Unmarshal(encoded, &probe); err != nil {
		return media.MiniMaxH3MediaProbe{}, fmt.Errorf("decode artifact metadata %s: %w", key, err)
	}
	return probe, nil
}

func artifactMetadataBool(metadata map[string]any, key string) bool {
	value, ok := metadata[key]
	if !ok {
		return false
	}
	if typed, ok := value.(bool); ok {
		return typed
	}
	return strings.EqualFold(strings.TrimSpace(fmt.Sprint(value)), "true")
}
