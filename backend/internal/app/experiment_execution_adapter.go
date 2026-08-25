package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cascade-demoops/backend/internal/experiment"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

type appExperimentExecutionAdapter struct {
	service      *Service
	pollInterval time.Duration
	now          func() time.Time
}

func newAppExperimentExecutionAdapter(service *Service) *appExperimentExecutionAdapter {
	return &appExperimentExecutionAdapter{service: service, pollInterval: 5 * time.Second, now: time.Now}
}

func (a *appExperimentExecutionAdapter) DescribeCapabilities(context.Context) (experiment.ModuleManifest, error) {
	if a == nil || a.service == nil {
		return experiment.ModuleManifest{}, errors.New("experiment execution adapter is unavailable")
	}
	return experiment.ModuleManifest{
		ModuleID: "demoops-compat-execution-capture", Version: "1.0.0",
		Capabilities:             []string{"async_build", "direct_transport", "temporal_visual_gate", "segmented_recording", "guided_final_film"},
		SupportedReplay:          []experiment.ReplayPolicy{experiment.ReplayObserveOnly, experiment.ReplayIdempotentWrite, experiment.ReplayOnceEffect},
		SupportsSegmentedCapture: true,
	}, nil
}

func (a *appExperimentExecutionAdapter) ExecuteLeg(ctx context.Context, request experiment.LegExecutionRequest, emit func(experiment.LegExecutionUpdate) error) error {
	if emit == nil || strings.TrimSpace(request.BuildPrompt) == "" {
		return errors.New("experiment adapter requires a build prompt and update sink")
	}
	if request.Checkpoint != nil && experimentCheckpointConfirmed(request.Checkpoint, "target_submit") {
		projectID, jobID, ok := parseDirectResultEntry(request.Checkpoint.ResultEntryRef)
		if !ok {
			return &experiment.AdapterError{Code: "completed_effect_requires_bound_direct_job", Phase: "resume_observe_only", State: experiment.RunStateWaitingInput, Retryable: false, EffectID: "target_submit", Cause: errors.New("a confirmed experiment checkpoint must retain its direct result binding")}
		}
		if err := emit(experiment.LegExecutionUpdate{Kind: "phase", Phase: "resume_observe_only", Summary: "从已确认的 Direct 结果入口恢复，不重放提交", EvidenceRefs: []string{jobID}}); err != nil {
			return err
		}
		status, err := a.service.GetDirectExecutionStatus(ctx, projectID, jobID)
		if err != nil || status.Status != "completed" {
			return &experiment.AdapterError{Code: "confirmed_result_revalidation_failed", Phase: "resume_observe_only", State: experiment.RunStateWaitingInput, Retryable: false, EvidenceRefs: []string{jobID}, Cause: err}
		}
		return a.completeDirectLeg(ctx, request, projectID, jobID, status, false, emit)
	}
	if request.Checkpoint != nil {
		if externalRef := experimentStartedEffectExternalRef(request.Checkpoint, "target_submit"); externalRef != "" {
			projectID, jobID, ok := parseDirectResultEntry(externalRef)
			if !ok {
				return &experiment.AdapterError{Code: "external_task_binding_invalid", Phase: "resume_observe_only", State: experiment.RunStateWaitingInput, Retryable: false}
			}
			if err := emit(experiment.LegExecutionUpdate{Kind: "phase", Phase: "resume_external_task", Summary: "续接已持久化的 Direct 任务，不重新规划或提交", EvidenceRefs: []string{jobID}}); err != nil {
				return err
			}
			status, statusErr := a.service.GetDirectExecutionStatus(ctx, projectID, jobID)
			if statusErr == nil && status.Status == "failed" && request.HarnessProfile == experiment.HarnessProfileAdaptiveBusinessV1 {
				return a.reconcileFailedDirectLeg(ctx, request, projectID, jobID, emit)
			}
			status, err := a.waitForDirectResult(ctx, projectID, jobID, request.ObservationPlan, emit)
			if err != nil {
				return err
			}
			return a.completeDirectLeg(ctx, request, projectID, jobID, status, true, emit)
		}
	}
	parsed, err := url.Parse(request.TargetURL)
	if err != nil || parsed.Hostname() == "" {
		return &experiment.AdapterError{Code: "target_scope_invalid", Phase: "plan_review", State: experiment.RunStateFailed, Cause: err}
	}
	if err := emit(experiment.LegExecutionUpdate{Kind: "phase", Phase: "source_intelligence", Summary: "冻结的产品规格已交给兼容理解 Adapter"}); err != nil {
		return err
	}
	interactionContracts, err := compileExperimentInteractionContracts(request.InteractionPlan, request.ObservationPlan)
	if err != nil {
		return &experiment.AdapterError{Code: "interaction_contract_compile_failed", Phase: "plan_review", State: experiment.RunStateFailed, Retryable: false, Cause: err}
	}
	var prepared ProductRunPrepareResult
	if request.HarnessProfile == experiment.HarnessProfileAdaptiveBusinessV1 {
		prepared, err = a.service.prepareAdaptiveExperimentRun(ctx, request, interactionContracts)
	} else {
		input := orchestrator.UserInput{
			Mode: model.AppModeWeb, ProductURL: request.TargetURL, ProductDescription: request.BuildPrompt,
			TargetDurationSec: 105, TargetAudience: request.ProductSpec.Audience, BrandTone: request.ProductSpec.VisualDirection.Theme,
			MustShow: interactionSemanticGoals(request.InteractionPlan), MustNotShow: append([]string{}, request.ProductSpec.ForbiddenOutcomes...),
			InteractionContracts: interactionContracts,
			AllowedDomains:       []string{parsed.Hostname()}, DemoCredentialRef: request.CredentialRef,
			WorkflowExecution: &model.WorkflowExecutionHints{
				TaskPackID: request.WorkflowTemplateID, RequiresFreshEntity: true, EntityName: request.ProjectName,
				PrimaryInputSemantic: "product_spec", DirectExecution: true, RequiresSubmission: true, ObserveAsyncResult: true,
			},
		}
		prepared, err = a.service.PrepareProductRun(ctx, CloudLifecycleRequest{UserInput: &input})
	}
	if err != nil || prepared.State == nil || prepared.Build == nil || prepared.Build.Package.ConfidenceSummary == nil {
		return &experiment.AdapterError{Code: "execution_package_planning_failed", Phase: "plan_review", State: experiment.RunStateWaitingExternal, Retryable: true, Cause: err}
	}
	if err := emit(experiment.LegExecutionUpdate{Kind: "phase", Phase: "plan_review", Summary: "执行包与 Interaction Contract 已完成本地门禁"}); err != nil {
		return err
	}
	effectKey := "submit-" + safePathSegment(request.RunID) + "-" + safePathSegment(request.LegID)
	runtimeMetadata := map[string]any{
		"experiment_run_id": request.RunID, "experiment_leg_id": request.LegID,
		"harness_profile":  request.HarnessProfile,
		"observation_plan": request.ObservationPlan, "interaction_plan": request.InteractionPlan,
		"visual_call_budget": request.VisualCallBudget, "expected_product_summary": experimentProductEvidenceSummary(request.ProductSpec),
	}
	recoveryPhase := ""
	if request.Kind == "recovery" {
		recoveryPhase = "once_effect_committed"
	}
	upload, err := a.service.UploadDirectExecutionPackage(ctx, prepared.State.ProjectID, DirectTransportUploadRequest{
		OrgID: defaultDesktopOrgID, PackageDigestSHA256: prepared.Build.PackageDigestSHA256,
		ApprovalSubjectDigestSHA256: prepared.Build.ApprovalSubjectDigestSHA256,
		ConfidenceAssessmentHash:    prepared.Build.Package.ConfidenceSummary.AssessmentHash,
		RiskConfirmed:               true, IdempotencyKey: effectKey, RecoveryInjectionPhase: recoveryPhase, RuntimeMetadata: runtimeMetadata,
		BeforePackageSubmit: func() error {
			return emit(experiment.LegExecutionUpdate{Kind: "once_effect_started", EffectID: "target_submit", EffectKind: "target_submission", IdempotencyKey: effectKey})
		},
		AfterPackageAdmitted: func(receipt model.DirectPackageReceipt) error {
			return emit(experiment.LegExecutionUpdate{Kind: "once_effect_admitted", EffectID: "target_submit", ExternalTaskRef: "direct:" + prepared.State.ProjectID + ":" + receipt.JobID, EvidenceRefs: []string{receipt.JobID}})
		},
	})
	if err != nil {
		var staged *directUploadStageError
		if errors.As(err, &staged) {
			if staged.ExternalTaskRef != "" {
				return &experiment.AdapterError{Code: "direct_upload_post_admission_failed", Phase: staged.Stage, State: experiment.RunStateWaitingExternal, Retryable: true, EvidenceRefs: []string{staged.ExternalTaskRef}, Cause: err}
			}
			if !staged.MayHaveBeenAdmitted {
				_ = emit(experiment.LegExecutionUpdate{Kind: "once_effect_rejected", EffectID: "target_submit", Summary: "Direct Gateway 在创建外部任务前明确拒绝执行包"})
				return &experiment.AdapterError{Code: "direct_upload_preflight_failed", Phase: staged.Stage, State: experiment.RunStateWaitingExternal, Retryable: true, Cause: err}
			}
		}
		return &experiment.AdapterError{Code: "once_effect_result_unknown", Phase: "target_submission", State: experiment.RunStateWaitingInput, Retryable: false, EffectID: "target_submit", Cause: err}
	}
	jobID := upload.Receipt.JobID
	if err := emit(experiment.LegExecutionUpdate{Kind: "phase", Phase: "request_submitted", Summary: "目标提交已被 Direct Gateway 接收", EvidenceRefs: []string{jobID}}); err != nil {
		return err
	}
	status, err := a.waitForDirectResult(ctx, prepared.State.ProjectID, jobID, request.ObservationPlan, emit)
	if err != nil {
		return err
	}
	return a.completeDirectLeg(ctx, request, prepared.State.ProjectID, jobID, status, true, emit)
}

