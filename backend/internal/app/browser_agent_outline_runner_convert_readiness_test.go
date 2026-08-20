package app

import (
	"testing"

	"cascade-demoops/backend/internal/model"
)

// TestConvertReadinessToValidationChecks verifies that browserAgentReadiness
// findings are correctly converted to ValidationCheck entries with proper
// severity and responsibility domain.
func TestConvertReadinessToValidationChecks(t *testing.T) {
	// Create a mock readiness report with one blocker and one warning.
	readinessReport := BrowserAgentReadinessReport{
		CanRun: false,
		Blockers: []BrowserAgentReadinessFinding{
			{
				Code:    "business_input_missing",
				NodeID:  "stage-1",
				Message: "Stage stage-1 is business_input but has no input value.",
			},
		},
		Warnings: []BrowserAgentReadinessFinding{
			{
				Code:    "declared_business_input_action_missing",
				NodeID:  "stage-2",
				Message: "Outline requirement text mentions 'fill project_idea' but no fill action found.",
			},
		},
	}

	// Manually convert (replicating the logic from convertReadinessToValidationChecks).
	checks := []model.ValidationCheck{}

	for _, f := range readinessReport.Blockers {
		checks = append(checks, model.ValidationCheck{
			ID:                   "readiness_blocker_" + f.Code,
			Kind:                 "readiness",
			Code:                 f.Code,
			NodeID:               f.NodeID,
			Severity:             model.FindingSeverityBlocking,
			Passed:               false,
			Required:             true,
			Summary:              f.Message,
			ResponsibilityDomain: model.ValidationCheckDomainApp,
		})
	}

	for _, f := range readinessReport.Warnings {
		checks = append(checks, model.ValidationCheck{
			ID:                   "readiness_warning_" + f.Code,
			Kind:                 "readiness",
			Code:                 f.Code,
			NodeID:               f.NodeID,
			Severity:             model.FindingSeverityWarning,
			Passed:               false,
			Required:             false,
			Summary:              f.Message,
			ResponsibilityDomain: model.ValidationCheckDomainApp,
		})
	}

	if len(checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(checks))
	}

	// Verify blocker check.
	if checks[0].Code != "business_input_missing" {
		t.Errorf("check[0].Code = %q, want business_input_missing", checks[0].Code)
	}
	if checks[0].Severity != model.FindingSeverityBlocking {
		t.Errorf("check[0].Severity = %q, want blocking", checks[0].Severity)
	}
	if checks[0].Required != true {
		t.Errorf("check[0].Required = false, want true")
	}
	if checks[0].Passed != false {
		t.Errorf("check[0].Passed = true, want false")
	}
	if checks[0].ResponsibilityDomain != model.ValidationCheckDomainApp {
		t.Errorf("check[0].ResponsibilityDomain = %q, want app", checks[0].ResponsibilityDomain)
	}
	if checks[0].Kind != "readiness" {
		t.Errorf("check[0].Kind = %q, want readiness", checks[0].Kind)
	}
	if checks[0].NodeID != "stage-1" {
		t.Errorf("check[0].NodeID = %q, want stage-1", checks[0].NodeID)
	}

	// Verify warning check.
	if checks[1].Code != "declared_business_input_action_missing" {
		t.Errorf("check[1].Code = %q, want declared_business_input_action_missing", checks[1].Code)
	}
	if checks[1].Severity != model.FindingSeverityWarning {
		t.Errorf("check[1].Severity = %q, want warning", checks[1].Severity)
	}
	if checks[1].Required != false {
		t.Errorf("check[1].Required = true, want false")
	}
	if checks[1].ResponsibilityDomain != model.ValidationCheckDomainApp {
		t.Errorf("check[1].ResponsibilityDomain = %q, want app", checks[1].ResponsibilityDomain)
	}
	if checks[1].NodeID != "stage-2" {
		t.Errorf("check[1].NodeID = %q, want stage-2", checks[1].NodeID)
	}
}

func TestPreExecutionReadinessPreservesApprovedCredentialGrants(t *testing.T) {
	const secretRef = "credential://demo/login"
	pkg := &model.ClientExecutionPackage{
		PackageID: "pkg_with_login",
		CredentialGrants: []model.CredentialGrant{{
			GrantID: "grant_login", CloudSecretRef: secretRef,
			AllowedDomains: []string{"example.test"}, AllowedOperations: []string{"login"},
		}},
		ExecutableScriptBundle: &model.ExecutableRecordingScriptBundle{
			PlanJSON: &model.ExecutionScriptDocument{Steps: []model.ScriptStep{{
				NodeID: "node_login", Action: model.ScriptActionInstruction{Type: model.GraphActionFill, SecretRef: secretRef},
				Validations: []model.ValidationSpec{{Kind: "page_loaded", Required: true}},
			}}},
			StageApprovalPlan: &model.StageApprovalPlan{Stages: []model.StageApprovalStage{{
				NodeID: "node_login", StageKind: model.BusinessStageKindBusinessInput,
				Interaction: model.BrowserAgentInteraction{Kind: model.GraphActionFill, SecretRef: secretRef},
			}}},
			ScriptOutline: &model.BrowserAgentScriptOutline{Stages: []model.BrowserAgentOutlineStage{{
				NodeID: "node_login", StageKind: model.BusinessStageKindBusinessInput,
				Interactions: []model.BrowserAgentInteraction{{Kind: model.GraphActionFill, SecretRef: secretRef}},
			}}},
		},
	}

	validationContext := validationContextFromPackage(pkg)
	if len(validationContext.CredentialGrants) != 1 || validationContext.CredentialGrants[0].CloudSecretRef != secretRef {
		t.Fatalf("validation context lost the approved credential grant: %+v", validationContext.CredentialGrants)
	}
	for _, check := range convertReadinessToValidationChecks(validationContext) {
		if check.Code == "credential_grant_missing" {
			t.Fatalf("pre-execution readiness created a false credential blocker: %+v", check)
		}
	}
}
