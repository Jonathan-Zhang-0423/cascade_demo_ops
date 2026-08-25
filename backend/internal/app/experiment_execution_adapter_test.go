package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/experiment"
	"cascade-demoops/backend/internal/model"
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
	for _, stage := range prepared.Build.Package.ExecutableScriptBundle.StageApprovalPlan.Stages {
		if stage.NodeID != "business_stage_continue_prepared_execution" && stage.NodeID != "business_stage_continue_prepared_execution_followup" {
			continue
		}
		foundContinuations[stage.NodeID] = stage.Interaction.Parameters["action_recipe"] == "continue_execution" && stage.Interaction.Parameters["optional_when_target_absent"] == "true" && stage.Interaction.Parameters["capture_result_surface_baseline"] == "true" && stage.Interaction.Parameters["target_wait_timeout_ms"] == "300000"
	}
	if !foundContinuations["business_stage_continue_prepared_execution"] || !foundContinuations["business_stage_continue_prepared_execution_followup"] {
		t.Fatalf("adaptive async package did not include the bounded runtime execution continuation chain: %+v", foundContinuations)
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
	calls, terminal, err := recordLiveBrowserVisualObservations(downloads, func(update experiment.LegExecutionUpdate) error {
		if update.Kind != "visual_observation" || len(update.EvidenceRefs) != 1 {
			t.Fatalf("unexpected visual update: %+v", update)
		}
		emitted++
		return nil
	})
	if err != nil || calls != 4 || emitted != 3 || !terminal {
		t.Fatalf("live observations calls=%d emitted=%d terminal=%t err=%v", calls, emitted, terminal, err)
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

func interactionPredicatesContain(values []model.InteractionPredicate, kind string) bool {
	for _, value := range values {
		if value.Kind == kind {
			return true
		}
	}
	return false
}
