package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

// BrowserAgentAcceptanceReport is intentionally independent from the App upload
// contract. It describes only Server-owned controlled acceptance evidence.
type BrowserAgentAcceptanceReport struct {
	SchemaVersion string                           `json:"schema_version"`
	GeneratedAt   time.Time                        `json:"generated_at"`
	Runtime       string                           `json:"runtime"`
	StrictGate    string                           `json:"strict_gate"`
	Scenarios     []BrowserAgentAcceptanceScenario `json:"scenarios"`
}

type BrowserAgentAcceptanceScenario struct {
	ID             string                           `json:"id"`
	Description    string                           `json:"description"`
	Expected       string                           `json:"expected"`
	Actual         string                           `json:"actual"`
	Verdict        string                           `json:"verdict"`
	ActionExecuted bool                             `json:"action_executed"`
	Evidence       []BrowserAgentAcceptanceArtifact `json:"evidence"`
	Assertions     []BrowserAgentAcceptanceCheck    `json:"assertions"`
	StopReason     string                           `json:"stop_reason,omitempty"`
	RepairAudit    *BrowserAgentRepairAudit         `json:"repair_audit,omitempty"`
}

type BrowserAgentRepairAudit struct {
	Applied                  bool   `json:"applied"`
	Field                    string `json:"field"`
	Before                   string `json:"before,omitempty"`
	After                    string `json:"after"`
	Attempt                  int    `json:"attempt"`
	PolicyDecision           string `json:"policy_decision"`
	SourceBundleHashSHA256   string `json:"source_bundle_hash_sha256"`
	ApprovedBundleHashIntact bool   `json:"approved_bundle_hash_intact"`
}

