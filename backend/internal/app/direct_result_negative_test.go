package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/direct"
	"cascade-demoops/backend/internal/model"
)

// Work-package E P1 negative tests. Each test drives the exact stable error
// code required by the 2026-08-13 server handoff so the Direct result gates
// cannot silently regress.

func TestDirectWorkerRejectsArtifactSizeDrift(t *testing.T) {
	job, result := completeDirectSuccessfulResultFixture(t)
	result.GeneratedAssets[0].SizeBytes += 1 // descriptor drift while bytes stay identical
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	err = validateDirectWorkerResult(job, result.ResultID, data)
	if err == nil || directResultContractErrorCode(err) != "result_artifact_binding_invalid" {
		t.Fatalf("expected artifact size drift rejection, got %v", err)
	}
}

func TestDirectWorkerRejectsReplayManifestMissingStage(t *testing.T) {
	job, result := completeDirectSuccessfulResultFixture(t)
	var manifest model.ReplayManifest
	if err := json.Unmarshal(job.Artifacts["artifact-manifest"].Bytes, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Stages = nil // result keeps its passed step, manifest no longer indexes it
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
		t.Fatalf("expected replay manifest missing-stage rejection, got %v", err)
	}
}

func TestDirectWorkerRejectsStageEventLogMissingOutcomeObserved(t *testing.T) {
	job, result := completeDirectSuccessfulResultFixture(t)
	events, err := decodeDirectStageEventBytes(job.Artifacts["artifact-events"].Bytes)
	if err != nil {
		t.Fatal(err)
	}
	// Drop every outcome_observed event: the passed node then has only its
	// terminal stage_completed event and no real observation.
	kept := events[:0]
	for _, event := range events {
		if event.EventType == model.StageExecutionEventOutcomeObserved {
			continue
		}
		kept = append(kept, event)
	}
	if len(kept) == 0 {
		t.Fatal("fixture must retain a terminal stage event")
	}
	var eventLog bytes.Buffer
	for _, event := range kept {
		line, marshalErr := json.Marshal(event)
		if marshalErr != nil {
			t.Fatal(marshalErr)
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
		t.Fatalf("expected missing outcome_observed rejection, got %v", err)
	}
}

func TestDirectWorkerRejectsFailedResultMissingDiagnostic(t *testing.T) {
	job, result := completeDirectFailedResultFixture(t)
	result.FailureDiagnostic = nil
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	err = validateDirectWorkerResult(job, result.ResultID, data)
	if err == nil {
		t.Fatal("failed result without diagnostic must be rejected")
	}
	if code := directResultContractErrorCode(err); code != "result_artifact_completeness_failed" && code != "result_artifact_content_invalid" {
		t.Fatalf("unexpected error code for missing diagnostic: %s (%v)", code, err)
	}
	if !strings.Contains(err.Error(), "failure_diagnostic") && !strings.Contains(err.Error(), "diagnostic") {
		t.Fatalf("rejection should reference the missing diagnostic, got %v", err)
	}
}

func TestDirectWorkerRejectsFailedResultMissingRepairRequest(t *testing.T) {
	job, result := completeDirectFailedResultFixture(t)
	result.RepairRequest = nil
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	err = validateDirectWorkerResult(job, result.ResultID, data)
	if err == nil {
		t.Fatal("failed result without repair request must be rejected")
	}
	if code := directResultContractErrorCode(err); code != "result_artifact_completeness_failed" && code != "result_artifact_content_invalid" {
		t.Fatalf("unexpected error code for missing repair request: %s (%v)", code, err)
	}
	if !strings.Contains(err.Error(), "repair") {
		t.Fatalf("rejection should reference the missing repair request, got %v", err)
	}
}

// The dedicated failed-result gate must independently enforce diagnostic,
// approval-gated repair, redaction, and no-final-video rules even when the
// generic status contract already passed.
func TestDirectFailedResultGateEnforcesContentRules(t *testing.T) {
	var source model.ClientExecutionPackage
	if job, _ := completeDirectFailedResultFixture(t); true {
		if err := json.Unmarshal(job.PackageJSON, &source); err != nil {
			t.Fatal(err)
		}
	}
	// Fresh fixture per case: the diagnostic and repair request are pointers,
	// so struct copies would alias the same objects.
	fresh := func() model.RecordingResultPackage {
		_, result := completeDirectFailedResultFixture(t)
		return result
	}

	missingDiagnostic := fresh()
	missingDiagnostic.FailureDiagnostic = nil
	if err := validateDirectFailedResult(source, missingDiagnostic); err == nil || directResultContractErrorCode(err) != "result_artifact_completeness_failed" {
		t.Fatalf("gate must require a diagnostic, got %v", err)
	}

	missingRepair := fresh()
	missingRepair.RepairRequest = nil
	if err := validateDirectFailedResult(source, missingRepair); err == nil || directResultContractErrorCode(err) != "result_artifact_completeness_failed" {
		t.Fatalf("gate must require a repair request, got %v", err)
	}

	unapprovedRepair := fresh()
	unapprovedRepair.RepairRequest.ApprovalRequired = false
	if err := validateDirectFailedResult(source, unapprovedRepair); err == nil || directResultContractErrorCode(err) != "result_artifact_completeness_failed" {
		t.Fatalf("gate must require approval-gated repair, got %v", err)
	}

	unredacted := fresh()
	unredacted.FailureDiagnostic.RedactionReport.FullHTMLIncluded = true
	if err := validateDirectFailedResult(source, unredacted); err == nil || directResultContractErrorCode(err) != "result_artifact_content_invalid" {
		t.Fatalf("gate must reject unredacted diagnostics, got %v", err)
	}

	withFinalVideo := fresh()
	job, _ := completeDirectFailedResultFixture(t)
	withFinalVideo.GeneratedAssets = append(withFinalVideo.GeneratedAssets, model.ArtifactRef{
		ID: "artifact-fake-video", Kind: "demo_video", URI: directArtifactURI(job.Status.JobID, "artifact-fake-video"),
		MimeType: "video/mp4", SHA256: strings.Repeat("a", 64), SizeBytes: 10,
	})
	if err := validateDirectFailedResult(source, withFinalVideo); err == nil || directResultContractErrorCode(err) != "result_artifact_content_invalid" {
		t.Fatalf("gate must reject a final video on failed results, got %v", err)
	}
}

// Infrastructure failures must never fabricate browser evidence: the packaged
// result marks browser evidence unavailable and carries no browser-derived
// observation or screenshot claims.
func TestDirectRuntimeFailureNeverFabricatesBrowserEvidence(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	createdAt := time.Date(2026, 8, 11, 10, 30, 0, 0, time.UTC)
	server := &DirectHTTPServer{}
	result, err := server.directRuntimeFailureResult(
		context.Background(), &pkg, "job-infra-no-evidence", t.TempDir(), model.RecordingResultPackage{},
		newRuntimeExecutionError(runtimeErrorVideoWorkerMissing, errors.New("worker unavailable")), createdAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.FailureDiagnostic == nil || !result.FailureDiagnostic.BrowserEvidenceUnavailable {
		t.Fatalf("infrastructure failure must mark browser_evidence_unavailable, got %+v", result.FailureDiagnostic)
	}
	if len(result.FailureDiagnostic.ScreenshotRefs) != 0 {
		t.Fatalf("infrastructure failure must not attach fabricated screenshots: %+v", result.FailureDiagnostic.ScreenshotRefs)
	}
	for _, step := range result.StepResults {
		lower := strings.ToLower(step.ObservedState)
		if strings.Contains(lower, "browser_assertion") || strings.Contains(lower, "url_observed") || strings.Contains(lower, "title_observed") {
			t.Fatalf("infrastructure failure step must not claim browser observation: %q", step.ObservedState)
		}
		if !strings.Contains(step.ObservedState, "infrastructure_failure=") {
			t.Fatalf("infrastructure failure step should record the failure code: %q", step.ObservedState)
		}
	}
}

// Error responses must never echo credentials, tokens, cookies, or raw page
// content even when the rejected payload contains them.
func TestDirectErrorResponsesNeverLeakSecrets(t *testing.T) {
	const (
		bootstrapToken = "bootstrap-secret-token-9f2a"
		workerToken    = "worker-secret-token-7b1c"
		leakMarker     = "LEAK-MARKER-password=hunter2;cookie=session-xyz;api_key=sk-abc123"
	)
	server := NewDirectHTTPServer(nil, "gateway.example", bootstrapToken, workerToken)
	lease := allocateDirectHTTPTestLease(t, server)
	fixtureJob, result := completeDirectSuccessfulResultFixture(t)
	receipt, err := server.gateway.CreateJobWithSourceDigest(
		lease.LeaseID, lease.InstallationID, fixtureJob.Status.PackageID,
		direct.HashSHA256(fixtureJob.PackageJSON), fixtureJob.SourcePackageDigest, fixtureJob.PackageJSON,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.gateway.ClaimWorker(receipt.JobID); err != nil {
		t.Fatal(err)
	}
	result.CloudJobID = receipt.JobID
	// Hide secret-looking content in every place an error message might echo.
	result.ExecutionTrace.ID = leakMarker
	for index := range result.StepResults {
		result.StepResults[index].ObservedState = leakMarker
	}
	result.GeneratedAssets[0].SHA256 = strings.Repeat("0", 64) // trigger a binding rejection
	resultJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range fixtureJob.Artifacts {
		if len(artifact.Bytes) == 0 {
			continue
		}
		if _, err := server.gateway.AddArtifact(receipt.JobID, artifact.Descriptor.ArtifactID, artifact.Descriptor.Kind, artifact.Descriptor.MimeType, artifact.Bytes); err != nil {
			t.Fatal(err)
		}
	}
	body, err := json.Marshal(map[string]any{
		"result_package_id": result.ResultID,
		"result_json":       json.RawMessage(resultJSON),
	})
	if err != nil {
		t.Fatal(err)
	}

	post := func(authToken string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/v1/worker/jobs/"+receipt.JobID+"/result", bytes.NewReader(body))
		request.RemoteAddr = "127.0.0.1:18400"
		if authToken != "" {
			request.Header.Set("Authorization", "Bearer "+authToken)
		}
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response
	}

	unauthorized := post("wrong-token")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d body=%s", unauthorized.Code, unauthorized.Body.String())
	}
	rejected := post(workerToken)
	if rejected.Code != http.StatusBadRequest {
		t.Fatalf("rejected status=%d body=%s", rejected.Code, rejected.Body.String())
	}
	for _, response := range []*httptest.ResponseRecorder{unauthorized, rejected} {
		payload := response.Body.String()
		for _, secret := range []string{bootstrapToken, workerToken, leakMarker, "hunter2", "session-xyz", "sk-abc123"} {
			if strings.Contains(payload, secret) {
				t.Fatalf("error response leaked secret %q: %s", secret, payload)
			}
		}
		if !strings.Contains(rejected.Body.String(), `"code":"result_artifact_binding_invalid"`) && response == rejected {
			t.Fatalf("rejection should surface the stable binding code, got %s", rejected.Body.String())
		}
	}
}
