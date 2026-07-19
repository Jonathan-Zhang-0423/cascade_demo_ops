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
	Mode   string `json:"mode"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	FPS    int    `json:"fps"`
	Format string `json:"format"`
	Preset string `json:"preset,omitempty"`
	CRF    int    `json:"crf,omitempty"`
}

type EditorRenderState struct {
	Status             EditorRenderStatus `json:"status"`
	JobID              string             `json:"job_id,omitempty"`
	Phase              string             `json:"phase,omitempty"`
	Progress           int                `json:"progress,omitempty"`
	CancelRequested    bool               `json:"cancel_requested,omitempty"`
	Revision           int                `json:"revision,omitempty"`
	StartedAt          time.Time          `json:"started_at,omitempty"`
	CompletedAt        time.Time          `json:"completed_at,omitempty"`
	VideoPath          string             `json:"video_path,omitempty"`
	RenderManifestPath string             `json:"render_manifest_path,omitempty"`
	Error              string             `json:"error,omitempty"`
}

type EditorProviderCapability struct {
	Provider             string `json:"provider"`
	Task                 string `json:"task"`
	Mode                 string `json:"mode"`
	Configured           bool   `json:"configured"`
	Model                string `json:"model,omitempty"`
	OutputKind           string `json:"output_kind"`
	AutoInclude          bool   `json:"auto_include"`
	RequiresReview       bool   `json:"requires_review"`
	PresentationOnly     bool   `json:"presentation_only"`
	CanRepresentBusiness bool   `json:"can_represent_business_step"`
}

type EditorSession struct {
	SchemaVersion        string                        `json:"schema_version"`
	SessionID            string                        `json:"session_id"`
	Name                 string                        `json:"name"`
	Mode                 EditorSessionMode             `json:"mode"`
	Status               EditorSessionStatus           `json:"status"`
	Revision             int                           `json:"revision"`
	CreatedAt            time.Time                     `json:"created_at"`
	UpdatedAt            time.Time                     `json:"updated_at"`
	AssetCatalog         AssetTimelineCatalog          `json:"asset_catalog"`
	EditPlan             DemoEditPlan                  `json:"edit_plan"`
	Validation           *DemoEditPlanValidationReport `json:"validation,omitempty"`
	PreviewProfile       EditorRenderProfile           `json:"preview_profile"`
	FinalProfile         EditorRenderProfile           `json:"final_profile"`
	Preview              EditorRenderState             `json:"preview"`
	FinalRender          EditorRenderState             `json:"final_render"`
	ProviderCapabilities []EditorProviderCapability    `json:"provider_capabilities"`
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

type EditorAudioAnalysisRequest struct {
	AssetID            string  `json:"asset_id"`
	BucketMS           int     `json:"bucket_ms,omitempty"`
	SilenceThresholdDB float64 `json:"silence_threshold_db,omitempty"`
	MinSilenceMS       int     `json:"min_silence_ms,omitempty"`
}

type EditorSavePlanRequest struct {
	ExpectedRevision int          `json:"expected_revision"`
	EditPlan         DemoEditPlan `json:"edit_plan"`
}
