package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

func TestMultimodalFlowPackagesReviewableScriptDocument(t *testing.T) {
	repo := createFixtureRepo(t)
	flow := newTestFlow(t)
	state, err := flow.Start(context.Background(), orchestrator.UserInput{
		Mode:               model.AppModeDesktop,
		ProductURL:         "https://app.example.com/dashboard",
		LocalRepoPath:      repo,
		ProductDescription: "新上线团队协作功能，需要给中国客户生成发布演示。",
		TargetAudience:     "产品与客户成功团队",
		BrandTone:          "专业、可信、简洁",
		MustShow:           []string{"邀请团队成员", "权限说明", "成功状态"},
		ForbiddenPages:     []string{"/billing"},
		ForbiddenData:      []string{"api_key", "customer_email"},
		DemoUsername:       "demo@example.com",
		DemoPassword:       "raw-password-123",
		RequirementDocuments: []model.RequirementDocumentInput{{
			ID:         "req_launch",
			Kind:       "markdown",
			Title:      "团队协作发布需求",
			Body:       "展示团队邀请、协作价值和完成证明。",
			FocusAreas: []string{"邀请成员", "协作价值", "完成证明"},
		}},
		WebpageScreenshots: []model.WebpageScreenshotInput{{
			ID:            "shot_dashboard",
			URL:           "https://app.example.com/dashboard",
			Title:         "工作台首页",
			PageRole:      "dashboard",
			Artifact:      model.ArtifactRef{ID: "artifact_shot_dashboard", URI: "file://redacted/dashboard.png", SHA256: "sha_shot"},
			OCRText:       "团队工作台 邀请成员",
			VisionSummary: "工作台展示团队协作入口和邀请成员按钮。",
			CapturedAt:    time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC),
			Annotations: []model.ScreenshotAnnotation{{
				ID:           "ann_invite",
				Kind:         "click",
				Label:        "邀请成员",
				SelectorHint: "[data-testid='invite-member']",
				FeatureRef:   "feature_primary_value",
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != orchestrator.FlowStatusAwaitingHuman || state.CurrentNode != orchestrator.NodeHumanApprove {
		t.Fatalf("expected script flow to stop at HumanApprove, got node=%s status=%s", state.CurrentNode, state.Status)
	}
	if state.UnderstandingReport == nil || state.ProductMap == nil || state.WorkflowGraph == nil || state.ScriptDocument == nil || state.ExecutableScriptBundle == nil {
		t.Fatalf("expected understanding, product map, graph, script document, and executable bundle: %+v", state)
	}
	if strings.Contains(strings.ToLower(state.ProductMap.Summary), "placeholder") {
		t.Fatalf("product map should not use placeholder summary: %s", state.ProductMap.Summary)
	}
	if len(state.CodeSnapshots) == 0 || state.CodeSnapshots[0].SourceDigestSHA256 == "" || len(state.CodeSnapshots[0].PathDigests) == 0 {
		t.Fatalf("expected code structure digest: %+v", state.CodeSnapshots)
	}
	assertScriptMatchesGraph(t, state.ScriptDocument, state.WorkflowGraph)
	if state.ScriptDocument.RecordingRunSpec.Locale != "zh-CN" || state.ScriptDocument.RecordingRunSpec.Timezone != "Asia/Shanghai" {
		t.Fatalf("script should default to Chinese desktop recording context: %+v", state.ScriptDocument.RecordingRunSpec)
	}
	if !strings.Contains(state.ScriptMarkdown, "## 执行步骤") || !strings.Contains(state.ScriptMarkdown, "## 审批清单") {
		t.Fatalf("markdown preview missing required Chinese sections:\n%s", state.ScriptMarkdown)
	}
	if state.ExecutableScriptBundle.Validation == nil || !state.ExecutableScriptBundle.Validation.Valid {
		t.Fatalf("expected valid executable script bundle: %+v", state.ExecutableScriptBundle.Validation)
	}
	if state.ExecutableScriptBundle.PlaywrightScript.InlineSource == "" || !strings.Contains(state.ExecutableScriptBundle.PlaywrightScript.InlineSource, "runCascadeRecording") {
		t.Fatalf("expected executable TypeScript script: %+v", state.ExecutableScriptBundle.PlaywrightScript)
	}
	scriptJSON, err := json.Marshal(state.ScriptDocument)
	if err != nil {
		t.Fatal(err)
	}
	bundleJSON, err := json.Marshal(state.ExecutableScriptBundle)
	if err != nil {
		t.Fatal(err)
	}
	codeJSON, err := json.Marshal(state.CodeSnapshots)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"raw-password-123", "BEGIN PRIVATE KEY", "function submitPayment", "src/components/BillingForm.tsx"} {
		if strings.Contains(string(scriptJSON), forbidden) {
			t.Fatalf("script document leaked forbidden token %q: %s", forbidden, scriptJSON)
		}
		if strings.Contains(string(bundleJSON), forbidden) {
			t.Fatalf("executable bundle leaked forbidden token %q: %s", forbidden, bundleJSON)
		}
		if strings.Contains(string(codeJSON), forbidden) {
			t.Fatalf("code snapshot leaked forbidden token %q: %s", forbidden, codeJSON)
		}
	}
}

func TestMultimodalFlowFallsBackToRuntimeAdaptiveBusinessAction(t *testing.T) {
	repo := createFixtureRepo(t)
	flow := newTestFlow(t)
	state, err := flow.Start(context.Background(), orchestrator.UserInput{
		Mode:               model.AppModeDesktop,
		LocalRepoPath:      repo,
		ProductDescription: "仅基于代码和截图生成本地演示脚本。",
		TargetAudience:     "实施团队",
		WebpageScreenshots: []model.WebpageScreenshotInput{{
			ID:            "shot_no_url",
			Title:         "无 URL 页面截图",
			PageRole:      "evidence",
			Artifact:      model.ArtifactRef{ID: "artifact_no_url", URI: "file://redacted/no-url.png", SHA256: "sha_no_url"},
			VisionSummary: "截图显示产品核心页面。",
		}},
	})
	if err != nil {
		t.Fatalf("expected runtime adaptive fallback instead of blocking flow: %v", err)
	}
	if state == nil || state.VerifiedInteractionPlan == nil || state.VerifiedInteractionPlan.BusinessActionCount == 0 {
		t.Fatalf("expected adaptive verified interaction plan, got state=%+v", state)
	}
	if state.MissingEvidenceReport == nil || state.MissingEvidenceReport.Blocking {
		t.Fatalf("expected non-blocking adaptive missing evidence warning, got %+v", state.MissingEvidenceReport)
	}
	if state.ScriptDocument == nil || !scriptDocumentHasExecutableBusinessAction(state.ScriptDocument) {
		t.Fatalf("expected adaptive flow to produce executable business action, got %+v", state.ScriptDocument)
	}
}

func scriptDocumentHasExecutableBusinessAction(doc *model.ExecutionScriptDocument) bool {
	if doc == nil {
		return false
	}
	for _, step := range doc.Steps {
		switch step.Action.Type {
		case model.GraphActionClick, model.GraphActionFill, model.GraphActionSelect, model.GraphActionUpload, model.GraphActionAPICall:
			return true
		}
	}
	return false
}

func assertScriptMatchesGraph(t *testing.T, doc *model.ExecutionScriptDocument, graph *model.DemoWorkflowGraph) {
	t.Helper()
	nodes := map[string]bool{}
	for _, node := range graph.Nodes {
		nodes[node.ID] = true
	}
	if len(doc.Steps) != len(graph.Nodes) {
		t.Fatalf("script step count %d != graph node count %d", len(doc.Steps), len(graph.Nodes))
	}
	allowedActions := map[model.GraphActionType]bool{
		model.GraphActionNavigate: true,
		model.GraphActionClick:    true,
		model.GraphActionFill:     true,
		model.GraphActionSelect:   true,
		model.GraphActionUpload:   true,
		model.GraphActionWait:     true,
		model.GraphActionAssert:   true,
		model.GraphActionInspect:  true,
		model.GraphActionAPICall:  true,
	}
	for index, step := range doc.Steps {
		if step.Order != index+1 {
			t.Fatalf("step order mismatch: %+v", step)
		}
		if !nodes[step.NodeID] {
			t.Fatalf("script step has unknown node id: %+v", step)
		}
		if !allowedActions[step.Action.Type] {
			t.Fatalf("script step uses unsupported action: %+v", step)
		}
		if step.ExpectedOutcome == "" || len(step.Validations) == 0 || step.Timing.NodeID != step.NodeID {
			t.Fatalf("script step lost required executable fields: %+v", step)
		}
	}
	if len(doc.RecordingRunSpec.Timeline.NodeTimingHints) != len(graph.Nodes) {
		t.Fatalf("recording run spec lost timing hints: %+v", doc.RecordingRunSpec.Timeline.NodeTimingHints)
	}
	if len(doc.SafetyPolicy.Redactions.MaskSelectors) == 0 {
		t.Fatal("script document must carry redaction selectors")
	}
	if doc.Reproducibility.GraphHashSHA256 == "" || doc.Reproducibility.ScriptHashSHA256 == "" || doc.Reproducibility.DeterministicSeed == "" {
		t.Fatalf("script document missing reproducibility hashes: %+v", doc.Reproducibility)
	}
}

func newTestFlow(t *testing.T) *orchestrator.CascadeFlow {
	t.Helper()
	flow, err := orchestrator.NewCascadeFlow(orchestrator.Dependencies{
		InputContext:        NewInputContextAgent(),
		RequirementReader:   NewRequirementReaderAgent(),
		CodeReader:          NewCodeReaderAgent(),
		PageReader:          NewPageReaderAgent(),
		ProjectIntelligence: NewProjectIntelligenceGraph(),
		Understanding:       NewMultimodalUnderstandingAgent(),
		ProductMap:          NewProductMapAgent(),
		PageVerifier:        NewPageInteractionVerifierAgent(),
		GraphBuilder:        NewGraphBuilderAgent(),
		ScriptPackager:      NewScriptPackagerAgent(),
		QAExecutor:          NewQAExecutorAgent(),
		AssetGenerator:      NewAssetGeneratorAgent(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return flow
}

func createFixtureRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src", "components"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"dependencies":{"react":"latest","vite":"latest"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	component := `
export function BillingForm() {
  const token = "never-store-me";
  function submitPayment() {
    return fetch("/api/team/invites");
  }
  return <button data-testid="invite-member" onClick={submitPayment}>Invite</button>;
}
type Team = { id: string; customer_email: string };
`
	if err := os.WriteFile(filepath.Join(root, "src", "components", "BillingForm.tsx"), []byte(component), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}
