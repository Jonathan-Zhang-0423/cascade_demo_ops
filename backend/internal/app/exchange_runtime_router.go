package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"cascade-demoops/backend/internal/driver"
	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/model"
)

const (
	runtimeErrorUnsupported                 = "unsupported_runtime"
	runtimeErrorOutlineRunnerUnavailable    = "browser_agent_outline_runner_unavailable"
	runtimeErrorVideoWorkerMissing          = "video_worker_missing"
	runtimeErrorNodeMissing                 = "node_runtime_missing"
	runtimeErrorLegacyRecordingRenderFailed = "recording_render_failed"
)

type executionRuntimeRequest struct {
	Package            *model.ClientExecutionPackage
	CloudJobID         string
	RecordingOutputDir string
	RenderOutputDir    string
	ResultCreatedAt    time.Time
	Progress           func(stage string, message string, progress int)
	TaskSecrets        map[string]driver.BrowserAgentTaskSecret
}

// BrowserAgentOutlineRunRequest is the Server-internal handoff to the new
// Outline Runner. It deliberately carries the approved package, not Playwright
// source or a live page object.
type BrowserAgentOutlineRunRequest struct {
	Package            *model.ClientExecutionPackage
	RuntimePlan        BrowserAgentRuntimePlan
	CloudJobID         string
	RecordingOutputDir string
	RenderOutputDir    string
	ResultCreatedAt    time.Time
	Progress           func(stage string, message string, progress int)
	EventSink          StageExecutionEventSink
	TaskSecrets        map[string]driver.BrowserAgentTaskSecret
}

type BrowserAgentOutlineRunner interface {
	Run(context.Context, BrowserAgentOutlineRunRequest) (model.RecordingResultPackage, error)
}

type legacyExecutionRuntimeRunner interface {
	Run(context.Context, executionRuntimeRequest) (model.RecordingResultPackage, error)
}

type executionRuntimeRouter struct {
	legacy  legacyExecutionRuntimeRunner
	outline BrowserAgentOutlineRunner
}

func newExecutionRuntimeRouter(legacy legacyExecutionRuntimeRunner, outline BrowserAgentOutlineRunner) executionRuntimeRouter {
	return executionRuntimeRouter{legacy: legacy, outline: outline}
}

func (r executionRuntimeRouter) Run(ctx context.Context, request executionRuntimeRequest) (model.RecordingResultPackage, error) {
	if request.Package == nil || request.Package.ExecutableScriptBundle == nil {
		return model.RecordingResultPackage{}, newRuntimeExecutionError(runtimeErrorUnsupported, errors.New("execution package runtime is missing"))
	}
	runtimeName := request.Package.ExecutableScriptBundle.ScriptManifest.Runtime
	switch runtimeName {
	case model.ExecutableScriptRuntimePlaywrightRestrictedSandbox:
		// Frozen compatibility branch: only historical packages and regression checks use it.
		// New Browser Agent work must stay on the Outline branch below.
		if r.legacy == nil {
			return model.RecordingResultPackage{}, newRuntimeExecutionError(runtimeErrorVideoWorkerMissing, errors.New("legacy Playwright runner is not configured"))
		}
		return r.legacy.Run(ctx, request)
	case model.ExecutableScriptRuntimeBrowserAgentOutlineV1:
		if r.outline == nil {
			return model.RecordingResultPackage{}, newRuntimeExecutionError(runtimeErrorOutlineRunnerUnavailable, errors.New("browser-agent outline runner is not configured"))
		}
		orchestrator := newBrowserAgentStageOrchestrator(contractBrowserAgentPolicyGuard{})
		runtimePlan, err := orchestrator.Prepare(request.Package)
		if err != nil {
			return model.RecordingResultPackage{}, err
		}
		eventSink, err := newStageEventAuditLog(request.RecordingOutputDir, request.CloudJobID)
		if err != nil {
			return model.RecordingResultPackage{}, newRuntimeExecutionError("stage_event_audit_unavailable", err)
		}
		result, err := r.outline.Run(ctx, BrowserAgentOutlineRunRequest{
			Package: request.Package, RuntimePlan: runtimePlan, CloudJobID: request.CloudJobID,
			RecordingOutputDir: request.RecordingOutputDir, RenderOutputDir: request.RenderOutputDir,
			ResultCreatedAt: request.ResultCreatedAt, Progress: request.Progress, EventSink: eventSink, TaskSecrets: request.TaskSecrets,
		})
		if err != nil {
			return model.RecordingResultPackage{}, err
		}
		result.ExecutionRuntime = runtimeName
		if eventSink != nil && eventSink.Count() > 0 {
			artifact, artifactErr := eventSink.ArtifactRef()
			if artifactErr != nil {
				return model.RecordingResultPackage{}, newRuntimeExecutionError("stage_event_audit_unavailable", artifactErr)
			}
			result.StageEventLogRef = &artifact
		}
		// Formal Direct results must carry a replay manifest that indexes the
		// same runtime events, validation reports and final rendered assets. Build
		// it after the runner returns so the manifest includes the stage log and
		// editor-facing render artifacts produced by the Outline Runner.
		if eventSink != nil {
			manifest, manifestErr := BuildReplayManifest(BuildReplayManifestInput{
				Result: result, Events: eventSink.Events(), Package: *request.Package,
				RunID: runtimePlan.RunID, EventDir: request.RecordingOutputDir,
				CreatedAt: request.ResultCreatedAt,
			})
			if manifestErr != nil {
				return model.RecordingResultPackage{}, newRuntimeExecutionError("replay_manifest_unavailable", manifestErr)
			}
			if err := AttachReplayManifestArtifact(&result, manifest); err != nil {
				return model.RecordingResultPackage{}, newRuntimeExecutionError("replay_manifest_unavailable", err)
			}
		}
		return result, nil
	default:
		return model.RecordingResultPackage{}, newRuntimeExecutionError(runtimeErrorUnsupported, fmt.Errorf("execution package runtime %q is not supported", runtimeName))
	}
}

