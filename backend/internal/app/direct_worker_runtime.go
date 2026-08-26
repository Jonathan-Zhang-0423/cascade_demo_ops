package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"cascade-demoops/backend/internal/driver"
	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/model"
)

// DirectWorkerArtifactFile binds a real Browser Agent output to the local file
// that the Ubuntu worker must upload before submitting the result package.
type DirectWorkerArtifactFile struct {
	Artifact model.ArtifactRef
	Path     string
}

// RunDirectBrowserAgentJob executes only the approved Browser Agent outline
// runtime. It is the server-side worker entry point; it never invokes Exchange.
func (s *Service) RunDirectBrowserAgentJob(ctx context.Context, pkg *model.ClientExecutionPackage, jobID, outputRoot string, progress func(string, string, int)) (model.RecordingResultPackage, []DirectWorkerArtifactFile, error) {
	return s.RunDirectBrowserAgentJobWithCredentials(ctx, pkg, jobID, outputRoot, nil, progress)
}

func (s *Service) RunDirectBrowserAgentJobWithCredentials(ctx context.Context, pkg *model.ClientExecutionPackage, jobID, outputRoot string, credentials map[string]model.DirectCredentialValue, progress func(string, string, int)) (model.RecordingResultPackage, []DirectWorkerArtifactFile, error) {
	if s == nil || pkg == nil {
		return model.RecordingResultPackage{}, nil, errors.New("direct Browser Agent package is required")
	}
	if strings.TrimSpace(jobID) == "" || strings.TrimSpace(outputRoot) == "" {
		return model.RecordingResultPackage{}, nil, errors.New("direct Browser Agent job_id and output root are required")
	}
	if err := model.ValidateClientExecutionPackageForCloudExecution(pkg); err != nil {
		return model.RecordingResultPackage{}, nil, err
	}
	if pkg.ExecutableScriptBundle == nil || pkg.ExecutableScriptBundle.ScriptManifest.Runtime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 {
		return model.RecordingResultPackage{}, nil, errors.New("direct Browser Agent worker accepts only browser-agent-outline-v1")
	}
	root, err := filepath.Abs(filepath.Clean(outputRoot))
	if err != nil {
		return model.RecordingResultPackage{}, nil, err
	}
	recordingDir := filepath.Join(root, "recording")
	renderDir := filepath.Join(root, "render")
	for _, dir := range []string{recordingDir, renderDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return model.RecordingResultPackage{}, nil, err
		}
	}
	router := newExecutionRuntimeRouter(nil, s.outlineRunner)
	taskSecrets := make(map[string]driver.BrowserAgentTaskSecret, len(credentials))
	for ref, credential := range credentials {
		taskSecrets[ref] = driver.BrowserAgentTaskSecret{Username: credential.Username, Password: credential.Password, ExpiresAt: credential.ExpiresAt, AllowedDomains: append([]string(nil), credential.AllowedDomains...), AllowedOperations: append([]string(nil), credential.AllowedOperations...)}
	}
	result, runErr := router.Run(ctx, executionRuntimeRequest{
		Package: pkg, CloudJobID: jobID, RecordingOutputDir: recordingDir, RenderOutputDir: renderDir,
		ResultCreatedAt: time.Now().UTC(), Progress: progress, TaskSecrets: taskSecrets,
	})
	if runErr != nil && runtimeExecutionErrorCode(runErr) == runtimeErrorWorkerRestartInjected {
		return model.RecordingResultPackage{}, nil, runErr
	}
	if runErr != nil {
		result, err = directWorkerFailureResult(pkg, jobID, runErr)
		if err != nil {
			return model.RecordingResultPackage{}, nil, fmt.Errorf("direct Browser Agent execution failed and its failure result could not be built: %w", err)
		}
	}
	if err := finalizeDirectReplayManifest(&result, jobID); err != nil {
		return model.RecordingResultPackage{}, nil, err
	}
	files, err := directWorkerArtifactFiles(result, root)
	if err != nil {
		return model.RecordingResultPackage{}, nil, err
	}
	prepareDirectWorkerResult(&result, jobID, files)
	return result, files, nil
}

