package model

import "time"

const DemoWorkflowGraphSchemaVersion = "demoops.workflow_graph.v1"

type GraphStatus string

const (
	GraphStatusDraft       GraphStatus = "draft"
	GraphStatusReviewReady GraphStatus = "review_ready"
	GraphStatusApproved    GraphStatus = "approved"
	GraphStatusRehearsing  GraphStatus = "rehearsing"
	GraphStatusValidated   GraphStatus = "validated"
	GraphStatusAssetReady  GraphStatus = "asset_ready"
	GraphStatusDeprecated  GraphStatus = "deprecated"
)

type GraphNodeType string

const (
	GraphNodeTypeStart      GraphNodeType = "start"
	GraphNodeTypeAction     GraphNodeType = "action"
	GraphNodeTypeDecision   GraphNodeType = "decision"
	GraphNodeTypeValidation GraphNodeType = "validation"
	GraphNodeTypeNarrative  GraphNodeType = "narrative"
	GraphNodeTypeCapture    GraphNodeType = "capture"
	GraphNodeTypeEnd        GraphNodeType = "end"
)

type GraphActionType string

const (
	GraphActionNavigate GraphActionType = "navigate"
	GraphActionClick    GraphActionType = "click"
	GraphActionFill     GraphActionType = "fill"
	GraphActionSelect   GraphActionType = "select"
	GraphActionUpload   GraphActionType = "upload"
	GraphActionWait     GraphActionType = "wait"
	GraphActionAssert   GraphActionType = "assert"
	GraphActionInspect  GraphActionType = "inspect"
	GraphActionAPICall  GraphActionType = "api_call"
)

type AssetKind string

const (
	AssetKindDemoVideo       AssetKind = "demo_video"
	AssetKindScreenshotPack  AssetKind = "screenshot_pack"
	AssetKindStepByStepDocs  AssetKind = "step_by_step_docs"
	AssetKindInteractiveDemo AssetKind = "interactive_demo"
	AssetKindSupportSnippet  AssetKind = "support_snippet"
	AssetKindSalesMaterial   AssetKind = "sales_material"
)

type CaptureScope string

const (
	CaptureScopeViewport CaptureScope = "viewport"
	CaptureScopeFullPage CaptureScope = "full_page"
	CaptureScopeElement  CaptureScope = "element"
)

// DemoWorkflowGraph is the executable product-demo knowledge graph.
// Videos, screenshots, and docs are rendered artifacts derived from this graph.
type DemoWorkflowGraph struct {
	ID            string              `json:"id"`
	ProjectID     string              `json:"project_id,omitempty"`
	SchemaVersion string              `json:"schema_version,omitempty"`
	Version       int                 `json:"version"`
	Status        GraphStatus         `json:"status,omitempty"`
	Name          string              `json:"name,omitempty"`
	Summary       string              `json:"summary,omitempty"`
	EntryPoint    string              `json:"entry_point"`
	Intent        *WorkflowIntent     `json:"intent,omitempty"`
	Requirements  []GraphRequirement  `json:"requirements,omitempty"`
	Variables     []GraphVariable     `json:"variables,omitempty"`
	TestData      []TestDataRecord    `json:"test_data,omitempty"`
	Nodes         []*GraphNode        `json:"nodes"`
	Edges         []*GraphEdge        `json:"edges"`
	States        []*GraphState       `json:"states,omitempty"`
	Validations   []*ValidationSpec   `json:"validations,omitempty"`
	Narratives    []*NarrativeSegment `json:"narratives,omitempty"`
	Assets        *AssetManifest      `json:"assets"`
	EvidenceRefs  []EvidenceRef       `json:"evidence_refs,omitempty"`
	Provenance    *GraphProvenance    `json:"provenance,omitempty"`
	Review        *GraphReviewState   `json:"review,omitempty"`
	Execution     *ExecutionPolicy    `json:"execution,omitempty"`
	Maintenance   *MaintenancePolicy  `json:"maintenance,omitempty"`
	CreatedAt     time.Time           `json:"created_at,omitempty"`
	UpdatedAt     time.Time           `json:"updated_at,omitempty"`
}

