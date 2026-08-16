package app

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"cascade-demoops/backend/internal/model"
)

// This file defines Server-owned, single-purpose controlled outline packages
// used to exercise individual runtime outcomes end-to-end:
//   - success: navigate + inspect both resolve and verify;
//   - locator missing: an approved target that is absent from the page, so the
//     runtime stops at target resolution (browser_agent_target_not_resolved);
//   - required validation failure: the target resolves and the action runs, but
//     a required text_contains assertion is false (outcome_verification_failed);
//   - build-not-completed (wait): the target resolves, but the required
//     "completed" element never becomes visible (outcome_verification_failed).
//
// Every package is a structurally valid `browser-agent-outline-v1`
// ClientExecutionPackage — the intended failure is a runtime outcome, never an
// upload rejection. Each failure package begins with a real navigate stage so
// the page is loaded and the failure is attributable to its specific stage,
// not to a page that was never reached. They reuse the same builder primitives
// as controlledBusinessAcceptancePackage so the protocol boundary is identical
// to the full business flow.

// controlledOutlineSuccessHandler serves a page whose approved targets all
// resolve: a workspace main region and a "Ready" status heading.
func controlledOutlineSuccessHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>Outline Success</title></head><body><main aria-label="Outline workspace"><h1>Workspace</h1><section aria-label="Status panel"><p>Controlled outline success fixture</p><h2 data-testid="status-badge">Ready</h2></section></main></body></html>`))
}

// controlledOutlineLocatorMissingHandler serves a workspace page that loads
// cleanly but does NOT contain the approved action control, so the second stage
// must stop at target resolution.
func controlledOutlineLocatorMissingHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>Outline Locator Missing</title></head><body><main aria-label="Outline workspace"><h1>Workspace</h1><p>The approved action control is intentionally absent from this fixture.</p></main></body></html>`))
}

// controlledOutlineRequiredValidationFailureHandler serves a workspace page
// with a real Submit button that resolves and clicks, but the result copy never
// shows the required confirmation phrase, so the required validation fails.
func controlledOutlineRequiredValidationFailureHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>Outline Validation Failure</title></head><body><main aria-label="Outline workspace"><h1>Checkout</h1><button data-testid="submit-action">Submit</button><p data-testid="submit-status">Processing...</p></main></body></html>`))
}

// controlledOutlineWaitTimeoutHandler serves a workspace page whose build is
// still running: the observe target resolves, but the required "build complete"
// element never appears, so the required completion validation cannot pass.
func controlledOutlineWaitTimeoutHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>Outline Build Running</title></head><body><main aria-label="Outline workspace"><h1>Build running</h1><p data-testid="build-status">Build in progress...</p></main></body></html>`))
}

// controlledOutlineBase loads the protocol fixture and rewrites the Server-owned
// identity fields shared by every single-purpose outline scenario. The caller
// supplies scenario-specific stage specs and identifiers.
func controlledOutlineBase(fixturePath, baseURL, packageID, projectID, contextID, runID, name string) (model.ClientExecutionPackage, string, error) {
	base, err := protocolAcceptancePackage(fixturePath, baseURL)
	if err != nil {
		return model.ClientExecutionPackage{}, "", err
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return model.ClientExecutionPackage{}, "", err
	}
	domain := parsed.Hostname()
	if domain == "" {
		return model.ClientExecutionPackage{}, "", errors.New("controlled outline fixture host is missing")
	}
	base.PackageID, base.ProjectID = packageID, projectID
	base.ProjectContextSummary.ContextID = contextID
	base.ProjectContextSummary.Name = name
	base.ProjectContextSummary.ProductURL = baseURL
	base.RecordingRunSpec.RunID, base.RecordingRunSpec.BaseURL, base.RecordingRunSpec.AllowedDomains = runID, baseURL, []string{domain}
	base.RecordingRunSpec.Timeline.TargetDurationSec = 8
	base.RecordingRunSpec.Outputs.OutputFormats = []string{"mp4"}
	base.CredentialGrants = nil
	base.Metadata = map[string]any{"dev_plaintext_upload_mode": true, "producer": "server_controlled_outline_scenario", "runtime": model.ExecutableScriptRuntimeBrowserAgentOutlineV1}
	return base, domain, nil
}

