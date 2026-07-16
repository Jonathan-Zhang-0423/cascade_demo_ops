package agents

import (
	"context"
	"encoding/json"
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
	if bundle.ScriptManifest.Runtime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 {
		t.Fatalf("expected browser agent outline runtime, got %+v", bundle.ScriptManifest)
	}
	if bundle.StageApprovalPlan == nil || bundle.ScriptOutline == nil || bundle.AgentPromptPolicy == nil || bundle.UnderstandingDossier == nil {
		t.Fatalf("outline bundle missing required outline artifacts: %+v", bundle)
	}
	for _, node := range graph.Nodes {
		if !outlineBundleContainsNode(bundle, node.ID) {
			t.Fatalf("outline bundle missing node id %s", node.ID)
		}
	}
	if !strings.Contains(strings.Join(bundle.ScriptOutline.Stages[0].CapturePoints, " "), "capture_stage_screenshot") {
		t.Fatalf("outline should preserve capture points: %+v", bundle.ScriptOutline.Stages[0])
	}
	payload, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"raw-password-123", "BEGIN PRIVATE KEY", "import ", "require(", "process.", "fetch("} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("outline bundle leaked forbidden token %q:\n%s", forbidden, payload)
		}
	}
	if bundle.Reproducibility.PlanHashSHA256 == "" || bundle.Reproducibility.StagePlanHashSHA256 == "" || bundle.Reproducibility.OutlineHashSHA256 == "" || bundle.Reproducibility.PromptPolicyHashSHA256 == "" || bundle.Reproducibility.BundleHashSHA256 == "" {
		t.Fatalf("bundle missing reproducibility hashes: %+v", bundle.Reproducibility)
	}
}

