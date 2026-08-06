package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	MiniMaxH3NormalizedWidth  = 1920
	MiniMaxH3NormalizedHeight = 1080
	MiniMaxH3NormalizedFPS    = 30
)

type MiniMaxH3CommandResult struct {
	Stdout []byte
	Stderr []byte
}

type MiniMaxH3CommandRunner interface {
	Run(ctx context.Context, command string, args ...string) (MiniMaxH3CommandResult, error)
}

type ExecMiniMaxH3CommandRunner struct{}

func (ExecMiniMaxH3CommandRunner) Run(ctx context.Context, command string, args ...string) (MiniMaxH3CommandResult, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	var stdout strings.Builder
	var stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return MiniMaxH3CommandResult{Stdout: []byte(stdout.String()), Stderr: []byte(stderr.String())}, err
}

type FFmpegMiniMaxH3MediaNormalizer struct {
	FFmpegPath  string
	FFprobePath string
	Runner      MiniMaxH3CommandRunner
}

func (n FFmpegMiniMaxH3MediaNormalizer) Normalize(ctx context.Context, sourcePath string, destinationPath string) (MiniMaxH3MediaProbe, MiniMaxH3MediaProbe, error) {
	sourcePath = strings.TrimSpace(sourcePath)
	destinationPath = strings.TrimSpace(destinationPath)
	if sourcePath == "" || destinationPath == "" {
		return MiniMaxH3MediaProbe{}, MiniMaxH3MediaProbe{}, errors.New("source and destination paths are required")
	}
	sourceAbs, err := filepath.Abs(sourcePath)
	if err != nil {
		return MiniMaxH3MediaProbe{}, MiniMaxH3MediaProbe{}, err
	}
	destinationAbs, err := filepath.Abs(destinationPath)
	if err != nil {
		return MiniMaxH3MediaProbe{}, MiniMaxH3MediaProbe{}, err
	}
	if strings.EqualFold(sourceAbs, destinationAbs) {
		return MiniMaxH3MediaProbe{}, MiniMaxH3MediaProbe{}, errors.New("normalized artifact path must differ from original artifact path")
	}
	if _, err := os.Stat(sourceAbs); err != nil {
		return MiniMaxH3MediaProbe{}, MiniMaxH3MediaProbe{}, fmt.Errorf("original artifact is unavailable: %w", err)
	}
	if _, err := os.Stat(destinationAbs); err == nil {
		return MiniMaxH3MediaProbe{}, MiniMaxH3MediaProbe{}, errors.New("normalized artifact destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return MiniMaxH3MediaProbe{}, MiniMaxH3MediaProbe{}, err
	}
	if err := os.MkdirAll(filepath.Dir(destinationAbs), 0o755); err != nil {
		return MiniMaxH3MediaProbe{}, MiniMaxH3MediaProbe{}, err
	}

	runner := n.Runner
	if runner == nil {
		runner = ExecMiniMaxH3CommandRunner{}
	}
	ffprobePath := strings.TrimSpace(n.FFprobePath)
	if ffprobePath == "" {
		ffprobePath = "ffprobe"
	}
	ffmpegPath := strings.TrimSpace(n.FFmpegPath)
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	originalProbe, err := probeMiniMaxH3Media(ctx, runner, ffprobePath, sourceAbs)
	if err != nil {
		return MiniMaxH3MediaProbe{}, MiniMaxH3MediaProbe{}, fmt.Errorf("original media probe failed: %w", err)
	}
	if err := validateMiniMaxH3OriginalProbe(originalProbe); err != nil {
		return originalProbe, MiniMaxH3MediaProbe{}, err
	}

	temporaryPath := strings.TrimSuffix(destinationAbs, filepath.Ext(destinationAbs)) + ".tmp.mp4"
	if _, err := os.Stat(temporaryPath); err == nil {
		return originalProbe, MiniMaxH3MediaProbe{}, errors.New("normalization temporary artifact already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return originalProbe, MiniMaxH3MediaProbe{}, err
	}
	args := []string{
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", sourceAbs,
		"-map", "0:v:0", "-map", "0:a?",
		"-vf", "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2:color=black",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-r", "30", "-fps_mode", "cfr",
		"-c:a", "aac", "-movflags", "+faststart",
		temporaryPath,
	}
	commandResult, err := runner.Run(ctx, ffmpegPath, args...)
	if err != nil {
		_ = os.Remove(temporaryPath)
		return originalProbe, MiniMaxH3MediaProbe{}, fmt.Errorf("ffmpeg normalization failed: %s", compactMiniMaxH3CommandError(commandResult, err))
	}
	normalizedProbe, err := probeMiniMaxH3Media(ctx, runner, ffprobePath, temporaryPath)
	if err != nil {
		_ = os.Remove(temporaryPath)
		return originalProbe, MiniMaxH3MediaProbe{}, fmt.Errorf("normalized media probe failed: %w", err)
	}
	if err := validateMiniMaxH3NormalizedProbe(normalizedProbe); err != nil {
		_ = os.Remove(temporaryPath)
		return originalProbe, normalizedProbe, err
	}
	if err := os.Rename(temporaryPath, destinationAbs); err != nil {
		_ = os.Remove(temporaryPath)
		return originalProbe, normalizedProbe, err
	}
	return originalProbe, normalizedProbe, nil
}

type miniMaxH3FFProbeResponse struct {
	Format struct {
		FormatName string `json:"format_name"`
		Duration   string `json:"duration"`
	} `json:"format"`
	Streams []struct {
		CodecType   string `json:"codec_type"`
		CodecName   string `json:"codec_name"`
		PixelFormat string `json:"pix_fmt"`
		Width       int    `json:"width"`
		Height      int    `json:"height"`
		FrameRate   string `json:"avg_frame_rate"`
		NominalRate string `json:"r_frame_rate"`
		Duration    string `json:"duration"`
	} `json:"streams"`
}

func probeMiniMaxH3Media(ctx context.Context, runner MiniMaxH3CommandRunner, ffprobePath string, path string) (MiniMaxH3MediaProbe, error) {
	result, err := runner.Run(ctx, ffprobePath,
		"-v", "error", "-show_entries", "format=format_name,duration:stream=codec_type,codec_name,pix_fmt,width,height,avg_frame_rate,r_frame_rate,duration",
		"-of", "json", path,
	)
	if err != nil {
		return MiniMaxH3MediaProbe{}, errors.New(compactMiniMaxH3CommandError(result, err))
	}
	var response miniMaxH3FFProbeResponse
	if err := json.Unmarshal(result.Stdout, &response); err != nil {
		return MiniMaxH3MediaProbe{}, fmt.Errorf("invalid ffprobe JSON: %w", err)
	}
	probe := MiniMaxH3MediaProbe{Format: strings.TrimSpace(response.Format.FormatName)}
	probe.DurationSec, _ = strconv.ParseFloat(strings.TrimSpace(response.Format.Duration), 64)
	for _, stream := range response.Streams {
		if stream.CodecType != "video" {
			continue
		}
		probe.VideoCodec = strings.TrimSpace(stream.CodecName)
		probe.PixelFormat = strings.TrimSpace(stream.PixelFormat)
		probe.Width = stream.Width
		probe.Height = stream.Height
		probe.FPS = parseMiniMaxH3FrameRate(stream.FrameRate)
		nominalFPS := parseMiniMaxH3FrameRate(stream.NominalRate)
		probe.CFR = probe.FPS > 0 && nominalFPS > 0 && math.Abs(probe.FPS-nominalFPS) <= 0.01
		if probe.DurationSec <= 0 {
			probe.DurationSec, _ = strconv.ParseFloat(strings.TrimSpace(stream.Duration), 64)
		}
		break
	}
	return probe, nil
}

func validateMiniMaxH3OriginalProbe(probe MiniMaxH3MediaProbe) error {
	if probe.VideoCodec == "" || probe.Width <= 0 || probe.Height <= 0 || probe.FPS <= 0 || probe.DurationSec <= 0 {
		return fmt.Errorf("original media is not a decodable video: %+v", probe)
	}
	return nil
}

func validateMiniMaxH3NormalizedProbe(probe MiniMaxH3MediaProbe) error {
	if !strings.Contains(strings.ToLower(probe.Format), "mp4") {
		return fmt.Errorf("normalized media container must be MP4, got %q", probe.Format)
	}
	if strings.ToLower(probe.VideoCodec) != "h264" {
		return fmt.Errorf("normalized media codec must be H.264, got %q", probe.VideoCodec)
	}
	if strings.ToLower(probe.PixelFormat) != "yuv420p" {
		return fmt.Errorf("normalized media pixel format must be yuv420p, got %q", probe.PixelFormat)
	}
	if probe.Width != MiniMaxH3NormalizedWidth || probe.Height != MiniMaxH3NormalizedHeight {
		return fmt.Errorf("normalized media dimensions must be 1920x1080, got %dx%d", probe.Width, probe.Height)
	}
	if math.Abs(probe.FPS-MiniMaxH3NormalizedFPS) > 0.01 {
		return fmt.Errorf("normalized media FPS must be CFR 30, got %.3f", probe.FPS)
	}
	if !probe.CFR {
		return errors.New("normalized media must use constant frame rate")
	}
	if probe.DurationSec <= 0 {
		return errors.New("normalized media duration must be positive")
	}
	return nil
}

func parseMiniMaxH3FrameRate(value string) float64 {
	parts := strings.Split(strings.TrimSpace(value), "/")
	if len(parts) == 2 {
		numerator, numeratorErr := strconv.ParseFloat(parts[0], 64)
		denominator, denominatorErr := strconv.ParseFloat(parts[1], 64)
		if numeratorErr == nil && denominatorErr == nil && denominator != 0 {
			return numerator / denominator
		}
	}
	valueFloat, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return valueFloat
}

func compactMiniMaxH3CommandError(result MiniMaxH3CommandResult, runErr error) string {
	message := strings.TrimSpace(string(result.Stderr))
	if message == "" {
		message = strings.TrimSpace(string(result.Stdout))
	}
	if message == "" && runErr != nil {
		message = runErr.Error()
	}
	message = strings.Join(strings.Fields(message), " ")
	if len(message) > 500 {
		message = message[:500] + "..."
	}
	return message
}
