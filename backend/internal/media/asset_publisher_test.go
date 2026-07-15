package media

import (
	"os"
	"path/filepath"
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

func TestStaticAssetPublisherCopiesLocalAssetsAndMintsHTTPSRefs(t *testing.T) {
	now := time.Date(2026, 7, 15, 19, 0, 0, 0, time.UTC)
	sourceDir := t.TempDir()
	publicDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "source_reference.mp4")
	if err := os.WriteFile(sourcePath, []byte("mp4-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	publisher := NewStaticAssetPublisher(publicDir, "https://assets.example.com/ark-inputs", func() time.Time { return now })
	plan := model.ArkAssetPublicationPlan{
		SourcePackageID: "pkg_1",
		Items: []model.ArkAssetPublicationItem{{
			Ref:                 model.DirectorMaterialRef{ID: "source_reference", Kind: "source_reference_video", URI: sourcePath, MimeType: "video/mp4"},
			TaskIDs:             []string{"seedance_reference_director_preview"},
			Usage:               "required_reference_video",
			Required:            true,
			AcceptedMimeTypes:   []string{"video/mp4"},
			NeedsPublication:    true,
			RecommendedFileName: "artifact_source_reference_video_001.mp4",
			Status:              "ready_after_publication",
		}},
	}

	result, err := publisher.PublishArkAssets(t.Context(), plan, model.DirectorMaterialRef{ID: "publication_plan", Kind: "ark_asset_publication_plan", URI: "artifacts/render/ark_asset_publication_plan.json", MimeType: "application/json"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != "static_public_dir" || result.Publisher != "static_asset_publisher" || result.Status != "ready" || !result.CanUseForRealCall || result.ContainsDryRunRefs {
		t.Fatalf("static publisher should produce callable refs: %+v", result)
	}
	if len(result.Items) != 1 || !result.Items[0].Published || result.Items[0].DryRun || result.Items[0].ProposedPublicRef == nil {
		t.Fatalf("expected published item with public ref: %+v", result.Items)
	}
	if result.Items[0].ProposedPublicRef.URI != "https://assets.example.com/ark-inputs/artifact_source_reference_video_001.mp4" {
		t.Fatalf("unexpected public URI: %+v", result.Items[0].ProposedPublicRef)
	}
	copied, err := os.ReadFile(filepath.Join(publicDir, "artifact_source_reference_video_001.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if string(copied) != "mp4-bytes" {
		t.Fatalf("copied bytes mismatch: %q", copied)
	}
}

func TestStaticAssetPublisherBlocksInvalidPublicBaseURL(t *testing.T) {
	publisher := NewStaticAssetPublisher(t.TempDir(), "http://assets.example.com/ark-inputs", func() time.Time {
		return time.Date(2026, 7, 15, 19, 15, 0, 0, time.UTC)
	})
	plan := model.ArkAssetPublicationPlan{
		SourcePackageID: "pkg_1",
		Items: []model.ArkAssetPublicationItem{{
			Ref:      model.DirectorMaterialRef{ID: "source_reference", Kind: "source_reference_video", URI: filepath.Join(t.TempDir(), "missing.mp4"), MimeType: "video/mp4"},
			TaskIDs:  []string{"seedance_reference_director_preview"},
			Required: true,
			Status:   "ready_after_publication",
		}},
	}

	result, err := publisher.PublishArkAssets(t.Context(), plan, model.DirectorMaterialRef{ID: "publication_plan", Kind: "ark_asset_publication_plan"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "blocked" || result.CanUseForRealCall || len(result.Blockers) == 0 {
		t.Fatalf("invalid public base URL should block real calls: %+v", result)
	}
	if result.Items[0].Status != "publisher_public_base_url_invalid" {
		t.Fatalf("unexpected item status: %+v", result.Items[0])
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
