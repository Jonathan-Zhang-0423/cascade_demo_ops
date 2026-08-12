package app

import (
	"strings"
	"testing"

	"cascade-demoops/backend/internal/driver"
	"cascade-demoops/backend/internal/model"
)

func TestOneTimeBrowserAgentCredentialBrokerResolvesOnlyApprovedRefAndDestroys(t *testing.T) {
	broker, err := newOneTimeBrowserAgentCredentialBroker("vault://approved/login", "sensitive-test-value")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Resolve("vault://other"); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("expected ref scope rejection, got %v", err)
	}
	value, err := broker.Resolve("vault://approved/login")
	if err != nil || value != "sensitive-test-value" {
		t.Fatalf("approved credential resolution failed: value=%q err=%v", value, err)
	}
	broker.Destroy()
	if _, err := broker.Resolve("vault://approved/login"); err == nil || !strings.Contains(err.Error(), "destroyed") {
		t.Fatalf("destroyed broker remained readable: %v", err)
	}
}

func TestDirectPackageCredentialRefsMustMatchApprovedGrant(t *testing.T) {
	pkg := &model.ClientExecutionPackage{
		CredentialGrants: []model.CredentialGrant{{GrantID: "grant-login", CloudSecretRef: "vault://approved/login"}},
		ExecutableScriptBundle: &model.ExecutableRecordingScriptBundle{
			PlanJSON: &model.ExecutionScriptDocument{Steps: []model.ScriptStep{{
				NodeID: "node-login", Action: model.ScriptActionInstruction{SecretRef: "vault://approved/login"},
			}}},
		},
	}
	if err := validateDirectPackageCredentialRefs(pkg); err != nil {
		t.Fatalf("approved secret_ref rejected: %v", err)
	}
	pkg.CredentialGrants = nil
	if err := validateDirectPackageCredentialRefs(pkg); err == nil || !strings.Contains(err.Error(), "credential grant") {
		t.Fatalf("unapproved secret_ref was accepted: %v", err)
	}
	pkg.CredentialGrants = []model.CredentialGrant{
		{GrantID: "grant-login", CloudSecretRef: "vault://approved/login"},
		{GrantID: "grant-other", CloudSecretRef: "vault://approved/other"},
	}
	pkg.ExecutableScriptBundle.PlanJSON.Steps = append(pkg.ExecutableScriptBundle.PlanJSON.Steps, model.ScriptStep{
		NodeID: "node-other", Action: model.ScriptActionInstruction{SecretRef: "vault://approved/other"},
	})
	if err := validateDirectPackageCredentialRefs(pkg); err == nil || !strings.Contains(err.Error(), "one distinct") {
		t.Fatalf("multiple credential refs must fail closed until protocol support exists: %v", err)
	}
}

func TestBrowserAgentStageSecretValuesAreEphemeral(t *testing.T) {
	broker, err := newOneTimeBrowserAgentCredentialBroker("vault://approved/login", "sensitive-test-value")
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Destroy()
	stage := driver.BrowserAgentWorkerStage{Interactions: []model.BrowserAgentInteraction{{SecretRef: "vault://approved/login"}}}
	values, err := browserAgentStageSecretValues(stage, broker)
	if err != nil || values["vault://approved/login"] != "sensitive-test-value" {
		t.Fatalf("stage secret resolution failed: values=%v err=%v", len(values), err)
	}
	clearBrowserAgentStageSecrets(values)
	if len(values) != 0 {
		t.Fatalf("ephemeral stage secret map was not cleared: %v", values)
	}
}
