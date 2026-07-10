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

	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/store"
)

func TestDevExchangeHTTPRoutesAreDisabledByDefault(t *testing.T) {
	server := newTestDevHTTPServer(t)
	body := []byte(`{"org_id":"org_1","project_id":"project_1","package_kind":"client_execution"}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/execution-packages/init", bytes.NewReader(body))
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("dev exchange routes should be disabled by default, got %d: %s", response.Code, response.Body.String())
	}
}

func TestDevExchangeHTTPRequiresBearerToken(t *testing.T) {
	t.Setenv(devExchangeHTTPEnv, "1")
	t.Setenv(devExchangeTokenEnv, "test-token")
	server := newTestDevHTTPServer(t)
	body := []byte(`{"org_id":"org_1","project_id":"project_1","package_kind":"client_execution"}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/execution-packages/init", bytes.NewReader(body))
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized without bearer token, got %d: %s", response.Code, response.Body.String())
	}
}

func TestDevExchangeHTTPLifecycleAcceptsProtocolPackage(t *testing.T) {
	t.Setenv(devExchangeHTTPEnv, "1")
	t.Setenv(devExchangeTokenEnv, "test-token")
	server := newTestDevHTTPServer(t)
	now := time.Date(2026, 7, 9, 15, 30, 0, 0, time.UTC)
	server.service.exchange.now = fixedClock(now)
	pkg := sampleClientExecutionPackageForAppTest(t)

	initPayload := exchangeHTTPDo[model.ExecutionPackageInitResponse](t, server, http.MethodPost, "/v1/execution-packages/init", model.ExecutionPackageInitRequest{
		OrgID:       pkg.OrgID,
		ProjectID:   pkg.ProjectID,
		PackageKind: model.ExchangePackageKindClientExecution,
	})
	if initPayload.UploadID == "" || len(initPayload.CascadeExecutionIPs) == 0 {
		t.Fatalf("unexpected init response: %+v", initPayload)
	}

	envelope := sampleEnvelopeForAppTest(t, pkg, now)
	uploadPayload := exchangeHTTPDo[model.ExecutionPackageUploadResponse](t, server, http.MethodPost, "/v1/execution-packages", exchangeUploadHTTPBody{
		UploadID:   initPayload.UploadID,
		Envelope:   envelope,
		PayloadRef: envelope.PayloadRef,
		Payload:    pkg,
	})
	if uploadPayload.ExchangePackageID == "" || uploadPayload.Status != model.ExchangePackageStatusAccepted {
		t.Fatalf("unexpected upload response: %+v", uploadPayload)
	}

	status := exchangeHTTPDo[model.ExecutionPackageStatusResponse](t, server, http.MethodGet, "/v1/execution-packages/"+uploadPayload.ExchangePackageID+"/status?org_id="+pkg.OrgID, nil)
	if status.Status != model.ExchangePackageStatusAccepted || status.CloudJobID != uploadPayload.CloudJobID {
		t.Fatalf("unexpected package status: %+v", status)
	}
	if status.Stage != "accepted" || status.ProgressPercent != 10 || status.Message == "" {
		t.Fatalf("status should include dev progress metadata: %+v", status)
	}
	if len(status.StageHistory) != 1 || status.StageHistory[0].Stage != "accepted" {
		t.Fatalf("status should include stage history: %+v", status.StageHistory)
	}

	debug := exchangeHTTPDo[ExecutionPackageDebugView](t, server, http.MethodGet, "/v1/dev/execution-packages/"+uploadPayload.ExchangePackageID+"/debug?org_id="+pkg.OrgID, nil)
	if debug.Package.PackageID != pkg.PackageID || debug.Package.ScriptStepCount != 1 || debug.Package.ScriptRuntime == "" {
		t.Fatalf("unexpected debug package summary: %+v", debug.Package)
	}
	if debug.Runtime.ArtifactRoot == "" || debug.Status.ExchangePackageID != uploadPayload.ExchangePackageID {
		t.Fatalf("unexpected debug runtime/status: %+v", debug)
	}
}

