package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/llm"
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
	if snapshot.InvestigationQuality == nil {
		t.Fatalf("expected code investigation quality summary")
	}
	if !snapshot.InvestigationQuality.ToolDriven {
		t.Fatalf("expected tool-driven investigation quality, got %+v", snapshot.InvestigationQuality)
	}
	if snapshot.InvestigationQuality.TotalFilesSelected != snapshot.FileCount || snapshot.InvestigationQuality.StructuredFileCount != snapshot.FileCount {
		t.Fatalf("quality summary should track structured read count, got quality=%+v file_count=%d", snapshot.InvestigationQuality, snapshot.FileCount)
	}
	if snapshot.InvestigationQuality.SourceTextPolicy != "no_raw_source_persisted" || snapshot.InvestigationQuality.OverreadRisk == "" {
		t.Fatalf("quality summary should expose source policy and overread risk, got %+v", snapshot.InvestigationQuality)
	}
	if snapshot.InvestigationQuality.OverreadRisk == "high" {
		t.Fatalf("small intent-scoped fixture should not be marked high overread, got %+v", snapshot.InvestigationQuality)
	}
	qualityJSON, err := json.Marshal(snapshot.InvestigationQuality)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"button-sidebar-toggle", "/aigc/.well-known/cascade-exchange", "/v1/app-installations/register", "/nix/store", "src/pages/projects/NewProject.tsx"} {
		if strings.Contains(string(qualityJSON), forbidden) {
			t.Fatalf("investigation quality leaked forbidden detail %q: %s", forbidden, string(qualityJSON))
		}
	}
}

func TestProjectInvestigationCollectCodeCandidatesUsesGitDiscoveryFirst(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	writeFixtureFile(t, root, ".gitignore", "ignored/\n")
	writeFixtureFile(t, root, "src/pages/projects/NewProject.tsx", `
		export function NewProjectPage() {
			return <button data-testid="new-project">新建项目</button>
		}
	`)
	writeFixtureFile(t, root, "src/pages/projects/NewlyAdded.tsx", `
		export function NewlyAdded() {
			return <button data-testid="newly-added">Newly added</button>
		}
	`)
	writeFixtureFile(t, root, "ignored/IgnoredPage.tsx", `
		export function IgnoredPage() {
			return <button data-testid="ignored-page">Ignored</button>
		}
	`)
	if err := exec.Command("git", "-C", root, "init").Run(); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", root, "add", ".gitignore", "src/pages/projects/NewProject.tsx").Run(); err != nil {
		t.Fatal(err)
	}
	candidates, err := collectCodeCandidates(context.Background(), root, 80*1024, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !candidateRelContains(candidates, "src/pages/projects/NewProject.tsx") {
		t.Fatalf("expected git-discovered tracked file in candidates, got %+v", candidates)
	}
	if !candidateRelContains(candidates, "src/pages/projects/NewlyAdded.tsx") {
		t.Fatalf("expected git-discovered untracked file in candidates, got %+v", candidates)
	}
	if candidateRelContains(candidates, "ignored/IgnoredPage.tsx") {
		t.Fatalf("expected ignored file to stay out of candidates, got %+v", candidates)
	}
}

func TestProjectInvestigationCollectCodeCandidatesSkipsNodeModulesForNonGitRepository(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("rg unavailable")
	}
	root := t.TempDir()
	writeFixtureFile(t, root, "frontend/web/src/pages/dashboard.tsx", `
		export function Dashboard() {
			return <button data-testid="button-new-project">New project</button>
		}
	`)
	writeFixtureFile(t, root, "node_modules/large-dependency/index.js", strings.Repeat("export const x = 1;\n", 20000))

	candidates, err := collectCodeCandidates(context.Background(), root, 80*1024, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !candidateRelContains(candidates, "frontend/web/src/pages/dashboard.tsx") {
		t.Fatalf("expected application source in candidates, got %+v", candidates)
	}
	if candidateRelContains(candidates, "node_modules/large-dependency/index.js") {
		t.Fatalf("node_modules must not be included in source summary candidates, got %+v", candidates)
	}
}

func TestCodeReaderUsesIntentDrivenBudget(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"react":"latest","vite":"latest"}}`)
	writeFixtureFile(t, root, "src/pages/projects/NewProject.tsx", `
		export function NewProjectPage() {
			const route = "/projects/new"
			return <button data-testid="new-project">新建项目</button>
		}
	`)
	for i := 0; i < 180; i++ {
		writeFixtureFile(t, root, filepath.ToSlash(filepath.Join("src", "unrelated", "File"+string(rune('a'+(i%26)))+string(rune('a'+((i/26)%26)))+".tsx")), `
			export function Filler() { return <div data-testid="profile-avatar">avatar</div> }
		`)
	}
	project := &model.ProjectContext{
		ID:                 "project_budget",
		ProductURL:         "https://cascadeai.cn",
		ProductDescription: "演示新建项目，俄罗斯方块，构建模式",
		Inputs: &model.ProjectInputBundle{Code: []model.CodeInput{{
			ID:           "code_budget",
			Kind:         "repository",
			LocalPath:    root,
			RepositoryID: "repo_budget",
		}}},
	}
	brief := &model.RequirementBrief{ProjectID: project.ID, Objective: project.ProductDescription, MustShow: []string{"新建项目", "构建模式"}}

	snapshots, err := NewCodeReaderAgent().ReadCode(context.Background(), project, brief)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := snapshots[0]
	if snapshot.ReadBudget == nil || snapshot.ReadBudget.TotalFileLimit != 32 || snapshot.ReadBudget.FilesPerRound != 6 || snapshot.ReadBudget.RepoIndexFileLimit != 24 {
		t.Fatalf("expected default intent-driven read budget, got %+v", snapshot.ReadBudget)
	}
	if snapshot.FileCount > 32 {
		t.Fatalf("CodeReader should not read more than budgeted files, read %d", snapshot.FileCount)
	}
	if !routeInsightContains(snapshot.Routes, "/projects/new") {
		t.Fatalf("budgeted read dropped relevant route: %+v", snapshot.Routes)
	}
	if selectorInsightContains(snapshot.Selectors, "[data-testid='profile-avatar']") {
		t.Fatalf("budgeted read should not keep unrelated chrome selectors: %+v", snapshot.Selectors)
	}
	if snapshot.InvestigationTrace == nil {
		t.Fatalf("expected tool-driven investigation trace")
	}
	if snapshot.InvestigationQuality == nil || snapshot.InvestigationQuality.OverreadRisk == "high" {
		t.Fatalf("default small-step budget should avoid high overread risk, got %+v", snapshot.InvestigationQuality)
	}
	if !investigationHasTool(snapshot.InvestigationTrace, "repo_index") || !investigationHasTool(snapshot.InvestigationTrace, "grep_text") {
		t.Fatalf("expected repo_index and grep_text tool calls, got %+v", snapshot.InvestigationTrace.ToolCalls)
	}
	if !investigationHasQuestion(snapshot.InvestigationTrace, "question_project_creation") {
		t.Fatalf("expected project creation investigation question, got %+v", snapshot.InvestigationTrace.Questions)
	}
}

func TestCodeReaderClonesGitRepositoryInputForIntentDrivenScan(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git executable is required for repository snapshot test")
	}
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"react":"latest","vite":"latest"}}`)
	writeFixtureFile(t, root, "src/pages/projects/NewProject.tsx", `
		export function NewProjectPage() {
			const route = "/projects/new"
			return <button data-testid="new-project">新建项目</button>
		}
	`)
	gitInitFixtureRepo(t, root)
	project := &model.ProjectContext{
		ID:                 "project_git_repo",
		ProductURL:         "https://cascadeai.cn",
		ProductDescription: "演示新建项目",
		Inputs: &model.ProjectInputBundle{Code: []model.CodeInput{{
			ID:           "code_git",
			Kind:         "git_repository",
			URI:          "file://" + filepath.ToSlash(root),
			RepositoryID: "repo_git",
			ReadOnly:     true,
		}}},
	}
	brief := &model.RequirementBrief{ProjectID: project.ID, Objective: project.ProductDescription, MustShow: []string{"新建项目"}}

	snapshots, err := NewCodeReaderAgent().ReadCode(context.Background(), project, brief)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("expected one snapshot, got %d", len(snapshots))
	}
	snapshot := snapshots[0]
	if snapshot.FileCount == 0 || snapshot.CommitSHA == "" {
		t.Fatalf("expected git snapshot to be cloned and scanned: %+v", snapshot)
	}
	if !routeInsightContains(snapshot.Routes, "/projects/new") {
		t.Fatalf("git repository scan dropped route: %+v", snapshot.Routes)
	}
	if !selectorInsightContains(snapshot.Selectors, "[data-testid='new-project']") {
		t.Fatalf("git repository scan dropped selector: %+v", snapshot.Selectors)
	}
	if snapshot.InvestigationTrace == nil || !strings.Contains(snapshot.InvestigationTrace.Summary, "GitHub 仓库只读快照") {
		t.Fatalf("expected git repository investigation trace, got %+v", snapshot.InvestigationTrace)
	}
}

func TestCodeReaderStopsAfterInvestigationQuestionsAreAnswered(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"react":"latest","vite":"latest"}}`)
	writeFixtureFile(t, root, "src/pages/projects/NewProject.tsx", `
		export function NewProjectPage() {
			const route = "/projects/new"
			const createPath = "/api/projects/create"
			return <button data-testid="new-project">新建项目</button>
		}
	`)
	writeFixtureFile(t, root, "backend/server/ProjectHandler.go", `
		package server
		func registerProjectRoutes() {
			router.POST("/api/projects/create", handleCreateProject)
		}
		func handleCreateProject() {}
	`)
	for i := 0; i < 12; i++ {
		writeFixtureFile(t, root, filepath.ToSlash(filepath.Join("src", "pages", "fallback", fmt.Sprintf("Fallback%d.tsx", i))), `
			export function FallbackPage() {
				return <button data-testid="profile-avatar">avatar</button>
			}
		`)
	}
	project := &model.ProjectContext{
		ID:                 "project_early_stop",
		ProductURL:         "https://cascadeai.cn",
		ProductDescription: "演示新建项目",
		Inputs: &model.ProjectInputBundle{Code: []model.CodeInput{{
			ID:           "code_early_stop",
			Kind:         "repository",
			LocalPath:    root,
			RepositoryID: "repo_early_stop",
		}}},
	}
	brief := &model.RequirementBrief{ProjectID: project.ID, Objective: project.ProductDescription, MustShow: []string{"新建项目"}}

	snapshots, err := NewCodeReaderAgent().ReadCode(context.Background(), project, brief)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := snapshots[0]
	if snapshot.InvestigationTrace == nil {
		t.Fatalf("expected investigation trace")
	}
	question := investigationQuestion(snapshot.InvestigationTrace, "question_project_creation")
	if question == nil || question.Status != "answered" {
		t.Fatalf("expected project creation question to be answered, got %+v", snapshot.InvestigationTrace.Questions)
	}
	if investigationHasTool(snapshot.InvestigationTrace, "focused_fallback") {
		t.Fatalf("answered investigation should not run focused fallback, got %+v", snapshot.InvestigationTrace.ToolCalls)
	}
	if selectorInsightContains(snapshot.Selectors, "[data-testid='profile-avatar']") {
		t.Fatalf("early stop should not read fallback chrome selectors, got %+v", snapshot.Selectors)
	}
}

func TestProjectInvestigationToolSuiteRespectsGlobalToolSearchBudget(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"react":"latest","vite":"latest"}}`)
	writeFixtureFile(t, root, "src/pages/projects/NewProject.tsx", `
		export function NewProjectPage() {
			const route = "/projects/new"
			return <button data-testid="new-project">新建项目</button>
		}
	`)
	for i := 0; i < 60; i++ {
		writeFixtureFile(t, root, filepath.ToSlash(filepath.Join("src", "features", fmt.Sprintf("Filler%02d.tsx", i))), `
			export function Filler() { return <button data-testid="profile-avatar">avatar</button> }
		`)
	}
	budget := model.CodeReadBudget{
		Mode:                   "tool_driven_intent_drilldown",
		RepoIndexFileLimit:     1,
		DrilldownRounds:        4,
		FilesPerRound:          1,
		TotalFileLimit:         8,
		MaxFileBytes:           80 * 1024,
		ToolSearchFileLimit:    12,
		ToolSearchBytesPerFile: 32 * 1024,
		ToolSearchResultLimit:  8,
	}
	candidates := collectCodeCandidatesForTest(t, root, nil, nil)
	result, err := NewProjectInvestigationToolSuite(nil).Investigate(context.Background(), ProjectInvestigationRequest{
		Root:       root,
		Candidates: candidates,
		Budget:     budget,
		Project:    &model.ProjectContext{ID: "project_global_search_budget", ProductDescription: "演示新建项目，俄罗斯方块，构建模式，agent实际构建"},
		Brief:      &model.RequirementBrief{ProjectID: "project_global_search_budget", Objective: "演示新建项目，俄罗斯方块，构建模式，agent实际构建"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Trace.TotalFilesSearched > budget.ToolSearchFileLimit {
		t.Fatalf("tool search should be globally budgeted, searched=%d limit=%d calls=%+v", result.Trace.TotalFilesSearched, budget.ToolSearchFileLimit, result.Trace.ToolCalls)
	}
}

func TestCodeReaderInfersFilesystemRoutesFromSelectedFiles(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"next":"latest","react":"latest"}}`)
	writeFixtureFile(t, root, "src/app/workspace/projects/new/page.tsx", `
		export default function NewProjectPage() {
			return <button data-testid="create-tetris-project">创建俄罗斯方块</button>
		}
	`)
	writeFixtureFile(t, root, "src/app/aigc/.well-known/cascade-exchange/page.tsx", `
		export default function ExchangeDebug() { return <main>debug</main> }
	`)
	project := &model.ProjectContext{
		ID:                 "project_file_routes",
		ProductURL:         "https://cascadeai.cn",
		ProductDescription: "演示新建项目，俄罗斯方块，构建模式",
		Inputs: &model.ProjectInputBundle{Code: []model.CodeInput{{
			ID:           "code_file_routes",
			Kind:         "repository",
			LocalPath:    root,
			RepositoryID: "repo_file_routes",
		}}},
	}
	brief := &model.RequirementBrief{ProjectID: project.ID, Objective: project.ProductDescription, MustShow: []string{"新建项目", "俄罗斯方块"}}
	agent := NewCodeReaderAgent()
	agent.Budget = model.CodeReadBudget{
		Mode:                   "tool_driven_intent_drilldown",
		RepoIndexFileLimit:     1,
		DrilldownRounds:        1,
		FilesPerRound:          2,
		TotalFileLimit:         4,
		MaxFileBytes:           80 * 1024,
		ToolSearchFileLimit:    20,
		ToolSearchBytesPerFile: 32 * 1024,
		ToolSearchResultLimit:  8,
	}

	snapshots, err := agent.ReadCode(context.Background(), project, brief)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := snapshots[0]
	if !routeInsightContains(snapshot.Routes, "/workspace/projects/new") {
		t.Fatalf("expected filesystem route /workspace/projects/new, got %+v", snapshot.Routes)
	}
	if routeInsightContains(snapshot.Routes, "/aigc/.well-known/cascade-exchange") {
		t.Fatalf("control-plane filesystem route should be filtered, got %+v", snapshot.Routes)
	}
	if !selectorInsightContains(snapshot.Selectors, "[data-testid='create-tetris-project']") {
		t.Fatalf("expected selected page selector, got %+v", snapshot.Selectors)
	}
}

func TestCodeReaderExtractsSemanticControlSelectors(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"next":"latest","react":"latest"}}`)
	writeFixtureFile(t, root, "src/app/workspace/projects/new/page.tsx", `
		export default function NewProjectPage() {
			return <form>
				<input aria-label="项目名称" placeholder="项目名称" name="projectName" />
				<button>新建项目</button>
				<button>菜单</button>
				<a>打开项目</a>
			</form>
		}
	`)
	project := &model.ProjectContext{
		ID:                 "project_semantic_controls",
		ProductURL:         "https://cascadeai.cn",
		ProductDescription: "演示新建项目，项目名称俄罗斯方块，构建模式",
		Inputs: &model.ProjectInputBundle{Code: []model.CodeInput{{
			ID:           "code_semantic_controls",
			Kind:         "repository",
			LocalPath:    root,
			RepositoryID: "repo_semantic_controls",
		}}},
	}
	brief := &model.RequirementBrief{ProjectID: project.ID, Objective: project.ProductDescription, MustShow: []string{"新建项目", "俄罗斯方块"}}
	agent := NewCodeReaderAgent()
	agent.Budget = model.CodeReadBudget{
		Mode:                   "tool_driven_intent_drilldown",
		RepoIndexFileLimit:     1,
		DrilldownRounds:        1,
		FilesPerRound:          1,
		TotalFileLimit:         3,
		MaxFileBytes:           80 * 1024,
		ToolSearchFileLimit:    20,
		ToolSearchBytesPerFile: 32 * 1024,
		ToolSearchResultLimit:  8,
	}

	snapshots, err := agent.ReadCode(context.Background(), project, brief)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := snapshots[0]
	for _, selector := range []string{`[aria-label*="项目名称"]`, `[placeholder*="项目名称"]`, `input[name="projectName"]`, `button:has-text("新建项目")`, `a:has-text("打开项目")`} {
		if !selectorInsightContains(snapshot.Selectors, selector) {
			t.Fatalf("expected semantic selector %s, got %+v", selector, snapshot.Selectors)
		}
	}
	if selectorInsightContains(snapshot.Selectors, `button:has-text("菜单")`) {
		t.Fatalf("chrome menu text should not become semantic business selector: %+v", snapshot.Selectors)
	}
	if !routeInsightContains(snapshot.Routes, "/workspace/projects/new") {
		t.Fatalf("semantic control fixture should retain filesystem route evidence, got %+v", snapshot.Routes)
	}
}

