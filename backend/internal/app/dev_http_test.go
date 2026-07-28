package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
	"cascade-demoops/backend/internal/store"
)

func TestDevHTTPBridgeGeneratesExecutionPackage(t *testing.T) {
	server := newTestDevHTTPServer(t)

	input := orchestrator.UserInput{
		Mode:               model.AppModeDesktop,
		ProductURL:         "https://app.example.com",
		ProductDescription: "生成一套团队协作功能的产品演示执行包。",
		TargetAudience:     "中国产品运营团队",
		ForbiddenPages:     []string{"/billing", "/settings/api-keys"},
		ForbiddenData:      []string{"客户邮箱", "API Key"},
		WebpageScreenshots: []model.WebpageScreenshotInput{verifiedActionScreenshotInput()},
	}
	body, err := json.Marshal(ExecutionPackageRequest{UserInput: &input})
	if err != nil {
		t.Fatal(err)
	}
	state, payload := postExecutionPackageWithPayload(t, server, body)
	if state.Status != orchestrator.FlowStatusAwaitingHuman || state.CurrentNode != orchestrator.NodeHumanApprove {
		t.Fatalf("expected awaiting human execution package state, got node=%s status=%s", state.CurrentNode, state.Status)
	}
	if state.UnderstandingReport == nil || state.ProductMap == nil || state.WorkflowGraph == nil || state.ScriptDocument == nil || state.ExecutableScriptBundle == nil {
		t.Fatalf("execution package missing required payloads: %+v", state)
	}
	if state.ExecutableScriptBundle.ScriptManifest.Runtime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 ||
		state.ExecutableScriptBundle.StageApprovalPlan == nil ||
		state.ExecutableScriptBundle.ScriptOutline == nil ||
		state.ExecutableScriptBundle.AgentPromptPolicy == nil ||
		state.ExecutableScriptBundle.Reproducibility.BundleHashSHA256 == "" {
		t.Fatalf("expected browser agent outline bundle and hashes: %+v", state.ExecutableScriptBundle)
	}
	for _, forbidden := range []string{"raw-password", "BEGIN PRIVATE KEY", ".env", "postgres://user:secret"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("response leaked forbidden value %q: %s", forbidden, payload)
		}
	}
}

func TestDevHTTPBridgeReadsLocalRepoSummary(t *testing.T) {
	server := newTestDevHTTPServer(t)
	repoPath := createDevBridgeFixtureRepo(t)
	body, err := json.Marshal(ExecutionPackageRequest{UserInput: &orchestrator.UserInput{
		Mode:               model.AppModeDesktop,
		ProductURL:         "https://app.example.com",
		LocalRepoPath:      repoPath,
		ProductDescription: "展示团队邀请流程",
		TargetAudience:     "中国运营团队",
		WebpageScreenshots: []model.WebpageScreenshotInput{verifiedActionScreenshotInput()},
	}})
	if err != nil {
		t.Fatal(err)
	}

	state := postExecutionPackage(t, server, body)

	if len(state.CodeSnapshots) == 0 {
		t.Fatal("expected code snapshots")
	}
	snapshot := state.CodeSnapshots[0]
	if snapshot.FileCount == 0 || snapshot.SourceDigestSHA256 == "" {
		t.Fatalf("expected scanned files and source digest: %+v", snapshot)
	}
	if !containsString(snapshot.Frameworks, "react") || len(snapshot.Selectors) == 0 || len(snapshot.Components) == 0 {
		t.Fatalf("expected framework, selector, and component summary: %+v", snapshot)
	}
	if state.ScriptDocument == nil || !scriptDocumentHasBusinessAction(state.ScriptDocument) {
		t.Fatalf("expected local repo selector evidence to produce a business action: %+v", state.ScriptDocument)
	}
	if _, err := buildClientExecutionPackageFromState(&state, "org_test", time.Now().UTC()); err != nil {
		t.Fatalf("expected executable package to be uploadable with repo-derived action: %v", err)
	}
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "function submitPayment") {
		t.Fatalf("response leaked full source: %s", payload)
	}
}

