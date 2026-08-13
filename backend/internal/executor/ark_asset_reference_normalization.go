package executor

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

const seedanceReferenceDurationSec = 5

func normalizeArkVideoReferences(ctx context.Context, requirements []model.ArkMediaSourceAssetRequirement, outputDir string, normalizer media.MiniMaxH3MediaNormalizer) ([]model.ArkMediaSourceAssetRequirement, []model.ArkMediaReadinessFinding) {
	updated := append([]model.ArkMediaSourceAssetRequirement{}, requirements...)
	findings := []model.ArkMediaReadinessFinding{}
	for index := range updated {
		requirement := &updated[index]
		if !requiresArkVideoReferenceNormalization(*requirement) {
			continue
		}
		if normalizer == nil {
			findings = append(findings, model.ArkMediaReadinessFinding{Code: "seedance_reference_normalizer_missing", Message: "FFmpeg and FFprobe are required to create a bounded Seedance MP4 reference derivative", RefID: requirement.Ref.ID, TaskID: requirement.TaskID})
			continue
		}
		sourcePath, ok := localDirectorMaterialPath(requirement.Ref.URI)
		if !ok {
			continue
		}
		destination := filepath.Join(outputDir, "model-references", safeID(firstNonEmptyString(requirement.Ref.ID, "seedance_reference"))+".mp4")
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			findings = append(findings, arkReferenceNormalizationFinding(requirement, err))
			continue
		}
		originalProbe, normalizedProbe, err := normalizer.Normalize(ctx, sourcePath, destination)
		if err != nil {
			findings = append(findings, arkReferenceNormalizationFinding(requirement, err))
			continue
		}
		digest, size, err := arkMediaFileDigest(destination)
		if err != nil {
			findings = append(findings, arkReferenceNormalizationFinding(requirement, err))
			continue
		}
		originalRef := requirement.Ref
		metadata := cloneArtifactMetadata(originalRef.Metadata)
		metadata["artifact_variant"] = "seedance_reference_derivative"
		metadata["source_artifact_id"] = originalRef.ID
		metadata["source_sha256"] = originalRef.SHA256
		metadata["normalization_profile"] = media.GeneratedShotNormalizationProfile
		metadata["normalization_status"] = "ok"
		metadata["media_probe_status"] = "ok"
		metadata["max_duration_sec"] = seedanceReferenceDurationSec
		metadata["original_media_probe"] = originalProbe
		metadata["normalized_media_probe"] = normalizedProbe
		metadata["model_reference_only"] = true
		metadata["include_in_demo"] = false
		requirement.Ref = model.DirectorMaterialRef{
			ID: originalRef.ID + "_seedance_reference", Kind: "model_reference_video", URI: destination,
			MimeType: "video/mp4", SHA256: digest, SizeBytes: size, AssetRole: "seedance_reference_video",
			IncludeInDemo: false, Sensitive: originalRef.Sensitive, Metadata: metadata,
		}
		requirement.CurrentURIIsPublic = false
		requirement.Status = "needs_public_uri"
		requirement.ActionRequired = "publish the normalized model-reference derivative as a short-lived HTTPS URL"
	}
	return updated, findings
}

func requiresArkVideoReferenceNormalization(requirement model.ArkMediaSourceAssetRequirement) bool {
	if requirement.TaskID != "seedance_reference_director_preview" || !strings.HasPrefix(strings.ToLower(requirement.Ref.MimeType), "video/") {
		return false
	}
	return !mimeTypeAllowed(requirement.Ref.MimeType, requirement.AcceptedMimeTypes)
}

func localDirectorMaterialPath(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if len(raw) >= 2 && raw[1] == ':' {
		return filepath.Clean(raw), true
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "file" {
		return "", false
	}
	pathValue, err := url.PathUnescape(parsed.Path)
	if err != nil {
		return "", false
	}
	pathValue = filepath.FromSlash(pathValue)
	if len(pathValue) >= 3 && pathValue[0] == filepath.Separator && pathValue[2] == ':' {
		pathValue = pathValue[1:]
	}
	return filepath.Clean(pathValue), true
}

func arkReferenceNormalizationFinding(requirement *model.ArkMediaSourceAssetRequirement, err error) model.ArkMediaReadinessFinding {
	return model.ArkMediaReadinessFinding{
		Code: "seedance_reference_normalization_failed", Message: fmt.Sprintf("could not create the bounded Seedance MP4 reference derivative: %v", err),
		RefID: requirement.Ref.ID, TaskID: requirement.TaskID,
	}
}
