package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cascade-demoops/backend/internal/driver"
	"cascade-demoops/backend/internal/model"
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

func TestControlledOutlineScenarioPackagesAreStructurallyValid(t *testing.T) {
	service := newTestDevHTTPServer(t).service
	service.runtime.DevRepoRoot = filepath.Join("..", "..", "..")
	fixturePath := service.browserAgentAcceptanceFixturePath()

	cases := []struct {
		name    string
		handler http.HandlerFunc
		build   func(string, string) (model.ClientExecutionPackage, error)
		stages  int
	}{
		{"success", controlledOutlineSuccessHandler, controlledOutlineSuccessPackage, 2},
		{"locator_missing", controlledOutlineLocatorMissingHandler, controlledOutlineLocatorMissingPackage, 2},
		{"required_validation_failure", controlledOutlineRequiredValidationFailureHandler, controlledOutlineRequiredValidationFailurePackage, 2},
		{"wait_timeout", controlledOutlineWaitTimeoutHandler, controlledOutlineWaitTimeoutPackage, 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(tc.handler))
			defer server.Close()
			pkg, err := tc.build(fixturePath, server.URL)
			if err != nil {
				t.Fatalf("%s package must be structurally valid: %v", tc.name, err)
			}
			if pkg.ExecutableScriptBundle == nil {
				t.Fatalf("%s package is missing its executable script bundle", tc.name)
			}
			bundle := pkg.ExecutableScriptBundle
			if len(bundle.PlanJSON.Steps) != tc.stages || len(bundle.StageApprovalPlan.Stages) != tc.stages || len(bundle.ScriptOutline.Stages) != tc.stages {
				t.Fatalf("%s package stage counts diverge: plan=%d stages=%d outline=%d want=%d", tc.name, len(bundle.PlanJSON.Steps), len(bundle.StageApprovalPlan.Stages), len(bundle.ScriptOutline.Stages), tc.stages)
			}
			if pkg.Metadata["runtime"] != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 {
				t.Fatalf("%s package is not a browser-agent outline runtime: %v", tc.name, pkg.Metadata["runtime"])
			}
		})
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
	if path := os.Getenv("CASCADE_FFMPEG_PATH"); path != "" {
		service.runtime.FFmpegPath = path
	}
	if path := os.Getenv("CASCADE_FFPROBE_PATH"); path != "" {
		service.runtime.FFprobePath = path
	}
	if !commandReady(service.runtime.FFmpegPath) || checkCommandReady(firstNonEmptyString(service.runtime.FFprobePath, "ffprobe")) != nil {
		t.Skip("complete BrowserAgent delivery acceptance requires FFmpeg and ffprobe; missing runtimes are covered by the final MP4 rejection tests")
	}
	service.editorWorker = driver.NewLocalDriver(service.nodeBinaryForExecution(), service.localVideoWorkerPath(), service.videoWorkerEnvironment())
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

func TestProtocolBrowserAgentAcceptanceBaseSuccessScenarioCompletes(t *testing.T) {
	service := newTestDevHTTPServer(t).service
	service.runtime.DevRepoRoot = filepath.Join("..", "..", "..")
	if path := os.Getenv("CASCADE_FFMPEG_PATH"); path != "" {
		service.runtime.FFmpegPath = path
	}
	if path := os.Getenv("CASCADE_FFPROBE_PATH"); path != "" {
		service.runtime.FFprobePath = path
	}
	if !commandReady(service.runtime.FFmpegPath) || checkCommandReady(firstNonEmptyString(service.runtime.FFprobePath, "ffprobe")) != nil {
		t.Skip("protocol acceptance diagnostics require FFmpeg and ffprobe")
	}
	service.editorWorker = driver.NewLocalDriver(service.nodeBinaryForExecution(), service.localVideoWorkerPath(), service.videoWorkerEnvironment())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Acceptance Dashboard</title></head><body><main aria-label="Dashboard"><h1>Dashboard</h1><button type="button" data-testid="invite-member">Invite teammate</button><p id="status">Waiting</p></main><script>document.querySelector('[data-testid="invite-member"]').addEventListener('click',()=>{document.querySelector('#status').textContent='Invite flow starts';const dialog=document.createElement('section');dialog.dataset.testid='invite-dialog';dialog.setAttribute('role','dialog');dialog.textContent='Invite teammate';document.body.appendChild(dialog)})</script></body></html>`))
	}))
	defer server.Close()
	pkg, err := protocolAcceptancePackage(service.browserAgentAcceptanceFixturePath(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.runProtocolAcceptanceScenario(t.Context(), pkg)
	if err != nil {
		t.Fatal(err)
	}
	scenario := protocolAcceptanceSuccessScenario(run)
	if scenario.Verdict != "passed" {
		t.Fatalf("base protocol success scenario failed: %s assertions=%+v failure=%+v artifacts=%+v", scenario.Actual, scenario.Assertions, run.Result.FailureDiagnostic, run.Result.GeneratedAssets)
	}
}
