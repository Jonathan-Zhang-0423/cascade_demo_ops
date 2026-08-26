package experiment

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

type LegExecutionRequest struct {
	RunID               string
	LegID               string
	Kind                string
	ProjectName         string
	BuildPrompt         string
	TargetURL           string
	CredentialRef       string
	AuthorizationRef    string
	BrowserAttempt      int
	Checkpoint          *Checkpoint
	ExistingArtifacts   []ArtifactRef
	FinalFilm           *FinalFilmBinding
	RunReport           RunReport
	ProductSpec         ProductSpec
	ObservationPlan     ObservationPlan
	InteractionPlan     InteractionPlan
	VisualCallBudget    int
	WorkflowTemplateID  string
	HarnessProfile      string
	RunStartedAt        time.Time
	ProductRepairRounds int
}

type LegExecutionUpdate struct {
	Kind                string
	Phase               string
	Summary             string
	EvidenceRefs        []string
	EffectID            string
	EffectKind          string
	IdempotencyKey      string
	StateFingerprintRef string
	ResultEntryRef      string
	ExternalTaskRef     string
	Artifacts           []ArtifactRef
	SegmentRefs         []ArtifactRef
	FinalFilmJobID      string
	FinalFilmRevision   int
	ProviderCallsUsed   int
	FinalFilmPackageID  string
	CapabilityScore     *CapabilitySummary
	EntityName          string
	EntityCreatedAt     time.Time
	EntityTaskRef       string
	RepairDirective     *model.RepairDirective
}

type LegExecutionAdapter interface {
	DescribeCapabilities(context.Context) (ModuleManifest, error)
	ExecuteLeg(context.Context, LegExecutionRequest, func(LegExecutionUpdate) error) error
}

type ModuleManifest struct {
	ModuleID                 string
	Version                  string
	Capabilities             []string
	SupportedReplay          []ReplayPolicy
	SupportsSegmentedCapture bool
}

type AdapterError struct {
	Code         string
	Phase        string
	State        RunState
	Retryable    bool
	EffectID     string
	EvidenceRefs []string
	Cause        error
}

func (e *AdapterError) Error() string {
	if e.Cause != nil {
		return e.Code + ": " + e.Cause.Error()
	}
	return e.Code
}

type Runner struct{ service *Service }

func NewRunner(service *Service) (*Runner, error) {
	if service == nil {
		return nil, errors.New("experiment runner requires a coordinator service")
	}
	return &Runner{service: service}, nil
}

