package executor

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestNewRecordRequestFromClientExecutionPackageUsesProtocolFields(t *testing.T) {
	pkg := sampleClientExecutionPackageForExecutorTest(t)

	request, err := NewRecordRequestFromClientExecutionPackage(&pkg, "artifacts/recording/job_1")
	if err != nil {
		t.Fatal(err)
	}
	if request.Graph != pkg.WorkflowGraph {
		t.Fatal("record request should use the approved workflow graph from the client package")
	}
	if request.SourcePackageID != pkg.PackageID || request.ExecutableScriptBundle != pkg.ExecutableScriptBundle {
		t.Fatalf("record request did not retain protocol package references: %+v", request)
	}
	if request.Viewport.Width != 1920 || request.Viewport.Height != 1080 {
		t.Fatalf("record request should prefer recording output resolution, got %+v", request.Viewport)
	}
	if request.RecordingMode != RecordingModePlaywright {
		t.Fatalf("record request should default to real Playwright recording, got %q", request.RecordingMode)
	}
	if !request.Headless || request.RecordingRunSpec == nil || request.RecordingRunSpec.BaseURL != "https://app.example.com" {
		t.Fatalf("record request did not carry run spec: %+v", request)
	}
}

func TestNewRecordRequestFromClientExecutionPackageRejectsInvalidProtocolPackage(t *testing.T) {
	pkg := sampleClientExecutionPackageForExecutorTest(t)
	pkg.ExecutableScriptBundle = nil

	if _, err := NewRecordRequestFromClientExecutionPackage(&pkg, "artifacts/recording/job_1"); err == nil {
		t.Fatal("expected invalid package to be rejected")
	}
}

