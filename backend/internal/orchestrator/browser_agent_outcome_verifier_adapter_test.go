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

func TestValidationReportOrderStability(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	// 构造3-stage场景
	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-order-001",
		SourcePackageID:           "pkg-order-001",
		SourceBundleHashSHA256:    "bundle-hash-123",
		EffectivePolicyHashSHA256: "policy-hash-456",
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan: &model.StageApprovalPlan{
			Stages: []model.StageApprovalStage{
				{NodeID: "stage-1", Order: 1},
				{NodeID: "stage-2", Order: 2},
				{NodeID: "stage-3", Order: 3},
			},
		},
		ScriptOutline: &model.BrowserAgentScriptOutline{
			ID: "outline-1",
		},
		BrowserAgentContract: &model.BrowserAgentContract{},
		AllowedDomains:       []string{"example.com"},
	}

	ctx := context.Background()

	// Phase 1: Pre-execution
	preReport, err := adapter.ValidateBeforeExecution(ctx, vctx)
	if err != nil {
		t.Fatalf("ValidateBeforeExecution failed: %v", err)
	}

	// Phase 2: Runtime - 构造3个stage的事件，每个stage一组事件
	allEvents := []model.StageExecutionEvent{
		{EventType: model.StageExecutionEventStageStarted, StageID: "stage-1", NodeID: "stage-1"},
		{
			EventType: model.StageExecutionEventOutcomeObserved,
			StageID:   "stage-1",
			NodeID:    "stage-1",
			Observation: &model.RuntimeObservation{
				Source: model.RuntimeObservationActualBrowser,
				URL:    "https://example.com/page1",
			},
		},
		{EventType: model.StageExecutionEventStageCompleted, StageID: "stage-1", NodeID: "stage-1"},
		{EventType: model.StageExecutionEventStageStarted, StageID: "stage-2", NodeID: "stage-2"},
		{
			EventType: model.StageExecutionEventOutcomeObserved,
			StageID:   "stage-2",
			NodeID:    "stage-2",
			Observation: &model.RuntimeObservation{
				Source: model.RuntimeObservationActualBrowser,
				URL:    "https://example.com/page2",
			},
		},
		{EventType: model.StageExecutionEventStageCompleted, StageID: "stage-2", NodeID: "stage-2"},
		{EventType: model.StageExecutionEventStageStarted, StageID: "stage-3", NodeID: "stage-3"},
		{
			EventType: model.StageExecutionEventOutcomeObserved,
			StageID:   "stage-3",
			NodeID:    "stage-3",
			Observation: &model.RuntimeObservation{
				Source: model.RuntimeObservationActualBrowser,
				URL:    "https://example.com/page3",
			},
		},
		{EventType: model.StageExecutionEventStageCompleted, StageID: "stage-3", NodeID: "stage-3"},
	}

	// 按stage分组调用ValidateStageEvents，收集runtime reports
	runtimeReports := make([]model.ValidationReport, 0)
	stage1Events := allEvents[0:3]
	stage2Events := allEvents[3:6]
	stage3Events := allEvents[6:9]

	for _, stageEvents := range [][]model.StageExecutionEvent{stage1Events, stage2Events, stage3Events} {
		report, err := adapter.ValidateStageEvents(ctx, vctx, stageEvents)
		if err != nil {
			t.Fatalf("ValidateStageEvents failed: %v", err)
		}
		if report.ReportID != "" {
			runtimeReports = append(runtimeReports, report)
		}
	}

	// Phase 3: Post-execution
	result := model.RecordingResultPackage{
		ResultID:        "result-1",
		SourcePackageID: "pkg-order-001",
		Status:          model.RecordingResultStatusGenerated,
		StepResults: []model.StepResult{
			{NodeID: "stage-1", Status: "passed"},
			{NodeID: "stage-2", Status: "passed"},
			{NodeID: "stage-3", Status: "passed"},
		},
	}

	postReport, err := adapter.ValidatePostExecution(ctx, vctx, result, allEvents)
	if err != nil {
		t.Fatalf("ValidatePostExecution failed: %v", err)
	}

	// 组装完整报告序列
	reports := append([]model.ValidationReport{preReport}, runtimeReports...)
	reports = append(reports, postReport)

	// 断言：至少5个报告 (1 pre + 3 runtime + 1 post)
	if len(reports) < 5 {
		t.Fatalf("Expected at least 5 reports (1 pre + 3 runtime + 1 post), got %d", len(reports))
	}

	// 第1个必须是 pre_execution
	if reports[0].Phase != model.ValidationPhasePreExecution {
		t.Errorf("reports[0].Phase = %v, want pre_execution", reports[0].Phase)
	}

	// 中间N个必须是 runtime_stage，且 NodeID 按执行顺序
	expectedNodeIDs := []string{"stage-1", "stage-2", "stage-3"}
	runtimeCount := len(reports) - 2 // 去掉首尾的pre和post

	for i := 1; i <= runtimeCount; i++ {
		if reports[i].Phase != model.ValidationPhaseRuntimeStage {
			t.Errorf("reports[%d].Phase = %v, want runtime_stage", i, reports[i].Phase)
		}
		// 验证NodeID顺序（假设每个stage产生一个runtime report）
		if i-1 < len(expectedNodeIDs) && reports[i].NodeID != expectedNodeIDs[i-1] {
			t.Errorf("reports[%d].NodeID = %v, want %v", i, reports[i].NodeID, expectedNodeIDs[i-1])
		}
	}

	// 最后1个必须是 post_execution
	lastIdx := len(reports) - 1
	if reports[lastIdx].Phase != model.ValidationPhasePostExecution {
		t.Errorf("reports[%d].Phase = %v, want post_execution", lastIdx, reports[lastIdx].Phase)
	}

	t.Logf("✅ ValidationReport order stable: 1 pre + %d runtime + 1 post", runtimeCount)
}
