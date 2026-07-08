package model

// DemoWorkflowGraph is the executable product-demo knowledge graph.
// Videos, screenshots, and docs are rendered artifacts derived from this graph.
type DemoWorkflowGraph struct {
	ID         string         `json:"id"`
	Version    int            `json:"version"`
	EntryPoint string         `json:"entry_point"`
	Nodes      []*GraphNode   `json:"nodes"`
	Edges      []*GraphEdge   `json:"edges"`
	Assets     *AssetManifest `json:"assets"`
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
}

type GraphEdge struct {
	ID        string `json:"id"`
	FromNode  string `json:"from_node"`
	ToNode    string `json:"to_node"`
	Condition string `json:"condition,omitempty"`
}

type AssetManifest struct {
	DemoVideo60s      bool `json:"demo_video_60s"`
	ScreenshotPack    bool `json:"screenshot_pack"`
	StepByStepDocs    bool `json:"step_by_step_docs"`
	InteractiveDemo   bool `json:"interactive_demo,omitempty"`
	SupportSnippet    bool `json:"support_snippet,omitempty"`
	SalesMaterial     bool `json:"sales_material,omitempty"`
	TargetDurationSec int  `json:"target_duration_sec"`
}

func NewMVPAssetManifest() *AssetManifest {
	return &AssetManifest{
		DemoVideo60s:      true,
		ScreenshotPack:    true,
		StepByStepDocs:    true,
		TargetDurationSec: 60,
	}
}
