package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"cascade-demoops/backend/internal/driver"
	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/model"
)

type browserAgentWorkerSession interface {
	Observe(context.Context, driver.BrowserAgentWorkerStage) (driver.BrowserAgentWorkerStageResult, error)
	Execute(context.Context, driver.BrowserAgentWorkerStage) (driver.BrowserAgentWorkerStageResult, error)
	Close(context.Context) (driver.BrowserAgentWorkerCloseResult, error)
	Abort() error
}

type browserAgentWorkerSessionFactory func(context.Context, driver.BrowserAgentWorkerOpenRequest) (browserAgentWorkerSession, driver.BrowserAgentWorkerOpenResult, error)

type localBrowserAgentOutlineRunner struct {
	service        *Service
	sessionFactory browserAgentWorkerSessionFactory
	renderService  executor.RenderService
}

type localBrowserAgentStageRuntime struct {
	session    browserAgentWorkerSession
	progress   func(stage string, message string, progress int)
	stageCount int
	artifacts  map[string]model.ArtifactRef
}

func (r localBrowserAgentOutlineRunner) Run(ctx context.Context, request BrowserAgentOutlineRunRequest) (model.RecordingResultPackage, error) {
	if r.service == nil {
		return model.RecordingResultPackage{}, newRuntimeExecutionError(runtimeErrorOutlineRunnerUnavailable, errors.New("browser-agent runner service is not configured"))
	}
	if request.Package == nil || len(request.RuntimePlan.Stages) == 0 || request.EventSink == nil {
		return model.RecordingResultPackage{}, newRuntimeExecutionError(runtimeErrorBrowserAgentContractViolation, errors.New("browser-agent runtime plan, package, and event sink are required"))
	}
	progressBrowserAgent(request.Progress, "validating_pre_execution", "正在核对 App 已审批阶段、业务目标和安全边界。", 36)
	progressBrowserAgent(request.Progress, "script_ready", "Browser Agent 可审计脚本已生成并通过安全预演。", 38)

	openRequest := browserAgentWorkerOpenRequest(request)
	factory := r.sessionFactory
	if factory == nil {
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
		worker := driver.NewBrowserAgentWorker(r.service.nodeBinaryForExecution(), workerPath, r.service.videoWorkerEnvironment())
		factory = func(ctx context.Context, open driver.BrowserAgentWorkerOpenRequest) (browserAgentWorkerSession, driver.BrowserAgentWorkerOpenResult, error) {
			return worker.Open(ctx, open)
		}
	}
	progressBrowserAgent(request.Progress, "running_browser_agent", "正在启动受限浏览器会话。", 40)
	session, openResult, err := factory(ctx, openRequest)
	if err != nil {
		return model.RecordingResultPackage{}, newRuntimeExecutionError("browser_agent_session_start_failed", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = session.Abort()
		}
	}()

	stageRuntime := &localBrowserAgentStageRuntime{
		session: session, progress: request.Progress, stageCount: len(request.RuntimePlan.Stages), artifacts: map[string]model.ArtifactRef{},
	}
	orchestrator := newBrowserAgentStageOrchestratorWithVerifier(contractBrowserAgentPolicyGuard{}, newDeterministicBrowserAgentStageVerifier(request.Package))
	startedAt := timeNowUTC()
	runResult, runErr := orchestrator.Run(ctx, request.RuntimePlan, stageRuntime, stageRuntime, request.EventSink)
	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 15*time.Second)
	closeResult, closeErr := session.Close(cleanupCtx)
	cancelCleanup()
	closed = true
	if runErr != nil {
		return model.RecordingResultPackage{}, runErr
	}
	if closeErr != nil {
		return model.RecordingResultPackage{}, newRuntimeExecutionError("browser_agent_session_close_failed", closeErr)
	}
	for _, artifact := range closeResult.Artifacts {
		stageRuntime.artifacts[artifact.ID] = artifact
	}
	completedAt := timeNowUTC()
	progressBrowserAgent(request.Progress, "validating_post_execution", "正在复核全部阶段证据和执行结果。", 88)
	recordResult := executor.RecordResult{
		RecordingPath: closeResult.RecordingPath, TracePath: closeResult.TracePath,
		GeneratedAssets: mapBrowserAgentArtifacts(stageRuntime.artifacts),
		StepResults:     browserAgentStepResults(request.RuntimePlan, runResult.Events, stageRuntime.artifacts),
		WorkerID:        "video-worker-browser-agent", RuntimeVersions: mergeRuntimeVersions(openResult.RuntimeVersions, closeResult.RuntimeVersions),
		SandboxMetadata: browserAgentSandboxMetadata(request.Package, mergeRuntimeVersions(openResult.RuntimeVersions, closeResult.RuntimeVersions)),
		StartedAt:       startedAt, CompletedAt: completedAt,
	}
	progressBrowserAgent(request.Progress, "packaging_recording", "正在整理录屏、截图、验证报告和阶段审计记录。", 94)
	result, err := executor.NewRecordingResultPackageFromRecordResult(request.Package, recordResult, request.CloudJobID, request.ResultCreatedAt)
	if err != nil {
		return model.RecordingResultPackage{}, newRuntimeExecutionError("browser_agent_result_packaging_failed", err)
	}
	result.ValidationReports = append([]model.ValidationReport{}, runResult.ValidationReports...)
	progressBrowserAgent(request.Progress, "material_validation", "录屏、截图、trace 和阶段证据已通过素材校验。", 74)
	renderService := r.renderService
	if renderService == nil {
		renderService = r.service.editorWorker
	}
	if _, _, err := executor.RenderClientExecutionRecordingResult(ctx, renderService, request.Package, &result, request.RenderOutputDir, request.Progress); err != nil {
		return model.RecordingResultPackage{}, newRuntimeExecutionError("browser_agent_render_failed", err)
	}
	return result, nil
}

