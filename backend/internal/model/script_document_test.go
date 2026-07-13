package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestExecutionScriptDocumentJSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	graph := NewDemoWorkflowGraph("graph_script", "project_1", "https://app.example.com")
	doc := &ExecutionScriptDocument{
		ID:              "script_graph_script",
		ProjectID:       "project_1",
		WorkflowGraphID: graph.ID,
		GraphVersion:    graph.Version,
		SchemaVersion:   ExecutionScriptDocumentSchemaVersion,
		Status:          ScriptDocumentStatusReviewReady,
		Title:           "中文审批脚本",
		WorkflowGraph:   graph,
		RecordingRunSpec: RecordingRunSpec{
			BaseURL:        "https://app.example.com",
			AllowedDomains: []string{"app.example.com"},
			Locale:         "zh-CN",
			Browser:        BrowserRunSpec{Engine: "chromium", Headless: true},
			Timeline:       RecordingTimeline{TargetDurationSec: 60},
			Outputs:        RecordingOutputRequest{RawRecording: true, FinalVideo: true, StepByStepDocs: true, Trace: true},
			Redactions:     RedactionPolicy{MaskSelectors: []string{"[data-sensitive]"}},
			FailurePolicy:  RecordingFailurePolicy{RetryAttempts: 2, SelectorRepairAllowed: true},
		},
		Steps: []ScriptStep{{
			ID:              "step_01_start",
			Order:           1,
			NodeID:          "start",
			PageTarget:      ScriptPageTarget{URL: "https://app.example.com"},
			Action:          ScriptActionInstruction{Type: GraphActionNavigate, Target: ActionTarget{URL: "https://app.example.com"}},
			ExpectedOutcome: "页面加载完成",
			Capture:         CaptureSpec{Screenshot: true, Video: true, Scope: CaptureScopeFullPage, FullPage: true},
			Timing:          NodeTimingHint{NodeID: "start", DurationMS: 3000},
			Narrative:       NarrativeCue{Title: "打开产品入口", Voiceover: "打开产品入口"},
			Blocking:        true,
		}},
		SafetyPolicy: ScriptSafetyPolicy{
			AllowedDomains: []string{"app.example.com"},
			ForbiddenPages: []string{"/billing"},
			ForbiddenData:  []string{"api_key"},
			Redactions:     RedactionPolicy{MaskSelectors: []string{"[data-sensitive]"}},
			PIIHandling:    "mask_in_artifacts",
		},
		Reproducibility: ReproducibilitySpec{
			GraphHashSHA256:      "sha_graph",
			ScriptHashSHA256:     "sha_script",
			InputFingerprints:    map[string]string{"requirements": "sha_req"},
			SourceSnapshotDigest: "sha_source",
			DeterministicSeed:    "seed_1",
		},
		ApprovalChecklist: ScriptApprovalChecklist{
			HumanApprovalRequired:              true,
			SourceSummaryOnly:                  true,
			CredentialScopeReviewRequired:      true,
			RedactionsReviewRequired:           true,
			IPAllowlistAcknowledgementRequired: true,
		},
		CreatedAt: now,
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var got ExecutionScriptDocument
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != ExecutionScriptDocumentSchemaVersion || got.Language != "" && got.Language != "zh-CN" {
		t.Fatalf("unexpected script document: %+v", got)
	}
	if got.Steps[0].NodeID != "start" || got.RecordingRunSpec.Locale != "zh-CN" || got.Steps[0].Capture.Scope != CaptureScopeFullPage || !got.Steps[0].Capture.FullPage {
		t.Fatalf("script document lost executable fields: %+v", got)
	}
}

func TestExecutionScriptDocumentHashIgnoresStoredHashAndMarkdownArtifact(t *testing.T) {
	doc := &ExecutionScriptDocument{
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
			Outputs:        RecordingOutputRequest{FinalVideo: true},
			Redactions:     RedactionPolicy{},
			FailurePolicy:  RecordingFailurePolicy{SelectorRepairAllowed: true},
		},
		Steps: []ScriptStep{{
			ID:              "step_1",
			Order:           1,
			NodeID:          "node_1",
			Action:          ScriptActionInstruction{Type: GraphActionInspect, Target: ActionTarget{Selector: "main"}},
			ExpectedOutcome: "main visible",
			Capture:         CaptureSpec{Screenshot: true},
			Timing:          NodeTimingHint{NodeID: "node_1", DurationMS: 1000},
			Narrative:       NarrativeCue{Title: "inspect"},
			Blocking:        true,
		}},
		SafetyPolicy:      ScriptSafetyPolicy{Redactions: RedactionPolicy{}},
		Reproducibility:   ReproducibilitySpec{GraphHashSHA256: "sha_graph"},
		ApprovalChecklist: ScriptApprovalChecklist{HumanApprovalRequired: true, SourceSummaryOnly: true},
	}
	first, err := doc.ComputeScriptHash()
	if err != nil {
		t.Fatal(err)
	}
	doc.Reproducibility.ScriptHashSHA256 = "stored_hash"
	doc.MarkdownArtifact = &ArtifactRef{ID: "markdown", URI: "cascade://script.md", SHA256: "sha_md"}
	second, err := doc.ComputeScriptHash()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("script hash should ignore stored hash and markdown artifact: %s != %s", first, second)
	}
}
