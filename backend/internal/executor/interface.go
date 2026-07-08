package executor

import (
	"context"

	"cascade-demoops/backend/internal/model"
)

type Service interface {
	Record(ctx context.Context, request RecordRequest) (RecordResult, error)
	Render(ctx context.Context, request RenderRequest) (RenderResult, error)
}

type RecordRequest struct {
	Graph     *model.DemoWorkflowGraph `json:"graph"`
	OutputDir string                   `json:"output_dir"`
	Viewport  Viewport                 `json:"viewport"`
	Headless  bool                     `json:"headless"`
}

type RecordResult struct {
	RecordingPath   string   `json:"recording_path"`
	ScreenshotPaths []string `json:"screenshot_paths"`
	TracePath       string   `json:"trace_path,omitempty"`
}

type RenderRequest struct {
	Graph          *model.DemoWorkflowGraph `json:"graph"`
	RecordingPaths []string                 `json:"recording_paths"`
	OutputDir      string                   `json:"output_dir"`
	DurationSec    int                      `json:"duration_sec"`
}

type RenderResult struct {
	VideoPath          string `json:"video_path"`
	StepByStepDocsPath string `json:"step_by_step_docs_path"`
}

type Viewport struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}