func TestScriptPackagerUsesDeterministicApprovalMarkdown(t *testing.T) {
	project, report, productMap, graph := executableBundleFixtures()
	pkg, err := NewScriptPackagerAgentWithLLM(failingMarkdownLLM{}).PackageScript(context.Background(), project, report, productMap, graph)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.ExecutableBundle == nil {
		t.Fatal("expected executable script bundle")
	}
	if !strings.Contains(pkg.Markdown, "未使用模型润色或新增目标") {
		t.Fatalf("expected deterministic markdown note, got:\n%s", pkg.Markdown)
	}
	if !strings.Contains(pkg.ExecutableBundle.ApprovalMarkdown.InlineMarkdown, "未使用模型润色或新增目标") {
		t.Fatalf("expected bundle approval markdown to carry deterministic note:\n%s", pkg.ExecutableBundle.ApprovalMarkdown.InlineMarkdown)
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
	bundle := validLegacyExecutableBundleFixture(t)
	source := "import fs from \"fs\";\n" + bundle.PlaywrightScript.InlineSource
	setBundleSourceForTest(bundle, source)
	validation := NewScriptBundleValidator().ValidateBundle(bundle)
	if validation.Valid {
		t.Fatalf("expected forbidden import to be rejected: %+v", validation)
	}
	assertFinding(t, validation, "禁止")
}

func TestScriptBundleValidatorRejectsRawSecret(t *testing.T) {
	bundle := validLegacyExecutableBundleFixture(t)
	source := bundle.PlaywrightScript.InlineSource + "\nconst token = \"sk-abcdefghijklmnop\";\n"
	setBundleSourceForTest(bundle, source)
	validation := NewScriptBundleValidator().ValidateBundle(bundle)
	if validation.Valid {
		t.Fatalf("expected raw secret to be rejected: %+v", validation)
	}
	assertFinding(t, validation, "secret")
}

func TestScriptBundleValidatorRejectsNodeMismatch(t *testing.T) {
	bundle := validLegacyExecutableBundleFixture(t)
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
	outlineStage := outlineStageByNodeID(pkg.ExecutableBundle, "node_observe")
	if outlineStage == nil {
		t.Fatal("expected observation outline stage")
	}
	if outlineStage.Interactions[0].Kind != model.GraphActionInspect {
		t.Fatalf("observation outline should remain inspect: %+v", outlineStage)
	}
}

func TestScriptPackagerDowngradesGenericBusinessSelector(t *testing.T) {
	project, report, productMap, graph := executableBundleFixtures()
	graph.Nodes[1].ID = "node_generic_click"
	graph.Nodes[1].Action = string(model.GraphActionClick)
	graph.Nodes[1].Selector = "main"
	graph.Nodes[1].ActionSpec = &model.GraphAction{
		Type:      model.GraphActionClick,
		Target:    model.ActionTarget{Selector: "main"},
		TimeoutMS: 12000,
	}
	graph.Nodes[1].Validations = []model.ValidationSpec{{
		ID:        "validate_generic",
		Kind:      "dom_visible",
		Target:    model.ActionTarget{Selector: "main"},
		Assertion: "generic selector should not block",
		Severity:  "blocking",
		Required:  true,
	}}

	pkg, err := NewScriptPackagerAgent().PackageScript(context.Background(), project, report, productMap, graph)
	if err != nil {
		t.Fatal(err)
	}
	var genericStep *model.ScriptStep
	for i := range pkg.Document.Steps {
		if pkg.Document.Steps[i].NodeID == "node_generic_click" {
			genericStep = &pkg.Document.Steps[i]
			break
		}
	}
	if genericStep == nil {
		t.Fatal("expected downgraded generic step")
	}
	if genericStep.Action.Type != model.GraphActionInspect {
		t.Fatalf("expected generic click to be downgraded to inspect, got %+v", genericStep.Action)
	}
	if genericStep.Blocking {
		t.Fatalf("downgraded generic step should not be blocking: %+v", genericStep)
	}
	outlineStage := outlineStageByNodeID(pkg.ExecutableBundle, "node_generic_click")
	if outlineStage == nil || len(outlineStage.Interactions) == 0 || outlineStage.Interactions[0].Kind != model.GraphActionInspect {
		t.Fatalf("downgraded generic step should be inspect in outline: %+v", outlineStage)
	}
}

func TestScriptPackagerEnforcesNaturalStageDuration(t *testing.T) {
	project, report, productMap, graph := executableBundleFixtures()
	pkg, err := NewScriptPackagerAgent().PackageScript(context.Background(), project, report, productMap, graph)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range pkg.Document.Steps {
		if step.Timing.DurationMS < 10000 {
			t.Fatalf("expected every stage to be at least 10s, got %s=%d", step.NodeID, step.Timing.DurationMS)
		}
	}
	if pkg.ExecutableBundle.StageApprovalPlan == nil {
		t.Fatal("expected stage approval plan")
	}
	for _, stage := range pkg.ExecutableBundle.StageApprovalPlan.Stages {
		if stage.DurationMS < 10000 {
			t.Fatalf("expected outline stage to be at least 10s, got %+v", stage)
		}
		if !containsString(stage.WaitConditions, "wait_after_entry_at_least_1000ms") {
			t.Fatalf("expected render wait condition in stage: %+v", stage)
		}
	}
}

func TestScriptCodeGeneratorPrefersTestIDOverGenericSelector(t *testing.T) {
	bundle := validExecutableBundleFixture(t)
	doc := bundle.PlanJSON
	doc.Steps[1].Action.Type = model.GraphActionClick
	doc.Steps[1].Action.Target.Selector = "main"
	doc.Steps[1].Action.Target.TestID = "primary-action"
	doc.Steps[1].PageTarget.Selector = "body"

	source, err := NewScriptCodeGenerator().Generate(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(source, `ctx.page.click("[data-testid='primary-action']"`) {
		t.Fatalf("expected generator to prefer test id selector:\n%s", source)
	}
	if strings.Contains(source, `ctx.page.click("main"`) {
		t.Fatalf("generator must not click generic selector when test id is available:\n%s", source)
	}
}

func TestScriptPackagerKeepsRuntimeAdaptiveBusinessAction(t *testing.T) {
	project, report, productMap, graph := executableBundleFixtures()
	graph.Nodes[1].Action = "click"
	graph.Nodes[1].Title = "新建项目"
	graph.Nodes[1].Selector = `button:has-text("新建项目")`
	graph.Nodes[1].ExpectedOutcome = "进入新建项目流程"
	graph.Nodes[1].ActionSpec = &model.GraphAction{
		Type: model.GraphActionClick,
		Target: model.ActionTarget{
			Selector: `button:has-text("新建项目")`,
			Label:    "新建项目",
			SelectorAlternatives: []model.SelectorCandidate{
				{Kind: "css", Value: `[data-testid="new-project"]`, Source: "runtime_adaptive", Confidence: 0.72},
				{Kind: "css", Value: `[role="button"]:has-text("新建项目")`, Source: "runtime_adaptive", Confidence: 0.66},
			},
		},
		TimeoutMS: 12000,
	}
	graph.Nodes[1].Metadata = map[string]any{
		"verification_status":     "runtime_adaptive",
		"verified_interaction_id": "adaptive_new_project",
	}

	pkg, err := NewScriptPackagerAgent().PackageScript(context.Background(), project, report, productMap, graph)
	if err != nil {
		t.Fatal(err)
	}
	var adaptiveStep *model.ScriptStep
	for i := range pkg.Document.Steps {
		if pkg.Document.Steps[i].NodeID == "node_invite" {
			adaptiveStep = &pkg.Document.Steps[i]
			break
		}
	}
	if adaptiveStep == nil {
		t.Fatal("expected adaptive business step")
	}
	if adaptiveStep.Action.Type != model.GraphActionClick {
		t.Fatalf("runtime adaptive business action should remain executable, got %+v", adaptiveStep.Action)
	}
	outlineStage := outlineStageByNodeID(pkg.ExecutableBundle, "node_invite")
	if outlineStage == nil || len(outlineStage.Components) == 0 {
		t.Fatalf("expected adaptive selector guidance in outline: %+v", outlineStage)
	}
	if !strings.Contains(outlineAuditText(outlineStage), "new-project") && !strings.Contains(outlineAuditText(outlineStage), "新建项目") {
		t.Fatalf("expected adaptive outline to carry selector alternatives: %+v", outlineStage)
	}
}

func TestScriptPackagerRejectsScriptThatMissesExplicitDemoIntent(t *testing.T) {
	project, report, productMap, graph := executableBundleFixtures()
	project.ProductDescription = "演示登录（10s），新建项目（10s，俄罗斯方块，构建模式），agent实际构建演示（60s等待）"
	graph.Name = "错误的新建项目演示"
	graph.Summary = "这份图错误地只包含账号展示和登录按钮。"
	graph.Nodes[1].ID = "node_user_email_display"
	graph.Nodes[1].Title = "user email display"
	graph.Nodes[1].Action = string(model.GraphActionFill)
	graph.Nodes[1].Selector = "[data-testid='user-email-display']"
	graph.Nodes[1].ExpectedOutcome = "user email display"
	graph.Nodes[1].ActionSpec = &model.GraphAction{
		Type:      model.GraphActionFill,
		Target:    model.ActionTarget{Selector: "[data-testid='user-email-display']", Label: "user email display"},
		TimeoutMS: 12000,
	}
	graph.Nodes[1].Metadata = map[string]any{"verification_status": "sidecar_unavailable", "verified_interaction_id": "code_user_email"}
	graph.Nodes[2].ID = "node_login_button"
	graph.Nodes[2].Title = "login btn"
	graph.Nodes[2].Action = string(model.GraphActionClick)
	graph.Nodes[2].Selector = "[data-testid='login-btn']"
	graph.Nodes[2].ExpectedOutcome = "login btn"
	graph.Nodes[2].ActionSpec = &model.GraphAction{
		Type:      model.GraphActionClick,
		Target:    model.ActionTarget{Selector: "[data-testid='login-btn']", Label: "login btn"},
		TimeoutMS: 12000,
	}
	graph.Nodes[2].Metadata = map[string]any{"verification_status": "sidecar_unavailable", "verified_interaction_id": "code_login_btn"}

	_, err := NewScriptPackagerAgent().PackageScript(context.Background(), project, report, productMap, graph)
	if err == nil {
		t.Fatal("expected explicit intent mismatch to reject bad executable script")
	}
	for _, want := range []string{"script_intent_mismatch", "俄罗斯方块", "构建模式", "60 秒"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to mention %q, got %v", want, err)
		}
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

func validLegacyExecutableBundleFixture(t *testing.T) *model.ExecutableRecordingScriptBundle {
	t.Helper()
	bundle := validExecutableBundleFixture(t)
	source, err := NewScriptCodeGenerator().Generate(bundle.PlanJSON)
	if err != nil {
		t.Fatal(err)
	}
	hash := hashString(source)
	bundle.ScriptManifest.Language = "typescript"
	bundle.ScriptManifest.Runtime = model.ExecutableScriptRuntimePlaywrightRestrictedSandbox
	bundle.ScriptManifest.EntryFunction = "runCascadeRecording"
	bundle.ScriptManifest.Generator = scriptCodeGeneratorName
	bundle.ScriptManifest.GeneratorVersion = scriptCodeGeneratorVersion
	bundle.ScriptManifest.ContextAPIs = []string{"ctx.page", "ctx.secrets", "ctx.capture", "ctx.assert", "ctx.log"}
	bundle.PlaywrightScript = model.ExecutableScriptSource{
		InlineSource: source,
		MimeType:     "text/typescript",
		SHA256:       hash,
		SizeBytes:    int64(len([]byte(source))),
	}
	bundle.Reproducibility.ScriptHashSHA256 = hash
	bundle.Reproducibility.BundleHashSHA256 = ""
	bundle.Validation = nil
	bundleHash, err := bundle.ComputeBundleHash()
	if err != nil {
		t.Fatal(err)
	}
	bundle.Reproducibility.BundleHashSHA256 = bundleHash
	validation := NewScriptBundleValidator().ValidateBundle(bundle)
	if !validation.Valid {
		t.Fatalf("legacy fixture should be valid: %+v", validation)
	}
	bundle.Validation = &validation
	return bundle
}

func outlineBundleContainsNode(bundle *model.ExecutableRecordingScriptBundle, nodeID string) bool {
	if bundle == nil || bundle.StageApprovalPlan == nil || bundle.ScriptOutline == nil {
		return false
	}
	for _, stage := range bundle.StageApprovalPlan.Stages {
		if stage.NodeID == nodeID {
			return true
		}
	}
	for _, stage := range bundle.ScriptOutline.Stages {
		if stage.NodeID == nodeID {
			return true
		}
	}
	return false
}

func outlineStageByNodeID(bundle *model.ExecutableRecordingScriptBundle, nodeID string) *model.BrowserAgentOutlineStage {
	if bundle == nil || bundle.ScriptOutline == nil {
		return nil
	}
	for i := range bundle.ScriptOutline.Stages {
		if bundle.ScriptOutline.Stages[i].NodeID == nodeID {
			return &bundle.ScriptOutline.Stages[i]
		}
	}
	return nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
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
			Metadata:       map[string]any{"verification_status": "verified", "verified_interaction_id": "verified_invite_email"},
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
