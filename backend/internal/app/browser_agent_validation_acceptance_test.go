package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

// Work-package F acceptance tests from the 2026-08-13 server handoff. They
// prove the Validation Agent finds real defects, keeps findings stable, and
// never leaks sensitive content in its reports.

// 缺少项目需求输入动作必须被识别为 App Outline 完整性问题：business_input
// 阶段没有任何 fill/select/upload 动作时，执行前验证必须以稳定错误码
// business_input_action_missing 阻断，并归类为 App 包缺陷（domain=app）。
func TestPreExecutionFlagsMissingBusinessInputAsAppOutlineCompleteness(t *testing.T) {
	const nodeID = "business_stage_project_idea_input"
	vctx := model.BrowserAgentValidationContext{
		RunID: "run_business_input_missing", SourcePackageID: "pkg_acceptance_1",
		SourceBundleHashSHA256: "bundle_hash", EffectivePolicyHashSHA256: "policy_hash",
		StageApprovalPlan: &model.StageApprovalPlan{Stages: []model.StageApprovalStage{{
			ID: "stage-1", Order: 1, NodeID: nodeID,
			StageKind:   model.BusinessStageKindBusinessInput,
			Interaction: model.BrowserAgentInteraction{Kind: model.GraphActionClick, Value: "俄罗斯方块"},
		}}},
		ScriptOutline: &model.BrowserAgentScriptOutline{Stages: []model.BrowserAgentOutlineStage{{NodeID: nodeID}}},
		Plan: &model.ExecutionScriptDocument{Steps: []model.ScriptStep{{
			ID: "step-1", Order: 1, NodeID: nodeID,
			Action: model.ScriptActionInstruction{Type: model.GraphActionClick},
		}}},
		BrowserAgentContract: &model.BrowserAgentContract{ID: "contract_1"},
	}
	report, err := newDeterministicBrowserAgentStageVerifier(nil).ValidateBeforeExecution(context.Background(), vctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := report.Validate(); err != nil {
		t.Fatal(err)
	}
	if report.Decision != model.ValidationDecisionStopAndReport {
		t.Fatalf("missing business input action must stop execution, got %q", report.Decision)
	}
	var flagged bool
	for _, check := range report.Checks {
		if check.Code == "business_input_action_missing" || check.Code == "business_input_missing" {
			flagged = true
			if check.Passed {
				t.Fatalf("business input check must fail: %+v", check)
			}
			if check.Severity != model.FindingSeverityBlocking {
				t.Fatalf("business input incompleteness must be blocking: %+v", check)
			}
			if check.ResponsibilityDomain != model.ValidationCheckDomainApp {
				t.Fatalf("business input incompleteness is an App Outline defect (domain=app), got %q", check.ResponsibilityDomain)
			}
			if check.NodeID != nodeID {
				t.Fatalf("check must bind to the offending node %q, got %q", nodeID, check.NodeID)
			}
		}
	}
	if !flagged {
		t.Fatalf("no business input completeness check was emitted: %+v", report.Checks)
	}
}

// URL 已进入项目页但业务未完成时不得判定成功：URL 匹配通过不足以代替
// 业务断言；business 断言失败必须把该 stage 判为 StopAndReport。
func TestURLMatchAloneDoesNotProveBusinessCompletion(t *testing.T) {
	verifier := deterministicBrowserAgentStageVerifier{requiredValidations: map[string][]model.ValidationSpec{
		"node_project": {
			{ID: "validate_route", Kind: "url_matches", Required: true},
			{ID: "validate_business_completion", Kind: "element_visible", Required: true},
		},
	}}
	events := []model.StageExecutionEvent{{
		SchemaVersion: model.StageExecutionEventSchemaVersion, EventID: "event_url_only", RunID: "run_url_only",
		SourcePackageID: "pkg_1", SourceBundleHashSHA256: "bundle_hash", PolicyHashSHA256: "policy_hash",
		NodeID: "node_project", StageID: "stage_project", Attempt: 1, Sequence: 1,
		EventType: model.StageExecutionEventOutcomeObserved, OccurredAt: timeNowUTC(),
		Observation: &model.RuntimeObservation{
			Source: model.RuntimeObservationAssertion,
			URL:    "https://app.example.com/projects/123",
			Assertions: []model.RuntimeAssertion{
				{Kind: "required_url_matches:validate_route", Passed: true},
				{Kind: "required_element_visible:validate_business_completion", Passed: false},
			},
		},
		EvidenceRefs: []model.EvidenceRef{{ID: "evidence_1", Kind: model.EvidenceKindWebScreenshot}},
	}}
	report, err := verifier.ValidateStageEvents(context.Background(), model.BrowserAgentValidationContext{
		SourcePackageID: "pkg_1", SourceBundleHashSHA256: "bundle_hash", EffectivePolicyHashSHA256: "policy_hash",
	}, events)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != model.ValidationDecisionStopAndReport {
		t.Fatalf("URL change without business completion must stop the stage: %+v", report)
	}
	if report.PassRate >= 1 {
		t.Fatalf("pass rate must reflect the failed business assertion: %+v", report)
	}
	var urlPassed, businessFailed bool
	for _, check := range report.Checks {
		if check.Kind == "required_url_matches:validate_route" && check.Passed {
			urlPassed = true
		}
		if check.Kind == "required_element_visible:validate_business_completion" && !check.Passed {
			businessFailed = true
		}
	}
	if !urlPassed || !businessFailed {
		t.Fatalf("report must keep URL pass and business failure distinguishable: %+v", report.Checks)
	}
}

// 相同输入重复运行应产生稳定 finding code 和可比较证据：同一验证上下文
// 与同一事件两次验证，输出的报告 check code、顺序与判定必须完全一致。
func TestValidationFindingsAreStableAcrossRepeatedRuns(t *testing.T) {
	vctx := model.BrowserAgentValidationContext{
		RunID: "run_stability", SourcePackageID: "pkg_1",
		SourceBundleHashSHA256: "bundle_hash", EffectivePolicyHashSHA256: "policy_hash",
		StageApprovalPlan: &model.StageApprovalPlan{Stages: []model.StageApprovalStage{{ID: "stage-1", Order: 1, NodeID: "node_1"}}},
		ScriptOutline:     &model.BrowserAgentScriptOutline{Stages: []model.BrowserAgentOutlineStage{{NodeID: "node_1"}}},
		BrowserAgentContract: &model.BrowserAgentContract{ID: "contract_1"},
	}
	events := func() []model.StageExecutionEvent {
		return []model.StageExecutionEvent{{
			SchemaVersion: model.StageExecutionEventSchemaVersion, EventID: "event_1", RunID: "run_stability",
			SourcePackageID: "pkg_1", SourceBundleHashSHA256: "bundle_hash", PolicyHashSHA256: "policy_hash",
			NodeID: "node_1", StageID: "stage-1", Attempt: 1, Sequence: 1,
			EventType: model.StageExecutionEventOutcomeObserved, OccurredAt: timeNowUTC(),
			Observation:  &model.RuntimeObservation{Source: model.RuntimeObservationAssertion, Assertions: []model.RuntimeAssertion{{Kind: "element_visible", Passed: false}}},
			EvidenceRefs: []model.EvidenceRef{{ID: "evidence_1", Kind: model.EvidenceKindWebScreenshot}},
		}}
	}
	verifier := deterministicBrowserAgentStageVerifier{}
	first, err := verifier.ValidateStageEvents(context.Background(), vctx, events())
	if err != nil {
		t.Fatal(err)
	}
	second, err := verifier.ValidateStageEvents(context.Background(), vctx, events())
	if err != nil {
		t.Fatal(err)
	}
	if first.Decision != second.Decision || first.PassRate != second.PassRate || len(first.Checks) != len(second.Checks) {
		t.Fatalf("repeated validation diverged: first=%+v second=%+v", first, second)
	}
	for index := range first.Checks {
		if first.Checks[index].Code != second.Checks[index].Code ||
			first.Checks[index].Kind != second.Checks[index].Kind ||
			first.Checks[index].Passed != second.Checks[index].Passed ||
			first.Checks[index].Severity != second.Checks[index].Severity {
			t.Fatalf("finding at %d is not stable: %+v vs %+v", index, first.Checks[index], second.Checks[index])
		}
	}
}

// 所有报告通过敏感信息防泄漏测试：验证报告与失败诊断的序列化内容不得
// 携带密码、Cookie、Token、API Key 或完整页面内容。
func TestValidationReportsAndDiagnosticsNeverCarrySensitiveContent(t *testing.T) {
	const secretMarker = "LEAK-CHECK-password=hunter2;cookie=sid-xyz;api_key=sk-123"
	vctx := model.BrowserAgentValidationContext{
		RunID: "run_leak", SourcePackageID: "pkg_1",
		SourceBundleHashSHA256: "bundle_hash", EffectivePolicyHashSHA256: "policy_hash",
		StageApprovalPlan: &model.StageApprovalPlan{Stages: []model.StageApprovalStage{{
			ID: "stage-1", Order: 1, NodeID: "node_1", StageKind: model.BusinessStageKindBusinessInput,
		}}},
		ScriptOutline:        &model.BrowserAgentScriptOutline{Stages: []model.BrowserAgentOutlineStage{{NodeID: "node_1"}}},
		BrowserAgentContract: &model.BrowserAgentContract{ID: "contract_1"},
		Plan:                 &model.ExecutionScriptDocument{Steps: []model.ScriptStep{{ID: "step-1", NodeID: "node_1"}}},
	}
	report, err := newDeterministicBrowserAgentStageVerifier(nil).ValidateBeforeExecution(context.Background(), vctx)
	if err != nil {
		t.Fatal(err)
	}
	encodedReport, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}

	pkg := readBrowserAgentOutlineFixture(t)
	server := &DirectHTTPServer{}
	failedResult, err := server.directRuntimeFailureResult(
		context.Background(), &pkg, "job-leak-check", t.TempDir(), model.RecordingResultPackage{},
		newRuntimeExecutionError(runtimeErrorVideoWorkerMissing, errors.New(secretMarker)), time.Date(2026, 8, 11, 11, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	encodedDiagnostic, err := json.Marshal(failedResult.FailureDiagnostic)
	if err != nil {
		t.Fatal(err)
	}
	if failedResult.FailureDiagnostic == nil || !failedResult.FailureDiagnostic.RedactionReport.Applied || failedResult.FailureDiagnostic.RedactionReport.FullHTMLIncluded {
		t.Fatalf("diagnostic must be redacted: %+v", failedResult.FailureDiagnostic.RedactionReport)
	}
	for name, payload := range map[string][]byte{"validation_report": encodedReport, "failure_diagnostic": encodedDiagnostic} {
		text := string(payload)
		for _, marker := range []string{"hunter2", "sid-xyz", "sk-123", "password=", "api_key=", "LEAK-CHECK"} {
			if strings.Contains(text, marker) {
				t.Fatalf("%s leaked sensitive marker %q", name, marker)
			}
		}
	}
}
