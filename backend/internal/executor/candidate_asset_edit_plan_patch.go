package executor

import (
	"strconv"
	"time"

	"cascade-demoops/backend/internal/model"
)

const defaultCandidateAssetPatchShotDurationMS = 2000

func NewCandidateAssetEditPlanPatch(source *model.ClientExecutionPackage, basePlan *model.DemoEditPlan, review *model.CandidateAssetReview, createdAt time.Time) model.CandidateAssetEditPlanPatch {
	sourcePackageID := ""
	if source != nil {
		sourcePackageID = source.PackageID
	}
	if review != nil && review.SourcePackageID != "" {
		sourcePackageID = review.SourcePackageID
	}
	basePlanID := ""
	if basePlan != nil {
		basePlanID = basePlan.PlanID
	}
	patch := model.CandidateAssetEditPlanPatch{
		SchemaVersion:   model.CandidateAssetEditPlanPatchSchemaVersion,
		PatchID:         "candidate_asset_edit_patch_" + safeID(firstNonEmptyString(sourcePackageID, "unknown_package")),
		CreatedAt:       createdAt,
		SourcePackageID: sourcePackageID,
		BasePlanID:      basePlanID,
		Status:          "no_review",
		ApplicationMode: "manual_or_explicit_opt_in_required",
		Policy:          candidateAssetEditPatchPolicy(),
		Notes: []string{
			"This patch is a proposal only; it is not applied to the current final demo video.",
			"Generated candidate assets remain non-authoritative presentation material and must not replace captured product UI or customer-side script steps.",
		},
	}
	if review == nil {
		patch.Warnings = append(patch.Warnings, model.ArkMediaReadinessFinding{
			Code:    "candidate_asset_review_missing",
			Message: "candidate asset edit patch was skipped because candidate_asset_review is missing",
		})
		return patch
	}
	patch.ReviewID = review.ReviewID
	for _, artifact := range review.RejectedArtifacts {
		if artifact.ID != "" {
			patch.RejectedArtifactIDs = append(patch.RejectedArtifactIDs, artifact.ID)
		}
	}
	for index, artifact := range review.ApprovedArtifacts {
		if !candidateArtifactCanBecomePatchShot(artifact) {
			patch.Warnings = append(patch.Warnings, model.ArkMediaReadinessFinding{
				Code:    "approved_candidate_not_patch_safe",
				Message: "approved candidate is missing presentation-only approval metadata required for edit patch proposal",
				RefID:   artifact.ID,
			})
			continue
		}
		patch.ApprovedArtifactIDs = append(patch.ApprovedArtifactIDs, artifact.ID)
		patch.ProposedShots = append(patch.ProposedShots, candidateAssetPatchShot(index+1, artifact))
	}
	if len(patch.ProposedShots) == 0 {
		patch.Status = "no_approved_candidates"
		if len(review.ApprovedArtifacts) == 0 {
			patch.Warnings = append(patch.Warnings, model.ArkMediaReadinessFinding{
				Code:    "approved_candidates_missing",
				Message: "no approved candidate assets are available for optional presentation shots",
				TaskID:  review.GenerationResultID,
			})
		}
		return patch
	}
	patch.Status = "proposed"
	return patch
}

func candidateAssetEditPatchPolicy() model.CandidateAssetEditPatchPolicy {
	return model.CandidateAssetEditPatchPolicy{
		AutoApply:                  false,
		RequiresExplicitOptIn:      true,
		RequiresRendererValidation: true,
		PreserveRequiredStepOrder:  true,
		PresentationOnly:           true,
		NonAuthoritativeOnly:       true,
		MustNotBindSourceStep:      true,
		AllowedPlacements:          []string{"intro", "section_divider", "outro"},
	}
}

func candidateArtifactCanBecomePatchShot(artifact model.ArtifactRef) bool {
	if artifact.Kind != "generated_video_candidate" {
		return false
	}
	if !stringMetadataBool(artifact.Metadata, "approved_for_demo") {
		return false
	}
	if !stringMetadataBool(artifact.Metadata, "presentation_only") {
		return false
	}
	if !stringMetadataBool(artifact.Metadata, "non_authoritative") {
		return false
	}
	if artifactStringMetadata(artifact.Metadata, "source_material_policy") != "non_authoritative_generated_candidate" {
		return false
	}
	return artifact.SourceNodeID == ""
}

func candidateAssetPatchShot(index int, artifact model.ArtifactRef) model.DemoEditShot {
	durationMS := firstPositiveInt(artifact.Metadata["duration_ms"], artifact.Metadata["target_duration_ms"], defaultCandidateAssetPatchShotDurationMS)
	if durationMS <= 0 {
		durationMS = defaultCandidateAssetPatchShotDurationMS
	}
	startMS := 0
	endMS := durationMS
	rangeMS := model.MillisecondRange{startMS, endMS}
	return model.DemoEditShot{
		ID:                "candidate_shot_" + strconv.Itoa(index) + "_" + safeID(artifact.ID),
		SourceArtifactID:  artifact.ID,
		SourceTimeRangeMS: &rangeMS,
		Purpose:           "Optional presentation-only segment from an approved non-authoritative generated candidate.",
		Operations: []model.EditOperation{{
			Type:    model.EditOperationTrim,
			StartMS: &startMS,
			EndMS:   &endMS,
		}},
	}
}
