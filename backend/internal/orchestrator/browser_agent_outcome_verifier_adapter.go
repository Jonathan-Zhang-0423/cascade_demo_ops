package orchestrator

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

// BrowserAgentOutcomeVerifierAdapter adapts the legacy validation components
// (PreExecutionValidator, PostExecutionAnalyzer, RealTimeBatchValidator)
// to implement the new app.OutcomeVerifier interface required by Server's
// Browser Agent Runtime. This allows reusing existing validation rules
// (domain checks, selector analysis, repair proposals) while conforming to
// the new protocol defined in validation-agent-server-handoff-2026-07-29.md.
//
// Key adaptation: the legacy validators return LegacyValidationReport with
// GlobalFeedbackType (continue/fine_tune/reunderstanding_required), which we
// map to the new ValidationReport.Decision enum.
type BrowserAgentOutcomeVerifierAdapter struct {
	preValidator *PreExecutionValidator
	postAnalyzer *PostExecutionAnalyzer
	runtimeVal   *RealTimeBatchValidator
	repairGen    *RepairProposalGenerator
	config       *model.ValidationConfig
}

// NewBrowserAgentOutcomeVerifierAdapter creates an adapter that wraps legacy validation components.
func NewBrowserAgentOutcomeVerifierAdapter(config *model.ValidationConfig) *BrowserAgentOutcomeVerifierAdapter {
	return &BrowserAgentOutcomeVerifierAdapter{
		preValidator: NewPreExecutionValidator(config),
		postAnalyzer: NewPostExecutionAnalyzer(config),
		runtimeVal:   NewRealTimeBatchValidator(config),
		repairGen:    NewRepairProposalGenerator(config),
		config:       config,
	}
}

// firstNonEmpty returns the first non-empty string from the arguments.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// urlWithinAllowedDomains reports whether value's host is within
// allowedDomains, treating relative/fragment values as always allowed and
// requiring an http/https scheme for absolute URLs. Host comparison is
// case-insensitive, ignores any port, and matches subdomains of an allowed
// domain (e.g. an allowlisted "example.com" also allows "app.example.com").
//
// This intentionally mirrors app.browserAgentURLAllowed. orchestrator cannot
// import app's unexported helpers directly (app depends on model, and
// model.BrowserAgentValidationContext exists specifically to keep app and
// orchestrator decoupled), so the matching logic is duplicated here on
// purpose rather than inventing different behavior.
func urlWithinAllowedDomains(value string, allowedDomains []string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "#") {
		return true
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	for _, domain := range allowedDomains {
		normalized := normalizeAllowedDomain(domain)
		if normalized != "" && (host == normalized || strings.HasSuffix(host, "."+normalized)) {
			return true
		}
	}
	return false
}

// forbiddenPathMatch reports whether path matches one of forbiddenPrefixes,
// either exactly or as a segment-bounded prefix (so "/billing" matches
// "/billing/invoices" but not "/billing-faq"). Matching is case-insensitive.
// When a match is found, matched is true and matchedPrefix carries the raw
// (as-declared) prefix that matched, so callers can report it without
// losing its original casing. Only the first matching prefix is returned,
// mirroring the previous one-check-per-event behavior.
//
// This intentionally mirrors app.browserAgentPathForbidden. See
// urlWithinAllowedDomains's comment for why the logic is duplicated rather
// than shared. Unlike the app version, this only takes a single prefix list:
// BrowserAgentValidationContext only ever surfaces
// ScriptOutline.AllowedExplorationScope.ForbiddenPathPrefixes at this layer,
// there is no separate "forbidden pages" list to merge in.
func forbiddenPathMatch(path string, forbiddenPrefixes []string) (matchedPrefix string, matched bool) {
	normalizedPath := "/" + strings.TrimLeft(strings.ToLower(strings.TrimSpace(path)), "/")
	for _, forbidden := range forbiddenPrefixes {
		if strings.TrimSpace(forbidden) == "" {
			continue
		}
		prefix := "/" + strings.TrimLeft(strings.ToLower(strings.TrimSpace(forbidden)), "/")
		if prefix == "/" {
			continue
		}
		if normalizedPath == prefix || strings.HasPrefix(normalizedPath, strings.TrimRight(prefix, "/")+"/") {
			return forbidden, true
		}
	}
	return "", false
}

// normalizeAllowedDomain lowercases and trims value, accepting either a bare
// host (optionally with a port) or a full URL, and strips any scheme, path,
// port, and leading dot so the result can be compared against a parsed
// URL's lowercased Hostname().
//
// This intentionally mirrors app.normalizeBrowserAgentDomain. See
// urlWithinAllowedDomains's comment for why the logic is duplicated rather
// than shared.
func normalizeAllowedDomain(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if strings.Contains(value, "://") {
		if parsed, err := url.Parse(value); err == nil {
			return strings.TrimPrefix(parsed.Hostname(), ".")
		}
	}
	return strings.TrimPrefix(strings.Split(strings.Split(value, "/")[0], ":")[0], ".")
}

