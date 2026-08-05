package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/driver"
	"cascade-demoops/backend/internal/model"
)

// TestControlledOutlineScenarioPackagesRunCompleteServerPath drives the four
// Server-owned controlled outline packages through the real Browser Agent
// runtime (Exchange intake, outline runner, result packaging) and asserts the
// P2 acceptance contract from
// docs/handoff/validation-agent-server-handoff-2026-07-29.md:
//   - the success package completes with strict ValidationReports, a final MP4,
//     recording, trace and a delivery ack;
//   - each failure package STOPS with a failed exchange status, a redacted
//     failure diagnostic (screenshot + trace) and an approval-required repair
//     request, rather than silently continuing.
//
// It reuses runProtocolAcceptanceScenario and protocolAcceptanceFailureScenario
// so the protocol boundary is identical to the fixed acceptance gate.
func TestControlledOutlineScenarioPackagesRunCompleteServerPath(t *testing.T) {
	service := newTestDevHTTPServer(t).service
	service.runtime.DevRepoRoot = filepath.Join("..", "..", "..")
	if path := os.Getenv("CASCADE_FFMPEG_PATH"); path != "" {
		service.runtime.FFmpegPath = path
	}
	if path := os.Getenv("CASCADE_FFPROBE_PATH"); path != "" {
		service.runtime.FFprobePath = path
	}
	if !commandReady(service.runtime.FFmpegPath) || checkCommandReady(firstNonEmptyString(service.runtime.FFprobePath, "ffprobe")) != nil {
		t.Skip("controlled outline e2e requires FFmpeg and ffprobe")
	}
	if !fileExists(service.localVideoWorkerPath()) {
		t.Skip("controlled outline e2e requires the video-worker build artifact")
	}
	if err := checkCommandReady(service.nodeBinaryForExecution()); err != nil {
		t.Skipf("controlled outline e2e requires node: %v", err)
	}
	service.editorWorker = driver.NewLocalDriver(service.nodeBinaryForExecution(), service.localVideoWorkerPath(), service.videoWorkerEnvironment())
	fixturePath := service.browserAgentAcceptanceFixturePath()

	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(controlledOutlineSuccessHandler))
		defer server.Close()
		pkg, err := controlledOutlineSuccessPackage(fixturePath, server.URL)
		if err != nil {
			t.Fatalf("build success package: %v", err)
		}
		run, err := service.runProtocolAcceptanceScenario(t.Context(), pkg)
		if err != nil {
			t.Fatalf("run success package: %v", err)
		}
		if run.Status.Status != model.ExchangePackageStatusCompleted || run.Result.Status != model.RecordingResultStatusGenerated {
			if run.Result.FailureDiagnostic != nil {
				t.Logf("success failure code=%q msg=%q", run.Result.FailureDiagnostic.Error.Code, run.Result.FailureDiagnostic.Error.Message)
			}
			for _, rep := range run.Result.ValidationReports {
				t.Logf("report phase=%s decision=%s checks=%d nodeid=%q stageid=%q evidence_quality=%q evidence_refs=%d", rep.Phase, rep.Decision, len(rep.Checks), rep.NodeID, rep.StageID, rep.EvidenceQuality, len(rep.EvidenceRefs))
				for _, chk := range rep.Checks {
					t.Logf("  check id=%s kind=%s code=%s severity=%s passed=%v required=%v summary=%q", chk.ID, chk.Kind, chk.Code, chk.Severity, chk.Passed, chk.Required, chk.Summary)
				}
			}
			t.Fatalf("success package did not complete: status=%s result=%s", run.Status.Status, run.Result.Status)
		}
		if !hasStrictBrowserAgentValidationReports(run.Result, 2) {
			t.Fatalf("success package missing strict validation reports: got %d", len(run.Result.ValidationReports))
		}
		if !protocolAcceptanceHasArtifact(run.Result, "demo_video") || !protocolAcceptanceHasArtifact(run.Result, "raw_recording") || !protocolAcceptanceHasArtifact(run.Result, "browser_trace") {
			t.Fatalf("success package missing required artifacts: %+v", run.Result.GeneratedAssets)
		}
		if run.Result.StageEventLogRef == nil {
			t.Fatal("success package missing stage event log")
		}
		if !run.Acknowledged || !run.DeliveryAcknowledged {
			t.Fatal("success package delivery was not acknowledged")
		}
	})

	failureCases := []struct {
		name         string
		handler      http.HandlerFunc
		build        func(string, string) (model.ClientExecutionPackage, error)
		expectedCode string
	}{
		{"locator_missing", controlledOutlineLocatorMissingHandler, controlledOutlineLocatorMissingPackage, "browser_agent_target_not_resolved"},
		{"required_validation_failure", controlledOutlineRequiredValidationFailureHandler, controlledOutlineRequiredValidationFailurePackage, "outcome_verification_failed"},
		{"wait_timeout", controlledOutlineWaitTimeoutHandler, controlledOutlineWaitTimeoutPackage, "outcome_verification_failed"},
	}
	for _, tc := range failureCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(tc.handler))
			defer server.Close()
			pkg, err := tc.build(fixturePath, server.URL)
			if err != nil {
				t.Fatalf("build %s package: %v", tc.name, err)
			}
			run, err := service.runProtocolAcceptanceScenario(t.Context(), pkg)
			if err != nil {
				t.Fatalf("run %s package: %v", tc.name, err)
			}
			if run.Status.Status != model.ExchangePackageStatusFailed || run.Result.Status != model.RecordingResultStatusFailed {
				t.Fatalf("%s package did not stop: status=%s result=%s", tc.name, run.Status.Status, run.Result.Status)
			}
			if run.Result.FailureDiagnostic == nil {
				t.Fatalf("%s package missing failure diagnostic", tc.name)
			}
			diag := run.Result.FailureDiagnostic
			t.Logf("%s failure code=%q screenshots=%d traces=%d", tc.name, diag.Error.Code, len(diag.ScreenshotRefs), len(diag.TraceRefs))
			if diag.Error.Code != tc.expectedCode {
				t.Fatalf("%s package failure code = %q, want %q", tc.name, diag.Error.Code, tc.expectedCode)
			}
			if !diag.RedactionReport.Applied || diag.RedactionReport.FullHTMLIncluded {
				t.Fatalf("%s package diagnostic was not redacted", tc.name)
			}
			if len(diag.ScreenshotRefs) == 0 || len(diag.TraceRefs) == 0 {
				t.Fatalf("%s package diagnostic missing screenshot/trace evidence", tc.name)
			}
			if run.Result.RepairRequest == nil || !run.Result.RepairRequest.ApprovalRequired {
				t.Fatalf("%s package missing approval-required repair request", tc.name)
			}
		})
	}

	repairCases := []struct {
		name           string
		handler        http.HandlerFunc
		build          func(string, string) (model.ClientExecutionPackage, error)
		expectedField  string
		expectedBefore string
		expectedAfter  string
		stageCount     int
	}{
		{"selector_alternative_repair", controlledOutlineSelectorRepairHandler, controlledOutlineSelectorRepairPackage, "script_outline.stages[].components[].selector", "", "testid:current-confirm-action", 2},
		{"busy_page_wait_repair", controlledOutlineBusyWaitRepairHandler, controlledOutlineBusyWaitRepairPackage, "script_outline.stages[].wait_conditions", "wait_after_entry_at_least_250ms", "wait_after_entry_at_least_1250ms", 2},
	}
	for _, tc := range repairCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(tc.handler))
			defer server.Close()
			pkg, err := tc.build(fixturePath, server.URL)
			if err != nil {
				t.Fatalf("build %s package: %v", tc.name, err)
			}
			sourceHash := pkg.ExecutableScriptBundle.Reproducibility.BundleHashSHA256
			run, err := service.runProtocolAcceptanceScenario(t.Context(), pkg)
			if err != nil {
				t.Fatalf("run %s package: %v", tc.name, err)
			}
			if run.Status.Status != model.ExchangePackageStatusCompleted || run.Result.Status != model.RecordingResultStatusGenerated {
				t.Fatalf("%s did not recover and complete: status=%s result=%s diagnostic=%+v", tc.name, run.Status.Status, run.Result.Status, run.Result.FailureDiagnostic)
			}
			if len(run.Result.PatchLedger) != 1 {
				t.Fatalf("%s must record exactly one bounded patch: %+v", tc.name, run.Result.PatchLedger)
			}
			entry := run.Result.PatchLedger[0]
			if !entry.Applied || entry.PolicyDecision != model.ValidationDecisionRepairAllowed || entry.Field != tc.expectedField || entry.Before != tc.expectedBefore || entry.After != tc.expectedAfter || entry.SourceBundleHashSHA256 != sourceHash {
				t.Fatalf("%s patch ledger violates the approved contract: %+v", tc.name, entry)
			}
			if !hasRepairAuditEvents(run.Result, entry.StageID) {
				t.Fatalf("%s result is missing proposed/applied/re-observed audit events", tc.name)
			}
			if !hasStrictBrowserAgentValidationReports(run.Result, tc.stageCount) || !run.Acknowledged || !run.DeliveryAcknowledged {
				t.Fatalf("%s did not preserve strict validation and delivery acknowledgement", tc.name)
			}
		})
	}
}

func hasRepairAuditEvents(result model.RecordingResultPackage, stageID string) bool {
	if result.StageEventLogRef == nil {
		return false
	}
	path := filepath.FromSlash(strings.TrimPrefix(result.StageEventLogRef.URI, "file://"))
	if len(path) > 2 && (path[0] == '/' || path[0] == '\\') && path[2] == ':' {
		path = path[1:]
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	proposed, applied, resolvedAgain := false, false, false
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var event model.StageExecutionEvent
		if json.Unmarshal([]byte(line), &event) != nil || event.StageID != stageID {
			continue
		}
		proposed = proposed || event.EventType == model.StageExecutionEventRepairProposed
		applied = applied || event.EventType == model.StageExecutionEventRepairApplied
		resolvedAgain = resolvedAgain || (event.EventType == model.StageExecutionEventTargetResolved && event.Attempt >= 2)
	}
	return proposed && applied && resolvedAgain
}
