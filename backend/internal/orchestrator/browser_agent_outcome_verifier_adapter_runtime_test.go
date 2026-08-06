package orchestrator

import (
	"cascade-demoops/backend/internal/model"
	"context"
	"testing"
	"time"
)

// TestValidateStageEvents_OutOfOrderEvents tests P0 event ordering detection
func TestValidateStageEvents_OutOfOrderEvents(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-run-order-001",
		SourcePackageID:           "pkg-order-001",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan:         &model.StageApprovalPlan{Stages: []model.StageApprovalStage{}},
		ScriptOutline:             &model.BrowserAgentScriptOutline{},
		BrowserAgentContract:      &model.BrowserAgentContract{},
	}

	// Out-of-order: completed before started
	events := []model.StageExecutionEvent{
		{
			StageID:    "stage-1",
			EventType:  model.StageExecutionEventStageCompleted,
			OccurredAt: time.Now(),
		},
		{
			StageID:    "stage-1",
			EventType:  model.StageExecutionEventStageStarted,
			OccurredAt: time.Now().Add(1 * time.Second),
		},
	}

	ctx := context.Background()
	report, err := adapter.ValidateStageEvents(ctx, vctx, events)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if report.Decision != model.ValidationDecisionStopAndReport {
		t.Errorf("Expected Decision 'stop_and_report' for out-of-order events, got '%s'", report.Decision)
	}

	foundOrderCheck := false
	for _, check := range report.Checks {
		if check.Code == "OUT_OF_ORDER_EVENTS" {
			foundOrderCheck = true
			if check.Passed {
				t.Errorf("Order check should not pass")
			}
			if check.Severity != model.FindingSeverityBlocking {
				t.Errorf("Order check should be blocking, got %s", check.Severity)
			}
		}
	}

	if !foundOrderCheck {
		t.Errorf("Expected OUT_OF_ORDER_EVENTS check")
	}
}

// TestValidateStageEvents_DerivedFromPlanEvidence tests P0 evidence quality rejection
func TestValidateStageEvents_DerivedFromPlanEvidence(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-run-evidence-001",
		SourcePackageID:           "pkg-evidence-001",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan:         &model.StageApprovalPlan{Stages: []model.StageApprovalStage{}},
		ScriptOutline:             &model.BrowserAgentScriptOutline{},
		BrowserAgentContract:      &model.BrowserAgentContract{},
	}

	// Event with derived_from_plan evidence (should be rejected)
	events := []model.StageExecutionEvent{
		{
			StageID:    "stage-1",
			EventType:  model.StageExecutionEventStageStarted,
			OccurredAt: time.Now(),
		},
		{
			StageID:    "stage-1",
			EventType:  model.StageExecutionEventOutcomeObserved,
			OccurredAt: time.Now().Add(1 * time.Second),
			Observation: &model.RuntimeObservation{
				Source: model.RuntimeObservationDerivedPlan, // Invalid!
				URL:    "https://example.com",
			},
		},
	}

	ctx := context.Background()
	report, err := adapter.ValidateStageEvents(ctx, vctx, events)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if report.Decision != model.ValidationDecisionStopAndReport {
		t.Errorf("Expected Decision 'stop_and_report' for derived_from_plan evidence, got '%s'", report.Decision)
	}

	foundEvidenceCheck := false
	for _, check := range report.Checks {
		if check.Code == "DERIVED_FROM_PLAN_EVIDENCE" {
			foundEvidenceCheck = true
			if check.Passed {
				t.Errorf("Evidence quality check should not pass")
			}
		}
	}

	if !foundEvidenceCheck {
		t.Errorf("Expected DERIVED_FROM_PLAN_EVIDENCE check")
	}
}

// TestValidateStageEvents_MissingOutcomeObserved tests P0 missing outcome detection
func TestValidateStageEvents_MissingOutcomeObserved(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-run-outcome-001",
		SourcePackageID:           "pkg-outcome-001",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan:         &model.StageApprovalPlan{Stages: []model.StageApprovalStage{}},
		ScriptOutline:             &model.BrowserAgentScriptOutline{},
		BrowserAgentContract:      &model.BrowserAgentContract{},
	}

	// Completed without outcome_observed
	events := []model.StageExecutionEvent{
		{
			StageID:    "stage-1",
			EventType:  model.StageExecutionEventStageStarted,
			OccurredAt: time.Now(),
		},
		{
			StageID:    "stage-1",
			EventType:  model.StageExecutionEventStageCompleted, // No outcome_observed!
			OccurredAt: time.Now().Add(1 * time.Second),
		},
	}

	ctx := context.Background()
	report, err := adapter.ValidateStageEvents(ctx, vctx, events)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if report.Decision != model.ValidationDecisionStopAndReport {
		t.Errorf("Expected Decision 'stop_and_report' for missing outcome, got '%s'", report.Decision)
	}

	foundOutcomeCheck := false
	for _, check := range report.Checks {
		if check.Code == "MISSING_OUTCOME_OBSERVED" {
			foundOutcomeCheck = true
			if check.Passed {
				t.Errorf("Outcome check should not pass")
			}
		}
	}

	if !foundOutcomeCheck {
		t.Errorf("Expected MISSING_OUTCOME_OBSERVED check")
	}
}

