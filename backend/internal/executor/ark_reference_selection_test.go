package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

func TestSelectSeedanceReferenceWindowBindsExactPassedStage(t *testing.T) {
	catalog := seedanceSelectionCatalog()
	selection, err := SelectSeedanceReferenceWindow(&catalog, "build")
	if err != nil {
		t.Fatal(err)
	}
	if selection.Status != "selected" || selection.RecordingArtifactID != "recording" || selection.SourceStepID != "build" || selection.VerifiedStageRangeMS != (model.MillisecondRange{4_000, 10_000}) || selection.ReferenceWindowRangeMS != selection.VerifiedStageRangeMS || selection.SelectionStrategy != "verified_stage_exact_window" {
		t.Fatalf("selection=%+v", selection)
	}
}

func TestSelectSeedanceReferenceWindowRequiresExplicitPassedEvidence(t *testing.T) {
	catalog := seedanceSelectionCatalog()
	for _, test := range []struct {
		step string
		want string
	}{
		{"", "explicit preferred_step_id"},
		{"missing", "not present"},
		{"failed", "not a passed"},
	} {
		if _, err := SelectSeedanceReferenceWindow(&catalog, test.step); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("step=%q error=%v", test.step, err)
		}
	}
}

func TestSelectSeedanceReferenceWindowKeepsLongAndShortStagesLegal(t *testing.T) {
	catalog := seedanceSelectionCatalog()
	selection, err := SelectSeedanceReferenceWindow(&catalog, "long")
	if err != nil {
		t.Fatal(err)
	}
	if selection.ReferenceWindowRangeMS != (model.MillisecondRange{12_000, 24_000}) || selection.SelectionStrategy != "verified_stage_opening_window" {
		t.Fatalf("long stage selection=%+v", selection)
	}
	selection, err = SelectSeedanceReferenceWindow(&catalog, "brief")
	if err != nil {
		t.Fatal(err)
	}
	if selection.ReferenceWindowRangeMS != (model.MillisecondRange{27_250, 29_250}) || selection.SelectionStrategy != "verified_stage_context_window" {
		t.Fatalf("brief stage selection=%+v", selection)
	}
}

func TestSelectSeedanceReferenceWindowRejectsMismatchedRecordingArtifact(t *testing.T) {
	catalog := seedanceSelectionCatalog()
	catalog.Timeline.RecordingArtifactID = "screenshot"
	if _, err := SelectSeedanceReferenceWindow(&catalog, "build"); err == nil || !strings.Contains(err.Error(), "does not identify a raw recording") {
		t.Fatalf("error=%v", err)
	}
}