func TestDevHTTPBridgeReadsGitHubRepoURLAsParallelCodeSource(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git executable is required for git repository bridge test")
	}
	server := newTestDevHTTPServer(t)
	gitRepoPath := createDevBridgeFixtureRepo(t)
	initGitFixtureRepo(t, gitRepoPath)
	localRepoPath := createDevBridgeFixtureRepo(t)
	body, err := json.Marshal(ExecutionPackageRequest{UserInput: &orchestrator.UserInput{
		Mode:               model.AppModeDesktop,
		ProductURL:         "https://app.example.com",
		GitRepoURL:         "file://" + filepath.ToSlash(gitRepoPath),
		LocalRepoPath:      localRepoPath,
		ProductDescription: "展示团队邀请流程",
		TargetAudience:     "中国运营团队",
		WebpageScreenshots: []model.WebpageScreenshotInput{verifiedActionScreenshotInput()},
	}})
	if err != nil {
		t.Fatal(err)
	}

	state := postExecutionPackage(t, server, body)

	if len(state.CodeSnapshots) < 2 {
		t.Fatalf("expected local and GitHub code snapshots, got %+v", state.CodeSnapshots)
	}
	if state.ProjectContext.GitRepoURL == "" || state.ProjectContext.LocalRepoPath == "" {
		t.Fatalf("project context should preserve parallel repo inputs: %+v", state.ProjectContext)
	}
	var gitSnapshot *model.CodeUnderstandingSnapshot
	for i := range state.CodeSnapshots {
		if strings.HasPrefix(state.CodeSnapshots[i].URI, "file://") {
			gitSnapshot = &state.CodeSnapshots[i]
			break
		}
	}
	if gitSnapshot == nil || gitSnapshot.FileCount == 0 || gitSnapshot.CommitSHA == "" {
		t.Fatalf("expected GitHub-style repo URL snapshot to be cloned and scanned: %+v", state.CodeSnapshots)
	}
	if !containsString(gitSnapshot.Frameworks, "react") || len(gitSnapshot.Selectors) == 0 {
		t.Fatalf("expected GitHub-style snapshot to include repo structure evidence: %+v", gitSnapshot)
	}
}

func TestDevHTTPBridgeGitHubCredentialOnlyReturnsConfigurationState(t *testing.T) {
	server := newTestDevHTTPServer(t)
	const token = "github_pat_test_value_that_must_never_be_returned"
	storedToken := ""
	configured := false
	server.storeGitHubToken = func(value string) error {
		storedToken = value
		configured = true
		return nil
	}
	server.githubTokenConfigured = func() bool { return configured }
	server.deleteGitHubToken = func() error {
		storedToken = ""
		configured = false
		return nil
	}

	assertResponse := func(method, path, body string, wantConfigured bool) {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
		}
		payload := response.Body.String()
		if strings.Contains(payload, token) || strings.Contains(payload, "token\"") {
			t.Fatalf("credential response exposed token material: %s", payload)
		}
		if !strings.Contains(payload, `"configured":`+strconv.FormatBool(wantConfigured)) {
			t.Fatalf("unexpected credential status response: %s", payload)
		}
	}

	assertResponse(http.MethodGet, "/v1/desktop/github-credential", "", false)
	assertResponse(http.MethodPost, "/v1/desktop/github-credential", `{"token":"`+token+`"}`, true)
	if storedToken != token {
		t.Fatal("credential store did not receive the submitted token")
	}
	assertResponse(http.MethodDelete, "/v1/desktop/github-credential", "", false)
	if storedToken != "" {
		t.Fatal("credential delete did not clear the stored token")
	}
}

