package finalfilm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

type bridgePublisher struct {
	result model.ArkAssetPublicationResult
	err    error
}

func (p bridgePublisher) PublishArkAssets(context.Context, model.ArkAssetPublicationPlan, model.DirectorMaterialRef) (model.ArkAssetPublicationResult, error) {
	return p.result, p.err
}

func TestPrepareSeedance25ReferenceRedactsPublisherFailureDetails(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "raw.webm")
	if err := os.WriteFile(source, []byte("raw"), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := model.AssetTimelineCatalog{
		SchemaVersion: model.AssetTimelineCatalogSchemaVersion,
		Timeline:      model.AssetTimelineInfo{DurationMS: 8_000, RecordingArtifactID: "raw_1"},
		Artifacts:     []model.TimelineArtifact{{ID: "raw_1", Kind: "raw_recording", LocalPath: source, URI: "file:///raw.webm", MimeType: "video/webm"}},
		Steps:         []model.TimelineStep{{StepID: "step_1", Status: "passed", StartMS: 1_000, EndMS: 7_000, Artifacts: []string{"raw_1"}}},
	}
	intent := media.GeneratedShotIntent{IntentID: "intent_1", Purpose: media.GeneratedShotPurposeIntro, Prompt: "safe", DurationSec: 5, AspectRatio: "16:9", References: []media.GeneratedShotReference{{ArtifactID: "raw_1", URI: "asset://raw_1", MimeType: "video/mp4", Usage: media.GeneratedShotReferenceGeneral}}, ContentPolicy: media.GeneratedShotContentPolicy{PresentationOnly: true, RequiresExplicitReview: true}, FailurePolicy: media.GeneratedShotFailureContinue}
	secretURL := "https://tos-cn-beijing.ivolces.com/private.mp4?X-Tos-Signature=not-for-audit"
	service := &Service{outputRoot: root, now: time.Now, assetPublisher: bridgePublisher{err: errors.New("put object " + secretURL + " from " + source)}, referenceNormalizer: media.FFmpegMiniMaxH3MediaNormalizer{Runner: bridgeNormalizerRunner{}}, referenceRetention: model.DefaultMediaDeliveryPreferences().TOSRetention}
	_, audit, err := service.prepareSeedance25Reference(context.Background(), model.FinalFilmJob{JobID: "job_1", SourcePackageID: "pkg_1", Catalog: catalog}, intent, "op_1")
	if err == nil || audit.FailureStage != "publication" || audit.ErrorClass != "publication_failed" {
		t.Fatalf("expected classified publication failure: audit=%+v err=%v", audit, err)
	}
	serialized := audit.Message + "\n" + err.Error()
	if strings.Contains(serialized, "X-Tos-Signature") || strings.Contains(serialized, source) || strings.Contains(serialized, "private.mp4") {
		t.Fatalf("persistent reference failure leaked provider detail: %q", serialized)
	}
}

func TestRedactGeneratedProviderExecutionFailureRedactsPrivateLocations(t *testing.T) {
	original := media.GeneratedShotProviderExecutionResult{
		Provider: "seedance", IntentID: "intent_1", ErrorClass: "provider_http_error",
		ErrorMessage: "post https://tos-cn-beijing.ivolces.com/a.mp4?X-Tos-Signature=not-for-audit from D:\\Engine-7-8\\private.mp4 failed",
	}
	got := redactGeneratedProviderExecutionFailure(original)
	if got.ErrorClass != "provider_http_error" || got.ErrorMessage == original.ErrorMessage {
		t.Fatalf("private provider error was not safely summarized: %+v", got)
	}
	if strings.Contains(strings.ToLower(got.ErrorMessage), "http") || strings.Contains(got.ErrorMessage, "D:\\Engine-7-8") {
		t.Fatalf("summary leaked a private location: %q", got.ErrorMessage)
	}
	plain := media.GeneratedShotProviderExecutionResult{ErrorMessage: "provider returned unsupported duration", ErrorClass: "provider_input_invalid"}
	if unchanged := redactGeneratedProviderExecutionFailure(plain); unchanged != plain {
		t.Fatalf("safe actionable provider error should be retained: %+v", unchanged)
	}
}

