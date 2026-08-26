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
	runtimeErrorWorkerRestartInjected         = "worker_restart_after_once_effect"
)

type BrowserAgentRuntimePlan struct {
	RunID                  string
	SourcePackageID        string
	SourceBundleHashSHA256 string
	SourcePlanHashSHA256   string
	PolicyHashSHA256       string
	HarnessProfile         string
	// The verifier receives the approved contracts, never a browser/page
	// object. This keeps Validation Agent decisions traceable to the package.
	WorkflowGraph        *model.DemoWorkflowGraph
	Plan                 *model.ExecutionScriptDocument
	StageApprovalPlan    *model.StageApprovalPlan
	ScriptOutline        *model.BrowserAgentScriptOutline
	BrowserAgentContract *model.BrowserAgentContract
	AllowedDomains       []string
	ForbiddenPages       []string
	ExplorationScope     model.BrowserAgentExplorationScope
	ForbiddenActions     []string
	RepairPolicy         model.BrowserAgentRepairPolicy
	Stages               []BrowserAgentRuntimeStage
	// InterruptAfterFirstOnceEffect is an internal recovery-test hook carried
	// only by an explicitly authorized experiment package. It never changes
	// action selection or replay semantics.
	InterruptAfterFirstOnceEffect bool
}

type BrowserAgentRuntimeStage struct {
	ID                               string
	Order                            int
	NodeID                           string
	StageKind                        model.BusinessStageKind
	Objective                        string
	BusinessIntent                   string
	EntryRoute                       string
	Route                            string
	URL                              string
	TargetRouteTemplate              string
	ExpectedRouteAfterAction         string
	RuntimeRouteVerificationRequired bool
	TargetContract                   model.BrowserAgentTargetContract
	InteractionContract              *model.InteractionContract
	Components                       []model.BrowserAgentComponentTarget
	Interactions                     []model.BrowserAgentInteraction
	WaitConditions                   []string
	CapturePoints                    []string
	CapturePlan                      *model.BrowserAgentCapturePlan
	SuccessState                     string
	DurationMS                       int
	Validations                      []model.ValidationSpec
	// PreferredSelectorAlternative exists only for this in-memory run. It is
	// set after the Worker has verified one App-approved fallback candidate.
	PreferredSelectorAlternative *model.SelectorCandidate
	// EvidenceBoundSelectorAlternatives are selectors already declared by the
	// App in this stage whose evidence IDs also appear on the approved
	// interaction target. They are runtime-only discovery candidates: the
	// Worker must still prove that exactly one is visible and satisfies the
	// immutable target contract before the existing repair policy may apply it.
	EvidenceBoundSelectorAlternatives []model.SelectorCandidate
	// ManualSessionCheckpoint exists only in a dev-visible post-login runtime
	// copy. It never changes the App package or the formal runtime plan.
	ManualSessionCheckpoint bool
	// CheckpointRestore is runtime-only and is never written back to the
	// approved package. It binds a resume observation to the exact URL already
	// recorded in the append-only stage audit log.
	CheckpointRestore bool
}

