package model

import (
	"errors"
	"fmt"
	"strings"
)

const InteractionContractSchemaVersion = "demoops.interaction_contract.v1"

type ProductArchetype string

const (
	ProductArchetypeUnknown      ProductArchetype = "unknown"
	ProductArchetypeAsyncBuilder ProductArchetype = "async_builder"
	ProductArchetypeCRUDForm     ProductArchetype = "crud_form"
	ProductArchetypeDashboard    ProductArchetype = "dashboard_analytics"
	ProductArchetypeCanvasEditor ProductArchetype = "canvas_editor"
	ProductArchetypeInteractive  ProductArchetype = "interactive_application"
)

// InteractionContract binds one approved semantic action to independently
// observable post-action outcomes. It intentionally contains no hostname,
// route literal, selector discovery rule, or product-specific stage name.
type InteractionContract struct {
	SchemaVersion       string                  `json:"schema_version"`
	ContractID          string                  `json:"contract_id"`
	SemanticGoal        string                  `json:"semantic_goal"`
	Archetype           ProductArchetype        `json:"archetype,omitempty"`
	ActionKind          GraphActionType         `json:"action_kind"`
	ReplayPolicy        InteractionReplayPolicy `json:"replay_policy"`
	TargetSemanticID    string                  `json:"target_semantic_id"`
	ActionTarget        ActionTarget            `json:"action_target,omitempty"`
	Preconditions       []InteractionPredicate  `json:"preconditions,omitempty"`
	ExpectedTransitions []InteractionPredicate  `json:"expected_transitions"`
	EvidenceRefs        []EvidenceRef           `json:"evidence_refs"`
	NonDestructive      bool                    `json:"non_destructive"`
}

type InteractionReplayPolicy string

const (
	InteractionReplayObserveOnly     InteractionReplayPolicy = "observe_only"
	InteractionReplayIdempotentWrite InteractionReplayPolicy = "idempotent_write"
	InteractionReplayOnceEffect      InteractionReplayPolicy = "once_effect"
)

type InteractionPredicate struct {
	ID           string        `json:"id"`
	Kind         string        `json:"kind"`
	Target       ActionTarget  `json:"target,omitempty"`
	Expected     any           `json:"expected,omitempty"`
	Required     bool          `json:"required"`
	TimeoutMS    int           `json:"timeout_ms,omitempty"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

var interactionPredicateKinds = map[string]bool{
	"url_matches": true, "element_visible": true, "element_hidden": true,
	"text_contains": true, "attribute_equals": true, "value_equals": true,
	"element_count": true, "page_title_contains": true,
	"state_changed": true, "page_changed": true,
	"dom_changed": true, "aria_changed": true, "network_settled": true,
	"visual_region_changed": true, "frame_surface_changed": true,
	"interactive_surface_visible": true,
	// Decode-only compatibility for packages produced before the generic
	// interactive surface contract was introduced.
	"playable_surface_visible": true,
}

func ValidateInteractionContract(contract InteractionContract) error {
	if contract.SchemaVersion != InteractionContractSchemaVersion {
		return errors.New("unsupported interaction contract schema_version")
	}
	if strings.TrimSpace(contract.ContractID) == "" || strings.TrimSpace(contract.SemanticGoal) == "" || strings.TrimSpace(contract.TargetSemanticID) == "" || strings.TrimSpace(string(contract.ActionKind)) == "" {
		return errors.New("interaction contract identity, goal, action, and target are required")
	}
	if !validProductArchetype(contract.Archetype) {
		return fmt.Errorf("unsupported product archetype %q", contract.Archetype)
	}
	if contract.ReplayPolicy != InteractionReplayObserveOnly && contract.ReplayPolicy != InteractionReplayIdempotentWrite && contract.ReplayPolicy != InteractionReplayOnceEffect {
		return fmt.Errorf("unsupported interaction replay_policy %q", contract.ReplayPolicy)
	}
	if !contract.NonDestructive {
		return errors.New("interaction contract must explicitly be non_destructive")
	}
	if len(contract.ExpectedTransitions) == 0 {
		return errors.New("interaction contract requires at least one expected transition")
	}
	if len(contract.EvidenceRefs) == 0 {
		return errors.New("interaction contract requires evidence_refs")
	}
	seen := map[string]bool{}
	for _, predicate := range append(append([]InteractionPredicate{}, contract.Preconditions...), contract.ExpectedTransitions...) {
		if strings.TrimSpace(predicate.ID) == "" || seen[predicate.ID] {
			return errors.New("interaction predicate id is blank or duplicated")
		}
		seen[predicate.ID] = true
		if !interactionPredicateKinds[strings.TrimSpace(predicate.Kind)] {
			return fmt.Errorf("unsupported interaction predicate kind %q", predicate.Kind)
		}
		if predicate.TimeoutMS < 0 {
			return errors.New("interaction predicate timeout_ms cannot be negative")
		}
	}
	for _, predicate := range contract.ExpectedTransitions {
		if !predicate.Required {
			return errors.New("every expected interaction transition must be required")
		}
	}
	if contract.ActionKind == GraphActionClick {
		independent := false
		for _, predicate := range contract.ExpectedTransitions {
			if predicate.Kind != "element_visible" || !sameInteractionTarget(contract.ActionTarget, predicate.Target) {
				independent = true
				break
			}
		}
		if !independent {
			return errors.New("click outcome cannot only re-observe the action target as visible")
		}
	}
	return nil
}

func sameInteractionTarget(left, right ActionTarget) bool {
	leftValues := []string{left.Selector, left.TestID, left.Role, left.Label, left.Text, left.URL}
	rightValues := []string{right.Selector, right.TestID, right.Role, right.Label, right.Text, right.URL}
	hasValue := false
	for index := range leftValues {
		l, r := strings.TrimSpace(leftValues[index]), strings.TrimSpace(rightValues[index])
		if l != "" || r != "" {
			hasValue = true
		}
		if l != r {
			return false
		}
	}
	return hasValue
}

func validProductArchetype(value ProductArchetype) bool {
	switch value {
	case "", ProductArchetypeUnknown, ProductArchetypeAsyncBuilder, ProductArchetypeCRUDForm, ProductArchetypeDashboard, ProductArchetypeCanvasEditor, ProductArchetypeInteractive:
		return true
	default:
		return false
	}
}