func TestExactPassedRawRecordingReferenceRequiresOpaqueBinding(t *testing.T) {
	catalog := model.AssetTimelineCatalog{
		Artifacts: []model.TimelineArtifact{{ID: "raw_1", Kind: "raw_recording"}},
		Steps:     []model.TimelineStep{{StepID: "step_1", Status: "passed", Artifacts: []string{"raw_1"}}},
	}
	intent := media.GeneratedShotIntent{IntentID: "intent_1", References: []media.GeneratedShotReference{{ArtifactID: "raw_1", URI: "asset://raw_1", MimeType: "video/mp4", Usage: media.GeneratedShotReferenceGeneral}}}
	rawID, stepID, err := exactPassedRawRecordingReference(catalog, intent)
	if err != nil || rawID != "raw_1" || stepID != "step_1" {
		t.Fatalf("unexpected binding: raw=%q step=%q err=%v", rawID, stepID, err)
	}
	intent.References[0].URI = "file:///secret/recording.mp4"
	if _, _, err := exactPassedRawRecordingReference(catalog, intent); err == nil {
		t.Fatal("expected local filesystem URI to be rejected at the persisted Director boundary")
	}
}

func TestExactPassedRawRecordingReferenceRejectsAmbiguousStages(t *testing.T) {
	catalog := model.AssetTimelineCatalog{
		Artifacts: []model.TimelineArtifact{{ID: "raw_1", Kind: "raw_recording"}},
		Steps: []model.TimelineStep{
			{StepID: "step_1", Status: "passed", Artifacts: []string{"raw_1"}},
			{StepID: "step_2", Status: "passed", Artifacts: []string{"raw_1"}},
		},
	}
	intent := media.GeneratedShotIntent{IntentID: "intent_1", References: []media.GeneratedShotReference{{ArtifactID: "raw_1", URI: "asset://raw_1", MimeType: "video/mp4", Usage: media.GeneratedShotReferenceGeneral}}}
	if _, _, err := exactPassedRawRecordingReference(catalog, intent); err == nil {
		t.Fatal("expected ambiguous passed-stage binding to be rejected")
	}
}

func TestPrepareSeedance25ReferencePublishesOnlyPassedStageAndDoesNotPersistURL(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "raw.webm")
	if err := os.WriteFile(source, []byte("raw"), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := model.AssetTimelineCatalog{
		SchemaVersion: model.AssetTimelineCatalogSchemaVersion,
		Timeline:      model.AssetTimelineInfo{DurationMS: 8_000, RecordingArtifactID: "raw_1"},
		Artifacts:     []model.TimelineArtifact{{ID: "raw_1", Kind: "raw_recording", LocalPath: source, URI: "file:///raw.webm", MimeType: "video/webm"}},
		Steps:         []model.TimelineStep{{StepID: "step_1", Status: "passed", StartMS: 1_000, EndMS: 7_000, Artifacts: []string{"raw_1"}}},
	}
	intent := media.GeneratedShotIntent{IntentID: "intent_1", Purpose: media.GeneratedShotPurposeIntro, Prompt: "safe", DurationSec: 5, AspectRatio: "16:9", References: []media.GeneratedShotReference{{ArtifactID: "raw_1", URI: "asset://raw_1", MimeType: "video/mp4", Usage: media.GeneratedShotReferenceGeneral}}, ContentPolicy: media.GeneratedShotContentPolicy{PresentationOnly: true, RequiresExplicitReview: true}, FailurePolicy: media.GeneratedShotFailureContinue}
	service := &Service{outputRoot: root, now: time.Now, assetPublisher: bridgePublisher{result: model.ArkAssetPublicationResult{Publisher: "fake-tos", Status: "ready", CanUseForRealCall: true, Items: []model.ArkAssetPublicationResultItem{{ProposedPublicRef: &model.DirectorMaterialRef{ID: "published", URI: "https://tos-cn-beijing.ivolces.com/object?signature=redacted", MimeType: "video/mp4", IncludeInDemo: false}}}}}, referenceNormalizer: media.FFmpegMiniMaxH3MediaNormalizer{Runner: bridgeNormalizerRunner{}}, referenceRetention: model.DefaultMediaDeliveryPreferences().TOSRetention}
	prepared, audit, err := service.prepareSeedance25Reference(context.Background(), model.FinalFilmJob{JobID: "job_1", SourcePackageID: "pkg_1", Catalog: catalog}, intent, "op_1")
	if err != nil {
		t.Fatal(err)
	}
	if !audit.CanCallProvider || audit.SourceStepID != "step_1" || audit.Status != "ready" || prepared.References[0].URI == "asset://raw_1" {
		t.Fatalf("unexpected prepared reference: audit=%+v intent=%+v", audit, prepared)
	}
	if audit.Message == "" || strings.Contains(audit.Message, "signature") {
		t.Fatalf("audit must be safe and descriptive: %+v", audit)
	}
}

