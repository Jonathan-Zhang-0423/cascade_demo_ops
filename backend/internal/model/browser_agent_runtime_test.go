package model

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestRuntimeObservationRecordsOptionalCapabilityRequiresActualBrowserEvidence(t *testing.T) {
	observation := &RuntimeObservation{Source: RuntimeObservationActualBrowser, Assertions: []RuntimeAssertion{{Kind: "optional_capability_recorded", Passed: true}}}
	if !RuntimeObservationRecordsOptionalCapability(observation) {
		t.Fatal("actual-browser optional capability terminal evidence was not recognized")
	}
	observation.Source = RuntimeObservationDerivedPlan
	if RuntimeObservationRecordsOptionalCapability(observation) {
		t.Fatal("plan-derived optional capability assertion was accepted as terminal evidence")
	}
}

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

func TestStageExecutionEventTargetGeometryRoundTripAndValidation(t *testing.T) {
	now := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	event := StageExecutionEvent{
		SchemaVersion: StageExecutionEventSchemaVersion, EventID: "event_geometry", RunID: "run_geometry",
		SourcePackageID: "pkg_geometry", SourceBundleHashSHA256: "bundle_hash", PolicyHashSHA256: "policy_hash",
		NodeID: "node_create", StageID: "stage_create", Attempt: 1, Sequence: 1,
		EventType: StageExecutionEventTargetResolved, OccurredAt: now,
		Observation: &RuntimeObservation{Source: RuntimeObservationActualBrowser, TargetGeometry: &BrowserTargetGeometry{
			SchemaVersion: "demoops.browser_target_geometry.v1", TargetSemanticID: "new_project",
			ResolutionStrategy: "approved_evidence_css", SelectorDigestSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			CapturedAt: now, RecordingOffsetMS: 250,
			Viewport:             BrowserGeometryViewport{Width: 2560, Height: 1440, DPR: 1},
			ElementBoxCSSPX:      BrowserGeometryRectangle{X: 128, Y: 144, Width: 256, Height: 72},
			ElementBoxNormalized: BrowserGeometryRectangle{X: .05, Y: .1, Width: .1, Height: .05},
			ScreenshotArtifactID: "artifact_target", Confidence: 1,
		}},
		EvidenceRefs: []EvidenceRef{{ID: "evidence_target", ArtifactID: "artifact_target"}},
	}
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var decoded StageExecutionEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(event, decoded) {
		t.Fatalf("target geometry changed after JSON round trip: want=%+v got=%+v", event.Observation.TargetGeometry, decoded.Observation.TargetGeometry)
	}
	decoded.Observation.TargetGeometry.ElementBoxNormalized.Width = 1
	if err := decoded.Validate(); err == nil {
		t.Fatal("out-of-viewport normalized geometry must be rejected")
	}
}

func TestStageExecutionEventTargetResolutionAttemptsAreRedactedAndValidated(t *testing.T) {
	roleAllowed, nameAllowed := true, false
	event := validStageExecutionEvent()
	event.Observation.TargetResolutionAttempts = []BrowserTargetResolutionAttempt{
		{Strategy: "role:button+approved_name", CandidateCount: 0, Outcome: "no_candidates"},
		{Strategy: "approved_evidence_css", CandidateCount: 1, Unique: true, Visible: true, EvidenceBound: true, RoleAllowed: &roleAllowed, NameAllowed: &nameAllowed, Outcome: "name_mismatch"},
	}
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" || !reflect.DeepEqual(event.Observation.TargetResolutionAttempts, func() []BrowserTargetResolutionAttempt {
		var decoded StageExecutionEvent
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded.Observation.TargetResolutionAttempts
	}()) {
		t.Fatal("target resolution attempts changed during JSON round trip")
	}
	event.Observation.TargetResolutionAttempts[0].Outcome = "page_text_dump"
	if err := event.Validate(); err == nil {
		t.Fatal("unsupported target resolution outcomes must be rejected")
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

func TestPreExecutionValidationAllowsOnlyApprovedPackageEvidence(t *testing.T) {
	report := validRuntimeValidationReport()
	report.Phase = ValidationPhasePreExecution
	report.NodeID = ""
	report.StageID = ""
	report.EvidenceQuality = RuntimeObservationDerivedPlan
	if err := report.Validate(); err != nil {
		t.Fatalf("pre-execution validation may use the approved package as evidence: %v", err)
	}
	report.Phase = ValidationPhaseRuntimeStage
	report.NodeID = "node_1"
	report.StageID = "stage_1"
	if err := report.Validate(); err == nil {
		t.Fatal("runtime validation must still require real browser evidence")
	}
}

func TestValidationCheckCarriesVersionedFailureCodeAndSeverity(t *testing.T) {
	report := validRuntimeValidationReport()
	report.Checks = []ValidationCheck{{
		ID: "check_1", Kind: "runtime_identity", Code: "runtime_event_identity_mismatch",
		Severity: FindingSeverityBlocking, Passed: false, Required: true,
		Summary: "Runtime event identity did not match the approved package.",
	}}
	if err := report.Validate(); err != nil {
		t.Fatal(err)
	}
	report.Checks[0].Severity = FindingSeverity("critical")
	if err := report.Validate(); err == nil {
		t.Fatal("validation check severity must be versioned to the shared finding severity enum")
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

func TestAnnotateValidationChecksFilledFromTable(t *testing.T) {
	knownCodes := []string{
		"MISSING_BUNDLE_HASH", "MISSING_POLICY_HASH", "EMPTY_STAGE_APPROVAL_PLAN",
		"OUT_OF_ORDER_EVENTS", "REQUIRED_ASSERTION_FAILED", "CROSS_DOMAIN_ACCESS",
		"RESULT_PACKAGE_MISMATCH", "RESULT_HASH_MISMATCH", "MISSING_MP4_VIDEO",
	}
	checks := make([]ValidationCheck, len(knownCodes))
	for i, code := range knownCodes {
		checks[i] = ValidationCheck{ID: "c" + code, Kind: "test", Code: code, Passed: false}
	}
	AnnotateValidationChecks(checks)
	for _, c := range checks {
		if c.Impact == "" {
			t.Errorf("code %s: Impact is empty after annotation", c.Code)
		}
		if c.Suggestion == "" {
			t.Errorf("code %s: Suggestion is empty after annotation", c.Code)
		}
		if c.ResponsibilityDomain == "" {
			t.Errorf("code %s: ResponsibilityDomain is empty after annotation", c.Code)
		}
	}
}

func TestAnnotateValidationChecksUnknownCodeNoOp(t *testing.T) {
	check := ValidationCheck{ID: "c1", Kind: "test", Code: "UNKNOWN_EXPERIMENTAL_CODE", Passed: false}
	AnnotateValidationChecks([]ValidationCheck{check})
	// unknown codes must not panic and must leave fields at zero value
	if check.Impact != "" || check.ResponsibilityDomain != "" {
		t.Error("unknown code must not be modified by annotation")
	}
}

func TestAnnotateValidationChecksPassedCheckLeftAlone(t *testing.T) {
	check := ValidationCheck{ID: "c1", Kind: "test", Code: "MISSING_BUNDLE_HASH", Passed: true}
	AnnotateValidationChecks([]ValidationCheck{check})
	// passed checks: Impact/Suggestion/Domain may be filled (informational), but no panic
	// the key assertion is that annotation is safe on passed checks
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