func TestRecordRequestJSONIncludesRecordingMode(t *testing.T) {
	data, err := json.Marshal(RecordRequest{RecordingMode: RecordingModePlaywright})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"recording_mode":"playwright"`) {
		t.Fatalf("recording mode must be serialized for video-worker: %s", data)
	}
}

func TestNewRecordingResultPackageFromRecordResultIsRenderable(t *testing.T) {
	pkg := sampleClientExecutionPackageForExecutorTest(t)
	completedAt := time.Date(2026, 7, 9, 19, 0, 0, 0, time.UTC)
	recordResult := RecordResult{
		RecordingPath:   "artifacts/recording/job_1/recording.webm",
		ScreenshotPaths: []string{"artifacts/recording/job_1/step-001.png"},
		TracePath:       "artifacts/recording/job_1/trace.zip",
		WorkerID:        "worker_1",
		RuntimeVersions: map[string]string{"runner": "playwright-restricted-sandbox-stub"},
		StartedAt:       completedAt.Add(-2 * time.Second),
		CompletedAt:     completedAt,
	}

	result, err := NewRecordingResultPackageFromRecordResult(&pkg, recordResult, "job_1", completedAt)
	if err != nil {
		t.Fatal(err)
	}
	if result.SourcePackageID != pkg.PackageID || result.CloudJobID != "job_1" {
		t.Fatalf("result package identity mismatch: %+v", result)
	}
	if result.ExecutionTrace == nil || result.ExecutionTrace.WorkflowGraphID != pkg.WorkflowGraph.ID || result.ExecutionTrace.PassRate != 1 {
		t.Fatalf("unexpected execution trace: %+v", result.ExecutionTrace)
	}
	if len(result.GeneratedAssets) != 3 {
		t.Fatalf("expected raw recording, screenshot, and trace artifacts, got %+v", result.GeneratedAssets)
	}
	if len(result.StepResults) != 1 || result.StepResults[0].NodeID != "node_start" || len(result.StepResults[0].Artifacts) != 1 {
		t.Fatalf("expected synthesized step result with screenshot artifact, got %+v", result.StepResults)
	}

	renderRequest, err := NewRenderRequestFromRecordingResult(&pkg, &result, "artifacts/render/job_1")
	if err != nil {
		t.Fatal(err)
	}
	if renderRequest.RecordingResultPackage != &result || renderRequest.ExecutionTrace != result.ExecutionTrace {
		t.Fatalf("render request should consume the recording result package: %+v", renderRequest)
	}
}

func TestNewRecordingResultPackageFromRecordResultDedupesWorkerGeneratedAssets(t *testing.T) {
	pkg := sampleClientExecutionPackageForExecutorTest(t)
	completedAt := time.Date(2026, 7, 9, 19, 30, 0, 0, time.UTC)
	recordingPath := "artifacts/recording/job_1/recording.webm"
	screenshotPath := "artifacts/recording/job_1/step-001.png"
	tracePath := "artifacts/recording/job_1/trace.zip"
	recordResult := RecordResult{
		RecordingPath:   recordingPath,
		ScreenshotPaths: []string{screenshotPath},
		TracePath:       tracePath,
		GeneratedAssets: []model.ArtifactRef{
			{ID: "artifact_raw_recording", Kind: "raw_recording", URI: recordingPath, MimeType: "video/webm", SHA256: "raw_hash", SizeBytes: 10, CreatedAt: completedAt},
			{ID: "artifact_screenshot_001", Kind: "screenshot", URI: screenshotPath, MimeType: "image/png", SHA256: "screenshot_hash", SizeBytes: 20, CreatedAt: completedAt, SourceNodeID: "node_start", Metadata: map[string]any{"asset_role": "primary", "capture_scope": "viewport", "include_in_demo": true}},
			{ID: "artifact_browser_trace", Kind: "browser_trace", URI: tracePath, MimeType: "application/zip", SHA256: "trace_hash", SizeBytes: 30, CreatedAt: completedAt},
		},
		StepResults: []model.StepResult{{NodeID: "node_start", Status: "passed"}},
		StartedAt:   completedAt.Add(-2 * time.Second),
		CompletedAt: completedAt,
	}

	result, err := NewRecordingResultPackageFromRecordResult(&pkg, recordResult, "job_1", completedAt)
	if err != nil {
		t.Fatal(err)
	}
	if got := countArtifactsByKind(result.GeneratedAssets, "raw_recording"); got != 1 {
		t.Fatalf("expected one raw recording artifact, got %d: %+v", got, result.GeneratedAssets)
	}
	if got := countArtifactsByKind(result.GeneratedAssets, "screenshot"); got != 1 {
		t.Fatalf("expected one screenshot artifact, got %d: %+v", got, result.GeneratedAssets)
	}
	if got := countArtifactsByKind(result.GeneratedAssets, "browser_trace"); got != 1 {
		t.Fatalf("expected one trace artifact, got %d: %+v", got, result.GeneratedAssets)
	}
	if len(result.StepResults) != 1 || len(result.StepResults[0].Artifacts) != 1 || result.StepResults[0].Artifacts[0].SHA256 != "screenshot_hash" {
		t.Fatalf("expected step result to keep worker screenshot metadata, got %+v", result.StepResults)
	}
	if result.StepResults[0].Artifacts[0].Metadata["capture_scope"] != "viewport" {
		t.Fatalf("expected step artifact metadata to be preserved, got %+v", result.StepResults[0].Artifacts[0].Metadata)
	}
	var screenshotDescriptor *model.PackageArtifactDescriptor
	for index := range result.Delivery.AssetRefs {
		if result.Delivery.AssetRefs[index].ID == "artifact_screenshot_001" {
			screenshotDescriptor = &result.Delivery.AssetRefs[index]
			break
		}
	}
	if screenshotDescriptor == nil || screenshotDescriptor.Metadata["asset_role"] != "primary" || screenshotDescriptor.Metadata["source_node_id"] != "node_start" {
		t.Fatalf("expected delivery descriptor to preserve screenshot metadata, got %+v", screenshotDescriptor)
	}
}

func TestNewRecordingResultPackageFromRecordResultDoesNotDuplicateWorkerTraceOrManifest(t *testing.T) {
	pkg := sampleClientExecutionPackageForExecutorTest(t)
	completedAt := time.Date(2026, 7, 9, 19, 45, 0, 0, time.UTC)
	tracePath := filepath.Join("artifacts", "recording", "job_1", "trace.zip")
	manifestPath := filepath.Join("artifacts", "recording", "job_1", "recording_artifact_manifest.json")
	traceURI := (&url.URL{Scheme: "file", Path: filepath.ToSlash(tracePath)}).String()
	manifestURI := (&url.URL{Scheme: "file", Path: filepath.ToSlash(manifestPath)}).String()
	recordResult := RecordResult{
		RecordingPath:        "artifacts/recording/job_1/recording.webm",
		TracePath:            tracePath,
		ArtifactManifestPath: manifestPath,
		GeneratedAssets:      []model.ArtifactRef{{ID: "artifact_browser_trace", Kind: "browser_trace", URI: traceURI}, {ID: "artifact_manifest", Kind: "artifact_manifest", URI: manifestURI}},
		StepResults:          []model.StepResult{{NodeID: "node_start", Status: "passed"}},
		StartedAt:            completedAt.Add(-2 * time.Second),
		CompletedAt:          completedAt,
	}

	result, err := NewRecordingResultPackageFromRecordResult(&pkg, recordResult, "job_1", completedAt)
	if err != nil {
		t.Fatal(err)
	}
	if got := countArtifactsByKind(result.GeneratedAssets, "browser_trace"); got != 1 {
		t.Fatalf("expected one browser trace artifact, got %d: %+v", got, result.GeneratedAssets)
	}
	if got := countArtifactsByKind(result.GeneratedAssets, "artifact_manifest"); got != 1 {
		t.Fatalf("expected one artifact manifest, got %d: %+v", got, result.GeneratedAssets)
	}
}

func TestNormalizedArtifactURIMatchesFileURIAndPath(t *testing.T) {
	path := filepath.Join("artifacts", "recording", "job_1", "trace.zip")
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	fileURI := (&url.URL{Scheme: "file", Path: filepath.ToSlash(absolutePath)}).String()
	if normalizedArtifactURI(absolutePath) != normalizedArtifactURI(fileURI) {
		t.Fatalf("expected file URI and path to normalize equally: %q vs %q", normalizedArtifactURI(absolutePath), normalizedArtifactURI(fileURI))
	}
}

func countArtifactsByKind(artifacts []model.ArtifactRef, kind string) int {
	count := 0
	for _, artifact := range artifacts {
		if artifact.Kind == kind {
			count++
		}
	}
	return count
}

func sampleClientExecutionPackageForExecutorTest(t *testing.T) model.ClientExecutionPackage {
	t.Helper()
	now := time.Date(2026, 7, 9, 18, 0, 0, 0, time.UTC)
	graph := model.NewDemoWorkflowGraph("graph_1", "project_1", "https://app.example.com/dashboard")
	graph.Status = model.GraphStatusApproved
	graph.Nodes = []*model.GraphNode{{
		ID:              "node_start",
		Type:            model.GraphNodeTypeAction,
		Title:           "Open dashboard",
		ExpectedOutcome: "Dashboard loads",
		ActionSpec:      &model.GraphAction{Type: model.GraphActionNavigate, Target: model.ActionTarget{URL: "https://app.example.com/dashboard"}},
		Capture:         &model.CaptureSpec{Screenshot: true, Video: true},
	}}
	graph.Edges = []*model.GraphEdge{}
	graphDigest, err := model.DigestCanonicalJSON(graph)
	if err != nil {
		t.Fatal(err)
	}

	runSpec := model.RecordingRunSpec{
		RunID:          "run_1",
		BaseURL:        "https://app.example.com",
		AllowedDomains: []string{"app.example.com"},
		Browser: model.BrowserRunSpec{
			Engine:    "chromium",
			Headless:  true,
			Viewports: []model.ViewportSpec{{Name: "desktop", Width: 1440, Height: 900, Device: "desktop"}},
		},
		Timeline:      model.RecordingTimeline{TargetDurationSec: 30},
		Outputs:       model.RecordingOutputRequest{RawRecording: true, FinalVideo: true, ScreenshotPack: true, StepByStepDocs: true, Trace: true, ResolutionWidth: 1920, ResolutionHeight: 1080},
		Redactions:    model.RedactionPolicy{MaskSelectors: []string{"[data-sensitive]"}},
		FailurePolicy: model.RecordingFailurePolicy{RetryAttempts: 1, SelectorRepairAllowed: true},
		Environment:   map[string]string{"locale": "en-US"},
	}
	doc := &model.ExecutionScriptDocument{
		ID:               "script_1",
		ProjectID:        "project_1",
		WorkflowGraphID:  graph.ID,
		GraphVersion:     graph.Version,
		SchemaVersion:    model.ExecutionScriptDocumentSchemaVersion,
		Status:           model.ScriptDocumentStatusApproved,
		WorkflowGraph:    graph,
		RecordingRunSpec: runSpec,
		Steps: []model.ScriptStep{{
			ID:              "step_1",
			Order:           1,
			NodeID:          "node_start",
			PageTarget:      model.ScriptPageTarget{URL: "https://app.example.com/dashboard"},
			Action:          model.ScriptActionInstruction{Type: model.GraphActionNavigate, Target: model.ActionTarget{URL: "https://app.example.com/dashboard"}},
			ExpectedOutcome: "Dashboard loads",
			Capture:         model.CaptureSpec{Screenshot: true, Video: true},
			Timing:          model.NodeTimingHint{NodeID: "node_start", DurationMS: 1200},
			Narrative:       model.NarrativeCue{Title: "Open dashboard"},
			Blocking:        true,
		}},
		SafetyPolicy:      model.ScriptSafetyPolicy{AllowedDomains: []string{"app.example.com"}, Redactions: model.RedactionPolicy{MaskSelectors: []string{"[data-sensitive]"}}},
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

	source := `type CascadeRecordingContext = { page: any; secrets: any; capture: any; assert: any; log: any };
type CascadeRecordingResult = { ok: boolean };
export async function runCascadeRecording(ctx: CascadeRecordingContext): Promise<CascadeRecordingResult> {
  await ctx.log.step("node_start", "Open dashboard");
  await ctx.page.goto("https://app.example.com/dashboard");
  return { ok: true };
}`
	if !strings.Contains(source, `"node_start"`) {
		t.Fatal("test script must bind to node_start")
	}
	markdown := "# Approval\n\n1. Open dashboard"
	scriptHash := model.SHA256Hex([]byte(source))
	markdownHash := model.SHA256Hex([]byte(markdown))
	bundle := &model.ExecutableRecordingScriptBundle{
		ID:              "bundle_1",
		ProjectID:       "project_1",
		WorkflowGraphID: graph.ID,
		SchemaVersion:   model.ExecutableRecordingScriptBundleSchemaVersion,
		Status:          model.ExecutableScriptBundleStatusValidated,
		ScriptManifest: model.ExecutableScriptManifest{
			ScriptID:            "recording_graph_1",
			Version:             1,
			Language:            "typescript",
			Runtime:             "playwright-restricted-sandbox",
			EntryFunction:       "runCascadeRecording",
			Generator:           "test",
			GeneratorVersion:    "0.1.0",
			DependencyAllowlist: []string{},
			ContextAPIs:         []string{"ctx.page", "ctx.log"},
			StepNodeIDs:         []string{"node_start"},
		},
		PlanJSON:         doc,
		PlaywrightScript: model.ExecutableScriptSource{InlineSource: source, MimeType: "text/typescript", SHA256: scriptHash, SizeBytes: int64(len(source))},
		ApprovalMarkdown: model.ApprovalMarkdownDocument{InlineMarkdown: markdown, MimeType: "text/markdown", SHA256: markdownHash, SizeBytes: int64(len(markdown))},
		SecurityPolicy: model.ExecutableScriptSecurityPolicy{
			AllowedDomains:       []string{"app.example.com"},
			Redactions:           model.RedactionPolicy{MaskSelectors: []string{"[data-sensitive]"}},
			AllowedContextAPIs:   []string{"ctx.page", "ctx.log"},
			AllowedPageMethods:   []string{"goto"},
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

	return model.ClientExecutionPackage{
		PackageID:              "pkg_1",
		OrgID:                  "org_1",
		ProjectID:              "project_1",
		SchemaVersion:          model.ClientExecutionPackageSchemaVersion,
		CreatedAt:              now,
		ApprovedAt:             now,
		ProjectContextSummary:  model.ProjectContextSummary{ContextID: "ctx_1", SchemaVersion: model.ProjectContextSchemaVersion, Mode: model.AppModeWeb, ProductURL: "https://app.example.com", TargetAudience: "sales"},
		ProductMapSummary:      model.ProductMapSummary{ProductMapID: "map_1", Version: 1, Summary: "Dashboard flow"},
		WorkflowGraph:          graph,
		RecordingRunSpec:       runSpec,
		ExecutableScriptBundle: bundle,
		EvidenceBundle:         model.EvidenceBundle{EvidenceRefs: []model.EvidenceRef{{ID: "ev_1", Kind: model.EvidenceKindRequirementDoc}}},
		Reproducibility:        model.ReproducibilitySpec{GraphHashSHA256: graphDigest, ScriptHashSHA256: planHash},
		SafetyReport: model.PackageSafetyReport{
			AllowedToUpload: true,
			UploadMode:      "structure_summary_only",
			HumanApproval: model.UserApprovalRecord{
				ApprovalID:       "approval_1",
				ApprovedByUserID: "user_1",
				ApprovedAt:       now,
				PlanDigestSHA256: graphDigest,
				ReviewedNodeIDs:  []string{"node_start"},
			},
		},
	}
}