func directWorkerFailureResult(pkg *model.ClientExecutionPackage, jobID string, runErr error) (model.RecordingResultPackage, error) {
	now := time.Now().UTC()
	nodeID := "browser_agent_runtime"
	if pkg.ExecutableScriptBundle != nil && pkg.ExecutableScriptBundle.PlanJSON != nil && len(pkg.ExecutableScriptBundle.PlanJSON.Steps) > 0 {
		nodeID = pkg.ExecutableScriptBundle.PlanJSON.Steps[0].NodeID
	}
	code := runtimeExecutionErrorCode(runErr)
	if strings.TrimSpace(code) == "" {
		code = "browser_agent_execution_failed"
	}
	result, err := executor.NewRecordingResultPackageFromRecordResult(pkg, executor.RecordResult{
		StepResults: []model.StepResult{{NodeID: nodeID, Status: "failed", StartedAt: now, CompletedAt: now, Error: &model.AgentError{Code: code, Message: "Browser Agent execution failed; inspect the redacted failure category and approved rerun workflow.", Retryable: true}}},
		WorkerID:    "browser-agent-direct-worker", StartedAt: now, CompletedAt: now,
	}, jobID, now)
	if err != nil {
		return result, err
	}
	result.FailureDiagnostic = &model.ScriptFailureDiagnostic{
		ID: "diag_" + safePathSegment(nodeID), SchemaVersion: model.ScriptFailureDiagnosticSchemaVersion,
		SourcePackageID: pkg.PackageID, CloudJobID: jobID, FailedNodeID: nodeID, Attempt: 1,
		Error:           model.AgentError{Code: code, Message: "Browser Agent infrastructure stopped before traceable page evidence was available.", Retryable: true},
		RedactionReport: model.DiagnosticRedactionReport{Applied: true, PolicyRef: pkg.PackageID + ".redactions", FullHTMLIncluded: false}, CapturedAt: now,
		BrowserEvidenceUnavailable: directInfrastructureFailureCode(code),
	}
	return result, nil
}

func directWorkerArtifactFiles(result model.RecordingResultPackage, root string) ([]DirectWorkerArtifactFile, error) {
	values := append([]model.ArtifactRef{}, result.GeneratedAssets...)
	for _, step := range result.StepResults {
		values = append(values, step.Artifacts...)
	}
	if result.ExecutionTrace != nil {
		values = append(values, result.ExecutionTrace.Artifacts...)
		for _, step := range result.ExecutionTrace.StepResults {
			values = append(values, step.Artifacts...)
		}
	}
	if result.StageEventLogRef != nil {
		values = append(values, *result.StageEventLogRef)
	}
	for _, descriptor := range result.Delivery.AssetRefs {
		values = append(values, artifactRefFromPackageDescriptor(descriptor))
	}
	if result.FailureDiagnostic != nil {
		for _, descriptor := range append(append([]model.PackageArtifactDescriptor{}, result.FailureDiagnostic.ScreenshotRefs...), result.FailureDiagnostic.TraceRefs...) {
			values = append(values, artifactRefFromPackageDescriptor(descriptor))
		}
		for _, descriptor := range []*model.PackageArtifactDescriptor{result.FailureDiagnostic.DOMSnapshotRef, result.FailureDiagnostic.AccessibilitySnapshotRef} {
			if descriptor != nil {
				values = append(values, artifactRefFromPackageDescriptor(*descriptor))
			}
		}
	}
	byID := map[string]DirectWorkerArtifactFile{}
	for _, artifact := range values {
		if strings.TrimSpace(artifact.ID) == "" || strings.TrimSpace(artifact.URI) == "" {
			continue
		}
		if _, exists := byID[artifact.ID]; exists {
			continue
		}
		path, err := directWorkerLocalPath(artifact.URI)
		if err != nil {
			return nil, fmt.Errorf("direct worker artifact %s: %w", artifact.ID, err)
		}
		absolute, err := filepath.Abs(filepath.Clean(path))
		if err != nil || !pathWithinRoot(absolute, root) {
			return nil, fmt.Errorf("direct worker artifact %s is outside its job output root", artifact.ID)
		}
		info, err := os.Stat(absolute)
		if err != nil || info.IsDir() {
			return nil, fmt.Errorf("direct worker artifact %s is not a readable file", artifact.ID)
		}
		file, err := os.Open(absolute)
		if err != nil {
			return nil, fmt.Errorf("direct worker artifact %s could not be opened", artifact.ID)
		}
		hasher := sha256.New()
		_, copyErr := io.Copy(hasher, file)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			return nil, fmt.Errorf("direct worker artifact %s could not be read", artifact.ID)
		}
		artifact.SHA256 = fmt.Sprintf("%x", hasher.Sum(nil))
		artifact.SizeBytes = info.Size()
		byID[artifact.ID] = DirectWorkerArtifactFile{Artifact: artifact, Path: absolute}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	files := make([]DirectWorkerArtifactFile, 0, len(ids))
	for _, id := range ids {
		files = append(files, byID[id])
	}
	return files, nil
}

