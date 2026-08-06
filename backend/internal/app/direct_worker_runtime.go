package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
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
	if runErr != nil {
		result, err = directWorkerFailureResult(pkg, jobID, runErr)
		if err != nil {
			return model.RecordingResultPackage{}, nil, fmt.Errorf("direct Browser Agent execution failed and its failure result could not be built: %w", err)
		}
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
	return executor.NewRecordingResultPackageFromRecordResult(pkg, executor.RecordResult{
		StepResults: []model.StepResult{{NodeID: nodeID, Status: "failed", StartedAt: now, CompletedAt: now, Error: &model.AgentError{Code: code, Message: "Browser Agent execution failed; inspect the redacted failure category and approved rerun workflow.", Retryable: true}}},
		WorkerID:    "browser-agent-direct-worker", StartedAt: now, CompletedAt: now,
	}, jobID, now)
}

func directWorkerArtifactFiles(result model.RecordingResultPackage, root string) ([]DirectWorkerArtifactFile, error) {
	values := append([]model.ArtifactRef{}, result.GeneratedAssets...)
	if result.ExecutionTrace != nil {
		values = append(values, result.ExecutionTrace.Artifacts...)
	}
	if result.StageEventLogRef != nil {
		values = append(values, *result.StageEventLogRef)
	}
	byID := map[string]DirectWorkerArtifactFile{}
	for _, artifact := range values {
		if strings.TrimSpace(artifact.ID) == "" || strings.TrimSpace(artifact.URI) == "" {
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
	return filepath.FromSlash(path), nil
}

func prepareDirectWorkerResult(result *model.RecordingResultPackage, jobID string, files []DirectWorkerArtifactFile) {
	if result == nil {
		return
	}
	byID := make(map[string]model.ArtifactRef, len(files))
	for _, file := range files {
		artifact := file.Artifact
		artifact.URI = "direct://jobs/" + url.PathEscape(jobID) + "/artifacts/" + url.PathEscape(artifact.ID)
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
	for index := range result.Delivery.AssetRefs {
		if value, ok := byID[result.Delivery.AssetRefs[index].ID]; ok {
			result.Delivery.AssetRefs[index].URI = value.URI
			result.Delivery.AssetRefs[index].SHA256 = value.SHA256
			result.Delivery.AssetRefs[index].SizeBytes = value.SizeBytes
		}
		result.Delivery.AssetRefs[index].Encrypted = true
		result.Delivery.AssetRefs[index].Sensitive = true
		result.Delivery.AssetRefs[index].RecipientKeyID = "direct-lease"
	}
	result.Delivery.EncryptionAlg = model.DirectTransportCryptoSuite
	result.Delivery.RecipientKeyID = "direct-lease"
}