type WorkflowIntent struct {
	UseCase            DemoUseCase      `json:"use_case,omitempty"`
	Audience           *AudienceProfile `json:"audience,omitempty"`
	Objective          string           `json:"objective,omitempty"`
	ValueProposition   string           `json:"value_proposition,omitempty"`
	PrimaryFeatureRefs []string         `json:"primary_feature_refs,omitempty"`
	SuccessCriteria    []string         `json:"success_criteria,omitempty"`
	DesiredEmotion     string           `json:"desired_emotion,omitempty"`
	CTA                string           `json:"cta,omitempty"`
}

type GraphRequirement struct {
	ID           string        `json:"id"`
	Kind         string        `json:"kind"`
	Description  string        `json:"description"`
	Required     bool          `json:"required"`
	NodeRefs     []string      `json:"node_refs,omitempty"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

type GraphVariable struct {
	Name         string        `json:"name"`
	Kind         string        `json:"kind,omitempty"`
	Value        string        `json:"value,omitempty"`
	SecretRef    string        `json:"secret_ref,omitempty"`
	Required     bool          `json:"required,omitempty"`
	Sensitive    bool          `json:"sensitive,omitempty"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

type TestDataRecord struct {
	ID           string            `json:"id"`
	Name         string            `json:"name,omitempty"`
	Purpose      string            `json:"purpose,omitempty"`
	Fields       map[string]string `json:"fields,omitempty"`
	SecretRefs   map[string]string `json:"secret_refs,omitempty"`
	ResetPolicy  string            `json:"reset_policy,omitempty"`
	EvidenceRefs []EvidenceRef     `json:"evidence_refs,omitempty"`
}

type GraphNode struct {
	ID              string `json:"id"`
	Action          string `json:"action"`
	Selector        string `json:"selector"`
	InputData       string `json:"input_data"`
	ExpectedOutcome string `json:"expected_outcome"`
	IsScreenshot    bool   `json:"is_screenshot"`
	HasZoom         bool   `json:"has_zoom"`
	RetryPolicy     int    `json:"retry_policy"`

	Type           GraphNodeType      `json:"type,omitempty"`
	Title          string             `json:"title,omitempty"`
	Goal           string             `json:"goal,omitempty"`
	Description    string             `json:"description,omitempty"`
	ActorRole      string             `json:"actor_role,omitempty"`
	PageRef        string             `json:"page_ref,omitempty"`
	FeatureRefs    []string           `json:"feature_refs,omitempty"`
	ActionSpec     *GraphAction       `json:"action_spec,omitempty"`
	StateBefore    []StateAssertion   `json:"state_before,omitempty"`
	StateAfter     []StateAssertion   `json:"state_after,omitempty"`
	Validations    []ValidationSpec   `json:"validations,omitempty"`
	Narrative      *NarrativeCue      `json:"narrative,omitempty"`
	Capture        *CaptureSpec       `json:"capture,omitempty"`
	Assets         []AssetRef         `json:"assets,omitempty"`
	EvidenceRefs   []EvidenceRef      `json:"evidence_refs,omitempty"`
	Alternatives   []AlternativePath  `json:"alternatives,omitempty"`
	FailurePolicy  *NodeFailurePolicy `json:"failure_policy,omitempty"`
	DurationHintMS int                `json:"duration_hint_ms,omitempty"`
	Sensitive      bool               `json:"sensitive,omitempty"`
	Tags           []string           `json:"tags,omitempty"`
	Metadata       map[string]any     `json:"metadata,omitempty"`
}

type GraphAction struct {
	Type          GraphActionType  `json:"type"`
	Target        ActionTarget     `json:"target"`
	Value         string           `json:"value,omitempty"`
	InputRef      string           `json:"input_ref,omitempty"`
	SecretRef     string           `json:"secret_ref,omitempty"`
	Parameters    map[string]any   `json:"parameters,omitempty"`
	TimeoutMS     int              `json:"timeout_ms,omitempty"`
	WaitUntil     string           `json:"wait_until,omitempty"`
	Preconditions []StateAssertion `json:"preconditions,omitempty"`
}

type ActionTarget struct {
	URL                  string              `json:"url,omitempty"`
	Selector             string              `json:"selector,omitempty"`
	Source               string              `json:"source,omitempty"`
	SelectorAlternatives []SelectorCandidate `json:"selector_alternatives,omitempty"`
	Role                 string              `json:"role,omitempty"`
	Text                 string              `json:"text,omitempty"`
	Label                string              `json:"label,omitempty"`
	TestID               string              `json:"test_id,omitempty"`
	Frame                string              `json:"frame,omitempty"`
	ComponentRef         string              `json:"component_ref,omitempty"`
	EvidenceRefs         []EvidenceRef       `json:"evidence_refs,omitempty"`
}

type SelectorCandidate struct {
	Kind                   string        `json:"kind"`
	Value                  string        `json:"value"`
	Confidence             float64       `json:"confidence,omitempty"`
	StabilityScore         float64       `json:"stability_score,omitempty"`
	Source                 string        `json:"source,omitempty"`
	EvidenceID             string        `json:"evidence_id,omitempty"`
	SourceKind             string        `json:"source_kind,omitempty"`
	SourceDigest           string        `json:"source_digest,omitempty"`
	ObservedRole           string        `json:"observed_role,omitempty"`
	ObservedAccessibleName string        `json:"observed_accessible_name,omitempty"`
	ObservedAt             time.Time     `json:"observed_at,omitempty"`
	LastValidatedAt        time.Time     `json:"last_validated_at,omitempty"`
	EvidenceRefs           []EvidenceRef `json:"evidence_refs,omitempty"`
}

type GraphState struct {
	ID             string              `json:"id"`
	Name           string              `json:"name"`
	Kind           string              `json:"kind,omitempty"`
	URLPattern     string              `json:"url_pattern,omitempty"`
	DOMHints       []SelectorCandidate `json:"dom_hints,omitempty"`
	DataAssertions []StateAssertion    `json:"data_assertions,omitempty"`
	FeatureRefs    []string            `json:"feature_refs,omitempty"`
	EvidenceRefs   []EvidenceRef       `json:"evidence_refs,omitempty"`
}

type StateAssertion struct {
	ID           string        `json:"id,omitempty"`
	Kind         string        `json:"kind"`
	Target       ActionTarget  `json:"target,omitempty"`
	Operator     string        `json:"operator,omitempty"`
	Expected     any           `json:"expected,omitempty"`
	Required     bool          `json:"required"`
	TimeoutMS    int           `json:"timeout_ms,omitempty"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

type ValidationSpec struct {
	ID           string        `json:"id"`
	Kind         string        `json:"kind"`
	Target       ActionTarget  `json:"target,omitempty"`
	Assertion    string        `json:"assertion,omitempty"`
	Expected     any           `json:"expected,omitempty"`
	Severity     string        `json:"severity,omitempty"`
	Required     bool          `json:"required"`
	RepairPolicy *RepairPolicy `json:"repair_policy,omitempty"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

type RepairPolicy struct {
	AllowSelectorRepair bool     `json:"allow_selector_repair"`
	AllowDataRepair     bool     `json:"allow_data_repair"`
	AllowStepSkip       bool     `json:"allow_step_skip"`
	MaxAttempts         int      `json:"max_attempts,omitempty"`
	EscalateToHumanOn   []string `json:"escalate_to_human_on,omitempty"`
}

type NarrativeSegment struct {
	ID           string        `json:"id"`
	NodeRefs     []string      `json:"node_refs,omitempty"`
	Title        string        `json:"title,omitempty"`
	Summary      string        `json:"summary,omitempty"`
	Voiceover    string        `json:"voiceover,omitempty"`
	Caption      string        `json:"caption,omitempty"`
	Tone         string        `json:"tone,omitempty"`
	AudienceLens string        `json:"audience_lens,omitempty"`
	Timing       *TimingHint   `json:"timing,omitempty"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

type NarrativeCue struct {
	Title        string      `json:"title,omitempty"`
	Voiceover    string      `json:"voiceover,omitempty"`
	Caption      string      `json:"caption,omitempty"`
	Callout      string      `json:"callout,omitempty"`
	Tone         string      `json:"tone,omitempty"`
	AudienceLens string      `json:"audience_lens,omitempty"`
	Timing       *TimingHint `json:"timing,omitempty"`
}

type TimingHint struct {
	StartMS    int `json:"start_ms,omitempty"`
	DurationMS int `json:"duration_ms,omitempty"`
	Order      int `json:"order,omitempty"`
}

type CaptureSpec struct {
	Screenshot    bool            `json:"screenshot"`
	Video         bool            `json:"video,omitempty"`
	Zoom          bool            `json:"zoom,omitempty"`
	Callout       bool            `json:"callout,omitempty"`
	Dedupe        *bool           `json:"dedupe,omitempty"`
	Scope         CaptureScope    `json:"scope,omitempty"`
	FullPage      bool            `json:"full_page,omitempty"`
	FocusSelector string          `json:"focus_selector,omitempty"`
	AssetRole     string          `json:"asset_role,omitempty"`
	Crop          *CropRect       `json:"crop,omitempty"`
	MaskSelectors []string        `json:"mask_selectors,omitempty"`
	Redactions    []RedactionSpec `json:"redactions,omitempty"`
}

type CropRect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

type RedactionSpec struct {
	Selector    string `json:"selector,omitempty"`
	Pattern     string `json:"pattern,omitempty"`
	Replacement string `json:"replacement,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type AssetRef struct {
	ID             string           `json:"id"`
	Kind           AssetKind        `json:"kind"`
	URI            string           `json:"uri,omitempty"`
	SourceNodeID   string           `json:"source_node_id,omitempty"`
	ExecutionRunID string           `json:"execution_run_id,omitempty"`
	EvidenceRefs   []EvidenceRef    `json:"evidence_refs,omitempty"`
	Provenance     *AssetProvenance `json:"provenance,omitempty"`
}

type AlternativePath struct {
	ID           string        `json:"id"`
	Reason       string        `json:"reason,omitempty"`
	Action       *GraphAction  `json:"action,omitempty"`
	NodeRefs     []string      `json:"node_refs,omitempty"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

type NodeFailurePolicy struct {
	RetryAttempts       int           `json:"retry_attempts,omitempty"`
	RetryBackoffMS      int           `json:"retry_backoff_ms,omitempty"`
	OnFailure           string        `json:"on_failure,omitempty"`
	RepairPolicy        *RepairPolicy `json:"repair_policy,omitempty"`
	HumanReviewRequired bool          `json:"human_review_required,omitempty"`
}

type GraphEdge struct {
	ID            string         `json:"id"`
	FromNode      string         `json:"from_node"`
	ToNode        string         `json:"to_node"`
	Condition     string         `json:"condition,omitempty"`
	ConditionSpec *EdgeCondition `json:"condition_spec,omitempty"`
	Priority      int            `json:"priority,omitempty"`
	EvidenceRefs  []EvidenceRef  `json:"evidence_refs,omitempty"`
}

type EdgeCondition struct {
	Kind       string `json:"kind"`
	Expression string `json:"expression,omitempty"`
	PassState  string `json:"pass_state,omitempty"`
	FailState  string `json:"fail_state,omitempty"`
}

type AssetManifest struct {
	DemoVideo60s      bool `json:"demo_video_60s"`
	ScreenshotPack    bool `json:"screenshot_pack"`
	StepByStepDocs    bool `json:"step_by_step_docs"`
	InteractiveDemo   bool `json:"interactive_demo,omitempty"`
	SupportSnippet    bool `json:"support_snippet,omitempty"`
	SalesMaterial     bool `json:"sales_material,omitempty"`
	TargetDurationSec int  `json:"target_duration_sec"`

	RequestedAssets []AssetRequest        `json:"requested_assets,omitempty"`
	GeneratedAssets []AssetRef            `json:"generated_assets,omitempty"`
	Channels        []DistributionChannel `json:"channels,omitempty"`
	Brand           *BrandKit             `json:"brand,omitempty"`
	Localization    []string              `json:"localization,omitempty"`
	ReviewStatus    string                `json:"review_status,omitempty"`
	Provenance      *AssetProvenance      `json:"provenance,omitempty"`
}

type AssetRequest struct {
	ID            string        `json:"id"`
	Kind          AssetKind     `json:"kind"`
	UseCase       DemoUseCase   `json:"use_case,omitempty"`
	AudienceID    string        `json:"audience_id,omitempty"`
	Format        string        `json:"format,omitempty"`
	DurationSec   int           `json:"duration_sec,omitempty"`
	SourceNodeIDs []string      `json:"source_node_ids,omitempty"`
	Required      bool          `json:"required"`
	Status        string        `json:"status,omitempty"`
	EvidenceRefs  []EvidenceRef `json:"evidence_refs,omitempty"`
}

type DistributionChannel struct {
	Kind        string            `json:"kind"`
	Destination string            `json:"destination,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type AssetProvenance struct {
	WorkflowGraphID  string        `json:"workflow_graph_id,omitempty"`
	GraphVersion     int           `json:"graph_version,omitempty"`
	ExecutionRunID   string        `json:"execution_run_id,omitempty"`
	RepoSnapshotID   string        `json:"repo_snapshot_id,omitempty"`
	ServerSnapshotID string        `json:"server_snapshot_id,omitempty"`
	EvidenceRefs     []EvidenceRef `json:"evidence_refs,omitempty"`
	GeneratedBy      string        `json:"generated_by,omitempty"`
	GeneratedAt      time.Time     `json:"generated_at,omitempty"`
}

type GraphProvenance struct {
	CreatedBy      string        `json:"created_by,omitempty"`
	Model          string        `json:"model,omitempty"`
	PromptRef      string        `json:"prompt_ref,omitempty"`
	ProductMapID   string        `json:"product_map_id,omitempty"`
	RepoSnapshotID string        `json:"repo_snapshot_id,omitempty"`
	BrowserScanID  string        `json:"browser_scan_id,omitempty"`
	EvidenceRefs   []EvidenceRef `json:"evidence_refs,omitempty"`
}

type GraphReviewState struct {
	Status     string    `json:"status,omitempty"`
	ReviewedBy string    `json:"reviewed_by,omitempty"`
	ReviewedAt time.Time `json:"reviewed_at,omitempty"`
	Notes      []string  `json:"notes,omitempty"`
	ChangeRefs []string  `json:"change_refs,omitempty"`
}

type ExecutionPolicy struct {
	RequiredPassRate float64        `json:"required_pass_rate,omitempty"`
	MaxAttempts      int            `json:"max_attempts,omitempty"`
	TimeoutMS        int            `json:"timeout_ms,omitempty"`
	Browser          string         `json:"browser,omitempty"`
	Headless         bool           `json:"headless,omitempty"`
	Viewports        []ViewportSpec `json:"viewports,omitempty"`
	TraceLevel       string         `json:"trace_level,omitempty"`
	FailurePolicy    *RepairPolicy  `json:"failure_policy,omitempty"`
}

type ViewportSpec struct {
	Name   string `json:"name,omitempty"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Device string `json:"device,omitempty"`
}

type MaintenancePolicy struct {
	UpdateTriggers     []string      `json:"update_triggers,omitempty"`
	StalenessDays      int           `json:"staleness_days,omitempty"`
	LastVerifiedAt     time.Time     `json:"last_verified_at,omitempty"`
	Owner              string        `json:"owner,omitempty"`
	RegressionChecks   []string      `json:"regression_checks,omitempty"`
	SourceEvidenceRefs []EvidenceRef `json:"source_evidence_refs,omitempty"`
}

func NewDemoWorkflowGraph(id string, projectID string, entryPoint string) *DemoWorkflowGraph {
	now := time.Now().UTC()
	return &DemoWorkflowGraph{
		ID:            id,
		ProjectID:     projectID,
		SchemaVersion: DemoWorkflowGraphSchemaVersion,
		Version:       1,
		Status:        GraphStatusDraft,
		EntryPoint:    entryPoint,
		Nodes:         []*GraphNode{},
		Edges:         []*GraphEdge{},
		Assets:        NewMVPAssetManifest(),
		Execution:     NewDefaultExecutionPolicy(),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func NewDefaultExecutionPolicy() *ExecutionPolicy {
	return &ExecutionPolicy{
		RequiredPassRate: 0.90,
		MaxAttempts:      2,
		TimeoutMS:        120000,
		Browser:          "chromium",
		Headless:         true,
		Viewports: []ViewportSpec{
			{Name: "desktop", Width: 1440, Height: 900, Device: "desktop"},
		},
		TraceLevel: "screenshots_dom_console_network",
		FailurePolicy: &RepairPolicy{
			AllowSelectorRepair: true,
			AllowDataRepair:     true,
			AllowStepSkip:       false,
			MaxAttempts:         2,
		},
	}
}

func NewMVPAssetManifest() *AssetManifest {
	return &AssetManifest{
		DemoVideo60s:      true,
		ScreenshotPack:    true,
		StepByStepDocs:    true,
		TargetDurationSec: 60,
		RequestedAssets: []AssetRequest{
			{ID: "demo_video_60s", Kind: AssetKindDemoVideo, Format: "mp4", DurationSec: 60, Required: true, Status: "requested"},
			{ID: "screenshot_pack", Kind: AssetKindScreenshotPack, Format: "png", Required: true, Status: "requested"},
			{ID: "step_by_step_docs", Kind: AssetKindStepByStepDocs, Format: "markdown", Required: true, Status: "requested"},
		},
	}
}
