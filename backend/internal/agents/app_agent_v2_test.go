package agents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

func TestCodeReaderScopesEvidenceToIntentAndFiltersNoise(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "src/pages/projects/NewProject.tsx", `
		export function NewProjectPage() {
			const route = "/projects/new"
			return <button data-testid="new-project">新建项目</button>
		}
		type ProjectCreateRequest = { name: string; mode: "build" }
		const createPath = "/api/projects/create"
	`)
	writeFixtureFile(t, root, "src/components/AppShell.tsx", `
		export function AppShell() {
			return <button data-testid="button-sidebar-toggle">toggle</button>
		}
	`)
	writeFixtureFile(t, root, "reports/generated.ts", `
		const nix = "/nix/store/aaaaaaaaaaaaaaaa-report"
		const exchange = "/aigc/.well-known/cascade-exchange"
		const register = "/v1/app-installations/register"
	`)

	project := &model.ProjectContext{
		ID:                 "project_code_focus",
		ProductURL:         "https://cascadeai.cn",
		ProductDescription: "演示登录，新建项目，俄罗斯方块，构建模式，agent实际构建演示",
		Inputs: &model.ProjectInputBundle{Code: []model.CodeInput{{
			ID:           "code_focus",
			Kind:         "repository",
			LocalPath:    root,
			RepositoryID: "repo_focus",
		}}},
	}
	brief := &model.RequirementBrief{
		ProjectID: project.ID,
		Objective: project.ProductDescription,
		MustShow:  []string{"新建项目", "俄罗斯方块", "构建模式"},
	}

	snapshots, err := NewCodeReaderAgent().ReadCode(context.Background(), project, brief)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("expected one snapshot, got %d", len(snapshots))
	}
	snapshot := snapshots[0]
	if !routeInsightContains(snapshot.Routes, "/projects/new") {
		t.Fatalf("expected intent route /projects/new, got %+v", snapshot.Routes)
	}
	if routeInsightContains(snapshot.Routes, "/aigc/.well-known/cascade-exchange") || routeInsightContains(snapshot.Routes, "/nix/store/aaaaaaaaaaaaaaaa-report") {
		t.Fatalf("control-plane or nix route leaked into product routes: %+v", snapshot.Routes)
	}
	if !apiInsightContains(snapshot.APIEndpoints, "/api/projects/create") {
		t.Fatalf("expected product API evidence, got %+v", snapshot.APIEndpoints)
	}
	if apiInsightContains(snapshot.APIEndpoints, "/v1/app-installations/register") {
		t.Fatalf("control-plane API leaked into product API evidence: %+v", snapshot.APIEndpoints)
	}
	if !selectorInsightContains(snapshot.Selectors, "[data-testid='new-project']") {
		t.Fatalf("expected new-project selector, got %+v", snapshot.Selectors)
	}
	if selectorInsightContains(snapshot.Selectors, "[data-testid='button-sidebar-toggle']") {
		t.Fatalf("chrome selector should not survive intent-scoped selector evidence: %+v", snapshot.Selectors)
	}
}

func TestVerifierFallsBackToRuntimeAdaptiveWhenScanFindsNoBusinessAction(t *testing.T) {
	project := graphQualityProject()
	project.ProductDescription = "演示登录（10s），新建项目（10s，俄罗斯方块，构建模式），agent实际构建演示（60s等待）"
	project.DemoAccount = &model.DemoAccount{UsernameSecretRef: "local-dev/demo_username", PasswordSecretRef: "local-dev/demo_password"}
	intelligence := graphQualityIntelligence()
	intelligence.RunIntentScope = runIntentScopeForProject(project)
	intelligence.DemoIntent.Objective = project.ProductDescription
	intelligence.DemoIntent.Goals = []model.DemoIntentGoal{
		{ID: "intent_login", Label: "登录", Required: true, TargetKeywords: []string{"登录"}},
		{ID: "intent_new_project", Label: "新建项目", Required: true, BusinessCritical: true, TargetKeywords: []string{"新建项目", "new project"}},
		{ID: "intent_tetris", Label: "俄罗斯方块", Required: true, BusinessCritical: true, TargetKeywords: []string{"俄罗斯方块", "tetris"}},
		{ID: "intent_build_mode", Label: "构建模式", Required: true, BusinessCritical: true, TargetKeywords: []string{"构建模式", "build mode"}},
		{ID: "intent_agent_build", Label: "agent实际构建演示", Required: true, BusinessCritical: true, TargetKeywords: []string{"agent", "构建", "生成"}},
	}
	intelligence.FeatureTrace = &model.FeatureTraceResult{
		ID:        "trace_empty_page",
		ProjectID: project.ID,
		Traces: []model.FeatureGoalTrace{{
			IntentGoalID:    "intent_new_project",
			IntentLabel:     "新建项目",
			MissingEvidence: []string{"selector"},
		}},
	}

	plan, missing, err := NewPageInteractionVerifierAgent().VerifyInteractions(context.Background(), project, nil, nil, nil, intelligence, orchestrator.PageVerificationCredentials{})
	if err != nil {
		t.Fatal(err)
	}
	if plan == nil || plan.BusinessActionCount < 4 {
		t.Fatalf("expected runtime-adaptive intent plan, got %+v", plan)
	}
	if missing == nil || missing.Blocking {
		t.Fatalf("expected non-blocking missing evidence warning, got %+v", missing)
	}
	if !runtimeAdaptiveActionContains(plan, "新建项目") || !runtimeAdaptiveActionContains(plan, "构建模式") || !runtimeAdaptiveActionContains(plan, "agent") {
		t.Fatalf("runtime-adaptive plan missed required business intent: %+v", plan.Actions)
	}
}

func writeFixtureFile(t *testing.T, root string, rel string, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func routeInsightContains(values []model.RouteInsight, path string) bool {
	for _, value := range values {
		if value.Path == path {
			return true
		}
	}
	return false
}

func apiInsightContains(values []model.APIEndpointInsight, path string) bool {
	for _, value := range values {
		if value.Path == path {
			return true
		}
	}
	return false
}

func selectorInsightContains(values []model.SelectorInsight, selector string) bool {
	for _, value := range values {
		if value.Value == selector {
			return true
		}
	}
	return false
}

func runtimeAdaptiveActionContains(plan *model.VerifiedInteractionPlan, text string) bool {
	if plan == nil {
		return false
	}
	for _, action := range plan.Actions {
		if action.VerificationStatus == "runtime_adaptive" && strings.Contains(action.Label+" "+action.ExpectedOutcome+" "+action.SuccessState, text) {
			return true
		}
	}
	return false
}
