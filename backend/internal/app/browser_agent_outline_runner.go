package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
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

type browserAgentWorkerRevalidationSession interface {
	Revalidate(context.Context, driver.BrowserAgentWorkerStage) (driver.BrowserAgentWorkerStageResult, error)
}

type browserAgentWorkerCredentialSession interface {
	ExecuteWithSecrets(context.Context, driver.BrowserAgentWorkerStage, map[string]string) (driver.BrowserAgentWorkerStageResult, error)
}

type browserAgentWorkerSessionFactory func(context.Context, driver.BrowserAgentWorkerOpenRequest) (browserAgentWorkerSession, driver.BrowserAgentWorkerOpenResult, error)

type localBrowserAgentOutlineRunner struct {
	service        *Service
	sessionFactory browserAgentWorkerSessionFactory
	renderService  executor.DeliveryRenderService
	// outcomeVerifier is a focused test override. Production runs snapshot the
	// Server-owned verifier from service at the beginning of each execution.
	outcomeVerifier BrowserAgentStageEventVerifier
}

type localBrowserAgentStageRuntime struct {
	session            browserAgentWorkerSession
	credentialResolver browserAgentCredentialResolver
	progress           func(stage string, message string, progress int)
	stageCount         int
	artifacts          map[string]model.ArtifactRef
	taskSecretRefs     map[string]bool
}

func (r localBrowserAgentOutlineRunner) Run(ctx context.Context, request BrowserAgentOutlineRunRequest) (model.RecordingResultPackage, error) {
	if r.service == nil {
		return model.RecordingResultPackage{}, newRuntimeExecutionError(runtimeErrorOutlineRunnerUnavailable, errors.New("browser-agent runner service is not configured"))
	}
	if request.Package == nil || len(request.RuntimePlan.Stages) == 0 || request.EventSink == nil {
		return model.RecordingResultPackage{}, newRuntimeExecutionError(runtimeErrorBrowserAgentContractViolation, errors.New("browser-agent runtime plan, package, and event sink are required"))
	}
	verifier := r.outcomeVerifier
	if verifier == nil {
		verifier = r.service.browserAgentOutcomeVerifierSnapshot()
	}
	if verifier == nil {
		verifier = newDeterministicBrowserAgentStageVerifier(request.Package)
	}
	// A full verifier is optional so existing focused verifier tests can inject
	// only the stage decision. The production verifier implements all phases.
	validationContext := validationContextFromPackage(request.Package)
	validationContext.RunID = request.RuntimePlan.RunID
	fullVerifier, _ := verifier.(OutcomeVerifier)
	preReports := []model.ValidationReport{}
	if fullVerifier != nil {
		preReport, verifyErr := fullVerifier.ValidateBeforeExecution(ctx, validationContext)
		if verifyErr != nil {
			return model.RecordingResultPackage{}, newRuntimeExecutionError("outcome_pre_verification_failed", verifyErr)
		}
		if err := preReport.Validate(); err != nil {
			return model.RecordingResultPackage{}, newRuntimeExecutionError("outcome_pre_verification_failed", err)
		}
		preReports = append(preReports, preReport)
		if preReport.Decision != model.ValidationDecisionContinue {
			return model.RecordingResultPackage{}, newRuntimeExecutionError("outcome_pre_verification_failed", errors.New("approved Browser Agent package did not pass pre-execution verification"))
		}
	}
	progressBrowserAgent(request.Progress, "validating_pre_execution", "正在核对 App 已审批阶段、业务目标和安全边界。", 36)
	progressBrowserAgent(request.Progress, "script_ready", "Browser Agent 可审计脚本已生成并通过安全预演。", 38)

	openRequest := browserAgentWorkerOpenRequest(request)
	openRequest.TaskSecrets = request.TaskSecrets
	// Only the Server-created local fixture contains no customer material. Real
	// App packages keep their raw recording sensitive by default.
	if request.Package.Metadata["producer"] == "server_controlled_business_acceptance" &&
		request.Package.Metadata["dev_plaintext_upload_mode"] == true &&
		request.Package.Metadata["runtime"] == model.ExecutableScriptRuntimeBrowserAgentOutlineV1 &&
		r.service.runtime.Profile != config.ProfileCloud {
		recordingSensitive := false
		openRequest.RecordingSensitive = &recordingSensitive
	}
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
		session: session, credentialResolver: request.CredentialResolver, progress: request.Progress, stageCount: len(request.RuntimePlan.Stages), artifacts: map[string]model.ArtifactRef{}, taskSecretRefs: taskSecretRefSet(request.TaskSecrets),
	}
	orchestrator := newBrowserAgentStageOrchestratorWithVerifier(contractBrowserAgentPolicyGuard{}, verifier)
	startedAt := timeNowUTC()
	runResult, runErr := orchestrator.Run(ctx, request.RuntimePlan, stageRuntime, stageRuntime, request.EventSink)
	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 15*time.Second)
	closeResult, closeErr := session.Close(cleanupCtx)
	cancelCleanup()
	closed = true
	if closeErr != nil {
		if runErr != nil {
			return model.RecordingResultPackage{}, newRuntimeExecutionError("browser_agent_session_close_failed", fmt.Errorf("browser-agent execution failed and the session could not close: %w", closeErr))
		}
		return model.RecordingResultPackage{}, newRuntimeExecutionError("browser_agent_session_close_failed", closeErr)
	}
	for _, artifact := range closeResult.Artifacts {
		stageRuntime.artifacts[artifact.ID] = artifact
	}
	if runResult.AuditError != nil {
		return model.RecordingResultPackage{}, newRuntimeExecutionError("stage_event_audit_unavailable", runResult.AuditError)
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
	if runErr != nil {
		recordResult.StepResults = browserAgentFailedStepResults(request.RuntimePlan, runResult.Events, stageRuntime.artifacts, runErr)
		recordResult.FailureDiagnostic = browserAgentFailureDiagnostic(request, runResult.Events, stageRuntime.artifacts, runErr, completedAt)
	}
	progressBrowserAgent(request.Progress, "packaging_recording", "正在整理录屏、截图、验证报告和阶段审计记录。", 94)
	result, err := executor.NewRecordingResultPackageFromRecordResult(request.Package, recordResult, request.CloudJobID, request.ResultCreatedAt)
	if err != nil {
		return model.RecordingResultPackage{}, newRuntimeExecutionError("browser_agent_result_packaging_failed", err)
	}
	result.ValidationReports = append([]model.ValidationReport{}, preReports...)
	result.ValidationReports = append(result.ValidationReports, runResult.ValidationReports...)
	result.PatchLedger = append([]model.RuntimePatchLedgerEntry{}, runResult.PatchLedger...)
	if fullVerifier != nil {
		postReport, verifyErr := fullVerifier.ValidatePostExecution(ctx, validationContext, result, runResult.Events)
		if verifyErr != nil {
			return model.RecordingResultPackage{}, newRuntimeExecutionError("outcome_post_verification_failed", verifyErr)
		}
		if err := postReport.Validate(); err != nil {
			return model.RecordingResultPackage{}, newRuntimeExecutionError("outcome_post_verification_failed", err)
		}
		result.ValidationReports = append(result.ValidationReports, postReport)
		if postReport.Decision != model.ValidationDecisionContinue && result.Status != model.RecordingResultStatusFailed {
			validationErr := newRuntimeExecutionError("outcome_post_verification_failed", fmt.Errorf("post-execution verifier stopped package with decision %s", postReport.Decision))
			recordResult.StepResults = browserAgentPostValidationFailedStepResults(recordResult.StepResults, postReport, validationErr)
			recordResult.FailureDiagnostic = browserAgentFailureDiagnostic(request, runResult.Events, stageRuntime.artifacts, validationErr, completedAt)
			result, err = executor.NewRecordingResultPackageFromRecordResult(request.Package, recordResult, request.CloudJobID, request.ResultCreatedAt)
			if err != nil {
				return model.RecordingResultPackage{}, newRuntimeExecutionError("browser_agent_result_packaging_failed", err)
			}
			result.ValidationReports = append([]model.ValidationReport{}, preReports...)
			result.ValidationReports = append(result.ValidationReports, runResult.ValidationReports...)
			result.ValidationReports = append(result.ValidationReports, postReport)
			result.PatchLedger = append([]model.RuntimePatchLedgerEntry{}, runResult.PatchLedger...)
		}
	}
	if result.Status == model.RecordingResultStatusGenerated && request.Package.RecordingRunSpec.Outputs.FinalVideo {
		progressBrowserAgent(request.Progress, "material_validation", "录屏、截图、trace 和阶段证据已通过素材校验。", 74)
		renderService := r.renderService
		if renderService == nil {
			renderService = r.service.editorWorker
		}
		if _, _, err := executor.RenderClientExecutionRecordingResult(ctx, renderService, request.Package, &result, request.RenderOutputDir, request.Progress); err != nil {
			return result, newRuntimeExecutionError("browser_agent_render_failed", err)
		}
	}
	return result, nil
}

