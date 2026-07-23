package executor

import (
	"errors"
	"fmt"
	"net/url"
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
	sandboxPolicy := model.ResolveSandboxPolicy(source.RecordingRunSpec, source.ExecutableScriptBundle)
	executableBundle := executableBundleWithGraphCaptureRequirements(source)
	return RecordRequest{
		Graph:                  source.WorkflowGraph,
		OutputDir:              outputDir,
		Viewport:               viewport,
		Headless:               source.RecordingRunSpec.Browser.Headless,
		RecordingMode:          defaultRecordingModeForPackage(source),
		SourcePackageID:        source.PackageID,
		RecordingRunSpec:       &source.RecordingRunSpec,
		SandboxPolicy:          &sandboxPolicy,
		ExecutableScriptBundle: executableBundle,
	}, nil
}

func executableBundleWithGraphCaptureRequirements(source *model.ClientExecutionPackage) *model.ExecutableRecordingScriptBundle {
	if source == nil || source.ExecutableScriptBundle == nil || source.ExecutableScriptBundle.PlanJSON == nil || source.WorkflowGraph == nil {
		if source == nil {
			return nil
		}
		return source.ExecutableScriptBundle
	}
	graphNodes := map[string]*model.GraphNode{}
	for _, node := range source.WorkflowGraph.Nodes {
		if node != nil && node.ID != "" {
			graphNodes[node.ID] = node
		}
	}
	if len(graphNodes) == 0 {
		return source.ExecutableScriptBundle
	}
	steps := source.ExecutableScriptBundle.PlanJSON.Steps
	updatedSteps := make([]model.ScriptStep, len(steps))
	changed := false
	for index, step := range steps {
		updated := step
		if node := graphNodes[step.NodeID]; nodeRequiresScreenshot(node) {
			updated.Capture = mergeGraphCaptureRequirement(updated.Capture, node)
			changed = true
		}
		updatedSteps[index] = updated
	}
	if !changed {
		return source.ExecutableScriptBundle
	}
	bundle := *source.ExecutableScriptBundle
	plan := *source.ExecutableScriptBundle.PlanJSON
	plan.Steps = updatedSteps
	bundle.PlanJSON = &plan
	return &bundle
}

func mergeGraphCaptureRequirement(stepCapture model.CaptureSpec, node *model.GraphNode) model.CaptureSpec {
	graphCapture := model.CaptureSpec{}
	if node != nil && node.Capture != nil {
		graphCapture = *node.Capture
	}
	disableDedupe := false
	stepCapture.Screenshot = true
	stepCapture.Dedupe = &disableDedupe
	if graphCapture.Video {
		stepCapture.Video = true
	}
	if graphCapture.Zoom {
		stepCapture.Zoom = true
	}
	if graphCapture.Callout {
		stepCapture.Callout = true
	}
	if stepCapture.Scope == "" && graphCapture.Scope != "" {
		stepCapture.Scope = graphCapture.Scope
	}
	if !stepCapture.FullPage && graphCapture.FullPage {
		stepCapture.FullPage = true
	}
	if stepCapture.FocusSelector == "" && graphCapture.FocusSelector != "" {
		stepCapture.FocusSelector = graphCapture.FocusSelector
	}
	if stepCapture.AssetRole == "" && graphCapture.AssetRole != "" {
		stepCapture.AssetRole = graphCapture.AssetRole
	}
	if stepCapture.Crop == nil && graphCapture.Crop != nil {
		crop := *graphCapture.Crop
		stepCapture.Crop = &crop
	}
	if len(stepCapture.MaskSelectors) == 0 && len(graphCapture.MaskSelectors) > 0 {
		stepCapture.MaskSelectors = append([]string{}, graphCapture.MaskSelectors...)
	}
	if len(stepCapture.Redactions) == 0 && len(graphCapture.Redactions) > 0 {
		stepCapture.Redactions = append([]model.RedactionSpec{}, graphCapture.Redactions...)
	}
	return stepCapture
}