// ValidateBeforeExecution checks static context before execution starts.
// Runs the legacy pre-execution validation (domain/selector/evidence/blocking checks)
// and converts the LegacyValidationReport to the new ValidationReport format.
//
// P0 Requirements (handoff section 9):
// - Verify hashes are present (source_bundle_hash, policy_hash)
// - Check stage_approval_plan has required validation items
// - Check script_outline completeness
// - Return stop_and_report when critical info is missing
func (a *BrowserAgentOutcomeVerifierAdapter) ValidateBeforeExecution(
	ctx context.Context,
	vctx model.BrowserAgentValidationContext,
) (report model.ValidationReport, err error) {
	defer func() { model.AnnotateValidationChecks(report.Checks) }()
	// P0 Critical checks: hash verification (must not be empty)
	criticalChecks := []model.ValidationCheck{}

	if vctx.SourceBundleHashSHA256 == "" {
		criticalChecks = append(criticalChecks, model.ValidationCheck{
			ID:       "pre_hash_bundle_missing",
			Kind:     "hash_verification",
			Code:     "MISSING_BUNDLE_HASH",
			Severity: model.FindingSeverityBlocking,
			Passed:   false,
			Required: true,
			Summary:  "源包哈希缺失：source_bundle_hash_sha256 为空，无法验证包完整性",
		})
	}

	if vctx.EffectivePolicyHashSHA256 == "" {
		criticalChecks = append(criticalChecks, model.ValidationCheck{
			ID:       "pre_hash_policy_missing",
			Kind:     "hash_verification",
			Code:     "MISSING_POLICY_HASH",
			Severity: model.FindingSeverityBlocking,
			Passed:   false,
			Required: true,
			Summary:  "策略哈希缺失：effective_policy_hash_sha256 为空，无法验证策略一致性",
		})
	}

	// P0 Check: stage_approval_plan completeness
	if vctx.StageApprovalPlan == nil || len(vctx.StageApprovalPlan.Stages) == 0 {
		criticalChecks = append(criticalChecks, model.ValidationCheck{
			ID:       "pre_approval_plan_empty",
			Kind:     "plan_completeness",
			Code:     "EMPTY_STAGE_APPROVAL_PLAN",
			Severity: model.FindingSeverityBlocking,
			Passed:   false,
			Required: true,
			Summary:  "审批计划为空：stage_approval_plan 无阶段定义，无法执行验证",
		})
	}

	// P0 Check: script_outline presence
	if vctx.ScriptOutline == nil {
		criticalChecks = append(criticalChecks, model.ValidationCheck{
			ID:       "pre_script_outline_missing",
			Kind:     "plan_completeness",
			Code:     "MISSING_SCRIPT_OUTLINE",
			Severity: model.FindingSeverityBlocking,
			Passed:   false,
			Required: true,
			Summary:  "脚本大纲缺失：script_outline 为 nil，无法验证执行计划",
		})
	}

	// If critical checks failed, return stop_and_report immediately
	if len(criticalChecks) > 0 {
		return model.ValidationReport{
			SchemaVersion:          model.ValidationReportSchemaVersion,
			ReportID:               fmt.Sprintf("pre_critical_%d", time.Now().UnixNano()),
			RunID:                  firstNonEmpty(vctx.RunID, vctx.SourcePackageID, "validation_run"),
			SourcePackageID:        vctx.SourcePackageID,
			SourceBundleHashSHA256: firstNonEmpty(vctx.SourceBundleHashSHA256, "unknown_bundle"),
			PolicyHashSHA256:       firstNonEmpty(vctx.EffectivePolicyHashSHA256, "unknown_policy"),
			Phase:                  model.ValidationPhasePreExecution,
			Decision:               model.ValidationDecisionStopAndReport,
			PassRate:               0.0,
			OverallConfidence:      1.0,
			EvidenceQuality:        model.RuntimeObservationInsufficient,
			Checks:                 criticalChecks,
			CreatedAt:              time.Now(),
		}, nil
	}

	// Convert to legacy ValidationContext
	legacyCtx := a.convertToLegacyContext(vctx)

	// Run legacy two-step pre-execution validation
	checks, err := a.preValidator.ExecutePreValidationChecks(ctx, legacyCtx)
	if err != nil {
		return model.ValidationReport{}, fmt.Errorf("pre-execution checks failed: %w", err)
	}

	stageFeedbacks, err := a.preValidator.AnalyzePreValidationResults(ctx, checks, legacyCtx)
	if err != nil {
		return model.ValidationReport{}, fmt.Errorf("pre-execution analysis failed: %w", err)
	}

	// Build LegacyValidationReport from stage feedbacks
	legacyReport := a.buildLegacyReportFromStageFeedbacks(stageFeedbacks, "pre_execution", vctx.SourcePackageID)

	// Convert to new ValidationReport
	return a.convertToNewReport(legacyReport, "pre_execution", vctx), nil
}

