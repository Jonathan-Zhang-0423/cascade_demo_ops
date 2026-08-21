package experiment

import "testing"

func TestInteractiveExperimentProofRequiresEveryDeclaredCapability(t *testing.T) {
	loaded, err := LoadDefinition("../../../experiments", "2048-v2")
	if err != nil {
		t.Fatal(err)
	}
	before, after := 4.0, 12.0
	results := []InteractionProofResult{
		{StepID: "surface_ready", EvidenceKinds: []string{"aria", "visual"}, EvidenceBySlot: map[string]string{"surface_overview": "artifact:surface"}},
		{StepID: "directional_moves", AttemptCount: 5, Actions: []string{"left", "up"}, EvidenceKinds: []string{"frame", "interaction"}, EvidenceBySlot: map[string]string{"before_move": "artifact:move:0", "after_move_one": "artifact:move:1", "after_move_two": "artifact:move:2"}, RegionChanged: true},
		{StepID: "merge_score", EvidenceKinds: []string{"visual", "interaction"}, EvidenceBySlot: map[string]string{"merge_state": "artifact:merge", "score_state": "artifact:value"}, RegionChanged: true, NumericBefore: &before, NumericAfter: &after},
		{StepID: "undo", EvidenceKinds: []string{"frame", "interaction"}, EvidenceBySlot: map[string]string{"pre_undo": "artifact:undo:0", "post_undo": "artifact:undo:1"}, RestoreSimilarity: .96},
		{StepID: "touch", EvidenceKinds: []string{"frame", "interaction"}, EvidenceBySlot: map[string]string{"mobile_before": "artifact:touch:0", "mobile_after": "artifact:touch:1"}, InputModality: "touch", RegionChanged: true},
		{StepID: "terminal_scenes", EvidenceKinds: []string{"aria", "visual"}, EvidenceBySlot: map[string]string{"victory_state": "artifact:state:1", "terminal_state": "artifact:state:2"}, StateVariants: []string{"success", "no_actions"}},
	}
	report, err := ValidateInteractionProof(loaded.InteractionPlan, results)
	if err != nil || !report.Passed {
		t.Fatalf("complete proof was rejected: report=%+v err=%v", report, err)
	}
	results[2].NumericAfter = &before
	report, err = ValidateInteractionProof(loaded.InteractionPlan, results)
	if err != nil || report.Passed || len(report.Findings) == 0 {
		t.Fatalf("missing numeric increase was accepted: report=%+v err=%v", report, err)
	}
}

func TestInteractionProofRejectsInlineEvidenceAndAttemptOverflow(t *testing.T) {
	plan := InteractionPlan{SchemaVersion: InteractionPlanSchemaVersion, PlanID: "plan-test", SurfaceKind: "canvas", Steps: []InteractionStep{
		{StepID: "step-one", SemanticIntent: "prove changed state", ReplayPolicy: ReplayIdempotentWrite, ExpectedChanges: []string{"frame"}, EvidenceSlots: []string{"before", "after"}, ProofRequirements: []ProofRequirement{{Kind: "region_changed"}}, MaxAttempts: 2},
		{StepID: "step-two", SemanticIntent: "prove visible state", ReplayPolicy: ReplayObserveOnly, ExpectedChanges: []string{"visual"}, EvidenceSlots: []string{"state"}, ProofRequirements: []ProofRequirement{{Kind: "all_evidence_slots"}}},
	}}
	report, err := ValidateInteractionProof(plan, []InteractionProofResult{
		{StepID: "step-one", AttemptCount: 3, EvidenceKinds: []string{"frame"}, EvidenceBySlot: map[string]string{"before": "data:image/png;base64,AAAA", "after": "artifact:after"}, RegionChanged: true},
		{StepID: "step-two", EvidenceKinds: []string{"visual"}, EvidenceBySlot: map[string]string{"state": "artifact:state"}},
	})
	if err != nil || report.Passed {
		t.Fatalf("unsafe or over-budget proof was accepted: report=%+v err=%v", report, err)
	}
}
