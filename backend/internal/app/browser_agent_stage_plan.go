package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"cascade-demoops/backend/internal/model"
)

const (
	runtimeErrorBrowserAgentContractViolation = "browser_agent_contract_violation"
	runtimeErrorBrowserAgentPolicyDenied      = "browser_agent_policy_denied"
)

type BrowserAgentRuntimePlan struct {
	RunID                  string
	SourcePackageID        string
	SourceBundleHashSHA256 string
	PolicyHashSHA256       string
	AllowedDomains         []string
	ForbiddenPages         []string
	ExplorationScope       model.BrowserAgentExplorationScope
	ForbiddenActions       []string
	RepairPolicy           model.BrowserAgentRepairPolicy
	Stages                 []BrowserAgentRuntimeStage
}

type BrowserAgentRuntimeStage struct {
	ID             string
	Order          int
	NodeID         string
	Objective      string
	BusinessIntent string
	EntryRoute     string
	Route          string
	URL            string
	TargetContract model.BrowserAgentTargetContract
	Components     []model.BrowserAgentComponentTarget
	Interactions   []model.BrowserAgentInteraction
	WaitConditions []string
	CapturePoints  []string
	CapturePlan    *model.BrowserAgentCapturePlan
	SuccessState   string
	DurationMS     int
	Validations    []model.ValidationSpec
	// PreferredSelectorAlternative exists only for this in-memory run. It is
	// set after the Worker has verified one App-approved fallback candidate.
	PreferredSelectorAlternative *model.SelectorCandidate
}

type BrowserAgentActionIntent struct {
	NodeID         string
	StageID        string
	ActionType     model.GraphActionType
	URL            string
	Route          string
	TargetContract model.BrowserAgentTargetContract
	NonDestructive bool
}

type BrowserAgentPolicyDecision struct {
	Allowed bool
	Code    string
	Reason  string
}

type BrowserAgentStageObserver interface {
	ObserveStage(context.Context, BrowserAgentRuntimePlan, BrowserAgentRuntimeStage) (BrowserAgentStageObservation, error)
}

type BrowserAgentStageActionExecutor interface {
	ExecuteStage(context.Context, BrowserAgentRuntimePlan, BrowserAgentRuntimeStage) (BrowserAgentStageActionResult, error)
}

type BrowserAgentStageObservation struct {
	Observation                  model.RuntimeObservation
	EvidenceRefs                 []model.EvidenceRef
	TargetResolved               bool
	PreferredSelectorAlternative *model.SelectorCandidate
	SuggestedWaitCondition       string
}

type BrowserAgentStageActionResult struct {
	Observation  *model.RuntimeObservation
	EvidenceRefs []model.EvidenceRef
}

type BrowserAgentStageEventVerifier interface {
	ValidateStageEvents(context.Context, BrowserAgentValidationContext, []model.StageExecutionEvent) (model.ValidationReport, error)
}

// BrowserAgentStageRepairProposer is deliberately separate from the verifier.
// A verifier may describe a safe mechanical repair, but it never applies one.
type BrowserAgentStageRepairProposer interface {
	ProposeRuntimeRepair(context.Context, BrowserAgentValidationContext, BrowserAgentRuntimeStage, []model.StageExecutionEvent, model.ValidationReport) (model.RuntimeRepairProposal, error)
}

type BrowserAgentStageRunResult struct {
	Events            []model.StageExecutionEvent
	ValidationReports []model.ValidationReport
	PatchLedger       []model.RuntimePatchLedgerEntry
}

type BrowserAgentPolicyGuard interface {
	Authorize(BrowserAgentRuntimePlan, BrowserAgentActionIntent) BrowserAgentPolicyDecision
}

type contractBrowserAgentPolicyGuard struct{}

type browserAgentStageOrchestrator struct {
	guard    BrowserAgentPolicyGuard
	verifier BrowserAgentStageEventVerifier
}

func newBrowserAgentStageOrchestrator(guard BrowserAgentPolicyGuard) browserAgentStageOrchestrator {
	if guard == nil {
		guard = contractBrowserAgentPolicyGuard{}
	}
	return browserAgentStageOrchestrator{guard: guard}
}

func newBrowserAgentStageOrchestratorWithVerifier(guard BrowserAgentPolicyGuard, verifier BrowserAgentStageEventVerifier) browserAgentStageOrchestrator {
	orchestrator := newBrowserAgentStageOrchestrator(guard)
	orchestrator.verifier = verifier
	return orchestrator
}