func TestFilesystemRoutesForRelCoversCommonFileRouters(t *testing.T) {
	cases := map[string]string{
		"src/app/workspace/projects/[id]/page.tsx": "/workspace/projects/:id",
		"src/app/(marketing)/login/page.tsx":       "/login",
		"src/pages/projects/[id].tsx":              "/projects/:id",
		"src/pages/index.tsx":                      "/",
		"src/routes/builder/+page.svelte":          "/builder",
		"src/routes/projects/[id]/+page.svelte":    "/projects/:id",
	}
	for rel, expected := range cases {
		routes := filesystemRoutesForRel(rel)
		if !stringSliceContains(routes, expected) {
			t.Fatalf("expected %s -> %s, got %+v", rel, expected, routes)
		}
	}
	if routes := filesystemRoutesForRel("src/app/aigc/.well-known/cascade-exchange/page.tsx"); stringSliceContains(routes, "/aigc/.well-known/cascade-exchange") {
		t.Fatalf("control-plane route should be filtered, got %+v", routes)
	}
}

func TestProjectInvestigationToolSuiteTracksQuestionProgress(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"react":"latest","vite":"latest"}}`)
	writeFixtureFile(t, root, "src/views/NewProject.tsx", `
		export function NewProject() {
			const route = "/workspace/projects/new"
			return <form>
				<input aria-label="项目名称" />
				<button data-testid="create-tetris-project">新建项目</button>
			</form>
		}
	`)
	writeFixtureFile(t, root, "backend/server/projects.go", `
		package server
		func createProject() {
			path := "/api/projects/create"
			_ = path
		}
	`)
	candidates := collectCodeCandidatesForTest(t, root, nil, nil)
	result, err := NewProjectInvestigationToolSuite(nil).Investigate(context.Background(), ProjectInvestigationRequest{
		Root:       root,
		Candidates: candidates,
		Budget: model.CodeReadBudget{
			Mode:                   "tool_driven_intent_drilldown",
			RepoIndexFileLimit:     1,
			DrilldownRounds:        3,
			FilesPerRound:          2,
			TotalFileLimit:         6,
			MaxFileBytes:           80 * 1024,
			ToolSearchFileLimit:    80,
			ToolSearchBytesPerFile: 32 * 1024,
			ToolSearchResultLimit:  10,
		},
		Project: &model.ProjectContext{ID: "project_question_trace", ProductDescription: "演示新建项目，项目名称俄罗斯方块"},
		Brief:   &model.RequirementBrief{ProjectID: "project_question_trace", Objective: "演示新建项目，项目名称俄罗斯方块", MustShow: []string{"新建项目", "俄罗斯方块"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	question := investigationQuestion(result.Trace, "question_project_creation")
	if question == nil {
		t.Fatalf("expected project creation investigation question, got %+v", result.Trace.Questions)
	}
	if question.Status != "answered" {
		t.Fatalf("expected project creation question to be answered, got %+v", question)
	}
	if len(question.ToolCallIDs) == 0 {
		t.Fatalf("expected question to reference tool calls, got %+v", question)
	}
	if !strings.Contains(result.Trace.Summary, "调查问题") {
		t.Fatalf("expected trace summary to include question progress, got %q", result.Trace.Summary)
	}
}

func TestProjectInvestigationToolSuiteRecordsSnippetRefsWithoutSourceText(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"react":"latest","vite":"latest"}}`)
	writeFixtureFile(t, root, "src/views/NewProject.tsx", `
		export function NewProject() {
			const password = "raw-password-123"
			const route = "/workspace/projects/new"
			return <button data-testid="create-tetris-project">新建项目：俄罗斯方块</button>
		}
	`)
	candidates := collectCodeCandidatesForTest(t, root, nil, nil)
	result, err := NewProjectInvestigationToolSuite(nil).Investigate(context.Background(), ProjectInvestigationRequest{
		Root:       root,
		Candidates: candidates,
		Budget: model.CodeReadBudget{
			Mode:                   "tool_driven_intent_drilldown",
			RepoIndexFileLimit:     1,
			DrilldownRounds:        2,
			FilesPerRound:          2,
			TotalFileLimit:         4,
			MaxFileBytes:           80 * 1024,
			ToolSearchFileLimit:    40,
			ToolSearchBytesPerFile: 32 * 1024,
			ToolSearchResultLimit:  10,
		},
		Project: &model.ProjectContext{ID: "project_snippet_trace", ProductDescription: "演示新建项目，俄罗斯方块"},
		Brief:   &model.RequirementBrief{ProjectID: "project_snippet_trace", Objective: "演示新建项目，俄罗斯方块"},
	})
	if err != nil {
		t.Fatal(err)
	}
	snippetCall := investigationToolCall(result.Trace, "read_evidence_snippets")
	if snippetCall == nil {
		t.Fatalf("expected read_evidence_snippets tool call, got %+v", result.Trace.ToolCalls)
	}
	if len(snippetCall.SnippetRefs) == 0 {
		t.Fatalf("expected snippet refs, got %+v", snippetCall)
	}
	if snippetCall.SnippetRefs[0].LineStart <= 0 || len(snippetCall.SnippetRefs[0].SignalKinds) == 0 {
		t.Fatalf("snippet ref should expose line window and signal kind, got %+v", snippetCall.SnippetRefs[0])
	}
	traceJSON, _ := json.Marshal(result.Trace)
	for _, forbidden := range []string{"raw-password-123", "const password", "create-tetris-project"} {
		if strings.Contains(string(traceJSON), forbidden) {
			t.Fatalf("investigation trace leaked source text %q:\n%s", forbidden, traceJSON)
		}
	}
	question := investigationQuestion(result.Trace, "question_project_creation")
	if question == nil || !stringSliceContains(question.ToolCallIDs, snippetCall.ID) {
		t.Fatalf("expected question to reference snippet tool call, question=%+v snippet_call=%s", question, snippetCall.ID)
	}
}

func TestCodeReaderUsesGrepToolBeforeStructuredReads(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"react":"latest","vite":"latest"}}`)
	for i := 0; i < 24; i++ {
		writeFixtureFile(t, root, filepath.ToSlash(filepath.Join("src", "components", "ChromeShell"+string(rune('a'+i))+".tsx")), `
			export function ChromeShell() { return <button data-testid="profile-avatar">avatar</button> }
		`)
	}
	writeFixtureFile(t, root, "src/views/Flow.tsx", `
		export function Flow() {
			const route = "/workspace/projects/create"
			return <form>
				<input aria-label="项目名称" />
				<button data-testid="create-tetris-project">新建项目：俄罗斯方块</button>
			</form>
		}
		const createProjectAPI = "/api/projects/create"
	`)

	project := &model.ProjectContext{
		ID:                 "project_grep_drilldown",
		ProductURL:         "https://cascadeai.cn",
		ProductDescription: "演示新建项目，项目名称俄罗斯方块，构建模式",
		Inputs: &model.ProjectInputBundle{Code: []model.CodeInput{{
			ID:           "code_grep_drilldown",
			Kind:         "repository",
			LocalPath:    root,
			RepositoryID: "repo_grep_drilldown",
		}}},
	}
	brief := &model.RequirementBrief{ProjectID: project.ID, Objective: project.ProductDescription, MustShow: []string{"新建项目", "俄罗斯方块", "构建模式"}}
	agent := NewCodeReaderAgent()
	agent.Budget = model.CodeReadBudget{
		Mode:                   "tool_driven_intent_drilldown",
		RepoIndexFileLimit:     1,
		DrilldownRounds:        1,
		FilesPerRound:          1,
		TotalFileLimit:         2,
		MaxFileBytes:           80 * 1024,
		ToolSearchFileLimit:    80,
		ToolSearchBytesPerFile: 32 * 1024,
		ToolSearchResultLimit:  10,
	}

	snapshots, err := agent.ReadCode(context.Background(), project, brief)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := snapshots[0]
	if snapshot.FileCount > 2 {
		t.Fatalf("expected bounded structured reads, got %d", snapshot.FileCount)
	}
	if !routeInsightContains(snapshot.Routes, "/workspace/projects/create") {
		t.Fatalf("grep-selected file should provide route evidence, got %+v", snapshot.Routes)
	}
	if !selectorInsightContains(snapshot.Selectors, "[data-testid='create-tetris-project']") {
		t.Fatalf("grep-selected file should provide business selector, got %+v", snapshot.Selectors)
	}
	if selectorInsightContains(snapshot.Selectors, "[data-testid='profile-avatar']") {
		t.Fatalf("unrelated chrome files should not be structurally read: %+v", snapshot.Selectors)
	}
	trace := snapshot.InvestigationTrace
	if trace == nil {
		t.Fatalf("expected investigation trace")
	}
	grepCall := investigationToolCall(trace, "grep_text")
	if grepCall == nil {
		t.Fatalf("expected grep_text tool call, got %+v", trace.ToolCalls)
	}
	if grepCall.SelectedFileCount != 1 || grepCall.MatchedFileCount == 0 {
		t.Fatalf("expected grep to select the content-relevant file, got %+v", grepCall)
	}
	if trace.TotalFilesSearched <= trace.TotalFilesSelected {
		t.Fatalf("expected search to inspect candidates before bounded structured reads, got searched=%d selected=%d", trace.TotalFilesSearched, trace.TotalFilesSelected)
	}
}

func TestCodeReaderFollowsImportsFromGrepSelectedFiles(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"react":"latest","vite":"latest"}}`)
	writeFixtureFile(t, root, "src/views/Flow.tsx", `
		import { Wizard } from "../components/Wizard"
		export function Flow() {
			const route = "/workspace/projects/new"
			return <Wizard />
		}
	`)
	writeFixtureFile(t, root, "src/components/Wizard.tsx", `
		export function Wizard() {
			const endpoint = "/api/workflows/start"
			return <button data-testid="wizard-next">Continue</button>
		}
	`)
	writeFixtureFile(t, root, "src/components/ChromeShell.tsx", `
		export function ChromeShell() { return <button data-testid="profile-avatar">avatar</button> }
	`)
	project := &model.ProjectContext{
		ID:                 "project_follow_imports",
		ProductURL:         "https://cascadeai.cn",
		ProductDescription: "演示新建项目，俄罗斯方块",
		Inputs: &model.ProjectInputBundle{Code: []model.CodeInput{{
			ID:           "code_follow_imports",
			Kind:         "repository",
			LocalPath:    root,
			RepositoryID: "repo_follow_imports",
		}}},
	}
	brief := &model.RequirementBrief{ProjectID: project.ID, Objective: project.ProductDescription, MustShow: []string{"新建项目", "俄罗斯方块"}}
	agent := NewCodeReaderAgent()
	agent.Budget = model.CodeReadBudget{
		Mode:                   "tool_driven_intent_drilldown",
		RepoIndexFileLimit:     1,
		DrilldownRounds:        1,
		FilesPerRound:          1,
		TotalFileLimit:         3,
		MaxFileBytes:           80 * 1024,
		ToolSearchFileLimit:    40,
		ToolSearchBytesPerFile: 32 * 1024,
		ToolSearchResultLimit:  10,
	}

	snapshots, err := agent.ReadCode(context.Background(), project, brief)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := snapshots[0]
	if snapshot.FileCount > 3 {
		t.Fatalf("expected import following to remain budgeted, read %d files", snapshot.FileCount)
	}
	if !investigationHasTool(snapshot.InvestigationTrace, "follow_imports") {
		t.Fatalf("expected follow_imports tool call, got %+v", snapshot.InvestigationTrace.ToolCalls)
	}
	if !selectorInsightContains(snapshot.Selectors, "[data-testid='wizard-next']") {
		t.Fatalf("expected selector from imported component, got %+v", snapshot.Selectors)
	}
	if !apiInsightContains(snapshot.APIEndpoints, "/api/workflows/start") {
		t.Fatalf("expected API evidence from imported component, got %+v", snapshot.APIEndpoints)
	}
	if selectorInsightContains(snapshot.Selectors, "[data-testid='profile-avatar']") {
		t.Fatalf("unrelated chrome component should not be pulled by import following: %+v", snapshot.Selectors)
	}
}

