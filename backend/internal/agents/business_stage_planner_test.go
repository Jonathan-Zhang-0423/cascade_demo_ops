package agents

import (
	"context"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestMissingEvidenceKeepsFailedLoginDiagnosticWhenMarketingControlsWereFound(t *testing.T) {
	project := &model.ProjectContext{ID: "project_login_diagnostic"}
	response := interactionVerifierResponse{
		OK: true,
		Results: []interactionVerifierResult{{
			interactionVerifierItem: interactionVerifierItem{ID: "marketing_cta", Label: "Get one month free", Kind: "click", Selector: "[data-testid='join']"},
			Status:                  "verified", Visible: true, Enabled: true,
		}},
		Diagnostics: &interactionVerifierDiagnostics{
			LoginAttempted: true, LoginStatus: "login_form_not_found", FinalURL: "https://product.example/",
			VerifiedCandidateCount: 1, DiscoveredBusinessControlCount: 1,
		},
	}

	report := missingEvidenceFromScanResults(project, nil, response, nil)
	if report == nil || len(report.Items) != 1 || report.Items[0].MissingKind != "login_form_not_found" {
		t.Fatalf("failed login diagnostic was lost because an unrelated control was verified: %+v", report)
	}
	if !report.Blocking {
		t.Fatal("a failed login must block even when an unrelated marketing control was verified")
	}
}

func TestIntentProjectNameDistinguishesNumericNamesFromDurations(t *testing.T) {
	tests := []struct {
		intent string
		want   string
	}{
		{intent: "新建项目（13s，2048，构建模式）", want: "2048"},
		{intent: "项目名称为2026", want: "2026"},
		{intent: "项目名称：俄罗斯方块", want: "俄罗斯方块"},
		{intent: "新建名为“俄罗斯方块”的项目，要求 Agent 实际生成代码", want: "俄罗斯方块"},
		{intent: "登录、创建俄罗斯方块项目、等待 Agent 真正编写完代码", want: "俄罗斯方块"},
		{intent: "新建项目 启动 Agent 实际构建", want: ""},
		{intent: "新建项目（13秒，构建模式）", want: ""},
	}
	for _, test := range tests {
		if got := intentProjectName(test.intent); got != test.want {
			t.Fatalf("intentProjectName(%q)=%q, want %q", test.intent, got, test.want)
		}
	}
}

func TestCompletionWaitUsesMaximumAsTimeoutNotCaptureDuration(t *testing.T) {
	intent := "等待 Agent 真正编写完代码，轮询直到全部步骤完成，最多 20 分钟；最终成片时长 2 分钟。"
	if got := requiredObservationDurationMS(intent); got != 0 {
		t.Fatalf("a maximum completion wait must not become a minimum capture duration, got %d", got)
	}
	if got := completionWaitTimeoutMS(intent); got != 20*60*1000 {
		t.Fatalf("completion timeout=%d, want %d", got, 20*60*1000)
	}
	if got := completionWaitTimeoutMS("等待 Agent 构建完成"); got != maxBuildCompletionWaitMS {
		t.Fatalf("default completion timeout=%d, want %d", got, maxBuildCompletionWaitMS)
	}
	if got := completionWaitTimeoutMS("停留在项目详情页查看状态"); got != 0 {
		t.Fatalf("ordinary final observation must not receive a long poll, got %d", got)
	}
	finalFilmIntent := "登录并创建项目，最终成片时长 2 分钟，输出 MP4。"
	if got := durationMSForIntentKeywords(finalFilmIntent, "登录"); got != 0 {
		t.Fatalf("final-film duration leaked into login capture timing: %d", got)
	}
	if got := durationMSForIntentKeywords(finalFilmIntent, "最终", "结果"); got != 0 {
		t.Fatalf("final-film duration leaked into browser final-observe timing: %d", got)
	}
	globalDemoIntent := "面向产品团队制作约120秒真实操作演示：通过安全凭据登录并创建项目。"
	if got := durationMSForIntentKeywords(globalDemoIntent, "登录"); got != 0 {
		t.Fatalf("global demo duration leaked into login capture timing: %d", got)
	}
}

func TestBusinessStagePlannerAddsBoundedKeyboardPlayabilityVerification(t *testing.T) {
	project := graphQualityProject()
	project.ProductDescription = "创建俄罗斯方块项目，等待 Agent 真正编写完成，打开最终预览看清棋盘、得分和操作说明，再按左、右、下和旋转键，确认方块位置或形状变化。"
	plan, err := NewBusinessStagePlannerAgent().PlanBusinessStages(context.Background(), project, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var preview, keyboard *model.BusinessStage
	for index := range plan.Stages {
		switch plan.Stages[index].ID {
		case "business_stage_playable_preview":
			preview = &plan.Stages[index]
		case "business_stage_verify_playable_controls":
			keyboard = &plan.Stages[index]
		}
	}
	if preview == nil || keyboard == nil {
		t.Fatalf("playability stages missing: %+v", plan.Stages)
	}
	if keyboard.Action.Type != string(model.GraphActionPress) || keyboard.Action.Parameters["keys"] != "ArrowLeft,ArrowRight,ArrowDown,ArrowUp" || !keyboard.Action.NonDestructive {
		t.Fatalf("keyboard stage is not strictly bounded: %+v", keyboard)
	}
	node := graphNodeFromBusinessStage(project, *keyboard, project.ProductURL, "feature_playable")
	if node.ActionSpec == nil || node.ActionSpec.Type != model.GraphActionPress || len(node.Validations) != 1 || node.Validations[0].Kind != "page_changed" || !node.Validations[0].Required {
		t.Fatalf("keyboard graph node must require visual-change evidence: %+v", node)
	}
}

func TestBusinessStagePlannerCreatesRequirementDrivenProjectStages(t *testing.T) {
	project := graphQualityProject()
	project.ProductDescription = "演示登录 7 秒，新建项目 13 秒，项目名称2048，选择构建模式，启动 agent 实际构建，并等待 45 秒观察。"
	project.DemoAccount = &model.DemoAccount{
		UsernameSecretRef: "secret://demo/username",
		PasswordSecretRef: "secret://demo/password",
	}
	intelligence := graphQualityIntelligence()
	intelligence.DemoIntent.Objective = project.ProductDescription
	intelligence.Architecture = &model.ProjectArchitectureMap{
		RouteTree: []model.ArchitectureRouteNode{
			{ID: "route_login", Path: "/login", Name: "登录"},
			{ID: "route_app", Path: "/app", Name: "工作台", AuthRequired: true},
			{ID: "route_project", Path: "/project/:id", Name: "项目详情", AuthRequired: true},
		},
	}
	verified := &model.VerifiedInteractionPlan{
		ID:                  "verified_project",
		ProjectID:           project.ID,
		SchemaVersion:       model.ProjectIntelligencePackSchemaVersion,
		BusinessActionCount: 3,
		Actions: []model.VerifiedInteractionAction{
			{ID: "login", Label: "Login", Kind: "click", Selector: "[data-testid='login-submit']", IsBusiness: false, VerificationStatus: "verified", SelectorScore: 100},
			{ID: "new_project", Label: "新建项目", Kind: "click", Selector: "[data-testid='new-project']", IsBusiness: true, VerificationStatus: "verified", SelectorScore: 100},
			{ID: "project_name", Label: "项目名称", Kind: "fill", Selector: "[data-testid='project-name']", InputValue: "2048", IsBusiness: true, VerificationStatus: "verified", SelectorScore: 100},
			{ID: "build_mode", Label: "构建模式", Kind: "click", Selector: "[data-testid='build-mode']", IsBusiness: true, VerificationStatus: "verified", SelectorScore: 100},
			{ID: "start_build", Label: "启动 agent 构建", Kind: "click", Selector: "[data-testid='start-build']", IsBusiness: true, VerificationStatus: "verified", SelectorScore: 100},
		},
	}

	plan, err := NewBusinessStagePlannerAgent().PlanBusinessStages(context.Background(), project, nil, graphQualityReport(project), graphQualityProductMap(), intelligence, verified)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.BusinessStageKind{
		model.BusinessStageKindSessionSetup,
		model.BusinessStageKindBusinessAction,
		model.BusinessStageKindBusinessInput,
		model.BusinessStageKindModeSelection,
		model.BusinessStageKindBusinessSubmit,
		model.BusinessStageKindObserveProgress,
		model.BusinessStageKindFinalObserve,
	}
	if len(plan.Stages) != len(want) {
		t.Fatalf("expected %d stages, got %d: %+v", len(want), len(plan.Stages), plan.Stages)
	}
	for i, kind := range want {
		if plan.Stages[i].Kind != kind {
			t.Fatalf("stage %d kind: got %s want %s", i, plan.Stages[i].Kind, kind)
		}
		if containsString(plan.Stages[i].Action.WaitConditions, "networkidle") {
			t.Fatalf("stage %d must not add an unconditional networkidle wait: %+v", i, plan.Stages[i].Action.WaitConditions)
		}
	}
	for _, index := range []int{1, 2, 3, 4} {
		if !plan.Stages[index].Action.NonDestructive {
			t.Fatalf("approved project-creation stage must be non-destructive: %+v", plan.Stages[index])
		}
	}
	if plan.CoreBusinessStageCount != 4 {
		t.Fatalf("expected 4 core business stages, got %d", plan.CoreBusinessStageCount)
	}
	inputStage := plan.Stages[2]
	if inputStage.Action.InputValue != "2048" || inputStage.Kind != model.BusinessStageKindBusinessInput {
		t.Fatalf("business input did not preserve project name semantics: %+v", inputStage)
	}
	if got := plan.Stages[0].DurationMS; got != 7000 {
		t.Fatalf("login stage must preserve explicit user duration, got %d", got)
	}
	if got := plan.Stages[1].DurationMS; got != 13000 {
		t.Fatalf("new project stage must preserve explicit user duration, got %d", got)
	}
	observeStage := plan.Stages[5]
	if observeStage.DurationMS != 45000 || observeStage.Action.Type != string(model.GraphActionWait) {
		t.Fatalf("observe stage must preserve explicit wait semantics, got %+v", observeStage)
	}
}

func TestBusinessStagePlannerDoesNotCrossBindAllowlistedActionTargets(t *testing.T) {
	project := graphQualityProject()
	project.ProductDescription = "进入新建项目，填写项目名俄罗斯方块，选择构建模式，启动 Agent 构建。"
	verified := &model.VerifiedInteractionPlan{Actions: []model.VerifiedInteractionAction{
		{ID: "new", Label: "新建项目", Kind: "click", Selector: "[data-testid='new-project']", IsBusiness: true, VerificationStatus: "verified"},
		{ID: "name", Label: "项目名称", Kind: "fill", Selector: "[data-testid='project-name-input']", IsBusiness: true, VerificationStatus: "verified"},
		{ID: "mode", Label: "选择构建模式", Kind: "click", Selector: "[data-testid='build-mode']", IsBusiness: true, VerificationStatus: "verified"},
		{ID: "start", Label: "启动 Agent 构建", Kind: "click", Selector: "[data-testid='start-build']", IsBusiness: true, VerificationStatus: "verified"},
	}}
	plan, err := NewBusinessStagePlannerAgent().PlanBusinessStages(context.Background(), project, nil, nil, nil, graphQualityIntelligence(), verified)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"business_stage_new_project_entry":  "[data-testid='new-project']",
		"business_stage_project_name_input": "[data-testid='project-name-input']",
		"business_stage_select_build_mode":  "[data-testid='build-mode']",
		"business_stage_start_agent_build":  "[data-testid='start-build']",
	}
	for _, stage := range plan.Stages {
		selector, ok := want[stage.ID]
		if !ok {
			continue
		}
		if len(stage.Targets) == 0 || stage.Targets[0].Selector != selector {
			t.Fatalf("stage %s cross-bound its target: got=%+v want=%s", stage.ID, stage.Targets, selector)
		}
	}
}

func TestBusinessStageNonDestructiveClassifierRejectsGenericAndDangerousActions(t *testing.T) {
	if businessStageIsApprovedNonDestructive(stageSpec{id: "primary_business_action", actionType: string(model.GraphActionClick)}) {
		t.Fatal("generic click must not be classified as non-destructive")
	}
	if businessStageIsApprovedNonDestructive(stageSpec{
		id: "start_agent_build", actionType: string(model.GraphActionClick), actionLabel: "启动构建并删除项目",
	}) {
		t.Fatal("dangerous action semantics must override the allowlist")
	}
	if !businessStageIsApprovedNonDestructive(stageSpec{
		id: "start_agent_build", actionType: string(model.GraphActionClick), actionLabel: "启动 agent 构建",
	}) {
		t.Fatal("explicit project build action should be classified as non-destructive")
	}
}

func TestBusinessStageResultValidationDoesNotReuseClickedButton(t *testing.T) {
	stage := model.BusinessStage{
		ID:     "business_stage_new_project_entry",
		Kind:   model.BusinessStageKindBusinessAction,
		Action: model.BusinessActionSemantics{Type: string(model.GraphActionClick), Label: "新建项目"},
		Targets: []model.BusinessTargetCandidate{
			{Label: "新建项目", TestID: "button-new-project", Selector: "[data-testid='project-list']"},
			{Label: "项目输入框", TestID: "input-project-idea", Selector: "[data-testid='input-project-idea']"},
		},
	}
	validation := businessStageValidation(stage, model.GraphActionClick, businessStageActionTarget(stage, "https://app.example/app"), true)
	if validation.Kind != "text_contains" || validation.Target.Role != "dialog" {
		t.Fatalf("unproven result selector should fall back to a semantic dialog validation, got %+v", validation)
	}
	if validation.Target.TestID != "" || validation.Target.Selector != "" || strings.Contains(validation.Target.Label, "project-list") {
		t.Fatalf("validation reused an unproven clicked/stale target: %+v", validation.Target)
	}
}

func TestBusinessTargetRankPrefersSpecificControlOverTextAggregatingContainer(t *testing.T) {
	container := model.BusinessTargetCandidate{
		Label: "新建项目 New project 项目名称 Project name 构建模式 Build mode 启动构建 Start build Builder workspace",
		Kind:  "click", Selector: "[data-testid='workspace']", TestID: "workspace", SelectorScore: 100, IsVerified: true,
	}
	button := model.BusinessTargetCandidate{
		Label: "新建项目", Kind: "click", Selector: "[data-testid='button-new-project']", TestID: "button-new-project", SelectorScore: 100, IsVerified: true,
	}
	if businessTargetRank(container) >= businessTargetRank(button) {
		t.Fatalf("a text-aggregating workspace container must not outrank the concrete new-project button: container=%d button=%d", businessTargetRank(container), businessTargetRank(button))
	}
}

func TestNewProjectEntryPrefersStableButtonOverExistingProjectNamedNewProject(t *testing.T) {
	stage := model.BusinessStage{
		ID:   "business_stage_new_project_entry",
		Kind: model.BusinessStageKindBusinessAction,
		Action: model.BusinessActionSemantics{
			Type: string(model.GraphActionClick), Label: "点击新建项目入口", NonDestructive: true,
		},
		Targets: []model.BusinessTargetCandidate{
			formalPageScanBusinessTarget("card-project-rh1fgnvomr6qbe7n", "click", "button", "03 新建项目"),
			formalPageScanBusinessTarget("text-project-name-rh1fgnvomr6qbe7n", "click", "button", "新建项目"),
			formalPageScanBusinessTarget("button-new-project", "click", "button", "新建项目"),
		},
	}

	target := businessStageActionTarget(stage, "https://cascadeai.cn/app")
	if target.TestID != "button-new-project" || target.Selector != "[data-testid='button-new-project']" {
		t.Fatalf("existing project entities must not replace the stable new-project entry: %+v", target)
	}
}

func TestNewProjectResultRejectsExistingProjectNameAndUsesOpenedFormInput(t *testing.T) {
	stage := model.BusinessStage{
		ID:   "business_stage_new_project_entry",
		Kind: model.BusinessStageKindBusinessAction,
		Action: model.BusinessActionSemantics{
			Type: string(model.GraphActionClick), Label: "点击新建项目入口", SuccessState: "新建项目表单可见",
		},
		Targets: []model.BusinessTargetCandidate{
			formalPageScanBusinessTarget("text-project-name-rh1fgnvomr6qbe7n", "click", "button", "新建项目"),
			formalPageScanBusinessTarget("button-new-project", "click", "button", "新建项目"),
			formalPageScanBusinessTarget("input-project-idea", "fill", "textbox", "项目名称"),
		},
	}

	validation := businessStageValidation(stage, model.GraphActionClick, businessStageActionTarget(stage, "https://cascadeai.cn/app"), true)
	if validation.Kind != "element_visible" || validation.Target.TestID != "input-project-idea" || validation.Target.Selector != "[data-testid='input-project-idea']" {
		t.Fatalf("new-project validation must use the opened form input, got %+v", validation)
	}
}

func TestVerifierSafeStateTransitionUsesOnlyStableNewProjectEntry(t *testing.T) {
	project := &model.ProjectContext{ID: "project_safe_transition", ProductURL: "https://cascadeai.cn/app"}
	stage := model.BusinessStage{
		ID:   "business_stage_new_project_entry",
		Kind: model.BusinessStageKindBusinessAction,
		Action: model.BusinessActionSemantics{
			Type: string(model.GraphActionClick), Label: "点击新建项目入口", NonDestructive: true,
		},
		EntryRoute: "https://cascadeai.cn/app",
		Targets: []model.BusinessTargetCandidate{
			formalPageScanBusinessTarget("card-project-rh1fgnvomr6qbe7n", "click", "button", "03 新建项目"),
			formalPageScanBusinessTarget("button-new-project", "click", "button", "新建项目"),
		},
	}
	for index := range stage.Targets {
		stage.Targets[index].URL = project.ProductURL
	}
	intelligence := &model.ProjectIntelligencePack{
		RunIntentScope:    &model.RunIntentScope{ProductOrigin: "https://cascadeai.cn", ProductURL: project.ProductURL, AllowedOrigins: []string{"https://cascadeai.cn"}},
		BusinessStagePlan: &model.BusinessStagePlan{Stages: []model.BusinessStage{stage}},
	}

	transitions := verifierSafeStateTransitions(project, intelligence)
	if len(transitions) != 1 || transitions[0].Selector != "[data-testid='button-new-project']" {
		t.Fatalf("safe exploration selected a stale project entity: %+v", transitions)
	}
}

func formalPageScanBusinessTarget(testID string, kind string, role string, accessibleName string) model.BusinessTargetCandidate {
	now := time.Date(2026, 8, 18, 8, 48, 10, 0, time.UTC)
	evidence := model.EvidenceRef{ID: "ev_" + testID, Kind: model.EvidenceKindBrowserScan, Confidence: 0.9}
	selector := "[data-testid='" + testID + "']"
	return model.BusinessTargetCandidate{
		ID: "target_" + testID, Label: accessibleName, Kind: kind, Selector: selector, TestID: testID,
		SelectorScore: 100, IsVerified: true, VerificationStatus: "verified", VerificationSource: "playwright_readonly_scan",
		EvidenceRefs: []model.EvidenceRef{evidence},
		Alternatives: []model.SelectorCandidate{{
			Kind: "testid", Value: testID, EvidenceID: evidence.ID, SourceKind: "page_scan", SourceDigest: "sha256:" + testID,
			ObservedRole: role, ObservedAccessibleName: accessibleName, ObservedURL: "https://cascadeai.cn/app", ObservedAt: &now,
			EvidenceRefs: []model.EvidenceRef{evidence},
		}},
	}
}

func TestBusinessStagePlannerDoesNotBindLoginToProjectNameStage(t *testing.T) {
	project := graphQualityProject()
	project.ProductDescription = "登录后新建项目，项目名称俄罗斯方块。"
	project.DemoAccount = &model.DemoAccount{UsernameSecretRef: "secret://demo/username", PasswordSecretRef: "secret://demo/password"}
	intelligence := graphQualityIntelligence()
	intelligence.DemoIntent.Objective = project.ProductDescription
	verified := &model.VerifiedInteractionPlan{
		ID:                  "verified_login_noise",
		ProjectID:           project.ID,
		SchemaVersion:       model.ProjectIntelligencePackSchemaVersion,
		BusinessActionCount: 1,
		Actions: []model.VerifiedInteractionAction{
			{ID: "password", Label: "Password", Kind: "fill", Selector: "input[type=password]", IsBusiness: false, VerificationStatus: "verified", SelectorScore: 90},
			{ID: "project_name", Label: "项目名称", Kind: "fill", Selector: "[data-testid='project-name']", InputValue: "俄罗斯方块", IsBusiness: true, VerificationStatus: "verified", SelectorScore: 100},
		},
	}

	plan, err := NewBusinessStagePlannerAgent().PlanBusinessStages(context.Background(), project, nil, nil, graphQualityProductMap(), intelligence, verified)
	if err != nil {
		t.Fatal(err)
	}
	var inputStage *model.BusinessStage
	for i := range plan.Stages {
		if plan.Stages[i].Kind == model.BusinessStageKindBusinessInput {
			inputStage = &plan.Stages[i]
			break
		}
	}
	if inputStage == nil {
		t.Fatal("missing business input stage")
	}
	for _, target := range inputStage.Targets {
		if target.Selector == "input[type=password]" {
			t.Fatalf("login/password target was bound to project name stage: %+v", inputStage.Targets)
		}
	}
}

func TestBusinessStagePlannerBindsVerifiedLoginOutcomeToSessionStage(t *testing.T) {
	project := graphQualityProject()
	project.ProductDescription = "登录进入工作台，然后点击新建项目。"
	project.DemoAccount = &model.DemoAccount{UsernameSecretRef: "secret://demo/username", PasswordSecretRef: "secret://demo/password"}
	intelligence := graphQualityIntelligence()
	intelligence.DemoIntent.Objective = project.ProductDescription
	loginEvidence := model.EvidenceRef{ID: "ev_login_scan", Kind: model.EvidenceKindBrowserScan, Confidence: 0.92}
	verified := &model.VerifiedInteractionPlan{Actions: []model.VerifiedInteractionAction{{
		ID: "intent_login_observe", Label: "演示登录完成并进入工作台", Kind: "wait", URL: "https://app.example.com/workspace",
		VerificationStatus: "verified", EvidenceRefs: []model.EvidenceRef{loginEvidence},
	}}}

	plan, err := NewBusinessStagePlannerAgent().PlanBusinessStages(context.Background(), project, nil, nil, graphQualityProductMap(), intelligence, verified)
	if err != nil {
		t.Fatal(err)
	}
	var session *model.BusinessStage
	for i := range plan.Stages {
		if plan.Stages[i].Kind == model.BusinessStageKindSessionSetup {
			session = &plan.Stages[i]
			break
		}
	}
	if session == nil || len(session.Targets) != 1 || session.Targets[0].URL != "https://app.example.com/workspace" {
		t.Fatalf("verified login outcome was not bound to session stage: %+v", session)
	}
	if len(session.EvidenceRefs) != 1 || session.EvidenceRefs[0].ID != loginEvidence.ID {
		t.Fatalf("session stage lost login runtime evidence: %+v", session.EvidenceRefs)
	}
}

func TestBusinessStagePlannerUsesCodeDiscoveredStageRoutes(t *testing.T) {
	project := graphQualityProject()
	project.ProductDescription = "演示登录，新建项目，项目名称俄罗斯方块，选择构建模式，启动 agent 构建并观察 45 秒。"
	project.DemoAccount = &model.DemoAccount{UsernameSecretRef: "secret://demo/username", PasswordSecretRef: "secret://demo/password"}
	intelligence := graphQualityIntelligence()
	intelligence.DemoIntent.Objective = project.ProductDescription
	intelligence.Architecture = &model.ProjectArchitectureMap{
		RouteTree: []model.ArchitectureRouteNode{
			{ID: "route_login", Path: "/login", Name: "登录"},
			{ID: "route_workspace", Path: "/workspace", Name: "工作台", AuthRequired: true},
			{ID: "route_project_new", Path: "/workspace/projects/new", Name: "新建项目", AuthRequired: true},
			{ID: "route_project_detail", Path: "/workspace/projects/:id", Name: "项目详情", AuthRequired: true},
			{ID: "route_project_build", Path: "/workspace/projects/:id/build", Name: "构建进度", AuthRequired: true},
		},
	}

	plan, err := NewBusinessStagePlannerAgent().PlanBusinessStages(context.Background(), project, nil, nil, nil, intelligence, nil)
	if err != nil {
		t.Fatal(err)
	}
	stageByKind := map[model.BusinessStageKind]model.BusinessStage{}
	for _, stage := range plan.Stages {
		stageByKind[stage.Kind] = stage
	}
	if got := stageByKind[model.BusinessStageKindBusinessAction].ExpectedRouteAfterAction; got != "/workspace/projects/new" {
		t.Fatalf("new project stage should transition to discovered creation route, got %q", got)
	}
	if got := stageByKind[model.BusinessStageKindBusinessInput].EntryRoute; got != "/workspace/projects/new" {
		t.Fatalf("project name stage should use discovered creation route, got %q", got)
	}
	if got := stageByKind[model.BusinessStageKindBusinessSubmit].ExpectedRouteAfterAction; got != "/workspace/projects/{id}/build" {
		t.Fatalf("start build stage should transition to discovered build route, got %q", got)
	}
	if got := stageByKind[model.BusinessStageKindObserveProgress].EntryRoute; got != "/workspace/projects/{id}/build" {
		t.Fatalf("observe stage should use discovered build route, got %q", got)
	}
}
