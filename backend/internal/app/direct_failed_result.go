package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/model"
)

func (s *DirectHTTPServer) directRuntimeFailureResult(ctx context.Context, pkg *model.ClientExecutionPackage, jobID, recordingDir string, existing model.RecordingResultPackage, runErr error, createdAt time.Time) (model.RecordingResultPackage, error) {
	if existing.ResultID != "" {
		markDirectResultAsRuntimeFailure(pkg, &existing, runErr, createdAt)
		return existing, nil
	}
	if pkg == nil || pkg.ExecutableScriptBundle == nil || pkg.ExecutableScriptBundle.StageApprovalPlan == nil || len(pkg.ExecutableScriptBundle.StageApprovalPlan.Stages) == 0 {
		return model.RecordingResultPackage{}, errors.New("cannot package Direct runtime failure without an approved stage")
	}
	stage := pkg.ExecutableScriptBundle.StageApprovalPlan.Stages[0]

	// Phase 3: Extract error code from blocking validation checks first, fallback to Go error.
	code := extractFirstBlockingValidationCode(existing.ValidationReports)
	if code == "" {
		code = runtimeExecutionErrorCode(runErr)
	}

	// Attach all blocking validation reports to diagnostic for deep traceability.
	blockingReports := filterBlockingReports(existing.ValidationReports)
	browserEvidenceUnavailable := directInfrastructureFailureCode(code)

	diagnostic := &model.ScriptFailureDiagnostic{
		ID: "diag_" + safePathSegment(stage.NodeID), SchemaVersion: model.ScriptFailureDiagnosticSchemaVersion,
		SourcePackageID: pkg.PackageID, CloudJobID: jobID, FailedNodeID: stage.NodeID, FailedStepOrder: stage.Order, Attempt: 1,
		Error:                      model.AgentError{Code: code, Message: "Server runtime failed before browser evidence was available."},
		RedactionReport:            model.DiagnosticRedactionReport{Applied: true, PolicyRef: pkg.PackageID + ".redactions", FullHTMLIncluded: false},
		BrowserEvidenceUnavailable: browserEvidenceUnavailable,
		CapturedAt:                 createdAt,
	}
	// Embed blocking ValidationReports into diagnostic (Phase 2).
	if len(blockingReports) > 0 {
		// Annotate checks with Impact/Suggestion/NextStep metadata before embedding
		for i := range blockingReports {
			model.AnnotateValidationChecks(blockingReports[i].Checks)
		}
		diagnostic.ValidationReports = blockingReports
	}

	step := model.StepResult{
		NodeID: stage.NodeID, Status: "failed", StartedAt: createdAt, CompletedAt: createdAt,
		ObservedState: "source=artifact_observation; infrastructure_failure=" + code,
		Error:         &model.AgentError{Code: code, Message: "Server runtime failed before browser evidence was available."},
	}
	result, err := executor.NewRecordingResultPackageFromRecordResult(pkg, executor.RecordResult{
		StepResults: []model.StepResult{step}, FailureDiagnostic: diagnostic,
		WorkerID: "direct-browser-agent-worker", StartedAt: createdAt, CompletedAt: createdAt,
	}, jobID, createdAt)
	if err != nil {
		return model.RecordingResultPackage{}, err
	}
	eventLog, err := newStageEventAuditLog(recordingDir, jobID)
	if err != nil || eventLog == nil {
		if err != nil {
			return model.RecordingResultPackage{}, fmt.Errorf("cannot create Direct infrastructure failure event log: %w", err)
		}
		return model.RecordingResultPackage{}, errors.New("cannot create Direct infrastructure failure event log: recording directory is empty")
	}
	event := model.StageExecutionEvent{
		SchemaVersion: model.StageExecutionEventSchemaVersion, EventID: "event_" + safePathSegment(jobID) + "_infrastructure_failed",
		RunID: jobID, SourcePackageID: pkg.PackageID,
		SourceBundleHashSHA256: pkg.ExecutableScriptBundle.Reproducibility.BundleHashSHA256,
		PolicyHashSHA256:       pkg.ExecutableScriptBundle.Reproducibility.BrowserAgentContractHashSHA256,
		NodeID:                 stage.NodeID, StageID: stage.ID, Attempt: 1, Sequence: 1,
		EventType: model.StageExecutionEventStageFailed, OccurredAt: createdAt,
	}
	if err := eventLog.Append(ctx, event); err != nil {
		return model.RecordingResultPackage{}, err
	}
	artifact, err := eventLog.ArtifactRef()
	if err != nil {
		return model.RecordingResultPackage{}, err
	}
	result.StageEventLogRef = &artifact
	return result, nil
}

