package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

func (s *DirectHTTPServer) finalizeDirectResult(_ context.Context, jobID, root string, pkg model.ClientExecutionPackage, result *model.RecordingResultPackage) error {
	if s == nil || s.service == nil || result == nil {
		return errors.New("direct result finalizer is not configured")
	}
	removeDirectReplayManifestRefs(result)
	events, err := s.readDirectStageEvents(*result)
	if err != nil {
		return err
	}
	if result.Status != model.RecordingResultStatusFailed && len(events) == 0 {
		return errors.New("direct successful result requires readable stage events")
	}

	// Upload using the runtime-local URIs first. Local paths must never survive
	// into the immutable result JSON returned to the App.
	synchronizeDirectResultChecksums(result)
	if err := s.uploadDirectResultArtifacts(jobID, *result); err != nil {
		return err
	}
	rewriteDirectResultURIs(jobID, result)

	runID := directResultRunID(*result, events)
	if runID == "" {
		return errors.New("direct result is missing a replay run identity")
	}
	createdAt := result.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	manifest, err := BuildReplayManifest(BuildReplayManifestInput{
		Result: *result, Events: events, Package: pkg, RunID: runID, CreatedAt: createdAt,
	})
	if err != nil {
		return err
	}
	validationRunReport, err := BuildValidationRunReport(*result, pkg, manifest)
	if err != nil {
		return fmt.Errorf("build validation run report: %w", err)
	}
	result.ValidationRunReport = &validationRunReport
	manifestArtifactID := manifest.ManifestID
	manifest.ManifestURI = directArtifactURI(jobID, manifestArtifactID)
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal Direct replay manifest: %w", err)
	}
	manifestJSON = append(manifestJSON, '\n')
	descriptor, err := s.gateway.AddArtifact(jobID, manifestArtifactID, "replay_manifest", "application/json", manifestJSON)
	if err != nil {
		return fmt.Errorf("upload Direct replay manifest: %w", err)
	}
	result.GeneratedAssets = append(result.GeneratedAssets, model.ArtifactRef{
		ID: manifestArtifactID, Kind: "replay_manifest", URI: manifest.ManifestURI,
		MimeType: "application/json", SHA256: descriptor.SHA256, SizeBytes: descriptor.Size,
	})
	result.Delivery.ResultPackageRef.ID = result.ResultID
	result.Delivery.ResultPackageRef.Kind = "recording_result_package"
	result.Delivery.ResultPackageRef.URI = directResultURI(jobID)
	result.Delivery.AckRequired = true
	synchronizeDirectResultChecksums(result)
	return validateDirectResultURIs(jobID, *result)
}

func (s *DirectHTTPServer) readDirectStageEvents(result model.RecordingResultPackage) ([]model.StageExecutionEvent, error) {
	if result.StageEventLogRef == nil || strings.TrimSpace(result.StageEventLogRef.URI) == "" {
		return nil, nil
	}
	path, err := s.service.localArtifactPath(result.StageEventLogRef.URI)
	if err != nil {
		return nil, fmt.Errorf("resolve Direct stage event log: %w", err)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open Direct stage event log: %w", err)
	}
	defer file.Close()
	var events []model.StageExecutionEvent
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event model.StageExecutionEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, fmt.Errorf("decode Direct stage event: %w", err)
		}
		if err := event.Validate(); err != nil {
			return nil, fmt.Errorf("validate Direct stage event: %w", err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read Direct stage event log: %w", err)
	}
	return events, nil
}

func directResultRunID(result model.RecordingResultPackage, events []model.StageExecutionEvent) string {
	if len(events) > 0 && strings.TrimSpace(events[0].RunID) != "" {
		return events[0].RunID
	}
	for _, report := range result.ValidationReports {
		if strings.TrimSpace(report.RunID) != "" {
			return report.RunID
		}
	}
	if result.ExecutionTrace != nil {
		return result.ExecutionTrace.ID
	}
	return ""
}

func directArtifactURI(jobID, artifactID string) string {
	return "direct://jobs/" + url.PathEscape(jobID) + "/artifacts/" + url.PathEscape(artifactID)
}

