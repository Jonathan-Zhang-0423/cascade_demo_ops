package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"cascade-demoops/backend/internal/model"
)

func (s *Service) RunUploadedExecutionPackage(ctx context.Context, orgID string, exchangePackageID string) (model.ExecutionPackageStatusResponse, error) {
	started, err := s.exchange.TryStartExecution(ctx, orgID, exchangePackageID)
	if err != nil {
		return model.ExecutionPackageStatusResponse{}, err
	}
	if !started.Started {
		return started.Status, nil
	}
	runCtx, cancel := context.WithCancel(context.Background())
	s.registerRunningExecution(orgID, exchangePackageID, cancel)
	go s.runUploadedExecutionPackage(runCtx, orgID, exchangePackageID, started.Payload, started.CloudJobID, started.TimeoutSec)
	return started.Status, nil
}

func (s *Service) runUploadedExecutionPackage(ctx context.Context, orgID string, exchangePackageID string, pkg model.ClientExecutionPackage, cloudJobID string, timeoutSec int) {
	defer s.unregisterRunningExecution(orgID, exchangePackageID)
	timeout := executionTimeout(timeoutSec)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	status, err := s.runUploadedExecutionPackageSync(runCtx, orgID, exchangePackageID, pkg, cloudJobID)
	if errors.Is(runCtx.Err(), context.Canceled) {
		_, _ = s.exchange.CancelExecution(context.Background(), orgID, exchangePackageID, "canceled_by_dev_request")
		return
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) && shouldMarkExecutionTimeout(status) {
		_, _ = s.failUploadedExecution(context.Background(), orgID, exchangePackageID, "execution_timeout", context.DeadlineExceeded)
		return
	}
	_ = err
}

func (s *Service) runUploadedExecutionPackageSync(ctx context.Context, orgID string, exchangePackageID string, pkg model.ClientExecutionPackage, cloudJobID string) (model.ExecutionPackageStatusResponse, error) {
	recordingDir, renderDir := executionRuntimeOutputDirs(s.runtime.ArtifactRoot, exchangePackageID)
	router := newExecutionRuntimeRouter(localLegacyPlaywrightRunner{service: s}, s.outlineRunner)
	result, err := router.Run(ctx, executionRuntimeRequest{
		Package: &pkg, CloudJobID: cloudJobID,
		RecordingOutputDir: recordingDir, RenderOutputDir: renderDir, ResultCreatedAt: time.Now().UTC(),
		Progress: func(stage string, message string, progress int) {
			_, _ = s.exchange.MarkExecutionStage(ctx, orgID, exchangePackageID, stage, message, progress)
		},
	})
	if err != nil {
		return s.failUploadedExecution(ctx, orgID, exchangePackageID, runtimeExecutionErrorCode(err), err)
	}
	return s.exchange.CompleteWithRecordingResult(ctx, orgID, exchangePackageID, result)
}

func (s *Service) GetExecutionPackageDebug(ctx context.Context, orgID string, exchangePackageID string) (ExecutionPackageDebugView, error) {
	status, err := s.exchange.Status(ctx, orgID, exchangePackageID)
	if err != nil {
		return ExecutionPackageDebugView{}, err
	}
	pkg, err := s.exchange.PayloadSnapshot(ctx, orgID, exchangePackageID)
	if err != nil {
		return ExecutionPackageDebugView{}, err
	}
	workerPath := s.localVideoWorkerPath()
	nodeBinary := s.nodeBinaryForExecution()
	workerReady := fileExists(workerPath)
	nodeReady := commandReady(nodeBinary)
	ffmpegReady := commandReady(os.Getenv("CASCADE_FFMPEG_PATH"))
	return ExecutionPackageDebugView{
		ExchangePackageID: exchangePackageID,
		CloudJobID:        status.CloudJobID,
		Status:            status,
		Runtime: ExecutionDebugRuntime{
			GOOS:             runtime.GOOS,
			GOARCH:           runtime.GOARCH,
			Profile:          string(s.runtime.Profile),
			Environment:      s.runtime.Environment,
			ArtifactRoot:     s.runtime.ArtifactRoot,
			VideoWorkerPath:  workerPath,
			VideoWorkerReady: workerReady,
			NodeBinary:       nodeBinary,
			NodeReady:        nodeReady,
			FFmpegReady:      ffmpegReady,
			LLMMode:          string(s.runtime.LLMMode),
			ArkMediaMode:     string(s.runtime.ArkMediaMode),
		},
		Package:   debugPackageSummary(pkg),
		Readiness: debugExecutionReadiness(status, pkg, workerReady, nodeReady),
		Result:    status.ResultSummary,
		Failure:   status.FailureSummary,
	}, nil
}

