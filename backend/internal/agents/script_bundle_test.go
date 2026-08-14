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

func TestScriptPackagerCarriesInvestigationQuestionsIntoStageAndOutline(t *testing.T) {
	project, report, productMap, graph := executableBundleFixtures()
	report.CodeSnapshots = []model.CodeUnderstandingSnapshot{{
		ID:        "code_with_questions",
		ProjectID: project.ID,
		InvestigationTrace: &model.CodeInvestigationTrace{
			ID:   "trace_questions",
			Mode: "tool_driven_intent_drilldown",
			Questions: []model.CodeInvestigationQuestion{{
				ID:               "question_team_invite",
				Question:         "邀请成员流程、邮箱输入和提交结果由哪些组件/API 支撑？",
				IntentLabel:      "邀请成员",
				ExpectedEvidence: []string{"route", "component_or_selector", "api_or_data_model"},
				QueryTerms:       []string{"invite", "email", "member", "邀请", "成员"},
				Status:           "partial",
				EvidenceSummary:  "files=2 routes=1 components=1 selectors=1 apis=0 models=0",
				RemainingGaps:    []string{"api_or_data_model"},
				ToolCallIDs:      []string{"tool_grep_text_invite", "tool_evidence_review_invite"},
				Confidence:       0.74,
			}},
		},
	}}
	pkg, err := NewScriptPackagerAgent().PackageScript(context.Background(), project, report, productMap, graph)
	if err != nil {
		t.Fatal(err)
	}
	stage := stageApprovalByNodeID(pkg.ExecutableBundle, "node_invite")
	if stage == nil {
		t.Fatal("expected invite stage")
	}
	if !investigationQuestionRefsContain(stage.InvestigationQuestionRefs, "question_team_invite") {
		t.Fatalf("stage should reference investigation question, got %+v", stage.InvestigationQuestionRefs)
	}
	outlineStage := outlineStageByNodeID(pkg.ExecutableBundle, "node_invite")
	if outlineStage == nil {
		t.Fatal("expected invite outline stage")
	}
	if !investigationQuestionRefsContain(outlineStage.InvestigationQuestionRefs, "question_team_invite") {
		t.Fatalf("outline stage should carry investigation question refs, got %+v", outlineStage.InvestigationQuestionRefs)
	}
	if !strings.Contains(pkg.ExecutableBundle.AgentPromptPolicy.SystemPrompt, "investigation_question_refs") {
		t.Fatalf("prompt policy should explain investigation_question_refs, got:\n%s", pkg.ExecutableBundle.AgentPromptPolicy.SystemPrompt)
	}
}

func TestScriptPackagerRouteAwarePlanFiltersSourcePaths(t *testing.T) {
	project, report, productMap, graph := executableBundleFixtures()
	graph.Nodes[1].Title = "新建项目"
	graph.Nodes[1].Goal = "进入新建项目流程并填写项目名称。"
	graph.Nodes[1].ExpectedOutcome = "进入新建项目流程"
	graph.Nodes[1].ActionSpec.Target.Selector = "[data-testid='new-project']"
	graph.Nodes[1].ActionSpec.Target.Label = "新建项目"
	intelligence := &model.ProjectIntelligencePack{
		ID:            "intel_routes",
		ProjectID:     project.ID,
		SchemaVersion: model.ProjectIntelligencePackSchemaVersion,
		RunIntentScope: &model.RunIntentScope{
			ProductOrigin: "https://app.example.com",
			ProductURL:    project.ProductURL,
		},
		Architecture: &model.ProjectArchitectureMap{
			RouteTree: []model.ArchitectureRouteNode{
				{ID: "route_source_file", Path: "/project/src/App.tsx", Name: "source file", EvidenceRefs: []model.EvidenceRef{{ID: "ev_source_route", Kind: model.EvidenceKindSourceCode}}},
				{ID: "route_app", Path: "/app", Name: "应用工作台", EvidenceRefs: []model.EvidenceRef{{ID: "ev_app_route", Kind: model.EvidenceKindSourceCode}}},
				{ID: "route_login", Path: "/login", Name: "登录页", EvidenceRefs: []model.EvidenceRef{{ID: "ev_login_route", Kind: model.EvidenceKindSourceCode}}},
			},
		},
	}
	pkg, err := NewScriptPackagerAgent().PackageScript(context.Background(), project, report, productMap, graph, intelligence)
	if err != nil {
		t.Fatal(err)
	}
	stage := stageApprovalByNodeID(pkg.ExecutableBundle, "node_invite")
	if stage == nil {
		t.Fatal("expected node_invite stage")
	}
	if strings.Contains(stage.TargetRoute, "src/") || strings.Contains(stage.TargetRouteTemplate, "src/") {
		t.Fatalf("stage route should not use source path: %+v", stage)
	}
	if stage.TargetRoute != "/app" {
		t.Fatalf("expected business route to resolve to /app, got %+v", stage)
	}
	if len(stage.CandidateRoutes) == 0 || stage.CandidateRoutes[0].Route != "/app" {
		t.Fatalf("expected /app candidate route, got %+v", stage.CandidateRoutes)
	}
	if !strings.Contains(pkg.Markdown, "页面路由：/app") {
		t.Fatalf("approval markdown should show route-aware page route, got:\n%s", pkg.Markdown)
	}
}

