package app

import (
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func timePtr(value time.Time) *time.Time { return &value }

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

func TestEvidenceBoundSelectorCandidatesRequireSameAppEvidence(t *testing.T) {
	shared := model.EvidenceRef{ID: "ev_verified_new_project", Kind: model.EvidenceKindBrowserScan, Confidence: 1}
	other := model.EvidenceRef{ID: "ev_project_list", Kind: model.EvidenceKindBrowserScan, Confidence: 1}
	components := []model.BrowserAgentComponentTarget{
		{ComponentRef: "component:wrong-primary", Selector: `[data-testid="project-list"]`, EvidenceRefs: []model.EvidenceRef{other}},
		{ComponentRef: "component:new-project", Selector: `[data-testid="button-new-project"]`, Confidence: .9, EvidenceRefs: []model.EvidenceRef{shared}, SelectorAlternatives: []model.SelectorCandidate{formalSelectorCandidateForTest(`[data-testid="button-new-project"]`, shared, "New project")}},
		{ComponentRef: "component:unrelated", Selector: `[data-testid="delete-project"]`, EvidenceRefs: []model.EvidenceRef{other}},
	}
	interactions := []model.BrowserAgentInteraction{{
		Kind:   model.GraphActionClick,
		Target: model.ActionTarget{Selector: `[data-testid="project-list"]`, EvidenceRefs: []model.EvidenceRef{shared}},
	}}

	candidates := evidenceBoundSelectorCandidates(components, interactions)
	if len(candidates) != 1 || candidates[0].Kind != "css" || candidates[0].Value != `[data-testid="button-new-project"]` {
		t.Fatalf("only the selector bound to the interaction evidence may be recovered: %+v", candidates)
	}
	if len(candidates[0].EvidenceRefs) != 1 || candidates[0].EvidenceRefs[0].ID != shared.ID {
		t.Fatalf("candidate must retain the App evidence binding: %+v", candidates[0].EvidenceRefs)
	}
}

func TestEvidenceBoundSelectorCandidatesRejectMissingAndAmbiguousBindings(t *testing.T) {
	shared := model.EvidenceRef{ID: "ev_shared", Kind: model.EvidenceKindBrowserScan, Confidence: 1}
	withoutBinding := evidenceBoundSelectorCandidates(
		[]model.BrowserAgentComponentTarget{{Selector: `[data-testid="server-guess"]`, EvidenceRefs: []model.EvidenceRef{{ID: "ev_other"}}}},
		[]model.BrowserAgentInteraction{{Kind: model.GraphActionClick, Target: model.ActionTarget{EvidenceRefs: []model.EvidenceRef{shared}}}},
	)
	if len(withoutBinding) != 0 {
		t.Fatalf("Server must not invent a selector without the same App evidence: %+v", withoutBinding)
	}

	ambiguous := evidenceBoundSelectorCandidates(
		[]model.BrowserAgentComponentTarget{
			{Selector: `[data-testid="candidate-a"]`, EvidenceRefs: []model.EvidenceRef{shared}, SelectorAlternatives: []model.SelectorCandidate{formalSelectorCandidateForTest(`[data-testid="candidate-a"]`, shared, "Candidate A")}},
			{Selector: `[data-testid="candidate-b"]`, EvidenceRefs: []model.EvidenceRef{shared}, SelectorAlternatives: []model.SelectorCandidate{formalSelectorCandidateForTest(`[data-testid="candidate-b"]`, shared, "Candidate B")}},
		},
		[]model.BrowserAgentInteraction{{Kind: model.GraphActionClick, Target: model.ActionTarget{EvidenceRefs: []model.EvidenceRef{shared}}}},
	)
	if len(ambiguous) != 2 {
		t.Fatalf("all App-declared evidence matches must remain visible to Worker ambiguity checks: %+v", ambiguous)
	}
}

func TestBrowserAgentRepairPolicyAppliesEvidenceBoundSelectorWithoutMutatingStage(t *testing.T) {
	plan, stage := repairPolicyFixture(t)
	primary := currentStageSelector(stage)
	evidence := model.EvidenceRef{ID: "ev_verified_new_project", Kind: model.EvidenceKindBrowserScan}
	candidate := formalSelectorCandidateForTest(`[data-testid="button-new-project"]`, evidence, "New project")
	stage.EvidenceBoundSelectorAlternatives = []model.SelectorCandidate{candidate}
	proposal := repairProposalFor(plan, stage, "selector_alternative", "script_outline.stages[].components[].selector", primary, selectorCandidateEncoding(candidate))

	patched, entry := evaluateBrowserAgentRepair(plan, stage, proposal, nil)
	if !entry.Applied || entry.PolicyDecision != model.ValidationDecisionRepairAllowed {
		t.Fatalf("Worker-verified App evidence-bound selector should use the existing repair policy: %+v", entry)
	}
	if patched.PreferredSelectorAlternative == nil || selectorCandidateEncoding(*patched.PreferredSelectorAlternative) != selectorCandidateEncoding(candidate) {
		t.Fatalf("runtime copy did not receive the approved selector: %+v", patched.PreferredSelectorAlternative)
	}
	if stage.PreferredSelectorAlternative != nil || currentStageSelector(stage) != primary {
		t.Fatalf("repair must not mutate the App-derived runtime stage: %+v", stage)
	}
}

func TestBrowserAgentSelectorRepairUsesAppAuthorizedField(t *testing.T) {
	plan, stage := repairPolicyFixture(t)
	plan.RepairPolicy.EditableFields = []string{"action.target.selector"}
	plan.RepairPolicy.ImmutableFields = []string{"action.value", "node_id"}
	candidate := formalSelectorCandidateForTest(`[data-testid="button-new-project"]`, model.EvidenceRef{ID: "ev_verified_new_project", Kind: model.EvidenceKindBrowserScan}, "New project")
	stage.EvidenceBoundSelectorAlternatives = []model.SelectorCandidate{candidate}
	observed := BrowserAgentStageObservation{
		PreferredSelectorAlternative: &candidate,
		EvidenceRefs:                 []model.EvidenceRef{{ID: "live_page", Kind: model.EvidenceKindWebScreenshot}},
	}

	proposal := browserAgentSelectorAlternativeProposal(plan, stage, observed)
	if proposal.Field != "action.target.selector" {
		t.Fatalf("selector proposal must use the field authorized by the App contract: %+v", proposal)
	}
	_, entry := evaluateBrowserAgentRepair(plan, stage, proposal, nil)
	if !entry.Applied {
		t.Fatalf("App-authorized action target selector repair was rejected: %+v", entry)
	}
}

func TestBrowserAgentSelectorRepairDoesNotWidenAppPolicy(t *testing.T) {
	plan, stage := repairPolicyFixture(t)
	plan.RepairPolicy.EditableFields = []string{"action.wait_until"}
	candidate := formalSelectorCandidateForTest(`[data-testid="button-new-project"]`, model.EvidenceRef{ID: "ev_verified_new_project", Kind: model.EvidenceKindBrowserScan}, "New project")
	stage.EvidenceBoundSelectorAlternatives = []model.SelectorCandidate{candidate}
	proposal := browserAgentSelectorAlternativeProposal(plan, stage, BrowserAgentStageObservation{PreferredSelectorAlternative: &candidate})

	_, entry := evaluateBrowserAgentRepair(plan, stage, proposal, nil)
	if entry.Applied || entry.Reason != "repair field is not editable by the approved contract" {
		t.Fatalf("Server must not authorize a selector field absent from the App policy: %+v", entry)
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

func formalSelectorCandidateForTest(value string, evidence model.EvidenceRef, accessibleName string) model.SelectorCandidate {
	return model.SelectorCandidate{
		Kind: "css", Value: value, Confidence: 0.9, StabilityScore: 0.9, Source: "page_scan",
		EvidenceID: evidence.ID, SourceKind: "page_scan", SourceDigest: "sha256:page-scan",
		ObservedRole: "button", ObservedAccessibleName: accessibleName,
		ObservedAt: timePtr(time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)), EvidenceRefs: []model.EvidenceRef{evidence},
	}
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
