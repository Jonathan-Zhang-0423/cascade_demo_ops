package model

import "time"

const AssetTimelineCatalogSchemaVersion = "demoops.asset_timeline_catalog.v1"
const DemoEditPlanSchemaVersion = "demoops.demo_edit_plan.v1"
const DemoEditPlanValidationSchemaVersion = "demoops.demo_edit_plan_validation.v1"

type DemoEditSourceMaterialPolicy string

const (
	DemoEditSourceMaterialPolicyExistingAssetsOnly DemoEditSourceMaterialPolicy = "existing_assets_only"
)

type DemoEditScriptOrderPolicy string

const (
	DemoEditScriptOrderPolicyPreserveRequiredStepOrder DemoEditScriptOrderPolicy = "preserve_required_step_order"
)

type DemoEditSourceAuthority string

const (
	DemoEditSourceAuthorityCustomerSideAgent DemoEditSourceAuthority = "customer_side_agent"
)

type DemoEditModelRole string

const (
	DemoEditModelRolePresentationOptimizerOnly DemoEditModelRole = "presentation_optimizer_only"
)

type EditOperationType string

const (
	EditOperationTrim            EditOperationType = "trim"
	EditOperationCrop            EditOperationType = "crop"
	EditOperationZoomPan         EditOperationType = "zoom_pan"
	EditOperationPan             EditOperationType = "pan"
	EditOperationHold            EditOperationType = "hold"
	EditOperationSpeed           EditOperationType = "speed"
	EditOperationTransition      EditOperationType = "transition"
	EditOperationCaption         EditOperationType = "caption"
	EditOperationCallout         EditOperationType = "callout"
	EditOperationHighlight       EditOperationType = "highlight"
	EditOperationCursorHighlight EditOperationType = "cursor_highlight"
	EditOperationBlurRegion      EditOperationType = "blur_region"
	EditOperationColorGrade      EditOperationType = "color_grade"
)

type EditOverlayType string

const (
	EditOverlayCaption         EditOverlayType = "caption"
	EditOverlayCallout         EditOverlayType = "callout"
	EditOverlayHighlightBox    EditOverlayType = "highlight_box"
	EditOverlayCursorHighlight EditOverlayType = "cursor_highlight"
	EditOverlaySpotlight       EditOverlayType = "spotlight"
	EditOverlayBlurRegion      EditOverlayType = "blur_region"
	EditOverlayProgressMarker  EditOverlayType = "progress_marker"
)

var DemoEditAllowedOperations = []EditOperationType{
	EditOperationTrim,
	EditOperationCrop,
	EditOperationZoomPan,
	EditOperationPan,
	EditOperationHold,
	EditOperationSpeed,
	EditOperationTransition,
	EditOperationCaption,
	EditOperationCallout,
	EditOperationHighlight,
	EditOperationCursorHighlight,
	EditOperationBlurRegion,
	EditOperationColorGrade,
}

var DemoEditAllowedOverlayTypes = []EditOverlayType{
	EditOverlayCaption,
	EditOverlayCallout,
	EditOverlayHighlightBox,
	EditOverlayCursorHighlight,
	EditOverlaySpotlight,
	EditOverlayBlurRegion,
	EditOverlayProgressMarker,
}

var DemoEditProhibitedPlanKeys = []string{
	"image_prompt",
	"video_prompt",
	"negative_prompt",
	"generate_asset",
	"generated_asset",
	"generated_image",
	"generated_video",
	"text_to_image",
	"text_to_video",
	"diffusion",
	"synthesize",
	"create_image",
	"create_video",
	"new_ui_action",
	"browser_action",
	"action_spec",
}

var DemoEditRequiredLockedFields = []string{
	"source_authority",
	"model_role",
	"source_material_policy",
	"script_order_policy",
	"source_artifact_id",
	"source_step_id",
	"source_time_range_ms",
	"required_step_order",
}

var DemoEditAllowedModelEditableFields = []string{
	"purpose",
	"overlays.text",
	"global_style.color_grade",
	"global_style.pacing",
	"global_style.transition_style",
	"operations.zoom",
	"operations.speed",
	"operations.style",
}

type AssetTimelineCatalog struct {
	SchemaVersion   string                   `json:"schema_version"`
	CatalogID       string                   `json:"catalog_id"`
	WorkflowGraphID string                   `json:"workflow_graph_id"`
	GraphVersion    int                      `json:"graph_version"`
	RunID           string                   `json:"run_id"`
	Source          AssetTimelineSource      `json:"source"`
	Constraints     AssetTimelineConstraints `json:"constraints"`
	Timeline        AssetTimelineInfo        `json:"timeline"`
	Steps           []TimelineStep           `json:"steps"`
	Artifacts       []TimelineArtifact       `json:"artifacts"`
}

type AssetTimelineSource struct {
	RecordingResultPackageID string    `json:"recording_result_package_id,omitempty"`
	ExecutionTraceID         string    `json:"execution_trace_id,omitempty"`
	GeneratedAt              time.Time `json:"generated_at"`
}

type AssetTimelineConstraints struct {
	SourceMaterialOnly                bool                `json:"source_material_only"`
	ProhibitNewImageOrVideoGeneration bool                `json:"prohibit_new_image_or_video_generation"`
	ScriptIsPrimaryStoryline          bool                `json:"script_is_primary_storyline"`
	AllowedEditOperations             []EditOperationType `json:"allowed_edit_operations"`
	ProhibitedPlanKeys                []string            `json:"prohibited_plan_keys"`
}