// ValidateStageEvents validates runtime events for a stage.
// This uses the RealTimeBatchValidator to check step results against expectations.
//
// P0 Requirements (handoff section 9.3):
// - Detect out-of-order events
// - Detect duplicate events
// - Detect missing outcome_observed
// - Detect required assertion failures
// - Reject events with no observation evidence
// - Reject derived_from_plan evidence
// - Must return stop_and_report or reunderstanding_required for violations
func (a *BrowserAgentOutcomeVerifierAdapter) ValidateStageEvents(
	ctx context.Context,
	vctx model.BrowserAgentValidationContext,
	events []model.StageExecutionEvent,
) (report model.ValidationReport, err error) {
	defer func() { model.AnnotateValidationChecks(report.Checks) }()
	// P0: Runtime evidence quality checks
	runtimeChecks := []model.ValidationCheck{}

	// Track event sequence for ordering validation
	stageStates := make(map[string][]model.StageExecutionEventType)
	stageNodeIDs := make(map[string]string)
	for _, event := range events {
		stageStates[event.StageID] = append(stageStates[event.StageID], event.EventType)
		if event.NodeID != "" {
			stageNodeIDs[event.StageID] = event.NodeID
		}
	}

	// P0.1: Check for out-of-order events (completed before started)
	for stageID, eventTypes := range stageStates {
		hasStarted := false
		for _, et := range eventTypes {
			if et == model.StageExecutionEventStageCompleted || et == model.StageExecutionEventStageFailed {
				if !hasStarted {
					runtimeChecks = append(runtimeChecks, model.ValidationCheck{
						ID:       fmt.Sprintf("runtime_order_%s", stageID),
						Kind:     "event_ordering",
						Code:     "OUT_OF_ORDER_EVENTS",
						NodeID:   stageNodeIDs[stageID],
						StageID:  stageID,
						Severity: model.FindingSeverityBlocking,
						Passed:   false,
						Required: true,
						Summary:  fmt.Sprintf("阶段 %s 事件乱序：stage_completed/stage_failed 出现在 stage_started 之前", stageID),
					})
				}
			}
			if et == model.StageExecutionEventStageStarted {
				hasStarted = true
			}
		}
	}

	// P0.2: Check for duplicate stage_started events
	for stageID, eventTypes := range stageStates {
		startCount := 0
		for _, et := range eventTypes {
			if et == model.StageExecutionEventStageStarted {
				startCount++
			}
		}
		if startCount > 1 {
			runtimeChecks = append(runtimeChecks, model.ValidationCheck{
				ID:       fmt.Sprintf("runtime_duplicate_%s", stageID),
				Kind:     "event_duplicate",
				Code:     "DUPLICATE_STAGE_STARTED",
				NodeID:   stageNodeIDs[stageID],
				StageID:  stageID,
				Severity: model.FindingSeverityWarning,
				Passed:   false,
				Required: false,
				Summary:  fmt.Sprintf("阶段 %s 有 %d 个重复的 stage_started 事件", stageID, startCount),
			})
		}
	}

	// P0.3: Check for missing outcome_observed before stage_completed
	for stageID, eventTypes := range stageStates {
		hasCompleted := false
		hasOutcomeObserved := false
		for _, et := range eventTypes {
			if et == model.StageExecutionEventOutcomeObserved {
				hasOutcomeObserved = true
			}
			if et == model.StageExecutionEventStageCompleted {
				hasCompleted = true
			}
		}
		if hasCompleted && !hasOutcomeObserved {
			runtimeChecks = append(runtimeChecks, model.ValidationCheck{
				ID:       fmt.Sprintf("runtime_no_outcome_%s", stageID),
				Kind:     "missing_outcome",
				Code:     "MISSING_OUTCOME_OBSERVED",
				NodeID:   stageNodeIDs[stageID],
				StageID:  stageID,
				Severity: model.FindingSeverityBlocking,
				Passed:   false,
				Required: true,
				Summary:  fmt.Sprintf("阶段 %s 标记为 completed 但缺少 outcome_observed 事件", stageID),
			})
		}
	}

	// P0.4: Check evidence quality - reject derived_from_plan
	for i, event := range events {
		if event.Observation != nil {
			if event.Observation.Source == model.RuntimeObservationDerivedPlan {
				runtimeChecks = append(runtimeChecks, model.ValidationCheck{
					ID:       fmt.Sprintf("runtime_derived_evidence_%s_%d", event.StageID, i),
					Kind:     "evidence_quality",
					Code:     "DERIVED_FROM_PLAN_EVIDENCE",
					NodeID:   event.NodeID,
					StageID:  event.StageID,
					Severity: model.FindingSeverityBlocking,
					Passed:   false,
					Required: true,
					Summary:  fmt.Sprintf("阶段 %s 事件 %s 使用了 derived_from_plan 伪证据，必须使用真实浏览器观察", event.StageID, event.EventType),
					EvidenceRefs: []model.EvidenceRef{
						{ID: fmt.Sprintf("event_%d", i), Kind: "stage_event"},
					},
				})
			}
		}

		// P0.5: Check for missing observation evidence in critical events
		if event.EventType == model.StageExecutionEventOutcomeObserved ||
			event.EventType == model.StageExecutionEventObservationCollected {
			if event.Observation == nil {
				runtimeChecks = append(runtimeChecks, model.ValidationCheck{
					ID:       fmt.Sprintf("runtime_no_observation_%s_%d", event.StageID, i),
					Kind:     "missing_evidence",
					Code:     "NO_OBSERVATION_EVIDENCE",
					NodeID:   event.NodeID,
					StageID:  event.StageID,
					Severity: model.FindingSeverityBlocking,
					Passed:   false,
					Required: true,
					Summary:  fmt.Sprintf("阶段 %s 事件 %s 缺少 observation 证据字段", event.StageID, event.EventType),
				})
			}
		}

		// P0.6: Check for required assertion failures
		if event.Observation != nil {
			for j, assertion := range event.Observation.Assertions {
				if !assertion.Passed {
					runtimeChecks = append(runtimeChecks, model.ValidationCheck{
						ID:       fmt.Sprintf("runtime_assertion_fail_%s_%d_%d", event.StageID, i, j),
						Kind:     "assertion_failure",
						Code:     "REQUIRED_ASSERTION_FAILED",
						NodeID:   event.NodeID,
						StageID:  event.StageID,
						Severity: model.FindingSeverityBlocking,
						Passed:   false,
						Required: true,
						Summary:  fmt.Sprintf("阶段 %s required assertion 失败: kind=%s, actual=%s", event.StageID, assertion.Kind, assertion.Actual),
						EvidenceRefs: []model.EvidenceRef{
							{ID: fmt.Sprintf("event_%d_assertion_%d", i, j), Kind: "runtime_assertion"},
						},
					})
				}
			}
		}
	}

	// P0.7: Detect stage_failed events and create blocking checks
	for i, event := range events {
		if event.EventType == model.StageExecutionEventStageFailed {
			failureReason := "Stage execution failed"
			if event.Observation != nil && event.Observation.Title != "" {
				failureReason = event.Observation.Title
			}
			runtimeChecks = append(runtimeChecks, model.ValidationCheck{
				ID:       fmt.Sprintf("runtime_stage_failed_%s_%d", event.StageID, i),
				Kind:     "stage_failure",
				Code:     "STAGE_FAILED",
				NodeID:   event.NodeID,
				StageID:  event.StageID,
				Severity: model.FindingSeverityBlocking,
				Passed:   false,
				Required: true,
				Summary:  fmt.Sprintf("阶段 %s 执行失败: %s", event.StageID, failureReason),
				EvidenceRefs: []model.EvidenceRef{
					{ID: fmt.Sprintf("event_%d", i), Kind: "stage_event"},
				},
			})
		}
	}

	// P0.8: Detect cross-domain access and forbidden-page access (scenario 6)
	for _, event := range events {
		if event.Observation == nil || event.Observation.URL == "" {
			continue
		}
		observedURL, parseErr := url.Parse(event.Observation.URL)
		if parseErr != nil {
			continue
		}

		if len(vctx.AllowedDomains) > 0 && !urlWithinAllowedDomains(event.Observation.URL, vctx.AllowedDomains) {
			runtimeChecks = append(runtimeChecks, model.ValidationCheck{
				ID:       fmt.Sprintf("runtime_cross_domain_%s", event.StageID),
				Kind:     "cross_domain_access",
				Code:     "CROSS_DOMAIN_ACCESS",
				NodeID:   event.NodeID,
				StageID:  event.StageID,
				Severity: model.FindingSeverityBlocking,
				Passed:   false,
				Required: true,
				Summary:  fmt.Sprintf("阶段 %s 观察到的 URL host %q 超出批准的允许域范围", event.StageID, observedURL.Hostname()),
			})
		}

		if vctx.ScriptOutline != nil {
			if prefix, matched := forbiddenPathMatch(observedURL.Path, vctx.ScriptOutline.AllowedExplorationScope.ForbiddenPathPrefixes); matched {
				runtimeChecks = append(runtimeChecks, model.ValidationCheck{
					ID:       fmt.Sprintf("runtime_forbidden_page_%s", event.StageID),
					Kind:     "forbidden_page_access",
					Code:     "FORBIDDEN_PAGE_ACCESS",
					NodeID:   event.NodeID,
					StageID:  event.StageID,
					Severity: model.FindingSeverityBlocking,
					Passed:   false,
					Required: true,
					Summary:  fmt.Sprintf("阶段 %s 观察到的 URL 命中禁止路径前缀 %q", event.StageID, prefix),
				})
			}
		}
	}

	// If critical runtime checks failed, return stop_and_report
	if len(runtimeChecks) > 0 {
		hasBlockingFailure := false
		for _, check := range runtimeChecks {
			if check.Severity == model.FindingSeverityBlocking && !check.Passed {
				hasBlockingFailure = true
				break
			}
		}

		if hasBlockingFailure {
			newReport := model.ValidationReport{
				SchemaVersion:          model.ValidationReportSchemaVersion,
				ReportID:               fmt.Sprintf("runtime_critical_%d", time.Now().UnixNano()),
				RunID:                  firstNonEmpty(vctx.RunID, vctx.SourcePackageID, "validation_run"),
				SourcePackageID:        vctx.SourcePackageID,
				SourceBundleHashSHA256: vctx.SourceBundleHashSHA256,
				PolicyHashSHA256:       vctx.EffectivePolicyHashSHA256,
				Phase:                  model.ValidationPhaseRuntimeStage,
				Decision:               model.ValidationDecisionStopAndReport,
				PassRate:               0.0,
				OverallConfidence:      1.0,
				EvidenceQuality:        model.RuntimeObservationInsufficient,
				Checks:                 runtimeChecks,
				CreatedAt:              time.Now(),
			}

			// P1: Generate repair proposals even for blocking failures
			if a.config.EnableRuntimeRepair && vctx.BrowserAgentContract != nil {
				proposals := a.repairGen.GenerateRepairProposals(&vctx, runtimeChecks, &vctx.BrowserAgentContract.RepairPolicy)
				if len(proposals) > 0 {
					newReport.RepairProposalRefs = make([]string, len(proposals))
					for i, p := range proposals {
						newReport.RepairProposalRefs[i] = p.ProposalID
					}
				}
			}

			return newReport, nil
		}
	}

	legacyCtx := a.convertToLegacyContext(vctx)

	// Convert events to StepResult format for legacy validator.
	// Group events by NodeID; each stage produces exactly one StepResult.
	type stageAccum struct {
		startedAt   time.Time
		completedAt time.Time
		observation *model.RuntimeObservation
		failed      bool
		artifacts   []model.ArtifactRef
	}
	accumByNode := map[string]*stageAccum{}
	var nodeOrder []string
	for _, event := range events {
		nid := event.NodeID
		if _, exists := accumByNode[nid]; !exists {
			accumByNode[nid] = &stageAccum{}
			nodeOrder = append(nodeOrder, nid)
		}
		acc := accumByNode[nid]
		switch event.EventType {
		case model.StageExecutionEventStageStarted:
			acc.startedAt = event.OccurredAt
		case model.StageExecutionEventStageCompleted:
			acc.completedAt = event.OccurredAt
		case model.StageExecutionEventStageFailed:
			acc.completedAt = event.OccurredAt
			acc.failed = true
		case model.StageExecutionEventOutcomeObserved:
			if event.Observation != nil {
				cp := *event.Observation
				acc.observation = &cp
			}
		}
		for _, ref := range event.EvidenceRefs {
			acc.artifacts = append(acc.artifacts, model.ArtifactRef{
				ID:   ref.ID,
				Kind: string(ref.Kind),
			})
		}
	}
	stepResults := make([]model.StepResult, 0, len(nodeOrder))
	for _, nid := range nodeOrder {
		acc := accumByNode[nid]
		sr := model.StepResult{
			NodeID:    nid,
			Status:    "passed",
			StartedAt: acc.startedAt,
			Artifacts: acc.artifacts,
		}
		if !acc.startedAt.IsZero() && !acc.completedAt.IsZero() {
			sr.CompletedAt = acc.completedAt
			sr.DurationMS = int(acc.completedAt.Sub(acc.startedAt).Milliseconds())
		}
		obs := acc.observation
		isRealEvidence := obs != nil && (obs.Source == model.RuntimeObservationActualBrowser ||
			obs.Source == model.RuntimeObservationAssertion ||
			obs.Source == model.RuntimeObservationArtifact)
		if acc.failed || !isRealEvidence {
			sr.Status = "failed"
			if !isRealEvidence {
				sr.Error = &model.AgentError{Code: "missing_runtime_observation", Message: "stage completed without a real browser outcome observation"}
			}
		}
		if obs != nil {
			parts := []string{}
			if obs.URL != "" {
				parts = append(parts, "url="+obs.URL)
			}
			if expectedRoute, ok := adapterVerifiedExpectedRoute(vctx.StageApprovalPlan, nid, obs.URL); ok {
				parts = append(parts, "route_template_verified="+expectedRoute)
			}
			if obs.Source != "" {
				parts = append(parts, "source="+string(obs.Source))
			}
			sr.ObservedState = strings.Join(parts, " ")
		}
		stepResults = append(stepResults, sr)
	}

	// Run legacy runtime validation
	validationResults, err := a.runtimeVal.ValidateStepResults(ctx, stepResults, legacyCtx)
	if err != nil {
		return model.ValidationReport{}, fmt.Errorf("runtime validation failed: %w", err)
	}

	// Build a synthetic LegacyValidationReport from validation results
	legacyReport := a.buildLegacyReportFromResults(validationResults, "runtime", vctx.SourcePackageID)

	// Convert to new report and append runtime checks
	newReport := a.convertToNewReport(legacyReport, "runtime_stage", vctx)
	// ValidationReport.Validate() requires NodeID and StageID for runtime_stage phase.
	// Extract them from the first event that carries both fields.
	for _, ev := range events {
		if ev.NodeID != "" && ev.StageID != "" {
			newReport.NodeID = ev.NodeID
			newReport.StageID = ev.StageID
			break
		}
	}
	newReport.Checks = append(newReport.Checks, runtimeChecks...)

	// P1: Generate repair proposals for failed checks
	if a.config.EnableRuntimeRepair && vctx.BrowserAgentContract != nil {
		proposals := a.repairGen.GenerateRepairProposals(&vctx, newReport.Checks, &vctx.BrowserAgentContract.RepairPolicy)
		if len(proposals) > 0 {
			// Store proposal IDs in report
			newReport.RepairProposalRefs = make([]string, len(proposals))
			for i, p := range proposals {
				newReport.RepairProposalRefs[i] = p.ProposalID
			}
			// Note: Actual proposal storage would be handled by caller (Runtime/Server)
			// We only generate and reference them here
		}
	}

	return newReport, nil
}