func (a *appExperimentExecutionAdapter) reconcileFailedDirectLeg(ctx context.Context, request experiment.LegExecutionRequest, projectID, sourceJobID string, emit func(experiment.LegExecutionUpdate) error) error {
	if err := emit(experiment.LegExecutionUpdate{Kind: "phase", Phase: "reconcile_observed_state", Summary: "失败诊断显示业务已进入后继实体；正在生成只观察续接包", EvidenceRefs: []string{sourceJobID}}); err != nil {
		return err
	}
	prepared, err := a.service.prepareAdaptiveDirectReconciliation(ctx, projectID, sourceJobID, request.InteractionPlan, request.ObservationPlan)
	if err != nil || prepared.State == nil || prepared.Build.Package.ConfidenceSummary == nil {
		return &experiment.AdapterError{Code: "observed_successor_reconciliation_unavailable", Phase: "reconcile_observed_state", State: experiment.RunStateWaitingInput, Retryable: false, EvidenceRefs: []string{sourceJobID}, Cause: err}
	}
	runtimeMetadata := map[string]any{
		"experiment_run_id": request.RunID, "experiment_leg_id": request.LegID,
		"harness_profile": request.HarnessProfile, "observation_plan": request.ObservationPlan, "interaction_plan": request.InteractionPlan,
		"visual_call_budget": request.VisualCallBudget, "expected_product_summary": experimentProductEvidenceSummary(request.ProductSpec),
		"reconciles_direct_job": sourceJobID,
	}
	reconcileKey := "reconcile-" + safePathSegment(request.RunID) + "-" + safePathSegment(request.LegID) + fmt.Sprintf("-%d", request.BrowserAttempt)
	upload, err := a.service.UploadDirectExecutionPackage(ctx, projectID, DirectTransportUploadRequest{
		OrgID: defaultDesktopOrgID, PackageDigestSHA256: prepared.Build.PackageDigestSHA256,
		ApprovalSubjectDigestSHA256: prepared.Build.ApprovalSubjectDigestSHA256,
		ConfidenceAssessmentHash:    prepared.Build.Package.ConfidenceSummary.AssessmentHash,
		RiskConfirmed:               true, IdempotencyKey: reconcileKey, RuntimeMetadata: runtimeMetadata,
	})
	if err != nil {
		return &experiment.AdapterError{Code: "adaptive_reconciliation_upload_failed", Phase: "reconcile_observed_state", State: experiment.RunStateWaitingExternal, Retryable: true, EvidenceRefs: []string{sourceJobID}, Cause: err}
	}
	jobID := upload.Receipt.JobID
	if err := emit(experiment.LegExecutionUpdate{Kind: "phase", Phase: "resume_observe_only", Summary: "只观察续接任务已启动；不会执行创建或提交", EvidenceRefs: []string{sourceJobID, jobID}}); err != nil {
		return err
	}
	status, err := a.waitForDirectResult(ctx, projectID, jobID, request.ObservationPlan, emit)
	if err != nil {
		return err
	}
	return a.completeDirectLeg(ctx, request, projectID, jobID, status, true, emit)
}