type BrowserAgentActionIntent struct {
	NodeID           string
	StageID          string
	InteractionIndex int
	ActionType       model.GraphActionType
	URL              string
	Route            string
	TargetContract   model.BrowserAgentTargetContract
	NonDestructive   bool
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

// BrowserAgentStageOutcomeRevalidator is intentionally separate from action
// execution. Capture-timing repairs may wait and recapture evidence, but must
// not replay an already completed business action.
type BrowserAgentStageOutcomeRevalidator interface {
	RevalidateStage(context.Context, BrowserAgentRuntimePlan, BrowserAgentRuntimeStage) (BrowserAgentStageActionResult, error)
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
	ValidateStageEvents(context.Context, model.BrowserAgentValidationContext, []model.StageExecutionEvent) (model.ValidationReport, error)
}

// BrowserAgentStageRepairProposer is deliberately separate from the verifier.
// A verifier may describe a safe mechanical repair, but it never applies one.
type BrowserAgentStageRepairProposer interface {
	ProposeRuntimeRepair(context.Context, model.BrowserAgentValidationContext, BrowserAgentRuntimeStage, []model.StageExecutionEvent, model.ValidationReport) (model.RuntimeRepairProposal, error)
}

type BrowserAgentStageRunResult struct {
	Events            []model.StageExecutionEvent
	ValidationReports []model.ValidationReport
	PatchLedger       []model.RuntimePatchLedgerEntry
	// AuditError is retained even when an error occurs while recording a
	// terminal failure event. The caller must treat it as a platform failure,
	// rather than silently delivering a result with an incomplete audit trail.
	AuditError error
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

func (p BrowserAgentRuntimePlan) validationContext() model.BrowserAgentValidationContext {
	return model.BrowserAgentValidationContext{
		RunID: p.RunID, SourcePackageID: p.SourcePackageID, SourceBundleHashSHA256: p.SourceBundleHashSHA256,
		EffectivePolicyHashSHA256: p.PolicyHashSHA256, AllowedDomains: p.AllowedDomains, WorkflowGraph: p.WorkflowGraph, Plan: p.Plan,
		StageApprovalPlan: p.StageApprovalPlan, ScriptOutline: p.ScriptOutline, BrowserAgentContract: p.BrowserAgentContract,
	}
}

func (o browserAgentStageOrchestrator) Prepare(pkg *model.ClientExecutionPackage) (BrowserAgentRuntimePlan, error) {
	plan, err := compileBrowserAgentRuntimePlan(pkg)
	if err != nil {
		return BrowserAgentRuntimePlan{}, err
	}
	for _, stage := range plan.Stages {
		for interactionIndex, interaction := range stage.Interactions {
			intent := BrowserAgentActionIntent{
				NodeID: stage.NodeID, StageID: stage.ID, InteractionIndex: interactionIndex, ActionType: interaction.Kind,
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
	completedStages := map[string]model.StageExecutionEvent{}
	skippedOptionalStages := map[string]bool{}
	actionCompletedStages := map[string]bool{}
	latestCompletedOrder := 0
	latestReachedOrder := 0
	interruptionConsumed := false
	historyEvents := []model.StageExecutionEvent{}
	if history, ok := sink.(stageExecutionEventHistory); ok {
		historyEvents = append(historyEvents, history.Events()...)
		for _, event := range historyEvents {
			if event.RunID != plan.RunID || event.SourcePackageID != plan.SourcePackageID || event.SourceBundleHashSHA256 != plan.SourceBundleHashSHA256 || event.PolicyHashSHA256 != plan.PolicyHashSHA256 {
				continue
			}
			if event.Sequence > sequence {
				sequence = event.Sequence
			}
			for _, stage := range plan.Stages {
				if stage.ID == event.StageID && stage.Order > latestReachedOrder {
					latestReachedOrder = stage.Order
				}
			}
			if event.EventType == model.StageExecutionEventStageCompleted {
				completedStages[event.StageID] = event
				for _, stage := range plan.Stages {
					if stage.ID == event.StageID && stage.Order > latestCompletedOrder {
						latestCompletedOrder = stage.Order
					}
				}
			}
			if event.EventType == model.StageExecutionEventStepSatisfied && event.HarnessDecision != nil && event.HarnessDecision.Kind == model.HarnessDecisionSkip {
				skippedOptionalStages[event.StageID] = true
			}
			if event.EventType == model.StageExecutionEventActionCompleted {
				actionCompletedStages[event.StageID] = true
			}
			if event.EventType == model.StageExecutionEventStageResumed {
				interruptionConsumed = true
			}
		}
	}
	attemptByStage := make(map[string]int, len(plan.Stages))
	for _, stage := range plan.Stages {
		attemptByStage[stage.ID] = 1
	}
	appendEventDetails := func(stage BrowserAgentRuntimeStage, eventType model.StageExecutionEventType, observation *model.RuntimeObservation, evidenceRefs []model.EvidenceRef, decision *model.HarnessDecision, effect *model.ActionEffectCheckpoint, capability *model.CapabilityScore) error {
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
		event.HarnessDecision = decision
		event.ActionEffect = effect
		event.CapabilityScore = capability
		event.EvidenceRefs = append([]model.EvidenceRef{}, evidenceRefs...)
		if eventType == model.StageExecutionEventTargetResolved || eventType == model.StageExecutionEventActionStarted || eventType == model.StageExecutionEventActionCompleted {
			event.Action = runtimeActionForStage(stage)
		}
		if err := sink.Append(ctx, event); err != nil {
			result.AuditError = err
			return err
		}
		result.Events = append(result.Events, event)
		return nil
	}
	appendEvent := func(stage BrowserAgentRuntimeStage, eventType model.StageExecutionEventType, observation *model.RuntimeObservation, evidenceRefs []model.EvidenceRef) error {
		return appendEventDetails(stage, eventType, observation, evidenceRefs, nil, nil, nil)
	}
	absorbedStages := historicalAdaptiveAbsorptions(plan, historyEvents)
	capabilityResults := []model.CapabilityResult{}
	for _, stage := range plan.Stages {
		stageEventStart := len(result.Events)
		if err := ctx.Err(); err != nil {
			return result, err
		}
		completedEvent, completed := completedStages[stage.ID]
		reobserveSkippedContinuation := adaptiveBusinessHarnessEnabled(plan) && adaptiveStageOptionalWhenTargetAbsent(stage) && skippedOptionalStages[stage.ID] && !actionCompletedStages[stage.ID]
		if completed && !reobserveSkippedContinuation {
			if stage.Order < latestCompletedOrder {
				if err := appendEvent(stage, model.StageExecutionEventStageResumed, completedEvent.Observation, completedEvent.EvidenceRefs); err != nil {
					return result, err
				}
				if stageReplayPolicy(stage) == model.InteractionReplayOnceEffect {
					interruptionConsumed = true
				}
				continue
			}
			revalidator, ok := executor.(BrowserAgentStageOutcomeRevalidator)
			if !ok {
				return result, newRuntimeExecutionError("browser_agent_checkpoint_revalidation_unavailable", fmt.Errorf("completed stage %s requires non-action revalidation", stage.ID))
			}
			checkpointStage := bindRuntimeStageToCheckpoint(stage, completedEvent.Observation)
			revalidated, revalidateErr := revalidator.RevalidateStage(ctx, plan, checkpointStage)
			// If a later stage already emitted an audit event, this stage's
			// successful outcome was consumed before the interruption. Restore
			// and prove the exact audited result route, but do not require a
			// mutable business assertion from an earlier observation to remain
			// true. The first incomplete stage will validate the current state.
			allowConsumedCheckpointRoute := latestReachedOrder > stage.Order
			if revalidateErr == nil && validResumedStageObservation(revalidated, completedEvent.Observation, stageReplayPolicy(stage), allowConsumedCheckpointRoute) {
				if err := appendEvent(stage, model.StageExecutionEventStageResumed, revalidated.Observation, revalidated.EvidenceRefs); err != nil {
					return result, err
				}
				if stageReplayPolicy(stage) == model.InteractionReplayOnceEffect {
					interruptionConsumed = true
				}
				continue
			}
			if stageReplayPolicy(stage) == model.InteractionReplayOnceEffect {
				return result, newRuntimeExecutionError("browser_agent_once_effect_replay_denied", fmt.Errorf("completed once-effect stage %s no longer matches its checkpoint and cannot be replayed", stage.ID))
			}
		}
		if checkpoint, absorbed := absorbedStages[stage.ID]; absorbed {
			if err := appendEvent(stage, model.StageExecutionEventStageStarted, nil, nil); err != nil {
				return result, err
			}
			observation := checkpoint.Observed
			observationRef := &model.RuntimeObservation{Source: model.RuntimeObservationArtifact, URL: checkpoint.EntityRef, BusinessState: &observation}
			evidence := modelEvidenceRefs(checkpoint.EvidenceRefs)
			decision := model.HarnessDecision{Kind: model.HarnessDecisionSkip, Confidence: observation.Confidence, Reason: "a prior action already crossed the required successor state", EvidenceRefs: append([]string{}, checkpoint.EvidenceRefs...), AbsorbedSteps: []string{stage.ID}}
			if err := appendEventDetails(stage, model.StageExecutionEventBusinessStateObserved, observationRef, evidence, &decision, nil, nil); err != nil {
				return result, err
			}
			if err := appendEventDetails(stage, model.StageExecutionEventActionEffectReclassified, observationRef, evidence, &decision, checkpoint, nil); err != nil {
				return result, err
			}
			if err := appendEventDetails(stage, model.StageExecutionEventStepSatisfied, observationRef, evidence, &decision, checkpoint, nil); err != nil {
				return result, err
			}
			if err := appendEventDetails(stage, model.StageExecutionEventTransitionAbsorbed, observationRef, evidence, &decision, checkpoint, nil); err != nil {
				return result, err
			}
			if err := appendEvent(stage, model.StageExecutionEventStageCompleted, observationRef, evidence); err != nil {
				return result, err
			}
			continue
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
		if adaptiveBusinessHarnessEnabled(plan) {
			snapshot := adaptiveBusinessSnapshot(stage, &observed.Observation, nil, observed.TargetResolved, timeNowUTC())
			observed.Observation.BusinessState = &snapshot
			decision := model.DecideBusinessTransition(snapshot, businessTransitionForStage(stage))
			if err := appendEventDetails(stage, model.StageExecutionEventBusinessStateObserved, &observed.Observation, observed.EvidenceRefs, &decision, nil, nil); err != nil {
				return result, err
			}
			if observed.TargetResolved && stage.StageKind == model.BusinessStageKindModeSelection && runtimeAssertionPassed(observed.Observation, "configuration_satisfied") {
				decision.Kind, decision.Confidence, decision.Reason = model.HarnessDecisionSkip, 0.95, "the observed configuration already matches the requested business mode"
				if err := appendEventDetails(stage, model.StageExecutionEventStepSatisfied, &observed.Observation, observed.EvidenceRefs, &decision, nil, nil); err != nil {
					return result, err
				}
				if err := appendEvent(stage, model.StageExecutionEventStageCompleted, &observed.Observation, observed.EvidenceRefs); err != nil {
					return result, err
				}
				continue
			}
			if !observed.TargetResolved && stage.StageKind == model.BusinessStageKindModeSelection {
				decision.Kind, decision.Confidence, decision.Reason = model.HarnessDecisionSkip, 0.9, "optional configuration control is absent; no primary action is guessed"
				if err := appendEventDetails(stage, model.StageExecutionEventStepSatisfied, &observed.Observation, observed.EvidenceRefs, &decision, nil, nil); err != nil {
					return result, err
				}
				completedObservation := optionalCompletionObservation(observed.Observation, "optional configuration was not required")
				if err := appendEvent(stage, model.StageExecutionEventStageCompleted, &completedObservation, observed.EvidenceRefs); err != nil {
					return result, err
				}
				continue
			}
			if !observed.TargetResolved && adaptiveStageOptionalWhenTargetAbsent(stage) {
				decision.Kind, decision.Confidence, decision.Reason = model.HarnessDecisionSkip, 0.9, "optional continuation control is absent; observed execution state will be reconciled by following stages"
				if err := appendEventDetails(stage, model.StageExecutionEventStepSatisfied, &observed.Observation, observed.EvidenceRefs, &decision, nil, nil); err != nil {
					return result, err
				}
				completedObservation := optionalCompletionObservation(observed.Observation, "optional execution confirmation was not required")
				if err := appendEvent(stage, model.StageExecutionEventStageCompleted, &completedObservation, observed.EvidenceRefs); err != nil {
					return result, err
				}
				continue
			}
			if decision.Kind == model.HarnessDecisionDefer {
				_ = appendEventDetails(stage, model.StageExecutionEventConfidenceDeferred, &observed.Observation, observed.EvidenceRefs, &decision, nil, nil)
				return result, newRuntimeExecutionError("browser_agent_business_state_deferred", errors.New("business state confidence is too low for an action"))
			}
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
			if layer, _, capability := stageCapability(stage); capability && layer == "enhancement" {
				decision := model.HarnessDecision{Kind: model.HarnessDecisionSkip, Confidence: 0.9, Reason: "optional enhancement control was not observed; core execution may continue", EvidenceRefs: evidenceStrings(observed.EvidenceRefs)}
				if item, ok := capabilityResultForStage(stage, false, observed.EvidenceRefs, "enhancement target was not observed"); ok {
					capabilityResults = append(capabilityResults, item)
				}
				if err := appendEventDetails(stage, model.StageExecutionEventStepSatisfied, &observed.Observation, observed.EvidenceRefs, &decision, nil, nil); err != nil {
					return result, err
				}
				completedObservation := optionalCompletionObservation(observed.Observation, "optional enhancement was not observed")
				if err := appendEvent(stage, model.StageExecutionEventStageCompleted, &completedObservation, observed.EvidenceRefs); err != nil {
					return result, err
				}
				continue
			}
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
			// Preserve stable, redacted worker failure categories (notably the
			// credential-login broker diagnostics) while keeping generic executor
			// failures under the historical action_failed code.
			code := runtimeExecutionErrorCode(err)
			if code == "execution_failed" || code == "" {
				code = redactedBrowserAgentFailureCode(err)
			}
			return result, newRuntimeExecutionError(code, err)
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
			report, err := o.verifier.ValidateStageEvents(ctx, plan.validationContext(), stageEvents)
			if err != nil {
				return result, newRuntimeExecutionError("outcome_verification_failed", err)
			}
			if err := report.Validate(); err != nil {
				return result, newRuntimeExecutionError("outcome_verification_failed", err)
			}
			result.ValidationReports = append(result.ValidationReports, report)
			if normalized, ok := normalizeEnhancementDecision(report, stage, actionResult.EvidenceRefs); ok {
				report = normalized
				result.ValidationReports[len(result.ValidationReports)-1] = report
			}
			if normalized, ok := normalizeWarningOnlyRepairDecision(report, actionResult.Observation, actionResult.EvidenceRefs); ok {
				// The legacy Validation Agent maps warning-only feedback to
				// repair_allowed even when every browser assertion passed. With no
				// concrete failing check there is nothing safe to patch; retain the
				// warning report and continue instead of inventing a repair proposal.
				report = normalized
				result.ValidationReports[len(result.ValidationReports)-1] = report
			}
			if report.Decision == model.ValidationDecisionRepairAllowed {
				proposer, ok := o.verifier.(BrowserAgentStageRepairProposer)
				if !ok {
					_ = appendEvent(stage, model.StageExecutionEventStageFailed, actionResult.Observation, actionResult.EvidenceRefs)
					return result, newRuntimeExecutionError("browser_agent_repair_proposal_missing", fmt.Errorf("outcome verifier requested repair for stage %s without a proposal", stage.ID))
				}
				proposal, proposalErr := proposer.ProposeRuntimeRepair(ctx, plan.validationContext(), stage, stageEvents, report)
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
				revalidateOnly := proposal.RepairKind == "capture_timing" || stageHasExplicitOnceEffectPolicy(stage)
				if !revalidateOnly {
					if err := appendEvent(stage, model.StageExecutionEventActionStarted, actionResult.Observation, proposal.EvidenceRefs); err != nil {
						return result, err
					}
				}
				var repairActionResult BrowserAgentStageActionResult
				var repairErr error
				if revalidateOnly {
					revalidator, ok := executor.(BrowserAgentStageOutcomeRevalidator)
					if !ok {
						_ = appendEvent(stage, model.StageExecutionEventStageFailed, actionResult.Observation, proposal.EvidenceRefs)
						return result, newRuntimeExecutionError("browser_agent_revalidation_unavailable", errors.New("non-replayable outcome repair requires a non-action revalidator"))
					}
					repairActionResult, repairErr = revalidator.RevalidateStage(ctx, plan, patchedStage)
				} else {
					repairActionResult, repairErr = executor.ExecuteStage(ctx, plan, patchedStage)
				}
				if repairErr != nil || repairActionResult.Observation == nil || !runtimeObservationIsRealEvidence(repairActionResult.Observation.Source) || len(repairActionResult.EvidenceRefs) == 0 {
					_ = appendEvent(stage, model.StageExecutionEventStageFailed, actionResult.Observation, proposal.EvidenceRefs)
					if repairErr != nil {
						return result, newRuntimeExecutionError("browser_agent_repair_action_failed", repairErr)
					}
					return result, newRuntimeExecutionError("browser_agent_repair_action_failed", errors.New("repaired stage did not provide real completion evidence"))
				}
				if revalidateOnly {
					if err := appendEvent(stage, model.StageExecutionEventObservationCollected, repairActionResult.Observation, repairActionResult.EvidenceRefs); err != nil {
						return result, err
					}
				} else {
					if err := appendEvent(stage, model.StageExecutionEventActionCompleted, repairActionResult.Observation, repairActionResult.EvidenceRefs); err != nil {
						return result, err
					}
				}
				if err := appendEvent(stage, model.StageExecutionEventOutcomeObserved, repairActionResult.Observation, repairActionResult.EvidenceRefs); err != nil {
					return result, err
				}
				repairEvents := result.Events[repairEventStart:]
				repairReport, repairVerifyErr := o.verifier.ValidateStageEvents(ctx, plan.validationContext(), repairEvents)
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
		passedCapability := runtimeObservationPassed(actionResult.Observation)
		completionObservation := actionResult.Observation
		if layer, _, capability := stageCapability(stage); capability && layer == "enhancement" && !passedCapability {
			copyObservation := optionalCompletionObservation(*actionResult.Observation, "optional enhancement proof was incomplete")
			completionObservation = &copyObservation
		}
		if err := appendEvent(stage, model.StageExecutionEventStageCompleted, completionObservation, actionResult.EvidenceRefs); err != nil {
			return result, err
		}
		if item, ok := capabilityResultForStage(stage, passedCapability, actionResult.EvidenceRefs, ""); ok {
			if !item.Passed && item.Layer == "enhancement" {
				item.Warning = "enhancement proof was incomplete"
			}
			capabilityResults = append(capabilityResults, item)
		}
		if checkpoint, absorbedID := adaptiveTransitionAbsorption(plan, stage, &observed.Observation, actionResult.Observation, actionResult.EvidenceRefs); checkpoint != nil {
			absorbedStages[absorbedID] = checkpoint
			snapshot := checkpoint.Observed
			actionResult.Observation.BusinessState = &snapshot
			decision := model.HarnessDecision{Kind: model.HarnessDecisionAdvance, Confidence: snapshot.Confidence, Reason: "the observed effect crossed multiple planned business phases", EvidenceRefs: append([]string{}, checkpoint.EvidenceRefs...), AbsorbedSteps: append([]string{}, checkpoint.AbsorbedSteps...)}
			if err := appendEventDetails(stage, model.StageExecutionEventActionEffectReclassified, actionResult.Observation, actionResult.EvidenceRefs, &decision, checkpoint, nil); err != nil {
				return result, err
			}
			if err := appendEventDetails(stage, model.StageExecutionEventActionEffectCommitted, actionResult.Observation, actionResult.EvidenceRefs, &decision, checkpoint, nil); err != nil {
				return result, err
			}
			if err := appendEventDetails(stage, model.StageExecutionEventTransitionAbsorbed, actionResult.Observation, actionResult.EvidenceRefs, &decision, checkpoint, nil); err != nil {
				return result, err
			}
		}
		if plan.InterruptAfterFirstOnceEffect && !interruptionConsumed && stageReplayPolicy(stage) == model.InteractionReplayOnceEffect {
			interruptionConsumed = true
			return result, newRuntimeExecutionError(runtimeErrorWorkerRestartInjected, errors.New("authorized recovery experiment interrupted the worker after a committed once-effect checkpoint"))
		}
	}
	if len(capabilityResults) > 0 {
		score := model.ScoreCapabilities(capabilityResults)
		last := plan.Stages[len(plan.Stages)-1]
		if err := appendEventDetails(last, model.StageExecutionEventCapabilityScored, nil, nil, nil, nil, &score); err != nil {
			return result, err
		}
		// Capability failure is a completed, evidence-bearing business result.
		// Keep the Direct package materializable and let the experiment adapter
		// enforce the director gate from the persisted score. This preserves the
		// fact track and quality report without spending H3/Seedance budget.
	}
	return result, nil
}

func adaptiveStageOptionalWhenTargetAbsent(stage BrowserAgentRuntimeStage) bool {
	for _, interaction := range stage.Interactions {
		if strings.EqualFold(strings.TrimSpace(fmt.Sprint(interaction.Parameters["optional_when_target_absent"])), "true") {
			return true
		}
	}
	return false
}

func validResumedStageObservation(result BrowserAgentStageActionResult, previous *model.RuntimeObservation, replayPolicy model.InteractionReplayPolicy, allowConsumedCheckpointRoute bool) bool {
	if result.Observation == nil || !runtimeObservationIsRealEvidence(result.Observation.Source) || len(result.EvidenceRefs) == 0 {
		return false
	}
	if sameBrowserStateFingerprint(previous, result.Observation) {
		return true
	}
	if (replayPolicy == model.InteractionReplayOnceEffect || allowConsumedCheckpointRoute) && sameExactObservedURL(previous, result.Observation) {
		return true
	}
	if len(result.Observation.Assertions) == 0 {
		return false
	}
	for _, assertion := range result.Observation.Assertions {
		if !assertion.Passed {
			return false
		}
	}
	return true
}

func sameExactObservedURL(previous, current *model.RuntimeObservation) bool {
	if previous == nil || current == nil {
		return false
	}
	left, leftErr := url.Parse(strings.TrimSpace(previous.URL))
	right, rightErr := url.Parse(strings.TrimSpace(current.URL))
	return leftErr == nil && rightErr == nil && left.Scheme != "" && left.Scheme == right.Scheme && left.Host == right.Host && left.EscapedPath() != "" && left.EscapedPath() == right.EscapedPath()
}

func bindRuntimeStageToCheckpoint(stage BrowserAgentRuntimeStage, observation *model.RuntimeObservation) BrowserAgentRuntimeStage {
	if observation == nil || strings.TrimSpace(observation.URL) == "" {
		return stage
	}
	checkpointURL := strings.TrimSpace(observation.URL)
	oldAnchors := []string{stage.URL, stage.Route, stage.EntryRoute}
	stage.URL, stage.Route, stage.EntryRoute = checkpointURL, checkpointURL, checkpointURL
	stage.CheckpointRestore = true
	for index := range stage.Validations {
		validation := &stage.Validations[index]
		if validation.Kind != "url_matches" {
			continue
		}
		expected := strings.TrimSpace(firstNonEmptyString(validation.Target.URL, fmt.Sprint(validation.Expected)))
		for _, anchor := range oldAnchors {
			if expected != "" && strings.TrimSpace(anchor) != "" && routeTargetsEquivalent(expected, anchor, checkpointURL) {
				validation.Target.URL = checkpointURL
				validation.Expected = checkpointURL
				break
			}
		}
	}
	return stage
}

func routeTargetsEquivalent(left, right, base string) bool {
	leftURL, leftErr := url.Parse(strings.TrimSpace(left))
	rightURL, rightErr := url.Parse(strings.TrimSpace(right))
	baseURL, baseErr := url.Parse(strings.TrimSpace(base))
	if leftErr != nil || rightErr != nil || baseErr != nil {
		return false
	}
	if !leftURL.IsAbs() {
		leftURL = baseURL.ResolveReference(leftURL)
	}
	if !rightURL.IsAbs() {
		rightURL = baseURL.ResolveReference(rightURL)
	}
	return leftURL.Scheme == rightURL.Scheme && leftURL.Host == rightURL.Host && strings.TrimSuffix(leftURL.Path, "/") == strings.TrimSuffix(rightURL.Path, "/")
}

func sameBrowserStateFingerprint(previous, current *model.RuntimeObservation) bool {
	if previous == nil || current == nil || previous.StateFingerprint == nil || current.StateFingerprint == nil {
		return false
	}
	left, right := previous.StateFingerprint, current.StateFingerprint
	return left.Origin != "" && left.Origin == right.Origin && left.RouteTemplate != "" && left.RouteTemplate == right.RouteTemplate && left.DocumentDigest != "" && left.DocumentDigest == right.DocumentDigest && left.ARIADigest != "" && left.ARIADigest == right.ARIADigest
}

func stageReplayPolicy(stage BrowserAgentRuntimeStage) model.InteractionReplayPolicy {
	if stage.InteractionContract != nil && stage.InteractionContract.ReplayPolicy != "" {
		return stage.InteractionContract.ReplayPolicy
	}
	if len(stage.Interactions) == 0 {
		return model.InteractionReplayObserveOnly
	}
	switch stage.Interactions[0].Kind {
	case model.GraphActionWait, model.GraphActionInspect, model.GraphActionAssert:
		return model.InteractionReplayObserveOnly
	case model.GraphActionFill, model.GraphActionSelect:
		return model.InteractionReplayIdempotentWrite
	default:
		return model.InteractionReplayOnceEffect
	}
}

func stageHasExplicitOnceEffectPolicy(stage BrowserAgentRuntimeStage) bool {
	return stage.InteractionContract != nil && stage.InteractionContract.ReplayPolicy == model.InteractionReplayOnceEffect
}

func warningOnlyRepairDecisionCanContinue(report model.ValidationReport, observation *model.RuntimeObservation) bool {
	if report.Decision != model.ValidationDecisionRepairAllowed || observation == nil || len(report.Checks) == 0 || len(observation.Assertions) == 0 {
		return false
	}
	hasWarning := false
	for _, check := range report.Checks {
		if check.Passed {
			continue
		}
		if check.Severity != model.FindingSeverityWarning || (check.Code != string(model.ValidationResultTypeWarning) && check.Code != string(model.ValidationResultTypeUncertainty)) {
			return false
		}
		hasWarning = true
	}
	if !hasWarning {
		return false
	}
	for _, assertion := range observation.Assertions {
		if !assertion.Passed {
			return false
		}
	}
	return true
}

func normalizeWarningOnlyRepairDecision(report model.ValidationReport, observation *model.RuntimeObservation, evidence []model.EvidenceRef) (model.ValidationReport, bool) {
	if !warningOnlyRepairDecisionCanContinue(report, observation) || len(evidence) == 0 {
		return report, false
	}
	report.Decision = model.ValidationDecisionContinue
	if len(report.EvidenceRefs) == 0 {
		report.EvidenceRefs = append([]model.EvidenceRef{}, evidence...)
	}
	return report, true
}

func runtimeActionForStage(stage BrowserAgentRuntimeStage) *model.RuntimeAction {
	action := model.RuntimeAction{TargetSemanticID: stage.TargetContract.SemanticID}
	if stage.ManualSessionCheckpoint {
		action.Kind = "manual_session_checkpoint"
		return &action
	}
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
		RepairKind: "selector_alternative", Field: browserAgentSelectorRepairField(plan.RepairPolicy),
		Before: currentStageSelector(stage), After: selectorCandidateEncoding(candidate),
		Confidence: 1, EvidenceRefs: append([]model.EvidenceRef{}, observed.EvidenceRefs...), CreatedAt: timeNowUTC(),
	}
}

func browserAgentSelectorRepairField(policy model.BrowserAgentRepairPolicy) string {
	// Current App packages authorize selector repair on the action target. Keep
	// the older outline field only for already-issued compatible contracts that
	// explicitly list it. The Server never widens the package repair policy.
	const actionTargetSelector = "action.target.selector"
	if fieldIsEditable(policy.EditableFields, actionTargetSelector) && !fieldIsImmutable(policy.ImmutableFields, actionTargetSelector) {
		return actionTargetSelector
	}
	return "script_outline.stages[].components[].selector"
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
			if selector := strings.TrimSpace(component.Selector); selector != "" {
				return selector
			}
			if testID := strings.TrimSpace(component.TestID); testID != "" {
				return "testid:" + testID
			}
			if role := strings.TrimSpace(component.Role); role != "" {
				name := firstNonEmptyString(component.Name, component.Label, component.Text)
				if name != "" {
					return "role:" + role + ":name:" + strings.TrimSpace(name)
				}
			}
		}
	}
	for _, interaction := range stage.Interactions {
		if selector := strings.TrimSpace(interaction.Target.Selector); selector != "" {
			return selector
		}
		if testID := strings.TrimSpace(interaction.Target.TestID); testID != "" {
			return "testid:" + testID
		}
		if role := strings.TrimSpace(interaction.Target.Role); role != "" {
			name := firstNonEmptyString(interaction.Target.Label, interaction.Target.Text)
			if name != "" {
				return "role:" + role + ":name:" + strings.TrimSpace(name)
			}
		}
	}
	if semanticID := strings.TrimSpace(stage.TargetContract.SemanticID); semanticID != "" {
		return "semantic:" + semanticID
	}
	return ""
}

func selectorCandidateEncoding(candidate model.SelectorCandidate) string {
	return strings.TrimSpace(candidate.Kind) + ":" + strings.TrimSpace(candidate.Value)
}

func runtimeObservationIsRealEvidence(source model.RuntimeObservationSource) bool {
	return model.RuntimeObservationIsRealEvidence(source)
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
		SourcePlanHashSHA256:   bundle.Reproducibility.PlanHashSHA256,
		PolicyHashSHA256:       bundle.Reproducibility.BrowserAgentContractHashSHA256,
		WorkflowGraph:          pkg.WorkflowGraph,
		Plan:                   bundle.PlanJSON,
		StageApprovalPlan:      bundle.StageApprovalPlan,
		ScriptOutline:          bundle.ScriptOutline,
		BrowserAgentContract:   bundle.BrowserAgentContract,
		AllowedDomains:         append([]string{}, bundle.SecurityPolicy.AllowedDomains...),
		ForbiddenPages:         append([]string{}, bundle.SecurityPolicy.ForbiddenPages...),
		ExplorationScope:       bundle.ScriptOutline.AllowedExplorationScope,
		ForbiddenActions:       append([]string{}, bundle.ScriptOutline.ForbiddenActions...),
		RepairPolicy:           bundle.BrowserAgentContract.RepairPolicy,
		HarnessProfile:         packageMetadataString(pkg.Metadata, "harness_profile"),
		Stages:                 make([]BrowserAgentRuntimeStage, 0, len(approvedStages)),
	}
	if plan.HarnessProfile == "" && packageMetadataString(pkg.Metadata, "experiment_run_id") != "" {
		// Packages admitted by the earlier interactive experiment already carry a
		// generic experiment binding. Treat them as adaptive during reconciliation
		// so a known successor state can be recovered without another submission.
		plan.HarnessProfile = model.AdaptiveBusinessHarnessProfileV1
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
		approvedInteraction, outlineInteraction, planInteraction := approved.InteractionContract, outline.InteractionContract, planStep.InteractionContract
		if approvedInteraction != nil || outlineInteraction != nil || planInteraction != nil {
			if approvedInteraction == nil || outlineInteraction == nil || planInteraction == nil || approvedInteraction.ContractID != outlineInteraction.ContractID || approvedInteraction.ContractID != planInteraction.ContractID {
				return BrowserAgentRuntimePlan{}, newRuntimeExecutionError(runtimeErrorBrowserAgentContractViolation, fmt.Errorf("browser-agent interaction contract conflict for node_id %q", approved.NodeID))
			}
			if err := model.ValidateInteractionContract(*approvedInteraction); err != nil {
				return BrowserAgentRuntimePlan{}, newRuntimeExecutionError(runtimeErrorBrowserAgentContractViolation, fmt.Errorf("browser-agent interaction contract invalid for node_id %q: %w", approved.NodeID, err))
			}
		}
		capturePlan := mergeApprovedCapturePlan(approved.CapturePlan, outline.CapturePlan)
		plan.Stages = append(plan.Stages, BrowserAgentRuntimeStage{
			ID: approved.ID, Order: approved.Order, NodeID: approved.NodeID, StageKind: approved.StageKind, Objective: approved.Objective,
			BusinessIntent: approved.BusinessIntent, EntryRoute: firstNonEmptyString(outline.EntryRoute, approved.EntryRoute),
			Route: firstNonEmptyString(outline.Route, approved.TargetRoute), URL: firstNonEmptyString(outline.URL, approved.TargetURL),
			TargetRouteTemplate:              firstNonEmptyString(outline.TargetRouteTemplate, approved.TargetRouteTemplate),
			ExpectedRouteAfterAction:         firstNonEmptyString(outline.ExpectedRouteAfterAction, approved.ExpectedRouteAfterAction),
			RuntimeRouteVerificationRequired: outline.RuntimeRouteVerificationRequired || approved.RuntimeRouteVerificationRequired,
			TargetContract:                   *approvedTarget, Components: append([]model.BrowserAgentComponentTarget{}, outline.Components...),
			InteractionContract: approvedInteraction,
			Interactions:        append([]model.BrowserAgentInteraction{}, outline.Interactions...),
			WaitConditions:      append([]string{}, outline.WaitConditions...), CapturePoints: append([]string{}, outline.CapturePoints...),
			CapturePlan: capturePlan, SuccessState: approved.SuccessState, DurationMS: approved.DurationMS,
			Validations:                       append([]model.ValidationSpec{}, planStep.Validations...),
			EvidenceBoundSelectorAlternatives: evidenceBoundSelectorCandidates(outline.Components, outline.Interactions),
		})
	}
	if len(plan.Stages) != len(outlineByNode) {
		return BrowserAgentRuntimePlan{}, newRuntimeExecutionError(runtimeErrorBrowserAgentContractViolation, errors.New("script outline contains stages that were not approved"))
	}
	return plan, nil
}

func packageMetadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, _ := metadata[key].(string)
	return strings.TrimSpace(value)
}

func evidenceBoundSelectorCandidates(components []model.BrowserAgentComponentTarget, interactions []model.BrowserAgentInteraction) []model.SelectorCandidate {
	if len(interactions) == 0 || len(interactions[0].Target.EvidenceRefs) == 0 {
		return nil
	}
	targetEvidence := evidenceIDSet(interactions[0].Target.EvidenceRefs)
	if len(targetEvidence) == 0 {
		return nil
	}
	result := []model.SelectorCandidate{}
	seen := map[string]struct{}{}
	appendCandidate := func(candidate model.SelectorCandidate) {
		if !model.SelectorCandidateHasFormalProvenance(candidate) {
			return
		}
		encoded := selectorCandidateEncoding(candidate)
		if strings.TrimSpace(candidate.Value) == "" || encoded == ":" {
			return
		}
		if _, exists := seen[encoded]; exists {
			return
		}
		seen[encoded] = struct{}{}
		result = append(result, candidate)
	}
	for _, component := range components {
		sharedEvidence := sharedEvidenceRefs(component.EvidenceRefs, targetEvidence)
		if len(sharedEvidence) == 0 {
			continue
		}
		for _, candidate := range component.SelectorAlternatives {
			candidateEvidence := sharedEvidenceRefs(candidate.EvidenceRefs, targetEvidence)
			if len(candidateEvidence) == 0 {
				candidateEvidence = sharedEvidence
			}
			candidate.EvidenceRefs = candidateEvidence
			appendCandidate(candidate)
		}
	}
	return result
}

func evidenceIDSet(refs []model.EvidenceRef) map[string]struct{} {
	result := map[string]struct{}{}
	for _, ref := range refs {
		if id := strings.TrimSpace(ref.ID); id != "" {
			result[id] = struct{}{}
		}
	}
	return result
}

func sharedEvidenceRefs(refs []model.EvidenceRef, allowed map[string]struct{}) []model.EvidenceRef {
	result := []model.EvidenceRef{}
	seen := map[string]struct{}{}
	for _, ref := range refs {
		id := strings.TrimSpace(ref.ID)
		if id == "" {
			continue
		}
		if _, ok := allowed[id]; !ok {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, ref)
	}
	return result
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
		if !browserAgentOriginAllowed(target, plan.ExplorationScope.AllowedOrigins) {
			return deniedBrowserAgentPolicy("origin_not_allowed", "target is outside approved origins")
		}
		// A control-plane or explicitly forbidden page must retain its precise
		// rejection reason even when it also falls outside the approved routes.
		if browserAgentPathForbidden(target, plan.ForbiddenPages, plan.ExplorationScope.ForbiddenPathPrefixes) {
			return deniedBrowserAgentPolicy("forbidden_page", "target matches a forbidden page or control-plane prefix")
		}
		if !browserAgentRouteAllowed(target, plan.ExplorationScope.AllowedRoutes) {
			return deniedBrowserAgentPolicy("route_not_allowed", "target is outside approved routes")
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

func browserAgentOriginAllowed(value string, allowedOrigins []string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") || strings.HasPrefix(value, "#") {
		return true
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	for _, origin := range allowedOrigins {
		approved, err := url.Parse(strings.TrimSpace(origin))
		if err == nil && approved.Scheme == parsed.Scheme && strings.EqualFold(approved.Host, parsed.Host) {
			return true
		}
	}
	return false
}

func browserAgentRouteAllowed(value string, allowedRoutes []string) bool {
	if len(allowedRoutes) == 0 {
		return false
	}
	path := normalizeBrowserAgentRoute(value)
	for _, route := range allowedRoutes {
		approved := normalizeBrowserAgentRoute(route)
		if approved == "/" || browserAgentRouteTemplatePrefixMatches(path, approved) {
			return true
		}
	}
	return false
}

// browserAgentRouteTemplatePrefixMatches binds an App-approved route template
// to an observed runtime path without persisting
// the identifier. Static segments remain exact and the observed path may
// continue into an approved subresource (for example /project/42/logs).
func browserAgentRouteTemplatePrefixMatches(actualPath, approvedTemplate string) bool {
	actualParts := browserAgentRouteParts(actualPath)
	approvedParts := browserAgentRouteParts(approvedTemplate)
	if len(actualParts) < len(approvedParts) {
		return false
	}
	for index, approved := range approvedParts {
		if approved == "*" || browserAgentRouteTemplateSegment(approved) {
			continue
		}
		if actualParts[index] != approved {
			return false
		}
	}
	return true
}

func browserAgentRouteParts(value string) []string {
	normalized := normalizeBrowserAgentRoute(value)
	trimmed := strings.Trim(normalized, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func browserAgentRouteTemplateSegment(value string) bool {
	return value == "*" || strings.HasPrefix(value, ":") || (strings.HasPrefix(value, "{") && strings.HasSuffix(value, "}"))
}

func normalizeBrowserAgentRoute(value string) string {
	value = strings.TrimSpace(value)
	if parsed, err := url.Parse(value); err == nil {
		// Absolute origins (for example https://product.example) represent the
		// site root. Keep route matching semantics identical for absolute URLs
		// and path-only routes so an initial session navigation is allowed by a
		// root scope entry ("/").
		if parsed.IsAbs() && parsed.Hostname() != "" && (parsed.Scheme == "http" || parsed.Scheme == "https") {
			value = parsed.Path
			if value == "" {
				value = "/"
			}
		} else if parsed.Path != "" {
			value = parsed.Path
		}
	}
	path := "/" + strings.Trim(strings.ToLower(value), "/")
	if path == "" {
		return "/"
	}
	return path
}

func deniedBrowserAgentPolicy(code string, reason string) BrowserAgentPolicyDecision {
	return BrowserAgentPolicyDecision{Allowed: false, Code: code, Reason: reason}
}

func browserAgentActionTypeAllowed(action model.GraphActionType) bool {
	switch action {
	case model.GraphActionNavigate, model.GraphActionClick, model.GraphActionFill, model.GraphActionSelect,
		model.GraphActionUpload, model.GraphActionWait, model.GraphActionAssert, model.GraphActionInspect, model.GraphActionPress, model.GraphActionGesture:
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

// Worker errors are intentionally reduced to a fixed, non-sensitive category
// before they enter the App result package. This preserves useful login
// diagnostics without leaking Playwright/page text.
func redactedBrowserAgentFailureCode(err error) string {
	message := strings.ToLower(strings.TrimSpace(errString(err)))
	for _, code := range []string{
		"browser_agent_login_form_not_resolved",
		"browser_agent_login_continue_not_resolved",
		"browser_agent_login_password_not_resolved",
		"browser_agent_login_submit_not_resolved",
		"browser_agent_login_invalid_credentials",
		"browser_agent_login_submission_not_confirmed",
		"browser_agent_login_captcha_required",
	} {
		if strings.Contains(message, code) {
			return code
		}
	}
	return "browser_agent_action_failed"
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func runtimeAssertionPassed(observation model.RuntimeObservation, kind string) bool {
	for _, assertion := range observation.Assertions {
		if assertion.Kind == kind && assertion.Passed {
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
