package orchestrator

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

// PostExecutionAnalyzer performs deterministic post-execution analysis of recorded
// step results against the approved stage plan. It reasons purely over in-memory
// structs: it never calls an LLM, browser, or network API. When a browser-side
// fact would be required to confirm something (live route, live element existence),
// it refuses to fabricate success and instead reports the gap.
type PostExecutionAnalyzer struct {
	config *model.ValidationConfig
}

// NewPostExecutionAnalyzer constructs a PostExecutionAnalyzer bound to the given
// validation config. A nil cfg is tolerated; threshold-gated behavior degrades
// gracefully (treated as no threshold enforced).
func NewPostExecutionAnalyzer(cfg *model.ValidationConfig) *PostExecutionAnalyzer {
	return &PostExecutionAnalyzer{config: cfg}
}

// AnalyzeExecutionResults compares every stage in the approval plan against the
// recorded step results and returns one PostExecutionAnalysis per stage.
//
// Step results are taken from validationContext.StepResults when non-empty,
// otherwise from validationContext.ExecutionTrace.StepResults. Stages are matched
// to steps by NodeID. When a stage has no recorded step, the analysis flags a
// critical "missing step" issue rather than silently passing.
func (pea *PostExecutionAnalyzer) AnalyzeExecutionResults(
	ctx context.Context,
	validationContext *model.ValidationContext,
) ([]model.PostExecutionAnalysis, error) {
	if err := ctx.Err(); err != nil {
		return []model.PostExecutionAnalysis{}, err
	}

	if validationContext == nil {
		return []model.PostExecutionAnalysis{}, fmt.Errorf("post-execution analysis: validation context is nil")
	}
	if validationContext.StageApprovalPlan == nil {
		return []model.PostExecutionAnalysis{}, fmt.Errorf("post-execution analysis: stage approval plan is nil")
	}
	stages := validationContext.StageApprovalPlan.Stages
	if stages == nil {
		return []model.PostExecutionAnalysis{}, fmt.Errorf("post-execution analysis: stage approval plan has no stages")
	}

	stepByNode := postExecStepsByNode(validationContext)
	analyses := make([]model.PostExecutionAnalysis, 0, len(stages))

	for i := range stages {
		if err := ctx.Err(); err != nil {
			return analyses, err
		}
		stage := &stages[i]
		step := stepByNode[stage.NodeID]
		hasStep := step != nil

		statusMatch, stateMatch, routeMatch, artifactMatch, durationMatch := postExecEvaluateMatches(stage, step)
		issues := postExecCollectIssues(stage, step, statusMatch, stateMatch, routeMatch, artifactMatch, durationMatch, hasStep)

		confidence := 0.0
		if hasStep {
			confidence = postExecMatchConfidence(statusMatch, stateMatch, routeMatch, artifactMatch, durationMatch)
		}
		assessment, recommendation := postExecAssess(issues)

		var expectedNode *model.GraphNode
		if validationContext.WorkflowGraph != nil {
			expectedNode = postExecNodeByID(validationContext.WorkflowGraph, stage.NodeID)
		} else {
			log.Printf("post-execution analysis: workflow graph is nil; cannot resolve expected outcome for node %s", stage.NodeID)
		}

		var stepCopy *model.StepResult
		if hasStep {
			cp := *step
			stepCopy = &cp
		}

		analyses = append(analyses, model.PostExecutionAnalysis{
			NodeID:            stage.NodeID,
			StepResult:        stepCopy,
			ExpectedOutcome:   expectedNode,
			StatusMatch:       statusMatch,
			RouteMatch:        routeMatch,
			StateMatch:        stateMatch,
			ArtifactMatch:     artifactMatch,
			DurationMatch:     durationMatch,
			ObservedIssues:    issues,
			OverallAssessment: assessment,
			Recommendation:    recommendation,
			Confidence:        confidence,
		})
	}

	log.Printf("post-execution analysis: analyzed %d stages for package %s", len(analyses), validationContext.PackageID)
	return analyses, nil
}

