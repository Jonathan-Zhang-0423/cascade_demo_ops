package app

import (
	"testing"

	"cascade-demoops/backend/internal/model"
)

func TestBrowserAgentRepairPolicyApprovesBoundedWaitRepair(t *testing.T) {
	plan, stage := repairPolicyFixture(t)
	proposal := repairProposalFor(plan, stage, "wait_strategy", "script_outline.stages[].wait_conditions", "wait_after_entry_at_least_1000ms", "wait_after_entry_at_least_1500ms")
	patched, entry := evaluateBrowserAgentRepair(plan, stage, proposal, nil)
	if !entry.Applied || entry.PolicyDecision != model.ValidationDecisionRepairAllowed {
		t.Fatalf("approved wait repair was rejected: %+v", entry)
	}
	if patched.WaitConditions[0] != "wait_after_entry_at_least_1500ms" || stage.WaitConditions[0] != "wait_after_entry_at_least_1000ms" {
		t.Fatalf("repair must patch only the in-memory stage: before=%+v after=%+v", stage.WaitConditions, patched.WaitConditions)
	}
}

func TestBrowserAgentRepairPolicyRejectsImmutableCrossStageAndLowConfidenceRepairs(t *testing.T) {
	plan, stage := repairPolicyFixture(t)
	immutable := repairProposalFor(plan, stage, "wait_strategy", "script_outline.stages[].objective", "Invite flow starts", "Delete workspace")
	_, entry := evaluateBrowserAgentRepair(plan, stage, immutable, nil)
	if entry.Applied || entry.Reason == "" {
		t.Fatalf("immutable business field must never be patched: %+v", entry)
	}

	crossStage := repairProposalFor(plan, stage, "wait_strategy", "script_outline.stages[].wait_conditions", "wait_after_entry_at_least_1000ms", "wait_after_entry_at_least_1500ms")
	crossStage.NodeID = "node_open_dashboard"
	_, entry = evaluateBrowserAgentRepair(plan, stage, crossStage, nil)
	if entry.Applied || entry.Reason == "" {
		t.Fatalf("cross-stage repair must be rejected: %+v", entry)
	}

	lowConfidence := repairProposalFor(plan, stage, "wait_strategy", "script_outline.stages[].wait_conditions", "wait_after_entry_at_least_1000ms", "wait_after_entry_at_least_1500ms")
	lowConfidence.Confidence = .89
	_, entry = evaluateBrowserAgentRepair(plan, stage, lowConfidence, nil)
	if entry.Applied || entry.Reason == "" {
		t.Fatalf("low-confidence repair must be rejected: %+v", entry)
	}
}

func TestBrowserAgentRepairPolicyEnforcesAttemptLimitAndRejectsUnsupportedKinds(t *testing.T) {
	plan, stage := repairPolicyFixture(t)
	first := repairProposalFor(plan, stage, "wait_strategy", "script_outline.stages[].wait_conditions", "wait_after_entry_at_least_1000ms", "wait_after_entry_at_least_1500ms")
	_, firstEntry := evaluateBrowserAgentRepair(plan, stage, first, nil)
	second := repairProposalFor(plan, stage, "wait_strategy", "script_outline.stages[].wait_conditions", "wait_after_entry_at_least_1000ms", "wait_after_entry_at_least_2000ms")
	_, secondEntry := evaluateBrowserAgentRepair(plan, stage, second, []model.RuntimePatchLedgerEntry{firstEntry})
	if !secondEntry.Applied {
		t.Fatalf("second approved repair should fit the fixture limit: %+v", secondEntry)
	}
	third := repairProposalFor(plan, stage, "wait_strategy", "script_outline.stages[].wait_conditions", "wait_after_entry_at_least_1000ms", "wait_after_entry_at_least_2500ms")
	_, thirdEntry := evaluateBrowserAgentRepair(plan, stage, third, []model.RuntimePatchLedgerEntry{firstEntry, secondEntry})
	if thirdEntry.Applied {
		t.Fatalf("repair attempt beyond the contract limit must be rejected: %+v", thirdEntry)
	}

	unsupported := repairProposalFor(plan, stage, "input_value", "script_outline.stages[].wait_conditions", "", "other@example.com")
	_, entry := evaluateBrowserAgentRepair(plan, stage, unsupported, nil)
	if entry.Applied {
		t.Fatalf("business input repair must be rejected: %+v", entry)
	}
}