// outlineNavigateStage returns the shared, always-resolving entry stage: it
// navigates to /app and verifies the workspace main region loaded. Every
// scenario begins here so downstream failures are attributable to their own
// stage rather than to an unreached page.
func outlineNavigateStage(baseURL string, evidence model.EvidenceRef) controlledBusinessStageSpec {
	return controlledBusinessStageSpec{NodeID: "node_open_workspace", StageID: "stage_open_workspace", Title: "Open outline workspace", Objective: "Workspace is visible", Intent: "Enter the approved outline workspace.", Route: "/app", Success: "Workspace main region is visible", Kind: model.BusinessStageKindSessionSetup, RouteState: model.BusinessRouteStateWorkspace, Action: model.BrowserAgentInteraction{Kind: model.GraphActionNavigate, Target: model.ActionTarget{URL: baseURL + "/app"}, NonDestructive: true, WaitConditions: []string{"wait_after_entry_at_least_250ms"}, SelectorPolicy: "approved_route_only"}, Target: model.BrowserAgentTargetContract{SemanticID: "target_workspace", Purpose: "Outline workspace", AllowedRoles: []string{"main"}, AllowedNames: []string{"Outline workspace"}, ComponentRef: "component:workspace", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1}, Component: model.BrowserAgentComponentTarget{ComponentRef: "component:workspace", Role: "main", Name: "Outline workspace", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1}, Validation: model.ValidationSpec{ID: "validation_workspace_route", Kind: "url_matches", Target: model.ActionTarget{URL: baseURL + "/app"}, Required: true, EvidenceRefs: []model.EvidenceRef{evidence}}}
}

// finalizeControlledOutline applies the shared stage machinery and enforces the
// full upload/cloud validation contract so any failure is a runtime outcome,
// not a malformed package.
func finalizeControlledOutline(base *model.ClientExecutionPackage, specs []controlledBusinessStageSpec, baseURL, domain string, evidence model.EvidenceRef) (model.ClientExecutionPackage, error) {
	applyControlledBusinessStages(base, specs, baseURL, domain, evidence)
	if err := normalizeClientExecutionPackageForUpload(base); err != nil {
		return model.ClientExecutionPackage{}, err
	}
	if err := model.ValidateClientExecutionPackageForCloudExecution(base); err != nil {
		return model.ClientExecutionPackage{}, fmt.Errorf("controlled outline scenario package invalid: %w", err)
	}
	return *base, nil
}