// GenerateComprehensiveFeedback converts post-execution analyses into per-stage
// StageFeedback records. The feedback type is derived from observed-issue severity:
//
//   - any blocker/critical issue        -> reunderstanding_required
//   - any recoverable major issue       -> fine_tune
//   - otherwise                         -> continue
//
// It also computes the overall pass rate (stages with feedback "continue" over the
// total) and, when that rate falls below the configured PassRateThreshold, appends
// a note to the worst-affected stage's summary.
func (pea *PostExecutionAnalyzer) GenerateComprehensiveFeedback(
	analyses []model.PostExecutionAnalysis,
	validationContext *model.ValidationContext,
) []model.StageFeedback {
	feedbacks := make([]model.StageFeedback, 0, len(analyses))
	if validationContext == nil {
		return feedbacks
	}
	stageByNode := postExecStagesByNode(validationContext)

	now := time.Now()
	passedNodes := 0
	worstIdx := -1
	worstRank := -1
	worstIssueCount := -1

	for i := range analyses {
		a := &analyses[i]
		stage := stageByNode[a.NodeID]

		feedbackType, riskLevel, blocked, blockReason := postExecClassifyAnalysis(a.ObservedIssues)
		results := postExecIssuesToResults(a.NodeID, model.LegacyValidationPhasePostExecutionBatch, a.ObservedIssues, a.Confidence, now)
		if feedbackType == model.ValidationFeedbackContinue {
			passedNodes++
		}

		order := 0
		businessStageID := ""
		var evidence []model.EvidenceRef
		if stage != nil {
			order = stage.Order
			businessStageID = stage.BusinessStageID
			evidence = append(evidence, stage.EvidenceRefs...)
		}
		if a.StepResult != nil && a.StepResult.Error != nil {
			evidence = append(evidence, a.StepResult.Error.EvidenceRefs...)
		}

		summary := a.OverallAssessment
		if summary == "" {
			summary = fmt.Sprintf("%d issue(s) observed", len(a.ObservedIssues))
		}

		feedbacks = append(feedbacks, model.StageFeedback{
			NodeID:            a.NodeID,
			StageOrder:        order,
			BusinessStageID:   businessStageID,
			FeedbackType:      feedbackType,
			ValidationResults: results,
			Summary:           summary,
			RiskLevel:         riskLevel,
			Confidence:        a.Confidence,
			RequiresRepair:    feedbackType == model.ValidationFeedbackFineTune,
			Blocked:           blocked,
			BlockReason:       blockReason,
			EvidenceRefs:      evidence,
			GeneratedAt:       now,
		})

		rank, issueCount := postExecWorstRank(a.ObservedIssues)
		if rank > worstRank || (rank == worstRank && issueCount > worstIssueCount) {
			worstRank = rank
			worstIssueCount = issueCount
			worstIdx = i
		}
	}

	totalNodes := len(analyses)
	passRate := 0.0
	if totalNodes > 0 {
		passRate = float64(passedNodes) / float64(totalNodes)
	}
	if pea.config != nil && totalNodes > 0 && passRate < pea.config.PassRateThreshold && worstIdx >= 0 {
		note := fmt.Sprintf(" [overall pass rate %.2f below threshold %.2f]", passRate, pea.config.PassRateThreshold)
		feedbacks[worstIdx].Summary += note
		log.Printf("post-execution feedback: pass rate %.2f below threshold %.2f; annotating stage %s",
			passRate, pea.config.PassRateThreshold, feedbacks[worstIdx].NodeID)
	}

	return feedbacks
}