type BrowserAgentAcceptanceArtifact struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	URI       string `json:"uri"`
	MimeType  string `json:"mime_type"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

type BrowserAgentAcceptanceCheck struct {
	Kind   string `json:"kind"`
	Passed bool   `json:"passed"`
	Actual string `json:"actual,omitempty"`
}

type BrowserAgentAcceptanceView struct {
	Ready      bool                          `json:"ready"`
	CanRun     bool                          `json:"can_run"`
	Message    string                        `json:"message"`
	ReportPath string                        `json:"report_path,omitempty"`
	Report     *BrowserAgentAcceptanceReport `json:"report,omitempty"`
}

func (s *Service) GetBrowserAgentAcceptance(ctx context.Context) (BrowserAgentAcceptanceView, error) {
	_ = ctx
	path := s.browserAgentAcceptanceReportPath()
	view := BrowserAgentAcceptanceView{
		CanRun:  s.browserAgentAcceptanceFixturePath() != "" && fileExists(s.localVideoWorkerPath()) && commandReady(s.nodeBinaryForExecution()),
		Message: "尚未运行协议固定验收包。验收以完整 ClientExecutionPackage 走 Intake、Router、Outline Runner 和 Result 包，不使用 App 用户数据或生产凭据。",
	}
	report, err := readBrowserAgentAcceptanceReport(path)
	if errors.Is(err, os.ErrNotExist) {
		return view, nil
	}
	if err != nil {
		return view, err
	}
	view.Ready, view.ReportPath, view.Report = true, path, &report
	if report.StrictGate == "passed" {
		view.Message = "最近一次协议固定验收包通过：每个场景均已走完整 Server 协议链路。"
	} else {
		view.Message = "最近一次固定验收包未通过；不得继续扩展 Browser Agent 功能。"
	}
	return view, nil
}

func (s *Service) RunBrowserAgentAcceptance(ctx context.Context) (BrowserAgentAcceptanceView, error) {
	if s == nil {
		return BrowserAgentAcceptanceView{}, errors.New("browser-agent acceptance service is not configured")
	}
	fixturePath := s.browserAgentAcceptanceFixturePath()
	if fixturePath == "" || !fileExists(fixturePath) {
		return BrowserAgentAcceptanceView{}, errors.New("browser-agent outline acceptance fixture is not available")
	}
	if !fileExists(s.localVideoWorkerPath()) {
		return BrowserAgentAcceptanceView{}, errors.New("video-worker build artifact is not available")
	}
	if err := checkCommandReady(s.nodeBinaryForExecution()); err != nil {
		return BrowserAgentAcceptanceView{}, err
	}
	outputDir := filepath.Dir(s.browserAgentAcceptanceReportPath())
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		return BrowserAgentAcceptanceView{}, err
	}
	// The fixed acceptance now executes six complete browser, recording, and
	// render scenarios. Keep a bounded wall clock, but allow the final scenario
	// to finish instead of canceling an otherwise healthy suite at three minutes.
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	report, err := s.runProtocolBrowserAgentAcceptance(runCtx, fixturePath)
	if err != nil {
		return BrowserAgentAcceptanceView{}, err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return BrowserAgentAcceptanceView{}, err
	}
	if err := os.WriteFile(s.browserAgentAcceptanceReportPath(), append(data, '\n'), 0o600); err != nil {
		return BrowserAgentAcceptanceView{}, err
	}
	return s.GetBrowserAgentAcceptance(ctx)
}

func (s *Service) browserAgentAcceptanceFixturePath() string {
	if s == nil || s.runtime.DevRepoRoot == "" {
		return ""
	}
	return filepath.Join(s.runtime.DevRepoRoot, "contracts", "exchange", "v1", "client_execution_package.browser_agent_outline.json")
}

func (s *Service) browserAgentAcceptanceReportPath() string {
	if s == nil || s.runtime.ArtifactRoot == "" {
		return ""
	}
	return filepath.Join(s.runtime.ArtifactRoot, "browser-agent-acceptance", "latest", "acceptance-report.json")
}

func readBrowserAgentAcceptanceReport(path string) (BrowserAgentAcceptanceReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return BrowserAgentAcceptanceReport{}, err
	}
	var report BrowserAgentAcceptanceReport
	if err := json.Unmarshal(data, &report); err != nil {
		return BrowserAgentAcceptanceReport{}, fmt.Errorf("decode browser-agent acceptance report: %w", err)
	}
	if report.SchemaVersion != "cascade.browser_agent_acceptance.v1" || report.Runtime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 || report.StrictGate != "passed" {
		failed := []string{}
		for _, scenario := range report.Scenarios {
			if scenario.Verdict != "passed" {
				failed = append(failed, scenario.ID+":"+scenario.Actual)
			}
		}
		return BrowserAgentAcceptanceReport{}, fmt.Errorf("browser-agent acceptance report is incomplete: schema=%q runtime=%q gate=%q failed=%v", report.SchemaVersion, report.Runtime, report.StrictGate, failed)
	}
	baseRequired := map[string]bool{"success_navigation_click": false, "semantic_target_contract_conflict": false, "locator_missing": false, "required_validation_failure": false, "recording_and_trace_delivery": false}
	repairRequired := map[string]bool{"approved_selector_alternative_repair": false, "busy_page_wait_repair": false}
	if len(report.Scenarios) != len(baseRequired) && len(report.Scenarios) != len(baseRequired)+len(repairRequired) {
		return BrowserAgentAcceptanceReport{}, errors.New("browser-agent acceptance report has an unsupported scenario count")
	}
	for _, scenario := range report.Scenarios {
		if scenario.Verdict != "passed" {
			return BrowserAgentAcceptanceReport{}, errors.New("browser-agent acceptance report contains an unapproved or failed scenario")
		}
		if _, exists := baseRequired[scenario.ID]; exists {
			baseRequired[scenario.ID] = true
			continue
		}
		if _, exists := repairRequired[scenario.ID]; exists && len(report.Scenarios) == len(baseRequired)+len(repairRequired) {
			repairRequired[scenario.ID] = true
			continue
		}
		return BrowserAgentAcceptanceReport{}, errors.New("browser-agent acceptance report contains an unapproved scenario")
	}
	for _, present := range baseRequired {
		if !present {
			return BrowserAgentAcceptanceReport{}, errors.New("browser-agent acceptance report is missing a required scenario")
		}
	}
	if len(report.Scenarios) == len(baseRequired)+len(repairRequired) {
		for _, present := range repairRequired {
			if !present {
				return BrowserAgentAcceptanceReport{}, errors.New("browser-agent repair acceptance report is missing a required scenario")
			}
		}
	}
	return report, nil
}

func (s *Service) runProtocolBrowserAgentAcceptance(ctx context.Context, fixturePath string) (BrowserAgentAcceptanceReport, error) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Acceptance Dashboard</title></head><body><main aria-label="Dashboard"><h1>Dashboard</h1><button type="button" data-testid="invite-member">Invite teammate</button><p data-testid="invite-dialog" hidden>Invite flow starts</p></main><script>document.querySelector('[data-testid="invite-member"]').addEventListener('click',()=>document.querySelector('[data-testid="invite-dialog"]').hidden=false)</script></body></html>`))
	}))
	defer server.Close()

	base, err := protocolAcceptancePackage(fixturePath, server.URL)
	if err != nil {
		return BrowserAgentAcceptanceReport{}, err
	}
	report := BrowserAgentAcceptanceReport{SchemaVersion: "cascade.browser_agent_acceptance.v1", GeneratedAt: timeNowUTC(), Runtime: model.ExecutableScriptRuntimeBrowserAgentOutlineV1, StrictGate: "passed"}
	success, err := s.runProtocolAcceptanceScenario(ctx, base)
	if err != nil {
		return BrowserAgentAcceptanceReport{}, err
	}
	report.Scenarios = append(report.Scenarios, protocolAcceptanceSuccessScenario(success))

	semantic := cloneProtocolAcceptancePackage(base)
	setAcceptanceTargetName(&semantic, "Delete workspace")
	semanticResult, err := s.runProtocolAcceptanceScenario(ctx, semantic)
	if err != nil {
		return BrowserAgentAcceptanceReport{}, err
	}
	report.Scenarios = append(report.Scenarios, protocolAcceptanceFailureScenario("semantic_target_contract_conflict", "页面存在按钮但批准目标合同不匹配", "点击前拦截，并返回失败结果包", semanticResult, false, "browser_agent_target_not_resolved"))

	missing := cloneProtocolAcceptancePackage(base)
	setAcceptanceTargetName(&missing, "Missing action")
	missingResult, err := s.runProtocolAcceptanceScenario(ctx, missing)
	if err != nil {
		return BrowserAgentAcceptanceReport{}, err
	}
	report.Scenarios = append(report.Scenarios, protocolAcceptanceFailureScenario("locator_missing", "批准目标不存在", "点击前拦截，并返回失败结果包", missingResult, false, "browser_agent_target_not_resolved"))

	validation := cloneProtocolAcceptancePackage(base)
	setAcceptanceRequiredValidation(&validation, "This text is intentionally absent")
	validationResult, err := s.runProtocolAcceptanceScenario(ctx, validation)
	if err != nil {
		return BrowserAgentAcceptanceReport{}, err
	}
	report.Scenarios = append(report.Scenarios, protocolAcceptanceFailureScenario("required_validation_failure", "动作完成但必填业务结果不成立", "停止后续 Stage，并返回失败结果包", validationResult, true, "outcome_verification_failed"))
	report.Scenarios = append(report.Scenarios, protocolAcceptanceArtifactScenario(success))

	selectorServer := httptest.NewServer(http.HandlerFunc(controlledOutlineSelectorRepairHandler))
	defer selectorServer.Close()
	selectorRepairPackage, err := controlledOutlineSelectorRepairPackage(fixturePath, selectorServer.URL)
	if err != nil {
		return BrowserAgentAcceptanceReport{}, err
	}
	selectorSourceHash := selectorRepairPackage.ExecutableScriptBundle.Reproducibility.BundleHashSHA256
	selectorRepair, err := s.runProtocolAcceptanceScenario(ctx, selectorRepairPackage)
	if err != nil {
		return BrowserAgentAcceptanceReport{}, err
	}
	report.Scenarios = append(report.Scenarios, protocolAcceptanceRepairScenario("approved_selector_alternative_repair", "主定位器失效时，仅使用 App 已批准的候选定位器", "自动恢复、记录补丁账本且不修改原执行包", selectorRepair, selectorSourceHash))

	waitServer := httptest.NewServer(http.HandlerFunc(controlledOutlineBusyWaitRepairHandler))
	defer waitServer.Close()
	waitRepairPackage, err := controlledOutlineBusyWaitRepairPackage(fixturePath, waitServer.URL)
	if err != nil {
		return BrowserAgentAcceptanceReport{}, err
	}
	waitSourceHash := waitRepairPackage.ExecutableScriptBundle.Reproducibility.BundleHashSHA256
	waitRepair, err := s.runProtocolAcceptanceScenario(ctx, waitRepairPackage)
	if err != nil {
		return BrowserAgentAcceptanceReport{}, err
	}
	report.Scenarios = append(report.Scenarios, protocolAcceptanceRepairScenario("busy_page_wait_repair", "页面仍忙时，仅延长协议允许的有限等待", "自动恢复、记录等待前后值且不修改原执行包", waitRepair, waitSourceHash))
	for _, scenario := range report.Scenarios {
		if scenario.Verdict != "passed" {
			report.StrictGate = "failed"
		}
	}
	if report.StrictGate != "passed" {
		failed := make([]string, 0, len(report.Scenarios))
		for _, scenario := range report.Scenarios {
			if scenario.Verdict != "passed" {
				failed = append(failed, scenario.ID)
			}
		}
		return BrowserAgentAcceptanceReport{}, fmt.Errorf("protocol browser-agent acceptance failed: %s", strings.Join(failed, ", "))
	}
	return report, nil
}

