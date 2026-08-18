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
	runner := &worker{service: service, client: &http.Client{Timeout: 2 * time.Minute}, baseURL: strings.TrimRight(*baseURL, "/"), token: token, outputRoot: root, poll: *poll, runTimeout: *runTimeout}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	fmt.Fprintln(os.Stdout, "Cascade Browser Agent direct worker started; gateway=loopback")
	must(runner.run(ctx))
}

func (w *worker) run(ctx context.Context) error {
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
	finalizeCtx, finalizeCancel := workerFinalizationContext(parent)
	defer finalizeCancel()
	for _, file := range files {
		if err := w.uploadArtifact(finalizeCtx, job.JobID, file); err != nil {
			fmt.Fprintln(os.Stderr, "artifact upload failed job=", safeID(job.JobID), "class=artifact_upload_failed")
			return
		}
	}
	if err := w.submitResult(finalizeCtx, job.JobID, result); err != nil {
		diagnosticCode := "none"
		if result.FailureDiagnostic != nil {
			diagnosticCode = safeID(result.FailureDiagnostic.Error.Code)
		}
		fmt.Fprintln(os.Stderr, "result submit failed job=", safeID(job.JobID), "class=result_submit_failed", "gateway_code=", gatewayErrorCode(err), "result_status=", safeID(string(result.Status)), "diagnostic_code=", diagnosticCode)
		return
	}
	completed = true
	fmt.Fprintln(os.Stdout, "job completed job=", safeID(job.JobID), "artifacts=", len(files))
}

func workerFinalizationContext(parent context.Context) (context.Context, context.CancelFunc) {
	// Execution may legitimately consume its full budget while producing a
	// structured failure package. Detach cancellation for the bounded result
	// handoff so an expired browser context cannot make the diagnostic vanish.
	return context.WithTimeout(context.WithoutCancel(parent), 2*time.Minute)
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
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}
	response, err := w.request(ctx, http.MethodPut, "/v1/worker/jobs/"+url.PathEscape(jobID)+"/result", bytes.NewReader(body), map[string]string{"Content-Type": "application/json"})
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
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(data, &payload)
	return &workerGatewayError{status: response.StatusCode, code: safeID(payload.Error.Code)}
}

type workerGatewayError struct {
	status int
	code   string
}

func (e *workerGatewayError) Error() string {
	return fmt.Sprintf("worker gateway HTTP %d code=%s", e.status, firstNonEmptyWorker(e.code, "unknown"))
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
