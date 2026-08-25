package model

import (
	"testing"
	"time"
)

func TestDecideBusinessTransitionSkipsSatisfiedSuccessor(t *testing.T) {
	snapshot := BusinessStateSnapshot{
		SchemaVersion: "demoops.business_state_snapshot.v1", Phase: BusinessPhaseBuildRunning, Confidence: 0.96, ObservedAt: time.Now(),
		EvidenceChannels: []BusinessEvidenceChannel{
			{Kind: "url_transition", Reference: "route-evidence", Confirmed: true},
			{Kind: "dom", Reference: "dom-evidence", Confirmed: true},
		},
	}
	decision := DecideBusinessTransition(snapshot, BusinessTransitionStep{StepID: "submit", From: BusinessPhaseInputReady, To: BusinessPhaseBuildRunning})
	if decision.Kind != HarnessDecisionSkip || decision.Confidence != snapshot.Confidence {
		t.Fatalf("successor evidence should skip the already-satisfied action: %+v", decision)
	}
}

func TestDecideBusinessTransitionDoesNotActBelowConfidenceThreshold(t *testing.T) {
	step := BusinessTransitionStep{StepID: "submit", From: BusinessPhaseInputReady, To: BusinessPhaseBuildRunning}
	if decision := DecideBusinessTransition(BusinessStateSnapshot{Phase: BusinessPhaseInputReady, Confidence: 0.8}, step); decision.Kind != HarnessDecisionObserve {
		t.Fatalf("medium-confidence state must request observation: %+v", decision)
	}
	if decision := DecideBusinessTransition(BusinessStateSnapshot{Phase: BusinessPhaseInputReady, Confidence: 0.5}, step); decision.Kind != HarnessDecisionDefer {
		t.Fatalf("low-confidence state must defer: %+v", decision)
	}
}

func TestCapabilityScoreOnlyCoreGatesFilm(t *testing.T) {
	results := []CapabilityResult{
		{ID: "preview", Layer: "core", Score: 20, Passed: true},
		{ID: "two-actions", Layer: "core", Score: 20, Passed: true},
		{ID: "metric", Layer: "core", Score: 15, Passed: true},
		{ID: "stable", Layer: "core", Score: 15, Passed: true},
		{ID: "undo", Layer: "enhancement", Score: 10, Passed: false},
		{ID: "touch", Layer: "enhancement", Score: 8, Passed: true},
		{ID: "win", Layer: "enhancement", Score: 6, Passed: false},
		{ID: "terminal", Layer: "enhancement", Score: 6, Passed: false},
	}
	score := ScoreCapabilities(results)
	if !score.CorePassed || !score.EligibleForFilm || score.CoreScore != 70 || score.EnhancementScore != 8 || score.TotalScore != 78 {
		t.Fatalf("enhancement warnings must not block a core-passing fact track: %+v", score)
	}
	results[1].Passed = false
	score = ScoreCapabilities(results)
	if score.CorePassed || score.EligibleForFilm {
		t.Fatalf("any failed core capability must stop director consumption: %+v", score)
	}
}