func TestCodeReaderFollowsConfiguredAliasImports(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"react":"latest","vite":"latest"}}`)
	writeFixtureFile(t, root, "tsconfig.json", `{
		"compilerOptions": {
			"baseUrl": ".",
			"paths": {
				"@features/*": ["src/features/*"],
				"@unsafe/*": ["node_modules/*"]
			}
		}
	}`)
	writeFixtureFile(t, root, "src/views/Flow.tsx", `
		import { Wizard } from "@features/Wizard"
		import React from "react"
		export function Flow() {
			const route = "/workspace/projects/new"
			return <Wizard />
		}
	`)
	writeFixtureFile(t, root, "src/features/Wizard.tsx", `
		export function Wizard() {
			const endpoint = "/api/workflows/start"
			return <button data-testid="wizard-next">Continue</button>
		}
	`)
	writeFixtureFile(t, root, "src/features/ChromeShell.tsx", `
		export function ChromeShell() { return <button data-testid="profile-avatar">avatar</button> }
	`)
	project := &model.ProjectContext{
		ID:                 "project_alias_imports",
		ProductURL:         "https://cascadeai.cn",
		ProductDescription: "演示新建项目，俄罗斯方块",
		Inputs: &model.ProjectInputBundle{Code: []model.CodeInput{{
			ID:           "code_alias_imports",
			Kind:         "repository",
			LocalPath:    root,
			RepositoryID: "repo_alias_imports",
		}}},
	}
	brief := &model.RequirementBrief{ProjectID: project.ID, Objective: project.ProductDescription, MustShow: []string{"新建项目", "俄罗斯方块"}}
	agent := NewCodeReaderAgent()
	agent.Budget = model.CodeReadBudget{
		Mode:                   "tool_driven_intent_drilldown",
		RepoIndexFileLimit:     1,
		DrilldownRounds:        1,
		FilesPerRound:          1,
		TotalFileLimit:         4,
		MaxFileBytes:           80 * 1024,
		ToolSearchFileLimit:    40,
		ToolSearchBytesPerFile: 32 * 1024,
		ToolSearchResultLimit:  10,
	}

	snapshots, err := agent.ReadCode(context.Background(), project, brief)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := snapshots[0]
	if !investigationHasTool(snapshot.InvestigationTrace, "follow_imports") {
		t.Fatalf("expected follow_imports tool call, got %+v", snapshot.InvestigationTrace.ToolCalls)
	}
	if !selectorInsightContains(snapshot.Selectors, "[data-testid='wizard-next']") {
		t.Fatalf("expected selector from alias imported component, got %+v", snapshot.Selectors)
	}
	if !apiInsightContains(snapshot.APIEndpoints, "/api/workflows/start") {
		t.Fatalf("expected API evidence from alias imported component, got %+v", snapshot.APIEndpoints)
	}
	if selectorInsightContains(snapshot.Selectors, "[data-testid='profile-avatar']") {
		t.Fatalf("unrelated alias-adjacent component should not be selected: %+v", snapshot.Selectors)
	}
	followCall := investigationToolCall(snapshot.InvestigationTrace, "follow_imports")
	if followCall.Metadata["alias_rule_count"] != 1 {
		t.Fatalf("expected only safe tsconfig alias rule to be used, got %+v", followCall.Metadata)
	}
	if hashes, ok := followCall.Metadata["alias_rule_hashes"].([]string); !ok || len(hashes) == 0 {
		t.Fatalf("expected alias rules to be represented as hashes, got %+v", followCall.Metadata)
	}
	metadataJSON, _ := json.Marshal(followCall.Metadata)
	if strings.Contains(string(metadataJSON), "@features") || strings.Contains(string(metadataJSON), "src/features") {
		t.Fatalf("follow_imports metadata should hash alias rules, got %s", metadataJSON)
	}
}

func TestCodeReaderFindsReferencesBackToParentRoute(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"react":"latest","vite":"latest"}}`)
	writeFixtureFile(t, root, "src/components/Wizard.tsx", `
		export function Wizard() {
			const endpoint = "/api/workflows/start"
			return <button data-testid="wizard-next">Continue</button>
		}
	`)
	writeFixtureFile(t, root, "src/views/Flow.tsx", `
		import { Wizard } from "../components/Wizard"
		export function Flow() {
			const route = "/workspace/projects/new"
			return <Wizard />
		}
	`)
	writeFixtureFile(t, root, "src/views/Unrelated.tsx", `
		export function Unrelated() { return <button data-testid="profile-avatar">avatar</button> }
	`)
	project := &model.ProjectContext{
		ID:                 "project_find_references",
		ProductURL:         "https://cascadeai.cn",
		ProductDescription: "演示 wizard 新建流程",
		Inputs: &model.ProjectInputBundle{Code: []model.CodeInput{{
			ID:           "code_find_references",
			Kind:         "repository",
			LocalPath:    root,
			RepositoryID: "repo_find_references",
		}}},
	}
	brief := &model.RequirementBrief{ProjectID: project.ID, Objective: project.ProductDescription, MustShow: []string{"wizard"}}
	agent := NewCodeReaderAgent()
	agent.Budget = model.CodeReadBudget{
		Mode:                   "tool_driven_intent_drilldown",
		RepoIndexFileLimit:     1,
		DrilldownRounds:        1,
		FilesPerRound:          1,
		TotalFileLimit:         3,
		MaxFileBytes:           80 * 1024,
		ToolSearchFileLimit:    40,
		ToolSearchBytesPerFile: 32 * 1024,
		ToolSearchResultLimit:  10,
	}

	snapshots, err := agent.ReadCode(context.Background(), project, brief)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := snapshots[0]
	if !investigationHasTool(snapshot.InvestigationTrace, "find_references") {
		t.Fatalf("expected find_references tool call, got %+v", snapshot.InvestigationTrace.ToolCalls)
	}
	if !routeInsightContains(snapshot.Routes, "/workspace/projects/new") {
		t.Fatalf("expected parent route found through references, got %+v", snapshot.Routes)
	}
	if !selectorInsightContains(snapshot.Selectors, "[data-testid='wizard-next']") {
		t.Fatalf("expected child selector retained, got %+v", snapshot.Selectors)
	}
	if selectorInsightContains(snapshot.Selectors, "[data-testid='profile-avatar']") {
		t.Fatalf("unrelated view should not be selected by find_references: %+v", snapshot.Selectors)
	}
}

func TestCodeReaderFindsBackendAPIHandlersFromFrontendCalls(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"react":"latest","vite":"latest"}}`)
	writeFixtureFile(t, root, "src/features/CreateProject.tsx", `
		export function CreateProject() {
			async function submit() {
				await fetch("/api/workflows/start", { method: "POST", body: JSON.stringify({ name: "俄罗斯方块", mode: "build" }) })
			}
			return <button data-testid="create-tetris-project" onClick={submit}>创建俄罗斯方块</button>
		}
	`)
	writeFixtureFile(t, root, "src/pages/Workspace.tsx", `
		import { CreateProject } from "../features/CreateProject"
		export function Workspace() {
			const route = "/workspace/projects/new"
			return <CreateProject />
		}
	`)
	writeFixtureFile(t, root, "backend/internal/service/handler.go", `
		package service
		type StartWorkflowRequest struct { Name string; Mode string }
		func RegisterWorkflowRoutes(router Router) {
			router.POST("/api/workflows/start", handleStartWorkflow)
		}
	`)
	writeFixtureFile(t, root, "backend/internal/debug/exchange.go", `
		package debug
		func RegisterDebug(router Router) { router.POST("/aigc/v1/app-installations/register", noop) }
	`)
	project := &model.ProjectContext{
		ID:                 "project_find_api_handlers",
		ProductURL:         "https://cascadeai.cn",
		ProductDescription: "演示新建项目，项目名称俄罗斯方块，构建模式启动 agent",
		Inputs: &model.ProjectInputBundle{Code: []model.CodeInput{{
			ID:           "code_find_api_handlers",
			Kind:         "repository",
			LocalPath:    root,
			RepositoryID: "repo_find_api_handlers",
		}}},
	}
	brief := &model.RequirementBrief{ProjectID: project.ID, Objective: project.ProductDescription, MustShow: []string{"新建项目", "俄罗斯方块", "构建模式"}}
	agent := NewCodeReaderAgent()
	agent.Budget = model.CodeReadBudget{
		Mode:                   "tool_driven_intent_drilldown",
		RepoIndexFileLimit:     1,
		DrilldownRounds:        1,
		FilesPerRound:          1,
		TotalFileLimit:         5,
		MaxFileBytes:           80 * 1024,
		ToolSearchFileLimit:    40,
		ToolSearchBytesPerFile: 32 * 1024,
		ToolSearchResultLimit:  10,
	}

	snapshots, err := agent.ReadCode(context.Background(), project, brief)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := snapshots[0]
	if !investigationHasTool(snapshot.InvestigationTrace, "find_api_handlers") {
		t.Fatalf("expected find_api_handlers tool call, got %+v", snapshot.InvestigationTrace.ToolCalls)
	}
	if !apiInsightContains(snapshot.APIEndpoints, "/api/workflows/start") {
		t.Fatalf("expected frontend/backend API evidence, got %+v", snapshot.APIEndpoints)
	}
	if !dataModelInsightContains(snapshot.DataModels, "StartWorkflowRequest") {
		t.Fatalf("expected backend request model from handler file, got %+v", snapshot.DataModels)
	}
	apiCall := investigationToolCall(snapshot.InvestigationTrace, "find_api_handlers")
	metadataJSON, _ := json.Marshal(apiCall.Metadata)
	if strings.Contains(string(metadataJSON), "/api/workflows/start") {
		t.Fatalf("find_api_handlers metadata should hash API paths, got %s", metadataJSON)
	}
}

func TestCodeReaderLLMPlannerCanChooseNextGrepQuery(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"react":"latest","vite":"latest"}}`)
	for i := 0; i < 16; i++ {
		writeFixtureFile(t, root, filepath.ToSlash(filepath.Join("src", "components", "GenericPanel"+string(rune('a'+i))+".tsx")), `
			export function GenericPanel() { return <button data-testid="profile-avatar">avatar</button> }
		`)
	}
	writeFixtureFile(t, root, "src/views/Flow.tsx", `
		export function Flow() {
			const route = "/workspace/orbital-lab"
			return <button data-testid="launch-orbital-lab">Launch orbital lab</button>
		}
	`)

	project := &model.ProjectContext{
		ID:                 "project_llm_planner",
		ProductURL:         "https://cascadeai.cn",
		ProductDescription: "演示研发工作流",
		Inputs: &model.ProjectInputBundle{Code: []model.CodeInput{{
			ID:           "code_llm_planner",
			Kind:         "repository",
			LocalPath:    root,
			RepositoryID: "repo_llm_planner",
		}}},
	}
	brief := &model.RequirementBrief{ProjectID: project.ID, Objective: project.ProductDescription, MustShow: []string{"研发工作流"}}
	agent := NewCodeReaderAgentWithLLM(codeInvestigationPlannerLLM{})
	agent.Budget = model.CodeReadBudget{
		Mode:                   "tool_driven_intent_drilldown",
		RepoIndexFileLimit:     1,
		DrilldownRounds:        1,
		FilesPerRound:          1,
		TotalFileLimit:         2,
		MaxFileBytes:           80 * 1024,
		ToolSearchFileLimit:    80,
		ToolSearchBytesPerFile: 32 * 1024,
		ToolSearchResultLimit:  10,
	}

	snapshots, err := agent.ReadCode(context.Background(), project, brief)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := snapshots[0]
	if !routeInsightContains(snapshot.Routes, "/workspace/orbital-lab") {
		t.Fatalf("LLM-planned grep query should select orbital-lab route evidence, got %+v", snapshot.Routes)
	}
	if !selectorInsightContains(snapshot.Selectors, "[data-testid='launch-orbital-lab']") {
		t.Fatalf("LLM-planned grep query should select orbital-lab selector evidence, got %+v", snapshot.Selectors)
	}
	plannerCall := investigationToolCall(snapshot.InvestigationTrace, "plan_investigation")
	if plannerCall == nil {
		t.Fatalf("expected plan_investigation call, got %+v", snapshot.InvestigationTrace)
	}
	if plannerCall.FallbackReason != "" {
		t.Fatalf("expected model planner, got fallback %+v", plannerCall)
	}
	if !strings.Contains(plannerCall.Query, "orbital-lab") {
		t.Fatalf("expected planner query to include model-selected term, got %+v", plannerCall)
	}
}