// TestValidateStageEvents_MissingObservation tests P0 missing observation detection
func TestValidateStageEvents_MissingObservation(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-run-obs-001",
		SourcePackageID:           "pkg-obs-001",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan:         &model.StageApprovalPlan{Stages: []model.StageApprovalStage{}},
		ScriptOutline:             &model.BrowserAgentScriptOutline{},
		BrowserAgentContract:      &model.BrowserAgentContract{},
	}

	// outcome_observed without observation field
	events := []model.StageExecutionEvent{
		{
			StageID:    "stage-1",
			EventType:  model.StageExecutionEventStageStarted,
			OccurredAt: time.Now(),
		},
		{
			StageID:     "stage-1",
			EventType:   model.StageExecutionEventOutcomeObserved,
			OccurredAt:  time.Now().Add(1 * time.Second),
			Observation: nil, // Missing!
		},
	}

	ctx := context.Background()
	report, err := adapter.ValidateStageEvents(ctx, vctx, events)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if report.Decision != model.ValidationDecisionStopAndReport {
		t.Errorf("Expected Decision 'stop_and_report' for missing observation, got '%s'", report.Decision)
	}

	foundObsCheck := false
	for _, check := range report.Checks {
		if check.Code == "NO_OBSERVATION_EVIDENCE" {
			foundObsCheck = true
		}
	}

	if !foundObsCheck {
		t.Errorf("Expected NO_OBSERVATION_EVIDENCE check")
	}
}

// TestValidateStageEvents_RequiredAssertionFailed tests P0 assertion failure detection
func TestValidateStageEvents_RequiredAssertionFailed(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-run-assert-001",
		SourcePackageID:           "pkg-assert-001",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan:         &model.StageApprovalPlan{Stages: []model.StageApprovalStage{}},
		ScriptOutline:             &model.BrowserAgentScriptOutline{},
		BrowserAgentContract:      &model.BrowserAgentContract{},
	}

	// Event with failed assertion
	events := []model.StageExecutionEvent{
		{
			StageID:    "stage-1",
			EventType:  model.StageExecutionEventStageStarted,
			OccurredAt: time.Now(),
		},
		{
			StageID:    "stage-1",
			EventType:  model.StageExecutionEventOutcomeObserved,
			OccurredAt: time.Now().Add(1 * time.Second),
			Observation: &model.RuntimeObservation{
				Source: model.RuntimeObservationActualBrowser,
				URL:    "https://example.com",
				Assertions: []model.RuntimeAssertion{
					{
						Kind:   "url_match",
						Passed: false, // Failed!
						Actual: "https://wrong.com",
					},
				},
			},
		},
	}

	ctx := context.Background()
	report, err := adapter.ValidateStageEvents(ctx, vctx, events)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Assertion failures are blocking
	foundAssertCheck := false
	for _, check := range report.Checks {
		if check.Code == "REQUIRED_ASSERTION_FAILED" {
			foundAssertCheck = true
			if check.Passed {
				t.Errorf("Assertion check should not pass")
			}
			if check.Severity != model.FindingSeverityBlocking {
				t.Errorf("Assertion failure should be blocking, got %s", check.Severity)
			}
		}
	}

	if !foundAssertCheck {
		t.Errorf("Expected REQUIRED_ASSERTION_FAILED check")
	}
}

// TestValidateStageEvents_ValidFlow tests P0 passes with valid events
func TestValidateStageEvents_ValidFlow(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-run-valid-001",
		SourcePackageID:           "pkg-valid-001",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan:         &model.StageApprovalPlan{Stages: []model.StageApprovalStage{}},
		ScriptOutline:             &model.BrowserAgentScriptOutline{},
		BrowserAgentContract:      &model.BrowserAgentContract{},
	}

	// Valid event flow
	events := []model.StageExecutionEvent{
		{
			StageID:    "stage-1",
			EventType:  model.StageExecutionEventStageStarted,
			OccurredAt: time.Now(),
		},
		{
			StageID:    "stage-1",
			EventType:  model.StageExecutionEventOutcomeObserved,
			OccurredAt: time.Now().Add(1 * time.Second),
			Observation: &model.RuntimeObservation{
				Source: model.RuntimeObservationActualBrowser,
				URL:    "https://example.com",
				Assertions: []model.RuntimeAssertion{
					{
						Kind:   "url_match",
						Passed: true,
						Actual: "https://example.com",
					},
				},
			},
		},
		{
			StageID:    "stage-1",
			EventType:  model.StageExecutionEventStageCompleted,
			OccurredAt: time.Now().Add(2 * time.Second),
		},
	}

	ctx := context.Background()
	report, err := adapter.ValidateStageEvents(ctx, vctx, events)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Should NOT be stop_and_report for valid flow
	if report.Decision == model.ValidationDecisionStopAndReport {
		// Check if any blocking checks failed
		for _, check := range report.Checks {
			if !check.Passed && check.Severity == model.FindingSeverityBlocking {
				t.Errorf("Valid flow should not have blocking failures: %s - %s", check.Code, check.Summary)
			}
		}
	}

	// Should not have critical P0 violations
	for _, check := range report.Checks {
		if check.Code == "OUT_OF_ORDER_EVENTS" ||
			check.Code == "DERIVED_FROM_PLAN_EVIDENCE" ||
			check.Code == "MISSING_OUTCOME_OBSERVED" ||
			check.Code == "NO_OBSERVATION_EVIDENCE" ||
			check.Code == "REQUIRED_ASSERTION_FAILED" {
			t.Errorf("Valid flow should not trigger P0 check: %s", check.Code)
		}
	}
}

