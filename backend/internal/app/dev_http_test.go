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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/credentialstore"
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

func TestDevHTTPBridgeClientPackagePreflightIsLocalAndDigestBound(t *testing.T) {
	server := newTestDevHTTPServer(t)
	input := orchestrator.UserInput{
		ProjectID:          "local-preflight-project",
		Mode:               model.AppModeDesktop,
		ProductURL:         "https://app.example.com",
		ProductDescription: "展示新建项目流程",
		TargetAudience:     "中国运营团队",
		WebpageScreenshots: []model.WebpageScreenshotInput{verifiedActionScreenshotInput()},
	}
	body, err := json.Marshal(ExecutionPackageRequest{UserInput: &input})
	if err != nil {
		t.Fatal(err)
	}
	state := postExecutionPackage(t, server, body)
	build, err := server.service.BuildClientExecutionPackage(t.Context(), state.ProjectID, defaultDesktopOrgID)
	if err != nil {
		t.Fatal(err)
	}
	requestBody, err := json.Marshal(ClientExecutionPackagePreflightRequest{
		OrgID: defaultDesktopOrgID, PackageDigestSHA256: build.PackageDigestSHA256,
		ApprovalSubjectDigestSHA256: build.ApprovalSubjectDigestSHA256,
		ConfidenceAssessmentHash:    build.Package.ConfidenceSummary.AssessmentHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/desktop/projects/"+url.PathEscape(state.ProjectID)+"/client-execution-package/preflight", bytes.NewReader(requestBody))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected preflight status %d: %s", response.Code, response.Body.String())
	}
	var bridge BridgeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &bridge); err != nil {
		t.Fatal(err)
	}
	if !bridge.OK {
		t.Fatalf("local preflight failed: %s", bridge.Error)
	}
	var result CloudPackagePreflightResult
	if err := json.Unmarshal(bridge.Data, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Valid || result.BuildStatus != "draft" || result.PackageDigestSHA256 != build.PackageDigestSHA256 || result.ApprovalSubjectDigestSHA256 != build.ApprovalSubjectDigestSHA256 {
		t.Fatalf("unexpected local preflight result: %+v", result)
	}
}

