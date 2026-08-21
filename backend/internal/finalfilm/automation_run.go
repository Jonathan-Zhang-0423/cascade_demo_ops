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
	"strings"

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
		case model.FinalFilmJobAwaitingFinalReview, model.FinalFilmJobCompleted, model.FinalFilmJobFailed, model.FinalFilmJobRevisionRequested:
			return job, nil
		default:
			return model.FinalFilmJob{}, fmt.Errorf("automatic runner cannot resume state %s", job.State)
		}
	}
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
		intent, attempt, done := nextAutomatedIntent(record.Intents, job.QualityReports, job.AutomationPolicy.ProviderPolicy.MaxAttemptsPerSlot)
		if done {
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
		adapter, _, selectErr := s.providers.Select(ctx, intent, provider)
		if selectErr != nil {
			result := media.GeneratedShotProviderExecutionResult{SchemaVersion: media.GeneratedShotProviderExecutionSchemaVersion, Provider: provider, IntentID: intent.IntentID, Status: media.GeneratedShotFailureContinue, FailurePolicy: media.GeneratedShotFailureContinue, ErrorClass: "provider_selection_failed", ErrorMessage: selectErr.Error()}
			if _, err = s.persistAutomatedProviderResult(ctx, job, record, intent, attempt, result); err != nil {
				return model.FinalFilmJob{}, err
			}
			continue
		}
		callIntent := intent
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
		if _, err = s.persistAutomatedProviderResult(ctx, admitted, record, intent, attempt, result); err != nil {
			return model.FinalFilmJob{}, err
		}
	}
}

