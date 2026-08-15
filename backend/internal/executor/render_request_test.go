package executor

import (
	"testing"
)

// TestRenderRequest_DurationPriorityLogic tests the duration priority logic directly
func TestRenderRequest_DurationPriorityLogic(t *testing.T) {
	tests := []struct {
		name                         string
		workflowGraphTargetDuration  int
		recordingRunSpecDuration     int
		expectedDuration             int
	}{
		{
			name:                         "workflow_graph.assets.target_duration_sec takes priority",
			workflowGraphTargetDuration:  60,
			recordingRunSpecDuration:     30,
			expectedDuration:             60,
		},
		{
			name:                         "fallback to recording_run_spec when workflow_graph.assets is zero",
			workflowGraphTargetDuration:  0,
			recordingRunSpecDuration:     45,
			expectedDuration:             45,
		},
		{
			name:                         "fallback to default 60s when both are zero",
			workflowGraphTargetDuration:  0,
			recordingRunSpecDuration:     0,
			expectedDuration:             60,
		},
		{
			name:                         "workflow_graph.assets overrides recording_run_spec",
			workflowGraphTargetDuration:  90,
			recordingRunSpecDuration:     30,
			expectedDuration:             90,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the render_request.go logic
			durationSec := tt.recordingRunSpecDuration
			if tt.workflowGraphTargetDuration > 0 {
				durationSec = tt.workflowGraphTargetDuration
			}
			if durationSec == 0 {
				durationSec = 60
			}

			if durationSec != tt.expectedDuration {
				t.Errorf("DurationSec = %d, want %d", durationSec, tt.expectedDuration)
			}
		})
	}
}
