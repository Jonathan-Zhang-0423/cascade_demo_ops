package app

import (
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestAdaptiveSessionSetupRequiresAuthenticatedSuccessorRoute(t *testing.T) {
	stage := BrowserAgentRuntimeStage{
		ID: "login", StageKind: model.BusinessStageKindSessionSetup,
		ExpectedRouteAfterAction: "https://app.example.test/workspace",
		TargetContract:           model.BrowserAgentTargetContract{SemanticID: "login", Confidence: 0.9},
	}
	login := &model.RuntimeObservation{Source: model.RuntimeObservationActualBrowser, URL: "https://app.example.test/login"}
	snapshot := adaptiveBusinessSnapshot(stage, login, nil, true, time.Now())
	decision := adaptiveBusinessDecision(stage, login, snapshot)
	if decision.Kind != model.HarnessDecisionAct {
		t.Fatalf("login entry must require the authentication effect, got %+v", decision)
	}
	workspace := &model.RuntimeObservation{Source: model.RuntimeObservationActualBrowser, URL: "https://app.example.test/workspace"}
	snapshot = adaptiveBusinessSnapshot(stage, workspace, nil, true, time.Now())
	decision = adaptiveBusinessDecision(stage, workspace, snapshot)
	if decision.Kind != model.HarnessDecisionSkip {
		t.Fatalf("authenticated workspace should satisfy session setup, got %+v", decision)
	}
}

func TestAdaptiveFinalObserveDoesNotClaimPreviewCandidateFromPlanPosition(t *testing.T) {
	stage := BrowserAgentRuntimeStage{
		ID: "wait-for-product", StageKind: model.BusinessStageKindFinalObserve,
		TargetContract: model.BrowserAgentTargetContract{SemanticID: "primary_interactive_surface", AllowedRoles: []string{"application"}, Confidence: 0.9},
	}
	placeholder := &model.RuntimeObservation{Source: model.RuntimeObservationActualBrowser, URL: "https://app.example.test/project/new"}
	snapshot := adaptiveBusinessSnapshot(stage, placeholder, nil, true, time.Now())
	if snapshot.Phase != model.BusinessPhaseBuildRunning {
		t.Fatalf("a planned final observation promoted an unverified placeholder: %+v", snapshot)
	}
	decision := adaptiveBusinessDecision(stage, placeholder, snapshot)
	if decision.Kind != model.HarnessDecisionObserve {
		t.Fatalf("the observer should still execute its non-mutating proof step: %+v", decision)
	}
}
