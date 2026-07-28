package app

import (
	"fmt"
	"strings"

	"cascade-demoops/backend/internal/model"
)

// evaluateBrowserAgentRepair only creates a patched in-memory stage. The
// approved package and its hashes remain immutable throughout the run.
func evaluateBrowserAgentRepair(plan BrowserAgentRuntimePlan, stage BrowserAgentRuntimeStage, proposal model.RuntimeRepairProposal, prior []model.RuntimePatchLedgerEntry) (BrowserAgentRuntimeStage, model.RuntimePatchLedgerEntry) {
	entry := model.RuntimePatchLedgerEntry{
		SchemaVersion: model.RuntimePatchLedgerEntrySchemaVersion,
		EntryID:       fmt.Sprintf("patch_%s", safePathSegment(proposal.ProposalID)), ProposalID: proposal.ProposalID,
		RunID: plan.RunID, NodeID: stage.NodeID, StageID: stage.ID, Attempt: repairAttemptForStage(prior, stage),
		SourceBundleHashSHA256: plan.SourceBundleHashSHA256, PolicyHashSHA256: plan.PolicyHashSHA256,
		Field: proposal.Field, Before: proposal.Before, After: proposal.After,
		PolicyDecision: model.ValidationDecisionStopAndReport, EvidenceRefs: append([]model.EvidenceRef{}, proposal.EvidenceRefs...),
	}
	if reason := validateBrowserAgentRepair(plan, stage, proposal, prior); reason != "" {
		entry.Reason = reason
		return stage, entry
	}
	patched, ok := applyBrowserAgentRepair(stage, proposal)
	if !ok {
		entry.Reason = "proposal cannot be applied to the approved runtime stage"
		return stage, entry
	}
	entry.PolicyDecision = model.ValidationDecisionRepairAllowed
	entry.Applied = true
	entry.AppliedAt = timeNowUTC()
	entry.Reason = "approved bounded runtime repair"
	return patched, entry
}

func validateBrowserAgentRepair(plan BrowserAgentRuntimePlan, stage BrowserAgentRuntimeStage, proposal model.RuntimeRepairProposal, prior []model.RuntimePatchLedgerEntry) string {
	policy := plan.RepairPolicy
	if proposal.RequiresApproval || proposal.RunID != plan.RunID || proposal.BaseBundleHashSHA256 != plan.SourceBundleHashSHA256 || proposal.PolicyHashSHA256 != plan.PolicyHashSHA256 {
		return "proposal identity or approval requirement does not match the running contract"
	}
	if policy.SameNodeOnly && (proposal.NodeID != stage.NodeID || proposal.StageID != stage.ID) {
		return "proposal crosses the approved stage boundary"
	}
	if proposal.Confidence < policy.MinAutoApplyConfidence || !containsExact(policy.AllowedRepairKinds, proposal.RepairKind) {
		return "repair kind or confidence is not approved"
	}
	if fieldIsImmutable(policy.ImmutableFields, proposal.Field) || !fieldIsEditable(policy.EditableFields, proposal.Field) {
		return "repair field is not editable by the approved contract"
	}
	if policy.MaxRepairAttempts <= 0 || repairAttemptForStage(prior, stage) > policy.MaxRepairAttempts || (policy.MaxPatchOperations > 0 && len(prior) >= policy.MaxPatchOperations) {
		return "repair limit exceeded"
	}
	if !repairKindMatchesField(proposal.RepairKind, proposal.Field) {
		return "repair kind does not match its editable field"
	}
	return ""
}

