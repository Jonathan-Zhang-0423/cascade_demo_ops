package orchestrator

import (
	"cascade-demoops/backend/internal/model"
	"context"
	"testing"
)

// TestValidateBeforeExecution_MissingHash tests P0 hash verification
func TestValidateBeforeExecution_MissingHash(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	// Missing source bundle hash
	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-run-001",
		SourcePackageID:           "pkg-001",
		SourceBundleHashSHA256:    "", // Missing!
		EffectivePolicyHashSHA256: "def456",
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan:         &model.StageApprovalPlan{Stages: []model.StageApprovalStage{}},
		ScriptOutline:             &model.BrowserAgentScriptOutline{},
		BrowserAgentContract:      &model.BrowserAgentContract{},
	}

	ctx := context.Background()
	report, err := adapter.ValidateBeforeExecution(ctx, vctx)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Should return stop_and_report
	if report.Decision != model.ValidationDecisionStopAndReport {
		t.Errorf("Expected Decision 'stop_and_report', got '%s'", report.Decision)
	}

	// Should have at least one failed check
	if len(report.Checks) == 0 {
		t.Errorf("Expected validation checks, got none")
	}

	foundHashCheck := false
	for _, check := range report.Checks {
		if check.Code == "MISSING_BUNDLE_HASH" {
			foundHashCheck = true
			if check.Passed {
				t.Errorf("Hash check should not pass")
			}
			if !check.Required {
				t.Errorf("Hash check should be required")
			}
			if check.Severity != model.FindingSeverityBlocking {
				t.Errorf("Hash check should be blocking severity, got %s", check.Severity)
			}
		}
	}

	if !foundHashCheck {
		t.Errorf("Expected MISSING_BUNDLE_HASH check")
	}
}

// TestValidateBeforeExecution_MissingPolicyHash tests P0 policy hash verification
func TestValidateBeforeExecution_MissingPolicyHash(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-run-002",
		SourcePackageID:           "pkg-002",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "", // Missing!
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan:         &model.StageApprovalPlan{Stages: []model.StageApprovalStage{}},
		ScriptOutline:             &model.BrowserAgentScriptOutline{},
		BrowserAgentContract:      &model.BrowserAgentContract{},
	}

	ctx := context.Background()
	report, err := adapter.ValidateBeforeExecution(ctx, vctx)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if report.Decision != model.ValidationDecisionStopAndReport {
		t.Errorf("Expected Decision 'stop_and_report', got '%s'", report.Decision)
	}

	foundPolicyCheck := false
	for _, check := range report.Checks {
		if check.Code == "MISSING_POLICY_HASH" {
			foundPolicyCheck = true
			if check.Passed {
				t.Errorf("Policy hash check should not pass")
			}
		}
	}

	if !foundPolicyCheck {
		t.Errorf("Expected MISSING_POLICY_HASH check")
	}
}

// TestValidateBeforeExecution_EmptyApprovalPlan tests P0 plan completeness
func TestValidateBeforeExecution_EmptyApprovalPlan(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-run-003",
		SourcePackageID:           "pkg-003",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan:         nil, // Missing!
		ScriptOutline:             &model.BrowserAgentScriptOutline{},
		BrowserAgentContract:      &model.BrowserAgentContract{},
	}

	ctx := context.Background()
	report, err := adapter.ValidateBeforeExecution(ctx, vctx)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if report.Decision != model.ValidationDecisionStopAndReport {
		t.Errorf("Expected Decision 'stop_and_report', got '%s'", report.Decision)
	}

	foundPlanCheck := false
	for _, check := range report.Checks {
		if check.Code == "EMPTY_STAGE_APPROVAL_PLAN" {
			foundPlanCheck = true
		}
	}

	if !foundPlanCheck {
		t.Errorf("Expected EMPTY_STAGE_APPROVAL_PLAN check")
	}
}

// TestValidateBeforeExecution_MissingScriptOutline tests P0 script outline check
func TestValidateBeforeExecution_MissingScriptOutline(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-run-004",
		SourcePackageID:           "pkg-004",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan:         &model.StageApprovalPlan{Stages: []model.StageApprovalStage{}},
		ScriptOutline:             nil, // Missing!
		BrowserAgentContract:      &model.BrowserAgentContract{},
	}

	ctx := context.Background()
	report, err := adapter.ValidateBeforeExecution(ctx, vctx)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if report.Decision != model.ValidationDecisionStopAndReport {
		t.Errorf("Expected Decision 'stop_and_report', got '%s'", report.Decision)
	}

	foundOutlineCheck := false
	for _, check := range report.Checks {
		if check.Code == "MISSING_SCRIPT_OUTLINE" {
			foundOutlineCheck = true
		}
	}

	if !foundOutlineCheck {
		t.Errorf("Expected MISSING_SCRIPT_OUTLINE check")
	}
}

// TestValidateBeforeExecution_AllHashesPresent tests P0 passes when hashes present
func TestValidateBeforeExecution_AllHashesPresent(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-run-005",
		SourcePackageID:           "pkg-005",
		SourceBundleHashSHA256:    "abc123valid",
		EffectivePolicyHashSHA256: "def456valid",
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan:         &model.StageApprovalPlan{Stages: []model.StageApprovalStage{}},
		ScriptOutline:             &model.BrowserAgentScriptOutline{},
		BrowserAgentContract:      &model.BrowserAgentContract{},
	}

	ctx := context.Background()
	report, err := adapter.ValidateBeforeExecution(ctx, vctx)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Should NOT be stop_and_report (critical checks passed)
	if report.Decision == model.ValidationDecisionStopAndReport {
		// Only check if it's due to hash checks
		for _, check := range report.Checks {
			if check.Code == "MISSING_BUNDLE_HASH" || check.Code == "MISSING_POLICY_HASH" {
				t.Errorf("Should not have hash checks when hashes are present")
			}
		}
	}

	// Should proceed to legacy validation (may have other checks)
	if report.Phase != model.ValidationPhasePreExecution {
		t.Errorf("Expected Phase 'pre_execution', got '%s'", report.Phase)
	}
}
