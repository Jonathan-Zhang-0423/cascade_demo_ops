package orchestrator

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

// RealTimeBatchValidator runs deterministic, in-loop validation of completed
// StepResults against the approved StageApprovalPlan. It emits ValidationResult
// records, rolls them up into StageFeedback, and proposes runtime repair patches
// scoped strictly to selector / wait-until / capture-timing fields.
//
// The validator never calls an LLM, network, or browser sidecar; it operates
// purely over the in-memory structs supplied via the ValidationContext.
type RealTimeBatchValidator struct {
	cfg *model.ValidationConfig
}

// NewRealTimeBatchValidator constructs a validator bound to cfg. A nil cfg is
// tolerated: threshold helpers fall back to the documented defaults
// (PassRateThreshold=0.9, ConfidenceThreshold=0.5).
func NewRealTimeBatchValidator(cfg *model.ValidationConfig) *RealTimeBatchValidator {
	return &RealTimeBatchValidator{cfg: cfg}
}

// stageIsCritical reports whether failures on this stage should be treated as
// critical: when the stage is on a route-critical path
// (RuntimeRouteVerificationRequired), or when the stage itself claims high
// expected confidence (>= pass-rate threshold, default 0.9).
func (rtb *RealTimeBatchValidator) stageIsCritical(stage model.StageApprovalStage, hasStage bool) bool {
	if !hasStage {
		return false
	}
	if stage.RuntimeRouteVerificationRequired {
		return true
	}
	return stage.Confidence >= rtPassRateThreshold(rtb.cfg)
}

