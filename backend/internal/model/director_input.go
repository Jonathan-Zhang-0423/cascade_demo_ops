package model

import "time"

const DirectorInputSchemaVersion = "demoops.director_input.v1"
const DirectorEditSuggestionSchemaVersion = "demoops.director_edit_suggestion.v1"
const DirectorEditSuggestionValidationSchemaVersion = "demoops.director_edit_suggestion_validation.v1"
const ArkMediaDryRunPlanSchemaVersion = "demoops.ark_media_dry_run_plan.v1"
const ArkAssetPublicationPlanSchemaVersion = "demoops.ark_asset_publication_plan.v1"
const ArkAssetPublicationResultSchemaVersion = "demoops.ark_asset_publication_result.v1"
const ArkMediaGenerationResultSchemaVersion = "demoops.ark_media_generation_result.v1"
const CandidateAssetReviewSchemaVersion = "demoops.candidate_asset_review.v1"
const CandidateAssetEditPlanPatchSchemaVersion = "demoops.candidate_asset_edit_plan_patch.v1"
const DirectorEditPlanPatchSchemaVersion = "demoops.director_edit_plan_patch.v1"
const DirectorEditPlanPatchValidationSchemaVersion = "demoops.director_edit_plan_patch_validation.v1"
const DirectorEditPlanPatchApplyResultSchemaVersion = "demoops.director_edit_plan_patch_apply_result.v1"

type DirectorInput struct {
	SchemaVersion        string                       `json:"schema_version"`
	DirectorInputID      string                       `json:"director_input_id"`
	CreatedAt            time.Time                    `json:"created_at"`
	SourcePackageID      string                       `json:"source_package_id"`
	RecordingResultID    string                       `json:"recording_result_id,omitempty"`
	Project              DirectorProjectContext       `json:"project"`
	UserIntent           DirectorUserIntent           `json:"user_intent"`
	DecisionPriority     DirectorDecisionPriority     `json:"decision_priority"`
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

type DirectorUserIntent struct {
	Status            string                    `json:"status"`
	InputMode         string                    `json:"input_mode"`
	HighestPriority   bool                      `json:"highest_priority"`
	RawPrompt         string                    `json:"raw_prompt,omitempty"`
	TargetAudience    string                    `json:"target_audience,omitempty"`
	TargetDurationSec int                       `json:"target_duration_sec,omitempty"`
	Requirements      []DirectorUserRequirement `json:"requirements,omitempty"`
	SourceTraces      []DirectorSourceTrace     `json:"source_traces,omitempty"`
}

type DirectorUserRequirement struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Text       string   `json:"text"`
	Required   bool     `json:"required"`
	Priority   int      `json:"priority"`
	Source     string   `json:"source"`
	SourcePath string   `json:"source_path,omitempty"`
	NodeRefs   []string `json:"node_refs,omitempty"`
}

type DirectorSourceTrace struct {
	Source     string `json:"source"`
	FieldPath  string `json:"field_path"`
	Confidence string `json:"confidence"`
}

type DirectorDecisionPriority struct {
	ResolutionOrder        []DirectorPriorityLayer `json:"resolution_order"`
	ConflictPolicy         string                  `json:"conflict_policy"`
	UserRequirementPolicy  string                  `json:"user_requirement_policy"`
	ClientScriptPolicy     string                  `json:"client_script_policy"`
	ModelSuggestionPolicy  string                  `json:"model_suggestion_policy"`
	RequiresSourceTracing  bool                    `json:"requires_source_tracing"`
	RequiresSatisfactionQA bool                    `json:"requires_satisfaction_qa"`
}

