package executor_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/driver"
	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/model"
)

func TestFullRecordingRenderPipelineSmoke(t *testing.T) {
	if os.Getenv("CASCADE_FULL_PIPELINE_SMOKE") != "1" {
		t.Skip("set CASCADE_FULL_PIPELINE_SMOKE=1 to run the real Playwright recording/render smoke test")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(fullPipelineSmokeHTML))
	}))
	defer server.Close()

	outputRoot := os.Getenv("CASCADE_FULL_PIPELINE_OUTPUT_DIR")
	if outputRoot == "" {
		outputRoot = filepath.Join("..", "..", "..", "artifacts", fmt.Sprintf("full-pipeline-smoke-%s", time.Now().Format("20060102-150405")))
	}
	recordingDir := filepath.Join(outputRoot, "recording")
	renderDir := filepath.Join(outputRoot, "render")
	workerPath := os.Getenv("CASCADE_VIDEO_WORKER_PATH")
	if workerPath == "" {
		workerPath = filepath.Join("..", "..", "..", "video-worker", "dist", "index.js")
	}
	if _, err := os.Stat(workerPath); err != nil {
		t.Fatalf("video worker is not built at %s: %v", workerPath, err)
	}

	source := fullPipelineClientExecutionPackage(t, server.URL)
	service := driver.NewLocalDriver(os.Getenv("CASCADE_NODE_BINARY"), workerPath)
	result, err := executor.RunClientExecutionRecordingAndRender(context.Background(), service, executor.RecordingRenderPipelineRequest{
		SourcePackage:      &source,
		CloudJobID:         "cloud_job_full_pipeline_smoke",
		RecordingOutputDir: recordingDir,
		RenderOutputDir:    renderDir,
		ResultCreatedAt:    time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}

	recordingPackage := result.RecordingResultPackage
	if recordingPackage.Status != model.RecordingResultStatusGenerated || recordingPackage.ExecutionTrace == nil {
		t.Fatalf("recording result package is not renderable: %+v", recordingPackage)
	}
	if recordingPackage.ExecutionTrace.PassRate != 1 {
		t.Fatalf("expected all steps to pass, got pass rate %.2f", recordingPackage.ExecutionTrace.PassRate)
	}
	if got := countArtifacts(recordingPackage.GeneratedAssets, "raw_recording"); got != 1 {
		t.Fatalf("expected one raw recording artifact, got %d: %+v", got, recordingPackage.GeneratedAssets)
	}
	if got := countArtifacts(recordingPackage.GeneratedAssets, "screenshot"); got < 1 {
		t.Fatalf("expected at least one screenshot artifact, got %d: %+v", got, recordingPackage.GeneratedAssets)
	}
	if got := countArtifacts(recordingPackage.GeneratedAssets, "browser_trace"); got != 1 {
		t.Fatalf("expected one browser trace artifact, got %d: %+v", got, recordingPackage.GeneratedAssets)
	}

	rawRecording := findArtifact(recordingPackage.GeneratedAssets, "raw_recording")
	if rawRecording == nil || rawRecording.Metadata["include_in_demo"] != true {
		t.Fatalf("raw recording must be eligible demo material: %+v", rawRecording)
	}
	trace := findArtifact(recordingPackage.GeneratedAssets, "browser_trace")
	if trace == nil || trace.Metadata["include_in_demo"] != false {
		t.Fatalf("browser trace must be debug-only material: %+v", trace)
	}

	renderResult := result.RenderResult
	if !strings.EqualFold(filepath.Ext(renderResult.VideoPath), ".mp4") {
		t.Fatalf("final delivery must be MP4, got %s", renderResult.VideoPath)
	}
	if renderResult.SourceReferenceVideoPath == "" || renderResult.VideoPath != renderResult.SourceReferenceVideoPath {
		t.Fatalf("final delivery must use the normalized, probed MP4 reference: %+v", renderResult)
	}
	if renderResult.ValidationReport == nil || !renderResult.ValidationReport.Valid {
		t.Fatalf("demo edit plan validation failed: %+v", renderResult.ValidationReport)
	}
	if renderResult.DemoEditPlan == nil || renderResult.DemoEditPlan.SourceMaterialPolicy != model.DemoEditSourceMaterialPolicyExistingAssetsOnly {
		t.Fatalf("demo edit plan lost source-only policy: %+v", renderResult.DemoEditPlan)
	}
	if renderResult.DemoEditPlan.SourceAuthority != model.DemoEditSourceAuthorityCustomerSideAgent || renderResult.DemoEditPlan.ModelRole != model.DemoEditModelRolePresentationOptimizerOnly {
		t.Fatalf("demo edit plan lost collaboration boundary: %+v", renderResult.DemoEditPlan)
	}
	if renderResult.AssetTimelineCatalog == nil || !renderResult.AssetTimelineCatalog.Constraints.ScriptIsPrimaryStoryline {
		t.Fatalf("asset timeline catalog lost script-storyline constraint: %+v", renderResult.AssetTimelineCatalog)
	}
	catalogRaw := findTimelineArtifact(renderResult.AssetTimelineCatalog.Artifacts, "raw_recording")
	if catalogRaw == nil || !catalogRaw.IncludeInDemo || catalogRaw.AssetRole != "raw_recording" {
		t.Fatalf("catalog raw recording metadata was not preserved: %+v", catalogRaw)
	}

	for _, path := range []string{
		rawRecording.URI,
		trace.URI,
		renderResult.VideoPath,
		renderResult.StepByStepDocsPath,
		renderResult.AssetTimelineCatalogPath,
		renderResult.DemoEditPlanPath,
		renderResult.ValidationReportPath,
		renderResult.RenderManifestPath,
		renderResult.MediaNormalizationReportPath,
		renderResult.RequirementReportPath,
	} {
		if err := requireLocalFile(path); err != nil {
			t.Fatal(err)
		}
	}
	if renderResult.SourceReferenceVideoPath != "" {
		if err := requireLocalFile(renderResult.SourceReferenceVideoPath); err != nil {
			t.Fatal(err)
		}
	}
	renderManifest := readRenderManifest(t, renderResult.RenderManifestPath)
	compositor, ok := renderManifest["compositor"].(map[string]any)
	if !ok {
		t.Fatalf("render manifest missing compositor result: %+v", renderManifest)
	}
	method, _ := compositor["method"].(string)
	if method != "ffmpeg_trim_concat" {
		t.Fatalf("final smoke must use deterministic FFmpeg composition, got %q: %+v", method, compositor)
	}
	shotPlan, ok := compositor["shot_plan"].([]any)
	if !ok || len(shotPlan) != len(renderResult.DemoEditPlan.Shots) {
		t.Fatalf("render manifest should carry a shot plan matching the edit plan: compositor=%+v edit_plan=%+v", compositor, renderResult.DemoEditPlan.Shots)
	}
	firstShot, ok := shotPlan[0].(map[string]any)
	if !ok || firstShot["source_time_range_ms"] == nil || firstShot["source_artifact_id"] == "" {
		t.Fatalf("render manifest shot plan lost source range or source artifact: %+v", firstShot)
	}

	summaryPath := filepath.Join(outputRoot, "full_pipeline_summary.json")
	summary := map[string]any{
		"input_package_id":                source.PackageID,
		"input_schema_version":            source.SchemaVersion,
		"output_root":                     outputRoot,
		"recording_result_id":             recordingPackage.ResultID,
		"recording_status":                recordingPackage.Status,
		"generated_asset_count":           len(recordingPackage.GeneratedAssets),
		"timeline_artifact_count":         len(renderResult.AssetTimelineCatalog.Artifacts),
		"demo_edit_plan_path":             renderResult.DemoEditPlanPath,
		"demo_edit_plan_valid":            renderResult.ValidationReport.Valid,
		"render_manifest_path":            renderResult.RenderManifestPath,
		"media_normalization_report_path": renderResult.MediaNormalizationReportPath,
		"requirement_report_path":         renderResult.RequirementReportPath,
		"source_reference_video_path":     renderResult.SourceReferenceVideoPath,
		"render_method":                   method,
		"video_path":                      renderResult.VideoPath,
		"final_video_file_created":        true,
		"source_material_only":            renderResult.AssetTimelineCatalog.Constraints.SourceMaterialOnly,
		"script_is_primary_storyline":     renderResult.AssetTimelineCatalog.Constraints.ScriptIsPrimaryStoryline,
		"prohibit_new_image_or_video":     renderResult.AssetTimelineCatalog.Constraints.ProhibitNewImageOrVideoGeneration,
		"customer_side_source_authority":  renderResult.DemoEditPlan.SourceAuthority,
	}
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outputRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(summaryPath, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("full pipeline smoke output: %s", outputRoot)
}

func fullPipelineClientExecutionPackage(t *testing.T, baseURL string) model.ClientExecutionPackage {
	t.Helper()
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	host := parsed.Host
	now := time.Date(2026, 7, 9, 11, 0, 0, 0, time.UTC)
	projectID := "project_full_pipeline_smoke"

	graph := model.NewDemoWorkflowGraph("graph_full_pipeline_smoke", projectID, baseURL)
	graph.Status = model.GraphStatusApproved
	graph.Name = "Full pipeline smoke"
	graph.Nodes = []*model.GraphNode{
		graphNode("node_open", "Open product", model.GraphActionNavigate, model.ActionTarget{URL: baseURL}, "Product page loads", true, 900),
		graphNode("node_fill", "Enter demo email", model.GraphActionFill, model.ActionTarget{Selector: "#email"}, "Email is entered", true, 700),
		graphNode("node_click", "Start demo", model.GraphActionClick, model.ActionTarget{Selector: "#start"}, "Primary action is clicked", true, 700),
		graphNode("node_assert", "Confirm status", model.GraphActionAssert, model.ActionTarget{Selector: "#status.done"}, "Confirmation is visible", true, 900),
	}
	graph.Edges = []*model.GraphEdge{
		{ID: "edge_open_fill", FromNode: "node_open", ToNode: "node_fill", Condition: "loaded"},
		{ID: "edge_fill_click", FromNode: "node_fill", ToNode: "node_click", Condition: "filled"},
		{ID: "edge_click_assert", FromNode: "node_click", ToNode: "node_assert", Condition: "clicked"},
	}
	graphDigest, err := model.DigestCanonicalJSON(graph)
	if err != nil {
		t.Fatal(err)
	}

	runSpec := model.RecordingRunSpec{
		RunID:          "run_full_pipeline_smoke",
		BaseURL:        baseURL,
		AllowedDomains: []string{host},
		Browser: model.BrowserRunSpec{
			Engine:        "chromium",
			VersionPolicy: "bundled_playwright",
			Headless:      true,
			Viewports:     []model.ViewportSpec{{Name: "desktop", Width: 960, Height: 640, Device: "desktop"}},
		},
		Timeline: model.RecordingTimeline{
			TargetDurationSec: 12,
			MaxDurationSec:    30,
			CaptureWindows: []model.CaptureWindow{
				{ID: "capture_open", NodeID: "node_open", StartMS: 0, DurationMS: 900, Role: "primary"},
				{ID: "capture_fill", NodeID: "node_fill", StartMS: 900, DurationMS: 700, Role: "primary"},
				{ID: "capture_click", NodeID: "node_click", StartMS: 1600, DurationMS: 700, Role: "primary"},
				{ID: "capture_assert", NodeID: "node_assert", StartMS: 2300, DurationMS: 900, Role: "primary"},
			},
		},
		Outputs: model.RecordingOutputRequest{
			RawRecording:     true,
			FinalVideo:       true,
			ScreenshotPack:   true,
			StepByStepDocs:   true,
			Trace:            true,
			OutputFormats:    []string{"mp4", "png", "json"},
			ResolutionWidth:  960,
			ResolutionHeight: 640,
		},
		Redactions:    model.RedactionPolicy{MaskSelectors: []string{"[data-sensitive]"}},
		FailurePolicy: model.RecordingFailurePolicy{RetryAttempts: 1, SelectorRepairAllowed: true, MaxRepairAttempts: 1},
		Environment:   map[string]string{"locale": "en-US", "timezone": "UTC"},
	}

	doc := &model.ExecutionScriptDocument{
		ID:               "script_full_pipeline_smoke",
		ProjectID:        projectID,
		WorkflowGraphID:  graph.ID,
		GraphVersion:     graph.Version,
		SchemaVersion:    model.ExecutionScriptDocumentSchemaVersion,
		Status:           model.ScriptDocumentStatusApproved,
		Title:            "Full pipeline smoke script",
		Summary:          "Open a local product page, enter an email, click start, and confirm status.",
		Language:         "typescript",
		WorkflowGraph:    graph,
		RecordingRunSpec: runSpec,
		Steps: []model.ScriptStep{
			scriptStep("step_open", 1, "node_open", baseURL, model.GraphActionNavigate, model.ActionTarget{URL: baseURL}, "", "Product page loads", 900),
			scriptStep("step_fill", 2, "node_fill", "", model.GraphActionFill, model.ActionTarget{Selector: "#email"}, "demo@example.com", "Email is entered", 700),
			scriptStep("step_click", 3, "node_click", "", model.GraphActionClick, model.ActionTarget{Selector: "#start"}, "", "Primary action is clicked", 700),
			scriptStep("step_assert", 4, "node_assert", "", model.GraphActionAssert, model.ActionTarget{Selector: "#status.done"}, "", "Confirmation is visible", 900),
		},
		SafetyPolicy: model.ScriptSafetyPolicy{
			AllowedDomains: []string{host},
			Redactions:     model.RedactionPolicy{MaskSelectors: []string{"[data-sensitive]"}},
			PIIHandling:    "synthetic_test_data_only",
		},
		Reproducibility:   model.ReproducibilitySpec{GraphHashSHA256: graphDigest},
		ApprovalChecklist: model.ScriptApprovalChecklist{HumanApprovalRequired: true, SourceSummaryOnly: true},
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	planHash, err := doc.ComputeScriptHash()
	if err != nil {
		t.Fatal(err)
	}
	doc.Reproducibility.ScriptHashSHA256 = planHash

	source := fmt.Sprintf(`type CascadeRecordingContext = { page: any; secrets: any; capture: any; assert: any; log: any };
type CascadeRecordingResult = { ok: boolean };
export async function runCascadeRecording(ctx: CascadeRecordingContext): Promise<CascadeRecordingResult> {
  await ctx.page.goto("%s");
  ctx.log("node_open");
  await ctx.page.fill("#email", "demo@example.com");
  ctx.log("node_fill");
  await ctx.page.click("#start");
  ctx.log("node_click");
  ctx.log("node_assert");
  return { ok: true };
}`, baseURL)
	markdown := "# Full pipeline smoke approval\n\n1. Open product page\n2. Enter demo email\n3. Start demo\n4. Confirm success"
	scriptHash := model.SHA256Hex([]byte(source))
	markdownHash := model.SHA256Hex([]byte(markdown))
	bundle := &model.ExecutableRecordingScriptBundle{
		ID:              "bundle_full_pipeline_smoke",
		ProjectID:       projectID,
		WorkflowGraphID: graph.ID,
		SchemaVersion:   model.ExecutableRecordingScriptBundleSchemaVersion,
		Status:          model.ExecutableScriptBundleStatusValidated,
		ScriptManifest: model.ExecutableScriptManifest{
			ScriptID:            "recording_full_pipeline_smoke",
			Version:             1,
			Language:            "typescript",
			Runtime:             "playwright-restricted-sandbox",
			EntryFunction:       "runCascadeRecording",
			Generator:           "full_pipeline_smoke_test",
			GeneratorVersion:    "0.1.0",
			DependencyAllowlist: []string{},
			ContextAPIs:         []string{"ctx.page", "ctx.log"},
			StepNodeIDs:         []string{"node_open", "node_fill", "node_click", "node_assert"},
		},
		PlanJSON:         doc,
		PlaywrightScript: model.ExecutableScriptSource{InlineSource: source, MimeType: "text/typescript", SHA256: scriptHash, SizeBytes: int64(len(source))},
		ApprovalMarkdown: model.ApprovalMarkdownDocument{InlineMarkdown: markdown, MimeType: "text/markdown", SHA256: markdownHash, SizeBytes: int64(len(markdown))},
		SecurityPolicy: model.ExecutableScriptSecurityPolicy{
			AllowedDomains:       []string{host},
			Redactions:           model.RedactionPolicy{MaskSelectors: []string{"[data-sensitive]"}},
			AllowedContextAPIs:   []string{"ctx.page", "ctx.log"},
			AllowedPageMethods:   []string{"goto", "fill", "click"},
			ForbiddenIdentifiers: []string{"import", "require", "eval", "process", "fetch"},
			NetworkPolicy:        "allowed_domains_only_via_ctx_page",
			FileSystemPolicy:     "no_direct_fs_access",
		},
		Reproducibility: model.ExecutableScriptReproducibility{
			PlanHashSHA256:     planHash,
			ScriptHashSHA256:   scriptHash,
			MarkdownHashSHA256: markdownHash,
			GraphHashSHA256:    graphDigest,
		},
		Validation: &model.ExecutableScriptValidation{Valid: true, ValidatedAt: now},
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	bundleHash, err := bundle.ComputeBundleHash()
	if err != nil {
		t.Fatal(err)
	}
	bundle.Reproducibility.BundleHashSHA256 = bundleHash

	pkg := model.ClientExecutionPackage{
		PackageID:              "pkg_full_pipeline_smoke",
		OrgID:                  "org_smoke",
		ProjectID:              projectID,
		SchemaVersion:          model.ClientExecutionPackageSchemaVersion,
		CreatedAt:              now,
		ApprovedAt:             now,
		ProjectContextSummary:  model.ProjectContextSummary{ContextID: "ctx_full_pipeline_smoke", SchemaVersion: model.ProjectContextSchemaVersion, Mode: model.AppModeWeb, ProductURL: baseURL, TargetAudience: "internal smoke test"},
		ProductMapSummary:      model.ProductMapSummary{ProductMapID: "map_full_pipeline_smoke", Version: 1, Summary: "Local product flow with email capture and confirmation."},
		WorkflowGraph:          graph,
		RecordingRunSpec:       runSpec,
		ExecutableScriptBundle: bundle,
		EvidenceBundle:         model.EvidenceBundle{EvidenceRefs: []model.EvidenceRef{{ID: "ev_full_pipeline_requirements", Kind: model.EvidenceKindRequirementDoc}}},
		Reproducibility:        model.ReproducibilitySpec{GraphHashSHA256: graphDigest, ScriptHashSHA256: planHash},
		SafetyReport: model.PackageSafetyReport{
			AllowedToUpload: true,
			UploadMode:      "structure_summary_only",
			HumanApproval: model.UserApprovalRecord{
				ApprovalID:       "approval_full_pipeline_smoke",
				ApprovedByUserID: "user_smoke",
				ApprovedAt:       now,
				PlanDigestSHA256: graphDigest,
				ReviewedNodeIDs:  []string{"node_open", "node_fill", "node_click", "node_assert"},
			},
			PIIHandling: "synthetic_test_data_only",
		},
	}
	if err := model.ValidateClientExecutionPackageForCloudExecution(&pkg); err != nil {
		t.Fatal(err)
	}
	return pkg
}

func graphNode(id string, title string, actionType model.GraphActionType, target model.ActionTarget, expected string, screenshot bool, durationMS int) *model.GraphNode {
	return &model.GraphNode{
		ID:              id,
		Type:            model.GraphNodeTypeAction,
		Title:           title,
		Action:          string(actionType),
		Selector:        target.Selector,
		ExpectedOutcome: expected,
		IsScreenshot:    screenshot,
		HasZoom:         actionType == model.GraphActionClick || actionType == model.GraphActionFill,
		RetryPolicy:     1,
		ActionSpec:      &model.GraphAction{Type: actionType, Target: target, TimeoutMS: 10000, WaitUntil: waitUntilForAction(actionType)},
		Capture:         &model.CaptureSpec{Screenshot: screenshot, Video: true, Zoom: actionType == model.GraphActionClick || actionType == model.GraphActionFill, FocusSelector: target.Selector, AssetRole: "primary"},
		DurationHintMS:  durationMS,
	}
}

func scriptStep(id string, order int, nodeID string, pageURL string, actionType model.GraphActionType, target model.ActionTarget, value string, expected string, durationMS int) model.ScriptStep {
	return model.ScriptStep{
		ID:              id,
		Order:           order,
		NodeID:          nodeID,
		Title:           strings.TrimPrefix(id, "step_"),
		PageTarget:      model.ScriptPageTarget{URL: pageURL, Selector: target.Selector},
		Action:          model.ScriptActionInstruction{Type: actionType, Target: target, Value: value, TimeoutMS: 10000, WaitUntil: waitUntilForAction(actionType)},
		ExpectedOutcome: expected,
		Capture:         model.CaptureSpec{Screenshot: true, Video: true, Zoom: actionType == model.GraphActionClick || actionType == model.GraphActionFill, FocusSelector: target.Selector, AssetRole: "primary"},
		Timing:          model.NodeTimingHint{NodeID: nodeID, DurationMS: durationMS},
		Narrative:       model.NarrativeCue{Title: expected, Caption: expected},
		Blocking:        true,
	}
}

func waitUntilForAction(actionType model.GraphActionType) string {
	if actionType == model.GraphActionNavigate {
		return "domcontentloaded"
	}
	return ""
}

func countArtifacts(artifacts []model.ArtifactRef, kind string) int {
	count := 0
	for _, artifact := range artifacts {
		if artifact.Kind == kind {
			count++
		}
	}
	return count
}

func findArtifact(artifacts []model.ArtifactRef, kind string) *model.ArtifactRef {
	for index := range artifacts {
		if artifacts[index].Kind == kind {
			return &artifacts[index]
		}
	}
	return nil
}

func findTimelineArtifact(artifacts []model.TimelineArtifact, kind string) *model.TimelineArtifact {
	for index := range artifacts {
		if artifacts[index].Kind == kind {
			return &artifacts[index]
		}
	}
	return nil
}

func readRenderManifest(t *testing.T, value string) map[string]any {
	t.Helper()
	filePath, err := localFilePath(value)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func requireLocalFile(value string) error {
	filePath, err := localFilePath(value)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filePath); err != nil {
		return fmt.Errorf("expected file %s to exist: %w", filePath, err)
	}
	return nil
}

func localFilePath(value string) (string, error) {
	filePath := value
	if strings.HasPrefix(value, "file://") {
		parsed, err := url.Parse(value)
		if err != nil {
			return "", err
		}
		filePath = parsed.Path
		if len(filePath) >= 3 && filePath[0] == '/' && filePath[2] == ':' {
			filePath = filePath[1:]
		}
		if parsed.Host != "" {
			filePath = `\\` + parsed.Host + filePath
		}
	}
	filePath = filepath.FromSlash(filePath)
	return filePath, nil
}

const fullPipelineSmokeHTML = `<!doctype html>
<html>
  <head>
    <meta charset="utf-8" />
    <title>Cascade Full Pipeline Smoke</title>
    <style>
      body { margin: 0; font-family: Arial, sans-serif; background: #f5f7fb; color: #172033; }
      main { width: 760px; margin: 56px auto; background: white; border: 1px solid #dde3ee; border-radius: 8px; padding: 28px; }
      label, input, button { display: block; font-size: 16px; }
      input { width: 320px; padding: 10px; margin: 8px 0 16px; border: 1px solid #aab4c4; border-radius: 6px; }
      button { padding: 10px 14px; border: 0; border-radius: 6px; color: white; background: #1769e0; cursor: pointer; }
      #status { margin-top: 18px; padding: 12px; border-radius: 6px; background: #eef2f7; }
      #status.done { color: #0f6b3a; background: #e7f7ed; }
    </style>
  </head>
  <body>
    <main>
      <h1>Product demo workspace</h1>
      <p>Run one deterministic product interaction for AIGC recording.</p>
      <label for="email">Demo email</label>
      <input id="email" autocomplete="off" />
      <button id="start" type="button">Start demo</button>
      <div id="status">Waiting for input</div>
    </main>
    <script>
      const button = document.getElementById("start");
      const email = document.getElementById("email");
      const status = document.getElementById("status");
      button.addEventListener("click", () => {
        status.className = "done";
        status.textContent = "Demo ready for " + (email.value || "visitor");
      });
    </script>
  </body>
</html>`
