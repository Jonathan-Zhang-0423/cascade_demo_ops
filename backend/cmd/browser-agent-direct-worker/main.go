package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"cascade-demoops/backend/internal/app"
	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/directtransport"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/store"
)

type worker struct {
	service    *app.Service
	client     *http.Client
	baseURL    string
	token      string
	outputRoot string
	poll       time.Duration
	runTimeout time.Duration
}

const (
	workerHTTPTimeout         = 15 * time.Minute
	workerFinalizationTimeout = 20 * time.Minute
	maxRecoveryEventLogBytes  = 1 << 20
)

func main() {
	baseURL := flag.String("gateway-url", env("CASCADE_DIRECT_WORKER_GATEWAY_URL", "http://127.0.0.1:18444"), "loopback gateway Worker API")
	outputRoot := flag.String("output-root", env("CASCADE_DIRECT_WORKER_OUTPUT_ROOT", "/var/lib/cascade-browser-agent/worker"), "ephemeral Browser Agent output root")
	poll := flag.Duration("poll-interval", envDuration("CASCADE_DIRECT_WORKER_POLL_INTERVAL", 2*time.Second), "job poll interval")
	runTimeout := flag.Duration("run-timeout", envDuration("CASCADE_DIRECT_WORKER_RUN_TIMEOUT", 45*time.Minute), "maximum runtime per job")
	flag.Parse()
	parsed, err := url.Parse(strings.TrimRight(*baseURL, "/"))
	must(err)
	if parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		must(errors.New("worker gateway URL must be loopback HTTP"))
	}
	token := strings.TrimSpace(os.Getenv("CASCADE_DIRECT_WORKER_TOKEN"))
	if len(token) < 32 {
		must(errors.New("CASCADE_DIRECT_WORKER_TOKEN must contain at least 32 characters"))
	}
	root, err := filepath.Abs(filepath.Clean(*outputRoot))
	must(err)
	must(os.MkdirAll(root, 0o700))
	cwd, err := os.Getwd()
	must(err)
	runtimeConfig, err := config.RuntimeConfigFromEnvWithRoot(config.DiscoverDevRepoRoot(cwd))
	must(err)
	runtimeConfig.ArtifactRoot = root
	runtimeConfig.DataRoot = filepath.Join(root, "state")
	runtimeConfig.CacheRoot = filepath.Join(root, "cache")
	runtimeConfig.LogRoot = filepath.Join(root, "logs")
	service, err := app.NewService(runtimeConfig, store.NewFileStateStore(filepath.Join(runtimeConfig.DataRoot, "projects")))
	must(err)
	runner := &worker{service: service, client: &http.Client{Timeout: workerHTTPTimeout}, baseURL: strings.TrimRight(*baseURL, "/"), token: token, outputRoot: root, poll: *poll, runTimeout: *runTimeout}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	fmt.Fprintln(os.Stdout, "Cascade Browser Agent direct worker started; gateway=loopback")
	must(runner.run(ctx))
}

func (w *worker) run(ctx context.Context) error {
	if err := w.recoverOrphanedFinalResults(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "orphaned final-result recovery incomplete:", redactError(err))
	}
	// A previous execution can finish the browser session but lose its bounded
	// finalization window while large recordings are still uploading. Publish
	// the small, authoritative stage log before claiming more work so the App
	// can reconcile an already-created successor without replaying submission.
	if err := w.recoverOrphanedStageEventLogs(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "orphaned stage-event recovery incomplete:", redactError(err))
	}
	for {
		job, ok, err := w.claim(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "claim failed:", redactError(err))
		} else if ok {
			w.runJob(ctx, job)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(w.poll):
		}
	}
}

