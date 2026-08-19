package executor

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

const SeedanceReferenceSelectionSchemaVersion = "demoops.seedance_reference_selection.v1"

// SeedanceReferenceSelection binds a model input window to one passed stage of
// the authoritative raw recording. It has no provider, publication, approval,
// or edit-plan mutation authority.
type SeedanceReferenceSelection struct {
	SchemaVersion          string                 `json:"schema_version"`
	Status                 string                 `json:"status"`
	RecordingArtifactID    string                 `json:"recording_artifact_id"`
	SourceStepID           string                 `json:"source_step_id"`
	VerifiedStageRangeMS   model.MillisecondRange `json:"verified_stage_range_ms"`
	ReferenceWindowRangeMS model.MillisecondRange `json:"reference_window_range_ms"`
	SelectionStrategy      string                 `json:"selection_strategy"`
	Policy                 string                 `json:"policy"`
}

// SelectSeedanceReferenceWindow requires an explicitly requested passed stage.
// The returned reference window includes that stage and is kept within the
// conservative 2-12 second input profile before provider publication.
func SelectSeedanceReferenceWindow(catalog *model.AssetTimelineCatalog, preferredStepID string) (SeedanceReferenceSelection, error) {
	selection := SeedanceReferenceSelection{
		SchemaVersion: SeedanceReferenceSelectionSchemaVersion,
		Status:        "blocked",
		Policy:        "verified_stage_bound_server_local_reference_only",
	}
	if catalog == nil {
		return selection, errors.New("asset timeline catalog is required for Seedance reference selection")
	}
	if strings.TrimSpace(preferredStepID) == "" {
		return selection, errors.New("explicit preferred_step_id is required; no recording-start fallback is allowed")
	}
	if catalog.Timeline.DurationMS < media.DefaultProviderReferenceMinSegmentMS {
		return selection, errors.New("recording is shorter than the provider reference minimum")
	}
	if strings.TrimSpace(catalog.Timeline.RecordingArtifactID) == "" {
		return selection, errors.New("asset timeline recording_artifact_id is required")
	}
	if !timelineContainsRawRecording(catalog, catalog.Timeline.RecordingArtifactID) {
		return selection, errors.New("asset timeline recording_artifact_id does not identify a raw recording artifact")
	}
	var matched *model.TimelineStep
	for index := range catalog.Steps {
		step := &catalog.Steps[index]
		if step.StepID == preferredStepID {
			matched = step
			break
		}
	}
	if matched == nil {
		return selection, fmt.Errorf("preferred_step_id %q is not present in the asset timeline", preferredStepID)
	}
	if !strings.EqualFold(strings.TrimSpace(matched.Status), "passed") {
		return selection, fmt.Errorf("preferred_step_id %q is not a passed stage", preferredStepID)
	}
	if matched.StartMS < 0 || matched.EndMS <= matched.StartMS || matched.EndMS > catalog.Timeline.DurationMS {
		return selection, fmt.Errorf("preferred_step_id %q has an invalid recording time range", preferredStepID)
	}
	stageRange := model.MillisecondRange{matched.StartMS, matched.EndMS}
	window, strategy := seedanceReferenceWindowForStage(stageRange, catalog.Timeline.DurationMS)
	selection.Status = "selected"
	selection.RecordingArtifactID = catalog.Timeline.RecordingArtifactID
	selection.SourceStepID = matched.StepID
	selection.VerifiedStageRangeMS = stageRange
	selection.ReferenceWindowRangeMS = window
	selection.SelectionStrategy = strategy
	return selection, nil
}

func timelineContainsRawRecording(catalog *model.AssetTimelineCatalog, recordingArtifactID string) bool {
	for _, artifact := range catalog.Artifacts {
		if artifact.ID == recordingArtifactID && strings.EqualFold(strings.TrimSpace(artifact.Kind), "raw_recording") {
			return true
		}
	}
	return false
}

func seedanceReferenceWindowForStage(stage model.MillisecondRange, recordingDurationMS int) (model.MillisecondRange, string) {
	stageDuration := stage[1] - stage[0]
	if stageDuration >= media.DefaultProviderReferenceMinSegmentMS && stageDuration <= media.DefaultProviderReferenceMaxSegmentMS {
		return stage, "verified_stage_exact_window"
	}
	if stageDuration > media.DefaultProviderReferenceMaxSegmentMS {
		return model.MillisecondRange{stage[0], stage[0] + media.DefaultProviderReferenceMaxSegmentMS}, "verified_stage_opening_window"
	}
	windowDuration := media.DefaultProviderReferenceMinSegmentMS
	start := stage[0] - (windowDuration-stageDuration)/2
	if start < 0 {
		start = 0
	}
	end := start + windowDuration
	if end > recordingDurationMS {
		end = recordingDurationMS
		start = end - windowDuration
	}
	return model.MillisecondRange{start, end}, "verified_stage_context_window"
}

