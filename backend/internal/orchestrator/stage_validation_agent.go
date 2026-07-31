package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"cascade-demoops/backend/internal/model"
)

// StageValidationAgent - Main validation orchestrator for A mode (Orchestrator layer)
// Handles three-phase validation: pre-execution, real-time batch, post-execution batch
type StageValidationAgent struct {
	config         *model.ValidationConfig
	preValidator   *PreExecutionValidator
	batchValidator *RealTimeBatchValidator
	postAnalyzer   *PostExecutionAnalyzer
	diagnostics    *ValidationDiagnostics
}

// NewStageValidationAgent - Create a new stage validation agent
func NewStageValidationAgent(config *model.ValidationConfig) *StageValidationAgent {
	if config == nil {
		config = DefaultValidationConfig()
	}

	agent := &StageValidationAgent{
		config: config,
	}

	// Initialize sub-components
	agent.preValidator = NewPreExecutionValidator(agent.config)
	agent.batchValidator = NewRealTimeBatchValidator(agent.config)
	agent.postAnalyzer = NewPostExecutionAnalyzer(agent.config)
	agent.diagnostics = NewValidationDiagnostics(agent.config)

	return agent
}

// DefaultValidationConfig - Create default validation configuration
func DefaultValidationConfig() *model.ValidationConfig {
	return &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
		PlaybackValidationEnabled: false, // Disabled by default (requires local playback)
		PassRateThreshold:         0.9,
		ConfidenceThreshold:       0.5,
		CriticalIssueThreshold:    1,
		EnableRuntimeRepair:       true,
		AutoApplyMinorRepairs:     true,
		MaxRepairAttemptsPerStage: 2,
		ParallelValidationEnabled: true,
		ValidationTimeoutSeconds:  300,
	}
}

// ValidateBeforeExecution - Phase 1: Pre-execution validation
// Checks: domain accessibility, selector existence, evidence requirements, blocking uncertainties
func (sva *StageValidationAgent) ValidateBeforeExecution(
	ctx context.Context,
	validationContext *model.ValidationContext,
) (*model.LegacyValidationReport, error) {
	if !sva.config.PreExecutionEnabled {
		log.Printf("Pre-execution validation disabled, skipping")
		return sva.createSkippedReport(model.LegacyValidationPhasePreExecution, validationContext), nil
	}

	log.Printf("Starting pre-execution validation for package %s", validationContext.PackageID)
	startTime := time.Now()

	// Execute pre-validation checks
	checks, err := sva.preValidator.ExecutePreValidationChecks(ctx, validationContext)
	if err != nil {
		return nil, fmt.Errorf("pre-validation execution failed: %w", err)
	}

	// Analyze checks and generate feedback
	stageFeedbacks, err := sva.preValidator.AnalyzePreValidationResults(ctx, checks, validationContext)
	if err != nil {
		return nil, fmt.Errorf("pre-validation analysis failed: %w", err)
	}

	// Determine global feedback type
	globalFeedbackType := sva.determineGlobalFeedbackType(stageFeedbacks)

	// Create validation report
	report := &model.LegacyValidationReport{
		ID:                  sva.generateReportID(validationContext.PackageID, model.LegacyValidationPhasePreExecution),
		ValidationContextID: validationContext.PackageID,
		Phase:               model.LegacyValidationPhasePreExecution,
		StageFeedbacks:      stageFeedbacks,
		GlobalFeedbackType:  globalFeedbackType,
		TotalStages:         sva.countStages(validationContext),
		CreatedAt:           time.Now(),
	}

	// Calculate statistics
	sva.calculateReportStatistics(report)

	duration := time.Since(startTime).Milliseconds()
	log.Printf("Pre-execution validation completed in %dms - Feedback: %s", duration, globalFeedbackType)

	return report, nil
}