func (w *worker) recoverOrphanedFinalResults(ctx context.Context) error {
	jobsRoot := filepath.Join(w.outputRoot, "jobs")
	entries, err := os.ReadDir(jobsRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var firstErr error
	for _, entry := range entries {
		jobID := entry.Name()
		if !entry.IsDir() || jobID == "" || safeID(jobID) != jobID {
			continue
		}
		jobRoot := filepath.Join(jobsRoot, jobID)
		marker := filepath.Join(jobRoot, ".finalization-result-submitted")
		if _, err := os.Stat(marker); err == nil {
			continue
		}
		payload, err := os.ReadFile(filepath.Join(jobRoot, "finalization-result.json"))
		if err != nil || len(payload) == 0 || len(payload) > 64<<20 {
			continue
		}
		var result model.RecordingResultPackage
		if json.Unmarshal(payload, &result) != nil || result.CloudJobID != jobID || result.ResultID == "" {
			continue
		}
		app.NormalizeDirectWorkerResultDescriptors(&result)
		app.NormalizeDirectWorkerEvidenceArtifactIDs(&result)
		app.SanitizeDirectWorkerResultMetadata(&result)
		manifestPath := filepath.Join(jobRoot, "recording", "replay-manifest.json")
		if manifestPayload, readErr := os.ReadFile(manifestPath); readErr == nil {
			var manifest model.ReplayManifest
			if json.Unmarshal(manifestPayload, &manifest) == nil {
				app.NormalizeValidationRunReportStageIndex(&result, manifest)
			}
		}
		if err := w.submitRecoveredResult(ctx, jobID, result); err != nil {
			if gatewayErrorCode(err) != "job_not_claimed" && firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := os.WriteFile(marker, []byte("submitted\n"), 0o600); err != nil && firstErr == nil {
			firstErr = err
		}
		fmt.Fprintln(os.Stdout, "orphaned final result recovered job=", safeID(jobID))
	}
	return firstErr
}

func (w *worker) claim(ctx context.Context) (directtransport.WorkerJob, bool, error) {
	var job directtransport.WorkerJob
	response, err := w.request(ctx, http.MethodPost, "/v1/worker/jobs/claim", nil, nil)
	if err != nil {
		return job, false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent {
		return job, false, nil
	}
	if response.StatusCode != http.StatusOK {
		return job, false, responseError(response)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<20)).Decode(&job); err != nil {
		return job, false, err
	}
	if job.ProtocolVersion != model.DirectWorkerProtocolVersion {
		return job, false, fmt.Errorf("worker protocol mismatch")
	}
	return job, true, nil
}

func (w *worker) runJob(parent context.Context, job directtransport.WorkerJob) {
	ctx, cancel := context.WithTimeout(parent, w.runTimeout)
	defer cancel()
	go w.monitorCancellation(ctx, job.JobID, cancel)
	completed := false
	defer func() {
		if !completed {
			releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer releaseCancel()
			_ = w.release(releaseCtx, job.JobID, "worker_interrupted")
		}
	}()
	credentials := map[string]model.DirectCredentialValue{}
	if len(job.Package.CredentialGrants) > 0 {
		credential, err := w.consumeCredential(ctx, job.JobID)
		if err != nil {
			fmt.Fprintln(os.Stderr, "credential consume failed job=", safeID(job.JobID), "class=credential_unavailable")
			return
		}
		credentials[credential.Credential.SecretRef] = credential.Credential
		defer clearCredentialMap(credentials)
	}
	jobRoot := filepath.Join(w.outputRoot, "jobs", safeID(job.JobID))
	progress := func(stage, message string, percent int) {
		_ = w.updateStatus(ctx, job.JobID, stage, message, percent)
	}
	result, files, err := w.service.RunDirectBrowserAgentJobWithCredentials(ctx, &job.Package, job.JobID, jobRoot, credentials, progress)
	clearCredentialMap(credentials)
	if err != nil {
		fmt.Fprintln(os.Stderr, "execution failed job=", safeID(job.JobID), "class=browser_agent_execution_failed")
		return
	}
	if ctx.Err() != nil {
		fmt.Fprintln(os.Stdout, "job execution context ended; skipping finalization job=", safeID(job.JobID))
		return
	}
	// A service restart cancels the process-level context. The browser runtime
	// may still return a well-formed failure package while unwinding, but that
	// package describes an infrastructure interruption rather than a terminal
	// product result. Let the deferred release return the job to the credential
	// gate so a new worker can reclaim its persisted checkpoint without
	// publishing a false terminal failure or replaying once-effects.
	if workerWasInterrupted(parent) {
		fmt.Fprintln(os.Stdout, "job interrupted; releasing for checkpoint recovery job=", safeID(job.JobID))
		return
	}
	finalizeCtx, finalizeCancel := workerFinalizationContext(parent)
	defer finalizeCancel()
	app.NormalizeDirectWorkerResultDescriptors(&result)
	app.NormalizeDirectWorkerEvidenceArtifactIDs(&result)
	app.SanitizeDirectWorkerResultMetadata(&result)
	if err := persistPreparedWorkerResult(jobRoot, result); err != nil {
		fmt.Fprintln(os.Stderr, "prepared result persistence failed job=", safeID(job.JobID), "class=result_persistence_failed")
		return
	}
	prioritizeRecoveryArtifacts(files)
	for _, file := range files {
		if err := w.uploadArtifact(finalizeCtx, job.JobID, file); err != nil {
			fmt.Fprintln(os.Stderr, "artifact upload failed job=", safeID(job.JobID), "artifact=", safeID(file.Artifact.ID), "size_bytes=", file.Artifact.SizeBytes, "class=artifact_upload_failed", "gateway_code=", gatewayErrorCode(err))
			return
		}
	}
	if err := w.submitResult(finalizeCtx, job.JobID, result); err != nil {
		diagnosticCode := "none"
		if result.FailureDiagnostic != nil {
			diagnosticCode = safeID(result.FailureDiagnostic.Error.Code)
		}
		fmt.Fprintln(os.Stderr, "result submit failed job=", safeID(job.JobID), "class=result_submit_failed", "gateway_code=", gatewayErrorCode(err), "result_status=", safeID(string(result.Status)), "diagnostic_code=", diagnosticCode, "detail=", redactError(err))
		return
	}
	completed = true
	fmt.Fprintln(os.Stdout, "job completed job=", safeID(job.JobID), "artifacts=", len(files))
}

func (w *worker) monitorCancellation(ctx context.Context, jobID string, cancel context.CancelFunc) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			queryCtx, queryCancel := context.WithTimeout(ctx, 5*time.Second)
			requested, err := w.cancellationRequested(queryCtx, jobID)
			queryCancel()
			if err == nil && requested {
				cancel()
				return
			}
		}
	}
}