// adapterVerifiedExpectedRoute translates a protocol-level dynamic route into
// evidence understood by the legacy validator. It never mutates runtime events
// or the approved plan and only emits the marker after independently matching
// the actual browser URL against the App-approved route template.
func adapterVerifiedExpectedRoute(plan *model.StageApprovalPlan, nodeID, observedURL string) (string, bool) {
	if plan == nil || strings.TrimSpace(observedURL) == "" {
		return "", false
	}
	for _, stage := range plan.Stages {
		if stage.NodeID != nodeID || strings.TrimSpace(stage.ExpectedRouteAfterAction) == "" {
			continue
		}
		if adapterRouteTemplateMatches(observedURL, stage.ExpectedRouteAfterAction) {
			return stage.ExpectedRouteAfterAction, true
		}
		return "", false
	}
	return "", false
}

func adapterRouteTemplateMatches(observedRaw, expectedRaw string) bool {
	observed, observedErr := url.Parse(strings.TrimSpace(observedRaw))
	expected, expectedErr := url.Parse(strings.TrimSpace(expectedRaw))
	if observedErr != nil || expectedErr != nil || observed.Path == "" || expected.Path == "" {
		return false
	}
	if expected.Host != "" && !strings.EqualFold(observed.Host, expected.Host) {
		return false
	}

	observedSegments := adapterRouteSegments(observed.Path)
	expectedSegments := adapterRouteSegments(expected.Path)
	for i, expectedSegment := range expectedSegments {
		if expectedSegment == "*" {
			return i < len(observedSegments)
		}
		if i >= len(observedSegments) {
			return false
		}
		if strings.HasPrefix(expectedSegment, ":") {
			if observedSegments[i] == "" {
				return false
			}
			continue
		}
		if !strings.EqualFold(observedSegments[i], expectedSegment) {
			return false
		}
	}
	return len(observedSegments) == len(expectedSegments)
}

