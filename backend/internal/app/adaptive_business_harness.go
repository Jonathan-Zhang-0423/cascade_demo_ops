package app

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

func adaptiveBusinessHarnessEnabled(plan BrowserAgentRuntimePlan) bool {
	return strings.TrimSpace(plan.HarnessProfile) == model.AdaptiveBusinessHarnessProfileV1
}

func businessTransitionForStage(stage BrowserAgentRuntimeStage) model.BusinessTransitionStep {
	step := model.BusinessTransitionStep{
		StepID: stage.ID, BusinessIntent: firstNonEmptyString(stage.BusinessIntent, stage.Objective),
		Required: true, EffectID: stage.ID, ReplayPolicy: stageReplayPolicy(stage),
		AllowedActionRoles:   append([]string{}, stage.TargetContract.AllowedRoles...),
		CompletionConditions: []string{"two_independent_evidence_channels"},
	}
	switch stage.StageKind {
	case model.BusinessStageKindSessionSetup:
		step.From, step.To = model.BusinessPhaseWorkspace, model.BusinessPhaseWorkspace
	case model.BusinessStageKindBusinessAction:
		step.From, step.To = model.BusinessPhaseWorkspace, model.BusinessPhaseCreationOpen
	case model.BusinessStageKindBusinessInput:
		step.From, step.To = model.BusinessPhaseCreationOpen, model.BusinessPhaseInputReady
	case model.BusinessStageKindModeSelection:
		step.From, step.To = model.BusinessPhaseInputReady, model.BusinessPhaseInputReady
		step.Required = false
	case model.BusinessStageKindBusinessSubmit:
		step.From, step.To = model.BusinessPhaseInputReady, model.BusinessPhaseBuildRunning
	case model.BusinessStageKindObserveProgress:
		step.From, step.To = model.BusinessPhaseBuildRunning, model.BusinessPhasePreviewCandidate
	case model.BusinessStageKindFinalObserve:
		step.From, step.To = model.BusinessPhasePreviewCandidate, model.BusinessPhasePreviewReady
	default:
		step.From, step.To = model.BusinessPhaseWorkspace, model.BusinessPhaseWorkspace
	}
	return step
}

func adaptiveBusinessSnapshot(stage BrowserAgentRuntimeStage, observation *model.RuntimeObservation, before *model.RuntimeObservation, targetResolved bool, observedAt time.Time) model.BusinessStateSnapshot {
	snapshot := model.BusinessStateSnapshot{
		SchemaVersion: "demoops.business_state_snapshot.v1", Phase: businessTransitionForStage(stage).From,
		Confidence: 0.7, ObservedAt: observedAt.UTC(),
	}
	if observation == nil {
		return snapshot
	}
	snapshot.PageRef = strings.TrimSpace(observation.URL)
	if runtimeObservationIsRealEvidence(observation.Source) && snapshot.PageRef != "" {
		snapshot.EvidenceChannels = append(snapshot.EvidenceChannels, model.BusinessEvidenceChannel{
			Kind: "page_state", Reference: snapshot.PageRef, Confirmed: true, Confidence: 0.8,
		})
	}
	if targetResolved {
		snapshot.Controls = append(snapshot.Controls, model.BusinessControlSummary{
			SemanticID: stage.TargetContract.SemanticID, Role: firstNonEmptyString(stage.TargetContract.AllowedRoles...), Enabled: true, Score: stage.TargetContract.Confidence,
		})
		snapshot.EvidenceChannels = append(snapshot.EvidenceChannels, model.BusinessEvidenceChannel{Kind: "action_target", Reference: firstRuntimeEvidenceID(observation), Confirmed: true, Confidence: 0.85})
	}
	if routeAdvancedForBusinessStage(stage, before, observation) {
		snapshot.Phase = businessSuccessorPhase(stage)
		snapshot.EntityRef = strings.TrimSpace(observation.URL)
		snapshot.LifecycleSignals = append(snapshot.LifecycleSignals, "route_advanced", "entity_identity_observed")
		snapshot.EvidenceChannels = append(snapshot.EvidenceChannels,
			model.BusinessEvidenceChannel{Kind: "url_transition", Reference: strings.TrimSpace(observation.URL), Confirmed: true, Confidence: 1},
		)
	}
	if fingerprintChanged(before, observation, "document") {
		snapshot.LifecycleSignals = append(snapshot.LifecycleSignals, "dom_changed")
		snapshot.EvidenceChannels = append(snapshot.EvidenceChannels, model.BusinessEvidenceChannel{Kind: "dom", Reference: firstRuntimeEvidenceID(observation), Confirmed: true, Confidence: 0.9})
	}
	if fingerprintChanged(before, observation, "aria") {
		snapshot.LifecycleSignals = append(snapshot.LifecycleSignals, "aria_changed")
		snapshot.EvidenceChannels = append(snapshot.EvidenceChannels, model.BusinessEvidenceChannel{Kind: "aria", Reference: firstRuntimeEvidenceID(observation), Confirmed: true, Confidence: 0.9})
	}
	confirmed := distinctConfirmedChannelCount(snapshot.EvidenceChannels)
	if model.BusinessPhaseAtLeast(snapshot.Phase, businessTransitionForStage(stage).To) && confirmed >= 2 {
		snapshot.Confidence = 0.95
	} else if targetResolved {
		snapshot.Confidence = 0.85
	} else if confirmed < 1 {
		snapshot.Confidence = 0.55
	}
	return snapshot
}

