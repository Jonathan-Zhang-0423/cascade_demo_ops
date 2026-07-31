package orchestrator

import (
	"cascade-demoops/backend/internal/model"
	"context"
	"testing"
	"time"
)

// P2 Integration Tests: Real package validation with OutcomeVerifier
// These tests use realistic RecordingResultPackage structures to verify
// the complete validation flow from pre-execution through post-execution.

// TestIntegration_SuccessPackage tests P2 requirement: 1 success case with complete artifacts and evidence
func TestIntegration_SuccessPackage(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
		EnableRuntimeRepair:       false,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	// Complete valid execution context
	vctx := model.BrowserAgentValidationContext{
		RunID:                     "integration-success-001",
		SourcePackageID:           "pkg-success-001",
		SourceBundleHashSHA256:    "sha256-success-bundle",
		EffectivePolicyHashSHA256: "sha256-success-policy",
		WorkflowGraph: &model.DemoWorkflowGraph{
			Nodes: []*model.GraphNode{
				{ID: "node-1", Type: "action", Title: "Login"},
			},
		},
		Plan: &model.ExecutionScriptDocument{
			Title: "Login Test Plan",
		},
		StageApprovalPlan: &model.StageApprovalPlan{
			Stages: []model.StageApprovalStage{
				{NodeID: "stage-login", Order: 1, Title: "Login Stage"},
			},
		},
		ScriptOutline: &model.BrowserAgentScriptOutline{
			Runtime: "browser-agent-outline-v1",
		},
		BrowserAgentContract: &model.BrowserAgentContract{
			RepairPolicy: model.BrowserAgentRepairPolicy{
				AllowedRepairKinds: []string{},
			},
		},
	}

	ctx := context.Background()

	// P2 Step 1: ValidateBeforeExecution should pass
	preReport, err := adapter.ValidateBeforeExecution(ctx, vctx)
	if err != nil {
		t.Fatalf("Pre-execution validation error: %v", err)
	}
	if preReport.Decision == model.ValidationDecisionStopAndReport {
		t.Errorf("Pre-execution should not block for valid context")
		for _, check := range preReport.Checks {
			if !check.Passed && check.Severity == model.FindingSeverityBlocking {
				t.Errorf("  Blocking check: %s - %s", check.Code, check.Summary)
			}
		}
	}

	// P2 Step 2: Complete stage events with real browser observations
	events := []model.StageExecutionEvent{
		{
			StageID:    "stage-login",
			EventType:  model.StageExecutionEventStageStarted,
			OccurredAt: time.Now(),
		},
		{
			StageID:   "stage-login",
			EventType: model.StageExecutionEventOutcomeObserved,
			OccurredAt: time.Now().Add(2 * time.Second),
			Observation: &model.RuntimeObservation{
				Source: model.RuntimeObservationActualBrowser,
				URL:    "https://example.com/dashboard",
				Assertions: []model.RuntimeAssertion{
					{
						Kind:   "url_match",
						Passed: true,
						Actual: "https://example.com/dashboard",
					},
					{
						Kind:   "element_visible",
						Passed: true,
						Actual: "#user-menu",
					},
				},
			},
			EvidenceRefs: []model.EvidenceRef{
				{ID: "screenshot-login-001", Kind: "screenshot"},
			},
		},
		{
			StageID:    "stage-login",
			EventType:  model.StageExecutionEventStageCompleted,
			OccurredAt: time.Now().Add(3 * time.Second),
		},
	}

	// P2 Step 3: ValidateStageEvents should pass
	stageReport, err := adapter.ValidateStageEvents(ctx, vctx, events)
	if err != nil {
		t.Fatalf("Stage events validation error: %v", err)
	}
	if stageReport.Decision == model.ValidationDecisionStopAndReport {
		t.Errorf("Stage validation should not block for valid events")
		for _, check := range stageReport.Checks {
			if !check.Passed && check.Severity == model.FindingSeverityBlocking {
				t.Errorf("  Blocking check: %s - %s", check.Code, check.Summary)
			}
		}
	}

	// P2 Step 4: Complete result package with all artifacts
	result := &model.RecordingResultPackage{
		ResultID:        "result-success-001",
		SourcePackageID: "pkg-success-001",
		Status:          model.RecordingResultStatusGenerated,
		GeneratedAssets: []model.ArtifactRef{
			{ID: "screenshot-login-001", Kind: "screenshot", URI: "s3://bucket/screenshots/login-001.png"},
			{ID: "trace-001", Kind: "trace", URI: "s3://bucket/traces/trace-001.zip"},
			{ID: "video-001", Kind: "video", URI: "s3://bucket/videos/recording-001.mp4"},
		},
		StageEventLogRef: &model.ArtifactRef{
			ID:   "events-log-001",
			Kind: "stage_event_log",
			URI:  "s3://bucket/logs/events-001.jsonl",
		},
	}

	// P2 Step 5: ValidatePostExecution should pass
	postReport, err := adapter.ValidatePostExecution(ctx, vctx, *result, events)
	if err != nil {
		t.Fatalf("Post-execution validation error: %v", err)
	}

	// Success package should not have blocking issues
	hasBlockingIssues := false
	for _, check := range postReport.Checks {
		if !check.Passed && check.Severity == model.FindingSeverityBlocking {
			hasBlockingIssues = true
			t.Errorf("Success package should not have blocking issues: %s - %s", check.Code, check.Summary)
		}
	}

	if hasBlockingIssues && postReport.Decision == model.ValidationDecisionStopAndReport {
		t.Errorf("Success package should not be blocked")
	}

	// Verify evidence quality is good
	if postReport.EvidenceQuality == model.RuntimeObservationDerivedPlan {
		t.Errorf("Success package should not have derived_from_plan evidence quality, got %s", postReport.EvidenceQuality)
	}
}

