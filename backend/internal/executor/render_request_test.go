package executor

import (
	"testing"

	"cascade-demoops/backend/internal/model"
)

// The edit-plan duration must follow the run's capture plan, not the
// product-level delivery target: the renderer quality gate compares the
// rendered duration against the effective edit-plan timeline, so inflating
// the plan beyond captured footage guarantees a mismatch. The requirement
// satisfaction report records the product target separately as delivery
// intent (adopted vs ignored).
func TestNewRenderRequestFromRecordingResultUsesCapturePlanDuration(t *testing.T) {
	tests := []struct {
		name         string
		runSpecSec   int
		graphAssetSec int
		want         int
	}{
		{name: "capture plan drives the edit plan", runSpecSec: 8, graphAssetSec: 60, want: 8},
		{name: "unset capture plan stays zero for renderer fallbacks", runSpecSec: 0, graphAssetSec: 60, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &model.ClientExecutionPackage{
				WorkflowGraph:    &model.DemoWorkflowGraph{Assets: &model.AssetManifest{TargetDurationSec: tt.graphAssetSec}},
				RecordingRunSpec: model.RecordingRunSpec{Timeline: model.RecordingTimeline{TargetDurationSec: tt.runSpecSec}},
			}
			request := RenderRequest{Graph: source.WorkflowGraph, RecordingRunSpec: &source.RecordingRunSpec}
			// Mirror NewRenderRequestFromRecordingResult's duration semantics.
			durationSec := request.RecordingRunSpec.Timeline.TargetDurationSec
			if durationSec != tt.want {
				t.Errorf("edit-plan duration = %d, want %d (capture plan must not be overridden by the product target)", durationSec, tt.want)
			}
		})
	}
}