func (r *Runner) RunLeg(ctx context.Context, runID, legID string, adapter LegExecutionAdapter) (Run, error) {
	if adapter == nil {
		return Run{}, errors.New("experiment leg adapter is required")
	}
	manifest, err := adapter.DescribeCapabilities(ctx)
	if err != nil {
		return Run{}, err
	}
	if err := validateLegAdapterManifest(manifest); err != nil {
		return Run{}, err
	}
	run, err := r.service.GetRun(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	index := legIndex(run, legID)
	if index < 0 {
		return Run{}, errors.New("experiment leg was not found")
	}
	leg := run.Legs[index]
	if leg.State == RunStateCreated {
		run, err = r.service.TransitionLeg(ctx, runID, LegTransitionRequest{ExpectedRevision: run.Revision, LegID: legID, State: RunStateQueued, Phase: "adapter_admitted", EventType: "module_admitted", Summary: "执行与录制模块已通过能力门禁"})
		if err != nil {
			return Run{}, err
		}
	}
	run, err = r.service.TransitionLeg(ctx, runID, LegTransitionRequest{ExpectedRevision: run.Revision, LegID: legID, State: RunStateRunning, Phase: "execution_capture_starting", EventType: "module_started", Summary: "执行与录制模块开始运行"})
	if err != nil {
		return Run{}, err
	}
	index = legIndex(run, legID)
	leg = run.Legs[index]
	request := LegExecutionRequest{
		RunID: run.RunID, LegID: leg.LegID, Kind: leg.Kind, ProjectName: leg.ProjectName, BuildPrompt: leg.BuildPrompt,
		TargetURL: run.TargetURL, CredentialRef: run.CredentialRef, AuthorizationRef: run.AuthorizationRef,
		BrowserAttempt: leg.BrowserAttempt, Checkpoint: leg.Checkpoint, ExistingArtifacts: append([]ArtifactRef{}, leg.ArtifactRefs...), FinalFilm: run.FinalFilm,
		RunReport: *buildReport(run), ProductSpec: run.ProductSpec, ObservationPlan: run.ObservationPlan,
		InteractionPlan: run.InteractionPlan, VisualCallBudget: run.Budget.VisualCallsPerRun - leg.VisualCallsUsed,
		WorkflowTemplateID:  run.WorkflowTemplate,
		HarnessProfile:      run.HarnessProfile,
		RunStartedAt:        run.CreatedAt,
		ProductRepairRounds: leg.ProductRepairRounds,
	}
	emit := func(update LegExecutionUpdate) error {
		current, getErr := r.service.GetRun(ctx, runID)
		if getErr != nil {
			return getErr
		}
		switch update.Kind {
		case "phase":
			_, getErr = r.service.TransitionLeg(ctx, runID, LegTransitionRequest{ExpectedRevision: current.Revision, LegID: legID, State: RunStateRunning, Phase: update.Phase, EventType: "module_progress", Summary: requiredSummary(update.Summary, "执行模块阶段已更新"), EvidenceRefs: update.EvidenceRefs})
		case "once_effect_started":
			_, getErr = r.service.BeginOnceEffect(ctx, runID, current.Revision, legID, update.EffectID, update.EffectKind, update.IdempotencyKey)
		case "once_effect_committed":
			_, getErr = r.service.CommitOnceEffect(ctx, runID, CommitOnceEffectRequest{ExpectedRevision: current.Revision, LegID: legID, EffectID: update.EffectID, StateFingerprintRef: update.StateFingerprintRef, ResultEntryRef: update.ResultEntryRef, EvidenceRefs: update.EvidenceRefs, SegmentRefs: update.SegmentRefs, EntityName: update.EntityName, EntityCreatedAt: update.EntityCreatedAt, EntityTaskRef: update.EntityTaskRef})
		case "once_effect_admitted":
			_, getErr = r.service.BindOnceEffectExternalTask(ctx, runID, current.Revision, legID, update.EffectID, update.ExternalTaskRef, update.EvidenceRefs)
		case "checkpoint_result_entry":
			_, getErr = r.service.AdvanceCheckpointResultEntry(ctx, runID, current.Revision, legID, update.ResultEntryRef, update.EvidenceRefs)
		case "once_effect_rejected":
			_, getErr = r.service.RejectOnceEffect(ctx, runID, current.Revision, legID, update.EffectID, update.Summary)
		case "visual_observation":
			updated, visualErr := r.service.RecordVisualCall(ctx, runID, current.Revision, legID, update.EvidenceRefs)
			if visualErr != nil {
				return visualErr
			}
			if len(update.Artifacts) > 0 {
				_, getErr = r.service.AppendLegArtifacts(ctx, runID, updated.Revision, legID, update.Artifacts, "visual_observation_materialized", requiredSummary(update.Summary, "时序视觉观察已物化"))
			}
		case "artifacts":
			_, getErr = r.service.AppendLegArtifacts(ctx, runID, current.Revision, legID, update.Artifacts, "artifacts_materialized", requiredSummary(update.Summary, "模块 Artifact 已物化"))
		case "capability_scored":
			if update.CapabilityScore == nil {
				return errors.New("capability_scored update requires a score")
			}
			_, getErr = r.service.RecordCapabilityScore(ctx, runID, current.Revision, legID, *update.CapabilityScore, update.EvidenceRefs)
		case "repair_directive":
			if update.RepairDirective == nil {
				return errors.New("repair_directive update requires a directive")
			}
			_, getErr = r.service.RecordRepairDirective(ctx, runID, current.Revision, legID, *update.RepairDirective)
		case "final_film_bound":
			_, getErr = r.service.BindFinalFilm(ctx, runID, current.Revision, update.FinalFilmJobID, update.FinalFilmRevision, update.ProviderCallsUsed, update.FinalFilmPackageID)
		default:
			getErr = fmt.Errorf("unsupported experiment adapter update %q", update.Kind)
		}
		return getErr
	}
	adapterErr := adapter.ExecuteLeg(ctx, request, emit)
	if adapterErr != nil {
		return r.handleAdapterError(ctx, runID, legID, adapterErr)
	}
	run, err = r.service.GetRun(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	return r.service.TransitionLeg(ctx, runID, LegTransitionRequest{ExpectedRevision: run.Revision, LegID: legID, State: RunStateSucceeded, Phase: "fact_track_ready", EventType: "module_succeeded", Summary: "执行、交互证据与事实轨基线已完成"})
}

func (r *Runner) handleAdapterError(ctx context.Context, runID, legID string, err error) (Run, error) {
	var adapterErr *AdapterError
	if !errors.As(err, &adapterErr) {
		adapterErr = &AdapterError{Code: "adapter_unclassified_failure", Phase: "adapter_failed", State: RunStateFailed, Retryable: false, Cause: err}
	}
	run, getErr := r.service.GetRun(ctx, runID)
	if getErr != nil {
		return Run{}, getErr
	}
	if adapterErr.Code == "once_effect_result_unknown" && strings.TrimSpace(adapterErr.EffectID) != "" {
		failure := adapterRunError(adapterErr)
		next, markErr := r.service.MarkOnceEffectUncertain(ctx, runID, run.Revision, legID, adapterErr.EffectID, adapterErr.EvidenceRefs, &failure)
		if markErr != nil {
			return next, markErr
		}
		return next, adapterErr
	}
	state := adapterErr.State
	if state == "" {
		state = RunStateFailed
	}
	phase := strings.TrimSpace(adapterErr.Phase)
	if phase == "" {
		phase = strings.TrimSpace(adapterErr.Code)
	}
	failure := adapterRunError(adapterErr)
	next, transitionErr := r.service.TransitionLeg(ctx, runID, LegTransitionRequest{ExpectedRevision: run.Revision, LegID: legID, State: state, Phase: phase, EventType: "module_interrupted", Summary: "执行模块已按确定性故障策略停止", EvidenceRefs: adapterErr.EvidenceRefs, LastError: &failure})
	if transitionErr != nil {
		return Run{}, transitionErr
	}
	return next, adapterErr
}

func adapterRunError(value *AdapterError) RunError {
	message := strings.TrimSpace(value.Error())
	const maxAdapterErrorMessageBytes = 2 << 10
	if len(message) > maxAdapterErrorMessageBytes {
		message = message[:maxAdapterErrorMessageBytes]
	}
	responsibility := "system"
	if value.State == RunStateWaitingExternal {
		responsibility = "external"
	}
	return RunError{Code: value.Code, Message: message, Retryable: value.Retryable, Responsibility: responsibility, EvidenceRefs: append([]string{}, value.EvidenceRefs...)}
}

func validateLegAdapterManifest(manifest ModuleManifest) error {
	if strings.TrimSpace(manifest.ModuleID) == "" || strings.TrimSpace(manifest.Version) == "" || len(manifest.Capabilities) == 0 || !manifest.SupportsSegmentedCapture {
		return errors.New("execution adapter manifest lacks identity, capabilities, or segmented capture")
	}
	policies := map[ReplayPolicy]bool{}
	for _, policy := range manifest.SupportedReplay {
		policies[policy] = true
	}
	if !policies[ReplayObserveOnly] || !policies[ReplayIdempotentWrite] || !policies[ReplayOnceEffect] {
		return errors.New("execution adapter must support all replay policies")
	}
	return nil
}

func requiredSummary(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}