type DirectorPriorityLayer struct {
	Rank        int    `json:"rank"`
	Source      string `json:"source"`
	Authority   string `json:"authority"`
	Description string `json:"description"`
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
	NodeID          string                           `json:"node_id"`
	Order           int                              `json:"order"`
	Action          string                           `json:"action,omitempty"`
	Title           string                           `json:"title,omitempty"`
	Goal            string                           `json:"goal,omitempty"`
	Target          ActionTarget                     `json:"target,omitempty"`
	ExpectedOutcome string                           `json:"expected_outcome,omitempty"`
	Narrative       *NarrativeCue                    `json:"narrative,omitempty"`
	Capture         *CaptureSpec                     `json:"capture,omitempty"`
	Timing          *NodeTimingHint                  `json:"timing,omitempty"`
	CaptureWindow   *CaptureWindow                   `json:"capture_window,omitempty"`
	Verification    *DirectorInteractionVerification `json:"verification,omitempty"`
	Required        bool                             `json:"required"`
	DurationMS      int                              `json:"duration_ms,omitempty"`
	EvidenceRefs    []EvidenceRef                    `json:"evidence_refs,omitempty"`
	Tags            []string                         `json:"tags,omitempty"`
}

type DirectorRecordingSummary struct {
	RunID               string               `json:"run_id,omitempty"`
	BaseURL             string               `json:"base_url,omitempty"`
	TargetDurationSec   int                  `json:"target_duration_sec,omitempty"`
	MaxDurationSec      int                  `json:"max_duration_sec,omitempty"`
	OutputFormats       []string             `json:"output_formats,omitempty"`
	RequestedResolution *DirectorResolution  `json:"requested_resolution,omitempty"`
	BrowserViewports    []ViewportSpec       `json:"browser_viewports,omitempty"`
	CaptureWindows      []CaptureWindow      `json:"capture_windows,omitempty"`
	NodeTimingHints     []NodeTimingHint     `json:"node_timing_hints,omitempty"`
	PassRate            float64              `json:"pass_rate,omitempty"`
	StepResults         []DirectorStepResult `json:"step_results,omitempty"`
}

type DirectorInteractionVerification struct {
	Status                string `json:"status,omitempty"`
	Source                string `json:"source,omitempty"`
	VerifiedInteractionID string `json:"verified_interaction_id,omitempty"`
	IntentGoalID          string `json:"intent_goal_id,omitempty"`
	RuntimeAdaptive       bool   `json:"runtime_adaptive,omitempty"`
	SelectorScore         int    `json:"selector_score,omitempty"`
	Authority             string `json:"authority"`
	Guidance              string `json:"guidance"`
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

type DirectorEditSuggestion struct {
	SchemaVersion        string                        `json:"schema_version"`
	SuggestionID         string                        `json:"suggestion_id"`
	CreatedAt            time.Time                     `json:"created_at"`
	SourcePackageID      string                        `json:"source_package_id"`
	DirectorInputID      string                        `json:"director_input_id"`
	Mode                 string                        `json:"mode"`
	Status               string                        `json:"status"`
	Adapter              DirectorAdapterInfo           `json:"adapter"`
	DecisionPriority     DirectorDecisionPriority      `json:"decision_priority"`
	SourceMaterialPolicy DemoEditSourceMaterialPolicy  `json:"source_material_policy"`
	ModelRole            DemoEditModelRole             `json:"model_role"`
	Summary              string                        `json:"summary"`
	RequirementHandling  []DirectorRequirementHandling `json:"requirement_handling,omitempty"`
	Narrative            []DirectorNarrativeBeat       `json:"narrative,omitempty"`
	ShotSuggestions      []DirectorShotSuggestion      `json:"shot_suggestions,omitempty"`
	CaptionSuggestions   []DirectorCaptionSuggestion   `json:"caption_suggestions,omitempty"`
	Style                DirectorStyleSuggestion       `json:"style"`
	Music                DirectorMusicSuggestion       `json:"music"`
	ProviderGate         *DirectorProviderGate         `json:"provider_gate,omitempty"`
	ProviderCall         *DirectorProviderCall         `json:"provider_call,omitempty"`
	SourceTraces         []DirectorSourceTrace         `json:"source_traces,omitempty"`
	Notes                []string                      `json:"notes,omitempty"`
}

type DirectorAdapterInfo struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	Provider     string `json:"provider"`
	Model        string `json:"model,omitempty"`
	RealCallMade bool   `json:"real_call_made"`
}