// MaterializeSeedanceReferenceWindow creates one local, normalized reference
// derivative from a selected evidence-bound stage. It deliberately does not
// publish the file, invoke a provider, approve output, or update an edit plan.
func MaterializeSeedanceReferenceWindow(ctx context.Context, catalog *model.AssetTimelineCatalog, selection SeedanceReferenceSelection, outputDir string, normalizer media.FFmpegMiniMaxH3MediaNormalizer) (model.DirectorMaterialRef, error) {
	if selection.Status != "selected" {
		return model.DirectorMaterialRef{}, errors.New("a selected Seedance reference window is required")
	}
	expected, err := SelectSeedanceReferenceWindow(catalog, selection.SourceStepID)
	if err != nil {
		return model.DirectorMaterialRef{}, err
	}
	if expected != selection {
		return model.DirectorMaterialRef{}, errors.New("Seedance reference selection does not match verified timeline evidence")
	}
	if strings.TrimSpace(outputDir) == "" {
		return model.DirectorMaterialRef{}, errors.New("output directory is required for Seedance reference materialization")
	}
	var recording *model.TimelineArtifact
	for index := range catalog.Artifacts {
		if catalog.Artifacts[index].ID == selection.RecordingArtifactID {
			recording = &catalog.Artifacts[index]
			break
		}
	}
	if recording == nil {
		return model.DirectorMaterialRef{}, errors.New("selected recording artifact is unavailable")
	}
	sourcePath := strings.TrimSpace(recording.LocalPath)
	if sourcePath == "" {
		var ok bool
		sourcePath, ok = localDirectorMaterialPath(recording.URI)
		if !ok {
			return model.DirectorMaterialRef{}, errors.New("selected recording artifact is not a local file")
		}
	}
	if _, err := os.Stat(sourcePath); err != nil {
		return model.DirectorMaterialRef{}, fmt.Errorf("selected recording artifact is unavailable: %w", err)
	}
	outputAbs, err := filepath.Abs(outputDir)
	if err != nil {
		return model.DirectorMaterialRef{}, err
	}
	if err := os.MkdirAll(outputAbs, 0o700); err != nil {
		return model.DirectorMaterialRef{}, err
	}
	durationMS := selection.ReferenceWindowRangeMS[1] - selection.ReferenceWindowRangeMS[0]
	normalizer.StartOffsetMS = selection.ReferenceWindowRangeMS[0]
	normalizer.MaxDurationMS = durationMS
	destination := filepath.Join(outputAbs, safeID(selection.RecordingArtifactID+"_"+selection.SourceStepID)+".mp4")
	originalProbe, normalizedProbe, err := normalizer.Normalize(ctx, sourcePath, destination)
	if err != nil {
		return model.DirectorMaterialRef{}, fmt.Errorf("selected Seedance reference normalization failed: %w", err)
	}
	actualDurationMS := int(math.Round(normalizedProbe.DurationSec * 1000))
	if absInt(actualDurationMS-durationMS) > 250 {
		return model.DirectorMaterialRef{}, fmt.Errorf("normalized Seedance reference duration mismatch: got=%dms want=%dms", actualDurationMS, durationMS)
	}
	digest, size, err := arkMediaFileDigest(destination)
	if err != nil {
		return model.DirectorMaterialRef{}, err
	}
	metadata := map[string]any{
		"artifact_variant":          "seedance_stage_reference_derivative",
		"source_artifact_id":        selection.RecordingArtifactID,
		"source_step_id":            selection.SourceStepID,
		"verified_stage_range_ms":   selection.VerifiedStageRangeMS,
		"reference_window_range_ms": selection.ReferenceWindowRangeMS,
		"selection_strategy":        selection.SelectionStrategy,
		"normalization_profile":     media.GeneratedShotNormalizationProfile,
		"normalization_status":      "ok",
		"media_probe_status":        "ok",
		"original_media_probe":      originalProbe,
		"normalized_media_probe":    normalizedProbe,
		"model_reference_only":      true,
		"include_in_demo":           false,
		"provider_output_adopted":   false,
		"candidate_review_required": true,
	}
	return model.DirectorMaterialRef{
		ID:   safeID(selection.RecordingArtifactID + "_" + selection.SourceStepID + "_seedance_reference"),
		Kind: "model_reference_video", URI: destination, MimeType: "video/mp4", SHA256: digest, SizeBytes: size,
		SourceNodeID: selection.SourceStepID, AssetRole: "seedance_stage_reference_video", IncludeInDemo: false, Metadata: metadata,
	}, nil
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
