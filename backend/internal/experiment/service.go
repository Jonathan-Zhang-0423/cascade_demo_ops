package experiment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

type ServiceOptions struct {
	Store          Store
	DefinitionRoot string
	Now            func() time.Time
	NewID          func(string) (string, error)
}

type Service struct {
	store          Store
	definitionRoot string
	now            func() time.Time
	newID          func(string) (string, error)
}

func NewService(options ServiceOptions) (*Service, error) {
	if options.Store == nil || strings.TrimSpace(options.DefinitionRoot) == "" {
		return nil, errors.New("experiment store and definition root are required")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	newID := options.NewID
	if newID == nil {
		newID = randomID
	}
	return &Service{store: options.Store, definitionRoot: options.DefinitionRoot, now: now, newID: newID}, nil
}

func (s *Service) CreateRun(ctx context.Context, request CreateRunRequest) (Run, error) {
	if err := ValidateCreateRequest(request); err != nil {
		return Run{}, err
	}
	request.DefinitionRef = strings.TrimSpace(request.DefinitionRef)
	request.TargetURL = strings.TrimSpace(request.TargetURL)
	request.CredentialRef = strings.TrimSpace(request.CredentialRef)
	request.AuthorizationRef = strings.TrimSpace(request.AuthorizationRef)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if existing, ok, err := s.store.FindByIdempotencyKey(ctx, request.IdempotencyKey); err != nil {
		return Run{}, err
	} else if ok {
		if existing.DefinitionRef != request.DefinitionRef || existing.TargetURL != request.TargetURL || existing.CredentialRef != request.CredentialRef || existing.AuthorizationRef != request.AuthorizationRef {
			return Run{}, errors.New("idempotency_key is already bound to a different experiment request")
		}
		return existing, nil
	}
	loaded, err := LoadDefinition(s.definitionRoot, request.DefinitionRef)
	if err != nil {
		return Run{}, fmt.Errorf("load experiment definition: %w", err)
	}
	runID, err := s.newID("experiment")
	if err != nil {
		return Run{}, err
	}
	mainProjectName := uniqueExperimentProjectName(loaded.Definition.MainProjectName, runID)
	recoveryProjectName := uniqueExperimentProjectName(loaded.Definition.RecoveryProjectName, runID)
	mainPrompt, err := CompileBuildPrompt(loaded.ProductSpec, mainProjectName)
	if err != nil {
		return Run{}, err
	}
	recoveryPrompt, err := CompileBuildPrompt(loaded.ProductSpec, recoveryProjectName)
	if err != nil {
		return Run{}, err
	}
	now := s.now().UTC()
	run := Run{
		SchemaVersion: RunSchemaVersion, RunID: runID, DefinitionRef: request.DefinitionRef, DefinitionID: loaded.Definition.DefinitionID,
		WorkflowTemplate: loaded.Definition.WorkflowTemplateID, TargetURL: request.TargetURL, CredentialRef: request.CredentialRef,
		AuthorizationRef: request.AuthorizationRef, IdempotencyKey: request.IdempotencyKey,
		State: RunStateQueued, Phase: "product_spec_frozen", Revision: 1, Budget: loaded.Definition.AuthorizationBudget,
		ProductSpec: loaded.ProductSpec, ObservationPlan: loaded.ObservationPlan, InteractionPlan: loaded.InteractionPlan,
		Legs: []RunLeg{
			{LegID: runID + ":main", Kind: "main", ProjectName: mainProjectName, BuildPrompt: mainPrompt, State: RunStateQueued, Phase: "awaiting_execution", BrowserAttempt: 1},
			{LegID: runID + ":recovery", Kind: "recovery", ProjectName: recoveryProjectName, BuildPrompt: recoveryPrompt, State: RunStateCreated, Phase: "awaiting_main_completion", BrowserAttempt: 1},
		},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := ValidateRun(run); err != nil {
		return Run{}, err
	}
	event := s.event(run, "experiment_admitted", "实验定义、产品规格和预算已冻结", "", nil)
	if err := s.store.Create(ctx, run, event); err != nil {
		return Run{}, err
	}
	return run, nil
}

func uniqueExperimentProjectName(base, runID string) string {
	base, runID = strings.TrimSpace(base), strings.TrimSpace(runID)
	if len(runID) > 6 {
		runID = runID[len(runID)-6:]
	}
	if runID == "" {
		return base
	}
	return base + " · " + strings.ToUpper(runID)
}

func (s *Service) GetRun(ctx context.Context, runID string) (Run, error) {
	return s.store.Get(ctx, strings.TrimSpace(runID))
}

func (s *Service) ListEvents(ctx context.Context, runID string) ([]Event, error) {
	return s.store.ListEvents(ctx, strings.TrimSpace(runID))
}

func (s *Service) ListRuns(ctx context.Context) ([]Run, error) {
	return s.store.ListRuns(ctx)
}

type LegTransitionRequest struct {
	ExpectedRevision int
	LegID            string
	State            RunState
	Phase            string
	EventType        string
	Summary          string
	EvidenceRefs     []string
	LastError        *RunError
}

func (s *Service) TransitionLeg(ctx context.Context, runID string, request LegTransitionRequest) (Run, error) {
	run, err := s.store.Get(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	if run.Revision != request.ExpectedRevision || !validRunState(request.State) || strings.TrimSpace(request.Phase) == "" || strings.TrimSpace(request.EventType) == "" || strings.TrimSpace(request.Summary) == "" {
		return Run{}, errors.New("leg transition requires current revision, valid state, phase, event type, and summary")
	}
	index := legIndex(run, request.LegID)
	if index < 0 {
		return Run{}, errors.New("experiment leg was not found")
	}
	if !legalRunStateTransition(run.Legs[index].State, request.State) {
		return Run{}, fmt.Errorf("illegal experiment leg state transition %s -> %s", run.Legs[index].State, request.State)
	}
	next := run
	next.Legs[index].State, next.Legs[index].Phase = request.State, strings.TrimSpace(request.Phase)
	if request.LastError != nil {
		failure := *request.LastError
		failure.EvidenceRefs = append([]string{}, request.LastError.EvidenceRefs...)
		next.LastError = &failure
	}
	next.State, next.Phase = aggregateRunState(next)
	if next.State == RunStateWaitingInput && next.Waiting == nil {
		next.Waiting = &WaitingState{Reason: "实验等待人工输入或最终终审", Responsibility: "user", NextAction: "查看当前 phase 和 Gate 证据"}
	} else if next.State != RunStateWaitingInput {
		next.Waiting = nil
	}
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	if terminalRunState(next.State) {
		next.TerminalAt = next.UpdatedAt
	}
	event := s.event(next, request.EventType, request.Summary, request.LegID, request.EvidenceRefs)
	if err := s.store.Transition(ctx, run.RunID, run.Revision, next, event); err != nil {
		return Run{}, err
	}
	return next, nil
}

func (s *Service) BeginOnceEffect(ctx context.Context, runID string, expectedRevision int, legID, effectID, kind, idempotencyKey string) (Run, error) {
	run, err := s.store.Get(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	if run.Revision != expectedRevision || len(strings.TrimSpace(idempotencyKey)) < 8 || strings.TrimSpace(effectID) == "" || (kind != "target_submission" && kind != "continuation") {
		return Run{}, errors.New("once-effect begin requires current revision, identity, kind, and idempotency key")
	}
	index := legIndex(run, legID)
	if index < 0 {
		return Run{}, errors.New("experiment leg was not found")
	}
	next := run
	checkpoint := next.Legs[index].Checkpoint
	if checkpoint == nil {
		checkpointID, idErr := s.newID("checkpoint")
		if idErr != nil {
			return Run{}, idErr
		}
		checkpoint = &Checkpoint{CheckpointID: checkpointID, VerifiedPhase: next.Legs[index].Phase, CreatedAt: s.now().UTC()}
	}
	for _, existing := range checkpoint.OnceEffects {
		if existing.EffectID != effectID {
			continue
		}
		if existing.IdempotencyKey == idempotencyKey && existing.Status == "started" {
			return run, nil
		}
		return Run{}, errors.New("once-effect cannot be started again after it has been recorded")
	}
	checkpoint.OnceEffects = append(checkpoint.OnceEffects, OnceEffectRecord{EffectID: effectID, Kind: kind, Status: "started", IdempotencyKey: idempotencyKey, StartedAt: s.now().UTC()})
	next.Legs[index].Checkpoint = checkpoint
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	event := s.event(next, "once_effect_started", "已在动作前保存 once-effect checkpoint", legID, nil)
	if err := s.store.Transition(ctx, run.RunID, run.Revision, next, event); err != nil {
		return Run{}, err
	}
	return next, nil
}

type CommitOnceEffectRequest struct {
	ExpectedRevision    int
	LegID               string
	EffectID            string
	StateFingerprintRef string
	ResultEntryRef      string
	EvidenceRefs        []string
	SegmentRefs         []ArtifactRef
}

func (s *Service) CommitOnceEffect(ctx context.Context, runID string, request CommitOnceEffectRequest) (Run, error) {
	run, err := s.store.Get(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	if run.Revision != request.ExpectedRevision || strings.TrimSpace(request.StateFingerprintRef) == "" || len(request.EvidenceRefs) == 0 {
		return Run{}, errors.New("once-effect commit requires current revision, state fingerprint, and result evidence")
	}
	index := legIndex(run, request.LegID)
	if index < 0 || run.Legs[index].Checkpoint == nil {
		return Run{}, errors.New("once-effect checkpoint was not found")
	}
	next := run
	checkpoint := next.Legs[index].Checkpoint
	found := false
	for effectIndex := range checkpoint.OnceEffects {
		record := &checkpoint.OnceEffects[effectIndex]
		if record.EffectID != request.EffectID {
			continue
		}
		if record.Status == "confirmed" {
			return run, nil
		}
		if record.Status != "started" {
			return Run{}, errors.New("only a started once-effect can be confirmed")
		}
		record.Status = "confirmed"
		record.EvidenceRefs = append([]string{}, request.EvidenceRefs...)
		record.ConfirmedAt = s.now().UTC()
		if record.Kind == "target_submission" {
			next.Legs[index].TargetSubmissions++
		}
		found = true
		break
	}
	if !found || next.Legs[index].TargetSubmissions > 1 {
		return Run{}, errors.New("once-effect is missing or would exceed the target-submission budget")
	}
	checkpoint.VerifiedPhase = "once_effect_committed"
	checkpoint.StateFingerprintRef = strings.TrimSpace(request.StateFingerprintRef)
	checkpoint.ResultEntryRef = strings.TrimSpace(request.ResultEntryRef)
	checkpoint.SegmentRefs = append(checkpoint.SegmentRefs, request.SegmentRefs...)
	checkpoint.CreatedAt = s.now().UTC()
	next.Legs[index].Checkpoint = checkpoint
	next.Legs[index].Phase = "once_effect_committed"
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	event := s.event(next, "once_effect_committed", "目标站点结果已确认，once-effect 不得重放", request.LegID, request.EvidenceRefs)
	if err := s.store.Transition(ctx, run.RunID, run.Revision, next, event); err != nil {
		return Run{}, err
	}
	return next, nil
}

func (s *Service) BindOnceEffectExternalTask(ctx context.Context, runID string, expectedRevision int, legID, effectID, externalTaskRef string, evidenceRefs []string) (Run, error) {
	run, err := s.store.Get(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	externalTaskRef = strings.TrimSpace(externalTaskRef)
	if run.Revision != expectedRevision || externalTaskRef == "" {
		return Run{}, errors.New("external task binding requires current revision and an opaque task ref")
	}
	index := legIndex(run, legID)
	if index < 0 || run.Legs[index].Checkpoint == nil {
		return Run{}, errors.New("once-effect checkpoint was not found")
	}
	next := run
	found := false
	for recordIndex := range next.Legs[index].Checkpoint.OnceEffects {
		record := &next.Legs[index].Checkpoint.OnceEffects[recordIndex]
		if record.EffectID != effectID {
			continue
		}
		if record.Status != "started" {
			return Run{}, errors.New("only a started once-effect can bind an external task")
		}
		if record.ExternalTaskRef != "" && record.ExternalTaskRef != externalTaskRef {
			return Run{}, errors.New("once-effect is already bound to a different external task")
		}
		record.ExternalTaskRef = externalTaskRef
		record.EvidenceRefs = append([]string{}, evidenceRefs...)
		found = true
		break
	}
	if !found {
		return Run{}, errors.New("started once-effect was not found")
	}
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	event := s.event(next, "once_effect_external_task_bound", "外部任务 ID 已持久化，恢复时只能续接同一任务", legID, evidenceRefs)
	if err := s.store.Transition(ctx, run.RunID, run.Revision, next, event); err != nil {
		return Run{}, err
	}
	return next, nil
}

// RejectOnceEffect clears a started record only when the external system has
// returned an authoritative pre-admission rejection. It must never be used for
// a timeout, connection loss, or any response that could have hidden a receipt.
func (s *Service) RejectOnceEffect(ctx context.Context, runID string, expectedRevision int, legID, effectID, summary string) (Run, error) {
	run, err := s.store.Get(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	if run.Revision != expectedRevision || strings.TrimSpace(effectID) == "" {
		return Run{}, errors.New("once-effect rejection requires current revision and effect identity")
	}
	index := legIndex(run, legID)
	if index < 0 || run.Legs[index].Checkpoint == nil {
		return Run{}, errors.New("once-effect checkpoint was not found")
	}
	next := run
	checkpoint := next.Legs[index].Checkpoint
	kept := make([]OnceEffectRecord, 0, len(checkpoint.OnceEffects))
	found := false
	for _, record := range checkpoint.OnceEffects {
		if record.EffectID == effectID && record.Status == "started" && strings.TrimSpace(record.ExternalTaskRef) == "" {
			found = true
			continue
		}
		kept = append(kept, record)
	}
	if !found {
		return Run{}, errors.New("only an unbound started once-effect can be rejected")
	}
	checkpoint.OnceEffects = kept
	if len(checkpoint.OnceEffects) == 0 && len(checkpoint.SegmentRefs) == 0 && checkpoint.StateFingerprintRef == "" && checkpoint.ResultEntryRef == "" {
		next.Legs[index].Checkpoint = nil
	}
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	summary = strings.TrimSpace(summary)
	if summary == "" {
		summary = "外部系统在任务接收前明确拒绝 once-effect"
	}
	event := s.event(next, "once_effect_rejected", summary, legID, nil)
	if err := s.store.Transition(ctx, run.RunID, run.Revision, next, event); err != nil {
		return Run{}, err
	}
	return next, nil
}

func (s *Service) MarkOnceEffectUncertain(ctx context.Context, runID string, expectedRevision int, legID, effectID string, evidenceRefs []string, failures ...*RunError) (Run, error) {
	run, err := s.store.Get(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	if run.Revision != expectedRevision {
		return Run{}, ErrRevisionConflict
	}
	index := legIndex(run, legID)
	if index < 0 || run.Legs[index].Checkpoint == nil {
		return Run{}, errors.New("once-effect checkpoint was not found")
	}
	next := run
	found := false
	for recordIndex := range next.Legs[index].Checkpoint.OnceEffects {
		record := &next.Legs[index].Checkpoint.OnceEffects[recordIndex]
		if record.EffectID == effectID && record.Status == "started" {
			record.Status = "uncertain"
			record.EvidenceRefs = append([]string{}, evidenceRefs...)
			found = true
		}
	}
	if !found {
		return Run{}, errors.New("started once-effect was not found")
	}
	next.Legs[index].State, next.Legs[index].Phase = RunStateWaitingInput, "once_effect_uncertain"
	next.State, next.Phase = RunStateWaitingInput, "once_effect_uncertain"
	next.Waiting = &WaitingState{Reason: "once-effect 的外部结果无法确认", Responsibility: "user", NextAction: "确认目标实体是否已经创建；系统不会自动重放"}
	if len(failures) > 0 && failures[0] != nil {
		failure := *failures[0]
		failure.EvidenceRefs = append([]string{}, failures[0].EvidenceRefs...)
		next.LastError = &failure
	}
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	event := s.event(next, "once_effect_uncertain", "once-effect 状态不确定，已停止自动重放", legID, evidenceRefs)
	if err := s.store.Transition(ctx, run.RunID, run.Revision, next, event); err != nil {
		return Run{}, err
	}
	return next, nil
}

func (s *Service) Resume(ctx context.Context, runID string, expectedRevision int) (Run, error) {
	run, err := s.store.Get(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	if run.Revision != expectedRevision {
		return Run{}, ErrRevisionConflict
	}
	if terminalRunState(run.State) {
		return Run{}, errors.New("terminal experiment run cannot be resumed")
	}
	for _, leg := range run.Legs {
		if leg.Checkpoint == nil {
			continue
		}
		for _, record := range leg.Checkpoint.OnceEffects {
			if record.Status == "uncertain" || (record.Status == "started" && strings.TrimSpace(record.ExternalTaskRef) == "") {
				next := run
				next.State, next.Phase = RunStateWaitingInput, "once_effect_uncertain"
				next.Waiting = &WaitingState{Reason: "once-effect 尚未获得可重放证明", Responsibility: "user", NextAction: "核对目标站点结果后提供输入"}
				next.Revision++
				next.UpdatedAt = s.now().UTC()
				event := s.event(next, "resume_deferred", "恢复已停止：once-effect 不允许自动重放", leg.LegID, record.EvidenceRefs)
				if err := s.store.Transition(ctx, run.RunID, run.Revision, next, event); err != nil {
					return Run{}, err
				}
				return next, nil
			}
		}
	}
	next := run
	next.State, next.Phase = RunStateQueued, "resume_checkpoint_verification"
	next.Waiting = nil
	next.LastError = nil
	resumeIndex, resumePhase := -1, ""
	for index := range next.Legs {
		leg := &next.Legs[index]
		if terminalRunState(leg.State) || leg.Checkpoint == nil {
			continue
		}
		for _, record := range leg.Checkpoint.OnceEffects {
			switch {
			case record.Status == "confirmed":
				resumeIndex, resumePhase = index, "resume_observe_only"
			case record.Status == "started" && strings.TrimSpace(record.ExternalTaskRef) != "" && resumeIndex < 0:
				resumeIndex, resumePhase = index, "resume_external_task"
			}
		}
		if resumeIndex == index {
			break
		}
	}
	if resumeIndex < 0 {
		for index := range next.Legs {
			if next.Legs[index].State == RunStateWaitingExternal || next.Legs[index].State == RunStateRunning {
				resumeIndex, resumePhase = index, "resume_checkpoint_verification"
				break
			}
		}
	}
	// A freshly admitted run may be resumed before the scheduler has claimed
	// its first leg. Requeue only that already-queued leg; this compatibility
	// path must not select a later recovery leg while an earlier leg is waiting.
	if resumeIndex < 0 && run.State == RunStateQueued {
		for index := range next.Legs {
			if next.Legs[index].State == RunStateQueued {
				resumeIndex = index
				resumePhase = strings.TrimSpace(next.Legs[index].Phase)
				if resumePhase == "" {
					resumePhase = "resume_checkpoint_verification"
				}
				break
			}
		}
	}
	if resumeIndex < 0 {
		return Run{}, errors.New("experiment has no resumable active leg")
	}
	next.Legs[resumeIndex].State = RunStateQueued
	next.Legs[resumeIndex].Phase = resumePhase
	next.Legs[resumeIndex].BrowserAttempt++
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	event := s.event(next, "run_resumed", "从已绑定 checkpoint 恢复；调度器只续接原活动腿", next.Legs[resumeIndex].LegID, nil)
	if err := s.store.Transition(ctx, run.RunID, run.Revision, next, event); err != nil {
		return Run{}, err
	}
	return next, nil
}

// Cancel terminates a non-terminal experiment without replaying or otherwise
// touching any external effect. It is deliberately revision-bound so an
// operator cannot cancel a newer phase based on a stale projection.
func (s *Service) Cancel(ctx context.Context, runID string, expectedRevision int, reason string) (Run, error) {
	run, err := s.store.Get(ctx, strings.TrimSpace(runID))
	if err != nil {
		return Run{}, err
	}
	if run.Revision != expectedRevision {
		return Run{}, ErrRevisionConflict
	}
	if terminalRunState(run.State) {
		return Run{}, errors.New("terminal experiment run cannot be canceled")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "experiment canceled by operator"
	}
	next := run
	next.State, next.Phase = RunStateCanceled, "canceled"
	next.Waiting = nil
	for index := range next.Legs {
		if !terminalRunState(next.Legs[index].State) {
			next.Legs[index].State = RunStateCanceled
			next.Legs[index].Phase = "canceled"
		}
	}
	next.LastError = &RunError{Code: "experiment_canceled", Message: reason, Retryable: false, Responsibility: "operator"}
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	next.TerminalAt = next.UpdatedAt
	next.Report = buildReport(next)
	event := s.event(next, "run_canceled", "实验已停止；不会恢复或重放外部动作", "", nil)
	if err := s.store.Transition(ctx, run.RunID, run.Revision, next, event); err != nil {
		return Run{}, err
	}
	return next, nil
}

func (s *Service) RecordVisualCall(ctx context.Context, runID string, expectedRevision int, legID string, evidenceRefs []string) (Run, error) {
	run, err := s.store.Get(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	if run.Revision != expectedRevision {
		return Run{}, ErrRevisionConflict
	}
	index := legIndex(run, legID)
	if index < 0 || run.Legs[index].VisualCallsUsed >= run.Budget.VisualCallsPerRun {
		return Run{}, errors.New("visual observation budget exhausted")
	}
	next := run
	next.Legs[index].VisualCallsUsed++
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	event := s.event(next, "visual_observation_recorded", "已记录一次时序视觉观察", legID, evidenceRefs)
	if err := s.store.Transition(ctx, run.RunID, run.Revision, next, event); err != nil {
		return Run{}, err
	}
	return next, nil
}

func (s *Service) BindFinalFilm(ctx context.Context, runID string, expectedRevision int, jobID string, revision, providerCalls int, packageIDs ...string) (Run, error) {
	run, err := s.store.Get(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	if run.Revision != expectedRevision || strings.TrimSpace(jobID) == "" || revision < 1 || providerCalls < 0 || providerCalls > run.Budget.ProviderCalls {
		return Run{}, errors.New("final film binding or provider budget is invalid")
	}
	if run.FinalFilm != nil && run.FinalFilm.JobID != strings.TrimSpace(jobID) {
		return Run{}, errors.New("experiment run is already bound to a different FinalFilm job")
	}
	if providerCalls < run.ProviderCallsUsed {
		return Run{}, errors.New("experiment provider consumption cannot decrease")
	}
	next := run
	decision, packageID := "", ""
	if next.FinalFilm != nil {
		decision, packageID = next.FinalFilm.Decision, next.FinalFilm.PackageID
	}
	if len(packageIDs) > 0 && strings.TrimSpace(packageIDs[0]) != "" {
		packageID = strings.TrimSpace(packageIDs[0])
	}
	next.FinalFilm = &FinalFilmBinding{JobID: strings.TrimSpace(jobID), Revision: revision, Decision: decision, PackageID: packageID}
	next.ProviderCallsUsed = providerCalls
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	event := s.event(next, "final_film_bound", "主跑已绑定唯一 FinalFilm 作业", next.Legs[0].LegID, nil)
	if err := s.store.Transition(ctx, run.RunID, run.Revision, next, event); err != nil {
		return Run{}, err
	}
	return next, nil
}

func (s *Service) AppendLegArtifacts(ctx context.Context, runID string, expectedRevision int, legID string, artifacts []ArtifactRef, eventType, summary string) (Run, error) {
	run, err := s.store.Get(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	if run.Revision != expectedRevision || len(artifacts) == 0 || strings.TrimSpace(eventType) == "" || strings.TrimSpace(summary) == "" {
		return Run{}, errors.New("artifact append requires current revision, artifacts, event type, and summary")
	}
	index := legIndex(run, legID)
	if index < 0 {
		return Run{}, errors.New("experiment leg was not found")
	}
	next := run
	seen := map[string]bool{}
	for _, artifact := range next.Legs[index].ArtifactRefs {
		seen[artifact.ArtifactID+fmt.Sprintf(":%d", artifact.Revision)] = true
	}
	evidenceRefs := []string{}
	for _, artifact := range artifacts {
		if strings.TrimSpace(artifact.ArtifactID) == "" || artifact.Revision < 1 || strings.TrimSpace(artifact.Role) == "" {
			return Run{}, errors.New("experiment artifact identity, revision, and role are required")
		}
		key := artifact.ArtifactID + fmt.Sprintf(":%d", artifact.Revision)
		if !seen[key] {
			next.Legs[index].ArtifactRefs = append(next.Legs[index].ArtifactRefs, artifact)
			seen[key] = true
		}
		evidenceRefs = append(evidenceRefs, artifact.ArtifactID)
	}
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	if err := s.store.Transition(ctx, run.RunID, run.Revision, next, s.event(next, eventType, summary, legID, evidenceRefs)); err != nil {
		return Run{}, err
	}
	return next, nil
}

func (s *Service) RecordHumanIntervention(ctx context.Context, runID string, expectedRevision int, legID, kind string, allowed bool) (Run, error) {
	run, err := s.store.Get(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	if run.Revision != expectedRevision || strings.TrimSpace(kind) == "" {
		return Run{}, errors.New("human intervention requires current revision and kind")
	}
	index := legIndex(run, legID)
	if index < 0 {
		return Run{}, errors.New("experiment leg was not found")
	}
	if allowed && kind != "final_review" {
		return Run{}, errors.New("only final review is an allowed mid-run human intervention")
	}
	next := run
	next.Legs[index].HumanInterventions = append(next.Legs[index].HumanInterventions, HumanIntervention{Kind: kind, Allowed: allowed, OccurredAt: s.now().UTC()})
	if !allowed {
		next.State, next.Phase = RunStateFailed, "automation_attribution_invalid"
		next.Legs[index].State, next.Legs[index].Phase = RunStateFailed, "automation_attribution_invalid"
		next.LastError = &RunError{Code: "out_of_band_human_intervention", Message: "实验检测到未授权的中途人工操作", Retryable: false, Responsibility: "operator"}
		next.TerminalAt = s.now().UTC()
	}
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	next.Report = buildReport(next)
	if err := s.store.Transition(ctx, run.RunID, run.Revision, next, s.event(next, "human_intervention_recorded", "已记录人工介入并更新自动化归因", legID, nil)); err != nil {
		return Run{}, err
	}
	return next, nil
}

func (s *Service) RecordFinalReview(ctx context.Context, runID string, expectedRevision int, decision, packageID string) (Run, error) {
	run, err := s.store.Get(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	decision, packageID = strings.TrimSpace(decision), strings.TrimSpace(packageID)
	if run.Revision != expectedRevision || run.FinalFilm == nil || (decision != "accept" && decision != "reject") || packageID == "" {
		return Run{}, errors.New("final review must bind the current experiment revision and FinalFilm package")
	}
	next := run
	next.FinalFilm.PackageID, next.FinalFilm.Decision = packageID, decision
	next.Legs[0].HumanInterventions = append(next.Legs[0].HumanInterventions, HumanIntervention{Kind: "final_review", Allowed: true, OccurredAt: s.now().UTC()})
	if decision == "accept" && next.Legs[0].State == RunStateSucceeded && next.Legs[1].State == RunStateSucceeded {
		next.State, next.Phase = RunStateSucceeded, "completed_after_final_review"
	} else if decision == "reject" {
		next.State, next.Phase = RunStateWaitingInput, "final_revision_requested"
		next.Waiting = &WaitingState{Reason: "最终成片被拒绝", Responsibility: "user", NextAction: "修改导演要求并重新授权模型消费"}
	}
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	if terminalRunState(next.State) {
		next.TerminalAt = next.UpdatedAt
	}
	next.Report = buildReport(next)
	event := s.event(next, "final_review_recorded", "已记录实验唯一最终终审", next.Legs[0].LegID, nil)
	if err := s.store.Transition(ctx, run.RunID, run.Revision, next, event); err != nil {
		return Run{}, err
	}
	return next, nil
}

func (s *Service) event(run Run, eventType, summary, legID string, evidenceRefs []string) Event {
	return Event{SchemaVersion: EventSchemaVersion, EventID: fmt.Sprintf("event_%s_%d", run.RunID, run.Revision), RunID: run.RunID, Type: eventType, State: run.State, Phase: run.Phase, LegID: legID, Summary: summary, EvidenceRefs: append([]string{}, evidenceRefs...), CreatedAt: s.now().UTC()}
}

func legIndex(run Run, legID string) int {
	for index := range run.Legs {
		if run.Legs[index].LegID == legID {
			return index
		}
	}
	return -1
}

func aggregateRunState(run Run) (RunState, string) {
	for _, leg := range run.Legs {
		if leg.State == RunStateFailed {
			return RunStateFailed, leg.Phase
		}
		if leg.State == RunStateWaitingInput {
			return RunStateWaitingInput, leg.Phase
		}
	}
	if run.Legs[0].State == RunStateSucceeded && run.Legs[1].State == RunStateSucceeded {
		if run.FinalFilm != nil && run.FinalFilm.Decision == "accept" {
			return RunStateSucceeded, "completed_after_final_review"
		}
		return RunStateWaitingInput, "awaiting_final_review"
	}
	for _, leg := range run.Legs {
		if leg.State == RunStateRunning {
			return RunStateRunning, leg.Phase
		}
		if leg.State == RunStateWaitingExternal {
			return RunStateWaitingExternal, leg.Phase
		}
		if leg.State == RunStateQueued {
			return RunStateQueued, leg.Phase
		}
	}
	return run.State, run.Phase
}

func legalRunStateTransition(from, to RunState) bool {
	if from == to {
		return true
	}
	allowed := map[RunState]map[RunState]bool{
		RunStateCreated:         {RunStateAdmitted: true, RunStateQueued: true, RunStateCanceled: true},
		RunStateAdmitted:        {RunStateQueued: true, RunStateCanceled: true, RunStateExpired: true},
		RunStateQueued:          {RunStateRunning: true, RunStateCanceled: true, RunStateExpired: true},
		RunStateRunning:         {RunStateWaitingInput: true, RunStateWaitingExternal: true, RunStateSucceeded: true, RunStateFailed: true, RunStateCanceled: true},
		RunStateWaitingExternal: {RunStateQueued: true, RunStateRunning: true, RunStateWaitingInput: true, RunStateSucceeded: true, RunStateFailed: true, RunStateCanceled: true, RunStateExpired: true},
		RunStateWaitingInput:    {RunStateQueued: true, RunStateCanceled: true, RunStateExpired: true},
	}
	return allowed[from][to]
}

func terminalRunState(value RunState) bool {
	return value == RunStateSucceeded || value == RunStateFailed || value == RunStateCanceled || value == RunStateExpired
}

func buildReport(run Run) *RunReport {
	report := &RunReport{SchemaVersion: ReportSchemaVersion, RunID: run.RunID, Scores: map[string]float64{"orchestration": 0, "product": 0, "evidence": 0, "film": 0, "recovery": 0}, ModelConsumption: ModelConsumption{ProviderCalls: run.ProviderCallsUsed}, Automation: AutomationAttribution{AllowedHumanGates: 1}}
	for _, leg := range run.Legs {
		report.ModelConsumption.VisualCalls += leg.VisualCallsUsed
		report.HumanInterventions = append(report.HumanInterventions, leg.HumanInterventions...)
		for _, intervention := range leg.HumanInterventions {
			if !intervention.Allowed {
				report.Automation.OutOfBandBrowserActions++
			}
		}
		report.ArtifactRefs = append(report.ArtifactRefs, leg.ArtifactRefs...)
		if leg.Checkpoint != nil {
			report.OnceEffects = append(report.OnceEffects, leg.Checkpoint.OnceEffects...)
		}
	}
	report.Automation.SystemActions = len(report.ArtifactRefs) + len(report.OnceEffects)
	report.Automation.AllowedHumanGates += len(report.HumanInterventions)
	report.Valid = run.State == RunStateSucceeded && report.Automation.OutOfBandBrowserActions == 0 && report.Automation.AllowedHumanGates <= 2
	if report.Valid {
		report.Scores = map[string]float64{"orchestration": 100, "product": 100, "evidence": 100, "film": 100, "recovery": 100}
	}
	return report
}

func randomID(prefix string) (string, error) {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return strings.TrimSpace(prefix) + "_" + hex.EncodeToString(bytes), nil
}
