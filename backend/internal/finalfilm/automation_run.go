package finalfilm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

func (s *Service) AuthorizeAutomation(ctx context.Context, jobID string, expectedRevision int, authorizationRef string, maxProviderCalls int) (model.FinalFilmJob, error) {
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	if job.Revision != expectedRevision {
		return model.FinalFilmJob{}, fmt.Errorf("%w: expected %d current %d", ErrRevisionConflict, expectedRevision, job.Revision)
	}
	if job.AutomationProfile != model.FinalFilmAutomationProfileGuidedDemoV1 || job.AutomationPolicy == nil {
		return model.FinalFilmJob{}, errors.New("automatic run requires guided-demo-v1")
	}
	if job.State != model.FinalFilmJobBaselineReady && job.State != model.FinalFilmJobRevisionRequested {
		return model.FinalFilmJob{}, errors.New("automatic run can start only from baseline_ready or revision_requested")
	}
	authorizationRef = strings.TrimSpace(authorizationRef)
	if authorizationRef == "" {
		return model.FinalFilmJob{}, errors.New("authorization_ref is required")
	}
	if maxProviderCalls <= 0 || maxProviderCalls > job.AutomationPolicy.MaxProviderCalls {
		return model.FinalFilmJob{}, errors.New("provider call budget exceeds the server policy")
	}
	next := job
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	next.State = model.FinalFilmJobAnalyzingEvidence
	next.Phase = "automation_authorized"
	next.RunAuthorization = &model.FinalFilmRunAuthorization{AuthorizationRef: authorizationRef, AuthorizedAt: next.UpdatedAt, MaxProviderCalls: maxProviderCalls}
	next.GenerationAuthorized = false
	next.GenerationAuthorizationRef = ""
	next.FinalReview = nil
	if err := s.store.TransitionJob(ctx, job.JobID, job.Revision, next, s.event(next, next.Phase, "已记录一次启动与费用授权，自动成片流程开始", map[string]any{"max_provider_calls": maxProviderCalls})); err != nil {
		return model.FinalFilmJob{}, err
	}
	return next, nil
}

// ResumeAutomation advances durable phases only. Provider admissions and
// idempotency keys are written before an external call, so a worker restart
// resumes the same paid attempt instead of silently granting a new one.
func (s *Service) ResumeAutomation(ctx context.Context, jobID string) (model.FinalFilmJob, error) {
	for {
		job, err := s.store.GetJob(ctx, jobID)
		if err != nil {
			return model.FinalFilmJob{}, err
		}
		switch job.State {
		case model.FinalFilmJobAnalyzingEvidence:
			if job.BaselineRender.VideoPath == "" {
				if _, err = s.RunBaseline(ctx, jobID); err != nil {
					return model.FinalFilmJob{}, err
				}
				continue
			}
			if _, err = s.analyzeAutomationEvidence(ctx, job); err != nil {
				return model.FinalFilmJob{}, err
			}
		case model.FinalFilmJobPlanning:
			if job.DirectorPlan == nil {
				if _, err = s.PlanDirectorGeneratedShots(ctx, jobID, job.Revision); err != nil {
					return model.FinalFilmJob{}, err
				}
				continue
			}
			if _, err = s.authorizeAutomatedGeneration(ctx, job); err != nil {
				return model.FinalFilmJob{}, err
			}
		case model.FinalFilmJobGeneratingPresentation:
			if _, err = s.runAutomatedCandidates(ctx, jobID); err != nil {
				return model.FinalFilmJob{}, err
			}
		case model.FinalFilmJobQualityGate:
			if _, err = s.beginAutomatedComposition(ctx, job); err != nil {
				return model.FinalFilmJob{}, err
			}
		case model.FinalFilmJobComposing:
			return s.finishAutomatedComposition(ctx, job)
		case model.FinalFilmJobFailed:
			recovered, recoverErr := s.recoverFailedAutomation(ctx, job)
			if recoverErr != nil {
				return job, recoverErr
			}
			if recovered.State == model.FinalFilmJobFailed {
				return recovered, nil
			}
		case model.FinalFilmJobAwaitingFinalReview:
			if needsAutomatedDeliveryProfileReconcile(job) {
				if _, err = s.reconcileAutomatedDeliveryProfile(ctx, job); err != nil {
					return model.FinalFilmJob{}, err
				}
				continue
			}
			return job, nil
		case model.FinalFilmJobCompleted, model.FinalFilmJobRevisionRequested:
			return job, nil
		default:
			return model.FinalFilmJob{}, fmt.Errorf("automatic runner cannot resume state %s", job.State)
		}
	}
}

func (s *Service) recoverFailedAutomation(ctx context.Context, job model.FinalFilmJob) (model.FinalFilmJob, error) {
	if job.LastError == nil || !job.LastError.Retryable || job.RunAuthorization == nil || job.AutomationPolicy == nil {
		return job, nil
	}
	record, err := decodeGeneratedTrack(job.GeneratedTrack)
	if err != nil {
		return job, err
	}
	if retryableAutomatedCompositionFailure(job.LastError.Message) {
		_, _, generationDone := nextAutomatedIntent(record.Intents, job.QualityReports, job.AutomationPolicy.ProviderPolicy.MaxAttemptsPerSlot)
		if generationDone {
			next := job
			next.Revision++
			next.UpdatedAt = s.now().UTC()
			next.State, next.Phase = model.FinalFilmJobQualityGate, "retryable_composition_reconciled"
			next.LastError, next.FinalCatalog, next.FinalPlan, next.FinalOutputValidation = nil, nil, nil, nil
			next.FinalRender = model.FinalFilmRenderOutput{}
			if err := s.store.TransitionJob(ctx, job.JobID, job.Revision, next, s.event(next, next.Phase, "已保留通过质检的候选并重试确定性合成，不产生新的模型调用", map[string]any{"provider_calls_used": next.RunAuthorization.ProviderCallsUsed})); err != nil {
				return model.FinalFilmJob{}, err
			}
			return next, nil
		}
	}
	next := job
	next.QualityReports = append([]model.CandidateQualityReport{}, job.QualityReports...)
	next.ProviderAttempts = append([]model.FinalFilmProviderAttempt{}, job.ProviderAttempts...)
	recovered := false

	removeReports := map[string]bool{}
	for index := range next.ProviderAttempts {
		attempt := &next.ProviderAttempts[index]
		report := automatedQualityReport(next.QualityReports, attempt.IntentID, attempt.Provider, attempt.Attempt)
		if report == nil || report.Decision == "accept" || attempt.RecoveryCount >= 1 {
			continue
		}
		key := automatedAttemptKey(attempt.IntentID, attempt.Provider, attempt.Attempt)
		if attempt.ProviderTaskID == "" && preAdmissionRequestRejected(report.Findings) {
			attempt.Status = "superseded_pre_admission"
			attempt.RecoveryCount++
			removeReports[key] = true
			recovered = true
			continue
		}
		if attempt.ProviderTaskID != "" && report.Decision == "provider_revision_required" && retryableProviderPollingFailure(report.Findings) {
			attempt.Status = "submitted"
			attempt.CompletedAt = time.Time{}
			attempt.RecoveryCount++
			removeReports[key] = true
			recovered = true
		}
	}
	if len(removeReports) > 0 {
		filtered := next.QualityReports[:0]
		for _, report := range next.QualityReports {
			if !removeReports[automatedAttemptKey(report.IntentID, report.Provider, report.Attempt)] {
				filtered = append(filtered, report)
			}
		}
		next.QualityReports = filtered
	}
	if !recovered {
		return job, nil
	}
	used := 0
	seenTasks := map[string]bool{}
	for _, attempt := range next.ProviderAttempts {
		taskID := strings.TrimSpace(attempt.ProviderTaskID)
		if taskID != "" && !seenTasks[taskID] {
			seenTasks[taskID] = true
			used++
		}
	}
	next.RunAuthorization.ProviderCallsUsed = used
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	next.State, next.Phase = model.FinalFilmJobGeneratingPresentation, "retryable_provider_reconciled"
	next.LastError, next.FinalCatalog, next.FinalPlan, next.FinalOutputValidation = nil, nil, nil, nil
	next.FinalRender = model.FinalFilmRenderOutput{}
	if err := s.store.TransitionJob(ctx, job.JobID, job.Revision, next, s.event(next, next.Phase, "已复用候选并恢复 provider 未接纳或可续轮询的任务", map[string]any{"provider_calls_used": used})); err != nil {
		return model.FinalFilmJob{}, err
	}
	return next, nil
}

