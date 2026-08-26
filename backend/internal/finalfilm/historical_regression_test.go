package finalfilm

import (
	"encoding/json"
	"os"
	"testing"
)

func TestR62HistoricalSampleIsRejectedForEveryConfirmedRootCause(t *testing.T) {
	raw, err := os.ReadFile("testdata/r62-rejected.json")
	if err != nil {
		t.Fatal(err)
	}
	var sample historicalRegressionSample
	if err := json.Unmarshal(raw, &sample); err != nil {
		t.Fatal(err)
	}
	reasons := rejectHistoricalRegression(sample)
	wantedPrefixes := []string{"incomplete_creation_chain:", "old_entity_recovery", "ambient_motion_on_fact_footage", "missing_real_content_quality_gate", "internal_field_entered_director_input:"}
	for _, prefix := range wantedPrefixes {
		found := false
		for _, reason := range reasons {
			if len(reason) >= len(prefix) && reason[:len(prefix)] == prefix {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("r62 regression did not reject %q: %+v", prefix, reasons)
		}
	}
}
