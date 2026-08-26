package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/experiment"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
	"cascade-demoops/backend/internal/store"
)

func TestExperimentExecutionAdapterDeclaresAllReplayAndCaptureCapabilities(t *testing.T) {
	adapter := newAppExperimentExecutionAdapter(&Service{})
	manifest, err := adapter.DescribeCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ModuleID == "" || len(manifest.SupportedReplay) != 3 || !manifest.SupportsSegmentedCapture {
		t.Fatalf("experiment adapter manifest is incomplete: %+v", manifest)
	}
}

func TestExperimentDirectBindingsIncludeCheckpointAndExternalTaskOnce(t *testing.T) {
	run := experiment.Run{
		Legs: []experiment.RunLeg{
			{Checkpoint: &experiment.Checkpoint{
				ResultEntryRef: "direct:project-one:job-one",
				OnceEffects: []experiment.OnceEffectRecord{
					{EffectID: "submit", ExternalTaskRef: "direct:project-one:job-one"},
					{EffectID: "repair", ExternalTaskRef: "direct:project-one:job-two"},
				},
			}},
		},
	}
	bindings := experimentDirectBindings(run)
	if len(bindings) != 2 || bindings[0].projectID != "project-one" || bindings[0].jobID != "job-one" || bindings[1].jobID != "job-two" {
		t.Fatalf("unexpected Direct bindings: %+v", bindings)
	}
}

func TestAdaptiveExperimentPackageCompilesWithoutCascadeFlow(t *testing.T) {
	loaded, err := experiment.LoadDefinition("../../../experiments", "2048-v2")
	if err != nil {
		t.Fatal(err)
	}
	contracts, err := compileExperimentInteractionContracts(loaded.InteractionPlan, loaded.ObservationPlan)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{states: store.NewMemoryStateStore()}
	request := experiment.LegExecutionRequest{
		RunID: "experiment-direct-compile", LegID: "experiment-direct-compile:main", Kind: "main",
		ProjectName: "runtime generated project", BuildPrompt: loaded.Definition.ShortGoal,
		TargetURL: "https://product.example/app", CredentialRef: "credential://demo/test",
		ProductSpec: loaded.ProductSpec, ObservationPlan: loaded.ObservationPlan,
		InteractionPlan: loaded.InteractionPlan, WorkflowTemplateID: loaded.Definition.WorkflowTemplateID,
		HarnessProfile: experiment.HarnessProfileAdaptiveBusinessV1,
	}
	prepared, err := service.prepareAdaptiveExperimentRun(context.Background(), request, contracts)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.State == nil || prepared.Build == nil || prepared.Build.Package.WorkflowGraph == nil || prepared.Build.Package.ExecutableScriptBundle == nil {
		t.Fatalf("adaptive direct compilation produced an incomplete package: %+v", prepared)
	}
	if prepared.State.CurrentNode != "ScriptPackage" {
		t.Fatalf("adaptive package entered an unexpected legacy flow node: %s", prepared.State.CurrentNode)
	}
	if prepared.State.RequirementBrief != nil || prepared.State.UnderstandingReport != nil || prepared.State.ProductMap != nil {
		t.Fatal("adaptive direct compilation unexpectedly materialized legacy understanding stages")
	}
	if prepared.State.ProjectContext.ProductDescription != loaded.Definition.ShortGoal {
		t.Fatalf("target build prompt changed during compilation: %q", prepared.State.ProjectContext.ProductDescription)
	}
	if got := len(prepared.Build.Package.ExecutableScriptBundle.PlanJSON.Steps); got < len(loaded.InteractionPlan.Steps) {
		t.Fatalf("compiled steps=%d interaction contracts=%d", got, len(loaded.InteractionPlan.Steps))
	}
	foundContinuations := map[string]bool{}
	modeConfigured := false
	for _, stage := range prepared.Build.Package.ExecutableScriptBundle.StageApprovalPlan.Stages {
		if stage.NodeID == "business_stage_select_build_mode" {
			modeConfigured = stage.Interaction.Parameters["action_recipe"] == "configure_boolean" && stage.Interaction.Parameters["desired_checked"] == "false" && stage.TargetContract != nil && containsExactString(stage.TargetContract.AllowedNames, "计划") && containsExactString(stage.TargetContract.AllowedNames, "Plan")
			continue
		}
		if stage.NodeID != "business_stage_continue_prepared_execution" && stage.NodeID != "business_stage_continue_prepared_execution_followup" {
			continue
		}
		expectedWait := "300000"
		if stage.NodeID == "business_stage_continue_prepared_execution" {
			expectedWait = strconv.Itoa(loaded.ObservationPlan.DeferAfterMS)
		}
		confirmationBound := stage.NodeID != "business_stage_continue_prepared_execution" || stage.Interaction.Parameters["continuation_confirmation_value"] == "确认，继续执行。"
		foundContinuations[stage.NodeID] = stage.Interaction.Parameters["action_recipe"] == "continue_execution" && stage.Interaction.Parameters["optional_when_target_absent"] == "true" && stage.Interaction.Parameters["capture_result_surface_baseline"] == "true" && stage.Interaction.Parameters["target_wait_timeout_ms"] == expectedWait && confirmationBound
	}
	if !modeConfigured {
		t.Fatal("adaptive async package did not compile direct execution as an idempotent boolean configuration")
	}
	if !foundContinuations["business_stage_continue_prepared_execution"] || !foundContinuations["business_stage_continue_prepared_execution_followup"] {
		t.Fatalf("adaptive async package did not include the bounded runtime execution continuation chain: %+v", foundContinuations)
	}
}

