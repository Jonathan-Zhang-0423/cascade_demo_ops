package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cascade-demoops/backend/internal/driver"
	"cascade-demoops/backend/internal/model"
)

// RunDirectJob is the Server-owned Worker execution bridge. It consumes one
// queued Direct job, reuses the existing Browser Agent runtime router, uploads
// the produced artifacts to the Direct Gateway, and commits the immutable
// RecordingResultPackage. It never rewrites the approved package.
func (s *DirectHTTPServer) RunDirectJob(ctx context.Context, jobID string) error {
	if s == nil || s.service == nil {
		return errors.New("direct worker service is not configured")
	}
	job, err := s.gateway.WorkerJob(jobID)
	if err != nil {
		return err
	}
	var packageJSON []byte
	switch job.Status.Status {
	case "queued":
		packageJSON, err = s.gateway.ClaimWorker(jobID)
		if err != nil {
			return err
		}
	case "running":
		packageJSON = append([]byte{}, job.PackageJSON...)
	default:
		return errors.New("direct job is not queued or claimed")
	}
	var pkg model.ClientExecutionPackage
	if err := json.Unmarshal(packageJSON, &pkg); err != nil {
		_ = s.gateway.FailJob(jobID, "invalid_package")
		return fmt.Errorf("decode direct package: %w", err)
	}
	if pkg.ExecutableScriptBundle == nil || pkg.ExecutableScriptBundle.ScriptManifest.Runtime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 {
		_ = s.gateway.FailJob(jobID, "unsupported_runtime")
		return errors.New("direct job package runtime is not browser-agent-outline-v1")
	}
	if err := model.ValidateClientExecutionPackageForDirectExecution(&pkg); err != nil {
		_ = s.gateway.FailJob(jobID, "package_validation_failed")
		return err
	}
	if err := validateDirectPackageCredentialRefs(&pkg); err != nil {
		_ = s.gateway.FailJob(jobID, "credential_grant_mismatch")
		return err
	}
	secretRefs := directPackageSecretRefs(&pkg)
	taskSecrets := map[string]driver.BrowserAgentTaskSecret{}
	if len(secretRefs) == 1 {
		envelope, consumeErr := s.gateway.ConsumeCredential(jobID)
		if consumeErr != nil {
			_ = s.gateway.FailJob(jobID, "credential_unavailable")
			return fmt.Errorf("consume direct credential: %w", consumeErr)
		}
		if envelope.SecretRef != secretRefs[0] {
			_ = s.gateway.FailJob(jobID, "credential_scope_mismatch")
			return errors.New("consumed Direct credential does not match the approved package secret_ref")
		}
		if strings.TrimSpace(envelope.Username) == "" || strings.TrimSpace(envelope.Secret) == "" {
			_ = s.gateway.FailJob(jobID, "credential_invalid")
			return errors.New("consumed Direct credential is empty")
		}
		taskSecrets[envelope.SecretRef] = driver.BrowserAgentTaskSecret{
			Username:          envelope.Username,
			Password:          envelope.Secret,
			ExpiresAt:         time.UnixMilli(envelope.ExpiresAtUnixMS).UTC(),
			AllowedDomains:    append([]string(nil), envelope.AllowedDomains...),
			AllowedOperations: append([]string(nil), envelope.AllowedOperations...),
		}
		envelope.Username = ""
		envelope.Secret = ""
	}
	root := filepath.Join(s.service.runtime.ArtifactRoot, "direct", safePathSegment(jobID))
	recordingDir := filepath.Join(root, "recording")
	renderDir := filepath.Join(root, "render")
	router := newExecutionRuntimeRouter(localLegacyPlaywrightRunner{service: s.service}, s.service.outlineRunner)
	result, err := router.Run(ctx, executionRuntimeRequest{
		Package: &pkg, CloudJobID: jobID, RecordingOutputDir: recordingDir, RenderOutputDir: renderDir, TaskSecrets: taskSecrets,
		ResultCreatedAt: time.Now().UTC(),
		Progress: func(stage, message string, progress int) {
			_ = s.gateway.UpdateJob(jobID, progress, stage)
		},
	})
	if err != nil {
		failedResult, packageErr := s.directRuntimeFailureResult(ctx, &pkg, jobID, recordingDir, result, err, time.Now().UTC())
		if packageErr != nil {
			_ = s.gateway.FailJob(jobID, runtimeExecutionErrorCode(err))
			return fmt.Errorf("package authoritative Direct failure result: %w", packageErr)
		}
		result = failedResult
	}
	if err := s.finalizeDirectResult(ctx, jobID, root, pkg, &result); err != nil {
		_ = s.gateway.FailJob(jobID, "artifact_upload_failed")
		return err
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		_ = s.gateway.FailJob(jobID, "result_encode_failed")
		return err
	}
	// Refresh after artifact upload so result asset refs are checked against the
	// immutable descriptors calculated from the uploaded bytes.
	validatedJob, err := s.gateway.WorkerJob(jobID)
	if err != nil {
		_ = s.gateway.FailJob(jobID, "result_validation_failed")
		return err
	}
	if err := validateDirectWorkerResult(validatedJob, result.ResultID, resultJSON); err != nil {
		_ = s.gateway.FailJob(jobID, "result_validation_failed")
		return err
	}
	return s.gateway.CommitJobResult(jobID, result.ResultID, resultJSON, directResultTerminalStatus(result))
}

