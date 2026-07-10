package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"cascade-demoops/backend/internal/driver"
	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/model"
)

func (s *Service) RunUploadedExecutionPackage(ctx context.Context, orgID string, exchangePackageID string) (model.ExecutionPackageStatusResponse, error) {
	pkg, cloudJobID, err := s.exchange.StartExecution(ctx, orgID, exchangePackageID)
	if err != nil {
		return model.ExecutionPackageStatusResponse{}, err
	}

	_, _ = s.exchange.MarkExecutionStage(ctx, orgID, exchangePackageID, "preparing_worker", "Checking local video-worker, Node runtime, and artifact output directories.", 35)
	workerPath := s.localVideoWorkerPath()
	if workerPath == "" {
		return s.failUploadedExecution(ctx, orgID, exchangePackageID, "video_worker_missing", errors.New("video worker path is not configured"))
	}
	if _, statErr := os.Stat(workerPath); statErr != nil {
		return s.failUploadedExecution(ctx, orgID, exchangePackageID, "video_worker_missing", statErr)
	}
	if nodeErr := checkCommandReady(s.nodeBinaryForExecution()); nodeErr != nil {
		return s.failUploadedExecution(ctx, orgID, exchangePackageID, "node_runtime_missing", nodeErr)
	}

	outputRoot := filepath.Join(s.runtime.ArtifactRoot, "exchange", safePathSegment(exchangePackageID))
	recordingDir := filepath.Join(outputRoot, "recording")
	renderDir := filepath.Join(outputRoot, "render")
	localDriver := driver.NewLocalDriver(s.nodeBinaryForExecution(), workerPath)
	result, err := executor.RunClientExecutionRecordingAndRender(ctx, localDriver, executor.RecordingRenderPipelineRequest{
		SourcePackage:      &pkg,
		CloudJobID:         cloudJobID,
		RecordingOutputDir: recordingDir,
		RenderOutputDir:    renderDir,
		ResultCreatedAt:    time.Now().UTC(),
		Progress: func(stage string, message string, progress int) {
			_, _ = s.exchange.MarkExecutionStage(ctx, orgID, exchangePackageID, stage, message, progress)
		},
	})
	if err != nil {
		return s.failUploadedExecution(ctx, orgID, exchangePackageID, "recording_render_failed", err)
	}
	return s.exchange.CompleteWithRecordingResult(ctx, orgID, exchangePackageID, result.RecordingResultPackage)
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
			VideoWorkerReady: fileExists(workerPath),
			NodeBinary:       nodeBinary,
			NodeReady:        commandReady(nodeBinary),
			FFmpegReady:      commandReady(os.Getenv("CASCADE_FFMPEG_PATH")),
			LLMMode:          string(s.runtime.LLMMode),
		},
		Package: debugPackageSummary(pkg),
		Result:  status.ResultSummary,
		Failure: status.FailureSummary,
	}, nil
}

func (s *Service) failUploadedExecution(ctx context.Context, orgID string, exchangePackageID string, code string, err error) (model.ExecutionPackageStatusResponse, error) {
	status, failErr := s.exchange.FailExecution(ctx, orgID, exchangePackageID, code, err)
	if failErr != nil && status.ExchangePackageID == "" {
		return status, failErr
	}
	return status, nil
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
