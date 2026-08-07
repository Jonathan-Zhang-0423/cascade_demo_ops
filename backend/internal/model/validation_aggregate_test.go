package model

import (
	"testing"
	"time"
)

func TestValidationAggregateBasicCounting(t *testing.T) {
	reports := []ValidationReport{
		{
			Phase:    ValidationPhasePreExecution,
			Decision: ValidationDecisionContinue,
			Checks:   nil,
		},
		{
			Phase:    ValidationPhaseRuntimeStage,
			NodeID:   "node_1",
			Decision: ValidationDecisionStopAndReport,
			Checks: []ValidationCheck{
				{Code: "STAGE_FAILED", Severity: FindingSeverityBlocking, Passed: false, Required: true, ResponsibilityDomain: ValidationCheckDomainServer},
				{Code: "MISSING_EVIDENCE_REFS", Severity: FindingSeverityWarning, Passed: false, Required: false, ResponsibilityDomain: ValidationCheckDomainServer},
			},
		},
		{
			Phase:    ValidationPhasePostExecution,
			Decision: ValidationDecisionStopAndReport,
			Checks: []ValidationCheck{
				{Code: "STAGE_FAILED", Severity: FindingSeverityBlocking, Passed: false, Required: true, ResponsibilityDomain: ValidationCheckDomainServer},
				{Code: "REQUIRED_STAGE_NOT_COMPLETED", Severity: FindingSeverityBlocking, Passed: false, Required: true, ResponsibilityDomain: ValidationCheckDomainServer},
			},
		},
	}
	steps := []StepResult{
		{NodeID: "node_1", Status: "failed", DurationMS: 1500},
		{NodeID: "node_2", Status: "not_started", DurationMS: 0},
	}

	agg := AggregateValidation(reports, steps)

	if agg.TotalChecks != 4 {
		t.Errorf("TotalChecks: want 4, got %d", agg.TotalChecks)
	}
	if agg.TotalFailed != 4 {
		t.Errorf("TotalFailed: want 4, got %d", agg.TotalFailed)
	}
	if agg.TotalBlocking != 3 {
		t.Errorf("TotalBlocking: want 3, got %d", agg.TotalBlocking)
	}
	if agg.UniqueFailureCodes != 3 {
		t.Errorf("UniqueFailureCodes: want 3 (STAGE_FAILED, MISSING_EVIDENCE_REFS, REQUIRED_STAGE_NOT_COMPLETED), got %d", agg.UniqueFailureCodes)
	}
	if agg.CodeCounts["STAGE_FAILED"] != 2 {
		t.Errorf("STAGE_FAILED count: want 2, got %d", agg.CodeCounts["STAGE_FAILED"])
	}
	if agg.TotalDurationMS != 1500 {
		t.Errorf("TotalDurationMS: want 1500, got %d", agg.TotalDurationMS)
	}
}

func TestValidationAggregateRepeatedCodes(t *testing.T) {
	reports := []ValidationReport{
		{
			Phase:  ValidationPhaseRuntimeStage,
			NodeID: "node_1",
			Checks: []ValidationCheck{
				{Code: "STAGE_FAILED", Severity: FindingSeverityBlocking, Passed: false, ResponsibilityDomain: ValidationCheckDomainServer},
			},
		},
		{
			Phase:  ValidationPhaseRuntimeStage,
			NodeID: "node_2",
			Checks: []ValidationCheck{
				{Code: "STAGE_FAILED", Severity: FindingSeverityBlocking, Passed: false, ResponsibilityDomain: ValidationCheckDomainServer},
			},
		},
	}

	agg := AggregateValidation(reports, nil)

	if len(agg.RepeatedCodes) != 1 {
		t.Fatalf("want 1 repeated code, got %d: %+v", len(agg.RepeatedCodes), agg.RepeatedCodes)
	}
	rc := agg.RepeatedCodes[0]
	if rc.Code != "STAGE_FAILED" || rc.Count != 2 {
		t.Errorf("repeated code wrong: %+v", rc)
	}
	if len(rc.NodeIDs) != 2 {
		t.Errorf("expected 2 node IDs in repeated code, got %d", len(rc.NodeIDs))
	}
}

func TestValidationAggregateDomainGrouping(t *testing.T) {
	reports := []ValidationReport{
		{
			Phase: ValidationPhaseRuntimeStage,
			Checks: []ValidationCheck{
				{Code: "CROSS_DOMAIN_ACCESS", Severity: FindingSeverityBlocking, Passed: false, ResponsibilityDomain: ValidationCheckDomainApp},
				{Code: "STAGE_FAILED", Severity: FindingSeverityBlocking, Passed: false, ResponsibilityDomain: ValidationCheckDomainServer},
				{Code: "MISSING_MP4_VIDEO", Severity: FindingSeverityWarning, Passed: false, ResponsibilityDomain: ValidationCheckDomainEnvironment},
			},
		},
	}

	agg := AggregateValidation(reports, nil)

	if len(agg.CodesByDomain[ValidationCheckDomainApp]) != 1 {
		t.Errorf("app domain: want 1 code, got %d", len(agg.CodesByDomain[ValidationCheckDomainApp]))
	}
	if len(agg.CodesByDomain[ValidationCheckDomainServer]) != 1 {
		t.Errorf("server domain: want 1 code, got %d", len(agg.CodesByDomain[ValidationCheckDomainServer]))
	}
	if len(agg.CodesByDomain[ValidationCheckDomainEnvironment]) != 1 {
		t.Errorf("environment domain: want 1 code, got %d", len(agg.CodesByDomain[ValidationCheckDomainEnvironment]))
	}
}

func TestValidationAggregateStageTiming(t *testing.T) {
	steps := []StepResult{
		{NodeID: "node_fast", Status: "passed", DurationMS: 200, StartedAt: time.Now()},
		{NodeID: "node_slow", Status: "passed", DurationMS: 3000, StartedAt: time.Now()},
		{NodeID: "node_medium", Status: "failed", DurationMS: 800},
	}

	agg := AggregateValidation(nil, steps)

	if agg.TotalDurationMS != 4000 {
		t.Errorf("TotalDurationMS: want 4000, got %d", agg.TotalDurationMS)
	}
	if agg.SlowestStageNodeID != "node_slow" {
		t.Errorf("SlowestStageNodeID: want node_slow, got %s", agg.SlowestStageNodeID)
	}
	if agg.StageDurationMS["node_slow"] != 3000 {
		t.Errorf("StageDurationMS[node_slow]: want 3000, got %d", agg.StageDurationMS["node_slow"])
	}
}

func TestValidationAggregateEmptyInputsSafe(t *testing.T) {
	agg := AggregateValidation(nil, nil)
	if agg.CodeCounts == nil || agg.CodesByDomain == nil {
		t.Error("nil inputs must return initialized maps, not nil")
	}
	if agg.TotalChecks != 0 || agg.TotalFailed != 0 {
		t.Error("empty inputs must produce zero counts")
	}
}
