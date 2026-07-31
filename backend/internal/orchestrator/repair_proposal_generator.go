package orchestrator

import (
	"cascade-demoops/backend/internal/model"
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// RepairProposalGenerator generates RuntimeRepairProposal instances based on validation failures.
//
// P1 Requirements (handoff section 6.3):
// - Only propose repairs within browser_agent_contract.repair_policy.allowed_repair_kinds
// - Never propose: changing business input, routes, success definition, stage order, permissions, credentials, secret_ref, destructive operations
// - All proposals must include: run/stage identity, two hash types, repair kind, field before/after, confidence, evidence, requires_approval flag
type RepairProposalGenerator struct {
	config *model.ValidationConfig
}

func NewRepairProposalGenerator(config *model.ValidationConfig) *RepairProposalGenerator {
	return &RepairProposalGenerator{
		config: config,
	}
}

// GenerateRepairProposals generates repair proposals from validation failures.
//
// Only allowed repair kinds (from handoff section 6.3):
// - selector_alternative: suggest alternative CSS/XPath selector
// - wait_strategy: adjust wait timeout or condition
// - capture_timing: adjust screenshot/assertion timing
// - same_domain_exploration: non-destructive navigation within same domain
// - frame_resolution: fix iframe/frame targeting
//
// Absolute prohibitions:
// - Changing business input data
// - Changing routes or expected URLs
// - Changing success criteria or outcome definitions
// - Changing stage order or dependencies
// - Changing permission boundaries
// - Reading/writing credentials or secret_ref
// - Any destructive operations (delete, truncate, drop, etc.)
func (g *RepairProposalGenerator) GenerateRepairProposals(
	vctx *model.BrowserAgentValidationContext,
	checks []model.ValidationCheck,
	repairPolicy *model.BrowserAgentRepairPolicy,
) []model.RuntimeRepairProposal {
	if repairPolicy == nil || len(repairPolicy.AllowedRepairKinds) == 0 {
		// No repair policy means no repairs allowed
		return nil
	}

	if !g.config.EnableRuntimeRepair {
		// Config disables runtime repair
		return nil
	}

	proposals := []model.RuntimeRepairProposal{}

	// Build allowed repair kinds map for fast lookup
	allowedKinds := make(map[string]bool)
	for _, kind := range repairPolicy.AllowedRepairKinds {
		allowedKinds[kind] = true
	}

	for _, check := range checks {
		if check.Passed {
			continue // No repair needed for passing checks
		}

		// Generate repair proposal based on check kind
		proposal := g.generateProposalForCheck(vctx, check, allowedKinds, repairPolicy)
		if proposal != nil {
			proposals = append(proposals, *proposal)
		}
	}

	return proposals
}

func (g *RepairProposalGenerator) generateProposalForCheck(
	vctx *model.BrowserAgentValidationContext,
	check model.ValidationCheck,
	allowedKinds map[string]bool,
	repairPolicy *model.BrowserAgentRepairPolicy,
) *model.RuntimeRepairProposal {
	if check.NodeID == "" || check.StageID == "" {
		return nil
	}

	// Map validation check kind to repair kind
	var repairKind string
	var field string
	var after string
	var confidence float64
	var requiresApproval bool

	switch check.Kind {
	case "stage_failure":
		// Stage failed - try to infer repair from the failure reason
		if check.Code == "STAGE_FAILED" {
			// Check observation title to determine repair type
			failureReason := check.Summary
			if strings.Contains(failureReason, "Selector") || strings.Contains(failureReason, "not found") {
				if !allowedKinds["selector_alternative"] {
					return nil
				}
				repairKind = "selector_alternative"
				field = "selector"
				after = "Suggest alternative selector based on failure"
				confidence = 0.3
				requiresApproval = true
			} else if strings.Contains(failureReason, "Timeout") || strings.Contains(failureReason, "waiting") {
				if !allowedKinds["wait_strategy"] {
					return nil
				}
				repairKind = "wait_strategy"
				field = "wait_timeout"
				after = "Increase timeout to handle slow loading"
				confidence = 0.6
				requiresApproval = false
			} else {
				// Generic stage failure - no specific repair available
				return nil
			}
		}

	case "selector_validity":
		// Selector failed to match elements
		if !allowedKinds["selector_alternative"] {
			return nil // Not allowed
		}
		repairKind = "selector_alternative"
		field = "selector"
		after = "Suggest alternative selector (requires manual inspection)"
		confidence = 0.3 // Low confidence without actual DOM analysis
		requiresApproval = true

	case "wait_strategy":
		// Wait timeout or condition issue
		if !allowedKinds["wait_strategy"] {
			return nil
		}
		repairKind = "wait_strategy"
		field = "wait_timeout"
		after = "Increase timeout by 50%"
		confidence = 0.6
		requiresApproval = false // Can auto-apply if confidence meets threshold

	case "capture_timing":
		// Screenshot or assertion timing issue
		if !allowedKinds["capture_timing"] {
			return nil
		}
		repairKind = "capture_timing"
		field = "capture_delay_ms"
		after = "Add 500ms delay before capture"
		confidence = 0.7
		requiresApproval = false

	case "frame_resolution":
		// Frame targeting issue
		if !allowedKinds["frame_resolution"] {
			return nil
		}
		repairKind = "frame_resolution"
		field = "frame_selector"
		after = "Auto-detect frame containing target element"
		confidence = 0.5
		requiresApproval = true

	case "navigation_path":
		// Navigation path issue
		if !allowedKinds["same_domain_exploration"] {
			return nil
		}
		// Check if navigation is within same domain
		if !g.isSameDomainNavigation(check) {
			return nil // Cross-domain navigation not allowed
		}
		repairKind = "same_domain_exploration"
		field = "navigation_url"
		after = "Suggest alternative same-domain path"
		confidence = 0.4
		requiresApproval = true

	default:
		// No repair strategy for this check kind
		return nil
	}

	// Apply repair policy constraints
	if repairPolicy.MinAutoApplyConfidence > 0 && confidence < repairPolicy.MinAutoApplyConfidence {
		requiresApproval = true
	}

	// Check if field is editable
	if len(repairPolicy.EditableFields) > 0 {
		fieldAllowed := false
		for _, ef := range repairPolicy.EditableFields {
			if ef == field {
				fieldAllowed = true
				break
			}
		}
		if !fieldAllowed {
			return nil // Field not in editable list
		}
	}

	// Check if field is immutable
	for _, imf := range repairPolicy.ImmutableFields {
		if imf == field {
			return nil // Field is immutable
		}
	}

	if allowed, _ := IsRepairAllowed(repairKind, field, repairPolicy); !allowed {
		return nil
	}

	proposal := &model.RuntimeRepairProposal{
		SchemaVersion:        model.RuntimeRepairProposalSchemaVersion,
		ProposalID:           fmt.Sprintf("repair_%d_%d", time.Now().UnixNano(), rand.Intn(10000)),
		RunID:                vctx.RunID,
		NodeID:               check.NodeID,
		StageID:              check.StageID,
		BaseBundleHashSHA256: vctx.SourceBundleHashSHA256,
		PolicyHashSHA256:     vctx.EffectivePolicyHashSHA256,
		RepairKind:           repairKind,
		Field:                field,
		Before:               "",
		After:                after,
		Confidence:           confidence,
		EvidenceRefs:         check.EvidenceRefs,
		RequiresApproval:     requiresApproval,
		CreatedAt:            time.Now(),
	}

	return proposal
}

func (g *RepairProposalGenerator) isSameDomainNavigation(check model.ValidationCheck) bool {
	// For now, conservatively assume cross-domain unless we have explicit same-domain evidence
	// In real implementation, would parse URLs from check.Summary or evidence
	return false
}

// IsRepairAllowed checks if a repair kind is allowed by policy.
// P1 absolute prohibitions enforced here.
func IsRepairAllowed(repairKind string, field string, repairPolicy *model.BrowserAgentRepairPolicy) (bool, string) {
	if repairPolicy == nil {
		return false, "no repair policy defined"
	}

	// Absolute prohibitions from handoff section 6.3
	prohibitedFields := []string{
		"business_input",
		"input_data",
		"form_data",
		"target_route",
		"expected_route",
		"success_criteria",
		"outcome_definition",
		"stage_order",
		"stage_dependencies",
		"permission_boundary",
		"credentials",
		"secret_ref",
		"auth_token",
		"api_key",
	}

	for _, pf := range prohibitedFields {
		if field == pf {
			return false, fmt.Sprintf("field '%s' is absolutely prohibited from repair", field)
		}
	}

	// Check if repair kind is in allowed list
	allowed := false
	for _, ak := range repairPolicy.AllowedRepairKinds {
		if ak == repairKind {
			allowed = true
			break
		}
	}

	if !allowed {
		return false, fmt.Sprintf("repair kind '%s' not in allowed_repair_kinds", repairKind)
	}

	// Check immutable fields
	for _, imf := range repairPolicy.ImmutableFields {
		if imf == field {
			return false, fmt.Sprintf("field '%s' is marked immutable", field)
		}
	}

	return true, ""
}