type DirectorProviderGate struct {
	Mode                   string                     `json:"mode"`
	Status                 string                     `json:"status"`
	Provider               string                     `json:"provider,omitempty"`
	Model                  string                     `json:"model,omitempty"`
	CanAttemptRealCall     bool                       `json:"can_attempt_real_call"`
	RealCallMade           bool                       `json:"real_call_made"`
	ModeGate               string                     `json:"mode_gate,omitempty"`
	RequiredBeforeRealCall []string                   `json:"required_before_real_call,omitempty"`
	Blockers               []ArkMediaReadinessFinding `json:"blockers,omitempty"`
	Warnings               []ArkMediaReadinessFinding `json:"warnings,omitempty"`
}

type DirectorProviderCall struct {
	Operation        string                         `json:"operation"`
	Status           string                         `json:"status"`
	Provider         string                         `json:"provider,omitempty"`
	Model            string                         `json:"model,omitempty"`
	TaskID           string                         `json:"task_id,omitempty"`
	ProviderStatus   string                         `json:"provider_status,omitempty"`
	RealCallMade     bool                           `json:"real_call_made"`
	RequestSummary   DirectorProviderRequestSummary `json:"request_summary"`
	Trace            DirectorProviderCallTrace      `json:"trace"`
	Output           map[string]any                 `json:"output,omitempty"`
	ErrorClass       string                         `json:"error_class,omitempty"`
	ErrorMessage     string                         `json:"error_message,omitempty"`
	NonAuthoritative bool                           `json:"non_authoritative"`
}

type DirectorProviderRequestSummary struct {
	ContentPartCount int      `json:"content_part_count"`
	ReferenceURIs    []string `json:"reference_uris,omitempty"`
	Resolution       string   `json:"resolution,omitempty"`
	Ratio            string   `json:"ratio,omitempty"`
	DurationSec      int      `json:"duration_sec,omitempty"`
	GenerateAudio    bool     `json:"generate_audio"`
	Watermark        bool     `json:"watermark"`
}

type DirectorProviderCallTrace struct {
	Method       string `json:"method,omitempty"`
	EndpointHost string `json:"endpoint_host,omitempty"`
	EndpointPath string `json:"endpoint_path,omitempty"`
	HTTPStatus   int    `json:"http_status,omitempty"`
	ErrorClass   string `json:"error_class,omitempty"`
	LatencyMS    int    `json:"latency_ms,omitempty"`
}

type DirectorRequirementHandling struct {
	RequirementID   string                `json:"requirement_id"`
	RequirementKind string                `json:"requirement_kind"`
	Status          string                `json:"status"`
	Handling        string                `json:"handling"`
	PrioritySource  string                `json:"priority_source"`
	SourceTrace     []DirectorSourceTrace `json:"source_trace,omitempty"`
}

type DirectorNarrativeBeat struct {
	ID               string                `json:"id"`
	Title            string                `json:"title,omitempty"`
	Purpose          string                `json:"purpose"`
	RelatedStepIDs   []string              `json:"related_step_ids,omitempty"`
	TargetDurationMS int                   `json:"target_duration_ms,omitempty"`
	PrioritySource   string                `json:"priority_source"`
	SourceTrace      []DirectorSourceTrace `json:"source_trace,omitempty"`
}

type DirectorShotSuggestion struct {
	ID                string                `json:"id"`
	SourceStepID      string                `json:"source_step_id,omitempty"`
	SourceArtifactID  string                `json:"source_artifact_id,omitempty"`
	SourceTimeRangeMS []int                 `json:"source_time_range_ms,omitempty"`
	Operation         string                `json:"operation"`
	Purpose           string                `json:"purpose"`
	PrioritySource    string                `json:"priority_source"`
	SourceTrace       []DirectorSourceTrace `json:"source_trace,omitempty"`
}

