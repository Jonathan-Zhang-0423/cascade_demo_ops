package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

type RealAppAcceptanceView struct {
	SchemaVersion string                  `json:"schema_version"`
	Scope         string                  `json:"scope"`
	OrgID         string                  `json:"org_id"`
	Items         []RealAppAcceptanceItem `json:"items"`
}

type RealAppAcceptanceItem struct {
	ExchangePackageID string                               `json:"exchange_package_id"`
	ProjectID         string                               `json:"project_id,omitempty"`
	PackageID         string                               `json:"package_id,omitempty"`
	CreatedAt         time.Time                            `json:"created_at,omitempty"`
	UpdatedAt         time.Time                            `json:"updated_at,omitempty"`
	Source            ExecutionPackageSourceSummary        `json:"source"`
	Package           ExecutionDebugPackageSummary         `json:"package"`
	Status            model.ExecutionPackageStatusResponse `json:"status"`
	Eligibility       RealAppAcceptanceEligibility         `json:"eligibility"`
	Result            *RealAppAcceptanceResult             `json:"result,omitempty"`
}

type RealAppAcceptanceEligibility struct {
	CanRun  bool                     `json:"can_run"`
	Verdict string                   `json:"verdict"`
	Issues  []RealAppAcceptanceIssue `json:"issues,omitempty"`
}

type RealAppAcceptanceIssue struct {
	Code    string `json:"code"`
	Owner   string `json:"owner"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
	Action  string `json:"action"`
}

type RealAppAcceptanceResult struct {
	Acceptance        *model.ExecutionAcceptanceSummary `json:"acceptance,omitempty"`
	Steps             []RealAppAcceptanceStep           `json:"steps,omitempty"`
	Validations       []RealAppAcceptanceValidation     `json:"validations,omitempty"`
	Patches           []model.RuntimePatchLedgerEntry   `json:"patches,omitempty"`
	Artifacts         []RealAppAcceptanceArtifact       `json:"artifacts,omitempty"`
	StageEvents       []RealAppAcceptanceStageEvent     `json:"stage_events,omitempty"`
	Failure           *model.ExecutionFailureSummary    `json:"failure,omitempty"`
	StageEventLogSeen bool                              `json:"stage_event_log_seen"`
}

type RealAppAcceptanceStep struct {
	NodeID          string   `json:"node_id"`
	Status          string   `json:"status"`
	DurationMS      int      `json:"duration_ms,omitempty"`
	Observed        bool     `json:"observed"`
	ValidationCount int      `json:"validation_count"`
	ArtifactKinds   []string `json:"artifact_kinds,omitempty"`
	ErrorCode       string   `json:"error_code,omitempty"`
}

type RealAppAcceptanceValidation struct {
	Phase           model.ValidationPhase          `json:"phase"`
	NodeID          string                         `json:"node_id,omitempty"`
	StageID         string                         `json:"stage_id,omitempty"`
	Decision        model.ValidationDecision       `json:"decision"`
	PassRate        float64                        `json:"pass_rate"`
	EvidenceQuality model.RuntimeObservationSource `json:"evidence_quality"`
	CheckCount      int                            `json:"check_count"`
	FailedChecks    int                            `json:"failed_checks"`
}

type RealAppAcceptanceArtifact struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	MimeType    string `json:"mime_type,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	SizeBytes   int64  `json:"size_bytes,omitempty"`
	Sensitive   bool   `json:"sensitive,omitempty"`
	DownloadURL string `json:"download_url,omitempty"`
}

type RealAppAcceptanceStageEvent struct {
	NodeID           string                        `json:"node_id"`
	StageID          string                        `json:"stage_id"`
	Attempt          int                           `json:"attempt"`
	Sequence         int64                         `json:"sequence"`
	EventType        model.StageExecutionEventType `json:"event_type"`
	OccurredAt       time.Time                     `json:"occurred_at"`
	EvidenceCount    int                           `json:"evidence_count"`
	AssertionCount   int                           `json:"assertion_count"`
	FailedAssertions int                           `json:"failed_assertions"`
}

func (s *Service) GetRealAppExecutionAcceptance(ctx context.Context, orgID string) (RealAppAcceptanceView, error) {
	orgID = strings.TrimSpace(orgID)
	if orgID == "" {
		return RealAppAcceptanceView{}, errors.New("org_id is required")
	}
	listed, err := s.ListExecutionPackages(ctx, orgID)
	if err != nil {
		return RealAppAcceptanceView{}, err
	}
	view := RealAppAcceptanceView{SchemaVersion: "cascade.real_app_execution_acceptance.v1", Scope: "formal_app_exchange_only", OrgID: orgID}
	for _, listedItem := range listed.Items {
		item, itemErr := s.realAppAcceptanceItem(ctx, orgID, listedItem)
		if itemErr != nil {
			return RealAppAcceptanceView{}, itemErr
		}
		if item.Source.Origin == "server_controlled_fixture" {
			continue
		}
		view.Items = append(view.Items, item)
	}
	return view, nil
}