func TestCodeReaderPrioritizesBuildCompletionAndPlayableResultQuestions(t *testing.T) {
	project := &model.ProjectContext{
		ID:                 "project_completion_evidence",
		ProductDescription: "等待 Agent 全部步骤完成，再打开俄罗斯方块预览并用方向键试玩，确认棋盘和得分变化。",
	}
	budget := model.CodeReadBudget{DrilldownRounds: 2}
	questions := buildCodeInvestigationQuestions(project, nil, budget)
	if len(questions) < 3 {
		t.Fatalf("expected the result-anchor and two requirement-critical questions first, got %+v", questions)
	}
	if questions[0].ID != "question_result_anchors" || !stringSliceContains(questions[0].QueryTerms, "build-result-card") || !stringSliceContains(questions[0].QueryTerms, "preview-iframe") {
		t.Fatalf("exact result anchors were not prioritized into the first bounded search: %+v", questions)
	}
	if questions[1].ID != "question_build_completion" || !stringSliceContains(questions[1].QueryTerms, "build-result-card") {
		t.Fatalf("build completion evidence was not prioritized: %+v", questions)
	}
	if questions[2].ID != "question_playable_result" || !stringSliceContains(questions[2].QueryTerms, "preview-iframe") {
		t.Fatalf("playable preview evidence was not prioritized: %+v", questions)
	}

	fallback := buildCodeInvestigationQueries(project, nil, budget, questions)
	planned := []codeInvestigationQuery{{questionID: "question_ad_hoc", terms: []string{"generic"}}}
	queries := append(requirementCriticalInvestigationQueries(fallback), planned...)
	if len(queries) < 2 || queries[0].questionID != "question_result_anchors" || queries[1].questionID != "question_build_completion" || !stringSliceContains(queries[0].terms, "preview-iframe") {
		t.Fatalf("model plan could starve requirement-critical queries: %+v", queries)
	}
}

func TestCodeSearchFindsEveryExactResultAnchorWithinBoundedScan(t *testing.T) {
	root := t.TempDir()
	candidates := make([]codeCandidateFile, 0, 132)
	for i := 0; i < 130; i++ {
		rel := fmt.Sprintf("src/components/Generic%03d.tsx", i)
		writeFixtureFile(t, root, rel, `export const Generic = () => <div>generic component</div>`)
		path := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		candidates = append(candidates, codeCandidateFile{path: path, rel: rel, name: filepath.Base(path), size: info.Size()})
	}
	for rel, content := range map[string]string{
		"src/components/ide/chat/plan-components.tsx": `export const Done = () => <div data-testid="build-result-card" />`,
		"src/components/preview/PreviewPanel.tsx":     `export const Preview = () => <iframe data-testid="preview-iframe" />`,
	} {
		writeFixtureFile(t, root, rel, content)
		path := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		candidates = append(candidates, codeCandidateFile{path: path, rel: rel, name: filepath.Base(path), size: info.Size()})
	}

	query := codeInvestigationQuery{
		questionID: "question_result_anchors",
		terms:      []string{"build-result-card", "preview-iframe"},
	}
	result, err := searchCodeCandidatesForQuery(context.Background(), candidates, map[string]bool{}, query, model.CodeReadBudget{
		ToolSearchFileLimit:    2,
		ToolSearchBytesPerFile: 32 * 1024,
		ToolSearchResultLimit:  2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.searched != 2 || len(result.matches) != 2 {
		t.Fatalf("expected both exact anchors inside the two-file scan, got searched=%d matches=%+v", result.searched, result.matches)
	}
	found := map[string]bool{}
	for _, match := range result.matches {
		for _, term := range match.terms {
			found[term] = true
		}
	}
	if !found["build-result-card"] || !found["preview-iframe"] {
		t.Fatalf("bounded result truncation dropped an exact anchor: %+v", result.matches)
	}
}

func TestProjectInvestigationToolSuiteIsReusableWithoutCodeReader(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{
		"scripts": {
			"dev": "CASCADE_TOKEN=super-secret vite",
			"build": "vite build"
		},
		"dependencies": {"react":"latest","vite":"latest"}
	}`)
	writeFixtureFile(t, root, "src/views/Flow.tsx", `
		export function Flow() {
			const route = "/workspace/orbital-lab"
			return <button data-testid="launch-orbital-lab">Launch orbital lab</button>
		}
	`)
	candidates := collectCodeCandidatesForTest(t, root, nil, nil)
	result, err := NewProjectInvestigationToolSuite(codeInvestigationPlannerLLM{}).Investigate(context.Background(), ProjectInvestigationRequest{
		Root:       root,
		Candidates: candidates,
		Budget: model.CodeReadBudget{
			Mode:                   "tool_driven_intent_drilldown",
			RepoIndexFileLimit:     1,
			DrilldownRounds:        1,
			FilesPerRound:          1,
			TotalFileLimit:         2,
			MaxFileBytes:           80 * 1024,
			ToolSearchFileLimit:    80,
			ToolSearchBytesPerFile: 32 * 1024,
			ToolSearchResultLimit:  10,
		},
		Project: &model.ProjectContext{ID: "project_reusable_tools", ProductDescription: "演示研发工作流"},
		Brief:   &model.RequirementBrief{ProjectID: "project_reusable_tools", Objective: "演示研发工作流"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SelectedCandidates) != 2 {
		t.Fatalf("expected manifest plus model-planned target file, got %+v", result.SelectedCandidates)
	}
	if result.Trace == nil || !investigationHasTool(result.Trace, "plan_investigation") || !investigationHasTool(result.Trace, "grep_text") {
		t.Fatalf("expected reusable tool trace with planner and grep calls, got %+v", result.Trace)
	}
	shellCall := investigationToolCall(result.Trace, "shell_metadata")
	if shellCall == nil {
		t.Fatalf("expected shell_metadata tool call, got %+v", result.Trace.ToolCalls)
	}
	metadataJSON, _ := json.Marshal(shellCall.Metadata)
	if !strings.Contains(string(metadataJSON), "dev") || !strings.Contains(string(metadataJSON), "build") || !strings.Contains(string(metadataJSON), "react") {
		t.Fatalf("expected metadata to expose script/dependency names, got %s", string(metadataJSON))
	}
	if strings.Contains(string(metadataJSON), "super-secret") || strings.Contains(string(metadataJSON), "CASCADE_TOKEN") || strings.Contains(shellCall.OutputSummary, "super-secret") {
		t.Fatalf("shell metadata leaked script command values: %s / %s", string(metadataJSON), shellCall.OutputSummary)
	}
	if !candidateRelContains(result.SelectedCandidates, "Flow.tsx") {
		t.Fatalf("expected suite to select Flow.tsx, got %+v", result.SelectedCandidates)
	}
}

func TestProjectInvestigationToolSuiteRunsEvidenceReviewAndAdaptiveNextQuery(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"react":"latest","vite":"latest"}}`)
	writeFixtureFile(t, root, "src/views/Flow.tsx", `
		export function Flow() {
			const route = "/workspace/orbital-lab"
			return <button data-testid="launch-orbital-lab">Launch orbital lab</button>
		}
	`)
	writeFixtureFile(t, root, "backend/server/OrbitalAPI.go", `
		package server
		func handleOrbitalLaunch() {
			path := "/api/orbital-lab/launch"
			_ = path
		}
	`)
	candidates := collectCodeCandidatesForTest(t, root, nil, nil)
	result, err := NewProjectInvestigationToolSuite(uiOnlyInvestigationPlannerLLM{}).Investigate(context.Background(), ProjectInvestigationRequest{
		Root:       root,
		Candidates: candidates,
		Budget: model.CodeReadBudget{
			Mode:                   "tool_driven_intent_drilldown",
			RepoIndexFileLimit:     1,
			DrilldownRounds:        2,
			FilesPerRound:          1,
			TotalFileLimit:         4,
			MaxFileBytes:           80 * 1024,
			ToolSearchFileLimit:    80,
			ToolSearchBytesPerFile: 32 * 1024,
			ToolSearchResultLimit:  10,
		},
		Project: &model.ProjectContext{ID: "project_multi_round_tools", ProductDescription: "演示 orbital lab"},
		Brief:   &model.RequirementBrief{ProjectID: "project_multi_round_tools", Objective: "演示 orbital lab"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if grepToolCallCount(result.Trace) < 2 {
		t.Fatalf("expected adaptive second grep round, got %+v", result.Trace.ToolCalls)
	}
	if !investigationHasTool(result.Trace, "evidence_review") {
		t.Fatalf("expected evidence_review tool call, got %+v", result.Trace.ToolCalls)
	}
	if !candidateRelContains(result.SelectedCandidates, "OrbitalAPI.go") {
		t.Fatalf("expected adaptive API query to select backend API file, got %+v", result.SelectedCandidates)
	}
}

func TestProjectInvestigationToolSuiteDoesNotRunAPIHandlerSearchForPureUIRound(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"react":"latest","vite":"latest"}}`)
	writeFixtureFile(t, root, "src/views/Flow.tsx", `
		export function Flow() {
			const route = "/workspace/orbital-lab"
			return <button data-testid="launch-orbital-lab">Launch orbital lab</button>
		}
	`)
	writeFixtureFile(t, root, "backend/server/OrbitalAPI.go", `
		package server
		func handleOrbitalLaunch() {
			path := "/api/orbital-lab/launch"
			_ = path
		}
	`)
	candidates := collectCodeCandidatesForTest(t, root, nil, nil)
	result, err := NewProjectInvestigationToolSuite(uiOnlyInvestigationPlannerLLM{}).Investigate(context.Background(), ProjectInvestigationRequest{
		Root:       root,
		Candidates: candidates,
		Budget: model.CodeReadBudget{
			Mode:                   "tool_driven_intent_drilldown",
			RepoIndexFileLimit:     1,
			DrilldownRounds:        1,
			FilesPerRound:          1,
			TotalFileLimit:         2,
			MaxFileBytes:           80 * 1024,
			ToolSearchFileLimit:    80,
			ToolSearchBytesPerFile: 32 * 1024,
			ToolSearchResultLimit:  10,
		},
		Project: &model.ProjectContext{ID: "project_ui_tool_policy", ProductDescription: "演示 orbital lab"},
		Brief:   &model.RequirementBrief{ProjectID: "project_ui_tool_policy", Objective: "演示 orbital lab"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if investigationToolCallCount(result.Trace, "find_api_handlers") != 0 {
		t.Fatalf("pure UI grep round should not mechanically run API handler search, got %+v", result.Trace.ToolCalls)
	}
	question := investigationQuestion(result.Trace, "question_product_entry")
	if question == nil || len(question.NextActions) == 0 || question.NextActions[0].Tool != "find_api_handlers" {
		t.Fatalf("missing API evidence should be represented as next action, got %+v", result.Trace.Questions)
	}
}

func TestProjectInvestigationToolSuitePlansNextActionFromObservationLLM(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"react":"latest","vite":"latest"}}`)
	writeFixtureFile(t, root, "src/views/Flow.tsx", `
		export function Flow() {
			const route = "/workspace/orbital-lab"
			return <button data-testid="launch-orbital-lab">Launch orbital lab</button>
		}
	`)
	writeFixtureFile(t, root, "backend/server/OrbitalAPI.go", `
		package server
		func handleOrbitalLaunch() {
			// backend handler evidence deliberately has no frontend selector.
		}
	`)
	candidates := collectCodeCandidatesForTest(t, root, nil, nil)
	result, err := NewProjectInvestigationToolSuite(observationNextActionPlannerLLM{}).Investigate(context.Background(), ProjectInvestigationRequest{
		Root:       root,
		Candidates: candidates,
		Budget: model.CodeReadBudget{
			Mode:                   "tool_driven_intent_drilldown",
			RepoIndexFileLimit:     1,
			DrilldownRounds:        1,
			FilesPerRound:          1,
			TotalFileLimit:         4,
			MaxFileBytes:           80 * 1024,
			ToolSearchFileLimit:    80,
			ToolSearchBytesPerFile: 32 * 1024,
			ToolSearchResultLimit:  10,
		},
		Project: &model.ProjectContext{ID: "project_observation_next_action", ProductDescription: "演示 orbital lab"},
		Brief:   &model.RequirementBrief{ProjectID: "project_observation_next_action", Objective: "演示 orbital lab"},
	})
	if err != nil {
		t.Fatal(err)
	}
	plannerCall := investigationToolCall(result.Trace, "plan_next_actions")
	if plannerCall == nil || plannerCall.FallbackReason != "" {
		t.Fatalf("expected observation LLM to plan next action, got %+v", result.Trace.ToolCalls)
	}
	nextActionGrep := investigationToolCallWithMetadata(result.Trace, "grep_text", "planned_from", "next_actions")
	if nextActionGrep == nil {
		t.Fatalf("expected observation-planned next action to schedule grep, got %+v", result.Trace.ToolCalls)
	}
	if !candidateRelContains(result.SelectedCandidates, "OrbitalAPI.go") {
		t.Fatalf("expected observation-planned query to select backend handler file, got %+v", result.SelectedCandidates)
	}
	question := investigationQuestion(result.Trace, "question_product_entry")
	if question == nil || len(question.NextActions) == 0 || !stringSliceContains(question.NextActions[0].QueryTerms, "handleOrbitalLaunch") {
		t.Fatalf("expected question next_actions to use observation-planned terms, got %+v", result.Trace.Questions)
	}
}

func TestProjectInvestigationPlannerReceivesToolCardsInPrompts(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"react":"latest","vite":"latest"}}`)
	writeFixtureFile(t, root, "src/views/Flow.tsx", `
		export function Flow() {
			return <button data-testid="launch-tool-card-lab">Launch tool card lab</button>
		}
	`)
	planner := &toolInventoryPlannerLLM{}
	candidates := collectCodeCandidatesForTest(t, root, nil, nil)
	_, err := NewProjectInvestigationToolSuite(planner).Investigate(context.Background(), ProjectInvestigationRequest{
		Root:       root,
		Candidates: candidates,
		Budget: model.CodeReadBudget{
			Mode:                   "tool_driven_intent_drilldown",
			RepoIndexFileLimit:     1,
			DrilldownRounds:        1,
			FilesPerRound:          1,
			TotalFileLimit:         3,
			MaxFileBytes:           80 * 1024,
			ToolSearchFileLimit:    50,
			ToolSearchBytesPerFile: 32 * 1024,
			ToolSearchResultLimit:  10,
		},
		Project: &model.ProjectContext{ID: "project_tool_cards", ProductDescription: "演示 tool card lab"},
		Brief:   &model.RequirementBrief{ProjectID: "project_tool_cards", Objective: "演示 tool card lab"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(planner.initialRequest, `"tool_cards"`) {
		t.Fatalf("expected initial planner payload to include tool cards, got %s", planner.initialRequest)
	}
	if !strings.Contains(planner.initialRequest, `"inspect_file_outline"`) || !strings.Contains(planner.initialRequest, `"list_related_files"`) {
		t.Fatalf("expected initial planner payload to describe follow-up tools, got %s", planner.initialRequest)
	}
	if !strings.Contains(planner.initialRequest, `"shell_run"`) || !strings.Contains(planner.initialRequest, `"allowed_initial_tools"`) {
		t.Fatalf("expected initial planner payload to expose shell_run as an allowed initial tool, got %s", planner.initialRequest)
	}
	if !strings.Contains(planner.nextActionRequest, `"tool_cards"`) {
		t.Fatalf("expected next-action planner payload to include tool cards, got %s", planner.nextActionRequest)
	}
	if !strings.Contains(planner.nextActionRequest, `"read_window"`) || !strings.Contains(planner.nextActionRequest, `"find_api_handlers"`) {
		t.Fatalf("expected next-action planner payload to describe read_window and api handler tools, got %s", planner.nextActionRequest)
	}
	if !strings.Contains(planner.nextActionRequest, `"shell_run"`) || !strings.Contains(planner.nextActionRequest, "rg_search_summary") {
		t.Fatalf("expected next-action planner payload to describe read-only shell_run command kinds, got %s", planner.nextActionRequest)
	}
}

func TestProjectInvestigationPlannerCanRequestReadOnlyShellRun(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"react":"latest","vite":"latest"}}`)
	writeFixtureFile(t, root, "src/features/shell-lab/ShellLab.tsx", `
		export function ShellLab() {
			const route = "/workspace/shell-lab"
			return <button data-testid="launch-shell-lab">Launch shell lab</button>
		}
	`)
	writeFixtureFile(t, root, "src/services/BuildClient.ts", `
		export function startBuild() {
			return fetch("/api/build/start")
		}
	`)
	planner := shellRunPlannerLLM{}
	candidates := collectCodeCandidatesForTest(t, root, nil, nil)
	result, err := NewProjectInvestigationToolSuite(planner).Investigate(context.Background(), ProjectInvestigationRequest{
		Root:       root,
		Candidates: candidates,
		Budget: model.CodeReadBudget{
			Mode:                   "tool_driven_intent_drilldown",
			RepoIndexFileLimit:     1,
			DrilldownRounds:        1,
			FilesPerRound:          1,
			TotalFileLimit:         4,
			MaxFileBytes:           80 * 1024,
			ToolSearchFileLimit:    80,
			ToolSearchBytesPerFile: 32 * 1024,
			ToolSearchResultLimit:  10,
		},
		Project: &model.ProjectContext{ID: "project_shell_run", ProductDescription: "演示 shell lab 启动"},
		Brief:   &model.RequirementBrief{ProjectID: "project_shell_run", Objective: "演示 shell lab 启动"},
	})
	if err != nil {
		t.Fatal(err)
	}
	shellCall := investigationToolCall(result.Trace, "shell_run")
	if shellCall == nil {
		t.Fatalf("expected planner-requested shell_run call, got %+v", result.Trace.ToolCalls)
	}
	if shellCall.SelectedFileCount == 0 {
		t.Fatalf("shell_run should select a relevant API client candidate, got %+v", shellCall)
	}
	if fmt.Sprint(shellCall.Metadata["command_kind"]) != "rg_files" {
		t.Fatalf("expected shell_run command_kind rg_files, got %+v", shellCall.Metadata)
	}
	if shellCall.SelectionReason == "" || shellCall.ReadPolicy != "allowlisted_read_only_git_rg" || shellCall.SourceTextPolicy != "no_source_text_persisted" {
		t.Fatalf("shell_run trace should expose selection/read/source policies, got %+v", shellCall)
	}
	if unexpectedGrep := investigationToolCallWithMetadataValues(result.Trace, "grep_text", map[string]string{"planned_from": "next_actions", "suggested_tool": "shell_run"}); unexpectedGrep != nil {
		t.Fatalf("shell_run next action should not first run grep_text, got %+v", unexpectedGrep)
	}
	if !candidateRelContains(result.SelectedCandidates, "BuildClient.ts") {
		t.Fatalf("expected shell_run planned query to select API client, got %+v", result.SelectedCandidates)
	}
	question := investigationQuestion(result.Trace, "question_ad_hoc")
	if question == nil || !stringSliceContains(question.ToolCallIDs, shellCall.ID) {
		t.Fatalf("expected ad hoc question to reference shell_run tool call, got %+v", result.Trace.Questions)
	}
	traceJSON, err := json.Marshal(result.Trace)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"src/services/BuildClient.ts", "/api/build/start", "fetch(", "rg --files", "git ls-files"} {
		if strings.Contains(string(traceJSON), forbidden) {
			t.Fatalf("persistent shell_run trace leaked forbidden detail %q: %s", forbidden, string(traceJSON))
		}
	}
}