// ValidateStepResults inspects each completed StepResult against its matching
// StageApprovalStage (matched by NodeID == stage.NodeID) and emits one or more
// ValidationResult records per step covering status, error, route, and duration
// checks. All checks are deterministic and in-memory.
func (rtb *RealTimeBatchValidator) ValidateStepResults(ctx context.Context, completedStepResults []model.StepResult, validationContext *model.ValidationContext) ([]model.ValidationResult, error) {
	if err := ctx.Err(); err != nil {
		return []model.ValidationResult{}, err
	}
	if validationContext == nil {
		return []model.ValidationResult{}, fmt.Errorf("real_time_batch_validator: validation context is nil")
	}
	if validationContext.StageApprovalPlan == nil {
		return []model.ValidationResult{}, fmt.Errorf("real_time_batch_validator: stage approval plan is nil")
	}

	stageIndex := rtBuildStageIndex(validationContext.StageApprovalPlan)
	if len(stageIndex) == 0 {
		log.Printf("real_time_batch_validator: no stages indexed from plan_id=%s; steps will be treated as unmatched", validationContext.StageApprovalPlan.ID)
	}

	now := time.Now()
	results := make([]model.ValidationResult, 0, len(completedStepResults))

	for _, step := range completedStepResults {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		stage, hasStage := stageIndex[step.NodeID]
		if !hasStage {
			log.Printf("real_time_batch_validator: no matching stage for node_id=%s status=%q", step.NodeID, step.Status)
		}
		critical := rtb.stageIsCritical(stage, hasStage)
		evidence := rtArtifactEvidenceRefs(step.Artifacts)

		expectedStatus := "passed"
		if hasStage && stage.SuccessState != "" {
			expectedStatus = stage.SuccessState
		}

		// Status check (always emitted: passed or failed).
		if rtIsSuccessStatus(step.Status) {
			results = append(results, model.ValidationResult{
				ID:             rtResultID(step.NodeID, "status"),
				NodeID:         step.NodeID,
				Phase:          model.LegacyValidationPhaseRealTimeBatch,
				Type:           model.ValidationResultTypePassed,
				Title:          "step status indicates success",
				Description:    fmt.Sprintf("step status %q matches a recognized success status", step.Status),
				ExpectedValue:  expectedStatus,
				ObservedValue:  rtFirstNonEmpty(step.ObservedState, step.Status),
				Confidence:     0.9,
				Critical:       false,
				ValidationRule: "status_match",
				EvidenceRefs:   evidence,
				Timestamp:      now,
			})
		} else {
			results = append(results, model.ValidationResult{
				ID:             rtResultID(step.NodeID, "status"),
				NodeID:         step.NodeID,
				Phase:          model.LegacyValidationPhaseRealTimeBatch,
				Type:           model.ValidationResultTypeFailed,
				Title:          "step status did not indicate success",
				Description:    fmt.Sprintf("step status %q is not a recognized success status", step.Status),
				ExpectedValue:  expectedStatus,
				ObservedValue:  rtFirstNonEmpty(step.ObservedState, step.Status),
				Confidence:     0.95,
				Critical:       critical,
				Blocker:        critical,
				ValidationRule: "status_match",
				EvidenceRefs:   evidence,
				Timestamp:      now,
			})
		}

		// Error check.
		if step.Error != nil {
			desc := step.Error.Message
			if desc == "" {
				desc = step.Error.Code
			}
			results = append(results, model.ValidationResult{
				ID:             rtResultID(step.NodeID, "error"),
				NodeID:         step.NodeID,
				Phase:          model.LegacyValidationPhaseRealTimeBatch,
				Type:           model.ValidationResultTypeFailed,
				Title:          fmt.Sprintf("step reported error: %s", rtFirstNonEmpty(step.Error.Code, "unknown")),
				Description:    desc,
				ObservedValue:  desc,
				Confidence:     0.98,
				Critical:       critical,
				Blocker:        critical,
				ValidationRule: "error_free",
				EvidenceRefs:   evidence,
				Timestamp:      now,
			})
		}

		// Route check: compare observed state to ExpectedRouteAfterAction
		// case-insensitively (exact match or substring).
		if hasStage && stage.ExpectedRouteAfterAction != "" && step.ObservedState != "" {
			if !rtRouteMatches(step.ObservedState, stage.ExpectedRouteAfterAction) {
				results = append(results, model.ValidationResult{
					ID:             rtResultID(step.NodeID, "route"),
					NodeID:         step.NodeID,
					Phase:          model.LegacyValidationPhaseRealTimeBatch,
					Type:           model.ValidationResultTypeWarning,
					Title:          "observed state did not contain expected route after action",
					Description:    fmt.Sprintf("expected route %q not found in observed state", stage.ExpectedRouteAfterAction),
					ExpectedValue:  stage.ExpectedRouteAfterAction,
					ObservedValue:  step.ObservedState,
					Confidence:     0.7,
					Critical:       false,
					ValidationRule: "route_match",
					EvidenceRefs:   evidence,
					Timestamp:      now,
				})
			}
		}

		// Duration check: flag when duration exceeds 2x the stage budget.
		if hasStage && stage.DurationMS > 0 && step.DurationMS > 2*stage.DurationMS {
			results = append(results, model.ValidationResult{
				ID:             rtResultID(step.NodeID, "duration"),
				NodeID:         step.NodeID,
				Phase:          model.LegacyValidationPhaseRealTimeBatch,
				Type:           model.ValidationResultTypeWarning,
				Title:          "step duration exceeded twice the expected budget",
				Description:    fmt.Sprintf("duration %dms is more than 2x expected %dms", step.DurationMS, stage.DurationMS),
				ExpectedValue:  fmt.Sprintf("%dms", stage.DurationMS),
				ObservedValue:  fmt.Sprintf("%dms", step.DurationMS),
				Confidence:     0.9,
				Critical:       false,
				ValidationRule: "duration_budget",
				EvidenceRefs:   evidence,
				Timestamp:      now,
			})
		}
	}

	log.Printf("real_time_batch_validator: validate_step_results steps=%d emitted=%d", len(completedStepResults), len(results))
	return results, nil
}

