package model

import (
	"errors"
	"sort"
	"strings"
	"time"
)

const AdaptiveBusinessHarnessProfileV1 = "adaptive-business-harness-v1"

type BusinessPhase string

const (
	BusinessPhaseWorkspace           BusinessPhase = "workspace"
	BusinessPhaseCreationOpen        BusinessPhase = "creation_open"
	BusinessPhaseInputReady          BusinessPhase = "input_ready"
	BusinessPhaseSubmitting          BusinessPhase = "submitting"
	BusinessPhaseBuildRunning        BusinessPhase = "build_running"
	BusinessPhasePreviewCandidate    BusinessPhase = "preview_candidate"
	BusinessPhasePreviewReady        BusinessPhase = "preview_ready"
	BusinessPhaseInteractionVerified BusinessPhase = "interaction_verified"
)

type BusinessEvidenceChannel struct {
	Kind       string  `json:"kind"`
	Reference  string  `json:"reference,omitempty"`
	Confirmed  bool    `json:"confirmed"`
	Confidence float64 `json:"confidence,omitempty"`
}

type BusinessControlSummary struct {
	SemanticID string  `json:"semantic_id,omitempty"`
	Role       string  `json:"role,omitempty"`
	Enabled    bool    `json:"enabled"`
	Editable   bool    `json:"editable,omitempty"`
	Score      float64 `json:"score,omitempty"`
}

// BusinessStateSnapshot is deliberately site-neutral. Large DOM, screenshot,
// trace, and media payloads stay in artifacts; the runtime event only carries
// compact lifecycle evidence.
type BusinessStateSnapshot struct {
	SchemaVersion    string                    `json:"schema_version"`
	Phase            BusinessPhase             `json:"phase"`
	Confidence       float64                   `json:"confidence"`
	PageRef          string                    `json:"page_ref,omitempty"`
	EntityRef        string                    `json:"entity_ref,omitempty"`
	Controls         []BusinessControlSummary  `json:"controls,omitempty"`
	LifecycleSignals []string                  `json:"lifecycle_signals,omitempty"`
	EvidenceChannels []BusinessEvidenceChannel `json:"evidence_channels"`
	ObservedAt       time.Time                 `json:"observed_at"`
}

type BusinessTransitionStep struct {
	StepID                string                  `json:"step_id"`
	From                  BusinessPhase           `json:"from"`
	To                    BusinessPhase           `json:"to"`
	BusinessIntent        string                  `json:"business_intent"`
	Required              bool                    `json:"required"`
	AllowedActionRoles    []string                `json:"allowed_action_roles,omitempty"`
	EffectID              string                  `json:"effect_id"`
	ReplayPolicy          InteractionReplayPolicy `json:"replay_policy"`
	CompletionConditions  []string                `json:"completion_conditions"`
	SuccessorEvidenceOnly bool                    `json:"successor_evidence_only,omitempty"`
}

type HarnessDecisionKind string

const (
	HarnessDecisionAct     HarnessDecisionKind = "act"
	HarnessDecisionObserve HarnessDecisionKind = "observe"
	HarnessDecisionAdvance HarnessDecisionKind = "advance"
	HarnessDecisionSkip    HarnessDecisionKind = "skip"
	HarnessDecisionDefer   HarnessDecisionKind = "defer"
)

type HarnessDecision struct {
	Kind          HarnessDecisionKind `json:"kind"`
	Confidence    float64             `json:"confidence"`
	Reason        string              `json:"reason"`
	EvidenceRefs  []string            `json:"evidence_refs,omitempty"`
	AbsorbedSteps []string            `json:"absorbed_steps,omitempty"`
}

type ActionEffectCheckpoint struct {
	SchemaVersion  string                  `json:"schema_version"`
	EffectID       string                  `json:"effect_id"`
	StepID         string                  `json:"step_id"`
	ReplayPolicy   InteractionReplayPolicy `json:"replay_policy"`
	Before         BusinessStateSnapshot   `json:"before"`
	ActionOccurred bool                    `json:"action_occurred"`
	Observed       BusinessStateSnapshot   `json:"observed"`
	EntityRef      string                  `json:"entity_ref,omitempty"`
	AbsorbedSteps  []string                `json:"absorbed_steps,omitempty"`
	EvidenceRefs   []string                `json:"evidence_refs,omitempty"`
	CommittedAt    time.Time               `json:"committed_at,omitempty"`
}

type CapabilityResult struct {
	ID       string   `json:"id"`
	Layer    string   `json:"layer"`
	Score    int      `json:"score"`
	Passed   bool     `json:"passed"`
	Evidence []string `json:"evidence_refs,omitempty"`
	Warning  string   `json:"warning,omitempty"`
}

