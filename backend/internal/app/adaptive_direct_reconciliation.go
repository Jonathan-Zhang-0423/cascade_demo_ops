package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"cascade-demoops/backend/internal/experiment"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

type adaptiveDirectReconciliationBuild struct {
	State               *orchestrator.CascadeState
	Build               ClientExecutionPackageBuild
	SourceResult        model.RecordingResultPackage
	PendingContinuation bool
}

type adaptiveObservedSuccessorEvidence struct {
	URL    string
	Events []model.StageExecutionEvent
}

// prepareAdaptiveDirectReconciliation compiles an observe/verify-only package
// from a failed Direct job whose diagnostic proves that the browser is already
// on a concrete successor entity. The experiment's existing launch
// authorization approves this continuation later during upload; this method
// never performs an external action itself.
func (s *Service) prepareAdaptiveDirectReconciliation(ctx context.Context, projectID, sourceJobID string, interactionPlan experiment.InteractionPlan, observationPlan experiment.ObservationPlan) (adaptiveDirectReconciliationBuild, error) {
	state, err := s.states.Load(ctx, strings.TrimSpace(projectID))
	if err != nil {
		return adaptiveDirectReconciliationBuild{}, err
	}
	if state.DesktopCloudRun == nil {
		return adaptiveDirectReconciliationBuild{}, errors.New("adaptive reconciliation requires a persisted Direct run")
	}
	var result model.RecordingResultPackage
	hasPersistedResult := state.DesktopCloudRun.ResultPackage != nil
	if hasPersistedResult {
		result = *state.DesktopCloudRun.ResultPackage
	}
	repairState := state
	var sourcePackage *model.ClientExecutionPackage
	loadedPackage, packageErr := s.GetDirectSourcePackage(ctx, projectID, sourceJobID)
	if packageErr == nil {
		sourcePackage = &loadedPackage
		if !hasPersistedResult || result.CloudJobID != strings.TrimSpace(sourceJobID) {
			result, err = s.getDirectHistoricalResult(ctx, projectID, sourceJobID)
			if err != nil {
				result, err = s.adaptiveInterruptedResultFromStageLog(ctx, projectID, sourceJobID, state, loadedPackage)
				if err != nil {
					return adaptiveDirectReconciliationBuild{}, fmt.Errorf("load adaptive reconciliation source result: %w", err)
				}
			}
		}
		if result.SourcePackageID != loadedPackage.PackageID {
			return adaptiveDirectReconciliationBuild{}, errors.New("adaptive reconciliation source package does not match the failed result")
		}
		hydratedGraph, hydrateErr := hydrateAdaptiveSourceGraph(loadedPackage)
		if hydrateErr != nil {
			return adaptiveDirectReconciliationBuild{}, hydrateErr
		}
		repairCopy := *state
		repairCopy.WorkflowGraph = hydratedGraph
		repairCopy.ExecutableScriptBundle = loadedPackage.ExecutableScriptBundle
		repairState = &repairCopy
	} else if adaptiveReconciliationNeedsAuthentication(state) || !hasPersistedResult || result.CloudJobID != strings.TrimSpace(sourceJobID) {
		return adaptiveDirectReconciliationBuild{}, fmt.Errorf("load adaptive reconciliation source package: %w", packageErr)
	}
	if result.Status != model.RecordingResultStatusFailed || result.CloudJobID != strings.TrimSpace(sourceJobID) || result.FailureDiagnostic == nil {
		return adaptiveDirectReconciliationBuild{}, errors.New("adaptive reconciliation source result does not match the failed Direct job")
	}
	observed := s.adaptiveObservedSuccessorEvidence(ctx, projectID, state, result)
	if observed.URL != "" {
		result.FailureDiagnostic.CurrentURL = observed.URL
	}
	if result.FailureDiagnostic.Error.Code == "browser_agent_once_effect_replay_denied" && adaptiveContinuationEffectObserved(repairState.WorkflowGraph, observed.Events) {
		if node := adaptivePostContinuationObservationNode(repairState.WorkflowGraph); node != nil {
			result.FailureDiagnostic.FailedNodeID = node.ID
			if sourcePackage != nil && sourcePackage.ExecutableScriptBundle != nil && sourcePackage.ExecutableScriptBundle.PlanJSON != nil {
				for _, step := range sourcePackage.ExecutableScriptBundle.PlanJSON.Steps {
					if step.NodeID == node.ID {
						result.FailureDiagnostic.FailedStepOrder = step.Order
						break
					}
				}
			}
		}
	}
	graph, eligible, err := terminalInteractionVerificationRepairGraph(repairState, result, time.Now().UTC())
	if err != nil || !eligible || !adaptiveReconciliationGraphSupported(graph) {
		if err == nil {
			err = errors.New("failed Direct job has no observed successor state")
		}
		return adaptiveDirectReconciliationBuild{}, err
	}
	if err := normalizeAdaptiveReconciliationResume(graph, result.FailureDiagnostic.CurrentURL, directDiagnosticRefs(*result.FailureDiagnostic)); err != nil {
		return adaptiveDirectReconciliationBuild{}, err
	}
	pendingContinuation := false
	// Prefer the earliest approved continuation from the original package. A
	// repair package may retain only its later follow-up; choosing that node
	// would correctly avoid duplicate input but could never answer the original
	// conversational confirmation.
	if sourcePackage != nil && sourcePackage.ExecutableScriptBundle != nil && sourcePackage.ExecutableScriptBundle.RepairLineage != nil && !adaptiveContinuationEffectObserved(repairState.WorkflowGraph, observed.Events) {
		parentJobID := strings.TrimSpace(sourcePackage.ExecutableScriptBundle.RepairLineage.SourceCloudJobID)
		if parentJobID != "" && parentJobID != strings.TrimSpace(sourceJobID) {
			parentPackage, parentPackageErr := s.GetDirectSourcePackage(ctx, projectID, parentJobID)
			parentResult, parentResultErr := s.getDirectHistoricalResult(ctx, projectID, parentJobID)
			if parentPackageErr == nil && parentResultErr != nil {
				parentResult, parentResultErr = s.adaptiveInterruptedResultFromStageLog(ctx, projectID, parentJobID, state, parentPackage)
			}
			if parentPackageErr == nil && parentResultErr == nil {
				parentGraph, parentGraphErr := hydrateAdaptiveSourceGraph(parentPackage)
				if parentGraphErr == nil {
					parentObserved := s.adaptiveObservedSuccessorEvidence(ctx, projectID, state, parentResult)
					pendingContinuation, err = insertPendingAdaptiveContinuation(graph, parentGraph, parentObserved.Events, result.FailureDiagnostic.CurrentURL, result.FailureDiagnostic.FailedNodeID)
					if err != nil {
						return adaptiveDirectReconciliationBuild{}, err
					}
				}
			}
		}
	}
	// A continuation that reached action_completed/effect_committed is a
	// once-effect. A later observation failure must never turn it back into a
	// pending action: resume from the observed entity and inspect only.
	if !pendingContinuation && !adaptiveContinuationEffectObserved(repairState.WorkflowGraph, observed.Events) {
		pendingContinuation, err = insertPendingAdaptiveContinuation(graph, repairState.WorkflowGraph, observed.Events, result.FailureDiagnostic.CurrentURL, result.FailureDiagnostic.FailedNodeID)
		if err != nil {
			return adaptiveDirectReconciliationBuild{}, err
		}
	}
	if sourcePackage != nil {
		if _, err := prependReusableSessionSetup(graph, *sourcePackage); err != nil {
			return adaptiveDirectReconciliationBuild{}, err
		}
	}
	if err := applyAdaptiveInteractionContracts(graph, interactionPlan, observationPlan, result.FailureDiagnostic.CurrentURL, directDiagnosticRefs(*result.FailureDiagnostic)); err != nil {
		return adaptiveDirectReconciliationBuild{}, err
	}
	next, err := cloneCascadeStateForRevision(state)
	if err != nil {
		return adaptiveDirectReconciliationBuild{}, err
	}
	if err := syncAdaptiveBusinessStagePlan(next, graph); err != nil {
		return adaptiveDirectReconciliationBuild{}, err
	}
	next, err = s.flow.RepackageReviewedGraph(ctx, next, graph)
	if err != nil {
		return adaptiveDirectReconciliationBuild{}, fmt.Errorf("repackage adaptive successor graph: %w", err)
	}
	if next.ExecutableScriptBundle == nil {
		return adaptiveDirectReconciliationBuild{}, errors.New("adaptive reconciliation executable bundle is missing")
	}
	repairAttempt := 1
	if result.RepairRequest != nil && result.RepairRequest.RepairAttempt >= repairAttempt {
		repairAttempt = result.RepairRequest.RepairAttempt + 1
	}
	baseBundleID, baseBundleHash := "", ""
	if repairState.ExecutableScriptBundle != nil {
		baseBundleID = repairState.ExecutableScriptBundle.ID
		baseBundleHash = repairState.ExecutableScriptBundle.Reproducibility.BundleHashSHA256
	}
	next.ExecutableScriptBundle.RepairLineage = &model.ScriptRepairLineage{
		BaseBundleID: baseBundleID, BaseBundleHashSHA256: baseBundleHash,
		SourceResultID: result.ResultID, SourceCloudJobID: result.CloudJobID, RepairAttempt: repairAttempt,
		ChangeSummary:  "Restore the approved session and observe the already-created successor entity without replaying creation or submission.",
		DiagnosticRefs: directDiagnosticRefs(*result.FailureDiagnostic), CreatedAt: time.Now().UTC(),
	}
	next.ExecutionPackageGeneration = state.ExecutionPackageGeneration
	next.Approved = false
	next.CurrentNode = orchestrator.NodeHumanApprove
	next.Status = orchestrator.FlowStatusAwaitingHuman
	previousRun := state.DesktopCloudRun
	next.DesktopCloudRun = &orchestrator.DesktopCloudRunState{
		SchemaVersion: desktopCloudRunSchemaVersion, Transport: directTransportStateName,
		OrgID: firstNonEmptyString(previousRun.OrgID, defaultDesktopOrgID), Status: "not_uploaded", Stage: "adaptive_reconciliation_ready",
		Message: "已从观察到的后继实体生成只观察续接包。", LastRepairSourceID: result.ResultID,
		RepairHistory: append([]orchestrator.DesktopDirectRepairAuditState(nil), previousRun.RepairHistory...),
	}
	if err := s.states.Save(ctx, next); err != nil {
		return adaptiveDirectReconciliationBuild{}, err
	}
	s.invalidateApprovedBuildsForProject(projectID)
	build, err := s.BuildClientExecutionPackage(ctx, projectID, firstNonEmptyString(previousRun.OrgID, defaultDesktopOrgID))
	if err != nil {
		return adaptiveDirectReconciliationBuild{}, fmt.Errorf("build adaptive successor package: %w", err)
	}
	return adaptiveDirectReconciliationBuild{State: next, Build: build, SourceResult: result, PendingContinuation: pendingContinuation}, nil
}