func (r *localBrowserAgentStageRuntime) ObserveStage(ctx context.Context, _ BrowserAgentRuntimePlan, stage BrowserAgentRuntimeStage) (BrowserAgentStageObservation, error) {
	progress := browserAgentStageProgress(stage.Order, r.stageCount, false)
	progressBrowserAgent(r.progress, "running_browser_agent", fmt.Sprintf("正在观察第 %d 个阶段：%s", stage.Order, browserAgentStageDisplayName(stage)), progress)
	if stage.ManualSessionCheckpoint {
		result, err := r.revalidateManualSessionCheckpoint(ctx, stage)
		if err != nil {
			return BrowserAgentStageObservation{}, err
		}
		return BrowserAgentStageObservation{Observation: result.Observation, EvidenceRefs: result.EvidenceRefs, TargetResolved: true}, nil
	}
	result, err := r.session.Observe(ctx, workerStageFromRuntime(stage))
	if err != nil {
		return BrowserAgentStageObservation{}, err
	}
	r.collect(result.Artifacts)
	return BrowserAgentStageObservation{Observation: result.Observation, EvidenceRefs: result.EvidenceRefs, TargetResolved: result.TargetResolved, PreferredSelectorAlternative: result.PreferredSelectorAlternative, SuggestedWaitCondition: result.SuggestedWaitCondition}, nil
}

