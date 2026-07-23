package model

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestStageExecutionEventJSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	want := StageExecutionEvent{
		SchemaVersion: StageExecutionEventSchemaVersion, EventID: "event_1", RunID: "run_1",
		SourcePackageID: "pkg_1", SourceBundleHashSHA256: "bundle_hash", PolicyHashSHA256: "policy_hash",
		NodeID: "node_create", StageID: "stage_create", Attempt: 1, Sequence: 4,
		EventType: StageExecutionEventOutcomeObserved, OccurredAt: now,
		Observation: &RuntimeObservation{
			Source: RuntimeObservationActualBrowser, URL: "https://app.example.com/projects/1", Title: "项目详情",
			Assertions: []RuntimeAssertion{{Kind: "url_matches", Passed: true, Actual: "/projects/1"}},
		},
		EvidenceRefs: []EvidenceRef{{ID: "screenshot_1", Kind: EvidenceKindBrowserTrace}},
	}
	if err := want.Validate(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got StageExecutionEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("event changed after JSON round trip:\nwant: %+v\n got: %+v", want, got)
	}
}

func TestValidationReportRequiresActualEvidenceToContinue(t *testing.T) {
	report := validRuntimeValidationReport()
	report.EvidenceQuality = RuntimeObservationDerivedPlan
	if err := report.Validate(); err == nil {
		t.Fatal("plan-derived evidence must not authorize continue")
	}
	report.EvidenceQuality = RuntimeObservationActualBrowser
	report.EvidenceRefs = nil
	if err := report.Validate(); err == nil {
		t.Fatal("continue without evidence refs must be rejected")
	}
	report.EvidenceRefs = []EvidenceRef{{ID: "artifact_1", Kind: EvidenceKindBrowserTrace}}
	if err := report.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeContractsRejectSensitiveText(t *testing.T) {
	event := validStageExecutionEvent()
	event.Observation.Title = "Authorization: Bearer secret-value"
	if err := event.Validate(); err == nil {
		t.Fatal("event must reject sensitive observation text")
	}

	proposal := validRuntimeRepairProposal()
	proposal.After = "<html><body>full page</body></html>"
	if err := proposal.Validate(); err == nil {
		t.Fatal("repair proposal must reject full-page HTML")
	}
}

func TestRuntimeRepairProposalJSONRoundTrip(t *testing.T) {
	want := validRuntimeRepairProposal()
	if err := want.Validate(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got RuntimeRepairProposal
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("proposal changed after JSON round trip: want %+v, got %+v", want, got)
	}
}

func TestRuntimePatchLedgerRejectsInvalidApplicationState(t *testing.T) {
	entry := RuntimePatchLedgerEntry{
		SchemaVersion: RuntimePatchLedgerEntrySchemaVersion, EntryID: "patch_1", ProposalID: "proposal_1",
		RunID: "run_1", NodeID: "node_1", StageID: "stage_1", Attempt: 1,
		SourceBundleHashSHA256: "bundle_hash", PolicyHashSHA256: "policy_hash",
		Field: "script_outline.stages[0].interactions[0].selector", After: "button:has-text('新建')",
		PolicyDecision: ValidationDecisionRepairAllowed, Applied: true,
	}
	if err := entry.Validate(); err == nil {
		t.Fatal("applied patch without applied_at must be rejected")
	}
	entry.AppliedAt = time.Date(2026, 7, 22, 12, 0, 2, 0, time.UTC)
	if err := entry.Validate(); err != nil {
		t.Fatal(err)
	}
	entry.RolledBack = true
	entry.Applied = false
	if err := entry.Validate(); err == nil {
		t.Fatal("rollback before apply must be rejected")
	}
}

func validStageExecutionEvent() StageExecutionEvent {
	return StageExecutionEvent{
		SchemaVersion: StageExecutionEventSchemaVersion, EventID: "event_1", RunID: "run_1",
		SourcePackageID: "pkg_1", SourceBundleHashSHA256: "bundle_hash", PolicyHashSHA256: "policy_hash",
		NodeID: "node_1", StageID: "stage_1", Attempt: 1, Sequence: 1,
		EventType: StageExecutionEventOutcomeObserved, OccurredAt: time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC),
		Observation:  &RuntimeObservation{Source: RuntimeObservationActualBrowser, Title: "项目详情"},
		EvidenceRefs: []EvidenceRef{{ID: "artifact_1"}},
	}
}

func validRuntimeValidationReport() ValidationReport {
	return ValidationReport{
		SchemaVersion: ValidationReportSchemaVersion, ReportID: "report_1", RunID: "run_1",
		SourcePackageID: "pkg_1", SourceBundleHashSHA256: "bundle_hash", PolicyHashSHA256: "policy_hash",
		Phase: ValidationPhaseRuntimeStage, NodeID: "node_1", StageID: "stage_1",
		Decision: ValidationDecisionContinue, PassRate: 1, OverallConfidence: .95,
		EvidenceQuality: RuntimeObservationActualBrowser, EvidenceRefs: []EvidenceRef{{ID: "artifact_1"}},
		CreatedAt: time.Date(2026, 7, 22, 12, 0, 1, 0, time.UTC),
	}
}

func validRuntimeRepairProposal() RuntimeRepairProposal {
	return RuntimeRepairProposal{
		SchemaVersion: RuntimeRepairProposalSchemaVersion, ProposalID: "proposal_1", RunID: "run_1",
		NodeID: "node_1", StageID: "stage_1", BaseBundleHashSHA256: "bundle_hash", PolicyHashSHA256: "policy_hash",
		RepairKind: "selector_alternative", Field: "script_outline.stages[0].interactions[0].selector",
		Before: "[data-testid='create']", After: "button:has-text('新建')", Confidence: .92,
		EvidenceRefs: []EvidenceRef{{ID: "artifact_1"}}, CreatedAt: time.Date(2026, 7, 22, 12, 0, 1, 0, time.UTC),
	}
}