type DirectorCaptionSuggestion struct {
	ID             string                `json:"id"`
	Text           string                `json:"text"`
	AnchorStepID   string                `json:"anchor_step_id,omitempty"`
	PrioritySource string                `json:"priority_source"`
	SourceTrace    []DirectorSourceTrace `json:"source_trace,omitempty"`
}

type DirectorStyleSuggestion struct {
	Pacing          string                `json:"pacing,omitempty"`
	TransitionStyle string                `json:"transition_style,omitempty"`
	ColorGrade      string                `json:"color_grade,omitempty"`
	MotionStyle     string                `json:"motion_style,omitempty"`
	SourceTrace     []DirectorSourceTrace `json:"source_trace,omitempty"`
}

type DirectorMusicSuggestion struct {
	Direction      string                `json:"direction,omitempty"`
	GenerateAudio  bool                  `json:"generate_audio"`
	PrioritySource string                `json:"priority_source"`
	SourceTrace    []DirectorSourceTrace `json:"source_trace,omitempty"`
}

type DirectorEditSuggestionValidationReport struct {
	SchemaVersion string                      `json:"schema_version"`
	SuggestionID  string                      `json:"suggestion_id"`
	Valid         bool                        `json:"valid"`
	CheckedAt     time.Time                   `json:"checked_at"`
	Errors        []DirectorSuggestionFinding `json:"errors,omitempty"`
	Warnings      []DirectorSuggestionFinding `json:"warnings,omitempty"`
	Policies      []string                    `json:"policies,omitempty"`
}

type DirectorSuggestionFinding struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Path     string `json:"path,omitempty"`
	Severity string `json:"severity"`
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
	SchemaVersion       string                      `json:"schema_version"`
	PlanID              string                      `json:"plan_id"`
	CreatedAt           time.Time                   `json:"created_at"`
	Mode                string                      `json:"mode"`
	SourcePackageID     string                      `json:"source_package_id"`
	ArkMediaDryRunRef   DirectorMaterialRef         `json:"ark_media_dry_run_ref"`
	Status              string                      `json:"status"`
	PublicationStrategy string                      `json:"publication_strategy"`
	URLTTLHours         int                         `json:"url_ttl_hours"`
	TOSRetention        MediaTOSRetentionPreference `json:"tos_retention,omitempty"`
	Items               []ArkAssetPublicationItem   `json:"items,omitempty"`
	Blockers            []ArkMediaReadinessFinding  `json:"blockers,omitempty"`
	Warnings            []ArkMediaReadinessFinding  `json:"warnings,omitempty"`
	Notes               []string                    `json:"notes,omitempty"`
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
	TOSRetention       MediaTOSRetentionPreference     `json:"tos_retention,omitempty"`
	DeleteAfter        time.Time                       `json:"delete_after,omitempty"`
	Notes              []string                        `json:"notes,omitempty"`
}

type ArkAssetPublicationResultItem struct {
	SourceRef          DirectorMaterialRef  `json:"source_ref"`
	ProposedPublicRef  *DirectorMaterialRef `json:"proposed_public_ref,omitempty"`
	TaskIDs            []string             `json:"task_ids,omitempty"`
	Usage              string               `json:"usage,omitempty"`
	Required           bool                 `json:"required"`
	Status             string               `json:"status"`
	Published          bool                 `json:"published"`
	DryRun             bool                 `json:"dry_run"`
	CanUseForRealCall  bool                 `json:"can_use_for_real_call"`
	ActionRequired     string               `json:"action_required,omitempty"`
	FailureStage       string               `json:"failure_stage,omitempty"`
	ErrorClass         string               `json:"error_class,omitempty"`
	ProviderHTTPStatus int                  `json:"provider_http_status,omitempty"`
}