func artifactRefFromPackageDescriptor(value model.PackageArtifactDescriptor) model.ArtifactRef {
	return model.ArtifactRef{
		ID: value.ID, Kind: value.Kind, URI: value.URI, MimeType: value.MimeType,
		SHA256: value.SHA256, SizeBytes: value.SizeBytes, Sensitive: value.Sensitive, Metadata: value.Metadata,
	}
}

func directWorkerLocalPath(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" {
		return value, nil
	}
	if parsed.Scheme != "file" || parsed.Host != "" {
		return "", errors.New("only local file artifacts may be uploaded")
	}
	path, err := url.PathUnescape(parsed.Path)
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return filepath.FromSlash(path), nil
}

func prepareDirectWorkerResult(result *model.RecordingResultPackage, jobID string, files []DirectWorkerArtifactFile) {
	if result == nil {
		return
	}
	byID := make(map[string]model.ArtifactRef, len(files))
	for _, file := range files {
		artifact := file.Artifact
		artifact.URI = directWorkerArtifactURI(jobID, artifact.ID)
		if artifact.Metadata != nil {
			metadata := make(map[string]any, len(artifact.Metadata))
			for key, value := range artifact.Metadata {
				if !strings.EqualFold(key, "local_path") {
					metadata[key] = value
				}
			}
			artifact.Metadata = metadata
		}
		byID[artifact.ID] = artifact
	}
	rewrite := func(values []model.ArtifactRef) []model.ArtifactRef {
		for index := range values {
			if value, ok := byID[values[index].ID]; ok {
				values[index] = value
			}
		}
		return values
	}
	result.GeneratedAssets = rewrite(result.GeneratedAssets)
	if result.ExecutionTrace != nil {
		result.ExecutionTrace.Artifacts = rewrite(result.ExecutionTrace.Artifacts)
		for index := range result.ExecutionTrace.StepResults {
			result.ExecutionTrace.StepResults[index].Artifacts = rewrite(result.ExecutionTrace.StepResults[index].Artifacts)
		}
	}
	for index := range result.StepResults {
		result.StepResults[index].Artifacts = rewrite(result.StepResults[index].Artifacts)
	}
	if result.StageEventLogRef != nil {
		if value, ok := byID[result.StageEventLogRef.ID]; ok {
			*result.StageEventLogRef = value
		}
	}
	rewriteDescriptor := func(value *model.PackageArtifactDescriptor) {
		if value == nil {
			return
		}
		if artifact, ok := byID[value.ID]; ok {
			value.URI = artifact.URI
			value.Kind = artifact.Kind
			value.MimeType = artifact.MimeType
			value.SHA256 = artifact.SHA256
			value.SizeBytes = artifact.SizeBytes
		}
		value.Encrypted = true
		value.Sensitive = true
		value.RecipientKeyID = "direct-lease"
		if value.Metadata != nil {
			metadata := make(map[string]any, len(value.Metadata))
			for key, item := range value.Metadata {
				if strings.EqualFold(key, "local_path") || strings.EqualFold(key, "dev_local_artifact") {
					continue
				}
				metadata[key] = item
			}
			value.Metadata = metadata
		}
	}
	if result.FailureDiagnostic != nil {
		for index := range result.FailureDiagnostic.ScreenshotRefs {
			rewriteDescriptor(&result.FailureDiagnostic.ScreenshotRefs[index])
		}
		for index := range result.FailureDiagnostic.TraceRefs {
			rewriteDescriptor(&result.FailureDiagnostic.TraceRefs[index])
		}
		rewriteDescriptor(result.FailureDiagnostic.DOMSnapshotRef)
		rewriteDescriptor(result.FailureDiagnostic.AccessibilitySnapshotRef)
	}
	for index := range result.Delivery.AssetRefs {
		rewriteDescriptor(&result.Delivery.AssetRefs[index])
	}
	result.Delivery.ResultPackageRef.URI = "direct://jobs/" + url.PathEscape(jobID) + "/result"
	result.Delivery.ResultPackageRef.Encrypted = true
	result.Delivery.ResultPackageRef.Sensitive = true
	result.Delivery.ResultPackageRef.RecipientKeyID = "direct-lease"
	if result.Delivery.ResultPackageRef.Metadata != nil {
		delete(result.Delivery.ResultPackageRef.Metadata, "dev_local_artifact")
		delete(result.Delivery.ResultPackageRef.Metadata, "local_path")
	}
	result.Delivery.EncryptionAlg = model.DirectTransportCryptoSuite
	result.Delivery.RecipientKeyID = "direct-lease"
}

