package app

import (
	"context"
	"sort"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

const DirectFailureReunderstandingSchemaVersion = "demoops.direct_failure_reunderstanding.v1"

type DirectFailureReunderstandingRequest struct {
	SchemaVersion           string   `json:"schema_version"`
	SourceJobID             string   `json:"source_job_id"`
	SourceResultID          string   `json:"source_result_id"`
	SourcePackageID         string   `json:"source_package_id"`
	RepairRequestID         string   `json:"repair_request_id"`
	BasePackageDigestSHA256 string   `json:"base_package_digest_sha256"`
	BaseGraphDigestSHA256   string   `json:"base_graph_digest_sha256"`
	FailedBundleHashSHA256  string   `json:"failed_bundle_hash_sha256"`
	FailedPlanHashSHA256    string   `json:"failed_plan_hash_sha256"`
	DiagnosticDigestSHA256  string   `json:"diagnostic_digest_sha256"`
	SelectedIssueIDs        []string `json:"selected_issue_ids"`
	IdempotencyKey          string   `json:"idempotency_key"`
	UserConfirmed           bool     `json:"user_confirmed"`
}

type DirectFailureReunderstandingResult struct {
	State                       *orchestrator.CascadeState         `json:"state"`
	Build                       *ClientExecutionPackageBuild       `json:"build,omitempty"`
	NewPackageID                string                             `json:"new_package_id,omitempty"`
	PackageDigestSHA256         string                             `json:"package_digest_sha256,omitempty"`
	GraphDigestSHA256           string                             `json:"graph_digest_sha256,omitempty"`
	ApprovalSubjectDigestSHA256 string                             `json:"approval_subject_digest_sha256,omitempty"`
	ConfidenceAssessmentHash    string                             `json:"confidence_assessment_hash,omitempty"`
	RepairLineage               *model.ScriptRepairLineage         `json:"repair_lineage,omitempty"`
	Issues                      []model.DirectReunderstandingIssue `json:"issues,omitempty"`
	RequiresReapproval          bool                               `json:"requires_reapproval"`
}

func (s *Service) ReunderstandDirectBrowserAgentFailure(ctx context.Context, projectID string, request DirectFailureReunderstandingRequest) (DirectFailureReunderstandingResult, error) {
	if request.SchemaVersion != DirectFailureReunderstandingSchemaVersion || !request.UserConfirmed || strings.TrimSpace(request.IdempotencyKey) == "" {
		return DirectFailureReunderstandingResult{}, &directReunderstandingError{"bad_request", "schema_version, idempotency_key, and explicit user confirmation are required"}
	}
	requestDigest, err := model.DigestCanonicalJSON(request)
	if err != nil {
		return DirectFailureReunderstandingResult{}, err
	}
	state, err := s.states.Load(ctx, strings.TrimSpace(projectID))
	if err != nil {
		return DirectFailureReunderstandingResult{}, err
	}
	if state.DesktopCloudRun == nil {
		return DirectFailureReunderstandingResult{}, &directReunderstandingError{"repair_lineage_mismatch", "persisted failed result is unavailable"}
	}
	run := state.DesktopCloudRun
	for _, audit := range run.RepairHistory {
		if audit.IdempotencyKey != request.IdempotencyKey {
			continue
		}
		if audit.RequestDigestSHA256 != requestDigest {
			return DirectFailureReunderstandingResult{}, &directReunderstandingError{"idempotency_conflict", "idempotency key was already used with a different request"}
		}
		return s.currentReunderstandingResult(ctx, state, audit.ReunderstandingIssues)
	}
	if run.ResultPackage == nil {
		return DirectFailureReunderstandingResult{}, &directReunderstandingError{"repair_lineage_mismatch", "persisted failed result is unavailable"}
	}
	result := run.ResultPackage
	if result.Status != model.RecordingResultStatusFailed || result.RepairRequest == nil || result.FailureDiagnostic == nil {
		return DirectFailureReunderstandingResult{}, &directReunderstandingError{"repair_lineage_mismatch", "result is not a traceable failed Browser Agent package"}
	}
	if request.SourceJobID != result.CloudJobID || request.SourceResultID != result.ResultID || request.SourcePackageID != result.SourcePackageID || request.RepairRequestID != result.RepairRequest.ID {
		return DirectFailureReunderstandingResult{}, &directReunderstandingError{"repair_lineage_mismatch", "request identities do not match the persisted failed result"}
	}
	diagnosticDigest, err := model.DigestCanonicalJSON(result.FailureDiagnostic)
	if err != nil {
		return DirectFailureReunderstandingResult{}, err
	}
	if request.DiagnosticDigestSHA256 != diagnosticDigest || request.BasePackageDigestSHA256 != run.PackageDigestSHA256 || request.BaseGraphDigestSHA256 != firstNonEmptyString(run.GraphDigestSHA256, result.AuditTrail.GraphDigest) || request.FailedBundleHashSHA256 != firstNonEmptyString(run.BundleHashSHA256, result.RepairRequest.FailedBundleHashSHA256) || request.FailedPlanHashSHA256 != firstNonEmptyString(run.PlanHashSHA256, result.RepairRequest.FailedPlanHashSHA256) {
		return DirectFailureReunderstandingResult{}, &directReunderstandingError{"package_preview_stale", "failed package lineage or digest changed after the repair preview"}
	}
	issues := append([]model.DirectReunderstandingIssue(nil), run.ReunderstandingIssues...)
	if len(issues) == 0 {
		issues = directIssuesFromFailedResult(*result, state.ExecutableScriptBundle)
	}
	if len(issues) == 0 {
		return DirectFailureReunderstandingResult{}, &directReunderstandingError{"reunderstanding_incomplete", "the failed result does not contain an authoritative App reunderstanding issue"}
	}
	selected := map[string]bool{}
	for _, value := range request.SelectedIssueIDs {
		selected[strings.TrimSpace(value)] = true
	}
	for _, issue := range issues {
		if issue.Required && !selected[directReunderstandingIssueID(issue)] {
			return DirectFailureReunderstandingResult{}, &directReunderstandingError{"bad_request", "all required reunderstanding issues must be selected"}
		}
	}
	input, err := userInputFromProjectContext(state.ProjectContext)
	if err != nil {
		return DirectFailureReunderstandingResult{}, &directReunderstandingError{"reunderstanding_incomplete", "required local credential or project input is unavailable"}
	}
	generation := state.ExecutionPackageGeneration
	next, err := s.CreateProject(ctx, input)
	if err != nil {
		return DirectFailureReunderstandingResult{}, &directReunderstandingError{"reunderstanding_incomplete", "real page rescan and package regeneration did not complete"}
	}
	next.ExecutionPackageGeneration = generation
	now := time.Now().UTC()
	lineage := &model.ScriptRepairLineage{BaseBundleID: state.ExecutableScriptBundle.ID, BaseBundleHashSHA256: request.FailedBundleHashSHA256, SourceResultID: result.ResultID, SourceCloudJobID: result.CloudJobID, RepairAttempt: result.RepairRequest.RepairAttempt + 1, ChangeSummary: "Re-understood from redacted Browser Agent failure evidence; requires approval.", DiagnosticRefs: directDiagnosticRefs(*result.FailureDiagnostic), CreatedAt: now}
	if next.ExecutableScriptBundle == nil {
		return DirectFailureReunderstandingResult{}, &directReunderstandingError{"reunderstanding_incomplete", "regenerated executable bundle is missing"}
	}
	next.ExecutableScriptBundle.RepairLineage = lineage
	next.Approved = false
	next.CurrentNode = orchestrator.NodeHumanApprove
	next.Status = orchestrator.FlowStatusAwaitingHuman
	audit := orchestrator.DesktopDirectRepairAuditState{SourceResultID: result.ResultID, SourcePackageID: result.SourcePackageID, SourceJobID: result.CloudJobID, RepairRequestID: result.RepairRequest.ID, IdempotencyKey: request.IdempotencyKey, RequestDigestSHA256: requestDigest, ResultPackage: result, DownloadedAssets: append([]orchestrator.DesktopDownloadedAssetState(nil), run.DownloadedAssets...), ReunderstandingIssues: issues, CreatedAt: now}
	next.DesktopCloudRun = &orchestrator.DesktopCloudRunState{SchemaVersion: desktopCloudRunSchemaVersion, Transport: directTransportStateName, OrgID: firstNonEmptyString(run.OrgID, defaultDesktopOrgID), Status: "not_uploaded", Stage: "local_generated", Message: "重新理解草稿已生成，等待人工审批。", RequiresReapproval: true, RepairHistory: append(append([]orchestrator.DesktopDirectRepairAuditState(nil), run.RepairHistory...), audit), LastRepairSourceID: result.ResultID}
	if err := s.states.Save(ctx, next); err != nil {
		return DirectFailureReunderstandingResult{}, err
	}
	s.invalidateApprovedBuildsForProject(projectID)
	build, err := s.BuildClientExecutionPackage(ctx, projectID, firstNonEmptyString(run.OrgID, defaultDesktopOrgID))
	if err != nil {
		next.DesktopCloudRun.Stage = "reunderstanding_incomplete"
		next.DesktopCloudRun.Message = "重新扫描已完成，但正式执行包门禁仍未通过。"
		next.DesktopCloudRun.BlockingErrorCode = "reunderstanding_incomplete"
		next.DesktopCloudRun.NextAction = "resolve_app_package_gate_and_rescan"
		if saveErr := s.states.Save(ctx, next); saveErr != nil {
			return DirectFailureReunderstandingResult{}, saveErr
		}
		return DirectFailureReunderstandingResult{State: next, Issues: issues, RequiresReapproval: true}, &directReunderstandingError{"reunderstanding_incomplete", "regenerated draft still fails the formal package gate"}
	}
	next.DesktopCloudRun.PackageID = build.Package.PackageID
	next.DesktopCloudRun.ExchangePackageID = build.Package.PackageID
	next.DesktopCloudRun.PackageDigestSHA256 = build.PackageDigestSHA256
	next.DesktopCloudRun.GraphDigestSHA256 = build.Package.Reproducibility.GraphHashSHA256
	next.DesktopCloudRun.ApprovalSubjectDigestSHA256 = build.ApprovalSubjectDigestSHA256
	if build.Package.ConfidenceSummary != nil {
		next.DesktopCloudRun.ConfidenceAssessmentHash = build.Package.ConfidenceSummary.AssessmentHash
	}
	if build.Package.ExecutableScriptBundle != nil {
		next.DesktopCloudRun.BundleHashSHA256 = build.Package.ExecutableScriptBundle.Reproducibility.BundleHashSHA256
		next.DesktopCloudRun.PlanHashSHA256 = build.Package.ExecutableScriptBundle.Reproducibility.PlanHashSHA256
	}
	next.DesktopCloudRun.RepairHistory[len(next.DesktopCloudRun.RepairHistory)-1].NewPackageID = build.Package.PackageID
	if err := s.states.Save(ctx, next); err != nil {
		return DirectFailureReunderstandingResult{}, err
	}
	return directReunderstandingResult(next, build, issues), nil
}

func (s *Service) currentReunderstandingResult(ctx context.Context, state *orchestrator.CascadeState, issues []model.DirectReunderstandingIssue) (DirectFailureReunderstandingResult, error) {
	orgID := defaultDesktopOrgID
	if state.DesktopCloudRun != nil {
		orgID = firstNonEmptyString(state.DesktopCloudRun.OrgID, defaultDesktopOrgID)
	}
	build, err := s.BuildClientExecutionPackage(ctx, state.ProjectID, orgID)
	if err != nil {
		return DirectFailureReunderstandingResult{State: state, Issues: issues, RequiresReapproval: true}, &directReunderstandingError{"reunderstanding_incomplete", "regenerated draft still fails the formal package gate"}
	}
	return directReunderstandingResult(state, build, issues), nil
}

func directReunderstandingResult(state *orchestrator.CascadeState, build ClientExecutionPackageBuild, issues []model.DirectReunderstandingIssue) DirectFailureReunderstandingResult {
	result := DirectFailureReunderstandingResult{State: state, Build: &build, NewPackageID: build.Package.PackageID, PackageDigestSHA256: build.PackageDigestSHA256, GraphDigestSHA256: build.Package.Reproducibility.GraphHashSHA256, ApprovalSubjectDigestSHA256: build.ApprovalSubjectDigestSHA256, Issues: issues, RequiresReapproval: true}
	if build.Package.ConfidenceSummary != nil {
		result.ConfidenceAssessmentHash = build.Package.ConfidenceSummary.AssessmentHash
	}
	if build.Package.ExecutableScriptBundle != nil {
		result.RepairLineage = build.Package.ExecutableScriptBundle.RepairLineage
	}
	return result
}

func directIssuesFromFailedResult(result model.RecordingResultPackage, bundle *model.ExecutableRecordingScriptBundle) []model.DirectReunderstandingIssue {
	issues := []model.DirectReunderstandingIssue{}
	for _, report := range result.ValidationReports {
		if report.Decision != model.ValidationDecisionReunderstandingRequired {
			continue
		}
		for _, check := range report.Checks {
			if check.Passed {
				continue
			}
			issue := model.DirectReunderstandingIssue{Code: firstNonEmptyString(check.Code, check.Kind), StageID: firstNonEmptyString(check.StageID, report.StageID), NodeID: firstNonEmptyString(check.NodeID, report.NodeID), Severity: check.Severity, Required: check.Required, Summary: check.Summary, Impact: check.Impact, Suggestion: check.Suggestion, NextStep: check.NextStep, ResponsibilityDomain: check.ResponsibilityDomain, EvidenceIDs: evidenceIDs(check.EvidenceRefs)}
			issue.IssueID = model.StableDirectReunderstandingIssueID(issue)
			issues = append(issues, issue)
		}
	}
	if bundle != nil {
		if consistency, ok := model.ValidateBrowserAgentOutlineConsistency(bundle).(*model.OutlineConsistencyError); ok && directAppGateCode(consistency.Code) {
			issue := model.DirectReunderstandingIssue{Code: consistency.Code, NodeID: consistency.NodeID, Severity: model.FindingSeverityBlocking, Required: true, Summary: consistency.Reason, ResponsibilityDomain: model.ValidationCheckDomainApp}
			issue.IssueID = model.StableDirectReunderstandingIssueID(issue)
			issues = append(issues, issue)
		}
	}
	return issues
}

func directAppGateCode(code string) bool {
	switch code {
	case "selector_route_provenance_mismatch", "authentication_context_unverified", "login_entry_evidence_missing", "login_success_validation_missing":
		return true
	}
	return false
}
func evidenceIDs(refs []model.EvidenceRef) []string {
	out := []string{}
	for _, ref := range refs {
		if ref.ID != "" {
			out = append(out, ref.ID)
		}
	}
	sort.Strings(out)
	return out
}
func directDiagnosticRefs(d model.ScriptFailureDiagnostic) []model.EvidenceRef {
	refs := []model.EvidenceRef{}
	for _, artifact := range append(append([]model.PackageArtifactDescriptor{}, d.ScreenshotRefs...), d.TraceRefs...) {
		refs = append(refs, model.EvidenceRef{ID: artifact.ID, Kind: model.EvidenceKindBrowserTrace})
	}
	return refs
}
func directReunderstandingIssueID(issue model.DirectReunderstandingIssue) string {
	if strings.TrimSpace(issue.IssueID) != "" {
		return strings.TrimSpace(issue.IssueID)
	}
	return model.StableDirectReunderstandingIssueID(issue)
}
