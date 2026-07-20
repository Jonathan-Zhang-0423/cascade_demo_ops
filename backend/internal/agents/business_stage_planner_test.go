package agents

import (
	"context"
	"testing"

	"cascade-demoops/backend/internal/model"
)

func TestBusinessStagePlannerCreatesRequirementDrivenTetrisStages(t *testing.T) {
	project := graphQualityProject()
	project.ProductDescription = "演示登录 10 秒，新建项目 10 秒，项目名称俄罗斯方块，选择构建模式，启动 agent 实际构建，并等待 60 秒观察。"
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
	if plan.CoreBusinessStageCount != 4 {
		t.Fatalf("expected 4 core business stages, got %d", plan.CoreBusinessStageCount)
	}
	inputStage := plan.Stages[2]
	if inputStage.Action.InputValue != "俄罗斯方块" || inputStage.Kind != model.BusinessStageKindBusinessInput {
		t.Fatalf("business input did not preserve project name semantics: %+v", inputStage)
	}
	observeStage := plan.Stages[5]
	if observeStage.DurationMS < 60000 || observeStage.Action.Type != string(model.GraphActionWait) {
		t.Fatalf("observe stage must preserve 60s wait semantics, got %+v", observeStage)
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

func TestBusinessStagePlannerUsesCodeDiscoveredStageRoutes(t *testing.T) {
	project := graphQualityProject()
	project.ProductDescription = "演示登录，新建项目，项目名称俄罗斯方块，选择构建模式，启动 agent 构建并观察 60 秒。"
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