// NormalizeDirectWorkerResultDescriptors reconciles package descriptors with
// the canonical artifact identity already present in the result. This is also
// safe to run when recovering a persisted finalization result: it needs no
// local paths and prevents a generic delivery label (for example "video")
// from disagreeing with the kind that was used for the authenticated upload.
func NormalizeDirectWorkerResultDescriptors(result *model.RecordingResultPackage) {
	if result == nil {
		return
	}
	canonical := map[string]model.ArtifactRef{}
	add := func(values []model.ArtifactRef) {
		for _, value := range values {
			if value.ID != "" {
				if _, exists := canonical[value.ID]; !exists {
					canonical[value.ID] = value
				}
			}
		}
	}
	add(result.GeneratedAssets)
	for _, step := range result.StepResults {
		add(step.Artifacts)
	}
	if result.ExecutionTrace != nil {
		add(result.ExecutionTrace.Artifacts)
		for _, step := range result.ExecutionTrace.StepResults {
			add(step.Artifacts)
		}
	}
	if result.StageEventLogRef != nil {
		add([]model.ArtifactRef{*result.StageEventLogRef})
	}
	normalize := func(descriptor *model.PackageArtifactDescriptor) {
		if descriptor == nil {
			return
		}
		if artifact, ok := canonical[descriptor.ID]; ok {
			descriptor.Kind = artifact.Kind
			descriptor.MimeType = artifact.MimeType
		}
	}
	for index := range result.Delivery.AssetRefs {
		normalize(&result.Delivery.AssetRefs[index])
	}
	if result.FailureDiagnostic != nil {
		for index := range result.FailureDiagnostic.ScreenshotRefs {
			normalize(&result.FailureDiagnostic.ScreenshotRefs[index])
		}
		for index := range result.FailureDiagnostic.TraceRefs {
			normalize(&result.FailureDiagnostic.TraceRefs[index])
		}
		normalize(result.FailureDiagnostic.DOMSnapshotRef)
		normalize(result.FailureDiagnostic.AccessibilitySnapshotRef)
	}
}

