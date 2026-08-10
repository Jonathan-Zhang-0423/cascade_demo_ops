package orchestrator

import (
	"cascade-demoops/backend/internal/model"
	"testing"
)

// TestRepairProposalGenerator_AllowedKinds tests P1 repair kind filtering
func TestRepairProposalGenerator_AllowedKinds(t *testing.T) {
	config := &model.ValidationConfig{
		EnableRuntimeRepair: true,
	}
	gen := NewRepairProposalGenerator(config)

	vctx := &model.BrowserAgentValidationContext{
		RunID:                     "test-repair-001",
		SourcePackageID:           "pkg-repair-001",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
	}

	checks := []model.ValidationCheck{
		{
			ID:       "check-1",
			Kind:     "selector_validity",
			Code:     "SELECTOR_NOT_FOUND",
			NodeID:   "node-1",
			StageID:  "stage-1",
			Severity: model.FindingSeverityBlocking,
			Passed:   false,
			Required: true,
			Summary:  "Selector #login-btn not found",
		},
		{
			ID:       "check-2",
			Kind:     "wait_strategy",
			Code:     "WAIT_TIMEOUT",
			Severity: model.FindingSeverityWarning,
			Passed:   false,
			Required: false,
			Summary:  "Wait timeout after 5000ms",
		},
	}

	// Policy allows only selector_alternative
	policy := &model.BrowserAgentRepairPolicy{
		AllowedRepairKinds: []string{"selector_alternative"},
		EditableFields:     []string{"selector"},
	}

	proposals := gen.GenerateRepairProposals(vctx, checks, policy)

	// Should only get 1 proposal (selector_alternative), not wait_strategy
	if len(proposals) != 1 {
		t.Errorf("Expected 1 proposal (selector_alternative only), got %d", len(proposals))
	}

	if len(proposals) > 0 && proposals[0].RepairKind != "selector_alternative" {
		t.Errorf("Expected repair kind 'selector_alternative', got '%s'", proposals[0].RepairKind)
	}
}

// TestRepairProposalGenerator_ImmutableFields tests P1 immutable field blocking
func TestRepairProposalGenerator_ImmutableFields(t *testing.T) {
	config := &model.ValidationConfig{
		EnableRuntimeRepair: true,
	}
	gen := NewRepairProposalGenerator(config)

	vctx := &model.BrowserAgentValidationContext{
		RunID:                     "test-repair-002",
		SourcePackageID:           "pkg-repair-002",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
	}

	checks := []model.ValidationCheck{
		{
			ID:       "check-1",
			Kind:     "selector_validity",
			Code:     "SELECTOR_NOT_FOUND",
			Severity: model.FindingSeverityBlocking,
			Passed:   false,
			Required: true,
			Summary:  "Selector #login-btn not found",
		},
	}

	// Policy allows selector_alternative but marks selector as immutable
	policy := &model.BrowserAgentRepairPolicy{
		AllowedRepairKinds: []string{"selector_alternative"},
		ImmutableFields:    []string{"selector"},
	}

	proposals := gen.GenerateRepairProposals(vctx, checks, policy)

	// Should get 0 proposals because selector is immutable
	if len(proposals) != 0 {
		t.Errorf("Expected 0 proposals (selector is immutable), got %d", len(proposals))
	}
}