// GenerateStageFeedback groups ValidationResult records by NodeID and produces
// a StageFeedback rollup per node. The feedback type is derived from the
// severity of the grouped results:
//   - reunderstanding_required when any critical unresolved result is present;
//   - fine_tune when only repairable major/minor results remain;
//   - continue otherwise.
func (rtb *RealTimeBatchValidator) GenerateStageFeedback(validationResults []model.ValidationResult, validationContext *model.ValidationContext) []model.StageFeedback {
	feedbacks := make([]model.StageFeedback, 0)
	if validationContext == nil {
		return feedbacks
	}
	stageIndex := rtBuildStageIndex(validationContext.StageApprovalPlan)

	grouped := make(map[string][]model.ValidationResult)
	order := make([]string, 0)
	for _, r := range validationResults {
		if _, ok := grouped[r.NodeID]; !ok {
			order = append(order, r.NodeID)
		}
		grouped[r.NodeID] = append(grouped[r.NodeID], r)
	}

	now := time.Now()
	for _, nodeID := range order {
		results := grouped[nodeID]
		stage, hasStage := stageIndex[nodeID]

		hasCriticalUnresolved := false
		hasRepairable := false
		passedCount := 0
		failedCount := 0
		warnCount := 0
		criticalUnresolvedCount := 0
		confidences := make([]float64, 0)
		allEvidence := make([]model.EvidenceRef, 0)
		for _, r := range results {
			isResolved := r.Repaired || r.Type == model.ValidationResultTypePassed
			// Any unresolved critical result escalates.
			if r.Critical && !isResolved {
				hasCriticalUnresolved = true
				criticalUnresolvedCount++
			}
			if !r.Critical && !isResolved && (r.Type == model.ValidationResultTypeWarning || r.Type == model.ValidationResultTypeFailed) {
				hasRepairable = true
			}
			if r.Confidence > 0 {
				confidences = append(confidences, r.Confidence)
			}
			allEvidence = append(allEvidence, r.EvidenceRefs...)
			switch r.Type {
			case model.ValidationResultTypePassed:
				passedCount++
			case model.ValidationResultTypeWarning:
				warnCount++
			case model.ValidationResultTypeFailed, model.ValidationResultTypeUnresolved, model.ValidationResultTypeUncertainty:
				failedCount++
			}
		}

		feedbackType := model.ValidationFeedbackContinue
		riskLevel := "low"
		blocked := false
		blockReason := ""
		summary := fmt.Sprintf("stage passed (%d result(s))", passedCount)
		switch {
		case hasCriticalUnresolved:
			feedbackType = model.ValidationFeedbackReunderstandingRequired
			riskLevel = "critical"
			blocked = true
			blockReason = fmt.Sprintf("%d unresolved critical issue(s) require reunderstanding", criticalUnresolvedCount)
			summary = fmt.Sprintf("stage blocked: %d critical/unresolved, %d warning", criticalUnresolvedCount, warnCount)
		case hasRepairable:
			feedbackType = model.ValidationFeedbackFineTune
			riskLevel = "medium"
			summary = fmt.Sprintf("stage needs fine-tune: %d warning/minor, %d failed", warnCount, failedCount)
		}

		// Confidence: min of result confidences, falling back to stage.Confidence.
		confidence := rtMinConfidence(confidences)
		if confidence == 0 && hasStage {
			confidence = stage.Confidence
		}

		stageOrder := 0
		businessStageID := ""
		if hasStage {
			stageOrder = stage.Order
			businessStageID = stage.BusinessStageID
		}

		feedbacks = append(feedbacks, model.StageFeedback{
			NodeID:            nodeID,
			StageOrder:        stageOrder,
			BusinessStageID:   businessStageID,
			FeedbackType:      feedbackType,
			ValidationResults: results,
			Summary:           summary,
			RiskLevel:         riskLevel,
			Confidence:        confidence,
			RequiresRepair:    feedbackType == model.ValidationFeedbackFineTune,
			Blocked:           blocked,
			BlockReason:       blockReason,
			EvidenceRefs:      allEvidence,
			GeneratedAt:       now,
		})
	}

	log.Printf("real_time_batch_validator: generated feedback stages=%d from results=%d", len(feedbacks), len(validationResults))
	return feedbacks
}

