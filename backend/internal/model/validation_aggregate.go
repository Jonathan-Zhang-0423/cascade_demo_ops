package model

// ValidationAggregate summarises all validation findings from a completed
// execution. It is produced by AggregateValidation and is intended for
// dashboards, query interfaces and cross-run comparisons. All fields are
// populated from the existing ValidationReports and StepResults — no
// additional I/O is required.
type ValidationAggregate struct {
	// Counts across all checks in all reports.
	TotalChecks       int
	TotalFailed       int
	TotalBlocking     int
	UniqueFailureCodes int

	// Failure code histogram: code -> number of failed checks with that code.
	CodeCounts map[string]int

	// Codes grouped by responsibility domain for team routing.
	CodesByDomain map[ValidationCheckDomain][]string

	// Codes whose severity is blocking (de-duplicated).
	BlockingCodes []string
	// Codes whose severity is warning (de-duplicated).
	WarningCodes []string

	// Stage timing derived from StepResults.
	StageDurationMS    map[string]int // nodeID -> DurationMS
	TotalDurationMS    int
	SlowestStageNodeID string

	// Codes that appear in more than one stage (cross-stage repeated issues).
	RepeatedCodes []RepeatedValidationCode
}

// RepeatedValidationCode records a failure code that was observed in multiple
// stages, helping identify systemic issues.
type RepeatedValidationCode struct {
	Code    string                `json:"code"`
	Count   int                   `json:"count"`
	Domain  ValidationCheckDomain `json:"domain,omitempty"`
	NodeIDs []string              `json:"node_ids,omitempty"`
}

// AggregateValidation computes a ValidationAggregate from the provided reports
// and step results. Both arguments may be nil or empty. The function is a pure
// computation — it does not write to disk or call any external service.
func AggregateValidation(reports []ValidationReport, steps []StepResult) ValidationAggregate {
	agg := ValidationAggregate{
		CodeCounts:      make(map[string]int),
		CodesByDomain:   make(map[ValidationCheckDomain][]string),
		StageDurationMS: make(map[string]int),
	}

	// --- failure code analysis --------------------------------------------------

	// codeNodeIDs tracks, for each failing code, which nodeIDs it appeared in.
	// Used to detect repeated cross-stage codes.
	codeNodeIDs := make(map[string]map[string]struct{})
	// codeDomain tracks the responsibility domain for de-duplication.
	codeDomain := make(map[string]ValidationCheckDomain)
	// seenDomainCodes tracks unique (domain, code) pairs for CodesByDomain.
	seenDomainCodes := make(map[ValidationCheckDomain]map[string]struct{})
	// seenBlockingCodes / seenWarningCodes for de-duplicated severity slices.
	seenBlocking := make(map[string]struct{})
	seenWarning := make(map[string]struct{})

	for _, rpt := range reports {
		for _, chk := range rpt.Checks {
			agg.TotalChecks++
			if chk.Passed {
				continue
			}
			agg.TotalFailed++
			if chk.Severity == FindingSeverityBlocking {
				agg.TotalBlocking++
				if _, exists := seenBlocking[chk.Code]; !exists && chk.Code != "" {
					seenBlocking[chk.Code] = struct{}{}
					agg.BlockingCodes = append(agg.BlockingCodes, chk.Code)
				}
			}
			if chk.Severity == FindingSeverityWarning {
				if _, exists := seenWarning[chk.Code]; !exists && chk.Code != "" {
					seenWarning[chk.Code] = struct{}{}
					agg.WarningCodes = append(agg.WarningCodes, chk.Code)
				}
			}
			if chk.Code == "" {
				continue
			}
			agg.CodeCounts[chk.Code]++

			// Track node association for repeated-code detection.
			if _, exists := codeNodeIDs[chk.Code]; !exists {
				codeNodeIDs[chk.Code] = make(map[string]struct{})
			}
			nodeID := firstNonEmptyStr(chk.NodeID, rpt.NodeID, "_unknown")
			codeNodeIDs[chk.Code][nodeID] = struct{}{}

			// Domain grouping (de-duplicated per domain).
			if chk.ResponsibilityDomain != "" {
				codeDomain[chk.Code] = chk.ResponsibilityDomain
				if _, exists := seenDomainCodes[chk.ResponsibilityDomain]; !exists {
					seenDomainCodes[chk.ResponsibilityDomain] = make(map[string]struct{})
				}
				if _, seen := seenDomainCodes[chk.ResponsibilityDomain][chk.Code]; !seen {
					seenDomainCodes[chk.ResponsibilityDomain][chk.Code] = struct{}{}
					agg.CodesByDomain[chk.ResponsibilityDomain] = append(
						agg.CodesByDomain[chk.ResponsibilityDomain], chk.Code)
				}
			}
		}
	}

	agg.UniqueFailureCodes = len(agg.CodeCounts)

	// Repeated codes: codes appearing in more than one distinct node.
	for code, nodeSet := range codeNodeIDs {
		if len(nodeSet) < 2 {
			continue
		}
		nodes := make([]string, 0, len(nodeSet))
		for n := range nodeSet {
			nodes = append(nodes, n)
		}
		agg.RepeatedCodes = append(agg.RepeatedCodes, RepeatedValidationCode{
			Code:    code,
			Count:   len(nodeSet),
			Domain:  codeDomain[code],
			NodeIDs: nodes,
		})
	}

	// --- stage timing -----------------------------------------------------------

	slowestMS := -1
	for _, step := range steps {
		if step.NodeID == "" {
			continue
		}
		agg.StageDurationMS[step.NodeID] = step.DurationMS
		agg.TotalDurationMS += step.DurationMS
		if step.DurationMS > slowestMS {
			slowestMS = step.DurationMS
			agg.SlowestStageNodeID = step.NodeID
		}
	}

	return agg
}

// firstNonEmptyStr returns the first non-empty string among the candidates.
func firstNonEmptyStr(candidates ...string) string {
	for _, c := range candidates {
		if c != "" {
			return c
		}
	}
	return ""
}