// ValidateRealTimeBatch - Phase 2: Real-time batch validation during execution
// Validates step results as they complete in batches
func (sva *StageValidationAgent) ValidateRealTimeBatch(
	ctx context.Context,
	validationContext *model.ValidationContext,
	completedStepResults []model.StepResult,
) (*model.LegacyValidationReport, error) {
	if !sva.config.RealTimeBatchEnabled {
		log.Printf("Real-time batch validation disabled, skipping")
		return sva.createSkippedReport(model.LegacyValidationPhaseRealTimeBatch, validationContext), nil
	}

	log.Printf("Starting real-time batch validation for %d completed steps", len(completedStepResults))
	startTime := time.Now()

	// Validate completed steps in batch
	validationResults, err := sva.batchValidator.ValidateStepResults(ctx, completedStepResults, validationContext)
	if err != nil {
		return nil, fmt.Errorf("real-time batch validation failed: %w", err)
	}

	// Generate stage feedback
	stageFeedbacks := sva.batchValidator.GenerateStageFeedback(validationResults, validationContext)

	// Apply runtime repairs if enabled
	if sva.config.EnableRuntimeRepair && sva.config.AutoApplyMinorRepairs {
		appliedRepairs := sva.batchValidator.ApplyRuntimeRepairs(ctx, stageFeedbacks)
		for i, feedback := range stageFeedbacks {
			stageFeedbacks[i].RepairsApplied = appliedRepairs[feedback.NodeID]
		}
	}

	// Determine global feedback type
	globalFeedbackType := sva.determineGlobalFeedbackType(stageFeedbacks)

	// Create validation report
	report := &model.LegacyValidationReport{
		ID:                  sva.generateReportID(validationContext.PackageID, model.LegacyValidationPhaseRealTimeBatch),
		ValidationContextID: validationContext.PackageID,
		Phase:               model.LegacyValidationPhaseRealTimeBatch,
		StageFeedbacks:      stageFeedbacks,
		GlobalFeedbackType:  globalFeedbackType,
		TotalStages:         sva.countStages(validationContext),
		CreatedAt:           time.Now(),
	}

	// Calculate statistics
	sva.calculateReportStatistics(report)

	duration := time.Since(startTime).Milliseconds()
	log.Printf("Real-time batch validation completed in %dms - Feedback: %s", duration, globalFeedbackType)

	return report, nil
}

// ValidatePostExecution - Phase 3: Post-execution batch analysis
// Comprehensive analysis of all step results against expected outcomes
func (sva *StageValidationAgent) ValidatePostExecution(
	ctx context.Context,
	validationContext *model.ValidationContext,
) (*model.LegacyValidationReport, error) {
	if !sva.config.PostExecutionBatchEnabled {
		log.Printf("Post-execution validation disabled, skipping")
		return sva.createSkippedReport(model.LegacyValidationPhasePostExecutionBatch, validationContext), nil
	}

	log.Printf("Starting post-execution validation for package %s", validationContext.PackageID)
	startTime := time.Now()

	// Perform comprehensive post-execution analysis
	analyses, err := sva.postAnalyzer.AnalyzeExecutionResults(ctx, validationContext)
	if err != nil {
		return nil, fmt.Errorf("post-execution analysis failed: %w", err)
	}

	// Generate comprehensive stage feedback
	stageFeedbacks := sva.postAnalyzer.GenerateComprehensiveFeedback(analyses, validationContext)

	// Determine global feedback type
	globalFeedbackType := sva.determineGlobalFeedbackType(stageFeedbacks)

	// Generate recommendation
	recommendation := sva.diagnostics.GenerateRecommendation(stageFeedbacks, globalFeedbackType)

	// Collect evidence references
	evidenceRefs := sva.diagnostics.CollectEvidenceReferences(stageFeedbacks, validationContext)

	// Create validation report
	report := &model.LegacyValidationReport{
		ID:                  sva.generateReportID(validationContext.PackageID, model.LegacyValidationPhasePostExecutionBatch),
		ValidationContextID: validationContext.PackageID,
		Phase:               model.LegacyValidationPhasePostExecutionBatch,
		StageFeedbacks:      stageFeedbacks,
		GlobalFeedbackType:  globalFeedbackType,
		Recommendation:      recommendation,
		EvidenceRefs:        evidenceRefs,
		TotalStages:         sva.countStages(validationContext),
		CreatedAt:           time.Now(),
	}

	// Calculate statistics
	sva.calculateReportStatistics(report)

	duration := time.Since(startTime).Milliseconds()
	log.Printf("Post-execution validation completed in %dms - Feedback: %s", duration, globalFeedbackType)

	return report, nil
}