func nodeRequiresScreenshot(node *model.GraphNode) bool {
	if node == nil {
		return false
	}
	return node.IsScreenshot || (node.Capture != nil && node.Capture.Screenshot)
}

func defaultRecordingModeForPackage(source *model.ClientExecutionPackage) RecordingMode {
	if source == nil || source.ExecutableScriptBundle == nil {
		return RecordingModeDryRun
	}
	return RecordingModePlaywright
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
	sandbox := sandboxMetadataForResult(source, result)
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
		Sandbox:         sandbox,
	}
	recordingResult := model.RecordingResultPackage{
		ResultID:         resultPackageResultID(source.PackageID),
		SourcePackageID:  source.PackageID,
		CloudJobID:       cloudJobID,
		SchemaVersion:    model.RecordingResultPackageSchemaVersion,
		Status:           model.RecordingResultStatusGenerated,
		ExecutionTrace:   trace,
		StepResults:      stepResults,
		GeneratedAssets:  artifacts,
		ExecutionRuntime: source.ExecutableScriptBundle.ScriptManifest.Runtime,
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
			Sandbox:             sandbox,
			SourcePackageDigest: source.Reproducibility.PackageHashSHA256,
			GraphDigest:         source.Reproducibility.GraphHashSHA256,
		},
		Delivery: model.ResultDelivery{
			ResultPackageRef: model.PackageArtifactDescriptor{
				ID:             resultPackageArtifactID(source.PackageID),
				Role:           "recording_result",
				Kind:           "recording_result_package",
				URI:            fmt.Sprintf("cascade://recording-results/%s", resultPackageResultID(source.PackageID)),
				SHA256:         resultPackageDigest(source),
				Encrypted:      true,
				Sensitive:      true,
				RecipientKeyID: resultRecipientKeyID(source),
			},
			AssetRefs:      assetDescriptorsFromArtifacts(source, artifacts, createdAt),
			RecipientKind:  model.ResultRecipientAppInstallation,
			RecipientKeyID: resultRecipientKeyID(source),
			EncryptionAlg:  model.CryptoSuiteXChaCha20Poly1305,
			AckRequired:    true,
			ExpiresAt:      createdAt.Add(24 * time.Hour),
		},
		CreatedAt: createdAt,
	}
	if result.FailureDiagnostic != nil || hasFailedStep(stepResults) {
		applyFailureRecordingResult(source, &recordingResult, result.FailureDiagnostic, stepResults, artifacts, createdAt)
		return recordingResult, nil
	}
	if err := model.ValidateRecordingResultPackageForRender(&recordingResult, source); err != nil {
		return model.RecordingResultPackage{}, err
	}
	return recordingResult, nil
}

func sandboxMetadataForResult(source *model.ClientExecutionPackage, result RecordResult) *model.SandboxExecutionMetadata {
	if result.SandboxMetadata != nil {
		metadata := *result.SandboxMetadata
		if len(metadata.RuntimeVersions) == 0 {
			metadata.RuntimeVersions = result.RuntimeVersions
		}
		return &metadata
	}
	if source == nil {
		return nil
	}
	policy := model.ResolveSandboxPolicy(source.RecordingRunSpec, source.ExecutableScriptBundle)
	return &model.SandboxExecutionMetadata{
		PolicyHashSHA256: policy.PolicyHashSHA256,
		Profile:          policy.Profile,
		IsolationMode:    policy.IsolationMode,
		NetworkMode:      policy.NetworkPolicy.Mode,
		WorkerID:         result.WorkerID,
		RuntimeVersions:  result.RuntimeVersions,
	}
}

