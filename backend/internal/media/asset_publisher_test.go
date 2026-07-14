package media

import (
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestDryRunAssetPublisherProducesNonCallableDryRunRefs(t *testing.T) {
	now := time.Date(2026, 7, 14, 14, 0, 0, 0, time.UTC)
	publisher := NewDryRunAssetPublisher(func() time.Time { return now })
	plan := model.ArkAssetPublicationPlan{
		SourcePackageID: "pkg_1",
		Items: []model.ArkAssetPublicationItem{{
			Ref:               model.DirectorMaterialRef{ID: "source_reference", Kind: "source_reference_video", URI: "artifacts/render/source_reference.mp4", MimeType: "video/mp4"},
			TaskIDs:           []string{"seedance_reference_director_preview"},
			Usage:             "required_reference_video",
			Required:          true,
			AcceptedMimeTypes: []string{"video/mp4"},
			NeedsPublication:  true,
			ExpectedPublicURI: "https://assets.example.com/source_reference.mp4",
			Status:            "ready_after_publication",
		}},
	}
	planRef := model.DirectorMaterialRef{ID: "publication_plan", Kind: "ark_asset_publication_plan", URI: "artifacts/render/ark_asset_publication_plan.json", MimeType: "application/json"}

	result, err := publisher.PublishArkAssets(t.Context(), plan, planRef)
	if err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != model.ArkAssetPublicationResultSchemaVersion || result.CreatedAt != now {
		t.Fatalf("unexpected result identity: %+v", result)
	}
	if result.Status != "dry_run_publication_required" || result.CanUseForRealCall || !result.ContainsDryRunRefs {
		t.Fatalf("dry-run publisher must not mark simulated refs as real-call ready: %+v", result)
	}
	if len(result.Items) != 1 || result.Items[0].ProposedPublicRef == nil {
		t.Fatalf("expected proposed dry-run public ref: %+v", result.Items)
	}
	if result.Items[0].ProposedPublicRef.URI != "https://assets.example.com/source_reference.mp4" || !result.Items[0].DryRun || result.Items[0].Published {
		t.Fatalf("unexpected dry-run item: %+v", result.Items[0])
	}
	if result.PublicationPlanRef.ID != "publication_plan" {
		t.Fatalf("publication result should reference publication plan artifact: %+v", result.PublicationPlanRef)
	}
}

func TestDryRunAssetPublisherPreservesExistingPublicRefs(t *testing.T) {
	publisher := NewDryRunAssetPublisher(func() time.Time { return time.Date(2026, 7, 14, 14, 15, 0, 0, time.UTC) })
	plan := model.ArkAssetPublicationPlan{
		SourcePackageID: "pkg_1",
		Items: []model.ArkAssetPublicationItem{{
			Ref:                model.DirectorMaterialRef{ID: "source_reference", Kind: "source_reference_video", URI: "https://assets.example.com/source_reference.mp4", MimeType: "video/mp4"},
			TaskIDs:            []string{"seedance_reference_director_preview"},
			Usage:              "required_reference_video",
			Required:           true,
			CurrentURIIsPublic: true,
			Status:             "ready",
		}},
	}

	result, err := publisher.PublishArkAssets(t.Context(), plan, model.DirectorMaterialRef{ID: "publication_plan", Kind: "ark_asset_publication_plan", URI: "https://assets.example.com/plan.json", MimeType: "application/json"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ready" || !result.CanUseForRealCall || result.ContainsDryRunRefs {
		t.Fatalf("existing public refs should be callable: %+v", result)
	}
	if len(result.Items) != 1 || result.Items[0].ProposedPublicRef == nil || result.Items[0].ProposedPublicRef.URI != "https://assets.example.com/source_reference.mp4" {
		t.Fatalf("public source ref should be preserved: %+v", result.Items)
	}
}
