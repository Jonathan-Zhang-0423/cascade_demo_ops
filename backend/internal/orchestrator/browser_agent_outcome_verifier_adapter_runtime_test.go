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

func TestValidateStageEvents_AppApprovedDynamicRoute(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)
	now := time.Now()

	baseContext := model.BrowserAgentValidationContext{
		RunID:                     "test-run-dynamic-route-001",
		SourcePackageID:           "pkg-dynamic-route-001",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan: &model.StageApprovalPlan{Stages: []model.StageApprovalStage{{
			ID:                               "step-4",
			NodeID:                           "business_stage_start_agent_build",
			ExpectedRouteAfterAction:         "/project/:id",
			RuntimeRouteVerificationRequired: true,
		}}},
		ScriptOutline:        &model.BrowserAgentScriptOutline{},
		BrowserAgentContract: &model.BrowserAgentContract{},
	}

	stageEvents := func(observedURL string) []model.StageExecutionEvent {
		observation := &model.RuntimeObservation{
			Source: model.RuntimeObservationAssertion,
			URL:    observedURL,
			Assertions: []model.RuntimeAssertion{
				{Kind: "action_click_completed", Passed: true, Actual: "semantic_build"},
				{Kind: "required_element_visible:validate_build", Passed: true, Actual: "expected_route_after_action_verified"},
			},
		}
		return []model.StageExecutionEvent{
			{NodeID: "business_stage_start_agent_build", StageID: "stage-step-4", EventType: model.StageExecutionEventStageStarted, OccurredAt: now},
			{NodeID: "business_stage_start_agent_build", StageID: "stage-step-4", EventType: model.StageExecutionEventOutcomeObserved, OccurredAt: now.Add(time.Second), Observation: observation},
			{NodeID: "business_stage_start_agent_build", StageID: "stage-step-4", EventType: model.StageExecutionEventStageCompleted, OccurredAt: now.Add(2 * time.Second), Observation: observation},
		}
	}

	t.Run("real dynamic route continues", func(t *testing.T) {
		report, err := adapter.ValidateStageEvents(context.Background(), baseContext, stageEvents("http://127.0.0.1:5000/project/elhq3xeomsp50778"))
		if err != nil {
			t.Fatalf("ValidateStageEvents returned error: %v", err)
		}
		if report.Decision != model.ValidationDecisionContinue || report.PassRate != 1 {
			t.Fatalf("dynamic route report = decision %q pass_rate %.2f, want continue/1", report.Decision, report.PassRate)
		}
	})

	t.Run("wrong route still requires repair", func(t *testing.T) {
		report, err := adapter.ValidateStageEvents(context.Background(), baseContext, stageEvents("http://127.0.0.1:5000/app"))
		if err != nil {
			t.Fatalf("ValidateStageEvents returned error: %v", err)
		}
		if report.Decision != model.ValidationDecisionRepairAllowed || report.PassRate >= 1 {
			t.Fatalf("wrong route report = decision %q pass_rate %.2f, want repair_allowed/<1", report.Decision, report.PassRate)
		}
	})
}