func applyFailureRecordingResult(source *model.ClientExecutionPackage, result *model.RecordingResultPackage, diagnostic *model.ScriptFailureDiagnostic, steps []model.StepResult, artifacts []model.ArtifactRef, createdAt time.Time) {
	result.Status = model.RecordingResultStatusFailed
	result.VerificationReport.PassRate = passRate(steps)
	result.VerificationReport.FailedNodeIDs = failedNodeIDs(steps)
	if diagnostic == nil {
		diagnostic = diagnosticFromFailedStep(source, result.CloudJobID, steps, artifacts, createdAt)
	}
	normalizeFailureDiagnostic(source, result.CloudJobID, diagnostic, createdAt)
	result.FailureDiagnostic = diagnostic
	result.RepairRequest = repairRequestForFailure(source, result, diagnostic, createdAt)
}

func normalizeFailureDiagnostic(source *model.ClientExecutionPackage, cloudJobID string, diagnostic *model.ScriptFailureDiagnostic, capturedAt time.Time) {
	if diagnostic == nil {
		return
	}
	if diagnostic.ID == "" {
		diagnostic.ID = "diag_" + safeID(diagnostic.FailedNodeID)
	}
	diagnostic.SchemaVersion = model.ScriptFailureDiagnosticSchemaVersion
	if diagnostic.SourcePackageID == "" && source != nil {
		diagnostic.SourcePackageID = source.PackageID
	}
	if diagnostic.CloudJobID == "" {
		diagnostic.CloudJobID = cloudJobID
	}
	if diagnostic.Error.Code == "" {
		diagnostic.Error.Code = "playwright_step_failed"
	}
	if diagnostic.Error.Message == "" {
		diagnostic.Error.Message = "Playwright step failed."
	}
	if diagnostic.RedactionReport.PolicyRef == "" && source != nil {
		diagnostic.RedactionReport.PolicyRef = source.PackageID + ".redactions"
	}
	diagnostic.RedactionReport.Applied = true
	diagnostic.RedactionReport.FullHTMLIncluded = false
	if diagnostic.CapturedAt.IsZero() {
		diagnostic.CapturedAt = capturedAt
	}
}

func diagnosticFromFailedStep(source *model.ClientExecutionPackage, cloudJobID string, steps []model.StepResult, artifacts []model.ArtifactRef, capturedAt time.Time) *model.ScriptFailureDiagnostic {
	failed := firstFailedStep(steps)
	failedNodeID := "unknown"
	message := "Playwright step failed."
	if failed != nil {
		failedNodeID = failed.NodeID
		if failed.Error != nil && failed.Error.Message != "" {
			message = failed.Error.Message
		} else if failed.ObservedState != "" {
			message = failed.ObservedState
		}
	}
	diagnostic := &model.ScriptFailureDiagnostic{
		ID:              "diag_" + safeID(failedNodeID),
		SchemaVersion:   model.ScriptFailureDiagnosticSchemaVersion,
		SourcePackageID: source.PackageID,
		CloudJobID:      cloudJobID,
		FailedNodeID:    failedNodeID,
		Error:           model.AgentError{Code: classifyFailureMessage(message), Message: message, Retryable: true},
		RedactionReport: model.DiagnosticRedactionReport{Applied: true, PolicyRef: source.PackageID + ".redactions", FullHTMLIncluded: false},
		CapturedAt:      capturedAt,
	}
	if failed != nil {
		diagnostic.FailedStepOrder = stepOrderForNode(source, failed.NodeID)
	}
	for _, artifact := range artifactsForFailure(failedNodeID, artifacts) {
		if artifact.Kind == "failure_screenshot" || artifact.Kind == "screenshot" || artifact.Kind == "webpage_screenshot" {
			if diagnostic.CurrentURL == "" {
				diagnostic.CurrentURL = stringMetadata(artifact.Metadata, "current_url")
			}
			if diagnostic.PageTitle == "" {
				diagnostic.PageTitle = stringMetadata(artifact.Metadata, "page_title")
			}
			diagnostic.ScreenshotRefs = append(diagnostic.ScreenshotRefs, localDiagnosticArtifactDescriptor(source, artifact, "failure_screenshot"))
		}
		if artifact.Kind == "browser_trace" || artifact.Kind == "execution_trace" {
			diagnostic.TraceRefs = append(diagnostic.TraceRefs, localDiagnosticArtifactDescriptor(source, artifact, "failure_trace"))
		}
	}
	return diagnostic
}