func adaptiveContinuationEffectObserved(source *model.DemoWorkflowGraph, events []model.StageExecutionEvent) bool {
	continuations := map[string]bool{}
	if source != nil {
		for _, node := range source.Nodes {
			if node == nil || node.ActionSpec == nil {
				continue
			}
			if recipe, _ := node.ActionSpec.Parameters["action_recipe"].(string); recipe == "continue_execution" {
				continuations[node.ID] = true
			}
		}
	}
	for _, event := range events {
		if !continuations[event.NodeID] {
			continue
		}
		if event.EventType == model.StageExecutionEventActionStarted || event.EventType == model.StageExecutionEventActionCompleted || event.EventType == model.StageExecutionEventActionEffectCommitted {
			return true
		}
	}
	return false
}

func adaptivePostContinuationObservationNode(source *model.DemoWorkflowGraph) *model.GraphNode {
	seenContinuation := false
	if source == nil {
		return nil
	}
	for _, node := range source.Nodes {
		if node == nil || node.ActionSpec == nil {
			continue
		}
		if recipe, _ := node.ActionSpec.Parameters["action_recipe"].(string); recipe == "continue_execution" {
			seenContinuation = true
			continue
		}
		if !seenContinuation {
			continue
		}
		if node.InteractionContract != nil && node.InteractionContract.ReplayPolicy == model.InteractionReplayObserveOnly && (node.ActionSpec.Type == model.GraphActionInspect || node.ActionSpec.Type == model.GraphActionWait) {
			return node
		}
	}
	return nil
}