type localLegacyPlaywrightRunner struct {
	service *Service
}

func (r localLegacyPlaywrightRunner) Run(ctx context.Context, request executionRuntimeRequest) (model.RecordingResultPackage, error) {
	if r.service == nil {
		return model.RecordingResultPackage{}, newRuntimeExecutionError(runtimeErrorVideoWorkerMissing, errors.New("legacy Playwright runner service is not configured"))
	}
	if request.Progress != nil {
		request.Progress("preparing_worker", "Checking local video-worker, Node runtime, and artifact output directories.", 35)
	}
	workerPath := r.service.localVideoWorkerPath()
	if workerPath == "" {
		return model.RecordingResultPackage{}, newRuntimeExecutionError(runtimeErrorVideoWorkerMissing, errors.New("video worker path is not configured"))
	}
	if _, err := os.Stat(workerPath); err != nil {
		return model.RecordingResultPackage{}, newRuntimeExecutionError(runtimeErrorVideoWorkerMissing, err)
	}
	if err := checkCommandReady(r.service.nodeBinaryForExecution()); err != nil {
		return model.RecordingResultPackage{}, newRuntimeExecutionError(runtimeErrorNodeMissing, err)
	}

	localDriver := driver.NewLocalDriver(r.service.nodeBinaryForExecution(), workerPath, r.service.videoWorkerEnvironment())
	result, err := executor.RunClientExecutionRecordingAndRender(ctx, localDriver, executor.RecordingRenderPipelineRequest{
		SourcePackage: request.Package, CloudJobID: request.CloudJobID,
		RecordingOutputDir: request.RecordingOutputDir, RenderOutputDir: request.RenderOutputDir,
		ResultCreatedAt: request.ResultCreatedAt, Progress: request.Progress,
	})
	if err != nil {
		return model.RecordingResultPackage{}, newRuntimeExecutionError(runtimeErrorLegacyRecordingRenderFailed, err)
	}
	return result.RecordingResultPackage, nil
}

type unavailableBrowserAgentOutlineRunner struct{}

func (unavailableBrowserAgentOutlineRunner) Run(ctx context.Context, request BrowserAgentOutlineRunRequest) (model.RecordingResultPackage, error) {
	if request.Progress != nil {
		request.Progress("validating_pre_execution", "Validating the approved Browser Agent stages and policy boundaries.", 36)
	}
	if len(request.RuntimePlan.Stages) == 0 {
		return model.RecordingResultPackage{}, newRuntimeExecutionError(runtimeErrorBrowserAgentContractViolation, errors.New("compiled browser-agent runtime plan is missing"))
	}
	if err := ctx.Err(); err != nil {
		return model.RecordingResultPackage{}, err
	}
	return model.RecordingResultPackage{}, newRuntimeExecutionError(runtimeErrorOutlineRunnerUnavailable, errors.New("browser-agent outline runner is not available in this build"))
}

type runtimeExecutionError struct {
	code string
	err  error
}

func newRuntimeExecutionError(code string, err error) error {
	return &runtimeExecutionError{code: code, err: err}
}

func (e *runtimeExecutionError) Error() string {
	if e == nil || e.err == nil {
		return "runtime execution failed"
	}
	return e.err.Error()
}

func (e *runtimeExecutionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func runtimeExecutionErrorCode(err error) string {
	var runtimeErr *runtimeExecutionError
	if errors.As(err, &runtimeErr) && runtimeErr.code != "" {
		return runtimeErr.code
	}
	return "execution_failed"
}

func executionRuntimeOutputDirs(root string, exchangePackageID string) (string, string) {
	outputRoot := filepath.Join(root, "exchange", safePathSegment(exchangePackageID))
	return filepath.Join(outputRoot, "recording"), filepath.Join(outputRoot, "render")
}