func TestAdaptiveInitialGraphKeepsBuildAndProductProofInOneSession(t *testing.T) {
	loaded, err := experiment.LoadDefinition("../../../experiments", "2048-v3")
	if err != nil {
		t.Fatal(err)
	}
	graph := &model.DemoWorkflowGraph{
		ID: "graph_initial_session", SchemaVersion: model.DemoWorkflowGraphSchemaVersion,
		Nodes: []*model.GraphNode{
			{ID: "session_setup", Type: model.GraphNodeTypeStart},
			{ID: "submit_and_continue", Type: model.GraphNodeTypeEnd, ActionSpec: &model.GraphAction{Type: model.GraphActionClick}},
		},
		Edges: []*model.GraphEdge{{ID: "edge_initial", FromNode: "session_setup", ToNode: "submit_and_continue", Condition: "validated", Priority: 1}},
	}
	if err := appendAdaptiveInteractionContractsToInitialGraph(graph, loaded.InteractionPlan, loaded.ObservationPlan); err != nil {
		t.Fatal(err)
	}
	if got, want := len(graph.Nodes), 2+len(loaded.InteractionPlan.Steps); got != want {
		t.Fatalf("single-session graph nodes=%d want=%d", got, want)
	}
	if graph.Nodes[1].Type != model.GraphNodeTypeAction || graph.Nodes[len(graph.Nodes)-1].Type != model.GraphNodeTypeEnd {
		t.Fatalf("build terminal was not extended into product proof: %+v", graph.Nodes)
	}
	for _, node := range graph.Nodes[2:] {
		if node.InteractionContract == nil || node.PageRef != "" || node.ActionSpec == nil || node.ActionSpec.Target.URL != "" {
			t.Fatalf("initial product proof must stay on the runtime-created entity: %+v", node)
		}
	}
}

