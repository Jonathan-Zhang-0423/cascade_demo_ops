package model

import "time"

const DirectorInputSchemaVersion = "demoops.director_input.v1"
const ArkMediaDryRunPlanSchemaVersion = "demoops.ark_media_dry_run_plan.v1"

type DirectorInput struct {
	SchemaVersion        string                       `json:"schema_version"`
	DirectorInputID      string                       `json:"director_input_id"`
	CreatedAt            time.Time                    `json:"created_at"`
	SourcePackageID      string                       `json:"source_package_id"`
	RecordingResultID    string                       `json:"recording_result_id,omitempty"`
	Project              DirectorProjectContext       `json:"project"`
	SourceAuthority      DemoEditSourceAuthority      `json:"source_authority"`
	ModelRole            DemoEditModelRole            `json:"model_role"`
	SourceMaterialPolicy DemoEditSourceMaterialPolicy `json:"source_material_policy"`
	StorylinePolicy      DirectorStorylinePolicy      `json:"storyline_policy"`
	ModelBoundaries      DirectorModelBoundaries      `json:"model_boundaries"`
	Workflow             DirectorWorkflowSummary      `json:"workflow"`
	Recording            DirectorRecordingSummary     `json:"recording"`
	Materials            DirectorMaterialSet          `json:"materials"`
	EditPlan             *DemoEditPlan                `json:"edit_plan,omitempty"`
	RequestedOutput      DirectorRequestedOutput      `json:"requested_output"`
	OpenQuestions        []string                     `json:"open_questions,omitempty"`
}

type DirectorProjectContext struct {
	OrgID          string   `json:"org_id,omitempty"`
	ProjectID      string   `json:"project_id,omitempty"`
	ProductURL     string   `json:"product_url,omitempty"`
	ProductName    string   `json:"product_name,omitempty"`
	TargetAudience string   `json:"target_audience,omitempty"`
	Goals          []string `json:"goals,omitempty"`
	UseCases       []string `json:"use_cases,omitempty"`
}

type DirectorStorylinePolicy struct {
	PrimaryStorylineSource string   `json:"primary_storyline_source"`
	PreserveStepOrder      bool     `json:"preserve_step_order"`
	RequiredStepOrder      []string `json:"required_step_order,omitempty"`
}

type DirectorModelBoundaries struct {
	CanDo    []string `json:"can_do"`
	CannotDo []string `json:"cannot_do"`
}

type DirectorWorkflowSummary struct {
	GraphID    string                 `json:"graph_id,omitempty"`
	Version    int                    `json:"version,omitempty"`
	EntryPoint string                 `json:"entry_point,omitempty"`
	Intent     *WorkflowIntent        `json:"intent,omitempty"`
	Nodes      []DirectorWorkflowStep `json:"nodes,omitempty"`
	Edges      []*GraphEdge           `json:"edges,omitempty"`
	Assets     *AssetManifest         `json:"assets,omitempty"`
}

type DirectorWorkflowStep struct {
	NodeID          string       `json:"node_id"`
	Order           int          `json:"order"`
	Action          string       `json:"action,omitempty"`
	Target          ActionTarget `json:"target,omitempty"`
	ExpectedOutcome string       `json:"expected_outcome,omitempty"`
	Required        bool         `json:"required"`
	DurationMS      int          `json:"duration_ms,omitempty"`
}

type DirectorRecordingSummary struct {
	RunID               string               `json:"run_id,omitempty"`
	BaseURL             string               `json:"base_url,omitempty"`
	TargetDurationSec   int                  `json:"target_duration_sec,omitempty"`
	MaxDurationSec      int                  `json:"max_duration_sec,omitempty"`
	OutputFormats       []string             `json:"output_formats,omitempty"`
	RequestedResolution *DirectorResolution  `json:"requested_resolution,omitempty"`
	CaptureWindows      []CaptureWindow      `json:"capture_windows,omitempty"`
	PassRate            float64              `json:"pass_rate,omitempty"`
	StepResults         []DirectorStepResult `json:"step_results,omitempty"`
}

type DirectorResolution struct {
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}

type DirectorStepResult struct {
	NodeID        string `json:"node_id"`
	Status        string `json:"status"`
	DurationMS    int    `json:"duration_ms,omitempty"`
	ObservedState string `json:"observed_state,omitempty"`
}

type DirectorMaterialSet struct {
	SourceReferenceVideo *DirectorMaterialRef  `json:"source_reference_video,omitempty"`
	FinalDemoVideo       *DirectorMaterialRef  `json:"final_demo_video,omitempty"`
	RawRecordings        []DirectorMaterialRef `json:"raw_recordings,omitempty"`
	Screenshots          []DirectorMaterialRef `json:"screenshots,omitempty"`
	MetadataArtifacts    []DirectorMaterialRef `json:"metadata_artifacts,omitempty"`
	AllArtifacts         []DirectorMaterialRef `json:"all_artifacts,omitempty"`
}

type DirectorMaterialRef struct {
	ID            string         `json:"id,omitempty"`
	Kind          string         `json:"kind"`
	URI           string         `json:"uri"`
	MimeType      string         `json:"mime_type,omitempty"`
	SHA256        string         `json:"sha256,omitempty"`
	SizeBytes     int64          `json:"size_bytes,omitempty"`
	SourceNodeID  string         `json:"source_node_id,omitempty"`
	AssetRole     string         `json:"asset_role,omitempty"`
	IncludeInDemo bool           `json:"include_in_demo,omitempty"`
	Sensitive     bool           `json:"sensitive,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

type DirectorRequestedOutput struct {
	PrimaryFormat       string `json:"primary_format"`
	ReferenceFormat     string `json:"reference_format"`
	PreferredAspect     string `json:"preferred_aspect,omitempty"`
	PreferredResolution string `json:"preferred_resolution,omitempty"`
}

type ArkMediaDryRunPlan struct {
	SchemaVersion          string              `json:"schema_version"`
	PlanID                 string              `json:"plan_id"`
	CreatedAt              time.Time           `json:"created_at"`
	Mode                   string              `json:"mode"`
	Reason                 string              `json:"reason"`
	SourcePackageID        string              `json:"source_package_id"`
	DirectorInputRef       DirectorMaterialRef `json:"director_input_ref"`
	VideoProvider          string              `json:"video_provider"`
	VideoBaseURL           string              `json:"video_base_url"`
	VideoModel             string              `json:"video_model"`
	ImageProvider          string              `json:"image_provider,omitempty"`
	ImageBaseURL           string              `json:"image_base_url,omitempty"`
	ImageModel             string              `json:"image_model,omitempty"`
	RecommendedTasks       []ArkMediaTaskSpec  `json:"recommended_tasks"`
	RequiredBeforeRealCall []string            `json:"required_before_real_call,omitempty"`
}

type ArkMediaTaskSpec struct {
	TaskID       string                `json:"task_id"`
	Kind         string                `json:"kind"`
	Endpoint     string                `json:"endpoint"`
	Method       string                `json:"method"`
	Model        string                `json:"model"`
	Purpose      string                `json:"purpose"`
	RequestBody  map[string]any        `json:"request_body"`
	InputRefs    []DirectorMaterialRef `json:"input_refs,omitempty"`
	OutputPolicy ArkMediaOutputPolicy  `json:"output_policy"`
}

type ArkMediaOutputPolicy struct {
	DownloadImmediately bool     `json:"download_immediately"`
	ExpectedFormats     []string `json:"expected_formats,omitempty"`
	URLTTLHours         int      `json:"url_ttl_hours,omitempty"`
	StoreAsArtifact     bool     `json:"store_as_artifact"`
}