func TestProjectInvestigationInitialPlannerCanStartWithReadOnlyShellRun(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"react":"latest","vite":"latest"}}`)
	writeFixtureFile(t, root, "src/services/BuildClient.ts", `
		export function startBuild() {
			return fetch("/api/build/start")
		}
	`)
	planner := initialShellRunPlannerLLM{}
	candidates := collectCodeCandidatesForTest(t, root, nil, nil)
	result, err := NewProjectInvestigationToolSuite(planner).Investigate(context.Background(), ProjectInvestigationRequest{
		Root:       root,
		Candidates: candidates,
		Budget: model.CodeReadBudget{
			Mode:                   "tool_driven_intent_drilldown",
			RepoIndexFileLimit:     1,
			DrilldownRounds:        1,
			FilesPerRound:          1,
			TotalFileLimit:         3,
			MaxFileBytes:           80 * 1024,
			ToolSearchFileLimit:    80,
			ToolSearchBytesPerFile: 32 * 1024,
			ToolSearchResultLimit:  10,
		},
		Project: &model.ProjectContext{ID: "project_initial_shell_run", ProductDescription: "演示 BuildClient 构建启动"},
		Brief:   &model.RequirementBrief{ProjectID: "project_initial_shell_run", Objective: "演示 BuildClient 构建启动"},
	})
	if err != nil {
		t.Fatal(err)
	}
	shellCall := investigationToolCall(result.Trace, "shell_run")
	if shellCall == nil {
		t.Fatalf("expected initial planner shell_run call, got %+v", result.Trace.ToolCalls)
	}
	if grepCall := investigationToolCall(result.Trace, "grep_text"); grepCall != nil {
		t.Fatalf("initial shell_run plan should not first run grep_text, got %+v", grepCall)
	}
	if fmt.Sprint(shellCall.Metadata["planned_from"]) != "initial_plan" || fmt.Sprint(shellCall.Metadata["command_kind"]) != "rg_files" {
		t.Fatalf("expected initial shell_run metadata, got %+v", shellCall.Metadata)
	}
	if shellCall.SelectionReason == "" || shellCall.ReadPolicy != "allowlisted_read_only_git_rg" || shellCall.SourceTextPolicy != "no_source_text_persisted" {
		t.Fatalf("initial shell_run trace should expose selection/read/source policies, got %+v", shellCall)
	}
	if !candidateRelContains(result.SelectedCandidates, "BuildClient.ts") {
		t.Fatalf("expected initial shell_run to select BuildClient, got %+v", result.SelectedCandidates)
	}
	traceJSON, err := json.Marshal(result.Trace)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"src/services/BuildClient.ts", "/api/build/start", "fetch(", "rg --files", "git ls-files"} {
		if strings.Contains(string(traceJSON), forbidden) {
			t.Fatalf("initial shell_run trace leaked forbidden detail %q: %s", forbidden, string(traceJSON))
		}
	}
}

func TestProjectInvestigationPlannerReceivesRedactedTransientSnippetObservation(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"react":"latest","vite":"latest"}}`)
	writeFixtureFile(t, root, "src/views/Flow.tsx", `
		export function Flow() {
			const launchSecret = "super-secret"
			const ownerEmail = "reader@example.com"
			const route = "/workspace/orbital-lab"
			return <button data-testid="launch-orbital-lab">Launch orbital lab</button>
		}
	`)
	candidates := collectCodeCandidatesForTest(t, root, nil, nil)
	planner := &snippetObservationCapturePlannerLLM{}
	result, err := NewProjectInvestigationToolSuite(planner).Investigate(context.Background(), ProjectInvestigationRequest{
		Root:       root,
		Candidates: candidates,
		Budget: model.CodeReadBudget{
			Mode:                   "tool_driven_intent_drilldown",
			RepoIndexFileLimit:     1,
			DrilldownRounds:        1,
			FilesPerRound:          1,
			TotalFileLimit:         3,
			MaxFileBytes:           80 * 1024,
			ToolSearchFileLimit:    80,
			ToolSearchBytesPerFile: 32 * 1024,
			ToolSearchResultLimit:  10,
		},
		Project: &model.ProjectContext{ID: "project_snippet_observation", ProductDescription: "演示 orbital lab"},
		Brief:   &model.RequirementBrief{ProjectID: "project_snippet_observation", Objective: "演示 orbital lab"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(planner.firstSnippetObservationRequest, "launch-orbital-lab") {
		t.Fatalf("expected transient planner payload to include redacted snippet context, got %s", planner.firstSnippetObservationRequest)
	}
	if strings.Contains(planner.firstSnippetObservationRequest, "super-secret") || strings.Contains(planner.firstSnippetObservationRequest, "reader@example.com") {
		t.Fatalf("transient planner payload leaked sensitive snippet data: %s", planner.firstSnippetObservationRequest)
	}
	traceJSON, err := json.Marshal(result.Trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(traceJSON), "Launch orbital lab") || strings.Contains(string(traceJSON), "super-secret") || strings.Contains(string(traceJSON), "reader@example.com") {
		t.Fatalf("persistent investigation trace leaked raw snippet text: %s", string(traceJSON))
	}
}

func TestProjectInvestigationPlannerCanRequestReadWindowBeforeNextSearch(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"react":"latest","vite":"latest"}}`)
	writeFixtureFile(t, root, "src/views/Flow.tsx", `
		export function Flow() {
			const route = "/workspace/window-lab"
			const handlerHint = "handleWindowLaunch"
			return <button data-testid="launch-window-lab">Launch window lab</button>
		}
	`)
	writeFixtureFile(t, root, "backend/server/WindowAPI.go", `
		package server
		func handleWindowLaunch() {}
	`)
	planner := &readWindowPlannerLLM{}
	candidates := collectCodeCandidatesForTest(t, root, nil, nil)
	result, err := NewProjectInvestigationToolSuite(planner).Investigate(context.Background(), ProjectInvestigationRequest{
		Root:       root,
		Candidates: candidates,
		Budget: model.CodeReadBudget{
			Mode:                   "tool_driven_intent_drilldown",
			RepoIndexFileLimit:     1,
			DrilldownRounds:        1,
			FilesPerRound:          1,
			TotalFileLimit:         5,
			MaxFileBytes:           80 * 1024,
			ToolSearchFileLimit:    80,
			ToolSearchBytesPerFile: 32 * 1024,
			ToolSearchResultLimit:  10,
		},
		Project: &model.ProjectContext{ID: "project_read_window", ProductDescription: "演示 window lab"},
		Brief:   &model.RequirementBrief{ProjectID: "project_read_window", Objective: "演示 window lab"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if investigationToolCallCount(result.Trace, "read_window") == 0 {
		t.Fatalf("expected planner-requested read_window tool call, got %+v", result.Trace.ToolCalls)
	}
	if unexpectedGrep := investigationToolCallWithMetadataValues(result.Trace, "grep_text", map[string]string{"planned_from": "next_actions", "suggested_tool": "read_window"}); unexpectedGrep != nil {
		t.Fatalf("read_window next action should not first run grep_text, got %+v", unexpectedGrep)
	}
	nextActionGrep := investigationToolCallWithMetadataValues(result.Trace, "grep_text", map[string]string{
		"planned_from":   "next_actions",
		"suggested_tool": "grep_text",
	})
	if nextActionGrep == nil || nextActionGrep.Metadata["suggested_tool"] != "grep_text" {
		t.Fatalf("expected read_window observation to drive a follow-up grep, got %+v", result.Trace.ToolCalls)
	}
	if !candidateRelContains(result.SelectedCandidates, "WindowAPI.go") {
		t.Fatalf("expected read_window-driven query to select backend handler, got %+v", result.SelectedCandidates)
	}
	traceJSON, err := json.Marshal(result.Trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(traceJSON), "handlerHint") || strings.Contains(string(traceJSON), "Launch window lab") {
		t.Fatalf("read_window trace must not persist raw window text: %s", string(traceJSON))
	}
}

func TestProjectInvestigationPlannerCanListRelatedFilesBeforeNextSearch(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"react":"latest","vite":"latest"}}`)
	writeFixtureFile(t, root, "src/features/window/Flow.tsx", `
		export function Flow() {
			const route = "/workspace/window-lab"
			return <button data-testid="launch-window-lab">Launch window lab</button>
		}
	`)
	writeFixtureFile(t, root, "src/features/window/WindowAPIClient.ts", `
		export async function startWindowBuild() {
			return fetch("/api/window-lab/start", { method: "POST" })
		}
	`)
	writeFixtureFile(t, root, "src/features/unrelated/ProfileAvatar.tsx", `
		export function ProfileAvatar() { return <button>avatar</button> }
	`)
	planner := &listRelatedFilesPlannerLLM{}
	candidates := collectCodeCandidatesForTest(t, root, nil, nil)
	result, err := NewProjectInvestigationToolSuite(planner).Investigate(context.Background(), ProjectInvestigationRequest{
		Root:       root,
		Candidates: candidates,
		Budget: model.CodeReadBudget{
			Mode:                   "tool_driven_intent_drilldown",
			RepoIndexFileLimit:     1,
			DrilldownRounds:        1,
			FilesPerRound:          1,
			TotalFileLimit:         5,
			MaxFileBytes:           80 * 1024,
			ToolSearchFileLimit:    80,
			ToolSearchBytesPerFile: 32 * 1024,
			ToolSearchResultLimit:  10,
		},
		Project: &model.ProjectContext{ID: "project_list_related", ProductDescription: "演示 window lab"},
		Brief:   &model.RequirementBrief{ProjectID: "project_list_related", Objective: "演示 window lab"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if investigationToolCallCount(result.Trace, "list_related_files") == 0 {
		t.Fatalf("expected planner-requested list_related_files tool call, got %+v", result.Trace.ToolCalls)
	}
	if unexpectedGrep := investigationToolCallWithMetadataValues(result.Trace, "grep_text", map[string]string{"planned_from": "next_actions", "suggested_tool": "list_related_files"}); unexpectedGrep != nil {
		t.Fatalf("list_related_files next action should not first run grep_text, got %+v", unexpectedGrep)
	}
	if !strings.Contains(planner.relatedFilesRequest, "WindowAPIClient.ts") {
		t.Fatalf("expected planner payload to include related file name observation, got %s", planner.relatedFilesRequest)
	}
	if strings.Contains(planner.relatedFilesRequest, "src/features/window") {
		t.Fatalf("related file observation should not expose repository paths: %s", planner.relatedFilesRequest)
	}
	if !candidateRelContains(result.SelectedCandidates, "WindowAPIClient.ts") {
		t.Fatalf("expected related-file driven query to select API client, got %+v", result.SelectedCandidates)
	}
	traceJSON, err := json.Marshal(result.Trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(traceJSON), "src/features/window") || strings.Contains(string(traceJSON), "startWindowBuild") {
		t.Fatalf("list_related_files trace should not leak paths or source text: %s", string(traceJSON))
	}
}

func TestProjectInvestigationPlannerCanInspectFileOutlineBeforeNextSearch(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"react":"latest","vite":"latest"}}`)
	writeFixtureFile(t, root, "src/features/window/Flow.tsx", `
		export function Flow() {
			const route = "/workspace/window-lab"
			return <button data-testid="launch-window-lab">Launch window lab</button>
		}
	`)
	writeFixtureFile(t, root, "src/features/window/WindowAPIClient.ts", `
		export async function startWindowBuild() {
			return fetch("/api/window-lab/start", { method: "POST" })
		}
	`)
	writeFixtureFile(t, root, "backend/server/WindowHandler.go", `
		package server
		func registerWindowRoutes() {
			router.POST("/api/window-lab/start", handleWindowLabStart)
		}
		func handleWindowLabStart() {}
	`)
	planner := &fileOutlinePlannerLLM{}
	candidates := collectCodeCandidatesForTest(t, root, nil, nil)
	result, err := NewProjectInvestigationToolSuite(planner).Investigate(context.Background(), ProjectInvestigationRequest{
		Root:       root,
		Candidates: candidates,
		Budget: model.CodeReadBudget{
			Mode:                   "tool_driven_intent_drilldown",
			RepoIndexFileLimit:     1,
			DrilldownRounds:        2,
			FilesPerRound:          1,
			TotalFileLimit:         6,
			MaxFileBytes:           80 * 1024,
			ToolSearchFileLimit:    100,
			ToolSearchBytesPerFile: 32 * 1024,
			ToolSearchResultLimit:  10,
		},
		Project: &model.ProjectContext{ID: "project_file_outline", ProductDescription: "演示 window lab"},
		Brief:   &model.RequirementBrief{ProjectID: "project_file_outline", Objective: "演示 window lab"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if investigationToolCallCount(result.Trace, "inspect_file_outline") == 0 {
		t.Fatalf("expected planner-requested inspect_file_outline tool call, got %+v", result.Trace.ToolCalls)
	}
	if unexpectedGrep := investigationToolCallWithMetadataValues(result.Trace, "grep_text", map[string]string{"planned_from": "next_actions", "suggested_tool": "inspect_file_outline"}); unexpectedGrep != nil {
		t.Fatalf("inspect_file_outline next action should not first run grep_text, got %+v", unexpectedGrep)
	}
	if !strings.Contains(planner.outlineRequest, "startWindowBuild") || !strings.Contains(planner.outlineRequest, "/api/window-lab/start") {
		t.Fatalf("expected planner payload to include file outline symbols and API paths, got %s", planner.outlineRequest)
	}
	if strings.Contains(planner.outlineRequest, "src/features/window") {
		t.Fatalf("file outline observation should not expose repository paths: %s", planner.outlineRequest)
	}
	if !candidateRelContains(result.SelectedCandidates, "WindowHandler.go") {
		t.Fatalf("expected outline-driven query to select backend handler, got %+v", result.SelectedCandidates)
	}
	traceJSON, err := json.Marshal(result.Trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(traceJSON), "src/features/window") || strings.Contains(string(traceJSON), "startWindowBuild") || strings.Contains(string(traceJSON), "return fetch") {
		t.Fatalf("inspect_file_outline trace should not leak paths or source text: %s", string(traceJSON))
	}
}

func TestProjectInvestigationToolSuiteSurfacesNextActionsForRemainingGaps(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"react":"latest","vite":"latest"}}`)
	writeFixtureFile(t, root, "src/views/Flow.tsx", `
		export function Flow() {
			const route = "/workspace/orbital-lab"
			return <button data-testid="launch-orbital-lab">Launch orbital lab</button>
		}
	`)
	candidates := collectCodeCandidatesForTest(t, root, nil, nil)
	result, err := NewProjectInvestigationToolSuite(uiOnlyInvestigationPlannerLLM{}).Investigate(context.Background(), ProjectInvestigationRequest{
		Root:       root,
		Candidates: candidates,
		Budget: model.CodeReadBudget{
			Mode:                   "tool_driven_intent_drilldown",
			RepoIndexFileLimit:     1,
			DrilldownRounds:        1,
			FilesPerRound:          1,
			TotalFileLimit:         2,
			MaxFileBytes:           80 * 1024,
			ToolSearchFileLimit:    80,
			ToolSearchBytesPerFile: 32 * 1024,
			ToolSearchResultLimit:  10,
		},
		Project: &model.ProjectContext{ID: "project_next_actions", ProductDescription: "演示 orbital lab"},
		Brief:   &model.RequirementBrief{ProjectID: "project_next_actions", Objective: "演示 orbital lab"},
	})
	if err != nil {
		t.Fatal(err)
	}
	question := investigationQuestion(result.Trace, "question_product_entry")
	if question == nil {
		t.Fatalf("expected product entry question, got %+v", result.Trace.Questions)
	}
	if question.Status == "answered" {
		t.Fatalf("expected unresolved question with next actions, got %+v", question)
	}
	if len(question.NextActions) == 0 {
		t.Fatalf("expected next actions for remaining gaps, got %+v", question)
	}
	if question.NextActions[0].Tool != "find_api_handlers" {
		t.Fatalf("expected api gap to suggest backend handler search, got %+v", question.NextActions)
	}
	if len(question.NextActions[0].QueryTerms) == 0 || len(question.NextActions[0].ExpectedEvidence) == 0 {
		t.Fatalf("expected next action to carry query terms and expected evidence, got %+v", question.NextActions[0])
	}
	traceJSON, err := json.Marshal(question)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(traceJSON), "Launch orbital lab") {
		t.Fatalf("next actions trace should not leak source text: %+v", question)
	}
}