func (o browserAgentStageOrchestrator) Prepare(pkg *model.ClientExecutionPackage) (BrowserAgentRuntimePlan, error) {
	plan, err := compileBrowserAgentRuntimePlan(pkg)
	if err != nil {
		return BrowserAgentRuntimePlan{}, err
	}
	for _, stage := range plan.Stages {
		for _, interaction := range stage.Interactions {
			intent := BrowserAgentActionIntent{
				NodeID: stage.NodeID, StageID: stage.ID, ActionType: interaction.Kind,
				URL: interaction.Target.URL, Route: stage.Route,
				TargetContract: stage.TargetContract, NonDestructive: interaction.NonDestructive,
			}
			for _, target := range []struct{ url, route string }{{intent.URL, intent.Route}, {stage.URL, stage.EntryRoute}} {
				intent.URL, intent.Route = target.url, target.route
				decision := o.guard.Authorize(plan, intent)
				if !decision.Allowed {
					return BrowserAgentRuntimePlan{}, newRuntimeExecutionError(runtimeErrorBrowserAgentPolicyDenied, fmt.Errorf("stage %s action denied (%s): %s", stage.ID, decision.Code, decision.Reason))
				}
			}
		}
	}
	return plan, nil
}

func (o browserAgentStageOrchestrator) Run(ctx context.Context, plan BrowserAgentRuntimePlan, observer BrowserAgentStageObserver, executor BrowserAgentStageActionExecutor, sink StageExecutionEventSink) (BrowserAgentStageRunResult, error) {
	if observer == nil || executor == nil || sink == nil {
		return BrowserAgentStageRunResult{}, newRuntimeExecutionError(runtimeErrorOutlineRunnerUnavailable, errors.New("browser-agent observer, action executor, and event sink are required"))
	}
	result := BrowserAgentStageRunResult{
		Events:            make([]model.StageExecutionEvent, 0, len(plan.Stages)*7),
		ValidationReports: make([]model.ValidationReport, 0, len(plan.Stages)),
		PatchLedger:       make([]model.RuntimePatchLedgerEntry, 0),
	}
	sequence := int64(0)
	attemptByStage := make(map[string]int, len(plan.Stages))
	for _, stage := range plan.Stages {
		attemptByStage[stage.ID] = 1
	}
	appendEvent := func(stage BrowserAgentRuntimeStage, eventType model.StageExecutionEventType, observation *model.RuntimeObservation, evidenceRefs []model.EvidenceRef) error {
		sequence++
		event := model.StageExecutionEvent{
			SchemaVersion: model.StageExecutionEventSchemaVersion,
			EventID:       fmt.Sprintf("event_%s_%04d", safePathSegment(plan.RunID), sequence),
			RunID:         plan.RunID, SourcePackageID: plan.SourcePackageID,
			SourceBundleHashSHA256: plan.SourceBundleHashSHA256, PolicyHashSHA256: plan.PolicyHashSHA256,
			NodeID: stage.NodeID, StageID: stage.ID, Attempt: attemptByStage[stage.ID], Sequence: sequence,
			EventType: eventType, OccurredAt: timeNowUTC(),
		}
		event.Observation = observation
		event.EvidenceRefs = append([]model.EvidenceRef{}, evidenceRefs...)
		if eventType == model.StageExecutionEventTargetResolved || eventType == model.StageExecutionEventActionStarted || eventType == model.StageExecutionEventActionCompleted {
			event.Action = runtimeActionForStage(stage)
		}
		if err := sink.Append(ctx, event); err != nil {
			return err
		}
		result.Events = append(result.Events, event)
		return nil
	}
	for _, stage := range plan.Stages {
		stageEventStart := len(result.Events)
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := appendEvent(stage, model.StageExecutionEventStageStarted, nil, nil); err != nil {
			return result, err
		}
		observed, err := observer.ObserveStage(ctx, plan, stage)
		if err != nil {
			insufficient := &model.RuntimeObservation{Source: model.RuntimeObservationInsufficient}
			_ = appendEvent(stage, model.StageExecutionEventStageFailed, insufficient, nil)
			return result, newRuntimeExecutionError("browser_agent_observation_failed", err)
		}
		if !runtimeObservationIsRealEvidence(observed.Observation.Source) || len(observed.EvidenceRefs) == 0 {
			insufficient := &model.RuntimeObservation{Source: model.RuntimeObservationInsufficient}
			_ = appendEvent(stage, model.StageExecutionEventStageFailed, insufficient, nil)
			return result, newRuntimeExecutionError("browser_agent_observation_failed", errors.New("stage observer did not provide real evidence"))
		}
		if err := appendEvent(stage, model.StageExecutionEventObservationCollected, &observed.Observation, observed.EvidenceRefs); err != nil {
			return result, err
		}
		activeStage := stage
		if observed.PreferredSelectorAlternative != nil || observed.SuggestedWaitCondition != "" {
			proposal, proposalOK := browserAgentInitialObservationRepairProposal(plan, stage, observed)
			if !proposalOK {
				_ = appendEvent(stage, model.StageExecutionEventStageFailed, &observed.Observation, observed.EvidenceRefs)
				return result, newRuntimeExecutionError("browser_agent_repair_proposal_invalid", errors.New("worker returned an invalid bounded observation repair"))
			}
			if err := appendEvent(stage, model.StageExecutionEventRepairProposed, &observed.Observation, proposal.EvidenceRefs); err != nil {
				return result, err
			}
			patchedStage, ledgerEntry := evaluateBrowserAgentRepair(plan, stage, proposal, result.PatchLedger)
			result.PatchLedger = append(result.PatchLedger, ledgerEntry)
			if !ledgerEntry.Applied {
				_ = appendEvent(stage, model.StageExecutionEventStageFailed, &observed.Observation, proposal.EvidenceRefs)
				return result, newRuntimeExecutionError("browser_agent_repair_policy_denied", fmt.Errorf("selector repair proposal %s was denied by policy", proposal.ProposalID))
			}
			attemptByStage[stage.ID]++
			if err := appendEvent(stage, model.StageExecutionEventRepairApplied, &observed.Observation, proposal.EvidenceRefs); err != nil {
				return result, err
			}
			repairedObservation, repairObserveErr := observer.ObserveStage(ctx, plan, patchedStage)
			if repairObserveErr != nil || !repairedObservation.TargetResolved || !runtimeObservationIsRealEvidence(repairedObservation.Observation.Source) || len(repairedObservation.EvidenceRefs) == 0 {
				_ = appendEvent(stage, model.StageExecutionEventStageFailed, &observed.Observation, proposal.EvidenceRefs)
				if repairObserveErr != nil {
					return result, newRuntimeExecutionError("browser_agent_repair_observation_failed", repairObserveErr)
				}
				return result, newRuntimeExecutionError("browser_agent_repair_observation_failed", errors.New("approved selector repair did not resolve the target"))
			}
			if err := appendEvent(stage, model.StageExecutionEventObservationCollected, &repairedObservation.Observation, repairedObservation.EvidenceRefs); err != nil {
				return result, err
			}
			observed = repairedObservation
			activeStage = patchedStage
		}
		if !observed.TargetResolved {
			_ = appendEvent(stage, model.StageExecutionEventStageFailed, &observed.Observation, observed.EvidenceRefs)
			return result, newRuntimeExecutionError("browser_agent_target_not_resolved", errors.New("no approved selector candidate resolved the target"))
		}
		if observed.TargetResolved {
			if err := appendEvent(stage, model.StageExecutionEventTargetResolved, &observed.Observation, observed.EvidenceRefs); err != nil {
				return result, err
			}
		}
		if err := appendEvent(stage, model.StageExecutionEventActionStarted, &observed.Observation, observed.EvidenceRefs); err != nil {
			return result, err
		}
		actionResult, err := executor.ExecuteStage(ctx, plan, activeStage)
		if err != nil {
			_ = appendEvent(stage, model.StageExecutionEventStageFailed, &observed.Observation, observed.EvidenceRefs)
			return result, newRuntimeExecutionError("browser_agent_action_failed", err)
		}
		if actionResult.Observation == nil || !runtimeObservationIsRealEvidence(actionResult.Observation.Source) || len(actionResult.EvidenceRefs) == 0 {
			_ = appendEvent(stage, model.StageExecutionEventStageFailed, &observed.Observation, observed.EvidenceRefs)
			return result, newRuntimeExecutionError("browser_agent_action_failed", errors.New("stage action executor did not provide real completion evidence"))
		}
		if err := appendEvent(stage, model.StageExecutionEventActionCompleted, actionResult.Observation, actionResult.EvidenceRefs); err != nil {
			return result, err
		}
		if err := appendEvent(stage, model.StageExecutionEventOutcomeObserved, actionResult.Observation, actionResult.EvidenceRefs); err != nil {
			return result, err
		}
		if o.verifier != nil {
			stageEvents := result.Events[stageEventStart:]
			report, err := o.verifier.ValidateStageEvents(ctx, BrowserAgentValidationContext{
				SourcePackageID: plan.SourcePackageID, SourceBundleHashSHA256: plan.SourceBundleHashSHA256,
				EffectivePolicyHashSHA256: plan.PolicyHashSHA256,
			}, stageEvents)
			if err != nil {
				return result, newRuntimeExecutionError("outcome_verification_failed", err)
			}
			if err := report.Validate(); err != nil {
				return result, newRuntimeExecutionError("outcome_verification_failed", err)
			}
			result.ValidationReports = append(result.ValidationReports, report)
			if report.Decision == model.ValidationDecisionRepairAllowed {
				proposer, ok := o.verifier.(BrowserAgentStageRepairProposer)
				if !ok {
					_ = appendEvent(stage, model.StageExecutionEventStageFailed, actionResult.Observation, actionResult.EvidenceRefs)
					return result, newRuntimeExecutionError("browser_agent_repair_proposal_missing", fmt.Errorf("outcome verifier requested repair for stage %s without a proposal", stage.ID))
				}
				proposal, proposalErr := proposer.ProposeRuntimeRepair(ctx, BrowserAgentValidationContext{
					SourcePackageID: plan.SourcePackageID, SourceBundleHashSHA256: plan.SourceBundleHashSHA256,
					EffectivePolicyHashSHA256: plan.PolicyHashSHA256,
				}, stage, stageEvents, report)
				if proposalErr != nil {
					_ = appendEvent(stage, model.StageExecutionEventStageFailed, actionResult.Observation, actionResult.EvidenceRefs)
					return result, newRuntimeExecutionError("browser_agent_repair_proposal_invalid", proposalErr)
				}
				if err := proposal.Validate(); err != nil {
					_ = appendEvent(stage, model.StageExecutionEventStageFailed, actionResult.Observation, actionResult.EvidenceRefs)
					return result, newRuntimeExecutionError("browser_agent_repair_proposal_invalid", err)
				}
				report.RepairProposalRefs = append(report.RepairProposalRefs, proposal.ProposalID)
				result.ValidationReports[len(result.ValidationReports)-1] = report
				if err := appendEvent(stage, model.StageExecutionEventRepairProposed, actionResult.Observation, proposal.EvidenceRefs); err != nil {
					return result, err
				}
				patchedStage, ledgerEntry := evaluateBrowserAgentRepair(plan, stage, proposal, result.PatchLedger)
				result.PatchLedger = append(result.PatchLedger, ledgerEntry)
				if !ledgerEntry.Applied {
					_ = appendEvent(stage, model.StageExecutionEventStageFailed, actionResult.Observation, proposal.EvidenceRefs)
					return result, newRuntimeExecutionError("browser_agent_repair_policy_denied", fmt.Errorf("repair proposal %s was denied by policy", proposal.ProposalID))
				}
				attemptByStage[stage.ID]++
				if err := appendEvent(stage, model.StageExecutionEventRepairApplied, actionResult.Observation, proposal.EvidenceRefs); err != nil {
					return result, err
				}
				repairEventStart := len(result.Events)
				if err := appendEvent(stage, model.StageExecutionEventActionStarted, actionResult.Observation, proposal.EvidenceRefs); err != nil {
					return result, err
				}
				repairActionResult, repairErr := executor.ExecuteStage(ctx, plan, patchedStage)
				if repairErr != nil || repairActionResult.Observation == nil || !runtimeObservationIsRealEvidence(repairActionResult.Observation.Source) || len(repairActionResult.EvidenceRefs) == 0 {
					_ = appendEvent(stage, model.StageExecutionEventStageFailed, actionResult.Observation, proposal.EvidenceRefs)
					if repairErr != nil {
						return result, newRuntimeExecutionError("browser_agent_repair_action_failed", repairErr)
					}
					return result, newRuntimeExecutionError("browser_agent_repair_action_failed", errors.New("repaired stage did not provide real completion evidence"))
				}
				if err := appendEvent(stage, model.StageExecutionEventActionCompleted, repairActionResult.Observation, repairActionResult.EvidenceRefs); err != nil {
					return result, err
				}
				if err := appendEvent(stage, model.StageExecutionEventOutcomeObserved, repairActionResult.Observation, repairActionResult.EvidenceRefs); err != nil {
					return result, err
				}
				repairEvents := result.Events[repairEventStart:]
				repairReport, repairVerifyErr := o.verifier.ValidateStageEvents(ctx, BrowserAgentValidationContext{SourcePackageID: plan.SourcePackageID, SourceBundleHashSHA256: plan.SourceBundleHashSHA256, EffectivePolicyHashSHA256: plan.PolicyHashSHA256}, repairEvents)
				if repairVerifyErr != nil || repairReport.Validate() != nil || repairReport.Decision != model.ValidationDecisionContinue {
					_ = appendEvent(stage, model.StageExecutionEventStageFailed, repairActionResult.Observation, repairActionResult.EvidenceRefs)
					if repairVerifyErr != nil {
						return result, newRuntimeExecutionError("outcome_verification_failed", repairVerifyErr)
					}
					return result, newRuntimeExecutionError("outcome_verification_failed", fmt.Errorf("repaired stage %s did not pass outcome verification", stage.ID))
				}
				result.ValidationReports = append(result.ValidationReports, repairReport)
				actionResult = repairActionResult
				report = repairReport
			}
			if report.Decision != model.ValidationDecisionContinue {
				_ = appendEvent(stage, model.StageExecutionEventStageFailed, actionResult.Observation, actionResult.EvidenceRefs)
				return result, newRuntimeExecutionError("outcome_verification_failed", fmt.Errorf("outcome verifier stopped stage %s with decision %s", stage.ID, report.Decision))
			}
		}
		if err := appendEvent(stage, model.StageExecutionEventStageCompleted, actionResult.Observation, actionResult.EvidenceRefs); err != nil {
			return result, err
		}
	}
	return result, nil
}