// controlledOutlineSuccessPackage builds a two-stage outline whose navigation
// and status inspection both resolve and verify against
// controlledOutlineSuccessHandler.
func controlledOutlineSuccessPackage(fixturePath, baseURL string) (model.ClientExecutionPackage, error) {
	evidence := model.EvidenceRef{ID: "ev_outline_success_fixture", Kind: "fixture_source", Summary: "Server-owned controlled outline success page", Confidence: 1}
	base, domain, err := controlledOutlineBase(fixturePath, baseURL, "pkg_controlled_outline_success", "project_controlled_outline_success", "ctx_controlled_outline_success", "run_controlled_outline_success", "Controlled outline success flow")
	if err != nil {
		return model.ClientExecutionPackage{}, err
	}
	specs := []controlledBusinessStageSpec{
		outlineNavigateStage(baseURL, evidence),
		{NodeID: "node_inspect_status", StageID: "stage_inspect_status", Title: "Inspect status badge", Objective: "Status badge shows Ready", Intent: "Verify the approved status heading state from live evidence.", Route: "/app", Success: "Status heading shows Ready", Kind: model.BusinessStageKindFinalObserve, RouteState: model.BusinessRouteStateWorkspace, Action: model.BrowserAgentInteraction{Kind: model.GraphActionInspect, Target: model.ActionTarget{Role: "heading", Text: "Ready"}, NonDestructive: true, WaitConditions: []string{"wait_after_entry_at_least_250ms"}, SelectorPolicy: "prefer_role_name"}, Target: model.BrowserAgentTargetContract{SemanticID: "target_status_badge", Purpose: "Status heading", AllowedRoles: []string{"heading"}, AllowedNames: []string{"Ready"}, ComponentRef: "component:status-badge", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1}, Component: model.BrowserAgentComponentTarget{ComponentRef: "component:status-badge", Role: "heading", Name: "Ready", TestID: "status-badge", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1}, Validation: model.ValidationSpec{ID: "validation_status_ready", Kind: "text_contains", Target: model.ActionTarget{TestID: "status-badge"}, Assertion: "Ready", Expected: "Ready", Required: true, EvidenceRefs: []model.EvidenceRef{evidence}}},
	}
	return finalizeControlledOutline(&base, specs, baseURL, domain, evidence)
}

// controlledOutlineLocatorMissingPackage builds a navigate-then-click outline
// whose second stage targets an approved control absent from
// controlledOutlineLocatorMissingHandler, so the runtime must stop at target
// resolution (browser_agent_target_not_resolved).
func controlledOutlineLocatorMissingPackage(fixturePath, baseURL string) (model.ClientExecutionPackage, error) {
	evidence := model.EvidenceRef{ID: "ev_outline_locator_missing_fixture", Kind: "fixture_source", Summary: "Server-owned controlled outline locator-missing page", Confidence: 1}
	base, domain, err := controlledOutlineBase(fixturePath, baseURL, "pkg_controlled_outline_locator_missing", "project_controlled_outline_locator_missing", "ctx_controlled_outline_locator_missing", "run_controlled_outline_locator_missing", "Controlled outline locator-missing flow")
	if err != nil {
		return model.ClientExecutionPackage{}, err
	}
	specs := []controlledBusinessStageSpec{
		outlineNavigateStage(baseURL, evidence),
		{NodeID: "node_click_missing_action", StageID: "stage_click_missing_action", Title: "Click approved action", Objective: "Approved action control is used", Intent: "Click the approved action control in the controlled fixture.", Route: "/app", Success: "Action control was activated", Kind: model.BusinessStageKindBusinessSubmit, RouteState: model.BusinessRouteStateWorkspace, Action: model.BrowserAgentInteraction{Kind: model.GraphActionClick, Target: model.ActionTarget{TestID: "nonexistent-action-btn"}, NonDestructive: true, WaitConditions: []string{"wait_after_entry_at_least_250ms"}, SelectorPolicy: "prefer_testid_then_role_name"}, Target: model.BrowserAgentTargetContract{SemanticID: "target_missing_action", Purpose: "Approved action control", AllowedRoles: []string{"button"}, AllowedNames: []string{"Confirm action"}, ComponentRef: "component:missing-action", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1}, Component: model.BrowserAgentComponentTarget{ComponentRef: "component:missing-action", Role: "button", Name: "Confirm action", TestID: "nonexistent-action-btn", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1}, Validation: model.ValidationSpec{ID: "validation_missing_action", Kind: "element_visible", Target: model.ActionTarget{TestID: "nonexistent-action-btn"}, Required: true, EvidenceRefs: []model.EvidenceRef{evidence}}},
	}
	return finalizeControlledOutline(&base, specs, baseURL, domain, evidence)
}