// ValidateFailedStagesWithPlayback attempts playback validation for the given
// failed node IDs. This agent CANNOT drive a live browser; it only has access to
// static recording snapshots that were captured during the original run.
//
// Therefore it never fabricates a success: SuccessStateMatch stays false unless
// real evidence confirms it (which, in a static-only world, it never does here).
// When no static snapshot artifact exists for a node, the result is marked
// "skipped" with a descriptive PlaybackError.
func (pea *PostExecutionAnalyzer) ValidateFailedStagesWithPlayback(
	ctx context.Context,
	validationContext *model.ValidationContext,
	failedStageIDs []string,
) ([]model.PlaybackValidationResult, error) {
	_ = ctx

	results := []model.PlaybackValidationResult{}
	if validationContext == nil {
		return results, fmt.Errorf("playback validation: validation context is nil")
	}
	if len(failedStageIDs) == 0 {
		return results, nil
	}

	stepByNode := postExecStepsByNode(validationContext)
	now := time.Now()

	for _, nodeID := range failedStageIDs {
		if nodeID == "" {
			continue
		}
		step := stepByNode[nodeID]
		passed := step != nil && strings.EqualFold(step.Status, "passed")
		snapshot := postExecFindSnapshotArtifact(step)

		result := model.PlaybackValidationResult{
			NodeID:            nodeID,
			PlaybackSessionID: fmt.Sprintf("static-playback-%s", nodeID),
			// SuccessStateMatch intentionally stays false: static snapshots alone
			// cannot confirm the success state of a failed stage.
			SuccessStateMatch: false,
			CreatedAt:         now,
			ValidationIssues:  []model.ValidationIssue{},
		}

		if snapshot == nil {
			result.PlaybackError = "playback session unavailable (static snapshots only)"
			result.OverallResult = "skipped"
			result.Confidence = 0
			result.ScreenshotValid = false
			result.ElementExists = false
			result.ValidationIssues = append(result.ValidationIssues, model.ValidationIssue{
				IssueID:     fmt.Sprintf("%s-playback-skipped", nodeID),
				Category:    "playback",
				Severity:    "major",
				Title:       "Playback unavailable",
				Description: "No static snapshot artifact (screenshot/dom/trace) is available for this node, and this agent cannot drive a live browser.",
				Expected:    "a static snapshot artifact or live playback session",
				Observed:    "no snapshot artifact",
				Recoverable: false,
				Blocker:     false,
			})
			results = append(results, result)
			continue
		}

		// A static snapshot exists. We may weakly confirm the screenshot is valid,
		// and infer element existence only when the originating step passed.
		result.ScreenshotValid = true
		result.ElementExists = passed
		result.OverallResult = "inconclusive"
		result.Confidence = postExecStaticPlaybackConfidence(passed)
		result.ValidationIssues = append(result.ValidationIssues, model.ValidationIssue{
			IssueID:     fmt.Sprintf("%s-playback-static-only", nodeID),
			Category:    "playback",
			Severity:    "minor",
			Title:       "Static-only evidence",
			Description: "Only a static snapshot is available; live page verification is required to confirm the success state.",
			Expected:    "live playback confirmation",
			Observed:    fmt.Sprintf("static artifact kind=%s", snapshot.Kind),
			Recoverable: true,
			Blocker:     false,
		})
		results = append(results, result)
	}

	log.Printf("playback validation: evaluated %d failed stage(s) (static snapshots only)", len(results))
	return results, nil
}

// GeneratePlaybackFeedback converts playback results into per-stage StageFeedback.
// For a failed stage the feedback is fine_tune or reunderstanding_required; it is
// NEVER continue unless playback explicitly confirmed SuccessStateMatch=true with
// Confidence > 0.
func (pea *PostExecutionAnalyzer) GeneratePlaybackFeedback(
	playbackResults []model.PlaybackValidationResult,
	validationContext *model.ValidationContext,
) []model.StageFeedback {
	feedbacks := make([]model.StageFeedback, 0, len(playbackResults))
	if validationContext == nil {
		return feedbacks
	}
	stageByNode := postExecStagesByNode(validationContext)
	now := time.Now()

	for i := range playbackResults {
		r := &playbackResults[i]
		stage := stageByNode[r.NodeID]

		feedbackType, riskLevel, blocked, blockReason := postExecClassifyPlayback(r)
		results := postExecPlaybackIssuesToResults(r.NodeID, r.ValidationIssues, r.Confidence, now)

		order := 0
		businessStageID := ""
		var evidence []model.EvidenceRef
		if stage != nil {
			order = stage.Order
			businessStageID = stage.BusinessStageID
			evidence = append(evidence, stage.EvidenceRefs...)
		}
		evidence = append(evidence, r.EvidenceRefs...)

		feedbacks = append(feedbacks, model.StageFeedback{
			NodeID:            r.NodeID,
			StageOrder:        order,
			BusinessStageID:   businessStageID,
			FeedbackType:      feedbackType,
			ValidationResults: results,
			Summary:           postExecPlaybackSummary(r),
			RiskLevel:         riskLevel,
			Confidence:        r.Confidence,
			RequiresRepair:    feedbackType == model.ValidationFeedbackFineTune,
			Blocked:           blocked,
			BlockReason:       blockReason,
			EvidenceRefs:      evidence,
			GeneratedAt:       now,
		})
	}

	return feedbacks
}

