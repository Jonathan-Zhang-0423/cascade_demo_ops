package agents

import (
	"context"
	"errors"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/llm"
	"cascade-demoops/backend/internal/model"
)

func TestScriptPackagerEmitsValidExecutableBundle(t *testing.T) {
	project, report, productMap, graph := executableBundleFixtures()
	pkg, err := NewScriptPackagerAgent().PackageScript(context.Background(), project, report, productMap, graph)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.ExecutableBundle == nil {
		t.Fatal("expected executable script bundle")
	}
	bundle := pkg.ExecutableBundle
	if bundle.SchemaVersion != model.ExecutableRecordingScriptBundleSchemaVersion {
		t.Fatalf("unexpected bundle schema: %s", bundle.SchemaVersion)
	}
	if bundle.Validation == nil || !bundle.Validation.Valid {
		t.Fatalf("expected valid generated bundle: %+v", bundle.Validation)
	}
	if got := bundle.PlanJSON.Steps[0].Capture.Scope; got != model.CaptureScopeFullPage {
		t.Fatalf("expected capture scope to be preserved in plan_json, got %q", got)
	}
	source := bundle.PlaywrightScript.InlineSource
	if !strings.Contains(source, `"scope":"full_page"`) || !strings.Contains(source, `"full_page":true`) {
		t.Fatalf("generated script missing capture scope options:\n%s", source)
	}
	for _, node := range graph.Nodes {
		if !strings.Contains(source, `"`+node.ID+`"`) {
			t.Fatalf("generated script missing node id %s:\n%s", node.ID, source)
		}
	}
	for _, forbidden := range []string{"raw-password-123", "BEGIN PRIVATE KEY", "import ", "require(", "process.", "fetch("} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("generated script leaked forbidden token %q:\n%s", forbidden, source)
		}
	}
	if bundle.Reproducibility.PlanHashSHA256 == "" || bundle.Reproducibility.ScriptHashSHA256 == "" || bundle.Reproducibility.BundleHashSHA256 == "" {
		t.Fatalf("bundle missing reproducibility hashes: %+v", bundle.Reproducibility)
	}
}

func TestScriptPackagerFallsBackWhenMarkdownLLMReturnsInvalidJSON(t *testing.T) {
	project, report, productMap, graph := executableBundleFixtures()
	pkg, err := NewScriptPackagerAgentWithLLM(failingMarkdownLLM{}).PackageScript(context.Background(), project, report, productMap, graph)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.ExecutableBundle == nil {
		t.Fatal("expected executable script bundle")
	}
	if !strings.Contains(pkg.Markdown, "审批文档润色失败") {
		t.Fatalf("expected markdown fallback note, got:\n%s", pkg.Markdown)
	}
	if !strings.Contains(pkg.ExecutableBundle.ApprovalMarkdown.InlineMarkdown, "审批文档润色失败") {
		t.Fatalf("expected bundle approval markdown to carry fallback note:\n%s", pkg.ExecutableBundle.ApprovalMarkdown.InlineMarkdown)
	}
}

func TestScriptCodeGeneratorIsDeterministic(t *testing.T) {
	_, report, productMap, graph := executableBundleFixtures()
	project, _, _, _ := executableBundleFixtures()
	pkg, err := NewScriptPackagerAgent().PackageScript(context.Background(), project, report, productMap, graph)
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewScriptCodeGenerator().Generate(pkg.Document)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewScriptCodeGenerator().Generate(pkg.Document)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("script generator must be deterministic for the same execution plan")
	}
}

type failingMarkdownLLM struct{}

func (f failingMarkdownLLM) GenerateJSON(ctx context.Context, task config.ModelTask, req llm.JSONRequest, target any) (*llm.CallTrace, error) {
	return &llm.CallTrace{
		Provider:       config.ModelProviderKimi,
		Model:          "kimi-k2.7-code",
		Task:           task,
		AdapterVersion: config.ModelAdapterVersion,
	}, errors.New("llm JSON parse failed: invalid character 'å' after object key:value pair")
}

func (f failingMarkdownLLM) GenerateText(ctx context.Context, task config.ModelTask, req llm.TextRequest) (string, *llm.CallTrace, error) {
	return "", nil, errors.New("not implemented")
}

func (f failingMarkdownLLM) GenerateMultimodal(ctx context.Context, task config.ModelTask, req llm.MultimodalRequest, target any) (*llm.CallTrace, error) {
	return nil, errors.New("not implemented")
}

func TestScriptBundleValidatorRejectsForbiddenAPI(t *testing.T) {
	bundle := validExecutableBundleFixture(t)
	source := "import fs from \"fs\";\n" + bundle.PlaywrightScript.InlineSource
	setBundleSourceForTest(bundle, source)
	validation := NewScriptBundleValidator().ValidateBundle(bundle)
	if validation.Valid {
		t.Fatalf("expected forbidden import to be rejected: %+v", validation)
	}
	assertFinding(t, validation, "禁止")
}

