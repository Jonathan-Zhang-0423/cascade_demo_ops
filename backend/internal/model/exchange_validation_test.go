package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestValidateClientExecutionPackageForDirectExecutionAcceptsCompleteApproval(t *testing.T) {
	pkg := approvedDirectOutlinePackage(t)
	if err := ValidateClientExecutionPackageForDirectExecution(&pkg); err != nil {
		t.Fatalf("complete Direct approval rejected: %v", err)
	}
}

func TestValidateClientExecutionPackageForDirectExecutionRejectsIncompleteOrStaleApproval(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*ClientExecutionPackage)
		message string
		code    string
	}{
		{
			name: "missing producer installation",
			mutate: func(pkg *ClientExecutionPackage) {
				pkg.ProducerInstallationID = ""
			},
			message: "origin",
			code:    "unverified_origin",
		},
		{
			name: "approval schema mismatch",
			mutate: func(pkg *ClientExecutionPackage) {
				pkg.SafetyReport.HumanApproval.ApprovalSchemaVersion = "cascade.user_approval.v0"
			},
			message: "approval_schema_version",
			code:    "approval_digest_mismatch",
		},
		{
			name: "approval sub digest mismatch",
			mutate: func(pkg *ClientExecutionPackage) {
				pkg.SafetyReport.HumanApproval.SubjectDigestsSHA256.ScriptOutline = "stale-outline-digest"
			},
			message: "subject digests",
			code:    "approval_digest_mismatch",
		},
		{
			name: "selector provenance missing",
			mutate: func(pkg *ClientExecutionPackage) {
				mutateFirstSelectorCandidateForTest(t, pkg, func(candidate *SelectorCandidate) { candidate.EvidenceID = "" })
				recomputeDirectPackageHashesForTest(t, pkg)
				subjectDigests, err := ComputeApprovalSubjectDigestsSHA256(*pkg)
				if err != nil {
					t.Fatal(err)
				}
				pkg.SafetyReport.HumanApproval.SubjectDigestsSHA256 = subjectDigests
				digest, err := ComputePackageApprovalSubjectDigest(*pkg)
				if err != nil {
					t.Fatal(err)
				}
				pkg.SafetyReport.HumanApproval.ApprovalSubjectDigestSHA256 = digest
			},
			message: "selector provenance",
			code:    "selector_provenance_incomplete",
		},
		{
			name: "missing approval id",
			mutate: func(pkg *ClientExecutionPackage) {
				pkg.SafetyReport.HumanApproval.ApprovalID = ""
			},
			message: "approval_id",
		},
		{
			name: "missing approval subject digest",
			mutate: func(pkg *ClientExecutionPackage) {
				pkg.SafetyReport.HumanApproval.ApprovalSubjectDigestSHA256 = ""
			},
			message: "subject digest",
		},
		{
			name: "package changed after approval",
			mutate: func(pkg *ClientExecutionPackage) {
				pkg.RecordingRunSpec.Timeline.TargetDurationSec++
			},
			message: "subject digest does not match",
		},
		{
			name: "unreviewed stage",
			mutate: func(pkg *ClientExecutionPackage) {
				pkg.SafetyReport.HumanApproval.ReviewedNodeIDs = pkg.SafetyReport.HumanApproval.ReviewedNodeIDs[:1]
			},
			message: "was not reviewed",
		},
		{
			name: "approval timestamp mismatch",
			mutate: func(pkg *ClientExecutionPackage) {
				pkg.SafetyReport.HumanApproval.ApprovedAt = pkg.SafetyReport.HumanApproval.ApprovedAt.Add(time.Second)
			},
			message: "approved_at does not match",
		},
		{
			name: "plan digest mismatch",
			mutate: func(pkg *ClientExecutionPackage) {
				pkg.SafetyReport.HumanApproval.PlanDigestSHA256 = "stale-plan-digest"
			},
			message: "plan digest does not match",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pkg := approvedDirectOutlinePackage(t)
			test.mutate(&pkg)
			err := ValidateClientExecutionPackageForDirectExecution(&pkg)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("expected %q error, got %v", test.message, err)
			}
			if test.code != "" && DirectPackageValidationCode(err) != test.code {
				t.Fatalf("expected code %q, got %q (%v)", test.code, DirectPackageValidationCode(err), err)
			}
		})
	}
}