// adaptiveInterruptedResultFromStageLog promotes the bounded Worker recovery
// log into reconciliation evidence when execution reached a real successor
// but the terminal result could not be uploaded before the credential grant
// expired. This value is never presented as a completed server result; it only
// drives an observe/verify continuation with a fresh Direct package.
func (s *Service) adaptiveInterruptedResultFromStageLog(ctx context.Context, projectID, sourceJobID string, state *orchestrator.CascadeState, source model.ClientExecutionPackage) (model.RecordingResultPackage, error) {
	var status model.DirectJobStatus
	err := s.directProjectRead(ctx, projectID, "/v1/direct/jobs/"+url.PathEscape(sourceJobID), "job_status", &status)
	if err != nil {
		return model.RecordingResultPackage{}, err
	}
	if status.JobID != sourceJobID {
		return model.RecordingResultPackage{}, errors.New("interrupted Direct status job binding mismatch")
	}
	if status.Status != "awaiting_credentials" {
		return model.RecordingResultPackage{}, errors.New("interrupted Direct job is not awaiting credential recovery")
	}
	var eventArtifact *model.DirectArtifact
	for index := range status.Artifacts {
		if status.Artifacts[index].Kind == "browser_agent_stage_event_log" {
			eventArtifact = &status.Artifacts[index]
			break
		}
	}
	if eventArtifact == nil {
		return model.RecordingResultPackage{}, errors.New("interrupted Direct job has no recovered stage-event log")
	}
	now := time.Now().UTC()
	eventRef := model.ArtifactRef{
		ID: eventArtifact.ArtifactID, Kind: eventArtifact.Kind, URI: directArtifactURI(sourceJobID, eventArtifact.ArtifactID),
		MimeType: eventArtifact.MimeType, SHA256: eventArtifact.SHA256, SizeBytes: eventArtifact.SizeBytes, Sensitive: true,
	}
	result := model.RecordingResultPackage{
		ResultID: "interrupted_recovery_" + safePathSegment(sourceJobID), SourcePackageID: source.PackageID,
		CloudJobID: sourceJobID, SchemaVersion: model.RecordingResultPackageSchemaVersion,
		Status: model.RecordingResultStatusFailed, ExecutionRuntime: model.ExecutableScriptRuntimeBrowserAgentOutlineV1,
		StageEventLogRef: &eventRef, CreatedAt: now,
		FailureDiagnostic: &model.ScriptFailureDiagnostic{
			ID: "diag_interrupted_" + safePathSegment(sourceJobID), SchemaVersion: model.ScriptFailureDiagnosticSchemaVersion,
			SourcePackageID: source.PackageID, CloudJobID: sourceJobID, Attempt: 1,
			Error:           model.AgentError{Code: "worker_interrupted_after_checkpoint", Message: "Worker finalization ended after browser evidence was persisted; reconcile the observed successor without replaying submission.", Retryable: true},
			RedactionReport: model.DiagnosticRedactionReport{Applied: true, PolicyRef: source.PackageID + ".redactions", FullHTMLIncluded: false}, CapturedAt: now,
		},
	}
	observed := s.adaptiveObservedSuccessorEvidence(ctx, projectID, state, result)
	if len(observed.Events) == 0 || strings.TrimSpace(observed.URL) == "" {
		return model.RecordingResultPackage{}, errors.New("recovered stage-event log has no observed successor evidence")
	}
	failed := selectAdaptiveInterruptedStage(observed.Events)
	if strings.TrimSpace(failed.NodeID) == "" {
		return model.RecordingResultPackage{}, errors.New("recovered stage-event log has no failed or interrupted stage")
	}
	result.FailureDiagnostic.FailedNodeID = failed.NodeID
	result.FailureDiagnostic.CurrentURL = observed.URL
	if source.ExecutableScriptBundle != nil && source.ExecutableScriptBundle.PlanJSON != nil {
		for _, step := range source.ExecutableScriptBundle.PlanJSON.Steps {
			if step.NodeID == failed.NodeID {
				result.FailureDiagnostic.FailedStepOrder = step.Order
				break
			}
		}
	}
	return result, nil
}