// TestRepairProposalGenerator_RequiresApproval tests P1 approval flag logic
func TestRepairProposalGenerator_RequiresApproval(t *testing.T) {
	config := &model.ValidationConfig{
		EnableRuntimeRepair: true,
	}
	gen := NewRepairProposalGenerator(config)

	vctx := &model.BrowserAgentValidationContext{
		RunID:                     "test-repair-003",
		SourcePackageID:           "pkg-repair-003",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
	}

	checks := []model.ValidationCheck{
		{
			ID:       "check-1",
			Kind:     "wait_strategy",
			Code:     "WAIT_TIMEOUT",
			NodeID:   "node-1",
			StageID:  "stage-1",
			Severity: model.FindingSeverityWarning,
			Passed:   false,
			Required: false,
			Summary:  "Wait timeout after 5000ms",
		},
	}

	// Policy allows wait_strategy with high auto-apply threshold
	policy := &model.BrowserAgentRepairPolicy{
		AllowedRepairKinds:     []string{"wait_strategy"},
		EditableFields:         []string{"wait_timeout"},
		MinAutoApplyConfidence: 0.8,
	}

	proposals := gen.GenerateRepairProposals(vctx, checks, policy)

	if len(proposals) != 1 {
		t.Fatalf("Expected 1 proposal, got %d", len(proposals))
	}

	// wait_strategy has confidence 0.6 < 0.8, so should require approval
	if !proposals[0].RequiresApproval {
		t.Errorf("Expected RequiresApproval=true when confidence (0.6) < threshold (0.8)")
	}

	// Test with lower threshold
	policy.MinAutoApplyConfidence = 0.5
	proposals2 := gen.GenerateRepairProposals(vctx, checks, policy)

	if len(proposals2) != 1 {
		t.Fatalf("Expected 1 proposal, got %d", len(proposals2))
	}

	// Now confidence 0.6 >= 0.5, so should NOT require approval
	if proposals2[0].RequiresApproval {
		t.Errorf("Expected RequiresApproval=false when confidence (0.6) >= threshold (0.5)")
	}
}

// TestIsRepairAllowed_ProhibitedFields tests P1 absolute prohibitions
func TestIsRepairAllowed_ProhibitedFields(t *testing.T) {
	policy := &model.BrowserAgentRepairPolicy{
		AllowedRepairKinds: []string{"any"},
	}

	prohibitedFields := []string{
		"business_input",
		"form_data",
		"target_route",
		"success_criteria",
		"stage_order",
		"credentials",
		"secret_ref",
		"auth_token",
	}

	for _, field := range prohibitedFields {
		allowed, reason := IsRepairAllowed("any", field, policy)
		if allowed {
			t.Errorf("Field '%s' should be absolutely prohibited, but was allowed", field)
		}
		if reason == "" {
			t.Errorf("Field '%s' prohibition should include a reason", field)
		}
	}
}

// TestIsRepairAllowed_NotInAllowedKinds tests P1 repair kind whitelist
func TestIsRepairAllowed_NotInAllowedKinds(t *testing.T) {
	policy := &model.BrowserAgentRepairPolicy{
		AllowedRepairKinds: []string{"selector_alternative", "wait_strategy"},
	}

	allowed, reason := IsRepairAllowed("capture_timing", "capture_delay_ms", policy)
	if allowed {
		t.Errorf("Repair kind 'capture_timing' not in allowed list, should be blocked")
	}
	if reason == "" {
		t.Errorf("Should provide reason for blocking repair kind")
	}

	// Test allowed kind
	allowed2, _ := IsRepairAllowed("wait_strategy", "wait_timeout", policy)
	if !allowed2 {
		t.Errorf("Repair kind 'wait_strategy' is in allowed list, should be allowed")
	}
}

// TestRepairProposalGenerator_DisabledConfig tests config-level disable
func TestRepairProposalGenerator_DisabledConfig(t *testing.T) {
	config := &model.ValidationConfig{
		EnableRuntimeRepair: false, // Disabled!
	}
	gen := NewRepairProposalGenerator(config)

	vctx := &model.BrowserAgentValidationContext{
		RunID:                     "test-repair-004",
		SourcePackageID:           "pkg-repair-004",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
	}

	checks := []model.ValidationCheck{
		{
			ID:       "check-1",
			Kind:     "selector_validity",
			Code:     "SELECTOR_NOT_FOUND",
			Severity: model.FindingSeverityBlocking,
			Passed:   false,
			Required: true,
			Summary:  "Selector #login-btn not found",
		},
	}

	policy := &model.BrowserAgentRepairPolicy{
		AllowedRepairKinds: []string{"selector_alternative"},
		EditableFields:     []string{"selector"},
	}

	proposals := gen.GenerateRepairProposals(vctx, checks, policy)

	// Should get 0 proposals because EnableRuntimeRepair=false
	if len(proposals) != 0 {
		t.Errorf("Expected 0 proposals when EnableRuntimeRepair=false, got %d", len(proposals))
	}
}