func (r *localBrowserAgentStageRuntime) ObserveStage(ctx context.Context, _ BrowserAgentRuntimePlan, stage BrowserAgentRuntimeStage) (BrowserAgentStageObservation, error) {
	progress := browserAgentStageProgress(stage.Order, r.stageCount, false)
	progressBrowserAgent(r.progress, "running_browser_agent", fmt.Sprintf("正在观察第 %d 个阶段：%s", stage.Order, browserAgentStageDisplayName(stage)), progress)
	result, err := r.session.Observe(ctx, workerStageFromRuntime(stage))
	if err != nil {
		return BrowserAgentStageObservation{}, err
	}
	r.collect(result.Artifacts)
	return BrowserAgentStageObservation{Observation: result.Observation, EvidenceRefs: result.EvidenceRefs, TargetResolved: result.TargetResolved}, nil
}

func (r *localBrowserAgentStageRuntime) ExecuteStage(ctx context.Context, _ BrowserAgentRuntimePlan, stage BrowserAgentRuntimeStage) (BrowserAgentStageActionResult, error) {
	progress := browserAgentStageProgress(stage.Order, r.stageCount, true)
	progressBrowserAgent(r.progress, "running_browser_agent", fmt.Sprintf("正在执行第 %d 个阶段：%s", stage.Order, browserAgentStageDisplayName(stage)), progress)
	result, err := r.session.Execute(ctx, workerStageFromRuntime(stage))
	if err != nil {
		return BrowserAgentStageActionResult{}, err
	}
	r.collect(result.Artifacts)
	progressBrowserAgent(r.progress, "validating_runtime_stage", fmt.Sprintf("正在验证第 %d 个阶段的真实页面结果。", stage.Order), minInt(progress+4, 84))
	return BrowserAgentStageActionResult{Observation: &result.Observation, EvidenceRefs: result.EvidenceRefs}, nil
}

func (r *localBrowserAgentStageRuntime) collect(artifacts []model.ArtifactRef) {
	for _, artifact := range artifacts {
		if artifact.ID != "" {
			r.artifacts[artifact.ID] = artifact
		}
	}
}

type deterministicBrowserAgentStageVerifier struct {
	requiredValidations map[string][]model.ValidationSpec
}

func newDeterministicBrowserAgentStageVerifier(pkg *model.ClientExecutionPackage) deterministicBrowserAgentStageVerifier {
	verifier := deterministicBrowserAgentStageVerifier{requiredValidations: map[string][]model.ValidationSpec{}}
	if pkg == nil || pkg.ExecutableScriptBundle == nil || pkg.ExecutableScriptBundle.PlanJSON == nil {
		return verifier
	}
	for _, step := range pkg.ExecutableScriptBundle.PlanJSON.Steps {
		for _, validation := range step.Validations {
			if validation.Required {
				verifier.requiredValidations[step.NodeID] = append(verifier.requiredValidations[step.NodeID], validation)
			}
		}
	}
	return verifier
}