func (s *Service) failUploadedExecution(ctx context.Context, orgID string, exchangePackageID string, code string, err error) (model.ExecutionPackageStatusResponse, error) {
	status, failErr := s.exchange.FailExecution(ctx, orgID, exchangePackageID, code, err)
	if failErr != nil && status.ExchangePackageID == "" {
		return status, failErr
	}
	return status, nil
}

func executionTimeout(timeoutSec int) time.Duration {
	if timeoutSec <= 0 {
		timeoutSec = defaultExecutionTimeoutSec
	}
	return time.Duration(timeoutSec) * time.Second
}

func shouldMarkExecutionTimeout(status model.ExecutionPackageStatusResponse) bool {
	if status.ExchangePackageID == "" {
		return true
	}
	if isTerminalExchangeStatus(status.Status) {
		return false
	}
	return status.Status != model.ExchangePackageStatusFailed || status.Error == nil || status.Error.Code != "execution_timeout"
}

func (s *Service) registerRunningExecution(orgID string, exchangePackageID string, cancel context.CancelFunc) {
	if s == nil || cancel == nil {
		return
	}
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	if s.runningTasks == nil {
		s.runningTasks = map[string]context.CancelFunc{}
	}
	s.runningTasks[runningExecutionKey(orgID, exchangePackageID)] = cancel
}

func (s *Service) unregisterRunningExecution(orgID string, exchangePackageID string) {
	if s == nil {
		return
	}
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	delete(s.runningTasks, runningExecutionKey(orgID, exchangePackageID))
}