func markDirectResultAsRuntimeFailure(pkg *model.ClientExecutionPackage, result *model.RecordingResultPackage, runErr error, createdAt time.Time) {
	if result == nil {
		return
	}
	code := runtimeExecutionErrorCode(runErr)
	result.Status = model.RecordingResultStatusFailed
	removeDirectFinalVideoRefs(result)
	screenshots := directFailureArtifactDescriptors(*result, []string{"screenshot", "step_screenshot", "webpage_screenshot"})
	traces := directFailureArtifactDescriptors(*result, []string{"browser_trace", "execution_trace"})
	result.FailureDiagnostic = &model.ScriptFailureDiagnostic{
		ID: "diag_runtime_delivery", SchemaVersion: model.ScriptFailureDiagnosticSchemaVersion,
		SourcePackageID: result.SourcePackageID, CloudJobID: result.CloudJobID, FailedNodeID: "runtime_delivery", Attempt: 1,
		Error:          model.AgentError{Code: code, Message: "Server runtime failed after browser execution completed."},
		ScreenshotRefs: screenshots, TraceRefs: traces,
		RedactionReport: model.DiagnosticRedactionReport{Applied: true, PolicyRef: result.SourcePackageID + ".redactions", FullHTMLIncluded: false},
		CapturedAt:      createdAt,
	}
	result.RepairRequest = &model.ScriptRepairRequest{
		ID: "repair_" + safePathSegment(result.ResultID), SourceResultID: result.ResultID,
		SourcePackageID: result.SourcePackageID, CloudJobID: result.CloudJobID,
		MaxRepairAttempts: 1, RepairAttempt: 1, ApprovalRequired: true,
		RequestedAt: createdAt, ExpiresAt: createdAt.Add(24 * time.Hour),
	}
	if pkg != nil && pkg.ExecutableScriptBundle != nil {
		result.RepairRequest.FailedBundleHashSHA256 = pkg.ExecutableScriptBundle.Reproducibility.BundleHashSHA256
		result.RepairRequest.FailedPlanHashSHA256 = pkg.ExecutableScriptBundle.Reproducibility.PlanHashSHA256
	}
}

func directFailureArtifactDescriptors(result model.RecordingResultPackage, kinds []string) []model.PackageArtifactDescriptor {
	allowed := map[string]bool{}
	for _, kind := range kinds {
		allowed[strings.ToLower(kind)] = true
	}
	var descriptors []model.PackageArtifactDescriptor
	seen := map[string]bool{}
	for _, ref := range directResultArtifactRefs(result) {
		if seen[ref.ID] || !allowed[strings.ToLower(ref.Kind)] {
			continue
		}
		seen[ref.ID] = true
		descriptors = append(descriptors, model.PackageArtifactDescriptor{
			ID: ref.ID, Kind: ref.Kind, URI: ref.URI, MimeType: ref.MimeType, SHA256: ref.SHA256, SizeBytes: ref.SizeBytes,
			Encrypted: true, Sensitive: true, RecipientKeyID: result.Delivery.RecipientKeyID, Metadata: ref.Metadata,
		})
	}
	return descriptors
}

func removeDirectFinalVideoRefs(result *model.RecordingResultPackage) {
	if result == nil {
		return
	}
	filterArtifacts := func(refs []model.ArtifactRef) []model.ArtifactRef {
		filtered := refs[:0]
		for _, ref := range refs {
			if !strings.EqualFold(ref.Kind, "demo_video") {
				filtered = append(filtered, ref)
			}
		}
		return filtered
	}
	result.GeneratedAssets = filterArtifacts(result.GeneratedAssets)
	if result.ExecutionTrace != nil {
		result.ExecutionTrace.Artifacts = filterArtifacts(result.ExecutionTrace.Artifacts)
	}
	filteredDelivery := result.Delivery.AssetRefs[:0]
	for _, ref := range result.Delivery.AssetRefs {
		if !strings.EqualFold(ref.Kind, "demo_video") {
			filteredDelivery = append(filteredDelivery, ref)
		}
	}
	result.Delivery.AssetRefs = filteredDelivery
}

func directInfrastructureFailureCode(code string) bool {
	switch strings.TrimSpace(code) {
	case runtimeErrorOutlineRunnerUnavailable,
		runtimeErrorVideoWorkerMissing,
		runtimeErrorNodeMissing,
		"browser_agent_session_start_failed",
		"browser_agent_session_close_failed",
		"stage_event_audit_unavailable",
		"outcome_pre_verification_failed":
		return true
	default:
		return false
	}
}

// extractFirstBlockingValidationCode returns the Code of the first blocking
// ValidationCheck that failed across all ValidationReports. Returns "" if no
// blocking failures exist.
func extractFirstBlockingValidationCode(reports []model.ValidationReport) string {
	for _, rpt := range reports {
		for _, chk := range rpt.Checks {
			if !chk.Passed && chk.Severity == model.FindingSeverityBlocking {
				return chk.Code
			}
		}
	}
	return ""
}

// filterBlockingReports returns a subset of reports containing at least one
// blocking failed check.
func filterBlockingReports(reports []model.ValidationReport) []model.ValidationReport {
	blocking := []model.ValidationReport{}
	for _, rpt := range reports {
		hasBlocking := false
		for _, chk := range rpt.Checks {
			if !chk.Passed && chk.Severity == model.FindingSeverityBlocking {
				hasBlocking = true
				break
			}
		}
		if hasBlocking {
			blocking = append(blocking, rpt)
		}
	}
	return blocking
}
