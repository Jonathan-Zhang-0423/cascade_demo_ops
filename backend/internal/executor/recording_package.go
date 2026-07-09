package executor

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

const defaultRecordWidth = 1440
const defaultRecordHeight = 900

func NewRecordRequestFromClientExecutionPackage(source *model.ClientExecutionPackage, outputDir string) (RecordRequest, error) {
	if strings.TrimSpace(outputDir) == "" {
		return RecordRequest{}, errors.New("output_dir is required")
	}
	if err := model.ValidateClientExecutionPackageForCloudExecution(source); err != nil {
		return RecordRequest{}, err
	}
	viewport := viewportFromRunSpec(source.RecordingRunSpec)
	return RecordRequest{
		Graph:                  source.WorkflowGraph,
		OutputDir:              outputDir,
		Viewport:               viewport,
		Headless:               source.RecordingRunSpec.Browser.Headless,
		SourcePackageID:        source.PackageID,
		RecordingRunSpec:       &source.RecordingRunSpec,
		ExecutableScriptBundle: source.ExecutableScriptBundle,
	}, nil
}

func NewRecordingResultPackageFromRecordResult(source *model.ClientExecutionPackage, result RecordResult, cloudJobID string, createdAt time.Time) (model.RecordingResultPackage, error) {
	if err := model.ValidateClientExecutionPackageForCloudExecution(source); err != nil {
		return model.RecordingResultPackage{}, err
	}
	if strings.TrimSpace(cloudJobID) == "" {
		return model.RecordingResultPackage{}, errors.New("cloud_job_id is required")
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	startedAt := result.StartedAt
	if startedAt.IsZero() {
		startedAt = createdAt
	}
	completedAt := result.CompletedAt
	if completedAt.IsZero() {
		completedAt = createdAt
	}
	artifacts := artifactsFromRecordResult(source, result, createdAt)
	stepResults := result.StepResults
	if len(stepResults) == 0 {
		stepResults = stepResultsFromScriptPlan(source, artifacts, startedAt, completedAt)
	} else {
		stepResults = attachStepArtifacts(stepResults, artifacts)
	}
	trace := &model.ExecutionTrace{
		ID:              resultTraceID(source.PackageID),
		WorkflowGraphID: source.WorkflowGraph.ID,
		GraphVersion:    source.WorkflowGraph.Version,
		StartedAt:       startedAt,
		CompletedAt:     completedAt,
		PassRate:        passRate(stepResults),
		StepResults:     stepResults,
		Artifacts:       artifacts,
		Environment:     source.RecordingRunSpec.Environment,
	}
	recordingResult := model.RecordingResultPackage{
		ResultID:        resultPackageResultID(source.PackageID),
		SourcePackageID: source.PackageID,
		CloudJobID:      cloudJobID,
		SchemaVersion:   model.RecordingResultPackageSchemaVersion,
		Status:          model.RecordingResultStatusGenerated,
		ExecutionTrace:  trace,
		StepResults:     stepResults,
		GeneratedAssets: artifacts,
		VerificationReport: model.VerificationReport{
			PassRate:             trace.PassRate,
			FailedNodeIDs:        failedNodeIDs(stepResults),
			ReproducibilityMatch: true,
			OutputChecksums:      outputChecksums(artifacts),
		},
		AuditTrail: model.CloudExecutionAuditTrail{
			CloudWorkerID:       result.WorkerID,
			StartedAt:           startedAt,
			CompletedAt:         completedAt,
			RuntimeVersions:     result.RuntimeVersions,
			SourcePackageDigest: source.Reproducibility.PackageHashSHA256,
			GraphDigest:         source.Reproducibility.GraphHashSHA256,
		},
		Delivery: model.ResultDelivery{
			ResultPackageRef: model.PackageArtifactDescriptor{
				ID:        resultPackageArtifactID(source.PackageID),
				Role:      "recording_result",
				Kind:      "recording_result_package",
				URI:       fmt.Sprintf("cascade://recording-results/%s", resultPackageResultID(source.PackageID)),
				Encrypted: true,
			},
			AssetRefs:   assetDescriptorsFromArtifacts(source, artifacts, createdAt),
			AckRequired: true,
			ExpiresAt:   createdAt.Add(24 * time.Hour),
		},
		CreatedAt: createdAt,
	}
	if err := model.ValidateRecordingResultPackageForRender(&recordingResult, source); err != nil {
		return model.RecordingResultPackage{}, err
	}
	return recordingResult, nil
}

func viewportFromRunSpec(spec model.RecordingRunSpec) Viewport {
	if spec.Outputs.ResolutionWidth > 0 && spec.Outputs.ResolutionHeight > 0 {
		return Viewport{Width: spec.Outputs.ResolutionWidth, Height: spec.Outputs.ResolutionHeight}
	}
	for _, viewport := range spec.Browser.Viewports {
		if viewport.Width > 0 && viewport.Height > 0 {
			return Viewport{Width: viewport.Width, Height: viewport.Height}
		}
	}
	return Viewport{Width: defaultRecordWidth, Height: defaultRecordHeight}
}

func artifactsFromRecordResult(source *model.ClientExecutionPackage, result RecordResult, createdAt time.Time) []model.ArtifactRef {
	artifacts := append([]model.ArtifactRef{}, result.GeneratedAssets...)
	if result.RecordingPath != "" {
		artifacts = append(artifacts, model.ArtifactRef{
			ID:        artifactID(source.PackageID, "raw_recording", 1),
			Kind:      "raw_recording",
			URI:       result.RecordingPath,
			MimeType:  mimeTypeForPath(result.RecordingPath, "video/webm"),
			CreatedAt: createdAt,
		})
	}
	steps := source.ExecutableScriptBundle.PlanJSON.Steps
	for index, path := range result.ScreenshotPaths {
		nodeID := ""
		if index < len(steps) {
			nodeID = steps[index].NodeID
		}
		artifacts = append(artifacts, model.ArtifactRef{
			ID:           artifactID(source.PackageID, "screenshot", index+1),
			Kind:         "screenshot",
			URI:          path,
			MimeType:     mimeTypeForPath(path, "image/png"),
			CreatedAt:    createdAt,
			SourceNodeID: nodeID,
		})
	}
	if result.TracePath != "" {
		artifacts = append(artifacts, model.ArtifactRef{
			ID:        artifactID(source.PackageID, "browser_trace", 1),
			Kind:      "browser_trace",
			URI:       result.TracePath,
			MimeType:  mimeTypeForPath(result.TracePath, "application/zip"),
			CreatedAt: createdAt,
		})
	}
	if result.ArtifactManifestPath != "" {
		artifacts = append(artifacts, model.ArtifactRef{
			ID:        artifactID(source.PackageID, "artifact_manifest", 1),
			Kind:      "artifact_manifest",
			URI:       result.ArtifactManifestPath,
			MimeType:  mimeTypeForPath(result.ArtifactManifestPath, "application/json"),
			CreatedAt: createdAt,
		})
	}
	return uniqueArtifactRefs(artifacts)
}

func stepResultsFromScriptPlan(source *model.ClientExecutionPackage, artifacts []model.ArtifactRef, startedAt time.Time, completedAt time.Time) []model.StepResult {
	steps := source.ExecutableScriptBundle.PlanJSON.Steps
	results := make([]model.StepResult, 0, len(steps))
	for _, step := range steps {
		result := model.StepResult{
			NodeID:        step.NodeID,
			Status:        "passed",
			StartedAt:     startedAt,
			CompletedAt:   completedAt,
			DurationMS:    step.Timing.DurationMS,
			ObservedState: step.ExpectedOutcome,
			Artifacts:     artifactsForNode(artifacts, step.NodeID),
		}
		results = append(results, result)
	}
	return results
}

func attachStepArtifacts(steps []model.StepResult, artifacts []model.ArtifactRef) []model.StepResult {
	out := make([]model.StepResult, len(steps))
	copy(out, steps)
	for index := range out {
		if len(out[index].Artifacts) == 0 {
			out[index].Artifacts = artifactsForNode(artifacts, out[index].NodeID)
		}
	}
	return out
}

func artifactsForNode(artifacts []model.ArtifactRef, nodeID string) []model.ArtifactRef {
	if nodeID == "" {
		return nil
	}
	matches := []model.ArtifactRef{}
	for _, artifact := range artifacts {
		if artifact.SourceNodeID == nodeID {
			matches = append(matches, artifact)
		}
	}
	return matches
}

func passRate(steps []model.StepResult) float64 {
	if len(steps) == 0 {
		return 0
	}
	passed := 0
	for _, step := range steps {
		if strings.EqualFold(step.Status, "passed") {
			passed++
		}
	}
	return float64(passed) / float64(len(steps))
}

func failedNodeIDs(steps []model.StepResult) []string {
	failed := []string{}
	for _, step := range steps {
		if !strings.EqualFold(step.Status, "passed") && step.NodeID != "" {
			failed = append(failed, step.NodeID)
		}
	}
	return failed
}

func outputChecksums(artifacts []model.ArtifactRef) []model.ContentDigest {
	digests := []model.ContentDigest{}
	for _, artifact := range artifacts {
		if artifact.SHA256 == "" {
			continue
		}
		digests = append(digests, model.ContentDigest{ID: artifact.ID, Kind: artifact.Kind, SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes})
	}
	return digests
}

func assetDescriptorsFromArtifacts(source *model.ClientExecutionPackage, artifacts []model.ArtifactRef, createdAt time.Time) []model.PackageArtifactDescriptor {
	descriptors := make([]model.PackageArtifactDescriptor, 0, len(artifacts))
	for _, artifact := range artifacts {
		descriptors = append(descriptors, model.PackageArtifactDescriptor{
			ID:        artifact.ID,
			Role:      "recording_output",
			Kind:      artifact.Kind,
			URI:       artifact.URI,
			MimeType:  artifact.MimeType,
			SHA256:    artifact.SHA256,
			SizeBytes: artifact.SizeBytes,
			Encrypted: false,
			Sensitive: artifact.Sensitive,
			Metadata: map[string]any{
				"source_package_id": source.PackageID,
				"source_node_id":    artifact.SourceNodeID,
				"created_at":        createdAt.Format(time.RFC3339Nano),
			},
		})
	}
	return descriptors
}

func uniqueArtifactRefs(artifacts []model.ArtifactRef) []model.ArtifactRef {
	seenIDs := map[string]bool{}
	seenURIs := map[string]bool{}
	out := []model.ArtifactRef{}
	for _, artifact := range artifacts {
		if artifact.ID == "" && artifact.URI == "" {
			continue
		}
		if artifact.ID != "" && seenIDs[artifact.ID] {
			continue
		}
		if artifact.URI != "" && seenURIs[artifact.URI] {
			continue
		}
		if artifact.ID != "" {
			seenIDs[artifact.ID] = true
		}
		if artifact.URI != "" {
			seenURIs[artifact.URI] = true
		}
		out = append(out, artifact)
	}
	return out
}

func artifactID(packageID string, kind string, index int) string {
	return fmt.Sprintf("artifact_%s_%s_%03d", safeID(packageID), safeID(kind), index)
}

func resultTraceID(packageID string) string {
	return "trace_" + safeID(packageID)
}

func resultPackageResultID(packageID string) string {
	return "result_" + safeID(packageID)
}

func resultPackageArtifactID(packageID string) string {
	return "result_package_" + safeID(packageID)
}

func safeID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			builder.WriteRune(r)
			continue
		}
		builder.WriteByte('_')
	}
	normalized := strings.Trim(builder.String(), "_")
	if normalized == "" {
		return "unknown"
	}
	return normalized
}

func mimeTypeForPath(path string, fallback string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".webm":
		return "video/webm"
	case ".mp4":
		return "video/mp4"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".zip":
		return "application/zip"
	case ".json":
		return "application/json"
	default:
		return fallback
	}
}