// ---------------------------------------------------------------------------
// Package-scoped helpers. All free functions are prefixed with postExec to avoid
// symbol collisions with sibling files (PreExecutionValidator, RealTimeBatchValidator,
// ValidationDiagnostics) being written concurrently in this package.
// ---------------------------------------------------------------------------

// postExecNodeByID is the canonical linear-scan node lookup (there is no node map
// or lookup method on *DemoWorkflowGraph). Mirrors scriptPackagerGraphNodeByID.
func postExecNodeByID(graph *model.DemoWorkflowGraph, id string) *model.GraphNode {
	if graph == nil || id == "" {
		return nil
	}
	for _, node := range graph.Nodes {
		if node != nil && node.ID == id {
			return node
		}
	}
	return nil
}

// postExecEffectiveSteps returns the step results that should drive analysis:
// the inline StepResults when present, otherwise the ExecutionTrace step results.
func postExecEffectiveSteps(validationContext *model.ValidationContext) []model.StepResult {
	if validationContext == nil {
		return nil
	}
	if len(validationContext.StepResults) > 0 {
		return validationContext.StepResults
	}
	if validationContext.ExecutionTrace != nil {
		return validationContext.ExecutionTrace.StepResults
	}
	return nil
}

// postExecStepsByNode indexes step results by NodeID. When multiple steps share a
// NodeID (e.g. retries), the last one wins as the most recent attempt.
func postExecStepsByNode(validationContext *model.ValidationContext) map[string]*model.StepResult {
	steps := postExecEffectiveSteps(validationContext)
	out := make(map[string]*model.StepResult)
	for i := range steps {
		if steps[i].NodeID == "" {
			continue
		}
		out[steps[i].NodeID] = &steps[i]
	}
	return out
}

// postExecStagesByNode indexes approval stages by NodeID (last wins).
func postExecStagesByNode(validationContext *model.ValidationContext) map[string]*model.StageApprovalStage {
	out := make(map[string]*model.StageApprovalStage)
	if validationContext == nil || validationContext.StageApprovalPlan == nil {
		return out
	}
	stages := validationContext.StageApprovalPlan.Stages
	for i := range stages {
		if stages[i].NodeID == "" {
			continue
		}
		out[stages[i].NodeID] = &stages[i]
	}
	return out
}