// NormalizeDirectWorkerEvidenceArtifactIDs repairs legacy evidence refs that
// placed a semantic evidence ID in artifact_id. Only IDs that resolve to an
// artifact already present in this result are retained across the transport
// boundary; the evidence citation itself is preserved.
func NormalizeDirectWorkerEvidenceArtifactIDs(result *model.RecordingResultPackage) {
	if result == nil {
		return
	}
	artifacts := map[string]bool{}
	addRef := func(value model.ArtifactRef) {
		if value.ID != "" {
			artifacts[value.ID] = true
		}
	}
	for _, value := range result.GeneratedAssets {
		addRef(value)
	}
	for _, step := range result.StepResults {
		for _, value := range step.Artifacts {
			addRef(value)
		}
	}
	if result.ExecutionTrace != nil {
		for _, value := range result.ExecutionTrace.Artifacts {
			addRef(value)
		}
		for _, step := range result.ExecutionTrace.StepResults {
			for _, value := range step.Artifacts {
				addRef(value)
			}
		}
	}
	for _, value := range result.Delivery.AssetRefs {
		if value.ID != "" {
			artifacts[value.ID] = true
		}
	}
	normalize := func(refs []model.EvidenceRef) {
		for index := range refs {
			artifactID := strings.TrimSpace(refs[index].ArtifactID)
			if artifactID == "" || artifacts[artifactID] {
				continue
			}
			candidate := strings.TrimPrefix(artifactID, "evidence_")
			if candidate != artifactID && artifacts[candidate] {
				refs[index].ArtifactID = candidate
			} else {
				refs[index].ArtifactID = ""
			}
		}
	}
	for reportIndex := range result.ValidationReports {
		report := &result.ValidationReports[reportIndex]
		normalize(report.EvidenceRefs)
		for checkIndex := range report.Checks {
			normalize(report.Checks[checkIndex].EvidenceRefs)
		}
	}
	if result.ValidationRunReport != nil {
		normalize(result.ValidationRunReport.EvidenceRefs)
		for findingIndex := range result.ValidationRunReport.Findings {
			normalize(result.ValidationRunReport.Findings[findingIndex].EvidenceRefs)
		}
	}
}

// SanitizeDirectWorkerResultMetadata removes worker-local path hints from
// artifact metadata after those files have been converted to authenticated
// Direct URIs. Business metadata is otherwise left untouched.
func SanitizeDirectWorkerResultMetadata(result *model.RecordingResultPackage) {
	if result == nil {
		return
	}
	sanitize := func(metadata map[string]any) map[string]any {
		if metadata == nil {
			return nil
		}
		clean := make(map[string]any, len(metadata))
		for key, value := range metadata {
			lowerKey := strings.ToLower(strings.TrimSpace(key))
			if lowerKey == "local_path" || lowerKey == "dev_local_artifact" || lowerKey == "render_manifest_path" {
				continue
			}
			if text, ok := value.(string); ok {
				lowerValue := strings.ToLower(strings.TrimSpace(text))
				if strings.HasPrefix(lowerValue, "file://") || strings.HasPrefix(lowerValue, "/var/lib/") || strings.HasPrefix(lowerValue, "/home/") || strings.HasPrefix(lowerValue, "/root/") {
					continue
				}
			}
			clean[key] = value
		}
		return clean
	}
	sanitizeRefs := func(values []model.ArtifactRef) {
		for index := range values {
			values[index].Metadata = sanitize(values[index].Metadata)
		}
	}
	sanitizeDescriptors := func(values []model.PackageArtifactDescriptor) {
		for index := range values {
			values[index].Metadata = sanitize(values[index].Metadata)
		}
	}
	sanitizeRefs(result.GeneratedAssets)
	for index := range result.StepResults {
		sanitizeRefs(result.StepResults[index].Artifacts)
	}
	if result.ExecutionTrace != nil {
		sanitizeRefs(result.ExecutionTrace.Artifacts)
		for index := range result.ExecutionTrace.StepResults {
			sanitizeRefs(result.ExecutionTrace.StepResults[index].Artifacts)
		}
	}
	if result.StageEventLogRef != nil {
		result.StageEventLogRef.Metadata = sanitize(result.StageEventLogRef.Metadata)
	}
	sanitizeDescriptors(result.Delivery.AssetRefs)
	result.Delivery.ResultPackageRef.Metadata = sanitize(result.Delivery.ResultPackageRef.Metadata)
	if result.FailureDiagnostic != nil {
		sanitizeDescriptors(result.FailureDiagnostic.ScreenshotRefs)
		sanitizeDescriptors(result.FailureDiagnostic.TraceRefs)
		if result.FailureDiagnostic.DOMSnapshotRef != nil {
			result.FailureDiagnostic.DOMSnapshotRef.Metadata = sanitize(result.FailureDiagnostic.DOMSnapshotRef.Metadata)
		}
		if result.FailureDiagnostic.AccessibilitySnapshotRef != nil {
			result.FailureDiagnostic.AccessibilitySnapshotRef.Metadata = sanitize(result.FailureDiagnostic.AccessibilitySnapshotRef.Metadata)
		}
	}
}