// ApplyRuntimeRepairs inspects stage feedbacks for MINOR repairable issues only
// and proposes runtime repair patches. Patches are strictly limited to selector,
// wait-strategy, and capture-timing fields; business logic, routes, and graph
// structure are never touched. Returns map[NodeID] -> []ValidationRepair.
//
// If the config disables runtime repair (EnableRuntimeRepair == false) or the
// config is nil, an empty (non-nil) map is returned.
func (rtb *RealTimeBatchValidator) ApplyRuntimeRepairs(ctx context.Context, stageFeedbacks []model.StageFeedback) map[string][]model.ValidationRepair {
	_ = ctx
	out := make(map[string][]model.ValidationRepair)
	if rtb.cfg == nil || !rtb.cfg.EnableRuntimeRepair {
		return out
	}

	now := time.Now()
	for _, feedback := range stageFeedbacks {
		if feedback.Blocked {
			continue
		}
		if feedback.FeedbackType != model.ValidationFeedbackFineTune {
			continue
		}
		nodeID := feedback.NodeID
		for _, r := range feedback.ValidationResults {
			if r.Critical || r.Repaired {
				continue
			}
			// Only minor / repairable result types.
			if r.Type != model.ValidationResultTypeWarning && r.Type != model.ValidationResultTypeFailed {
				continue
			}
			repair, ok := rtSynthesizeRepair(nodeID, r, now)
			if !ok {
				continue
			}
			out[nodeID] = append(out[nodeID], repair)
		}
	}

	log.Printf("real_time_batch_validator: proposed repairs nodes=%d from feedbacks=%d", len(out), len(stageFeedbacks))
	return out
}

// rtBuildStageIndex maps NodeID -> StageApprovalStage for O(1) lookup. Stages
// with an empty NodeID are skipped.
func rtBuildStageIndex(plan *model.StageApprovalPlan) map[string]model.StageApprovalStage {
	index := make(map[string]model.StageApprovalStage)
	if plan == nil {
		return index
	}
	for _, stage := range plan.Stages {
		if stage.NodeID == "" {
			continue
		}
		index[stage.NodeID] = stage
	}
	return index
}

// rtIsSuccessStatus reports whether status indicates success. Mirrors
// executor.passRate and every other StepResult.Status consumer in the backend,
// which accept only "passed" as success; anything else (including empty string)
// is treated as a failure.
func rtIsSuccessStatus(status string) bool {
	return strings.EqualFold(strings.TrimSpace(status), "passed")
}

// rtRouteMatches reports whether observed carries the expected route, compared
// case-insensitively as either an exact match or a substring of the observed
// state text.
func rtRouteMatches(observed, expected string) bool {
	if strings.EqualFold(observed, expected) {
		return true
	}
	return strings.Contains(strings.ToLower(observed), strings.ToLower(expected))
}

// rtArtifactEvidenceRefs converts a step's ArtifactRefs into lightweight
// EvidenceRef citations so validation results stay traceable to produced
// evidence. Returns nil when no usable artifact IDs are present.
func rtArtifactEvidenceRefs(artifacts []model.ArtifactRef) []model.EvidenceRef {
	if len(artifacts) == 0 {
		return nil
	}
	refs := make([]model.EvidenceRef, 0, len(artifacts))
	for _, art := range artifacts {
		if art.ID == "" {
			continue
		}
		summary := art.Label
		if summary == "" {
			summary = art.URI
		}
		refs = append(refs, model.EvidenceRef{
			ID:         art.ID,
			ArtifactID: art.ID,
			Summary:    summary,
		})
	}
	if len(refs) == 0 {
		return nil
	}
	return refs
}

// rtMinConfidence returns the minimum positive confidence among values, or 0
// when no positive confidence is present.
func rtMinConfidence(values []float64) float64 {
	min := 0.0
	for _, v := range values {
		if v <= 0 {
			continue
		}
		if min == 0 || v < min {
			min = v
		}
	}
	return min
}

// rtResultID builds a deterministic ValidationResult ID from nodeID + suffix.
func rtResultID(nodeID, suffix string) string {
	if nodeID == "" {
		nodeID = "unknown"
	}
	return fmt.Sprintf("rtb-%s-%s", nodeID, suffix)
}

