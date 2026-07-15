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
		SchemaVersion:     model.CandidateAssetReviewSchemaVersion,
		ReviewID:          "candidate_asset_review_" + safeID(firstNonEmptyString(sourcePackageID, "unknown_package")),
		CreatedAt:         createdAt,
		SourcePackageID:   sourcePackageID,
		Status:            "no_candidates",
		Policy:            candidateAssetReviewPolicy(),
		Items:             []model.CandidateAssetReviewItem{},
		ApprovedArtifacts: []model.ArtifactRef{},
		RejectedArtifacts: []model.ArtifactRef{},
		Warnings:          []model.ArkMediaReadinessFinding{},
		Notes: []string{
			"Approved candidates remain presentation-only and must not represent customer-side script steps.",
			"Approval only allows future DemoEditPlan references; it does not automatically include generated candidates in the final video.",
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

	approvedCount := 0
	for index, artifact := range generationResult.DownloadedArtifacts {
		item, reviewedArtifact := reviewCandidateAsset(artifact)
		review.Items = append(review.Items, item)
		generationResult.DownloadedArtifacts[index] = reviewedArtifact
		if item.ApprovedForDemo {
			approvedCount++
			review.ApprovedArtifacts = append(review.ApprovedArtifacts, reviewedArtifact)
		} else {
			review.RejectedArtifacts = append(review.RejectedArtifacts, reviewedArtifact)
		}
	}
	switch {
	case approvedCount == len(generationResult.DownloadedArtifacts):
		review.Status = "approved"
	case approvedCount > 0:
		review.Status = "partially_approved"
	default:
		review.Status = "rejected"
	}
	return review
}

func candidateAssetReviewPolicy() model.CandidateAssetReviewPolicy {
	return model.CandidateAssetReviewPolicy{
		DecisionMode:             "conservative_auto_review",
		SourceMaterialPolicy:     "non_authoritative_generated_candidate",
		AllowedKinds:             []string{"generated_video_candidate"},
		RequiresLocalFile:        true,
		RequiresNonAuthoritative: true,
		RequiresPresentationOnly: true,
		AutoIncludeInDemo:        false,
	}
}

func reviewCandidateAsset(artifact model.ArtifactRef) (model.CandidateAssetReviewItem, model.ArtifactRef) {
	reasons := []string{}
	risks := []string{}
	findings := []model.ArkMediaReadinessFinding{}
	approved := true
	reviewed := artifact
	reviewed.Metadata = cloneArtifactMetadata(reviewed.Metadata)

	if artifact.Kind != "generated_video_candidate" {
		approved = false
		findings = append(findings, candidateReviewFinding("unsupported_candidate_kind", "only generated_video_candidate can be approved for deterministic rendering", artifact.ID))
	}
	if !strings.HasPrefix(strings.ToLower(artifact.MimeType), "video/") {
		approved = false
		findings = append(findings, candidateReviewFinding("unsupported_candidate_mime", "only video candidates can be approved by the current compositor", artifact.ID))
	}
	if !isLocalArtifactURI(artifact.URI) {
		approved = false
		findings = append(findings, candidateReviewFinding("candidate_not_local", "candidate must be downloaded to a local artifact before approval", artifact.ID))
	}
	if artifact.Sensitive {
		approved = false
		findings = append(findings, candidateReviewFinding("candidate_sensitive", "sensitive generated candidates cannot be approved for demo use", artifact.ID))
	}
	if artifact.SourceNodeID != "" {
		approved = false
		findings = append(findings, candidateReviewFinding("candidate_bound_to_script_step", "generated candidates cannot be bound to customer-side script steps", artifact.ID))
	}
	if stringMetadataBool(artifact.Metadata, "non_authoritative") != true {
		approved = false
		findings = append(findings, candidateReviewFinding("non_authoritative_missing", "candidate must carry non_authoritative=true", artifact.ID))
	}
	if artifactStringMetadata(artifact.Metadata, "source_material_policy") != "non_authoritative_generated_candidate" {
		approved = false
		findings = append(findings, candidateReviewFinding("candidate_policy_missing", "candidate must carry non_authoritative_generated_candidate policy", artifact.ID))
	}
	if artifact.SHA256 == "" || artifact.SizeBytes <= 0 {
		approved = false
		findings = append(findings, candidateReviewFinding("candidate_integrity_missing", "candidate must include sha256 and size_bytes after download", artifact.ID))
	}

	if approved {
		reasons = append(reasons,
			"candidate is a local downloaded video artifact",
			"candidate is explicitly non-authoritative and presentation-only",
			"candidate is not bound to a customer-side script step",
		)
		reviewed.Metadata["approved_for_demo"] = true
		reviewed.Metadata["approval_mode"] = "conservative_auto_review"
		reviewed.Metadata["presentation_only"] = true
	} else {
		risks = append(risks, "candidate cannot be safely referenced by DemoEditPlan until findings are resolved")
		reviewed.Metadata["approved_for_demo"] = false
	}
	reviewed.Metadata["include_in_demo"] = false
	reviewed.Metadata["source_material_policy"] = "non_authoritative_generated_candidate"

	item := model.CandidateAssetReviewItem{
		ArtifactID:       artifact.ID,
		Kind:             artifact.Kind,
		URI:              artifact.URI,
		MimeType:         artifact.MimeType,
		Status:           "rejected",
		ApprovedForDemo:  approved,
		IncludeInDemo:    false,
		PresentationOnly: approved,
		Reasons:          reasons,
		Risks:            risks,
		Findings:         findings,
		ApprovedMetadata: map[string]any{
			"approved_for_demo":      reviewed.Metadata["approved_for_demo"],
			"include_in_demo":        false,
			"source_material_policy": "non_authoritative_generated_candidate",
			"non_authoritative":      reviewed.Metadata["non_authoritative"],
			"presentation_only":      reviewed.Metadata["presentation_only"],
		},
	}
	if approved {
		item.Status = "approved"
	}
	return item, reviewed
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
