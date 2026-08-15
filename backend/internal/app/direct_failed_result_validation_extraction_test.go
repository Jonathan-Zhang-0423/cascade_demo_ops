package app

import (
	"testing"

	"cascade-demoops/backend/internal/model"
)

func TestExtractFirstBlockingValidationCode(t *testing.T) {
	tests := []struct {
		name     string
		reports  []model.ValidationReport
		wantCode string
	}{
		{
			name:     "empty reports",
			reports:  nil,
			wantCode: "",
		},
		{
			name: "no blocking failures",
			reports: []model.ValidationReport{
				{
					ReportID: "rpt-1",
					Checks: []model.ValidationCheck{
						{Code: "warning_code_1", Passed: false, Severity: model.FindingSeverityWarning},
						{Code: "passed_code", Passed: true, Severity: model.FindingSeverityBlocking},
					},
				},
			},
			wantCode: "",
		},
		{
			name: "single blocking failure",
			reports: []model.ValidationReport{
				{
					ReportID: "rpt-1",
					Checks: []model.ValidationCheck{
						{Code: "check_approved_outline_contract", Passed: false, Severity: model.FindingSeverityBlocking},
					},
				},
			},
			wantCode: "check_approved_outline_contract",
		},
		{
			name: "multiple blocking failures - returns first",
			reports: []model.ValidationReport{
				{
					ReportID: "rpt-1",
					Checks: []model.ValidationCheck{
						{Code: "warning_1", Passed: false, Severity: model.FindingSeverityWarning},
						{Code: "first_blocking", Passed: false, Severity: model.FindingSeverityBlocking},
						{Code: "second_blocking", Passed: false, Severity: model.FindingSeverityBlocking},
					},
				},
			},
			wantCode: "first_blocking",
		},
		{
			name: "blocking in second report",
			reports: []model.ValidationReport{
				{
					ReportID: "rpt-1",
					Checks: []model.ValidationCheck{
						{Code: "warning_1", Passed: false, Severity: model.FindingSeverityWarning},
					},
				},
				{
					ReportID: "rpt-2",
					Checks: []model.ValidationCheck{
						{Code: "blocking_in_second", Passed: false, Severity: model.FindingSeverityBlocking},
					},
				},
			},
			wantCode: "blocking_in_second",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFirstBlockingValidationCode(tt.reports)
			if got != tt.wantCode {
				t.Errorf("extractFirstBlockingValidationCode() = %q, want %q", got, tt.wantCode)
			}
		})
	}
}

func TestFilterBlockingReports(t *testing.T) {
	tests := []struct {
		name       string
		reports    []model.ValidationReport
		wantCount  int
		wantIDs    []string
	}{
		{
			name:      "empty reports",
			reports:   nil,
			wantCount: 0,
		},
		{
			name: "no blocking failures",
			reports: []model.ValidationReport{
				{ReportID: "rpt-1", Checks: []model.ValidationCheck{
					{Code: "w1", Passed: false, Severity: model.FindingSeverityWarning},
				}},
			},
			wantCount: 0,
		},
		{
			name: "one report with blocking",
			reports: []model.ValidationReport{
				{ReportID: "rpt-1", Checks: []model.ValidationCheck{
					{Code: "b1", Passed: false, Severity: model.FindingSeverityBlocking},
				}},
			},
			wantCount: 1,
			wantIDs:   []string{"rpt-1"},
		},
		{
			name: "mixed - only blocking reports returned",
			reports: []model.ValidationReport{
				{ReportID: "rpt-warning", Checks: []model.ValidationCheck{
					{Code: "w1", Passed: false, Severity: model.FindingSeverityWarning},
				}},
				{ReportID: "rpt-blocking-1", Checks: []model.ValidationCheck{
					{Code: "b1", Passed: false, Severity: model.FindingSeverityBlocking},
				}},
				{ReportID: "rpt-all-pass", Checks: []model.ValidationCheck{
					{Code: "p1", Passed: true, Severity: model.FindingSeverityBlocking},
				}},
				{ReportID: "rpt-blocking-2", Checks: []model.ValidationCheck{
					{Code: "w2", Passed: false, Severity: model.FindingSeverityWarning},
					{Code: "b2", Passed: false, Severity: model.FindingSeverityBlocking},
				}},
			},
			wantCount: 2,
			wantIDs:   []string{"rpt-blocking-1", "rpt-blocking-2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterBlockingReports(tt.reports)
			if len(got) != tt.wantCount {
				t.Errorf("filterBlockingReports() count = %d, want %d", len(got), tt.wantCount)
			}
			if tt.wantIDs != nil {
				gotIDs := make([]string, len(got))
				for i, r := range got {
					gotIDs[i] = r.ReportID
				}
				if len(gotIDs) != len(tt.wantIDs) {
					t.Errorf("filterBlockingReports() IDs = %v, want %v", gotIDs, tt.wantIDs)
				} else {
					for i, id := range gotIDs {
						if id != tt.wantIDs[i] {
							t.Errorf("filterBlockingReports() ID[%d] = %q, want %q", i, id, tt.wantIDs[i])
						}
					}
				}
			}
		})
	}
}