func experimentProductEvidenceSummary(spec experiment.ProductSpec) string {
	parts := []string{
		"Objective: " + strings.TrimSpace(spec.Objective),
		"Visual theme: " + strings.TrimSpace(spec.VisualDirection.Theme),
		"Motion: " + strings.TrimSpace(spec.VisualDirection.Motion),
	}
	for _, requirement := range spec.Requirements {
		parts = append(parts, "Required: "+strings.TrimSpace(requirement.Statement))
	}
	for _, requirement := range spec.InteractionRequirements {
		parts = append(parts, "Interaction: "+strings.TrimSpace(requirement.Statement))
	}
	for _, criterion := range spec.ObservableAcceptance {
		if criterion.Required {
			parts = append(parts, "Acceptance: "+strings.TrimSpace(criterion.Statement))
		}
	}
	for _, forbidden := range spec.ForbiddenOutcomes {
		parts = append(parts, "Forbidden: "+strings.TrimSpace(forbidden))
	}
	return truncateForUpload(strings.Join(parts, "\n"), 4096)
}

func compileExperimentInteractionContracts(plan experiment.InteractionPlan, observationPlan experiment.ObservationPlan) ([]model.InteractionContract, error) {
	if err := experiment.ValidateInteractionPlan(plan); err != nil {
		return nil, err
	}
	result := make([]model.InteractionContract, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		ref := model.EvidenceRef{ID: "ev_interaction_plan_" + step.StepID, Kind: model.EvidenceKindUserInput, Summary: "结构化交互证据计划", FieldPath: "interaction_plan.steps." + step.StepID, Confidence: 1}
		actionKind := model.GraphActionInspect
		switch step.Action.Kind {
		case "keyboard_sequence":
			actionKind = model.GraphActionPress
		case "activate_control", "activate_state_variants":
			actionKind = model.GraphActionClick
		case "touch_swipe":
			actionKind = model.GraphActionGesture
		}
		target := model.ActionTarget{EvidenceRefs: []model.EvidenceRef{ref}}
		if len(step.Action.AllowedRoles) > 0 {
			target.Role = step.Action.AllowedRoles[0]
		}
		if len(step.Action.AllowedNames) > 0 {
			target.Label = step.Action.AllowedNames[0]
			target.Text = step.Action.AllowedNames[0]
		}
		parameters := map[string]any{
			"evidence_step_id": step.StepID, "evidence_slots": append([]string{}, step.EvidenceSlots...),
			"max_attempts": step.MaxAttempts, "action_recipe": step.Action.Kind,
		}
		if step.CapabilityLayer != "" {
			parameters["capability_layer"] = step.CapabilityLayer
			parameters["capability_score"] = step.CapabilityScore
			parameters["proof_session_id"] = step.ProofSessionID
		}
		if len(step.Action.Keys) > 0 {
			parameters["keys"] = append([]string{}, step.Action.Keys...)
			parameters["focus_preview"] = true
		}
		if len(step.Action.AllowedRoles) > 0 {
			parameters["allowed_roles"] = append([]string{}, step.Action.AllowedRoles...)
		}
		if len(step.Action.AllowedNames) > 0 {
			parameters["allowed_names"] = append([]string{}, step.Action.AllowedNames...)
		}
		if step.Action.SwipeDirection != "" {
			parameters["swipe_direction"] = step.Action.SwipeDirection
		}
		if step.Action.Viewport != "" {
			parameters["viewport"] = step.Action.Viewport
		}
		predicates := []model.InteractionPredicate{}
		seenKinds := map[string]bool{}
		appendPredicate := func(kind string, expected any) {
			if kind == "" || seenKinds[kind] {
				return
			}
			seenKinds[kind] = true
			timeoutMS := 12000
			if kind == "interactive_surface_visible" && step.ReplayPolicy == experiment.ReplayObserveOnly && observationPlan.DeferAfterMS > timeoutMS {
				timeoutMS = observationPlan.DeferAfterMS
			}
			predicates = append(predicates, model.InteractionPredicate{ID: "proof_" + step.StepID + "_" + kind, Kind: kind, Target: target, Expected: expected, Required: true, TimeoutMS: timeoutMS, EvidenceRefs: []model.EvidenceRef{ref}})
		}
		hasSpecializedProof := false
		for _, proof := range step.ProofRequirements {
			if proof.Kind != "all_evidence_slots" && proof.Kind != "region_changed" {
				hasSpecializedProof = true
			}
		}
		if step.Action.Kind == "observe" && step.ReplayPolicy == experiment.ReplayObserveOnly && !hasSpecializedProof {
			// Async interactive results require two independent channels: a
			// deterministic surface plus repeated visual confirmation that the
			// requested product is complete and generation is no longer active.
			parameters["require_visual_terminal_confirmation"] = true
			parameters["refresh_after_ms"] = observationPlan.WarnAfterMS
		}
		if step.Action.Kind == "observe" && !hasSpecializedProof {
			appendPredicate("interactive_surface_visible", true)
		} else if step.Action.Kind == "observe" {
			// Keep a transport-v1-compatible state transition alongside richer
			// proof predicates. Older admission validators can recognize this
			// without weakening the specialized worker-side evidence gate.
			appendPredicate("state_changed", true)
		} else if step.Action.Kind != "observe" {
			for _, change := range step.ExpectedChanges {
				switch change {
				case "aria":
					appendPredicate("aria_changed", true)
				case "visual":
					appendPredicate("visual_region_changed", true)
				case "frame", "interaction":
					appendPredicate("frame_surface_changed", true)
				case "network":
					appendPredicate("network_settled", true)
				case "dom":
					appendPredicate("dom_changed", true)
				case "url":
					appendPredicate("page_changed", true)
				}
			}
		}
		for _, proof := range step.ProofRequirements {
			switch proof.Kind {
			case "distinct_actions":
				appendPredicate("distinct_actions_observed", proof.MinCount)
			case "numeric_increase":
				appendPredicate("numeric_increased", true)
			case "approximate_state_restore":
				appendPredicate("approximate_state_restored", proof.MinSimilarity)
			case "input_modality":
				appendPredicate("input_modality_used", proof.Modality)
			case "state_variants":
				appendPredicate("state_variants_observed", proof.MinCount)
			}
		}
		contract := model.InteractionContract{
			SchemaVersion: model.InteractionContractSchemaVersion, ContractID: "experiment_interaction_" + step.StepID,
			SemanticGoal: step.SemanticIntent, Archetype: model.ProductArchetypeInteractive, ActionKind: actionKind,
			ReplayPolicy: model.InteractionReplayPolicy(step.ReplayPolicy), TargetSemanticID: step.Action.TargetSemanticID,
			ActionTarget: target, Parameters: parameters, ExpectedTransitions: predicates, EvidenceRefs: []model.EvidenceRef{ref}, NonDestructive: true,
		}
		if err := model.ValidateInteractionContract(contract); err != nil {
			return nil, fmt.Errorf("%s: %w", step.StepID, err)
		}
		result = append(result, contract)
	}
	return result, nil
}