func (s *Service) cancelRunningExecution(orgID string, exchangePackageID string) {
	if s == nil {
		return
	}
	s.runningMu.Lock()
	cancel := s.runningTasks[runningExecutionKey(orgID, exchangePackageID)]
	s.runningMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func runningExecutionKey(orgID string, exchangePackageID string) string {
	return orgID + "\x00" + exchangePackageID
}

func (s *Service) localVideoWorkerPath() string {
	if s == nil {
		return ""
	}
	if s.runtime.SidecarPaths != nil && s.runtime.SidecarPaths["video-worker"] != "" {
		return s.runtime.SidecarPaths["video-worker"]
	}
	if s.runtime.DevRepoRoot != "" {
		return filepath.Join(s.runtime.DevRepoRoot, "video-worker", "dist", "index.js")
	}
	return ""
}

func (s *Service) nodeBinaryForExecution() string {
	if s == nil || s.runtime.NodeBinaryPath == "" {
		return "node"
	}
	return s.runtime.NodeBinaryPath
}

type ExecutionPackageDebugView struct {
	ExchangePackageID string                               `json:"exchange_package_id"`
	CloudJobID        string                               `json:"cloud_job_id,omitempty"`
	Status            model.ExecutionPackageStatusResponse `json:"status"`
	Runtime           ExecutionDebugRuntime                `json:"runtime"`
	Package           ExecutionDebugPackageSummary         `json:"package"`
	Readiness         ExecutionDebugReadiness              `json:"readiness"`
	Result            *model.ExecutionResultSummary        `json:"result,omitempty"`
	Failure           *model.ExecutionFailureSummary       `json:"failure,omitempty"`
}

type ExecutionDebugRuntime struct {
	GOOS             string `json:"goos"`
	GOARCH           string `json:"goarch"`
	Profile          string `json:"profile,omitempty"`
	Environment      string `json:"environment,omitempty"`
	ArtifactRoot     string `json:"artifact_root,omitempty"`
	VideoWorkerPath  string `json:"video_worker_path,omitempty"`
	VideoWorkerReady bool   `json:"video_worker_ready"`
	NodeBinary       string `json:"node_binary,omitempty"`
	NodeReady        bool   `json:"node_ready"`
	FFmpegReady      bool   `json:"ffmpeg_ready"`
	LLMMode          string `json:"llm_mode,omitempty"`
	ArkMediaMode     string `json:"ark_media_mode,omitempty"`
}

type ExecutionDebugPackageSummary struct {
	PackageID             string   `json:"package_id,omitempty"`
	OrgID                 string   `json:"org_id,omitempty"`
	ProjectID             string   `json:"project_id,omitempty"`
	SchemaVersion         string   `json:"schema_version,omitempty"`
	ProductURL            string   `json:"product_url,omitempty"`
	BaseURL               string   `json:"base_url,omitempty"`
	AllowedDomains        []string `json:"allowed_domains,omitempty"`
	WorkflowGraphID       string   `json:"workflow_graph_id,omitempty"`
	WorkflowNodeCount     int      `json:"workflow_node_count,omitempty"`
	ScriptStepCount       int      `json:"script_step_count,omitempty"`
	ScriptRuntime         string   `json:"script_runtime,omitempty"`
	ScriptHashConfigured  bool     `json:"script_hash_configured"`
	RawRecordingRequested bool     `json:"raw_recording_requested"`
	TraceRequested        bool     `json:"trace_requested"`
	FinalVideoRequested   bool     `json:"final_video_requested"`
}

type ExecutionDebugReadiness struct {
	CanRun   bool                    `json:"can_run"`
	Blockers []ExecutionDebugBlocker `json:"blockers,omitempty"`
}

type ExecutionDebugBlocker struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func debugPackageSummary(pkg model.ClientExecutionPackage) ExecutionDebugPackageSummary {
	summary := ExecutionDebugPackageSummary{
		PackageID:             pkg.PackageID,
		OrgID:                 pkg.OrgID,
		ProjectID:             pkg.ProjectID,
		SchemaVersion:         pkg.SchemaVersion,
		ProductURL:            pkg.ProjectContextSummary.ProductURL,
		BaseURL:               pkg.RecordingRunSpec.BaseURL,
		AllowedDomains:        append([]string{}, pkg.RecordingRunSpec.AllowedDomains...),
		RawRecordingRequested: pkg.RecordingRunSpec.Outputs.RawRecording,
		TraceRequested:        pkg.RecordingRunSpec.Outputs.Trace,
		FinalVideoRequested:   pkg.RecordingRunSpec.Outputs.FinalVideo,
	}
	if pkg.WorkflowGraph != nil {
		summary.WorkflowGraphID = pkg.WorkflowGraph.ID
		summary.WorkflowNodeCount = len(pkg.WorkflowGraph.Nodes)
	}
	if pkg.ExecutableScriptBundle != nil {
		summary.ScriptRuntime = pkg.ExecutableScriptBundle.ScriptManifest.Runtime
		summary.ScriptHashConfigured = pkg.ExecutableScriptBundle.PlaywrightScript.SHA256 != ""
		if pkg.ExecutableScriptBundle.PlanJSON != nil {
			summary.ScriptStepCount = len(pkg.ExecutableScriptBundle.PlanJSON.Steps)
		}
	}
	return summary
}

func debugExecutionReadiness(status model.ExecutionPackageStatusResponse, pkg model.ClientExecutionPackage, workerReady bool, nodeReady bool) ExecutionDebugReadiness {
	blockers := []ExecutionDebugBlocker{}
	if isTerminalExchangeStatus(status.Status) {
		blockers = append(blockers, ExecutionDebugBlocker{Code: "execution_terminal", Message: "Execution is already terminal and cannot be started again."})
	}
	if status.Status == model.ExchangePackageStatusRunning || status.Status == model.ExchangePackageStatusQueued {
		blockers = append(blockers, ExecutionDebugBlocker{Code: "execution_already_started", Message: "Execution is already running or queued."})
	}
	if pkg.PackageID == "" {
		blockers = append(blockers, ExecutionDebugBlocker{Code: "payload_unavailable", Message: "Plain client execution payload is not available to the cloud-side runner."})
	}
	if !workerReady {
		blockers = append(blockers, ExecutionDebugBlocker{Code: "video_worker_missing", Message: "Video-worker build artifact is not configured or not present."})
	}
	if !nodeReady {
		blockers = append(blockers, ExecutionDebugBlocker{Code: "node_runtime_missing", Message: "Node runtime is not available for the video-worker."})
	}
	return ExecutionDebugReadiness{CanRun: len(blockers) == 0, Blockers: blockers}
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func commandReady(command string) bool {
	return checkCommandReady(command) == nil
}

func checkCommandReady(command string) error {
	if command == "" {
		command = "ffmpeg"
	}
	if filepath.IsAbs(command) {
		if fileExists(command) {
			return nil
		}
		return os.ErrNotExist
	}
	_, err := exec.LookPath(command)
	return err
}

func safePathSegment(value string) string {
	if value == "" {
		return "item"
	}
	out := []rune(value)
	for index, char := range out {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' {
			continue
		}
		out[index] = '_'
	}
	return string(out)
}