type protocolAcceptanceRun struct {
	Status               model.ExecutionPackageStatusResponse
	Result               model.RecordingResultPackage
	Acknowledged         bool
	DeliveryAcknowledged bool
}

func (s *Service) runProtocolAcceptanceScenario(ctx context.Context, pkg model.ClientExecutionPackage) (protocolAcceptanceRun, error) {
	if err := normalizeClientExecutionPackageForUpload(&pkg); err != nil {
		return protocolAcceptanceRun{}, err
	}
	// Scenario fixtures mutate the Server-owned base package after its digest
	// was baked; refresh the approval digests exactly like the production
	// approval path so exchange intake compares a self-consistent subject.
	if _, err := refreshPackageApprovalDigests(&pkg); err != nil {
		return protocolAcceptanceRun{}, err
	}
	now := timeNowUTC()
	envelope, err := envelopeForClientExecutionPackage(pkg, now)
	if err != nil {
		return protocolAcceptanceRun{}, err
	}
	init, err := s.exchange.Init(ctx, model.ExecutionPackageInitRequest{OrgID: pkg.OrgID, ProjectID: pkg.ProjectID, PackageKind: model.ExchangePackageKindClientExecution, Producer: envelope.Producer})
	if err != nil {
		return protocolAcceptanceRun{}, err
	}
	upload, err := s.exchange.Upload(ctx, model.ExecutionPackageUploadRequest{UploadID: init.UploadID, Envelope: envelope, PayloadRef: envelope.PayloadRef}, pkg)
	if err != nil {
		return protocolAcceptanceRun{}, err
	}
	started, err := s.exchange.TryStartExecution(ctx, pkg.OrgID, upload.ExchangePackageID)
	if err != nil || !started.Started {
		return protocolAcceptanceRun{}, fmt.Errorf("protocol acceptance package did not enter runtime: %w", err)
	}
	status, err := s.runUploadedExecutionPackageSync(ctx, pkg.OrgID, upload.ExchangePackageID, started.Payload, started.CloudJobID)
	if err != nil {
		return protocolAcceptanceRun{}, err
	}
	resultID := status.ResultPackageID
	if resultID == "" {
		if status.Error != nil {
			return protocolAcceptanceRun{}, fmt.Errorf("protocol acceptance execution failed before result packaging: %s: %s", status.Error.Code, status.Error.Message)
		}
		return protocolAcceptanceRun{}, errors.New("protocol acceptance result package is missing")
	}
	result, err := s.exchange.GetResultPackage(ctx, pkg.OrgID, resultID)
	if err != nil {
		return protocolAcceptanceRun{}, err
	}
	ack, err := s.exchange.AckResultPackage(ctx, pkg.OrgID, model.ResultPackageAckRequest{
		ResultPackageID: resultID, AckedByInstallID: defaultDesktopInstallID,
		ReceivedAssetIDs: acceptanceArtifactIDs(result), VerifiedChecksums: true,
	})
	if err != nil {
		return protocolAcceptanceRun{}, err
	}
	return protocolAcceptanceRun{Status: status, Result: result, Acknowledged: ack.DeliveryStatus == model.ResultDeliveryStatusAcked, DeliveryAcknowledged: ack.VerifiedChecksums}, nil
}

