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

type MediaProbeRequest struct {
	Path string `json:"path"`
}

type MediaProbeResult struct {
	Path             string  `json:"path"`
	FileName         string  `json:"file_name"`
	SizeBytes        int64   `json:"size_bytes"`
	SHA256           string  `json:"sha256"`
	MimeType         string  `json:"mime_type"`
	Format           string  `json:"format,omitempty"`
	DurationMS       int     `json:"duration_ms,omitempty"`
	VideoCodec       string  `json:"video_codec,omitempty"`
	AudioCodec       string  `json:"audio_codec,omitempty"`
	Width            int     `json:"width,omitempty"`
	Height           int     `json:"height,omitempty"`
	FPS              float64 `json:"fps,omitempty"`
	PixelFormat      string  `json:"pixel_format,omitempty"`
	FFProbeAvailable bool    `json:"ffprobe_available"`
}

type AudioAnalysisRequest struct {
	Path               string  `json:"path"`
	BucketMS           int     `json:"bucket_ms,omitempty"`
	SilenceThresholdDB float64 `json:"silence_threshold_db,omitempty"`
	MinSilenceMS       int     `json:"min_silence_ms,omitempty"`
}

type AudioAnalysisResult struct {
	SchemaVersion      string    `json:"schema_version"`
	ArtifactID         string    `json:"artifact_id,omitempty"`
	Path               string    `json:"path"`
	HasAudio           bool      `json:"has_audio"`
	DurationMS         int       `json:"duration_ms"`
	BucketMS           int       `json:"bucket_ms"`
	SampleRateHZ       int       `json:"sample_rate_hz"`
	PeakDBFS           []float64 `json:"peak_dbfs"`
	RMSDBFS            []float64 `json:"rms_dbfs"`
	SilenceRangesMS    [][2]int  `json:"silence_ranges_ms"`
	SilenceThresholdDB float64   `json:"silence_threshold_db"`
	MinSilenceMS       int       `json:"min_silence_ms"`
	FFmpegAvailable    bool      `json:"ffmpeg_available"`
}

type EditPlanValidationRequest struct {
	Catalog  model.AssetTimelineCatalog `json:"catalog"`
	EditPlan model.DemoEditPlan         `json:"edit_plan"`
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
	RecordingRunSpec           *model.RecordingRunSpec       `json:"recording_run_spec,omitempty"`
	ExecutionTrace             *model.ExecutionTrace         `json:"execution_trace,omitempty"`
	ExecutionTracePath         string                        `json:"execution_trace_path,omitempty"`
	GeneratedAssets            []model.ArtifactRef           `json:"generated_assets,omitempty"`
	ArtifactManifestPath       string                        `json:"artifact_manifest_path,omitempty"`
	RecordingResultPackage     *model.RecordingResultPackage `json:"recording_result_package,omitempty"`
	RecordingResultPackagePath string                        `json:"recording_result_package_path,omitempty"`
	AssetTimelineCatalog       *model.AssetTimelineCatalog   `json:"asset_timeline_catalog,omitempty"`
	EditPlan                   *model.DemoEditPlan           `json:"edit_plan,omitempty"`
	RenderProfile              *model.EditorRenderProfile    `json:"render_profile,omitempty"`
}

type RenderResult struct {
	VideoPath                            string                                        `json:"video_path"`
	SourceReferenceVideoPath             string                                        `json:"source_reference_video_path,omitempty"`
	StepByStepDocsPath                   string                                        `json:"step_by_step_docs_path"`
	AssetTimelineCatalogPath             string                                        `json:"asset_timeline_catalog_path,omitempty"`
	DemoEditPlanPath                     string                                        `json:"demo_edit_plan_path,omitempty"`
	DirectorInputPath                    string                                        `json:"director_input_path,omitempty"`
	DirectorEditSuggestionPath           string                                        `json:"director_edit_suggestion_path,omitempty"`
	DirectorEditValidationPath           string                                        `json:"director_edit_suggestion_validation_path,omitempty"`
	DirectorEditPlanPatchPath            string                                        `json:"director_edit_plan_patch_path,omitempty"`
	DirectorEditPlanPatchValidationPath  string                                        `json:"director_edit_plan_patch_validation_path,omitempty"`
	DirectorEditPlanPatchApplyResultPath string                                        `json:"director_edit_plan_patch_apply_result_path,omitempty"`
	ArkMediaDryRunPlanPath               string                                        `json:"ark_media_dry_run_plan_path,omitempty"`
	ArkAssetPublicationPlanPath          string                                        `json:"ark_asset_publication_plan_path,omitempty"`
	ArkAssetPublicationResultPath        string                                        `json:"ark_asset_publication_result_path,omitempty"`
	ArkMediaGenerationResultPath         string                                        `json:"ark_media_generation_result_path,omitempty"`
	CandidateAssetReviewPath             string                                        `json:"candidate_asset_review_path,omitempty"`
	CandidateAssetEditPatchPath          string                                        `json:"candidate_asset_edit_patch_path,omitempty"`
	ValidationReportPath                 string                                        `json:"validation_report_path,omitempty"`
	RenderManifestPath                   string                                        `json:"render_manifest_path,omitempty"`
	MediaNormalizationReportPath         string                                        `json:"media_normalization_report_path,omitempty"`
	RequirementReportPath                string                                        `json:"requirement_satisfaction_report_path,omitempty"`
	AssetTimelineCatalog                 *model.AssetTimelineCatalog                   `json:"asset_timeline_catalog,omitempty"`
	DemoEditPlan                         *model.DemoEditPlan                           `json:"demo_edit_plan,omitempty"`
	DirectorInput                        *model.DirectorInput                          `json:"director_input,omitempty"`
	DirectorEditSuggestion               *model.DirectorEditSuggestion                 `json:"director_edit_suggestion,omitempty"`
	DirectorEditValidation               *model.DirectorEditSuggestionValidationReport `json:"director_edit_suggestion_validation,omitempty"`
	DirectorEditPlanPatch                *model.DirectorEditPlanPatch                  `json:"director_edit_plan_patch,omitempty"`
	DirectorEditPlanPatchValidation      *model.DirectorEditPlanPatchValidationReport  `json:"director_edit_plan_patch_validation,omitempty"`
	DirectorEditPlanPatchApplyResult     *model.DirectorEditPlanPatchApplyResult       `json:"director_edit_plan_patch_apply_result,omitempty"`
	ArkMediaDryRunPlan                   *model.ArkMediaDryRunPlan                     `json:"ark_media_dry_run_plan,omitempty"`
	ArkAssetPublicationPlan              *model.ArkAssetPublicationPlan                `json:"ark_asset_publication_plan,omitempty"`
	ArkAssetPublicationResult            *model.ArkAssetPublicationResult              `json:"ark_asset_publication_result,omitempty"`
	ArkMediaGenerationResult             *model.ArkMediaGenerationResult               `json:"ark_media_generation_result,omitempty"`
	CandidateAssetReview                 *model.CandidateAssetReview                   `json:"candidate_asset_review,omitempty"`
	CandidateAssetEditPatch              *model.CandidateAssetEditPlanPatch            `json:"candidate_asset_edit_patch,omitempty"`
	ValidationReport                     *model.DemoEditPlanValidationReport           `json:"validation_report,omitempty"`
}

type Viewport struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}
