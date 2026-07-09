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
	if state.ExecutableScriptBundle.PlaywrightScript.InlineSource == "" || state.ExecutableScriptBundle.Reproducibility.BundleHashSHA256 == "" {
		t.Fatalf("expected executable script source and hashes: %+v", state.ExecutableScriptBundle)
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
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "function submitPayment") {
		t.Fatalf("response leaked full source: %s", payload)
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
