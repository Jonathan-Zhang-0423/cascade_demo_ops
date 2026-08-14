package orchestrator

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

// PreExecutionValidator runs Phase 1 (pre-execution) stage validation.
// It inspects the StageApprovalPlan carried in a ValidationContext and emits
// deterministic PreValidationCheck records, then rolls them up into
// StageFeedback. It performs no network, LLM, or browser-sidecar calls; every
// decision is computed from the in-memory structs.
type PreExecutionValidator struct {
	config *model.ValidationConfig
}

// NewPreExecutionValidator stores the validation config. A nil cfg is replaced
// with the package default (DefaultValidationConfig), which sets
// PassRateThreshold=0.9 and ConfidenceThreshold=0.5. Threshold accessors here
// additionally fall back to 0.9/0.5 if the config or its fields are zero.
func NewPreExecutionValidator(cfg *model.ValidationConfig) *PreExecutionValidator {
	if cfg == nil {
		cfg = DefaultValidationConfig()
	}
	return &PreExecutionValidator{config: cfg}
}

// confidenceThreshold returns the configured confidence threshold, defaulting
// to 0.5 when the config or the field is unset.
func (pev *PreExecutionValidator) confidenceThreshold() float64 {
	if pev == nil || pev.config == nil || pev.config.ConfidenceThreshold <= 0 {
		return 0.5
	}
	return pev.config.ConfidenceThreshold
}

// ExecutePreValidationChecks runs the pre-execution check categories across
// every stage in validationContext.StageApprovalPlan.Stages and emits one
// PreValidationCheck per check. Categories: "domain", "selector", "evidence",
// "blocking_uncertainty", plus a conditional "confidence" risk check.
//
// It is defensive against nil: a nil context or plan yields an empty (non-nil)
// slice and a logged note rather than a panic.
func (pev *PreExecutionValidator) ExecutePreValidationChecks(
	ctx context.Context,
	validationContext *model.ValidationContext,
) ([]model.PreValidationCheck, error) {
	checks := []model.PreValidationCheck{}
	if err := ctx.Err(); err != nil {
		return checks, err
	}
	if validationContext == nil || validationContext.StageApprovalPlan == nil {
		log.Printf("pre_execution_validator: validation context or stage approval plan is nil; emitting no checks")
		return checks, nil
	}
	plan := validationContext.StageApprovalPlan
	if plan.Stages == nil {
		log.Printf("pre_execution_validator: stage approval plan has nil stages; emitting no checks")
		return checks, nil
	}

	allowedDomains := validationContext.RecordingRunSpec.AllowedDomains
	threshold := pev.confidenceThreshold()
	evidenceDemanded := len(plan.EvidenceRefs) > 0

	for i := range plan.Stages {
		if err := ctx.Err(); err != nil {
			return checks, err
		}
		stage := plan.Stages[i]
		nodeID := preExecStageNodeID(stage)
		route := preExecStageRoute(stage)

		// --- domain check --------------------------------------------------
		checks = append(checks, pev.buildDomainCheck(stage, nodeID, route, allowedDomains))

		// --- selector check ------------------------------------------------
		checks = append(checks, pev.buildSelectorCheck(stage, nodeID))

		// --- evidence check ------------------------------------------------
		checks = append(checks, pev.buildEvidenceCheck(stage, nodeID, evidenceDemanded))

		// --- blocking_uncertainty check ------------------------------------
		checks = append(checks, pev.buildBlockingUncertaintyCheck(stage, nodeID, plan))

		// --- confidence risk check (conditional) ---------------------------
		if stage.Confidence > 0 && stage.Confidence < threshold {
			checks = append(checks, model.PreValidationCheck{
				CheckID:       preExecCheckID(nodeID, "confidence"),
				CheckCategory: "confidence_gate",
				CheckName:     "stage_confidence_above_threshold",
				CheckType:     "confidence",
				NodeID:        nodeID,
				Required:      false,
				Executed:      true,
				Passed:        false,
				Threshold:     threshold,
				ExpectedValue: fmt.Sprintf("confidence >= %.2f", threshold),
				Details:       fmt.Sprintf("stage confidence %.2f below threshold %.2f", stage.Confidence, threshold),
				Confidence:    stage.Confidence,
				RiskLevel:     "medium",
			})
		}
	}

	log.Printf("pre_execution_validator: emitted %d check(s) across %d stage(s)", len(checks), len(plan.Stages))
	return checks, nil
}

