package orchestrator

import (
	"cascade-demoops/backend/internal/model"
	"context"
	"testing"
	"time"
)

// TestValidatePostExecution_MissingResultPackage tests P0 missing result package detection
func TestValidatePostExecution_MissingResultPackage(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-post-001",
		SourcePackageID:           "pkg-post-001",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan:         &model.StageApprovalPlan{Stages: []model.StageApprovalStage{}},
		ScriptOutline:             &model.BrowserAgentScriptOutline{},
		BrowserAgentContract:      &model.BrowserAgentContract{},
	}

	ctx := context.Background()
	report, err := adapter.ValidatePostExecution(ctx, vctx, model.RecordingResultPackage{}, []model.StageExecutionEvent{})

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if report.Decision != model.ValidationDecisionStopAndReport {
		t.Errorf("Expected Decision 'stop_and_report' for missing result package, got '%s'", report.Decision)
	}

	foundCheck := false
	for _, check := range report.Checks {
		if check.Code == "MISSING_RESULT_PACKAGE" {
			foundCheck = true
			if check.Passed {
				t.Errorf("Missing result package check should not pass")
			}
			if check.Severity != model.FindingSeverityBlocking {
				t.Errorf("Missing result package should be blocking, got %s", check.Severity)
			}
		}
	}

	if !foundCheck {
		t.Errorf("Expected MISSING_RESULT_PACKAGE check")
	}
}

// TestValidatePostExecution_MissingArtifacts tests P0 artifact completeness checks
func TestValidatePostExecution_MissingArtifacts(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-post-artifacts-001",
		SourcePackageID:           "pkg-post-artifacts-001",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan:         &model.StageApprovalPlan{Stages: []model.StageApprovalStage{}},
		ScriptOutline:             &model.BrowserAgentScriptOutline{},
		BrowserAgentContract:      &model.BrowserAgentContract{},
	}

	// Result package with no artifacts
	result := &model.RecordingResultPackage{
		ResultID:        "result-001",
		SourcePackageID: "pkg-post-artifacts-001",
		Status:          model.RecordingResultStatusGenerated,
		GeneratedAssets: []model.ArtifactRef{}, // Empty!
		StageEventLogRef: nil,                   // Missing!
	}

	ctx := context.Background()
	report, err := adapter.ValidatePostExecution(ctx, vctx, *result, []model.StageExecutionEvent{})

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Missing stage event log is a warning (non-blocking), so decision should not be stop_and_report
	if report.Decision == model.ValidationDecisionStopAndReport {
		t.Errorf("Missing stage event log is warning-only; expected non-stop decision, got 'stop_and_report'")
	}

	foundStageEventLogCheck := false
	for _, check := range report.Checks {
		if check.Code == "MISSING_STAGE_EVENT_LOG" {
			foundStageEventLogCheck = true
			if check.Severity != model.FindingSeverityWarning {
				t.Errorf("Missing stage event log should be warning, got %s", check.Severity)
			}
		}
	}

	if !foundStageEventLogCheck {
		t.Errorf("Expected MISSING_STAGE_EVENT_LOG check")
	}

	// Should also detect missing screenshots, trace, MP4 as warnings
	codes := make(map[string]bool)
	for _, check := range report.Checks {
		codes[check.Code] = true
	}

	if !codes["MISSING_SCREENSHOTS"] {
		t.Errorf("Expected MISSING_SCREENSHOTS warning")
	}
	if !codes["MISSING_TRACE_ARTIFACT"] {
		t.Errorf("Expected MISSING_TRACE_ARTIFACT warning")
	}
	if !codes["MISSING_MP4_VIDEO"] {
		t.Errorf("Expected MISSING_MP4_VIDEO warning")
	}
}