func retryableAutomatedCompositionFailure(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	for _, token := range []string{
		"final requirement satisfaction report",
		"automated final duration",
		"automated final freeze duration",
		"automated final black duration",
		"automated final integrated loudness",
		"automated final true peak",
		"validate automated edl",
		"render automated final",
		"build review package",
	} {
		if strings.Contains(message, token) {
			return true
		}
	}
	return false
}

func automatedIntentAccepted(reports []model.CandidateQualityReport, intentID string) bool {
	for _, report := range reports {
		if report.IntentID == intentID && report.Decision == "accept" {
			return true
		}
	}
	return false
}

func automatedQualityReport(reports []model.CandidateQualityReport, intentID, provider string, attempt int) *model.CandidateQualityReport {
	for index := len(reports) - 1; index >= 0; index-- {
		if reports[index].IntentID == intentID && reports[index].Provider == provider && reports[index].Attempt == attempt {
			return &reports[index]
		}
	}
	return nil
}

func automatedAttemptKey(intentID, provider string, attempt int) string {
	return intentID + "\x00" + provider + fmt.Sprintf("\x00%d", attempt)
}

func findGeneratedCandidateByTask(record GeneratedTrackRecord, intentID, taskID string) *media.GeneratedShotCandidate {
	for index := len(record.Candidates) - 1; index >= 0; index-- {
		candidate := &record.Candidates[index]
		if candidate.IntentID == intentID && candidate.ProviderTaskID == taskID {
			return candidate
		}
	}
	return nil
}

func (s *Service) persistedCandidatePassesCurrentGate(ctx context.Context, intent media.GeneratedShotIntent, candidate media.GeneratedShotCandidate) bool {
	if media.ValidateGeneratedShotCandidate(candidate) != nil {
		return false
	}
	probe, err := s.renderer.ProbeMedia(ctx, executor.MediaProbeRequest{Path: candidate.NormalizedArtifact.Path})
	return err == nil && probe.QualityAnalysisAvailable && probe.BlackDurationMS <= automatedBlackDurationLimitMS(intent) && probe.FreezeDurationMS <= 1000
}

func preAdmissionRequestRejected(findings []string) bool {
	text := strings.ToLower(strings.Join(findings, " "))
	return strings.Contains(text, "invalidparameter.tasktypeconstraint") || strings.Contains(text, "omni_reference_task_type")
}