// TestValidateStageEvents_CrossDomainAccess tests scenario 6: the agent
// observed a page outside the approved allowed_domains list.
func TestValidateStageEvents_CrossDomainAccess(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-run-domain-001",
		SourcePackageID:           "pkg-domain-001",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
		AllowedDomains:            []string{"127.0.0.1"},
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan:         &model.StageApprovalPlan{Stages: []model.StageApprovalStage{}},
		ScriptOutline:             &model.BrowserAgentScriptOutline{},
		BrowserAgentContract:      &model.BrowserAgentContract{},
	}

	events := []model.StageExecutionEvent{
		{
			StageID:    "stage-1",
			NodeID:     "node-1",
			EventType:  model.StageExecutionEventOutcomeObserved,
			OccurredAt: time.Now(),
			Observation: &model.RuntimeObservation{
				Source: model.RuntimeObservationActualBrowser,
				URL:    "https://evil.example.com/dashboard",
			},
			EvidenceRefs: []model.EvidenceRef{{ID: "ev-1", Kind: "screenshot"}},
		},
	}

	ctx := context.Background()
	report, err := adapter.ValidateStageEvents(ctx, vctx, events)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if report.Decision != model.ValidationDecisionStopAndReport {
		t.Errorf("Expected Decision 'stop_and_report' for cross-domain access, got '%s'", report.Decision)
	}

	foundCheck := false
	for _, check := range report.Checks {
		if check.Code == "CROSS_DOMAIN_ACCESS" {
			foundCheck = true
			if check.Passed {
				t.Errorf("Cross-domain check should not pass")
			}
			if check.Severity != model.FindingSeverityBlocking {
				t.Errorf("Cross-domain check should be blocking, got %s", check.Severity)
			}
			if check.NodeID != "node-1" || check.StageID != "stage-1" {
				t.Errorf("Expected check to carry NodeID/StageID, got NodeID=%q StageID=%q", check.NodeID, check.StageID)
			}
		}
	}

	if !foundCheck {
		t.Errorf("Expected CROSS_DOMAIN_ACCESS check")
	}
}

// TestValidateStageEvents_ForbiddenPageAccess tests scenario 6: the agent
// observed a page whose path matches a forbidden path prefix from the
// approved ScriptOutline.AllowedExplorationScope.
func TestValidateStageEvents_ForbiddenPageAccess(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-run-forbidden-001",
		SourcePackageID:           "pkg-forbidden-001",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
		AllowedDomains:            []string{"127.0.0.1"},
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan:         &model.StageApprovalPlan{Stages: []model.StageApprovalStage{}},
		ScriptOutline: &model.BrowserAgentScriptOutline{
			AllowedExplorationScope: model.BrowserAgentExplorationScope{
				AllowedOrigins:        []string{"http://127.0.0.1:5100"},
				ForbiddenPathPrefixes: []string{"/billing", "/admin"},
			},
		},
		BrowserAgentContract: &model.BrowserAgentContract{},
	}

	events := []model.StageExecutionEvent{
		{
			StageID:    "stage-1",
			NodeID:     "node-1",
			EventType:  model.StageExecutionEventOutcomeObserved,
			OccurredAt: time.Now(),
			Observation: &model.RuntimeObservation{
				Source: model.RuntimeObservationActualBrowser,
				URL:    "http://127.0.0.1:5100/billing/invoices",
			},
			EvidenceRefs: []model.EvidenceRef{{ID: "ev-1", Kind: "screenshot"}},
		},
	}

	ctx := context.Background()
	report, err := adapter.ValidateStageEvents(ctx, vctx, events)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if report.Decision != model.ValidationDecisionStopAndReport {
		t.Errorf("Expected Decision 'stop_and_report' for forbidden-page access, got '%s'", report.Decision)
	}

	foundCheck := false
	for _, check := range report.Checks {
		if check.Code == "FORBIDDEN_PAGE_ACCESS" {
			foundCheck = true
			if check.Passed {
				t.Errorf("Forbidden-page check should not pass")
			}
			if check.Severity != model.FindingSeverityBlocking {
				t.Errorf("Forbidden-page check should be blocking, got %s", check.Severity)
			}
		}
	}

	if !foundCheck {
		t.Errorf("Expected FORBIDDEN_PAGE_ACCESS check")
	}
}

