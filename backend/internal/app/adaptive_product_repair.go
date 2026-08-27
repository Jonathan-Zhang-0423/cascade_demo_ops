package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"cascade-demoops/backend/internal/agents"
	"cascade-demoops/backend/internal/experiment"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

func (s *Service) prepareAdaptiveSameEntityProductRepair(ctx context.Context, projectID, sourceJobID string, request experiment.LegExecutionRequest, score model.CapabilityScore) (ProductRunPrepareResult, error) {
	state, err := s.states.Load(ctx, strings.TrimSpace(projectID))
	if err != nil {
		return ProductRunPrepareResult{}, err
	}
	if state.ProjectContext == nil || state.ProjectIntelligence == nil || state.DesktopCloudRun == nil {
		return ProductRunPrepareResult{}, errors.New("same-entity repair requires the persisted project, intelligence pack, and Direct run")
	}
	result, err := s.getDirectHistoricalResult(ctx, projectID, sourceJobID)
	if err != nil && state.DesktopCloudRun.ResultPackage != nil && state.DesktopCloudRun.ResultPackage.CloudJobID == sourceJobID {
		result = *state.DesktopCloudRun.ResultPackage
		err = nil
	}
	if err != nil {
		return ProductRunPrepareResult{}, errors.New("same-entity repair requires a materialized source result")
	}
	if result.Status == model.RecordingResultStatusFailed {
		if _, repairable := adaptiveFailedCapabilityScore(request, result); !repairable && !adaptiveSameEntityRepairPackageRetryable(request, result) {
			return ProductRunPrepareResult{}, errors.New("same-entity repair requires a completed result or a verified interaction-stage failure")
		}
	}
	observed := s.adaptiveObservedSuccessorEvidence(ctx, projectID, state, result)
	entityCandidates := []string{observed.URL}
	// A failed recovery package may have drifted to the workspace before its
	// first product-repair action.  Preserve the concrete entity observed by an
	// earlier verified result instead of compiling the next repair against that
	// shallower shell.  RepairHistory is part of the same run lineage; it does
	// not discover or attach an unrelated project.
	for _, audit := range state.DesktopCloudRun.RepairHistory {
		if audit.ResultPackage == nil {
			continue
		}
		currentURL := ""
		if audit.ResultPackage.FailureDiagnostic != nil {
			currentURL = strings.TrimSpace(audit.ResultPackage.FailureDiagnostic.CurrentURL)
			entityCandidates = append(entityCandidates, currentURL)
		}
		if audit.ResultPackage.StageEventLogRef != nil && currentURL == "" {
			historical := s.adaptiveObservedSuccessorEvidence(ctx, projectID, state, *audit.ResultPackage)
			entityCandidates = append(entityCandidates, historical.URL)
		}
	}
	entityURL := selectAdaptiveBoundEntityURL(request.TargetURL, state.ProjectContext, entityCandidates)
	if entityURL == "" {
		return ProductRunPrepareResult{}, errors.New("same-entity repair has no observed entity entry URL")
	}
	repairPrompt := adaptiveProductRepairPrompt(request.ProductSpec, request.InteractionPlan, score.Missing, request.ProductRepairRounds)
	if repairPrompt == "" {
		return ProductRunPrepareResult{}, errors.New("same-entity repair could not derive a public product request")
	}
	next, err := cloneCascadeStateForRevision(state)
	if err != nil {
		return ProductRunPrepareResult{}, err
	}
	next.ProjectContext.ProductURL = entityURL
	next.ProjectContext.ProductDescription = repairPrompt
	next.ProjectContext.UpdatedAt = time.Now().UTC()
	if next.ProjectContext.Inputs == nil {
		next.ProjectContext.Inputs = &model.ProjectInputBundle{}
	}
	next.ProjectContext.Inputs.RawUserPrompt = repairPrompt
	next.ProjectContext.Inputs.WorkflowExecution = &model.WorkflowExecutionHints{
		TaskPackID: request.WorkflowTemplateID, SameEntityRepair: true, ExistingEntityURL: entityURL,
		RepairInputSemantic: "product_repair", DirectExecution: true, RequiresSubmission: true, ObserveAsyncResult: true,
	}
	next.ProjectContext.Inputs.InteractionContracts, err = compileExperimentInteractionContracts(request.InteractionPlan, request.ObservationPlan)
	if err != nil {
		return ProductRunPrepareResult{}, err
	}
	if next.ProjectIntelligence.RunIntentScope == nil {
		next.ProjectIntelligence.RunIntentScope = &model.RunIntentScope{ID: "same_entity_repair_scope_" + projectID, ProjectID: projectID, SchemaVersion: model.ProjectIntelligencePackSchemaVersion}
	}
	next.ProjectIntelligence.RunIntentScope.ProductURL = entityURL
	next.ProjectContext.ProjectIntelligence = next.ProjectIntelligence
	stagePlan, err := agents.NewBusinessStagePlannerAgent().PlanBusinessStages(ctx, next.ProjectContext, next.RequirementBrief, next.UnderstandingReport, next.ProductMap, next.ProjectIntelligence, next.VerifiedInteractionPlan)
	if err != nil {
		return ProductRunPrepareResult{}, fmt.Errorf("plan same-entity repair stages: %w", err)
	}
	next.ProjectIntelligence.BusinessStagePlan = stagePlan
	graph, err := agents.NewGraphBuilderAgent().GenerateGraph(ctx, next.ProjectContext, next.ProductMap, next.UnderstandingReport, next.ProjectIntelligence)
	if err != nil {
		return ProductRunPrepareResult{}, fmt.Errorf("build same-entity repair graph: %w", err)
	}
	previousRun := state.DesktopCloudRun
	repairHistory := append([]orchestrator.DesktopDirectRepairAuditState(nil), previousRun.RepairHistory...)
	if len(repairHistory) == 0 || repairHistory[len(repairHistory)-1].SourceJobID != sourceJobID {
		resultCopy := result
		repairHistory = append(repairHistory, orchestrator.DesktopDirectRepairAuditState{
			SourceResultID: result.ResultID, SourcePackageID: result.SourcePackageID, SourceJobID: sourceJobID,
			ResultPackage: &resultCopy, DownloadedAssets: append([]orchestrator.DesktopDownloadedAssetState(nil), previousRun.DownloadedAssets...), CreatedAt: time.Now().UTC(),
		})
	}
	next, err = s.flow.RepackageReviewedGraph(ctx, next, graph)
	if err != nil {
		return ProductRunPrepareResult{}, fmt.Errorf("package same-entity repair graph: %w", err)
	}
	if next.ExecutableScriptBundle == nil {
		return ProductRunPrepareResult{}, errors.New("same-entity repair executable bundle is missing")
	}
	baseBundleID, baseBundleHash := "", ""
	if state.ExecutableScriptBundle != nil {
		baseBundleID = state.ExecutableScriptBundle.ID
		baseBundleHash = state.ExecutableScriptBundle.Reproducibility.BundleHashSHA256
	}
	next.ExecutableScriptBundle.RepairLineage = &model.ScriptRepairLineage{
		BaseBundleID: baseBundleID, BaseBundleHashSHA256: baseBundleHash, SourceResultID: result.ResultID, SourceCloudJobID: sourceJobID,
		RepairAttempt: request.ProductRepairRounds + 1, ChangeSummary: "Submit one bounded product repair to the already-bound entity, then re-run required product verification.", CreatedAt: time.Now().UTC(),
	}
	next.ExecutionPackageGeneration = state.ExecutionPackageGeneration + 1
	next.DesktopCloudRun = &orchestrator.DesktopCloudRunState{
		SchemaVersion: desktopCloudRunSchemaVersion, Transport: directTransportStateName,
		OrgID: firstNonEmptyString(previousRun.OrgID, defaultDesktopOrgID), Status: "not_uploaded", Stage: "same_entity_product_repair_ready",
		Message: "已为本次绑定实体生成自动产品修复执行包。", LastRepairSourceID: result.ResultID,
		RepairHistory: repairHistory,
	}
	if err := s.states.Save(ctx, next); err != nil {
		return ProductRunPrepareResult{}, err
	}
	s.invalidateApprovedBuildsForProject(projectID)
	build, err := s.BuildClientExecutionPackage(ctx, projectID, firstNonEmptyString(previousRun.OrgID, defaultDesktopOrgID))
	if err != nil {
		return ProductRunPrepareResult{}, err
	}
	return ProductRunPrepareResult{State: compactStateForPrepareResponse(next, &build), Build: &build}, nil
}

