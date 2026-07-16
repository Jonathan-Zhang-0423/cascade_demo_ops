package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