// TestValidateStageEvents_CrossDomainAccess_AllowedDomainNoViolation is a
// true-negative counterpart to TestValidateStageEvents_CrossDomainAccess: an
// observed URL whose host is exactly on the allowed-domains list must not
// raise CROSS_DOMAIN_ACCESS.
func TestValidateStageEvents_CrossDomainAccess_AllowedDomainNoViolation(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-run-domain-ok-001",
		SourcePackageID:           "pkg-domain-ok-001",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
		AllowedDomains:            []string{"127.0.0.1"},
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan:         &model.StageApprovalPlan{Stages: []model.StageApprovalStage{}},
		ScriptOutline:             &model.BrowserAgentScriptOutline{},
		BrowserAgentContract:      &model.BrowserAgentContract{},
	}

	events := []model.StageExecutionEvent{
		{
			StageID:    "stage-1",
			NodeID:     "node-1",
			EventType:  model.StageExecutionEventStageStarted,
			OccurredAt: time.Now(),
		},
		{
			StageID:    "stage-1",
			NodeID:     "node-1",
			EventType:  model.StageExecutionEventOutcomeObserved,
			OccurredAt: time.Now().Add(1 * time.Second),
			Observation: &model.RuntimeObservation{
				Source: model.RuntimeObservationActualBrowser,
				URL:    "https://127.0.0.1/dashboard",
				Assertions: []model.RuntimeAssertion{
					{
						Kind:   "url_match",
						Passed: true,
						Actual: "https://127.0.0.1/dashboard",
					},
				},
			},
			EvidenceRefs: []model.EvidenceRef{{ID: "ev-1", Kind: "screenshot"}},
		},
		{
			StageID:    "stage-1",
			NodeID:     "node-1",
			EventType:  model.StageExecutionEventStageCompleted,
			OccurredAt: time.Now().Add(2 * time.Second),
		},
	}

	ctx := context.Background()
	report, err := adapter.ValidateStageEvents(ctx, vctx, events)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if report.Decision == model.ValidationDecisionStopAndReport {
		t.Errorf("Expected decision other than 'stop_and_report' for allowed-domain URL, got '%s'", report.Decision)
	}

	for _, check := range report.Checks {
		if check.Code == "CROSS_DOMAIN_ACCESS" {
			t.Errorf("Did not expect CROSS_DOMAIN_ACCESS check for URL within allowed domains, got: %+v", check)
		}
	}
}

// TestValidateStageEvents_ForbiddenPageAccess_SimilarPathNotForbidden is a
// true-negative counterpart to TestValidateStageEvents_ForbiddenPageAccess:
// a path that merely starts with the same characters as a forbidden prefix
// (but is not that path, nor a sub-path of it) must not raise
// FORBIDDEN_PAGE_ACCESS. This guards against naive strings.HasPrefix
// matching, which would false-positive "/billing-faq" against "/billing".
func TestValidateStageEvents_ForbiddenPageAccess_SimilarPathNotForbidden(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-run-forbidden-ok-001",
		SourcePackageID:           "pkg-forbidden-ok-001",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
		AllowedDomains:            []string{"127.0.0.1"},
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan:         &model.StageApprovalPlan{Stages: []model.StageApprovalStage{}},
		ScriptOutline: &model.BrowserAgentScriptOutline{
			AllowedExplorationScope: model.BrowserAgentExplorationScope{
				AllowedOrigins:        []string{"http://127.0.0.1:5100"},
				ForbiddenPathPrefixes: []string{"/billing", "/admin"},
			},
		},
		BrowserAgentContract: &model.BrowserAgentContract{},
	}

	events := []model.StageExecutionEvent{
		{
			StageID:    "stage-1",
			NodeID:     "node-1",
			EventType:  model.StageExecutionEventStageStarted,
			OccurredAt: time.Now(),
		},
		{
			StageID:    "stage-1",
			NodeID:     "node-1",
			EventType:  model.StageExecutionEventOutcomeObserved,
			OccurredAt: time.Now().Add(1 * time.Second),
			Observation: &model.RuntimeObservation{
				Source: model.RuntimeObservationActualBrowser,
				URL:    "http://127.0.0.1/billing-faq",
				Assertions: []model.RuntimeAssertion{
					{
						Kind:   "url_match",
						Passed: true,
						Actual: "http://127.0.0.1/billing-faq",
					},
				},
			},
			EvidenceRefs: []model.EvidenceRef{{ID: "ev-1", Kind: "screenshot"}},
		},
		{
			StageID:    "stage-1",
			NodeID:     "node-1",
			EventType:  model.StageExecutionEventStageCompleted,
			OccurredAt: time.Now().Add(2 * time.Second),
		},
	}

	ctx := context.Background()
	report, err := adapter.ValidateStageEvents(ctx, vctx, events)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if report.Decision == model.ValidationDecisionStopAndReport {
		t.Errorf("Expected decision other than 'stop_and_report' for /billing-faq path, got '%s'", report.Decision)
	}

	for _, check := range report.Checks {
		if check.Code == "FORBIDDEN_PAGE_ACCESS" {
			t.Errorf("Did not expect FORBIDDEN_PAGE_ACCESS check for /billing-faq (distinct from forbidden prefix /billing), got: %+v", check)
		}
	}
}

