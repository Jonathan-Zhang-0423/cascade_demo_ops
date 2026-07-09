package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestExecutableRecordingScriptBundleJSONRoundTrip(t *testing.T) {
	doc := minimalExecutionScriptDocumentForBundleTest()
	source := `type CascadeRecordingContext = { page: any; secrets: any; capture: any; assert: any; log: any };
type CascadeRecordingResult = { ok: boolean };
export async function runCascadeRecording(ctx: CascadeRecordingContext): Promise<CascadeRecordingResult> {
  await ctx.log.step("node_1", "打开页面");
  return { ok: true };
}`
	sourceHash := SHA256Hex([]byte(source))
	bundle := &ExecutableRecordingScriptBundle{
		ID:              "bundle_script_1",
		ProjectID:       "project_1",
		WorkflowGraphID: "graph_1",
		SchemaVersion:   ExecutableRecordingScriptBundleSchemaVersion,
		Status:          ExecutableScriptBundleStatusReviewReady,
		ScriptManifest: ExecutableScriptManifest{
			ScriptID:            "recording_graph_1",
			Version:             1,
			Language:            "typescript",
			Runtime:             "playwright-restricted-sandbox",
			EntryFunction:       "runCascadeRecording",
			Generator:           "test_generator",
			GeneratorVersion:    "0.1.0",
			DependencyAllowlist: []string{},
			ContextAPIs:         []string{"ctx.page", "ctx.secrets", "ctx.capture", "ctx.assert", "ctx.log"},
			StepNodeIDs:         []string{"node_1"},
		},
		PlanJSON:         doc,
		PlaywrightScript: ExecutableScriptSource{InlineSource: source, MimeType: "text/typescript", SHA256: sourceHash, SizeBytes: int64(len(source))},
		ApprovalMarkdown: ApprovalMarkdownDocument{InlineMarkdown: "# 审批", MimeType: "text/markdown", SHA256: SHA256Hex([]byte("# 审批"))},
		SecurityPolicy: ExecutableScriptSecurityPolicy{
			AllowedDomains:       []string{"app.example.com"},
			ForbiddenPages:       []string{"/billing"},
			ForbiddenData:        []string{"api_key"},
			Redactions:           RedactionPolicy{MaskSelectors: []string{"[data-sensitive]"}},
			AllowedContextAPIs:   []string{"ctx.page", "ctx.secrets", "ctx.capture", "ctx.assert", "ctx.log"},
			ForbiddenIdentifiers: []string{"import", "require", "eval", "process"},
		},
		Reproducibility: ExecutableScriptReproducibility{
			PlanHashSHA256:     "sha_plan",
			ScriptHashSHA256:   sourceHash,
			MarkdownHashSHA256: SHA256Hex([]byte("# 审批")),
			GraphHashSHA256:    "sha_graph",
			DeterministicSeed:  "seed_1",
		},
		Validation: &ExecutableScriptValidation{Valid: true},
	}
	hash, err := bundle.ComputeBundleHash()
	if err != nil {
		t.Fatal(err)
	}
	bundle.Reproducibility.BundleHashSHA256 = hash
	data, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	var got ExecutableRecordingScriptBundle
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != ExecutableRecordingScriptBundleSchemaVersion || got.ScriptManifest.EntryFunction != "runCascadeRecording" {
		t.Fatalf("bundle lost required fields: %+v", got)
	}
	if got.PlaywrightScript.SHA256 != sourceHash || got.Validation == nil || !got.Validation.Valid {
		t.Fatalf("bundle lost script hash or validation: %+v", got)
	}
}

func TestExecutableRecordingScriptBundleHashIgnoresStoredBundleHashAndValidation(t *testing.T) {
	bundle := &ExecutableRecordingScriptBundle{
		ID:              "bundle_1",
		ProjectID:       "project_1",
		WorkflowGraphID: "graph_1",
		SchemaVersion:   ExecutableRecordingScriptBundleSchemaVersion,
		PlanJSON:        minimalExecutionScriptDocumentForBundleTest(),
		PlaywrightScript: ExecutableScriptSource{
			InlineSource: "export async function runCascadeRecording(ctx: CascadeRecordingContext): Promise<CascadeRecordingResult> { return { ok: true }; }",
			SHA256:       "sha_script",
		},
		ApprovalMarkdown: ApprovalMarkdownDocument{InlineMarkdown: "# 审批", SHA256: "sha_markdown"},
		SecurityPolicy:   ExecutableScriptSecurityPolicy{Redactions: RedactionPolicy{}},
		Reproducibility:  ExecutableScriptReproducibility{PlanHashSHA256: "sha_plan", ScriptHashSHA256: "sha_script", MarkdownHashSHA256: "sha_markdown"},
	}
	first, err := bundle.ComputeBundleHash()
	if err != nil {
		t.Fatal(err)
	}
	bundle.Reproducibility.BundleHashSHA256 = "stored_hash"
	bundle.Validation = &ExecutableScriptValidation{Valid: false, Findings: []AgentFinding{{ID: "finding", Severity: FindingSeverityBlocking, Summary: "x"}}}
	second, err := bundle.ComputeBundleHash()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("bundle hash should ignore stored bundle hash and validation: %s != %s", first, second)
	}
}

