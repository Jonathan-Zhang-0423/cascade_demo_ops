package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"cascade-demoops/backend/internal/experiment"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

type adaptiveDirectReconciliationBuild struct {
	State        *orchestrator.CascadeState
	Build        ClientExecutionPackageBuild
	SourceResult model.RecordingResultPackage
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
				return adaptiveDirectReconciliationBuild{}, fmt.Errorf("load adaptive reconciliation source result: %w", err)
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
	return adaptiveDirectReconciliationBuild{State: next, Build: build, SourceResult: result}, nil
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
			ContractID: "interaction_adaptive_observed_resume_" + node.ID,
			SemanticGoal: "进入已观察到的业务实体，只继续观察和验证。",
			ActionKind: model.GraphActionNavigate, ReplayPolicy: model.InteractionReplayObserveOnly,
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
		stage := model.BusinessStage{
			ID: stageID, Order: index + 1, Kind: kind, Title: node.Title, Objective: node.Goal,
			UserIntent: node.Goal, RouteState: routeState, EntryRoute: node.PageRef, ExpectedRouteAfterAction: node.PageRef,
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
		if isSessionSetupGraphNode(node) || isAdaptiveSuccessorResumeGraphNode(node) {
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
		node.Type = model.GraphNodeTypeAction
		base = append(base, node)
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