// TestValidatePostExecution_IncompleteStages tests P0 required stage completion check
func TestValidatePostExecution_IncompleteStages(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-post-stages-001",
		SourcePackageID:           "pkg-post-stages-001",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan: &model.StageApprovalPlan{
			Stages: []model.StageApprovalStage{
				{NodeID: "stage-1", Order: 1, Title: "Login"},
				{NodeID: "stage-2", Order: 2, Title: "Navigate"},
				{NodeID: "stage-3", Order: 3, Title: "Submit"},
			},
		},
		ScriptOutline:        &model.BrowserAgentScriptOutline{},
		BrowserAgentContract: &model.BrowserAgentContract{},
	}

	// Only stage-1 completed, stage-2 and stage-3 missing
	events := []model.StageExecutionEvent{
		{
			StageID:    "stage-1",
			EventType:  model.StageExecutionEventStageStarted,
			OccurredAt: time.Now(),
		},
		{
			StageID:    "stage-1",
			EventType:  model.StageExecutionEventStageCompleted,
			OccurredAt: time.Now().Add(1 * time.Second),
		},
	}

	result := &model.RecordingResultPackage{
		ResultID:         "result-stages-001",
		SourcePackageID:  "pkg-post-stages-001",
		Status:           model.RecordingResultStatusGenerated,
		GeneratedAssets:  []model.ArtifactRef{},
		StageEventLogRef: &model.ArtifactRef{ID: "log-1", Kind: "stage_event_log"},
	}

	ctx := context.Background()
	report, err := adapter.ValidatePostExecution(ctx, vctx, *result, events)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if report.Decision != model.ValidationDecisionStopAndReport {
		t.Errorf("Expected Decision 'stop_and_report' for incomplete stages, got '%s'", report.Decision)
	}

	// Should detect 2 missing stages
	missingCount := 0
	for _, check := range report.Checks {
		if check.Code == "REQUIRED_STAGE_NOT_COMPLETED" {
			missingCount++
			if check.Severity != model.FindingSeverityBlocking {
				t.Errorf("Incomplete stage should be blocking, got %s", check.Severity)
			}
		}
	}

	if missingCount != 2 {
		t.Errorf("Expected 2 REQUIRED_STAGE_NOT_COMPLETED checks, got %d", missingCount)
	}
}

// TestValidatePostExecution_MissingEvidenceRefs tests P0 evidence traceability check
func TestValidatePostExecution_MissingEvidenceRefs(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-post-evidence-001",
		SourcePackageID:           "pkg-post-evidence-001",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan:         &model.StageApprovalPlan{Stages: []model.StageApprovalStage{}},
		ScriptOutline:             &model.BrowserAgentScriptOutline{},
		BrowserAgentContract:      &model.BrowserAgentContract{},
	}

	// outcome_observed without evidence_refs
	events := []model.StageExecutionEvent{
		{
			StageID:    "stage-1",
			EventType:  model.StageExecutionEventStageStarted,
			OccurredAt: time.Now(),
		},
		{
			StageID:   "stage-1",
			EventType: model.StageExecutionEventOutcomeObserved,
			OccurredAt: time.Now().Add(1 * time.Second),
			Observation: &model.RuntimeObservation{
				Source: model.RuntimeObservationActualBrowser,
				URL:    "https://example.com",
			},
			EvidenceRefs: []model.EvidenceRef{}, // Empty!
		},
	}

	result := &model.RecordingResultPackage{
		ResultID:         "result-evidence-001",
		SourcePackageID:  "pkg-post-evidence-001",
		Status:           model.RecordingResultStatusGenerated,
		GeneratedAssets:  []model.ArtifactRef{},
		StageEventLogRef: &model.ArtifactRef{ID: "log-1", Kind: "stage_event_log"},
	}

	ctx := context.Background()
	report, err := adapter.ValidatePostExecution(ctx, vctx, *result, events)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Missing evidence_refs is a warning, not blocking
	foundCheck := false
	for _, check := range report.Checks {
		if check.Code == "MISSING_EVIDENCE_REFS" {
			foundCheck = true
			if check.Severity != model.FindingSeverityWarning {
				t.Errorf("Missing evidence refs should be warning, got %s", check.Severity)
			}
		}
	}

	if !foundCheck {
		t.Errorf("Expected MISSING_EVIDENCE_REFS check")
	}
}