func (a *appExperimentExecutionAdapter) completeDirectLeg(ctx context.Context, request experiment.LegExecutionRequest, projectID, jobID string, status model.DirectJobStatus, commitEffect bool, emit func(experiment.LegExecutionUpdate) error) error {
	result, err := a.service.GetDirectResult(ctx, projectID, jobID)
	if err != nil {
		return &experiment.AdapterError{Code: "direct_result_unavailable", Phase: "result_materialization", State: experiment.RunStateWaitingExternal, Retryable: true, Cause: err}
	}
	if result.Status == model.RecordingResultStatusFailed {
		return &experiment.AdapterError{Code: "explicit_terminal_build_failure", Phase: "terminal_failed", State: experiment.RunStateFailed, Retryable: false, EvidenceRefs: resultEvidenceRefs(result)}
	}
	downloads, err := a.downloadDirectArtifacts(ctx, projectID, jobID, status.Artifacts)
	if err != nil {
		return &experiment.AdapterError{Code: "artifact_materialization_failed", Phase: "result_materialization", State: experiment.RunStateWaitingExternal, Retryable: true, Cause: err}
	}
	evidenceRefs := resultEvidenceRefs(result)
	segmentRefs := experimentSegmentRefs(downloads)
	if commitEffect {
		if err := emit(experiment.LegExecutionUpdate{
			Kind: "once_effect_committed", EffectID: "target_submit", StateFingerprintRef: "result:" + result.ResultID,
			ResultEntryRef: "direct:" + projectID + ":" + jobID, EvidenceRefs: evidenceRefs, SegmentRefs: segmentRefs,
		}); err != nil {
			return err
		}
	}
	if request.HarnessProfile == experiment.HarnessProfileAdaptiveBusinessV1 {
		score, scoreEvidence, scoreErr := readAdaptiveCapabilityScore(downloads)
		if scoreErr != nil {
			return &experiment.AdapterError{Code: "capability_score_invalid", Phase: "interaction_verification", State: experiment.RunStateFailed, Retryable: false, EvidenceRefs: evidenceRefs, Cause: scoreErr}
		}
		if score == nil {
			return &experiment.AdapterError{Code: "capability_score_missing", Phase: "interaction_verification", State: experiment.RunStateWaitingInput, Retryable: true, EvidenceRefs: evidenceRefs}
		}
		summary := &experiment.CapabilitySummary{CoreScore: score.CoreScore, EnhancementScore: score.EnhancementScore, TotalScore: score.TotalScore, CorePassed: score.CorePassed, EligibleForFilm: score.EligibleForFilm, Missing: append([]string{}, score.Missing...)}
		if err := emit(experiment.LegExecutionUpdate{Kind: "capability_scored", Summary: "核心能力与增强能力已完成分层评分", EvidenceRefs: []string{scoreEvidence}, CapabilityScore: summary}); err != nil {
			return err
		}
		if !score.CorePassed || !score.EligibleForFilm {
			return &experiment.AdapterError{Code: "core_capability_gate_failed", Phase: "interaction_verification", State: experiment.RunStateFailed, Retryable: false, EvidenceRefs: []string{scoreEvidence}}
		}
	}
	visualArtifacts := []experiment.ArtifactRef{}
	liveVisualCalls, liveTerminal, liveErr := recordLiveBrowserVisualObservations(downloads, emit)
	if liveErr != nil {
		return liveErr
	}
	if liveVisualCalls > request.VisualCallBudget {
		return &experiment.AdapterError{Code: "visual_call_budget_exceeded", Phase: "visual_observation_deferred", State: experiment.RunStateFailed, Retryable: false, EvidenceRefs: evidenceRefs}
	}
	if !experimentArtifactsContainRole(request.ExistingArtifacts, "temporal_visual_observation") && !liveTerminal {
		remaining := request.VisualCallBudget - liveVisualCalls
		if remaining < 1 {
			return &experiment.AdapterError{Code: "visual_terminal_evidence_incomplete", Phase: "preview_candidate", State: experiment.RunStateWaitingInput, Retryable: true, EvidenceRefs: evidenceRefs}
		}
		visualRequest := request
		visualRequest.VisualCallBudget = remaining
		visualArtifacts, err = a.runTemporalVisualGate(ctx, visualRequest, downloads, evidenceRefs, emit)
		if err != nil {
			return err
		}
	}
	materialized, err := a.service.GetDirectEditorMaterialization(ctx, projectID)
	if err != nil || !materialized.Ready {
		if err == nil {
			err = errors.New(materialized.Message)
		}
		return &experiment.AdapterError{Code: "fact_track_materialization_deferred", Phase: "fact_track_materialization", State: experiment.RunStateWaitingExternal, Retryable: true, Cause: err}
	}
	artifacts := append(experimentArtifactRefs(downloads), visualArtifacts...)
	if err := emit(experiment.LegExecutionUpdate{Kind: "artifacts", Artifacts: artifacts, Summary: "事实轨、阶段证据和时序观察已物化"}); err != nil {
		return err
	}
	if request.Kind == "main" {
		return a.runFinalFilm(ctx, request, projectID, result, materialized.SessionID, downloads, emit)
	}
	return nil
}

