package orchestrator

import (
	"context"
	"testing"

	"cascade-demoops/backend/internal/model"
)

func TestNewBrowserAgentOutcomeVerifierAdapter(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
		EnableRuntimeRepair:       true,
		AutoApplyMinorRepairs:     false,
		MaxRepairAttemptsPerStage: 2,
		PassRateThreshold:         0.8,
		ConfidenceThreshold:       0.7,
		CriticalIssueThreshold:    1,
		ParallelValidationEnabled: false,
		ValidationTimeoutSeconds:  30,
	}

	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	if adapter == nil {
		t.Fatal("Expected adapter to be created, got nil")
	}

	if adapter.preValidator == nil {
		t.Error("Expected preValidator to be initialized")
	}

	if adapter.postAnalyzer == nil {
		t.Error("Expected postAnalyzer to be initialized")
	}

	if adapter.runtimeVal == nil {
		t.Error("Expected runtimeVal to be initialized")
	}
}

func TestValidateBeforeExecution(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
		EnableRuntimeRepair:       true,
		AutoApplyMinorRepairs:     false,
		MaxRepairAttemptsPerStage: 2,
		PassRateThreshold:         0.8,
		ConfidenceThreshold:       0.7,
		CriticalIssueThreshold:    1,
		ParallelValidationEnabled: false,
		ValidationTimeoutSeconds:  30,
	}

	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	// Minimal validation context
	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-run-001",
		SourcePackageID:           "pkg-001",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan:         &model.StageApprovalPlan{},
		ScriptOutline:             &model.BrowserAgentScriptOutline{},
		BrowserAgentContract:      &model.BrowserAgentContract{},
	}

	ctx := context.Background()
	report, err := adapter.ValidateBeforeExecution(ctx, vctx)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if report.ReportID == "" {
		t.Error("Expected report ID to be set")
	}

	if report.SourcePackageID != "pkg-001" {
		t.Errorf("Expected SourcePackageID 'pkg-001', got '%s'", report.SourcePackageID)
	}

	if report.Phase != "pre_execution" {
		t.Errorf("Expected Phase 'pre_execution', got '%s'", report.Phase)
	}
}

func TestValidateStageEvents(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
		EnableRuntimeRepair:       true,
		AutoApplyMinorRepairs:     false,
		MaxRepairAttemptsPerStage: 2,
		PassRateThreshold:         0.8,
		ConfidenceThreshold:       0.7,
		CriticalIssueThreshold:    1,
		ParallelValidationEnabled: false,
		ValidationTimeoutSeconds:  30,
	}

	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:             "test-run-001",
		SourcePackageID:   "pkg-001",
		WorkflowGraph:     &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		StageApprovalPlan: &model.StageApprovalPlan{Stages: []model.StageApprovalStage{}},
	}

	events := []model.StageExecutionEvent{
		{
			StageID:   "stage-1",
			EventType: model.StageExecutionEventStageStarted,
		},
	}

	ctx := context.Background()
	report, err := adapter.ValidateStageEvents(ctx, vctx, events)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if report.Phase != "runtime_stage" {
		t.Errorf("Expected Phase 'runtime_stage', got '%s'", report.Phase)
	}
}

func TestValidatePostExecution(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
		EnableRuntimeRepair:       true,
		AutoApplyMinorRepairs:     false,
		MaxRepairAttemptsPerStage: 2,
		PassRateThreshold:         0.8,
		ConfidenceThreshold:       0.7,
		CriticalIssueThreshold:    1,
		ParallelValidationEnabled: false,
		ValidationTimeoutSeconds:  30,
	}

	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:           "test-run-001",
		SourcePackageID: "pkg-001",
	}

	result := &model.RecordingResultPackage{
		ResultID:        "result-001",
		SourcePackageID: "pkg-001",
	}

	events := []model.StageExecutionEvent{
		{
			StageID:   "stage-1",
			EventType: model.StageExecutionEventStageCompleted,
		},
	}

	ctx := context.Background()
	report, err := adapter.ValidatePostExecution(ctx, vctx, *result, events)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if report.Phase != "post_execution" {
		t.Errorf("Expected Phase 'post_execution', got '%s'", report.Phase)
	}
}
