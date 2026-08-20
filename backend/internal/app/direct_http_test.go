package app

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/direct"
	"cascade-demoops/backend/internal/model"
)

func TestDirectHealthRequiresBootstrapToken(t *testing.T) {
	server := NewDirectHTTPServer(nil, "gateway.example", "bootstrap", "worker")
	request := httptest.NewRequest(http.MethodGet, "/v1/direct/health", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
	request.Header.Set("Authorization", "Bearer bootstrap")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authorized status = %d body=%s", response.Code, response.Body.String())
	}
	var health model.DirectHealthResponse
	if err := json.NewDecoder(response.Body).Decode(&health); err != nil {
		t.Fatalf("health response must decode as the shared protocol type: %v body=%s", err, response.Body.String())
	}
	if health.ProtocolVersion != model.DirectTransportProtocolVersion || health.ServerTime.IsZero() {
		t.Fatalf("unexpected typed health response: %+v", health)
	}
}

func TestDirectPackageIdempotencyConflictUsesStableHTTPError(t *testing.T) {
	if status := directStatus(direct.ErrPackageIdempotencyConflict); status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	if code := directCode(direct.ErrPackageIdempotencyConflict); code != "package_idempotency_conflict" {
		t.Fatalf("code = %q", code)
	}
	if status := directStatus(direct.ErrGatewayPersistence); status != http.StatusInternalServerError {
		t.Fatalf("persistence status = %d, want %d", status, http.StatusInternalServerError)
	}
	if code := directCode(direct.ErrGatewayPersistence); code != "gateway_state_unavailable" {
		t.Fatalf("persistence code = %q", code)
	}
}

