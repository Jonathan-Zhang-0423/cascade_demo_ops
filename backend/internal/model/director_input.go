package model

import "time"

const DirectorInputSchemaVersion = "demoops.director_input.v1"
const ArkMediaDryRunPlanSchemaVersion = "demoops.ark_media_dry_run_plan.v1"
const ArkAssetPublicationPlanSchemaVersion = "demoops.ark_asset_publication_plan.v1"
const ArkAssetPublicationResultSchemaVersion = "demoops.ark_asset_publication_result.v1"

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
	SchemaVersion           string                           `json:"schema_version"`
	PlanID                  string                           `json:"plan_id"`
	CreatedAt               time.Time                        `json:"created_at"`
	Mode                    string                           `json:"mode"`
	Reason                  string                           `json:"reason"`
	SourcePackageID         string                           `json:"source_package_id"`
	DirectorInputRef        DirectorMaterialRef              `json:"director_input_ref"`
	VideoProvider           string                           `json:"video_provider"`
	VideoBaseURL            string                           `json:"video_base_url"`
	VideoModel              string                           `json:"video_model"`
	ImageProvider           string                           `json:"image_provider,omitempty"`
	ImageBaseURL            string                           `json:"image_base_url,omitempty"`
	ImageModel              string                           `json:"image_model,omitempty"`
	RecommendedTasks        []ArkMediaTaskSpec               `json:"recommended_tasks"`
	ProviderConstraints     ArkMediaConstraints              `json:"provider_constraints"`
	SourceAssetRequirements []ArkMediaSourceAssetRequirement `json:"source_asset_requirements,omitempty"`
	OutputHandling          ArkMediaOutputHandling           `json:"output_handling"`
	RealCallReadiness       ArkMediaRealCallReadiness        `json:"real_call_readiness"`
	RequiredBeforeRealCall  []string                         `json:"required_before_real_call,omitempty"`
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

type ArkMediaConstraints struct {
	Seedance ArkMediaVideoConstraints `json:"seedance"`
	Seedream ArkMediaImageConstraints `json:"seedream"`
}

type ArkMediaVideoConstraints struct {
	Model                      string   `json:"model"`
	MinDurationSec             int      `json:"min_duration_sec"`
	MaxDurationSec             int      `json:"max_duration_sec"`
	MaxReferenceVideoTotalSec  int      `json:"max_reference_video_total_sec"`
	MaxReferenceInputCount     int      `json:"max_reference_input_count"`
	AcceptedVideoMimeTypes     []string `json:"accepted_video_mime_types"`
	AcceptedImageMimeTypes     []string `json:"accepted_image_mime_types"`
	RequiresPublicHTTPAssets   bool     `json:"requires_public_http_assets"`
	AllowsAudioGeneration      bool     `json:"allows_audio_generation"`
	NonAuthoritativeOutputOnly bool     `json:"non_authoritative_output_only"`
}

type ArkMediaImageConstraints struct {
	Model                      string   `json:"model"`
	AcceptedOutputFormats      []string `json:"accepted_output_formats"`
	RequiresPublicHTTPAssets   bool     `json:"requires_public_http_assets"`
	NonProductVisualsOnly      bool     `json:"non_product_visuals_only"`
	NonAuthoritativeOutputOnly bool     `json:"non_authoritative_output_only"`
}

type ArkMediaSourceAssetRequirement struct {
	Ref                DirectorMaterialRef `json:"ref"`
	TaskID             string              `json:"task_id"`
	Usage              string              `json:"usage"`
	Required           bool                `json:"required"`
	AcceptedMimeTypes  []string            `json:"accepted_mime_types,omitempty"`
	RequiresPublicURI  bool                `json:"requires_public_uri"`
	CurrentURIIsPublic bool                `json:"current_uri_is_public"`
	Status             string              `json:"status"`
	ActionRequired     string              `json:"action_required,omitempty"`
}

type ArkMediaExpectedOutput struct {
	TaskID        string   `json:"task_id"`
	Kind          string   `json:"kind"`
	Role          string   `json:"role"`
	MimeTypes     []string `json:"mime_types,omitempty"`
	IncludeInDemo bool     `json:"include_in_demo"`
}