// controlledOutlineRequiredValidationFailurePackage builds a navigate-then-click
// outline whose Submit action resolves and runs, but whose required
// text_contains assertion checks a phrase controlledOutlineRequiredValidation-
// FailureHandler never shows (outcome_verification_failed).
func controlledOutlineRequiredValidationFailurePackage(fixturePath, baseURL string) (model.ClientExecutionPackage, error) {
	evidence := model.EvidenceRef{ID: "ev_outline_validation_failure_fixture", Kind: "fixture_source", Summary: "Server-owned controlled outline validation-failure page", Confidence: 1}
	base, domain, err := controlledOutlineBase(fixturePath, baseURL, "pkg_controlled_outline_validation_failure", "project_controlled_outline_validation_failure", "ctx_controlled_outline_validation_failure", "run_controlled_outline_validation_failure", "Controlled outline validation-failure flow")
	if err != nil {
		return model.ClientExecutionPackage{}, err
	}
	specs := []controlledBusinessStageSpec{
		outlineNavigateStage(baseURL, evidence),
		{NodeID: "node_submit_action", StageID: "stage_submit_action", Title: "Submit and verify result", Objective: "Payment confirmation is observed", Intent: "Submit the approved action and verify the required business outcome.", Route: "/app", Success: "Payment confirmed is visible", Kind: model.BusinessStageKindBusinessSubmit, RouteState: model.BusinessRouteStateWorkspace, Action: model.BrowserAgentInteraction{Kind: model.GraphActionClick, Target: model.ActionTarget{TestID: "submit-action"}, NonDestructive: true, WaitConditions: []string{"wait_after_entry_at_least_250ms"}, SelectorPolicy: "prefer_testid_then_role_name"}, Target: model.BrowserAgentTargetContract{SemanticID: "target_submit_action", Purpose: "Submit action", AllowedRoles: []string{"button"}, AllowedNames: []string{"Submit"}, ComponentRef: "component:submit-action", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1}, Component: model.BrowserAgentComponentTarget{ComponentRef: "component:submit-action", Role: "button", Name: "Submit", TestID: "submit-action", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1}, Validation: model.ValidationSpec{ID: "validation_payment_confirmed", Kind: "text_contains", Target: model.ActionTarget{TestID: "submit-status"}, Assertion: "Payment confirmed", Expected: "Payment confirmed", Required: true, EvidenceRefs: []model.EvidenceRef{evidence}}},
	}
	return finalizeControlledOutline(&base, specs, baseURL, domain, evidence)
}

// controlledOutlineWaitTimeoutPackage builds a navigate-then-observe outline
// whose build-status target resolves, but whose required element_visible
// assertion waits for a "build complete" element controlledOutlineWaitTimeout-
// Handler never renders (outcome_verification_failed / build not completed).
func controlledOutlineWaitTimeoutPackage(fixturePath, baseURL string) (model.ClientExecutionPackage, error) {
	evidence := model.EvidenceRef{ID: "ev_outline_wait_timeout_fixture", Kind: "fixture_source", Summary: "Server-owned controlled outline build-not-completed page", Confidence: 1}
	base, domain, err := controlledOutlineBase(fixturePath, baseURL, "pkg_controlled_outline_wait_timeout", "project_controlled_outline_wait_timeout", "ctx_controlled_outline_wait_timeout", "run_controlled_outline_wait_timeout", "Controlled outline build-not-completed flow")
	if err != nil {
		return model.ClientExecutionPackage{}, err
	}
	specs := []controlledBusinessStageSpec{
		outlineNavigateStage(baseURL, evidence),
		{NodeID: "node_wait_build_complete", StageID: "stage_wait_build_complete", Title: "Verify build completion", Objective: "Build completion is observed", Intent: "Verify the approved build-complete element before finishing.", Route: "/app", Success: "Build complete element is visible", Kind: model.BusinessStageKindFinalObserve, RouteState: model.BusinessRouteStateBuildRunning, Action: model.BrowserAgentInteraction{Kind: model.GraphActionInspect, Target: model.ActionTarget{Role: "heading", Text: "Build running"}, NonDestructive: true, WaitConditions: []string{"wait_after_entry_at_least_250ms"}, SelectorPolicy: "prefer_role_name"}, Target: model.BrowserAgentTargetContract{SemanticID: "target_build_status", Purpose: "Build status heading", AllowedRoles: []string{"heading"}, AllowedNames: []string{"Build running"}, ComponentRef: "component:build-status", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1}, Component: model.BrowserAgentComponentTarget{ComponentRef: "component:build-status", Role: "heading", Name: "Build running", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1}, Validation: model.ValidationSpec{ID: "validation_build_complete", Kind: "element_visible", Target: model.ActionTarget{TestID: "build-complete"}, Required: true, EvidenceRefs: []model.EvidenceRef{evidence}}},
	}
	return finalizeControlledOutline(&base, specs, baseURL, domain, evidence)
}