func directWorkerArtifactURI(jobID, artifactID string) string {
	return "direct://jobs/" + url.PathEscape(jobID) + "/artifacts/" + url.PathEscape(artifactID)
}

// finalizeDirectReplayManifest replaces Server-local file references inside
// the manifest with the authenticated Direct artifact identities that the App
// will receive. It runs before artifact discovery so the uploaded checksum and
// size describe the final manifest bytes, not a local-path draft.
func finalizeDirectReplayManifest(result *model.RecordingResultPackage, jobID string) error {
	if result == nil {
		return errors.New("direct result is required")
	}
	manifestIndex := -1
	for index := range result.GeneratedAssets {
		if result.GeneratedAssets[index].Kind == "replay_manifest" {
			manifestIndex = index
			break
		}
	}
	if manifestIndex < 0 {
		return nil
	}
	manifestArtifact := result.GeneratedAssets[manifestIndex]
	path, err := directWorkerLocalPath(manifestArtifact.URI)
	if err != nil {
		return fmt.Errorf("finalize replay manifest path: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("finalize replay manifest read: %w", err)
	}
	var manifest model.ReplayManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("finalize replay manifest decode: %w", err)
	}
	manifest.ManifestURI = directWorkerArtifactURI(jobID, manifestArtifact.ID)
	manifest.ProtocolRuntime = model.DirectTransportProtocolVersion
	findURI := func(kinds ...string) string {
		values := append([]model.ArtifactRef{}, result.GeneratedAssets...)
		if result.ExecutionTrace != nil {
			values = append(values, result.ExecutionTrace.Artifacts...)
		}
		for _, artifact := range values {
			for _, kind := range kinds {
				if strings.EqualFold(artifact.Kind, kind) && artifact.ID != "" {
					return directWorkerArtifactURI(jobID, artifact.ID)
				}
			}
		}
		return ""
	}
	manifest.MP4URI = findURI("demo_video", "mp4")
	manifest.RawRecordingURI = findURI("raw_recording")
	manifest.BrowserTraceURI = findURI("browser_trace", "execution_trace", "playwright_trace", "trace")
	if result.StageEventLogRef != nil && result.StageEventLogRef.ID != "" {
		manifest.StageEventLogURI = directWorkerArtifactURI(jobID, result.StageEventLogRef.ID)
	}
	if err := manifest.Validate(); err != nil {
		return fmt.Errorf("finalize replay manifest validate: %w", err)
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("finalize replay manifest encode: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return fmt.Errorf("finalize replay manifest write: %w", err)
	}
	manifestArtifact.SHA256 = model.SHA256Hex(encoded)
	manifestArtifact.SizeBytes = int64(len(encoded))
	result.GeneratedAssets[manifestIndex] = manifestArtifact
	for index := range result.Delivery.AssetRefs {
		if result.Delivery.AssetRefs[index].ID == manifestArtifact.ID {
			result.Delivery.AssetRefs[index].SHA256 = manifestArtifact.SHA256
			result.Delivery.AssetRefs[index].SizeBytes = manifestArtifact.SizeBytes
		}
	}
	return nil
}