func TestExecutableRecordingScriptBundleRepairLineageRoundTripAndHash(t *testing.T) {
	bundle := &ExecutableRecordingScriptBundle{
		ID:              "bundle_repair_1",
		ProjectID:       "project_1",
		WorkflowGraphID: "graph_1",
		SchemaVersion:   ExecutableRecordingScriptBundleSchemaVersion,
		PlanJSON:        minimalExecutionScriptDocumentForBundleTest(),
		PlaywrightScript: ExecutableScriptSource{
			InlineSource: "export async function runCascadeRecording(ctx: CascadeRecordingContext): Promise<CascadeRecordingResult> { return { ok: true }; }",
			SHA256:       "sha_script",
		},
		ApprovalMarkdown: ApprovalMarkdownDocument{InlineMarkdown: "# 修复审批", SHA256: "sha_markdown"},
		SecurityPolicy:   ExecutableScriptSecurityPolicy{Redactions: RedactionPolicy{}},
		Reproducibility:  ExecutableScriptReproducibility{PlanHashSHA256: "sha_plan", ScriptHashSHA256: "sha_script", MarkdownHashSHA256: "sha_markdown"},
	}
	baseHash, err := bundle.ComputeBundleHash()
	if err != nil {
		t.Fatal(err)
	}
	bundle.RepairLineage = &ScriptRepairLineage{
		BaseBundleID:         "bundle_original",
		BaseBundleHashSHA256: "sha_original_bundle",
		SourceResultID:       "result_failed",
		SourceCloudJobID:     "job_failed",
		RepairAttempt:        1,
		ChangeSummary:        "修复邀请按钮 selector。",
		DiagnosticRefs:       []EvidenceRef{{ID: "diag_result_failed", Kind: EvidenceKindBrowserTrace}},
		CreatedAt:            time.Date(2026, 7, 8, 11, 0, 0, 0, time.UTC),
	}
	repairHash, err := bundle.ComputeBundleHash()
	if err != nil {
		t.Fatal(err)
	}
	if baseHash != repairHash {
		t.Fatalf("repair lineage should not alter executable bundle hash: %s != %s", baseHash, repairHash)
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	var got ExecutableRecordingScriptBundle
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.RepairLineage == nil || got.RepairLineage.SourceResultID != "result_failed" || got.RepairLineage.RepairAttempt != 1 {
		t.Fatalf("repair lineage did not round-trip: %+v", got.RepairLineage)
	}
}

func minimalExecutionScriptDocumentForBundleTest() *ExecutionScriptDocument {
	return &ExecutionScriptDocument{
		ID:              "script_1",
		ProjectID:       "project_1",
		WorkflowGraphID: "graph_1",
		GraphVersion:    1,
		SchemaVersion:   ExecutionScriptDocumentSchemaVersion,
		RecordingRunSpec: RecordingRunSpec{
			BaseURL:        "https://app.example.com",
			AllowedDomains: []string{"app.example.com"},
			Browser:        BrowserRunSpec{Engine: "chromium", Headless: true},
			Timeline:       RecordingTimeline{TargetDurationSec: 60},
			Outputs:        RecordingOutputRequest{RawRecording: true, FinalVideo: true, StepByStepDocs: true, Trace: true},
			Redactions:     RedactionPolicy{MaskSelectors: []string{"[data-sensitive]"}},
			FailurePolicy:  RecordingFailurePolicy{SelectorRepairAllowed: true},
		},
		Steps: []ScriptStep{{
			ID:              "step_1",
			Order:           1,
			NodeID:          "node_1",
			PageTarget:      ScriptPageTarget{URL: "https://app.example.com/dashboard"},
			Action:          ScriptActionInstruction{Type: GraphActionNavigate, Target: ActionTarget{URL: "https://app.example.com/dashboard"}},
			ExpectedOutcome: "页面加载",
			Validations:     []ValidationSpec{{ID: "validate_1", Kind: "expected", Required: true}},
			Capture:         CaptureSpec{Screenshot: true, Video: true},
			Timing:          NodeTimingHint{NodeID: "node_1", DurationMS: 1000},
			Narrative:       NarrativeCue{Title: "打开页面"},
			Blocking:        true,
		}},
		SafetyPolicy:      ScriptSafetyPolicy{AllowedDomains: []string{"app.example.com"}, Redactions: RedactionPolicy{MaskSelectors: []string{"[data-sensitive]"}}},
		Reproducibility:   ReproducibilitySpec{GraphHashSHA256: "sha_graph", ScriptHashSHA256: "sha_plan"},
		ApprovalChecklist: ScriptApprovalChecklist{HumanApprovalRequired: true, SourceSummaryOnly: true},
	}
}
