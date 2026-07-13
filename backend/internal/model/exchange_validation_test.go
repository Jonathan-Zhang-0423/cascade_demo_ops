package model

import (
	"strings"
	"testing"
	"time"
)

func TestValidateClientExecutionPackageIntakeAcceptsTeamProtocol(t *testing.T) {
	now := time.Date(2026, 7, 9, 14, 40, 0, 0, time.UTC)
	pkg := sampleClientExecutionPackage(t)
	envelope := sampleEnvelopeForPayload(t, pkg, now)

	if err := ValidateClientExecutionPackageIntake(&envelope, &pkg, now, map[string]bool{}, staticSignatureVerifier(true)); err != nil {
		t.Fatal(err)
	}
}

func TestValidateClientExecutionPackageIntakeRejectsProtocolMismatch(t *testing.T) {
	now := time.Date(2026, 7, 9, 14, 40, 0, 0, time.UTC)
	pkg := sampleClientExecutionPackage(t)
	envelope := sampleEnvelopeForPayload(t, pkg, now)
	envelope.PayloadSchemaVersion = "demoops.some_other_payload.v1"

	err := ValidateClientExecutionPackageIntake(&envelope, &pkg, now, map[string]bool{}, staticSignatureVerifier(true))
	if err == nil || !strings.Contains(err.Error(), "payload_schema_version") {
		t.Fatalf("expected payload schema mismatch, got %v", err)
	}
}

func TestValidateClientExecutionPackageIntakeRejectsUnsafePackage(t *testing.T) {
	now := time.Date(2026, 7, 9, 14, 40, 0, 0, time.UTC)
	pkg := sampleClientExecutionPackage(t)
	pkg.SafetyReport.AllowedToUpload = false
	envelope := sampleEnvelopeForPayload(t, pkg, now)

	err := ValidateClientExecutionPackageIntake(&envelope, &pkg, now, map[string]bool{}, staticSignatureVerifier(true))
	if err == nil || !strings.Contains(err.Error(), "allowed_to_upload") {
		t.Fatalf("expected safety report rejection, got %v", err)
	}
}

func TestValidateClientExecutionPackageIntakeRejectsBundleDrift(t *testing.T) {
	now := time.Date(2026, 7, 9, 14, 40, 0, 0, time.UTC)
	pkg := sampleClientExecutionPackage(t)
	pkg.ExecutableScriptBundle.PlanJSON.Steps[0].NodeID = "node_not_in_graph"
	envelope := sampleEnvelopeForPayload(t, pkg, now)

	err := ValidateClientExecutionPackageIntake(&envelope, &pkg, now, map[string]bool{}, staticSignatureVerifier(true))
	if err == nil || !strings.Contains(err.Error(), "not in workflow graph") {
		t.Fatalf("expected script document graph mismatch, got %v", err)
	}
}

func TestValidateClientExecutionPackageIntakeRejectsManifestPlanDrift(t *testing.T) {
	now := time.Date(2026, 7, 9, 14, 40, 0, 0, time.UTC)
	pkg := sampleClientExecutionPackage(t)
	pkg.ExecutableScriptBundle.ScriptManifest.StepNodeIDs = append(pkg.ExecutableScriptBundle.ScriptManifest.StepNodeIDs, "node_not_in_plan")
	envelope := sampleEnvelopeForPayload(t, pkg, now)

	err := ValidateClientExecutionPackageIntake(&envelope, &pkg, now, map[string]bool{}, staticSignatureVerifier(true))
	if err == nil || !strings.Contains(err.Error(), "not in plan_json") {
		t.Fatalf("expected manifest plan mismatch, got %v", err)
	}
}

func TestValidateClientExecutionPackageIntakeRejectsInvalidCaptureScope(t *testing.T) {
	now := time.Date(2026, 7, 9, 14, 40, 0, 0, time.UTC)
	pkg := sampleClientExecutionPackage(t)
	pkg.ExecutableScriptBundle.PlanJSON.Steps[0].Capture.Scope = CaptureScope("screen")
	envelope := sampleEnvelopeForPayload(t, pkg, now)

	err := ValidateClientExecutionPackageIntake(&envelope, &pkg, now, map[string]bool{}, staticSignatureVerifier(true))
	if err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("expected invalid capture scope rejection, got %v", err)
	}
}

func TestValidateRecordingResultPackageForRender(t *testing.T) {
	pkg := sampleClientExecutionPackage(t)
	result := sampleRenderableRecordingResult(t, pkg)
	if err := ValidateRecordingResultPackageForRender(&result, &pkg); err != nil {
		t.Fatal(err)
	}

	result.ExecutionTrace.WorkflowGraphID = "other_graph"
	if err := ValidateRecordingResultPackageForRender(&result, &pkg); err == nil {
		t.Fatal("expected workflow graph mismatch")
	}
}

func sampleRenderableRecordingResult(t *testing.T, pkg ClientExecutionPackage) RecordingResultPackage {
	t.Helper()
	now := time.Date(2026, 7, 9, 15, 0, 0, 0, time.UTC)
	assets := []ArtifactRef{{
		ID:           "artifact_raw_recording",
		Kind:         "raw_recording",
		URI:          "file:///tmp/recording.webm",
		MimeType:     "video/webm",
		SHA256:       "sha_recording",
		SourceNodeID: "node_open_dashboard",
	}}
	steps := []StepResult{{NodeID: "node_open_dashboard", Status: "passed", DurationMS: 1200}}
	return RecordingResultPackage{
		ResultID:        "result_renderable",
		SourcePackageID: pkg.PackageID,
		CloudJobID:      "job_1",
		SchemaVersion:   RecordingResultPackageSchemaVersion,
		Status:          RecordingResultStatusGenerated,
		ExecutionTrace: &ExecutionTrace{
			ID:              "trace_1",
			WorkflowGraphID: pkg.WorkflowGraph.ID,
			GraphVersion:    pkg.WorkflowGraph.Version,
			PassRate:        1,
			StepResults:     steps,
			Artifacts:       assets,
		},
		StepResults:     steps,
		GeneratedAssets: assets,
		VerificationReport: VerificationReport{
			PassRate:             1,
			ReproducibilityMatch: true,
		},
		AuditTrail: CloudExecutionAuditTrail{CompletedAt: now},
		Delivery: ResultDelivery{
			ResultPackageRef: PackageArtifactDescriptor{
				ID:        "result_package_artifact",
				Role:      "recording_result",
				Kind:      "recording_result_package",
				URI:       "s3://cascade-results/result.json.enc",
				SHA256:    "sha_result",
				Encrypted: true,
				Sensitive: true,
			},
			RecipientKind:  ResultRecipientAppInstallation,
			RecipientKeyID: "install_result_key_1",
			EncryptionAlg:  CryptoSuiteXChaCha20Poly1305,
			AckRequired:    true,
		},
		CreatedAt: now,
	}
}