// TestIntegration_LocatorMissingPackage tests P2 requirement: 1 locator-missing case
func TestIntegration_LocatorMissingPackage(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
		EnableRuntimeRepair:       true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "integration-locator-001",
		SourcePackageID:           "pkg-locator-001",
		SourceBundleHashSHA256:    "sha256-locator-bundle",
		EffectivePolicyHashSHA256: "sha256-locator-policy",
		WorkflowGraph: &model.DemoWorkflowGraph{
			Nodes: []*model.GraphNode{
				{ID: "node-1", Type: "action", Title: "Click Button"},
			},
		},
		Plan: &model.ExecutionScriptDocument{
			Title: "Click Button Plan",
		},
		StageApprovalPlan: &model.StageApprovalPlan{
			Stages: []model.StageApprovalStage{
				{NodeID: "stage-click", Order: 1, Title: "Click Stage"},
			},
		},
		ScriptOutline: &model.BrowserAgentScriptOutline{
			Runtime: "browser-agent-outline-v1",
		},
		BrowserAgentContract: &model.BrowserAgentContract{
			RepairPolicy: model.BrowserAgentRepairPolicy{
				AllowedRepairKinds: []string{"selector_alternative"},
				EditableFields:     []string{"selector"},
			},
		},
	}

	ctx := context.Background()

	// Pre-execution passes
	_, err := adapter.ValidateBeforeExecution(ctx, vctx)
	if err != nil {
		t.Fatalf("Pre-execution validation error: %v", err)
	}

	// Stage events show selector not found (using stage_failed event)
	events := []model.StageExecutionEvent{
		{
			StageID:    "stage-click",
			EventType:  model.StageExecutionEventStageStarted,
			OccurredAt: time.Now(),
		},
		{
			StageID:    "stage-click",
			EventType:  model.StageExecutionEventStageFailed,
			OccurredAt: time.Now().Add(1 * time.Second),
			Observation: &model.RuntimeObservation{
				Source: model.RuntimeObservationActualBrowser,
				URL:    "https://example.com/form",
				Title:  "Selector #submit-button not found on page",
			},
		},
	}

	// ValidateStageEvents should detect the blocker
	stageReport, err := adapter.ValidateStageEvents(ctx, vctx, events)
	if err != nil {
		t.Fatalf("Stage events validation error: %v", err)
	}

	// Should be blocked
	if stageReport.Decision != model.ValidationDecisionStopAndReport {
		t.Errorf("Locator missing should result in stop_and_report, got %s", stageReport.Decision)
	}

	// Should have blocking check for missing selector
	foundSelectorCheck := false
	for _, check := range stageReport.Checks {
		if check.Code == "SELECTOR_NOT_FOUND" || check.Code == "STAGE_FAILED" {
			foundSelectorCheck = true
			if check.Severity != model.FindingSeverityBlocking {
				t.Errorf("Selector not found should be blocking, got %s", check.Severity)
			}
		}
	}
	if !foundSelectorCheck {
		t.Errorf("Expected blocking check for selector not found")
	}

	// Should have repair proposals (policy allows selector_alternative)
	if len(stageReport.RepairProposalRefs) == 0 {
		t.Errorf("Expected repair proposals for selector issue when policy allows it")
	}
}

