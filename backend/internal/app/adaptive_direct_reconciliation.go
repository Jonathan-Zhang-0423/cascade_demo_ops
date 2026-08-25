package app

import (
	"context"
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
	if state.DesktopCloudRun == nil || state.DesktopCloudRun.ResultPackage == nil {
		return adaptiveDirectReconciliationBuild{}, errors.New("adaptive reconciliation requires a persisted failed result")
	}
	result := *state.DesktopCloudRun.ResultPackage
	if result.Status != model.RecordingResultStatusFailed || result.CloudJobID != strings.TrimSpace(sourceJobID) || result.FailureDiagnostic == nil {
		return adaptiveDirectReconciliationBuild{}, errors.New("adaptive reconciliation source result does not match the failed Direct job")
	}
	graph, eligible, err := terminalInteractionVerificationRepairGraph(state, result, time.Now().UTC())
	if err != nil || !eligible || graph == nil || !strings.HasPrefix(graph.ID, "graph_adaptive_successor_repair_") {
		if err == nil {
			err = errors.New("failed Direct job has no observed successor state")
		}
		return adaptiveDirectReconciliationBuild{}, err
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