func TestDevExchangeHTTPAcceptsEncryptedPayloadRefOnlyUpload(t *testing.T) {
	t.Setenv(devExchangeHTTPEnv, "1")
	t.Setenv(devExchangeTokenEnv, "test-token")
	server := newTestDevHTTPServer(t)
	now := time.Date(2026, 7, 9, 15, 35, 0, 0, time.UTC)
	server.service.exchange.now = fixedClock(now)
	pkg := sampleClientExecutionPackageForAppTest(t)
	initPayload := exchangeHTTPDo[model.ExecutionPackageInitResponse](t, server, http.MethodPost, "/v1/execution-packages/init", model.ExecutionPackageInitRequest{
		OrgID:       pkg.OrgID,
		ProjectID:   pkg.ProjectID,
		PackageKind: model.ExchangePackageKindClientExecution,
	})
	envelope := sampleEnvelopeForAppTest(t, pkg, now)
	envelope.Crypto.Nonce = "nonce_http_payload_ref_only"
	envelope.PayloadRef.Kind = model.PayloadRefKindArtifact
	envelope.PayloadRef.ArtifactID = "payload_artifact_1"
	envelope.PayloadRef.URI = "s3://cascade-exchange/payload.enc"
	envelope.PayloadRef.InlineCiphertext = ""
	envelope.PayloadRef.SHA256 = "ciphertext_hash_1"
	envelope.PayloadRef.SizeBytes = 2048
	envelope.PayloadRef.Encrypted = true
	envelope.PayloadRef.Sensitive = true
	envelope.Crypto.CiphertextDigestSHA256 = envelope.PayloadRef.SHA256

	uploadPayload := exchangeHTTPDo[model.ExecutionPackageUploadResponse](t, server, http.MethodPost, "/v1/execution-packages", exchangeUploadHTTPBody{
		UploadID:   initPayload.UploadID,
		Envelope:   envelope,
		PayloadRef: envelope.PayloadRef,
	})
	if uploadPayload.ExchangePackageID == "" || uploadPayload.Status != model.ExchangePackageStatusAccepted {
		t.Fatalf("unexpected payload-ref upload response: %+v", uploadPayload)
	}
	status := exchangeHTTPDo[model.ExecutionPackageStatusResponse](t, server, http.MethodGet, "/v1/execution-packages/"+uploadPayload.ExchangePackageID+"/status?org_id="+pkg.OrgID, nil)
	if status.Stage != "accepted" || !strings.Contains(status.Message, "Encrypted execution package accepted") {
		t.Fatalf("expected encrypted upload status message, got %+v", status)
	}
}

func TestDevExchangeHTTPRunEndpointMarksMissingWorkerAsFailed(t *testing.T) {
	t.Setenv(devExchangeHTTPEnv, "1")
	t.Setenv(devExchangeTokenEnv, "test-token")
	server := newTestDevHTTPServer(t)
	now := time.Date(2026, 7, 9, 15, 45, 0, 0, time.UTC)
	server.service.exchange.now = fixedClock(now)
	pkg := sampleClientExecutionPackageForAppTest(t)
	initPayload := exchangeHTTPDo[model.ExecutionPackageInitResponse](t, server, http.MethodPost, "/v1/execution-packages/init", model.ExecutionPackageInitRequest{
		OrgID:       pkg.OrgID,
		ProjectID:   pkg.ProjectID,
		PackageKind: model.ExchangePackageKindClientExecution,
	})
	envelope := sampleEnvelopeForAppTest(t, pkg, now)
	envelope.Crypto.Nonce = "nonce_run_endpoint"
	uploadPayload := exchangeHTTPDo[model.ExecutionPackageUploadResponse](t, server, http.MethodPost, "/v1/execution-packages", exchangeUploadHTTPBody{
		UploadID:   initPayload.UploadID,
		Envelope:   envelope,
		PayloadRef: envelope.PayloadRef,
		Payload:    pkg,
	})

	runStatus := exchangeHTTPDo[model.ExecutionPackageStatusResponse](t, server, http.MethodPost, "/v1/dev/execution-packages/"+uploadPayload.ExchangePackageID+"/run", nil, cascadeOrgIDHeader, pkg.OrgID)
	if runStatus.Status != model.ExchangePackageStatusRunning || runStatus.Stage != "validated" {
		t.Fatalf("expected run endpoint to return started running status, got %+v", runStatus)
	}

	secondRun := exchangeHTTPDo[model.ExecutionPackageStatusResponse](t, server, http.MethodPost, "/v1/dev/execution-packages/"+uploadPayload.ExchangePackageID+"/run", nil, cascadeOrgIDHeader, pkg.OrgID)
	if secondRun.Status != model.ExchangePackageStatusRunning && secondRun.Status != model.ExchangePackageStatusFailed {
		t.Fatalf("duplicate run should return current status without restarting, got %+v", secondRun)
	}

	failed := waitForExchangeHTTPStatus(t, server, uploadPayload.ExchangePackageID, pkg.OrgID, model.ExchangePackageStatusFailed)
	if failed.Error == nil || failed.Error.Code != "video_worker_missing" {
		t.Fatalf("expected missing test worker to fail dev run, got %+v", failed)
	}
	if failed.Stage != "failed" || failed.ProgressPercent != 100 || failed.Message == "" {
		t.Fatalf("failed dev run should include progress metadata, got %+v", failed)
	}
	if failed.FailureSummary == nil || failed.FailureSummary.FailedStage != "preparing_worker" {
		t.Fatalf("failed dev run should report failed stage, got %+v", failed.FailureSummary)
	}
	if len(failed.StageHistory) < 3 {
		t.Fatalf("failed dev run should include stage history, got %+v", failed.StageHistory)
	}
}