func TestScriptBundleValidatorRejectsRawSecret(t *testing.T) {
	bundle := validExecutableBundleFixture(t)
	source := bundle.PlaywrightScript.InlineSource + "\nconst token = \"sk-abcdefghijklmnop\";\n"
	setBundleSourceForTest(bundle, source)
	validation := NewScriptBundleValidator().ValidateBundle(bundle)
	if validation.Valid {
		t.Fatalf("expected raw secret to be rejected: %+v", validation)
	}
	assertFinding(t, validation, "secret")
}

func TestScriptBundleValidatorRejectsNodeMismatch(t *testing.T) {
	bundle := validExecutableBundleFixture(t)
	source := strings.ReplaceAll(bundle.PlaywrightScript.InlineSource, `"node_open"`, `"node_missing"`)
	setBundleSourceForTest(bundle, source)
	validation := NewScriptBundleValidator().ValidateBundle(bundle)
	if validation.Valid {
		t.Fatalf("expected node mismatch to be rejected: %+v", validation)
	}
	assertFinding(t, validation, "node_open")
}

func TestScriptCodeGeneratorRejectsSensitiveInlineFillValue(t *testing.T) {
	doc := validExecutableBundleFixture(t).PlanJSON
	doc.Steps[1].Action.Type = model.GraphActionFill
	doc.Steps[1].Action.Value = "password=raw-secret"
	doc.Steps[1].Action.SecretRef = ""
	if _, err := NewScriptCodeGenerator().Generate(doc); err == nil {
		t.Fatal("expected sensitive inline fill value to be rejected")
	}
}

func TestObservationStepsAreNonBlockingAndDoNotRequireMainFallback(t *testing.T) {
	project, report, productMap, graph := executableBundleFixtures()
	graph.Nodes = append(graph.Nodes[:1], graph.Nodes[2:]...)
	graph.Nodes[1].ID = "node_observe"
	graph.Nodes[1].Type = model.GraphNodeTypeCapture
	graph.Nodes[1].Action = string(model.GraphActionInspect)
	graph.Nodes[1].Selector = ""
	graph.Nodes[1].ActionSpec = &model.GraphAction{
		Type:      model.GraphActionInspect,
		Target:    model.ActionTarget{},
		TimeoutMS: 12000,
	}
	graph.Nodes[1].Validations = []model.ValidationSpec{{
		ID:        "validate_observe",
		Kind:      "surface_visible",
		Assertion: "观察型素材节点可继续录制",
		Severity:  "warning",
		Required:  false,
	}}
	graph.Nodes[1].Capture = &model.CaptureSpec{Screenshot: true, Video: true, Zoom: true, Callout: true}

	pkg, err := NewScriptPackagerAgent().PackageScript(context.Background(), project, report, productMap, graph)
	if err != nil {
		t.Fatal(err)
	}
	var observeStep *model.ScriptStep
	for i := range pkg.Document.Steps {
		if pkg.Document.Steps[i].NodeID == "node_observe" {
			observeStep = &pkg.Document.Steps[i]
			break
		}
	}
	if observeStep == nil {
		t.Fatal("expected observation step in generated plan")
	}
	if observeStep.Blocking {
		t.Fatalf("observation step should be non-blocking: %+v", observeStep)
	}
	if observeStep.PageTarget.Selector == "main" || observeStep.Action.Target.Selector == "main" {
		t.Fatalf("observation step should not inject brittle main selector fallback: %+v", observeStep)
	}
	source := pkg.ExecutableBundle.PlaywrightScript.InlineSource
	if !strings.Contains(source, "observation step validation is non-blocking") {
		t.Fatalf("generated script should mark observation validation as non-blocking:\n%s", source)
	}
	if strings.Contains(source, `ctx.assert.step("node_observe"`) {
		t.Fatalf("observation step should not emit blocking assert.step:\n%s", source)
	}
}

func validExecutableBundleFixture(t *testing.T) *model.ExecutableRecordingScriptBundle {
	t.Helper()
	project, report, productMap, graph := executableBundleFixtures()
	pkg, err := NewScriptPackagerAgent().PackageScript(context.Background(), project, report, productMap, graph)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.ExecutableBundle == nil {
		t.Fatal("expected executable bundle")
	}
	return pkg.ExecutableBundle
}

func setBundleSourceForTest(bundle *model.ExecutableRecordingScriptBundle, source string) {
	hash := hashString(source)
	bundle.PlaywrightScript.InlineSource = source
	bundle.PlaywrightScript.SHA256 = hash
	bundle.Reproducibility.ScriptHashSHA256 = hash
	bundle.Reproducibility.BundleHashSHA256 = ""
	bundle.Validation = nil
}

func assertFinding(t *testing.T, validation model.ExecutableScriptValidation, contains string) {
	t.Helper()
	for _, finding := range validation.Findings {
		if strings.Contains(finding.Summary, contains) || strings.Contains(finding.ID, contains) {
			return
		}
	}
	t.Fatalf("expected finding containing %q, got %+v", contains, validation.Findings)
}