func TestBrowserAgentRepairPolicyCreatesMissingCapturePlanWithinBounds(t *testing.T) {
	plan, stage := repairPolicyFixture(t)
	stage.CapturePlan = nil
	proposal := repairProposalFor(plan, stage, "capture_timing", "script_outline.stages[].capture_plan.pre_capture_wait_ms", "", "1200")
	patched, entry := evaluateBrowserAgentRepair(plan, stage, proposal, nil)
	if !entry.Applied || patched.CapturePlan == nil || patched.CapturePlan.PreCaptureWaitMS != 1200 {
		t.Fatalf("bounded capture plan creation was not applied: entry=%+v stage=%+v", entry, patched.CapturePlan)
	}
	invalid := repairProposalFor(plan, stage, "capture_timing", "script_outline.stages[].capture_plan.hold_after_ms", "", "16000")
	_, rejected := evaluateBrowserAgentRepair(plan, stage, invalid, nil)
	if rejected.Applied || rejected.Reason == "" {
		t.Fatalf("out-of-bound capture timing must be rejected: %+v", rejected)
	}
}

func TestBrowserAgentRepairPolicyRejectsSelectorNotDeclaredByApp(t *testing.T) {
	plan, stage := repairPolicyFixture(t)
	proposal := repairProposalFor(plan, stage, "selector_alternative", "script_outline.stages[].components[].selector", stage.Components[0].Selector, "testid:server-invented-target")
	_, entry := evaluateBrowserAgentRepair(plan, stage, proposal, nil)
	if entry.Applied || entry.Reason == "" {
		t.Fatalf("server-invented selector must not be applied: %+v", entry)
	}
}

func TestBrowserAgentRepairPolicyRejectsWaitShorteningAndOutOfRangeWaits(t *testing.T) {
	plan, stage := repairPolicyFixture(t)
	shorter := repairProposalFor(plan, stage, "wait_strategy", "script_outline.stages[].wait_conditions", "wait_after_entry_at_least_1000ms", "wait_after_entry_at_least_500ms")
	_, entry := evaluateBrowserAgentRepair(plan, stage, shorter, nil)
	if entry.Applied || entry.Reason == "" {
		t.Fatalf("wait repairs must only increase the declared wait: %+v", entry)
	}
	outOfRange := repairProposalFor(plan, stage, "wait_strategy", "script_outline.stages[].wait_conditions", "wait_after_entry_at_least_1000ms", "wait_after_entry_at_least_16000ms")
	_, entry = evaluateBrowserAgentRepair(plan, stage, outOfRange, nil)
	if entry.Applied || entry.Reason == "" {
		t.Fatalf("wait repairs must remain within the bounded maximum: %+v", entry)
	}
}

func repairPolicyFixture(t *testing.T) (BrowserAgentRuntimePlan, BrowserAgentRuntimeStage) {
	t.Helper()
	pkg := readBrowserAgentOutlineFixture(t)
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	return plan, plan.Stages[1]
}

func repairProposalFor(plan BrowserAgentRuntimePlan, stage BrowserAgentRuntimeStage, kind, field, before, after string) model.RuntimeRepairProposal {
	return model.RuntimeRepairProposal{
		SchemaVersion: model.RuntimeRepairProposalSchemaVersion, ProposalID: "proposal_" + kind + "_" + after,
		RunID: plan.RunID, NodeID: stage.NodeID, StageID: stage.ID,
		BaseBundleHashSHA256: plan.SourceBundleHashSHA256, PolicyHashSHA256: plan.PolicyHashSHA256,
		RepairKind: kind, Field: field, Before: before, After: after, Confidence: .95,
		EvidenceRefs: []model.EvidenceRef{{ID: "evidence_repair", Kind: model.EvidenceKindBrowserTrace}}, CreatedAt: timeNowUTC(),
	}
}