func runtimeActionForStage(stage BrowserAgentRuntimeStage) *model.RuntimeAction {
	action := model.RuntimeAction{TargetSemanticID: stage.TargetContract.SemanticID}
	if len(stage.Interactions) > 0 {
		action.Kind = string(stage.Interactions[0].Kind)
	}
	return &action
}

// browserAgentSelectorAlternativeProposal accepts only a candidate the Worker
// already proved unique, visible, and contract-compatible on the live page.
// It never constructs a selector from page content.
func browserAgentSelectorAlternativeProposal(plan BrowserAgentRuntimePlan, stage BrowserAgentRuntimeStage, observed BrowserAgentStageObservation) model.RuntimeRepairProposal {
	candidate := *observed.PreferredSelectorAlternative
	return model.RuntimeRepairProposal{
		SchemaVersion: model.RuntimeRepairProposalSchemaVersion,
		ProposalID:    fmt.Sprintf("selector_%s_%s_%s", safePathSegment(plan.RunID), safePathSegment(stage.ID), safePathSegment(candidate.Kind+"_"+candidate.Value)),
		RunID:         plan.RunID, NodeID: stage.NodeID, StageID: stage.ID,
		BaseBundleHashSHA256: plan.SourceBundleHashSHA256, PolicyHashSHA256: plan.PolicyHashSHA256,
		RepairKind: "selector_alternative", Field: "script_outline.stages[].components[].selector",
		Before: currentStageSelector(stage), After: selectorCandidateEncoding(candidate),
		Confidence: 1, EvidenceRefs: append([]model.EvidenceRef{}, observed.EvidenceRefs...), CreatedAt: timeNowUTC(),
	}
}