func nextAutomatedIntent(intents []media.GeneratedShotIntent, reports []model.CandidateQualityReport, maxAttempts int) (media.GeneratedShotIntent, int, bool) {
	for _, intent := range intents {
		attempts, accepted := 0, false
		for _, report := range reports {
			if report.IntentID != intent.IntentID {
				continue
			}
			if report.Attempt > attempts {
				attempts = report.Attempt
			}
			accepted = accepted || report.Decision == "accept"
		}
		if !accepted && attempts < maxAttempts {
			return intent, attempts + 1, false
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
	for index := range attempts {
		candidate := &attempts[index]
		if candidate.IntentID == intentID && candidate.Provider == provider && candidate.Attempt == attempt {
			return candidate
		}
	}
	return nil
}

func (s *Service) persistAutomatedProviderResult(ctx context.Context, job model.FinalFilmJob, record GeneratedTrackRecord, intent media.GeneratedShotIntent, attempt int, result media.GeneratedShotProviderExecutionResult) (model.FinalFilmJob, error) {
	record.Executions = append(record.Executions, result)
	findings := []string{}
	technicalPass := false
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
			probe, probeErr := s.renderer.ProbeMedia(ctx, executor.MediaProbeRequest{Path: result.Candidate.NormalizedArtifact.Path})
			if probeErr != nil {
				technicalPass = false
				findings = append(findings, "quality_probe_failed: "+probeErr.Error())
			} else if !probe.QualityAnalysisAvailable {
				technicalPass = false
				findings = append(findings, "quality_analysis_unavailable: FFmpeg black/freeze analysis is required")
			} else {
				if probe.BlackDurationMS > 500 {
					technicalPass = false
					findings = append(findings, fmt.Sprintf("black_duration_exceeded: %dms > 500ms", probe.BlackDurationMS))
				}
				if probe.FreezeDurationMS > 1000 {
					technicalPass = false
					findings = append(findings, fmt.Sprintf("freeze_duration_exceeded: %dms > 1000ms", probe.FreezeDurationMS))
				}
			}
		}
	} else if result.ErrorMessage != "" {
		findings = append(findings, result.ErrorMessage)
	}
	decision := "retry"
	if technicalPass {
		decision = "accept"
	} else if attempt >= job.AutomationPolicy.ProviderPolicy.MaxAttemptsPerSlot {
		decision = "fallback_fact_track"
	}
	reportID, _ := s.newID("quality_report")
	report := model.CandidateQualityReport{SchemaVersion: model.CandidateQualityReportSchemaVersion, ReportID: reportID, IntentID: intent.IntentID, Provider: result.Provider, Attempt: attempt, TechnicalPass: technicalPass, ContentPass: technicalPass, Findings: findings, Decision: decision, CheckedAt: s.now().UTC()}
	if technicalPass {
		report.Score = 1
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
	if len(next.ProviderAttempts) > 0 {
		last := &next.ProviderAttempts[len(next.ProviderAttempts)-1]
		if last.IntentID == intent.IntentID && last.Attempt == attempt {
			last.Status, last.ProviderTaskID, last.CompletedAt = decision, result.ProviderTaskID, report.CheckedAt
		}
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

func (s *Service) finishAutomatedComposition(ctx context.Context, job model.FinalFilmJob) (model.FinalFilmJob, error) {
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
	result, err := s.renderer.Render(ctx, executor.RenderRequest{OutputDir: filepath.Join(s.outputRoot, job.JobID, fmt.Sprintf("final-r%d", job.Revision)), DurationSec: maxInt(1, (plan.TargetDurationMS+999)/1000), GeneratedAssets: catalogArtifactRefs(catalog), AssetTimelineCatalog: &catalog, EditPlan: &plan, RenderProfile: &job.RenderProfile, ModelExecution: &executor.RenderModelExecutionAudit{Invoked: len(shotIDs) > 0, Provider: "multiple", PlanSource: "automated_final_output_review_pending", ProviderCallStatus: "candidate_quality_gated", RealCallMade: job.RunAuthorization.ProviderCallsUsed > 0, ProviderOutputAdopted: len(shotIDs) > 0, PatchID: "automated-edl", PatchApplied: true, AdoptedShotIDs: shotIDs, Note: "presentation-only candidates remain subject to final-output review"}})
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	outputValidation, err := validateFinalFilmOutput(ctx, s.renderer, result, job.RenderProfile, plan, s.requireTestNarration, s.now().UTC())
	if err == nil && (outputValidation.DurationMS < job.AutomationPolicy.TargetDuration.MinMS || outputValidation.DurationMS > job.AutomationPolicy.TargetDuration.MaxMS) {
		err = fmt.Errorf("automated final duration %dms is outside 90-120 seconds", outputValidation.DurationMS)
	}
	if err == nil && !outputValidation.QualityAnalysisAvailable {
		err = errors.New("automated final requires FFmpeg black/freeze/loudness analysis")
	}
	if err == nil && outputValidation.BlackDurationMS > 500 {
		err = fmt.Errorf("automated final black duration %dms exceeds 500ms", outputValidation.BlackDurationMS)
	}
	if err == nil {
		// Product UIs legitimately hold a stable result while the viewer reads it.
		// Keep generated candidates on their strict per-shot freeze gate above,
		// but judge the assembled film relative to the factual baseline so chapter
		// packaging cannot add a new long freeze to an otherwise valid demo.
		allowedFreezeMS := 3000
		if baselineProbe, probeErr := s.renderer.ProbeMedia(ctx, executor.MediaProbeRequest{Path: job.BaselineRender.VideoPath}); probeErr == nil && baselineProbe.QualityAnalysisAvailable {
			allowedFreezeMS += baselineProbe.FreezeDurationMS
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
		return model.FinalFilmJob{}, err
	}
	next := job
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	next.State = model.FinalFilmJobAwaitingFinalReview
	next.Phase = "awaiting_final_review"
	next.FinalCatalog, next.FinalPlan = &catalog, &plan
	next.FinalRender = model.FinalFilmRenderOutput{Status: "ready", VideoPath: result.VideoPath, RenderManifestPath: result.RenderManifestPath, PlanID: plan.PlanID, PlanRevision: job.EditorRevision, CompletedAt: next.UpdatedAt}
	next.FinalOutputValidation = &outputValidation
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
		shot := model.DemoEditShot{ID: "generated_shot_" + candidate.CandidateID, SourceArtifactID: artifactID, SourceTimeRangeMS: &rangeMS, Purpose: "Presentation-only generated chapter packaging pending final-output review.", Operations: []model.EditOperation{{Type: model.EditOperationTrim, StartMS: &start, EndMS: &end}, {Type: model.EditOperationTransition, Style: "fade"}}}
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
	plan.PlanID = job.BaselinePlan.PlanID + "+automated"
	plan.TargetDurationMS = timelineDuration(plan)
	return catalog, plan, shotIDs
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
	case model.FinalFilmJobAnalyzingEvidence, model.FinalFilmJobPlanning, model.FinalFilmJobGeneratingPresentation, model.FinalFilmJobQualityGate, model.FinalFilmJobComposing:
		return true
	default:
		return false
	}
}
