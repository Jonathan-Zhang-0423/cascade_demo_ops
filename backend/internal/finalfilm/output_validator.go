package finalfilm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"time"

	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/model"
)

type finalFilmRenderManifest struct {
	Compositor struct {
		QualityStatus     string   `json:"quality_status"`
		FallbackReason    string   `json:"fallback_reason"`
		FFmpegAvailable   *bool    `json:"ffmpeg_available"`
		PlannedOperations []string `json:"planned_operations"`
		AppliedOperations []string `json:"applied_operations"`
		SkippedOperations []any    `json:"skipped_operations"`
	} `json:"compositor"`
	RequirementSatisfactionReport struct {
		Status string `json:"status"`
	} `json:"requirement_satisfaction_report"`
}

func validateFinalFilmOutput(ctx context.Context, renderer Renderer, result executor.RenderResult, profile model.EditorRenderProfile, checkedAt time.Time) (model.FinalFilmOutputValidation, error) {
	validation := model.FinalFilmOutputValidation{Status: "failed", CheckedAt: checkedAt.UTC()}
	fail := func(err error) (model.FinalFilmOutputValidation, error) {
		validation.Error = err.Error()
		return validation, err
	}
	probe, err := renderer.ProbeMedia(ctx, executor.MediaProbeRequest{Path: result.VideoPath})
	if err != nil {
		return fail(fmt.Errorf("probe final video: %w", err))
	}
	validation.VideoSHA256, validation.VideoSizeBytes = strings.ToLower(strings.TrimSpace(probe.SHA256)), probe.SizeBytes
	validation.Width, validation.Height, validation.FPS, validation.DurationMS = probe.Width, probe.Height, probe.FPS, probe.DurationMS
	if !probe.FFProbeAvailable || probe.SizeBytes <= 0 || len(validation.VideoSHA256) != 64 || probe.DurationMS <= 0 {
		return fail(errors.New("final video probe lacks ffprobe, integrity, size, or duration evidence"))
	}
	if probe.Width != profile.Width || probe.Height != profile.Height || math.Abs(probe.FPS-float64(profile.FPS)) > 0.01 {
		return fail(fmt.Errorf("final video profile mismatch: got %dx%d %.3ffps", probe.Width, probe.Height, probe.FPS))
	}
	if !strings.Contains(strings.ToLower(probe.Format), "mp4") || !strings.EqualFold(probe.VideoCodec, "h264") || !strings.EqualFold(probe.PixelFormat, "yuv420p") {
		return fail(errors.New("final video must be MP4/H.264/yuv420p"))
	}
	data, err := os.ReadFile(result.RenderManifestPath)
	if err != nil {
		return fail(fmt.Errorf("read final render manifest: %w", err))
	}
	var manifest finalFilmRenderManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fail(fmt.Errorf("decode final render manifest: %w", err))
	}
	validation.CompositorQualityStatus = strings.TrimSpace(manifest.Compositor.QualityStatus)
	validation.RequirementReportStatus = strings.TrimSpace(manifest.RequirementSatisfactionReport.Status)
	if validation.CompositorQualityStatus != "ok" || manifest.Compositor.FFmpegAvailable == nil || !*manifest.Compositor.FFmpegAvailable || strings.TrimSpace(manifest.Compositor.FallbackReason) != "" {
		return fail(errors.New("final compositor must report ffmpeg quality ok with no fallback"))
	}
	if validation.RequirementReportStatus != "satisfied" && validation.RequirementReportStatus != "satisfied_with_warnings" {
		return fail(errors.New("final requirement satisfaction report is not satisfied"))
	}
	if len(manifest.Compositor.SkippedOperations) > 0 {
		return fail(errors.New("final compositor skipped planned operations"))
	}
	for _, operation := range manifest.Compositor.PlannedOperations {
		if !slices.Contains(manifest.Compositor.AppliedOperations, operation) {
			return fail(fmt.Errorf("final compositor did not apply planned operation %s", operation))
		}
	}
	validation.Status = "passed"
	return validation, nil
}