func (s *Service) GetRealAppExecutionAcceptanceItem(ctx context.Context, orgID, exchangePackageID string) (RealAppAcceptanceItem, error) {
	listed, err := s.ListExecutionPackages(ctx, strings.TrimSpace(orgID))
	if err != nil {
		return RealAppAcceptanceItem{}, err
	}
	for _, item := range listed.Items {
		if item.ExchangePackageID == exchangePackageID {
			return s.realAppAcceptanceItem(ctx, orgID, item)
		}
	}
	return RealAppAcceptanceItem{}, errors.New("exchange package not found")
}

func (s *Service) RunRealAppExecutionAcceptance(ctx context.Context, orgID, exchangePackageID string) (model.ExecutionPackageStatusResponse, error) {
	item, err := s.GetRealAppExecutionAcceptanceItem(ctx, orgID, exchangePackageID)
	if err != nil {
		return model.ExecutionPackageStatusResponse{}, err
	}
	if !item.Eligibility.CanRun {
		return item.Status, fmt.Errorf("real App acceptance is blocked: %s", acceptanceIssueSummary(item.Eligibility.Issues))
	}
	return s.RunUploadedExecutionPackage(ctx, orgID, exchangePackageID)
}

func (s *Service) realAppAcceptanceItem(ctx context.Context, orgID string, listed model.ExecutionPackageListItem) (RealAppAcceptanceItem, error) {
	status, err := s.GetExecutionPackageStatus(ctx, orgID, listed.ExchangePackageID)
	if err != nil {
		return RealAppAcceptanceItem{}, err
	}
	debug, err := s.GetExecutionPackageDebug(ctx, orgID, listed.ExchangePackageID)
	if err != nil {
		return RealAppAcceptanceItem{}, err
	}
	source, err := s.exchange.ExecutionPackageSource(ctx, orgID, listed.ExchangePackageID)
	if err != nil {
		return RealAppAcceptanceItem{}, err
	}
	item := RealAppAcceptanceItem{ExchangePackageID: listed.ExchangePackageID, ProjectID: listed.ProjectID, PackageID: listed.PackageID, CreatedAt: listed.CreatedAt, UpdatedAt: listed.UpdatedAt, Source: source, Package: debug.Package, Status: status}
	item.Eligibility = realAppAcceptanceEligibility(source, debug, status)
	if status.ResultPackageID != "" {
		result, snapshotErr := s.exchange.ResultSnapshot(ctx, orgID, status.ResultPackageID)
		if snapshotErr == nil {
			item.Result = s.realAppAcceptanceResult(orgID, status, result)
		}
	}
	return item, nil
}

func realAppAcceptanceEligibility(source ExecutionPackageSourceSummary, debug ExecutionPackageDebugView, status model.ExecutionPackageStatusResponse) RealAppAcceptanceEligibility {
	eligibility := RealAppAcceptanceEligibility{Verdict: "ready"}
	if !source.AppGenerated || source.Origin != "app_formal_exchange" || !source.TransportAuthenticated {
		eligibility.Issues = append(eligibility.Issues, RealAppAcceptanceIssue{Code: "app_origin_unverified", Owner: "app", Field: "transport.Cascade-Session / envelope.producer.install_id", Message: "执行包没有绑定到经过验证的 App Installation 会话。", Action: "App 使用同一 Installation 会话完成 init 和 upload，并保证 producer.install_id 一致。"})
	}
	if debug.Package.ScriptRuntime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 {
		eligibility.Issues = append(eligibility.Issues, RealAppAcceptanceIssue{Code: "runtime_not_outline_v1", Owner: "app", Field: "executable_script_bundle.script_manifest.runtime", Message: "执行包不是 browser-agent-outline-v1。", Action: "App 按最新协议重新生成并审批 Outline 执行包。"})
	}
	for _, blocker := range debug.Readiness.Blockers {
		if blocker.Code == "execution_terminal" || blocker.Code == "execution_already_started" {
			continue
		}
		owner, field, action := acceptanceBlockerResponsibility(blocker.Code)
		eligibility.Issues = append(eligibility.Issues, RealAppAcceptanceIssue{Code: blocker.Code, Owner: owner, Field: field, Message: blocker.Message, Action: action})
	}
	if isTerminalExchangeStatus(status.Status) {
		eligibility.Verdict = "finished"
		return eligibility
	}
	if status.Status == model.ExchangePackageStatusRunning || status.Status == model.ExchangePackageStatusQueued {
		eligibility.Verdict = "running"
		return eligibility
	}
	eligibility.CanRun = len(eligibility.Issues) == 0
	if !eligibility.CanRun {
		eligibility.Verdict = "blocked"
	}
	return eligibility
}