type ArkMediaGenerationResult struct {
	SchemaVersion       string                        `json:"schema_version"`
	ResultID            string                        `json:"result_id"`
	CreatedAt           time.Time                     `json:"created_at"`
	SourcePackageID     string                        `json:"source_package_id"`
	DirectorInputID     string                        `json:"director_input_id,omitempty"`
	SuggestionID        string                        `json:"suggestion_id,omitempty"`
	Status              string                        `json:"status"`
	Provider            string                        `json:"provider,omitempty"`
	Model               string                        `json:"model,omitempty"`
	TaskID              string                        `json:"task_id,omitempty"`
	ProviderStatus      string                        `json:"provider_status,omitempty"`
	NonAuthoritative    bool                          `json:"non_authoritative"`
	CandidateArtifacts  []ArtifactRef                 `json:"candidate_artifacts,omitempty"`
	DownloadedArtifacts []ArtifactRef                 `json:"downloaded_artifacts,omitempty"`
	PollAttempts        []ArkMediaProviderPollAttempt `json:"poll_attempts,omitempty"`
	ProviderCall        *DirectorProviderCall         `json:"provider_call,omitempty"`
	Warnings            []ArkMediaReadinessFinding    `json:"warnings,omitempty"`
	Notes               []string                      `json:"notes,omitempty"`
}

type ArkMediaProviderPollAttempt struct {
	Attempt           int                       `json:"attempt"`
	CheckedAt         time.Time                 `json:"checked_at"`
	Status            string                    `json:"status"`
	ProviderStatus    string                    `json:"provider_status,omitempty"`
	CandidateURLCount int                       `json:"candidate_url_count"`
	Trace             DirectorProviderCallTrace `json:"trace,omitempty"`
	ErrorClass        string                    `json:"error_class,omitempty"`
	ErrorMessage      string                    `json:"error_message,omitempty"`
}

type CandidateAssetReview struct {
	SchemaVersion          string                     `json:"schema_version"`
	ReviewID               string                     `json:"review_id"`
	CreatedAt              time.Time                  `json:"created_at"`
	SourcePackageID        string                     `json:"source_package_id,omitempty"`
	GenerationResultID     string                     `json:"generation_result_id,omitempty"`
	Status                 string                     `json:"status"`
	Policy                 CandidateAssetReviewPolicy `json:"policy"`
	Items                  []CandidateAssetReviewItem `json:"items,omitempty"`
	ApprovedArtifacts      []ArtifactRef              `json:"approved_artifacts,omitempty"`
	PendingReviewArtifacts []ArtifactRef              `json:"pending_review_artifacts,omitempty"`
	RejectedArtifacts      []ArtifactRef              `json:"rejected_artifacts,omitempty"`
	Warnings               []ArkMediaReadinessFinding `json:"warnings,omitempty"`
	Notes                  []string                   `json:"notes,omitempty"`
}

type CandidateAssetReviewPolicy struct {
	DecisionMode               string   `json:"decision_mode"`
	SourceMaterialPolicy       string   `json:"source_material_policy"`
	AllowedKinds               []string `json:"allowed_kinds"`
	RequiresLocalFile          bool     `json:"requires_local_file"`
	RequiresNonAuthoritative   bool     `json:"requires_non_authoritative"`
	RequiresPresentationOnly   bool     `json:"requires_presentation_only"`
	AutoIncludeInDemo          bool     `json:"auto_include_in_demo"`
	RequiresExplicitUserReview bool     `json:"requires_explicit_user_review"`
}

type CandidateAssetReviewItem struct {
	ArtifactID             string                     `json:"artifact_id"`
	Kind                   string                     `json:"kind"`
	URI                    string                     `json:"uri"`
	MimeType               string                     `json:"mime_type,omitempty"`
	Status                 string                     `json:"status"`
	ApprovedForDemo        bool                       `json:"approved_for_demo"`
	MediaEligible          bool                       `json:"media_eligible"`
	ExplicitReviewRequired bool                       `json:"explicit_review_required"`
	IncludeInDemo          bool                       `json:"include_in_demo"`
	PresentationOnly       bool                       `json:"presentation_only"`
	Reasons                []string                   `json:"reasons,omitempty"`
	Risks                  []string                   `json:"risks,omitempty"`
	Findings               []ArkMediaReadinessFinding `json:"findings,omitempty"`
	ApprovedMetadata       map[string]any             `json:"approved_metadata,omitempty"`
}

