package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/credentialstore"
	"cascade-demoops/backend/internal/driver"
	"cascade-demoops/backend/internal/model"
)

func TestPersistLocalVisibleResultPackageRetainsTraceabilityContract(t *testing.T) {
	result := model.RecordingResultPackage{
		ResultID:        "result_local_visible",
		SourcePackageID: "pkg_app_original",
		CloudJobID:      "run_local_visible",
		SchemaVersion:   model.RecordingResultPackageSchemaVersion,
		Status:          model.RecordingResultStatusFailed,
		StepResults: []model.StepResult{{
			NodeID: "node_failed", Status: "failed", ObservedState: "required target was not visible",
		}},
		ValidationReports: []model.ValidationReport{{
			SchemaVersion: model.ValidationReportSchemaVersion, ReportID: "validation_stage", RunID: "run_local_visible",
			SourcePackageID: "pkg_app_original", SourceBundleHashSHA256: "bundle_hash", PolicyHashSHA256: "policy_hash",
			Phase: model.ValidationPhaseRuntimeStage, NodeID: "node_failed", StageID: "stage_failed",
			Decision: model.ValidationDecisionStopAndReport, PassRate: 0, OverallConfidence: 1,
			EvidenceQuality: model.RuntimeObservationActualBrowser, CreatedAt: timeNowUTC(),
		}},
		StageEventLogRef: &model.ArtifactRef{ID: "events", Kind: "browser_agent_stage_event_log", URI: "file:///events.jsonl", SHA256: "event_hash"},
	}
	path, err := persistLocalVisibleResultPackage(t.TempDir(), result)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var persisted model.RecordingResultPackage
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.SourcePackageID != result.SourcePackageID || len(persisted.StepResults) != 1 || len(persisted.ValidationReports) != 1 || persisted.StageEventLogRef == nil || persisted.StageEventLogRef.SHA256 != "event_hash" {
		t.Fatalf("persisted local result lost traceability fields: %+v", persisted)
	}
}

func TestDevVisibleRealProductTestPackageStaysProtocolValidAndBounded(t *testing.T) {
	fixture := filepath.Join("..", "..", "..", "contracts", "exchange", "v1", "client_execution_package.browser_agent_outline.json")
	pkg, err := devVisibleRealProductTestPackage(fixture, "http://127.0.0.1:5000/app")
	if err != nil {
		t.Fatal(err)
	}
	if err := model.ValidateClientExecutionPackageForCloudExecution(&pkg); err != nil {
		t.Fatalf("fixed local test package must keep the protocol contract: %v", err)
	}
	encoded, err := json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}
	var roundTripped model.ClientExecutionPackage
	if err := json.Unmarshal(encoded, &roundTripped); err != nil {
		t.Fatal(err)
	}
	if err := model.ValidateClientExecutionPackageForCloudExecution(&roundTripped); err != nil {
		t.Fatalf("fixed local test package must survive the HTTP JSON boundary: %v", err)
	}
	if pkg.Metadata["dev_test_only"] != true || pkg.Metadata["not_for_exchange_upload"] != true {
		t.Fatalf("package must remain explicitly local test-only: %+v", pkg.Metadata)
	}
	if pkg.ExecutableScriptBundle == nil || pkg.ExecutableScriptBundle.ScriptOutline == nil {
		t.Fatal("package outline is missing")
	}
	outline := pkg.ExecutableScriptBundle.ScriptOutline
	if len(outline.Stages) != 3 || len(outline.AllowedExplorationScope.AllowedOrigins) != 1 || outline.AllowedExplorationScope.AllowedOrigins[0] != "http://127.0.0.1:5000" {
		t.Fatalf("unexpected fixed package scope: %+v", outline)
	}
	want := []string{"button-new-project", "input-project-idea", "button-create-project"}
	for index, stage := range outline.Stages {
		if len(stage.Interactions) != 1 || stage.Interactions[0].Target.TestID != want[index] {
			t.Fatalf("unexpected action at stage %d: %+v", index, stage)
		}
	}
}

func TestDevVisibleFixedExecuteRequestCannotCarryActions(t *testing.T) {
	var request DevVisibleBrowserAgentFixedExecuteRequest
	decoder := json.NewDecoder(strings.NewReader(`{"dev_test_ack":true,"actions":["delete"]}`))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err == nil {
		t.Fatal("fixed execute request must reject caller-supplied actions")
	}
}

func TestDevVisibleTargetURLAcceptsExplicitLoopbackURL(t *testing.T) {
	target, err := devVisibleTargetURL("http://127.0.0.1:5000/app")
	if err != nil {
		t.Fatal(err)
	}
	if devVisibleOrigin(target) != "http://127.0.0.1:5000" || target.Path != "/app" {
		t.Fatalf("unexpected target: %+v", target)
	}
}