// TestRepairProposalGenerator_NoPolicy tests nil policy handling
func TestRepairProposalGenerator_NoPolicy(t *testing.T) {
	config := &model.ValidationConfig{
		EnableRuntimeRepair: true,
	}
	gen := NewRepairProposalGenerator(config)

	vctx := &model.BrowserAgentValidationContext{
		RunID:                     "test-repair-005",
		SourcePackageID:           "pkg-repair-005",
		SourceBundleHashSHA256:    "abc123",
		EffectivePolicyHashSHA256: "def456",
	}

	checks := []model.ValidationCheck{
		{
			ID:       "check-1",
			Kind:     "selector_validity",
			Code:     "SELECTOR_NOT_FOUND",
			Severity: model.FindingSeverityBlocking,
			Passed:   false,
			Required: true,
			Summary:  "Selector #login-btn not found",
		},
	}

	proposals := gen.GenerateRepairProposals(vctx, checks, nil)

	// Should get 0 proposals because policy is nil
	if len(proposals) != 0 {
		t.Errorf("Expected 0 proposals when policy is nil, got %d", len(proposals))
	}
}

// TestRepairProposalGenerator_ProposalFields tests P1 required fields
func TestRepairProposalGenerator_ProposalFields(t *testing.T) {
	config := &model.ValidationConfig{
		EnableRuntimeRepair: true,
	}
	gen := NewRepairProposalGenerator(config)

	vctx := &model.BrowserAgentValidationContext{
		RunID:                     "test-repair-006",
		SourcePackageID:           "pkg-repair-006",
		SourceBundleHashSHA256:    "hash-abc",
		EffectivePolicyHashSHA256: "policy-def",
	}

	checks := []model.ValidationCheck{
		{
			ID:       "check-1",
			Kind:     "wait_strategy",
			Code:     "WAIT_TIMEOUT",
			NodeID:   "node-1",
			StageID:  "stage-1",
			Severity: model.FindingSeverityWarning,
			Passed:   false,
			Required: false,
			Summary:  "Wait timeout after 5000ms",
			EvidenceRefs: []model.EvidenceRef{
				{ID: "evidence-1", Kind: "screenshot"},
			},
		},
	}

	policy := &model.BrowserAgentRepairPolicy{
		AllowedRepairKinds: []string{"wait_strategy"},
		EditableFields:     []string{"wait_timeout"},
	}

	proposals := gen.GenerateRepairProposals(vctx, checks, policy)

	if len(proposals) != 1 {
		t.Fatalf("Expected 1 proposal, got %d", len(proposals))
	}

	p := proposals[0]

	// P1 requirement: All proposals must include run/stage identity, two hashes, repair kind, field before/after, confidence, evidence, requires_approval
	if p.RunID != vctx.RunID {
		t.Errorf("Proposal RunID mismatch: expected '%s', got '%s'", vctx.RunID, p.RunID)
	}
	if p.BaseBundleHashSHA256 != vctx.SourceBundleHashSHA256 {
		t.Errorf("Proposal BaseBundleHashSHA256 mismatch: expected '%s', got '%s'", vctx.SourceBundleHashSHA256, p.BaseBundleHashSHA256)
	}
	if p.PolicyHashSHA256 != vctx.EffectivePolicyHashSHA256 {
		t.Errorf("Proposal PolicyHashSHA256 mismatch: expected '%s', got '%s'", vctx.EffectivePolicyHashSHA256, p.PolicyHashSHA256)
	}
	if p.RepairKind == "" {
		t.Errorf("Proposal RepairKind is empty")
	}
	if p.Field == "" {
		t.Errorf("Proposal Field is empty")
	}
	if p.After == "" {
		t.Errorf("Proposal After is empty")
	}
	if p.Confidence <= 0 {
		t.Errorf("Proposal Confidence should be > 0, got %f", p.Confidence)
	}
	if len(p.EvidenceRefs) != 1 {
		t.Errorf("Proposal should have evidence refs from check, got %d", len(p.EvidenceRefs))
	}
	if p.ProposalID == "" {
		t.Errorf("Proposal ProposalID is empty")
	}
	if p.SchemaVersion != model.RuntimeRepairProposalSchemaVersion {
		t.Errorf("Proposal SchemaVersion: expected '%s', got '%s'", model.RuntimeRepairProposalSchemaVersion, p.SchemaVersion)
	}
	if p.NodeID != checks[0].NodeID {
		t.Errorf("Proposal NodeID: expected '%s', got '%s'", checks[0].NodeID, p.NodeID)
	}
	if p.StageID != checks[0].StageID {
		t.Errorf("Proposal StageID: expected '%s', got '%s'", checks[0].StageID, p.StageID)
	}
}

