package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/experiment"
	"cascade-demoops/backend/internal/model"
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
	if err != nil { t.Fatal(err) }
	contracts, err := compileExperimentInteractionContracts(loaded.InteractionPlan)
	if err != nil { t.Fatal(err) }
	if len(contracts) != len(loaded.InteractionPlan.Steps) { t.Fatalf("contracts=%d steps=%d", len(contracts), len(loaded.InteractionPlan.Steps)) }
	wantActions := []model.GraphActionType{model.GraphActionInspect, model.GraphActionPress, model.GraphActionInspect, model.GraphActionClick, model.GraphActionGesture, model.GraphActionClick}
	for index, contract := range contracts {
		if contract.ActionKind != wantActions[index] { t.Fatalf("contract %d action=%s want=%s", index, contract.ActionKind, wantActions[index]) }
		if contract.Parameters["evidence_step_id"] != loaded.InteractionPlan.Steps[index].StepID { t.Fatalf("contract %d lost evidence step binding", index) }
		if err := model.ValidateInteractionContract(contract); err != nil { t.Fatalf("contract %d invalid: %v", index, err) }
	}
	if got := contracts[1].ExpectedTransitions; !interactionPredicatesContain(got, "distinct_actions_observed") || !interactionPredicatesContain(got, "frame_surface_changed") { t.Fatalf("direction proof predicates=%+v", got) }
	if got := contracts[4].ExpectedTransitions; !interactionPredicatesContain(got, "input_modality_used") { t.Fatalf("touch proof predicates=%+v", got) }
}

func interactionPredicatesContain(values []model.InteractionPredicate, kind string) bool {
	for _, value := range values { if value.Kind == kind { return true } }
	return false
}