func retryableProviderPollingFailure(findings []string) bool {
	text := strings.ToLower(strings.Join(findings, " "))
	for _, token := range []string{"http 502", "http 503", "timeout", "timed out", "temporar", "connection reset"} {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

func (s *Service) analyzeAutomationEvidence(ctx context.Context, job model.FinalFilmJob) (model.FinalFilmJob, error) {
	sourceAudioPresent := false
	if recording := recordingArtifact(job.Catalog); recording != nil {
		path := strings.TrimSpace(recording.LocalPath)
		if path == "" {
			path = filePathFromLocalURI(recording.URI)
		}
		if probe, probeErr := s.renderer.ProbeMedia(ctx, executor.MediaProbeRequest{Path: path}); probeErr == nil {
			sourceAudioPresent = strings.TrimSpace(probe.AudioCodec) != ""
		}
	}
	digest, err := buildDirectorEvidenceDigest(job, sourceAudioPresent, s.now().UTC())
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	next := job
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	next.State = model.FinalFilmJobPlanning
	next.Phase = "director_evidence_ready"
	next.EvidenceDigest = &digest
	if err := s.store.TransitionJob(ctx, job.JobID, job.Revision, next, s.event(next, next.Phase, "事实轨证据摘要已锁定，进入自动导演规划", map[string]any{"digest_id": digest.DigestID, "required_steps": len(digest.RequiredSteps)})); err != nil {
		return model.FinalFilmJob{}, err
	}
	return next, nil
}

func (s *Service) authorizeAutomatedGeneration(ctx context.Context, job model.FinalFilmJob) (model.FinalFilmJob, error) {
	if job.RunAuthorization == nil || job.DirectorPlan == nil {
		return model.FinalFilmJob{}, errors.New("automatic generation requires authorization and director plan")
	}
	next := job
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	next.State = model.FinalFilmJobGeneratingPresentation
	next.Phase = "automated_generation_authorized"
	next.GenerationAuthorized = true
	next.GenerationAuthorizedAt = next.UpdatedAt
	next.GenerationAuthorizationRef = job.RunAuthorization.AuthorizationRef
	if err := s.store.TransitionJob(ctx, job.JobID, job.Revision, next, s.event(next, next.Phase, "按服务端固定 H3/Seedance 分工开始生成展示镜头", nil)); err != nil {
		return model.FinalFilmJob{}, err
	}
	return next, nil
}

func (s *Service) runAutomatedCandidates(ctx context.Context, jobID string) (model.FinalFilmJob, error) {
	for {
		job, err := s.store.GetJob(ctx, jobID)
		if err != nil {
			return model.FinalFilmJob{}, err
		}
		if job.State != model.FinalFilmJobGeneratingPresentation || job.RunAuthorization == nil || job.AutomationPolicy == nil {
			return model.FinalFilmJob{}, errors.New("automatic candidate generation requires generating_presentation")
		}
		record, err := decodeGeneratedTrack(job.GeneratedTrack)
		if err != nil {
			return model.FinalFilmJob{}, err
		}
		if pending := pendingAutomatedQualityBatch(record.Intents, job.QualityReports); len(pending) > 0 {
			updated, reviewErr := s.reviewAutomatedCandidateBatch(ctx, job, record, pending)
			if reviewErr != nil {
				return model.FinalFilmJob{}, reviewErr
			}
			if updated.State == model.FinalFilmJobRevisionRequested {
				return updated, nil
			}
			continue
		}
		intent, attempt, done := nextAutomatedIntent(record.Intents, job.QualityReports, job.AutomationPolicy.ProviderPolicy.MaxAttemptsPerSlot)
		if done {
			for _, requiredIntent := range record.Intents {
				if !automatedIntentAccepted(job.QualityReports, requiredIntent.IntentID) {
					return s.blockAutomatedProviderRevision(ctx, job, requiredIntent.IntentID, "required generated slot did not pass all quality gates")
				}
			}
			next := job
			next.Revision++
			next.UpdatedAt = s.now().UTC()
			next.State = model.FinalFilmJobQualityGate
			next.Phase = "automated_quality_gate_complete"
			if err := s.store.TransitionJob(ctx, job.JobID, job.Revision, next, s.event(next, next.Phase, "候选自动质检与一次重生预算已完成，准备确定性合成", map[string]any{"quality_reports": len(next.QualityReports)})); err != nil {
				return model.FinalFilmJob{}, err
			}
			return next, nil
		}
		if s.providers == nil {
			return model.FinalFilmJob{}, errors.New("generated shot provider registry is unavailable")
		}
		provider := providerForAutomatedPurpose(intent.Purpose, job.AutomationPolicy.ProviderPolicy)
		callIntent, publicationErr := s.prepareAutomatedPresentationReferences(ctx, job, intent, automatedIdempotencyKey(job, intent, attempt))
		if publicationErr != nil {
			result := media.GeneratedShotProviderExecutionResult{SchemaVersion: media.GeneratedShotProviderExecutionSchemaVersion, Provider: provider, IntentID: intent.IntentID, Status: media.GeneratedShotFailureContinue, FailurePolicy: media.GeneratedShotFailureContinue, ErrorClass: "reference_publication_failed", ErrorMessage: publicationErr.Error()}
			updated, persistErr := s.persistAutomatedProviderResult(ctx, job, record, intent, attempt, result)
			if persistErr != nil {
				return model.FinalFilmJob{}, persistErr
			}
			if updated.State == model.FinalFilmJobRevisionRequested {
				return updated, nil
			}
			continue
		}
		adapter, _, selectErr := s.providers.Select(ctx, callIntent, provider)
		if selectErr != nil {
			result := media.GeneratedShotProviderExecutionResult{SchemaVersion: media.GeneratedShotProviderExecutionSchemaVersion, Provider: provider, IntentID: intent.IntentID, Status: media.GeneratedShotFailureContinue, FailurePolicy: media.GeneratedShotFailureContinue, ErrorClass: "provider_selection_failed", ErrorMessage: selectErr.Error()}
			updated, persistErr := s.persistAutomatedProviderResult(ctx, job, record, intent, attempt, result)
			if persistErr != nil {
				return model.FinalFilmJob{}, persistErr
			}
			if updated.State == model.FinalFilmJobRevisionRequested {
				return updated, nil
			}
			continue
		}
		if attempt > 1 {
			callIntent.Prompt += regenerationFeedback(job.QualityReports, intent.IntentID)
		}
		key := automatedIdempotencyKey(job, callIntent, attempt)
		admitted := job
		resumeTaskID := ""
		if existing := findAutomatedProviderAttempt(job.ProviderAttempts, intent.IntentID, provider, attempt); existing != nil && (existing.Status == "admitted" || existing.Status == "submitted") {
			key = existing.IdempotencyKey
			resumeTaskID = existing.ProviderTaskID
		} else {
			if job.RunAuthorization.ProviderCallsUsed >= job.RunAuthorization.MaxProviderCalls {
				return model.FinalFilmJob{}, errors.New("automatic provider call budget exhausted")
			}
			admitted.Revision++
			admitted.UpdatedAt = s.now().UTC()
			admitted.RunAuthorization.ProviderCallsUsed++
			admitted.ProviderAttempts = append(admitted.ProviderAttempts, model.FinalFilmProviderAttempt{IntentID: intent.IntentID, Provider: provider, Attempt: attempt, IdempotencyKey: key, Status: "admitted", AdmittedAt: admitted.UpdatedAt})
			if err := s.store.TransitionJob(ctx, job.JobID, job.Revision, admitted, s.event(admitted, "provider_attempt_admitted", "模型调用已持久化准入", map[string]any{"intent_id": intent.IntentID, "provider": provider, "attempt": attempt})); err != nil {
				return model.FinalFilmJob{}, err
			}
		}
		checkpointTask := func(taskID string) error {
			taskID = strings.TrimSpace(taskID)
			if taskID == "" {
				return errors.New("provider task checkpoint requires a task ID")
			}
			next := admitted
			stored := findAutomatedProviderAttempt(next.ProviderAttempts, intent.IntentID, provider, attempt)
			if stored == nil {
				return errors.New("provider task checkpoint is missing its admitted attempt")
			}
			stored.ProviderTaskID = taskID
			stored.Status = "submitted"
			next.Revision++
			next.UpdatedAt = s.now().UTC()
			if checkpointErr := s.store.TransitionJob(ctx, admitted.JobID, admitted.Revision, next, s.event(next, "provider_task_submitted", "模型异步 task ID 已持久化，可在重启后续轮询", map[string]any{"intent_id": intent.IntentID, "provider": provider, "attempt": attempt, "provider_task_id": taskID})); checkpointErr != nil {
				return checkpointErr
			}
			admitted = next
			return nil
		}
		result, executeErr := adapter.Execute(ctx, media.GeneratedShotProviderExecutionRequest{Intent: callIntent, GenerationAuthorized: true, AuthorizationRef: admitted.RunAuthorization.AuthorizationRef, IdempotencyKey: key, AdmissionScope: job.JobID, OutputDir: filepath.Join(s.outputRoot, job.JobID, "generated", intent.IntentID, provider, fmt.Sprintf("attempt-%d", attempt)), Timeout: s.providerTimeout, ResumeProviderTaskID: resumeTaskID, OnTaskSubmitted: checkpointTask})
		if executeErr != nil && result.ErrorMessage == "" {
			result.ErrorMessage = executeErr.Error()
		}
		updated, persistErr := s.persistAutomatedProviderResult(ctx, admitted, record, intent, attempt, result)
		if persistErr != nil {
			return model.FinalFilmJob{}, persistErr
		}
		if updated.State == model.FinalFilmJobRevisionRequested {
			return updated, nil
		}
	}
}

func nextAutomatedIntent(intents []media.GeneratedShotIntent, reports []model.CandidateQualityReport, maxAttempts int) (media.GeneratedShotIntent, int, bool) {
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		for _, intent := range intents {
			if automatedIntentAccepted(reports, intent.IntentID) {
				continue
			}
			hasAttempt, awaiting := false, false
			for _, report := range reports {
				if report.IntentID == intent.IntentID && report.Attempt == attempt {
					hasAttempt = true
					awaiting = report.Decision == "awaiting_content_review"
				}
			}
			if awaiting {
				continue
			}
			if !hasAttempt {
				return intent, attempt, false
			}
		}
	}
	return media.GeneratedShotIntent{}, 0, true
}

func providerForAutomatedPurpose(purpose string, policy model.FinalFilmProviderPolicy) string {
	if purpose == media.GeneratedShotPurposeIntro {
		return policy.IntroProvider
	}
	if purpose == media.GeneratedShotPurposeOutro {
		return policy.OutroProvider
	}
	return policy.TransitionProvider
}

func automatedIdempotencyKey(job model.FinalFilmJob, intent media.GeneratedShotIntent, attempt int) string {
	sum := sha256.Sum256([]byte(job.JobID + "\x00" + job.RunAuthorization.AuthorizationRef + "\x00" + intent.IntentID + fmt.Sprintf("\x00%d\x00", attempt) + intent.Prompt))
	return "finalfilm_auto_" + hex.EncodeToString(sum[:16])
}

func regenerationFeedback(reports []model.CandidateQualityReport, intentID string) string {
	for index := len(reports) - 1; index >= 0; index-- {
		if reports[index].IntentID == intentID {
			return " Regenerate once and correct these issues: " + strings.Join(reports[index].Findings, "; ")
		}
	}
	return ""
}

func findAutomatedProviderAttempt(attempts []model.FinalFilmProviderAttempt, intentID, provider string, attempt int) *model.FinalFilmProviderAttempt {
	for index := len(attempts) - 1; index >= 0; index-- {
		candidate := &attempts[index]
		if candidate.IntentID == intentID && candidate.Provider == provider && candidate.Attempt == attempt {
			return candidate
		}
	}
	return nil
}

func pendingAutomatedQualityBatch(intents []media.GeneratedShotIntent, reports []model.CandidateQualityReport) []model.CandidateQualityReport {
	for attempt := 1; attempt <= 2; attempt++ {
		pending := []model.CandidateQualityReport{}
		ready := true
		for _, intent := range intents {
			if automatedIntentAccepted(reports, intent.IntentID) {
				continue
			}
			var current *model.CandidateQualityReport
			for index := len(reports) - 1; index >= 0; index-- {
				if reports[index].IntentID == intent.IntentID && reports[index].Attempt == attempt {
					copyReport := reports[index]
					current = &copyReport
					break
				}
			}
			if current == nil {
				ready = false
				continue
			}
			if current.Decision == "awaiting_content_review" {
				pending = append(pending, *current)
			}
		}
		if ready && len(pending) > 0 {
			return pending
		}
	}
	return nil
}

func (s *Service) reviewAutomatedCandidateBatch(ctx context.Context, job model.FinalFilmJob, record GeneratedTrackRecord, pending []model.CandidateQualityReport) (model.FinalFilmJob, error) {
	if job.DirectorVisualCallsUsed >= 2 {
		return s.blockAutomatedProviderRevision(ctx, job, pending[0].IntentID, "candidate visual review budget exhausted")
	}
	inputs := make([]VisualQualityReviewInput, 0, len(pending))
	for _, report := range pending {
		intent := generatedIntentByID(record.Intents, report.IntentID)
		if intent == nil || strings.TrimSpace(report.ContactSheetPath) == "" {
			return s.blockAutomatedProviderRevision(ctx, job, report.IntentID, "candidate contact sheet is unavailable")
		}
		inputs = append(inputs, VisualQualityReviewInput{IntentID: report.IntentID, Purpose: intent.Purpose, Attempt: report.Attempt, CandidateID: report.CandidateID, ContactSheetPath: report.ContactSheetPath})
	}
	if s.visualReviewer == nil {
		return s.blockAutomatedProviderRevision(ctx, job, pending[0].IntentID, "visual content reviewer is unavailable")
	}
	reviews, err := s.visualReviewer.ReviewCandidates(ctx, inputs)
	if err != nil {
		return model.FinalFilmJob{}, fmt.Errorf("review candidate contact sheets: %w", err)
	}
	byKey := map[string]VisualQualityReviewResult{}
	for _, review := range reviews {
		byKey[fmt.Sprintf("%s\x00%d", review.IntentID, review.Attempt)] = review
	}
	next := job
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	next.DirectorVisualCallsUsed++
	blockedIntent := ""
	for index := range next.QualityReports {
		report := &next.QualityReports[index]
		if report.Decision != "awaiting_content_review" {
			continue
		}
		review, ok := byKey[fmt.Sprintf("%s\x00%d", report.IntentID, report.Attempt)]
		if !ok {
			report.Findings = append(report.Findings, "visual reviewer omitted the required candidate")
		} else {
			report.TextPass, report.ContentPass = review.TextPass, review.ContentPass
			report.Score = review.Score
			report.Findings = append(report.Findings, review.Findings...)
		}
		if report.TechnicalPass && report.TemporalPass && report.TextPass && report.ContentPass {
			report.Decision = "accept"
		} else if report.Attempt < next.AutomationPolicy.ProviderPolicy.MaxAttemptsPerSlot {
			report.Decision = "retry"
		} else {
			report.Decision = "provider_revision_required"
			blockedIntent = report.IntentID
		}
		report.CheckedAt = next.UpdatedAt
		if attempt := findAutomatedProviderAttempt(next.ProviderAttempts, report.IntentID, report.Provider, report.Attempt); attempt != nil {
			attempt.Status, attempt.CompletedAt = report.Decision, report.CheckedAt
		}
	}
	eventType, message := "candidate_content_batch_reviewed", "候选多帧联系表已完成批量文字与内容质检"
	if blockedIntent != "" {
		next.State, next.Phase = model.FinalFilmJobRevisionRequested, "provider_revision_required"
		next.RepairDirectives = append(next.RepairDirectives, providerRepairDirective(blockedIntent, "generated_media_quality_failed"))
		eventType, message = next.Phase, "必需生成槽位两次仍不合格，已阻断而非降级交付"
	}
	if err := s.store.TransitionJob(ctx, job.JobID, job.Revision, next, s.event(next, eventType, message, map[string]any{"candidate_count": len(pending), "visual_calls_used": next.DirectorVisualCallsUsed})); err != nil {
		return model.FinalFilmJob{}, err
	}
	return next, nil
}

func generatedIntentByID(intents []media.GeneratedShotIntent, intentID string) *media.GeneratedShotIntent {
	for index := range intents {
		if intents[index].IntentID == intentID {
			return &intents[index]
		}
	}
	return nil
}

func providerRepairDirective(intentID, failureClass string) model.RepairDirective {
	return model.RepairDirective{SchemaVersion: model.RepairDirectiveSchemaVersion, SourceModule: "director-media", FailureClass: failureClass, TargetModule: "director-media", Action: "revise_provider_slot_" + intentID, Attempt: 2, MaxAttempts: 2, ResumePhase: "generated_media_qc"}
}

func (s *Service) blockAutomatedProviderRevision(ctx context.Context, job model.FinalFilmJob, intentID, reason string) (model.FinalFilmJob, error) {
	next := job
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	next.State, next.Phase = model.FinalFilmJobRevisionRequested, "provider_revision_required"
	next.RepairDirectives = append(next.RepairDirectives, providerRepairDirective(intentID, reason))
	if err := s.store.TransitionJob(ctx, job.JobID, job.Revision, next, s.event(next, next.Phase, "必需生成镜头未通过内容门禁，等待修订", map[string]any{"intent_id": intentID, "reason": reason})); err != nil {
		return model.FinalFilmJob{}, err
	}
	return next, nil
}

func (s *Service) persistAutomatedProviderResult(ctx context.Context, job model.FinalFilmJob, record GeneratedTrackRecord, intent media.GeneratedShotIntent, attempt int, result media.GeneratedShotProviderExecutionResult) (model.FinalFilmJob, error) {
	record.Executions = append(record.Executions, result)
	findings := []string{}
	technicalPass, temporalPass, contactSheetPath := false, false, ""
	if result.Candidate != nil {
		if err := media.ValidateGeneratedShotCandidate(*result.Candidate); err != nil {
			findings = append(findings, err.Error())
		} else {
			review := result.StructuralReview
			if review == nil {
				reviewID, _ := s.newID("structural_review")
				computed := media.ReviewGeneratedShotCandidateStructure(reviewID, intent, *result.Candidate)
				review = &computed
			}
			record.Candidates = append(record.Candidates, *result.Candidate)
			record.StructuralReviews = append(record.StructuralReviews, *review)
			technicalPass = review.StructurallyEligible
			for _, finding := range review.Findings {
				findings = append(findings, finding.Code+": "+finding.Message)
			}
			sheetPath := strings.TrimSuffix(result.Candidate.NormalizedArtifact.Path, filepath.Ext(result.Candidate.NormalizedArtifact.Path)) + "-contact-sheet.jpg"
			probe, probeErr := s.renderer.ProbeMedia(ctx, executor.MediaProbeRequest{Path: result.Candidate.NormalizedArtifact.Path, ContactSheetPath: sheetPath, ContactSheetFrames: 8, AnalyzeTemporalQuality: true})
			if probeErr != nil {
				technicalPass = false
				findings = append(findings, "quality_probe_failed: "+probeErr.Error())
			} else if !probe.QualityAnalysisAvailable {
				technicalPass = false
				findings = append(findings, "quality_analysis_unavailable: FFmpeg black/freeze analysis is required")
			} else {
				contactSheetPath = probe.ContactSheetPath
				blackLimit := automatedBlackDurationLimitMS(intent)
				if probe.BlackDurationMS > blackLimit {
					technicalPass = false
					findings = append(findings, fmt.Sprintf("black_duration_exceeded: %dms > %dms", probe.BlackDurationMS, blackLimit))
				}
				if probe.FreezeDurationMS > 1000 {
					technicalPass = false
					findings = append(findings, fmt.Sprintf("freeze_duration_exceeded: %dms > 1000ms", probe.FreezeDurationMS))
				}
				temporalPass = probe.TemporalAnalysisAvailable && probe.JitterMeasurementAvailable && probe.FullScreenFlashCount <= 1
				if !probe.TemporalAnalysisAvailable {
					findings = append(findings, "temporal_analysis_unavailable")
				}
				if !probe.JitterMeasurementAvailable {
					findings = append(findings, "jitter_measurement_unavailable")
				}
				if probe.FullScreenFlashCount > 1 {
					findings = append(findings, fmt.Sprintf("full_screen_flash_count_exceeded: %d", probe.FullScreenFlashCount))
				}
				if strings.TrimSpace(contactSheetPath) == "" {
					temporalPass = false
					findings = append(findings, "contact_sheet_unavailable")
				}
			}
		}
	} else if result.ErrorMessage != "" {
		findings = append(findings, result.ErrorMessage)
	}
	decision := "retry"
	if technicalPass && temporalPass {
		decision = "awaiting_content_review"
	} else if attempt >= job.AutomationPolicy.ProviderPolicy.MaxAttemptsPerSlot {
		decision = "provider_revision_required"
	}
	reportID, _ := s.newID("quality_report")
	report := model.CandidateQualityReport{SchemaVersion: model.CandidateQualityReportSchemaVersion, ReportID: reportID, IntentID: intent.IntentID, Provider: result.Provider, Attempt: attempt, TechnicalPass: technicalPass, TemporalPass: temporalPass, TextPass: false, ContentPass: false, Findings: findings, Decision: decision, CheckedAt: s.now().UTC(), ContactSheetPath: contactSheetPath}
	if technicalPass && result.Candidate != nil {
		report.CandidateID = result.Candidate.CandidateID
	}
	record.UpdatedAt = report.CheckedAt
	raw, err := json.Marshal(record)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	next := job
	next.Revision++
	next.UpdatedAt = report.CheckedAt
	next.GeneratedTrack = raw
	next.QualityReports = append(next.QualityReports, report)
	if decision == "provider_revision_required" {
		next.State, next.Phase = model.FinalFilmJobRevisionRequested, "provider_revision_required"
		next.RepairDirectives = append(next.RepairDirectives, providerRepairDirective(intent.IntentID, "generated_media_technical_or_temporal_failure"))
	}
	if stored := findAutomatedProviderAttempt(next.ProviderAttempts, intent.IntentID, result.Provider, attempt); stored != nil {
		stored.Status, stored.ProviderTaskID, stored.CompletedAt = decision, result.ProviderTaskID, report.CheckedAt
	}
	if err := s.store.TransitionJob(ctx, job.JobID, job.Revision, next, s.event(next, "candidate_quality_recorded", "候选技术与内容边界质检已记录", map[string]any{"intent_id": intent.IntentID, "attempt": attempt, "decision": decision})); err != nil {
		return model.FinalFilmJob{}, err
	}
	return next, nil
}

func (s *Service) beginAutomatedComposition(ctx context.Context, job model.FinalFilmJob) (model.FinalFilmJob, error) {
	next := job
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	next.State = model.FinalFilmJobComposing
	next.Phase = "composing_automated_final"
	if err := s.store.TransitionJob(ctx, job.JobID, job.Revision, next, s.event(next, next.Phase, "使用版本化 EDL 与 FFmpeg 开始确定性合成", nil)); err != nil {
		return model.FinalFilmJob{}, err
	}
	return next, nil
}

func automatedFinalDeliveryProfile(source model.EditorRenderProfile) model.EditorRenderProfile {
	result := source
	result.Mode, result.Width, result.Height, result.FPS, result.Format = "final", 1920, 1080, 30, "mp4"
	if strings.TrimSpace(result.Preset) == "" {
		result.Preset = "medium"
	}
	if result.CRF <= 0 {
		result.CRF = 18
	}
	return result
}

func needsAutomatedDeliveryProfileReconcile(job model.FinalFilmJob) bool {
	if job.AutomationProfile != model.FinalFilmAutomationProfileGuidedDemoV1 || job.FinalOutputValidation == nil {
		return false
	}
	validation := job.FinalOutputValidation
	return validation.Width != 1920 || validation.Height != 1080 || math.Abs(validation.FPS-30) > 0.25 || automatedPlanContainsInternalCaption(job.FinalPlan) || automatedPlanContainsDynamicFactMotion(job.FinalPlan)
}

func (s *Service) reconcileAutomatedDeliveryProfile(ctx context.Context, job model.FinalFilmJob) (model.FinalFilmJob, error) {
	next := job
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	next.State, next.Phase = model.FinalFilmJobQualityGate, "delivery_profile_reconciled"
	next.RenderProfile = automatedFinalDeliveryProfile(job.RenderProfile)
	next.FinalCatalog, next.FinalPlan, next.FinalOutputValidation, next.ReviewPackage = nil, nil, nil, nil
	next.FinalRender = model.FinalFilmRenderOutput{}
	if err := s.store.TransitionJob(ctx, job.JobID, job.Revision, next, s.event(next, next.Phase, "最终交付规格与业务字幕已统一，使用既有候选重新合成", map[string]any{"provider_calls_used": next.RunAuthorization.ProviderCallsUsed})); err != nil {
		return model.FinalFilmJob{}, err
	}
	return next, nil
}

func (s *Service) finishAutomatedComposition(ctx context.Context, job model.FinalFilmJob) (model.FinalFilmJob, error) {
	if job.MediaCoverage == nil || model.ValidateMediaCoverageReport(*job.MediaCoverage) != nil || !job.MediaCoverage.Complete {
		return s.routeAutomatedCompositionRepair(ctx, job, "media_coverage_incomplete", "execution-capture", "rerecord_missing_chapters")
	}
	if !allRequiredGeneratedSlotsAccepted(job) {
		return s.blockAutomatedProviderRevision(ctx, job, "required_slot", "generated slot acceptance is incomplete")
	}
	record, err := decodeGeneratedTrack(job.GeneratedTrack)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	catalog, plan, shotIDs := compileAutomatedGeneratedPlan(job, record)
	if err := validateFactTrackUnchanged(job.BaselinePlan, plan); err != nil {
		return model.FinalFilmJob{}, err
	}
	validation, err := s.renderer.ValidateEditPlan(ctx, executor.EditPlanValidationRequest{Catalog: catalog, EditPlan: plan})
	if err != nil || !validation.Valid {
		return model.FinalFilmJob{}, fmt.Errorf("validate automated EDL: %v %+v", err, validation.Errors)
	}
	renderProfile := automatedFinalDeliveryProfile(job.RenderProfile)
	result, err := s.renderer.Render(ctx, executor.RenderRequest{OutputDir: filepath.Join(s.outputRoot, job.JobID, fmt.Sprintf("final-r%d", job.Revision)), DurationSec: maxInt(1, (plan.TargetDurationMS+999)/1000), GeneratedAssets: catalogArtifactRefs(catalog), AssetTimelineCatalog: &catalog, EditPlan: &plan, RenderProfile: &renderProfile, ModelExecution: &executor.RenderModelExecutionAudit{Invoked: len(shotIDs) > 0, Provider: "multiple", PlanSource: "automated_final_output_review_pending", ProviderCallStatus: "candidate_quality_gated", RealCallMade: job.RunAuthorization.ProviderCallsUsed > 0, ProviderOutputAdopted: len(shotIDs) > 0, PatchID: "automated-edl", PatchApplied: true, AdoptedShotIDs: shotIDs, Note: "presentation-only candidates remain subject to final-output review"}})
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	outputValidation, err := validateFinalFilmOutput(ctx, s.renderer, result, renderProfile, plan, s.requireTestNarration, s.now().UTC())
	if err == nil && (outputValidation.DurationMS < job.AutomationPolicy.TargetDuration.MinMS || outputValidation.DurationMS > job.AutomationPolicy.TargetDuration.MaxMS) {
		err = fmt.Errorf("automated final duration %dms is outside 90-120 seconds", outputValidation.DurationMS)
	}
	if err == nil && !outputValidation.QualityAnalysisAvailable {
		err = errors.New("automated final requires FFmpeg black/freeze/loudness analysis")
	}
	if err == nil && outputValidation.BlackDurationMS > 1000 {
		err = fmt.Errorf("automated final black duration %dms exceeds 1000ms", outputValidation.BlackDurationMS)
	}
	if err == nil {
		// Product UIs legitimately hold a stable result while the viewer reads it.
		// Keep generated candidates on their strict per-shot freeze gate above,
		// but judge the assembled film relative to the factual baseline so chapter
		// packaging cannot add a new long freeze to an otherwise valid demo.
		allowedFreezeMS := 3000
		if baselineProbe, probeErr := s.renderer.ProbeMedia(ctx, executor.MediaProbeRequest{Path: job.BaselineRender.VideoPath}); probeErr == nil && baselineProbe.QualityAnalysisAvailable {
			allowedFreezeMS = automatedFinalFreezeAllowanceMS(baselineProbe, outputValidation.DurationMS)
		}
		if outputValidation.FreezeDurationMS > allowedFreezeMS {
			err = fmt.Errorf("automated final freeze duration %dms exceeds factual baseline allowance %dms", outputValidation.FreezeDurationMS, allowedFreezeMS)
		}
	}
	if err == nil && outputValidation.AudioCodec != "" && !outputValidation.VerifiedSilence && (outputValidation.IntegratedLUFS < -18 || outputValidation.IntegratedLUFS > -14) {
		err = fmt.Errorf("automated final integrated loudness %.2f LUFS is outside -16±2 LUFS", outputValidation.IntegratedLUFS)
	}
	if err == nil && outputValidation.AudioCodec != "" && !outputValidation.VerifiedSilence && outputValidation.TruePeakDB > -1 {
		err = fmt.Errorf("automated final true peak %.2f dB exceeds -1 dB", outputValidation.TruePeakDB)
	}
	if err != nil {
		return s.routeAutomatedCompositionRepair(ctx, job, "final_technical_quality_failed", "director-media", "recompose_final")
	}
	if automatedPlanContainsInternalCaption(&plan) || automatedPlanContainsDynamicFactMotion(&plan) || !allAutomatedCaptionsApproved(plan, job.PublicNarrativeFacts) {
		return s.routeAutomatedCompositionRepair(ctx, job, "director_plan_content_boundary_failed", "director-media", "replan_and_recompose")
	}
	if job.DirectorVisualCallsUsed >= 3 || s.visualReviewer == nil {
		return s.routeAutomatedCompositionRepair(ctx, job, "final_visual_review_unavailable", "director-media", "review_final_sequence")
	}
	contactSheetPath := strings.TrimSuffix(result.VideoPath, filepath.Ext(result.VideoPath)) + "-contact-sheet.jpg"
	temporalProbe, probeErr := s.renderer.ProbeMedia(ctx, executor.MediaProbeRequest{Path: result.VideoPath, ContactSheetPath: contactSheetPath, ContactSheetFrames: 12, AnalyzeTemporalQuality: true})
	if probeErr != nil || !temporalProbe.TemporalAnalysisAvailable || !temporalProbe.JitterMeasurementAvailable || strings.TrimSpace(temporalProbe.ContactSheetPath) == "" {
		return s.routeAutomatedCompositionRepair(ctx, job, "final_temporal_evidence_unavailable", "director-media", "recompose_final")
	}
	approved := []string{}
	for _, fact := range job.PublicNarrativeFacts {
		approved = append(approved, fact.ApprovedCaptionVariants...)
	}
	visualReport, reviewErr := s.visualReviewer.ReviewFinal(ctx, FinalVisualReviewInput{ContactSheetPath: temporalProbe.ContactSheetPath, RequiredChapters: model.RequiredDemoChapters(), ApprovedCaptions: approved})
	if reviewErr != nil {
		return model.FinalFilmJob{}, fmt.Errorf("review final contact sheet: %w", reviewErr)
	}
	visualReport.SchemaVersion = "demoops.final_visual_quality_report.v1"
	visualReport.ContactSheetPath = temporalProbe.ContactSheetPath
	visualReport.TemporalPass = visualReport.TemporalPass && temporalProbe.FullScreenFlashCount <= 6
	visualReport.CheckedAt = s.now().UTC()
	if !visualReport.TemporalPass || !visualReport.TextPass || !visualReport.ContentPass {
		next := job
		next.DirectorVisualCallsUsed++
		next.FinalVisualQuality = &visualReport
		return s.routeAutomatedCompositionRepair(ctx, next, "final_visual_quality_failed", "director-media", "replan_and_recompose")
	}
	next := job
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	next.CompositionAttempts++
	next.DirectorVisualCallsUsed++
	next.FinalVisualQuality = &visualReport
	next.State = model.FinalFilmJobAwaitingFinalReview
	next.Phase = "awaiting_final_review"
	next.FinalCatalog, next.FinalPlan = &catalog, &plan
	next.FinalRender = model.FinalFilmRenderOutput{Status: "ready", VideoPath: result.VideoPath, RenderManifestPath: result.RenderManifestPath, PlanID: plan.PlanID, PlanRevision: job.EditorRevision, CompletedAt: next.UpdatedAt}
	next.RenderProfile = renderProfile
	next.FinalOutputValidation = &outputValidation
	next.ProviderAttempts = reconcileAutomatedProviderAttemptAudit(job.ProviderAttempts, job.QualityReports)
	reviewPackage, err := s.buildReviewPackage(ctx, next, record)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	next.ReviewPackage = &reviewPackage
	if err := s.store.TransitionJob(ctx, job.JobID, job.Revision, next, s.event(next, next.Phase, "自动成片与本地审核包已完成，等待唯一一次人工终审", map[string]any{"video_path": result.VideoPath, "review_package": reviewPackage.ZIPPath})); err != nil {
		return model.FinalFilmJob{}, err
	}
	return next, nil
}

func allRequiredGeneratedSlotsAccepted(job model.FinalFilmJob) bool {
	for _, intent := range job.PresentationIntents {
		accepted := false
		for _, report := range job.QualityReports {
			if report.IntentID == intent.IntentID && report.Decision == "accept" && report.TechnicalPass && report.TemporalPass && report.TextPass && report.ContentPass {
				accepted = true
			}
		}
		if !accepted {
			return false
		}
	}
	return len(job.PresentationIntents) == 3
}

func allAutomatedCaptionsApproved(plan model.DemoEditPlan, facts []model.PublicNarrativeFact) bool {
	approved := map[string]bool{}
	for _, fact := range facts {
		for _, caption := range fact.ApprovedCaptionVariants {
			approved[caption] = true
		}
	}
	for _, shot := range plan.Shots {
		for _, overlay := range shot.Overlays {
			if overlay.Type == model.EditOverlayCaption && !approved[overlay.Text] {
				return false
			}
		}
	}
	return true
}

func (s *Service) routeAutomatedCompositionRepair(ctx context.Context, job model.FinalFilmJob, failureClass, targetModule, action string) (model.FinalFilmJob, error) {
	next := job
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	next.CompositionAttempts++
	attempt := next.CompositionAttempts
	if attempt > 3 {
		attempt = 3
	}
	directive := model.RepairDirective{SchemaVersion: model.RepairDirectiveSchemaVersion, SourceModule: "director-media", FailureClass: failureClass, TargetModule: targetModule, Action: action, Attempt: attempt, MaxAttempts: 3, ResumePhase: "director_planning"}
	next.RepairDirectives = append(next.RepairDirectives, directive)
	next.FinalCatalog, next.FinalPlan, next.FinalOutputValidation, next.ReviewPackage = nil, nil, nil, nil
	next.FinalRender = model.FinalFilmRenderOutput{}
	if next.CompositionAttempts < 3 && targetModule == "director-media" && next.DirectorVisualCallsUsed < 3 {
		next.State, next.Phase = model.FinalFilmJobPlanning, "director_replanning"
		next.DirectorPlan = nil
		// A composition repair re-enters the planning admission point. Keep the
		// run authorization and its original provider budget, but clear the
		// per-plan generation grant so the replacement plan can be validated and
		// admitted normally. Accepted candidates remain durable and are reused;
		// this path must not silently create another provider attempt.
		next.GenerationAuthorized = false
		next.GenerationAuthorizationRef = ""
		next.GenerationAuthorizedAt = time.Time{}
	} else {
		next.State, next.Phase = model.FinalFilmJobRevisionRequested, "final_qc_revision_required"
	}
	if err := s.store.TransitionJob(ctx, job.JobID, job.Revision, next, s.event(next, next.Phase, "最终成片门禁生成了定向返工指令", map[string]any{"failure_class": failureClass, "action": action, "attempt": attempt})); err != nil {
		return model.FinalFilmJob{}, err
	}
	return next, nil
}

func automatedFinalFreezeAllowanceMS(baseline executor.MediaProbeResult, outputDurationMS int) int {
	const graceMS = 3000
	if baseline.DurationMS <= 0 || outputDurationMS <= 0 {
		return graceMS
	}
	ratio := math.Min(0.80, math.Max(0, float64(baseline.FreezeDurationMS)/float64(baseline.DurationMS)))
	return graceMS + int(math.Round(ratio*float64(outputDurationMS)))
}

func reconcileAutomatedProviderAttemptAudit(attempts []model.FinalFilmProviderAttempt, reports []model.CandidateQualityReport) []model.FinalFilmProviderAttempt {
	result := append([]model.FinalFilmProviderAttempt{}, attempts...)
	for index := range result {
		report := automatedQualityReport(reports, result[index].IntentID, result[index].Provider, result[index].Attempt)
		if report == nil {
			continue
		}
		result[index].Status = report.Decision
		if !report.CheckedAt.IsZero() {
			result[index].CompletedAt = report.CheckedAt
		}
	}
	return result
}

func compileAutomatedGeneratedPlan(job model.FinalFilmJob, record GeneratedTrackRecord) (model.AssetTimelineCatalog, model.DemoEditPlan, []string) {
	catalog := job.Catalog
	catalog.Artifacts = append([]model.TimelineArtifact{}, job.Catalog.Artifacts...)
	plan := job.BaselinePlan
	plan.Shots = append([]model.DemoEditShot{}, job.BaselinePlan.Shots...)
	if job.DirectorPlan != nil && job.DirectorPlan.StoryPlan != nil {
		factsByStepID := map[string]model.DirectorTimelineSegment{}
		for _, segment := range job.DirectorPlan.StoryPlan.Timeline {
			if segment.Kind == "fact" && segment.SourceStepID != "" {
				factsByStepID[segment.SourceStepID] = segment
			}
		}
		for index := range plan.Shots {
			if fact, ok := factsByStepID[plan.Shots[index].SourceStepID]; ok && fact.Speed != 1 {
				speed := fact.Speed
				plan.Shots[index].Operations = append(plan.Shots[index].Operations, model.EditOperation{Type: model.EditOperationSpeed, Speed: &speed})
			}
		}
	}
	accepted := map[string]model.CandidateQualityReport{}
	for _, report := range job.QualityReports {
		if report.Decision == "accept" {
			accepted[report.IntentID] = report
		}
	}
	prefix, suffix := []model.DemoEditShot{}, []model.DemoEditShot{}
	after := map[string][]model.DemoEditShot{}
	shotIDs := []string{}
	for _, intent := range record.Intents {
		report, ok := accepted[intent.IntentID]
		if !ok {
			continue
		}
		candidate := findGeneratedCandidate(record, report.CandidateID)
		if candidate == nil {
			continue
		}
		placement, anchor := automatedPlacement(job, intent.IntentID)
		duration := int(math.Round(candidate.NormalizedArtifact.Probe.DurationSec * 1000))
		artifactID := "automated_candidate_" + candidate.CandidateID
		catalog.Artifacts = append(catalog.Artifacts, automatedGeneratedTimelineArtifact(artifactID, *candidate, duration))
		rangeMS := model.MillisecondRange{0, duration}
		start, end := 0, duration
		shot := model.DemoEditShot{ID: "generated_shot_" + candidate.CandidateID, SourceArtifactID: artifactID, SourceTimeRangeMS: &rangeMS, Purpose: "Presentation-only generated chapter packaging pending final-output review.", Operations: []model.EditOperation{{Type: model.EditOperationTrim, StartMS: &start, EndMS: &end}}}
		shotIDs = append(shotIDs, shot.ID)
		switch placement {
		case "before_first_required_step":
			prefix = append(prefix, shot)
		case "after_last_required_step":
			suffix = append(suffix, shot)
		default:
			after[anchor] = append(after[anchor], shot)
		}
	}
	lastShotForStep := map[string]int{}
	for index, shot := range plan.Shots {
		if shot.SourceStepID != "" {
			lastShotForStep[shot.SourceStepID] = index
		}
	}
	assembled := append([]model.DemoEditShot{}, prefix...)
	for index, shot := range plan.Shots {
		assembled = append(assembled, shot)
		if lastShotForStep[shot.SourceStepID] == index {
			assembled = append(assembled, after[shot.SourceStepID]...)
		}
	}
	plan.Shots = append(assembled, suffix...)
	replaceAutomatedFactCaptions(&plan, job.PublicNarrativeFacts, job.Catalog)
	plan.PlanID = job.BaselinePlan.PlanID + "+automated"
	if job.DirectorPlan != nil && job.DirectorPlan.StoryPlan != nil {
		target := job.DirectorPlan.StoryPlan.TargetDurationMS
		if job.AutomationProfile == model.FinalFilmAutomationProfileGuidedDemoV1 && target < 105_000 {
			target = 105_000
		}
		_ = target // the quality gate rejects short coverage instead of slowing UI footage to fill time
	}
	plan.TargetDurationMS = timelineDuration(plan)
	return catalog, plan, shotIDs
}

func replaceAutomatedFactCaptions(plan *model.DemoEditPlan, facts []model.PublicNarrativeFact, catalog model.AssetTimelineCatalog) {
	if plan == nil {
		return
	}
	factByEvidence := map[string]model.PublicNarrativeFact{}
	factByStep := map[string]model.PublicNarrativeFact{}
	for _, fact := range facts {
		if model.ValidatePublicNarrativeFact(fact) != nil {
			continue
		}
		for _, evidenceRef := range fact.VisibleEvidenceRefs {
			factByEvidence[evidenceRef] = fact
		}
	}
	required := make([]model.TimelineStep, 0, len(catalog.Steps))
	for _, step := range catalog.Steps {
		if step.Required {
			required = append(required, step)
		}
	}
	sort.SliceStable(required, func(i, j int) bool { return required[i].Order < required[j].Order })
	for index, step := range required {
		if index < len(facts) {
			factByStep[step.StepID] = facts[index]
		}
	}
	for shotIndex := range plan.Shots {
		shot := &plan.Shots[shotIndex]
		fact, ok := factByStep[shot.SourceStepID]
		if !ok {
			fact, ok = factByEvidence[shot.SourceArtifactID]
		}
		if !ok {
			// Removing an untrusted caption is preferable to inventing a public
			// statement from action, expected_outcome, or observed_state.
			shot.Overlays = removeCaptionOverlays(shot.Overlays)
			continue
		}
		caption := fact.ApprovedCaptionVariants[0]
		shot.Purpose = caption
		found := false
		for overlayIndex := range shot.Overlays {
			if shot.Overlays[overlayIndex].Type == model.EditOverlayCaption {
				shot.Overlays[overlayIndex].Text = caption
				found = true
			}
		}
		if !found {
			start, end := 0, 3000
			shot.Overlays = append(shot.Overlays, model.EditOverlay{Type: model.EditOverlayCaption, Text: caption, StartMS: &start, EndMS: &end})
		}
	}
}

func removeCaptionOverlays(overlays []model.EditOverlay) []model.EditOverlay {
	result := overlays[:0]
	for _, overlay := range overlays {
		if overlay.Type != model.EditOverlayCaption {
			result = append(result, overlay)
		}
	}
	return result
}

func automatedPlanContainsInternalCaption(plan *model.DemoEditPlan) bool {
	if plan == nil {
		return false
	}
	for _, shot := range plan.Shots {
		for _, overlay := range shot.Overlays {
			text := strings.ToLower(overlay.Text)
			if overlay.Type == model.EditOverlayCaption && model.ValidatePublicCaption(text) != nil {
				return true
			}
		}
	}
	return false
}

func automatedPlanContainsDynamicFactMotion(plan *model.DemoEditPlan) bool {
	if plan == nil {
		return false
	}
	for _, shot := range plan.Shots {
		if shot.SourceStepID == "" {
			continue
		}
		for _, operation := range shot.Operations {
			if operation.Type == model.EditOperationPan || operation.Type == model.EditOperationZoomPan || operation.Style == "ambient_motion" {
				return true
			}
		}
	}
	return false
}

func automatedPlacement(job model.FinalFilmJob, intentID string) (string, string) {
	if job.DirectorPlan != nil && job.DirectorPlan.StoryPlan != nil {
		for _, segment := range job.DirectorPlan.StoryPlan.Timeline {
			if segment.IntentID == intentID {
				return segment.Placement, segment.AnchorAfterStepID
			}
		}
	}
	return "after_last_required_step", ""
}

func automatedGeneratedTimelineArtifact(id string, candidate media.GeneratedShotCandidate, duration int) model.TimelineArtifact {
	return model.TimelineArtifact{ID: id, Kind: media.GeneratedShotEditorAssetKind, URI: candidate.NormalizedArtifact.Path, LocalPath: candidate.NormalizedArtifact.Path, MimeType: candidate.NormalizedArtifact.MimeType, SHA256: candidate.NormalizedArtifact.SHA256, SizeBytes: candidate.NormalizedArtifact.SizeBytes, DurationMS: duration, AssetRole: "presentation_generated_candidate", IncludeInDemo: true, Metadata: map[string]any{"media_eligible": true, "approval_mode": "final_output_review_pending", "review_scope": model.FinalFilmReviewScopeFinalOutput, "automated_quality_gate_passed": true, "presentation_only": true, "non_authoritative": true, "source_material_policy": media.GeneratedShotSourceMaterialPolicy, "artifact_variant": "normalized", "normalization_status": "ok", "media_probe_status": "ok", "candidate_id": candidate.CandidateID, "intent_id": candidate.IntentID, "normalization_profile": candidate.NormalizedArtifact.NormalizationProfile, "width": candidate.NormalizedArtifact.Probe.Width, "height": candidate.NormalizedArtifact.Probe.Height, "fps": candidate.NormalizedArtifact.Probe.FPS, "cfr": candidate.NormalizedArtifact.Probe.CFR}}
}

func automatedBlackDurationLimitMS(intent media.GeneratedShotIntent) int {
	if intent.Purpose == media.GeneratedShotPurposeIntro || intent.Purpose == media.GeneratedShotPurposeOutro {
		// A short fade to/from black is part of the requested packaging grammar.
		// Keep the gate strict for dividers while allowing a sub-second bookend.
		return 750
	}
	return 500
}

func (s *Service) MarkAutomationFailed(ctx context.Context, jobID string, cause error) (model.FinalFilmJob, error) {
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	if job.AutomationProfile == "" || finalFilmTerminalState(job.State) || job.State == model.FinalFilmJobAwaitingFinalReview || job.State == model.FinalFilmJobRevisionRequested {
		return job, nil
	}
	next := job
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	next.State, next.Phase = model.FinalFilmJobFailed, "automation_failed"
	next.LastError = &model.FinalFilmJobError{Code: "automation_failed", Message: cause.Error(), Retryable: true, OccurredAt: next.UpdatedAt}
	if err := s.store.TransitionJob(ctx, job.JobID, job.Revision, next, s.event(next, next.Phase, "自动成片流程失败，已保留所有事实轨和阶段检查点", nil)); err != nil {
		return model.FinalFilmJob{}, err
	}
	return next, nil
}

func (s *Service) ResumeRunnableAutomations() {
	jobs, err := s.store.ListJobs(context.Background())
	if err != nil {
		return
	}
	for _, job := range jobs {
		if job.AutomationProfile == model.FinalFilmAutomationProfileGuidedDemoV1 && automatedRunnableState(job.State) {
			jobID := job.JobID
			go func() {
				if _, resumeErr := s.ResumeAutomation(context.Background(), jobID); resumeErr != nil {
					_, _ = s.MarkAutomationFailed(context.Background(), jobID, resumeErr)
				}
			}()
		}
	}
}

func automatedRunnableState(state model.FinalFilmJobState) bool {
	switch state {
	case model.FinalFilmJobAnalyzingEvidence, model.FinalFilmJobPlanning, model.FinalFilmJobGeneratingPresentation, model.FinalFilmJobQualityGate, model.FinalFilmJobComposing, model.FinalFilmJobFailed, model.FinalFilmJobAwaitingFinalReview:
		return true
	default:
		return false
	}
}