// TestValidatePostExecution_SourcePackageMismatch tests scenario 11: the
// result package's source_package_id does not match the approved run's
// source package, meaning the evidence chain does not belong to this run.
func TestValidatePostExecution_SourcePackageMismatch(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-post-mismatch-001",
		SourcePackageID:           "pkg-approved-001",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan:         &model.StageApprovalPlan{Stages: []model.StageApprovalStage{}},
		ScriptOutline:             &model.BrowserAgentScriptOutline{},
		BrowserAgentContract:      &model.BrowserAgentContract{},
	}

	result := &model.RecordingResultPackage{
		ResultID:        "result-mismatch-001",
		SourcePackageID: "pkg-DIFFERENT-999",
		Status:          model.RecordingResultStatusGenerated,
		GeneratedAssets: []model.ArtifactRef{
			{ID: "screenshot-1", Kind: "screenshot", URI: "s3://bucket/s.png"},
			{ID: "trace-1", Kind: "trace", URI: "s3://bucket/t.zip"},
			{ID: "video-1", Kind: "video", URI: "s3://bucket/v.mp4"},
		},
		StageEventLogRef: &model.ArtifactRef{ID: "log-1", Kind: "stage_event_log"},
	}

	ctx := context.Background()
	report, err := adapter.ValidatePostExecution(ctx, vctx, *result, []model.StageExecutionEvent{})

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if report.Decision != model.ValidationDecisionStopAndReport {
		t.Errorf("Expected Decision 'stop_and_report' for source package mismatch, got '%s'", report.Decision)
	}

	foundCheck := false
	for _, check := range report.Checks {
		if check.Code == "RESULT_PACKAGE_MISMATCH" {
			foundCheck = true
			if check.Passed {
				t.Errorf("Result package mismatch check should not pass")
			}
			if check.Severity != model.FindingSeverityBlocking {
				t.Errorf("Result package mismatch should be blocking, got %s", check.Severity)
			}
		}
	}

	if !foundCheck {
		t.Errorf("Expected RESULT_PACKAGE_MISMATCH check")
	}
}

// TestValidatePostExecution_HashMismatch tests scenario 11: the result
// package's audit-trail source digest does not match the approved run's
// source bundle hash.
func TestValidatePostExecution_HashMismatch(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-post-hash-001",
		SourcePackageID:           "pkg-approved-002",
		SourceBundleHashSHA256:    "expected-hash-abc123",
		EffectivePolicyHashSHA256: "def456",
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan:         &model.StageApprovalPlan{Stages: []model.StageApprovalStage{}},
		ScriptOutline:             &model.BrowserAgentScriptOutline{},
		BrowserAgentContract:      &model.BrowserAgentContract{},
	}

	result := &model.RecordingResultPackage{
		ResultID:        "result-hash-001",
		SourcePackageID: "pkg-approved-002",
		Status:          model.RecordingResultStatusGenerated,
		GeneratedAssets: []model.ArtifactRef{
			{ID: "screenshot-1", Kind: "screenshot", URI: "s3://bucket/s.png"},
			{ID: "trace-1", Kind: "trace", URI: "s3://bucket/t.zip"},
			{ID: "video-1", Kind: "video", URI: "s3://bucket/v.mp4"},
		},
		StageEventLogRef: &model.ArtifactRef{ID: "log-1", Kind: "stage_event_log"},
		AuditTrail: model.CloudExecutionAuditTrail{
			SourcePackageDigest: "actual-hash-DIFFERENT-999",
		},
	}

	ctx := context.Background()
	report, err := adapter.ValidatePostExecution(ctx, vctx, *result, []model.StageExecutionEvent{})

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if report.Decision != model.ValidationDecisionStopAndReport {
		t.Errorf("Expected Decision 'stop_and_report' for hash mismatch, got '%s'", report.Decision)
	}

	foundCheck := false
	for _, check := range report.Checks {
		if check.Code == "RESULT_HASH_MISMATCH" {
			foundCheck = true
			if check.Passed {
				t.Errorf("Result hash mismatch check should not pass")
			}
			if check.Severity != model.FindingSeverityBlocking {
				t.Errorf("Result hash mismatch should be blocking, got %s", check.Severity)
			}
		}
	}

	if !foundCheck {
		t.Errorf("Expected RESULT_HASH_MISMATCH check")
	}
}