func browserAgentInitialObservationRepairProposal(plan BrowserAgentRuntimePlan, stage BrowserAgentRuntimeStage, observed BrowserAgentStageObservation) (model.RuntimeRepairProposal, bool) {
	if observed.PreferredSelectorAlternative != nil {
		return browserAgentSelectorAlternativeProposal(plan, stage, observed), true
	}
	before := stageEntryWaitCondition(stage)
	if before == "" || !strictlyLongerWaitCondition(before, observed.SuggestedWaitCondition) {
		return model.RuntimeRepairProposal{}, false
	}
	return model.RuntimeRepairProposal{
		SchemaVersion: model.RuntimeRepairProposalSchemaVersion,
		ProposalID:    fmt.Sprintf("wait_%s_%s", safePathSegment(plan.RunID), safePathSegment(stage.ID)),
		RunID:         plan.RunID, NodeID: stage.NodeID, StageID: stage.ID,
		BaseBundleHashSHA256: plan.SourceBundleHashSHA256, PolicyHashSHA256: plan.PolicyHashSHA256,
		RepairKind: "wait_strategy", Field: "script_outline.stages[].wait_conditions",
		Before: before, After: observed.SuggestedWaitCondition,
		Confidence: 1, EvidenceRefs: append([]model.EvidenceRef{}, observed.EvidenceRefs...), CreatedAt: timeNowUTC(),
	}, true
}

