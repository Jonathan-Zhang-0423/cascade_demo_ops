package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"cascade-demoops/backend/internal/direct"
	"cascade-demoops/backend/internal/model"
)

type directResultContractError struct {
	code    string
	message string
}

func (e *directResultContractError) Error() string { return e.message }

func newDirectResultContractError(code, format string, args ...any) error {
	return &directResultContractError{code: code, message: fmt.Sprintf(format, args...)}
}

func directResultContractErrorCode(err error) string {
	var contractErr *directResultContractError
	if errors.As(err, &contractErr) && contractErr.code != "" {
		return contractErr.code
	}
	return "result_validation_failed"
}

func validateDirectStructuredArtifacts(job *direct.Job, source model.ClientExecutionPackage, result model.RecordingResultPackage) error {
	if result.Classification != "" && result.Classification != model.RecordingResultClassificationFormalAppDirect && result.Classification != model.RecordingResultClassificationFailedLoginGate {
		return newDirectResultContractError("result_classification_invalid", "formal Direct result classification must be formal_app_direct or failed_login_gate, got %q", result.Classification)
	}
	manifestRef, ok := findDirectResultArtifact(result, "replay_manifest")
	if !ok {
		return newDirectResultContractError("result_artifact_completeness_failed", "direct result is missing replay manifest")
	}
	manifestArtifact, ok := job.Artifacts[manifestRef.ID]
	if !ok || len(manifestArtifact.Bytes) == 0 {
		return newDirectResultContractError("result_artifact_completeness_failed", "replay manifest bytes are unavailable")
	}
	var manifest model.ReplayManifest
	if err := json.Unmarshal(manifestArtifact.Bytes, &manifest); err != nil {
		return newDirectResultContractError("result_artifact_content_invalid", "decode replay manifest: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		return newDirectResultContractError("result_artifact_content_invalid", "validate replay manifest: %v", err)
	}
	if source.ExecutableScriptBundle == nil || source.ExecutableScriptBundle.StageApprovalPlan == nil {
		return newDirectResultContractError("result_artifact_content_invalid", "approved stage plan is unavailable")
	}
	bundle := source.ExecutableScriptBundle
	if manifest.PackageID != source.PackageID || manifest.BundleHashSHA256 != bundle.Reproducibility.BundleHashSHA256 || manifest.PolicyHashSHA256 != bundle.Reproducibility.BrowserAgentContractHashSHA256 {
		return newDirectResultContractError("result_artifact_content_invalid", "replay manifest package, bundle, or policy binding is invalid")
	}
	if manifest.ProtocolRuntime != bundle.ScriptManifest.Runtime || manifest.ExecutionBundleRuntime != result.ExecutionRuntime || manifest.ProtocolRuntime != manifest.ExecutionBundleRuntime {
		return newDirectResultContractError("result_artifact_content_invalid", "replay manifest runtime binding is invalid")
	}
	if manifest.ManifestURI != directArtifactURI(job.Status.JobID, manifestRef.ID) {
		return newDirectResultContractError("result_artifact_content_invalid", "replay manifest URI is not bound to the current job")
	}
	expectedManifestStatus := "success"
	if result.Status == model.RecordingResultStatusFailed {
		expectedManifestStatus = "failed"
		if result.FailureDiagnostic == nil || manifest.FailedNodeID != result.FailureDiagnostic.FailedNodeID || manifest.FinalDecision == model.ValidationDecisionContinue {
			return newDirectResultContractError("result_artifact_content_invalid", "failed replay manifest does not match the authoritative failure diagnostic")
		}
	}
	if manifest.Status != expectedManifestStatus {
		return newDirectResultContractError("result_artifact_content_invalid", "replay manifest status does not match recording result")
	}
	if result.StageEventLogRef == nil {
		return newDirectResultContractError("result_artifact_completeness_failed", "stage event log reference is missing")
	}
	eventArtifact, ok := job.Artifacts[result.StageEventLogRef.ID]
	if !ok || len(eventArtifact.Bytes) == 0 {
		return newDirectResultContractError("result_artifact_completeness_failed", "stage event log bytes are unavailable")
	}
	events, err := decodeDirectStageEventBytes(eventArtifact.Bytes)
	if err != nil {
		return newDirectResultContractError("result_artifact_content_invalid", "%v", err)
	}
	if len(events) == 0 {
		return newDirectResultContractError("result_artifact_content_invalid", "stage event log is empty")
	}
	if manifest.RunID != events[0].RunID || manifest.StageEventLogURI != result.StageEventLogRef.URI {
		return newDirectResultContractError("result_artifact_content_invalid", "replay manifest stage event binding is invalid")
	}

	approved := map[string]model.StageApprovalStage{}
	for _, stage := range bundle.StageApprovalPlan.Stages {
		approved[stage.NodeID] = stage
	}
	eventIDs := map[string]bool{}
	outcomeEvidence := map[string]bool{}
	completed := map[string]bool{}
	failed := map[string]bool{}
	geometryByNode := map[string]model.BrowserTargetGeometry{}
	var previousSequence int64
	for _, event := range events {
		if eventIDs[event.EventID] || event.Sequence <= previousSequence {
			return newDirectResultContractError("result_artifact_content_invalid", "stage event IDs must be unique and sequence must strictly increase")
		}
		eventIDs[event.EventID] = true
		previousSequence = event.Sequence
		stage, exists := approved[event.NodeID]
		if !exists || stage.ID != event.StageID {
			return newDirectResultContractError("result_artifact_content_invalid", "stage event references an unapproved node or stage")
		}
		if event.RunID != manifest.RunID || event.SourcePackageID != source.PackageID || event.SourceBundleHashSHA256 != manifest.BundleHashSHA256 || event.PolicyHashSHA256 != manifest.PolicyHashSHA256 {
			return newDirectResultContractError("result_artifact_content_invalid", "stage event identity does not match replay manifest")
		}
		switch event.EventType {
		case model.StageExecutionEventOutcomeObserved:
			outcomeEvidence[event.NodeID] = len(event.EvidenceRefs) > 0
		case model.StageExecutionEventStageCompleted:
			completed[event.NodeID] = true
			if model.RuntimeObservationRecordsOptionalCapability(event.Observation) && len(event.EvidenceRefs) > 0 {
				outcomeEvidence[event.NodeID] = true
			}
		case model.StageExecutionEventStageFailed:
			failed[event.NodeID] = true
		}
		if event.Observation != nil && event.Observation.TargetGeometry != nil {
			geometryByNode[event.NodeID] = *event.Observation.TargetGeometry
		}
	}

	manifestStages := map[string]model.ReplayManifestStage{}
	for _, stage := range manifest.Stages {
		manifestStages[stage.NodeID] = stage
	}
	for _, step := range result.StepResults {
		stage, approvedStage := approved[step.NodeID]
		manifestStage, manifestStageExists := manifestStages[step.NodeID]
		if !approvedStage || !manifestStageExists || manifestStage.StageID != stage.ID || manifestStage.Status != step.Status {
			return newDirectResultContractError("result_artifact_content_invalid", "replay manifest does not match the result step for node %q", step.NodeID)
		}
		if geometry, hasGeometry := geometryByNode[step.NodeID]; hasGeometry {
			if geometry.ScreenshotArtifactID == "" || !containsDirectString(manifestStage.BeforeScreenshotArtifactIDs, geometry.ScreenshotArtifactID) {
				return newDirectResultContractError("result_artifact_content_invalid", "target geometry for node %q is not bound to its pre-action screenshot", step.NodeID)
			}
			if artifact, exists := directResultArtifactByID(result, geometry.ScreenshotArtifactID); exists && !directArtifactViewportMatches(artifact, geometry.Viewport) {
				return newDirectResultContractError("result_artifact_content_invalid", "target geometry viewport does not match screenshot dimensions for node %q", step.NodeID)
			}
		}
		switch step.Status {
		case "passed":
			if !outcomeEvidence[step.NodeID] || !completed[step.NodeID] {
				return newDirectResultContractError("result_artifact_content_invalid", "completed node %q is missing outcome evidence or terminal event", step.NodeID)
			}
		case "failed":
			if !failed[step.NodeID] {
				return newDirectResultContractError("result_artifact_content_invalid", "failed node %q is missing stage_failed event", step.NodeID)
			}
		}
	}

	reportIDs := map[string]bool{}
	for _, ref := range manifest.ValidationReports {
		reportIDs[ref.ReportID] = true
	}
	for _, report := range result.ValidationReports {
		if !reportIDs[report.ReportID] {
			return newDirectResultContractError("result_artifact_content_invalid", "replay manifest does not index validation report %q", report.ReportID)
		}
	}
	infrastructureFailure := result.Status == model.RecordingResultStatusFailed && result.FailureDiagnostic != nil && directInfrastructureFailureCode(result.FailureDiagnostic.Error.Code)
	if !infrastructureFailure && source.RecordingRunSpec.Outputs.RawRecording && !manifestReferencesDirectKind(manifest.RawRecordingURI, result, "raw_recording") {
		return newDirectResultContractError("result_artifact_content_invalid", "replay manifest does not reference the requested raw recording")
	}
	if result.Status != model.RecordingResultStatusFailed && source.RecordingRunSpec.Outputs.FinalVideo && !manifestReferencesDirectKind(manifest.MP4URI, result, "demo_video") {
		return newDirectResultContractError("result_artifact_content_invalid", "replay manifest does not reference the requested final video")
	}
	if !infrastructureFailure && source.RecordingRunSpec.Outputs.Trace && !manifestReferencesAnyDirectKind(manifest.BrowserTraceURI, result, []string{"browser_trace", "execution_trace"}) {
		return newDirectResultContractError("result_artifact_content_invalid", "replay manifest does not reference the requested browser trace")
	}
	return nil
}

func containsDirectString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func directResultArtifactByID(result model.RecordingResultPackage, id string) (model.ArtifactRef, bool) {
	for _, artifact := range directResultArtifactRefs(result) {
		if artifact.ID == id {
			return artifact, true
		}
	}
	return model.ArtifactRef{}, false
}

// directArtifactViewportMatches checks only declared, non-sensitive image
// dimensions. Missing dimensions remain backward compatible; when supplied,
// a mismatch is a hard evidence-contract error.
func directArtifactViewportMatches(artifact model.ArtifactRef, viewport model.BrowserGeometryViewport) bool {
	if artifact.Metadata == nil {
		return true
	}
	width, hasWidth := directArtifactNumber(artifact.Metadata["width"])
	height, hasHeight := directArtifactNumber(artifact.Metadata["height"])
	if !hasWidth || !hasHeight {
		return true
	}
	return width == viewport.Width && height == viewport.Height
}

func directArtifactNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func decodeDirectStageEventBytes(data []byte) ([]model.StageExecutionEvent, error) {
	var events []model.StageExecutionEvent
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event model.StageExecutionEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, fmt.Errorf("decode stage event: %w", err)
		}
		if err := event.Validate(); err != nil {
			return nil, fmt.Errorf("validate stage event: %w", err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read stage event log: %w", err)
	}
	return events, nil
}

func findDirectResultArtifact(result model.RecordingResultPackage, kind string) (model.ArtifactRef, bool) {
	for _, ref := range directResultArtifactRefs(result) {
		if strings.EqualFold(ref.Kind, kind) {
			return ref, true
		}
	}
	return model.ArtifactRef{}, false
}

func manifestReferencesDirectKind(uri string, result model.RecordingResultPackage, kind string) bool {
	return manifestReferencesAnyDirectKind(uri, result, []string{kind})
}

func manifestReferencesAnyDirectKind(uri string, result model.RecordingResultPackage, kinds []string) bool {
	for _, ref := range directResultArtifactRefs(result) {
		for _, kind := range kinds {
			if strings.EqualFold(ref.Kind, kind) && ref.URI == uri {
				return true
			}
		}
	}
	return false
}