func TestExperimentDirectBindingRoundTripAndRuntimeMetadataStayInProcessOnly(t *testing.T) {
	projectID, jobID, ok := parseDirectResultEntry("direct:project-one:job-one")
	if !ok || projectID != "project-one" || jobID != "job-one" {
		t.Fatalf("direct result binding did not round-trip: %q %q %v", projectID, jobID, ok)
	}
	if _, _, ok := parseDirectResultEntry("direct:missing"); ok {
		t.Fatal("incomplete direct result binding was accepted")
	}
	payload, err := json.Marshal(DirectTransportUploadRequest{
		IdempotencyKey: "experiment-idempotency", RecoveryInjectionPhase: "once_effect_committed",
		RuntimeMetadata: map[string]any{"experiment_run_id": "run-one"},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	if strings.Contains(text, "recovery_injection") || strings.Contains(text, "experiment_run_id") {
		t.Fatalf("internal recovery controls escaped through the public transport request: %s", text)
	}
}

func TestCompileExperimentInteractionContractsPreservesExecutableProofSemantics(t *testing.T) {
	loaded, err := experiment.LoadDefinition("../../../experiments", "2048-v2")
	if err != nil {
		t.Fatal(err)
	}
	contracts, err := compileExperimentInteractionContracts(loaded.InteractionPlan, loaded.ObservationPlan)
	if err != nil {
		t.Fatal(err)
	}
	if len(contracts) != len(loaded.InteractionPlan.Steps) {
		t.Fatalf("contracts=%d steps=%d", len(contracts), len(loaded.InteractionPlan.Steps))
	}
	wantActions := []model.GraphActionType{model.GraphActionInspect, model.GraphActionPress, model.GraphActionInspect, model.GraphActionInspect, model.GraphActionClick, model.GraphActionGesture, model.GraphActionClick}
	for index, contract := range contracts {
		if contract.ActionKind != wantActions[index] {
			t.Fatalf("contract %d action=%s want=%s", index, contract.ActionKind, wantActions[index])
		}
		if contract.Parameters["evidence_step_id"] != loaded.InteractionPlan.Steps[index].StepID {
			t.Fatalf("contract %d lost evidence step binding", index)
		}
		if err := model.ValidateInteractionContract(contract); err != nil {
			t.Fatalf("contract %d invalid: %v", index, err)
		}
	}
	if got := contracts[1].ExpectedTransitions; !interactionPredicatesContain(got, "distinct_actions_observed") || !interactionPredicatesContain(got, "frame_surface_changed") {
		t.Fatalf("direction proof predicates=%+v", got)
	}
	if got := contracts[0].ExpectedTransitions[0].TimeoutMS; got != int(loaded.ObservationPlan.DeferAfterMS) {
		t.Fatalf("surface readiness timeout=%d want observation defer budget=%d", got, loaded.ObservationPlan.DeferAfterMS)
	}
	if contracts[0].Parameters["require_visual_terminal_confirmation"] != true {
		t.Fatalf("surface readiness must bind deterministic and repeated visual terminal channels: %+v", contracts[0].Parameters)
	}
	if contracts[0].Parameters["refresh_after_ms"] != float64(loaded.ObservationPlan.WarnAfterMS) && contracts[0].Parameters["refresh_after_ms"] != loaded.ObservationPlan.WarnAfterMS {
		t.Fatalf("surface readiness must bind the requested refresh point: %+v", contracts[0].Parameters)
	}
	if got := contracts[2].ExpectedTransitions; !interactionPredicatesContain(got, "numeric_increased") || !interactionPredicatesContain(got, "state_changed") {
		t.Fatalf("numeric proof predicates=%+v", got)
	}
	if got := contracts[5].ExpectedTransitions; !interactionPredicatesContain(got, "input_modality_used") {
		t.Fatalf("touch proof predicates=%+v", got)
	}
}

func TestExperimentProductEvidenceSummaryStaysInternalAndDetailed(t *testing.T) {
	loaded, err := experiment.LoadDefinition("../../../experiments", "2048-v2")
	if err != nil {
		t.Fatal(err)
	}
	summary := experimentProductEvidenceSummary(loaded.ProductSpec)
	if !strings.Contains(summary, loaded.ProductSpec.Objective) || !strings.Contains(summary, loaded.ProductSpec.Requirements[0].Statement) || !strings.Contains(summary, loaded.ProductSpec.ObservableAcceptance[0].Statement) {
		t.Fatalf("internal visual evidence summary lost required details: %s", summary)
	}
	if len([]rune(summary)) > 4096 {
		t.Fatalf("internal visual evidence summary is unbounded: %d", len([]rune(summary)))
	}
}

func TestRecordLiveBrowserVisualObservationsCountsCallsAndRequiresConfidentTerminal(t *testing.T) {
	dir := t.TempDir()
	write := func(name, decision string, confidence float64, providerCalls int) CloudDeliverableDownloadResult {
		path := filepath.Join(dir, name+".json")
		payload, _ := json.Marshal(map[string]any{"schema_version": browserVisualObservationSchemaVersion, "decision": decision, "confidence": confidence, "provider_calls_used": providerCalls})
		if err := os.WriteFile(path, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		return CloudDeliverableDownloadResult{ArtifactID: name, Kind: "browser_visual_observation", LocalPath: path}
	}
	downloads := []CloudDeliverableDownloadResult{
		write("unknown", "unknown", 0, 2), write("weak", "succeeded", .7, 1), write("terminal", "succeeded", .93, 1),
		{ArtifactID: "screenshot", Kind: "browser_visual_poll_screenshot", LocalPath: filepath.Join(dir, "ignored.png")},
	}
	emitted := 0
	calls, terminal, err := recordLiveBrowserVisualObservations(downloads, 4, func(update experiment.LegExecutionUpdate) error {
		if update.Kind != "visual_observation" || len(update.EvidenceRefs) != 1 {
			t.Fatalf("unexpected visual update: %+v", update)
		}
		emitted++
		return nil
	})
	if err != nil || calls != 4 || emitted != 4 || !terminal {
		t.Fatalf("live observations calls=%d emitted=%d terminal=%t err=%v", calls, emitted, terminal, err)
	}
	emitted = 0
	calls, terminal, err = recordLiveBrowserVisualObservations(downloads, 0, func(update experiment.LegExecutionUpdate) error {
		emitted++
		return nil
	})
	if err != nil || calls != 4 || emitted != 0 || !terminal {
		t.Fatalf("recovered live observations were not reused idempotently: calls=%d emitted=%d terminal=%t err=%v", calls, emitted, terminal, err)
	}
}

func TestReadAdaptiveCapabilityScoreUsesPersistedStageEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stage-events.jsonl")
	event := model.StageExecutionEvent{
		SchemaVersion: model.StageExecutionEventSchemaVersion, EventType: model.StageExecutionEventCapabilityScored,
		CapabilityScore: &model.CapabilityScore{SchemaVersion: "demoops.capability_score.v1", CoreScore: 70, EnhancementScore: 18, TotalScore: 88, CorePassed: true, EligibleForFilm: true},
	}
	payload, _ := json.Marshal(event)
	if err := os.WriteFile(path, append(payload, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	score, evidence, err := readAdaptiveCapabilityScore([]CloudDeliverableDownloadResult{{ArtifactID: "events", Kind: "browser_agent_stage_event_log", LocalPath: path}})
	if err != nil || score == nil || score.TotalScore != 88 || evidence != "events" {
		t.Fatalf("capability score was not recovered from stage events: score=%+v evidence=%q err=%v", score, evidence, err)
	}
}

func TestAdaptiveObservationTimeoutDefersWithoutDeclaringBusinessFailure(t *testing.T) {
	request := experiment.LegExecutionRequest{
		HarnessProfile: experiment.HarnessProfileAdaptiveBusinessV1,
		InteractionPlan: experiment.InteractionPlan{Steps: []experiment.InteractionStep{
			{StepID: "surface_ready", ReplayPolicy: experiment.ReplayObserveOnly},
			{StepID: "directional_moves", ReplayPolicy: experiment.ReplayIdempotentWrite},
		}},
	}
	result := model.RecordingResultPackage{FailureDiagnostic: &model.ScriptFailureDiagnostic{
		FailedNodeID: "business_stage_contract_experiment_interaction_surface_ready",
		Error:        model.AgentError{Code: "outcome_verification_failed"},
	}}
	if !adaptiveObservationFailureShouldDefer(request, result) {
		t.Fatal("an inconclusive observe-only timeout was promoted to an explicit business failure")
	}
	result.FailureDiagnostic.FailedNodeID = "business_stage_contract_experiment_interaction_directional_moves"
	if adaptiveObservationFailureShouldDefer(request, result) {
		t.Fatal("an action proof failure was incorrectly treated as passive observation deferral")
	}
	result.FailureDiagnostic.FailedNodeID = "business_stage_contract_experiment_interaction_surface_ready"
	result.FailureDiagnostic.Error.Code = "explicit_visible_terminal_error"
	if adaptiveObservationFailureShouldDefer(request, result) {
		t.Fatal("an explicit terminal failure was incorrectly deferred")
	}
}

func TestAdaptiveV2ActionProofFailureRoutesToSameEntityProductRepair(t *testing.T) {
	request := experiment.LegExecutionRequest{
		HarnessProfile: experiment.HarnessProfileAdaptiveBusinessV2,
		InteractionPlan: experiment.InteractionPlan{Steps: []experiment.InteractionStep{
			{StepID: "surface_ready", ReplayPolicy: experiment.ReplayObserveOnly, CapabilityLayer: "core", CapabilityScore: 20},
			{StepID: "directional_moves", ReplayPolicy: experiment.ReplayIdempotentWrite, CapabilityLayer: "core", CapabilityScore: 25},
			{StepID: "touch", ReplayPolicy: experiment.ReplayIdempotentWrite, CapabilityLayer: "core", CapabilityScore: 10},
		}},
	}
	result := model.RecordingResultPackage{
		ResultID: "result-failed-proof",
		FailureDiagnostic: &model.ScriptFailureDiagnostic{
			FailedNodeID: "business_stage_contract_experiment_interaction_directional_moves",
			Error:        model.AgentError{Code: "outcome_verification_failed"},
		},
		StepResults: []model.StepResult{{NodeID: "business_stage_contract_experiment_interaction_surface_ready", Status: "passed"}},
	}
	score, repairable := adaptiveFailedCapabilityScore(request, result)
	if !repairable || score.CorePassed || score.EligibleForFilm || score.CoreScore != 20 {
		t.Fatalf("action proof failure did not become a bounded failed capability score: repairable=%v score=%+v", repairable, score)
	}
	if got := strings.Join(score.Missing, ","); got != "directional_moves,touch" {
		t.Fatalf("missing required capabilities = %q", got)
	}
	result.FailureDiagnostic.FailedNodeID = "business_stage_contract_experiment_interaction_surface_ready"
	if _, repairable := adaptiveFailedCapabilityScore(request, result); repairable {
		t.Fatal("passive build uncertainty was incorrectly turned into a product edit")
	}
}

func TestAdaptiveTransportWaitDoesNotPreemptWorkerProgressWindow(t *testing.T) {
	request := experiment.LegExecutionRequest{
		HarnessProfile:  experiment.HarnessProfileAdaptiveBusinessV1,
		ObservationPlan: experiment.ObservationPlan{DeferAfterMS: 30 * 60 * 1000},
	}
	if got := directObservationTransportTimeout(request); got != 91*time.Minute {
		t.Fatalf("adaptive transport timeout = %s, want 91m", got)
	}
	request.HarnessProfile = "legacy"
	if got := directObservationTransportTimeout(request); got != 30*time.Minute+30*time.Second {
		t.Fatalf("legacy transport timeout changed: %s", got)
	}
}

func TestAdaptiveV2ObservationDeadlineWaitsForBoundExternalTask(t *testing.T) {
	v2 := directObservationDeadlineError(experiment.LegExecutionRequest{
		HarnessProfile: experiment.HarnessProfileAdaptiveBusinessV2,
	}, "job-bound")
	if v2.State != experiment.RunStateWaitingExternal || v2.Phase != "waiting_external" || !v2.Retryable {
		t.Fatalf("v2 deadline = state %q phase %q retryable %v", v2.State, v2.Phase, v2.Retryable)
	}
	if len(v2.EvidenceRefs) != 1 || v2.EvidenceRefs[0] != "job-bound" {
		t.Fatalf("v2 deadline lost bound task evidence: %#v", v2.EvidenceRefs)
	}

	v1 := directObservationDeadlineError(experiment.LegExecutionRequest{
		HarnessProfile: experiment.HarnessProfileAdaptiveBusinessV1,
	}, "job-legacy")
	if v1.State != experiment.RunStateWaitingInput || v1.Phase != "observation_deferred" {
		t.Fatalf("v1 compatibility changed: state %q phase %q", v1.State, v1.Phase)
	}
}

func TestAdaptiveWaitFailureKeepsObservedStateReconciliationRaceClosed(t *testing.T) {
	request := experiment.LegExecutionRequest{HarnessProfile: experiment.HarnessProfileAdaptiveBusinessV2}
	status := model.DirectJobStatus{Status: "failed"}
	deferred := &experiment.AdapterError{Code: "confidence_deferred", State: experiment.RunStateWaitingInput, Retryable: true}
	if !adaptiveWaitResultNeedsReconciliation(request, status, deferred) {
		t.Fatal("a passive observation failure that completed during polling bypassed reconciliation")
	}
	if adaptiveWaitResultNeedsReconciliation(request, status, &experiment.AdapterError{Code: "explicit_terminal_build_failure"}) {
		t.Fatal("an explicit terminal failure was incorrectly routed into observed-state reconciliation")
	}
	request.HarnessProfile = "legacy"
	if adaptiveWaitResultNeedsReconciliation(request, status, deferred) {
		t.Fatal("legacy execution semantics changed")
	}
}

func TestAdaptiveRuntimeFailureRetryIsLimitedToPreBrowserInfrastructure(t *testing.T) {
	for _, code := range []string{runtimeErrorVideoWorkerMissing, runtimeErrorNodeMissing, "browser_agent_session_start_failed"} {
		if !adaptiveRetryableRuntimeFailure(code) {
			t.Fatalf("pre-browser infrastructure failure %q was not retryable", code)
		}
	}
	for _, code := range []string{"outcome_verification_failed", "explicit_terminal_build_failure", ""} {
		if adaptiveRetryableRuntimeFailure(code) {
			t.Fatalf("business failure %q was incorrectly made retryable", code)
		}
	}
}

func TestAdaptiveResumePrefersLatestReconciliationJobWithoutReplayingEffect(t *testing.T) {
	states := store.NewMemoryStateStore()
	state := &orchestrator.CascadeState{
		ProjectID:              "project-one",
		DesktopCloudRun:        &orchestrator.DesktopCloudRunState{CloudJobID: "job-observe-latest"},
		ExecutableScriptBundle: &model.ExecutableRecordingScriptBundle{RepairLineage: &model.ScriptRepairLineage{SourceCloudJobID: "job-original"}},
	}
	if err := states.Save(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	adapter := newAppExperimentExecutionAdapter(&Service{states: states})
	if got := adapter.latestAdaptiveReconciliationJobID(t.Context(), "project-one", "job-original", experiment.HarnessProfileAdaptiveBusinessV1); got != "job-observe-latest" {
		t.Fatalf("resume selected stale effect job %q", got)
	}
	if got := adapter.latestAdaptiveReconciliationJobID(t.Context(), "project-one", "job-other", experiment.HarnessProfileAdaptiveBusinessV1); got != "job-other" {
		t.Fatalf("unrelated repair lineage was selected: %q", got)
	}
}

func TestRecoveredStageLogTriggersReconciliationInsteadOfCredentialReplay(t *testing.T) {
	status := model.DirectJobStatus{Status: "awaiting_credentials", Artifacts: []model.DirectArtifact{{ArtifactID: "events", Kind: "browser_agent_stage_event_log", SizeBytes: 128}}}
	if !directStatusHasRecoveredStageLog(status) {
		t.Fatal("bounded recovery evidence was not recognized")
	}
	status.Artifacts[0].SizeBytes = 0
	if directStatusHasRecoveredStageLog(status) {
		t.Fatal("empty recovery evidence was accepted")
	}
}

func interactionPredicatesContain(values []model.InteractionPredicate, kind string) bool {
	for _, value := range values {
		if value.Kind == kind {
			return true
		}
	}
	return false
}