// postExecEvaluateMatches computes the five match booleans for a stage vs. a step.
// A nil step yields all-false matches (except vacuous-true cases where the stage
// declares no expectation for that dimension).
func postExecEvaluateMatches(stage *model.StageApprovalStage, step *model.StepResult) (status, state, route, artifact, duration bool) {
	// StatusMatch: step status indicates success.
	if step != nil {
		status = strings.EqualFold(step.Status, "passed")
	}

	successState, observed, expectedRoute := "", "", ""
	if stage != nil {
		successState = stage.SuccessState
		expectedRoute = stage.ExpectedRouteAfterAction
	}
	if step != nil {
		observed = step.ObservedState
	}

	// StateMatch: empty SuccessState -> vacuously true; otherwise substring match.
	if successState == "" {
		state = true
	} else if observed != "" {
		state = postExecSubstringMatchCI(observed, successState)
	}

	// RouteMatch: empty ExpectedRouteAfterAction -> vacuously true; otherwise
	// observed state must be consistent with the expected route.
	if expectedRoute == "" {
		route = true
	} else if observed != "" {
		route = postExecSubstringMatchCI(observed, expectedRoute)
	}

	// ArtifactMatch: required artifact kinds must be present in step.Artifacts.
	artifact = postExecArtifactMatch(stage, step)

	// DurationMatch: when stage.DurationMS > 0, step.DurationMS must lie in
	// [0.5*stage.DurationMS, 2*stage.DurationMS]; otherwise vacuously true.
	stageDurationMS := 0
	stepDurationMS := 0
	if stage != nil {
		stageDurationMS = stage.DurationMS
	}
	if step != nil {
		stepDurationMS = step.DurationMS
	}
	if stageDurationMS <= 0 {
		duration = true
	} else {
		duration = float64(stepDurationMS) >= 0.5*float64(stageDurationMS) &&
			float64(stepDurationMS) <= 2.0*float64(stageDurationMS)
	}
	return status, state, route, artifact, duration
}