func readAdaptiveCapabilityScore(downloads []CloudDeliverableDownloadResult) (*model.CapabilityScore, string, error) {
	var latest *model.CapabilityScore
	evidence := ""
	for _, item := range downloads {
		if item.Kind != "browser_agent_stage_event_log" {
			continue
		}
		file, err := os.Open(item.LocalPath)
		if err != nil {
			return nil, "", err
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64*1024), experiment.MaxEventBodyBytes)
		for scanner.Scan() {
			var event model.StageExecutionEvent
			if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
				_ = file.Close()
				return nil, "", err
			}
			if event.EventType == model.StageExecutionEventCapabilityScored && event.CapabilityScore != nil {
				copyScore := *event.CapabilityScore
				latest = &copyScore
				evidence = item.ArtifactID
			}
		}
		scanErr := scanner.Err()
		_ = file.Close()
		if scanErr != nil {
			return nil, "", scanErr
		}
	}
	if latest == nil {
		return nil, "", nil
	}
	if latest.SchemaVersion != "demoops.capability_score.v1" || latest.CoreScore < 0 || latest.EnhancementScore < 0 || latest.TotalScore != latest.CoreScore+latest.EnhancementScore || latest.TotalScore > 100 || latest.EligibleForFilm != latest.CorePassed {
		return nil, "", errors.New("stage event log contains an invalid capability score")
	}
	return latest, evidence, nil
}