// TestIntegration_RequiredValidationFailure tests P2 requirement: 1 required validation failure case
func TestIntegration_RequiredValidationFailure(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
		EnableRuntimeRepair:       false,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "integration-validation-001",
		SourcePackageID:           "pkg-validation-001",
		SourceBundleHashSHA256:    "sha256-validation-bundle",
		EffectivePolicyHashSHA256: "sha256-validation-policy",
		WorkflowGraph: &model.DemoWorkflowGraph{
			Nodes: []*model.GraphNode{
				{ID: "node-1", Type: "action", Title: "Verify Payment"},
			},
		},
		Plan: &model.ExecutionScriptDocument{
			Title: "Payment Verification Plan",
		},
		StageApprovalPlan: &model.StageApprovalPlan{
			Stages: []model.StageApprovalStage{
				{NodeID: "stage-payment", Order: 1, Title: "Payment Stage"},
			},
		},
		ScriptOutline: &model.BrowserAgentScriptOutline{
			Runtime: "browser-agent-outline-v1",
		},
		BrowserAgentContract: &model.BrowserAgentContract{
			RepairPolicy: model.BrowserAgentRepairPolicy{
				AllowedRepairKinds: []string{},
			},
		},
	}

	ctx := context.Background()

	// Pre-execution passes
	_, err := adapter.ValidateBeforeExecution(ctx, vctx)
	if err != nil {
		t.Fatalf("Pre-execution validation error: %v", err)
	}

	// Stage events with failed required assertion
	events := []model.StageExecutionEvent{
		{
			StageID:    "stage-payment",
			EventType:  model.StageExecutionEventStageStarted,
			OccurredAt: time.Now(),
		},
		{
			StageID:   "stage-payment",
			EventType: model.StageExecutionEventOutcomeObserved,
			OccurredAt: time.Now().Add(2 * time.Second),
			Observation: &model.RuntimeObservation{
				Source: model.RuntimeObservationActualBrowser,
				URL:    "https://shop.example.com/checkout",
				Assertions: []model.RuntimeAssertion{
					{
						Kind:   "payment_success",
						Passed: false, // Required assertion failed!
						Actual: "Payment declined (expected: Payment completed)",
					},
					{
						Kind:   "receipt_visible",
						Passed: false,
						Actual: "Receipt element not found",
					},
				},
			},
			EvidenceRefs: []model.EvidenceRef{
				{ID: "screenshot-payment-fail-001", Kind: "screenshot"},
			},
		},
		{
			StageID:    "stage-payment",
			EventType:  model.StageExecutionEventStageCompleted,
			OccurredAt: time.Now().Add(3 * time.Second),
		},
	}

	// ValidateStageEvents should detect required assertion failures
	stageReport, err := adapter.ValidateStageEvents(ctx, vctx, events)
	if err != nil {
		t.Fatalf("Stage events validation error: %v", err)
	}

	// Should be blocked due to required assertion failures
	if stageReport.Decision != model.ValidationDecisionStopAndReport {
		t.Errorf("Required validation failure should result in stop_and_report, got %s", stageReport.Decision)
	}

	// Should have blocking checks for failed assertions
	foundAssertionFailure := false
	for _, check := range stageReport.Checks {
		if check.Code == "REQUIRED_ASSERTION_FAILED" {
			foundAssertionFailure = true
			if check.Severity != model.FindingSeverityBlocking {
				t.Errorf("Required assertion failure should be blocking, got %s", check.Severity)
			}
		}
	}
	if !foundAssertionFailure {
		t.Errorf("Expected REQUIRED_ASSERTION_FAILED check")
	}

	// Complete result package
	result := &model.RecordingResultPackage{
		ResultID:        "result-validation-001",
		SourcePackageID: "pkg-validation-001",
		Status:          model.RecordingResultStatusGenerated,
		GeneratedAssets: []model.ArtifactRef{
			{ID: "screenshot-payment-fail-001", Kind: "screenshot", URI: "s3://bucket/screenshots/payment-fail.png"},
			{ID: "trace-001", Kind: "trace", URI: "s3://bucket/traces/trace-001.zip"},
			{ID: "video-001", Kind: "video", URI: "s3://bucket/videos/recording-001.mp4"},
		},
		StageEventLogRef: &model.ArtifactRef{
			ID:   "events-log-001",
			Kind: "stage_event_log",
			URI:  "s3://bucket/logs/events-001.jsonl",
		},
	}

	// ValidatePostExecution should also detect the failure
	postReport, err := adapter.ValidatePostExecution(ctx, vctx, *result, events)
	if err != nil {
		t.Fatalf("Post-execution validation error: %v", err)
	}

	// Should be blocked
	if postReport.Decision != model.ValidationDecisionStopAndReport {
		t.Errorf("Required validation failure should block post-execution, got %s", postReport.Decision)
	}

	// Evidence quality should reflect the failure
	if postReport.EvidenceQuality == model.RuntimeObservationActualBrowser && postReport.Decision != model.ValidationDecisionStopAndReport {
		t.Errorf("Failed validation with browser evidence should still be blocked")
	}
}