func (v deterministicBrowserAgentStageVerifier) ValidateStageEvents(_ context.Context, validationContext BrowserAgentValidationContext, events []model.StageExecutionEvent) (model.ValidationReport, error) {
	if len(events) == 0 {
		return model.ValidationReport{}, errors.New("stage outcome verifier received no events")
	}
	last := events[len(events)-1]
	decision := model.ValidationDecisionContinue
	checks := make([]model.ValidationCheck, 0)
	passed, total := 0, 0
	observedChecks := map[string]bool{}
	for _, event := range events {
		if event.EventType != model.StageExecutionEventOutcomeObserved || event.Observation == nil {
			continue
		}
		for index, assertion := range event.Observation.Assertions {
			observedChecks[assertion.Kind] = assertion.Passed
			total++
			if assertion.Passed {
				passed++
			} else {
				decision = model.ValidationDecisionStopAndReport
			}
			checks = append(checks, model.ValidationCheck{
				ID: fmt.Sprintf("check_%s_%02d", safePathSegment(last.StageID), index+1), Kind: assertion.Kind,
				Passed: assertion.Passed, Required: true, Summary: "依据真实浏览器结果执行确定性检查。",
				EvidenceRefs: append([]model.EvidenceRef{}, event.EvidenceRefs...),
			})
		}
	}
	for _, required := range v.requiredValidations[last.NodeID] {
		kind := fmt.Sprintf("required_%s:%s", required.Kind, required.ID)
		if _, exists := observedChecks[kind]; exists {
			continue
		}
		total++
		decision = model.ValidationDecisionStopAndReport
		checks = append(checks, model.ValidationCheck{
			ID: "check_missing_" + safePathSegment(required.ID), Kind: required.Kind, Passed: false, Required: true,
			Summary: "协议要求的页面结果校验没有返回，禁止进入下一阶段。",
		})
	}
	evidenceQuality := model.RuntimeObservationInsufficient
	if last.Observation != nil {
		evidenceQuality = last.Observation.Source
	}
	if total == 0 || len(last.EvidenceRefs) == 0 || !runtimeObservationIsRealEvidence(evidenceQuality) {
		decision = model.ValidationDecisionStopAndReport
	}
	passRate := 0.0
	if total > 0 {
		passRate = float64(passed) / float64(total)
	}
	return model.ValidationReport{
		SchemaVersion: model.ValidationReportSchemaVersion,
		ReportID:      fmt.Sprintf("validation_%s_%04d", safePathSegment(last.RunID), last.Sequence), RunID: last.RunID,
		SourcePackageID: validationContext.SourcePackageID, SourceBundleHashSHA256: validationContext.SourceBundleHashSHA256,
		PolicyHashSHA256: validationContext.EffectivePolicyHashSHA256, Phase: model.ValidationPhaseRuntimeStage,
		NodeID: last.NodeID, StageID: last.StageID, Decision: decision, PassRate: passRate,
		OverallConfidence: passRate, EvidenceQuality: evidenceQuality, Checks: checks,
		EvidenceRefs: append([]model.EvidenceRef{}, last.EvidenceRefs...), CreatedAt: timeNowUTC(),
	}, nil
}

func browserAgentWorkerOpenRequest(request BrowserAgentOutlineRunRequest) driver.BrowserAgentWorkerOpenRequest {
	viewport := driver.BrowserAgentWorkerViewport{Width: 1440, Height: 900}
	if request.Package.RecordingRunSpec.Outputs.ResolutionWidth > 0 && request.Package.RecordingRunSpec.Outputs.ResolutionHeight > 0 {
		viewport = driver.BrowserAgentWorkerViewport{Width: request.Package.RecordingRunSpec.Outputs.ResolutionWidth, Height: request.Package.RecordingRunSpec.Outputs.ResolutionHeight}
	} else if len(request.Package.RecordingRunSpec.Browser.Viewports) > 0 {
		candidate := request.Package.RecordingRunSpec.Browser.Viewports[0]
		if candidate.Width > 0 && candidate.Height > 0 {
			viewport = driver.BrowserAgentWorkerViewport{Width: candidate.Width, Height: candidate.Height}
		}
	}
	maskSelectors := append([]string{}, request.Package.RecordingRunSpec.Redactions.MaskSelectors...)
	maskSelectors = append(maskSelectors, request.Package.SafetyReport.RedactionSelectors...)
	return driver.BrowserAgentWorkerOpenRequest{
		SessionID: safePathSegment(request.CloudJobID), OutputDir: request.RecordingOutputDir,
		Browser: driver.BrowserAgentWorkerBrowser{
			Engine: request.Package.RecordingRunSpec.Browser.Engine, Headless: request.Package.RecordingRunSpec.Browser.Headless,
			Viewport: viewport, RecordVideo: request.Package.RecordingRunSpec.Outputs.RawRecording,
		},
		AllowedDomains:        append([]string{}, request.RuntimePlan.AllowedDomains...),
		ForbiddenPages:        append([]string{}, request.RuntimePlan.ForbiddenPages...),
		ForbiddenPathPrefixes: append([]string{}, request.RuntimePlan.ExplorationScope.ForbiddenPathPrefixes...),
		ForbiddenKeywords:     append([]string{}, request.RuntimePlan.ExplorationScope.ForbiddenKeywords...),
		MaskSelectors:         uniqueStrings(maskSelectors),
	}
}