func protocolAcceptancePackage(fixturePath string, baseURL string) (model.ClientExecutionPackage, error) {
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		return model.ClientExecutionPackage{}, err
	}
	host := strings.TrimPrefix(baseURL, "http://")
	data = bytes.ReplaceAll(data, []byte("https://app.example.com"), []byte(baseURL))
	data = bytes.ReplaceAll(data, []byte("app.example.com"), []byte(host))
	var pkg model.ClientExecutionPackage
	if err := json.Unmarshal(data, &pkg); err != nil {
		return model.ClientExecutionPackage{}, err
	}
	if err := normalizeClientExecutionPackageForUpload(&pkg); err != nil {
		return model.ClientExecutionPackage{}, err
	}
	// The protocol gate verifies execution, recording, dual-profile rendering,
	// probing, packaging, and acknowledgement. A one-minute delivery target adds
	// minutes of redundant encoding to every test without exercising another
	// protocol branch, so the acceptance-only clone uses a compact render.
	pkg.RecordingRunSpec.Timeline.TargetDurationSec = 6
	if pkg.ExecutableScriptBundle != nil && pkg.ExecutableScriptBundle.PlanJSON != nil {
		pkg.ExecutableScriptBundle.PlanJSON.RecordingRunSpec.Timeline.TargetDurationSec = 6
	}
	// Session entry must stay inside the approved route scope: the worker opens
	// the session at the product URL before executing the first stage, so the
	// declared product entry is the fixture's first approved route rather than
	// the bare base origin (which the route policy would reject as "/").
	if outline := pkg.ExecutableScriptBundle; outline != nil && outline.ScriptOutline != nil {
		if routes := outline.ScriptOutline.AllowedExplorationScope.AllowedRoutes; len(routes) > 0 && strings.HasPrefix(routes[0], "/") {
			pkg.ProjectContextSummary.ProductURL = baseURL + routes[0]
		}
	}
	return pkg, nil
}