func recordLiveBrowserVisualObservations(downloads []CloudDeliverableDownloadResult, emit func(experiment.LegExecutionUpdate) error) (int, bool, error) {
	calls, terminal := 0, false
	for _, item := range downloads {
		if item.Kind != "browser_visual_observation" {
			continue
		}
		data, err := os.ReadFile(item.LocalPath)
		if err != nil {
			return calls, terminal, &experiment.AdapterError{Code: "live_visual_observation_unreadable", Phase: "visual_observation_deferred", State: experiment.RunStateWaitingExternal, Retryable: true, Cause: err}
		}
		if len(data) == 0 || len(data) > experiment.MaxEventBodyBytes {
			return calls, terminal, &experiment.AdapterError{Code: "live_visual_observation_size_invalid", Phase: "visual_observation_deferred", State: experiment.RunStateFailed, Retryable: false}
		}
		var observation struct {
			SchemaVersion string  `json:"schema_version"`
			Decision      string  `json:"decision"`
			Confidence    float64 `json:"confidence"`
			ProviderCalls int     `json:"provider_calls_used"`
		}
		if err := json.Unmarshal(data, &observation); err != nil || observation.SchemaVersion != browserVisualObservationSchemaVersion {
			return calls, terminal, &experiment.AdapterError{Code: "live_visual_observation_invalid", Phase: "visual_observation_deferred", State: experiment.RunStateFailed, Retryable: false, Cause: err}
		}
		calls += max(1, observation.ProviderCalls)
		if observation.Decision == "succeeded" && observation.Confidence >= .85 {
			terminal = true
		}
		if emit != nil {
			if err := emit(experiment.LegExecutionUpdate{Kind: "visual_observation", Summary: "Worker 时序视觉观察已计入运行预算", EvidenceRefs: []string{item.ArtifactID}}); err != nil {
				return calls, terminal, err
			}
		}
	}
	return calls, terminal, nil
}

func (a *appExperimentExecutionAdapter) waitForDirectResult(ctx context.Context, projectID, jobID string, plan experiment.ObservationPlan, emit func(experiment.LegExecutionUpdate) error) (model.DirectJobStatus, error) {
	started := a.now()
	warned := false
	lastProgress := -1
	for {
		status, err := a.service.GetDirectExecutionStatus(ctx, projectID, jobID)
		if err != nil {
			return status, &experiment.AdapterError{Code: "direct_status_unavailable", Phase: "waiting_external", State: experiment.RunStateWaitingExternal, Retryable: true, Cause: err}
		}
		if status.ProgressPercent != lastProgress {
			lastProgress = status.ProgressPercent
			_ = emit(experiment.LegExecutionUpdate{Kind: "phase", Phase: normalizedDirectPhase(status), Summary: "异步构建进度已变化", EvidenceRefs: []string{jobID}})
		}
		switch status.Status {
		case "completed":
			return status, nil
		case "failed", "canceled", "expired":
			return status, &experiment.AdapterError{Code: firstNonEmptyString(status.BlockingErrorCode, "explicit_terminal_build_failure"), Phase: "terminal_failed", State: experiment.RunStateFailed, Retryable: false, EvidenceRefs: []string{jobID}}
		case "awaiting_credentials":
			if _, err := a.service.ReuploadDirectCredential(ctx, projectID, jobID); err != nil {
				return status, &experiment.AdapterError{Code: "credential_restore_failed", Phase: "waiting_input", State: experiment.RunStateWaitingInput, Retryable: true, Cause: err}
			}
		case "awaiting_manual_login":
			return status, &experiment.AdapterError{Code: "midrun_manual_login_forbidden", Phase: "waiting_input", State: experiment.RunStateWaitingInput, Retryable: false, EvidenceRefs: []string{jobID}}
		}
		elapsed := a.now().Sub(started)
		if !warned && elapsed >= time.Duration(plan.WarnAfterMS)*time.Millisecond {
			warned = true
			_ = emit(experiment.LegExecutionUpdate{Kind: "phase", Phase: "build_progress_warning", Summary: "五分钟内未观察到足够进展，继续在预算内等待", EvidenceRefs: []string{jobID}})
		}
		// Let the Worker publish its deterministic timeout result, but do not turn
		// a ten-minute business deadline into another long polling window.
		if elapsed >= time.Duration(plan.DeferAfterMS)*time.Millisecond+30*time.Second {
			return status, &experiment.AdapterError{Code: "observation_deadline_reached", Phase: "observation_deferred", State: experiment.RunStateWaitingInput, Retryable: true, EvidenceRefs: []string{jobID}}
		}
		select {
		case <-ctx.Done():
			return status, ctx.Err()
		case <-time.After(a.pollInterval):
		}
	}
}

func (a *appExperimentExecutionAdapter) downloadDirectArtifacts(ctx context.Context, projectID, jobID string, artifacts []model.DirectArtifact) ([]CloudDeliverableDownloadResult, error) {
	downloads := make([]CloudDeliverableDownloadResult, 0, len(artifacts))
	for _, artifact := range artifacts {
		download, err := a.service.DownloadDirectArtifact(ctx, projectID, DirectArtifactDownloadRequest{JobID: jobID, Artifact: artifact})
		if err != nil {
			return nil, err
		}
		if !download.ChecksumVerified {
			return nil, fmt.Errorf("artifact %s did not cross the transport boundary with verified integrity", artifact.ArtifactID)
		}
		downloads = append(downloads, download)
	}
	return downloads, nil
}