// AnalyzePreValidationResults groups checks by NodeID into StageFeedback
// records, preserving stage order from StageApprovalPlan.Stages. Each failed
// or unexecuted-required check is mapped to a ValidationResult. The feedback
// type escalates to reunderstanding_required on domain failures or unresolved
// blocking uncertainties, to fine_tune for minor selector/evidence/confidence
// issues, and to continue otherwise.
func (pev *PreExecutionValidator) AnalyzePreValidationResults(
	ctx context.Context,
	checks []model.PreValidationCheck,
	validationContext *model.ValidationContext,
) ([]model.StageFeedback, error) {
	feedbacks := []model.StageFeedback{}
	if err := ctx.Err(); err != nil {
		return feedbacks, err
	}
	if validationContext == nil || validationContext.StageApprovalPlan == nil {
		log.Printf("pre_execution_validator: analysis skipped (nil context or plan)")
		return feedbacks, nil
	}
	plan := validationContext.StageApprovalPlan
	if plan.Stages == nil {
		return feedbacks, nil
	}

	byNode := make(map[string][]model.PreValidationCheck)
	for _, c := range checks {
		byNode[c.NodeID] = append(byNode[c.NodeID], c)
	}

	for i := range plan.Stages {
		if err := ctx.Err(); err != nil {
			return feedbacks, err
		}
		stage := plan.Stages[i]
		nodeID := preExecStageNodeID(stage)
		stageChecks := byNode[nodeID]
		if len(stageChecks) == 0 {
			continue
		}
		feedbacks = append(feedbacks, pev.buildFeedback(stage, nodeID, stageChecks))
	}
	return feedbacks, nil
}

// buildFeedback aggregates a stage's checks into a single StageFeedback record.
func (pev *PreExecutionValidator) buildFeedback(
	stage model.StageApprovalStage,
	nodeID string,
	stageChecks []model.PreValidationCheck,
) model.StageFeedback {
	var (
		// Initialized to a non-nil empty slice so a clean stage serializes to
		// "validation_results": [] rather than null (the model field has no
		// omitempty), which would break JSON consumers that iterate the array.
		results        = []model.ValidationResult{}
		evidenceUnion  []model.EvidenceRef
		seenEvidence   = make(map[string]bool)
		domainFailed   bool
		blockingFailed bool
		hasMinorFail   bool
		riskLevels     []string
		confs          []float64
	)

	if stage.Confidence > 0 {
		confs = append(confs, stage.Confidence)
	}

	cite := func(er model.EvidenceRef) {
		key := er.ID + "|" + er.FieldPath + "|" + er.ArtifactID
		if seenEvidence[key] {
			return
		}
		seenEvidence[key] = true
		evidenceUnion = append(evidenceUnion, er)
	}

	for _, c := range stageChecks {
		if c.Confidence > 0 {
			confs = append(confs, c.Confidence)
		}
		if c.RiskLevel != "" {
			riskLevels = append(riskLevels, c.RiskLevel)
		}
		for _, er := range c.EvidenceRefs {
			cite(er)
		}

		isDomainFail := c.CheckType == "domain" && c.Executed && !c.Passed
		isBlockingFail := c.CheckType == "blocking_uncertainty" && c.Required && !c.Passed
		if isDomainFail {
			domainFailed = true
		}
		if isBlockingFail {
			blockingFailed = true
		}

		// Map failed or unexecuted-required checks to structured results.
		// Gate the failed branch on actually being executed, so that an
		// explicitly not-applicable check (Required=false, Executed=false,
		// Passed=false — e.g. an empty-route domain check on a non-navigate
		// stage) is not misclassified as a failure.
		if (c.Executed && !c.Passed) || (c.Required && !c.Executed) {
			results = append(results, pev.checkToResult(c, isDomainFail, isBlockingFail))
			if !isDomainFail && !isBlockingFail {
				hasMinorFail = true
			}
		}
	}
	for _, er := range stage.EvidenceRefs {
		cite(er)
	}

	feedbackType := model.ValidationFeedbackContinue
	blocked := false
	blockReason := ""
	riskLevel := preExecMaxRiskLevel(riskLevels...)

	switch {
	case domainFailed || blockingFailed:
		feedbackType = model.ValidationFeedbackReunderstandingRequired
		blocked = true
		parts := []string{}
		if domainFailed {
			parts = append(parts, "domain check failed")
		}
		if blockingFailed {
			parts = append(parts, "blocking uncertainty unresolved")
		}
		blockReason = strings.Join(parts, "; ")
		riskLevel = "critical"
	case hasMinorFail:
		feedbackType = model.ValidationFeedbackFineTune
		if riskLevel == "low" || riskLevel == "" {
			riskLevel = "medium"
		}
	default:
		feedbackType = model.ValidationFeedbackContinue
	}

	confidence := preExecMinPositiveConfidence(confs...)
	if confidence == 0 {
		confidence = stage.Confidence
	}

	summary := fmt.Sprintf("stage %s (order=%d): %d check(s), %d issue(s); feedback=%s",
		stage.ID, stage.Order, len(stageChecks), len(results), feedbackType)

	return model.StageFeedback{
		NodeID:            nodeID,
		StageOrder:        stage.Order,
		BusinessStageID:   stage.BusinessStageID,
		FeedbackType:      feedbackType,
		ValidationResults: results,
		Summary:           summary,
		RiskLevel:         riskLevel,
		Confidence:        confidence,
		RequiresRepair:    hasMinorFail,
		Blocked:           blocked,
		BlockReason:       blockReason,
		EvidenceRefs:      evidenceUnion,
		GeneratedAt:       time.Now(),
	}
}