func cloneProtocolAcceptancePackage(pkg model.ClientExecutionPackage) model.ClientExecutionPackage {
	data, _ := json.Marshal(pkg)
	var clone model.ClientExecutionPackage
	_ = json.Unmarshal(data, &clone)
	return clone
}

func setAcceptanceTargetName(pkg *model.ClientExecutionPackage, name string) {
	if pkg == nil || pkg.ExecutableScriptBundle == nil {
		return
	}
	for index := range pkg.ExecutableScriptBundle.StageApprovalPlan.Stages {
		stage := &pkg.ExecutableScriptBundle.StageApprovalPlan.Stages[index]
		if stage.NodeID == "node_invite_member" && stage.TargetContract != nil {
			stage.TargetContract.AllowedNames = []string{name}
		}
	}
	for index := range pkg.ExecutableScriptBundle.ScriptOutline.Stages {
		stage := &pkg.ExecutableScriptBundle.ScriptOutline.Stages[index]
		if stage.NodeID == "node_invite_member" && stage.TargetContract != nil {
			stage.TargetContract.AllowedNames = []string{name}
		}
	}
}

func setAcceptanceRequiredValidation(pkg *model.ClientExecutionPackage, expected string) {
	if pkg == nil || pkg.ExecutableScriptBundle == nil || pkg.ExecutableScriptBundle.PlanJSON == nil {
		return
	}
	for index := range pkg.ExecutableScriptBundle.PlanJSON.Steps {
		step := &pkg.ExecutableScriptBundle.PlanJSON.Steps[index]
		if step.NodeID == "node_invite_member" {
			step.Validations = []model.ValidationSpec{{ID: "validation_absent_text", Kind: "text_contains", Expected: expected, Required: true}}
		}
	}
}

