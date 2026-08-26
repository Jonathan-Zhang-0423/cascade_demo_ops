package finalfilm

import "strings"

// historicalRegressionSample is a compact, media-free description of a
// previously delivered film. Keeping the rejected r62 video out of git makes
// the regression cheap while preserving the exact product-level failure facts.
type historicalRegressionSample struct {
	SampleID             string   `json:"sample_id"`
	RequiredChapters     []string `json:"required_chapters"`
	CoveredChapters      []string `json:"covered_chapters"`
	FreshEntity          bool     `json:"fresh_entity"`
	FactOperationStyles  []string `json:"fact_operation_styles"`
	CandidateContentGate string   `json:"candidate_content_gate"`
	DirectorInputFields  []string `json:"director_input_fields"`
}

func rejectHistoricalRegression(sample historicalRegressionSample) []string {
	reasons := []string{}
	covered := map[string]bool{}
	for _, chapter := range sample.CoveredChapters {
		covered[strings.TrimSpace(chapter)] = true
	}
	for _, chapter := range sample.RequiredChapters {
		if !covered[strings.TrimSpace(chapter)] {
			reasons = append(reasons, "incomplete_creation_chain:"+chapter)
		}
	}
	if !sample.FreshEntity {
		reasons = append(reasons, "old_entity_recovery")
	}
	for _, style := range sample.FactOperationStyles {
		if style == "ambient_motion" {
			reasons = append(reasons, "ambient_motion_on_fact_footage")
			break
		}
	}
	if sample.CandidateContentGate == "technical_only" {
		reasons = append(reasons, "missing_real_content_quality_gate")
	}
	for _, field := range sample.DirectorInputFields {
		lower := strings.ToLower(field)
		if lower == "action" || lower == "observed_state" || lower == "expected_outcome" || strings.Contains(lower, "selector") || strings.Contains(lower, "dom") {
			reasons = append(reasons, "internal_field_entered_director_input:"+field)
		}
	}
	return reasons
}