func TestDevVisibleOpenRequestUsesCanonical2KEvidenceViewport(t *testing.T) {
	target, err := devVisibleTargetURL("http://127.0.0.1:5000/app")
	if err != nil {
		t.Fatal(err)
	}
	open := devVisibleBrowserAgentOpenRequest(
		"session-2k", t.TempDir(), target,
		DevVisibleBrowserAgentPrepareRequest{AutoLogin: true, MaskSelectors: []string{"[data-private]"}},
		true, false,
	)
	if open.Browser.Viewport.Width != 2560 || open.Browser.Viewport.Height != 1440 {
		t.Fatalf("visible acceptance must capture the canonical 2K 16:9 evidence master: %+v", open.Browser.Viewport)
	}
	if !open.Browser.RecordVideo || open.Browser.Headless {
		t.Fatalf("unexpected visible acceptance browser options: %+v", open.Browser)
	}
	if len(open.AllowedOrigins) != 1 || open.AllowedOrigins[0] != "http://127.0.0.1:5000" {
		t.Fatalf("visible acceptance origin scope changed: %+v", open.AllowedOrigins)
	}
}

func TestDevVisiblePostLoginRuntimePlanUsesManualCheckpointOnlyForFirstSessionStage(t *testing.T) {
	plan := BrowserAgentRuntimePlan{Stages: []BrowserAgentRuntimeStage{
		{ID: "stage_session", Order: 1, NodeID: "node_session", StageKind: model.BusinessStageKindSessionSetup, URL: "http://127.0.0.1:5000/app", Interactions: []model.BrowserAgentInteraction{{Kind: model.GraphActionFill}}},
		{ID: "stage_action", Order: 2, NodeID: "node_action", StageKind: model.BusinessStageKindBusinessAction, URL: "http://127.0.0.1:5000/app", Interactions: []model.BrowserAgentInteraction{{Kind: model.GraphActionClick}}},
	}}
	updated := devVisiblePostLoginRuntimePlan(plan, "http://127.0.0.1:5000/app")
	if !updated.Stages[0].ManualSessionCheckpoint || updated.Stages[1].ManualSessionCheckpoint {
		t.Fatalf("only the first post-login session stage may become a manual checkpoint: %+v", updated.Stages)
	}
	if updated.Stages[0].Interactions[0].Kind != model.GraphActionFill {
		t.Fatal("the App-approved interaction semantics must remain unchanged in the runtime copy")
	}
	notLoggedIn := devVisiblePostLoginRuntimePlan(plan, "http://127.0.0.1:5000/login")
	if notLoggedIn.Stages[0].ManualSessionCheckpoint {
		t.Fatal("a login-page session must never receive the post-login checkpoint")
	}
}

func TestDevVisiblePackageBindingRejectsAValidPackageForAnotherOrigin(t *testing.T) {
	var pkg model.ClientExecutionPackage
	if err := json.Unmarshal(readContractFixture(t, "client_execution_package.browser_agent_outline.json"), &pkg); err != nil {
		t.Fatal(err)
	}
	_, err := validateDevVisiblePackageBinding(pkg, "http://127.0.0.1:5000")
	if err == nil || !strings.Contains(err.Error(), "base_url does not match") {
		t.Fatalf("expected valid fixture to be rejected for a different visible origin, got %v", err)
	}
}

func TestDevVisibleOriginScopeRejectsExtraOrigin(t *testing.T) {
	if !visibleOriginsRestricted([]string{"http://127.0.0.1:5000"}, "http://127.0.0.1:5000") {
		t.Fatal("expected the exact visible origin to be accepted")
	}
	if visibleOriginsRestricted([]string{"http://127.0.0.1:5000", "http://127.0.0.1:5001"}, "http://127.0.0.1:5000") {
		t.Fatal("expected an additional origin to be rejected")
	}
}

func TestDevVisibleTargetURLRejectsNonLocalAndSensitiveURLs(t *testing.T) {
	for _, value := range []string{
		"https://127.0.0.1:5000/app",
		"http://localhost:5000/app",
		"http://127.0.0.1/app",
		"http://user:password@127.0.0.1:5000/app",
		"http://127.0.0.1:5000/app?token=not-allowed",
	} {
		if _, err := devVisibleTargetURL(value); err == nil {
			t.Fatalf("expected target to be rejected: %s", value)
		}
	}
}