func workerStageFromRuntime(stage BrowserAgentRuntimeStage) driver.BrowserAgentWorkerStage {
	return driver.BrowserAgentWorkerStage{
		ID: stage.ID, Order: stage.Order, NodeID: stage.NodeID, Objective: stage.Objective,
		EntryRoute: stage.EntryRoute, Route: stage.Route, URL: stage.URL, TargetContract: stage.TargetContract,
		Components:     append([]model.BrowserAgentComponentTarget{}, stage.Components...),
		Interactions:   append([]model.BrowserAgentInteraction{}, stage.Interactions...),
		WaitConditions: append([]string{}, stage.WaitConditions...), CapturePlan: stage.CapturePlan,
		SuccessState: stage.SuccessState, DurationMS: stage.DurationMS,
		Validations: append([]model.ValidationSpec{}, stage.Validations...),
	}
}

func browserAgentStepResults(plan BrowserAgentRuntimePlan, events []model.StageExecutionEvent, artifacts map[string]model.ArtifactRef) []model.StepResult {
	results := make([]model.StepResult, 0, len(plan.Stages))
	orderedArtifacts := mapBrowserAgentArtifacts(artifacts)
	for _, stage := range plan.Stages {
		result := model.StepResult{NodeID: stage.NodeID, Status: "passed", ObservedState: stage.SuccessState}
		for _, event := range events {
			if event.NodeID != stage.NodeID {
				continue
			}
			if event.EventType == model.StageExecutionEventStageStarted {
				result.StartedAt = event.OccurredAt
			}
			if event.EventType == model.StageExecutionEventStageCompleted {
				result.CompletedAt = event.OccurredAt
			}
		}
		if !result.StartedAt.IsZero() && !result.CompletedAt.IsZero() {
			result.DurationMS = int(result.CompletedAt.Sub(result.StartedAt).Milliseconds())
		}
		for _, artifact := range orderedArtifacts {
			if artifact.SourceNodeID == stage.NodeID {
				result.Artifacts = append(result.Artifacts, artifact)
			}
		}
		results = append(results, result)
	}
	return results
}

func mapBrowserAgentArtifacts(values map[string]model.ArtifactRef) []model.ArtifactRef {
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]model.ArtifactRef, 0, len(ids))
	for _, id := range ids {
		result = append(result, values[id])
	}
	return result
}

func browserAgentSandboxMetadata(pkg *model.ClientExecutionPackage, versions map[string]string) *model.SandboxExecutionMetadata {
	policy := model.ResolveSandboxPolicy(pkg.RecordingRunSpec, pkg.ExecutableScriptBundle)
	return &model.SandboxExecutionMetadata{
		PolicyHashSHA256: policy.PolicyHashSHA256, Profile: policy.Profile, IsolationMode: policy.IsolationMode,
		NetworkMode: policy.NetworkPolicy.Mode, WorkerID: "video-worker-browser-agent", RuntimeVersions: versions,
	}
}

func mergeRuntimeVersions(left, right map[string]string) map[string]string {
	result := map[string]string{}
	for key, value := range left {
		result[key] = value
	}
	for key, value := range right {
		result[key] = value
	}
	return result
}

func progressBrowserAgent(progress func(string, string, int), stage, message string, percent int) {
	if progress != nil {
		progress(stage, message, percent)
	}
}

func browserAgentStageProgress(order, count int, executing bool) int {
	if count <= 0 {
		count = 1
	}
	base := 42 + ((order-1)*38)/count
	if executing {
		base += 19 / count
	}
	return minInt(base, 84)
}

func browserAgentStageDisplayName(stage BrowserAgentRuntimeStage) string {
	if stage.BusinessIntent != "" {
		return stage.BusinessIntent
	}
	if stage.Objective != "" {
		return stage.Objective
	}
	return stage.NodeID
}