type CandidateAssetEditPlanPatch struct {
	SchemaVersion       string                        `json:"schema_version"`
	PatchID             string                        `json:"patch_id"`
	CreatedAt           time.Time                     `json:"created_at"`
	SourcePackageID     string                        `json:"source_package_id,omitempty"`
	BasePlanID          string                        `json:"base_plan_id,omitempty"`
	ReviewID            string                        `json:"review_id,omitempty"`
	Status              string                        `json:"status"`
	ApplicationMode     string                        `json:"application_mode"`
	Policy              CandidateAssetEditPatchPolicy `json:"policy"`
	ApprovedArtifactIDs []string                      `json:"approved_artifact_ids,omitempty"`
	RejectedArtifactIDs []string                      `json:"rejected_artifact_ids,omitempty"`
	ProposedShots       []DemoEditShot                `json:"proposed_shots,omitempty"`
	Warnings            []ArkMediaReadinessFinding    `json:"warnings,omitempty"`
	Notes               []string                      `json:"notes,omitempty"`
}

type CandidateAssetEditPatchPolicy struct {
	AutoApply                  bool     `json:"auto_apply"`
	RequiresExplicitOptIn      bool     `json:"requires_explicit_opt_in"`
	RequiresRendererValidation bool     `json:"requires_renderer_validation"`
	PreserveRequiredStepOrder  bool     `json:"preserve_required_step_order"`
	PresentationOnly           bool     `json:"presentation_only"`
	NonAuthoritativeOnly       bool     `json:"non_authoritative_only"`
	MustNotBindSourceStep      bool     `json:"must_not_bind_source_step"`
	AllowedPlacements          []string `json:"allowed_placements"`
}

type DirectorEditPlanPatch struct {
	SchemaVersion     string                      `json:"schema_version"`
	PatchID           string                      `json:"patch_id"`
	CreatedAt         time.Time                   `json:"created_at"`
	SourcePackageID   string                      `json:"source_package_id,omitempty"`
	DirectorInputID   string                      `json:"director_input_id,omitempty"`
	SuggestionID      string                      `json:"suggestion_id,omitempty"`
	BasePlanID        string                      `json:"base_plan_id,omitempty"`
	Status            string                      `json:"status"`
	ApplicationMode   string                      `json:"application_mode"`
	Policy            DirectorEditPlanPatchPolicy `json:"policy"`
	ShotPatches       []DirectorEditPlanShotPatch `json:"shot_patches,omitempty"`
	GlobalStylePatch  *DemoEditGlobalStyle        `json:"global_style_patch,omitempty"`
	RequirementRefs   []string                    `json:"requirement_refs,omitempty"`
	SkippedSuggestion []DirectorEditPlanPatchSkip `json:"skipped_suggestions,omitempty"`
	Warnings          []ArkMediaReadinessFinding  `json:"warnings,omitempty"`
	Notes             []string                    `json:"notes,omitempty"`
}

type DirectorEditPlanPatchPolicy struct {
	AutoApply                                  bool     `json:"auto_apply"`
	RequiresValidation                         bool     `json:"requires_validation"`
	RequiresRendererValidation                 bool     `json:"requires_renderer_validation"`
	ExistingAssetsOnly                         bool     `json:"existing_assets_only"`
	PreserveRequiredStepOrder                  bool     `json:"preserve_required_step_order"`
	PreserveLockedSourceFields                 bool     `json:"preserve_locked_source_fields"`
	RuntimeAdaptiveRequiresCaptureConfirmation bool     `json:"runtime_adaptive_requires_capture_confirmation"`
	AllowedEditableFields                      []string `json:"allowed_editable_fields"`
	LockedFields                               []string `json:"locked_fields"`
}