type AssetTimelineInfo struct {
	DurationMS          int    `json:"duration_ms"`
	RecordingArtifactID string `json:"recording_artifact_id,omitempty"`
}

type TimelineStep struct {
	StepID          string              `json:"step_id"`
	Order           int                 `json:"order"`
	Action          string              `json:"action"`
	Status          string              `json:"status"`
	Required        bool                `json:"required"`
	StartMS         int                 `json:"start_ms"`
	EndMS           int                 `json:"end_ms"`
	DurationMS      int                 `json:"duration_ms"`
	ExpectedOutcome string              `json:"expected_outcome,omitempty"`
	ObservedState   string              `json:"observed_state,omitempty"`
	SourceNode      *TimelineSourceNode `json:"source_node,omitempty"`
	Artifacts       []string            `json:"artifacts"`
}

type TimelineSourceNode struct {
	Selector      string `json:"selector,omitempty"`
	FocusSelector string `json:"focus_selector,omitempty"`
	AssetRole     string `json:"asset_role,omitempty"`
}

type TimelineArtifact struct {
	ID            string         `json:"id"`
	Kind          string         `json:"kind"`
	URI           string         `json:"uri"`
	MimeType      string         `json:"mime_type,omitempty"`
	Label         string         `json:"label,omitempty"`
	SHA256        string         `json:"sha256,omitempty"`
	SizeBytes     int64          `json:"size_bytes,omitempty"`
	Sensitive     bool           `json:"sensitive,omitempty"`
	SourceStepID  string         `json:"source_step_id,omitempty"`
	AssetRole     string         `json:"asset_role,omitempty"`
	IncludeInDemo bool           `json:"include_in_demo,omitempty"`
	CaptureScope  string         `json:"capture_scope,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	DurationMS    int            `json:"duration_ms,omitempty"`
	LocalPath     string         `json:"local_path,omitempty"`
}

type MillisecondRange [2]int

type DemoEditPlan struct {
	SchemaVersion        string                       `json:"schema_version,omitempty"`
	PlanID               string                       `json:"plan_id"`
	CatalogID            string                       `json:"catalog_id,omitempty"`
	Objective            string                       `json:"objective,omitempty"`
	SourceAuthority      DemoEditSourceAuthority      `json:"source_authority"`
	ModelRole            DemoEditModelRole            `json:"model_role"`
	SourceMaterialPolicy DemoEditSourceMaterialPolicy `json:"source_material_policy"`
	ScriptOrderPolicy    DemoEditScriptOrderPolicy    `json:"script_order_policy"`
	LockedFields         []string                     `json:"locked_fields"`
	ModelEditableFields  []string                     `json:"model_editable_fields"`
	TargetDurationMS     int                          `json:"target_duration_ms,omitempty"`
	Shots                []DemoEditShot               `json:"shots"`
	GlobalStyle          *DemoEditGlobalStyle         `json:"global_style,omitempty"`
}

type DemoEditGlobalStyle struct {
	ColorGrade      string `json:"color_grade,omitempty"`
	Pacing          string `json:"pacing,omitempty"`
	TransitionStyle string `json:"transition_style,omitempty"`
}

type DemoEditShot struct {
	ID                string            `json:"id"`
	SourceArtifactID  string            `json:"source_artifact_id"`
	SourceStepID      string            `json:"source_step_id,omitempty"`
	SourceTimeRangeMS *MillisecondRange `json:"source_time_range_ms,omitempty"`
	Purpose           string            `json:"purpose"`
	Operations        []EditOperation   `json:"operations,omitempty"`
	Overlays          []EditOverlay     `json:"overlays,omitempty"`
}

type EditOperation struct {
	Type          EditOperationType `json:"type"`
	StartMS       *int              `json:"start_ms,omitempty"`
	EndMS         *int              `json:"end_ms,omitempty"`
	FocusSelector string            `json:"focus_selector,omitempty"`
	Zoom          *float64          `json:"zoom,omitempty"`
	X             *float64          `json:"x,omitempty"`
	Y             *float64          `json:"y,omitempty"`
	Scale         *float64          `json:"scale,omitempty"`
	Speed         *float64          `json:"speed,omitempty"`
	Style         string            `json:"style,omitempty"`
}

type EditOverlay struct {
	Type           EditOverlayType `json:"type"`
	Text           string          `json:"text,omitempty"`
	SourceStepID   string          `json:"source_step_id,omitempty"`
	TargetSelector string          `json:"target_selector,omitempty"`
	StartMS        *int            `json:"start_ms,omitempty"`
	EndMS          *int            `json:"end_ms,omitempty"`
}

type DemoEditPlanValidationReport struct {
	SchemaVersion string                          `json:"schema_version"`
	Valid         bool                            `json:"valid"`
	CheckedAt     time.Time                       `json:"checked_at"`
	PlanID        string                          `json:"plan_id,omitempty"`
	Errors        []DemoEditPlanValidationFinding `json:"errors"`
	Warnings      []DemoEditPlanValidationFinding `json:"warnings"`
}

type DemoEditPlanValidationFinding struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
}