func (w *worker) cancellationRequested(ctx context.Context, jobID string) (bool, error) {
	response, err := w.request(ctx, http.MethodGet, "/v1/worker/jobs/"+url.PathEscape(jobID)+"/control", nil, nil)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, responseError(response)
	}
	var value struct {
		ProtocolVersion string `json:"protocol_version"`
		JobID           string `json:"job_id"`
		CancelRequested bool   `json:"cancel_requested"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 32<<10)).Decode(&value); err != nil {
		return false, err
	}
	if value.ProtocolVersion != model.DirectWorkerProtocolVersion || value.JobID != jobID {
		return false, errors.New("worker control response binding mismatch")
	}
	return value.CancelRequested, nil
}

func persistPreparedWorkerResult(jobRoot string, result model.RecordingResultPackage) error {
	payload, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(jobRoot, "finalization-result.json"), append(payload, '\n'), 0o600)
}

func prioritizeRecoveryArtifacts(files []app.DirectWorkerArtifactFile) {
	sort.SliceStable(files, func(i, j int) bool {
		left := files[i].Artifact.Kind == "browser_agent_stage_event_log"
		right := files[j].Artifact.Kind == "browser_agent_stage_event_log"
		return left && !right
	})
}

func (w *worker) recoverOrphanedStageEventLogs(ctx context.Context) error {
	jobsRoot := filepath.Join(w.outputRoot, "jobs")
	entries, err := os.ReadDir(jobsRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var firstErr error
	for _, entry := range entries {
		jobID := entry.Name()
		if !entry.IsDir() || jobID == "" || safeID(jobID) != jobID {
			continue
		}
		jobRoot := filepath.Join(jobsRoot, jobID)
		marker := filepath.Join(jobRoot, ".stage-event-recovery-uploaded")
		if _, err := os.Stat(marker); err == nil {
			continue
		}
		path := filepath.Join(jobRoot, "recording", "browser-agent-stage-events.jsonl")
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxRecoveryEventLogBytes {
			continue
		}
		file := app.DirectWorkerArtifactFile{Artifact: model.ArtifactRef{
			ID: "stage_event_log_" + jobID, Kind: "browser_agent_stage_event_log",
			MimeType: "application/x-ndjson", SizeBytes: info.Size(),
			Metadata: map[string]any{"role": "stage_event_log"},
		}, Path: path}
		if err := w.uploadRecoveryArtifact(ctx, jobID, file); err != nil {
			// Completed jobs and still-running jobs do not need orphan recovery.
			// Keep scanning other spools, but surface real transport failures.
			if gatewayErrorCode(err) != "job_not_awaiting_recovery" && firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := os.WriteFile(marker, []byte("uploaded\n"), 0o600); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func workerWasInterrupted(parent context.Context) bool {
	return parent != nil && parent.Err() != nil
}

func workerFinalizationContext(parent context.Context) (context.Context, context.CancelFunc) {
	// Execution may legitimately consume its full budget while producing a
	// structured failure package. Detach cancellation for the bounded result
	// handoff so an expired browser context cannot make the diagnostic vanish.
	return context.WithTimeout(context.WithoutCancel(parent), workerFinalizationTimeout)
}

func (w *worker) release(ctx context.Context, jobID, reason string) error {
	body, _ := json.Marshal(directtransport.WorkerReleaseRequest{Reason: reason})
	response, err := w.request(ctx, http.MethodPost, "/v1/worker/jobs/"+url.PathEscape(jobID)+"/release", bytes.NewReader(body), map[string]string{"Content-Type": "application/json"})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusConflict {
		return responseError(response)
	}
	return nil
}

func (w *worker) consumeCredential(ctx context.Context, jobID string) (directtransport.WorkerCredential, error) {
	var value directtransport.WorkerCredential
	response, err := w.request(ctx, http.MethodPost, "/v1/worker/jobs/"+url.PathEscape(jobID)+"/credentials/consume", nil, nil)
	if err != nil {
		return value, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return value, responseError(response)
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&value)
	return value, err
}

func (w *worker) updateStatus(ctx context.Context, jobID, stage, message string, percent int) error {
	if percent < 1 {
		percent = 1
	}
	if percent > 99 {
		percent = 99
	}
	body, _ := json.Marshal(directtransport.WorkerStatusUpdate{Status: "running", Stage: stage, Message: message, ProgressPercent: percent})
	response, err := w.request(ctx, http.MethodPut, "/v1/worker/jobs/"+url.PathEscape(jobID)+"/status", bytes.NewReader(body), map[string]string{"Content-Type": "application/json"})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return responseError(response)
	}
	return nil
}

func (w *worker) uploadArtifact(ctx context.Context, jobID string, file app.DirectWorkerArtifactFile) error {
	return w.uploadArtifactWithRecovery(ctx, jobID, file, false)
}

func (w *worker) uploadRecoveryArtifact(ctx context.Context, jobID string, file app.DirectWorkerArtifactFile) error {
	return w.uploadArtifactWithRecovery(ctx, jobID, file, true)
}

func (w *worker) uploadArtifactWithRecovery(ctx context.Context, jobID string, file app.DirectWorkerArtifactFile, recovery bool) error {
	opened, err := os.Open(file.Path)
	if err != nil {
		return err
	}
	defer opened.Close()
	hash := strings.ToLower(strings.TrimSpace(file.Artifact.SHA256))
	if hash == "" {
		h := sha256.New()
		if _, err := io.Copy(h, opened); err != nil {
			return err
		}
		hash = hex.EncodeToString(h.Sum(nil))
		if _, err := opened.Seek(0, io.SeekStart); err != nil {
			return err
		}
	}
	path := "/v1/worker/jobs/" + url.PathEscape(jobID) + "/artifacts/" + url.PathEscape(file.Artifact.ID) + "?file_name=" + url.QueryEscape(filepath.Base(file.Path))
	headers := map[string]string{"Content-Type": file.Artifact.MimeType, "X-Artifact-SHA256": hash, "X-Artifact-Role": stringValue(file.Artifact.Metadata, "role"), "X-Artifact-Kind": file.Artifact.Kind}
	if recovery {
		headers["X-Artifact-Recovery"] = "stage-event-log-v1"
	}
	response, err := w.request(ctx, http.MethodPut, path, opened, headers)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return responseError(response)
	}
	return nil
}

func (w *worker) submitResult(ctx context.Context, jobID string, result model.RecordingResultPackage) error {
	return w.submitResultWithHeaders(ctx, jobID, result, nil)
}

func (w *worker) submitRecoveredResult(ctx context.Context, jobID string, result model.RecordingResultPackage) error {
	return w.submitResultWithHeaders(ctx, jobID, result, map[string]string{"X-Result-Recovery": "finalization-result-v1"})
}

func (w *worker) submitResultWithHeaders(ctx context.Context, jobID string, result model.RecordingResultPackage, headers map[string]string) error {
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if headers == nil {
		headers = map[string]string{}
	}
	headers["Content-Type"] = "application/json"
	response, err := w.request(ctx, http.MethodPut, "/v1/worker/jobs/"+url.PathEscape(jobID)+"/result", bytes.NewReader(body), headers)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return responseError(response)
	}
	return nil
}

func (w *worker) request(ctx context.Context, method, path string, body io.Reader, headers map[string]string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, w.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+w.token)
	for key, value := range headers {
		if value != "" {
			request.Header.Set(key, value)
		}
	}
	return w.client.Do(request)
}

func responseError(response *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(data, &payload)
	return &workerGatewayError{status: response.StatusCode, code: safeID(payload.Error.Code), message: strings.TrimSpace(payload.Error.Message)}
}

type workerGatewayError struct {
	status  int
	code    string
	message string
}

func (e *workerGatewayError) Error() string {
	return fmt.Sprintf("worker gateway HTTP %d code=%s message=%s", e.status, firstNonEmptyWorker(e.code, "unknown"), firstNonEmptyWorker(e.message, "not_provided"))
}

func gatewayErrorCode(err error) string {
	var gatewayErr *workerGatewayError
	if errors.As(err, &gatewayErr) {
		return firstNonEmptyWorker(gatewayErr.code, "unknown")
	}
	return "network_error"
}

func firstNonEmptyWorker(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
func clearCredentialMap(values map[string]model.DirectCredentialValue) {
	for key, value := range values {
		value.Username, value.Password = "", ""
		values[key] = value
		delete(values, key)
	}
}
func redactError(err error) string {
	if err == nil {
		return ""
	}
	value := strings.ReplaceAll(strings.ReplaceAll(err.Error(), "\r", " "), "\n", " ")
	if len(value) > 300 {
		value = value[:300]
	}
	return value
}
func safeID(value string) string {
	value = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			return r
		}
		return -1
	}, value)
	if len(value) > 80 {
		value = value[:80]
	}
	return value
}
func stringValue(values map[string]any, key string) string {
	if value, ok := values[key].(string); ok {
		return value
	}
	return ""
}
func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
func envDuration(name string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(os.Getenv(name))
	if err == nil && value > 0 {
		return value
	}
	return fallback
}
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, redactError(err))
		os.Exit(1)
	}
}