func directResultURI(jobID string) string {
	return "direct://jobs/" + url.PathEscape(jobID) + "/result"
}

func rewriteDirectResultURIs(jobID string, result *model.RecordingResultPackage) {
	if result == nil {
		return
	}
	rewriteRef := func(ref *model.ArtifactRef) {
		if ref != nil && strings.TrimSpace(ref.ID) != "" {
			ref.URI = directArtifactURI(jobID, ref.ID)
		}
	}
	for index := range result.GeneratedAssets {
		rewriteRef(&result.GeneratedAssets[index])
	}
	if result.ExecutionTrace != nil {
		for index := range result.ExecutionTrace.Artifacts {
			rewriteRef(&result.ExecutionTrace.Artifacts[index])
		}
	}
	for index := range result.StepResults {
		for artifactIndex := range result.StepResults[index].Artifacts {
			rewriteRef(&result.StepResults[index].Artifacts[artifactIndex])
		}
	}
	if result.StageEventLogRef != nil {
		rewriteRef(result.StageEventLogRef)
	}
	for index := range result.Delivery.AssetRefs {
		if result.Delivery.AssetRefs[index].ID != "" {
			result.Delivery.AssetRefs[index].URI = directArtifactURI(jobID, result.Delivery.AssetRefs[index].ID)
		}
	}
	if result.FailureDiagnostic != nil {
		rewriteDescriptor := func(descriptor *model.PackageArtifactDescriptor) {
			if descriptor != nil && descriptor.ID != "" {
				descriptor.URI = directArtifactURI(jobID, descriptor.ID)
			}
		}
		for index := range result.FailureDiagnostic.ScreenshotRefs {
			rewriteDescriptor(&result.FailureDiagnostic.ScreenshotRefs[index])
		}
		for index := range result.FailureDiagnostic.TraceRefs {
			rewriteDescriptor(&result.FailureDiagnostic.TraceRefs[index])
		}
		rewriteDescriptor(result.FailureDiagnostic.DOMSnapshotRef)
		rewriteDescriptor(result.FailureDiagnostic.AccessibilitySnapshotRef)
	}
	result.Delivery.ResultPackageRef.URI = directResultURI(jobID)
}

func validateDirectResultURIs(jobID string, result model.RecordingResultPackage) error {
	for _, ref := range directResultArtifactRefs(result) {
		lower := strings.ToLower(ref.URI)
		if strings.Contains(lower, "file://") || strings.Contains(lower, "dev_local_artifact") || strings.Contains(lower, "local-dev/result-key") || filepath.IsAbs(ref.URI) {
			return newDirectResultContractError("result_contains_local_runtime_data", "direct result artifact %q contains local runtime data", ref.ID)
		}
		if ref.URI != directArtifactURI(jobID, ref.ID) {
			return newDirectResultContractError("result_artifact_content_invalid", "direct result artifact %q is not bound to the current job", ref.ID)
		}
	}
	if result.Delivery.ResultPackageRef.URI != directResultURI(jobID) {
		return newDirectResultContractError("result_artifact_content_invalid", "direct result package URI is not bound to the current job")
	}
	if !directHasArtifact(directResultArtifactRefs(result), []string{"replay_manifest"}, "application/json") {
		return newDirectResultContractError("result_artifact_completeness_failed", "direct result requires a replay manifest")
	}
	return nil
}

func removeDirectReplayManifestRefs(result *model.RecordingResultPackage) {
	if result == nil {
		return
	}
	filter := func(refs []model.ArtifactRef) []model.ArtifactRef {
		filtered := refs[:0]
		for _, ref := range refs {
			if !strings.EqualFold(ref.Kind, "replay_manifest") {
				filtered = append(filtered, ref)
			}
		}
		return filtered
	}
	result.GeneratedAssets = filter(result.GeneratedAssets)
	if result.ExecutionTrace != nil {
		result.ExecutionTrace.Artifacts = filter(result.ExecutionTrace.Artifacts)
	}
	for index := range result.StepResults {
		result.StepResults[index].Artifacts = filter(result.StepResults[index].Artifacts)
	}
}