// RunDirectWorkerScheduler runs the same Server-owned execution bridge behind
// a bounded loopback scheduler. ClaimWorker remains the atomic duplicate-run
// guard. The scheduler does not expose credentials or package bodies.
func (s *DirectHTTPServer) RunDirectWorkerScheduler(ctx context.Context, pollInterval time.Duration, concurrency int) error {
	if s == nil || s.service == nil {
		return errors.New("direct worker service is not configured")
	}
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	if concurrency <= 0 {
		concurrency = 1
	}
	s.markWorkerHeartbeat("embedded")
	sem := make(chan struct{}, concurrency)
	running := map[string]bool{}
	var runningMu sync.Mutex
	launch := func(jobID string) {
		runningMu.Lock()
		if running[jobID] {
			runningMu.Unlock()
			return
		}
		running[jobID] = true
		runningMu.Unlock()
		go func() {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				runningMu.Lock()
				delete(running, jobID)
				runningMu.Unlock()
				return
			}
			defer func() {
				<-sem
				runningMu.Lock()
				delete(running, jobID)
				runningMu.Unlock()
			}()
			_ = s.RunDirectJob(ctx, jobID)
		}()
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		s.markWorkerHeartbeat("embedded")
		for _, jobID := range s.gateway.QueuedJobIDs() {
			launch(jobID)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *DirectHTTPServer) uploadDirectResultArtifacts(jobID string, result model.RecordingResultPackage) error {
	for _, artifact := range directResultArtifactRefs(result) {
		path, err := s.service.localArtifactPath(artifact.URI)
		if err != nil {
			return fmt.Errorf("resolve result artifact %q: %w", artifact.ID, err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read result artifact %q: %w", artifact.ID, err)
		}
		descriptor, err := s.gateway.AddArtifact(jobID, artifact.ID, artifact.Kind, artifact.MimeType, data)
		if err != nil {
			return fmt.Errorf("upload result artifact %q: %w", artifact.ID, err)
		}
		if descriptor.SHA256 != artifact.SHA256 || descriptor.Size != artifact.SizeBytes {
			return fmt.Errorf("result artifact %q changed before Direct upload", artifact.ID)
		}
	}
	return nil
}

// RunQueuedDirectJobs executes a bounded number of jobs selected by the
// caller. A persistent queue/claim-next loop belongs in the production worker
// process; keeping selection outside this method avoids duplicate claims.
func (s *DirectHTTPServer) RunQueuedDirectJobs(ctx context.Context, jobIDs []string) error {
	for _, jobID := range jobIDs {
		if strings.TrimSpace(jobID) == "" {
			continue
		}
		if err := s.RunDirectJob(ctx, jobID); err != nil {
			return err
		}
	}
	return nil
}

// RunQueuedDirectJobsFromGateway drains the current in-memory queue snapshot.
// Production callers may invoke it from a dedicated loopback Worker process;
// ClaimWorker still makes each job single-consumer.
func (s *DirectHTTPServer) RunQueuedDirectJobsFromGateway(ctx context.Context) error {
	return s.RunQueuedDirectJobs(ctx, s.gateway.QueuedJobIDs())
}