// TestValidatePostExecution_ValidComplete tests P0 passes with complete artifacts
func TestValidatePostExecution_ValidComplete(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-post-valid-001",
		SourcePackageID:           "pkg-post-valid-001",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan: &model.StageApprovalPlan{
			Stages: []model.StageApprovalStage{
				{NodeID: "stage-1", Order: 1, Title: "Login"},
			},
		},
		ScriptOutline:        &model.BrowserAgentScriptOutline{},
		BrowserAgentContract: &model.BrowserAgentContract{},
	}

	// Complete valid execution
	events := []model.StageExecutionEvent{
		{
			StageID:    "stage-1",
			EventType:  model.StageExecutionEventStageStarted,
			OccurredAt: time.Now(),
		},
		{
			StageID:   "stage-1",
			EventType: model.StageExecutionEventOutcomeObserved,
			OccurredAt: time.Now().Add(1 * time.Second),
			Observation: &model.RuntimeObservation{
				Source: model.RuntimeObservationActualBrowser,
				URL:    "https://example.com/dashboard",
			},
			EvidenceRefs: []model.EvidenceRef{
				{ID: "screenshot-1", Kind: "screenshot"},
			},
		},
		{
			StageID:    "stage-1",
			EventType:  model.StageExecutionEventStageCompleted,
			OccurredAt: time.Now().Add(2 * time.Second),
		},
	}

	result := &model.RecordingResultPackage{
		ResultID:        "result-valid-001",
		SourcePackageID: "pkg-post-valid-001",
		Status:          model.RecordingResultStatusGenerated,
		GeneratedAssets: []model.ArtifactRef{
			{ID: "screenshot-1", Kind: "screenshot", URI: "s3://bucket/screenshot1.png"},
			{ID: "trace-1", Kind: "trace", URI: "s3://bucket/trace.zip"},
			{ID: "video-1", Kind: "video", URI: "s3://bucket/recording.mp4"},
		},
		StageEventLogRef: &model.ArtifactRef{ID: "log-1", Kind: "stage_event_log", URI: "s3://bucket/events.jsonl"},
	}

	ctx := context.Background()
	report, err := adapter.ValidatePostExecution(ctx, vctx, *result, events)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Should NOT be stop_and_report for valid complete execution
	if report.Decision == model.ValidationDecisionStopAndReport {
		for _, check := range report.Checks {
			if !check.Passed && check.Severity == model.FindingSeverityBlocking {
				t.Errorf("Valid complete execution should not have blocking failures: %s - %s", check.Code, check.Summary)
			}
		}
	}

	// Should not have critical P0 violations
	for _, check := range report.Checks {
		if check.Code == "MISSING_RESULT_PACKAGE" ||
		   check.Code == "MISSING_STAGE_EVENT_LOG" ||
		   check.Code == "REQUIRED_STAGE_NOT_COMPLETED" {
			t.Errorf("Valid complete execution should not trigger P0 blocking check: %s", check.Code)
		}
	}
}