// controlledOutlineSelectorRepairHandler simulates a harmless locator drift:
// the approved semantic target is unchanged, but only the App-approved
// selector alternative still resolves on the live page.
func controlledOutlineSelectorRepairHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>Outline Selector Repair</title></head><body><main aria-label="Outline workspace"><h1>Workspace</h1><button data-testid="current-confirm-action" onclick="document.querySelector('[data-testid=repair-status]').textContent='Confirmed'">Confirm action</button><p data-testid="repair-status">Waiting</p></main></body></html>`))
}

// controlledOutlineSelectorRepairPackage starts with a stale primary selector
// and declares the current selector as an App-approved alternative. Server may
// use that alternative for this run only; the signed source bundle is immutable.
func controlledOutlineSelectorRepairPackage(fixturePath, baseURL string) (model.ClientExecutionPackage, error) {
	evidence := model.EvidenceRef{ID: "ev_outline_selector_repair_fixture", Kind: "fixture_source", Summary: "Server-owned controlled selector-repair page", Confidence: 1}
	base, domain, err := controlledOutlineBase(fixturePath, baseURL, "pkg_controlled_outline_selector_repair", "project_controlled_outline_selector_repair", "ctx_controlled_outline_selector_repair", "run_controlled_outline_selector_repair", "Controlled outline selector-repair flow")
	if err != nil {
		return model.ClientExecutionPackage{}, err
	}
	// The approved alternative must carry formal App provenance: evidence ID,
	// source digest, observed role/name, and observation time, exactly like a
	// real App-exported candidate. The runtime may only repair within it.
	observedAt := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	component := model.BrowserAgentComponentTarget{
		ComponentRef: "component:confirm-action", TestID: "stale-confirm-action",
		SelectorAlternatives: []model.SelectorCandidate{{
			Kind: "testid", Value: "current-confirm-action", Confidence: 1, StabilityScore: 1, Source: "app_approved_fixture",
			EvidenceID: evidence.ID, SourceKind: "approved_manual_annotation", SourceDigest: "fixture:" + evidence.ID,
			ObservedRole: "button", ObservedAccessibleName: "Confirm action", ObservedAt: &observedAt,
			EvidenceRefs: []model.EvidenceRef{evidence},
		}},
		EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1,
	}
	specs := []controlledBusinessStageSpec{
		outlineNavigateStage(baseURL, evidence),
		{NodeID: "node_confirm_action", StageID: "stage_confirm_action", Title: "Confirm approved action", Objective: "Approved confirmation is completed", Intent: "Use only the App-approved live selector alternative and verify the confirmation result.", Route: "/app", Success: "Confirmed is visible", Kind: model.BusinessStageKindBusinessSubmit, RouteState: model.BusinessRouteStateWorkspace, Action: model.BrowserAgentInteraction{Kind: model.GraphActionClick, Target: model.ActionTarget{TestID: "stale-confirm-action"}, NonDestructive: true, WaitConditions: []string{"wait_after_entry_at_least_250ms"}, SelectorPolicy: "app_approved_alternatives_only"}, Target: model.BrowserAgentTargetContract{SemanticID: "target_confirm_action", Purpose: "Approved confirmation action", ComponentRef: "component:confirm-action", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1}, Component: component, Validation: model.ValidationSpec{ID: "validation_confirmed", Kind: "text_contains", Target: model.ActionTarget{TestID: "repair-status"}, Assertion: "Confirmed", Expected: "Confirmed", Required: true, EvidenceRefs: []model.EvidenceRef{evidence}}},
	}
	return finalizeControlledOutline(&base, specs, baseURL, domain, evidence)
}

// controlledOutlineBusyWaitRepairHandler keeps the approved target visible but
// marks the page busy until the initial 250 ms observation window has elapsed.
// The Worker may then propose one bounded wait extension before re-observing.
func controlledOutlineBusyWaitRepairHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>Outline Busy Wait Repair</title></head><body aria-busy="true"><main aria-label="Outline workspace"><h1>Workspace</h1><button data-testid="delayed-action" style="display:none" onclick="document.querySelector('[data-testid=wait-status]').textContent='Ready after wait'">Delayed action</button><p data-testid="wait-status">Loading</p></main><script>const originalQuerySelector=Document.prototype.querySelector;Document.prototype.querySelector=function(selector){if(selector==='[aria-busy="true"]'){setTimeout(()=>{document.body.setAttribute('aria-busy','false');originalQuerySelector.call(document,'[data-testid=delayed-action]').style.display='inline-block'},0);return document.body}return originalQuerySelector.call(this,selector)}</script></body></html>`))
}

