package experiment

import (
	"errors"
	"fmt"
	"strings"
)

type InteractionProofResult struct {
	StepID            string            `json:"step_id"`
	AttemptCount      int               `json:"attempt_count"`
	Actions           []string          `json:"actions,omitempty"`
	EvidenceKinds     []string          `json:"evidence_kinds"`
	EvidenceBySlot    map[string]string `json:"evidence_by_slot"`
	RegionChanged     bool              `json:"region_changed,omitempty"`
	NumericBefore     *float64          `json:"numeric_before,omitempty"`
	NumericAfter      *float64          `json:"numeric_after,omitempty"`
	RestoreSimilarity float64           `json:"restore_similarity,omitempty"`
	InputModality     string            `json:"input_modality,omitempty"`
	StateVariants     []string          `json:"state_variants,omitempty"`
}

type InteractionProofReport struct {
	SchemaVersion string                       `json:"schema_version"`
	PlanID        string                       `json:"plan_id"`
	Passed        bool                         `json:"passed"`
	StepResults   []InteractionProofStepReport `json:"step_results"`
	Findings      []string                     `json:"findings,omitempty"`
}

type InteractionProofStepReport struct {
	StepID       string   `json:"step_id"`
	Passed       bool     `json:"passed"`
	EvidenceRefs []string `json:"evidence_refs"`
	Findings     []string `json:"findings,omitempty"`
}

func ValidateInteractionProof(plan InteractionPlan, results []InteractionProofResult) (InteractionProofReport, error) {
	if err := ValidateInteractionPlan(plan); err != nil {
		return InteractionProofReport{}, err
	}
	byStep := map[string]InteractionProofResult{}
	for _, result := range results {
		if strings.TrimSpace(result.StepID) == "" || byStep[result.StepID].StepID != "" {
			return InteractionProofReport{}, errors.New("interaction proof results contain a blank or duplicate step")
		}
		byStep[result.StepID] = result
	}
	report := InteractionProofReport{SchemaVersion: "demoops.interaction_proof_report.v1", PlanID: plan.PlanID, Passed: true}
	for _, step := range plan.Steps {
		result, ok := byStep[step.StepID]
		stepReport := InteractionProofStepReport{StepID: step.StepID, Passed: ok}
		if !ok {
			stepReport.Findings = append(stepReport.Findings, "required interaction proof is missing")
		} else {
			if step.MaxAttempts > 0 && (result.AttemptCount < 1 || result.AttemptCount > step.MaxAttempts) {
				stepReport.Findings = append(stepReport.Findings, "interaction attempt budget was exceeded or omitted")
			}
			for _, expected := range step.ExpectedChanges {
				if !containsString(result.EvidenceKinds, expected) {
					stepReport.Findings = append(stepReport.Findings, "missing evidence channel: "+expected)
				}
			}
			for _, slot := range step.EvidenceSlots {
				if ref := strings.TrimSpace(result.EvidenceBySlot[slot]); ref == "" || strings.HasPrefix(strings.ToLower(ref), "data:") {
					stepReport.Findings = append(stepReport.Findings, "missing bounded evidence slot: "+slot)
				} else {
					stepReport.EvidenceRefs = append(stepReport.EvidenceRefs, ref)
				}
			}
			for _, requirement := range step.ProofRequirements {
				if finding := evaluateProofRequirement(requirement, result); finding != "" {
					stepReport.Findings = append(stepReport.Findings, finding)
				}
			}
			stepReport.Passed = len(stepReport.Findings) == 0
		}
		if !stepReport.Passed {
			report.Passed = false
			for _, finding := range stepReport.Findings {
				report.Findings = append(report.Findings, fmt.Sprintf("%s: %s", step.StepID, finding))
			}
		}
		report.StepResults = append(report.StepResults, stepReport)
	}
	return report, nil
}

func evaluateProofRequirement(requirement ProofRequirement, result InteractionProofResult) string {
	switch requirement.Kind {
	case "all_evidence_slots":
		return ""
	case "distinct_actions":
		seen := map[string]bool{}
		for _, action := range result.Actions {
			if value := strings.TrimSpace(action); value != "" {
				seen[value] = true
			}
		}
		if len(seen) < requirement.MinCount {
			return "distinct action proof is incomplete"
		}
	case "region_changed":
		if !result.RegionChanged {
			return "interactive region did not show a bounded change"
		}
	case "numeric_increase":
		if result.NumericBefore == nil || result.NumericAfter == nil || *result.NumericAfter <= *result.NumericBefore {
			return "numeric outcome did not increase"
		}
	case "approximate_state_restore":
		if result.RestoreSimilarity < requirement.MinSimilarity {
			return "restored state similarity is below the declared threshold"
		}
	case "input_modality":
		if result.InputModality != requirement.Modality {
			return "input modality proof does not match the declared modality"
		}
	case "state_variants":
		seen := map[string]bool{}
		for _, state := range result.StateVariants {
			if value := strings.TrimSpace(state); value != "" {
				seen[value] = true
			}
		}
		if len(seen) < requirement.MinCount {
			return "state-variant proof is incomplete"
		}
	}
	return ""
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