// checkToResult converts a failed/unexecuted PreValidationCheck into a
// ValidationResult. Domain and blocking_uncertainty failures are critical
// (blocking_uncertainty additionally sets Blocker).
func (pev *PreExecutionValidator) checkToResult(c model.PreValidationCheck, isDomainFail, isBlockingFail bool) model.ValidationResult {
	res := model.ValidationResult{
		ID:             c.CheckID + ":result",
		NodeID:         c.NodeID,
		Phase:          model.LegacyValidationPhasePreExecution,
		Title:          c.CheckName,
		Description:    c.Details,
		Confidence:     c.Confidence,
		ValidationRule: c.CheckType,
		EvidenceRefs:   c.EvidenceRefs,
		Timestamp:      time.Now(),
	}
	if c.ExpectedValue != nil {
		res.ExpectedValue = fmt.Sprint(c.ExpectedValue)
	}
	if c.Error != "" {
		if res.Description == "" {
			res.Description = c.Error
		} else {
			res.Description = res.Description + " — " + c.Error
		}
	}

	switch {
	case !c.Executed:
		res.Type = model.ValidationResultTypeUnresolved
	case isBlockingFail:
		res.Type = model.ValidationResultTypeFailed
		res.Critical = true
		res.Blocker = true
	case isDomainFail:
		res.Type = model.ValidationResultTypeFailed
		res.Critical = true
	case c.CheckType == "evidence":
		res.Type = model.ValidationResultTypeFailed
	case c.CheckType == "selector" || c.CheckType == "confidence":
		res.Type = model.ValidationResultTypeWarning
	default:
		res.Type = model.ValidationResultTypeWarning
	}
	return res
}

// buildDomainCheck emits the "domain" check: the host of the stage's resolved
// route must fall within RecordingRunSpec.AllowedDomains.
func (pev *PreExecutionValidator) buildDomainCheck(stage model.StageApprovalStage, nodeID, route string, allowedDomains []string) model.PreValidationCheck {
	check := model.PreValidationCheck{
		CheckID:       preExecCheckID(nodeID, "domain"),
		CheckCategory: "route_domain",
		CheckName:     "route_domain_allowed",
		CheckType:     "domain",
		NodeID:        nodeID,
		Target:        route,
		Confidence:    0.95,
	}
	if route == "" {
		check.Required = false
		check.Executed = false
		check.Passed = false
		check.Error = "no route configured for stage"
		check.Details = "stage has no EntryRoute/TargetRoute/TargetURL/ExpectedRouteAfterAction"
		check.RiskLevel = "medium"
		check.Confidence = 0.3
		return check
	}
	check.Required = true
	check.Executed = true
	check.ExpectedValue = strings.Join(allowedDomains, ", ")

	host, err := preExecParseHost(route)
	switch {
	case err != nil:
		// A bare path (e.g. "/dashboard") carries no host; treat as not-applicable
		// rather than a hard failure so it doesn't drive hasMinorFail.
		check.Required = false
		check.Executed = false
		check.Passed = false
		check.Error = err.Error()
		check.Details = fmt.Sprintf("could not parse host from route %q", route)
		check.RiskLevel = "medium"
		check.Confidence = 0.3
	case len(allowedDomains) == 0:
		// No allow-list configured: pass-with-warning.
		check.Passed = true
		check.Details = fmt.Sprintf("host %q allowed; no allowed_domains configured (warning)", host)
		check.RiskLevel = "warning"
		check.Confidence = 0.5
	case preExecHostAllowed(host, allowedDomains):
		check.Passed = true
		check.Details = fmt.Sprintf("host %q within allowed domains", host)
		check.RiskLevel = "low"
		check.Confidence = 0.95
	default:
		check.Passed = false
		check.Details = fmt.Sprintf("host %q not within allowed domains %v", host, allowedDomains)
		check.RiskLevel = "critical"
		check.Confidence = 0.3
	}
	return check
}