func TestPrepareAutomatedPresentationReferencePublishesOnlyTextFreePalette(t *testing.T) {
	root := t.TempDir()
	palettePath := filepath.Join(root, "palette.png")
	if err := os.WriteFile(palettePath, []byte("palette"), 0o600); err != nil {
		t.Fatal(err)
	}
	publicURL := "https://assets.example.test/generated/palette.png?temporary=1"
	publisher := bridgePublisher{result: model.ArkAssetPublicationResult{
		Publisher: "test-publisher", Status: "ready", CanUseForRealCall: true,
		Items: []model.ArkAssetPublicationResultItem{{ProposedPublicRef: &model.DirectorMaterialRef{ID: "published_palette", URI: publicURL, MimeType: "image/png"}}},
	}}
	service := &Service{outputRoot: root, now: time.Now, assetPublisher: publisher, referenceRetention: model.DefaultMediaDeliveryPreferences().TOSRetention}
	job := model.FinalFilmJob{
		JobID: "job_palette", SourcePackageID: "package_palette",
		RunAuthorization: &model.FinalFilmRunAuthorization{AuthorizationRef: "auth_palette"},
		Catalog: model.AssetTimelineCatalog{Artifacts: []model.TimelineArtifact{{
			ID: "palette_1", Kind: "generated_palette_reference", URI: "asset://palette_1", LocalPath: palettePath,
			MimeType: "image/png", SizeBytes: 7, AssetRole: "presentation_reference", Metadata: map[string]any{"text_free": true, "ui_free": true},
		}}},
	}
	intent := media.GeneratedShotIntent{IntentID: "intro_1", References: []media.GeneratedShotReference{{ArtifactID: "palette_1", URI: "asset://palette_1", MimeType: "image/png"}}}
	prepared, err := service.prepareAutomatedPresentationReferences(context.Background(), job, intent, "operation_1")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.References[0].URI != publicURL || job.Catalog.Artifacts[0].URI != "asset://palette_1" || intent.References[0].URI != "asset://palette_1" {
		t.Fatalf("publication must be ephemeral and must not mutate persisted references: prepared=%+v job=%+v", prepared.References, job.Catalog.Artifacts)
	}
}

func TestPrepareAutomatedPresentationReferenceFallsBackToTextOnlyWithoutPublisher(t *testing.T) {
	root := t.TempDir()
	palettePath := filepath.Join(root, "palette.png")
	if err := os.WriteFile(palettePath, []byte("palette"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := &Service{outputRoot: root, now: time.Now, referenceRetention: model.DefaultMediaDeliveryPreferences().TOSRetention}
	job := model.FinalFilmJob{
		JobID: "job_palette_text_only", SourcePackageID: "package_palette_text_only",
		RunAuthorization: &model.FinalFilmRunAuthorization{AuthorizationRef: "auth_palette"},
		Catalog: model.AssetTimelineCatalog{Artifacts: []model.TimelineArtifact{{
			ID: "palette_1", Kind: "generated_palette_reference", URI: "asset://palette_1", LocalPath: palettePath,
			MimeType: "image/png", SizeBytes: 7, AssetRole: "presentation_reference", Metadata: map[string]any{"text_free": true, "ui_free": true},
		}}},
	}
	intent := media.GeneratedShotIntent{IntentID: "intro_1", References: []media.GeneratedShotReference{{ArtifactID: "palette_1", URI: "asset://palette_1", MimeType: "image/png"}}}
	prepared, err := service.prepareAutomatedPresentationReferences(context.Background(), job, intent, "operation_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.References) != 0 {
		t.Fatalf("publisher-free automated generation must use the provider's text-only route: %+v", prepared.References)
	}
	if len(intent.References) != 1 || intent.References[0].URI != "asset://palette_1" {
		t.Fatalf("fallback must not mutate the persisted intent: %+v", intent.References)
	}
}

type bridgeNormalizerRunner struct{}

func (bridgeNormalizerRunner) Run(_ context.Context, command string, args ...string) (media.MiniMaxH3CommandResult, error) {
	if strings.Contains(command, "ffprobe") {
		format := `{"format":{"format_name":"matroska,webm","duration":"8"},"streams":[{"codec_type":"video","codec_name":"vp8","pix_fmt":"yuv420p","width":2560,"height":1440,"avg_frame_rate":"30/1","r_frame_rate":"30/1"}]}`
		if strings.Contains(filepath.Base(args[len(args)-1]), ".tmp.") {
			format = `{"format":{"format_name":"mov,mp4","duration":"6"},"streams":[{"codec_type":"video","codec_name":"h264","pix_fmt":"yuv420p","width":1920,"height":1080,"avg_frame_rate":"30/1","r_frame_rate":"30/1"}]}`
		}
		return media.MiniMaxH3CommandResult{Stdout: []byte(format)}, nil
	}
	return media.MiniMaxH3CommandResult{}, os.WriteFile(args[len(args)-1], []byte("normalized"), 0o600)
}