// rtFirstNonEmpty returns the first non-empty argument, or "" if all are empty.
func rtFirstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// rtPassRateThreshold returns the configured pass-rate threshold, defaulting to
// 0.9 when the config is absent or leaves the field at zero.
func rtPassRateThreshold(cfg *model.ValidationConfig) float64 {
	if cfg == nil || cfg.PassRateThreshold <= 0 {
		return 0.9
	}
	return cfg.PassRateThreshold
}

// rtConfidenceThreshold returns the configured confidence threshold, defaulting
// to 0.5 when the config is absent or leaves the field at zero.
func rtConfidenceThreshold(cfg *model.ValidationConfig) float64 {
	if cfg == nil || cfg.ConfidenceThreshold <= 0 {
		return 0.5
	}
	return cfg.ConfidenceThreshold
}

// rtSynthesizeRepair builds a runtime ValidationRepair for a single minor
// validation result. Only selector / wait-strategy / capture-timing patches are
// ever produced. All confidences are < 1.0.
//
// Result classification is driven by ValidationRule:
//   - duration_budget -> WaitStrategyPatch (budget exceeded, relax the wait);
//   - route_match     -> TimingPatch (route not settled, delay capture);
//   - status_match / error_free -> WaitStrategyPatch fallback. A selector repair
//     is never synthesized here because the node's real selector lives on the
//     WorkflowGraph, which is not available in this method's signature, and the
//     ObservedValue for these rules is a status/state/URL or error string rather
//     than a CSS selector.
func rtSynthesizeRepair(nodeID string, result model.ValidationResult, now time.Time) (model.ValidationRepair, bool) {
	observed := result.ObservedValue
	switch result.ValidationRule {
	case "duration_budget":
		repair := model.ValidationRepair{
			Kind:          "wait_strategy",
			FieldName:     "wait_until",
			OriginalValue: observed,
			RepairedValue: "visible",
			AppliedAt:     now,
			Metadata:      map[string]any{"reason": result.Title},
		}
		repair.WaitStrategyPatch = &model.WaitStrategyPatch{
			NodeID:          nodeID,
			OriginalWait:    "immediate",
			PatchedWait:     "visible",
			TimeoutMS:       5000,
			PollingInterval: 250,
		}
		return repair, true
	case "route_match":
		repair := model.ValidationRepair{
			Kind:          "capture_timing",
			FieldName:     "capture_point",
			OriginalValue: observed,
			RepairedValue: 750,
			AppliedAt:     now,
			Metadata:      map[string]any{"reason": result.Title},
		}
		repair.TimingPatch = &model.TimingPatch{
			NodeID:          nodeID,
			CapturePoint:    "post_navigation",
			OriginalDelayMS: 0,
			PatchedDelayMS:  750,
			StabilizationMS: 500,
		}
		return repair, true
	case "status_match", "error_free":
		// For status/error failures a selector repair is never synthesized:
		// ObservedValue here is a status/state/URL or error string, never the
		// node's real CSS selector (that lives on the WorkflowGraph, which is
		// not available in this method's signature). Feeding a non-selector
		// value into a selector-patching heuristic would fabricate a
		// SelectorPatch whose OriginalSelector is a URL/error message. The
		// wait-strategy fallback is always used instead — the safest available
		// knob for transient status/error failures.
		log.Printf("real_time_batch_validator: selector repair deferred for node=%s (no graph selector context); using wait-strategy fallback", nodeID)
		repair := model.ValidationRepair{
			Kind:          "wait_strategy",
			FieldName:     "wait_until",
			OriginalValue: observed,
			RepairedValue: "stable",
			AppliedAt:     now,
			Metadata:      map[string]any{"reason": result.Title, "selector_deferred": true},
		}
		repair.WaitStrategyPatch = &model.WaitStrategyPatch{
			NodeID:          nodeID,
			OriginalWait:    "immediate",
			PatchedWait:     "stable",
			TimeoutMS:       3000,
			PollingInterval: 200,
		}
		return repair, true
	}
	return model.ValidationRepair{}, false
}