// ValidatePlayback - Phase 4: Playback validation (optional, local only)
// Uses static snapshots from recording to verify page states
func (sva *StageValidationAgent) ValidatePlayback(
	ctx context.Context,
	validationContext *model.ValidationContext,
	failedStageIDs []string,
) (*model.LegacyValidationReport, error) {
	if !sva.config.PlaybackValidationEnabled {
		log.Printf("Playback validation disabled, skipping")
		return nil, errors.New("playback validation is not enabled")
	}

	log.Printf("Starting playback validation for %d failed stages", len(failedStageIDs))
	startTime := time.Now()

	// Perform playback validation on failed stages
	playbackResults, err := sva.postAnalyzer.ValidateFailedStagesWithPlayback(ctx, validationContext, failedStageIDs)
	if err != nil {
		return nil, fmt.Errorf("playback validation failed: %w", err)
	}

	// Generate stage feedback from playback results
	stageFeedbacks := sva.postAnalyzer.GeneratePlaybackFeedback(playbackResults, validationContext)

	// Determine global feedback type
	globalFeedbackType := sva.determineGlobalFeedbackType(stageFeedbacks)

	// Create validation report
	report := &model.LegacyValidationReport{
		ID:                  sva.generateReportID(validationContext.PackageID, model.LegacyValidationPhasePlayback),
		ValidationContextID: validationContext.PackageID,
		Phase:               model.LegacyValidationPhasePlayback,
		StageFeedbacks:      stageFeedbacks,
		GlobalFeedbackType:  globalFeedbackType,
		TotalStages:         sva.countStages(validationContext),
		CreatedAt:           time.Now(),
	}

	// Calculate statistics
	sva.calculateReportStatistics(report)

	duration := time.Since(startTime).Milliseconds()
	log.Printf("Playback validation completed in %dms - Feedback: %s", duration, globalFeedbackType)

	return report, nil
}

// DetermineExecutionDecision - Determine whether to continue, fine-tune, or rollback
func (sva *StageValidationAgent) DetermineExecutionDecision(
	ctx context.Context,
	reports []*model.LegacyValidationReport,
) (string, []model.BlockReason, error) {
	if len(reports) == 0 {
		return model.ValidationFeedbackContinue, nil, nil
	}

	// Collect all blocking reasons across all reports
	var blockingReasons []model.BlockReason
	var criticalIssueCount int
	var majorIssueCount int

	for _, report := range reports {
		for _, stageFeedback := range report.StageFeedbacks {
			if stageFeedback.Blocked {
				blockingReasons = append(blockingReasons, model.BlockReason{
					ReasonCode: stageFeedback.FeedbackType,
					Message:    stageFeedback.BlockReason,
					NodeID:     stageFeedback.NodeID,
					Severity:   "blocking",
				})
			}

			// Count issues by severity
			for _, result := range stageFeedback.ValidationResults {
				if result.Critical {
					criticalIssueCount++
				} else if result.Type == model.ValidationResultTypeFailed {
					majorIssueCount++
				}
			}
		}
	}

	// Check if critical issue threshold exceeded
	if criticalIssueCount >= sva.config.CriticalIssueThreshold {
		log.Printf("Critical issue threshold exceeded: %d >= %d", criticalIssueCount, sva.config.CriticalIssueThreshold)
		return model.ValidationFeedbackReunderstandingRequired, blockingReasons, nil
	}

	// Check if there are any blocking reasons
	if len(blockingReasons) > 0 {
		log.Printf("Blocking reasons detected: %d", len(blockingReasons))
		return model.ValidationFeedbackReunderstandingRequired, blockingReasons, nil
	}

	// Check if major issues exist that need fine-tuning
	if majorIssueCount > 0 {
		log.Printf("Major issues detected, recommending fine-tune: %d", majorIssueCount)
		return model.ValidationFeedbackFineTune, blockingReasons, nil
	}

	// Check if any stage has low confidence.
	// Skip non-participating (e.g. disabled-phase) reports: they carry zero-
	// valued OverallConfidence and would otherwise force a spurious fine_tune
	// despite their own GlobalFeedbackType being "continue".
	for _, report := range reports {
		if len(report.StageFeedbacks) == 0 {
			continue
		}
		if report.OverallConfidence < sva.config.ConfidenceThreshold {
			log.Printf("Low overall confidence detected: %.2f < %.2f", report.OverallConfidence, sva.config.ConfidenceThreshold)
			return model.ValidationFeedbackFineTune, blockingReasons, nil
		}
	}

	// Check if pass rate is below threshold.
	// Skip real_time_batch reports: their PassRate is computed against the full
	// plan (report.TotalStages) while the numerator only reflects completed
	// stages, so it measures progress, not quality. Real-time quality failures
	// are already caught by the critical/blocking/major/confidence checks
	// above. Reserve this threshold for reports where every plan stage has been
	// evaluated (e.g. post_execution_batch).
	for _, report := range reports {
		if len(report.StageFeedbacks) == 0 || report.Phase == model.LegacyValidationPhaseRealTimeBatch {
			continue
		}
		if report.PassRate < sva.config.PassRateThreshold {
			log.Printf("Pass rate below threshold: %.2f < %.2f", report.PassRate, sva.config.PassRateThreshold)
			return model.ValidationFeedbackFineTune, blockingReasons, nil
		}
	}

	// All checks passed
	log.Printf("All validation checks passed, continuing execution")
	return model.ValidationFeedbackContinue, blockingReasons, nil
}