func protocolAcceptanceSuccessScenario(run protocolAcceptanceRun) BrowserAgentAcceptanceScenario {
	hasFinalVideo := protocolAcceptanceHasArtifact(run.Result, "demo_video")
	strictReports := hasStrictBrowserAgentValidationReports(run.Result, 2)
	passed := run.Status.Status == model.ExchangePackageStatusCompleted && run.Result.Status == model.RecordingResultStatusGenerated && strictReports && hasFinalVideo && run.Acknowledged && run.DeliveryAcknowledged
	return BrowserAgentAcceptanceScenario{ID: "success_navigation_click", Description: "完整协议链：Intake 校验、导航、语义点击、结果包和交付确认", Expected: "任务完成，两个 Stage 均有真实验证、最终视频、结果包和 ack", Actual: acceptanceActual(passed), Verdict: acceptanceVerdict(passed), ActionExecuted: true, Evidence: protocolAcceptanceArtifacts(run.Result), Assertions: []BrowserAgentAcceptanceCheck{{Kind: "exchange_status", Passed: run.Status.Status == model.ExchangePackageStatusCompleted, Actual: string(run.Status.Status)}, {Kind: "strict_validation_reports", Passed: strictReports, Actual: fmt.Sprintf("%d", len(run.Result.ValidationReports))}, {Kind: "final_video", Passed: hasFinalVideo, Actual: "demo_video"}, {Kind: "result_package", Passed: run.Result.Status == model.RecordingResultStatusGenerated, Actual: string(run.Result.Status)}, {Kind: "delivery_ack", Passed: run.Acknowledged && run.DeliveryAcknowledged, Actual: "acknowledged"}}}
}

func hasStrictBrowserAgentValidationReports(result model.RecordingResultPackage, stageCount int) bool {
	if len(result.ValidationReports) != stageCount+2 || len(result.ValidationReports) < 2 {
		return false
	}
	return result.ValidationReports[0].Phase == model.ValidationPhasePreExecution &&
		result.ValidationReports[len(result.ValidationReports)-1].Phase == model.ValidationPhasePostExecution
}

func protocolAcceptanceFailureScenario(id, description, expected string, run protocolAcceptanceRun, actionExecuted bool, expectedCode string) BrowserAgentAcceptanceScenario {
	hasScreenshot, hasTrace := false, false
	actualCode := "missing_failure_diagnostic"
	if run.Result.FailureDiagnostic != nil {
		hasScreenshot = len(run.Result.FailureDiagnostic.ScreenshotRefs) > 0
		hasTrace = len(run.Result.FailureDiagnostic.TraceRefs) > 0
		actualCode = run.Result.FailureDiagnostic.Error.Code
	}
	passed := run.Status.Status == model.ExchangePackageStatusFailed && run.Result.Status == model.RecordingResultStatusFailed && run.Result.FailureDiagnostic != nil && run.Result.RepairRequest != nil && run.Result.RepairRequest.ApprovalRequired && run.Result.FailureDiagnostic.RedactionReport.Applied && !run.Result.FailureDiagnostic.RedactionReport.FullHTMLIncluded && run.Result.FailureDiagnostic.Error.Code == expectedCode && hasScreenshot && hasTrace
	return BrowserAgentAcceptanceScenario{ID: id, Description: description, Expected: expected, Actual: actualCode, Verdict: acceptanceVerdict(passed), ActionExecuted: actionExecuted, Evidence: protocolAcceptanceArtifacts(run.Result), StopReason: actualCode, Assertions: []BrowserAgentAcceptanceCheck{{Kind: "exchange_status", Passed: run.Status.Status == model.ExchangePackageStatusFailed, Actual: string(run.Status.Status)}, {Kind: "failure_result", Passed: run.Result.Status == model.RecordingResultStatusFailed, Actual: string(run.Result.Status)}, {Kind: "failure_code", Passed: actualCode == expectedCode, Actual: actualCode}, {Kind: "redacted_diagnostic", Passed: run.Result.FailureDiagnostic != nil && run.Result.FailureDiagnostic.RedactionReport.Applied && !run.Result.FailureDiagnostic.RedactionReport.FullHTMLIncluded, Actual: "redacted"}, {Kind: "failure_screenshot", Passed: hasScreenshot, Actual: "captured"}, {Kind: "failure_trace", Passed: hasTrace, Actual: "captured"}, {Kind: "repair_request", Passed: run.Result.RepairRequest != nil && run.Result.RepairRequest.ApprovalRequired, Actual: "approval_required"}}}
}