func selectAdaptiveInterruptedStage(events []model.StageExecutionEvent) model.StageExecutionEvent {
	var failed model.StageExecutionEvent
	started := map[string]model.StageExecutionEvent{}
	finished := map[string]bool{}
	for _, event := range events {
		if event.EventType == model.StageExecutionEventStageFailed && event.Sequence >= failed.Sequence {
			failed = event
		}
		switch event.EventType {
		case model.StageExecutionEventActionStarted:
			started[event.NodeID] = event
		case model.StageExecutionEventActionCompleted, model.StageExecutionEventStageCompleted, model.StageExecutionEventStageFailed:
			finished[event.NodeID] = true
		}
	}
	if strings.TrimSpace(failed.NodeID) == "" {
		for nodeID, event := range started {
			if !finished[nodeID] && event.Sequence >= failed.Sequence {
				failed = event
			}
		}
	}
	return failed
}

func (s *Service) adaptiveObservedSuccessorEvidence(ctx context.Context, projectID string, state *orchestrator.CascadeState, result model.RecordingResultPackage) adaptiveObservedSuccessorEvidence {
	current := strings.TrimSpace(result.FailureDiagnostic.CurrentURL)
	if state == nil || state.ProjectContext == nil || state.DesktopCloudRun == nil || result.StageEventLogRef == nil {
		return adaptiveObservedSuccessorEvidence{URL: current}
	}
	var descriptor *model.DirectArtifact
	for index := range state.DesktopCloudRun.DirectArtifacts {
		if state.DesktopCloudRun.DirectArtifacts[index].ArtifactID == result.StageEventLogRef.ID {
			descriptor = &state.DesktopCloudRun.DirectArtifacts[index]
			break
		}
	}
	if descriptor == nil {
		var status model.DirectJobStatus
		if err := s.directProjectRead(ctx, projectID, "/v1/direct/jobs/"+url.PathEscape(result.CloudJobID), "job_status", &status); err == nil && status.JobID == result.CloudJobID {
			for index := range status.Artifacts {
				if status.Artifacts[index].ArtifactID == result.StageEventLogRef.ID {
					descriptor = &status.Artifacts[index]
					break
				}
			}
		}
	}
	if descriptor == nil {
		return adaptiveObservedSuccessorEvidence{URL: current}
	}
	download, err := s.DownloadDirectArtifact(ctx, projectID, DirectArtifactDownloadRequest{JobID: result.CloudJobID, Artifact: *descriptor})
	if err != nil || !download.ChecksumVerified {
		return adaptiveObservedSuccessorEvidence{URL: current}
	}
	file, err := os.Open(download.LocalPath)
	if err != nil {
		return adaptiveObservedSuccessorEvidence{URL: current}
	}
	defer file.Close()
	events := make([]model.StageExecutionEvent, 0, 64)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), experiment.MaxEventBodyBytes)
	for scanner.Scan() {
		var event model.StageExecutionEvent
		if json.Unmarshal(scanner.Bytes(), &event) == nil && event.Observation != nil {
			events = append(events, event)
		}
	}
	if scanner.Err() != nil {
		return adaptiveObservedSuccessorEvidence{URL: current}
	}
	return adaptiveObservedSuccessorEvidence{URL: selectObservedSuccessorURL(state.ProjectContext, current, events), Events: events}
}

func insertPendingAdaptiveContinuation(repair, source *model.DemoWorkflowGraph, events []model.StageExecutionEvent, observedURL, failedNodeID string) (bool, error) {
	if repair == nil || source == nil || len(repair.Nodes) == 0 {
		return false, nil
	}
	actionStarted, skipped := map[string]bool{}, map[string]bool{}
	for _, event := range events {
		switch event.EventType {
		case model.StageExecutionEventActionStarted, model.StageExecutionEventActionEffectCommitted:
			actionStarted[event.NodeID] = true
		case model.StageExecutionEventActionCompleted:
			actionStarted[event.NodeID] = true
		case model.StageExecutionEventStepSatisfied:
			if event.HarnessDecision != nil && event.HarnessDecision.Kind == model.HarnessDecisionSkip {
				skipped[event.NodeID] = true
			}
		}
	}
	var candidate *model.GraphNode
	for _, node := range source.Nodes {
		if node == nil || node.ActionSpec == nil || actionStarted[node.ID] || !skipped[node.ID] {
			continue
		}
		if value, _ := node.ActionSpec.Parameters["action_recipe"].(string); value != "continue_execution" {
			continue
		}
		candidate = node
		break
	}
	if candidate == nil {
		return false, nil
	}
	encoded, err := json.Marshal(candidate)
	if err != nil {
		return false, err
	}
	var pending model.GraphNode
	if err := json.Unmarshal(encoded, &pending); err != nil {
		return false, err
	}
	pending.Type = model.GraphNodeTypeAction
	pending.PageRef = observedURL
	if pending.ActionSpec.Target.URL != "" {
		pending.ActionSpec.Target.URL = observedURL
	}
	for index := range pending.Validations {
		if pending.Validations[index].Kind == "url_matches" {
			pending.Validations[index].Target.URL = observedURL
			pending.Validations[index].Expected = observedURL
		}
	}
	if pending.InteractionContract != nil {
		pending.InteractionContract.ReplayPolicy = model.InteractionReplayOnceEffect
		pending.InteractionContract.ActionTarget = pending.ActionSpec.Target
	}
	if pending.Metadata == nil {
		pending.Metadata = map[string]any{}
	}
	pending.Metadata["adaptive_pending_continuation"] = true
	pending.Metadata["runtime_adaptive"] = true
	pending.Metadata["non_destructive"] = true
	pending.Metadata["replay_policy"] = string(model.InteractionReplayOnceEffect)
	repair.Nodes = append([]*model.GraphNode{repair.Nodes[0], &pending}, repair.Nodes[1:]...)
	repair.Edges = make([]*model.GraphEdge, 0, len(repair.Nodes)-1)
	for index := 1; index < len(repair.Nodes); index++ {
		repair.Edges = append(repair.Edges, &model.GraphEdge{ID: fmt.Sprintf("edge_adaptive_reconcile_%02d", index), FromNode: repair.Nodes[index-1].ID, ToNode: repair.Nodes[index].ID, Condition: "validated", Priority: index})
	}
	return true, nil
}