func applyBrowserAgentRepair(stage BrowserAgentRuntimeStage, proposal model.RuntimeRepairProposal) (BrowserAgentRuntimeStage, bool) {
	// Stage contains slices and a capture-plan pointer. A shallow struct copy
	// would mutate the approved runtime plan while applying a one-run patch.
	patched := cloneBrowserAgentRuntimeStage(stage)
	switch proposal.RepairKind {
	case "selector_alternative":
		candidate, ok := approvedSelectorCandidate(stage, proposal.After)
		if !ok || (proposal.Before != "" && proposal.Before != currentStageSelector(stage)) {
			return stage, false
		}
		patched.PreferredSelectorAlternative = &candidate
		return patched, true
	case "wait_strategy":
		if proposal.Before != "" && !containsExact(patched.WaitConditions, proposal.Before) {
			return stage, false
		}
		if proposal.Before != "" && !strictlyLongerWaitCondition(proposal.Before, proposal.After) {
			return stage, false
		}
		if proposal.Before == "" {
			if _, ok := boundedEntryWaitMilliseconds(proposal.After); !ok {
				return stage, false
			}
		}
		if proposal.Before != "" {
			for index := range patched.WaitConditions {
				if patched.WaitConditions[index] == proposal.Before {
					patched.WaitConditions[index] = proposal.After
					return patched, true
				}
			}
		}
		patched.WaitConditions = append(patched.WaitConditions, proposal.After)
		return patched, true
	case "capture_timing":
		if !strings.HasPrefix(proposal.Field, "script_outline.stages[].capture_plan") {
			return stage, false
		}
		value, ok := positiveRepairMilliseconds(proposal.After)
		if !ok || (proposal.Before != "" && strings.TrimSpace(proposal.Before) != "0" && !isPositiveRepairMilliseconds(proposal.Before)) {
			return stage, false
		}
		if patched.CapturePlan == nil {
			patched.CapturePlan = &model.BrowserAgentCapturePlan{}
		}
		if strings.HasSuffix(proposal.Field, "pre_capture_wait_ms") {
			patched.CapturePlan.PreCaptureWaitMS = value
			return patched, true
		}
		if strings.HasSuffix(proposal.Field, "hold_after_ms") {
			patched.CapturePlan.HoldAfterMS = value
			return patched, true
		}
	}
	return stage, false
}

func approvedSelectorCandidate(stage BrowserAgentRuntimeStage, encoded string) (model.SelectorCandidate, bool) {
	for _, component := range stage.Components {
		if stage.TargetContract.ComponentRef != "" && component.ComponentRef != stage.TargetContract.ComponentRef {
			continue
		}
		for _, candidate := range component.SelectorAlternatives {
			if selectorCandidateEncoding(candidate) == encoded {
				return candidate, true
			}
		}
	}
	return model.SelectorCandidate{}, false
}

func cloneBrowserAgentRuntimeStage(stage BrowserAgentRuntimeStage) BrowserAgentRuntimeStage {
	copy := stage
	copy.Components = append([]model.BrowserAgentComponentTarget{}, stage.Components...)
	copy.Interactions = append([]model.BrowserAgentInteraction{}, stage.Interactions...)
	copy.WaitConditions = append([]string{}, stage.WaitConditions...)
	copy.CapturePoints = append([]string{}, stage.CapturePoints...)
	copy.Validations = append([]model.ValidationSpec{}, stage.Validations...)
	if stage.PreferredSelectorAlternative != nil {
		candidate := *stage.PreferredSelectorAlternative
		copy.PreferredSelectorAlternative = &candidate
	}
	if stage.CapturePlan != nil {
		capture := *stage.CapturePlan
		capture.RequiredAssets = append([]string{}, stage.CapturePlan.RequiredAssets...)
		capture.Notes = append([]string{}, stage.CapturePlan.Notes...)
		copy.CapturePlan = &capture
	}
	return copy
}

func repairAttemptForStage(prior []model.RuntimePatchLedgerEntry, stage BrowserAgentRuntimeStage) int {
	attempt := 1
	for _, entry := range prior {
		if entry.NodeID == stage.NodeID && entry.StageID == stage.ID {
			attempt++
		}
	}
	return attempt
}

func repairKindMatchesField(kind, field string) bool {
	switch kind {
	case "selector_alternative":
		return strings.Contains(field, ".components[].selector")
	case "wait_strategy":
		return strings.HasSuffix(field, ".wait_conditions")
	case "capture_timing":
		return strings.Contains(field, ".capture_plan")
	default:
		return false
	}
}

func fieldIsEditable(fields []string, field string) bool {
	for _, allowed := range fields {
		allowed, field = normalizeRepairField(allowed), normalizeRepairField(field)
		if allowed == field || strings.HasPrefix(field, strings.TrimRight(allowed, ".")+".") {
			return true
		}
	}
	return false
}

func isPositiveRepairMilliseconds(value string) bool {
	_, ok := positiveRepairMilliseconds(value)
	return ok
}
func fieldIsImmutable(fields []string, field string) bool {
	for _, immutable := range fields {
		if strings.Contains(normalizeRepairField(field), normalizeRepairField(immutable)) {
			return true
		}
	}
	return false
}
func normalizeRepairField(value string) string {
	return strings.ReplaceAll(strings.TrimSpace(value), "[0]", "[]")
}
func containsExact(values []string, wanted string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == strings.TrimSpace(wanted) {
			return true
		}
	}
	return false
}

func positiveRepairMilliseconds(value string) (int, bool) {
	var milliseconds int
	if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &milliseconds); err != nil || milliseconds < 0 || milliseconds > 15_000 {
		return 0, false
	}
	return milliseconds, true
}
