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
	if request.SourcePackageID != pkg.PackageID || request.ExecutableScriptBundle == nil || request.ExecutableScriptBundle.ID != pkg.ExecutableScriptBundle.ID {
		t.Fatalf("record request did not retain protocol package references: %+v", request)
	}
	if request.ExecutableScriptBundle.PlanJSON == pkg.ExecutableScriptBundle.PlanJSON {
		t.Fatal("record request should use an execution-normalized plan copy when graph screenshot requirements are present")
	}
	if got := request.ExecutableScriptBundle.PlanJSON.Steps[0].Capture; !got.Screenshot || got.Dedupe == nil || *got.Dedupe {
		t.Fatalf("graph-required screenshots should disable step screenshot dedupe for worker execution: %+v", got)
	}
	if pkg.ExecutableScriptBundle.PlanJSON.Steps[0].Capture.Dedupe != nil {
		t.Fatalf("record request normalization must not mutate the source package: %+v", pkg.ExecutableScriptBundle.PlanJSON.Steps[0].Capture)
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
	if request.SandboxPolicy == nil || request.SandboxPolicy.PolicyHashSHA256 == "" || request.SandboxPolicy.IsolationMode != model.SandboxIsolationContainer {
		t.Fatalf("record request did not carry resolved sandbox policy: %+v", request.SandboxPolicy)
	}
}

