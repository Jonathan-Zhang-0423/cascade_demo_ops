package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
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