// buildSelectorCheck emits the "selector" check: the stage's interaction must
// carry a resolvable DOM selector candidate.
func (pev *PreExecutionValidator) buildSelectorCheck(stage model.StageApprovalStage, nodeID string) model.PreValidationCheck {
	check := model.PreValidationCheck{
		CheckID:       preExecCheckID(nodeID, "selector"),
		CheckCategory: "interaction_target",
		CheckName:     "interaction_selector_present",
		CheckType:     "selector",
		NodeID:        nodeID,
		Confidence:    0.8,
	}
	if stage.RuntimeAdaptive && strings.TrimSpace(stage.Interaction.Target.Selector) != "" && !preExecTargetHasFormalPrimarySelector(stage.Interaction.Target) {
		check.Target = stage.Interaction.Target.Selector
		check.Required = true
		check.Executed = true
		check.Passed = false
		check.Details = "runtime-adaptive primary selector is not represented by a complete evidence-bound candidate"
		check.RiskLevel = "critical"
		check.Confidence = 0.1
		return check
	}
	sel, selType, selConf, present := preExecEffectiveSelector(stage)
	check.Target = sel
	if present {
		check.Required = true
		check.Executed = true
		check.Passed = true
		check.Details = fmt.Sprintf("selector %q (type=%s)", sel, selType)
		check.RiskLevel = "low"
		if selConf > 0 {
			check.Confidence = selConf
		}
		return check
	}
	kind := string(stage.Interaction.Kind)
	if kind == "wait" || kind == "api_call" || kind == "navigate" {
		// These interaction kinds legitimately have no DOM selector.
		check.Required = false
		check.Executed = true
		check.Passed = true
		check.Details = fmt.Sprintf("interaction kind %q does not require a DOM selector", kind)
		check.RiskLevel = "low"
		check.Confidence = 0.7
		return check
	}
	if stage.TargetContract != nil {
		// Semantic target resolved by the browser agent at runtime; no static
		// DOM selector is expected at pre-execution time.
		check.Required = false
		check.Executed = true
		check.Passed = true
		check.Details = "target_contract provides semantic selector (resolved at runtime)"
		check.RiskLevel = "low"
		check.Confidence = 0.7
		return check
	}
	check.Required = false
	check.Executed = true
	check.Passed = false
	check.Details = "no selector candidate"
	check.RiskLevel = "high"
	check.Confidence = 0.2
	return check
}

// buildEvidenceCheck emits the "evidence" hard-gate check.
func (pev *PreExecutionValidator) buildEvidenceCheck(stage model.StageApprovalStage, nodeID string, evidenceDemanded bool) model.PreValidationCheck {
	check := model.PreValidationCheck{
		CheckID:       preExecCheckID(nodeID, "evidence"),
		CheckCategory: "evidence_gate",
		CheckName:     "evidence_requirements_present",
		CheckType:     "evidence",
		NodeID:        nodeID,
		Required:      true,
		Executed:      true,
		ExpectedValue: "at least one EvidenceRef supporting the stage claim",
	}
	check.EvidenceRefs = append(check.EvidenceRefs, stage.EvidenceRefs...)
	switch {
	case len(stage.EvidenceRefs) > 0:
		check.Passed = true
		check.Details = fmt.Sprintf("%d evidence ref(s) attached", len(stage.EvidenceRefs))
		check.RiskLevel = "low"
		check.Confidence = 0.85
	case evidenceDemanded:
		check.Passed = false
		check.Details = "stage references no evidence but plan demands evidence"
		check.RiskLevel = "high"
		check.Confidence = 0.3
	default:
		check.Passed = true
		check.Details = "no evidence referenced (plan does not require it)"
		check.RiskLevel = "medium"
		check.Confidence = 0.5
	}
	return check
}