func TestScriptPackagerRouteAwarePlanUsesProductSubRoutes(t *testing.T) {
	project, report, productMap, graph := executableBundleFixtures()
	project.ProductURL = "https://cascadeai.cn"
	project.AccessPolicy.AllowedDomains = []string{"cascadeai.cn"}
	graph.EntryPoint = "https://cascadeai.cn"
	graph.Nodes = []*model.GraphNode{
		{
			ID:              "start",
			Type:            model.GraphNodeTypeAction,
			Title:           "打开产品入口",
			ExpectedOutcome: "产品入口页面加载完成",
			ActionSpec:      &model.GraphAction{Type: model.GraphActionNavigate, Target: model.ActionTarget{URL: "https://cascadeai.cn"}, TimeoutMS: 7000},
			DurationHintMS:  7000,
		},
		{
			ID:              "login",
			Type:            model.GraphNodeTypeAction,
			Title:           "演示登录完成并进入工作台",
			ExpectedOutcome: "登录完成，进入可演示的产品工作台上下文。",
			ActionSpec:      &model.GraphAction{Type: model.GraphActionWait, TimeoutMS: 7000},
			DurationHintMS:  7000,
		},
		{
			ID:              "new_project",
			Type:            model.GraphNodeTypeAction,
			Title:           "新建项目",
			Goal:            "点击新建项目入口。",
			ExpectedOutcome: "点击新建项目入口",
			ActionSpec:      &model.GraphAction{Type: model.GraphActionClick, Target: model.ActionTarget{Selector: "[data-testid='new-project']", Label: "新建项目"}, TimeoutMS: 13000},
			DurationHintMS:  13000,
			Metadata:        map[string]any{"verification_status": "verified"},
		},
		{
			ID:              "project_name",
			Type:            model.GraphNodeTypeAction,
			Title:           "输入项目名称：俄罗斯方块",
			Goal:            "填写项目名称。",
			ExpectedOutcome: "填写项目名称",
			ActionSpec:      &model.GraphAction{Type: model.GraphActionFill, Target: model.ActionTarget{Selector: "input[aria-label*='项目']", Label: "项目名称"}, Value: "俄罗斯方块", TimeoutMS: 13000},
			DurationHintMS:  13000,
			Metadata:        map[string]any{"verification_status": "verified"},
		},
		{
			ID:              "build_mode",
			Type:            model.GraphNodeTypeAction,
			Title:           "选择构建模式",
			Goal:            "选择构建模式。",
			ExpectedOutcome: "选择构建模式",
			ActionSpec:      &model.GraphAction{Type: model.GraphActionClick, Target: model.ActionTarget{Selector: "[data-testid='build-mode']", Label: "构建模式"}, TimeoutMS: 13000},
			DurationHintMS:  13000,
			Metadata:        map[string]any{"verification_status": "verified"},
		},
		{
			ID:              "start_agent_build",
			Type:            model.GraphNodeTypeAction,
			Title:           "启动 agent 实际构建",
			Goal:            "启动 agent 构建。",
			ExpectedOutcome: "启动 agent 构建",
			ActionSpec:      &model.GraphAction{Type: model.GraphActionClick, Target: model.ActionTarget{Selector: "[data-testid='start-build']", Label: "启动构建"}, TimeoutMS: 13000},
			DurationHintMS:  13000,
			Metadata:        map[string]any{"verification_status": "verified"},
		},
		{
			ID:              "agent_build_wait",
			Type:            model.GraphNodeTypeAction,
			Title:           "等待 agent 实际构建 45 秒",
			Goal:            "持续观察 agent 构建过程。",
			ExpectedOutcome: "持续观察 agent 构建过程，等待结果逐步出现。",
			ActionSpec:      &model.GraphAction{Type: model.GraphActionWait, TimeoutMS: 45000},
			DurationHintMS:  45000,
			Metadata:        map[string]any{"verification_status": "runtime_adaptive"},
		},
	}
	intelligence := cascadeRouteIntelligenceForTest(project)
	pkg, err := NewScriptPackagerAgent().PackageScript(context.Background(), project, report, productMap, graph, intelligence)
	if err != nil {
		t.Fatal(err)
	}

	login := stageApprovalByNodeID(pkg.ExecutableBundle, "login")
	if login == nil || login.TargetRoute != "/login" || login.ExpectedRouteAfterAction != "/app" {
		t.Fatalf("expected user login to use /login then /app, got %+v", login)
	}
	newProject := stageApprovalByNodeID(pkg.ExecutableBundle, "new_project")
	if newProject == nil || newProject.TargetRoute != "/app" || newProject.ExpectedRouteAfterAction != "/app" {
		t.Fatalf("expected new project entry to stay in app workspace, got %+v", newProject)
	}
	projectName := stageApprovalByNodeID(pkg.ExecutableBundle, "project_name")
	if projectName == nil || projectName.TargetRoute != "/app" || projectName.ExpectedRouteAfterAction != "/app" {
		t.Fatalf("expected project name fill to stay in app workspace, got %+v", projectName)
	}
	buildMode := stageApprovalByNodeID(pkg.ExecutableBundle, "build_mode")
	if buildMode == nil || buildMode.TargetRoute != "/app" || buildMode.ExpectedRouteAfterAction != "/app" {
		t.Fatalf("expected build mode selection to stay in app workspace, got %+v", buildMode)
	}
	startBuild := stageApprovalByNodeID(pkg.ExecutableBundle, "start_agent_build")
	if startBuild == nil || startBuild.TargetRouteTemplate != "/project/{id}" || startBuild.ExpectedRouteAfterAction != "/project/{id}" || startBuild.TargetURL != "" {
		t.Fatalf("expected start build to resolve to dynamic project route without fabricated URL, got %+v", startBuild)
	}
	waitBuild := stageApprovalByNodeID(pkg.ExecutableBundle, "agent_build_wait")
	if waitBuild == nil || waitBuild.EntryRoute != "/project/{id}" || waitBuild.TargetRouteTemplate != "/project/{id}" {
		t.Fatalf("expected build wait to continue on dynamic project route, got %+v", waitBuild)
	}
	if login.CapturePlan == nil || login.CapturePlan.ShotType != "session_setup" || !strings.Contains(login.CapturePlan.ClipSuggestion, "不展示明文密码") {
		t.Fatalf("login stage should carry safe login capture plan, got %+v", login.CapturePlan)
	}
	if waitBuild.CapturePlan == nil || waitBuild.CapturePlan.MinDurationMS != 45000 || !containsString(waitBuild.CapturePlan.RequiredAssets, "stage_video") || !strings.Contains(waitBuild.CapturePlan.ClipSuggestion, "连续录屏") {
		t.Fatalf("build observation should carry explicit continuous capture plan, got %+v", waitBuild.CapturePlan)
	}
	outlineWait := outlineStageByNodeID(pkg.ExecutableBundle, "agent_build_wait")
	if outlineWait == nil || outlineWait.CapturePlan == nil || outlineWait.CapturePlan.MinDurationMS != 45000 {
		t.Fatalf("outline should carry explicit capture plan for browser agent, got %+v", outlineWait)
	}
	if !strings.Contains(pkg.Markdown, "页面路由：/project/{id}") {
		t.Fatalf("approval markdown should expose dynamic project route, got:\n%s", pkg.Markdown)
	}
	if !strings.Contains(pkg.Markdown, "素材意图：") || !strings.Contains(pkg.Markdown, "最低 45 秒") {
		t.Fatalf("approval markdown should expose material capture intent and duration, got:\n%s", pkg.Markdown)
	}
	for _, stage := range pkg.ExecutableBundle.StageApprovalPlan.Stages {
		for _, candidate := range stage.CandidateRoutes {
			if strings.Contains(candidate.Route, "/admin/login") && stage.NodeID == "login" {
				t.Fatalf("normal user login should not prefer admin login candidate: %+v", stage.CandidateRoutes)
			}
			if strings.Contains(candidate.Route, "/aigc") || strings.Contains(candidate.Route, "/.well-known") || strings.Contains(candidate.Route, "/src/") {
				t.Fatalf("stage includes forbidden/control/source route candidate: %+v", candidate)
			}
		}
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

func TestScriptPackagerPreservesExplicitStageDurations(t *testing.T) {
	project, report, productMap, graph := executableBundleFixtures()
	pkg, err := NewScriptPackagerAgent().PackageScript(context.Background(), project, report, productMap, graph)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]int{
		"node_open":    2000,
		"node_invite":  3000,
		"node_success": 2000,
	}
	for _, step := range pkg.Document.Steps {
		if got := step.Timing.DurationMS; got != expected[step.NodeID] {
			t.Fatalf("expected explicit stage duration to be preserved, got %s=%d", step.NodeID, got)
		}
	}
	if pkg.ExecutableBundle.StageApprovalPlan == nil {
		t.Fatal("expected stage approval plan")
	}
	for _, stage := range pkg.ExecutableBundle.StageApprovalPlan.Stages {
		if got := stage.DurationMS; got != expected[stage.NodeID] {
			t.Fatalf("expected stage approval duration to preserve graph hint, got %+v", stage)
		}
		if stage.CapturePlan == nil || stage.CapturePlan.MinDurationMS != expected[stage.NodeID] {
			t.Fatalf("expected capture plan to preserve explicit duration, got %+v", stage.CapturePlan)
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
	project.ProductDescription = "演示登录（7s），新建项目（13s，2048，构建模式），agent实际构建演示（45s等待）"
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
	for _, want := range []string{"script_intent_mismatch", "2048", "构建模式", "45 秒"} {
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

func stageApprovalByNodeID(bundle *model.ExecutableRecordingScriptBundle, nodeID string) *model.StageApprovalStage {
	if bundle == nil || bundle.StageApprovalPlan == nil {
		return nil
	}
	for i := range bundle.StageApprovalPlan.Stages {
		if bundle.StageApprovalPlan.Stages[i].NodeID == nodeID {
			return &bundle.StageApprovalPlan.Stages[i]
		}
	}
	return nil
}

func investigationQuestionRefsContain(refs []model.InvestigationQuestionRef, id string) bool {
	for _, ref := range refs {
		if ref.ID == id {
			return true
		}
	}
	return false
}

func cascadeRouteIntelligenceForTest(project *model.ProjectContext) *model.ProjectIntelligencePack {
	return &model.ProjectIntelligencePack{
		ID:            "intel_cascade_routes",
		ProjectID:     project.ID,
		SchemaVersion: model.ProjectIntelligencePackSchemaVersion,
		RunIntentScope: &model.RunIntentScope{
			ProductOrigin: "https://cascadeai.cn",
			ProductURL:    project.ProductURL,
		},
		Architecture: &model.ProjectArchitectureMap{
			RouteTree: []model.ArchitectureRouteNode{
				{ID: "route_home", Path: "/", Name: "首页", EvidenceRefs: []model.EvidenceRef{{ID: "ev_route_home", Kind: model.EvidenceKindSourceCode}}},
				{ID: "route_login", Path: "/login", Name: "用户登录页", EvidenceRefs: []model.EvidenceRef{{ID: "ev_route_login", Kind: model.EvidenceKindSourceCode}}},
				{ID: "route_admin_login", Path: "/admin/login", Name: "管理员登录页", EvidenceRefs: []model.EvidenceRef{{ID: "ev_route_admin_login", Kind: model.EvidenceKindSourceCode}}},
				{ID: "route_auth", Path: "/auth", Name: "认证回调", EvidenceRefs: []model.EvidenceRef{{ID: "ev_route_auth", Kind: model.EvidenceKindSourceCode}}},
				{ID: "route_app", Path: "/app", Name: "应用工作台", AuthRequired: true, EvidenceRefs: []model.EvidenceRef{{ID: "ev_route_app", Kind: model.EvidenceKindSourceCode}}},
				{ID: "route_project_detail", Path: "/project/:id", Name: "项目详情与构建工作区", AuthRequired: true, EvidenceRefs: []model.EvidenceRef{{ID: "ev_route_project", Kind: model.EvidenceKindSourceCode}}},
				{ID: "route_builder_square", Path: "/BuilderSquare", Name: "应用广场", EvidenceRefs: []model.EvidenceRef{{ID: "ev_route_square", Kind: model.EvidenceKindSourceCode}}},
				{ID: "route_aigc", Path: "/aigc/v1/execution-packages", Name: "exchange control plane", EvidenceRefs: []model.EvidenceRef{{ID: "ev_route_control", Kind: model.EvidenceKindSourceCode}}},
				{ID: "route_source_file", Path: "/project/src/App.tsx", Name: "source file", EvidenceRefs: []model.EvidenceRef{{ID: "ev_route_source", Kind: model.EvidenceKindSourceCode}}},
			},
		},
	}
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