// TestValidateStageEvents_CrossDomainAccess_AllowlistPortIsStripped is a
// true-negative counterpart to TestValidateStageEvents_CrossDomainAccess: an
// allowed-domains entry that carries an explicit port (as approved packages
// commonly do for local dev targets, e.g. "127.0.0.1:5100") must still match
// an observed URL on that host. net/url's Hostname() is always port-free, so
// a bare equality check against a port-carrying allowlist entry would never
// match.
func TestValidateStageEvents_CrossDomainAccess_AllowlistPortIsStripped(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-run-domain-port-001",
		SourcePackageID:           "pkg-domain-port-001",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
		AllowedDomains:            []string{"127.0.0.1:5100"},
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan:         &model.StageApprovalPlan{Stages: []model.StageApprovalStage{}},
		ScriptOutline:             &model.BrowserAgentScriptOutline{},
		BrowserAgentContract:      &model.BrowserAgentContract{},
	}

	events := []model.StageExecutionEvent{
		{
			StageID:    "stage-1",
			NodeID:     "node-1",
			EventType:  model.StageExecutionEventStageStarted,
			OccurredAt: time.Now(),
		},
		{
			StageID:    "stage-1",
			NodeID:     "node-1",
			EventType:  model.StageExecutionEventOutcomeObserved,
			OccurredAt: time.Now().Add(1 * time.Second),
			Observation: &model.RuntimeObservation{
				Source: model.RuntimeObservationActualBrowser,
				URL:    "http://127.0.0.1:5100/dashboard",
				Assertions: []model.RuntimeAssertion{
					{
						Kind:   "url_match",
						Passed: true,
						Actual: "http://127.0.0.1:5100/dashboard",
					},
				},
			},
			EvidenceRefs: []model.EvidenceRef{{ID: "ev-1", Kind: "screenshot"}},
		},
		{
			StageID:    "stage-1",
			NodeID:     "node-1",
			EventType:  model.StageExecutionEventStageCompleted,
			OccurredAt: time.Now().Add(2 * time.Second),
		},
	}

	ctx := context.Background()
	report, err := adapter.ValidateStageEvents(ctx, vctx, events)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if report.Decision == model.ValidationDecisionStopAndReport {
		t.Errorf("Expected decision other than 'stop_and_report' for port-carrying allowlist entry, got '%s'", report.Decision)
	}

	for _, check := range report.Checks {
		if check.Code == "CROSS_DOMAIN_ACCESS" {
			t.Errorf("Did not expect CROSS_DOMAIN_ACCESS check when allowlist entry's port should be stripped before matching, got: %+v", check)
		}
	}
}