// GenerateDiagnosticReport - Generate comprehensive diagnostic report for rollback
func (sva *StageValidationAgent) GenerateDiagnosticReport(
	ctx context.Context,
	reports []*model.LegacyValidationReport,
	validationContext *model.ValidationContext,
) (*model.ScriptFailureDiagnostic, error) {
	log.Printf("Generating diagnostic report for package %s", validationContext.PackageID)

	// Collect all validation results
	var allResults []model.ValidationResult
	for _, report := range reports {
		for _, stageFeedback := range report.StageFeedbacks {
			allResults = append(allResults, stageFeedback.ValidationResults...)
		}
	}

	// Find critical and major issues
	var criticalIssues []model.ValidationResult
	var majorIssues []model.ValidationResult
	for _, result := range allResults {
		if result.Critical {
			criticalIssues = append(criticalIssues, result)
		} else if result.Type == model.ValidationResultTypeFailed {
			majorIssues = append(majorIssues, result)
		}
	}

	// Generate repair hints
	repairHints := sva.diagnostics.GenerateRepairHints(criticalIssues, majorIssues, validationContext)

	// Construct diagnostic
	diagnostic := &model.ScriptFailureDiagnostic{
		ID:              sva.generateDiagnosticID(validationContext.PackageID),
		SchemaVersion:   model.ScriptFailureDiagnosticSchemaVersion,
		SourcePackageID: validationContext.PackageID,
		CloudJobID:      validationContext.CloudJobID,
		FailedNodeID:    sva.findFirstFailedNodeID(reports),
		Error: model.AgentError{
			Code:    "validation_failed",
			Message: sva.generateErrorMessage(criticalIssues, majorIssues),
		},
		RepairHints: repairHints,
		CapturedAt:  time.Now(),
	}

	return diagnostic, nil
}

// Helper methods

func (sva *StageValidationAgent) determineGlobalFeedbackType(stageFeedbacks []model.StageFeedback) string {
	hasBlocking := false
	hasCritical := false
	hasMajor := false

	for _, feedback := range stageFeedbacks {
		if feedback.Blocked {
			hasBlocking = true
		}
		if feedback.RiskLevel == "critical" {
			hasCritical = true
		} else if feedback.RiskLevel == "major" || feedback.FeedbackType == model.ValidationFeedbackFineTune {
			// "major" is the canonical RiskLevel for fine-tune stages, but the
			// pre-execution and real-time-batch emitters use "medium"/"high".
			// FeedbackType is the authoritative per-stage verdict, so honor it
			// too; fine_tune stages are never Blocked, so this does not affect
			// the blocking/critical escalation above.
			hasMajor = true
		}
	}

	if hasBlocking || hasCritical {
		return model.ValidationFeedbackReunderstandingRequired
	} else if hasMajor {
		return model.ValidationFeedbackFineTune
	}
	return model.ValidationFeedbackContinue
}

func (sva *StageValidationAgent) calculateReportStatistics(report *model.LegacyValidationReport) {
	report.PassedStages = 0
	report.FailedStages = 0
	report.WarningStages = 0
	report.CriticalIssues = 0
	report.BlockerIssues = 0
	totalConfidence := 0.0
	confidenceCount := 0

	for _, feedback := range report.StageFeedbacks {
		if feedback.Blocked {
			report.FailedStages++
		} else if feedback.FeedbackType == model.ValidationFeedbackContinue {
			report.PassedStages++
		} else if feedback.FeedbackType == model.ValidationFeedbackFineTune {
			report.WarningStages++
		} else {
			report.FailedStages++
		}

		for _, result := range feedback.ValidationResults {
			if result.Critical {
				report.CriticalIssues++
			}
			if result.Blocker {
				report.BlockerIssues++
			}
		}

		if feedback.Confidence > 0 {
			totalConfidence += feedback.Confidence
			confidenceCount++
		}
	}

	// Calculate overall confidence
	if confidenceCount > 0 {
		report.OverallConfidence = totalConfidence / float64(confidenceCount)
	}

	// Calculate pass rate
	if report.TotalStages > 0 {
		report.PassRate = float64(report.PassedStages) / float64(report.TotalStages)
	}
}