func selectAdaptiveBoundEntityURL(targetURL string, project *model.ProjectContext, candidates []string) string {
	if project == nil {
		return ""
	}
	base := *project
	if strings.TrimSpace(targetURL) != "" {
		base.ProductURL = strings.TrimSpace(targetURL)
	}
	best, bestScore := "", -1
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		score := observedSuccessorURLScore(&base, candidate)
		if candidate == "" || score < 0 || score <= bestScore {
			continue
		}
		best, bestScore = candidate, score
	}
	return best
}

func adaptiveProductRepairPrompt(spec experiment.ProductSpec, plan experiment.InteractionPlan, missing []string, priorAttempts ...int) string {
	statements := map[string]string{}
	for _, step := range plan.Steps {
		statements[strings.TrimSpace(step.StepID)] = strings.TrimSpace(step.SemanticIntent)
	}
	for _, criterion := range spec.ObservableAcceptance {
		if criterion.Required {
			statements[criterion.ID] = strings.TrimSpace(criterion.Statement)
		}
	}
	for _, requirement := range spec.Requirements {
		statements[requirement.ID] = strings.TrimSpace(requirement.Statement)
	}
	selected := []string{}
	seen := map[string]bool{}
	missingSet := map[string]bool{}
	for _, id := range missing {
		missingSet[strings.TrimSpace(id)] = true
	}
	// The score's missing list is normalized for reporting and may therefore
	// be alphabetic rather than causal. Follow the declared interaction plan
	// order so foundational surface readiness is repaired before stability or
	// advanced controls. When the product surface itself is missing, use the
	// first public observable criterion: it is more concrete than the generic
	// harness phrase "surface ready" and remains product-spec driven.
	if missingSet["surface_ready"] {
		for _, criterion := range spec.ObservableAcceptance {
			value := strings.TrimSpace(criterion.Statement)
			if criterion.Required && value != "" {
				selected, seen[value] = append(selected, value), true
				break
			}
		}
	}
	if len(selected) == 0 {
		for _, step := range plan.Steps {
			id := strings.TrimSpace(step.StepID)
			value := strings.TrimSpace(step.SemanticIntent)
			if missingSet[id] && value != "" && !seen[value] {
				selected, seen[value] = append(selected, value), true
				break
			}
		}
	}
	if len(selected) == 0 {
		for _, id := range missing {
			if value := statements[strings.TrimSpace(id)]; value != "" && !seen[value] {
				selected, seen[value] = append(selected, value), true
				break
			}
		}
	}
	if len(selected) == 0 {
		for _, criterion := range spec.ObservableAcceptance {
			if criterion.Required && strings.TrimSpace(criterion.Statement) != "" && !seen[criterion.Statement] {
				selected, seen[criterion.Statement] = append(selected, strings.TrimSpace(criterion.Statement)), true
				break
			}
		}
	}
	if len(selected) == 0 {
		return ""
	}
	repeated := len(priorAttempts) > 0 && priorAttempts[0] > 0
	subject := strings.TrimRight(selected[0], ".。；; ")
	// The initial one-line request intentionally stays provider-neutral and
	// concise. When a real product defect already requires a bounded repair,
	// carry the frozen public visual direction into that same edit so the
	// repaired product does not remain functionally correct but visibly off-spec.
	if !repeated {
		if theme := strings.TrimRight(strings.TrimSpace(spec.VisualDirection.Theme), ".。；; "); theme != "" {
			subject += "；视觉统一为" + theme
		}
	}
	prefix, suffix := "请修复当前项目：", "必须能实际工作。保留已有内容，直接更新当前项目。"
	if repeated {
		prefix, suffix = "上轮修复后实际预览仍失败。请先复现并检查已加载代码，再修复：", "。完成后在预览中运行确认，保留已有内容。"
	}
	prefixRunes, subjectRunes, suffixRunes := []rune(prefix), []rune(subject), []rune(suffix)
	if available := 140 - len(prefixRunes) - len(suffixRunes); available < len(subjectRunes) {
		if available < 2 {
			return ""
		}
		subjectRunes = append(subjectRunes[:available-1], '…')
	}
	return string(prefixRunes) + string(subjectRunes) + string(suffixRunes)
}