// TestValidateStageEvents_CrossDomainAccess_CaseInsensitiveHostMatch is a
// true-negative counterpart to TestValidateStageEvents_CrossDomainAccess: a
// mixed-case observed host must still match a lowercase allowed-domains
// entry.
func TestValidateStageEvents_CrossDomainAccess_CaseInsensitiveHostMatch(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-run-domain-case-001",
		SourcePackageID:           "pkg-domain-case-001",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
		AllowedDomains:            []string{"example.com"},
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan:         &model.StageApprovalPlan{Stages: []model.StageApprovalStage{}},
		ScriptOutline:             &model.BrowserAgentScriptOutline{},
		BrowserAgentContract:      &model.BrowserAgentContract{},
	}

	events := []model.StageExecutionEvent{
		{
			StageID:    "stage-1",
			NodeID:     "node-1",
			EventType:  model.StageExecutionEventStageStarted,
			OccurredAt: time.Now(),
		},
		{
			StageID:    "stage-1",
			NodeID:     "node-1",
			EventType:  model.StageExecutionEventOutcomeObserved,
			OccurredAt: time.Now().Add(1 * time.Second),
			Observation: &model.RuntimeObservation{
				Source: model.RuntimeObservationActualBrowser,
				URL:    "https://EXAMPLE.com/dashboard",
				Assertions: []model.RuntimeAssertion{
					{
						Kind:   "url_match",
						Passed: true,
						Actual: "https://EXAMPLE.com/dashboard",
					},
				},
			},
			EvidenceRefs: []model.EvidenceRef{{ID: "ev-1", Kind: "screenshot"}},
		},
		{
			StageID:    "stage-1",
			NodeID:     "node-1",
			EventType:  model.StageExecutionEventStageCompleted,
			OccurredAt: time.Now().Add(2 * time.Second),
		},
	}

	ctx := context.Background()
	report, err := adapter.ValidateStageEvents(ctx, vctx, events)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if report.Decision == model.ValidationDecisionStopAndReport {
		t.Errorf("Expected decision other than 'stop_and_report' for mixed-case host matching a lowercase allowlist entry, got '%s'", report.Decision)
	}

	for _, check := range report.Checks {
		if check.Code == "CROSS_DOMAIN_ACCESS" {
			t.Errorf("Did not expect CROSS_DOMAIN_ACCESS check for case-insensitive host match, got: %+v", check)
		}
	}
}

// TestValidateStageEvents_CrossDomainAccess_SubdomainMatchesParentDomain is
// a true-negative counterpart to TestValidateStageEvents_CrossDomainAccess:
// a subdomain of an allowed domain (e.g. "app.example.com" under
// "example.com") must be treated as within scope, matching the app
// package's browserAgentURLAllowed suffix-matching semantics.
func TestValidateStageEvents_CrossDomainAccess_SubdomainMatchesParentDomain(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-run-domain-subdomain-001",
		SourcePackageID:           "pkg-domain-subdomain-001",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
		AllowedDomains:            []string{"example.com"},
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan:         &model.StageApprovalPlan{Stages: []model.StageApprovalStage{}},
		ScriptOutline:             &model.BrowserAgentScriptOutline{},
		BrowserAgentContract:      &model.BrowserAgentContract{},
	}

	events := []model.StageExecutionEvent{
		{
			StageID:    "stage-1",
			NodeID:     "node-1",
			EventType:  model.StageExecutionEventStageStarted,
			OccurredAt: time.Now(),
		},
		{
			StageID:    "stage-1",
			NodeID:     "node-1",
			EventType:  model.StageExecutionEventOutcomeObserved,
			OccurredAt: time.Now().Add(1 * time.Second),
			Observation: &model.RuntimeObservation{
				Source: model.RuntimeObservationActualBrowser,
				URL:    "https://app.example.com/dashboard",
				Assertions: []model.RuntimeAssertion{
					{
						Kind:   "url_match",
						Passed: true,
						Actual: "https://app.example.com/dashboard",
					},
				},
			},
			EvidenceRefs: []model.EvidenceRef{{ID: "ev-1", Kind: "screenshot"}},
		},
		{
			StageID:    "stage-1",
			NodeID:     "node-1",
			EventType:  model.StageExecutionEventStageCompleted,
			OccurredAt: time.Now().Add(2 * time.Second),
		},
	}

	ctx := context.Background()
	report, err := adapter.ValidateStageEvents(ctx, vctx, events)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if report.Decision == model.ValidationDecisionStopAndReport {
		t.Errorf("Expected decision other than 'stop_and_report' for subdomain of an allowed domain, got '%s'", report.Decision)
	}

	for _, check := range report.Checks {
		if check.Code == "CROSS_DOMAIN_ACCESS" {
			t.Errorf("Did not expect CROSS_DOMAIN_ACCESS check for subdomain of an allowed domain, got: %+v", check)
		}
	}
}