func executableBundleFixtures() (*model.ProjectContext, *model.MultimodalUnderstandingReport, *model.ProductMap, *model.DemoWorkflowGraph) {
	project := &model.ProjectContext{
		ID:                 "project_bundle",
		SchemaVersion:      model.ProjectContextSchemaVersion,
		Mode:               model.AppModeDesktop,
		ProductURL:         "https://app.example.com/dashboard",
		ProductDescription: "给中国客户展示团队协作价值。",
		TargetAudience:     "产品与客户成功团队",
		ForbiddenPages:     []string{"/billing"},
		ForbiddenData:      []string{"api_key"},
		AccessPolicy: &model.AccessPolicy{
			CredentialVaultRequired: true,
			SessionIsolation:        true,
			AutoExpireCredentials:   true,
			AllowedDomains:          []string{"app.example.com"},
			AuditLogRequired:        true,
		},
		SecurityPolicy: &model.SecurityPolicy{
			ForbiddenPages: []string{"/billing"},
			ForbiddenData:  []string{"api_key"},
			MaskSelectors:  []string{"[data-sensitive]"},
			PIIHandling:    "mask_in_artifacts",
		},
		Inputs: &model.ProjectInputBundle{
			Credentials: []model.CredentialInput{{ID: "cred_demo", Kind: "demo_account", SecretRef: "vault://demo/password", Scope: "recording_session"}},
		},
	}
	report := &model.MultimodalUnderstandingReport{
		ID:                 "report_bundle",
		ProjectID:          project.ID,
		SchemaVersion:      model.MultimodalUnderstandingReportSchemaVersion,
		Summary:            "已理解团队协作功能。",
		InputFingerprints:  map[string]string{"requirements": "sha_req"},
		SourceDigestSHA256: "sha_source",
		EvidenceRefs:       []model.EvidenceRef{{ID: "ev_req", Kind: model.EvidenceKindRequirementDoc, Summary: "发布演示需求", Confidence: 0.9}},
	}
	productMap := &model.ProductMap{ID: "map_bundle", ProjectID: project.ID, Version: 1, Summary: "团队协作入口、邀请动作和成功状态。"}
	graph := model.NewDemoWorkflowGraph("graph_bundle", project.ID, "https://app.example.com/dashboard")
	graph.Status = model.GraphStatusReviewReady
	graph.Name = "团队协作演示"
	graph.Summary = "展示团队邀请和成功状态。"
	graph.Nodes = []*model.GraphNode{
		{
			ID:              "node_open",
			Type:            model.GraphNodeTypeAction,
			Title:           "打开工作台",
			Action:          "navigate",
			ExpectedOutcome: "工作台加载完成",
			IsScreenshot:    true,
			RetryPolicy:     2,
			ActionSpec: &model.GraphAction{
				Type:      model.GraphActionNavigate,
				Target:    model.ActionTarget{URL: "https://app.example.com/dashboard"},
				TimeoutMS: 10000,
				WaitUntil: "networkidle",
			},
			Capture:        &model.CaptureSpec{Screenshot: true, Video: true, Scope: model.CaptureScopeFullPage, FullPage: true, MaskSelectors: []string{"[data-sensitive]"}},
			DurationHintMS: 2000,
		},
		{
			ID:              "node_invite",
			Type:            model.GraphNodeTypeAction,
			Title:           "邀请成员",
			Action:          "fill",
			Selector:        "[data-testid='invite-email']",
			ExpectedOutcome: "邀请表单已填写",
			IsScreenshot:    true,
			HasZoom:         true,
			RetryPolicy:     2,
			ActionSpec: &model.GraphAction{
				Type:      model.GraphActionFill,
				Target:    model.ActionTarget{Selector: "[data-testid='invite-email']"},
				SecretRef: "vault://demo/password",
				TimeoutMS: 10000,
			},
			Capture:        &model.CaptureSpec{Screenshot: true, Video: true, Zoom: true, MaskSelectors: []string{"[data-testid='invite-email']"}},
			DurationHintMS: 3000,
		},
		{
			ID:              "node_success",
			Type:            model.GraphNodeTypeValidation,
			Title:           "验证成功状态",
			Action:          "assert",
			Selector:        "[data-testid='success-state']",
			ExpectedOutcome: "成功状态可见",
			IsScreenshot:    true,
			RetryPolicy:     1,
			ActionSpec: &model.GraphAction{
				Type:      model.GraphActionAssert,
				Target:    model.ActionTarget{Selector: "[data-testid='success-state']"},
				TimeoutMS: 10000,
			},
			Capture:        &model.CaptureSpec{Screenshot: true, Video: true},
			DurationHintMS: 2000,
		},
	}
	graph.Edges = []*model.GraphEdge{
		{ID: "edge_open_invite", FromNode: "node_open", ToNode: "node_invite"},
		{ID: "edge_invite_success", FromNode: "node_invite", ToNode: "node_success"},
	}
	return project, report, productMap, graph
}