func stageEntryWaitCondition(stage BrowserAgentRuntimeStage) string {
	for _, condition := range stage.WaitConditions {
		if _, ok := boundedEntryWaitMilliseconds(condition); ok {
			return condition
		}
	}
	return ""
}

func strictlyLongerWaitCondition(before, after string) bool {
	beforeMS, beforeOK := boundedEntryWaitMilliseconds(before)
	afterMS, afterOK := boundedEntryWaitMilliseconds(after)
	return beforeOK && afterOK && afterMS > beforeMS
}

func boundedEntryWaitMilliseconds(condition string) (int, bool) {
	var milliseconds int
	if _, err := fmt.Sscanf(strings.TrimSpace(condition), "wait_after_entry_at_least_%dms", &milliseconds); err != nil || milliseconds < 0 || milliseconds > 15_000 {
		return 0, false
	}
	return milliseconds, true
}

func currentStageSelector(stage BrowserAgentRuntimeStage) string {
	for _, component := range stage.Components {
		if stage.TargetContract.ComponentRef == "" || component.ComponentRef == stage.TargetContract.ComponentRef {
			return component.Selector
		}
	}
	return ""
}

func selectorCandidateEncoding(candidate model.SelectorCandidate) string {
	return strings.TrimSpace(candidate.Kind) + ":" + strings.TrimSpace(candidate.Value)
}