func repairRequestForFailure(source *model.ClientExecutionPackage, result *model.RecordingResultPackage, diagnostic *model.ScriptFailureDiagnostic, requestedAt time.Time) *model.ScriptRepairRequest {
	repairAttempt := 1
	maxAttempts := 1
	if source != nil {
		maxAttempts = source.RecordingRunSpec.FailurePolicy.MaxRepairAttempts
		if maxAttempts <= 0 {
			maxAttempts = source.RecordingRunSpec.FailurePolicy.RetryAttempts
		}
	}
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	request := &model.ScriptRepairRequest{
		ID:                "repair_" + safeID(result.ResultID),
		SourceResultID:    result.ResultID,
		SourcePackageID:   result.SourcePackageID,
		CloudJobID:        result.CloudJobID,
		MaxRepairAttempts: maxAttempts,
		RepairAttempt:     repairAttempt,
		ApprovalRequired:  true,
		RequestedAt:       requestedAt,
		ExpiresAt:         requestedAt.Add(24 * time.Hour),
	}
	if source != nil && source.ExecutableScriptBundle != nil {
		request.FailedBundleHashSHA256 = source.ExecutableScriptBundle.Reproducibility.BundleHashSHA256
		request.FailedPlanHashSHA256 = source.ExecutableScriptBundle.Reproducibility.PlanHashSHA256
	}
	_ = diagnostic
	return request
}

func hasFailedStep(steps []model.StepResult) bool {
	return firstFailedStep(steps) != nil
}

func firstFailedStep(steps []model.StepResult) *model.StepResult {
	for index := range steps {
		if !strings.EqualFold(steps[index].Status, "passed") {
			return &steps[index]
		}
	}
	return nil
}

func stepOrderForNode(source *model.ClientExecutionPackage, nodeID string) int {
	if source == nil || source.ExecutableScriptBundle == nil || source.ExecutableScriptBundle.PlanJSON == nil || nodeID == "" {
		return 0
	}
	for _, step := range source.ExecutableScriptBundle.PlanJSON.Steps {
		if step.NodeID == nodeID {
			return step.Order
		}
	}
	return 0
}

func artifactsForFailure(nodeID string, artifacts []model.ArtifactRef) []model.ArtifactRef {
	matches := []model.ArtifactRef{}
	for _, artifact := range artifacts {
		if artifact.Kind == "browser_trace" || artifact.Kind == "execution_trace" {
			matches = append(matches, artifact)
			continue
		}
		if nodeID != "" && artifact.SourceNodeID == nodeID {
			matches = append(matches, artifact)
			continue
		}
		if artifact.Kind == "failure_screenshot" {
			matches = append(matches, artifact)
		}
	}
	return matches
}

func localDiagnosticArtifactDescriptor(source *model.ClientExecutionPackage, artifact model.ArtifactRef, role string) model.PackageArtifactDescriptor {
	metadata := map[string]any{"dev_local_artifact": true}
	for key, value := range artifact.Metadata {
		metadata[key] = value
	}
	return model.PackageArtifactDescriptor{
		ID:             artifact.ID,
		Role:           role,
		Kind:           artifact.Kind,
		URI:            artifact.URI,
		MimeType:       artifact.MimeType,
		SHA256:         firstNonEmptyString(artifact.SHA256, model.SHA256Hex([]byte(artifact.URI))),
		SizeBytes:      artifact.SizeBytes,
		Encrypted:      true,
		Sensitive:      true,
		RecipientKeyID: resultRecipientKeyID(source),
		Metadata:       metadata,
	}
}

