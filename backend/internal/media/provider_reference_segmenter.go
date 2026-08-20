package media

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

const (
	ProviderReferenceSegmentationSchemaVersion = "demoops.provider_reference_segmentation.v1"
	DefaultProviderReferenceMaxSegmentMS       = 12_000
	DefaultProviderReferenceMinSegmentMS       = 2_000
	ProviderReferenceAbsoluteMaxSegmentMS      = 15_000
)

// ProviderReferenceSegment is a Server-local derivative of immutable browser
// evidence. It is not an editor asset and has no approval or timeline authority.
type ProviderReferenceSegment struct {
	ID                 string              `json:"id"`
	SourceOffsetMS     int                 `json:"source_offset_ms"`
	DurationMS         int                 `json:"duration_ms"`
	Path               string              `json:"path"`
	SourceProbe        MiniMaxH3MediaProbe `json:"source_probe"`
	NormalizedProbe    MiniMaxH3MediaProbe `json:"normalized_probe"`
	NormalizationState string              `json:"normalization_state"`
	Usage              string              `json:"usage"`
}

type ProviderReferenceSegmentationResult struct {
	SchemaVersion string                     `json:"schema_version"`
	SourcePath    string                     `json:"source_path"`
	SourceProbe   MiniMaxH3MediaProbe        `json:"source_probe"`
	Segments      []ProviderReferenceSegment `json:"segments"`
	Policy        string                     `json:"policy"`
}

// FFmpegProviderReferenceSegmenter creates bounded, normalized reference
// derivatives for model input. It never uploads media or invokes a provider.
type FFmpegProviderReferenceSegmenter struct {
	FFmpegPath           string
	FFprobePath          string
	Runner               MiniMaxH3CommandRunner
	MaxSegmentDurationMS int
	MinSegmentDurationMS int
}

func (s FFmpegProviderReferenceSegmenter) Segment(ctx context.Context, sourcePath, outputDir string) (ProviderReferenceSegmentationResult, error) {
	sourcePath = strings.TrimSpace(sourcePath)
	outputDir = strings.TrimSpace(outputDir)
	if sourcePath == "" || outputDir == "" {
		return ProviderReferenceSegmentationResult{}, errors.New("source path and output directory are required")
	}
	sourceAbs, err := filepath.Abs(sourcePath)
	if err != nil {
		return ProviderReferenceSegmentationResult{}, err
	}
	info, err := os.Stat(sourceAbs)
	if err != nil {
		return ProviderReferenceSegmentationResult{}, fmt.Errorf("source recording is unavailable: %w", err)
	}
	if info.IsDir() {
		return ProviderReferenceSegmentationResult{}, errors.New("source recording must be a file")
	}
	outputAbs, err := filepath.Abs(outputDir)
	if err != nil {
		return ProviderReferenceSegmentationResult{}, err
	}
	runner := s.Runner
	if runner == nil {
		runner = ExecMiniMaxH3CommandRunner{}
	}
	ffprobePath := strings.TrimSpace(s.FFprobePath)
	if ffprobePath == "" {
		ffprobePath = "ffprobe"
	}
	sourceProbe, err := probeMiniMaxH3Media(ctx, runner, ffprobePath, sourceAbs)
	if err != nil {
		return ProviderReferenceSegmentationResult{}, fmt.Errorf("source media probe failed: %w", err)
	}
	if err := validateMiniMaxH3OriginalProbe(sourceProbe); err != nil {
		return ProviderReferenceSegmentationResult{}, err
	}
	planned, err := PlanProviderReferenceSegments(int(math.Round(sourceProbe.DurationSec*1000)), s.MaxSegmentDurationMS, s.MinSegmentDurationMS)
	if err != nil {
		return ProviderReferenceSegmentationResult{}, err
	}
	if err := os.MkdirAll(outputAbs, 0o700); err != nil {
		return ProviderReferenceSegmentationResult{}, err
	}
	result := ProviderReferenceSegmentationResult{
		SchemaVersion: ProviderReferenceSegmentationSchemaVersion,
		SourcePath:    sourceAbs, SourceProbe: sourceProbe, Segments: make([]ProviderReferenceSegment, 0, len(planned)),
		Policy: "server_local_normalized_model_reference_only",
	}
	for index, slice := range planned {
		path := filepath.Join(outputAbs, fmt.Sprintf("reference-%02d.mp4", index+1))
		normalizer := FFmpegMiniMaxH3MediaNormalizer{
			FFmpegPath: s.FFmpegPath, FFprobePath: s.FFprobePath, Runner: runner,
			StartOffsetMS: slice.SourceOffsetMS, MaxDurationMS: slice.DurationMS,
		}
		originalProbe, normalizedProbe, err := normalizer.Normalize(ctx, sourceAbs, path)
		if err != nil {
			return ProviderReferenceSegmentationResult{}, fmt.Errorf("segment %d normalization failed: %w", index+1, err)
		}
		if normalizedProbe.DurationSec < 1.99 || normalizedProbe.DurationSec > 15.01 {
			return ProviderReferenceSegmentationResult{}, fmt.Errorf("segment %d normalized duration %.3fs is outside the 2-15 second provider reference limit", index+1, normalizedProbe.DurationSec)
		}
		result.Segments = append(result.Segments, ProviderReferenceSegment{
			ID: fmt.Sprintf("provider_reference_%03d", index+1), SourceOffsetMS: slice.SourceOffsetMS, DurationMS: slice.DurationMS,
			Path: path, SourceProbe: originalProbe, NormalizedProbe: normalizedProbe,
			NormalizationState: "normalized", Usage: "provider_reference_only",
		})
	}
	return result, nil
}

// PlanProviderReferenceSegments partitions a recording into contiguous,
// balanced slices. The result avoids an unusably short final clip while never
// exceeding the provider's 15-second legal reference limit.
func PlanProviderReferenceSegments(totalDurationMS, maxSegmentMS, minSegmentMS int) ([]ProviderReferenceSegment, error) {
	if maxSegmentMS == 0 {
		maxSegmentMS = DefaultProviderReferenceMaxSegmentMS
	}
	if minSegmentMS == 0 {
		minSegmentMS = DefaultProviderReferenceMinSegmentMS
	}
	if totalDurationMS < minSegmentMS {
		return nil, fmt.Errorf("source duration %dms is below the %dms provider reference minimum", totalDurationMS, minSegmentMS)
	}
	if minSegmentMS < DefaultProviderReferenceMinSegmentMS || minSegmentMS > maxSegmentMS || maxSegmentMS > ProviderReferenceAbsoluteMaxSegmentMS {
		return nil, errors.New("provider reference segment bounds must be between 2 and 15 seconds")
	}
	count := int(math.Ceil(float64(totalDurationMS) / float64(maxSegmentMS)))
	if count < 1 {
		count = 1
	}
	base := totalDurationMS / count
	remainder := totalDurationMS % count
	if base < minSegmentMS || base+1 > maxSegmentMS {
		return nil, errors.New("source duration cannot be partitioned into legal provider reference segments")
	}
	segments := make([]ProviderReferenceSegment, 0, count)
	offset := 0
	for index := 0; index < count; index++ {
		duration := base
		if index < remainder {
			duration++
		}
		segments = append(segments, ProviderReferenceSegment{
			ID: fmt.Sprintf("provider_reference_%03d", index+1), SourceOffsetMS: offset, DurationMS: duration,
			NormalizationState: "planned", Usage: "provider_reference_only",
		})
		offset += duration
	}
	return segments, nil
}
