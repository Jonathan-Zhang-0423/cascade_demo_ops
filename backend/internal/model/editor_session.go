package model

import "time"

const EditorSessionSchemaVersion = "demoops.editor_session.v1"

type EditorSessionMode string

const (
	EditorSessionModeDemoSafe EditorSessionMode = "demo_safe"
)

type EditorSessionStatus string

const (
	EditorSessionStatusEditing   EditorSessionStatus = "editing"
	EditorSessionStatusRendering EditorSessionStatus = "rendering"
	EditorSessionStatusReady     EditorSessionStatus = "ready"
	EditorSessionStatusFailed    EditorSessionStatus = "failed"
)

type EditorRenderStatus string

const (
	EditorRenderStatusNotStarted EditorRenderStatus = "not_started"
	EditorRenderStatusRunning    EditorRenderStatus = "running"
	EditorRenderStatusReady      EditorRenderStatus = "ready"
	EditorRenderStatusFailed     EditorRenderStatus = "failed"
	EditorRenderStatusCancelled  EditorRenderStatus = "cancelled"
)

type EditorRenderProfile struct {
	ID     string `json:"id,omitempty"`
	Mode   string `json:"mode"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	FPS    int    `json:"fps"`
	Format string `json:"format"`
	Preset string `json:"preset,omitempty"`
	CRF    int    `json:"crf,omitempty"`
}

type EditorRenderState struct {
	Status               EditorRenderStatus `json:"status"`
	JobID                string             `json:"job_id,omitempty"`
	Phase                string             `json:"phase,omitempty"`
	Progress             int                `json:"progress,omitempty"`
	CancelRequested      bool               `json:"cancel_requested,omitempty"`
	Revision             int                `json:"revision,omitempty"`
	StartedAt            time.Time          `json:"started_at,omitempty"`
	CompletedAt          time.Time          `json:"completed_at,omitempty"`
	VideoPath            string             `json:"video_path,omitempty"`
	RenderManifestPath   string             `json:"render_manifest_path,omitempty"`
	DeliveryManifestPath string             `json:"delivery_manifest_path,omitempty"`
	Error                string             `json:"error,omitempty"`
}

// EditorPresentationCapabilityProfile is the stable App-facing contract for
// optional generated presentation material. Provider selection, model IDs,
// endpoints and provider-native parameters intentionally stay server-side.
type EditorPresentationCapabilityProfile struct {
	Capability     string                               `json:"capability"`
	ProfileVersion string                               `json:"profile_version"`
	Available      bool                                 `json:"available"`
	Limits         EditorPresentationCapabilityLimits   `json:"limits"`
	Policies       EditorPresentationCapabilityPolicies `json:"policies"`
}

type EditorPresentationCapabilityLimits struct {
	MaxReferenceAssets              int      `json:"max_reference_assets"`
	MaxReferenceImages              int      `json:"max_reference_images"`
	MaxReferenceVideos              int      `json:"max_reference_videos"`
	MaxReferenceAudios              int      `json:"max_reference_audios"`
	ReferenceVideoMinSec            int      `json:"reference_video_min_sec"`
	ReferenceVideoMaxSec            int      `json:"reference_video_max_sec"`
	ReferenceVideoTotalMaxSec       int      `json:"reference_video_total_max_sec"`
	CandidateDurationMinSec         int      `json:"candidate_duration_min_sec"`
	CandidateDurationMaxSec         int      `json:"candidate_duration_max_sec"`
	RecommendedCandidateDurationSec int      `json:"recommended_candidate_duration_sec"`
	AcceptedImageFormats            []string `json:"accepted_image_formats"`
	AcceptedVideoFormats            []string `json:"accepted_video_formats"`
	MaxRequestBodyBytes             int64    `json:"max_request_body_bytes"`
	GeneratedAudioEnabled           bool     `json:"generated_audio_enabled"`
}

type EditorPresentationCapabilityPolicies struct {
	PresentationOnly               bool `json:"presentation_only"`
	RequiresExplicitReview         bool `json:"requires_explicit_review"`
	MayReplaceCapturedUI           bool `json:"may_replace_captured_ui"`
	FailureBlocksRecordingDelivery bool `json:"failure_blocks_recording_delivery"`
}

// EditorProviderCapability remains decode-only for v1 sessions written by
// older builds. New sessions and responses must leave it empty.
type EditorProviderCapability struct {
	Provider             string `json:"provider,omitempty"`
	Task                 string `json:"task,omitempty"`
	Mode                 string `json:"mode,omitempty"`
	Configured           bool   `json:"configured,omitempty"`
	Model                string `json:"model,omitempty"`
	OutputKind           string `json:"output_kind,omitempty"`
	AutoInclude          bool   `json:"auto_include,omitempty"`
	RequiresReview       bool   `json:"requires_review,omitempty"`
	PresentationOnly     bool   `json:"presentation_only,omitempty"`
	CanRepresentBusiness bool   `json:"can_represent_business_step,omitempty"`
}

type EditorAutomationSummary struct {
	SourcePackageID           string             `json:"source_package_id,omitempty"`
	ExecutionRuntime          string             `json:"execution_runtime,omitempty"`
	ValidationState           string             `json:"validation_state"`
	LatestDecision            ValidationDecision `json:"latest_decision,omitempty"`
	ValidationReportCount     int                `json:"validation_report_count"`
	EvidenceBackedReportCount int                `json:"evidence_backed_report_count"`
	PatchCount                int                `json:"patch_count"`
	AppliedPatchCount         int                `json:"applied_patch_count"`
	RolledBackPatchCount      int                `json:"rolled_back_patch_count"`
	StageEventAuditAvailable  bool               `json:"stage_event_audit_available"`
}

type EditorSession struct {
	SchemaVersion            string                                `json:"schema_version"`
	SessionID                string                                `json:"session_id"`
	Name                     string                                `json:"name"`
	Mode                     EditorSessionMode                     `json:"mode"`
	Status                   EditorSessionStatus                   `json:"status"`
	Revision                 int                                   `json:"revision"`
	CreatedAt                time.Time                             `json:"created_at"`
	UpdatedAt                time.Time                             `json:"updated_at"`
	AssetCatalog             AssetTimelineCatalog                  `json:"asset_catalog"`
	EditPlan                 DemoEditPlan                          `json:"edit_plan"`
	Validation               *DemoEditPlanValidationReport         `json:"validation,omitempty"`
	PreviewProfile           EditorRenderProfile                   `json:"preview_profile"`
	FinalProfile             EditorRenderProfile                   `json:"final_profile"`
	Preview                  EditorRenderState                     `json:"preview"`
	FinalRender              EditorRenderState                     `json:"final_render"`
	PresentationCapabilities []EditorPresentationCapabilityProfile `json:"presentation_capabilities,omitempty"`
	ProviderCapabilities     []EditorProviderCapability            `json:"provider_capabilities,omitempty"`
	Automation               *EditorAutomationSummary              `json:"automation,omitempty"`
}

type EditorCreateSessionRequest struct {
	Name       string `json:"name,omitempty"`
	SourcePath string `json:"source_path,omitempty"`
}

type EditorCreateFromResultPackageRequest struct {
	Name                string                  `json:"name,omitempty"`
	ResultPackagePath   string                  `json:"result_package_path,omitempty"`
	ResultPackage       *RecordingResultPackage `json:"result_package,omitempty"`
	RecordingArtifactID string                  `json:"recording_artifact_id,omitempty"`
	RecordingPath       string                  `json:"recording_path,omitempty"`
	ArtifactPaths       map[string]string       `json:"artifact_paths,omitempty"`
}

type EditorImportAssetRequest struct {
	Path  string `json:"path"`
	Label string `json:"label,omitempty"`
}

type EditorSavePlanRequest struct {
	ExpectedRevision int          `json:"expected_revision"`
	EditPlan         DemoEditPlan `json:"edit_plan"`
}

type EditorReviewPresentationCandidateRequest struct {
	ExpectedRevision int    `json:"expected_revision"`
	ArtifactID       string `json:"artifact_id"`
	Approved         bool   `json:"approved"`
}