type CapabilityScore struct {
	SchemaVersion    string             `json:"schema_version"`
	CoreScore        int                `json:"core_score"`
	EnhancementScore int                `json:"enhancement_score"`
	TotalScore       int                `json:"total_score"`
	CorePassed       bool               `json:"core_passed"`
	EligibleForFilm  bool               `json:"eligible_for_film"`
	Missing          []string           `json:"missing,omitempty"`
	Results          []CapabilityResult `json:"results"`
}

var businessPhaseOrder = map[BusinessPhase]int{
	BusinessPhaseWorkspace: 1, BusinessPhaseCreationOpen: 2, BusinessPhaseInputReady: 3,
	BusinessPhaseSubmitting: 4, BusinessPhaseBuildRunning: 5, BusinessPhasePreviewCandidate: 6,
	BusinessPhasePreviewReady: 7, BusinessPhaseInteractionVerified: 8,
}

func BusinessPhaseAtLeast(actual, expected BusinessPhase) bool {
	actualOrder, actualOK := businessPhaseOrder[actual]
	expectedOrder, expectedOK := businessPhaseOrder[expected]
	return actualOK && expectedOK && actualOrder >= expectedOrder
}

func DecideBusinessTransition(snapshot BusinessStateSnapshot, step BusinessTransitionStep) HarnessDecision {
	refs := confirmedBusinessEvidenceRefs(snapshot.EvidenceChannels)
	if BusinessPhaseAtLeast(snapshot.Phase, step.To) && snapshot.Confidence >= 0.85 && confirmedBusinessEvidenceCount(snapshot.EvidenceChannels) >= 2 {
		return HarnessDecision{Kind: HarnessDecisionSkip, Confidence: snapshot.Confidence, Reason: "successor state already satisfies the step", EvidenceRefs: refs, AbsorbedSteps: []string{step.StepID}}
	}
	if snapshot.Confidence >= 0.85 && BusinessPhaseAtLeast(snapshot.Phase, step.From) {
		return HarnessDecision{Kind: HarnessDecisionAct, Confidence: snapshot.Confidence, Reason: "current state is suitable for the approved transition", EvidenceRefs: refs}
	}
	if snapshot.Confidence >= 0.65 {
		return HarnessDecision{Kind: HarnessDecisionObserve, Confidence: snapshot.Confidence, Reason: "one more observation is required before acting", EvidenceRefs: refs}
	}
	return HarnessDecision{Kind: HarnessDecisionDefer, Confidence: snapshot.Confidence, Reason: "business state confidence is too low to guess an action", EvidenceRefs: refs}
}

func ScoreCapabilities(results []CapabilityResult) CapabilityScore {
	score := CapabilityScore{SchemaVersion: "demoops.capability_score.v1", Results: append([]CapabilityResult{}, results...)}
	coreCount, corePassed := 0, true
	for _, result := range results {
		switch strings.TrimSpace(result.Layer) {
		case "core":
			coreCount++
			if result.Passed {
				score.CoreScore += result.Score
			} else {
				corePassed = false
				score.Missing = append(score.Missing, result.ID)
			}
		case "enhancement":
			if result.Passed {
				score.EnhancementScore += result.Score
			} else {
				score.Missing = append(score.Missing, result.ID)
			}
		}
	}
	score.TotalScore = score.CoreScore + score.EnhancementScore
	score.CorePassed = coreCount > 0 && corePassed && score.CoreScore == 70
	score.EligibleForFilm = score.CorePassed
	sort.Strings(score.Missing)
	return score
}

func ValidateBusinessStateSnapshot(snapshot BusinessStateSnapshot) error {
	if snapshot.SchemaVersion != "demoops.business_state_snapshot.v1" || !BusinessPhaseAtLeast(snapshot.Phase, snapshot.Phase) || snapshot.Confidence < 0 || snapshot.Confidence > 1 || snapshot.ObservedAt.IsZero() {
		return errors.New("invalid business state snapshot")
	}
	if len(snapshot.EvidenceChannels) == 0 {
		return errors.New("business state snapshot requires evidence channels")
	}
	return nil
}

func confirmedBusinessEvidenceCount(channels []BusinessEvidenceChannel) int {
	seen := map[string]bool{}
	for _, channel := range channels {
		if channel.Confirmed && strings.TrimSpace(channel.Kind) != "" {
			seen[channel.Kind] = true
		}
	}
	return len(seen)
}

func confirmedBusinessEvidenceRefs(channels []BusinessEvidenceChannel) []string {
	refs := []string{}
	seen := map[string]bool{}
	for _, channel := range channels {
		ref := strings.TrimSpace(channel.Reference)
		if channel.Confirmed && ref != "" && !seen[ref] {
			seen[ref] = true
			refs = append(refs, ref)
		}
	}
	return refs
}
