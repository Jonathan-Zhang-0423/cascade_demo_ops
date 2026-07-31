package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

func TestDevHTTPBridgeDemoCredentialReturnsOnlyOpaqueRef(t *testing.T) {
	server := newTestDevHTTPServer(t)
	stored := []string{}
	server.storeDemoCredential = func(ref, username, password string) error {
		stored = []string{ref, username, password}
		return nil
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/desktop/demo-credential", strings.NewReader(`{"ref":"assistant-demo","username":"demo-user","password":"private-password"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}
	payload := response.Body.String()
	if strings.Contains(payload, "demo-user") || strings.Contains(payload, "private-password") {
		t.Fatalf("credential response exposed secret material: %s", payload)
	}
	if !strings.Contains(payload, `"secretRef":"credential://demo/assistant-demo"`) || len(stored) != 3 || stored[2] != "private-password" {
		t.Fatalf("credential was not stored through the local vault adapter: payload=%s stored=%v", payload, stored)
	}
}

func TestDevHTTPAssistantAcceptsManualConfigurationProposal(t *testing.T) {
	server := newTestDevHTTPServer(t)
	create := httptest.NewRecorder()
	createRequest := httptest.NewRequest(http.MethodPost, "/v1/desktop/assistant/sessions", bytes.NewReader([]byte(`{"context":{"surface":"projects","scopeKey":"manual-http"}}`)))
	createRequest.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(create, createRequest)
	if create.Code != http.StatusOK {
		t.Fatalf("create assistant session status=%d body=%s", create.Code, create.Body.String())
	}
	var created BridgeResponse
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	var session model.AssistantSession
	if err := json.Unmarshal(created.Data, &session); err != nil {
		t.Fatal(err)
	}

	proposalBody := []byte(`{"baseVersion":1,"idempotencyKey":"manual-http-1","patch":{"projectName":"HTTP Manual","productURL":"https://manual.example","objective":"展示手动兜底","targetAudience":"产品团队","targetDurationSec":45,"mustShow":["审批流程"]}}`)
	proposal := httptest.NewRecorder()
	proposalRequest := httptest.NewRequest(http.MethodPost, "/v1/desktop/assistant/sessions/"+session.ID+"/configuration-proposals", bytes.NewReader(proposalBody))
	proposalRequest.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(proposal, proposalRequest)
	if proposal.Code != http.StatusOK {
		t.Fatalf("manual proposal status=%d body=%s", proposal.Code, proposal.Body.String())
	}
	var proposed BridgeResponse
	if err := json.Unmarshal(proposal.Body.Bytes(), &proposed); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(proposed.Data, &session); err != nil {
		t.Fatal(err)
	}
	if session.Configuration.ProjectName != "" || session.NextAction.Kind != string(model.AssistantProposalConfigurationPatch) || session.NextAction.ProposalID == "" {
		t.Fatalf("manual HTTP route bypassed or failed to create pending proposal: %+v", session)
	}
	last := session.Messages[len(session.Messages)-1]
	if last.GenerationSource != "manual" || len(last.Proposals) != 1 || last.Proposals[0].Patch == nil {
		t.Fatalf("manual HTTP proposal metadata missing: %+v", last)
	}
}

func TestDevHTTPPlanningModelSettingsNeverEchoAPIKey(t *testing.T) {
	server := newTestDevHTTPServer(t)
	keys := map[string]string{}
	server.service.storeModelKey = func(provider, key string) error { keys[provider] = key; return nil }
	server.service.readModelKey = func(provider string) (string, error) { return keys[provider], nil }
	server.service.deleteModelKey = func(provider string) error { delete(keys, provider); return nil }
	const secret = "http-model-secret"
	request := httptest.NewRequest(http.MethodPut, "/v1/desktop/planning-model", strings.NewReader(`{"provider":"kimi","model":"kimi-test","api_key":"`+secret+`"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("planning model settings status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), secret) || !strings.Contains(response.Body.String(), `"configured":true`) {
		t.Fatalf("planning model settings leaked key or omitted status: %s", response.Body.String())
	}
	if keys["kimi"] != secret {
		t.Fatal("planning model key did not reach secure store adapter")
	}
}

func TestDevHTTPPlanningModelVerifyUsesDedicatedSafeRoute(t *testing.T) {
	server := newTestDevHTTPServer(t)
	request := httptest.NewRequest(http.MethodPost, "/v1/desktop/planning-model/verify", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("planning model verify status=%d body=%s", response.Code, response.Body.String())
	}
	payload := response.Body.String()
	if !strings.Contains(payload, `"task":"planning"`) || strings.Contains(strings.ToLower(payload), "api_key") {
		t.Fatalf("planning verification response is unsafe or incomplete: %s", payload)
	}
}

func TestDevHTTPBridgeDemoCredentialRejectsUnsafeRef(t *testing.T) {
	server := newTestDevHTTPServer(t)
	called := false
	server.storeDemoCredential = func(string, string, string) error { called = true; return nil }
	request := httptest.NewRequest(http.MethodPost, "/v1/desktop/demo-credential", strings.NewReader(`{"ref":"../unsafe","username":"demo","password":"secret"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code == http.StatusOK || called {
		t.Fatalf("unsafe credential ref must be rejected: status=%d called=%v body=%s", response.Code, called, response.Body.String())
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
	if build.BuildStatus != "draft" || !build.Package.ApprovedAt.IsZero() || build.Package.SafetyReport.AllowedToUpload || build.Package.SafetyReport.HumanApproval.ApprovalID != "" || build.Envelope.EnvelopeID != "" {
		t.Fatalf("preview must remain an unapproved draft: %+v", build)
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
	if len(payload) > 256*1024 {
		t.Fatalf("outline upload package too large: %d bytes", len(payload))
	}
	if build.Package.ConfidenceSummary == nil || build.Package.ConfidenceSummary.AssessmentHash == "" || build.ApprovalSubjectDigestSHA256 == "" || build.SizeReport.TotalBytes == 0 {
		t.Fatalf("draft must expose confidence, approval digest, and size report: %+v", build)
	}
	if build.Package.WorkflowGraph.Intent == nil || build.Package.WorkflowGraph.Assets == nil {
		t.Fatalf("compact workflow graph lost director intent or assets: %+v", build.Package.WorkflowGraph)
	}
}

func TestBlockingMissingEvidenceCannotBeDowngradedToWarning(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	pkg.ExecutableScriptBundle.StageApprovalPlan.UncertaintyReport = []model.StageUncertainty{{ID: "missing_business", Kind: "missing_evidence", Summary: "关键业务证据缺失", Blocking: true}}
	findings := preflightBrowserAgentOutline(pkg.ExecutableScriptBundle)
	for _, finding := range findings {
		if finding.ID == "stage_uncertainty_missing_business" {
			if finding.Severity != model.FindingSeverityBlocking {
				t.Fatalf("blocking evidence uncertainty was downgraded: %+v", finding)
			}
			return
		}
	}
	t.Fatal("expected blocking uncertainty finding")
}

func TestBuildClientExecutionPackageRejectsInjectedCodeSelectorInPageOnlyMode(t *testing.T) {
	server := newTestDevHTTPServer(t)
	repoPath := createDevBridgeFixtureRepo(t)
	body, err := json.Marshal(ExecutionPackageRequest{UserInput: &orchestrator.UserInput{
		Mode: model.AppModeDesktop, ProductURL: "https://app.example.com", LocalRepoPath: repoPath,
		ProductDescription: "生成团队协作产品演示执行包。", TargetAudience: "产品团队",
		WebpageScreenshots: []model.WebpageScreenshotInput{verifiedActionScreenshotInputForURL("https://app.example.com")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	state := postExecutionPackage(t, server, body)
	if state.SourceBinding == nil || state.SourceBinding.EffectiveMode != model.ProductSourceModePageOnly {
		t.Fatalf("fixture must enter page-only mode: %+v", state.SourceBinding)
	}
	state.ExecutableScriptBundle.PlanJSON.Steps[0].Action.Target.Source = "code_reader"
	state.ExecutableScriptBundle.PlanJSON.Steps[0].Action.Target.Selector = "[data-testid='foreign-product']"
	build, err := buildClientExecutionPackageFromState(&state, "org_test", time.Now().UTC())
	if build.Envelope.EnvelopeID != "" || build.Package.PackageID != "" {
		t.Fatalf("blocked preflight must not return a package or envelope: %+v", build)
	}
	var preflight *packagePreflightError
	if !errors.As(err, &preflight) || len(preflight.Findings) == 0 || preflight.Findings[0].ID != "source_evidence_leakage" {
		t.Fatalf("expected source_evidence_leakage preflight, got %T: %v", err, err)
	}
}

func TestCompactPrepareResponseRedactsLocalSourcePaths(t *testing.T) {
	root := filepath.Join(`C:\Users\Alice\private`, "customer-project")
	documentPath := filepath.Join(`C:\Users\Alice\private`, "requirements.md")
	state := &orchestrator.CascadeState{ProjectContext: &model.ProjectContext{
		ID: "project-path-redaction", Mode: model.AppModeDesktop, ProductURL: "https://product.example",
		LocalRepoPath: root,
		Inputs: &model.ProjectInputBundle{
			Repositories:         []model.RepositoryInput{{LocalPath: root, Provider: "local", ReadOnly: true}},
			RequirementDocuments: []model.RequirementDocumentInput{{ID: "requirements", Kind: "local_file", LocalPath: documentPath}},
		},
	}}

	compact := compactStateForPrepareResponse(state, nil)
	payload, err := json.Marshal(compact)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(payload)
	if strings.Contains(serialized, `C:\Users\Alice`) || strings.Contains(serialized, "private") {
		t.Fatalf("prepare response leaked an absolute local path: %s", serialized)
	}
	if compact.ProjectContext.LocalRepoPath != "customer-project" || compact.ProjectContext.Inputs.Repositories[0].LocalPath != "customer-project" {
		t.Fatalf("prepare response should retain only safe basenames: %+v", compact.ProjectContext)
	}
	if compact.ProjectContext.Inputs.RequirementDocuments[0].LocalPath != "" {
		t.Fatalf("requirement document path must be omitted: %+v", compact.ProjectContext.Inputs.RequirementDocuments[0])
	}
}

func TestSourceBindingHTTPGetAndStaleDecision(t *testing.T) {
	server := newTestDevHTTPServer(t)
	repoPath := createDevBridgeFixtureRepo(t)
	body, err := json.Marshal(ExecutionPackageRequest{UserInput: &orchestrator.UserInput{
		ProjectID: "project-source-http", Mode: model.AppModeDesktop, ProductURL: "https://app.example.com", LocalRepoPath: repoPath,
		ProductDescription: "生成团队协作产品演示执行包。", TargetAudience: "产品团队",
		WebpageScreenshots: []model.WebpageScreenshotInput{verifiedActionScreenshotInputForURL("https://app.example.com")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	state := postExecutionPackage(t, server, body)

	request := httptest.NewRequest(http.MethodGet, "/v1/desktop/projects/"+state.ProjectID+"/source-binding", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), state.SourceBinding.AssessmentHash) {
		t.Fatalf("GET source-binding did not return current assessment: status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/desktop/projects/"+state.ProjectID+"/source-binding/decisions", strings.NewReader(`{"decision":"continue_page_only","assessment_hash":"stale","idempotency_key":"decision-http-1"}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"source_binding_stale"`) {
		t.Fatalf("stale source-binding decision did not return structured error: status=%d body=%s", response.Code, response.Body.String())
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

func TestPrepareProductRunReadsMatchedLocalProjectAndStopsAtDraftApproval(t *testing.T) {
	server := newTestDevHTTPServer(t)
	repoPath := createDevBridgeFixtureRepo(t)
	if err := os.WriteFile(filepath.Join(repoPath, "package.json"), []byte(`{"name":"app-example","homepage":"https://app.example.com","dependencies":{"react":"latest","vite":"latest"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(CloudLifecycleRequest{UserInput: &orchestrator.UserInput{
		Mode:               model.AppModeDesktop,
		ProductURL:         "https://app.example.com",
		LocalRepoPath:      repoPath,
		ProductDescription: "展示团队邀请流程。",
		TargetAudience:     "中国运营团队",
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
		t.Fatalf("local prepare failed: %s", bridge.Error)
	}
	var prepared ProductRunPrepareResult
	if err := json.Unmarshal(bridge.Data, &prepared); err != nil {
		t.Fatal(err)
	}
	if prepared.State == nil || prepared.State.SourceBinding == nil || prepared.Build == nil {
		t.Fatalf("local prepare did not return source binding and package draft: %+v", prepared)
	}
	if prepared.State.SourceBinding.Status != model.ProductSourceBindingMatched || prepared.State.SourceBinding.EffectiveMode != model.ProductSourceModeMixed {
		t.Fatalf("expected matched local source evidence, got %+v", prepared.State.SourceBinding)
	}
	build := prepared.Build
	if build.BuildStatus != "draft" || build.ApprovalSubjectDigestSHA256 == "" || build.Package.ConfidenceSummary == nil {
		t.Fatalf("expected confidence-scored local draft: %+v", build)
	}
	if !build.Package.ApprovedAt.IsZero() || build.Package.SafetyReport.AllowedToUpload || build.Package.SafetyReport.HumanApproval.ApprovalID != "" || build.Envelope.EnvelopeID != "" {
		t.Fatalf("local configuration must stop before independent upload approval: %+v", build)
	}
	bundle := build.Package.ExecutableScriptBundle
	if bundle == nil || bundle.StageApprovalPlan == nil || bundle.ScriptOutline == nil || bundle.ApprovalMarkdown.InlineMarkdown == "" {
		t.Fatalf("local prepare did not generate the three-in-one package: %+v", bundle)
	}
	payload := response.Body.String()
	if strings.Contains(payload, repoPath) || strings.Contains(payload, "function submitPayment") {
		t.Fatalf("local prepare response leaked an absolute path or complete source: %s", payload)
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

func TestDevHTTPServesOnlyVerifiedDownloadedReviewVideo(t *testing.T) {
	server := newTestDevHTTPServer(t)
	root := filepath.Join(server.service.runtime.ArtifactRoot, "desktop", "downloads", "project_media", "result_media")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	videoPath := filepath.Join(root, "demo.mp4")
	if err := os.WriteFile(videoPath, []byte("verified-video"), 0o600); err != nil {
		t.Fatal(err)
	}
	videoHash := sha256.Sum256([]byte("verified-video"))
	if err := writeVerifiedDownloadMarker(videoPath, hex.EncodeToString(videoHash[:])); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/desktop/projects/project_media/cloud/deliverable/media?result_package_id=result_media&file=demo.mp4", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "verified-video" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("verified media was not served safely: status=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}

	for _, rawURL := range []string{
		"/v1/desktop/projects/project_media/cloud/deliverable/media?result_package_id=result_media&file=..%2Fsecret.mp4",
		"/v1/desktop/projects/project_media/cloud/deliverable/media?result_package_id=result_media&file=demo.mp4.part",
		"/v1/desktop/projects/project_media/cloud/deliverable/media?result_package_id=result_media&file=notes.json",
	} {
		blocked := httptest.NewRecorder()
		server.Handler().ServeHTTP(blocked, httptest.NewRequest(http.MethodGet, rawURL, nil))
		if blocked.Code == http.StatusOK {
			t.Fatalf("unsafe media reference was served: %s", rawURL)
		}
	}
}

func TestDevHTTPBrowserLocalSourceRegistrationIsLoopbackDevOnly(t *testing.T) {
	server := newTestDevHTTPServerWithEnvironment(t, "development")
	repoPath := createDevBridgeFixtureRepo(t)
	body, err := json.Marshal(devLocalSourceRequest{Kind: "local_repository", Path: repoPath, DevTestAck: true})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/desktop/dev/local-sources", bytes.NewReader(body))
	request.RemoteAddr = "127.0.0.1:54321"
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
	var ref LocalSourceRef
	if err := json.Unmarshal(bridge.Data, &ref); err != nil {
		t.Fatal(err)
	}
	if ref.Ref == "" || ref.Kind != "local_repository" || ref.Label == "" || ref.Path != "" {
		t.Fatalf("unexpected public local-source ref: %+v", ref)
	}
	resolved, ok := server.service.ResolveLocalSourceRef(ref.Ref)
	if !ok || resolved.Path != repoPath {
		t.Fatalf("expected private path to remain server-side: %+v ok=%t", resolved, ok)
	}
	if strings.Contains(response.Body.String(), repoPath) {
		t.Fatalf("response leaked absolute local path: %s", response.Body.String())
	}
}

func TestDevHTTPBrowserLocalSourceRegistrationRejectsUnsafeContexts(t *testing.T) {
	repoPath := createDevBridgeFixtureRepo(t)
	tests := []struct {
		name       string
		server     *DevHTTPServer
		remoteAddr string
		ack        bool
	}{
		{name: "non loopback", server: newTestDevHTTPServerWithEnvironment(t, "development"), remoteAddr: "192.0.2.10:54321", ack: true},
		{name: "missing acknowledgement", server: newTestDevHTTPServerWithEnvironment(t, "development"), remoteAddr: "127.0.0.1:54321", ack: false},
		{name: "non development environment", server: newTestDevHTTPServerWithEnvironment(t, "test"), remoteAddr: "127.0.0.1:54321", ack: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := json.Marshal(devLocalSourceRequest{Kind: "local_repository", Path: repoPath, DevTestAck: test.ack})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/v1/desktop/dev/local-sources", bytes.NewReader(body))
			request.RemoteAddr = test.remoteAddr
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			test.server.Handler().ServeHTTP(response, request)
			if response.Code == http.StatusOK {
				t.Fatalf("expected rejection: %s", response.Body.String())
			}
		})
	}
}

func newTestDevHTTPServer(t *testing.T) *DevHTTPServer {
	return newTestDevHTTPServerWithEnvironment(t, "test")
}

func newTestDevHTTPServerWithEnvironment(t *testing.T, environment string) *DevHTTPServer {
	t.Helper()
	root := t.TempDir()
	service, err := NewService(config.AppRuntimeConfig{
		Profile:         config.ProfileDev,
		Environment:     environment,
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
		ModelProviders: map[config.ModelProvider]config.ModelProviderCredential{
			config.ModelProviderKimi: {
				Provider:     config.ModelProviderKimi,
				BaseURL:      "https://api.moonshot.cn/v1",
				DefaultModel: "kimi-k2.7-code",
				Enabled:      true,
			},
		},
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