// TestIntegration_TimeoutPackage tests P2 requirement: 1 timeout case
func TestIntegration_TimeoutPackage(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
		EnableRuntimeRepair:       true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "integration-timeout-001",
		SourcePackageID:           "pkg-timeout-001",
		SourceBundleHashSHA256:    "sha256-timeout-bundle",
		EffectivePolicyHashSHA256: "sha256-timeout-policy",
		WorkflowGraph: &model.DemoWorkflowGraph{
			Nodes: []*model.GraphNode{
				{ID: "node-1", Type: "action", Title: "Load Slow Page"},
			},
		},
		Plan: &model.ExecutionScriptDocument{
			Title: "Slow Page Load Plan",
		},
		StageApprovalPlan: &model.StageApprovalPlan{
			Stages: []model.StageApprovalStage{
				{NodeID: "stage-load", Order: 1, Title: "Load Stage"},
				{NodeID: "stage-verify", Order: 2, Title: "Verify Stage"},
			},
		},
		ScriptOutline: &model.BrowserAgentScriptOutline{
			Runtime: "browser-agent-outline-v1",
		},
		BrowserAgentContract: &model.BrowserAgentContract{
			RepairPolicy: model.BrowserAgentRepairPolicy{
				AllowedRepairKinds:     []string{"wait_strategy"},
				EditableFields:         []string{"wait_timeout"},
				MinAutoApplyConfidence: 0.7,
			},
		},
	}

	ctx := context.Background()

	// Pre-execution passes
	_, err := adapter.ValidateBeforeExecution(ctx, vctx)
	if err != nil {
		t.Fatalf("Pre-execution validation error: %v", err)
	}

	// Stage events showing wait timeout (using stage_failed event)
	events := []model.StageExecutionEvent{
		{
			StageID:    "stage-load",
			EventType:  model.StageExecutionEventStageStarted,
			OccurredAt: time.Now(),
		},
		{
			StageID:    "stage-load",
			EventType:  model.StageExecutionEventStageFailed,
			OccurredAt: time.Now().Add(5 * time.Second),
			Observation: &model.RuntimeObservation{
				Source: model.RuntimeObservationActualBrowser,
				URL:    "https://slow.example.com",
				Title:  "Timeout waiting for element #content after 5000ms",
			},
		},
	}

	// ValidateStageEvents should detect timeout and offer repair
	stageReport, err := adapter.ValidateStageEvents(ctx, vctx, events)
	if err != nil {
		t.Fatalf("Stage events validation error: %v", err)
	}

	// Should be blocked
	if stageReport.Decision != model.ValidationDecisionStopAndReport {
		t.Errorf("Timeout should result in stop_and_report, got %s", stageReport.Decision)
	}

	// Should have blocking check for timeout
	foundTimeoutCheck := false
	for _, check := range stageReport.Checks {
		if check.Code == "WAIT_TIMEOUT" || check.Code == "STAGE_FAILED" {
			foundTimeoutCheck = true
			if check.Severity != model.FindingSeverityBlocking {
				t.Errorf("Timeout should be blocking, got %s", check.Severity)
			}
		}
	}
	if !foundTimeoutCheck {
		t.Errorf("Expected blocking check for timeout")
	}

	// Should have repair proposals (policy allows wait_strategy)
	if len(stageReport.RepairProposalRefs) == 0 {
		t.Errorf("Expected repair proposals for timeout when policy allows wait_strategy")
	}

	// Incomplete result package (stage-verify never completed)
	result := &model.RecordingResultPackage{
		ResultID:        "result-timeout-001",
		SourcePackageID: "pkg-timeout-001",
		Status:          model.RecordingResultStatusGenerated,
		GeneratedAssets: []model.ArtifactRef{
			{ID: "screenshot-timeout-001", Kind: "screenshot", URI: "s3://bucket/screenshots/timeout.png"},
			{ID: "trace-001", Kind: "trace", URI: "s3://bucket/traces/trace-001.zip"},
			{ID: "video-001", Kind: "video", URI: "s3://bucket/videos/recording-001.mp4"},
		},
		StageEventLogRef: &model.ArtifactRef{
			ID:   "events-log-001",
			Kind: "stage_event_log",
			URI:  "s3://bucket/logs/events-001.jsonl",
		},
	}

	// ValidatePostExecution should detect incomplete stages
	postReport, err := adapter.ValidatePostExecution(ctx, vctx, *result, events)
	if err != nil {
		t.Fatalf("Post-execution validation error: %v", err)
	}

	// Should be blocked due to incomplete stages
	if postReport.Decision != model.ValidationDecisionStopAndReport {
		t.Errorf("Incomplete execution should be blocked, got %s", postReport.Decision)
	}

	// Should detect missing stage completion
	foundIncompleteCheck := false
	for _, check := range postReport.Checks {
		if check.Code == "REQUIRED_STAGE_NOT_COMPLETED" {
			foundIncompleteCheck = true
			if check.Severity != model.FindingSeverityBlocking {
				t.Errorf("Incomplete stage should be blocking, got %s", check.Severity)
			}
		}
	}
	if !foundIncompleteCheck {
		t.Errorf("Expected REQUIRED_STAGE_NOT_COMPLETED check for stage-verify")
	}
}