func (a *appExperimentExecutionAdapter) runTemporalVisualGate(ctx context.Context, request experiment.LegExecutionRequest, downloads []CloudDeliverableDownloadResult, structuralRefs []string, emit func(experiment.LegExecutionUpdate) error) ([]experiment.ArtifactRef, error) {
	images := make([]CloudDeliverableDownloadResult, 0)
	for _, item := range downloads {
		if item.MimeType == "image/png" || item.MimeType == "image/jpeg" {
			images = append(images, item)
		}
	}
	sort.SliceStable(images, func(i, j int) bool { return images[i].LocalPath < images[j].LocalPath })
	if len(images) == 0 {
		return nil, &experiment.AdapterError{Code: "visual_evidence_missing", Phase: "visual_observation_deferred", State: experiment.RunStateWaitingInput, Retryable: false, EvidenceRefs: structuralRefs}
	}
	observer, err := experiment.NewTemporalVisualObserver(a.service.llm, a.service.runtime.ArtifactRoot)
	if err != nil {
		return nil, &experiment.AdapterError{Code: "visual_model_unavailable", Phase: "visual_observation_deferred", State: experiment.RunStateWaitingExternal, Retryable: true, Cause: err}
	}
	limit := request.VisualCallBudget
	if limit > len(images) {
		limit = len(images)
	}
	phase, previousPath, previousRef := request.ObservationPlan.InitialPhase, "", ""
	artifacts := make([]experiment.ArtifactRef, 0, limit)
	terminal := false
	for index := 0; index < limit; index++ {
		image := images[index]
		allowed := allowedObservationTransitions(request.ObservationPlan, phase)
		observation, observeErr := observer.Observe(ctx, experiment.VisualObservationRequest{
			RunID: request.RunID, LegID: request.LegID, Sequence: index + 1, CurrentPhase: phase,
			AllowedNextPhases: allowed, PreviousArtifactPath: previousPath, PreviousArtifactRef: previousRef,
			CurrentArtifactPath: image.LocalPath, CurrentArtifactRef: image.ArtifactID,
			ObservedEvidenceKinds: []string{"visual", "dom", "url"}, MaterialChanged: true, Plan: request.ObservationPlan,
		})
		if observeErr != nil {
			return artifacts, &experiment.AdapterError{Code: "visual_model_unavailable", Phase: "visual_observation_deferred", State: experiment.RunStateWaitingExternal, Retryable: true, Cause: observeErr}
		}
		artifacts = append(artifacts, observation.ArtifactRef)
		if err := emit(experiment.LegExecutionUpdate{Kind: "visual_observation", Summary: "关键帧已通过站点无关的时序视觉门禁", EvidenceRefs: []string{image.ArtifactID, observation.ArtifactRef.ArtifactID}, Artifacts: []experiment.ArtifactRef{observation.ArtifactRef}}); err != nil {
			return artifacts, err
		}
		phase, previousPath, previousRef = observation.Transition, image.LocalPath, image.ArtifactID
		if observation.Decision == "succeed" && observation.Transition == request.ObservationPlan.TerminalPhase {
			terminal = true
			break
		}
	}
	if !terminal {
		return artifacts, &experiment.AdapterError{Code: "visual_terminal_evidence_incomplete", Phase: "preview_candidate", State: experiment.RunStateWaitingInput, Retryable: true, EvidenceRefs: structuralRefs}
	}
	return artifacts, nil
}