// TestRepairProposalGenerator_CaptureTimingKind tests capture_timing repair proposal generation
func TestRepairProposalGenerator_CaptureTimingKind(t *testing.T) {
	config := &model.ValidationConfig{EnableRuntimeRepair: true}
	gen := NewRepairProposalGenerator(config)

	vctx := &model.BrowserAgentValidationContext{
		RunID:                     "run-capture-001",
		SourcePackageID:           "pkg-capture-001",
		SourceBundleHashSHA256:    "bundle-capture-abc",
		EffectivePolicyHashSHA256: "policy-capture-def",
	}

	checks := []model.ValidationCheck{
		{
			ID:      "check-capture-1",
			Kind:    "capture_timing",
			Code:    "CAPTURE_TIMING_ISSUE",
			NodeID:  "node-capture-1",
			StageID: "stage-capture-1",
			Severity: model.FindingSeverityWarning,
			Passed:  false,
			Required: false,
			Summary: "Screenshot captured before element fully rendered",
		},
	}

	policy := &model.BrowserAgentRepairPolicy{
		AllowedRepairKinds: []string{"capture_timing"},
		EditableFields:     []string{"capture_delay_ms"},
	}

	proposals := gen.GenerateRepairProposals(vctx, checks, policy)

	if len(proposals) != 1 {
		t.Fatalf("Expected 1 proposal for capture_timing, got %d", len(proposals))
	}

	p := proposals[0]

	if p.RepairKind != "capture_timing" {
		t.Errorf("Expected RepairKind='capture_timing', got '%s'", p.RepairKind)
	}
	if p.Field != "capture_delay_ms" {
		t.Errorf("Expected Field='capture_delay_ms', got '%s'", p.Field)
	}
	if p.RunID != vctx.RunID {
		t.Errorf("Expected RunID='%s', got '%s'", vctx.RunID, p.RunID)
	}
	if p.NodeID != checks[0].NodeID {
		t.Errorf("Expected NodeID='%s', got '%s'", checks[0].NodeID, p.NodeID)
	}
	if p.StageID != checks[0].StageID {
		t.Errorf("Expected StageID='%s', got '%s'", checks[0].StageID, p.StageID)
	}
	if p.BaseBundleHashSHA256 != vctx.SourceBundleHashSHA256 {
		t.Errorf("Expected BaseBundleHashSHA256='%s', got '%s'", vctx.SourceBundleHashSHA256, p.BaseBundleHashSHA256)
	}
	if p.PolicyHashSHA256 != vctx.EffectivePolicyHashSHA256 {
		t.Errorf("Expected PolicyHashSHA256='%s', got '%s'", vctx.EffectivePolicyHashSHA256, p.PolicyHashSHA256)
	}
	if err := p.Validate(); err != nil {
		t.Errorf("Generated capture_timing proposal failed Validate(): %v", err)
	}
}

// TestRepairProposalGenerator_FrameResolutionKind tests frame_resolution repair proposal generation
func TestRepairProposalGenerator_FrameResolutionKind(t *testing.T) {
	config := &model.ValidationConfig{EnableRuntimeRepair: true}
	gen := NewRepairProposalGenerator(config)

	vctx := &model.BrowserAgentValidationContext{
		RunID:                     "run-frame-001",
		SourcePackageID:           "pkg-frame-001",
		SourceBundleHashSHA256:    "bundle-frame-abc",
		EffectivePolicyHashSHA256: "policy-frame-def",
	}

	checks := []model.ValidationCheck{
		{
			ID:      "check-frame-1",
			Kind:    "frame_resolution",
			Code:    "FRAME_NOT_FOUND",
			NodeID:  "node-frame-1",
			StageID: "stage-frame-1",
			Severity: model.FindingSeverityBlocking,
			Passed:  false,
			Required: true,
			Summary: "Target element not reachable in current frame context",
		},
	}

	policy := &model.BrowserAgentRepairPolicy{
		AllowedRepairKinds: []string{"frame_resolution"},
		EditableFields:     []string{"frame_selector"},
	}

	proposals := gen.GenerateRepairProposals(vctx, checks, policy)

	if len(proposals) != 1 {
		t.Fatalf("Expected 1 proposal for frame_resolution, got %d", len(proposals))
	}

	p := proposals[0]

	if p.RepairKind != "frame_resolution" {
		t.Errorf("Expected RepairKind='frame_resolution', got '%s'", p.RepairKind)
	}
	if p.Field != "frame_selector" {
		t.Errorf("Expected Field='frame_selector', got '%s'", p.Field)
	}
	// RuntimeRepairProposal has RequiresApproval field; frame_resolution sets it true
	if !p.RequiresApproval {
		t.Errorf("Expected RequiresApproval=true for frame_resolution, got false")
	}
}