func TestDevVisiblePrepareRequiresAcknowledgementBeforeWorkerStarts(t *testing.T) {
	server := newTestDevHTTPServer(t)
	request := httptest.NewRequest(http.MethodPost, "/v1/desktop/dev-visible-browser-agent/prepare", strings.NewReader(`{"target_url":"http://127.0.0.1:5000/app","dev_test_ack":false}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "dev_test_ack=true") {
		t.Fatalf("expected explicit local-test acknowledgement error, got %d: %s", response.Code, response.Body.String())
	}
}

func TestDevVisiblePrepareRejectsExternalTargetBeforeWorkerStarts(t *testing.T) {
	server := newTestDevHTTPServer(t)
	request := httptest.NewRequest(http.MethodPost, "/v1/desktop/dev-visible-browser-agent/prepare", strings.NewReader(`{"target_url":"https://example.com/app","dev_test_ack":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "127.0.0.1") {
		t.Fatalf("expected external target rejection, got %d: %s", response.Code, response.Body.String())
	}
}

func TestDevVisibleAutomaticLoginRequiresServerEnvironmentBeforeWorkerStarts(t *testing.T) {
	t.Setenv(devVisibleAutoLoginEmailEnv, "")
	t.Setenv(devVisibleAutoLoginPasswordEnv, "")
	server := newTestDevHTTPServer(t)
	request := httptest.NewRequest(http.MethodPost, "/v1/desktop/dev-visible-browser-agent/prepare", strings.NewReader(`{"target_url":"http://127.0.0.1:5000/app","auto_login":true,"dev_test_ack":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), devVisibleAutoLoginEmailEnv) || !strings.Contains(response.Body.String(), devVisibleAutoLoginPasswordEnv) {
		t.Fatalf("expected automatic-login environment error, got %d: %s", response.Code, response.Body.String())
	}
}

func TestDevVisibleAutomaticLoginResolvesOpaqueCredentialRef(t *testing.T) {
	manager := newDevVisibleBrowserAgentManager(nil)
	resolvedRef := ""
	manager.readDemoCredential = func(ref string) (credentialstore.DemoCredential, error) {
		resolvedRef = ref
		return credentialstore.DemoCredential{Username: "private-user", Password: "private-password"}, nil
	}

	credential, err := manager.resolveAutoLoginCredential("credential://demo/unattended-e2e")
	if err != nil {
		t.Fatal(err)
	}
	if resolvedRef != "unattended-e2e" || credential.Username != "private-user" || credential.Password != "private-password" {
		t.Fatalf("opaque credential ref was not resolved correctly: ref=%q credential=%+v", resolvedRef, credential)
	}
	if _, err := manager.resolveAutoLoginCredential("credential://demo/bad/ref"); err == nil {
		t.Fatal("malformed visible-browser credential_ref was accepted")
	}
}

func TestDevVisiblePrepareRejectsCredentialsInRequestBody(t *testing.T) {
	server := newTestDevHTTPServer(t)
	request := httptest.NewRequest(http.MethodPost, "/v1/desktop/dev-visible-browser-agent/prepare", strings.NewReader(`{"target_url":"http://127.0.0.1:5000/app","auto_login":true,"email":"user@example.com","password":"not-accepted","dev_test_ack":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest || !strings.Contains(strings.ToLower(response.Body.String()), "unknown field") {
		t.Fatalf("expected request credential fields to be rejected, got %d: %s", response.Code, response.Body.String())
	}
}

func TestMergeVisibleCloseArtifactsAddsRecordingTraceAndRecoveredEvidence(t *testing.T) {
	target := map[string]model.ArtifactRef{"stage": {ID: "stage", Kind: "screenshot"}}
	mergeVisibleCloseArtifacts(target, driver.BrowserAgentWorkerCloseResult{Artifacts: []model.ArtifactRef{
		{ID: "raw", Kind: "raw_recording", URI: "file:///raw.webm"},
		{ID: "trace", Kind: "browser_trace", URI: "file:///trace.zip"},
		{ID: "recovered", Kind: "screenshot", URI: "file:///recovered.png"},
	}})
	for _, id := range []string{"stage", "raw", "trace", "recovered"} {
		if _, ok := target[id]; !ok {
			t.Fatalf("close artifact %q was not merged: %+v", id, target)
		}
	}
}

func TestDevVisibleStatusRouteDoesNotOpenOrExecuteABrowser(t *testing.T) {
	server := newTestDevHTTPServer(t)
	request := httptest.NewRequest(http.MethodGet, "/v1/desktop/dev-visible-browser-agent/not-a-session", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "session was not found") {
		t.Fatalf("expected a read-only missing-session response, got %d: %s", response.Code, response.Body.String())
	}
}