// buildBlockingUncertaintyCheck emits the "blocking_uncertainty" check by
// scanning the plan-level UncertaintyReport plus stage-level RiskNotes.
func (pev *PreExecutionValidator) buildBlockingUncertaintyCheck(stage model.StageApprovalStage, nodeID string, plan *model.StageApprovalPlan) model.PreValidationCheck {
	check := model.PreValidationCheck{
		CheckID:       preExecCheckID(nodeID, "blocking_uncertainty"),
		CheckCategory: "uncertainty_gate",
		CheckName:     "no_blocking_uncertainty",
		CheckType:     "blocking_uncertainty",
		NodeID:        nodeID,
		Required:      true,
		Executed:      true,
	}
	blockers := preExecBlockingUncertaintiesForStage(plan, stage.ID, stage.NodeID)
	if len(blockers) > 0 {
		summaries := make([]string, 0, len(blockers))
		for _, b := range blockers {
			if b.Summary != "" {
				summaries = append(summaries, b.Summary)
			}
			check.EvidenceRefs = append(check.EvidenceRefs, b.EvidenceRefs...)
		}
		check.Passed = false
		check.Details = fmt.Sprintf("%d blocking uncertainty(ies): %s", len(blockers), strings.Join(summaries, "; "))
		check.RiskLevel = "critical"
		check.Confidence = 0.1
		return check
	}
	if len(stage.RiskNotes) > 0 {
		check.Passed = true
		check.Details = fmt.Sprintf("no blocking uncertainty; %d risk note(s): %s", len(stage.RiskNotes), strings.Join(stage.RiskNotes, "; "))
		check.RiskLevel = "medium"
		check.Confidence = 0.6
		return check
	}
	check.Passed = true
	check.Details = "no blocking uncertainty"
	check.RiskLevel = "low"
	check.Confidence = 0.9
	return check
}

// ---------------------------------------------------------------------------
// Package-scoped helpers (prefixed preExec to avoid collisions with sibling
// validator files being written concurrently).
// ---------------------------------------------------------------------------

// preExecStageNodeID returns the stage's graph NodeID, falling back to the
// stage's own ID when NodeID is unset so grouping stays deterministic.
func preExecStageNodeID(stage model.StageApprovalStage) string {
	if stage.NodeID != "" {
		return stage.NodeID
	}
	return stage.ID
}

// preExecStageRoute resolves the stage route per the documented precedence:
// EntryRoute, else TargetURL (full URL wins over bare path), else TargetRoute,
// else ExpectedRouteAfterAction.
func preExecStageRoute(stage model.StageApprovalStage) string {
	if stage.EntryRoute != "" {
		return stage.EntryRoute
	}
	// Prefer a full URL over a bare path so the domain check has a host to parse.
	if stage.TargetURL != "" {
		return stage.TargetURL
	}
	if stage.TargetRoute != "" {
		return stage.TargetRoute
	}
	return stage.ExpectedRouteAfterAction
}

// preExecCheckID builds a deterministic check id: "<nodeID>:<checkType>".
func preExecCheckID(nodeID, checkType string) string {
	if nodeID == "" {
		nodeID = "unknown"
	}
	return nodeID + ":" + checkType
}

// preExecParseHost extracts the hostname from a route or URL string. Full URLs
// are parsed directly; scheme-less "host/path" forms get a synthetic scheme.
// Bare paths (leading "/") and empty strings yield an error.
func preExecParseHost(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty route")
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", fmt.Errorf("parse route %q: %w", raw, err)
		}
		return u.Hostname(), nil
	}
	if strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("route %q is a bare path with no host", raw)
	}
	u, err := url.Parse("https://" + raw)
	if err != nil {
		return "", fmt.Errorf("parse route %q: %w", raw, err)
	}
	if u.Hostname() == "" {
		return "", fmt.Errorf("route %q has no host", raw)
	}
	return u.Hostname(), nil
}

// preExecNormalizeDomain strips scheme, path, port, wildcard and leading-dot
// markers from an allow-list entry so it can be compared against a bare host.
func preExecNormalizeDomain(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	if d == "" {
		return ""
	}
	if i := strings.Index(d, "://"); i >= 0 {
		d = d[i+3:]
	}
	if i := strings.Index(d, "/"); i >= 0 {
		d = d[:i]
	}
	if i := strings.LastIndex(d, ":"); i >= 0 {
		d = d[:i]
	}
	d = strings.TrimPrefix(d, "*.")
	d = strings.TrimPrefix(d, ".")
	return d
}