func approvedDirectOutlinePackage(t *testing.T) ClientExecutionPackage {
	t.Helper()
	pkg := confidenceFixture(t)
	// Confidence is independently recomputed by the App. Omitting the optional
	// cached summary here keeps this test focused on the approval binding.
	pkg.ConfidenceSummary = nil
	approvedAt := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	pkg.ApprovedAt = approvedAt
	pkg.ProducerInstallationID = "direct_install_test_origin"
	populateSelectorProvenanceForTest(t, &pkg, approvedAt)
	recomputeDirectPackageHashesForTest(t, &pkg)
	pkg.SafetyReport.AllowedToUpload = true
	pkg.SafetyReport.HumanApproval = UserApprovalRecord{
		ApprovalID:               "approval-direct-test",
		ApprovedByUserID:         "desktop-test-user",
		ApprovedByInstallationID: pkg.ProducerInstallationID,
		ApprovalSchemaVersion:    UserApprovalSchemaVersion,
		ApprovedAt:               approvedAt,
		PlanDigestSHA256:         pkg.ExecutableScriptBundle.Reproducibility.PlanHashSHA256,
	}
	for _, stage := range pkg.ExecutableScriptBundle.StageApprovalPlan.Stages {
		pkg.SafetyReport.HumanApproval.ReviewedNodeIDs = append(pkg.SafetyReport.HumanApproval.ReviewedNodeIDs, stage.NodeID)
	}
	subjectDigests, err := ComputeApprovalSubjectDigestsSHA256(pkg)
	if err != nil {
		t.Fatal(err)
	}
	pkg.SafetyReport.HumanApproval.SubjectDigestsSHA256 = subjectDigests
	digest, err := ComputePackageApprovalSubjectDigest(pkg)
	if err != nil {
		t.Fatal(err)
	}
	pkg.SafetyReport.HumanApproval.ApprovalSubjectDigestSHA256 = digest
	return pkg
}

func recomputeDirectPackageHashesForTest(t *testing.T, pkg *ClientExecutionPackage) {
	t.Helper()
	graphHash, err := DigestCanonicalJSON(pkg.WorkflowGraph)
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
	bundle.Reproducibility.StagePlanHashSHA256, err = DigestCanonicalJSON(bundle.StageApprovalPlan)
	if err != nil {
		t.Fatal(err)
	}
	bundle.Reproducibility.OutlineHashSHA256, err = DigestCanonicalJSON(bundle.ScriptOutline)
	if err != nil {
		t.Fatal(err)
	}
	bundle.Reproducibility.PromptPolicyHashSHA256, err = DigestCanonicalJSON(bundle.AgentPromptPolicy)
	if err != nil {
		t.Fatal(err)
	}
	bundle.Reproducibility.BrowserAgentContractHashSHA256, err = DigestCanonicalJSON(bundle.BrowserAgentContract)
	if err != nil {
		t.Fatal(err)
	}
	bundle.Reproducibility.BundleHashSHA256, err = bundle.ComputeBundleHash()
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateClientExecutionPackageForDirectInstallationBindsLeaseOrigin(t *testing.T) {
	pkg := approvedDirectOutlinePackage(t)
	if err := ValidateClientExecutionPackageForDirectInstallation(&pkg, pkg.ProducerInstallationID); err != nil {
		t.Fatal(err)
	}
	err := ValidateClientExecutionPackageForDirectInstallation(&pkg, "direct_install_other")
	if DirectPackageValidationCode(err) != "unverified_origin" {
		t.Fatalf("installation mismatch code=%q err=%v", DirectPackageValidationCode(err), err)
	}
}

func populateSelectorProvenanceForTest(t *testing.T, pkg *ClientExecutionPackage, observedAt time.Time) {
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
							evidenceID := fmt.Sprintf("ev_selector_%d", index)
							candidate["evidence_id"] = evidenceID
							candidate["source_kind"] = "page_scan"
							candidate["source_digest"] = SHA256Hex([]byte(fmt.Sprintf("%v:%v:%d", candidate["kind"], candidate["value"], index)))
							candidate["observed_role"] = "button"
							candidate["observed_accessible_name"] = fmt.Sprintf("Approved target %d", index)
							candidate["observed_at"] = observedAt.UTC().Format(time.RFC3339Nano)
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
}

func mutateFirstSelectorCandidateForTest(t *testing.T, pkg *ClientExecutionPackage, mutate func(*SelectorCandidate)) {
	t.Helper()
	for stageIndex := range pkg.ExecutableScriptBundle.ScriptOutline.Stages {
		stage := &pkg.ExecutableScriptBundle.ScriptOutline.Stages[stageIndex]
		for componentIndex := range stage.Components {
			candidates := stage.Components[componentIndex].SelectorAlternatives
			if len(candidates) > 0 {
				mutate(&candidates[0])
				stage.Components[componentIndex].SelectorAlternatives = candidates
				return
			}
		}
	}
	t.Fatal("test package has no selector candidate")
}

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