func TestDevHTTPBridgeRejectsForeignOriginBeforeCredentialMutation(t *testing.T) {
	server := newTestDevHTTPServer(t)
	called := false
	server.storeGitHubToken = func(string) error {
		called = true
		return nil
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/desktop/github-credential", strings.NewReader(`{"token":"github_pat_csrf"}`))
	request.Header.Set("Origin", "https://attacker.example")
	request.Header.Set("Content-Type", "text/plain")
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusForbidden || called {
		t.Fatalf("foreign origin must be rejected before credential mutation: status=%d called=%v", response.Code, called)
	}
}

func TestBuildClientExecutionPackageRedactsCredentialTextBeforePreflight(t *testing.T) {
	server := newTestDevHTTPServer(t)
	repoPath := createDevBridgeFixtureRepo(t)
	body, err := json.Marshal(ExecutionPackageRequest{UserInput: &orchestrator.UserInput{
		Mode:               model.AppModeDesktop,
		ProductURL:         "https://cascadeai.cn",
		LocalRepoPath:      repoPath,
		ProductDescription: "演示登录（10s，账号yikai.xu@cascadeai.co密码000000），然后新建项目。",
		TargetAudience:     "运营",
		WebpageScreenshots: []model.WebpageScreenshotInput{verifiedActionScreenshotInputForURL("https://cascadeai.cn")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	state := postExecutionPackage(t, server, body)

	build, err := buildClientExecutionPackageFromState(&state, "org_test", time.Now().UTC())
	if err != nil {
		t.Fatalf("expected credential text to be redacted before package preflight: %v", err)
	}
	payload, err := json.Marshal(build.Package)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, forbidden := range []string{"000000", "yikai.xu@cascadeai.co"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("client execution package leaked credential %q: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, "secret_ref:local-dev/demo_password") {
		t.Fatalf("expected package to preserve password secret_ref placeholder: %s", text)
	}
}

func TestBuildClientExecutionPackageUsesMinimalBrowserAgentOutlinePayload(t *testing.T) {
	server := newTestDevHTTPServer(t)
	body, err := json.Marshal(ExecutionPackageRequest{UserInput: &orchestrator.UserInput{
		Mode:               model.AppModeDesktop,
		ProductURL:         "https://cascadeai.cn",
		ProductDescription: "生成一套团队协作功能的产品演示执行包。",
		TargetAudience:     "内部产品团队",
		WebpageScreenshots: []model.WebpageScreenshotInput{verifiedActionScreenshotInputForURL("https://cascadeai.cn")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	state := postExecutionPackage(t, server, body)
	build, err := buildClientExecutionPackageFromState(&state, "org_test", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	bundle := build.Package.ExecutableScriptBundle
	if bundle.ScriptManifest.Runtime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 {
		t.Fatalf("expected outline runtime, got %s", bundle.ScriptManifest.Runtime)
	}
	if bundle.PlaywrightScript.InlineSource != "" {
		t.Fatal("outline upload package must not include Playwright inline source")
	}
	if bundle.UnderstandingDossier != nil {
		t.Fatal("outline upload package must not inline full project understanding dossier")
	}
	if bundle.PlanJSON.WorkflowGraph != nil {
		t.Fatal("outline upload package must not duplicate workflow_graph inside plan_json")
	}
	if bundle.BrowserAgentContract == nil || bundle.Reproducibility.BrowserAgentContractHashSHA256 == "" {
		t.Fatalf("outline upload package missing browser agent contract or hash: %+v", bundle.Reproducibility)
	}
	for _, step := range bundle.PlanJSON.Steps {
		if step.TargetContract == nil || step.TargetContract.SemanticID == "" {
			t.Fatalf("step missing target contract: %+v", step)
		}
		if browserAgentStepNeedsValidation(step) && !browserAgentStepHasRequiredValidation(step) {
			t.Fatalf("step missing required browser-agent validation: %+v", step)
		}
	}
	payload, err := json.Marshal(build.Package)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) > 512*1024 {
		t.Fatalf("outline upload package too large: %d bytes", len(payload))
	}
}

func TestPackageLeakageDetectionAllowsSafetyPolicyAPIKeyWords(t *testing.T) {
	pkg := &model.ClientExecutionPackage{
		PackageID:     "pkg_leakage_policy_words",
		OrgID:         "org_test",
		ProjectID:     "project_test",
		SchemaVersion: model.ClientExecutionPackageSchemaVersion,
		ExecutableScriptBundle: &model.ExecutableRecordingScriptBundle{
			ScriptOutline: &model.BrowserAgentScriptOutline{
				ForbiddenActions: []string{"api_key_read", "raw_secret_exfiltration"},
				AllowedExplorationScope: model.BrowserAgentExplorationScope{
					ForbiddenKeywords: []string{"api key", "token"},
				},
			},
		},
		SafetyReport: model.PackageSafetyReport{
			PIIHandling: "凭据密码只允许通过 secret_ref 注入，禁止展示口令相关内容。不要展示 .env 内容",
		},
	}
	if leakage := detectPackageLeakage(pkg); leakage != "" {
		t.Fatalf("safety policy words must not be treated as leaked secrets: %s", leakage)
	}
	pkg.Metadata = map[string]any{"bad_example": "sk-1234567890abcdef123456"}
	if leakage := detectPackageLeakage(pkg); leakage != "api key" {
		t.Fatalf("expected real sk-like token to be blocked, got %q", leakage)
	}
	pkg.Metadata = map[string]any{"bad_path": "/home/app/.env"}
	if leakage := detectPackageLeakage(pkg); leakage != ".env" {
		t.Fatalf("expected .env path to be blocked, got %q", leakage)
	}
	pkg.Metadata = map[string]any{"bad_password": "密码：000000"}
	if leakage := detectPackageLeakage(pkg); leakage != "raw password literal" {
		t.Fatalf("expected raw password literal to be blocked, got %q", leakage)
	}
}

func TestPackageSummariesUseRawRequirementScope(t *testing.T) {
	project := &model.ProjectContext{
		ID:                 "project_scope",
		SchemaVersion:      model.ProjectContextSchemaVersion,
		Mode:               model.AppModeDesktop,
		ProductURL:         "https://cascadeai.cn",
		TargetAudience:     "内部团队",
		ProductDescription: "演示登录（10s），新建项目（10s，俄罗斯方块，构建模式），agent实际构建演示（60s等待进入项目看实际发生了什么）。",
		Goals: []model.DemoGoal{{
			ID:               "goal_bad",
			ValueProposition: "graph can be approved",
			SuccessCriteria:  []string{"graph can be approved", "rehearsal pass rate is at least 90%"},
		}},
	}
	contextSummary := projectContextSummaryForPackage(project, &orchestrator.CascadeState{}, "")
	text := mustJSONForTest(t, contextSummary)
	if strings.Contains(text, "graph can be approved") || strings.Contains(text, "rehearsal pass rate") {
		t.Fatalf("project summary leaked model-expanded success criteria: %s", text)
	}

	productMap := &model.ProductMap{
		ID:      "map_scope",
		Version: 1,
		Summary: "Graph 审批机制以及排练通过率≥90%的质量保障。",
		Pages: []*model.ProductPage{{
			ID:      "page_dashboard",
			Actions: []string{"打开产品入口", "点击全部审批按钮", "点击取消计划按钮"},
			PrimaryActions: []model.UIActionRef{
				{ID: "build", Label: "build phase indicator", Kind: "inspect", Selector: "[data-testid='build-phase-indicator']"},
				{ID: "approve", Label: "button approve all", Kind: "click", Selector: "[data-testid='button-approve-all']"},
			},
		}},
		Features: []*model.Feature{{ID: "feature_bad", Name: "排练通过率与质量保障", UserValue: "rehearsal pass rate is at least 90%"}},
	}
	productSummary := productMapSummaryForPackage(project, productMap)
	text = mustJSONForTest(t, productSummary)
	for _, forbidden := range []string{"graph can be approved", "rehearsal pass rate", "排练通过率", "button-approve-all", "点击取消计划按钮"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("product summary leaked out-of-scope token %q: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, "build-phase-indicator") {
		t.Fatalf("product summary dropped in-scope build evidence: %s", text)
	}
}

func mustJSONForTest(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRedactClientExecutionPackageTextDoesNotCorruptGeneratedScript(t *testing.T) {
	pkg := sampleClientExecutionPackageForAppTest(t)
	pkg.ExecutableScriptBundle.PlaywrightScript.InlineSource += `
const passwordSelector = "input[type='password']";
const cascadeDemoPasswordSecretRef = "local-dev/demo_password";
`
	if err := redactClientExecutionPackageText(&pkg); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(payload) {
		t.Fatalf("redacted package must remain valid JSON: %s", payload)
	}
	source := pkg.ExecutableScriptBundle.PlaywrightScript.InlineSource
	if !strings.Contains(source, "passwordSelector") || !strings.Contains(source, "local-dev/demo_password") {
		t.Fatalf("script control-flow text should not be corrupted by credential redaction:\n%s", source)
	}
}

func TestDevHTTPBridgeInvalidLocalRepoPathDegradesWithoutFailing(t *testing.T) {
	server := newTestDevHTTPServer(t)
	body, err := json.Marshal(ExecutionPackageRequest{UserInput: &orchestrator.UserInput{
		Mode:               model.AppModeDesktop,
		ProductURL:         "https://app.example.com",
		LocalRepoPath:      filepath.Join(t.TempDir(), "missing"),
		ProductDescription: "展示团队邀请流程",
		TargetAudience:     "中国运营团队",
		WebpageScreenshots: []model.WebpageScreenshotInput{verifiedActionScreenshotInput()},
	}})
	if err != nil {
		t.Fatal(err)
	}

	state := postExecutionPackage(t, server, body)

	if state.ExecutableScriptBundle == nil {
		t.Fatal("expected execution package even when repo path is invalid")
	}
	if len(state.CodeSnapshots) == 0 || state.CodeSnapshots[0].FileCount != 0 {
		t.Fatalf("expected degraded code snapshot without scanned files: %+v", state.CodeSnapshots)
	}
}

func TestPrepareProductRunDoesNotRequireCloudExchange(t *testing.T) {
	server := newTestDevHTTPServer(t)
	body, err := json.Marshal(CloudLifecycleRequest{UserInput: &orchestrator.UserInput{
		Mode:               model.AppModeDesktop,
		ProductURL:         "https://app.example.com",
		ProductDescription: "演示新建项目并查看工作台。",
		TargetAudience:     "中国运营团队",
		WebpageScreenshots: []model.WebpageScreenshotInput{verifiedActionScreenshotInput()},
	}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/desktop/projects/local/product-run/prepare", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}
	var bridge BridgeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &bridge); err != nil {
		t.Fatal(err)
	}
	if !bridge.OK {
		t.Fatalf("prepare should not require cloud exchange: %s", bridge.Error)
	}
	var prepared ProductRunPrepareResult
	if err := json.Unmarshal(bridge.Data, &prepared); err != nil {
		t.Fatal(err)
	}
	if prepared.State == nil || prepared.Build == nil || prepared.Build.Package.PackageID == "" {
		t.Fatalf("prepare should return local state and build, got %+v", prepared)
	}
}

func TestDevHTTPBridgeExposesExecutionEvents(t *testing.T) {
	server := newTestDevHTTPServer(t)
	body, err := json.Marshal(ExecutionPackageRequest{UserInput: &orchestrator.UserInput{
		Mode:               model.AppModeDesktop,
		ProductURL:         "https://app.example.com",
		ProductDescription: "展示团队邀请流程",
		TargetAudience:     "中国运营团队",
		WebpageScreenshots: []model.WebpageScreenshotInput{verifiedActionScreenshotInput()},
	}})
	if err != nil {
		t.Fatal(err)
	}

	postExecutionPackage(t, server, body)

	request := httptest.NewRequest(http.MethodGet, "/v1/desktop/projects/local/execution-events", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}
	var bridge BridgeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &bridge); err != nil {
		t.Fatal(err)
	}
	var events []DevExecutionEvent
	if err := json.Unmarshal(bridge.Data, &events); err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("expected execution events")
	}
	payload := response.Body.String()
	if !strings.Contains(payload, "RequirementReaderAgent") || !strings.Contains(payload, "ScriptPackagerAgent") {
		t.Fatalf("events missing agent progress: %s", payload)
	}
	for _, forbidden := range []string{"raw-password", "BEGIN PRIVATE KEY", ".env", "postgres://user:secret"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("execution events leaked forbidden value %q: %s", forbidden, payload)
		}
	}
}

func TestDevHTTPBridgeRedactsLocalPathsInErrors(t *testing.T) {
	message := redactBridgeError(`open C:\Users\CascadeAI\AppData\Roaming\CascadeDemoOps\state.json.tmp: Access is denied.`)

	if strings.Contains(message, `C:\Users`) || strings.Contains(message, "AppData") {
		t.Fatalf("expected local path to be redacted: %s", message)
	}
	if !strings.Contains(message, "Access is denied") {
		t.Fatalf("expected error reason to remain: %s", message)
	}

	urlMessage := redactBridgeError(`页面预扫描完成，最终URL=https://cascadeai.cn/login 页面标题=Cascade AI。 open /home/ubuntu/.env: denied`)
	if !strings.Contains(urlMessage, "https://cascadeai.cn/login") {
		t.Fatalf("expected public URL to remain readable: %s", urlMessage)
	}
	if strings.Contains(urlMessage, "/home/ubuntu") || !strings.Contains(urlMessage, "[local_path]") {
		t.Fatalf("expected local unix path to be redacted: %s", urlMessage)
	}
}

func TestDevHTTPBridgeSanitizesLLMJSONErrors(t *testing.T) {
	message := redactBridgeError(`llm JSON parse failed: json: cannot unmarshal array into Go value of type agents.requirementLLMOutput`)

	if strings.Contains(message, "cannot unmarshal") || strings.Contains(message, "agents.requirementLLMOutput") {
		t.Fatalf("expected technical JSON parse detail to be hidden: %s", message)
	}
	if !strings.Contains(message, "模型返回的 JSON 结构不稳定") {
		t.Fatalf("expected user-facing LLM JSON error, got: %s", message)
	}
}

func TestBuildClientExecutionPackageBlocksObservationOnlyInput(t *testing.T) {
	server := newTestDevHTTPServer(t)
	body, err := json.Marshal(ExecutionPackageRequest{UserInput: &orchestrator.UserInput{
		Mode:               model.AppModeDesktop,
		ProductURL:         "https://app.example.com",
		ProductDescription: "只观察首页，不执行真实业务动作。",
		TargetAudience:     "中国运营团队",
	}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/desktop/projects/local/execution-package", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected observation-only input to block fake scripts, got status %d: %s", response.Code, response.Body.String())
	}
	var bridge BridgeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &bridge); err != nil {
		t.Fatal(err)
	}
	if bridge.OK || bridge.ErrorInfo == nil || bridge.ErrorInfo.Code != "missing_evidence" {
		t.Fatalf("expected missing_evidence bridge error, got %+v", bridge)
	}
	if !strings.Contains(bridge.Error, "页面预扫描") && !strings.Contains(bridge.Error, "verified") {
		t.Fatalf("expected actionable missing evidence message, got %q", bridge.Error)
	}
}

func verifiedActionScreenshotInput() model.WebpageScreenshotInput {
	return verifiedActionScreenshotInputForURL("https://app.example.com")
}

func verifiedActionScreenshotInputForURL(pageURL string) model.WebpageScreenshotInput {
	return model.WebpageScreenshotInput{
		ID:            "shot_verified_actions",
		URL:           pageURL,
		Title:         "工作台",
		PageRole:      "dashboard",
		Artifact:      model.ArtifactRef{ID: "artifact_verified_actions", URI: "file://redacted/verified.png", SHA256: "sha_verified"},
		OCRText:       "团队邀请成员 新建项目",
		VisionSummary: "页面包含团队邀请成员与新建项目入口。",
		CapturedAt:    time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
		Annotations: []model.ScreenshotAnnotation{
			{ID: "ann_invite_member", Kind: "click", Label: "团队邀请成员", SelectorHint: "[data-testid='invite-member']", FeatureRef: "feature_team_invite"},
			{ID: "ann_new_project", Kind: "click", Label: "新建项目", SelectorHint: "[data-testid='new-project']", FeatureRef: "feature_new_project"},
		},
	}
}

func TestNormalizeClientExecutionPackageRepairsRoundTripHashDrift(t *testing.T) {
	pkg := sampleClientExecutionPackageForAppTest(t)
	pkg.ExecutableScriptBundle.PlaywrightScript.InlineSource += "\n"
	if err := model.ValidateClientExecutionPackageForCloudExecution(&pkg); err == nil {
		t.Fatal("expected stale script hash to fail validation before normalization")
	}

	if err := normalizeClientExecutionPackageForUpload(&pkg); err != nil {
		t.Fatal(err)
	}

	scriptHash := model.SHA256Hex([]byte(pkg.ExecutableScriptBundle.PlaywrightScript.InlineSource))
	if pkg.ExecutableScriptBundle.PlaywrightScript.SHA256 != scriptHash {
		t.Fatalf("expected playwright script hash to be recomputed, got %s want %s", pkg.ExecutableScriptBundle.PlaywrightScript.SHA256, scriptHash)
	}
	if pkg.ExecutableScriptBundle.Reproducibility.ScriptHashSHA256 != scriptHash {
		t.Fatalf("expected reproducibility script hash to be recomputed, got %+v", pkg.ExecutableScriptBundle.Reproducibility)
	}
	if err := model.ValidateClientExecutionPackageForCloudExecution(&pkg); err != nil {
		t.Fatalf("expected normalized package to pass cloud validation: %v", err)
	}
}

func TestDevHTTPBridgeRuntimeHealthIsRedacted(t *testing.T) {
	server := newTestDevHTTPServer(t)
	request := httptest.NewRequest(http.MethodGet, "/v1/desktop/runtime-health", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}
	payload := response.Body.String()
	if strings.Contains(payload, "secret") || strings.Contains(payload, "postgres://user:secret") {
		t.Fatalf("runtime health leaked sensitive value: %s", payload)
	}
	if !strings.Contains(payload, "model_providers") {
		t.Fatalf("runtime health missing provider status: %s", payload)
	}
	if !strings.Contains(payload, "ark_media_mode") {
		t.Fatalf("runtime health missing ark media mode: %s", payload)
	}
	if !strings.Contains(payload, "app_capabilities") {
		t.Fatalf("runtime health missing app capabilities: %s", payload)
	}
	if !strings.Contains(payload, `"server_recording_required":true`) || !strings.Contains(payload, `"local_recording_execution":false`) {
		t.Fatalf("runtime health must declare server-side recording boundary: %s", payload)
	}
}

func TestDesktopProfileDoesNotExposeDevExchangeRunRoutes(t *testing.T) {
	t.Setenv(devExchangeHTTPEnv, "1")
	t.Setenv(devExchangeTokenEnv, "desktop-test-token")
	root := t.TempDir()
	service, err := NewService(config.AppRuntimeConfig{
		Profile:         config.ProfileDesktop,
		Environment:     "test",
		Mode:            model.AppModeDesktop,
		DatabaseDialect: config.DatabaseSQLite,
		SQLitePath:      filepath.Join(root, "cascade_demoops.db"),
		DataRoot:        root,
		ArtifactRoot:    filepath.Join(root, "artifacts"),
		CacheRoot:       filepath.Join(root, "cache"),
		LogRoot:         filepath.Join(root, "logs"),
		ResourceRoot:    root,
		DevRepoRoot:     root,
		SidecarPaths:    map[string]string{},
	}, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	server := NewDevHTTPServer(service)
	request := httptest.NewRequest(http.MethodPost, "/v1/dev/execution-packages/xpkg_local/run?org_id=org_test", nil)
	request.Header.Set("Authorization", "Bearer desktop-test-token")
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("desktop profile must not expose local dev recording run route, got status %d body=%s", response.Code, response.Body.String())
	}
}

func TestDesktopCloudEventProxyForwardsSessionAndResumeCursor(t *testing.T) {
	var authorization, orgID, lastEventID string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		orgID = r.Header.Get(cascadeOrgIDHeader)
		lastEventID = r.Header.Get("Last-Event-ID")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "id: xpkg_stream:3\nevent: stage\ndata: {\"event_id\":\"xpkg_stream:3\",\"stage\":\"recording\",\"status\":\"running\"}\n\n")
	}))
	defer upstream.Close()

	root := t.TempDir()
	service, err := NewService(config.AppRuntimeConfig{
		Profile:              config.ProfileDesktop,
		Environment:          "test",
		Mode:                 model.AppModeDesktop,
		DatabaseDialect:      config.DatabaseSQLite,
		SQLitePath:           filepath.Join(root, "cascade_demoops.db"),
		DataRoot:             root,
		ArtifactRoot:         filepath.Join(root, "artifacts"),
		CacheRoot:            filepath.Join(root, "cache"),
		LogRoot:              filepath.Join(root, "logs"),
		ResourceRoot:         root,
		DevRepoRoot:          root,
		SidecarPaths:         map[string]string{},
		CloudExchangeBaseURL: upstream.URL,
		CloudExchangeToken:   "stream-token",
	}, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/desktop/projects/project_stream/cloud/events?org_id=org_stream&exchange_package_id=xpkg_stream", nil)
	request.Header.Set("Last-Event-ID", "xpkg_stream:2")
	response := httptest.NewRecorder()

	NewDevHTTPServer(service).Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("unexpected proxy response status=%d content-type=%q body=%s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	if authorization != "Bearer stream-token" || orgID != "org_stream" || lastEventID != "xpkg_stream:2" {
		t.Fatalf("proxy headers authorization=%q org=%q last-event-id=%q", authorization, orgID, lastEventID)
	}
	if !strings.Contains(response.Body.String(), "id: xpkg_stream:3") || strings.Contains(response.Body.String(), "stream-token") {
		t.Fatalf("unexpected proxied stream: %s", response.Body.String())
	}
}

func TestDevHTTPBridgeModelDiagnosticsAreRedacted(t *testing.T) {
	server := newTestDevHTTPServer(t)
	request := httptest.NewRequest(http.MethodGet, "/v1/desktop/model-diagnostics", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}
	payload := response.Body.String()
	if strings.Contains(payload, "secret") || strings.Contains(payload, "postgres://user:secret") {
		t.Fatalf("model diagnostics leaked sensitive value: %s", payload)
	}
	if !strings.Contains(payload, "deterministic_mode") && !strings.Contains(payload, "route_not_configured") {
		t.Fatalf("model diagnostics should expose safe failure class: %s", payload)
	}
}

func TestCloudDoJSONAcceptsWrappedAndDirectResponses(t *testing.T) {
	for name, body := range map[string]string{
		"wrapped": `{"ok":true,"data":{"upload_id":"upload_wrapped","server_public_key_id":"kms_wrapped","cascade_execution_ips":["203.0.113.10"]}}`,
		"direct":  `{"upload_id":"upload_direct","server_public_key_id":"kms_direct","cascade_execution_ips":["203.0.113.11"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			defer upstream.Close()
			req, err := http.NewRequest(http.MethodGet, upstream.URL, nil)
			if err != nil {
				t.Fatal(err)
			}

			got, err := cloudDoJSON[model.ExecutionPackageInitResponse](upstream.Client(), req)
			if err != nil {
				t.Fatal(err)
			}
			if got.UploadID == "" || got.ServerPublicKeyID == "" {
				t.Fatalf("expected populated cloud response, got %+v", got)
			}
		})
	}
}

func TestCloudDoJSONReportsNonJSONResponses(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("404 page not found"))
	}))
	defer upstream.Close()
	req, err := http.NewRequest(http.MethodGet, upstream.URL, nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = cloudDoJSON[model.ExecutionPackageInitResponse](upstream.Client(), req)
	if err == nil {
		t.Fatal("expected non-JSON response error")
	}
	if !strings.Contains(err.Error(), "non-JSON response") || !strings.Contains(err.Error(), "404 page not found") {
		t.Fatalf("expected readable non-JSON error, got %v", err)
	}
}

func TestEnsureLocalDevAddressRejectsNonLocalBinds(t *testing.T) {
	if err := EnsureLocalDevAddress("127.0.0.1:4317"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureLocalDevAddress("localhost:4317"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureLocalDevAddress(":4317"); err == nil {
		t.Fatal("expected wildcard bind to be rejected")
	}
	if err := EnsureLocalDevAddress("0.0.0.0:4317"); err == nil {
		t.Fatal("expected non-local bind to be rejected")
	}
}

func newTestDevHTTPServer(t *testing.T) *DevHTTPServer {
	t.Helper()
	root := t.TempDir()
	service, err := NewService(config.AppRuntimeConfig{
		Profile:         config.ProfileDev,
		Environment:     "test",
		Mode:            model.AppModeDesktop,
		DatabaseDialect: config.DatabaseSQLite,
		SQLitePath:      filepath.Join(root, "cascade_demoops.db"),
		DataRoot:        root,
		ArtifactRoot:    filepath.Join(root, "artifacts"),
		CacheRoot:       filepath.Join(root, "cache"),
		LogRoot:         filepath.Join(root, "logs"),
		ResourceRoot:    root,
		DevRepoRoot:     root,
		SidecarPaths:    map[string]string{},
		DatabaseURL:     "postgres://user:secret@example/db",
		LLMMode:         config.LLMModeDeterministic,
		ModelTaskRoutes: map[config.ModelTask]config.ModelTaskRoute{
			config.ModelTaskPlanning: {
				Task:     config.ModelTaskPlanning,
				Provider: config.ModelProviderKimi,
				Model:    "kimi-k2.7-code",
			},
		},
	}, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	return NewDevHTTPServer(service)
}

func postExecutionPackage(t *testing.T, server *DevHTTPServer, body []byte) orchestrator.CascadeState {
	t.Helper()
	state, _ := postExecutionPackageWithPayload(t, server, body)
	return state
}

func postExecutionPackageWithPayload(t *testing.T, server *DevHTTPServer, body []byte) (orchestrator.CascadeState, string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/desktop/projects/local/execution-package", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}
	var bridge BridgeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &bridge); err != nil {
		t.Fatal(err)
	}
	if !bridge.OK {
		t.Fatalf("bridge error: %s", bridge.Error)
	}
	var state orchestrator.CascadeState
	if err := json.Unmarshal(bridge.Data, &state); err != nil {
		t.Fatal(err)
	}
	return state, response.Body.String()
}

func createDevBridgeFixtureRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	if err := os.MkdirAll(src, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"dependencies":{"react":"latest","vite":"latest"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	component := `import React from "react";
type InviteMember = { email: string; token?: string };
export function InvitePanel() {
  function submitPayment() { return fetch("/api/invite"); }
  return <button data-testid="invite-member" onClick={submitPayment}>Invite</button>;
}`
	if err := os.WriteFile(filepath.Join(src, "InvitePanel.tsx"), []byte(component), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func initGitFixtureRepo(t *testing.T, root string) {
	t.Helper()
	runGitFixtureCommand(t, root, "init")
	runGitFixtureCommand(t, root, "config", "user.email", "cascade-test@example.com")
	runGitFixtureCommand(t, root, "config", "user.name", "Cascade Test")
	runGitFixtureCommand(t, root, "add", ".")
	runGitFixtureCommand(t, root, "commit", "-m", "fixture")
}

func runGitFixtureCommand(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(output))
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
