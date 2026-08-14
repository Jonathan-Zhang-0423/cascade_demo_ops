package model

import "testing"

func TestValidatePresentationGenerationIntentsEnforcesStableBoundary(t *testing.T) {
	valid, err := PresentationGenerationIntentDefaults("presentation_intro_001", "intro", []string{"shot_1", "shot_2"})
	if err != nil || ValidatePresentationGenerationIntents([]PresentationGenerationIntent{valid}) != nil {
		t.Fatalf("valid intent rejected: intent=%+v err=%v", valid, err)
	}
	tests := []struct {
		name   string
		mutate func(*PresentationGenerationIntent)
	}{
		{"provider capability", func(v *PresentationGenerationIntent) { v.Capability = "seedance" }},
		{"business purpose", func(v *PresentationGenerationIntent) { v.Purpose = "business_step" }},
		{"required generation", func(v *PresentationGenerationIntent) { v.Required = true }},
		{"too many refs", func(v *PresentationGenerationIntent) { v.ReferenceAssetRefs = []string{"1", "2", "3", "4", "5"} }},
		{"too short", func(v *PresentationGenerationIntent) { v.RequestedSlot.PreferredDurationSec = 3 }},
		{"replace UI", func(v *PresentationGenerationIntent) { v.ContentPolicy.MayReplaceCapturedUI = true }},
		{"blocking failure", func(v *PresentationGenerationIntent) { v.FailurePolicy = "fail_recording" }},
		{"source step binding", func(v *PresentationGenerationIntent) { v.SourceStepID = "business_step_1" }},
		{"provider", func(v *PresentationGenerationIntent) { v.Provider = "seedance" }},
		{"model", func(v *PresentationGenerationIntent) { v.ModelID = "provider-model-id" }},
		{"endpoint", func(v *PresentationGenerationIntent) { v.APIEndpoint = "https://provider.invalid/v1" }},
		{"generation fps", func(v *PresentationGenerationIntent) { v.GenerationFPS = 30 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if err := ValidatePresentationGenerationIntents([]PresentationGenerationIntent{candidate}); err == nil {
				t.Fatalf("invalid intent accepted: %+v", candidate)
			}
		})
	}
}