func runtimeObservationIsRealEvidence(source model.RuntimeObservationSource) bool {
	switch source {
	case model.RuntimeObservationActualBrowser, model.RuntimeObservationAssertion, model.RuntimeObservationArtifact:
		return true
	default:
		return false
	}
}

func compileBrowserAgentRuntimePlan(pkg *model.ClientExecutionPackage) (BrowserAgentRuntimePlan, error) {
	if pkg == nil || pkg.ExecutableScriptBundle == nil {
		return BrowserAgentRuntimePlan{}, newRuntimeExecutionError(runtimeErrorBrowserAgentContractViolation, errors.New("browser-agent package or executable bundle is missing"))
	}
	bundle := pkg.ExecutableScriptBundle
	if bundle.ScriptManifest.Runtime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 || bundle.PlanJSON == nil || bundle.StageApprovalPlan == nil || bundle.ScriptOutline == nil || bundle.BrowserAgentContract == nil {
		return BrowserAgentRuntimePlan{}, newRuntimeExecutionError(runtimeErrorBrowserAgentContractViolation, errors.New("browser-agent outline contract is incomplete"))
	}

	approvedByNode := make(map[string]model.StageApprovalStage, len(bundle.StageApprovalPlan.Stages))
	for _, stage := range bundle.StageApprovalPlan.Stages {
		if _, exists := approvedByNode[stage.NodeID]; exists {
			return BrowserAgentRuntimePlan{}, newRuntimeExecutionError(runtimeErrorBrowserAgentContractViolation, fmt.Errorf("stage approval plan contains duplicate node_id %q", stage.NodeID))
		}
		approvedByNode[stage.NodeID] = stage
	}
	outlineByNode := make(map[string]model.BrowserAgentOutlineStage, len(bundle.ScriptOutline.Stages))
	for _, stage := range bundle.ScriptOutline.Stages {
		if _, exists := outlineByNode[stage.NodeID]; exists {
			return BrowserAgentRuntimePlan{}, newRuntimeExecutionError(runtimeErrorBrowserAgentContractViolation, fmt.Errorf("script outline contains duplicate node_id %q", stage.NodeID))
		}
		outlineByNode[stage.NodeID] = stage
	}
	planStepByNode := make(map[string]model.ScriptStep, len(bundle.PlanJSON.Steps))
	for _, step := range bundle.PlanJSON.Steps {
		if _, exists := planStepByNode[step.NodeID]; exists {
			return BrowserAgentRuntimePlan{}, newRuntimeExecutionError(runtimeErrorBrowserAgentContractViolation, fmt.Errorf("plan_json contains duplicate node_id %q", step.NodeID))
		}
		planStepByNode[step.NodeID] = step
	}

	approvedStages := append([]model.StageApprovalStage{}, bundle.StageApprovalPlan.Stages...)
	sort.SliceStable(approvedStages, func(i, j int) bool { return approvedStages[i].Order < approvedStages[j].Order })
	plan := BrowserAgentRuntimePlan{
		RunID: pkg.RecordingRunSpec.RunID, SourcePackageID: pkg.PackageID,
		SourceBundleHashSHA256: bundle.Reproducibility.BundleHashSHA256,
		PolicyHashSHA256:       bundle.Reproducibility.BrowserAgentContractHashSHA256,
		AllowedDomains:         append([]string{}, bundle.SecurityPolicy.AllowedDomains...),
		ForbiddenPages:         append([]string{}, bundle.SecurityPolicy.ForbiddenPages...),
		ExplorationScope:       bundle.ScriptOutline.AllowedExplorationScope,
		ForbiddenActions:       append([]string{}, bundle.ScriptOutline.ForbiddenActions...),
		RepairPolicy:           bundle.BrowserAgentContract.RepairPolicy,
		Stages:                 make([]BrowserAgentRuntimeStage, 0, len(approvedStages)),
	}
	for index, approved := range approvedStages {
		outline, ok := outlineByNode[approved.NodeID]
		if !ok {
			return BrowserAgentRuntimePlan{}, newRuntimeExecutionError(runtimeErrorBrowserAgentContractViolation, fmt.Errorf("script outline is missing approved node_id %q", approved.NodeID))
		}
		planStep, ok := planStepByNode[approved.NodeID]
		if !ok {
			return BrowserAgentRuntimePlan{}, newRuntimeExecutionError(runtimeErrorBrowserAgentContractViolation, fmt.Errorf("plan_json is missing approved node_id %q", approved.NodeID))
		}
		if approved.Order != index+1 || outline.Order != approved.Order {
			return BrowserAgentRuntimePlan{}, newRuntimeExecutionError(runtimeErrorBrowserAgentContractViolation, fmt.Errorf("browser-agent stage order mismatch for node_id %q", approved.NodeID))
		}
		if outline.StageID != "" && outline.StageID != approved.ID {
			return BrowserAgentRuntimePlan{}, newRuntimeExecutionError(runtimeErrorBrowserAgentContractViolation, fmt.Errorf("browser-agent stage_id mismatch for node_id %q", approved.NodeID))
		}
		if strings.TrimSpace(outline.Objective) != strings.TrimSpace(approved.Objective) || strings.TrimSpace(outline.SuccessState) != strings.TrimSpace(approved.SuccessState) {
			return BrowserAgentRuntimePlan{}, newRuntimeExecutionError(runtimeErrorBrowserAgentContractViolation, fmt.Errorf("browser-agent immutable objective or success state conflict for node_id %q", approved.NodeID))
		}
		if len(outline.Interactions) == 0 || outline.Interactions[0].Kind != approved.Interaction.Kind {
			return BrowserAgentRuntimePlan{}, newRuntimeExecutionError(runtimeErrorBrowserAgentContractViolation, fmt.Errorf("browser-agent action semantics conflict for node_id %q", approved.NodeID))
		}
		for _, interaction := range outline.Interactions {
			if interaction.Kind != approved.Interaction.Kind && !browserAgentAncillaryAction(interaction.Kind) {
				return BrowserAgentRuntimePlan{}, newRuntimeExecutionError(runtimeErrorBrowserAgentContractViolation, fmt.Errorf("browser-agent outline inserted unapproved action type %q for node_id %q", interaction.Kind, approved.NodeID))
			}
			if interaction.Kind == approved.Interaction.Kind && (interaction.Value != approved.Interaction.Value || interaction.InputRef != approved.Interaction.InputRef || interaction.SecretRef != approved.Interaction.SecretRef) {
				return BrowserAgentRuntimePlan{}, newRuntimeExecutionError(runtimeErrorBrowserAgentContractViolation, fmt.Errorf("browser-agent input semantics conflict for node_id %q", approved.NodeID))
			}
		}
		approvedTarget, outlineTarget := approved.TargetContract, outline.TargetContract
		if approvedTarget == nil || outlineTarget == nil || approvedTarget.SemanticID != outlineTarget.SemanticID || approvedTarget.Destructive != outlineTarget.Destructive {
			return BrowserAgentRuntimePlan{}, newRuntimeExecutionError(runtimeErrorBrowserAgentContractViolation, fmt.Errorf("browser-agent target contract conflict for node_id %q", approved.NodeID))
		}
		capturePlan := mergeApprovedCapturePlan(approved.CapturePlan, outline.CapturePlan)
		plan.Stages = append(plan.Stages, BrowserAgentRuntimeStage{
			ID: approved.ID, Order: approved.Order, NodeID: approved.NodeID, Objective: approved.Objective,
			BusinessIntent: approved.BusinessIntent, EntryRoute: firstNonEmptyString(outline.EntryRoute, approved.EntryRoute),
			Route: firstNonEmptyString(outline.Route, approved.TargetRoute), URL: firstNonEmptyString(outline.URL, approved.TargetURL),
			TargetContract: *approvedTarget, Components: append([]model.BrowserAgentComponentTarget{}, outline.Components...),
			Interactions:   append([]model.BrowserAgentInteraction{}, outline.Interactions...),
			WaitConditions: append([]string{}, outline.WaitConditions...), CapturePoints: append([]string{}, outline.CapturePoints...),
			CapturePlan: capturePlan, SuccessState: approved.SuccessState, DurationMS: approved.DurationMS,
			Validations: append([]model.ValidationSpec{}, planStep.Validations...),
		})
	}
	if len(plan.Stages) != len(outlineByNode) {
		return BrowserAgentRuntimePlan{}, newRuntimeExecutionError(runtimeErrorBrowserAgentContractViolation, errors.New("script outline contains stages that were not approved"))
	}
	return plan, nil
}