func selectObservedSuccessorURL(project *model.ProjectContext, current string, events []model.StageExecutionEvent) string {
	bestURL := strings.TrimSpace(current)
	bestScore, bestSequence := observedSuccessorURLScore(project, bestURL), int64(-1)
	for _, event := range events {
		if event.Observation == nil {
			continue
		}
		candidate := strings.TrimSpace(event.Observation.URL)
		score := observedSuccessorURLScore(project, candidate)
		if score < 0 || score < bestScore || (score == bestScore && event.Sequence <= bestSequence) {
			continue
		}
		bestURL, bestScore, bestSequence = candidate, score, event.Sequence
	}
	return bestURL
}

func observedSuccessorURLScore(project *model.ProjectContext, candidate string) int {
	approved, err := approvedTerminalProjectURL(project, candidate)
	if err != nil {
		return -1
	}
	target, targetErr := url.Parse(approved)
	base, baseErr := url.Parse(strings.TrimSpace(project.ProductURL))
	if targetErr != nil || baseErr != nil {
		return -1
	}
	segments := len(strings.FieldsFunc(strings.Trim(target.Path, "/"), func(r rune) bool { return r == '/' }))
	score := segments * 10
	if !sameURLWithoutQuery(target, base) {
		score += 100
	}
	return score
}

func sameURLWithoutQuery(left, right *url.URL) bool {
	return left != nil && right != nil && strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host) && strings.TrimSuffix(left.Path, "/") == strings.TrimSuffix(right.Path, "/")
}

func adaptiveReconciliationGraphSupported(graph *model.DemoWorkflowGraph) bool {
	if graph == nil {
		return false
	}
	return strings.HasPrefix(graph.ID, "graph_adaptive_successor_repair_") || strings.HasPrefix(graph.ID, "graph_terminal_interaction_repair_")
}

// A failure after the successor route was reached may be reported by an
// interaction proof rather than the submit stage itself. Normalize the first
// same-run navigation into the same observe-only resume contract so the
// continuation cannot accidentally fall back to the old workflow entry point.
func normalizeAdaptiveReconciliationResume(graph *model.DemoWorkflowGraph, observedURL string, evidence []model.EvidenceRef) error {
	if graph == nil || strings.TrimSpace(observedURL) == "" {
		return errors.New("adaptive reconciliation resume requires an observed entity URL")
	}
	for _, node := range graph.Nodes {
		if node == nil || node.ActionSpec == nil || node.ActionSpec.Type != model.GraphActionNavigate {
			continue
		}
		node.PageRef = observedURL
		node.ActionSpec.Target.URL = observedURL
		node.InteractionContract = &model.InteractionContract{
			SchemaVersion: model.InteractionContractSchemaVersion,
			ContractID:    "interaction_adaptive_observed_resume_" + node.ID,
			SemanticGoal:  "进入已观察到的业务实体，只继续观察和验证。",
			ActionKind:    model.GraphActionNavigate, ReplayPolicy: model.InteractionReplayObserveOnly,
			TargetSemanticID: "observed_successor_entity", ActionTarget: node.ActionSpec.Target,
			ExpectedTransitions: []model.InteractionPredicate{{
				ID: "observe_adaptive_successor_route", Kind: "url_matches", Target: model.ActionTarget{URL: observedURL},
				Expected: observedURL, Required: true, TimeoutMS: 30_000, EvidenceRefs: append([]model.EvidenceRef(nil), evidence...),
			}},
			EvidenceRefs: append([]model.EvidenceRef(nil), evidence...), NonDestructive: true,
		}
		if node.Metadata == nil {
			node.Metadata = map[string]any{}
		}
		node.Metadata["adaptive_successor_resume"] = true
		node.Metadata["terminal_repair_resume"] = false
		node.Metadata["runtime_adaptive"] = true
		node.Metadata["verification_status"] = "runtime_adaptive"
		node.Metadata["non_destructive"] = true
		node.Metadata["replay_policy"] = string(model.InteractionReplayObserveOnly)
		node.Metadata["expected_route_after_action"] = observedURL
		return nil
	}
	return errors.New("adaptive reconciliation graph has no observed-route navigation")
}