func classifyFailureMessage(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "timeout"):
		return "selector_timeout"
	case strings.Contains(lower, "navigation") || strings.Contains(lower, "net::"):
		return "navigation_failed"
	case strings.Contains(lower, "selector"):
		return "selector_missing"
	default:
		return "playwright_step_failed"
	}
}

func stringMetadata(metadata map[string]any, key string) string {
	if value, ok := metadata[key].(string); ok {
		return value
	}
	return ""
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
	if result.RecordingPath != "" && !artifactExistsForPath(artifacts, result.RecordingPath) && !artifactExistsForKind(artifacts, "raw_recording") {
		artifacts = append(artifacts, model.ArtifactRef{
			ID:        artifactID(source.PackageID, "raw_recording", 1),
			Kind:      "raw_recording",
			URI:       result.RecordingPath,
			MimeType:  mimeTypeForPath(result.RecordingPath, "video/webm"),
			CreatedAt: createdAt,
			Metadata: map[string]any{
				"asset_role":      "raw_recording",
				"include_in_demo": true,
			},
		})
	}
	steps := source.ExecutableScriptBundle.PlanJSON.Steps
	for index, path := range result.ScreenshotPaths {
		nodeID := ""
		if index < len(steps) {
			nodeID = steps[index].NodeID
		}
		if artifactExistsForPath(artifacts, path) || artifactExistsForNodeKind(artifacts, nodeID, "screenshot") {
			continue
		}
		artifacts = append(artifacts, model.ArtifactRef{
			ID:           artifactID(source.PackageID, "screenshot", index+1),
			Kind:         "screenshot",
			URI:          path,
			MimeType:     mimeTypeForPath(path, "image/png"),
			CreatedAt:    createdAt,
			SourceNodeID: nodeID,
			Metadata: map[string]any{
				"asset_role":      "primary",
				"capture_scope":   "viewport",
				"include_in_demo": true,
			},
		})
	}
	if result.TracePath != "" && !artifactExistsForPath(artifacts, result.TracePath) && !artifactExistsForAnyKind(artifacts, "browser_trace", "execution_trace") {
		artifacts = append(artifacts, model.ArtifactRef{
			ID:        artifactID(source.PackageID, "browser_trace", 1),
			Kind:      "browser_trace",
			URI:       result.TracePath,
			MimeType:  mimeTypeForPath(result.TracePath, "application/zip"),
			CreatedAt: createdAt,
			Metadata: map[string]any{
				"asset_role":      "debug_trace",
				"include_in_demo": false,
			},
		})
	}
	if result.ArtifactManifestPath != "" && !artifactExistsForPath(artifacts, result.ArtifactManifestPath) && !artifactExistsForKind(artifacts, "artifact_manifest") {
		artifacts = append(artifacts, model.ArtifactRef{
			ID:        artifactID(source.PackageID, "artifact_manifest", 1),
			Kind:      "artifact_manifest",
			URI:       result.ArtifactManifestPath,
			MimeType:  mimeTypeForPath(result.ArtifactManifestPath, "application/json"),
			CreatedAt: createdAt,
			Metadata: map[string]any{
				"asset_role":      "debug_manifest",
				"include_in_demo": false,
			},
		})
	}
	return uniqueArtifactRefs(artifacts)
}

func artifactExistsForKind(artifacts []model.ArtifactRef, kind string) bool {
	for _, artifact := range artifacts {
		if artifact.Kind == kind {
			return true
		}
	}
	return false
}

func artifactExistsForAnyKind(artifacts []model.ArtifactRef, kinds ...string) bool {
	for _, kind := range kinds {
		if artifactExistsForKind(artifacts, kind) {
			return true
		}
	}
	return false
}

func artifactExistsForNodeKind(artifacts []model.ArtifactRef, nodeID string, kind string) bool {
	if nodeID == "" {
		return false
	}
	for _, artifact := range artifacts {
		if artifact.SourceNodeID == nodeID && artifact.Kind == kind {
			return true
		}
	}
	return false
}