// Explicit capture duration is an App-approved business requirement. The
// outline may add wait/capture detail, but can never shorten that minimum.
func mergeApprovedCapturePlan(approved, outline *model.BrowserAgentCapturePlan) *model.BrowserAgentCapturePlan {
	if approved == nil && outline == nil {
		return nil
	}
	if outline == nil {
		copy := *approved
		return &copy
	}
	copy := *outline
	if approved != nil && approved.MinDurationMS > copy.MinDurationMS {
		copy.MinDurationMS = approved.MinDurationMS
	}
	return &copy
}

func (contractBrowserAgentPolicyGuard) Authorize(plan BrowserAgentRuntimePlan, intent BrowserAgentActionIntent) BrowserAgentPolicyDecision {
	if strings.TrimSpace(intent.NodeID) == "" || strings.TrimSpace(intent.StageID) == "" {
		return deniedBrowserAgentPolicy("runtime_identity_missing", "action is not bound to an approved stage and node")
	}
	if !browserAgentActionTypeAllowed(intent.ActionType) {
		return deniedBrowserAgentPolicy("action_type_not_allowed", fmt.Sprintf("action type %q is not allowed", intent.ActionType))
	}
	for _, forbidden := range plan.ForbiddenActions {
		if strings.EqualFold(strings.TrimSpace(forbidden), string(intent.ActionType)) {
			return deniedBrowserAgentPolicy("forbidden_action", fmt.Sprintf("action type %q is forbidden by the outline", intent.ActionType))
		}
	}
	var approvedStage *BrowserAgentRuntimeStage
	for index := range plan.Stages {
		if plan.Stages[index].ID == intent.StageID && plan.Stages[index].NodeID == intent.NodeID {
			approvedStage = &plan.Stages[index]
			break
		}
	}
	if approvedStage == nil {
		return deniedBrowserAgentPolicy("stage_not_approved", "action stage and node are not present in the compiled approval plan")
	}
	if intent.TargetContract.SemanticID != approvedStage.TargetContract.SemanticID {
		return deniedBrowserAgentPolicy("target_contract_mismatch", "action target does not match the approved semantic target")
	}
	if intent.TargetContract.Destructive || !intent.NonDestructive {
		return deniedBrowserAgentPolicy("destructive_action_denied", "destructive or unclassified action is not allowed")
	}
	target := firstNonEmptyString(intent.URL, intent.Route)
	if target != "" {
		if !browserAgentURLAllowed(target, plan.AllowedDomains) {
			return deniedBrowserAgentPolicy("domain_not_allowed", "target is outside allowed domains")
		}
		if browserAgentPathForbidden(target, plan.ForbiddenPages, plan.ExplorationScope.ForbiddenPathPrefixes) {
			return deniedBrowserAgentPolicy("forbidden_page", "target matches a forbidden page or control-plane prefix")
		}
		lowerTarget := strings.ToLower(target)
		for _, keyword := range plan.ExplorationScope.ForbiddenKeywords {
			keyword = strings.ToLower(strings.TrimSpace(keyword))
			if keyword != "" && strings.Contains(lowerTarget, keyword) {
				return deniedBrowserAgentPolicy("forbidden_keyword", "target contains a forbidden exploration keyword")
			}
		}
	}
	return BrowserAgentPolicyDecision{Allowed: true, Code: "allowed", Reason: "action remains inside the approved non-destructive contract"}
}