// preExecHostAllowed reports whether host equals an allowed domain or is a
// subdomain of one (host == allowed OR host endswith "."+allowed).
func preExecHostAllowed(host string, allowed []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	for _, a := range allowed {
		norm := preExecNormalizeDomain(a)
		if norm == "" {
			continue
		}
		if host == norm || strings.HasSuffix(host, "."+norm) {
			return true
		}
	}
	return false
}

// preExecEffectiveSelector resolves the stage interaction's effective DOM
// selector following the established repo precedence: primary Selector, then
// the best SelectorAlternative, then a testID-derived selector. The boolean
// indicates whether a candidate exists.
func preExecEffectiveSelector(stage model.StageApprovalStage) (selector, selectorType string, confidence float64, present bool) {
	target := stage.Interaction.Target
	if target.Selector != "" {
		if !stage.RuntimeAdaptive {
			return target.Selector, "primary", 0.9, true
		}
		for _, candidate := range target.SelectorAlternatives {
			if model.SelectorCandidateHasFormalProvenance(candidate) && preExecCandidateMatchesSelector(candidate, target.Selector) {
				return target.Selector, "primary_evidence_bound", maxFloat(candidate.Confidence, 0.8), true
			}
		}
	}
	bestVal := ""
	bestConf := 0.0
	for _, cand := range target.SelectorAlternatives {
		if cand.Value == "" || !model.SelectorCandidateHasFormalProvenance(cand) {
			continue
		}
		if bestVal == "" || cand.Confidence > bestConf {
			bestVal = cand.Value
			bestConf = cand.Confidence
		}
	}
	if bestVal != "" {
		return bestVal, "alternative", bestConf, true
	}
	if target.TestID != "" {
		return "[data-testid=\"" + target.TestID + "\"]", "testid", 0.7, true
	}
	return "", "", 0, false
}

func preExecTargetHasFormalPrimarySelector(target model.ActionTarget) bool {
	for _, candidate := range target.SelectorAlternatives {
		if model.SelectorCandidateHasFormalProvenance(candidate) && preExecCandidateMatchesSelector(candidate, target.Selector) {
			return true
		}
	}
	return false
}

func preExecCandidateMatchesSelector(candidate model.SelectorCandidate, selector string) bool {
	if strings.EqualFold(strings.TrimSpace(candidate.Value), strings.TrimSpace(selector)) {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(candidate.Kind), "testid") {
		return false
	}
	compact := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(selector, "\"", "'"), " ", ""))
	return strings.Contains(compact, "data-testid='"+strings.ToLower(strings.TrimSpace(candidate.Value))+"'")
}

func maxFloat(left, right float64) float64 {
	if left > right {
		return left
	}
	return right
}

// preExecBlockingUncertaintiesForStage collects plan-level blocking
// uncertainties that reference the given stage (by StageID or NodeID), plus
// plan-wide blocking entries (empty StageID and NodeID) which apply globally.
func preExecBlockingUncertaintiesForStage(plan *model.StageApprovalPlan, stageID, nodeID string) []model.StageUncertainty {
	var out []model.StageUncertainty
	if plan == nil {
		return out
	}
	for i := range plan.UncertaintyReport {
		u := plan.UncertaintyReport[i]
		if !u.Blocking {
			continue
		}
		if stageID != "" && u.StageID == stageID {
			out = append(out, u)
			continue
		}
		if nodeID != "" && u.NodeID == nodeID {
			out = append(out, u)
			continue
		}
		if u.StageID == "" && u.NodeID == "" {
			out = append(out, u)
		}
	}
	return out
}

// preExecMinPositiveConfidence returns the smallest strictly-positive value in
// values, or 0 if none are positive. Zero values are ignored so that an unset
// stage/check confidence cannot tank the aggregate.
func preExecMinPositiveConfidence(values ...float64) float64 {
	var min float64
	set := false
	for _, v := range values {
		if v <= 0 {
			continue
		}
		if !set || v < min {
			min = v
			set = true
		}
	}
	return min
}

// preExecMaxRiskLevel returns the highest-severity risk label among the inputs.
// Unknown / empty labels are treated as lower than "low".
func preExecMaxRiskLevel(risks ...string) string {
	rank := map[string]int{"critical": 4, "high": 3, "medium": 2, "low": 1, "warning": 1}
	best := "low"
	bestRank := 0
	for _, r := range risks {
		r = strings.TrimSpace(strings.ToLower(r))
		rr, ok := rank[r]
		if !ok {
			continue
		}
		if rr > bestRank {
			bestRank = rr
			best = r
		}
	}
	return best
}