func TestMaterializeSeedanceReferenceWindowUsesVerifiedStageRange(t *testing.T) {
	root := t.TempDir()
	recordingPath := filepath.Join(root, "recording.webm")
	if err := os.WriteFile(recordingPath, []byte("recording"), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := seedanceSelectionCatalog()
	catalog.Artifacts[0].LocalPath = recordingPath
	selection, err := SelectSeedanceReferenceWindow(&catalog, "build")
	if err != nil {
		t.Fatal(err)
	}
	runner := &referenceMaterializerRunner{}
	ref, err := MaterializeSeedanceReferenceWindow(context.Background(), &catalog, selection, filepath.Join(root, "references"), media.FFmpegMiniMaxH3MediaNormalizer{Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	if ref.MimeType != "video/mp4" || ref.IncludeInDemo || ref.Metadata["source_step_id"] != "build" || ref.Metadata["reference_window_range_ms"] != (model.MillisecondRange{4_000, 10_000}) || ref.Metadata["model_reference_only"] != true {
		t.Fatalf("reference=%+v", ref)
	}
	if !strings.Contains(strings.Join(runner.ffmpegArgs, " "), "-ss 4.000") || !strings.Contains(strings.Join(runner.ffmpegArgs, " "), "-t 6.000") {
		t.Fatalf("reference materializer did not use selected time range: %s", strings.Join(runner.ffmpegArgs, " "))
	}
}

func TestMaterializeSeedanceReferenceWindowRejectsTamperedSelection(t *testing.T) {
	catalog := seedanceSelectionCatalog()
	selection, err := SelectSeedanceReferenceWindow(&catalog, "build")
	if err != nil {
		t.Fatal(err)
	}
	selection.ReferenceWindowRangeMS = model.MillisecondRange{0, 6_000}
	if _, err := MaterializeSeedanceReferenceWindow(context.Background(), &catalog, selection, t.TempDir(), media.FFmpegMiniMaxH3MediaNormalizer{}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("error=%v", err)
	}
}

func TestBuildSeedanceReferencePublicationPlanBindsPassedStageAndKeepsModelOnly(t *testing.T) {
	catalog := seedanceSelectionCatalog()
	selection, err := SelectSeedanceReferenceWindow(&catalog, "build")
	if err != nil {
		t.Fatal(err)
	}
	ref := model.DirectorMaterialRef{ID: "recording_build_seedance_reference", URI: "D:/artifacts/reference.mp4", MimeType: "video/mp4", IncludeInDemo: false, SourceNodeID: "build", AssetRole: "seedance_stage_reference_video"}
	plan, err := BuildSeedanceReferencePublicationPlan(&catalog, selection, ref, "pkg_01", "task_01", model.DefaultMediaDeliveryPreferences().TOSRetention, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if plan.SchemaVersion != SeedanceReferencePublicationPlanSchemaVersion || plan.Status != "ready_after_publication" || plan.PublicationStrategy != "private_tos_presigned_url" || len(plan.Items) != 1 {
		t.Fatalf("unexpected publication plan: %+v", plan)
	}
	item := plan.Items[0]
	if !item.Required || !item.NeedsPublication || item.NeedsConversion || item.Usage != "seedance_reference_video" || item.TaskIDs[0] != "task_01" || item.Ref.IncludeInDemo {
		t.Fatalf("publication item violates model-only policy: %+v", item)
	}
}

func TestBuildSeedanceReferencePublicationPlanRejectsDemoInclusion(t *testing.T) {
	catalog := seedanceSelectionCatalog()
	selection, err := SelectSeedanceReferenceWindow(&catalog, "build")
	if err != nil {
		t.Fatal(err)
	}
	ref := model.DirectorMaterialRef{ID: "reference", URI: "D:/artifacts/reference.mp4", MimeType: "video/mp4", IncludeInDemo: true}
	if _, err := BuildSeedanceReferencePublicationPlan(&catalog, selection, ref, "pkg", "task", model.MediaTOSRetentionPreference{}, time.Now()); err == nil || !strings.Contains(err.Error(), "included in the delivered demo") {
		t.Fatalf("expected model-only inclusion guard, got %v", err)
	}
}

func seedanceSelectionCatalog() model.AssetTimelineCatalog {
	return model.AssetTimelineCatalog{
		SchemaVersion: model.AssetTimelineCatalogSchemaVersion,
		Timeline:      model.AssetTimelineInfo{DurationMS: 30_000, RecordingArtifactID: "recording"},
		Artifacts:     []model.TimelineArtifact{{ID: "recording", Kind: "raw_recording"}, {ID: "screenshot", Kind: "screenshot"}},
		Steps: []model.TimelineStep{
			{StepID: "build", Status: "passed", StartMS: 4_000, EndMS: 10_000},
			{StepID: "failed", Status: "failed", StartMS: 10_000, EndMS: 12_000},
			{StepID: "long", Status: "passed", StartMS: 12_000, EndMS: 27_000},
			{StepID: "brief", Status: "passed", StartMS: 28_000, EndMS: 28_500},
		},
	}
}

type referenceMaterializerRunner struct{ ffmpegArgs []string }

func (r *referenceMaterializerRunner) Run(_ context.Context, command string, args ...string) (media.MiniMaxH3CommandResult, error) {
	if strings.Contains(command, "ffprobe") {
		path := args[len(args)-1]
		if strings.Contains(filepath.Base(path), ".tmp.") {
			return media.MiniMaxH3CommandResult{Stdout: []byte(`{"format":{"format_name":"mov,mp4","duration":"6"},"streams":[{"codec_type":"video","codec_name":"h264","pix_fmt":"yuv420p","width":1920,"height":1080,"avg_frame_rate":"30/1","r_frame_rate":"30/1"}]}`)}, nil
		}
		return media.MiniMaxH3CommandResult{Stdout: []byte(`{"format":{"format_name":"matroska,webm","duration":"30"},"streams":[{"codec_type":"video","codec_name":"vp8","pix_fmt":"yuv420p","width":2560,"height":1440,"avg_frame_rate":"30/1","r_frame_rate":"30/1"}]}`)}, nil
	}
	r.ffmpegArgs = append(r.ffmpegArgs, args...)
	return media.MiniMaxH3CommandResult{}, os.WriteFile(args[len(args)-1], []byte("reference"), 0o600)
}