func TestProjectInvestigationToolSuiteExecutesAdaptiveQueryBeyondInitialPlannerRounds(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"react":"latest","vite":"latest"}}`)
	writeFixtureFile(t, root, "src/views/Flow.tsx", `
		export function Flow() {
			const route = "/workspace/orbital-lab"
			return <button data-testid="launch-orbital-lab">Launch orbital lab</button>
		}
	`)
	writeFixtureFile(t, root, "backend/server/OrbitalAPI.go", `
		package server
		func handleOrbitalLaunch() {
			path := "/api/orbital-lab/launch"
			_ = path
		}
	`)
	candidates := collectCodeCandidatesForTest(t, root, nil, nil)
	result, err := NewProjectInvestigationToolSuite(twoRoundUIOnlyInvestigationPlannerLLM{}).Investigate(context.Background(), ProjectInvestigationRequest{
		Root:       root,
		Candidates: candidates,
		Budget: model.CodeReadBudget{
			Mode:                   "tool_driven_intent_drilldown",
			RepoIndexFileLimit:     1,
			DrilldownRounds:        2,
			FilesPerRound:          1,
			TotalFileLimit:         5,
			MaxFileBytes:           80 * 1024,
			ToolSearchFileLimit:    80,
			ToolSearchBytesPerFile: 32 * 1024,
			ToolSearchResultLimit:  10,
		},
		Project: &model.ProjectContext{ID: "project_adaptive_budget", ProductDescription: "演示 orbital lab"},
		Brief:   &model.RequirementBrief{ProjectID: "project_adaptive_budget", Objective: "演示 orbital lab"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if grepToolCallCount(result.Trace) < 3 {
		t.Fatalf("expected adaptive grep after the two initial planner rounds, got %+v", result.Trace.ToolCalls)
	}
	nextActionGrep := investigationToolCallWithMetadata(result.Trace, "grep_text", "planned_from", "next_actions")
	if nextActionGrep == nil {
		t.Fatalf("expected next_actions to schedule a follow-up grep_text call, got %+v", result.Trace.ToolCalls)
	}
	if nextActionGrep.Metadata["suggested_tool"] != "find_api_handlers" {
		t.Fatalf("expected next_actions follow-up to preserve suggested tool, got %+v", nextActionGrep.Metadata)
	}
	if !candidateRelContains(result.SelectedCandidates, "OrbitalAPI.go") {
		t.Fatalf("expected adaptive query beyond initial planner rounds to select backend API file, got %+v", result.SelectedCandidates)
	}
}

func TestProjectIntelligenceGraphUsesInvestigationSuiteForFocusedDrilldown(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"react":"latest","vite":"latest"}}`)
	writeFixtureFile(t, root, "src/views/Flow.tsx", `
		export function Flow() {
			const route = "/workspace/orbital-lab"
			return <button data-testid="launch-orbital-lab">Launch orbital lab</button>
		}
		const launchAPI = "/api/orbital-lab/launch"
	`)
	project := &model.ProjectContext{
		ID:                 "project_pi_drilldown",
		ProductURL:         "https://cascadeai.cn",
		ProductDescription: "演示 orbital-lab 研发工作流",
		Inputs: &model.ProjectInputBundle{Code: []model.CodeInput{{
			ID:           "code_pi_drilldown",
			Kind:         "repository",
			LocalPath:    root,
			RepositoryID: "repo_pi_drilldown",
		}}},
	}
	brief := &model.RequirementBrief{
		ProjectID: project.ID,
		Objective: "演示 orbital-lab 研发工作流",
		MustShow:  []string{"orbital-lab", "launch-orbital-lab"},
	}

	pack, _, graphTrace, err := NewProjectIntelligenceGraph().RunProjectIntelligence(context.Background(), project, brief, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pack == nil || pack.Architecture == nil || !architectureRouteContains(pack.Architecture.RouteTree, "/workspace/orbital-lab") {
		t.Fatalf("expected focused route drilldown to add orbital route, got %+v", pack)
	}
	if !interactionSurfaceContainsSelector(pack.InteractionSurfaces, "[data-testid='launch-orbital-lab']") {
		t.Fatalf("expected focused component drilldown to add orbital selector, got %+v", pack.InteractionSurfaces)
	}
	if !apiContractContains(pack.APIContracts, "/api/orbital-lab/launch") {
		t.Fatalf("expected focused API drilldown to add orbital API, got %+v", pack.APIContracts)
	}
	if !agentGraphTraceOutputContains(graphTrace, "工具化 route drilldown") {
		t.Fatalf("expected graph trace to mention focused route drilldown, got %+v", graphTrace)
	}
}

func TestScriptReadinessBlocksHighOverreadCodeInvestigation(t *testing.T) {
	state := projectUnderstandingStateWithCodeInvestigationQuality(&model.CodeInvestigationQualitySummary{
		Mode:                     "tool_driven_intent_drilldown",
		ToolDriven:               true,
		ToolCallCount:            4,
		SpecializedToolCallCount: 0,
		TotalFilesDiscovered:     100,
		TotalFilesSearched:       95,
		TotalFilesSelected:       90,
		StructuredFileCount:      90,
		SelectedFileRatio:        0.9,
		SearchFileRatio:          0.95,
		SourceTextPolicy:         "no_raw_source_persisted",
		OverreadRisk:             "high",
		Summary:                  "工具化调查：4 次工具调用，结构化读取 90/100 文件，过读风险=high。",
		Confidence:               0.32,
	})

	readiness := scriptReadinessFromState(state, nil)
	if readiness.CanProceed {
		t.Fatalf("high overread code investigation should block packaging, got %+v", readiness)
	}
	if !agentFindingsContainKind(readiness.Blockers, "code_investigation_overread") {
		t.Fatalf("expected code investigation overread blocker, got %+v", readiness.Blockers)
	}
	if readiness.CodeInvestigationOverreadRisk != "high" || readiness.CodeInvestigationSpecializedToolCallCount != 0 {
		t.Fatalf("readiness should carry code investigation metrics, got %+v", readiness)
	}
}