func businessSuccessorPhase(stage BrowserAgentRuntimeStage) model.BusinessPhase {
	transition := businessTransitionForStage(stage)
	if stage.StageKind == model.BusinessStageKindModeSelection {
		// A configuration click that leaves the creation container and lands on
		// a newly identified entity is semantically the submit transition.
		return model.BusinessPhaseBuildRunning
	}
	return transition.To
}

func routeAdvancedForBusinessStage(stage BrowserAgentRuntimeStage, before, after *model.RuntimeObservation) bool {
	if after == nil || strings.TrimSpace(after.URL) == "" {
		return false
	}
	left := ""
	if before != nil {
		left = strings.TrimSpace(before.URL)
	}
	if left == "" {
		left = firstNonEmptyString(stage.EntryRoute, stage.Route, stage.URL)
	}
	leftURL, leftErr := url.Parse(left)
	rightURL, rightErr := url.Parse(strings.TrimSpace(after.URL))
	if leftErr != nil || rightErr != nil || rightURL.Host == "" {
		return false
	}
	if !leftURL.IsAbs() {
		base, baseErr := url.Parse(firstNonEmptyString(stage.URL, after.URL))
		if baseErr != nil {
			return false
		}
		leftURL = base.ResolveReference(leftURL)
	}
	if leftURL.Host != rightURL.Host || strings.TrimSuffix(leftURL.EscapedPath(), "/") == strings.TrimSuffix(rightURL.EscapedPath(), "/") {
		return false
	}
	return true
}

func fingerprintChanged(before, after *model.RuntimeObservation, kind string) bool {
	if before == nil || after == nil || before.StateFingerprint == nil || after.StateFingerprint == nil {
		return false
	}
	switch kind {
	case "document":
		return before.StateFingerprint.DocumentDigest != "" && after.StateFingerprint.DocumentDigest != "" && before.StateFingerprint.DocumentDigest != after.StateFingerprint.DocumentDigest
	case "aria":
		return before.StateFingerprint.ARIADigest != "" && after.StateFingerprint.ARIADigest != "" && before.StateFingerprint.ARIADigest != after.StateFingerprint.ARIADigest
	default:
		return false
	}
}

func firstRuntimeEvidenceID(observation *model.RuntimeObservation) string {
	if observation == nil {
		return ""
	}
	for _, assertion := range observation.Assertions {
		for _, ref := range assertion.EvidenceRefs {
			if strings.TrimSpace(ref.ID) != "" {
				return ref.ID
			}
		}
	}
	return "runtime_observation"
}

func distinctConfirmedChannelCount(channels []model.BusinessEvidenceChannel) int {
	seen := map[string]bool{}
	for _, channel := range channels {
		if channel.Confirmed {
			kind := businessEvidenceFamily(channel.Kind)
			if kind != "" {
				seen[kind] = true
			}
		}
	}
	return len(seen)
}

func businessEvidenceFamily(kind string) string {
	switch strings.TrimSpace(kind) {
	case "page_state", "url_transition":
		return "navigation"
	default:
		return strings.TrimSpace(kind)
	}
}

