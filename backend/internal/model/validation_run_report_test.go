package model

import (
	"strings"
	"testing"
	"time"
)

func TestValidationRunReportValidateRequiresFormalProvenance(t *testing.T) {
	report := validValidationRunReport()
	report.FormalAppServerSuccess = true
	report.AppGenerated = true
	report.TransportAuthenticated = true
	report.FormalExchange = true
	report.OriginalPackageUnchanged = false

	err := report.Validate()
	if err == nil || !strings.Contains(err.Error(), "original_package_unchanged") {
		t.Fatalf("formal report without unchanged package must fail, got %v", err)
	}
}

func TestValidationRunReportAllowsServerControlledNonFormalReport(t *testing.T) {
	report := validValidationRunReport()
	report.FormalAppServerSuccess = false
	report.SourceOrigin = "server_controlled_fixture"
	report.AppGenerated = false
	report.TransportAuthenticated = false
	report.FormalExchange = false
	report.OriginalPackageUnchanged = true

	if err := report.Validate(); err != nil {
		t.Fatalf("server-controlled report should validate as non-formal: %v", err)
	}
}

func TestValidationRunReportValidateRequiresFindingResponsibilityAndEvidence(t *testing.T) {
	report := validValidationRunReport()
	report.Findings = []ValidationRunFinding{{
		ID: "finding-1", Code: "stage_failed", Severity: FindingSeverityBlocking,
		Passed: false, Category: ValidationFailureCategoryServerExecution,
	}}

	err := report.Validate()
	if err == nil || !strings.Contains(err.Error(), "responsibility_domain") {
		t.Fatalf("failed finding without domain/evidence must fail, got %v", err)
	}
}

func TestValidationRunReportJSONCarriesOrderedStagesAndFeedback(t *testing.T) {
	report := validValidationRunReport()
	report.Stages = []ValidationRunStageSummary{{NodeID: "node-1", StageID: "stage-1", Order: 1, Status: "passed"}}
	report.AppFeedback = []ValidationFeedback{{Code: "business_input_missing", Summary: "App package is incomplete", NextStep: "Regenerate package"}}
	report.ServerFeedback = []ValidationFeedback{{Code: "stage_failed", Summary: "Runtime failed", NextStep: "Replay stage"}}
	if err := report.Validate(); err != nil {
		t.Fatalf("valid report rejected: %v", err)
	}
	if report.Stages[0].Order != 1 || report.AppFeedback[0].Code != "business_input_missing" || report.ServerFeedback[0].Code != "stage_failed" {
		t.Fatalf("report lost ordered stage or feedback data: %+v", report)
	}
}

func TestRecordingResultValidatesAttachedValidationRunReport(t *testing.T) {
	runReport := validValidationRunReport()
	result := RecordingResultPackage{SchemaVersion: RecordingResultPackageSchemaVersion, SourcePackageID: "pkg-other", ValidationRunReport: &runReport}
	if err := result.ValidateStatusContract(); err == nil {
		t.Fatal("result with mismatched validation run report package must fail")
	}
	runReport.PackageID = "pkg-1"
	result.SourcePackageID = "pkg-1"
	result.ValidationRunReport = &runReport
	if err := result.ValidateStatusContract(); err != nil {
		t.Fatalf("matching validation run report rejected: %v", err)
	}
}

func validValidationRunReport() ValidationRunReport {
	return ValidationRunReport{
		SchemaVersion: ValidationRunReportSchemaVersion,
		ReportID:      "validation-run-report-1", RunID: "run-1", PackageID: "pkg-1",
		BundleHashSHA256: "bundle-1", PolicyHashSHA256: "policy-1",
		Status: "success", FinalDecision: ValidationDecisionContinue,
		OriginalPackageUnchanged: true, SourceOrigin: "server_controlled_fixture",
		ReproducibilityConditions: []string{"same package and policy hashes", "same runtime versions"},
		CreatedAt:                 time.Now().UTC(),
	}
}
