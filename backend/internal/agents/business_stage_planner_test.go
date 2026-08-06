package agents

import (
	"context"
	"strings"
	"testing"

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

func TestBusinessStagePlannerCreatesRequirementDrivenTetrisStages(t *testing.T) {
	project := graphQualityProject()
	project.ProductDescription = "演示登录 7 秒，新建项目 13 秒，项目名称俄罗斯方块，选择构建模式，启动 agent 实际构建，并等待 45 秒观察。"
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
		ID:                  "verified_tetris",
		ProjectID:           project.ID,
		SchemaVersion:       model.ProjectIntelligencePackSchemaVersion,
		BusinessActionCount: 3,
		Actions: []model.VerifiedInteractionAction{
			{ID: "login", Label: "Login", Kind: "click", Selector: "[data-testid='login-submit']", IsBusiness: false, VerificationStatus: "verified", SelectorScore: 100},
			{ID: "new_project", Label: "新建项目", Kind: "click", Selector: "[data-testid='new-project']", IsBusiness: true, VerificationStatus: "verified", SelectorScore: 100},
			{ID: "project_name", Label: "项目名称", Kind: "fill", Selector: "[data-testid='project-name']", InputValue: "俄罗斯方块", IsBusiness: true, VerificationStatus: "verified", SelectorScore: 100},
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
	if inputStage.Action.InputValue != "俄罗斯方块" || inputStage.Kind != model.BusinessStageKindBusinessInput {
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
	if validation.Kind != "element_visible" {
		t.Fatalf("expected result visibility validation, got %+v", validation)
	}
	if validation.Target.TestID != "input-project-idea" || strings.Contains(validation.Target.Selector, "project-list") {
		t.Fatalf("validation reused clicked/stale target: %+v", validation.Target)
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
	if got := stageByKind[model.BusinessStageKindBusinessSubmit].ExpectedRouteAfterAction; got != "/workspace/projects/:id/build" {
		t.Fatalf("start build stage should transition to discovered build route, got %q", got)
	}
	if got := stageByKind[model.BusinessStageKindObserveProgress].EntryRoute; got != "/workspace/projects/:id/build" {
		t.Fatalf("observe stage should use discovered build route, got %q", got)
	}
}