// hydrateAdaptiveSourceGraph restores executable fields that are compacted out
// of the uploaded graph from the plan in the same approved package. It never
// changes the action or target; it only reconstructs the package's own binding
// so recovery can classify the failed effect correctly.
func hydrateAdaptiveSourceGraph(source model.ClientExecutionPackage) (*model.DemoWorkflowGraph, error) {
	if source.WorkflowGraph == nil || source.ExecutableScriptBundle == nil || source.ExecutableScriptBundle.PlanJSON == nil {
		return nil, errors.New("adaptive reconciliation source package is incomplete")
	}
	encoded, err := json.Marshal(source.WorkflowGraph)
	if err != nil {
		return nil, err
	}
	var graph model.DemoWorkflowGraph
	if err := json.Unmarshal(encoded, &graph); err != nil {
		return nil, err
	}
	nodes := make(map[string]*model.GraphNode, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if node != nil {
			nodes[node.ID] = node
		}
	}
	for _, step := range source.ExecutableScriptBundle.PlanJSON.Steps {
		node := nodes[step.NodeID]
		if node == nil {
			continue
		}
		action := step.Action
		node.Action = string(action.Type)
		node.ActionSpec = &model.GraphAction{
			Type: action.Type, Target: action.Target, Value: action.Value, InputRef: action.InputRef, SecretRef: action.SecretRef,
			Parameters: action.Parameters, TimeoutMS: action.TimeoutMS, WaitUntil: action.WaitUntil, Preconditions: append([]model.StateAssertion(nil), action.Preconditions...),
		}
		node.PageRef = firstNonEmptyString(step.PageTarget.URL, step.PageTarget.PageRef, action.Target.URL, node.PageRef)
		node.ExpectedOutcome = step.ExpectedOutcome
		node.Validations = append([]model.ValidationSpec(nil), step.Validations...)
		node.EvidenceRefs = append([]model.EvidenceRef(nil), step.EvidenceRefs...)
		node.DurationHintMS = firstPositiveInt(step.Timing.DurationMS, node.DurationHintMS)
		if step.InteractionContract != nil {
			contract := *step.InteractionContract
			node.InteractionContract = &contract
		}
		if node.Metadata == nil {
			node.Metadata = map[string]any{}
		}
		node.Metadata["business_stage_id"] = step.NodeID
		node.Metadata["business_stage_kind"] = string(step.StageKind)
		node.Metadata["business_route_state"] = string(step.RouteState)
	}
	return &graph, nil
}

func adaptiveReconciliationNeedsAuthentication(state *orchestrator.CascadeState) bool {
	if state == nil || state.ProjectContext == nil || state.ProjectContext.DemoAccount == nil {
		return false
	}
	account := state.ProjectContext.DemoAccount
	return strings.TrimSpace(account.UsernameSecretRef) != "" && strings.TrimSpace(account.PasswordSecretRef) != ""
}

// prependReusableSessionSetup reconstructs only the approved authentication
// stage from the source package's executable step. Creation, form input and
// submission stages are deliberately never copied into a reconciliation run.
func prependReusableSessionSetup(graph *model.DemoWorkflowGraph, source model.ClientExecutionPackage) (bool, error) {
	if graph == nil || source.ExecutableScriptBundle == nil || source.ExecutableScriptBundle.PlanJSON == nil {
		return false, nil
	}
	for _, existing := range graph.Nodes {
		if isSessionSetupGraphNode(existing) {
			return false, nil
		}
	}
	var sourceNode *model.GraphNode
	for _, step := range source.ExecutableScriptBundle.PlanJSON.Steps {
		if step.StageKind != model.BusinessStageKindSessionSetup || strings.TrimSpace(step.Action.SecretRef) == "" {
			continue
		}
		grantBound := false
		for _, grant := range source.CredentialGrants {
			if strings.TrimSpace(grant.CloudSecretRef) == strings.TrimSpace(step.Action.SecretRef) {
				grantBound = true
				break
			}
		}
		if !grantBound {
			return false, errors.New("adaptive reconciliation session setup is not bound to an approved credential grant")
		}
		for _, candidate := range source.WorkflowGraph.Nodes {
			if candidate != nil && candidate.ID == step.NodeID {
				encoded, marshalErr := json.Marshal(candidate)
				if marshalErr != nil {
					return false, marshalErr
				}
				if unmarshalErr := json.Unmarshal(encoded, &sourceNode); unmarshalErr != nil {
					return false, unmarshalErr
				}
				break
			}
		}
		if sourceNode == nil {
			sourceNode = &model.GraphNode{ID: step.NodeID}
		}
		action := step.Action
		sourceNode.Type = model.GraphNodeTypeAction
		sourceNode.Title = firstNonEmptyString(step.Title, "恢复已批准的业务会话")
		sourceNode.Goal = firstNonEmptyString(step.BusinessValue, "恢复原任务已批准的登录会话。")
		sourceNode.Action = string(action.Type)
		// The source workflow graph is a compact compatibility projection and may
		// retain a stale primary selector even when the approved executable step
		// intentionally uses selector-free runtime login discovery. Reuse the exact
		// executable target below and never promote that projection into authority.
		sourceNode.Selector = ""
		sourceNode.PageRef = firstNonEmptyString(step.PageTarget.URL, step.PageTarget.PageRef, action.Target.URL, source.ProjectContextSummary.ProductURL)
		sourceNode.ActionSpec = &model.GraphAction{
			Type: action.Type, Target: action.Target, Value: action.Value, InputRef: action.InputRef, SecretRef: action.SecretRef,
			Parameters: action.Parameters, TimeoutMS: action.TimeoutMS, WaitUntil: action.WaitUntil, Preconditions: append([]model.StateAssertion(nil), action.Preconditions...),
		}
		sourceNode.ExpectedOutcome = step.ExpectedOutcome
		sourceNode.Validations = append([]model.ValidationSpec(nil), step.Validations...)
		sourceNode.EvidenceRefs = append([]model.EvidenceRef(nil), step.EvidenceRefs...)
		sourceNode.DurationHintMS = firstPositiveInt(step.Timing.DurationMS, 6_000)
		capture, narrative := step.Capture, step.Narrative
		sourceNode.Capture, sourceNode.Narrative = &capture, &narrative
		if step.InteractionContract != nil {
			contract := *step.InteractionContract
			sourceNode.InteractionContract = &contract
		}
		if sourceNode.Metadata == nil {
			sourceNode.Metadata = map[string]any{}
		}
		sourceNode.Metadata["business_stage_id"] = step.NodeID
		sourceNode.Metadata["business_stage_kind"] = string(model.BusinessStageKindSessionSetup)
		sourceNode.Metadata["business_route_state"] = string(step.RouteState)
		sourceNode.Metadata["business_stage_entry_route"] = sourceNode.PageRef
		for _, validation := range sourceNode.Validations {
			if validation.Required && validation.Kind == "url_matches" && strings.TrimSpace(validation.Target.URL) != "" {
				sourceNode.Metadata["expected_route_after_action"] = validation.Target.URL
				break
			}
		}
		sourceNode.Metadata["replay_policy"] = string(model.InteractionReplayObserveOnly)
		sourceNode.Metadata["runtime_adaptive"] = true
		sourceNode.Metadata["verification_status"] = "runtime_adaptive"
		sourceNode.Metadata["non_destructive"] = true
		sourceNode.Metadata["adaptive_session_restore"] = true
		graph.Nodes = append([]*model.GraphNode{sourceNode}, graph.Nodes...)
		return true, nil
	}
	return false, nil
}