// TestValidatePostExecution_ObservedStateTraceability tests observed_state traceability cross-check
func TestValidatePostExecution_ObservedStateTraceability(t *testing.T) {
	config := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
	}
	adapter := NewBrowserAgentOutcomeVerifierAdapter(config)

	vctx := model.BrowserAgentValidationContext{
		RunID:                     "test-post-traceability-001",
		SourcePackageID:           "pkg-post-traceability-001",
		SourceBundleHashSHA256:    "bundle-hash-123",
		EffectivePolicyHashSHA256: "policy-hash-456",
		WorkflowGraph:             &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{}},
		Plan:                      &model.ExecutionScriptDocument{},
		StageApprovalPlan: &model.StageApprovalPlan{
			Stages: []model.StageApprovalStage{
				{NodeID: "stage-1", Order: 1},
			},
		},
		ScriptOutline:        &model.BrowserAgentScriptOutline{ID: "outline-1"},
		BrowserAgentContract: &model.BrowserAgentContract{},
		AllowedDomains:       []string{"example.com"},
	}

	t.Run("no_source_marker_fails", func(t *testing.T) {
		// Case 1: observed_state doesn't contain "source=" → fails
		events := []model.StageExecutionEvent{
			{
				EventType: model.StageExecutionEventOutcomeObserved,
				NodeID:    "stage-1",
				StageID:   "stage-1",
				Observation: &model.RuntimeObservation{
					Source: model.RuntimeObservationActualBrowser,
					URL:    "https://example.com/page1",
				},
				EvidenceRefs: []model.EvidenceRef{
					{ID: "screenshot-1", Kind: "screenshot"},
				},
				OccurredAt: time.Now(),
			},
			{
				EventType:  model.StageExecutionEventStageCompleted,
				NodeID:     "stage-1",
				StageID:    "stage-1",
				OccurredAt: time.Now().Add(1 * time.Second),
			},
		}

		result := model.RecordingResultPackage{
			ResultID:        "result-1",
			SourcePackageID: "pkg-post-traceability-001",
			Status:          model.RecordingResultStatusGenerated,
			StepResults: []model.StepResult{
				{NodeID: "stage-1", Status: "passed", ObservedState: "manually crafted state"},
			},
			AuditTrail:       model.CloudExecutionAuditTrail{SourcePackageDigest: "bundle-hash-123"},
			GeneratedAssets:  []model.ArtifactRef{},
			StageEventLogRef: &model.ArtifactRef{ID: "log-1", Kind: "stage_event_log"},
		}

		ctx := context.Background()
		report, err := adapter.ValidatePostExecution(ctx, vctx, result, events)
		if err != nil {
			t.Fatalf("ValidatePostExecution failed: %v", err)
		}

		// Should produce observed_state_not_runtime_derived failure
		found := false
		for _, check := range report.Checks {
			if check.Code == "observed_state_not_runtime_derived" && !check.Passed {
				found = true
				if check.Severity != model.FindingSeverityBlocking {
					t.Errorf("check.Severity = %v, want blocking", check.Severity)
				}
				break
			}
		}
		if !found {
			t.Errorf("Expected observed_state_not_runtime_derived check to fail, but not found")
		}
	})

	t.Run("no_outcome_event_fails", func(t *testing.T) {
		// Case 2: observed_state exists but no corresponding outcome_observed event → fails
		events := []model.StageExecutionEvent{
			// No outcome_observed event
			{
				EventType:  model.StageExecutionEventStageStarted,
				NodeID:     "stage-1",
				StageID:    "stage-1",
				OccurredAt: time.Now(),
			},
			{
				EventType:  model.StageExecutionEventStageCompleted,
				NodeID:     "stage-1",
				StageID:    "stage-1",
				OccurredAt: time.Now().Add(1 * time.Second),
			},
		}

		result := model.RecordingResultPackage{
			ResultID:        "result-2",
			SourcePackageID: "pkg-post-traceability-001",
			Status:          model.RecordingResultStatusGenerated,
			StepResults: []model.StepResult{
				{NodeID: "stage-1", Status: "passed", ObservedState: "source=runtime"},
			},
			AuditTrail:       model.CloudExecutionAuditTrail{SourcePackageDigest: "bundle-hash-123"},
			GeneratedAssets:  []model.ArtifactRef{},
			StageEventLogRef: &model.ArtifactRef{ID: "log-1", Kind: "stage_event_log"},
		}

		ctx := context.Background()
		report, err := adapter.ValidatePostExecution(ctx, vctx, result, events)
		if err != nil {
			t.Fatalf("ValidatePostExecution failed: %v", err)
		}

		// Should produce observed_state_not_runtime_derived failure
		found := false
		for _, check := range report.Checks {
			if check.Code == "observed_state_not_runtime_derived" && !check.Passed {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected observed_state_not_runtime_derived check to fail when no outcome event exists")
		}
	})

	t.Run("valid_traceability_passes", func(t *testing.T) {
		// Case 3: observed_state contains "source=" AND has real event → passes
		events := []model.StageExecutionEvent{
			{
				EventType: model.StageExecutionEventOutcomeObserved,
				NodeID:    "stage-1",
				StageID:   "stage-1",
				Observation: &model.RuntimeObservation{
					Source: model.RuntimeObservationActualBrowser,
					URL:    "https://example.com/page1",
				},
				EvidenceRefs: []model.EvidenceRef{
					{ID: "screenshot-1", Kind: "screenshot"},
				},
				OccurredAt: time.Now(),
			},
			{
				EventType:  model.StageExecutionEventStageCompleted,
				NodeID:     "stage-1",
				StageID:    "stage-1",
				OccurredAt: time.Now().Add(1 * time.Second),
			},
		}

		result := model.RecordingResultPackage{
			ResultID:        "result-3",
			SourcePackageID: "pkg-post-traceability-001",
			Status:          model.RecordingResultStatusGenerated,
			StepResults: []model.StepResult{
				{NodeID: "stage-1", Status: "passed", ObservedState: "source=runtime, url=https://example.com/page1"},
			},
			AuditTrail:      model.CloudExecutionAuditTrail{SourcePackageDigest: "bundle-hash-123"},
			GeneratedAssets: []model.ArtifactRef{},
			StageEventLogRef: &model.ArtifactRef{ID: "log-1", Kind: "stage_event_log"},
		}

		ctx := context.Background()
		report, err := adapter.ValidatePostExecution(ctx, vctx, result, events)
		if err != nil {
			t.Fatalf("ValidatePostExecution failed: %v", err)
		}

		// Should NOT produce observed_state_not_runtime_derived failure
		for _, check := range report.Checks {
			if check.Code == "observed_state_not_runtime_derived" && !check.Passed {
				t.Errorf("Unexpected observed_state_not_runtime_derived failure: %v", check.Summary)
			}
		}
	})
}