func protocolAcceptanceArtifactScenario(run protocolAcceptanceRun) BrowserAgentAcceptanceScenario {
	hasRecording, hasTrace := false, false
	for _, artifact := range run.Result.GeneratedAssets {
		hasRecording = hasRecording || artifact.Kind == "raw_recording"
		hasTrace = hasTrace || artifact.Kind == "browser_trace"
	}
	passed := hasRecording && hasTrace && run.Result.StageEventLogRef != nil
	return BrowserAgentAcceptanceScenario{ID: "recording_and_trace_delivery", Description: "完整协议链保留录屏、Trace 与 Stage 事件审计", Expected: "录屏、Trace 和事件审计均进入结果", Actual: acceptanceActual(passed), Verdict: acceptanceVerdict(passed), Evidence: protocolAcceptanceArtifacts(run.Result), Assertions: []BrowserAgentAcceptanceCheck{{Kind: "recording", Passed: hasRecording, Actual: "raw_recording"}, {Kind: "trace", Passed: hasTrace, Actual: "browser_trace"}, {Kind: "stage_event_log", Passed: run.Result.StageEventLogRef != nil, Actual: "jsonl"}}}
}

func protocolAcceptanceRepairScenario(id, description, expected string, run protocolAcceptanceRun, sourceHash string) BrowserAgentAcceptanceScenario {
	passed := run.Status.Status == model.ExchangePackageStatusCompleted && run.Result.Status == model.RecordingResultStatusGenerated && len(run.Result.PatchLedger) == 1 && run.Acknowledged && run.DeliveryAcknowledged
	var audit *BrowserAgentRepairAudit
	if len(run.Result.PatchLedger) == 1 {
		entry := run.Result.PatchLedger[0]
		intact := entry.SourceBundleHashSHA256 == sourceHash
		audit = &BrowserAgentRepairAudit{Applied: entry.Applied, Field: entry.Field, Before: entry.Before, After: entry.After, Attempt: entry.Attempt, PolicyDecision: string(entry.PolicyDecision), SourceBundleHashSHA256: entry.SourceBundleHashSHA256, ApprovedBundleHashIntact: intact}
		passed = passed && entry.Applied && entry.PolicyDecision == model.ValidationDecisionRepairAllowed && intact
	}
	return BrowserAgentAcceptanceScenario{ID: id, Description: description, Expected: expected, Actual: acceptanceActual(passed), Verdict: acceptanceVerdict(passed), ActionExecuted: true, Evidence: protocolAcceptanceArtifacts(run.Result), Assertions: []BrowserAgentAcceptanceCheck{{Kind: "patch_ledger", Passed: audit != nil && audit.Applied, Actual: acceptanceActual(audit != nil && audit.Applied)}, {Kind: "approved_bundle_hash_intact", Passed: audit != nil && audit.ApprovedBundleHashIntact, Actual: acceptanceActual(audit != nil && audit.ApprovedBundleHashIntact)}, {Kind: "delivery_ack", Passed: run.Acknowledged && run.DeliveryAcknowledged, Actual: acceptanceActual(run.Acknowledged && run.DeliveryAcknowledged)}}, RepairAudit: audit}
}

func protocolAcceptanceHasArtifact(result model.RecordingResultPackage, kind string) bool {
	for _, artifact := range result.GeneratedAssets {
		if artifact.Kind == kind {
			return true
		}
	}
	return false
}

func protocolAcceptanceArtifacts(result model.RecordingResultPackage) []BrowserAgentAcceptanceArtifact {
	artifacts := []BrowserAgentAcceptanceArtifact{}
	for _, artifact := range result.GeneratedAssets {
		artifacts = append(artifacts, BrowserAgentAcceptanceArtifact{ID: artifact.ID, Kind: artifact.Kind, URI: artifact.URI, MimeType: artifact.MimeType, SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes})
	}
	if result.StageEventLogRef != nil {
		artifact := result.StageEventLogRef
		artifacts = append(artifacts, BrowserAgentAcceptanceArtifact{ID: artifact.ID, Kind: artifact.Kind, URI: artifact.URI, MimeType: artifact.MimeType, SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes})
	}
	return artifacts
}

func acceptanceArtifactIDs(result model.RecordingResultPackage) []string {
	ids := make([]string, 0, len(result.Delivery.AssetRefs))
	for _, asset := range result.Delivery.AssetRefs {
		if asset.ID != "" {
			ids = append(ids, asset.ID)
		}
	}
	return ids
}

func acceptanceActual(passed bool) string {
	if passed {
		return "pass"
	}
	return "fail"
}
func acceptanceVerdict(passed bool) string {
	if passed {
		return "passed"
	}
	return "failed"
}