func acceptanceBlockerResponsibility(code string) (owner, field, action string) {
	switch code {
	case "video_worker_missing", "node_runtime_missing":
		return "server", "server.runtime", "配置并构建 Server 的 Node/video-worker 运行环境。"
	case "execution_terminal", "execution_already_started":
		return "server", "exchange.status", "查看当前执行结果；需要重试时由 App 生成新的执行包。"
	case "payload_unavailable":
		return "app", "payload_ref", "确认 Server Worker 已完成生产加密包的解密交接。"
	default:
		return "app", "client_execution_package", "按照协议修正字段并重新生成、审批和上传执行包。"
	}
}

func (s *Service) realAppAcceptanceResult(orgID string, status model.ExecutionPackageStatusResponse, result model.RecordingResultPackage) *RealAppAcceptanceResult {
	view := &RealAppAcceptanceResult{Failure: status.FailureSummary, Patches: append([]model.RuntimePatchLedgerEntry{}, result.PatchLedger...), StageEventLogSeen: result.StageEventLogRef != nil}
	if status.ResultSummary != nil {
		view.Acceptance = status.ResultSummary.Acceptance
	}
	for _, step := range result.StepResults {
		artifactKinds := make([]string, 0, len(step.Artifacts))
		for _, artifact := range step.Artifacts {
			artifactKinds = appendUniqueAcceptanceString(artifactKinds, artifact.Kind)
		}
		errorCode := ""
		if step.Error != nil {
			errorCode = step.Error.Code
		}
		view.Steps = append(view.Steps, RealAppAcceptanceStep{NodeID: step.NodeID, Status: step.Status, DurationMS: step.DurationMS, Observed: !strings.Contains(strings.ToLower(step.ObservedState), string(model.RuntimeObservationDerivedPlan)), ValidationCount: len(step.ValidationIDs), ArtifactKinds: artifactKinds, ErrorCode: errorCode})
	}
	for _, report := range result.ValidationReports {
		failed := 0
		for _, check := range report.Checks {
			if !check.Passed {
				failed++
			}
		}
		view.Validations = append(view.Validations, RealAppAcceptanceValidation{Phase: report.Phase, NodeID: report.NodeID, StageID: report.StageID, Decision: report.Decision, PassRate: report.PassRate, EvidenceQuality: report.EvidenceQuality, CheckCount: len(report.Checks), FailedChecks: failed})
	}
	for _, artifact := range result.GeneratedAssets {
		view.Artifacts = append(view.Artifacts, RealAppAcceptanceArtifact{ID: artifact.ID, Kind: artifact.Kind, MimeType: artifact.MimeType, SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes, Sensitive: artifact.Sensitive, DownloadURL: fmt.Sprintf("/v1/desktop/app-execution-acceptance/%s/artifacts/%s?org_id=%s", urlPathEscape(status.ExchangePackageID), urlPathEscape(artifact.ID), url.QueryEscape(orgID))})
	}
	view.StageEvents = s.readSafeAcceptanceStageEvents(result.StageEventLogRef)
	return view
}

func (s *Service) readSafeAcceptanceStageEvents(ref *model.ArtifactRef) []RealAppAcceptanceStageEvent {
	if ref == nil {
		return nil
	}
	path, err := s.localArtifactPath(ref.URI)
	if err != nil {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	var events []RealAppAcceptanceStageEvent
	scanner := bufio.NewScanner(file)
	for scanner.Scan() && len(events) < 5000 {
		var event model.StageExecutionEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		assertions, failed := 0, 0
		if event.Observation != nil {
			assertions = len(event.Observation.Assertions)
			for _, assertion := range event.Observation.Assertions {
				if !assertion.Passed {
					failed++
				}
			}
		}
		events = append(events, RealAppAcceptanceStageEvent{NodeID: event.NodeID, StageID: event.StageID, Attempt: event.Attempt, Sequence: event.Sequence, EventType: event.EventType, OccurredAt: event.OccurredAt, EvidenceCount: len(event.EvidenceRefs), AssertionCount: assertions, FailedAssertions: failed})
	}
	return events
}

func acceptanceIssueSummary(issues []RealAppAcceptanceIssue) string {
	parts := make([]string, 0, len(issues))
	for _, issue := range issues {
		parts = append(parts, issue.Code+" ("+issue.Owner+")")
	}
	return strings.Join(parts, ", ")
}

func appendUniqueAcceptanceString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
