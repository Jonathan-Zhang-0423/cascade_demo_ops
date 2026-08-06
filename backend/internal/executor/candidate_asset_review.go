package executor

import (
	"path/filepath"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

func ReviewArkMediaCandidateAssets(source *model.ClientExecutionPackage, generationResult *model.ArkMediaGenerationResult, createdAt time.Time) model.CandidateAssetReview {
	sourcePackageID := ""
	if source != nil {
		sourcePackageID = source.PackageID
	}
	if generationResult != nil && generationResult.SourcePackageID != "" {
		sourcePackageID = generationResult.SourcePackageID
	}
	review := model.CandidateAssetReview{
		SchemaVersion:          model.CandidateAssetReviewSchemaVersion,
		ReviewID:               "candidate_asset_review_" + safeID(firstNonEmptyString(sourcePackageID, "unknown_package")),
		CreatedAt:              createdAt,
		SourcePackageID:        sourcePackageID,
		Status:                 "no_candidates",
		Policy:                 candidateAssetReviewPolicy(),
		Items:                  []model.CandidateAssetReviewItem{},
		ApprovedArtifacts:      []model.ArtifactRef{},
		PendingReviewArtifacts: []model.ArtifactRef{},
		RejectedArtifacts:      []model.ArtifactRef{},
		Warnings:               []model.ArkMediaReadinessFinding{},
		Notes: []string{
			"Media-eligible candidates remain unapproved until a user explicitly reviews their content.",
			"User approval only allows future DemoEditPlan references; it never automatically includes generated candidates in the final video.",
		},
	}
	if generationResult == nil {
		review.Status = "generation_result_missing"
		review.Warnings = append(review.Warnings, model.ArkMediaReadinessFinding{
			Code:    "generation_result_missing",
			Message: "candidate asset review skipped because ark media generation result is missing",
		})
		return review
	}
	review.GenerationResultID = generationResult.ResultID
	if len(generationResult.DownloadedArtifacts) == 0 {
		review.Warnings = append(review.Warnings, model.ArkMediaReadinessFinding{
			Code:    "downloaded_candidates_missing",
			Message: "no downloaded provider candidates are available for review",
			TaskID:  generationResult.TaskID,
		})
		return review
	}

	eligibleCount := 0
	for index, artifact := range generationResult.DownloadedArtifacts {
		item, reviewedArtifact := reviewCandidateAsset(artifact)
		review.Items = append(review.Items, item)
		generationResult.DownloadedArtifacts[index] = reviewedArtifact
		if item.MediaEligible {
			eligibleCount++
			review.PendingReviewArtifacts = append(review.PendingReviewArtifacts, reviewedArtifact)
		} else {
			review.RejectedArtifacts = append(review.RejectedArtifacts, reviewedArtifact)
		}
	}
	switch {
	case eligibleCount == len(generationResult.DownloadedArtifacts):
		review.Status = "media_eligible_awaiting_user_review"
	case eligibleCount > 0:
		review.Status = "partially_media_eligible_awaiting_user_review"
	default:
		review.Status = "rejected"
	}
	return review
}

func candidateAssetReviewPolicy() model.CandidateAssetReviewPolicy {
	return model.CandidateAssetReviewPolicy{
		DecisionMode:               "media_validation_only",
		SourceMaterialPolicy:       "non_authoritative_generated_candidate",
		AllowedKinds:               []string{"generated_video_candidate"},
		RequiresLocalFile:          true,
		RequiresNonAuthoritative:   true,
		RequiresPresentationOnly:   true,
		AutoIncludeInDemo:          false,
		RequiresExplicitUserReview: true,
	}
}

func reviewCandidateAsset(artifact model.ArtifactRef) (model.CandidateAssetReviewItem, model.ArtifactRef) {
	reasons := []string{}
	risks := []string{}
	findings := []model.ArkMediaReadinessFinding{}
	mediaEligible := true
	reviewed := artifact
	reviewed.Metadata = cloneArtifactMetadata(reviewed.Metadata)

	if artifact.Kind != "generated_video_candidate" {
		mediaEligible = false
		findings = append(findings, candidateReviewFinding("unsupported_candidate_kind", "only generated_video_candidate can be approved for deterministic rendering", artifact.ID))
	}
	if !strings.HasPrefix(strings.ToLower(artifact.MimeType), "video/") {
		mediaEligible = false
		findings = append(findings, candidateReviewFinding("unsupported_candidate_mime", "only video candidates can be approved by the current compositor", artifact.ID))
	}
	if !isLocalArtifactURI(artifact.URI) {
		mediaEligible = false
		findings = append(findings, candidateReviewFinding("candidate_not_local", "candidate must be downloaded to a local artifact before approval", artifact.ID))
	}
	if artifact.Sensitive {
		mediaEligible = false
		findings = append(findings, candidateReviewFinding("candidate_sensitive", "sensitive generated candidates cannot be approved for demo use", artifact.ID))
	}
	if artifact.SourceNodeID != "" {
		mediaEligible = false
		findings = append(findings, candidateReviewFinding("candidate_bound_to_script_step", "generated candidates cannot be bound to customer-side script steps", artifact.ID))
	}
	if stringMetadataBool(artifact.Metadata, "non_authoritative") != true {
		mediaEligible = false
		findings = append(findings, candidateReviewFinding("non_authoritative_missing", "candidate must carry non_authoritative=true", artifact.ID))
	}
	if artifactStringMetadata(artifact.Metadata, "source_material_policy") != "non_authoritative_generated_candidate" {
		mediaEligible = false
		findings = append(findings, candidateReviewFinding("candidate_policy_missing", "candidate must carry non_authoritative_generated_candidate policy", artifact.ID))
	}
	if artifact.SHA256 == "" || artifact.SizeBytes <= 0 {
		mediaEligible = false
		findings = append(findings, candidateReviewFinding("candidate_integrity_missing", "candidate must include sha256 and size_bytes after download", artifact.ID))
	}
	if !candidateArtifactHasEditorMediaProfile(artifact) {
		mediaEligible = false
		findings = append(findings, candidateReviewFinding("candidate_media_not_normalized", "candidate must be a probed normalized derivative using the editor MP4/H.264/yuv420p/1920x1080/CFR30 profile", artifact.ID))
	}

	if mediaEligible {
		reasons = append(reasons,
			"candidate is a local downloaded video artifact",
			"candidate is explicitly non-authoritative and presentation-only",
			"candidate is not bound to a customer-side script step",
		)
		reviewed.Metadata["approved_for_demo"] = false
		reviewed.Metadata["media_eligible"] = true
		reviewed.Metadata["explicit_review_required"] = true
		reviewed.Metadata["presentation_only"] = true
	} else {
		risks = append(risks, "candidate cannot be safely referenced by DemoEditPlan until findings are resolved")
		reviewed.Metadata["approved_for_demo"] = false
		reviewed.Metadata["media_eligible"] = false
	}
	reviewed.Metadata["include_in_demo"] = false
	reviewed.Metadata["source_material_policy"] = "non_authoritative_generated_candidate"

	item := model.CandidateAssetReviewItem{
		ArtifactID:             artifact.ID,
		Kind:                   artifact.Kind,
		URI:                    artifact.URI,
		MimeType:               artifact.MimeType,
		Status:                 "rejected",
		ApprovedForDemo:        false,
		MediaEligible:          mediaEligible,
		ExplicitReviewRequired: mediaEligible,
		IncludeInDemo:          false,
		PresentationOnly:       mediaEligible,
		Reasons:                reasons,
		Risks:                  risks,
		Findings:               findings,
		ApprovedMetadata: map[string]any{
			"approved_for_demo":      reviewed.Metadata["approved_for_demo"],
			"include_in_demo":        false,
			"source_material_policy": "non_authoritative_generated_candidate",
			"non_authoritative":      reviewed.Metadata["non_authoritative"],
			"presentation_only":      reviewed.Metadata["presentation_only"],
		},
	}
	if mediaEligible {
		item.Status = "awaiting_user_review"
	}
	return item, reviewed
}

func candidateArtifactHasEditorMediaProfile(artifact model.ArtifactRef) bool {
	return artifactStringMetadata(artifact.Metadata, "artifact_variant") == "normalized" &&
		artifactStringMetadata(artifact.Metadata, "normalization_status") == "ok" &&
		artifactStringMetadata(artifact.Metadata, "media_probe_status") == "ok" &&
		artifactStringMetadata(artifact.Metadata, "normalization_profile") == "editor_mp4_h264_yuv420p_1920x1080_cfr30_v1"
}

func candidateReviewFinding(code string, message string, refID string) model.ArkMediaReadinessFinding {
	return model.ArkMediaReadinessFinding{Code: code, Message: message, RefID: refID}
}

func isLocalArtifactURI(uri string) bool {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return false
	}
	lower := strings.ToLower(uri)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "asset://") {
		return false
	}
	if strings.HasPrefix(lower, "file://") {
		return true
	}
	return filepath.IsAbs(uri) || strings.HasPrefix(uri, ".") || !strings.Contains(uri, "://")
}

func stringMetadataBool(metadata map[string]any, key string) bool {
	if metadata == nil {
		return false
	}
	value, ok := metadata[key].(bool)
	return ok && value
}
