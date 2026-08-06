package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type scriptedMiniMaxH3CommandRunner struct {
	calls []struct {
		command string
		args    []string
	}
}

func (s *scriptedMiniMaxH3CommandRunner) Run(_ context.Context, command string, args ...string) (MiniMaxH3CommandResult, error) {
	s.calls = append(s.calls, struct {
		command string
		args    []string
	}{command: command, args: append([]string{}, args...)})
	if strings.Contains(strings.ToLower(filepath.Base(command)), "ffprobe") {
		path := args[len(args)-1]
		if strings.Contains(filepath.Base(path), ".tmp.") {
			return MiniMaxH3CommandResult{Stdout: []byte(`{"format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2","duration":"5.000"},"streams":[{"codec_type":"video","codec_name":"h264","pix_fmt":"yuv420p","width":1920,"height":1080,"avg_frame_rate":"30/1","r_frame_rate":"30/1"}]}`)}, nil
		}
		return MiniMaxH3CommandResult{Stdout: []byte(`{"format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2","duration":"5.000"},"streams":[{"codec_type":"video","codec_name":"hevc","pix_fmt":"yuv420p10le","width":2048,"height":1152,"avg_frame_rate":"24/1","r_frame_rate":"24/1"}]}`)}, nil
	}
	destination := args[len(args)-1]
	if err := os.WriteFile(destination, []byte("normalized"), 0o644); err != nil {
		return MiniMaxH3CommandResult{}, err
	}
	return MiniMaxH3CommandResult{}, nil
}

func TestFFmpegMiniMaxH3MediaNormalizerUsesLockedProfileAndDoubleProbe(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "original.mp4")
	destination := filepath.Join(root, "normalized.mp4")
	if err := os.WriteFile(source, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedMiniMaxH3CommandRunner{}
	normalizer := FFmpegMiniMaxH3MediaNormalizer{FFmpegPath: "ffmpeg-test", FFprobePath: "ffprobe-test", Runner: runner}
	original, normalized, err := normalizer.Normalize(t.Context(), source, destination)
	if err != nil {
		t.Fatal(err)
	}
	if original.VideoCodec != "hevc" || normalized.VideoCodec != "h264" || normalized.PixelFormat != "yuv420p" || normalized.FPS != 30 || !normalized.CFR || normalized.Width != 1920 || normalized.Height != 1080 {
		t.Fatalf("probes = original=%+v normalized=%+v", original, normalized)
	}
	if len(runner.calls) != 3 {
		t.Fatalf("command calls = %+v", runner.calls)
	}
	ffmpegArgs := strings.Join(runner.calls[1].args, " ")
	for _, required := range []string{"libx264", "yuv420p", "-r 30", "-fps_mode cfr", "scale=1920:1080", "+faststart"} {
		if !strings.Contains(ffmpegArgs, required) {
			t.Fatalf("ffmpeg args missing %q: %s", required, ffmpegArgs)
		}
	}
	if _, err := os.Stat(destination); err != nil {
		t.Fatalf("normalized output missing: %v", err)
	}
}

func TestFFmpegMiniMaxH3MediaNormalizerRejectsBadNormalizedProbe(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "original.mp4")
	if err := os.WriteFile(source, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := MiniMaxH3CommandRunnerFunc(func(_ context.Context, command string, args ...string) (MiniMaxH3CommandResult, error) {
		if strings.Contains(strings.ToLower(filepath.Base(command)), "ffprobe") {
			return MiniMaxH3CommandResult{Stdout: []byte(`{"format":{"format_name":"mov,mp4","duration":"5"},"streams":[{"codec_type":"video","codec_name":"hevc","pix_fmt":"yuv420p10le","width":1280,"height":720,"avg_frame_rate":"24/1","r_frame_rate":"24/1"}]}`)}, nil
		}
		return MiniMaxH3CommandResult{}, os.WriteFile(args[len(args)-1], []byte("bad-normalized"), 0o644)
	})
	_, _, err := (FFmpegMiniMaxH3MediaNormalizer{Runner: runner}).Normalize(t.Context(), source, filepath.Join(root, "normalized.mp4"))
	if err == nil || !strings.Contains(err.Error(), "codec must be H.264") {
		t.Fatalf("err = %v", err)
	}
}

type MiniMaxH3CommandRunnerFunc func(ctx context.Context, command string, args ...string) (MiniMaxH3CommandResult, error)

func (f MiniMaxH3CommandRunnerFunc) Run(ctx context.Context, command string, args ...string) (MiniMaxH3CommandResult, error) {
	if f == nil {
		return MiniMaxH3CommandResult{}, errors.New("runner is nil")
	}
	return f(ctx, command, args...)
}