func (r *localBrowserAgentStageRuntime) ExecuteStage(ctx context.Context, _ BrowserAgentRuntimePlan, stage BrowserAgentRuntimeStage) (BrowserAgentStageActionResult, error) {
	progress := browserAgentStageProgress(stage.Order, r.stageCount, true)
	progressBrowserAgent(r.progress, "running_browser_agent", fmt.Sprintf("正在执行第 %d 个阶段：%s", stage.Order, browserAgentStageDisplayName(stage)), progress)
	if stage.ManualSessionCheckpoint {
		result, err := r.revalidateManualSessionCheckpoint(ctx, stage)
		if err != nil {
			return BrowserAgentStageActionResult{}, err
		}
		progressBrowserAgent(r.progress, "validating_runtime_stage", fmt.Sprintf("Verifying manual login checkpoint for stage %d.", stage.Order), minInt(progress+4, 84))
		return BrowserAgentStageActionResult{Observation: &result.Observation, EvidenceRefs: result.EvidenceRefs}, nil
	}
	workerStage := workerStageFromRuntime(stage)
	if browserAgentStageUsesTaskSecret(workerStage, r.taskSecretRefs) {
		result, err := r.session.Execute(ctx, workerStage)
		if err != nil {
			return BrowserAgentStageActionResult{}, err
		}
		r.collect(result.Artifacts)
		progressBrowserAgent(r.progress, "validating_runtime_stage", fmt.Sprintf("正在验证第 %d 个阶段的真实页面结果。", stage.Order), minInt(progress+4, 84))
		return BrowserAgentStageActionResult{Observation: &result.Observation, EvidenceRefs: result.EvidenceRefs}, nil
	}
	secretValues, err := browserAgentStageSecretValues(workerStage, r.credentialResolver)
	if err != nil {
		return BrowserAgentStageActionResult{}, err
	}
	defer clearBrowserAgentStageSecrets(secretValues)
	var result driver.BrowserAgentWorkerStageResult
	if len(secretValues) > 0 {
		credentialSession, ok := r.session.(browserAgentWorkerCredentialSession)
		if !ok {
			return BrowserAgentStageActionResult{}, errors.New("browser-agent worker does not support credential broker execution")
		}
		result, err = credentialSession.ExecuteWithSecrets(ctx, workerStage, secretValues)
	} else {
		result, err = r.session.Execute(ctx, workerStage)
	}
	if err != nil {
		return BrowserAgentStageActionResult{}, err
	}
	r.collect(result.Artifacts)
	progressBrowserAgent(r.progress, "validating_runtime_stage", fmt.Sprintf("正在验证第 %d 个阶段的真实页面结果。", stage.Order), minInt(progress+4, 84))
	return BrowserAgentStageActionResult{Observation: &result.Observation, EvidenceRefs: result.EvidenceRefs}, nil
}

func taskSecretRefSet(values map[string]driver.BrowserAgentTaskSecret) map[string]bool {
	refs := make(map[string]bool, len(values))
	for ref := range values {
		if ref = strings.TrimSpace(ref); ref != "" {
			refs[ref] = true
		}
	}
	return refs
}

func browserAgentStageUsesTaskSecret(stage driver.BrowserAgentWorkerStage, refs map[string]bool) bool {
	for _, interaction := range stage.Interactions {
		if refs[strings.TrimSpace(interaction.SecretRef)] {
			return true
		}
	}
	return false
}

func (r *localBrowserAgentStageRuntime) revalidateManualSessionCheckpoint(ctx context.Context, stage BrowserAgentRuntimeStage) (driver.BrowserAgentWorkerStageResult, error) {
	session, ok := r.session.(browserAgentWorkerRevalidationSession)
	if !ok {
		return driver.BrowserAgentWorkerStageResult{}, errors.New("manual session checkpoint requires non-action browser revalidation")
	}
	result, err := session.Revalidate(ctx, workerStageFromRuntime(stage))
	if err != nil {
		return driver.BrowserAgentWorkerStageResult{}, err
	}
	result.TargetResolved = true
	result.Observation.Assertions = append(result.Observation.Assertions, model.RuntimeAssertion{
		Kind: "manual_session_checkpoint_verified", Passed: true, Actual: result.Observation.URL,
	})
	r.collect(result.Artifacts)
	return result, nil
}