func artifactExistsForPath(artifacts []model.ArtifactRef, path string) bool {
	key := normalizedArtifactURI(path)
	if key == "" {
		return false
	}
	for _, artifact := range artifacts {
		if normalizedArtifactURI(artifact.URI) == key {
			return true
		}
	}
	return false
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
		metadata := artifactDescriptorMetadata(source, artifact, createdAt)
		descriptors = append(descriptors, model.PackageArtifactDescriptor{
			ID:             artifact.ID,
			Role:           deliveryDescriptorRole(artifact),
			Kind:           deliveryDescriptorKind(artifact),
			URI:            artifact.URI,
			MimeType:       artifact.MimeType,
			SHA256:         firstNonEmptyString(artifact.SHA256, model.SHA256Hex([]byte(artifact.URI))),
			SizeBytes:      artifact.SizeBytes,
			Encrypted:      true,
			Sensitive:      true,
			RecipientKeyID: resultRecipientKeyID(source),
			Metadata:       metadata,
		})
	}
	return descriptors
}

func deliveryDescriptorRole(artifact model.ArtifactRef) string {
	if strings.EqualFold(artifact.Kind, "demo_video") {
		return model.ArtifactRoleFinalDemoVideo
	}
	return model.ArtifactRoleRecordingOutput
}

func deliveryDescriptorKind(artifact model.ArtifactRef) string {
	if strings.EqualFold(artifact.Kind, "demo_video") {
		return model.ArtifactKindVideo
	}
	return artifact.Kind
}

func resultRecipientKeyID(source *model.ClientExecutionPackage) string {
	if source != nil && source.ProjectContextSummary.ContextID != "" {
		return "app_installation:" + source.ProjectContextSummary.ContextID
	}
	return "app_installation:unknown"
}

func resultPackageDigest(source *model.ClientExecutionPackage) string {
	if source == nil {
		return ""
	}
	digest := source.Reproducibility.PackageHashSHA256
	if digest == "" {
		digest = source.Reproducibility.GraphHashSHA256
	}
	if digest == "" {
		digest = model.SHA256Hex([]byte(source.PackageID))
	}
	return digest
}

func artifactDescriptorMetadata(source *model.ClientExecutionPackage, artifact model.ArtifactRef, createdAt time.Time) map[string]any {
	metadata := map[string]any{}
	for key, value := range artifact.Metadata {
		metadata[key] = value
	}
	if source != nil {
		metadata["source_package_id"] = source.PackageID
	}
	if artifact.SourceNodeID != "" {
		metadata["source_node_id"] = artifact.SourceNodeID
	}
	metadata["created_at"] = createdAt.Format(time.RFC3339Nano)
	return metadata
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

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func normalizedArtifactURI(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) >= 2 && value[1] == ':' {
		return strings.ToLower(filepath.Clean(value))
	}
	parsed, err := url.Parse(value)
	if err == nil && parsed.Scheme == "file" {
		rawPath := parsed.Path
		if rawPath == "" {
			rawPath = parsed.Opaque
		}
		if parsed.Host != "" {
			if len(parsed.Host) == 2 && parsed.Host[1] == ':' {
				rawPath = parsed.Host + rawPath
			} else {
				rawPath = "//" + parsed.Host + rawPath
			}
		}
		if unescaped, unescapeErr := url.PathUnescape(rawPath); unescapeErr == nil {
			rawPath = unescaped
		}
		filePath := filepath.FromSlash(rawPath)
		if len(filePath) >= 3 && filePath[0] == filepath.Separator && filePath[2] == ':' {
			filePath = filePath[1:]
		}
		return strings.ToLower(filepath.Clean(filePath))
	}
	if err == nil && parsed.Scheme != "" {
		return value
	}
	return strings.ToLower(filepath.Clean(value))
}