func TestValidateStageEvents_DynamicRouteAssertionsContinueWithoutRepair(t *testing.T) {
	config := &model.ValidationConfig{
		RealTimeBatchEnabled: true,
		EnableRuntimeRepair:  true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)
	now := time.Now()
	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-run-dynamic-route",
		SourcePackageID:           "pkg-dynamic-route",
		SourceBundleHashSHA256:    "bundle-dynamic-route",
		EffectivePolicyHashSHA256: "policy-dynamic-route",
		WorkflowGraph:             &model.DemoWorkflowGraph{},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan: &model.StageApprovalPlan{Stages: []model.StageApprovalStage{{
			ID:                               "stage-start-build",
			NodeID:                           "node-start-build",
			ExpectedRouteAfterAction:         "/project/:id",
			RuntimeRouteVerificationRequired: true,
			SuccessState:                     "passed",
		}}},
		ScriptOutline:        &model.BrowserAgentScriptOutline{},
		BrowserAgentContract: &model.BrowserAgentContract{},
	}
	events := []model.StageExecutionEvent{
		{NodeID: "node-start-build", StageID: "stage-start-build", EventType: model.StageExecutionEventStageStarted, OccurredAt: now},
		{
			NodeID: "node-start-build", StageID: "stage-start-build", EventType: model.StageExecutionEventOutcomeObserved, OccurredAt: now.Add(time.Second),
			Observation: &model.RuntimeObservation{
				Source: model.RuntimeObservationActualBrowser,
				URL:    "https://cascadeai.cn/project/demo-tetris",
				Assertions: []model.RuntimeAssertion{
					{Kind: "action_click_completed", Passed: true, Actual: "true"},
					{Kind: "required_url_matches", Passed: true, Actual: "https://cascadeai.cn/project/demo-tetris"},
				},
			},
		},
		{NodeID: "node-start-build", StageID: "stage-start-build", EventType: model.StageExecutionEventStageCompleted, OccurredAt: now.Add(2 * time.Second)},
	}

	report, err := adapter.ValidateStageEvents(context.Background(), vctx, events)
	if err != nil {
		t.Fatalf("ValidateStageEvents() error = %v", err)
	}
	if report.Decision != model.ValidationDecisionContinue {
		t.Fatalf("decision = %q, want continue; report=%+v", report.Decision, report)
	}
	if report.PassRate != 1 {
		t.Fatalf("pass rate = %v, want 1", report.PassRate)
	}
	if len(report.RepairProposalRefs) != 0 {
		t.Fatalf("successful dynamic route must not request repair: %v", report.RepairProposalRefs)
	}
}

func TestValidateStageEvents_RepairDecisionRequiresConcreteProposal(t *testing.T) {
	config := &model.ValidationConfig{
		RealTimeBatchEnabled: true,
		EnableRuntimeRepair:  true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)
	now := time.Now()
	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-run-warning",
		SourcePackageID:           "pkg-warning",
		SourceBundleHashSHA256:    "bundle-warning",
		EffectivePolicyHashSHA256: "policy-warning",
		WorkflowGraph:             &model.DemoWorkflowGraph{},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan: &model.StageApprovalPlan{Stages: []model.StageApprovalStage{{
			ID: "stage-warning", NodeID: "node-warning", DurationMS: 100,
		}}},
		ScriptOutline:        &model.BrowserAgentScriptOutline{},
		BrowserAgentContract: &model.BrowserAgentContract{},
	}
	events := []model.StageExecutionEvent{
		{NodeID: "node-warning", StageID: "stage-warning", EventType: model.StageExecutionEventStageStarted, OccurredAt: now},
		{NodeID: "node-warning", StageID: "stage-warning", EventType: model.StageExecutionEventOutcomeObserved, OccurredAt: now.Add(time.Second), Observation: &model.RuntimeObservation{Source: model.RuntimeObservationActualBrowser, URL: "https://cascadeai.cn/app"}},
		{NodeID: "node-warning", StageID: "stage-warning", EventType: model.StageExecutionEventStageCompleted, OccurredAt: now.Add(time.Second)},
	}

	report, err := adapter.ValidateStageEvents(context.Background(), vctx, events)
	if err != nil {
		t.Fatalf("ValidateStageEvents() error = %v", err)
	}
	if report.Decision != model.ValidationDecisionContinue {
		t.Fatalf("proposal-less warning decision = %q, want continue", report.Decision)
	}
	if len(report.RepairProposalRefs) != 0 {
		t.Fatalf("unexpected repair proposal refs: %v", report.RepairProposalRefs)
	}
}