func TestDirectHealthReportsWorkerReadinessWithoutSecrets(t *testing.T) {
	server := NewDirectHTTPServer(nil, "gateway.example", "bootstrap", "worker")
	health := httptest.NewRequest(http.MethodGet, "/v1/direct/health", nil)
	health.Header.Set("Authorization", "Bearer bootstrap")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, health)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"ready":false`)) {
		t.Fatalf("expected worker not ready before heartbeat: status=%d body=%s", response.Code, response.Body.String())
	}
	if bytes.Contains(response.Body.Bytes(), []byte("worker_token")) || bytes.Contains(response.Body.Bytes(), []byte("bootstrap")) {
		t.Fatalf("health response leaked credential material: %s", response.Body.String())
	}

	heartbeat := httptest.NewRequest(http.MethodPost, "/v1/worker/heartbeat", strings.NewReader(`{"mode":"external"}`))
	heartbeat.RemoteAddr = "127.0.0.1:18400"
	heartbeat.Header.Set("Authorization", "Bearer worker")
	heartbeat.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, heartbeat)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"ready":true`)) {
		t.Fatalf("expected worker ready after heartbeat: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestDirectWorkerHeartbeatRequiresLoopbackAndValidMode(t *testing.T) {
	server := NewDirectHTTPServer(nil, "gateway.example", "bootstrap", "worker")
	request := httptest.NewRequest(http.MethodPost, "/v1/worker/heartbeat", strings.NewReader(`{"mode":"external"}`))
	request.RemoteAddr = "192.0.2.10:1"
	request.Header.Set("Authorization", "Bearer worker")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("non-loopback heartbeat status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/worker/heartbeat", strings.NewReader(`{"mode":"unknown"}`))
	request.RemoteAddr = "127.0.0.1:18400"
	request.Header.Set("Authorization", "Bearer worker")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid worker mode status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/worker/heartbeat", strings.NewReader(`{"mode":"external","protocol_version":"cascade.browser_agent_worker.v0"}`))
	request.RemoteAddr = "127.0.0.1:18400"
	request.Header.Set("Authorization", "Bearer worker")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !bytes.Contains(response.Body.Bytes(), []byte(`"code":"protocol_mismatch"`)) {
		t.Fatalf("worker protocol mismatch status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestDirectWorkerV1ClaimStatusArtifactAndReleaseContract(t *testing.T) {
	server := NewDirectHTTPServer(nil, "gateway.example", "bootstrap", "worker")
	_, receipt := createDirectWorkerTestJob(t, server, false)

	claim := func(protocolVersion string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"protocol_version": protocolVersion, "job_id": receipt.JobID})
		request := httptest.NewRequest(http.MethodPost, "/v1/worker/jobs/claim", bytes.NewReader(body))
		request.RemoteAddr = "127.0.0.1:18400"
		request.Header.Set("Authorization", "Bearer worker")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response
	}
	if response := claim("cascade.browser_agent_worker.v0"); response.Code != http.StatusConflict || !bytes.Contains(response.Body.Bytes(), []byte(`"code":"protocol_mismatch"`)) {
		t.Fatalf("claim protocol mismatch status=%d body=%s", response.Code, response.Body.String())
	}
	response := claim(direct.WorkerProtocolVersion)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"protocol_version":"`+direct.WorkerProtocolVersion+`"`)) || !bytes.Contains(response.Body.Bytes(), []byte(receipt.JobID)) {
		t.Fatalf("worker v1 claim status=%d body=%s", response.Code, response.Body.String())
	}

	status := func(progress int, stage string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"progress": progress, "stage": stage})
		request := httptest.NewRequest(http.MethodPut, "/v1/worker/jobs/"+receipt.JobID+"/status", bytes.NewReader(body))
		request.RemoteAddr = "127.0.0.1:18400"
		request.Header.Set("Authorization", "Bearer worker")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response
	}
	if response := status(0, "invalid"); response.Code != http.StatusBadRequest {
		t.Fatalf("zero progress status=%d body=%s", response.Code, response.Body.String())
	}
	if response := status(100, "invalid"); response.Code != http.StatusBadRequest {
		t.Fatalf("terminal progress status=%d body=%s", response.Code, response.Body.String())
	}
	if response := status(50, "recording"); response.Code != http.StatusOK {
		t.Fatalf("running progress status=%d body=%s", response.Code, response.Body.String())
	}

	artifactBody, _ := json.Marshal(map[string]string{
		"artifact_id": "artifact-body", "kind": "step_screenshot", "mime_type": "image/png",
		"bytes_base64": base64.StdEncoding.EncodeToString([]byte("image")),
	})
	artifactRequest := httptest.NewRequest(http.MethodPut, "/v1/worker/jobs/"+receipt.JobID+"/artifacts/artifact-path", bytes.NewReader(artifactBody))
	artifactRequest.RemoteAddr = "127.0.0.1:18400"
	artifactRequest.Header.Set("Authorization", "Bearer worker")
	artifactResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(artifactResponse, artifactRequest)
	if artifactResponse.Code != http.StatusBadRequest || !bytes.Contains(artifactResponse.Body.Bytes(), []byte(`"code":"result_artifact_binding_invalid"`)) {
		t.Fatalf("artifact identity mismatch status=%d body=%s", artifactResponse.Code, artifactResponse.Body.String())
	}

	releaseRequest := httptest.NewRequest(http.MethodPost, "/v1/worker/jobs/"+receipt.JobID+"/release", nil)
	releaseRequest.RemoteAddr = "127.0.0.1:18400"
	releaseRequest.Header.Set("Authorization", "Bearer worker")
	releaseResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(releaseResponse, releaseRequest)
	if releaseResponse.Code != http.StatusOK || !bytes.Contains(releaseResponse.Body.Bytes(), []byte(`"status":"queued"`)) {
		t.Fatalf("ordinary worker release status=%d body=%s", releaseResponse.Code, releaseResponse.Body.String())
	}
}

func TestDirectWorkerCredentialJobReleaseRequiresFreshEnvelope(t *testing.T) {
	server := NewDirectHTTPServer(nil, "gateway.example", "bootstrap", "worker")
	lease, receipt := createDirectWorkerTestJob(t, server, true)
	envelope := direct.DirectCredentialEnvelope{
		JobID: receipt.JobID, PackageID: receipt.PackageID, PackageSHA256: receipt.PackageSHA256,
		GrantID: "grant-login", SecretRef: "vault://login", Secret: "secret",
		InstallationID: lease.InstallationID, LeaseID: lease.LeaseID,
		AllowedDomains: []string{"app.example.com"}, AllowedOperations: []string{"login"},
		ExpiresAtUnixMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	if err := server.gateway.StoreCredential(receipt.JobID, lease.LeaseID, envelope); err != nil {
		t.Fatal(err)
	}
	if _, err := server.gateway.ClaimWorker(receipt.JobID); err != nil {
		t.Fatal(err)
	}
	status, err := server.gateway.ReleaseWorkerClaim(receipt.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "awaiting_credentials" {
		t.Fatalf("credential job release must await a fresh envelope: %+v", status)
	}
	if _, err := server.gateway.ConsumeCredential(receipt.JobID); err == nil {
		t.Fatal("released credential job retained its one-time envelope")
	}
}

func createDirectWorkerTestJob(t *testing.T, server *DirectHTTPServer, credentialsRequired bool) (direct.DirectPortLease, direct.DirectPackageReceipt) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	request := direct.DirectLeaseRequest{
		ProtocolVersion: direct.ProtocolVersion, InstallationID: direct.InstallationID(public), ClientVersion: "worker-contract-test",
		TimestampUnixMS: now.UnixMilli(), RequestNonce: fmt.Sprintf("lease-%d", now.UnixNano()), SigningPublicKeyBase64: base64.StdEncoding.EncodeToString(public),
	}
	request.SignatureBase64 = base64.StdEncoding.EncodeToString(ed25519.Sign(private, direct.LeaseRequestSigningBytes(request)))
	lease, err := server.gateway.Allocate(request)
	if err != nil {
		t.Fatal(err)
	}
	packageJSON := []byte(`{"package_id":"pkg-worker-test"}`)
	receipt, err := server.gateway.CreateJobWithSourceDigestAndCredentialRequirement(
		lease.LeaseID, lease.InstallationID, "pkg-worker-test", direct.HashSHA256(packageJSON), "approved-source-digest", packageJSON, credentialsRequired,
	)
	if err != nil {
		t.Fatal(err)
	}
	return lease, receipt
}

func TestDirectCredentialEnvelopeMustStayWithinApprovedPackageGrant(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	grantExpiry := now.Add(10 * time.Minute)
	pkg := model.ClientExecutionPackage{
		RecordingRunSpec: model.RecordingRunSpec{AllowedDomains: []string{"app.example.com"}},
		CredentialGrants: []model.CredentialGrant{{
			GrantID: "grant-login", CloudSecretRef: "vault://login", ExpiresAt: grantExpiry,
			AllowedDomains: []string{"app.example.com"}, AllowedOperations: []string{"login", "record_demo"},
		}},
	}
	packageJSON, err := json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}
	job := &direct.Job{PackageJSON: packageJSON}
	valid := direct.DirectCredentialEnvelope{
		GrantID: "grant-login", SecretRef: "vault://login", AllowedDomains: []string{"APP.EXAMPLE.COM"},
		AllowedOperations: []string{"login"}, ExpiresAtUnixMS: now.Add(5 * time.Minute).UnixMilli(),
	}
	if err := validateDirectCredentialEnvelopeAgainstPackage(job, valid); err != nil {
		t.Fatalf("approved credential scope rejected: %v", err)
	}
	tests := []struct {
		name    string
		mutate  func(*direct.DirectCredentialEnvelope)
		message string
	}{
		{"unknown grant", func(value *direct.DirectCredentialEnvelope) { value.GrantID = "grant-other" }, "grant_id"},
		{"secret ref drift", func(value *direct.DirectCredentialEnvelope) { value.SecretRef = "vault://other" }, "secret_ref"},
		{"domain escalation", func(value *direct.DirectCredentialEnvelope) { value.AllowedDomains = []string{"admin.example.com"} }, "domains exceed"},
		{"operation escalation", func(value *direct.DirectCredentialEnvelope) { value.AllowedOperations = []string{"export_data"} }, "operations exceed"},
		{"expiry escalation", func(value *direct.DirectCredentialEnvelope) {
			value.ExpiresAtUnixMS = grantExpiry.Add(time.Second).UnixMilli()
		}, "expiry exceeds"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope := valid
			test.mutate(&envelope)
			err := validateDirectCredentialEnvelopeAgainstPackage(job, envelope)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("expected %q error, got %v", test.message, err)
			}
		})
	}
}

func TestDirectLeaseAndWorkerLoopbackGate(t *testing.T) {
	server := NewDirectHTTPServer(nil, "gateway.example", "bootstrap", "worker")
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	lease := direct.DirectLeaseRequest{ProtocolVersion: direct.ProtocolVersion, InstallationID: direct.InstallationID(public), ClientVersion: "app-test", TimestampUnixMS: now, RequestNonce: "lease-nonce", SigningPublicKeyBase64: base64.StdEncoding.EncodeToString(public)}
	lease.SignatureBase64 = base64.StdEncoding.EncodeToString(ed25519.Sign(private, direct.LeaseRequestSigningBytes(lease)))
	body, _ := json.Marshal(lease)
	request := httptest.NewRequest(http.MethodPost, "/v1/direct/leases", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer bootstrap")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("lease status = %d body=%s", response.Code, response.Body.String())
	}
	worker := httptest.NewRequest(http.MethodPost, "/v1/worker/jobs/job/claim", nil)
	worker.RemoteAddr = "192.0.2.10:1"
	worker.Header.Set("Authorization", "Bearer worker")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, worker)
	if response.Code != http.StatusForbidden {
		t.Fatalf("worker remote status = %d", response.Code)
	}
}

func TestDirectWorkerRejectsTransportDigestAsSourceDigest(t *testing.T) {
	job := &direct.Job{
		Status:              direct.DirectJobStatus{JobID: "job-1", PackageID: "pkg-1"},
		PackageSHA256:       "transport-digest",
		SourcePackageDigest: "approved-digest",
	}
	result := model.RecordingResultPackage{
		SchemaVersion:   model.RecordingResultPackageSchemaVersion,
		ResultID:        "result-1",
		SourcePackageID: "pkg-1",
		CloudJobID:      "job-1",
		AuditTrail:      model.CloudExecutionAuditTrail{SourcePackageDigest: "transport-digest"},
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDirectWorkerResult(job, "result-1", data); err == nil {
		t.Fatal("expected transport digest mismatch")
	}
}

func TestDirectWorkerAcceptsCompleteSuccessfulResult(t *testing.T) {
	job, result := completeDirectSuccessfulResultFixture(t)
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDirectWorkerResult(job, result.ResultID, data); err != nil {
		t.Fatalf("complete Direct result rejected: %v", err)
	}
}

func TestDirectWorkerRejectsSuccessWithoutStageEventLog(t *testing.T) {
	job, result := completeDirectSuccessfulResultFixture(t)
	result.StageEventLogRef = nil
	synchronizeDirectResultChecksums(&result)
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	err = validateDirectWorkerResult(job, result.ResultID, data)
	if err == nil || !strings.Contains(err.Error(), "stage_event_log_ref") {
		t.Fatalf("expected stage-event completeness error, got %v", err)
	}
}

func TestDirectWorkerRejectsSuccessWithoutFinalMP4(t *testing.T) {
	job, result := completeDirectSuccessfulResultFixture(t)
	filtered := result.GeneratedAssets[:0]
	for _, ref := range result.GeneratedAssets {
		if ref.Kind != "demo_video" {
			filtered = append(filtered, ref)
		}
	}
	result.GeneratedAssets = filtered
	result.ExecutionTrace.Artifacts = append([]model.ArtifactRef{}, filtered...)
	delete(job.Artifacts, "artifact-video")
	synchronizeDirectResultChecksums(&result)
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	err = validateDirectWorkerResult(job, result.ResultID, data)
	if err == nil || !strings.Contains(err.Error(), "final MP4") {
		t.Fatalf("expected final MP4 completeness error, got %v", err)
	}
}

func TestDirectWorkerRejectsSuccessWithoutRuntimeValidation(t *testing.T) {
	job, result := completeDirectSuccessfulResultFixture(t)
	result.ValidationReports = []model.ValidationReport{result.ValidationReports[0], result.ValidationReports[2]}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	err = validateDirectWorkerResult(job, result.ResultID, data)
	if err == nil || !strings.Contains(err.Error(), "runtime validation") {
		t.Fatalf("expected runtime validation completeness error, got %v", err)
	}
}

func TestDirectWorkerRejectsLocalRuntimeURI(t *testing.T) {
	job, result := completeDirectSuccessfulResultFixture(t)
	result.GeneratedAssets[0].URI = "file:///local/recording.webm"
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	err = validateDirectWorkerResult(job, result.ResultID, data)
	if err == nil || directResultContractErrorCode(err) != "result_contains_local_runtime_data" {
		t.Fatalf("expected local runtime URI rejection, got %v", err)
	}
}

func TestDirectWorkerResultEarlyGatesUseProtocolErrorCodes(t *testing.T) {
	tests := []struct {
		name     string
		wantCode string
		mutate   func(*model.RecordingResultPackage)
	}{
		{
			name:     "result identity drift",
			wantCode: "result_artifact_content_invalid",
			mutate: func(result *model.RecordingResultPackage) {
				result.ResultID = "result-from-another-job"
			},
		},
		{
			name:     "artifact descriptor incomplete",
			wantCode: "result_artifact_completeness_failed",
			mutate: func(result *model.RecordingResultPackage) {
				result.GeneratedAssets[0].SizeBytes = 0
			},
		},
		{
			name:     "artifact bytes binding drift",
			wantCode: "result_artifact_binding_invalid",
			mutate: func(result *model.RecordingResultPackage) {
				result.GeneratedAssets[0].SHA256 = strings.Repeat("0", 64)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			job, result := completeDirectSuccessfulResultFixture(t)
			resultPackageID := result.ResultID
			test.mutate(&result)
			data, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			err = validateDirectWorkerResult(job, resultPackageID, data)
			if err == nil || directResultContractErrorCode(err) != test.wantCode {
				t.Fatalf("error=%v code=%q, want %q", err, directResultContractErrorCode(err), test.wantCode)
			}
		})
	}
}

func TestDirectWorkerResultHTTPReturnsProtocolErrorCodes(t *testing.T) {
	tests := []struct {
		name     string
		wantCode string
		mutate   func(*model.RecordingResultPackage)
	}{
		{
			name:     "content identity",
			wantCode: "result_artifact_content_invalid",
			mutate: func(result *model.RecordingResultPackage) {
				result.ResultID = "result-from-another-job"
			},
		},
		{
			name:     "descriptor completeness",
			wantCode: "result_artifact_completeness_failed",
			mutate: func(result *model.RecordingResultPackage) {
				result.GeneratedAssets[0].SizeBytes = 0
			},
		},
		{
			name:     "uploaded bytes binding",
			wantCode: "result_artifact_binding_invalid",
			mutate: func(result *model.RecordingResultPackage) {
				result.GeneratedAssets[0].SHA256 = strings.Repeat("0", 64)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			const workerToken = "result-contract-worker-token"
			server := NewDirectHTTPServer(nil, "gateway.example", "bootstrap", workerToken)
			lease := allocateDirectHTTPTestLease(t, server)
			fixtureJob, result := completeDirectSuccessfulResultFixture(t)
			for _, artifactID := range []string{"artifact-raw", "artifact-trace", "artifact-shot", "artifact-video", "artifact-docs"} {
				setDirectFixtureArtifactBytes(t, fixtureJob, &result, artifactID, []byte("endpoint-fixture-"+artifactID))
			}
			receipt, err := server.gateway.CreateJobWithSourceDigest(
				lease.LeaseID,
				lease.InstallationID,
				fixtureJob.Status.PackageID,
				direct.HashSHA256(fixtureJob.PackageJSON),
				fixtureJob.SourcePackageDigest,
				fixtureJob.PackageJSON,
			)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := server.gateway.ClaimWorker(receipt.JobID); err != nil {
				t.Fatal(err)
			}
			for artifactID, artifact := range fixtureJob.Artifacts {
				if len(artifact.Bytes) == 0 {
					t.Fatalf("fixture bytes are unavailable for %q", artifactID)
				}
				if _, err := server.gateway.AddArtifact(receipt.JobID, artifact.Descriptor.ArtifactID, artifact.Descriptor.Kind, artifact.Descriptor.MimeType, artifact.Bytes); err != nil {
					t.Fatal(err)
				}
			}
			result.CloudJobID = receipt.JobID
			resultPackageID := result.ResultID
			test.mutate(&result)
			resultJSON, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(map[string]any{
				"result_package_id": resultPackageID,
				"result_json":       json.RawMessage(resultJSON),
			})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/v1/worker/jobs/"+receipt.JobID+"/result", bytes.NewReader(body))
			request.RemoteAddr = "127.0.0.1:18400"
			request.Header.Set("Authorization", "Bearer "+workerToken)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || !bytes.Contains(response.Body.Bytes(), []byte(`"code":"`+test.wantCode+`"`)) {
				t.Fatalf("status=%d body=%s, want code=%q", response.Code, response.Body.String(), test.wantCode)
			}
		})
	}
}

func TestDirectWorkerRejectsReplayManifestPolicyDrift(t *testing.T) {
	job, result := completeDirectSuccessfulResultFixture(t)
	var manifest model.ReplayManifest
	if err := json.Unmarshal(job.Artifacts["artifact-manifest"].Bytes, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.PolicyHashSHA256 = "different-policy"
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	setDirectFixtureArtifactBytes(t, job, &result, "artifact-manifest", data)
	synchronizeDirectResultChecksums(&result)
	resultJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	err = validateDirectWorkerResult(job, result.ResultID, resultJSON)
	if err == nil || directResultContractErrorCode(err) != "result_artifact_content_invalid" {
		t.Fatalf("expected replay manifest content rejection, got %v", err)
	}
}

func TestDirectWorkerRejectsNonIncreasingStageEventSequence(t *testing.T) {
	job, result := completeDirectSuccessfulResultFixture(t)
	events, err := decodeDirectStageEventBytes(job.Artifacts["artifact-events"].Bytes)
	if err != nil {
		t.Fatal(err)
	}
	events[1].Sequence = events[0].Sequence
	var eventLog bytes.Buffer
	for _, event := range events {
		line, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		eventLog.Write(line)
		eventLog.WriteByte('\n')
	}
	setDirectFixtureArtifactBytes(t, job, &result, "artifact-events", eventLog.Bytes())
	synchronizeDirectResultChecksums(&result)
	resultJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	err = validateDirectWorkerResult(job, result.ResultID, resultJSON)
	if err == nil || directResultContractErrorCode(err) != "result_artifact_content_invalid" {
		t.Fatalf("expected stage event sequence rejection, got %v", err)
	}
}

func TestDirectWorkerAcceptsAuthoritativeFailedResult(t *testing.T) {
	job, result := completeDirectFailedResultFixture(t)
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDirectWorkerResult(job, result.ResultID, data); err != nil {
		t.Fatalf("authoritative failed result rejected: %v", err)
	}
}

func TestDirectWorkerRejectsFailedResultWithoutTraceEvidence(t *testing.T) {
	job, result := completeDirectFailedResultFixture(t)
	result.FailureDiagnostic.TraceRefs = nil
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	err = validateDirectWorkerResult(job, result.ResultID, data)
	if err == nil || directResultContractErrorCode(err) != "result_artifact_completeness_failed" {
		t.Fatalf("expected failed result trace completeness rejection, got %v", err)
	}
}

func completeDirectSuccessfulResultFixture(t *testing.T) (*direct.Job, model.RecordingResultPackage) {
	t.Helper()
	const (
		packageID = "pkg-direct-complete"
		jobID     = "job-direct-complete"
		sourceSHA = "approved-source-digest"
	)
	now := time.Date(2026, 8, 11, 8, 0, 0, 0, time.UTC)
	source := model.ClientExecutionPackage{
		PackageID: packageID,
		RecordingRunSpec: model.RecordingRunSpec{Outputs: model.RecordingOutputRequest{
			RawRecording: true, FinalVideo: true, ScreenshotPack: true, StepByStepDocs: true, Trace: true,
		}},
		ExecutableScriptBundle: &model.ExecutableRecordingScriptBundle{
			ScriptManifest: model.ExecutableScriptManifest{Runtime: model.ExecutableScriptRuntimeBrowserAgentOutlineV1},
			Reproducibility: model.ExecutableScriptReproducibility{
				BundleHashSHA256: "bundle-digest", PlanHashSHA256: "plan-digest", BrowserAgentContractHashSHA256: "policy-digest",
			},
			StageApprovalPlan: &model.StageApprovalPlan{Stages: []model.StageApprovalStage{{
				ID: "stage-1", NodeID: "node-1", Order: 1,
			}}},
		},
	}
	packageJSON, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	assets := []model.ArtifactRef{
		{ID: "artifact-raw", Kind: "raw_recording", URI: directArtifactURI(jobID, "artifact-raw"), MimeType: "video/webm", SHA256: "sha-raw", SizeBytes: 10},
		{ID: "artifact-trace", Kind: "browser_trace", URI: directArtifactURI(jobID, "artifact-trace"), MimeType: "application/zip", SHA256: "sha-trace", SizeBytes: 20},
		{ID: "artifact-shot", Kind: "step_screenshot", URI: directArtifactURI(jobID, "artifact-shot"), MimeType: "image/png", SHA256: "sha-shot", SizeBytes: 30},
		{ID: "artifact-video", Kind: "demo_video", URI: directArtifactURI(jobID, "artifact-video"), MimeType: "video/mp4", SHA256: "sha-video", SizeBytes: 40},
		{ID: "artifact-docs", Kind: "step_by_step_docs", URI: directArtifactURI(jobID, "artifact-docs"), MimeType: "text/markdown", SHA256: "sha-docs", SizeBytes: 50},
		{ID: "artifact-manifest", Kind: "replay_manifest", URI: directArtifactURI(jobID, "artifact-manifest"), MimeType: "application/json", SHA256: "sha-manifest", SizeBytes: 70},
	}
	stageLog := model.ArtifactRef{ID: "artifact-events", Kind: "browser_agent_stage_event_log", URI: directArtifactURI(jobID, "artifact-events"), MimeType: "application/x-ndjson", SHA256: "sha-events", SizeBytes: 60}
	job := &direct.Job{
		Status:              direct.DirectJobStatus{JobID: jobID, PackageID: packageID},
		SourcePackageDigest: sourceSHA,
		PackageJSON:         packageJSON,
		Artifacts:           map[string]direct.Artifact{},
	}
	for _, ref := range append(append([]model.ArtifactRef{}, assets...), stageLog) {
		job.Artifacts[ref.ID] = direct.Artifact{Descriptor: direct.DirectArtifactDescriptor{
			ArtifactID: ref.ID, Kind: ref.Kind, MimeType: ref.MimeType, SHA256: ref.SHA256, Size: ref.SizeBytes,
		}}
	}
	report := func(id string, phase model.ValidationPhase, nodeID, stageID string) model.ValidationReport {
		return model.ValidationReport{
			SchemaVersion: model.ValidationReportSchemaVersion, ReportID: id, RunID: "run-direct-complete",
			SourcePackageID: packageID, SourceBundleHashSHA256: "bundle-digest", PolicyHashSHA256: "policy-digest",
			Phase: phase, NodeID: nodeID, StageID: stageID, Decision: model.ValidationDecisionContinue,
			PassRate: 1, OverallConfidence: 1, EvidenceQuality: model.RuntimeObservationAssertion,
			EvidenceRefs: []model.EvidenceRef{{ID: "evidence-" + id}}, CreatedAt: now,
		}
	}
	result := model.RecordingResultPackage{
		ResultID: "result-direct-complete", SourcePackageID: packageID, CloudJobID: jobID,
		SchemaVersion: model.RecordingResultPackageSchemaVersion, Status: model.RecordingResultStatusGenerated,
		ExecutionRuntime: model.ExecutableScriptRuntimeBrowserAgentOutlineV1,
		ExecutionTrace: &model.ExecutionTrace{ID: "trace-direct-complete", StepResults: []model.StepResult{{
			NodeID: "node-1", Status: "passed", ObservedState: "source=browser_assertion; assertion:complete=passed",
		}}, Artifacts: append([]model.ArtifactRef{}, assets...)},
		StepResults:     []model.StepResult{{NodeID: "node-1", Status: "passed", ObservedState: "source=browser_assertion; assertion:complete=passed"}},
		GeneratedAssets: append([]model.ArtifactRef{}, assets...), StageEventLogRef: &stageLog,
		ValidationReports: []model.ValidationReport{
			report("pre", model.ValidationPhasePreExecution, "", ""),
			report("runtime", model.ValidationPhaseRuntimeStage, "node-1", "stage-1"),
			report("post", model.ValidationPhasePostExecution, "", ""),
		},
		VerificationReport: model.VerificationReport{PassRate: 1, ReproducibilityMatch: true},
		AuditTrail:         model.CloudExecutionAuditTrail{SourcePackageDigest: sourceSHA},
		Delivery: model.ResultDelivery{ResultPackageRef: model.PackageArtifactDescriptor{
			ID: "result-direct-complete", Kind: "recording_result_package", URI: directResultURI(jobID),
		}},
	}
	events := []model.StageExecutionEvent{
		{
			SchemaVersion: model.StageExecutionEventSchemaVersion, EventID: "event-outcome", RunID: "run-direct-complete",
			SourcePackageID: packageID, SourceBundleHashSHA256: "bundle-digest", PolicyHashSHA256: "policy-digest",
			NodeID: "node-1", StageID: "stage-1", Attempt: 1, Sequence: 1,
			EventType: model.StageExecutionEventOutcomeObserved, OccurredAt: now,
			Observation:  &model.RuntimeObservation{Source: model.RuntimeObservationAssertion, Assertions: []model.RuntimeAssertion{{Kind: "business_complete", Passed: true}}},
			EvidenceRefs: []model.EvidenceRef{{ID: "artifact-shot", Kind: model.EvidenceKindWebScreenshot}},
		},
		{
			SchemaVersion: model.StageExecutionEventSchemaVersion, EventID: "event-completed", RunID: "run-direct-complete",
			SourcePackageID: packageID, SourceBundleHashSHA256: "bundle-digest", PolicyHashSHA256: "policy-digest",
			NodeID: "node-1", StageID: "stage-1", Attempt: 1, Sequence: 2,
			EventType: model.StageExecutionEventStageCompleted, OccurredAt: now.Add(time.Second),
			Observation:  &model.RuntimeObservation{Source: model.RuntimeObservationAssertion},
			EvidenceRefs: []model.EvidenceRef{{ID: "artifact-shot", Kind: model.EvidenceKindWebScreenshot}},
		},
	}
	var eventLog bytes.Buffer
	for _, event := range events {
		line, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		eventLog.Write(line)
		eventLog.WriteByte('\n')
	}
	manifest := model.ReplayManifest{
		SchemaVersion: model.ReplayManifestSchemaVersion, ManifestID: "artifact-manifest", CreatedAt: now,
		RunID: "run-direct-complete", PackageID: packageID, BundleHashSHA256: "bundle-digest", PolicyHashSHA256: "policy-digest",
		Status: "success", FinalDecision: model.ValidationDecisionContinue,
		Stages: []model.ReplayManifestStage{{NodeID: "node-1", StageID: "stage-1", Order: 1, Status: "passed", EvidenceArtifactIDs: []string{"artifact-shot"}}},
		ValidationReports: []model.ReplayManifestValidationRef{
			{ReportID: "pre", Phase: model.ValidationPhasePreExecution, Decision: model.ValidationDecisionContinue},
			{ReportID: "runtime", Phase: model.ValidationPhaseRuntimeStage, Decision: model.ValidationDecisionContinue},
			{ReportID: "post", Phase: model.ValidationPhasePostExecution, Decision: model.ValidationDecisionContinue},
		},
		MP4URI: directArtifactURI(jobID, "artifact-video"), RawRecordingURI: directArtifactURI(jobID, "artifact-raw"),
		BrowserTraceURI: directArtifactURI(jobID, "artifact-trace"), StageEventLogURI: directArtifactURI(jobID, "artifact-events"),
		ManifestURI: directArtifactURI(jobID, "artifact-manifest"), ProtocolRuntime: model.ExecutableScriptRuntimeBrowserAgentOutlineV1,
		ExecutionBundleRuntime: model.ExecutableScriptRuntimeBrowserAgentOutlineV1,
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	setDirectFixtureArtifactBytes(t, job, &result, "artifact-events", eventLog.Bytes())
	setDirectFixtureArtifactBytes(t, job, &result, "artifact-manifest", manifestJSON)
	synchronizeDirectResultChecksums(&result)
	return job, result
}

func setDirectFixtureArtifactBytes(t *testing.T, job *direct.Job, result *model.RecordingResultPackage, artifactID string, data []byte) {
	t.Helper()
	artifact, ok := job.Artifacts[artifactID]
	if !ok {
		t.Fatalf("fixture artifact %q not found", artifactID)
	}
	artifact.Bytes = append([]byte{}, data...)
	artifact.Descriptor.SHA256 = direct.HashSHA256(data)
	artifact.Descriptor.Size = int64(len(data))
	job.Artifacts[artifactID] = artifact
	update := func(ref *model.ArtifactRef) {
		if ref != nil && ref.ID == artifactID {
			ref.SHA256 = artifact.Descriptor.SHA256
			ref.SizeBytes = artifact.Descriptor.Size
		}
	}
	for index := range result.GeneratedAssets {
		update(&result.GeneratedAssets[index])
	}
	if result.ExecutionTrace != nil {
		for index := range result.ExecutionTrace.Artifacts {
			update(&result.ExecutionTrace.Artifacts[index])
		}
	}
	if result.StageEventLogRef != nil {
		update(result.StageEventLogRef)
	}
}

func completeDirectFailedResultFixture(t *testing.T) (*direct.Job, model.RecordingResultPackage) {
	t.Helper()
	job, result := completeDirectSuccessfulResultFixture(t)
	now := time.Date(2026, 8, 11, 8, 5, 0, 0, time.UTC)
	filter := func(refs []model.ArtifactRef) []model.ArtifactRef {
		filtered := refs[:0]
		for _, ref := range refs {
			if ref.Kind != "demo_video" && ref.Kind != "step_by_step_docs" {
				filtered = append(filtered, ref)
			}
		}
		return filtered
	}
	result.Status = model.RecordingResultStatusFailed
	result.GeneratedAssets = filter(result.GeneratedAssets)
	result.ExecutionTrace.Artifacts = filter(result.ExecutionTrace.Artifacts)
	delete(job.Artifacts, "artifact-video")
	delete(job.Artifacts, "artifact-docs")
	result.StepResults = []model.StepResult{{
		NodeID: "node-1", Status: "failed", ObservedState: "source=actual_browser_observation; assertion:business_complete=failed",
		Error: &model.AgentError{Code: "browser_agent_outcome_not_observed", Message: "Expected business outcome was not observed."},
	}}
	result.ExecutionTrace.StepResults = append([]model.StepResult{}, result.StepResults...)
	result.ExecutionTrace.PassRate = 0
	result.VerificationReport.PassRate = 0
	result.VerificationReport.FailedNodeIDs = []string{"node-1"}
	for index := range result.ValidationReports {
		if result.ValidationReports[index].Phase != model.ValidationPhasePreExecution {
			result.ValidationReports[index].Decision = model.ValidationDecisionStopAndReport
		}
	}
	result.FailureDiagnostic = &model.ScriptFailureDiagnostic{
		ID: "diag-node-1", SchemaVersion: model.ScriptFailureDiagnosticSchemaVersion,
		SourcePackageID: result.SourcePackageID, CloudJobID: result.CloudJobID, FailedNodeID: "node-1", FailedStepOrder: 1, Attempt: 1,
		Error: model.AgentError{Code: "browser_agent_outcome_not_observed", Message: "Expected business outcome was not observed."},
		ScreenshotRefs: []model.PackageArtifactDescriptor{{
			ID: "artifact-shot", Kind: "step_screenshot", URI: directArtifactURI(result.CloudJobID, "artifact-shot"), MimeType: "image/png",
			SHA256: job.Artifacts["artifact-shot"].Descriptor.SHA256, SizeBytes: job.Artifacts["artifact-shot"].Descriptor.Size,
			Encrypted: true, Sensitive: true, RecipientKeyID: "direct-installation-key",
		}},
		TraceRefs: []model.PackageArtifactDescriptor{{
			ID: "artifact-trace", Kind: "browser_trace", URI: directArtifactURI(result.CloudJobID, "artifact-trace"), MimeType: "application/zip",
			SHA256: job.Artifacts["artifact-trace"].Descriptor.SHA256, SizeBytes: job.Artifacts["artifact-trace"].Descriptor.Size,
			Encrypted: true, Sensitive: true, RecipientKeyID: "direct-installation-key",
		}},
		RedactionReport: model.DiagnosticRedactionReport{Applied: true, PolicyRef: "policy-digest", FullHTMLIncluded: false},
		CapturedAt:      now,
	}
	result.RepairRequest = &model.ScriptRepairRequest{
		ID: "repair-failed", SourceResultID: result.ResultID, SourcePackageID: result.SourcePackageID, CloudJobID: result.CloudJobID,
		FailedBundleHashSHA256: "bundle-digest", FailedPlanHashSHA256: "plan-digest", MaxRepairAttempts: 1, RepairAttempt: 1,
		ApprovalRequired: true, RequestedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	events := []model.StageExecutionEvent{
		{
			SchemaVersion: model.StageExecutionEventSchemaVersion, EventID: "event-started", RunID: "run-direct-complete",
			SourcePackageID: result.SourcePackageID, SourceBundleHashSHA256: "bundle-digest", PolicyHashSHA256: "policy-digest",
			NodeID: "node-1", StageID: "stage-1", Attempt: 1, Sequence: 1,
			EventType: model.StageExecutionEventStageStarted, OccurredAt: now,
		},
		{
			SchemaVersion: model.StageExecutionEventSchemaVersion, EventID: "event-failed", RunID: "run-direct-complete",
			SourcePackageID: result.SourcePackageID, SourceBundleHashSHA256: "bundle-digest", PolicyHashSHA256: "policy-digest",
			NodeID: "node-1", StageID: "stage-1", Attempt: 1, Sequence: 2,
			EventType: model.StageExecutionEventStageFailed, OccurredAt: now.Add(time.Second),
			Observation:  &model.RuntimeObservation{Source: model.RuntimeObservationActualBrowser},
			EvidenceRefs: []model.EvidenceRef{{ID: "artifact-shot", Kind: model.EvidenceKindWebScreenshot}},
		},
	}
	var eventLog bytes.Buffer
	for _, event := range events {
		line, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		eventLog.Write(line)
		eventLog.WriteByte('\n')
	}
	manifest := model.ReplayManifest{
		SchemaVersion: model.ReplayManifestSchemaVersion, ManifestID: "artifact-manifest", CreatedAt: now,
		RunID: "run-direct-complete", PackageID: result.SourcePackageID, BundleHashSHA256: "bundle-digest", PolicyHashSHA256: "policy-digest",
		Status: "failed", FailedNodeID: "node-1", FinalDecision: model.ValidationDecisionStopAndReport,
		Stages: []model.ReplayManifestStage{{NodeID: "node-1", StageID: "stage-1", Order: 1, Status: "failed", FailureCode: "browser_agent_outcome_not_observed", EvidenceArtifactIDs: []string{"artifact-shot"}}},
		ValidationReports: []model.ReplayManifestValidationRef{
			{ReportID: "pre", Phase: model.ValidationPhasePreExecution, Decision: model.ValidationDecisionContinue},
			{ReportID: "runtime", Phase: model.ValidationPhaseRuntimeStage, Decision: model.ValidationDecisionStopAndReport},
			{ReportID: "post", Phase: model.ValidationPhasePostExecution, Decision: model.ValidationDecisionStopAndReport},
		},
		RawRecordingURI: directArtifactURI(result.CloudJobID, "artifact-raw"), BrowserTraceURI: directArtifactURI(result.CloudJobID, "artifact-trace"),
		StageEventLogURI: directArtifactURI(result.CloudJobID, "artifact-events"), ManifestURI: directArtifactURI(result.CloudJobID, "artifact-manifest"),
		ProtocolRuntime: model.ExecutableScriptRuntimeBrowserAgentOutlineV1, ExecutionBundleRuntime: model.ExecutableScriptRuntimeBrowserAgentOutlineV1,
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	setDirectFixtureArtifactBytes(t, job, &result, "artifact-events", eventLog.Bytes())
	setDirectFixtureArtifactBytes(t, job, &result, "artifact-manifest", manifestJSON)
	synchronizeDirectResultChecksums(&result)
	return job, result
}

// This test exercises the complete Direct API intake boundary with a
// Server-controlled fixture only. It deliberately does not invoke the App,
// claim/run a job, or start Chromium; app_formal_run must remain false until
// the separately approved App package test.
func TestDirectServerControlledFixtureUploadOnly(t *testing.T) {
	const (
		bootstrap = "fixture-bootstrap-token"
		worker    = "fixture-worker-token"
	)
	server := NewDirectHTTPServer(nil, "gateway.example", bootstrap, worker)

	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	leaseReq := direct.DirectLeaseRequest{
		ProtocolVersion:        direct.ProtocolVersion,
		InstallationID:         direct.InstallationID(public),
		ClientVersion:          "server-fixture-preflight",
		TimestampUnixMS:        now,
		RequestNonce:           "fixture-lease-nonce",
		SigningPublicKeyBase64: base64.StdEncoding.EncodeToString(public),
	}
	leaseReq.SignatureBase64 = base64.StdEncoding.EncodeToString(ed25519.Sign(private, direct.LeaseRequestSigningBytes(leaseReq)))
	leaseBody, _ := json.Marshal(leaseReq)
	leaseHTTP := httptest.NewRequest(http.MethodPost, "/v1/direct/leases", bytes.NewReader(leaseBody))
	leaseHTTP.Header.Set("Authorization", "Bearer "+bootstrap)
	leaseResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(leaseResponse, leaseHTTP)
	if leaseResponse.Code != http.StatusCreated {
		t.Fatalf("fixture lease status=%d body=%s", leaseResponse.Code, leaseResponse.Body.String())
	}
	var lease direct.DirectPortLease
	if err := json.Unmarshal(leaseResponse.Body.Bytes(), &lease); err != nil {
		t.Fatal(err)
	}
	if lease.LeaseID == "" || lease.DataPort < direct.DataPortMin || lease.DataPort > direct.DataPortMax {
		t.Fatalf("invalid fixture lease: %+v", lease)
	}

	fixture := readContractFixture(t, "client_execution_package.browser_agent_outline.json")
	var pkg model.ClientExecutionPackage
	if err := json.Unmarshal(fixture, &pkg); err != nil {
		t.Fatal(err)
	}
	// The checked-in fixture predates the App-produced package hash field. For
	// this server-only intake test, retain the original fixture digest as the
	// approved source hash while uploading the explicitly marked test payload.
	pkg.Reproducibility.PackageHashSHA256 = direct.HashSHA256(fixture)
	// The checked-in fixture declares a dormant demo grant but no stage uses a
	// secret_ref. Keep this server-controlled fixture credential-free so it tests
	// package queueing rather than the App credential upload path.
	pkg.CredentialGrants = nil
	pkg.ProducerInstallationID = lease.InstallationID
	populateDirectSelectorProvenanceFixture(t, &pkg, time.Now().UTC())
	recomputeDirectFixtureHashes(t, &pkg)
	pkg.SafetyReport.HumanApproval.ApprovedByInstallationID = lease.InstallationID
	pkg.SafetyReport.HumanApproval.ApprovalSchemaVersion = model.UserApprovalSchemaVersion
	pkg.SafetyReport.HumanApproval.PlanDigestSHA256 = pkg.ExecutableScriptBundle.Reproducibility.PlanHashSHA256
	pkg.SafetyReport.HumanApproval.SubjectDigestsSHA256, err = model.ComputeApprovalSubjectDigestsSHA256(pkg)
	if err != nil {
		t.Fatal(err)
	}
	pkg.SafetyReport.HumanApproval.ApprovalSubjectDigestSHA256, err = model.ComputePackageApprovalSubjectDigest(pkg)
	if err != nil {
		t.Fatal(err)
	}
	fixture, err = json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if err := model.ValidateClientExecutionPackageForDirectExecution(&pkg); err != nil {
		t.Fatalf("fixture must pass formal Direct validation: %v", err)
	}
	message, err := direct.EncryptMessage(lease.LeaseToken, lease.InstallationID, lease.LeaseID, lease.DataPort, "fixture-package-message", "client_execution_package", "app_to_browser_agent", fixture, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(message)
	uploadPath := "/v1/direct/packages"
	uploadHTTP := httptest.NewRequest(http.MethodPost, "https://gateway.example"+uploadPath, bytes.NewReader(payload))
	uploadHTTP.Host = "gateway.example:" + fmt.Sprint(lease.DataPort)
	uploadHTTP.RemoteAddr = "127.0.0.1:54321"
	stamp := time.Now().UnixMilli()
	nonce := "fixture-upload-request-nonce"
	digest := direct.HashSHA256(payload)
	uploadHTTP.Header.Set("X-Cascade-Timestamp", fmt.Sprint(stamp))
	uploadHTTP.Header.Set("X-Cascade-Nonce", nonce)
	uploadHTTP.Header.Set("X-Cascade-Body-SHA256", digest)
	uploadHTTP.Header.Set("X-Cascade-Signature", direct.SignDataRequest(http.MethodPost, (&url.URL{Path: uploadPath}).EscapedPath(), stamp, nonce, digest, lease.LeaseToken, lease.InstallationID))
	uploadResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(uploadResponse, uploadHTTP)
	if uploadResponse.Code != http.StatusOK {
		t.Fatalf("fixture upload status=%d body=%s", uploadResponse.Code, uploadResponse.Body.String())
	}
	var receiptMessage direct.DirectEncryptedMessage
	if err := json.Unmarshal(uploadResponse.Body.Bytes(), &receiptMessage); err != nil {
		t.Fatal(err)
	}
	receiptPlain, err := receiptMessage.Decrypt(lease.LeaseToken, lease.InstallationID, lease.DataPort)
	if err != nil {
		t.Fatal(err)
	}
	var receipt direct.DirectPackageReceipt
	if err := json.Unmarshal(receiptPlain, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "queued" || receipt.PackageID != pkg.PackageID || receipt.JobID == "" {
		t.Fatalf("unexpected fixture receipt: %+v", receipt)
	}

	queuedHTTP := httptest.NewRequest(http.MethodGet, "/v1/worker/jobs/queued", nil)
	queuedHTTP.RemoteAddr = "127.0.0.1:54321"
	queuedHTTP.Header.Set("Authorization", "Bearer "+worker)
	queuedResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(queuedResponse, queuedHTTP)
	if queuedResponse.Code != http.StatusOK || !bytes.Contains(queuedResponse.Body.Bytes(), []byte(receipt.JobID)) {
		t.Fatalf("queued fixture job missing: status=%d body=%s", queuedResponse.Code, queuedResponse.Body.String())
	}
	t.Log("server_controlled_fixture_only=true app_formal_run=false; Chromium and App formal package were not invoked")
}

func TestDirectPackageHTTPRejectsFormalContractViolationsWithStableCodes(t *testing.T) {
	tests := []struct {
		name       string
		wantCode   string
		mutate     func(*testing.T, *model.ClientExecutionPackage)
		refinalize bool
	}{
		{
			name:     "installation origin mismatch",
			wantCode: "unverified_origin",
			mutate: func(_ *testing.T, pkg *model.ClientExecutionPackage) {
				pkg.ProducerInstallationID = "installation-from-another-client"
			},
		},
		{
			name:     "approval digest drift",
			wantCode: "approval_digest_mismatch",
			mutate: func(_ *testing.T, pkg *model.ClientExecutionPackage) {
				pkg.SafetyReport.HumanApproval.SubjectDigestsSHA256.PlanJSON = strings.Repeat("0", 64)
			},
		},
		{
			name:       "selector evidence missing",
			wantCode:   "selector_provenance_incomplete",
			refinalize: true,
			mutate: func(t *testing.T, pkg *model.ClientExecutionPackage) {
				clearFirstDirectSelectorEvidenceID(t, pkg)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := NewDirectHTTPServer(nil, "gateway.example", "bootstrap", "worker")
			lease := allocateDirectHTTPTestLease(t, server)
			pkg := completeDirectHTTPPackageFixture(t, lease.InstallationID)
			test.mutate(t, &pkg)
			if test.refinalize {
				recomputeDirectFixtureHashes(t, &pkg)
				finalizeDirectFixtureApproval(t, &pkg)
			}

			response := uploadDirectHTTPPackageFixture(t, server, lease, pkg, "negative-contract-"+test.wantCode)
			if response.Code != http.StatusBadRequest || !bytes.Contains(response.Body.Bytes(), []byte(`"code":"`+test.wantCode+`"`)) {
				t.Fatalf("status=%d body=%s, want code=%q", response.Code, response.Body.String(), test.wantCode)
			}
		})
	}
}

func allocateDirectHTTPTestLease(t *testing.T, server *DirectHTTPServer) direct.DirectPortLease {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	request := direct.DirectLeaseRequest{
		ProtocolVersion: direct.ProtocolVersion, InstallationID: direct.InstallationID(public), ClientVersion: "direct-negative-contract-test",
		TimestampUnixMS: now.UnixMilli(), RequestNonce: fmt.Sprintf("negative-lease-%d", now.UnixNano()), SigningPublicKeyBase64: base64.StdEncoding.EncodeToString(public),
	}
	request.SignatureBase64 = base64.StdEncoding.EncodeToString(ed25519.Sign(private, direct.LeaseRequestSigningBytes(request)))
	lease, err := server.gateway.Allocate(request)
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

func completeDirectHTTPPackageFixture(t *testing.T, installationID string) model.ClientExecutionPackage {
	t.Helper()
	fixture := readContractFixture(t, "client_execution_package.browser_agent_outline.json")
	var pkg model.ClientExecutionPackage
	if err := json.Unmarshal(fixture, &pkg); err != nil {
		t.Fatal(err)
	}
	pkg.Reproducibility.PackageHashSHA256 = direct.HashSHA256(fixture)
	pkg.CredentialGrants = nil
	pkg.ProducerInstallationID = installationID
	populateDirectSelectorProvenanceFixture(t, &pkg, time.Now().UTC())
	recomputeDirectFixtureHashes(t, &pkg)
	pkg.SafetyReport.HumanApproval.ApprovedByInstallationID = installationID
	pkg.SafetyReport.HumanApproval.ApprovalSchemaVersion = model.UserApprovalSchemaVersion
	finalizeDirectFixtureApproval(t, &pkg)
	if err := model.ValidateClientExecutionPackageForDirectInstallation(&pkg, installationID); err != nil {
		t.Fatalf("completed Direct fixture is invalid: %v", err)
	}
	return pkg
}

func finalizeDirectFixtureApproval(t *testing.T, pkg *model.ClientExecutionPackage) {
	t.Helper()
	pkg.SafetyReport.HumanApproval.PlanDigestSHA256 = pkg.ExecutableScriptBundle.Reproducibility.PlanHashSHA256
	var err error
	pkg.SafetyReport.HumanApproval.SubjectDigestsSHA256, err = model.ComputeApprovalSubjectDigestsSHA256(*pkg)
	if err != nil {
		t.Fatal(err)
	}
	pkg.SafetyReport.HumanApproval.ApprovalSubjectDigestSHA256, err = model.ComputePackageApprovalSubjectDigest(*pkg)
	if err != nil {
		t.Fatal(err)
	}
}

func uploadDirectHTTPPackageFixture(t *testing.T, server *DirectHTTPServer, lease direct.DirectPortLease, pkg model.ClientExecutionPackage, messageID string) *httptest.ResponseRecorder {
	t.Helper()
	plain, err := json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}
	message, err := direct.EncryptMessage(lease.LeaseToken, lease.InstallationID, lease.LeaseID, lease.DataPort, messageID, "client_execution_package", "app_to_browser_agent", plain, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/direct/packages"
	request := httptest.NewRequest(http.MethodPost, "https://gateway.example"+path, bytes.NewReader(payload))
	request.Host = "gateway.example:" + fmt.Sprint(lease.DataPort)
	request.RemoteAddr = "127.0.0.1:54321"
	stamp := time.Now().UnixMilli()
	nonce := messageID + "-request"
	digest := direct.HashSHA256(payload)
	request.Header.Set("X-Cascade-Timestamp", fmt.Sprint(stamp))
	request.Header.Set("X-Cascade-Nonce", nonce)
	request.Header.Set("X-Cascade-Body-SHA256", digest)
	request.Header.Set("X-Cascade-Signature", direct.SignDataRequest(http.MethodPost, (&url.URL{Path: path}).EscapedPath(), stamp, nonce, digest, lease.LeaseToken, lease.InstallationID))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func recomputeDirectFixtureHashes(t *testing.T, pkg *model.ClientExecutionPackage) {
	t.Helper()
	graphHash, err := model.DigestCanonicalJSON(pkg.WorkflowGraph)
	if err != nil {
		t.Fatal(err)
	}
	pkg.Reproducibility.GraphHashSHA256 = graphHash
	bundle := pkg.ExecutableScriptBundle
	bundle.Reproducibility.GraphHashSHA256 = graphHash
	bundle.Reproducibility.PlanHashSHA256, err = bundle.PlanJSON.ComputeScriptHash()
	if err != nil {
		t.Fatal(err)
	}
	bundle.Reproducibility.StagePlanHashSHA256, err = model.DigestCanonicalJSON(bundle.StageApprovalPlan)
	if err != nil {
		t.Fatal(err)
	}
	bundle.Reproducibility.OutlineHashSHA256, err = model.DigestCanonicalJSON(bundle.ScriptOutline)
	if err != nil {
		t.Fatal(err)
	}
	bundle.Reproducibility.PromptPolicyHashSHA256, err = model.DigestCanonicalJSON(bundle.AgentPromptPolicy)
	if err != nil {
		t.Fatal(err)
	}
	bundle.Reproducibility.BrowserAgentContractHashSHA256, err = model.DigestCanonicalJSON(bundle.BrowserAgentContract)
	if err != nil {
		t.Fatal(err)
	}
	bundle.Reproducibility.BundleHashSHA256, err = bundle.ComputeBundleHash()
	if err != nil {
		t.Fatal(err)
	}
}

func populateDirectSelectorProvenanceFixture(t *testing.T, pkg *model.ClientExecutionPackage, observedAt time.Time) {
	t.Helper()
	data, err := json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	index := 0
	var walk func(any)
	walk = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				if key == "selector_alternatives" || key == "dom_hints" {
					if candidates, ok := child.([]any); ok {
						for _, raw := range candidates {
							candidate, ok := raw.(map[string]any)
							if !ok {
								continue
							}
							index++
							evidenceID := fmt.Sprintf("ev_direct_fixture_selector_%d", index)
							candidate["evidence_id"] = evidenceID
							candidate["source_kind"] = "page_scan"
							candidate["source_digest"] = model.SHA256Hex([]byte(fmt.Sprintf("%v:%v:%d", candidate["kind"], candidate["value"], index)))
							candidate["observed_role"] = "button"
							candidate["observed_accessible_name"] = fmt.Sprintf("Server fixture target %d", index)
							candidate["observed_at"] = observedAt.Format(time.RFC3339Nano)
							candidate["evidence_refs"] = []any{map[string]any{"id": evidenceID, "kind": "browser_scan"}}
						}
					}
					continue
				}
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(root)
	data, err = json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, pkg); err != nil {
		t.Fatal(err)
	}
	setRoute := func(candidates []model.SelectorCandidate, route string) {
		for index := range candidates {
			candidates[index].ObservedURL = route
			candidates[index].ObservedRouteTemplate = route
			candidates[index].ObservedPageRole = "product"
			candidates[index].ObservedFormRole = "none"
			candidates[index].EvidenceDigestSHA256 = candidates[index].SourceDigest
		}
	}
	if pkg.ExecutableScriptBundle == nil {
		return
	}
	if plan := pkg.ExecutableScriptBundle.PlanJSON; plan != nil {
		for index := range plan.Steps {
			route := plan.Steps[index].PageTarget.URL
			setRoute(plan.Steps[index].PageTarget.SelectorAlternatives, route)
			setRoute(plan.Steps[index].Action.Target.SelectorAlternatives, route)
			for validationIndex := range plan.Steps[index].Validations {
				setRoute(plan.Steps[index].Validations[validationIndex].Target.SelectorAlternatives, route)
			}
		}
	}
	if plan := pkg.ExecutableScriptBundle.StageApprovalPlan; plan != nil {
		for index := range plan.Stages {
			setRoute(plan.Stages[index].Interaction.Target.SelectorAlternatives, plan.Stages[index].EntryRoute)
		}
	}
	if outline := pkg.ExecutableScriptBundle.ScriptOutline; outline != nil {
		for index := range outline.Stages {
			route := outline.Stages[index].Route
			for componentIndex := range outline.Stages[index].Components {
				setRoute(outline.Stages[index].Components[componentIndex].SelectorAlternatives, route)
			}
			for interactionIndex := range outline.Stages[index].Interactions {
				setRoute(outline.Stages[index].Interactions[interactionIndex].Target.SelectorAlternatives, route)
			}
		}
	}
}

func clearFirstDirectSelectorEvidenceID(t *testing.T, pkg *model.ClientExecutionPackage) {
	t.Helper()
	for stageIndex := range pkg.ExecutableScriptBundle.ScriptOutline.Stages {
		stage := &pkg.ExecutableScriptBundle.ScriptOutline.Stages[stageIndex]
		for componentIndex := range stage.Components {
			candidates := stage.Components[componentIndex].SelectorAlternatives
			if len(candidates) == 0 {
				continue
			}
			candidates[0].EvidenceID = ""
			stage.Components[componentIndex].SelectorAlternatives = candidates
			return
		}
	}
	t.Fatal("fixture does not contain a script-outline selector candidate")
}