func (sva *StageValidationAgent) generateReportID(packageID string, phase model.LegacyValidationPhase) string {
	timestamp := time.Now().Format("20060102-150405")
	return fmt.Sprintf("val-%s-%s-%s", packageID, phase, timestamp)
}

func (sva *StageValidationAgent) generateDiagnosticID(packageID string) string {
	return fmt.Sprintf("diag-%s-%d", packageID, time.Now().Unix())
}

func (sva *StageValidationAgent) createSkippedReport(phase model.LegacyValidationPhase, validationContext *model.ValidationContext) *model.LegacyValidationReport {
	return &model.LegacyValidationReport{
		ID:                  sva.generateReportID(validationContext.PackageID, phase),
		ValidationContextID: validationContext.PackageID,
		Phase:               phase,
		StageFeedbacks:      []model.StageFeedback{},
		GlobalFeedbackType:  model.ValidationFeedbackContinue,
		Summary:             "Validation skipped (disabled in configuration)",
		TotalStages:         sva.countStages(validationContext),
		CreatedAt:           time.Now(),
	}
}

func (sva *StageValidationAgent) findFirstFailedNodeID(reports []*model.LegacyValidationReport) string {
	for _, report := range reports {
		for _, feedback := range report.StageFeedbacks {
			if feedback.Blocked || feedback.FeedbackType == model.ValidationFeedbackReunderstandingRequired {
				return feedback.NodeID
			}
		}
	}
	return ""
}

func (sva *StageValidationAgent) generateErrorMessage(criticalIssues, majorIssues []model.ValidationResult) string {
	if len(criticalIssues) > 0 {
		return fmt.Sprintf("Validation failed with %d critical issues. First issue: %s", len(criticalIssues), criticalIssues[0].Description)
	} else if len(majorIssues) > 0 {
		return fmt.Sprintf("Validation failed with %d major issues. First issue: %s", len(majorIssues), majorIssues[0].Description)
	}
	return "Validation failed"
}

// countStages returns the number of stages in the validation context's approval
// plan, nil-safe. The StageApprovalPlan pointer is nullable on ValidationContext,
// and the disabled-phase code path reaches TotalStages via createSkippedReport
// before any real work, so every dereference must go through this helper.
func (sva *StageValidationAgent) countStages(validationContext *model.ValidationContext) int {
	if validationContext == nil || validationContext.StageApprovalPlan == nil {
		return 0
	}
	return len(validationContext.StageApprovalPlan.Stages)
}

// GetValidationContext - Build validation context from execution package
func (sva *StageValidationAgent) GetValidationContext(
	packageID string,
	workflowGraph *model.DemoWorkflowGraph,
	recordingRunSpec model.RecordingRunSpec,
	stageApprovalPlan *model.StageApprovalPlan,
	cloudJobID string,
) *model.ValidationContext {
	return &model.ValidationContext{
		PackageID:         packageID,
		WorkflowGraph:     workflowGraph,
		RecordingRunSpec:  recordingRunSpec,
		StageApprovalPlan: stageApprovalPlan,
		CloudJobID:        cloudJobID,
		CreatedAt:         time.Now(),
	}
}

// ExportValidationReportJSON - Export validation report as JSON
func (sva *StageValidationAgent) ExportValidationReportJSON(report *model.LegacyValidationReport) ([]byte, error) {
	return json.MarshalIndent(report, "", "  ")
}

// ImportValidationReportJSON - Import validation report from JSON
func (sva *StageValidationAgent) ImportValidationReportJSON(data []byte) (*model.LegacyValidationReport, error) {
	var report model.LegacyValidationReport
	err := json.Unmarshal(data, &report)
	if err != nil {
		return nil, fmt.Errorf("failed to parse validation report JSON: %w", err)
	}
	return &report, nil
}