func (r *localBrowserAgentStageRuntime) RevalidateStage(ctx context.Context, _ BrowserAgentRuntimePlan, stage BrowserAgentRuntimeStage) (BrowserAgentStageActionResult, error) {
	session, ok := r.session.(browserAgentWorkerRevalidationSession)
	if !ok {
		return BrowserAgentStageActionResult{}, errors.New("browser-agent worker does not support non-action outcome revalidation")
	}
	progressBrowserAgent(r.progress, "validating_runtime_stage", fmt.Sprintf("正在重新截取并验证第 %d 个阶段的页面结果。", stage.Order), browserAgentStageProgress(stage.Order, r.stageCount, true))
	result, err := session.Revalidate(ctx, workerStageFromRuntime(stage))
	if err != nil {
		return BrowserAgentStageActionResult{}, err
	}
	r.collect(result.Artifacts)
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

// ValidateBeforeExecution is deliberately structural: before the browser has
// opened, the only permissible evidence is the approved, hash-bound package.
// It checks that the compiled plan did not lose the required validation rules.
func (v deterministicBrowserAgentStageVerifier) ValidateBeforeExecution(_ context.Context, validationContext model.BrowserAgentValidationContext) (model.ValidationReport, error) {
	checks := []model.ValidationCheck{}
	evidence := []model.EvidenceRef{{ID: "approved_package_contract", Kind: model.EvidenceKindDocs, Summary: "hash-bound approved package"}}
	structurePassed := validationContext.StageApprovalPlan != nil && validationContext.ScriptOutline != nil && validationContext.BrowserAgentContract != nil
	checks = append(checks, runtimeValidationCheck("check_approved_outline_contract", "approved_outline_contract", "approved_contract_missing", structurePassed, "Server verified the approved stage plan, outline, and Browser Agent contract are present.", evidence))
	hashPassed := strings.TrimSpace(validationContext.SourceBundleHashSHA256) != "" && strings.TrimSpace(validationContext.EffectivePolicyHashSHA256) != ""
	checks = append(checks, runtimeValidationCheck("check_approved_hashes", "approved_hash_binding", "approved_hash_missing", hashPassed, "Server verified the approved package and Browser Agent policy hashes are present.", evidence))
	stagePlanPassed := validationContext.StageApprovalPlan != nil && len(validationContext.StageApprovalPlan.Stages) > 0 && validationContext.ScriptOutline != nil && len(validationContext.ScriptOutline.Stages) > 0
	checks = append(checks, runtimeValidationCheck("check_approved_stage_count", "approved_stage_count", "approved_stage_plan_empty", stagePlanPassed, "Server verified the approved stage plan and script outline contain executable stages.", evidence))

	// Readiness checks: business input completeness, selector provenance, action structure.
	// Converted from browserAgentReadiness findings to ValidationCheck format.
	readinessChecks := convertReadinessToValidationChecks(validationContext)
	checks = append(checks, readinessChecks...)

	decision := model.ValidationDecisionContinue
	if !structurePassed || !hashPassed || !stagePlanPassed {
		decision = model.ValidationDecisionStopAndReport
	}
	// If any readiness check is blocking and failed, escalate decision.
	for _, chk := range readinessChecks {
		if !chk.Passed && chk.Severity == model.FindingSeverityBlocking {
			decision = model.ValidationDecisionStopAndReport
			break
		}
	}
	passRate := validationPassRate(checks)
	reportBundleHash := firstNonEmptyString(validationContext.SourceBundleHashSHA256, "missing_source_bundle_hash")
	reportPolicyHash := firstNonEmptyString(validationContext.EffectivePolicyHashSHA256, "missing_policy_hash")
	return model.ValidationReport{
		SchemaVersion: model.ValidationReportSchemaVersion, ReportID: "validation_pre_" + safePathSegment(validationContext.SourcePackageID),
		RunID: firstNonEmptyString(validationContext.RunID, validationContext.SourcePackageID, "preflight"), SourcePackageID: validationContext.SourcePackageID,
		SourceBundleHashSHA256: reportBundleHash, PolicyHashSHA256: reportPolicyHash,
		Phase: model.ValidationPhasePreExecution, Decision: decision, PassRate: passRate, OverallConfidence: passRate,
		EvidenceQuality: model.RuntimeObservationDerivedPlan, Checks: checks,
		EvidenceRefs: evidence, CreatedAt: timeNowUTC(),
	}, nil
}

// ValidatePostExecution makes the aggregate decision only from observed stage
// events and packaged StepResults; it never treats an expected success state as
// proof that the browser reached it.
func (v deterministicBrowserAgentStageVerifier) ValidatePostExecution(_ context.Context, validationContext model.BrowserAgentValidationContext, result model.RecordingResultPackage, events []model.StageExecutionEvent) (model.ValidationReport, error) {
	checks := []model.ValidationCheck{}
	evidenceRefs := []model.EvidenceRef{}
	completed := map[string]bool{}
	outcomes := map[string]*model.RuntimeObservation{}
	for index := range events {
		event := events[index]
		if event.EventType == model.StageExecutionEventStageCompleted {
			completed[event.NodeID] = true
		}
		if event.EventType == model.StageExecutionEventOutcomeObserved && event.Observation != nil {
			outcome := *event.Observation
			outcomes[event.NodeID] = &outcome
			evidenceRefs = append(evidenceRefs, event.EvidenceRefs...)
		}
	}
	runtimeReports := map[string]model.ValidationReport{}
	for _, report := range result.ValidationReports {
		if report.Phase == model.ValidationPhaseRuntimeStage && report.NodeID != "" {
			runtimeReports[report.NodeID] = report
		}
	}
	decision := model.ValidationDecisionContinue
	failedNodeID := ""
	for _, step := range result.StepResults {
		hasObservation := outcomes[step.NodeID] != nil && runtimeObservationIsRealEvidence(outcomes[step.NodeID].Source)
		nodeEvidence := evidenceRefsForNode(events, step.NodeID)
		completionPassed := step.Status == "passed" && completed[step.NodeID] && hasObservation
		checks = append(checks, runtimeValidationCheck("check_post_"+safePathSegment(step.NodeID), "observed_stage_completion", "observed_stage_completion_missing", completionPassed, "Stage completion is accepted only when a real browser outcome observation was recorded.", nodeEvidence))
		runtimeReport, hasRuntimeReport := runtimeReports[step.NodeID]
		reportPassed := hasRuntimeReport && runtimeReport.Decision == model.ValidationDecisionContinue && runtimeReport.EvidenceQuality != model.RuntimeObservationDerivedPlan
		checks = append(checks, runtimeValidationCheck("check_runtime_report_"+safePathSegment(step.NodeID), "runtime_stage_report", "runtime_stage_report_missing", reportPassed, "Post-execution validation requires a successful runtime-stage report for each delivered step.", runtimeReport.EvidenceRefs))
		statePassed := strings.Contains(step.ObservedState, "source=") && step.ObservedState != ""
		checks = append(checks, runtimeValidationCheck("check_observed_state_"+safePathSegment(step.NodeID), "runtime_observed_state", "observed_state_not_runtime_derived", statePassed, "Step observed_state must be derived from runtime observations, not planned success text.", nodeEvidence))
		evidencePassed := len(nodeEvidence) > 0 || len(step.Artifacts) > 0
		checks = append(checks, runtimeValidationCheck("check_evidence_refs_"+safePathSegment(step.NodeID), "traceable_evidence_refs", "evidence_refs_missing", evidencePassed, "Delivered step evidence must be traceable to runtime events or artifacts.", nodeEvidence))
		if !completionPassed || !reportPassed || !statePassed || !evidencePassed {
			decision = model.ValidationDecisionStopAndReport
			if failedNodeID == "" {
				failedNodeID = step.NodeID
			}
		}
	}
	if len(result.StepResults) == 0 || len(evidenceRefs) == 0 {
		decision = model.ValidationDecisionStopAndReport
	}
	passRate := validationPassRate(checks)
	evidenceQuality := model.RuntimeObservationInsufficient
	if len(evidenceRefs) > 0 {
		evidenceQuality = model.RuntimeObservationAssertion
	}
	return model.ValidationReport{
		SchemaVersion: model.ValidationReportSchemaVersion, ReportID: "validation_post_" + safePathSegment(validationContext.SourcePackageID),
		RunID: firstNonEmptyString(validationContext.RunID, validationContext.SourcePackageID, "postflight"), SourcePackageID: validationContext.SourcePackageID,
		SourceBundleHashSHA256: validationContext.SourceBundleHashSHA256, PolicyHashSHA256: validationContext.EffectivePolicyHashSHA256,
		Phase: model.ValidationPhasePostExecution, NodeID: failedNodeID, Decision: decision, PassRate: passRate, OverallConfidence: passRate,
		EvidenceQuality: evidenceQuality, Checks: checks, EvidenceRefs: uniqueEvidenceRefs(evidenceRefs), CreatedAt: timeNowUTC(),
	}, nil
}

func boolPassRate(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func runtimeValidationCheck(id, kind, code string, passed bool, summary string, evidenceRefs []model.EvidenceRef) model.ValidationCheck {
	severity := model.FindingSeverityInfo
	if !passed {
		severity = model.FindingSeverityBlocking
	}
	return model.ValidationCheck{
		ID: id, Kind: kind, Code: code, Severity: severity, Passed: passed, Required: true,
		Summary: summary, EvidenceRefs: append([]model.EvidenceRef{}, evidenceRefs...),
	}
}

// convertReadinessToValidationChecks translates browserAgentReadiness findings
// into ValidationCheck format for pre-execution validation. All readiness issues
// are assigned ResponsibilityDomain = app (package structure problems).
func convertReadinessToValidationChecks(validationContext model.BrowserAgentValidationContext) []model.ValidationCheck {
	// Construct a minimal ClientExecutionPackage from validation context for readiness check.
	pkg := &model.ClientExecutionPackage{
		PackageID: validationContext.SourcePackageID,
		ExecutableScriptBundle: &model.ExecutableRecordingScriptBundle{
			ScriptManifest: model.ExecutableScriptManifest{
				Runtime: model.ExecutableScriptRuntimeBrowserAgentOutlineV1,
			},
			StageApprovalPlan: validationContext.StageApprovalPlan,
			ScriptOutline:     validationContext.ScriptOutline,
			PlanJSON:          validationContext.Plan,
		},
	}

	readinessReport := browserAgentReadiness(pkg)
	checks := []model.ValidationCheck{}

	// Convert blockers.
	for _, finding := range readinessReport.Blockers {
		checks = append(checks, model.ValidationCheck{
			ID:                   "readiness_blocker_" + finding.Code,
			Kind:                 "readiness",
			Code:                 finding.Code,
			NodeID:               finding.NodeID,
			Severity:             model.FindingSeverityBlocking,
			Passed:               false,
			Required:             true,
			Summary:              finding.Message,
			ResponsibilityDomain: model.ValidationCheckDomainApp,
		})
	}

	// Convert warnings.
	for _, finding := range readinessReport.Warnings {
		checks = append(checks, model.ValidationCheck{
			ID:                   "readiness_warning_" + finding.Code,
			Kind:                 "readiness",
			Code:                 finding.Code,
			NodeID:               finding.NodeID,
			Severity:             model.FindingSeverityWarning,
			Passed:               false,
			Required:             false,
			Summary:              finding.Message,
			ResponsibilityDomain: model.ValidationCheckDomainApp,
		})
	}

	return checks
}

func validationPassRate(checks []model.ValidationCheck) float64 {
	if len(checks) == 0 {
		return 0
	}
	passed := 0
	for _, check := range checks {
		if check.Passed {
			passed++
		}
	}
	return float64(passed) / float64(len(checks))
}

func evidenceRefsForNode(events []model.StageExecutionEvent, nodeID string) []model.EvidenceRef {
	refs := []model.EvidenceRef{}
	for _, event := range events {
		if event.NodeID == nodeID && event.EventType == model.StageExecutionEventOutcomeObserved {
			refs = append(refs, event.EvidenceRefs...)
		}
	}
	return uniqueEvidenceRefs(refs)
}

func uniqueEvidenceRefs(refs []model.EvidenceRef) []model.EvidenceRef {
	seen := map[string]bool{}
	result := make([]model.EvidenceRef, 0, len(refs))
	for _, ref := range refs {
		if ref.ID == "" || seen[ref.ID] {
			continue
		}
		seen[ref.ID] = true
		result = append(result, ref)
	}
	return result
}

func (v deterministicBrowserAgentStageVerifier) ValidateStageEvents(_ context.Context, validationContext model.BrowserAgentValidationContext, events []model.StageExecutionEvent) (model.ValidationReport, error) {
	if len(events) == 0 {
		return model.ValidationReport{}, errors.New("stage outcome verifier received no events")
	}
	last := events[len(events)-1]
	decision := model.ValidationDecisionContinue
	checks := make([]model.ValidationCheck, 0)
	passed, total := 0, 0
	observedChecks := map[string]bool{}
	identityPassed := true
	for _, event := range events {
		if event.RunID != "" && validationContext.RunID != "" && event.RunID != validationContext.RunID {
			identityPassed = false
		}
		if event.SourcePackageID != validationContext.SourcePackageID || event.SourceBundleHashSHA256 != validationContext.SourceBundleHashSHA256 || event.PolicyHashSHA256 != validationContext.EffectivePolicyHashSHA256 {
			identityPassed = false
		}
	}
	if identityPassed {
		checks = append(checks, runtimeValidationCheck("check_"+safePathSegment(last.StageID)+"_identity", "runtime_event_identity", "runtime_event_identity_mismatch", identityPassed, "Runtime event identity must match the approved package and policy hashes.", last.EvidenceRefs))
	} else {
		total++
		decision = model.ValidationDecisionStopAndReport
		checks = append(checks, runtimeValidationCheck("check_"+safePathSegment(last.StageID)+"_identity", "runtime_event_identity", "runtime_event_identity_mismatch", identityPassed, "Runtime event identity must match the approved package and policy hashes.", last.EvidenceRefs))
	}
	for _, event := range events {
		if event.Action != nil && browserAgentForbiddenAction(event.Action.Kind, validationContext.ForbiddenActions) {
			decision = model.ValidationDecisionStopAndReport
			total++
			checks = append(checks, model.ValidationCheck{
				ID: "check_" + safePathSegment(event.StageID) + "_forbidden_operation", Kind: "forbidden_operation",
				Code: "forbidden_operation_attempted", NodeID: event.NodeID, StageID: event.StageID,
				Severity: model.FindingSeverityBlocking, Passed: false, Required: true,
				Summary:      "Runtime attempted an operation that the approved Browser Agent contract forbids.",
				EvidenceRefs: append([]model.EvidenceRef{}, event.EvidenceRefs...), ResponsibilityDomain: model.ValidationCheckDomainApp,
			})
		}
		if event.EventType != model.StageExecutionEventOutcomeObserved || event.Observation == nil {
			continue
		}
		urlAssertionPassed := false
		businessAssertionPassed := false
		for index, assertion := range event.Observation.Assertions {
			observedChecks[assertion.Kind] = assertion.Passed
			total++
			if assertion.Passed {
				passed++
				if strings.Contains(strings.ToLower(assertion.Kind), "url") {
					urlAssertionPassed = true
				} else {
					businessAssertionPassed = true
				}
			} else {
				decision = model.ValidationDecisionStopAndReport
			}
			checks = append(checks, model.ValidationCheck{
				ID: fmt.Sprintf("check_%s_%02d", safePathSegment(last.StageID), index+1), Kind: assertion.Kind,
				Passed: assertion.Passed, Required: true, Summary: "依据真实浏览器结果执行确定性检查。",
				EvidenceRefs: append([]model.EvidenceRef{}, event.EvidenceRefs...),
			})
		}
		if urlAssertionPassed && !businessAssertionPassed {
			decision = model.ValidationDecisionStopAndReport
			total++
			checks = append(checks, model.ValidationCheck{
				ID: "check_" + safePathSegment(last.StageID) + "_business_outcome", Kind: "business_outcome_semantics",
				Code: "url_change_not_business_completion", NodeID: last.NodeID, StageID: last.StageID,
				Severity: model.FindingSeverityBlocking, Passed: false, Required: true,
				Summary:      "A URL change alone cannot prove that the approved business outcome completed.",
				EvidenceRefs: append([]model.EvidenceRef{}, event.EvidenceRefs...), ResponsibilityDomain: model.ValidationCheckDomainApp,
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

func browserAgentForbiddenAction(kind string, forbidden []string) bool {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return false
	}
	for _, value := range forbidden {
		if strings.ToLower(strings.TrimSpace(value)) == kind {
			return true
		}
	}
	return false
}

func browserAgentWorkerOpenRequest(request BrowserAgentOutlineRunRequest) driver.BrowserAgentWorkerOpenRequest {
	// Browser Agent recordings are evidence masters, not delivery renders.
	// Keep one canonical 16:9 2K web-content viewport so later crop, highlight,
	// and model-reference coordinates share a stable frame. App output sizing
	// remains an independent delivery intent applied by the editor.
	viewport := driver.BrowserAgentWorkerViewport{Width: 2560, Height: 1440}
	maskSelectors := append([]string{}, request.Package.RecordingRunSpec.Redactions.MaskSelectors...)
	maskSelectors = append(maskSelectors, request.Package.SafetyReport.RedactionSelectors...)
	recordingSensitive := true
	return driver.BrowserAgentWorkerOpenRequest{
		SessionID: safePathSegment(request.CloudJobID), OutputDir: request.RecordingOutputDir,
		InitialURL: firstNonEmptyString(request.Package.ProjectContextSummary.ProductURL, request.Package.RecordingRunSpec.BaseURL),
		Browser: driver.BrowserAgentWorkerBrowser{
			Engine: request.Package.RecordingRunSpec.Browser.Engine, Headless: request.Package.RecordingRunSpec.Browser.Headless,
			Viewport: viewport, RecordVideo: request.Package.RecordingRunSpec.Outputs.RawRecording,
		},
		AllowedDomains:        append([]string{}, request.RuntimePlan.AllowedDomains...),
		AllowedOrigins:        append([]string{}, request.RuntimePlan.ExplorationScope.AllowedOrigins...),
		AllowedRoutes:         append([]string{}, request.RuntimePlan.ExplorationScope.AllowedRoutes...),
		ForbiddenPages:        append([]string{}, request.RuntimePlan.ForbiddenPages...),
		ForbiddenPathPrefixes: append([]string{}, request.RuntimePlan.ExplorationScope.ForbiddenPathPrefixes...),
		ForbiddenKeywords:     append([]string{}, request.RuntimePlan.ExplorationScope.ForbiddenKeywords...),
		MaskSelectors:         uniqueStrings(maskSelectors),
		RecordingSensitive:    &recordingSensitive,
	}
}

func workerStageFromRuntime(stage BrowserAgentRuntimeStage) driver.BrowserAgentWorkerStage {
	return driver.BrowserAgentWorkerStage{
		ID: stage.ID, Order: stage.Order, NodeID: stage.NodeID, StageKind: stage.StageKind, Objective: stage.Objective,
		EntryRoute: stage.EntryRoute, Route: stage.Route, URL: stage.URL, TargetContract: stage.TargetContract,
		TargetRouteTemplate: stage.TargetRouteTemplate, ExpectedRouteAfterAction: stage.ExpectedRouteAfterAction,
		RuntimeRouteVerificationRequired: stage.RuntimeRouteVerificationRequired,
		Components:                       append([]model.BrowserAgentComponentTarget{}, stage.Components...),
		Interactions:                     append([]model.BrowserAgentInteraction{}, stage.Interactions...),
		WaitConditions:                   append([]string{}, stage.WaitConditions...), CapturePlan: stage.CapturePlan,
		SuccessState: stage.SuccessState, DurationMS: stage.DurationMS,
		Validations:                       append([]model.ValidationSpec{}, stage.Validations...),
		PreferredSelectorAlternative:      stage.PreferredSelectorAlternative,
		EvidenceBoundSelectorAlternatives: append([]model.SelectorCandidate{}, stage.EvidenceBoundSelectorAlternatives...),
	}
}

func browserAgentStepResults(plan BrowserAgentRuntimePlan, events []model.StageExecutionEvent, artifacts map[string]model.ArtifactRef) []model.StepResult {
	results := make([]model.StepResult, 0, len(plan.Stages))
	orderedArtifacts := mapBrowserAgentArtifacts(artifacts)
	for _, stage := range plan.Stages {
		result := model.StepResult{NodeID: stage.NodeID, Status: "passed"}
		var outcome *model.RuntimeObservation
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
			if event.EventType == model.StageExecutionEventOutcomeObserved && event.Observation != nil {
				copy := *event.Observation
				outcome = &copy
			}
		}
		result.ObservedState = browserAgentObservedState(outcome)
		if outcome == nil || !runtimeObservationIsRealEvidence(outcome.Source) {
			result.Status = "failed"
			result.Error = &model.AgentError{Code: "missing_runtime_observation", Message: "Approved stage completed without a real browser outcome observation."}
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

// browserAgentObservedState intentionally records only a redacted, factual
// summary of runtime evidence. It must never fall back to stage.SuccessState,
// which is an App expectation rather than execution evidence.
func browserAgentObservedState(observation *model.RuntimeObservation) string {
	if observation == nil {
		return ""
	}
	parts := []string{fmt.Sprintf("source=%s", observation.Source)}
	if observation.URL != "" {
		parts = append(parts, "url_observed")
	}
	if observation.Title != "" {
		parts = append(parts, "title_observed")
	}
	for _, assertion := range observation.Assertions {
		if assertion.Kind == "" {
			continue
		}
		state := "failed"
		if assertion.Passed {
			state = "passed"
		}
		parts = append(parts, fmt.Sprintf("assertion:%s=%s", assertion.Kind, state))
	}
	return strings.Join(parts, "; ")
}

func browserAgentPostValidationFailedStepResults(results []model.StepResult, report model.ValidationReport, verifyErr error) []model.StepResult {
	updated := append([]model.StepResult{}, results...)
	for index := range updated {
		if report.NodeID != "" && updated[index].NodeID != report.NodeID {
			continue
		}
		updated[index].Status = "failed"
		updated[index].Error = &model.AgentError{Code: runtimeExecutionErrorCode(verifyErr), Message: "Post-execution validation did not confirm the approved outcome."}
		if updated[index].ObservedState == "" {
			updated[index].ObservedState = "Post-execution validation did not receive sufficient real browser evidence."
		}
		return updated
	}
	return updated
}

// Keep completed stages intact and mark only the stopped stage as failed.
// This prevents a runtime error from becoming plan-derived success evidence.
func browserAgentFailedStepResults(plan BrowserAgentRuntimePlan, events []model.StageExecutionEvent, artifacts map[string]model.ArtifactRef, runErr error) []model.StepResult {
	results := browserAgentStepResults(plan, events, artifacts)
	completed := map[string]bool{}
	for _, event := range events {
		if event.EventType == model.StageExecutionEventStageCompleted {
			completed[event.NodeID] = true
		}
	}
	for index := range results {
		if !completed[results[index].NodeID] {
			results[index].Status = "not_started"
			results[index].ObservedState = "Not executed because Browser Agent stopped earlier."
			results[index].Error = nil
		}
	}
	failedNodeID := ""
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].EventType == model.StageExecutionEventStageFailed && events[index].NodeID != "" {
			failedNodeID = events[index].NodeID
			break
		}
	}
	if failedNodeID == "" && len(plan.Stages) > 0 {
		failedNodeID = plan.Stages[0].NodeID
	}
	for index := range results {
		if results[index].NodeID == failedNodeID {
			results[index].Status = "failed"
			results[index].ObservedState = "Browser Agent stopped before this approved stage completed."
			results[index].Error = &model.AgentError{Code: runtimeExecutionErrorCode(runErr), Message: "Browser Agent execution stopped; see the redacted failure diagnostic."}
			break
		}
	}
	return results
}

func browserAgentFailureDiagnostic(request BrowserAgentOutlineRunRequest, events []model.StageExecutionEvent, artifacts map[string]model.ArtifactRef, runErr error, capturedAt time.Time) *model.ScriptFailureDiagnostic {
	failedNodeID, failedStageID, currentURL, pageTitle := "unknown", "", "", ""
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.NodeID != "" && (event.EventType == model.StageExecutionEventStageFailed || failedNodeID == "unknown") {
			failedNodeID, failedStageID = event.NodeID, event.StageID
		}
		if event.Observation != nil {
			if currentURL == "" {
				currentURL = event.Observation.URL
			}
			if pageTitle == "" {
				pageTitle = event.Observation.Title
			}
		}
	}
	failedOrder := 0
	for _, stage := range request.RuntimePlan.Stages {
		if stage.NodeID == failedNodeID {
			failedOrder = stage.Order
			break
		}
	}
	code := runtimeExecutionErrorCode(runErr)
	if code == "execution_failed" {
		code = "browser_agent_execution_failed"
	}
	maskedSelectors := append([]string{}, request.Package.RecordingRunSpec.Redactions.MaskSelectors...)
	maskedSelectors = append(maskedSelectors, request.Package.SafetyReport.RedactionSelectors...)
	diagnostic := &model.ScriptFailureDiagnostic{
		ID: "diag_" + safePathSegment(firstNonEmptyString(failedStageID, failedNodeID)), SchemaVersion: model.ScriptFailureDiagnosticSchemaVersion,
		SourcePackageID: request.Package.PackageID, CloudJobID: request.CloudJobID, FailedNodeID: failedNodeID,
		FailedStepOrder: failedOrder, Attempt: 1,
		Error:      model.AgentError{Code: code, Message: "Browser Agent stopped before the approved stage could be completed.", Retryable: false},
		CurrentURL: currentURL, PageTitle: pageTitle,
		RedactionReport: model.DiagnosticRedactionReport{Applied: true, PolicyRef: request.Package.PackageID + ".redactions", MaskedSelectors: uniqueStrings(maskedSelectors), FullHTMLIncluded: false},
		CapturedAt:      capturedAt,
	}
	var fallbackScreenshots []model.PackageArtifactDescriptor
	for _, artifact := range mapBrowserAgentArtifacts(artifacts) {
		if artifact.Kind == "browser_trace" {
			diagnostic.TraceRefs = append(diagnostic.TraceRefs, browserAgentDiagnosticArtifact(artifact, "failure_trace"))
			continue
		}
		if artifact.Kind == "screenshot" || artifact.Kind == "failure_screenshot" {
			if artifact.SourceNodeID == failedNodeID {
				diagnostic.ScreenshotRefs = append(diagnostic.ScreenshotRefs, browserAgentDiagnosticArtifact(artifact, "failure_screenshot"))
			} else {
				fallbackScreenshots = append(fallbackScreenshots, browserAgentDiagnosticArtifact(artifact, "failure_screenshot"))
			}
		}
	}
	if len(diagnostic.ScreenshotRefs) == 0 {
		diagnostic.ScreenshotRefs = append(diagnostic.ScreenshotRefs, fallbackScreenshots...)
	}
	return diagnostic
}

func browserAgentDiagnosticArtifact(artifact model.ArtifactRef, role string) model.PackageArtifactDescriptor {
	metadata := map[string]any{"dev_local_artifact": true}
	for key, value := range artifact.Metadata {
		metadata[key] = value
	}
	return model.PackageArtifactDescriptor{ID: artifact.ID, Role: role, Kind: artifact.Kind, URI: artifact.URI, MimeType: artifact.MimeType, SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes, Encrypted: true, Sensitive: true, RecipientKeyID: "local-dev/result-key", Metadata: metadata}
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