func TestDevHTTPBridgeReadsLocalRepoSummary(t *testing.T) {
	server := newTestDevHTTPServer(t)
	repoPath := createDevBridgeFixtureRepo(t)
	body, err := json.Marshal(ExecutionPackageRequest{UserInput: &orchestrator.UserInput{
		Mode:               model.AppModeDesktop,
		ProductURL:         "https://app.example.com",
		LocalRepoPath:      repoPath,
		ProductDescription: "展示新建项目流程",
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

func TestDevHTTPBridgeUsesEphemeralCredentialFallbackOnlyInLocalDevelopment(t *testing.T) {
	server := newTestDevHTTPServerWithEnvironment(t, "development")
	server.storeDemoCredential = func(string, string, string) error {
		return errors.New("interactive credential store is unavailable")
	}
	server.readDemoCredential = func(string) (credentialstore.DemoCredential, error) {
		return credentialstore.DemoCredential{}, errors.New("persistent credential is unavailable")
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/desktop/demo-credential", strings.NewReader(`{"ref":"ephemeral-e2e","username":"private-user","password":"private-password"}`))
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = "127.0.0.1:51234"
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"storage":"dev_ephemeral_memory"`) {
		t.Fatalf("local development fallback was not used: status=%d body=%s", response.Code, response.Body.String())
	}
	credential, err := server.readDevExecutionCredential("credential://demo/ephemeral-e2e")
	if err != nil {
		t.Fatal(err)
	}
	if credential.Username != "private-user" || credential.Password != "private-password" {
		t.Fatalf("ephemeral credential was not retained in process memory: %+v", credential)
	}
}

func TestDevHTTPExecutionPackageResolvesOnlyOpaqueCredentialRef(t *testing.T) {
	server := newTestDevHTTPServer(t)
	resolvedRef := ""
	server.readDemoCredential = func(ref string) (credentialstore.DemoCredential, error) {
		resolvedRef = ref
		return credentialstore.DemoCredential{Username: "private-user", Password: "private-password"}, nil
	}
	body := `{"credential_ref":"credential://demo/unattended-e2e","user_input":{"mode":"desktop"}}`
	request := httptest.NewRequest(http.MethodPost, "/v1/desktop/projects/auto/execution-package", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if resolvedRef != "unattended-e2e" {
		t.Fatalf("opaque credential ref was not resolved: %q", resolvedRef)
	}
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid empty project input to fail after credential resolution, got %d: %s", response.Code, response.Body.String())
	}
	serialized := response.Body.String()
	if strings.Contains(serialized, "private-user") || strings.Contains(serialized, "private-password") {
		t.Fatalf("execution package response leaked resolved credential material: %s", serialized)
	}
}

func TestDevHTTPExecutionPackageRejectsMalformedCredentialRef(t *testing.T) {
	server := newTestDevHTTPServer(t)
	for _, ref := range []string{"demo/unattended", "credential://demo/", "credential://demo/bad/ref"} {
		if _, err := server.readDevExecutionCredential(ref); err == nil {
			t.Fatalf("malformed credential ref %q was accepted", ref)
		}
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

func TestDevHTTPAssistantV2CompletesIndependentActionsAndRejectsStaleDigest(t *testing.T) {
	t.Setenv("CASCADE_AGENT_ACTIONS_V2", "true")
	server := newTestDevHTTPServer(t)
	handler := server.Handler()

	create := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/desktop/assistant/sessions", strings.NewReader(`{"context":{"surface":"projects","scopeKey":"actions-v2-http"}}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(create, request)
	session := decodeBridgeAssistantSession(t, create)

	turn := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/v1/desktop/assistant/sessions/"+session.ID+"/turns", strings.NewReader(`{"message":"选择本地项目目录，并存储演示账号","idempotencyKey":"turn-actions-v2"}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(turn, request)
	session = decodeBridgeAssistantSession(t, turn)
	sourceAction := findAssistantActionBySpec(t, session, "source.select_local")
	registered, err := server.service.RegisterLocalSource("local_repository", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	completeBody, err := json.Marshal(model.AgentActionCompleteRequest{
		DependencyDigest: sourceAction.DependencyDigest,
		IdempotencyKey:   "complete-source-v2",
		SelectedSources:  []model.ConfigurationSourceRef{{Ref: registered.Ref, Kind: "local_repository", Label: registered.Label}},
	})
	if err != nil {
		t.Fatal(err)
	}
	complete := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/v1/desktop/assistant/sessions/"+session.ID+"/actions/"+sourceAction.ID+"/complete", bytes.NewReader(completeBody))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(complete, request)
	session = decodeBridgeAssistantSession(t, complete)
	credentialAction := findAssistantActionBySpec(t, session, "credential.store_demo")
	if credentialAction.Status != "available" {
		t.Fatalf("independent credential action became stale after source completion: %+v", credentialAction)
	}

	missingDigest := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/v1/desktop/assistant/sessions/"+session.ID+"/actions/"+credentialAction.ID+"/complete", strings.NewReader(`{"idempotencyKey":"complete-credential-without-digest","credentialRefs":["credential://demo/fixture"]}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(missingDigest, request)
	if missingDigest.Code != http.StatusBadRequest || !strings.Contains(missingDigest.Body.String(), "dependency digest is required") {
		t.Fatalf("missing dependency digest was not rejected: status=%d body=%s", missingDigest.Code, missingDigest.Body.String())
	}

	staleBody, err := json.Marshal(model.AgentActionCompleteRequest{DependencyDigest: "sha256:stale", IdempotencyKey: "complete-credential-stale", CredentialRefs: []string{"credential://demo/fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	stale := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/v1/desktop/assistant/sessions/"+session.ID+"/actions/"+credentialAction.ID+"/complete", bytes.NewReader(staleBody))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(stale, request)
	if stale.Code != http.StatusBadRequest || !strings.Contains(stale.Body.String(), "dependency is stale") {
		t.Fatalf("stale dependency digest was not rejected: status=%d body=%s", stale.Code, stale.Body.String())
	}
}

func TestDevHTTPAssistantV2ConfirmsSafeActionBatch(t *testing.T) {
	t.Setenv("CASCADE_AGENT_ACTIONS_V2", "true")
	server := newTestDevHTTPServer(t)
	handler := server.Handler()

	create := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/desktop/assistant/sessions", strings.NewReader(`{"context":{"surface":"projects","scopeKey":"batch-v2-http"}}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(create, request)
	session := decodeBridgeAssistantSession(t, create)

	proposalBody := []byte(`{"baseVersion":1,"idempotencyKey":"batch-proposal","patch":{"projectName":"Batch Fixture","productURL":"https://example.com","objective":"展示安全批次","mustShow":["打开工作台"]}}`)
	proposal := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/v1/desktop/assistant/sessions/"+session.ID+"/configuration-proposals", bytes.NewReader(proposalBody))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(proposal, request)
	session = decodeBridgeAssistantSession(t, proposal)
	if len(session.ActionBatches) == 0 || session.IntentPlan == nil {
		t.Fatalf("V2 session did not project proposal into an action batch: %+v", session)
	}
	batch := session.ActionBatches[len(session.ActionBatches)-1]
	confirmBody, err := json.Marshal(model.AgentActionBatchConfirmRequest{BaseIntentDigest: session.IntentPlan.Digest, IdempotencyKey: "confirm-batch-v2"})
	if err != nil {
		t.Fatal(err)
	}
	confirmed := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/v1/desktop/assistant/sessions/"+session.ID+"/batches/"+batch.ID+"/confirm", bytes.NewReader(confirmBody))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(confirmed, request)
	session = decodeBridgeAssistantSession(t, confirmed)
	if session.Configuration.ProjectName != "Batch Fixture" {
		t.Fatalf("safe configuration batch was not applied: %+v", session.Configuration)
	}
}

func decodeBridgeAssistantSession(t *testing.T, response *httptest.ResponseRecorder) model.AssistantSession {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("assistant request status=%d body=%s", response.Code, response.Body.String())
	}
	var bridge BridgeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &bridge); err != nil {
		t.Fatal(err)
	}
	var session model.AssistantSession
	if err := json.Unmarshal(bridge.Data, &session); err != nil {
		t.Fatal(err)
	}
	return session
}

func findAssistantActionBySpec(t *testing.T, session model.AssistantSession, specID string) model.AgentAction {
	t.Helper()
	for _, action := range session.Actions {
		if action.SpecID == specID {
			return action
		}
	}
	t.Fatalf("assistant action %s not found: %+v", specID, session.Actions)
	return model.AgentAction{}
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
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	server := newTestDevHTTPServerWithRepository(t, repoRoot)
	server.readDemoCredential = func(ref string) (credentialstore.DemoCredential, error) {
		if ref != "dev-http-redaction" {
			return credentialstore.DemoCredential{}, errors.New("credential not found")
		}
		return credentialstore.DemoCredential{Username: "demo.user@example.test", Password: "FixtureOnly987"}, nil
	}
	product := newAuthenticatedWorkspaceTestServer(t)
	body, err := json.Marshal(ExecutionPackageRequest{UserInput: &orchestrator.UserInput{
		Mode:               model.AppModeDesktop,
		ProductURL:         product.URL,
		ProductDescription: "演示登录（10s，账号demo.user@example.test密码FixtureOnly987），然后新建项目。",
		TargetAudience:     "运营",
		AllowedDomains:     []string{"127.0.0.1"},
		DemoUsername:       "demo.user@example.test",
		DemoPassword:       "FixtureOnly987",
		DemoCredentialRef:  "credential://demo/dev-http-redaction",
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
	for _, forbidden := range []string{"FixtureOnly987", "demo.user@example.test"} {
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

func TestBrowserAgentUploadViewCompactsInvestigationTraceAndRedundantRoutes(t *testing.T) {
	questions := []model.InvestigationQuestionRef{
		{
			ID:              "question_result",
			IntentLabel:     strings.Repeat("result ", 20),
			Status:          "answered",
			EvidenceSummary: strings.Repeat("confirmed source evidence ", 20),
			RemainingGaps:   []string{"first gap", "second gap", "third gap"},
			NextActions:     []model.CodeInvestigationNextAction{{Tool: "code_search", Reason: strings.Repeat("detail ", 40)}},
			ToolCallIDs:     []string{"tool_1", "tool_2"},
			Confidence:      0.93,
		},
	}
	routes := []model.BrowserAgentRouteCandidate{{Route: "/project/:id", EvidenceRefs: []model.EvidenceRef{{ID: "ev_route", Summary: strings.Repeat("route ", 50)}}}}
	plan := &model.StageApprovalPlan{Stages: []model.StageApprovalStage{{
		EntryRoute: "/project/:id", CandidateRoutes: routes, InvestigationQuestionRefs: questions,
	}}}
	outline := &model.BrowserAgentScriptOutline{Stages: []model.BrowserAgentOutlineStage{{
		Route: "/project/:id", CandidateRoutes: routes, InvestigationQuestionRefs: questions,
	}}}

	compactStageApprovalPlanForUpload(plan)
	compactBrowserAgentOutlineForUpload(outline)

	for name, refs := range map[string][]model.InvestigationQuestionRef{
		"stage plan": plan.Stages[0].InvestigationQuestionRefs,
		"outline":    outline.Stages[0].InvestigationQuestionRefs,
	} {
		if len(refs) != 1 || refs[0].ID != "question_result" || refs[0].Status != "answered" || refs[0].Confidence != 0.93 {
			t.Fatalf("%s lost compact investigation identity: %+v", name, refs)
		}
		if refs[0].NextActions != nil || refs[0].ToolCallIDs != nil || len(refs[0].RemainingGaps) != 1 || len([]rune(refs[0].EvidenceSummary)) > 100 {
			t.Fatalf("%s retained oversized local investigation trace: %+v", name, refs[0])
		}
	}
	if plan.Stages[0].CandidateRoutes != nil || outline.Stages[0].CandidateRoutes != nil {
		t.Fatalf("fixed routes must not duplicate candidate routes in upload view: plan=%+v outline=%+v", plan.Stages[0].CandidateRoutes, outline.Stages[0].CandidateRoutes)
	}
}

func TestCompactScriptStepPromotesMatchingFormalValidationSelector(t *testing.T) {
	observedAt := time.Now().UTC()
	candidate := model.SelectorCandidate{
		Kind: "testid", Value: "preview-iframe", EvidenceID: "ev_preview", SourceKind: "source_scan",
		SourceDigest: "sha256:preview", ObservedRole: "iframe", ObservedAccessibleName: "Preview",
		ObservedAt: &observedAt, EvidenceRefs: []model.EvidenceRef{{ID: "ev_preview", Kind: model.EvidenceKindSourceCode}},
	}
	step := model.ScriptStep{
		Action: model.ScriptActionInstruction{Type: model.GraphActionPress, Target: model.ActionTarget{
			Selector: "[data-testid='preview-iframe']", TestID: "preview-iframe",
		}},
		Validations: []model.ValidationSpec{{Required: true, Target: model.ActionTarget{SelectorAlternatives: []model.SelectorCandidate{candidate}}}},
	}

	compactScriptStepForUpload(&step)

	if len(step.Action.Target.SelectorAlternatives) != 1 || step.Action.Target.SelectorAlternatives[0].EvidenceID != "ev_preview" {
		t.Fatalf("matching formal validation selector was not promoted to the action target: %+v", step.Action.Target.SelectorAlternatives)
	}
}

func TestBrowserAgentPreflightAcceptsPlayableAndKeyboardValidations(t *testing.T) {
	tests := []model.ScriptStep{
		{
			NodeID: "business_stage_playable_preview", StageKind: model.BusinessStageKindFinalObserve,
			Action:      model.ScriptActionInstruction{Type: model.GraphActionInspect},
			Validations: []model.ValidationSpec{{Kind: "playable_surface_visible", Expected: true, Required: true}},
		},
		{
			NodeID: "business_stage_verify_playable_controls", StageKind: model.BusinessStageKindFinalObserve,
			Action:      model.ScriptActionInstruction{Type: model.GraphActionPress},
			Validations: []model.ValidationSpec{{Kind: "page_changed", Expected: true, Required: true}},
		},
	}
	for _, step := range tests {
		if !browserAgentStepNeedsValidation(step) || !browserAgentStepHasRequiredValidation(step) {
			t.Fatalf("playable browser-agent step must pass required-validation preflight: %+v", step)
		}
	}
}

func TestBuildClientExecutionPackagePreviewDigestIsStable(t *testing.T) {
	server := newTestDevHTTPServer(t)
	body, err := json.Marshal(ExecutionPackageRequest{UserInput: &orchestrator.UserInput{
		Mode: model.AppModeDesktop, ProductURL: "https://app.example.com",
		ProductDescription: "创建项目并启动构建。", TargetAudience: "普通用户",
		WebpageScreenshots: []model.WebpageScreenshotInput{verifiedActionScreenshotInputForURL("https://app.example.com")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	state := postExecutionPackage(t, server, body)
	first, err := buildClientExecutionPackageFromState(&state, "org_test", time.Unix(100, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildClientExecutionPackageFromState(&state, "org_test", time.Unix(200, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if first.PackageDigestSHA256 != second.PackageDigestSHA256 || first.ApprovalSubjectDigestSHA256 != second.ApprovalSubjectDigestSHA256 {
		t.Fatalf("unchanged preview digests must be stable: first=%+v second=%+v", first, second)
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

func TestPageOnlyPackageRetainsRequirementsAndStripsSourceExecutionEvidence(t *testing.T) {
	server := newTestDevHTTPServer(t)
	repoPath := createDevBridgeFixtureRepo(t)
	body, err := json.Marshal(ExecutionPackageRequest{UserInput: &orchestrator.UserInput{
		Mode: model.AppModeDesktop, ProductURL: "https://app.example.com", LocalRepoPath: repoPath,
		ProductDescription: "展示新建项目并进入工作台。", TargetAudience: "普通用户",
		MustShow: []string{"新建项目", "工作台状态"}, MustNotShow: []string{"API Key"},
		ForbiddenPages: []string{"/billing"}, ForbiddenData: []string{"客户邮箱"},
		WebpageScreenshots: []model.WebpageScreenshotInput{verifiedActionScreenshotInputForURL("https://app.example.com")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	state := postExecutionPackage(t, server, body)
	if state.SourceBinding == nil || state.SourceBinding.EffectiveMode != model.ProductSourceModePageOnly {
		t.Fatalf("fixture must enter page-only mode: %+v", state.SourceBinding)
	}
	if state.ProjectContext == nil || state.ProjectContext.Inputs == nil || len(state.ProjectContext.Inputs.Requirements) != 5 {
		t.Fatalf("structured requirements were not retained in page-only state: %+v", state.ProjectContext)
	}
	build, err := buildClientExecutionPackageFromState(&state, "org_test", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if build.Package.WorkflowGraph == nil || len(build.Package.WorkflowGraph.Requirements) != 5 {
		t.Fatalf("page-only package lost structured requirements: %+v", build.Package.WorkflowGraph)
	}
	if model.ClientPackageContainsSourceDerivedExecutionEvidence(&build.Package) {
		t.Fatalf("page-only package retained source-derived execution evidence at %v", packageSourceEvidenceLocations(&build.Package))
	}
	for _, requirement := range build.Package.WorkflowGraph.Requirements {
		if requirement.Kind != "must_show" && requirement.Required {
			t.Fatalf("negative safety constraint was counted as positive coverage: %+v", requirement)
		}
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
	pkg.Metadata = map[string]any{"bad_password": "密码：FixtureOnly987"}
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
		ProductDescription: "展示新建项目流程。",
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

func TestBridgeErrorDoesNotMisclassifyRequestJSONAsLLMFailure(t *testing.T) {
	err := errors.New("json: cannot unmarshal string into Go struct field DevAppPackageRawWaiverRequest.approved_blocking_reason_hashes of type []string")
	info := bridgeErrorInfo(err)
	if info.Code != "bad_request" || info.Retryable {
		t.Fatalf("request JSON shape error must not be reported as an LLM failure: %+v", info)
	}
	if strings.Contains(info.Message, "模型返回") || !strings.Contains(info.Message, "cannot unmarshal") {
		t.Fatalf("request JSON error should preserve an actionable decoder message: %q", info.Message)
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

func TestDesktopProfileRejectsLegacyExchangeProjectRoutes(t *testing.T) {
	root := t.TempDir()
	service, err := NewService(config.AppRuntimeConfig{
		Profile: config.ProfileDesktop, Environment: "production", Mode: model.AppModeDesktop,
		DatabaseDialect: config.DatabaseSQLite, SQLitePath: filepath.Join(root, "cascade_demoops.db"),
		DataRoot: root, ArtifactRoot: filepath.Join(root, "artifacts"), CacheRoot: filepath.Join(root, "cache"),
		LogRoot: filepath.Join(root, "logs"), ResourceRoot: root, DevRepoRoot: root, SidecarPaths: map[string]string{},
	}, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	server := NewDevHTTPServer(service)
	request := httptest.NewRequest(http.MethodPost, "/v1/desktop/projects/project_legacy/cloud/init", strings.NewReader(`{"risk_confirmed":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("legacy exchange rejection should use bridge response, got %d: %s", response.Code, response.Body.String())
	}
	var bridge BridgeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &bridge); err != nil {
		t.Fatal(err)
	}
	if bridge.OK || !strings.Contains(bridge.Error, "legacy_exchange_disabled") {
		t.Fatalf("legacy exchange route was not rejected: %+v", bridge)
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

func TestDevHTTPBridgeModelReadinessIsRedactedAndUsesRequiredRoutes(t *testing.T) {
	server := newTestDevHTTPServer(t)
	request := httptest.NewRequest(http.MethodGet, "/v1/desktop/model-readiness", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}
	payload := response.Body.String()
	if strings.Contains(payload, "secret") || strings.Contains(payload, "postgres://user:secret") || strings.Contains(payload, "api_key") {
		t.Fatalf("model readiness leaked sensitive value: %s", payload)
	}
	for _, task := range []string{"planning", "code_reading", "multimodal_understanding", "video_operation"} {
		if !strings.Contains(payload, task) {
			t.Fatalf("model readiness omitted required task %q: %s", task, payload)
		}
	}
	if !strings.Contains(payload, "cascade.model_readiness.v1") {
		t.Fatalf("model readiness schema missing: %s", payload)
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

func TestServerAcceptancePageIsVisibleOnlyToLocalDevClients(t *testing.T) {
	tests := []struct {
		name        string
		environment string
		remoteAddr  string
		wantStatus  int
	}{
		{name: "local development", environment: "development", remoteAddr: "127.0.0.1:54321", wantStatus: http.StatusOK},
		{name: "local test", environment: "test", remoteAddr: "[::1]:54321", wantStatus: http.StatusOK},
		{name: "production", environment: "production", remoteAddr: "127.0.0.1:54321", wantStatus: http.StatusForbidden},
		{name: "non loopback", environment: "development", remoteAddr: "192.0.2.10:54321", wantStatus: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/dev/server-acceptance", nil)
			request.RemoteAddr = test.remoteAddr
			response := httptest.NewRecorder()
			newTestDevHTTPServerWithEnvironment(t, test.environment).Handler().ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("unexpected status %d, want %d: %s", response.Code, test.wantStatus, response.Body.String())
			}
			if test.wantStatus == http.StatusOK {
				body := response.Body.String()
				if !strings.Contains(body, "Server 执行验收") || !strings.Contains(body, "不能作为真实 App → Server 联调通过依据") || !strings.Contains(body, "真实 App 包验收") {
					t.Fatalf("acceptance page is missing its title or controlled-test warning: %s", body)
				}
				if response.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("acceptance page must not be cached: %v", response.Header())
				}
				if !strings.Contains(response.Header().Get("Content-Security-Policy"), "default-src 'self'") {
					t.Fatalf("acceptance page is missing its content security policy: %v", response.Header())
				}
			}
		})
	}
}

func TestServerAcceptanceAPIRejectsNonLocalClients(t *testing.T) {
	tests := []struct {
		name        string
		environment string
		remoteAddr  string
	}{
		{name: "production", environment: "production", remoteAddr: "127.0.0.1:54321"},
		{name: "non loopback", environment: "development", remoteAddr: "192.0.2.10:54321"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/v1/desktop/browser-agent-acceptance", nil)
			request.RemoteAddr = test.remoteAddr
			response := httptest.NewRecorder()
			newTestDevHTTPServerWithEnvironment(t, test.environment).Handler().ServeHTTP(response, request)
			if response.Code == http.StatusOK {
				t.Fatalf("unsafe client reached acceptance API: %s", response.Body.String())
			}
			var bridge BridgeResponse
			if err := json.Unmarshal(response.Body.Bytes(), &bridge); err != nil {
				t.Fatal(err)
			}
			if bridge.OK || !strings.Contains(bridge.Error, "server acceptance") {
				t.Fatalf("unexpected rejection response: %+v", bridge)
			}
		})
	}
}

func newTestDevHTTPServer(t *testing.T) *DevHTTPServer {
	return newTestDevHTTPServerWithEnvironment(t, "test")
}

func newTestDevHTTPServerWithEnvironment(t *testing.T, environment string) *DevHTTPServer {
	return newTestDevHTTPServerWithEnvironmentAndRepository(t, environment, "")
}

func newTestDevHTTPServerWithRepository(t *testing.T, repoRoot string) *DevHTTPServer {
	return newTestDevHTTPServerWithEnvironmentAndRepository(t, "test", repoRoot)
}

func newTestDevHTTPServerWithEnvironmentAndRepository(t *testing.T, environment, repoRoot string) *DevHTTPServer {
	t.Helper()
	root := t.TempDir()
	if strings.TrimSpace(repoRoot) == "" {
		repoRoot = root
	}
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
		DevRepoRoot:     repoRoot,
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
