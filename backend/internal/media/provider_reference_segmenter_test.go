package media

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanProviderReferenceSegmentsBalancesLegalSlices(t *testing.T) {
	segments, err := PlanProviderReferenceSegments(25_000, 12_000, 2_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 3 {
		t.Fatalf("segment count=%d", len(segments))
	}
	offset := 0
	for _, segment := range segments {
		if segment.SourceOffsetMS != offset || segment.DurationMS < 2_000 || segment.DurationMS > 12_000 || segment.Usage != "provider_reference_only" {
			t.Fatalf("invalid planned segment: %+v", segment)
		}
		offset += segment.DurationMS
	}
	if offset != 25_000 {
		t.Fatalf("planned duration=%d", offset)
	}
}

func TestPlanProviderReferenceSegmentsRejectsIllegalBounds(t *testing.T) {
	if _, err := PlanProviderReferenceSegments(1_999, 12_000, 2_000); err == nil {
		t.Fatal("expected sub-minimum source to be rejected")
	}
	if _, err := PlanProviderReferenceSegments(20_000, 16_000, 2_000); err == nil {
		t.Fatal("expected provider maximum over 15 seconds to be rejected")
	}
}

func TestFFmpegProviderReferenceSegmenterRejectsDirectorySource(t *testing.T) {
	_, err := (FFmpegProviderReferenceSegmenter{}).Segment(context.Background(), t.TempDir(), filepath.Join(t.TempDir(), "segments"))
	if err == nil || !strings.Contains(err.Error(), "must be a file") {
		t.Fatalf("expected directory source rejection, got %v", err)
	}
}

func TestFFmpegProviderReferenceSegmenterProducesNormalizedServerOnlySlices(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "recording.webm")
	if err := os.WriteFile(source, []byte("recording"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &providerReferenceSegmenterRunner{}
	result, err := (FFmpegProviderReferenceSegmenter{
		FFmpegPath: "ffmpeg-test", FFprobePath: "ffprobe-test", Runner: runner, MaxSegmentDurationMS: 12_000, MinSegmentDurationMS: 2_000,
	}).Segment(context.Background(), source, filepath.Join(root, "segments"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Policy != "server_local_normalized_model_reference_only" || len(result.Segments) != 3 {
		t.Fatalf("result=%+v", result)
	}
	for _, segment := range result.Segments {
		if segment.NormalizationState != "normalized" || segment.Usage != "provider_reference_only" || segment.NormalizedProbe.Width != 1920 || segment.NormalizedProbe.Height != 1080 || segment.NormalizedProbe.FPS != 30 || !segment.NormalizedProbe.CFR {
			t.Fatalf("segment=%+v", segment)
		}
	}
	joined := strings.Join(runner.ffmpegArgs, " ")
	for _, required := range []string{"-ss 8.334", "-t 8.333", "scale=1920:1080", "yuv420p", "-r 30"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("segmentation ffmpeg args missing %q: %s", required, joined)
		}
	}
}

type providerReferenceSegmenterRunner struct{ ffmpegArgs []string }

func (r *providerReferenceSegmenterRunner) Run(_ context.Context, command string, args ...string) (MiniMaxH3CommandResult, error) {
	if strings.Contains(command, "ffprobe") {
		path := args[len(args)-1]
		if strings.Contains(filepath.Base(path), ".tmp.") {
			return MiniMaxH3CommandResult{Stdout: []byte(`{"format":{"format_name":"mov,mp4","duration":"8.334"},"streams":[{"codec_type":"video","codec_name":"h264","pix_fmt":"yuv420p","width":1920,"height":1080,"avg_frame_rate":"30/1","r_frame_rate":"30/1"}]}`)}, nil
		}
		return MiniMaxH3CommandResult{Stdout: []byte(`{"format":{"format_name":"matroska,webm","duration":"25"},"streams":[{"codec_type":"video","codec_name":"vp8","pix_fmt":"yuv420p","width":2560,"height":1440,"avg_frame_rate":"30/1","r_frame_rate":"30/1"}]}`)}, nil
	}
	r.ffmpegArgs = append(r.ffmpegArgs, args...)
	return MiniMaxH3CommandResult{}, os.WriteFile(args[len(args)-1], []byte("normalized"), 0o600)
}
