package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	if snapshot.ReadBudget == nil || snapshot.ReadBudget.TotalFileLimit != 80 {
		t.Fatalf("expected default intent-driven read budget, got %+v", snapshot.ReadBudget)
	}
	if snapshot.FileCount > 80 {
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
	if !investigationHasTool(snapshot.InvestigationTrace, "repo_index") || !investigationHasTool(snapshot.InvestigationTrace, "grep_text") {
		t.Fatalf("expected repo_index and grep_text tool calls, got %+v", snapshot.InvestigationTrace.ToolCalls)
	}
	if !investigationHasQuestion(snapshot.InvestigationTrace, "question_project_creation") {
		t.Fatalf("expected project creation investigation question, got %+v", snapshot.InvestigationTrace.Questions)
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

func collectCodeCandidatesForTest(t *testing.T, root string, project *model.ProjectContext, brief *model.RequirementBrief) []codeCandidateFile {
	t.Helper()
	candidates := []codeCandidateFile{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		name := entry.Name()
		if entry.IsDir() {
			if shouldSkipDir(name) && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !isScannableFile(name) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = name
		}
		if shouldSkipCodeFileRel(rel, name) {
			return nil
		}
		candidates = append(candidates, codeCandidateFile{
			path:  path,
			rel:   rel,
			name:  name,
			size:  info.Size(),
			score: codeFilePriority(rel, name, project, brief),
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return candidates
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