func (a *appExperimentExecutionAdapter) runFinalFilm(ctx context.Context, request experiment.LegExecutionRequest, projectID string, result model.RecordingResultPackage, sessionID string, downloads []CloudDeliverableDownloadResult, emit func(experiment.LegExecutionUpdate) error) error {
	if err := emit(experiment.LegExecutionUpdate{Kind: "phase", Phase: "director_media", Summary: "主跑进入自动导演与成片模块"}); err != nil {
		return err
	}
	session, err := a.service.GetEditorSession(ctx, sessionID)
	if err != nil {
		return err
	}
	supplements, err := a.materializeReviewSupplements(request)
	if err != nil {
		return err
	}
	for _, download := range downloads {
		if download.Kind == "browser_agent_stage_event_log" || download.Kind == "recording_segment_manifest" {
			supplements = append(supplements, model.FinalFilmReviewSupplement{Role: "experiment_" + safePathSegment(download.Kind), SourcePath: download.LocalPath, RelativePath: "experiment/" + filepath.Base(download.LocalPath), Required: false})
		}
	}
	var job model.FinalFilmJob
	if request.FinalFilm == nil {
		job, err = a.service.CreateFinalFilmJob(ctx, FinalFilmCreateRequest{
			EditorSessionID: sessionID, ExpectedRevision: session.Revision, SourcePackageID: result.SourcePackageID,
			AutomationProfile: model.FinalFilmAutomationProfileGuidedDemoV1, ReviewSupplements: supplements,
		})
		if err != nil {
			return &experiment.AdapterError{Code: "final_film_create_failed", Phase: "director_media", State: experiment.RunStateWaitingExternal, Retryable: true, Cause: err}
		}
		if err := emit(experiment.LegExecutionUpdate{Kind: "final_film_bound", FinalFilmJobID: job.JobID, FinalFilmRevision: job.Revision, ProviderCallsUsed: 0}); err != nil {
			return err
		}
		job, err = a.service.RunFinalFilmAutomation(ctx, job.JobID, FinalFilmRunRequest{ExpectedRevision: job.Revision, AuthorizationRef: request.AuthorizationRef, MaxProviderCalls: 8})
		if err != nil {
			return &experiment.AdapterError{Code: "final_film_authorization_failed", Phase: "director_media", State: experiment.RunStateWaitingInput, Retryable: false, Cause: err}
		}
	} else {
		job, err = a.service.GetFinalFilmJob(ctx, request.FinalFilm.JobID)
		if err != nil {
			return &experiment.AdapterError{Code: "final_film_resume_failed", Phase: "director_media", State: experiment.RunStateWaitingExternal, Retryable: true, Cause: err}
		}
	}
	if job.State == model.FinalFilmJobBaselineReady && job.RunAuthorization == nil {
		job, err = a.service.RunFinalFilmAutomation(ctx, job.JobID, FinalFilmRunRequest{ExpectedRevision: job.Revision, AuthorizationRef: request.AuthorizationRef, MaxProviderCalls: 8})
		if err != nil {
			return &experiment.AdapterError{Code: "final_film_authorization_failed", Phase: "director_media", State: experiment.RunStateWaitingInput, Retryable: false, Cause: err}
		}
	}
	for {
		job, err = a.service.GetFinalFilmJob(ctx, job.JobID)
		if err != nil {
			return err
		}
		used := 0
		if job.RunAuthorization != nil {
			used = job.RunAuthorization.ProviderCallsUsed
		}
		if used > 8 {
			return &experiment.AdapterError{Code: "provider_budget_exceeded", Phase: "director_media", State: experiment.RunStateFailed}
		}
		switch job.State {
		case model.FinalFilmJobAwaitingFinalReview:
			if job.ReviewPackage == nil {
				return errors.New("final film reached review without an immutable review package")
			}
			return emit(experiment.LegExecutionUpdate{Kind: "final_film_bound", FinalFilmJobID: job.JobID, FinalFilmRevision: job.Revision, ProviderCallsUsed: used, FinalFilmPackageID: job.ReviewPackage.PackageID, EvidenceRefs: []string{job.ReviewPackage.PackageID}})
		case model.FinalFilmJobFailed, model.FinalFilmJobCancelled:
			return &experiment.AdapterError{Code: "final_film_failed", Phase: string(job.State), State: experiment.RunStateFailed, Retryable: job.LastError != nil && job.LastError.Retryable}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (a *appExperimentExecutionAdapter) materializeReviewSupplements(request experiment.LegExecutionRequest) ([]model.FinalFilmReviewSupplement, error) {
	root := filepath.Join(a.service.runtime.ArtifactRoot, "experiments", safePathSegment(request.RunID), safePathSegment(request.LegID), "inputs")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	values := []struct {
		name, role string
		value      any
	}{
		{"product-spec.json", "experiment_product_spec", request.ProductSpec},
		{"build-observation-plan.json", "experiment_observation_plan", request.ObservationPlan},
		{"interaction-evidence-plan.json", "experiment_interaction_plan", request.InteractionPlan},
		{"experiment-run-report.json", "experiment_automation_attribution", request.RunReport},
	}
	result := make([]model.FinalFilmReviewSupplement, 0, len(values))
	for _, value := range values {
		payload, err := json.MarshalIndent(value.value, "", "  ")
		if err != nil {
			return nil, err
		}
		path := filepath.Join(root, value.name)
		if err := os.WriteFile(path, append(payload, '\n'), 0o600); err != nil {
			return nil, err
		}
		result = append(result, model.FinalFilmReviewSupplement{Role: value.role, SourcePath: path, RelativePath: "experiment/" + value.name, Required: true})
	}
	return result, nil
}

func interactionSemanticGoals(plan experiment.InteractionPlan) []string {
	values := make([]string, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		if value := strings.TrimSpace(step.SemanticIntent); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func experimentCheckpointConfirmed(checkpoint *experiment.Checkpoint, effectID string) bool {
	for _, effect := range checkpoint.OnceEffects {
		if effect.EffectID == effectID && effect.Status == "confirmed" {
			return true
		}
	}
	return false
}

func experimentStartedEffectExternalRef(checkpoint *experiment.Checkpoint, effectID string) string {
	for _, effect := range checkpoint.OnceEffects {
		if effect.EffectID == effectID && effect.Status == "started" {
			return strings.TrimSpace(effect.ExternalTaskRef)
		}
	}
	return ""
}

func parseDirectResultEntry(value string) (string, string, bool) {
	parts := strings.SplitN(strings.TrimSpace(value), ":", 3)
	if len(parts) != 3 || parts[0] != "direct" || strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[2]) == "" {
		return "", "", false
	}
	return parts[1], parts[2], true
}

func experimentArtifactsContainRole(artifacts []experiment.ArtifactRef, role string) bool {
	for _, artifact := range artifacts {
		if artifact.Role == role {
			return true
		}
	}
	return false
}

func resultEvidenceRefs(result model.RecordingResultPackage) []string {
	values := []string{result.ResultID}
	if result.StageEventLogRef != nil {
		values = append(values, result.StageEventLogRef.ID)
	}
	for _, artifact := range result.GeneratedAssets {
		if artifact.ID != "" {
			values = append(values, artifact.ID)
		}
	}
	return values
}

func experimentArtifactRefs(downloads []CloudDeliverableDownloadResult) []experiment.ArtifactRef {
	result := make([]experiment.ArtifactRef, 0, len(downloads))
	for _, item := range downloads {
		role := firstNonEmptyString(item.Role, item.Kind, "direct_artifact")
		result = append(result, experiment.ArtifactRef{ArtifactID: item.ArtifactID, Revision: 1, Role: role})
	}
	return result
}

func experimentSegmentRefs(downloads []CloudDeliverableDownloadResult) []experiment.ArtifactRef {
	result := []experiment.ArtifactRef{}
	for _, item := range downloads {
		if item.Kind == "recording_segment" || item.Kind == "recording_segment_manifest" {
			result = append(result, experiment.ArtifactRef{ArtifactID: item.ArtifactID, Revision: 1, Role: item.Kind})
		}
	}
	return result
}

func normalizedDirectPhase(status model.DirectJobStatus) string {
	phase := strings.TrimSpace(status.Stage)
	if phase == "" {
		phase = strings.TrimSpace(status.Status)
	}
	if phase == "" {
		return "waiting_external"
	}
	return phase
}

func allowedObservationTransitions(plan experiment.ObservationPlan, phase string) []string {
	result := []string{phase}
	for _, transition := range plan.Transitions {
		if transition.From == phase {
			result = append(result, transition.To)
		}
	}
	return result
}
