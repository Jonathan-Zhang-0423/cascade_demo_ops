package model

import "testing"

func TestInteractionContractRequiresIndependentOutcomeAndReplayPolicy(t *testing.T) {
	contract := InteractionContract{
		SchemaVersion: InteractionContractSchemaVersion,
		ContractID:    "interaction_1", SemanticGoal: "Create an item and observe the resulting state",
		Archetype: ProductArchetypeCRUDForm, ActionKind: GraphActionClick,
		ReplayPolicy: InteractionReplayOnceEffect, TargetSemanticID: "create_item",
		ExpectedTransitions: []InteractionPredicate{{ID: "result", Kind: "dom_changed", Required: true}},
		EvidenceRefs:        []EvidenceRef{{ID: "evidence_1"}}, NonDestructive: true,
	}
	if err := ValidateInteractionContract(contract); err != nil {
		t.Fatalf("valid interaction contract rejected: %v", err)
	}
	contract.ReplayPolicy = ""
	if err := ValidateInteractionContract(contract); err == nil {
		t.Fatal("missing replay policy must be rejected")
	}
	contract.ReplayPolicy = InteractionReplayOnceEffect
	contract.ExpectedTransitions = nil
	if err := ValidateInteractionContract(contract); err == nil {
		t.Fatal("missing post-action outcome must be rejected")
	}
}

func TestInteractionContractAcceptsAllGenericObserverKinds(t *testing.T) {
	for _, kind := range []string{"state_changed", "dom_changed", "aria_changed", "network_settled", "visual_region_changed", "frame_surface_changed", "interactive_surface_visible"} {
		contract := InteractionContract{
			SchemaVersion: InteractionContractSchemaVersion, ContractID: "interaction_" + kind,
			SemanticGoal: "Observe a generic outcome", Archetype: ProductArchetypeUnknown,
			ActionKind: GraphActionInspect, ReplayPolicy: InteractionReplayObserveOnly, TargetSemanticID: "surface",
			ExpectedTransitions: []InteractionPredicate{{ID: "outcome_" + kind, Kind: kind, Required: true}},
			EvidenceRefs:        []EvidenceRef{{ID: "evidence_" + kind}}, NonDestructive: true,
		}
		if err := ValidateInteractionContract(contract); err != nil {
			t.Fatalf("kind %s rejected: %v", kind, err)
		}
	}
}