type ArkMediaOutputHandling struct {
	DownloadWithinHours            int                      `json:"download_within_hours"`
	StoreAsArtifact                bool                     `json:"store_as_artifact"`
	RecordRedactedProviderMetadata bool                     `json:"record_redacted_provider_metadata"`
	NonAuthoritativeOutputOnly     bool                     `json:"non_authoritative_output_only"`
	MustNotReplaceCapturedUI       bool                     `json:"must_not_replace_captured_ui"`
	ExpectedOutputs                []ArkMediaExpectedOutput `json:"expected_outputs,omitempty"`
}

type ArkMediaRealCallReadiness struct {
	Status             string                     `json:"status"`
	CanCallNow         bool                       `json:"can_call_now"`
	CanCallWhenEnabled bool                       `json:"can_call_when_enabled"`
	ModeGate           string                     `json:"mode_gate,omitempty"`
	Blockers           []ArkMediaReadinessFinding `json:"blockers,omitempty"`
	Warnings           []ArkMediaReadinessFinding `json:"warnings,omitempty"`
}

type ArkMediaReadinessFinding struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	RefID   string `json:"ref_id,omitempty"`
	TaskID  string `json:"task_id,omitempty"`
}

type ArkAssetPublicationPlan struct {
	SchemaVersion       string                     `json:"schema_version"`
	PlanID              string                     `json:"plan_id"`
	CreatedAt           time.Time                  `json:"created_at"`
	Mode                string                     `json:"mode"`
	SourcePackageID     string                     `json:"source_package_id"`
	ArkMediaDryRunRef   DirectorMaterialRef        `json:"ark_media_dry_run_ref"`
	Status              string                     `json:"status"`
	PublicationStrategy string                     `json:"publication_strategy"`
	URLTTLHours         int                        `json:"url_ttl_hours"`
	Items               []ArkAssetPublicationItem  `json:"items,omitempty"`
	Blockers            []ArkMediaReadinessFinding `json:"blockers,omitempty"`
	Warnings            []ArkMediaReadinessFinding `json:"warnings,omitempty"`
	Notes               []string                   `json:"notes,omitempty"`
}

type ArkAssetPublicationItem struct {
	Ref                 DirectorMaterialRef `json:"ref"`
	TaskIDs             []string            `json:"task_ids,omitempty"`
	Usage               string              `json:"usage"`
	Required            bool                `json:"required"`
	AcceptedMimeTypes   []string            `json:"accepted_mime_types,omitempty"`
	CurrentURIIsPublic  bool                `json:"current_uri_is_public"`
	NeedsPublication    bool                `json:"needs_publication"`
	NeedsConversion     bool                `json:"needs_conversion"`
	RecommendedFileName string              `json:"recommended_file_name,omitempty"`
	ExpectedPublicURI   string              `json:"expected_public_uri,omitempty"`
	Status              string              `json:"status"`
	ActionRequired      string              `json:"action_required,omitempty"`
}

type ArkAssetPublicationResult struct {
	SchemaVersion      string                          `json:"schema_version"`
	ResultID           string                          `json:"result_id"`
	CreatedAt          time.Time                       `json:"created_at"`
	Mode               string                          `json:"mode"`
	Publisher          string                          `json:"publisher"`
	SourcePackageID    string                          `json:"source_package_id"`
	PublicationPlanRef DirectorMaterialRef             `json:"publication_plan_ref"`
	Status             string                          `json:"status"`
	CanUseForRealCall  bool                            `json:"can_use_for_real_call"`
	ContainsDryRunRefs bool                            `json:"contains_dry_run_refs"`
	Items              []ArkAssetPublicationResultItem `json:"items,omitempty"`
	Blockers           []ArkMediaReadinessFinding      `json:"blockers,omitempty"`
	Warnings           []ArkMediaReadinessFinding      `json:"warnings,omitempty"`
	Notes              []string                        `json:"notes,omitempty"`
}

type ArkAssetPublicationResultItem struct {
	SourceRef         DirectorMaterialRef  `json:"source_ref"`
	ProposedPublicRef *DirectorMaterialRef `json:"proposed_public_ref,omitempty"`
	TaskIDs           []string             `json:"task_ids,omitempty"`
	Usage             string               `json:"usage,omitempty"`
	Required          bool                 `json:"required"`
	Status            string               `json:"status"`
	Published         bool                 `json:"published"`
	DryRun            bool                 `json:"dry_run"`
	CanUseForRealCall bool                 `json:"can_use_for_real_call"`
	ActionRequired    string               `json:"action_required,omitempty"`
}