// postExecArtifactMatch verifies that every required asset kind declared on the
// stage's CapturePlan is present among the step's artifacts (compared by Kind,
// since ArtifactRef has no Role field). A stage that demands no artifacts passes.
func postExecArtifactMatch(stage *model.StageApprovalStage, step *model.StepResult) bool {
	if stage == nil || stage.CapturePlan == nil || len(stage.CapturePlan.RequiredAssets) == 0 {
		return true
	}
	if step == nil || len(step.Artifacts) == 0 {
		return false
	}
	for _, required := range stage.CapturePlan.RequiredAssets {
		if required == "" {
			continue
		}
		found := false
		for _, a := range step.Artifacts {
			if postExecSubstringMatchCI(a.Kind, required) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// postExecCollectIssues turns the match booleans into a list of ValidationIssue
// records. When the step itself is missing, a single critical blocker is emitted
// and the remaining dimensions are not double-counted.
func postExecCollectIssues(
	stage *model.StageApprovalStage,
	step *model.StepResult,
	statusMatch, stateMatch, routeMatch, artifactMatch, durationMatch, hasStep bool,
) []model.ValidationIssue {
	issues := []model.ValidationIssue{}
	nodeID := ""
	if stage != nil {
		nodeID = stage.NodeID
	}

	if !hasStep {
		issues = append(issues, model.ValidationIssue{
			IssueID:     fmt.Sprintf("%s-missing-step", nodeID),
			Category:    "execution",
			Severity:    "critical",
			Title:       "No step result for stage",
			Description: "No execution step result was recorded for this stage's node; cannot confirm the stage ran.",
			Expected:    "a passed step result matching node_id",
			Observed:    "no step result",
			Recoverable: false,
			Blocker:     true,
		})
		return issues
	}

	if !statusMatch {
		errMsg := ""
		retryable := false
		if step != nil && step.Error != nil {
			errMsg = step.Error.Message
			retryable = step.Error.Retryable
		}
		description := "Step status was not 'passed'."
		if errMsg != "" {
			description = fmt.Sprintf("Step status was not 'passed': %s", errMsg)
		}
		issues = append(issues, model.ValidationIssue{
			IssueID:     fmt.Sprintf("%s-status", nodeID),
			Category:    "execution",
			Severity:    "critical",
			Title:       "Step did not pass",
			Description: description,
			Expected:    "passed",
			Observed:    postExecStatusOrEmpty(step),
			Recoverable: retryable,
			Blocker:     true,
		})
	}

	if !stateMatch {
		expected, observed := "", ""
		if stage != nil {
			expected = stage.SuccessState
		}
		if step != nil {
			observed = step.ObservedState
		}
		issues = append(issues, model.ValidationIssue{
			IssueID:     fmt.Sprintf("%s-state", nodeID),
			Category:    "state",
			Severity:    "major",
			Title:       "Success state not observed",
			Description: "The declared success state was not reflected in the observed step state.",
			Expected:    expected,
			Observed:    observed,
			Recoverable: true,
			Blocker:     false,
		})
	}

	if !routeMatch {
		expected, observed := "", ""
		if stage != nil {
			expected = stage.ExpectedRouteAfterAction
		}
		if step != nil {
			observed = step.ObservedState
		}
		issues = append(issues, model.ValidationIssue{
			IssueID:     fmt.Sprintf("%s-route", nodeID),
			Category:    "route",
			Severity:    "major",
			Title:       "Expected route not observed",
			Description: "The expected post-action route was not present in the observed state.",
			Expected:    expected,
			Observed:    observed,
			Recoverable: true,
			Blocker:     false,
		})
	}

	if !artifactMatch {
		issues = append(issues, model.ValidationIssue{
			IssueID:     fmt.Sprintf("%s-artifact", nodeID),
			Category:    "artifact",
			Severity:    "minor",
			Title:       "Required artifacts missing",
			Description: "One or more required capture assets were not produced for this stage.",
			Expected:    postExecRequiredAssetsString(stage),
			Observed:    postExecArtifactKindsString(step),
			Recoverable: true,
			Blocker:     false,
		})
	}

	if !durationMatch {
		expected, observed := "", ""
		if stage != nil && stage.DurationMS > 0 {
			expected = fmt.Sprintf("%dms (within [%.0f, %.0f])", stage.DurationMS, 0.5*float64(stage.DurationMS), 2.0*float64(stage.DurationMS))
		}
		if step != nil {
			observed = fmt.Sprintf("%dms", step.DurationMS)
		}
		issues = append(issues, model.ValidationIssue{
			IssueID:     fmt.Sprintf("%s-duration", nodeID),
			Category:    "duration",
			Severity:    "minor",
			Title:       "Duration outside expected band",
			Description: "The step duration fell outside the 0.5x-2x band of the stage's expected duration.",
			Expected:    expected,
			Observed:    observed,
			Recoverable: true,
			Blocker:     false,
		})
	}

	return issues
}

// postExecMatchConfidence rolls the five match booleans into a [0,1] confidence.
// Status dominates (40%); state/route/artifact/duration carry the remainder.
func postExecMatchConfidence(status, state, route, artifact, duration bool) float64 {
	score := 0.0
	if status {
		score += 0.40
	}
	if state {
		score += 0.25
	}
	if route {
		score += 0.15
	}
	if artifact {
		score += 0.10
	}
	if duration {
		score += 0.10
	}
	return score
}

// postExecAssess derives a human-readable OverallAssessment and Recommendation
// from the observed issues.
func postExecAssess(issues []model.ValidationIssue) (string, string) {
	if len(issues) == 0 {
		return "passed", "continue"
	}
	hasCritical, hasMajor, hasMinor := false, false, false
	for _, iss := range issues {
		switch strings.ToLower(iss.Severity) {
		case "critical":
			hasCritical = true
		case "major":
			hasMajor = true
		case "minor":
			hasMinor = true
		}
	}
	switch {
	case hasCritical:
		return "failed", "halt and review before continuing"
	case hasMajor:
		return "passed_with_issues", "investigate and apply runtime repairs"
	case hasMinor:
		return "passed_with_warnings", "monitor; minor repairs optional"
	}
	return "passed", "continue"
}

// postExecClassifyAnalysis maps a stage's observed issues to a feedback type per
// the contract: blocker/critical -> reunderstanding_required; recoverable major ->
// fine_tune; otherwise continue. RiskLevel strings ("critical"/"major") align with
// StageValidationAgent.determineGlobalFeedbackType.
func postExecClassifyAnalysis(issues []model.ValidationIssue) (feedbackType, riskLevel string, blocked bool, blockReason string) {
	hasCriticalBlocker := false
	hasMajorRecoverable := false
	hasMinor := false
	for _, iss := range issues {
		if iss.Blocker {
			hasCriticalBlocker = true
		}
		switch strings.ToLower(iss.Severity) {
		case "critical":
			hasCriticalBlocker = true
		case "major":
			if iss.Recoverable {
				hasMajorRecoverable = true
			}
		case "minor":
			hasMinor = true
		}
	}
	switch {
	case hasCriticalBlocker:
		return model.ValidationFeedbackReunderstandingRequired, "critical", true, "critical issue or blocker detected; reunderstanding required"
	case hasMajorRecoverable:
		return model.ValidationFeedbackFineTune, "major", false, ""
	case hasMinor:
		return model.ValidationFeedbackContinue, "minor", false, ""
	}
	return model.ValidationFeedbackContinue, "low", false, ""
}

// postExecClassifyPlayback maps a playback result to a feedback type. Continue is
// only returned when playback explicitly confirmed SuccessStateMatch with positive
// confidence; otherwise a failed stage yields fine_tune or reunderstanding_required.
func postExecClassifyPlayback(r *model.PlaybackValidationResult) (feedbackType, riskLevel string, blocked bool, blockReason string) {
	if r.SuccessStateMatch && r.Confidence > 0 {
		return model.ValidationFeedbackContinue, "low", false, ""
	}
	if r.OverallResult == "skipped" {
		return model.ValidationFeedbackReunderstandingRequired, "critical", true, "playback evidence unavailable; cannot confirm recovery, reunderstanding required"
	}
	return model.ValidationFeedbackFineTune, "major", false, ""
}

// postExecIssuesToResults converts ValidationIssue records into ValidationResult
// records for the given phase. When there are no issues, a single "passed" result
// is emitted so consumers can see the stage was checked.
func postExecIssuesToResults(
	nodeID string,
	phase model.LegacyValidationPhase,
	issues []model.ValidationIssue,
	confidence float64,
	now time.Time,
) []model.ValidationResult {
	results := make([]model.ValidationResult, 0, len(issues)+1)
	if len(issues) == 0 {
		results = append(results, model.ValidationResult{
			ID:          fmt.Sprintf("%s-passed", nodeID),
			NodeID:      nodeID,
			Phase:       phase,
			Type:        model.ValidationResultTypePassed,
			Title:       "All checks passed",
			Description: "Analysis found no issues for this stage.",
			Confidence:  confidence,
			Timestamp:   now,
		})
		return results
	}
	for _, iss := range issues {
		resultType := model.ValidationResultTypeWarning
		critical := false
		switch strings.ToLower(iss.Severity) {
		case "critical":
			resultType = model.ValidationResultTypeFailed
			critical = true
		case "major":
			resultType = model.ValidationResultTypeFailed
		case "minor":
			resultType = model.ValidationResultTypeWarning
		}
		results = append(results, model.ValidationResult{
			ID:            iss.IssueID,
			NodeID:        nodeID,
			Phase:         phase,
			Type:          resultType,
			Title:         iss.Title,
			Description:   iss.Description,
			ExpectedValue: iss.Expected,
			ObservedValue: iss.Observed,
			Confidence:    confidence,
			Critical:      critical,
			Blocker:       iss.Blocker,
			EvidenceRefs:  iss.EvidenceRefs,
			Timestamp:     now,
		})
	}
	return results
}

// postExecPlaybackIssuesToResults converts playback ValidationIssue records into
// ValidationResult records. Unlike postExecIssuesToResults it never synthesizes a
// "passed" result, because playback here only runs for failed stages.
func postExecPlaybackIssuesToResults(
	nodeID string,
	issues []model.ValidationIssue,
	confidence float64,
	now time.Time,
) []model.ValidationResult {
	results := make([]model.ValidationResult, 0, len(issues)+1)
	for _, iss := range issues {
		resultType := model.ValidationResultTypeWarning
		critical := false
		switch strings.ToLower(iss.Severity) {
		case "critical":
			resultType = model.ValidationResultTypeFailed
			critical = true
		case "major":
			resultType = model.ValidationResultTypeFailed
		}
		results = append(results, model.ValidationResult{
			ID:            iss.IssueID,
			NodeID:        nodeID,
			Phase:         model.LegacyValidationPhasePlayback,
			Type:          resultType,
			Title:         iss.Title,
			Description:   iss.Description,
			ExpectedValue: iss.Expected,
			ObservedValue: iss.Observed,
			Confidence:    confidence,
			Critical:      critical,
			Blocker:       iss.Blocker,
			EvidenceRefs:  iss.EvidenceRefs,
			Timestamp:     now,
		})
	}
	return results
}

// postExecPlaybackSummary renders a short human summary of a playback result.
func postExecPlaybackSummary(r *model.PlaybackValidationResult) string {
	if r.PlaybackError != "" {
		return fmt.Sprintf("playback skipped: %s", r.PlaybackError)
	}
	return fmt.Sprintf("playback %s (static-only; success_state_match=%t)", r.OverallResult, r.SuccessStateMatch)
}

// postExecWorstRank returns the highest single-issue severity rank (0..3) and the
// total issue count, used to pick the worst-affected stage for annotation.
func postExecWorstRank(issues []model.ValidationIssue) (rank, issueCount int) {
	issueCount = len(issues)
	for _, iss := range issues {
		if r := postExecSeverityRank(iss.Severity); r > rank {
			rank = r
		}
	}
	return rank, issueCount
}

// postExecSeverityRank maps severity strings to an ordering rank.
func postExecSeverityRank(severity string) int {
	switch strings.ToLower(severity) {
	case "critical":
		return 3
	case "major":
		return 2
	case "minor":
		return 1
	}
	return 0
}

// postExecFindSnapshotArtifact returns the first step artifact that looks like a
// static page snapshot (screenshot / dom / trace / recording).
func postExecFindSnapshotArtifact(step *model.StepResult) *model.ArtifactRef {
	if step == nil {
		return nil
	}
	for i := range step.Artifacts {
		if postExecIsSnapshotArtifact(step.Artifacts[i]) {
			return &step.Artifacts[i]
		}
	}
	return nil
}

// postExecIsSnapshotArtifact classifies an artifact Kind as a static snapshot.
func postExecIsSnapshotArtifact(a model.ArtifactRef) bool {
	kind := strings.ToLower(a.Kind)
	switch {
	case strings.Contains(kind, "screenshot"),
		strings.Contains(kind, "snapshot"),
		strings.Contains(kind, "dom"),
		strings.Contains(kind, "trace"),
		strings.Contains(kind, "recording"):
		return true
	}
	return false
}

// postExecStaticPlaybackConfidence returns the weak confidence ceiling for a
// static-only playback assessment (always below the 0.5 ConfidenceThreshold).
func postExecStaticPlaybackConfidence(passed bool) float64 {
	if passed {
		return 0.35
	}
	return 0.25
}

// postExecSubstringMatchCI reports whether either string contains the other
// (case-insensitive). Empty inputs never match.
func postExecSubstringMatchCI(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	la := strings.ToLower(a)
	lb := strings.ToLower(b)
	return strings.Contains(la, lb) || strings.Contains(lb, la)
}

// postExecStatusOrEmpty returns the step status for observation, normalizing the
// empty string to a visible sentinel.
func postExecStatusOrEmpty(step *model.StepResult) string {
	if step == nil {
		return ""
	}
	if step.Status == "" {
		return "(empty)"
	}
	return step.Status
}

// postExecRequiredAssetsString renders a stage's required asset kinds.
func postExecRequiredAssetsString(stage *model.StageApprovalStage) string {
	if stage == nil || stage.CapturePlan == nil || len(stage.CapturePlan.RequiredAssets) == 0 {
		return "(none required)"
	}
	return strings.Join(stage.CapturePlan.RequiredAssets, ", ")
}

// postExecArtifactKindsString renders the kinds of artifacts a step produced.
func postExecArtifactKindsString(step *model.StepResult) string {
	if step == nil || len(step.Artifacts) == 0 {
		return "(none produced)"
	}
	kinds := make([]string, 0, len(step.Artifacts))
	for _, a := range step.Artifacts {
		if a.Kind != "" {
			kinds = append(kinds, a.Kind)
		}
	}
	if len(kinds) == 0 {
		return "(none produced)"
	}
	return strings.Join(kinds, ", ")
}
