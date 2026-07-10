package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
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
	if runStatus.Status != model.ExchangePackageStatusFailed || runStatus.Error == nil || runStatus.Error.Code != "video_worker_missing" {
		t.Fatalf("expected missing test worker to fail dev run, got %+v", runStatus)
	}
	if runStatus.Stage != "failed" || runStatus.ProgressPercent != 100 || runStatus.Message == "" {
		t.Fatalf("failed dev run should include progress metadata, got %+v", runStatus)
	}
	if runStatus.FailureSummary == nil || runStatus.FailureSummary.FailedStage != "preparing_worker" {
		t.Fatalf("failed dev run should report failed stage, got %+v", runStatus.FailureSummary)
	}
	if len(runStatus.StageHistory) < 3 {
		t.Fatalf("failed dev run should include stage history, got %+v", runStatus.StageHistory)
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