func TestScriptReadinessCarriesToolDrivenCodeInvestigationQuality(t *testing.T) {
	state := projectUnderstandingStateWithCodeInvestigationQuality(&model.CodeInvestigationQualitySummary{
		Mode:                     "tool_driven_intent_drilldown",
		ToolDriven:               true,
		ToolCallCount:            7,
		SpecializedToolCallCount: 3,
		ShellRunToolCallCount:    1,
		TotalFilesDiscovered:     40,
		TotalFilesSearched:       12,
		TotalFilesSelected:       6,
		StructuredFileCount:      6,
		SelectedFileRatio:        0.15,
		SearchFileRatio:          0.3,
		OpenQuestionCount:        1,
		RemainingGaps:            []string{"确认构建模式后端 handler"},
		SourceTextPolicy:         "no_raw_source_persisted",
		OverreadRisk:             "low",
		Summary:                  "工具化调查：7 次工具调用，3 次专用工具，结构化读取 6/40 文件，过读风险=low。",
		Confidence:               0.86,
	})

	readiness := scriptReadinessFromState(state, []model.DemoScenarioPlan{{ID: "scenario_tetris", Name: "俄罗斯方块构建演示", EstimatedSteps: 6, EstimatedDurationSec: 80}})
	if !readiness.CanProceed {
		t.Fatalf("low-risk tool-driven investigation should allow packaging, got %+v", readiness)
	}
	if readiness.CodeInvestigationOverreadRisk != "low" || !readiness.CodeInvestigationToolDriven || readiness.CodeInvestigationSpecializedToolCallCount != 3 {
		t.Fatalf("readiness should expose tool-driven code investigation quality, got %+v", readiness)
	}
	if !stringSliceContains(readiness.CodeInvestigationGaps, "确认构建模式后端 handler") {
		t.Fatalf("readiness should carry bounded investigation gaps, got %+v", readiness.CodeInvestigationGaps)
	}
	if !agentFindingsContainKind(readiness.Warnings, "code_investigation_open_questions") {
		t.Fatalf("expected open question warning, got %+v", readiness.Warnings)
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

func gitInitFixtureRepo(t *testing.T, root string) {
	t.Helper()
	runGitForTest(t, root, "init")
	runGitForTest(t, root, "config", "user.email", "cascade-test@example.com")
	runGitForTest(t, root, "config", "user.name", "Cascade Test")
	runGitForTest(t, root, "add", ".")
	runGitForTest(t, root, "commit", "-m", "fixture")
}

func runGitForTest(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(output))
	}
}

func collectCodeCandidatesForTest(t *testing.T, root string, project *model.ProjectContext, brief *model.RequirementBrief) []codeCandidateFile {
	t.Helper()
	candidates, err := collectCodeCandidates(context.Background(), root, 80*1024, project, brief)
	if err != nil {
		t.Fatal(err)
	}
	return candidates
}

func projectUnderstandingStateWithCodeInvestigationQuality(quality *model.CodeInvestigationQualitySummary) *ProjectUnderstandingState {
	project := &model.ProjectContext{
		ID:                 "project_investigation_readiness",
		ProductURL:         "https://cascadeai.cn",
		ProductDescription: "演示登录，新建项目，俄罗斯方块，构建模式，agent实际构建演示",
	}
	return &ProjectUnderstandingState{
		Project: project,
		Brief: &model.RequirementBrief{
			ProjectID: project.ID,
			Objective: project.ProductDescription,
			MustShow:  []string{"新建项目", "俄罗斯方块", "构建模式"},
		},
		CodeSnapshots: []model.CodeUnderstandingSnapshot{{
			ID:                   "code_investigation_readiness",
			ProjectID:            project.ID,
			SchemaVersion:        model.MultimodalUnderstandingReportSchemaVersion,
			FileCount:            firstPositiveInt(quality.StructuredFileCount, quality.TotalFilesSelected, 6),
			InvestigationQuality: quality,
		}},
		PageSnapshots: []model.PageUnderstandingSnapshot{{
			ID:            "page_workspace",
			ProjectID:     project.ID,
			SchemaVersion: model.MultimodalUnderstandingReportSchemaVersion,
			URL:           "https://cascadeai.cn/workspace",
			Title:         "Workspace",
		}},
		Pack: &model.ProjectIntelligencePack{
			ID:            "project_intelligence_readiness",
			ProjectID:     project.ID,
			SchemaVersion: model.ProjectIntelligencePackSchemaVersion,
			Architecture: &model.ProjectArchitectureMap{
				ID:        "arch_readiness",
				ProjectID: project.ID,
				RouteTree: []model.ArchitectureRouteNode{{ID: "route_workspace", Path: "/workspace"}},
			},
			InteractionSurfaces: []model.InteractionSurface{{
				ID:    "surface_workspace",
				URL:   "https://cascadeai.cn/workspace",
				Title: "Workspace",
				Actions: []model.UIActionRef{{
					ID:       "action_new_project",
					Label:    "新建项目",
					Kind:     "click",
					Selector: "[data-testid='new-project']",
				}},
				StableSelectors: []model.SelectorCandidate{{Kind: "css", Value: "[data-testid='new-project']", StabilityScore: 0.92}},
			}},
		},
	}
}

