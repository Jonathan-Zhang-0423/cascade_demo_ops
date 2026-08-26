package finalfilm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"cascade-demoops/backend/internal/model"
)

type PaletteBoardRequest struct {
	ArtifactID string
	Color      string
	OutputPath string
}

type PaletteBoardBuilder interface {
	BuildPaletteBoard(context.Context, PaletteBoardRequest) (model.TimelineArtifact, error)
}

type FFmpegPaletteBoardBuilder struct{ FFmpegPath string }

var paletteColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (b FFmpegPaletteBoardBuilder) BuildPaletteBoard(ctx context.Context, request PaletteBoardRequest) (model.TimelineArtifact, error) {
	if strings.TrimSpace(b.FFmpegPath) == "" || strings.TrimSpace(request.ArtifactID) == "" || !paletteColorPattern.MatchString(request.Color) || strings.TrimSpace(request.OutputPath) == "" {
		return model.TimelineArtifact{}, errors.New("palette board requires ffmpeg, artifact identity, hex color, and output path")
	}
	path := filepath.Clean(request.OutputPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return model.TimelineArtifact{}, err
	}
	filter := fmt.Sprintf("color=c=%s:s=1920x1080:d=0.1", request.Color)
	command := exec.CommandContext(ctx, b.FFmpegPath, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", filter, "-frames:v", "1", path)
	command.Env = os.Environ()
	if output, err := command.CombinedOutput(); err != nil {
		return model.TimelineArtifact{}, fmt.Errorf("generate text-free palette board: %w: %s", err, strings.TrimSpace(string(output)))
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return model.TimelineArtifact{}, errors.New("FFmpeg palette board was not materialized")
	}
	return model.TimelineArtifact{ID: request.ArtifactID, Kind: "generated_palette_reference", URI: "asset://" + request.ArtifactID, LocalPath: path, MimeType: "image/png", Label: "Text-free palette reference", SizeBytes: info.Size(), Sensitive: false, AssetRole: "presentation_reference", IncludeInDemo: true, Metadata: map[string]any{"text_free": true, "ui_free": true, "source": "observed_palette", "timeline_insertable": false}}, nil
}