func isSessionSetupGraphNode(node *model.GraphNode) bool {
	if node == nil || node.Metadata == nil {
		return false
	}
	kind, _ := node.Metadata["business_stage_kind"].(string)
	return model.BusinessStageKind(kind) == model.BusinessStageKindSessionSetup
}

func syncAdaptiveBusinessStagePlan(state *orchestrator.CascadeState, graph *model.DemoWorkflowGraph) error {
	if state == nil || state.ProjectIntelligence == nil || state.ProjectIntelligence.BusinessStagePlan == nil || graph == nil {
		return errors.New("adaptive continuation requires a business stage plan")
	}
	plan := state.ProjectIntelligence.BusinessStagePlan
	stages := make([]model.BusinessStage, 0, len(graph.Nodes))
	for index, node := range graph.Nodes {
		if node == nil || node.ActionSpec == nil {
			continue
		}
		if node.Metadata == nil {
			node.Metadata = map[string]any{}
		}
		stageID, _ := node.Metadata["business_stage_id"].(string)
		if strings.TrimSpace(stageID) == "" {
			stageID = node.ID
			node.Metadata["business_stage_id"] = stageID
		}
		kind := model.BusinessStageKindFinalObserve
		if value, ok := node.Metadata["business_stage_kind"].(string); ok && strings.TrimSpace(value) != "" {
			kind = model.BusinessStageKind(value)
		}
		routeState := model.BusinessRouteStateBuildRunning
		if value, ok := node.Metadata["business_route_state"].(string); ok && strings.TrimSpace(value) != "" {
			routeState = model.BusinessRouteState(value)
		}
		expectedRoute := node.PageRef
		if kind == model.BusinessStageKindSessionSetup {
			for _, validation := range node.Validations {
				if validation.Required && validation.Kind == "url_matches" && strings.TrimSpace(validation.Target.URL) != "" {
					expectedRoute = validation.Target.URL
					break
				}
			}
		}
		stage := model.BusinessStage{
			ID: stageID, Order: index + 1, Kind: kind, Title: node.Title, Objective: node.Goal,
			UserIntent: node.Goal, RouteState: routeState, EntryRoute: node.PageRef, ExpectedRouteAfterAction: expectedRoute,
			DurationMS: firstPositiveInt(node.DurationHintMS, 1_000), EvidenceRefs: append([]model.EvidenceRef(nil), node.EvidenceRefs...), Confidence: 1,
			Action: model.BusinessActionSemantics{Type: string(node.ActionSpec.Type), Label: node.Title, SuccessState: node.ExpectedOutcome, NonDestructive: true},
		}
		if node.InteractionContract != nil {
			copyContract := *node.InteractionContract
			stage.InteractionContract = &copyContract
		}
		stages = append(stages, stage)
	}
	if len(stages) < 2 {
		return errors.New("adaptive continuation business stage plan is empty")
	}
	plan.Stages = stages
	plan.CoreBusinessStageCount = len(stages)
	return nil
}

func firstPositiveInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func applyAdaptiveInteractionContracts(graph *model.DemoWorkflowGraph, plan experiment.InteractionPlan, observationPlan experiment.ObservationPlan, observedURL string, runtimeEvidence []model.EvidenceRef) error {
	if graph == nil || strings.TrimSpace(observedURL) == "" {
		return errors.New("adaptive interaction continuation requires a graph and observed entity URL")
	}
	contracts, err := compileExperimentInteractionContracts(plan, observationPlan)
	if err != nil {
		return err
	}
	existing := map[string]*model.GraphNode{}
	var template *model.GraphNode
	base := make([]*model.GraphNode, 0, len(graph.Nodes)+len(contracts))
	for _, node := range graph.Nodes {
		if node == nil {
			continue
		}
		if isSessionSetupGraphNode(node) || isAdaptiveSuccessorResumeGraphNode(node) || isAdaptivePendingContinuationGraphNode(node) {
			node.Type = model.GraphNodeTypeAction
			base = append(base, node)
			continue
		}
		if node.InteractionContract != nil {
			existing[node.InteractionContract.ContractID] = node
			if template == nil {
				template = node
			}
			continue
		}
		// The source failure suffix can contain legacy progress/final-observe
		// placeholders without an Interaction Contract. They are not independent
		// runtime evidence and must not survive into the continuation: the frozen
		// experiment plan below rebuilds every required observation and proof.
	}
	for index := range contracts {
		contract := contracts[index]
		node := existing[contract.ContractID]
		if node == nil {
			node = &model.GraphNode{RetryPolicy: 1, IsScreenshot: true, DurationHintMS: 6_000}
			if template != nil {
				node.Capture = template.Capture
				node.Narrative = template.Narrative
				node.FailurePolicy = template.FailurePolicy
			}
		}
		node.ID = "business_stage_contract_" + contract.ContractID
		node.Type = model.GraphNodeTypeAction
		node.Title = "验证交互证据：" + contract.SemanticGoal
		node.Goal = contract.SemanticGoal
		node.Action = string(contract.ActionKind)
		node.PageRef = observedURL
		node.Selector = ""
		node.InputData = ""
		node.ExpectedOutcome = "结构化交互契约的必需结果变化已通过独立证据验证。"
		node.EvidenceRefs = append(append([]model.EvidenceRef(nil), contract.EvidenceRefs...), runtimeEvidence...)
		target := contract.ActionTarget
		if target.URL == "" {
			target.URL = observedURL
		}
		node.ActionSpec = &model.GraphAction{Type: contract.ActionKind, Target: target, Parameters: contract.Parameters, TimeoutMS: 12_000, WaitUntil: "domcontentloaded"}
		node.StateBefore = nil
		node.StateAfter = nil
		node.Validations = make([]model.ValidationSpec, 0, len(contract.ExpectedTransitions))
		for _, predicate := range contract.ExpectedTransitions {
			validationTarget := predicate.Target
			if validationTarget.URL == "" {
				validationTarget.URL = observedURL
			}
			node.Validations = append(node.Validations, model.ValidationSpec{
				ID: predicate.ID, Kind: predicate.Kind, Target: validationTarget, Expected: predicate.Expected,
				Required: predicate.Required, TimeoutMS: predicate.TimeoutMS, Severity: "blocking", EvidenceRefs: append([]model.EvidenceRef(nil), predicate.EvidenceRefs...),
			})
		}
		contractCopy := contract
		node.InteractionContract = &contractCopy
		if node.Metadata == nil {
			node.Metadata = map[string]any{}
		}
		node.Metadata["adaptive_successor_verification"] = true
		node.Metadata["runtime_adaptive"] = true
		node.Metadata["non_destructive"] = true
		node.Metadata["verification_status"] = "runtime_adaptive"
		node.Metadata["business_stage_kind"] = string(model.BusinessStageKindFinalObserve)
		node.Metadata["business_route_state"] = string(model.BusinessRouteStateBuildRunning)
		node.Metadata["expected_route_after_action"] = observedURL
		node.Metadata["replay_policy"] = string(contract.ReplayPolicy)
		base = append(base, node)
	}
	if len(base) < 2 {
		return errors.New("adaptive continuation has no verification suffix")
	}
	for index := range base {
		base[index].Type = model.GraphNodeTypeAction
	}
	base[0].Type = model.GraphNodeTypeStart
	base[len(base)-1].Type = model.GraphNodeTypeEnd
	graph.Nodes = base
	graph.Edges = make([]*model.GraphEdge, 0, len(base)-1)
	for index := 1; index < len(base); index++ {
		graph.Edges = append(graph.Edges, &model.GraphEdge{ID: fmt.Sprintf("edge_adaptive_contract_%02d", index), FromNode: base[index-1].ID, ToNode: base[index].ID, Condition: "validated", Priority: index})
	}
	return nil
}

func isAdaptiveSuccessorResumeGraphNode(node *model.GraphNode) bool {
	if node == nil || node.ActionSpec == nil || node.ActionSpec.Type != model.GraphActionNavigate || node.Metadata == nil {
		return false
	}
	resume, _ := node.Metadata["adaptive_successor_resume"].(bool)
	return resume
}

func isAdaptivePendingContinuationGraphNode(node *model.GraphNode) bool {
	if node == nil || node.ActionSpec == nil || node.Metadata == nil {
		return false
	}
	pending, _ := node.Metadata["adaptive_pending_continuation"].(bool)
	recipe, _ := node.ActionSpec.Parameters["action_recipe"].(string)
	return pending && node.ActionSpec.Type == model.GraphActionClick && recipe == "continue_execution"
}
