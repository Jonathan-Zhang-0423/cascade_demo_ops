package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestDirectRuntimeFailurePackagesInfrastructureDiagnostic(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	createdAt := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	server := &DirectHTTPServer{}
	result, err := server.directRuntimeFailureResult(
		context.Background(), &pkg, "job-infrastructure-failed", t.TempDir(), model.RecordingResultPackage{},
		newRuntimeExecutionError(runtimeErrorVideoWorkerMissing, errors.New("worker unavailable")), createdAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != model.RecordingResultStatusFailed || result.FailureDiagnostic == nil || result.RepairRequest == nil || !result.RepairRequest.ApprovalRequired {
		t.Fatalf("infrastructure failure was not packaged authoritatively: %+v", result)
	}
	if result.FailureDiagnostic.Error.Code != runtimeErrorVideoWorkerMissing || !result.FailureDiagnostic.RedactionReport.Applied || result.FailureDiagnostic.RedactionReport.FullHTMLIncluded {
		t.Fatalf("infrastructure diagnostic is unsafe or incorrectly classified: %+v", result.FailureDiagnostic)
	}
	if result.StageEventLogRef == nil || result.StageEventLogRef.SHA256 == "" || result.StageEventLogRef.SizeBytes == 0 {
		t.Fatalf("infrastructure failure is missing a replayable stage event log: %+v", result.StageEventLogRef)
	}
}

func TestDirectRuntimeFailurePreservesBrowserEvidenceAndRemovesFinalVideo(t *testing.T) {
	job, result := completeDirectSuccessfulResultFixture(t)
	pkg := model.ClientExecutionPackage{}
	if err := json.Unmarshal(job.PackageJSON, &pkg); err != nil {
		t.Fatal(err)
	}
	markDirectResultAsRuntimeFailure(&pkg, &result, newRuntimeExecutionError("browser_agent_render_failed", errors.New("render failed")), time.Now().UTC())
	if result.Status != model.RecordingResultStatusFailed || result.FailureDiagnostic == nil || len(result.FailureDiagnostic.ScreenshotRefs) == 0 || len(result.FailureDiagnostic.TraceRefs) == 0 {
		t.Fatalf("post-browser failure did not retain diagnostic evidence: %+v", result.FailureDiagnostic)
	}
	if directHasArtifact(directResultArtifactRefs(result), []string{"demo_video"}, "") {
		t.Fatal("failed result retained a final demo video")
	}
}