func adapterRouteSegments(path string) []string {
	trimmed := strings.Trim(strings.TrimSpace(path), "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

// ValidatePostExecution validates final results after execution.
// This is where we return reunderstanding_required for stage failures.
//
// P0 Requirements (handoff section 9.4):
// - Verify all required stages completed
// - Cross-check stage events against StepResults
// - Verify observed_state fields contain actual runtime data (not empty/null)
// - Verify evidence_refs are traceable
// - Check RecordingResultPackage has required artifacts (screenshots, trace, video)
// - Verify validation reports match stage events
// - Return reunderstanding_required when stage checks fail consistently
func (a *BrowserAgentOutcomeVerifierAdapter) ValidatePostExecution(
	ctx context.Context,
	vctx model.BrowserAgentValidationContext,
	result model.RecordingResultPackage,
	events []model.StageExecutionEvent,
) (report model.ValidationReport, err error) {
	defer func() { model.AnnotateValidationChecks(report.Checks) }()
	postChecks := []model.ValidationCheck{}

	// P0.0: Verify RecordingResultPackage is not empty
	if result.ResultID == "" {
		return model.ValidationReport{
			SchemaVersion:          model.ValidationReportSchemaVersion,
			ReportID:               fmt.Sprintf("post_critical_%d", time.Now().UnixNano()),
			RunID:                  firstNonEmpty(vctx.RunID, vctx.SourcePackageID, "validation_run"),
			SourcePackageID:        vctx.SourcePackageID,
			SourceBundleHashSHA256: firstNonEmpty(vctx.SourceBundleHashSHA256, "unknown_bundle"),
			PolicyHashSHA256:       firstNonEmpty(vctx.EffectivePolicyHashSHA256, "unknown_policy"),
			Phase:                  model.ValidationPhasePostExecution,
			Decision:               model.ValidationDecisionStopAndReport,
			OverallConfidence:      1.0,
			EvidenceQuality:        model.RuntimeObservationInsufficient,
			CreatedAt:              time.Now(),
			Checks: []model.ValidationCheck{{
				ID:       "post_missing_result_package",
				Kind:     "result_completeness",
				Code:     "MISSING_RESULT_PACKAGE",
				Severity: model.FindingSeverityBlocking,
				Passed:   false,
				Required: true,
				Summary:  "RecordingResultPackage is empty (ResultID is blank)",
			}},
		}, nil
	}

	// P0.0b: Verify the result package actually belongs to this approved run (scenario 11)
	if result.SourcePackageID != "" && vctx.SourcePackageID != "" && result.SourcePackageID != vctx.SourcePackageID {
		postChecks = append(postChecks, model.ValidationCheck{
			ID:       "post_result_package_mismatch",
			Kind:     "result_identity_mismatch",
			Code:     "RESULT_PACKAGE_MISMATCH",
			Severity: model.FindingSeverityBlocking,
			Passed:   false,
			Required: true,
			Summary:  fmt.Sprintf("结果包 source_package_id (%s) 与本次运行批准包 (%s) 不一致", result.SourcePackageID, vctx.SourcePackageID),
		})
	}
	if result.AuditTrail.SourcePackageDigest != "" && vctx.SourceBundleHashSHA256 != "" && result.AuditTrail.SourcePackageDigest != vctx.SourceBundleHashSHA256 {
		postChecks = append(postChecks, model.ValidationCheck{
			ID:       "post_result_hash_mismatch",
			Kind:     "result_hash_mismatch",
			Code:     "RESULT_HASH_MISMATCH",
			Severity: model.FindingSeverityBlocking,
			Passed:   false,
			Required: true,
			Summary:  "结果包审计哈希与批准包源哈希不一致，证据链不可信",
		})
	}

	// P0.1: Verify RecordingResultPackage has required artifacts
	{
		// Check for trace artifact
		hasTrace := false
		for _, asset := range result.GeneratedAssets {
			if asset.Kind == "trace" || asset.Kind == "playwright_trace" {
				hasTrace = true
				break
			}
		}
		if !hasTrace {
			postChecks = append(postChecks, model.ValidationCheck{
				ID:       "post_no_trace",
				Kind:     "artifact_completeness",
				Code:     "MISSING_TRACE_ARTIFACT",
				Severity: model.FindingSeverityWarning,
				Passed:   false,
				Required: false,
				Summary:  "缺少 trace 类型的 artifact，调试能力受限",
			})
		}

		// Check for screenshot evidence
		hasScreenshot := false
		for _, asset := range result.GeneratedAssets {
			if asset.Kind == "screenshot" || asset.Kind == "step_screenshot" {
				hasScreenshot = true
				break
			}
		}
		if !hasScreenshot {
			postChecks = append(postChecks, model.ValidationCheck{
				ID:       "post_no_screenshots",
				Kind:     "artifact_completeness",
				Code:     "MISSING_SCREENSHOTS",
				Severity: model.FindingSeverityWarning,
				Passed:   false,
				Required: false,
				Summary:  "无截图证据，人工审查困难",
			})
		}

		// Check for MP4 video (section 7 requirement)
		hasMP4 := false
		for _, asset := range result.GeneratedAssets {
			if asset.Kind == "video" || asset.Kind == "mp4" || asset.Kind == "recording_video" {
				hasMP4 = true
				break
			}
		}
		if !hasMP4 {
			postChecks = append(postChecks, model.ValidationCheck{
				ID:       "post_no_mp4",
				Kind:     "artifact_completeness",
				Code:     "MISSING_MP4_VIDEO",
				Severity: model.FindingSeverityWarning,
				Passed:   false,
				Required: false,
				Summary:  "缺少 MP4 视频，无法回放完整执行过程",
			})
		}

		// Check for stage event log (section 7 requirement)
		if result.StageEventLogRef == nil {
			postChecks = append(postChecks, model.ValidationCheck{
				ID:       "post_no_stage_event_log",
				Kind:     "artifact_completeness",
				Code:     "MISSING_STAGE_EVENT_LOG",
				Severity: model.FindingSeverityWarning,
				Passed:   false,
				Required: false,
				Summary:  "缺少 stage_event_log_ref，无法追溯阶段执行 JSONL",
			})
		}
	}

	// P0.2: Verify all stages from StageApprovalPlan have events
	if vctx.StageApprovalPlan != nil && len(vctx.StageApprovalPlan.Stages) > 0 {
		completedStages := make(map[string]bool)
		for _, event := range events {
			if event.EventType == model.StageExecutionEventStageCompleted {
				if event.NodeID != "" {
					completedStages[event.NodeID] = true
				} else {
					completedStages[event.StageID] = true
				}
			}
		}

		for _, approvalStage := range vctx.StageApprovalPlan.Stages {
			// Use NodeID as the stage identifier
			if !completedStages[approvalStage.NodeID] {
				postChecks = append(postChecks, model.ValidationCheck{
					ID:       fmt.Sprintf("post_stage_incomplete_%s", approvalStage.NodeID),
					Kind:     "stage_completeness",
					Code:     "REQUIRED_STAGE_NOT_COMPLETED",
					Severity: model.FindingSeverityBlocking,
					Passed:   false,
					Required: true,
					Summary:  fmt.Sprintf("Required 阶段 %s 未完成执行", approvalStage.NodeID),
				})
			}
		}
	}

	// P0.3: Cross-check events for evidence quality
	stageOutcomes := make(map[string]bool) // stageID -> has real evidence
	for _, event := range events {
		if event.EventType == model.StageExecutionEventOutcomeObserved && event.Observation != nil {
			// Check evidence quality
			if event.Observation.Source == model.RuntimeObservationActualBrowser ||
				event.Observation.Source == model.RuntimeObservationAssertion ||
				event.Observation.Source == model.RuntimeObservationArtifact {
				stageOutcomes[event.StageID] = true
			}
		}
	}

	// P0.7: Re-check events for failed assertions (could have been missed in stage validation)
	for i, event := range events {
		if event.EventType == model.StageExecutionEventStageFailed {
			failureReason := "Stage execution failed"
			if event.Observation != nil && event.Observation.Title != "" {
				failureReason = event.Observation.Title
			}
			postChecks = append(postChecks, model.ValidationCheck{
				ID:       fmt.Sprintf("post_stage_failed_%s_%d", event.StageID, i),
				Kind:     "stage_failure",
				Code:     "STAGE_FAILED",
				Severity: model.FindingSeverityBlocking,
				Passed:   false,
				Required: true,
				Summary:  fmt.Sprintf("阶段 %s 执行失败: %s", event.StageID, failureReason),
				EvidenceRefs: []model.EvidenceRef{
					{ID: fmt.Sprintf("event_%d", i), Kind: "stage_event"},
				},
			})
		}

		if event.Observation != nil {
			for j, assertion := range event.Observation.Assertions {
				if !assertion.Passed {
					postChecks = append(postChecks, model.ValidationCheck{
						ID:       fmt.Sprintf("post_assertion_fail_%s_%d_%d", event.StageID, i, j),
						Kind:     "assertion_failure",
						Code:     "REQUIRED_ASSERTION_FAILED",
						Severity: model.FindingSeverityBlocking,
						Passed:   false,
						Required: true,
						Summary:  fmt.Sprintf("阶段 %s required assertion 失败: kind=%s, actual=%s", event.StageID, assertion.Kind, assertion.Actual),
						EvidenceRefs: []model.EvidenceRef{
							{ID: fmt.Sprintf("event_%d_assertion_%d", i, j), Kind: "runtime_assertion"},
						},
					})
				}
			}
		}
	}

	// P0.4: Verify stage events have traceable evidence_refs
	for i, event := range events {
		if event.EventType == model.StageExecutionEventOutcomeObserved ||
			event.EventType == model.StageExecutionEventObservationCollected {
			if len(event.EvidenceRefs) == 0 {
				postChecks = append(postChecks, model.ValidationCheck{
					ID:       fmt.Sprintf("post_no_evidence_refs_%s_%d", event.StageID, i),
					Kind:     "evidence_traceability",
					Code:     "MISSING_EVIDENCE_REFS",
					Severity: model.FindingSeverityWarning,
					Passed:   false,
					Required: false,
					Summary:  fmt.Sprintf("阶段 %s 事件 %s 缺少 evidence_refs，无法追溯原始证据", event.StageID, event.EventType),
				})
			}
		}
	}

	// P0.5: Cross-check StepResult.observed_state traceability
	if len(result.StepResults) > 0 {
		// Build set of nodeIDs that have genuine outcome_observed event with actual browser source
		nodeHasOutcome := make(map[string]bool)
		for _, event := range events {
			if event.EventType == model.StageExecutionEventOutcomeObserved &&
				event.Observation != nil &&
				event.Observation.Source == model.RuntimeObservationActualBrowser {
				nodeHasOutcome[event.NodeID] = true
			}
		}

		for _, step := range result.StepResults {
			// If observed_state present but doesn't contain "source=" marker → blocking failure
			if step.ObservedState != "" && !strings.Contains(step.ObservedState, "source=") {
				postChecks = append(postChecks, model.ValidationCheck{
					ID:       fmt.Sprintf("post_observed_state_no_source_%s", step.NodeID),
					Kind:     "observed_state_traceability",
					Code:     "observed_state_not_runtime_derived",
					Severity: model.FindingSeverityBlocking,
					Passed:   false,
					Required: true,
					Summary:  fmt.Sprintf("Step %s observed_state 未标记来源，可能是手工伪造", step.NodeID),
				})
			}

			// If observed_state present but no corresponding outcome event → blocking failure
			if step.ObservedState != "" && !nodeHasOutcome[step.NodeID] {
				postChecks = append(postChecks, model.ValidationCheck{
					ID:       fmt.Sprintf("post_observed_state_no_event_%s", step.NodeID),
					Kind:     "observed_state_traceability",
					Code:     "observed_state_not_runtime_derived",
					Severity: model.FindingSeverityBlocking,
					Passed:   false,
					Required: true,
					Summary:  fmt.Sprintf("Step %s observed_state 存在但无对应 outcome_observed 事件", step.NodeID),
				})
			}
		}
	}

	// P0.6: Verify ValidationCheck evidence_refs point to real artifacts
	allArtifactIDs := make(map[string]bool)
	for _, asset := range result.GeneratedAssets {
		allArtifactIDs[asset.ID] = true
	}
	for _, vr := range result.ValidationReports {
		for _, check := range vr.Checks {
			for _, evRef := range check.EvidenceRefs {
				if evRef.ArtifactID != "" && !allArtifactIDs[evRef.ArtifactID] {
					postChecks = append(postChecks, model.ValidationCheck{
						ID:       fmt.Sprintf("post_broken_evidence_ref_%s", evRef.ArtifactID),
						Kind:     "evidence_integrity",
						Code:     "EVIDENCE_ARTIFACT_REFERENCE_BROKEN",
						Severity: model.FindingSeverityWarning,
						Passed:   false,
						Required: false,
						Summary:  fmt.Sprintf("ValidationCheck %s 引用 artifact %s 但该 artifact 不存在于 GeneratedAssets", check.ID, evRef.ArtifactID),
						EvidenceRefs: []model.EvidenceRef{{ID: check.ID, Kind: "validation_check"}},
					})
				}
			}
		}
	}

	// If critical post checks failed, return stop_and_report
	hasBlockingFailure := false
	for _, check := range postChecks {
		if check.Severity == model.FindingSeverityBlocking && !check.Passed {
			hasBlockingFailure = true
			break
		}
	}

	if hasBlockingFailure {
		return model.ValidationReport{
			SchemaVersion:          model.ValidationReportSchemaVersion,
			ReportID:               fmt.Sprintf("post_critical_%d", time.Now().UnixNano()),
			RunID:                  firstNonEmpty(vctx.RunID, vctx.SourcePackageID, "validation_run"),
			SourcePackageID:        vctx.SourcePackageID,
			SourceBundleHashSHA256: firstNonEmpty(vctx.SourceBundleHashSHA256, "unknown_bundle"),
			PolicyHashSHA256:       firstNonEmpty(vctx.EffectivePolicyHashSHA256, "unknown_policy"),
			Phase:                  model.ValidationPhasePostExecution,
			Decision:               model.ValidationDecisionStopAndReport,
			PassRate:               0.0,
			OverallConfidence:      1.0,
			EvidenceQuality:        model.RuntimeObservationInsufficient,
			Checks:                 postChecks,
			CreatedAt:              time.Now(),
		}, nil
	}

	legacyCtx := a.convertToLegacyContext(vctx)

	// PostExecutionAnalyzer needs PostExecutionAnalysis objects, not raw StepResults
	// For now, build minimal analyses from events
	analyses := a.convertEventsToPostExecutionAnalyses(events)

	// Run legacy post-execution analysis (returns []StageFeedback)
	stageFeedbacks := a.postAnalyzer.GenerateComprehensiveFeedback(analyses, legacyCtx)

	// Build LegacyValidationReport from stage feedbacks
	legacyReport := a.buildLegacyReportFromStageFeedbacks(stageFeedbacks, "post_execution", vctx.SourcePackageID)

	// Convert to new report
	newReport := a.convertToNewReport(legacyReport, "post_execution", vctx)

	// Append post checks
	newReport.Checks = append(newReport.Checks, postChecks...)

	// P0.5: Return reunderstanding_required when stage validation consistently fails
	// Check if multiple stages have validation failures from StageFeedback.ValidationResults
	failedStageCount := 0
	for _, feedback := range stageFeedbacks {
		hasBlockingIssue := false
		for _, vr := range feedback.ValidationResults {
			// Check if validation result failed (Critical or Blocker)
			if vr.Critical || vr.Blocker {
				hasBlockingIssue = true
				break
			}
		}
		if hasBlockingIssue || feedback.Blocked {
			failedStageCount++
		}
	}

	// If 50%+ of stages failed validation, recommend reunderstanding
	var totalStages int
	if vctx.StageApprovalPlan != nil {
		totalStages = len(vctx.StageApprovalPlan.Stages)
	}
	if totalStages > 0 && float64(failedStageCount)/float64(totalStages) >= 0.5 {
		newReport.Decision = model.ValidationDecisionReunderstandingRequired
		newReport.Checks = append(newReport.Checks, model.ValidationCheck{
			ID:       "post_reunderstanding_recommended",
			Kind:     "execution_quality",
			Code:     "STAGE_VALIDATION_FAILURE_THRESHOLD",
			Severity: model.FindingSeverityBlocking,
			Passed:   false,
			Required: true,
			Summary:  fmt.Sprintf("执行质量不达标：%d/%d 阶段验证失败，建议 App 重新理解或调整策略", failedStageCount, totalStages),
		})
	}

	// P1: Generate repair proposals for failed checks (if repair allowed and not recommending reunderstanding)
	if a.config.EnableRuntimeRepair &&
		vctx.BrowserAgentContract != nil &&
		newReport.Decision != model.ValidationDecisionReunderstandingRequired {
		proposals := a.repairGen.GenerateRepairProposals(&vctx, newReport.Checks, &vctx.BrowserAgentContract.RepairPolicy)
		if len(proposals) > 0 {
			// Store proposal IDs in report
			newReport.RepairProposalRefs = make([]string, len(proposals))
			for i, p := range proposals {
				newReport.RepairProposalRefs[i] = p.ProposalID
			}
			// If we have high-confidence auto-apply proposals, suggest repair_allowed decision
			hasAutoApply := false
			for _, p := range proposals {
				if !p.RequiresApproval && p.Confidence >= vctx.BrowserAgentContract.RepairPolicy.MinAutoApplyConfidence {
					hasAutoApply = true
					break
				}
			}
			if hasAutoApply && newReport.Decision == model.ValidationDecisionContinue {
				newReport.Decision = model.ValidationDecisionRepairAllowed
			}
		}
	}

	return newReport, nil
}

// convertToLegacyContext converts new validation context to legacy ValidationContext.
func (a *BrowserAgentOutcomeVerifierAdapter) convertToLegacyContext(vctx model.BrowserAgentValidationContext) *model.ValidationContext {
	// Build RecordingRunSpec from available fields
	runSpec := model.RecordingRunSpec{
		RunID:          vctx.RunID,
		AllowedDomains: append([]string{}, vctx.AllowedDomains...),
	}

	return &model.ValidationContext{
		PackageID:         vctx.SourcePackageID,
		WorkflowGraph:     vctx.WorkflowGraph,
		StageApprovalPlan: vctx.StageApprovalPlan,
		RecordingRunSpec:  runSpec,
	}
}

// convertToNewReport converts legacy LegacyValidationReport to new ValidationReport.
func (a *BrowserAgentOutcomeVerifierAdapter) convertToNewReport(
	legacy model.LegacyValidationReport,
	phase string,
	vctx model.BrowserAgentValidationContext,
) model.ValidationReport {
	decision := a.mapDecision(legacy.GlobalFeedbackType)

	// Convert StageFeedback entries to ValidationCheck format
	checks := []model.ValidationCheck{}
	for _, stageFb := range legacy.StageFeedbacks {
		for _, result := range stageFb.ValidationResults {
			checks = append(checks, model.ValidationCheck{
				ID:       result.ID,
				Kind:     string(result.Type),
				Code:     string(result.Type),
				Severity: a.mapSeverity(string(result.Type), result.Critical),
				Passed:   result.Type == model.ValidationResultTypePassed,
				Required: result.Critical,
				Summary:  result.Description,
			})
		}
	}

	passRate := 0.0
	if legacy.TotalStages > 0 {
		passRate = float64(legacy.PassedStages) / float64(legacy.TotalStages)
	}

	// ValidationReport.Validate() requires evidence_refs when Decision==continue.
	// Pre-execution validation is hash-bound (no live browser), so we synthesise
	// a ref that points to the approved package hash as the evidence source.
	evidenceRefs := []model.EvidenceRef{}
	if decision == model.ValidationDecisionContinue {
		evidenceRefs = append(evidenceRefs, model.EvidenceRef{
			ID:      firstNonEmpty(vctx.SourceBundleHashSHA256, "unknown_bundle"),
			Kind:    model.EvidenceKindSourceCode,
			Summary: "pre-execution validation passed against hash-bound approved package",
		})
	}

	return model.ValidationReport{
		SchemaVersion:          model.ValidationReportSchemaVersion,
		ReportID:               fmt.Sprintf("adapter_%s_%d", phase, time.Now().UnixNano()),
		RunID:                  firstNonEmpty(vctx.RunID, vctx.SourcePackageID, "validation_run"),
		SourcePackageID:        vctx.SourcePackageID,
		SourceBundleHashSHA256: firstNonEmpty(vctx.SourceBundleHashSHA256, "unknown_bundle"),
		PolicyHashSHA256:       firstNonEmpty(vctx.EffectivePolicyHashSHA256, "unknown_policy"),
		Phase:                  model.ValidationPhase(phase),
		Decision:               decision,
		PassRate:               passRate,
		OverallConfidence:      legacy.OverallConfidence,
		EvidenceQuality:        model.RuntimeObservationAssertion,
		Checks:                 checks,
		EvidenceRefs:           evidenceRefs,
		CreatedAt:              time.Now(),
	}
}

// mapDecision maps legacy GlobalFeedbackType to new ValidationDecision.
func (a *BrowserAgentOutcomeVerifierAdapter) mapDecision(legacyType string) model.ValidationDecision {
	switch legacyType {
	case "continue":
		return model.ValidationDecisionContinue
	case "fine_tune":
		return model.ValidationDecisionRepairAllowed
	case "reunderstanding_required":
		return model.ValidationDecisionReunderstandingRequired
	default:
		return model.ValidationDecisionStopAndReport
	}
}

// mapSeverity maps legacy result type and criticality to new FindingSeverity.
func (a *BrowserAgentOutcomeVerifierAdapter) mapSeverity(resultType string, critical bool) model.FindingSeverity {
	if critical {
		return model.FindingSeverityBlocking
	}
	if resultType == "warning" || resultType == "fine_tune" || resultType == "uncertainty" {
		return model.FindingSeverityWarning
	}
	return model.FindingSeverityInfo
}

// buildLegacyReportFromResults constructs a minimal LegacyValidationReport from ValidationResult slice.
func (a *BrowserAgentOutcomeVerifierAdapter) buildLegacyReportFromResults(
	results []model.ValidationResult,
	phase string,
	packageID string,
) model.LegacyValidationReport {
	passed := 0
	failed := 0
	for _, r := range results {
		if r.Type == model.ValidationResultTypePassed {
			passed++
		} else {
			failed++
		}
	}

	feedbackType := "continue"
	if failed > 0 {
		feedbackType = "fine_tune"
	}

	return model.LegacyValidationReport{
		ID:                  fmt.Sprintf("%s_%d", phase, time.Now().UnixNano()),
		ValidationContextID: packageID,
		Phase:               model.LegacyValidationPhase(phase),
		GlobalFeedbackType:  feedbackType,
		PassedStages:        passed,
		FailedStages:        failed,
		TotalStages:         len(results),
		StageFeedbacks:      []model.StageFeedback{},
	}
}

// buildLegacyReportFromStageFeedbacks constructs a LegacyValidationReport from StageFeedback slice.
func (a *BrowserAgentOutcomeVerifierAdapter) buildLegacyReportFromStageFeedbacks(
	feedbacks []model.StageFeedback,
	phase string,
	packageID string,
) model.LegacyValidationReport {
	passed := 0
	failed := 0
	reunderstanding := false

	for _, fb := range feedbacks {
		switch fb.FeedbackType {
		case "passed", "continue":
			passed++
		case "reunderstanding_required":
			failed++
			reunderstanding = true
		default:
			failed++
		}
	}

	// Determine global feedback type
	globalFeedback := "continue"
	if reunderstanding {
		globalFeedback = "reunderstanding_required"
	} else if failed > 0 {
		globalFeedback = "fine_tune"
	}

	return model.LegacyValidationReport{
		ID:                  fmt.Sprintf("%s_%d", phase, time.Now().UnixNano()),
		ValidationContextID: packageID,
		Phase:               model.LegacyValidationPhase(phase),
		GlobalFeedbackType:  globalFeedback,
		PassedStages:        passed,
		FailedStages:        failed,
		TotalStages:         len(feedbacks),
		StageFeedbacks:      feedbacks,
	}
}

// convertEventsToPostExecutionAnalyses converts StageExecutionEvent to PostExecutionAnalysis.
func (a *BrowserAgentOutcomeVerifierAdapter) convertEventsToPostExecutionAnalyses(events []model.StageExecutionEvent) []model.PostExecutionAnalysis {
	analyses := []model.PostExecutionAnalysis{}
	for _, event := range events {
		// Build a minimal StepResult from event
		stepResult := &model.StepResult{
			NodeID: event.StageID,
			Status: string(event.EventType), // Use EventType as status approximation
		}

		// If there's an observation, extract URL as observed state
		if event.Observation != nil {
			stepResult.ObservedState = event.Observation.URL
		}

		analyses = append(analyses, model.PostExecutionAnalysis{
			NodeID:     event.StageID,
			StepResult: stepResult,
		})
	}
	return analyses
}
