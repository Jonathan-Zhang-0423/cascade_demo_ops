package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadBrowserAgentAcceptanceReportRequiresUsableScenarios(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acceptance-report.json")
	content := `{
  "schema_version":"cascade.browser_agent_acceptance.v1",
  "generated_at":"2026-07-23T09:00:00Z",
  "runtime":"browser-agent-outline-v1",
  "strict_gate":"passed",
  "scenarios":[
    {"id":"success_navigation_click","description":"controlled click","expected":"pass","actual":"pass","verdict":"passed","action_executed":true,"evidence":[],"assertions":[]},
    {"id":"semantic_target_contract_conflict","description":"contract block","expected":"pass","actual":"pass","verdict":"passed","action_executed":false,"evidence":[],"assertions":[]},
    {"id":"locator_missing","description":"locator block","expected":"pass","actual":"pass","verdict":"passed","action_executed":false,"evidence":[],"assertions":[]},
    {"id":"approved_selector_alternative_repair","description":"approved selector repair","expected":"pass","actual":"pass","verdict":"passed","action_executed":true,"evidence":[],"assertions":[]},
    {"id":"busy_page_wait_repair","description":"busy wait repair","expected":"pass","actual":"pass","verdict":"passed","action_executed":true,"evidence":[],"assertions":[]},
    {"id":"required_validation_failure","description":"validation stop","expected":"pass","actual":"pass","verdict":"passed","action_executed":true,"evidence":[],"assertions":[]},
    {"id":"recording_and_trace_delivery","description":"evidence retained","expected":"pass","actual":"pass","verdict":"passed","action_executed":false,"evidence":[],"assertions":[]}
  ]
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := readBrowserAgentAcceptanceReport(path)
	if err != nil {
		t.Fatal(err)
	}
	if report.StrictGate != "passed" || report.Runtime != "browser-agent-outline-v1" || len(report.Scenarios) != 7 {
		t.Fatalf("unexpected acceptance report: %+v", report)
	}
}

func TestControlledBusinessAcceptancePackageAlignsWithOutlineProtocol(t *testing.T) {
	service := newTestDevHTTPServer(t).service
	service.runtime.DevRepoRoot = filepath.Join("..", "..", "..")
	server := httptest.NewServer(http.HandlerFunc(controlledBusinessFixtureHandler))
	defer server.Close()
	pkg, err := controlledBusinessAcceptancePackage(service.browserAgentAcceptanceFixturePath(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.ExecutableScriptBundle == nil || len(pkg.ExecutableScriptBundle.PlanJSON.Steps) != 5 || len(pkg.ExecutableScriptBundle.StageApprovalPlan.Stages) != 5 || len(pkg.ExecutableScriptBundle.ScriptOutline.Stages) != 5 {
		t.Fatalf("controlled business package is not a five-stage outline: %+v", pkg.ExecutableScriptBundle)
	}
}

func TestBrowserAgentAcceptanceReportRejectsIncompleteData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acceptance-report.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":"wrong","generated_at":"`+time.Now().UTC().Format(time.RFC3339)+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readBrowserAgentAcceptanceReport(path); err == nil {
		t.Fatal("incomplete acceptance report must be rejected")
	}
}

func TestReadBrowserAgentAcceptanceReportAcceptsBaseProtocolGate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acceptance-report.json")
	content := `{"schema_version":"cascade.browser_agent_acceptance.v1","generated_at":"2026-07-24T09:00:00Z","runtime":"browser-agent-outline-v1","strict_gate":"passed","scenarios":[{"id":"success_navigation_click","verdict":"passed"},{"id":"semantic_target_contract_conflict","verdict":"passed"},{"id":"locator_missing","verdict":"passed"},{"id":"required_validation_failure","verdict":"passed"},{"id":"recording_and_trace_delivery","verdict":"passed"}]}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readBrowserAgentAcceptanceReport(path); err != nil {
		t.Fatalf("five-scenario Server protocol gate must remain valid: %v", err)
	}
}

func TestProtocolBrowserAgentAcceptanceRunsCompleteServerPath(t *testing.T) {
	service := newTestDevHTTPServer(t).service
	service.runtime.DevRepoRoot = filepath.Join("..", "..", "..")
	if _, err := service.RunBrowserAgentAcceptance(t.Context()); err != nil {
		t.Fatal(err)
	}
	view, err := service.GetBrowserAgentAcceptance(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !view.Ready || view.Report == nil || view.Report.StrictGate != "passed" {
		t.Fatalf("protocol acceptance gate did not pass: %+v", view)
	}
}