// TestRepairProposalGenerator_TimingFrameRejectedByPolicy tests that capture_timing and frame_resolution
// are both blocked when the policy AllowedRepairKinds excludes them
func TestRepairProposalGenerator_TimingFrameRejectedByPolicy(t *testing.T) {
	config := &model.ValidationConfig{EnableRuntimeRepair: true}
	gen := NewRepairProposalGenerator(config)

	vctx := &model.BrowserAgentValidationContext{
		RunID:                     "run-reject-001",
		SourcePackageID:           "pkg-reject-001",
		SourceBundleHashSHA256:    "bundle-reject-abc",
		EffectivePolicyHashSHA256: "policy-reject-def",
	}

	checks := []model.ValidationCheck{
		{
			ID:      "check-reject-1",
			Kind:    "capture_timing",
			Code:    "CAPTURE_TIMING_ISSUE",
			NodeID:  "node-reject-1",
			StageID: "stage-reject-1",
			Severity: model.FindingSeverityWarning,
			Passed:  false,
			Required: false,
			Summary: "Screenshot captured before element fully rendered",
		},
		{
			ID:      "check-reject-2",
			Kind:    "frame_resolution",
			Code:    "FRAME_NOT_FOUND",
			NodeID:  "node-reject-2",
			StageID: "stage-reject-2",
			Severity: model.FindingSeverityBlocking,
			Passed:  false,
			Required: true,
			Summary: "Target element not reachable in current frame context",
		},
	}

	// Policy excludes both capture_timing and frame_resolution
	policy := &model.BrowserAgentRepairPolicy{
		AllowedRepairKinds: []string{"selector_alternative"},
		EditableFields:     []string{"selector"},
	}

	proposals := gen.GenerateRepairProposals(vctx, checks, policy)

	if len(proposals) != 0 {
		t.Errorf("Expected 0 proposals when policy excludes capture_timing and frame_resolution, got %d", len(proposals))
	}
}

func TestRepairProposalGenerator_ValidateCompliance(t *testing.T) {
	config := &model.ValidationConfig{EnableRuntimeRepair: true}
	gen := NewRepairProposalGenerator(config)

	vctx := &model.BrowserAgentValidationContext{
		RunID:                     "run-validate-001",
		SourcePackageID:           "pkg-validate-001",
		SourceBundleHashSHA256:    "bundlehash-abc",
		EffectivePolicyHashSHA256: "policyhash-def",
	}

	checks := []model.ValidationCheck{
		{
			ID:       "check-1",
			Kind:     "wait_strategy",
			Code:     "WAIT_TIMEOUT",
			NodeID:   "node-1",
			StageID:  "stage-1",
			Severity: model.FindingSeverityWarning,
			Passed:   false,
			Required: false,
			Summary:  "Wait timeout after 5000ms",
		},
	}

	policy := &model.BrowserAgentRepairPolicy{
		AllowedRepairKinds: []string{"wait_strategy"},
		EditableFields:     []string{"wait_timeout"},
	}

	proposals := gen.GenerateRepairProposals(vctx, checks, policy)
	if len(proposals) != 1 {
		t.Fatalf("Expected 1 proposal, got %d", len(proposals))
	}

	if err := proposals[0].Validate(); err != nil {
		t.Errorf("Generated proposal failed Validate(): %v", err)
	}
}