type DirectorEditPlanShotPatch struct {
	ShotID            string                `json:"shot_id"`
	SourceStepID      string                `json:"source_step_id,omitempty"`
	SourceArtifactID  string                `json:"source_artifact_id"`
	SourceTimeRangeMS *MillisecondRange     `json:"source_time_range_ms,omitempty"`
	ProposedPurpose   string                `json:"proposed_purpose,omitempty"`
	AddOperations     []EditOperation       `json:"add_operations,omitempty"`
	AddOverlays       []EditOverlay         `json:"add_overlays,omitempty"`
	PrioritySource    string                `json:"priority_source"`
	SourceTrace       []DirectorSourceTrace `json:"source_trace,omitempty"`
	RuntimeAdaptive   bool                  `json:"runtime_adaptive,omitempty"`
}

type DirectorEditPlanPatchSkip struct {
	Kind    string `json:"kind"`
	RefID   string `json:"ref_id,omitempty"`
	Reason  string `json:"reason"`
	Field   string `json:"field,omitempty"`
	Blocked bool   `json:"blocked,omitempty"`
}

type DirectorEditPlanPatchValidationReport struct {
	SchemaVersion string                          `json:"schema_version"`
	PatchID       string                          `json:"patch_id"`
	BasePlanID    string                          `json:"base_plan_id,omitempty"`
	Valid         bool                            `json:"valid"`
	CheckedAt     time.Time                       `json:"checked_at"`
	Policies      []string                        `json:"policies,omitempty"`
	Errors        []DemoEditPlanValidationFinding `json:"errors,omitempty"`
	Warnings      []DemoEditPlanValidationFinding `json:"warnings,omitempty"`
}

type DirectorEditPlanPatchApplyResult struct {
	SchemaVersion      string                           `json:"schema_version"`
	ResultID           string                           `json:"result_id"`
	CreatedAt          time.Time                        `json:"created_at"`
	PatchID            string                           `json:"patch_id,omitempty"`
	BasePlanID         string                           `json:"base_plan_id,omitempty"`
	OutputPlanID       string                           `json:"output_plan_id,omitempty"`
	Status             string                           `json:"status"`
	Applied            bool                             `json:"applied"`
	RerenderRequested  bool                             `json:"rerender_requested"`
	Rerendered         bool                             `json:"rerendered"`
	AppliedShotIDs     []string                         `json:"applied_shot_ids,omitempty"`
	SkippedShotIDs     []string                         `json:"skipped_shot_ids,omitempty"`
	OutputVideoPath    string                           `json:"output_video_path,omitempty"`
	OutputPlanPath     string                           `json:"output_plan_path,omitempty"`
	RenderManifestPath string                           `json:"render_manifest_path,omitempty"`
	RenderVerification *DirectorPatchRenderVerification `json:"render_verification,omitempty"`
	Warnings           []DemoEditPlanValidationFinding  `json:"warnings,omitempty"`
	Errors             []DemoEditPlanValidationFinding  `json:"errors,omitempty"`
	Notes              []string                         `json:"notes,omitempty"`
}

type DirectorPatchRenderVerification struct {
	Status                  string                          `json:"status"`
	Method                  string                          `json:"method,omitempty"`
	QualityStatus           string                          `json:"quality_status,omitempty"`
	FFMpegAvailable         *bool                           `json:"ffmpeg_available,omitempty"`
	FallbackReason          string                          `json:"fallback_reason,omitempty"`
	RequirementReportStatus string                          `json:"requirement_report_status,omitempty"`
	PlannedOperations       []string                        `json:"planned_operations,omitempty"`
	AppliedOperations       []string                        `json:"applied_operations,omitempty"`
	SkippedOperations       []DirectorPatchSkippedOperation `json:"skipped_operations,omitempty"`
	PendingOperationTypes   []string                        `json:"pending_operation_types,omitempty"`
}

type DirectorPatchSkippedOperation struct {
	Type   string `json:"type"`
	Reason string `json:"reason,omitempty"`
}