func evidenceStrings(refs []model.EvidenceRef) []string {
	values := make([]string, 0, len(refs))
	for _, ref := range refs {
		if value := strings.TrimSpace(ref.ID); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func modelEvidenceRefs(values []string) []model.EvidenceRef {
	refs := make([]model.EvidenceRef, 0, len(values))
	for _, value := range values {
		if id := strings.TrimSpace(value); id != "" {
			refs = append(refs, model.EvidenceRef{ID: id, Kind: model.EvidenceKindExecutionRun, Summary: "adaptive business lifecycle evidence", Confidence: 1})
		}
	}
	return refs
}

func adaptiveTransitionAbsorption(plan BrowserAgentRuntimePlan, stage BrowserAgentRuntimeStage, before, after *model.RuntimeObservation, evidence []model.EvidenceRef) (*model.ActionEffectCheckpoint, string) {
	if !adaptiveBusinessHarnessEnabled(plan) || stage.StageKind != model.BusinessStageKindModeSelection || !routeAdvancedForBusinessStage(stage, before, after) {
		return nil, ""
	}
	snapshotBefore := adaptiveBusinessSnapshot(stage, before, nil, true, timeNowUTC())
	snapshotAfter := adaptiveBusinessSnapshot(stage, after, before, false, timeNowUTC())
	if snapshotAfter.Confidence < 0.85 || distinctConfirmedChannelCount(snapshotAfter.EvidenceChannels) < 2 {
		return nil, ""
	}
	absorbed := ""
	for _, candidate := range plan.Stages {
		if candidate.Order > stage.Order && candidate.StageKind == model.BusinessStageKindBusinessSubmit {
			absorbed = candidate.ID
			break
		}
	}
	if absorbed == "" {
		return nil, ""
	}
	checkpoint := &model.ActionEffectCheckpoint{
		SchemaVersion: "demoops.action_effect_checkpoint.v1", EffectID: absorbed, StepID: stage.ID,
		ReplayPolicy: model.InteractionReplayOnceEffect, Before: snapshotBefore, ActionOccurred: true,
		Observed: snapshotAfter, EntityRef: snapshotAfter.EntityRef, AbsorbedSteps: []string{absorbed},
		EvidenceRefs: evidenceStrings(evidence), CommittedAt: timeNowUTC(),
	}
	return checkpoint, absorbed
}

func historicalAdaptiveAbsorptions(plan BrowserAgentRuntimePlan, events []model.StageExecutionEvent) map[string]*model.ActionEffectCheckpoint {
	result := map[string]*model.ActionEffectCheckpoint{}
	if !adaptiveBusinessHarnessEnabled(plan) {
		return result
	}
	for _, stage := range plan.Stages {
		if stage.StageKind != model.BusinessStageKindModeSelection {
			continue
		}
		var before, after *model.RuntimeObservation
		evidence := []model.EvidenceRef{}
		for index := range events {
			event := events[index]
			if event.StageID != stage.ID || event.Observation == nil {
				continue
			}
			switch event.EventType {
			case model.StageExecutionEventObservationCollected:
				if before == nil {
					copyValue := *event.Observation
					before = &copyValue
				}
			case model.StageExecutionEventActionCompleted, model.StageExecutionEventOutcomeObserved, model.StageExecutionEventStageCompleted:
				copyValue := *event.Observation
				after = &copyValue
				evidence = append(evidence, event.EvidenceRefs...)
			}
		}
		if checkpoint, absorbed := adaptiveTransitionAbsorption(plan, stage, before, after, evidence); checkpoint != nil {
			result[absorbed] = checkpoint
		}
	}
	return result
}

func stageCapability(stage BrowserAgentRuntimeStage) (string, int, bool) {
	parameters := map[string]any(nil)
	if stage.InteractionContract != nil {
		parameters = stage.InteractionContract.Parameters
	}
	if len(parameters) == 0 && len(stage.Interactions) > 0 {
		parameters = stage.Interactions[0].Parameters
	}
	if len(parameters) == 0 {
		return "", 0, false
	}
	layer, _ := parameters["capability_layer"].(string)
	score := 0
	switch value := parameters["capability_score"].(type) {
	case int:
		score = value
	case float64:
		score = int(value)
	case json.Number:
		score, _ = strconv.Atoi(value.String())
	}
	return strings.TrimSpace(layer), score, (layer == "core" || layer == "enhancement") && score > 0
}

func capabilityResultForStage(stage BrowserAgentRuntimeStage, passed bool, evidence []model.EvidenceRef, warning string) (model.CapabilityResult, bool) {
	layer, score, ok := stageCapability(stage)
	if !ok {
		return model.CapabilityResult{}, false
	}
	return model.CapabilityResult{ID: stage.ID, Layer: layer, Score: score, Passed: passed, Evidence: evidenceStrings(evidence), Warning: warning}, true
}

func runtimeObservationPassed(observation *model.RuntimeObservation) bool {
	if observation == nil || len(observation.Assertions) == 0 {
		return false
	}
	for _, assertion := range observation.Assertions {
		if !assertion.Passed {
			return false
		}
	}
	return true
}

func optionalCompletionObservation(observation model.RuntimeObservation, summary string) model.RuntimeObservation {
	copyObservation := observation
	copyObservation.Assertions = []model.RuntimeAssertion{{Kind: "optional_capability_recorded", Passed: true, Actual: summary}}
	return copyObservation
}

func normalizeEnhancementDecision(report model.ValidationReport, stage BrowserAgentRuntimeStage, evidence []model.EvidenceRef) (model.ValidationReport, bool) {
	layer, _, ok := stageCapability(stage)
	if !ok || layer != "enhancement" || report.Decision == model.ValidationDecisionContinue || len(evidence) == 0 {
		return report, false
	}
	report.Decision = model.ValidationDecisionContinue
	report.EvidenceQuality = model.RuntimeObservationAssertion
	if len(report.EvidenceRefs) == 0 {
		report.EvidenceRefs = append([]model.EvidenceRef{}, evidence...)
	}
	for index := range report.Checks {
		if !report.Checks[index].Passed {
			report.Checks[index].Severity = model.FindingSeverityWarning
		}
	}
	return report, true
}
