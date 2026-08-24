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
	wantActions := []model.GraphActionType{model.GraphActionInspect, model.GraphActionPress, model.GraphActionInspect, model.GraphActionClick, model.GraphActionGesture, model.GraphActionClick}
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
	if got := contracts[2].ExpectedTransitions; !interactionPredicatesContain(got, "numeric_increased") || !interactionPredicatesContain(got, "state_changed") {
		t.Fatalf("numeric proof predicates=%+v", got)
	}
	if got := contracts[4].ExpectedTransitions; !interactionPredicatesContain(got, "input_modality_used") {
		t.Fatalf("touch proof predicates=%+v", got)
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

func interactionPredicatesContain(values []model.InteractionPredicate, kind string) bool {
	for _, value := range values {
		if value.Kind == kind {
			return true
		}
	}
	return false
}