func agentFindingsContainKind(findings []model.AgentFinding, kind string) bool {
	for _, finding := range findings {
		if finding.Kind == kind {
			return true
		}
	}
	return false
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

func apiContractContains(values []model.APIContractSummary, path string) bool {
	for _, value := range values {
		if value.Path == path {
			return true
		}
	}
	return false
}

func dataModelInsightContains(values []model.DataModelInsight, name string) bool {
	for _, value := range values {
		if value.Name == name {
			return true
		}
	}
	return false
}

func architectureRouteContains(values []model.ArchitectureRouteNode, path string) bool {
	for _, value := range values {
		if value.Path == path {
			return true
		}
	}
	return false
}

func interactionSurfaceContainsSelector(values []model.InteractionSurface, selector string) bool {
	for _, surface := range values {
		for _, candidate := range surface.StableSelectors {
			if candidate.Value == selector {
				return true
			}
		}
		for _, action := range surface.Actions {
			if action.Selector == selector {
				return true
			}
		}
	}
	return false
}

func agentGraphTraceOutputContains(trace *model.AgentGraphTrace, text string) bool {
	if trace == nil {
		return false
	}
	for _, step := range trace.Steps {
		if strings.Contains(step.OutputSummary, text) || strings.Contains(step.InputSummary, text) {
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

func investigationHasTool(trace *model.CodeInvestigationTrace, tool string) bool {
	return investigationToolCall(trace, tool) != nil
}

func investigationHasQuestion(trace *model.CodeInvestigationTrace, id string) bool {
	return investigationQuestion(trace, id) != nil
}

func investigationQuestion(trace *model.CodeInvestigationTrace, id string) *model.CodeInvestigationQuestion {
	if trace == nil {
		return nil
	}
	for i := range trace.Questions {
		if trace.Questions[i].ID == id {
			return &trace.Questions[i]
		}
	}
	return nil
}

func investigationQuestionWithNextAction(trace *model.CodeInvestigationTrace, tool string) *model.CodeInvestigationQuestion {
	if trace == nil {
		return nil
	}
	for i := range trace.Questions {
		for _, action := range trace.Questions[i].NextActions {
			if action.Tool == tool {
				return &trace.Questions[i]
			}
		}
	}
	return nil
}

func investigationToolCall(trace *model.CodeInvestigationTrace, tool string) *model.CodeInvestigationToolCall {
	if trace == nil {
		return nil
	}
	for i := range trace.ToolCalls {
		if trace.ToolCalls[i].Tool == tool {
			return &trace.ToolCalls[i]
		}
	}
	return nil
}

func investigationToolCallWithMetadata(trace *model.CodeInvestigationTrace, tool string, key string, value string) *model.CodeInvestigationToolCall {
	if trace == nil {
		return nil
	}
	for i := range trace.ToolCalls {
		call := &trace.ToolCalls[i]
		if call.Tool != tool || call.Metadata == nil {
			continue
		}
		if fmt.Sprint(call.Metadata[key]) == value {
			return call
		}
	}
	return nil
}

func investigationToolCallWithMetadataValues(trace *model.CodeInvestigationTrace, tool string, values map[string]string) *model.CodeInvestigationToolCall {
	if trace == nil {
		return nil
	}
	for i := range trace.ToolCalls {
		call := &trace.ToolCalls[i]
		if call.Tool != tool || call.Metadata == nil {
			continue
		}
		matched := true
		for key, value := range values {
			if fmt.Sprint(call.Metadata[key]) != value {
				matched = false
				break
			}
		}
		if matched {
			return call
		}
	}
	return nil
}

func investigationToolCallCount(trace *model.CodeInvestigationTrace, tool string) int {
	count := 0
	if trace == nil {
		return count
	}
	for _, call := range trace.ToolCalls {
		if call.Tool == tool {
			count++
		}
	}
	return count
}

func grepToolCallCount(trace *model.CodeInvestigationTrace) int {
	count := 0
	if trace == nil {
		return count
	}
	for _, call := range trace.ToolCalls {
		if call.Tool == "grep_text" {
			count++
		}
	}
	return count
}

func stringSliceContains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func candidateRelContains(candidates []codeCandidateFile, text string) bool {
	for _, candidate := range candidates {
		if strings.Contains(filepath.ToSlash(candidate.rel), text) {
			return true
		}
	}
	return false
}

type codeInvestigationPlannerLLM struct{}

func (codeInvestigationPlannerLLM) GenerateJSON(ctx context.Context, task config.ModelTask, req llm.JSONRequest, target any) (*llm.CallTrace, error) {
	data := []byte(`{
		"summary": "repo map shows a Flow view likely tied to the requested workflow; search its semantic id.",
		"queries": [
			{"purpose": "定位研发工作流的核心页面和启动控件", "terms": ["orbital-lab", "launch-orbital-lab"]}
		],
		"confidence": 0.86
	}`)
	if err := json.Unmarshal(data, target); err != nil {
		return nil, err
	}
	return &llm.CallTrace{Provider: config.ModelProviderGLM, Model: "test-planner", Task: task, AdapterVersion: "test", Mode: config.LLMModeDeterministic}, nil
}

func (codeInvestigationPlannerLLM) GenerateText(ctx context.Context, task config.ModelTask, req llm.TextRequest) (string, *llm.CallTrace, error) {
	return "", nil, nil
}

func (codeInvestigationPlannerLLM) GenerateMultimodal(ctx context.Context, task config.ModelTask, req llm.MultimodalRequest, target any) (*llm.CallTrace, error) {
	return nil, nil
}

type uiOnlyInvestigationPlannerLLM struct{}

func (uiOnlyInvestigationPlannerLLM) GenerateJSON(ctx context.Context, task config.ModelTask, req llm.JSONRequest, target any) (*llm.CallTrace, error) {
	data := []byte(`{
		"summary": "first inspect the visible launch control",
		"queries": [
			{"purpose": "定位可见启动控件", "terms": ["launch-orbital-lab"]}
		],
		"confidence": 0.82
	}`)
	if err := json.Unmarshal(data, target); err != nil {
		return nil, err
	}
	return &llm.CallTrace{Provider: config.ModelProviderGLM, Model: "test-ui-planner", Task: task, AdapterVersion: "test", Mode: config.LLMModeDeterministic}, nil
}

func (uiOnlyInvestigationPlannerLLM) GenerateText(ctx context.Context, task config.ModelTask, req llm.TextRequest) (string, *llm.CallTrace, error) {
	return "", nil, nil
}

func (uiOnlyInvestigationPlannerLLM) GenerateMultimodal(ctx context.Context, task config.ModelTask, req llm.MultimodalRequest, target any) (*llm.CallTrace, error) {
	return nil, nil
}

type twoRoundUIOnlyInvestigationPlannerLLM struct{}

func (twoRoundUIOnlyInvestigationPlannerLLM) GenerateJSON(ctx context.Context, task config.ModelTask, req llm.JSONRequest, target any) (*llm.CallTrace, error) {
	data := []byte(`{
		"summary": "planner fills the configured rounds before evidence review asks for backend evidence",
		"queries": [
			{"purpose": "定位可见启动控件", "terms": ["launch-orbital-lab"]},
			{"purpose": "检查无关 UI 文案", "terms": ["unrelated-ui-token"]}
		],
		"confidence": 0.82
	}`)
	if err := json.Unmarshal(data, target); err != nil {
		return nil, err
	}
	return &llm.CallTrace{Provider: config.ModelProviderGLM, Model: "test-two-round-ui-planner", Task: task, AdapterVersion: "test", Mode: config.LLMModeDeterministic}, nil
}

func (twoRoundUIOnlyInvestigationPlannerLLM) GenerateText(ctx context.Context, task config.ModelTask, req llm.TextRequest) (string, *llm.CallTrace, error) {
	return "", nil, nil
}

func (twoRoundUIOnlyInvestigationPlannerLLM) GenerateMultimodal(ctx context.Context, task config.ModelTask, req llm.MultimodalRequest, target any) (*llm.CallTrace, error) {
	return nil, nil
}

type observationNextActionPlannerLLM struct{}

func (observationNextActionPlannerLLM) GenerateJSON(ctx context.Context, task config.ModelTask, req llm.JSONRequest, target any) (*llm.CallTrace, error) {
	var data []byte
	switch target.(type) {
	case *codeInvestigationLLMOutput:
		data = []byte(`{
			"summary": "first locate the orbital lab UI control",
			"queries": [
				{"purpose": "定位 orbital lab 页面和启动控件", "terms": ["orbital", "lab", "launch-orbital-lab"]}
			],
			"confidence": 0.84
		}`)
	case *codeInvestigationNextActionsLLMOutput:
		data = []byte(`{
			"summary": "UI evidence is missing backend support; inspect the likely handler symbol next.",
			"next_actions": [
				{
					"tool": "grep_text",
					"reason": "上一轮只确认了 route/component，继续按 handler 符号查后端实现。",
					"query_terms": ["handleOrbitalLaunch", "orbital"],
					"expected_evidence": ["api_or_data_model"]
				}
			],
			"confidence": 0.86
		}`)
	default:
		data = []byte(`{}`)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return nil, err
	}
	return &llm.CallTrace{Provider: config.ModelProviderGLM, Model: "test-observation-next-action-planner", Task: task, AdapterVersion: "test", Mode: config.LLMModeDeterministic}, nil
}

func (observationNextActionPlannerLLM) GenerateText(ctx context.Context, task config.ModelTask, req llm.TextRequest) (string, *llm.CallTrace, error) {
	return "", nil, nil
}

func (observationNextActionPlannerLLM) GenerateMultimodal(ctx context.Context, task config.ModelTask, req llm.MultimodalRequest, target any) (*llm.CallTrace, error) {
	return nil, nil
}

type snippetObservationCapturePlannerLLM struct {
	lastNextActionRequest          string
	firstSnippetObservationRequest string
}

func (l *snippetObservationCapturePlannerLLM) GenerateJSON(ctx context.Context, task config.ModelTask, req llm.JSONRequest, target any) (*llm.CallTrace, error) {
	var data []byte
	switch target.(type) {
	case *codeInvestigationLLMOutput:
		data = []byte(`{
			"summary": "locate the UI route first",
			"queries": [
				{"purpose": "定位 orbital lab 控件", "terms": ["orbital", "lab", "launch-orbital-lab"]}
			],
			"confidence": 0.84
		}`)
	case *codeInvestigationNextActionsLLMOutput:
		l.lastNextActionRequest = req.User
		if l.firstSnippetObservationRequest == "" && strings.Contains(req.User, `"snippet_observations":[`) {
			l.firstSnippetObservationRequest = req.User
		}
		data = []byte(`{
			"summary": "use the observed UI context to continue backend lookup",
			"next_actions": [
				{
					"tool": "grep_text",
					"reason": "snippet observation showed the launch control; continue with handler terms.",
					"query_terms": ["orbital", "handler"],
					"expected_evidence": ["api_or_data_model"]
				}
			],
			"confidence": 0.82
		}`)
	default:
		data = []byte(`{}`)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return nil, err
	}
	return &llm.CallTrace{Provider: config.ModelProviderGLM, Model: "test-snippet-observation-planner", Task: task, AdapterVersion: "test", Mode: config.LLMModeDeterministic}, nil
}

func (l *snippetObservationCapturePlannerLLM) GenerateText(ctx context.Context, task config.ModelTask, req llm.TextRequest) (string, *llm.CallTrace, error) {
	return "", nil, nil
}

func (l *snippetObservationCapturePlannerLLM) GenerateMultimodal(ctx context.Context, task config.ModelTask, req llm.MultimodalRequest, target any) (*llm.CallTrace, error) {
	return nil, nil
}

type readWindowPlannerLLM struct {
	nextActionCalls int
}

func (l *readWindowPlannerLLM) GenerateJSON(ctx context.Context, task config.ModelTask, req llm.JSONRequest, target any) (*llm.CallTrace, error) {
	var data []byte
	switch target.(type) {
	case *codeInvestigationLLMOutput:
		data = []byte(`{
			"summary": "find the visible window lab control first",
			"queries": [
				{"purpose": "定位 window lab 页面和启动控件", "terms": ["window", "lab", "launch-window-lab"]}
			],
			"confidence": 0.84
		}`)
	case *codeInvestigationNextActionsLLMOutput:
		l.nextActionCalls++
		if l.nextActionCalls == 1 {
			data = []byte(`{
				"summary": "read a little more around the UI snippet before deciding backend terms",
				"next_actions": [
					{
						"tool": "read_window",
						"reason": "上一轮 snippet 只显示控件，需要读取附近小窗口找 handler hint。",
						"query_terms": ["launch-window-lab"],
						"expected_evidence": ["api_or_data_model"]
					}
				],
				"confidence": 0.82
			}`)
		} else {
			data = []byte(`{
				"summary": "window observation exposed the handler hint; grep backend symbol next",
				"next_actions": [
					{
						"tool": "grep_text",
						"reason": "read_window 观察到 handleWindowLaunch，继续定位后端实现。",
						"query_terms": ["handleWindowLaunch"],
						"expected_evidence": ["api_or_data_model"]
					}
				],
				"confidence": 0.88
			}`)
		}
	default:
		data = []byte(`{}`)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return nil, err
	}
	return &llm.CallTrace{Provider: config.ModelProviderGLM, Model: "test-read-window-planner", Task: task, AdapterVersion: "test", Mode: config.LLMModeDeterministic}, nil
}

func (l *readWindowPlannerLLM) GenerateText(ctx context.Context, task config.ModelTask, req llm.TextRequest) (string, *llm.CallTrace, error) {
	return "", nil, nil
}

func (l *readWindowPlannerLLM) GenerateMultimodal(ctx context.Context, task config.ModelTask, req llm.MultimodalRequest, target any) (*llm.CallTrace, error) {
	return nil, nil
}

type listRelatedFilesPlannerLLM struct {
	nextActionCalls     int
	relatedFilesRequest string
}

func (l *listRelatedFilesPlannerLLM) GenerateJSON(ctx context.Context, task config.ModelTask, req llm.JSONRequest, target any) (*llm.CallTrace, error) {
	var data []byte
	switch target.(type) {
	case *codeInvestigationLLMOutput:
		data = []byte(`{
			"summary": "find the window lab UI file first",
			"queries": [
				{"purpose": "定位 window lab 页面和启动控件", "terms": ["window", "lab", "launch-window-lab"]}
			],
			"confidence": 0.84
		}`)
	case *codeInvestigationNextActionsLLMOutput:
		l.nextActionCalls++
		if strings.Contains(req.User, `"related_file_observations":[`) {
			l.relatedFilesRequest = req.User
		}
		if l.nextActionCalls == 1 {
			data = []byte(`{
				"summary": "inspect related files around the matched feature directory",
				"next_actions": [
					{
						"tool": "list_related_files",
						"reason": "先看命中页面同级有哪些 API/client/component 文件，再决定下一步。",
						"query_terms": ["window"],
						"expected_evidence": ["api_or_data_model"]
					}
				],
				"confidence": 0.83
			}`)
		} else {
			data = []byte(`{
				"summary": "related files show an API client candidate; grep its file name next",
				"next_actions": [
					{
						"tool": "grep_text",
						"reason": "list_related_files 发现 WindowAPIClient.ts，继续读取该 API client 摘要。",
						"query_terms": ["WindowAPIClient"],
						"expected_evidence": ["api_or_data_model"]
					}
				],
				"confidence": 0.87
			}`)
		}
	default:
		data = []byte(`{}`)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return nil, err
	}
	return &llm.CallTrace{Provider: config.ModelProviderGLM, Model: "test-list-related-files-planner", Task: task, AdapterVersion: "test", Mode: config.LLMModeDeterministic}, nil
}

func (l *listRelatedFilesPlannerLLM) GenerateText(ctx context.Context, task config.ModelTask, req llm.TextRequest) (string, *llm.CallTrace, error) {
	return "", nil, nil
}

func (l *listRelatedFilesPlannerLLM) GenerateMultimodal(ctx context.Context, task config.ModelTask, req llm.MultimodalRequest, target any) (*llm.CallTrace, error) {
	return nil, nil
}

type fileOutlinePlannerLLM struct {
	nextActionCalls int
	outlineRequest  string
}

func (l *fileOutlinePlannerLLM) GenerateJSON(ctx context.Context, task config.ModelTask, req llm.JSONRequest, target any) (*llm.CallTrace, error) {
	var data []byte
	switch target.(type) {
	case *codeInvestigationLLMOutput:
		data = []byte(`{
			"summary": "find the window lab UI file first",
			"queries": [
				{"purpose": "定位 window lab 页面和启动控件", "terms": ["window", "lab", "launch-window-lab"]}
			],
			"confidence": 0.84
		}`)
	case *codeInvestigationNextActionsLLMOutput:
		l.nextActionCalls++
		if strings.Contains(req.User, `"file_outline_observations":[`) {
			l.outlineRequest = req.User
		}
		switch l.nextActionCalls {
		case 1:
			data = []byte(`{
				"summary": "inspect related files around the matched feature directory",
				"next_actions": [
					{
						"tool": "list_related_files",
						"reason": "先看命中页面同级有哪些 API/client/component 文件，再决定下一步。",
						"query_terms": ["window"],
						"expected_evidence": ["api_or_data_model"]
					}
				],
				"confidence": 0.83
			}`)
		case 2:
			data = []byte(`{
				"summary": "inspect the related API client outline",
				"next_actions": [
					{
						"tool": "inspect_file_outline",
						"reason": "根据邻近文件列表，先读 API client 的结构轮廓。",
						"query_terms": ["WindowAPIClient"],
						"expected_evidence": ["api_or_data_model"]
					}
				],
				"confidence": 0.86
			}`)
		default:
			data = []byte(`{
				"summary": "outline shows the API path; grep the backend handler next",
				"next_actions": [
					{
						"tool": "grep_text",
						"reason": "outline 里看到 /api/window-lab/start，继续定位后端实现。",
						"query_terms": ["/api/window-lab/start"],
						"expected_evidence": ["api_or_data_model"]
					}
				],
				"confidence": 0.89
			}`)
		}
	default:
		data = []byte(`{}`)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return nil, err
	}
	return &llm.CallTrace{Provider: config.ModelProviderGLM, Model: "test-file-outline-planner", Task: task, AdapterVersion: "test", Mode: config.LLMModeDeterministic}, nil
}

func (l *fileOutlinePlannerLLM) GenerateText(ctx context.Context, task config.ModelTask, req llm.TextRequest) (string, *llm.CallTrace, error) {
	return "", nil, nil
}

func (l *fileOutlinePlannerLLM) GenerateMultimodal(ctx context.Context, task config.ModelTask, req llm.MultimodalRequest, target any) (*llm.CallTrace, error) {
	return nil, nil
}

type toolInventoryPlannerLLM struct {
	initialRequest    string
	nextActionRequest string
}

func (l *toolInventoryPlannerLLM) GenerateJSON(ctx context.Context, task config.ModelTask, req llm.JSONRequest, target any) (*llm.CallTrace, error) {
	var data []byte
	switch target.(type) {
	case *codeInvestigationLLMOutput:
		l.initialRequest = req.User
		data = []byte(`{
			"summary": "start with the visible tool-card lab control",
			"queries": [
				{"purpose": "定位 tool card lab 页面和启动控件", "terms": ["tool-card", "launch-tool-card-lab"]}
			],
			"confidence": 0.81
		}`)
	case *codeInvestigationNextActionsLLMOutput:
		l.nextActionRequest = req.User
		data = []byte(`{
			"summary": "next inspect nearby files and then API handler hints",
			"next_actions": [
				{
					"tool": "inspect_file_outline",
					"reason": "先看命中文件附近的结构轮廓。",
					"query_terms": ["tool-card"],
					"expected_evidence": ["api_or_data_model"]
				}
			],
			"confidence": 0.85
		}`)
	default:
		data = []byte(`{}`)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return nil, err
	}
	return &llm.CallTrace{Provider: config.ModelProviderGLM, Model: "test-tool-inventory-planner", Task: task, AdapterVersion: "test", Mode: config.LLMModeDeterministic}, nil
}

func (l *toolInventoryPlannerLLM) GenerateText(ctx context.Context, task config.ModelTask, req llm.TextRequest) (string, *llm.CallTrace, error) {
	return "", nil, nil
}

func (l *toolInventoryPlannerLLM) GenerateMultimodal(ctx context.Context, task config.ModelTask, req llm.MultimodalRequest, target any) (*llm.CallTrace, error) {
	return nil, nil
}

type shellRunPlannerLLM struct{}

func (shellRunPlannerLLM) GenerateJSON(ctx context.Context, task config.ModelTask, req llm.JSONRequest, target any) (*llm.CallTrace, error) {
	var data []byte
	switch target.(type) {
	case *codeInvestigationLLMOutput:
		data = []byte(`{
			"summary": "start with the visible shell lab control",
			"queries": [
				{"purpose": "定位 shell lab 页面和启动控件", "terms": ["shell-lab", "launch-shell-lab"]}
			],
			"confidence": 0.82
		}`)
	case *codeInvestigationNextActionsLLMOutput:
		data = []byte(`{
			"summary": "the UI file is found; use read-only shell discovery to find the nearby API client by filename.",
			"next_actions": [
				{
					"tool": "shell_run",
					"command_kind": "rg_files",
					"reason": "像 CLI 助手一样先做只读文件名探测，定位 BuildClient。",
					"query_terms": ["BuildClient"],
					"expected_evidence": ["api_or_data_model"]
				}
			],
			"confidence": 0.87
		}`)
	default:
		data = []byte(`{}`)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return nil, err
	}
	return &llm.CallTrace{Provider: config.ModelProviderGLM, Model: "test-shell-run-planner", Task: task, AdapterVersion: "test", Mode: config.LLMModeDeterministic}, nil
}

func (shellRunPlannerLLM) GenerateText(ctx context.Context, task config.ModelTask, req llm.TextRequest) (string, *llm.CallTrace, error) {
	return "", nil, nil
}

func (shellRunPlannerLLM) GenerateMultimodal(ctx context.Context, task config.ModelTask, req llm.MultimodalRequest, target any) (*llm.CallTrace, error) {
	return nil, nil
}

type initialShellRunPlannerLLM struct{}

func (initialShellRunPlannerLLM) GenerateJSON(ctx context.Context, task config.ModelTask, req llm.JSONRequest, target any) (*llm.CallTrace, error) {
	var data []byte
	switch target.(type) {
	case *codeInvestigationLLMOutput:
		data = []byte(`{
			"summary": "start by listing matching file names before reading content.",
			"queries": [
				{
					"tool": "shell_run",
					"command_kind": "rg_files",
					"purpose": "首轮先用只读文件名探测定位 BuildClient。",
					"terms": ["BuildClient"],
					"expected_evidence": ["api_or_data_model"]
				}
			],
			"confidence": 0.86
		}`)
	case *codeInvestigationNextActionsLLMOutput:
		data = []byte(`{"summary":"no follow-up needed","next_actions":[],"confidence":0.6}`)
	default:
		data = []byte(`{}`)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return nil, err
	}
	return &llm.CallTrace{Provider: config.ModelProviderGLM, Model: "test-initial-shell-run-planner", Task: task, AdapterVersion: "test", Mode: config.LLMModeDeterministic}, nil
}

func (initialShellRunPlannerLLM) GenerateText(ctx context.Context, task config.ModelTask, req llm.TextRequest) (string, *llm.CallTrace, error) {
	return "", nil, nil
}

func (initialShellRunPlannerLLM) GenerateMultimodal(ctx context.Context, task config.ModelTask, req llm.MultimodalRequest, target any) (*llm.CallTrace, error) {
	return nil, nil
}
