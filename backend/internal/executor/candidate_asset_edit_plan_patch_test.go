package executor

import (
	"path/filepath"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestNewCandidateAssetEditPlanPatchProposesPresentationOnlyShots(t *testing.T) {
	source := sampleClientExecutionPackageForExecutorTest(t)
	basePlan := &model.DemoEditPlan{PlanID: "edit_plan_base"}
	artifact := model.ArtifactRef{
		ID:        "artifact_candidate_001",
		Kind:      "generated_video_candidate",
		URI:       filepath.Join(t.TempDir(), "candidate.mp4"),
		MimeType:  "video/mp4",
		SHA256:    "sha_candidate",
		SizeBytes: 128,
		Metadata: map[string]any{
			"approved_for_demo":      true,
			"presentation_only":      true,
			"include_in_demo":        false,
			"source_material_policy": "non_authoritative_generated_candidate",
			"non_authoritative":      true,
			"duration_ms":            1500,
		},
	}
	review := model.CandidateAssetReview{
		ReviewID:          "candidate_review_1",
		SourcePackageID:   source.PackageID,
		Status:            "approved",
		ApprovedArtifacts: []model.ArtifactRef{artifact},
	}

	patch := NewCandidateAssetEditPlanPatch(&source, basePlan, &review, time.Date(2026, 7, 15, 22, 0, 0, 0, time.UTC))

	if patch.SchemaVersion != model.CandidateAssetEditPlanPatchSchemaVersion || patch.Status != "proposed" || patch.BasePlanID != "edit_plan_base" {
		t.Fatalf("unexpected candidate edit patch identity: %+v", patch)
	}
	if patch.Policy.AutoApply || !patch.Policy.RequiresExplicitOptIn || !patch.Policy.MustNotBindSourceStep {
		t.Fatalf("candidate patch policy should require explicit opt-in and avoid script binding: %+v", patch.Policy)
	}
	if len(patch.ProposedShots) != 1 || patch.ProposedShots[0].SourceArtifactID != artifact.ID {
		t.Fatalf("expected one proposed candidate shot: %+v", patch.ProposedShots)
	}
	if patch.ProposedShots[0].SourceStepID != "" {
		t.Fatalf("candidate shot must not claim a customer-side script step: %+v", patch.ProposedShots[0])
	}
	if patch.ProposedShots[0].SourceTimeRangeMS == nil || (*patch.ProposedShots[0].SourceTimeRangeMS)[1] != 1500 {
		t.Fatalf("candidate shot should preserve candidate duration metadata: %+v", patch.ProposedShots[0].SourceTimeRangeMS)
	}
}

func TestNewCandidateAssetEditPlanPatchSkipsUnsafeApprovedArtifacts(t *testing.T) {
	review := model.CandidateAssetReview{
		ReviewID:        "candidate_review_unsafe",
		SourcePackageID: "pkg_1",
		Status:          "approved",
		ApprovedArtifacts: []model.ArtifactRef{{
			ID:           "artifact_candidate_bound_to_step",
			Kind:         "generated_video_candidate",
			URI:          filepath.Join(t.TempDir(), "candidate.mp4"),
			MimeType:     "video/mp4",
			SourceNodeID: "node_open",
			Metadata: map[string]any{
				"approved_for_demo":      true,
				"presentation_only":      true,
				"source_material_policy": "non_authoritative_generated_candidate",
				"non_authoritative":      true,
			},
		}},
	}

	patch := NewCandidateAssetEditPlanPatch(nil, nil, &review, time.Date(2026, 7, 15, 22, 5, 0, 0, time.UTC))

	if patch.Status != "no_approved_candidates" || len(patch.ProposedShots) != 0 {
		t.Fatalf("unsafe approved artifact should not become a proposed shot: %+v", patch)
	}
	if len(patch.Warnings) == 0 || patch.Warnings[0].Code != "approved_candidate_not_patch_safe" {
		t.Fatalf("unsafe approved artifact should produce warning: %+v", patch.Warnings)
	}
}