func TestNewRecordRequestFromClientExecutionPackageRestoresGraphRequiredStepScreenshot(t *testing.T) {
	pkg := sampleClientExecutionPackageForExecutorTest(t)
	pkg.WorkflowGraph.Nodes = append(pkg.WorkflowGraph.Nodes, &model.GraphNode{
		ID:              "node_hold",
		Type:            model.GraphNodeTypeCapture,
		Action:          string(model.GraphActionWait),
		Title:           "Hold product page",
		ExpectedOutcome: "Product page remains visible",
		IsScreenshot:    true,
		Capture:         &model.CaptureSpec{Screenshot: true, Video: true, AssetRole: "primary"},
	})
	pkg.ExecutableScriptBundle.ScriptManifest.StepNodeIDs = append(pkg.ExecutableScriptBundle.ScriptManifest.StepNodeIDs, "node_hold")
	pkg.ExecutableScriptBundle.PlanJSON.Steps = append(pkg.ExecutableScriptBundle.PlanJSON.Steps, model.ScriptStep{
		ID:              "step_2",
		Order:           2,
		NodeID:          "node_hold",
		Action:          model.ScriptActionInstruction{Type: model.GraphActionWait},
		ExpectedOutcome: "Product page remains visible",
		Capture:         model.CaptureSpec{Screenshot: false, Video: true},
		Timing:          model.NodeTimingHint{NodeID: "node_hold", DurationMS: 1200},
		Blocking:        true,
	})
	pkg.ExecutableScriptBundle.PlaywrightScript.InlineSource += "\n// \"node_hold\"\n"
	refreshTestPackageDigests(t, &pkg)

	request, err := NewRecordRequestFromClientExecutionPackage(&pkg, "artifacts/recording/job_1")
	if err != nil {
		t.Fatal(err)
	}

	var holdStep *model.ScriptStep
	for index := range request.ExecutableScriptBundle.PlanJSON.Steps {
		if request.ExecutableScriptBundle.PlanJSON.Steps[index].NodeID == "node_hold" {
			holdStep = &request.ExecutableScriptBundle.PlanJSON.Steps[index]
			break
		}
	}
	if holdStep == nil {
		t.Fatalf("normalized plan lost node_hold: %+v", request.ExecutableScriptBundle.PlanJSON.Steps)
	}
	if !holdStep.Capture.Screenshot || holdStep.Capture.Dedupe == nil || *holdStep.Capture.Dedupe || holdStep.Capture.AssetRole != "primary" {
		t.Fatalf("graph-required screenshot was not restored for worker execution: %+v", holdStep.Capture)
	}
	if pkg.ExecutableScriptBundle.PlanJSON.Steps[1].Capture.Screenshot || pkg.ExecutableScriptBundle.PlanJSON.Steps[1].Capture.Dedupe != nil {
		t.Fatalf("source package should remain unchanged: %+v", pkg.ExecutableScriptBundle.PlanJSON.Steps[1].Capture)
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
	data, err := json.Marshal(RecordRequest{RecordingMode: RecordingModePlaywright, SandboxPolicy: &model.SandboxPolicy{Profile: model.SandboxProfileDev}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"recording_mode":"playwright"`) {
		t.Fatalf("recording mode must be serialized for video-worker: %s", data)
	}
	if !strings.Contains(string(data), `"sandbox_policy"`) {
		t.Fatalf("sandbox policy must be serialized for video-worker: %s", data)
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
	if result.ExecutionTrace.Sandbox == nil || result.ExecutionTrace.Sandbox.PolicyHashSHA256 == "" || result.AuditTrail.Sandbox == nil {
		t.Fatalf("expected sandbox metadata in trace and audit trail: trace=%+v audit=%+v", result.ExecutionTrace.Sandbox, result.AuditTrail.Sandbox)
	}
	if result.AuditTrail.Sandbox.PolicyHashSHA256 != result.ExecutionTrace.Sandbox.PolicyHashSHA256 {
		t.Fatalf("sandbox policy hash mismatch between trace and audit trail: %+v vs %+v", result.ExecutionTrace.Sandbox, result.AuditTrail.Sandbox)
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

func TestNewRecordingResultPackageFromRecordResultBuildsFailureDiagnostic(t *testing.T) {
	pkg := sampleClientExecutionPackageForExecutorTest(t)
	completedAt := time.Date(2026, 7, 9, 20, 30, 0, 0, time.UTC)
	failureScreenshot := model.ArtifactRef{
		ID:           "artifact_failure_screenshot_001",
		Kind:         "failure_screenshot",
		URI:          "file:///tmp/failure-step-001.png",
		MimeType:     "image/png",
		SHA256:       "failure_hash",
		SizeBytes:    20,
		CreatedAt:    completedAt,
		Sensitive:    true,
		SourceNodeID: "node_start",
		Metadata:     map[string]any{"asset_role": "failure_screenshot", "include_in_demo": false},
	}
	trace := model.ArtifactRef{ID: "artifact_browser_trace", Kind: "browser_trace", URI: "file:///tmp/trace.zip", MimeType: "application/zip", SHA256: "trace_hash", SizeBytes: 30, CreatedAt: completedAt}
	recordResult := RecordResult{
		TracePath:       "file:///tmp/trace.zip",
		GeneratedAssets: []model.ArtifactRef{failureScreenshot, trace},
		StepResults: []model.StepResult{{
			NodeID:        "node_start",
			Status:        "failed",
			DurationMS:    1000,
			ObservedState: "Timeout waiting for selector",
		}},
		FailureDiagnostic: &model.ScriptFailureDiagnostic{
			ID:              "diag_node_start",
			SchemaVersion:   model.ScriptFailureDiagnosticSchemaVersion,
			FailedNodeID:    "node_start",
			FailedStepOrder: 1,
			Error:           model.AgentError{Code: "selector_timeout", Message: "Timeout waiting for selector", Retryable: true},
			CurrentURL:      "https://app.example.com/dashboard",
			PageTitle:       "Dashboard",
			ScreenshotRefs:  []model.PackageArtifactDescriptor{localDiagnosticArtifactDescriptor(&pkg, failureScreenshot, "failure_screenshot")},
			TraceRefs:       []model.PackageArtifactDescriptor{localDiagnosticArtifactDescriptor(&pkg, trace, "failure_trace")},
			RedactionReport: model.DiagnosticRedactionReport{Applied: true, FullHTMLIncluded: false},
			CapturedAt:      completedAt,
		},
		StartedAt:   completedAt.Add(-time.Second),
		CompletedAt: completedAt,
	}

	result, err := NewRecordingResultPackageFromRecordResult(&pkg, recordResult, "job_1", completedAt)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != model.RecordingResultStatusFailed || result.FailureDiagnostic == nil || result.RepairRequest == nil {
		t.Fatalf("expected failed result with diagnostic and repair request, got %+v", result)
	}
	if result.FailureDiagnostic.FailedNodeID != "node_start" || result.FailureDiagnostic.CurrentURL == "" {
		t.Fatalf("unexpected failure diagnostic: %+v", result.FailureDiagnostic)
	}
	if len(result.FailureDiagnostic.ScreenshotRefs) != 1 || !result.FailureDiagnostic.ScreenshotRefs[0].Encrypted || !result.FailureDiagnostic.ScreenshotRefs[0].Sensitive || result.FailureDiagnostic.ScreenshotRefs[0].RecipientKeyID == "" {
		t.Fatalf("failure screenshot descriptor must be encrypted, sensitive, and recipient-bound: %+v", result.FailureDiagnostic.ScreenshotRefs)
	}
	if len(result.FailureDiagnostic.TraceRefs) != 1 || !result.FailureDiagnostic.TraceRefs[0].Encrypted || !result.FailureDiagnostic.TraceRefs[0].Sensitive || result.FailureDiagnostic.TraceRefs[0].RecipientKeyID == "" {
		t.Fatalf("failure trace descriptor must be encrypted, sensitive, and recipient-bound: %+v", result.FailureDiagnostic.TraceRefs)
	}
	if result.RepairRequest.SourceResultID != result.ResultID || !result.RepairRequest.ApprovalRequired {
		t.Fatalf("unexpected repair request: %+v", result.RepairRequest)
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

func refreshTestPackageDigests(t *testing.T, pkg *model.ClientExecutionPackage) {
	t.Helper()
	graphDigest, err := model.DigestCanonicalJSON(pkg.WorkflowGraph)
	if err != nil {
		t.Fatal(err)
	}
	pkg.ExecutableScriptBundle.PlanJSON.Reproducibility.GraphHashSHA256 = graphDigest
	planHash, err := pkg.ExecutableScriptBundle.PlanJSON.ComputeScriptHash()
	if err != nil {
		t.Fatal(err)
	}
	pkg.ExecutableScriptBundle.PlanJSON.Reproducibility.ScriptHashSHA256 = planHash
	source := pkg.ExecutableScriptBundle.PlaywrightScript.InlineSource
	scriptHash := model.SHA256Hex([]byte(source))
	pkg.ExecutableScriptBundle.PlaywrightScript.SHA256 = scriptHash
	pkg.ExecutableScriptBundle.PlaywrightScript.SizeBytes = int64(len(source))
	pkg.ExecutableScriptBundle.Reproducibility.PlanHashSHA256 = planHash
	pkg.ExecutableScriptBundle.Reproducibility.ScriptHashSHA256 = scriptHash
	pkg.ExecutableScriptBundle.Reproducibility.GraphHashSHA256 = graphDigest
	pkg.ExecutableScriptBundle.Reproducibility.BundleHashSHA256 = ""
	bundleHash, err := pkg.ExecutableScriptBundle.ComputeBundleHash()
	if err != nil {
		t.Fatal(err)
	}
	pkg.ExecutableScriptBundle.Reproducibility.BundleHashSHA256 = bundleHash
	pkg.Reproducibility.GraphHashSHA256 = graphDigest
	pkg.Reproducibility.ScriptHashSHA256 = planHash
	pkg.SafetyReport.HumanApproval.PlanDigestSHA256 = graphDigest
}