func deniedBrowserAgentPolicy(code string, reason string) BrowserAgentPolicyDecision {
	return BrowserAgentPolicyDecision{Allowed: false, Code: code, Reason: reason}
}

func browserAgentActionTypeAllowed(action model.GraphActionType) bool {
	switch action {
	case model.GraphActionNavigate, model.GraphActionClick, model.GraphActionFill, model.GraphActionSelect,
		model.GraphActionUpload, model.GraphActionWait, model.GraphActionAssert, model.GraphActionInspect:
		return true
	default:
		return false
	}
}

func browserAgentAncillaryAction(action model.GraphActionType) bool {
	return action == model.GraphActionWait || action == model.GraphActionAssert || action == model.GraphActionInspect
}

func browserAgentURLAllowed(value string, allowedDomains []string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "#") {
		return true
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	for _, domain := range allowedDomains {
		normalized := normalizeBrowserAgentDomain(domain)
		if normalized != "" && (host == normalized || strings.HasSuffix(host, "."+normalized)) {
			return true
		}
	}
	return false
}

func browserAgentPathForbidden(value string, forbiddenPages []string, forbiddenPrefixes []string) bool {
	path := strings.TrimSpace(value)
	if parsed, err := url.Parse(value); err == nil && parsed.Path != "" {
		path = parsed.Path
	}
	path = "/" + strings.TrimLeft(strings.ToLower(path), "/")
	for _, forbidden := range append(append([]string{}, forbiddenPages...), forbiddenPrefixes...) {
		prefix := "/" + strings.TrimLeft(strings.ToLower(strings.TrimSpace(forbidden)), "/")
		if prefix != "/" && (path == prefix || strings.HasPrefix(path, strings.TrimRight(prefix, "/")+"/")) {
			return true
		}
	}
	return false
}

func normalizeBrowserAgentDomain(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if strings.Contains(value, "://") {
		if parsed, err := url.Parse(value); err == nil {
			return strings.TrimPrefix(parsed.Hostname(), ".")
		}
	}
	return strings.TrimPrefix(strings.Split(strings.Split(value, "/")[0], ":")[0], ".")
}