func TestDevExchangeHTTPStatusProvidesDownloadURLsAndServesDeliverable(t *testing.T) {
	t.Setenv(devExchangeHTTPEnv, "1")
	t.Setenv(devExchangeTokenEnv, "test-token")
	server := newTestDevHTTPServer(t)
	now := time.Date(2026, 7, 9, 16, 0, 0, 0, time.UTC)
	server.service.exchange.now = fixedClock(now)
	pkg := sampleClientExecutionPackageForAppTest(t)
	initPayload := exchangeHTTPDo[model.ExecutionPackageInitResponse](t, server, http.MethodPost, "/v1/execution-packages/init", model.ExecutionPackageInitRequest{
		OrgID:       pkg.OrgID,
		ProjectID:   pkg.ProjectID,
		PackageKind: model.ExchangePackageKindClientExecution,
	})
	envelope := sampleEnvelopeForAppTest(t, pkg, now)
	envelope.Crypto.Nonce = "nonce_download_endpoint"
	uploadPayload := exchangeHTTPDo[model.ExecutionPackageUploadResponse](t, server, http.MethodPost, "/v1/execution-packages", exchangeUploadHTTPBody{
		UploadID: initPayload.UploadID,
		Envelope: envelope,
		Payload:  pkg,
	})
	if _, _, err := server.service.exchange.StartExecution(t.Context(), pkg.OrgID, uploadPayload.ExchangePackageID); err != nil {
		t.Fatal(err)
	}

	artifactPath := filepath.Join(server.service.runtime.ArtifactRoot, "exchange", "xpkg_test", "render", "demo.webm")
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, []byte("demo-video"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := sampleRecordingResultForAppTest(pkg)
	result.GeneratedAssets = []model.ArtifactRef{{
		ID:       "artifact_demo_video",
		Kind:     "demo_video",
		URI:      artifactPath,
		MimeType: "video/webm",
		Metadata: map[string]any{"asset_role": "final_demo", "include_in_demo": true},
	}}
	result.ExecutionTrace.Artifacts = nil
	completed, err := server.service.CompleteExecutionPackageWithResult(t.Context(), pkg.OrgID, uploadPayload.ExchangePackageID, result)
	if err != nil {
		t.Fatal(err)
	}

	status := exchangeHTTPDo[model.ExecutionPackageStatusResponse](t, server, http.MethodGet, "/v1/execution-packages/"+uploadPayload.ExchangePackageID+"/status?org_id="+pkg.OrgID, nil)
	deliverable := findDeliverable(status.ResultSummary.Deliverables, "demo_video")
	if deliverable == nil || deliverable.DownloadURL == "" {
		t.Fatalf("expected downloadable demo deliverable, got %+v", status.ResultSummary.Deliverables)
	}
	if !strings.Contains(deliverable.DownloadURL, completed.ResultPackageID) || !strings.Contains(deliverable.DownloadURL, "artifact_demo_video") {
		t.Fatalf("download_url should include result and artifact identity: %+v", deliverable)
	}

	request := httptest.NewRequest(http.MethodGet, deliverable.DownloadURL+"?org_id="+pkg.OrgID, nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected download status %d: %s", response.Code, response.Body.String())
	}
	if got := response.Body.String(); got != "demo-video" {
		t.Fatalf("unexpected downloaded body %q", got)
	}
	if response.Header().Get("X-Cascade-Artifact-ID") != "artifact_demo_video" {
		t.Fatalf("missing artifact header: %+v", response.Header())
	}
}

func TestDevExchangeHTTPRestoresStatusAndDownloadAfterRestart(t *testing.T) {
	t.Setenv(devExchangeHTTPEnv, "1")
	t.Setenv(devExchangeTokenEnv, "test-token")
	server := newTestDevHTTPServer(t)
	now := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	server.service.exchange.now = fixedClock(now)
	pkg := sampleClientExecutionPackageForAppTest(t)
	initPayload := exchangeHTTPDo[model.ExecutionPackageInitResponse](t, server, http.MethodPost, "/v1/execution-packages/init", model.ExecutionPackageInitRequest{
		OrgID:       pkg.OrgID,
		ProjectID:   pkg.ProjectID,
		PackageKind: model.ExchangePackageKindClientExecution,
	})
	envelope := sampleEnvelopeForAppTest(t, pkg, now)
	envelope.Crypto.Nonce = "nonce_http_restart"
	uploadPayload := exchangeHTTPDo[model.ExecutionPackageUploadResponse](t, server, http.MethodPost, "/v1/execution-packages", exchangeUploadHTTPBody{
		UploadID: initPayload.UploadID,
		Envelope: envelope,
		Payload:  pkg,
	})
	if _, _, err := server.service.exchange.StartExecution(t.Context(), pkg.OrgID, uploadPayload.ExchangePackageID); err != nil {
		t.Fatal(err)
	}
	artifactPath := filepath.Join(server.service.runtime.ArtifactRoot, "exchange", "xpkg_restart", "render", "demo.webm")
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, []byte("persisted-demo"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := sampleRecordingResultForAppTest(pkg)
	result.GeneratedAssets = []model.ArtifactRef{{
		ID:       "artifact_demo_video",
		Kind:     "demo_video",
		URI:      artifactPath,
		MimeType: "video/webm",
		Metadata: map[string]any{"asset_role": "final_demo", "include_in_demo": true},
	}}
	result.ExecutionTrace.Artifacts = nil
	completed, err := server.service.CompleteExecutionPackageWithResult(t.Context(), pkg.OrgID, uploadPayload.ExchangePackageID, result)
	if err != nil {
		t.Fatal(err)
	}

	restartedService, err := NewService(server.service.runtime, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewDevHTTPServer(restartedService)
	status := exchangeHTTPDo[model.ExecutionPackageStatusResponse](t, restarted, http.MethodGet, "/v1/execution-packages/"+uploadPayload.ExchangePackageID+"/status?org_id="+pkg.OrgID, nil)
	if status.Status != model.ExchangePackageStatusCompleted || status.ResultPackageID != completed.ResultPackageID {
		t.Fatalf("restored status mismatch: %+v", status)
	}
	deliverable := findDeliverable(status.ResultSummary.Deliverables, "demo_video")
	if deliverable == nil || deliverable.DownloadURL == "" {
		t.Fatalf("restored status missing downloadable deliverable: %+v", status.ResultSummary)
	}

	request := httptest.NewRequest(http.MethodGet, deliverable.DownloadURL+"?org_id="+pkg.OrgID, nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()
	restarted.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected restored download status %d: %s", response.Code, response.Body.String())
	}
	if got := response.Body.String(); got != "persisted-demo" {
		t.Fatalf("unexpected restored download body %q", got)
	}
}

func TestDevExchangeHTTPListsPersistedPackagesAfterRestart(t *testing.T) {
	t.Setenv(devExchangeHTTPEnv, "1")
	t.Setenv(devExchangeTokenEnv, "test-token")
	server := newTestDevHTTPServer(t)
	now := time.Date(2026, 7, 10, 11, 30, 0, 0, time.UTC)
	server.service.exchange.now = fixedClock(now)
	pkg := sampleClientExecutionPackageForAppTest(t)
	initPayload := exchangeHTTPDo[model.ExecutionPackageInitResponse](t, server, http.MethodPost, "/v1/execution-packages/init", model.ExecutionPackageInitRequest{
		OrgID:       pkg.OrgID,
		ProjectID:   pkg.ProjectID,
		PackageKind: model.ExchangePackageKindClientExecution,
	})
	envelope := sampleEnvelopeForAppTest(t, pkg, now)
	envelope.Crypto.Nonce = "nonce_http_list_restart"
	uploadPayload := exchangeHTTPDo[model.ExecutionPackageUploadResponse](t, server, http.MethodPost, "/v1/execution-packages", exchangeUploadHTTPBody{
		UploadID: initPayload.UploadID,
		Envelope: envelope,
		Payload:  pkg,
	})
	if _, _, err := server.service.exchange.StartExecution(t.Context(), pkg.OrgID, uploadPayload.ExchangePackageID); err != nil {
		t.Fatal(err)
	}
	artifactPath := filepath.Join(server.service.runtime.ArtifactRoot, "exchange", "xpkg_list", "render", "demo.webm")
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, []byte("listed-demo"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := sampleRecordingResultForAppTest(pkg)
	result.GeneratedAssets = []model.ArtifactRef{{
		ID:       "artifact_demo_video",
		Kind:     "demo_video",
		URI:      artifactPath,
		MimeType: "video/webm",
		Metadata: map[string]any{"asset_role": "final_demo", "include_in_demo": true},
	}}
	result.ExecutionTrace.Artifacts = nil
	completed, err := server.service.CompleteExecutionPackageWithResult(t.Context(), pkg.OrgID, uploadPayload.ExchangePackageID, result)
	if err != nil {
		t.Fatal(err)
	}

	restartedService, err := NewService(server.service.runtime, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewDevHTTPServer(restartedService)
	executions := exchangeHTTPDo[model.ExecutionPackageListResponse](t, restarted, http.MethodGet, "/v1/dev/execution-packages?org_id="+pkg.OrgID, nil)
	if len(executions.Items) != 1 {
		t.Fatalf("expected one persisted execution, got %+v", executions.Items)
	}
	execution := executions.Items[0]
	if execution.ExchangePackageID != uploadPayload.ExchangePackageID || execution.ResultPackageID != completed.ResultPackageID {
		t.Fatalf("persisted execution list mismatch: %+v", execution)
	}
	if execution.ResultSummary == nil || findDeliverable(execution.ResultSummary.Deliverables, "demo_video") == nil {
		t.Fatalf("execution list missing deliverable summary: %+v", execution.ResultSummary)
	}
	if strings.Contains(mustJSON(t, executions), "runCascadeRecording") {
		t.Fatalf("execution list leaked script payload: %s", mustJSON(t, executions))
	}

	results := exchangeHTTPDo[model.ResultPackageListResponse](t, restarted, http.MethodGet, "/v1/dev/result-packages?org_id="+pkg.OrgID, nil)
	if len(results.Items) != 1 {
		t.Fatalf("expected one persisted result, got %+v", results.Items)
	}
	resultItem := results.Items[0]
	if resultItem.ResultPackageID != completed.ResultPackageID || resultItem.ExchangePackageID != uploadPayload.ExchangePackageID {
		t.Fatalf("persisted result list mismatch: %+v", resultItem)
	}
	deliverable := findDeliverable(resultItem.ResultSummary.Deliverables, "demo_video")
	if deliverable == nil || deliverable.DownloadURL == "" {
		t.Fatalf("result list missing downloadable deliverable: %+v", resultItem.ResultSummary)
	}
}

func TestServiceRejectsDeliverableOutsideArtifactRoot(t *testing.T) {
	server := newTestDevHTTPServer(t)
	if _, err := server.service.localArtifactPath(filepath.Join(server.service.runtime.DataRoot, "outside.webm")); err == nil {
		t.Fatal("expected artifact outside artifact root to be rejected")
	}
}

func TestEnsureLocalDevAddressAllowsExplicitRemoteExchangeBind(t *testing.T) {
	t.Setenv(devExchangeHTTPEnv, "1")
	t.Setenv(devExchangeTokenEnv, "test-token")
	t.Setenv(devExchangeRemoteBindEnv, "1")

	if err := EnsureLocalDevAddress("0.0.0.0:4317"); err != nil {
		t.Fatalf("expected explicit dev exchange remote bind to be allowed: %v", err)
	}
}

func exchangeHTTPDo[T any](t *testing.T, server *DevHTTPServer, method string, path string, body any, headers ...string) T {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer test-token")
	for index := 0; index+1 < len(headers); index += 2 {
		request.Header.Set(headers[index], headers[index+1])
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "postgres://user:secret") {
		t.Fatalf("exchange response leaked local test secret: %s", response.Body.String())
	}
	var result T
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func waitForExchangeHTTPStatus(t *testing.T, server *DevHTTPServer, exchangePackageID string, orgID string, want model.ExchangePackageStatus) model.ExecutionPackageStatusResponse {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var status model.ExecutionPackageStatusResponse
	for time.Now().Before(deadline) {
		status = exchangeHTTPDo[model.ExecutionPackageStatusResponse](t, server, http.MethodGet, "/v1/execution-packages/"+exchangePackageID+"/status?org_id="+orgID, nil)
		if status.Status == want {
			return status
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for status %q, last status %+v", want, status)
	return model.ExecutionPackageStatusResponse{}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