// controlledOutlineBusyWaitRepairPackage authorizes only a bounded wait change;
// it does not authorize a selector, action, intent, route, or validation change.
func controlledOutlineBusyWaitRepairPackage(fixturePath, baseURL string) (model.ClientExecutionPackage, error) {
	evidence := model.EvidenceRef{ID: "ev_outline_busy_wait_repair_fixture", Kind: "fixture_source", Summary: "Server-owned controlled busy-page repair", Confidence: 1}
	base, domain, err := controlledOutlineBase(fixturePath, baseURL, "pkg_controlled_outline_busy_wait_repair", "project_controlled_outline_busy_wait_repair", "ctx_controlled_outline_busy_wait_repair", "run_controlled_outline_busy_wait_repair", "Controlled outline busy-wait repair flow")
	if err != nil {
		return model.ClientExecutionPackage{}, err
	}
	specs := []controlledBusinessStageSpec{
		outlineNavigateStage(baseURL, evidence),
		{NodeID: "node_delayed_action", StageID: "stage_delayed_action", Title: "Run delayed action", Objective: "Approved delayed action is completed", Intent: "Wait only within the App-approved bound, then run the approved action.", Route: "/app", Success: "Ready after wait is visible", Kind: model.BusinessStageKindBusinessSubmit, RouteState: model.BusinessRouteStateWorkspace, Action: model.BrowserAgentInteraction{Kind: model.GraphActionClick, Target: model.ActionTarget{TestID: "delayed-action"}, NonDestructive: true, WaitConditions: []string{"wait_after_entry_at_least_250ms"}, SelectorPolicy: "prefer_testid"}, Target: model.BrowserAgentTargetContract{SemanticID: "target_delayed_action", Purpose: "Approved delayed action", AllowedRoles: []string{"button"}, AllowedNames: []string{"Delayed action"}, ComponentRef: "component:delayed-action", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1}, Component: model.BrowserAgentComponentTarget{ComponentRef: "component:delayed-action", Role: "button", Name: "Delayed action", TestID: "delayed-action", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1}, Validation: model.ValidationSpec{ID: "validation_ready_after_wait", Kind: "text_contains", Target: model.ActionTarget{TestID: "wait-status"}, Assertion: "Ready after wait", Expected: "Ready after wait", Required: true, EvidenceRefs: []model.EvidenceRef{evidence}}},
	}
	return finalizeControlledOutline(&base, specs, baseURL, domain, evidence)
}
