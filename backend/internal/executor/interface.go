package executor

import (
	"context"
	"time"

	"cascade-demoops/backend/internal/model"
)

type Service interface {
	Record(ctx context.Context, request RecordRequest) (RecordResult, error)
	Render(ctx context.Context, request RenderRequest) (RenderResult, error)
}

type RecordingMode string

const (
	RecordingModeDryRun     RecordingMode = "dry_run"
	RecordingModePlaywright RecordingMode = "playwright"
)

type RecordRequest struct {
	Graph                  *model.DemoWorkflowGraph               `json:"graph"`
	OutputDir              string                                 `json:"output_dir"`
	Viewport               Viewport                               `json:"viewport"`
	Headless               bool                                   `json:"headless"`
	RecordingMode          RecordingMode                          `json:"recording_mode,omitempty"`
	SourcePackageID        string                                 `json:"source_package_id,omitempty"`
	RecordingRunSpec       *model.RecordingRunSpec                `json:"recording_run_spec,omitempty"`
	SandboxPolicy          *model.SandboxPolicy                   `json:"sandbox_policy,omitempty"`
	ExecutableScriptBundle *model.ExecutableRecordingScriptBundle `json:"executable_script_bundle,omitempty"`
}

type RecordResult struct {
	RecordingPath        string                          `json:"recording_path,omitempty"`
	ScreenshotPaths      []string                        `json:"screenshot_paths,omitempty"`
	TracePath            string                          `json:"trace_path,omitempty"`
	ArtifactManifestPath string                          `json:"artifact_manifest_path,omitempty"`
	GeneratedAssets      []model.ArtifactRef             `json:"generated_assets,omitempty"`
	StepResults          []model.StepResult              `json:"step_results,omitempty"`
	FailureDiagnostic    *model.ScriptFailureDiagnostic  `json:"failure_diagnostic,omitempty"`
	WorkerID             string                          `json:"worker_id,omitempty"`
	RuntimeVersions      map[string]string               `json:"runtime_versions,omitempty"`
	SandboxMetadata      *model.SandboxExecutionMetadata `json:"sandbox_metadata,omitempty"`
	StartedAt            time.Time                       `json:"started_at,omitempty"`
	CompletedAt          time.Time                       `json:"completed_at,omitempty"`
}

type RenderRequest struct {
	Graph                      *model.DemoWorkflowGraph      `json:"graph,omitempty"`
	RecordingPaths             []string                      `json:"recording_paths,omitempty"`
	OutputDir                  string                        `json:"output_dir,omitempty"`
	DurationSec                int                           `json:"duration_sec,omitempty"`
	ExecutionTrace             *model.ExecutionTrace         `json:"execution_trace,omitempty"`
	ExecutionTracePath         string                        `json:"execution_trace_path,omitempty"`
	GeneratedAssets            []model.ArtifactRef           `json:"generated_assets,omitempty"`
	ArtifactManifestPath       string                        `json:"artifact_manifest_path,omitempty"`
	RecordingResultPackage     *model.RecordingResultPackage `json:"recording_result_package,omitempty"`
	RecordingResultPackagePath string                        `json:"recording_result_package_path,omitempty"`
	EditPlan                   *model.DemoEditPlan           `json:"edit_plan,omitempty"`
}

type RenderResult struct {
	VideoPath                string                              `json:"video_path"`
	StepByStepDocsPath       string                              `json:"step_by_step_docs_path"`
	AssetTimelineCatalogPath string                              `json:"asset_timeline_catalog_path,omitempty"`
	DemoEditPlanPath         string                              `json:"demo_edit_plan_path,omitempty"`
	ValidationReportPath     string                              `json:"validation_report_path,omitempty"`
	RenderManifestPath       string                              `json:"render_manifest_path,omitempty"`
	AssetTimelineCatalog     *model.AssetTimelineCatalog         `json:"asset_timeline_catalog,omitempty"`
	DemoEditPlan             *model.DemoEditPlan                 `json:"demo_edit_plan,omitempty"`
	ValidationReport         *model.DemoEditPlanValidationReport `json:"validation_report,omitempty"`
}

type Viewport struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}
