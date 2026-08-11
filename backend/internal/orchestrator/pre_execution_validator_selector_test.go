package orchestrator

import (
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestPreExecutionSelectorCheckRejectsUnprovenRuntimeAdaptivePrimary(t *testing.T) {
	stage := model.StageApprovalStage{
		NodeID:          "node_create",
		RuntimeAdaptive: true,
		Interaction: model.BrowserAgentInteraction{
			Kind: model.GraphActionClick,
			Target: model.ActionTarget{
				Selector: `[data-testid='guessed-create']`,
			},
		},
		TargetContract: &model.BrowserAgentTargetContract{SemanticID: "semantic_create", Purpose: "Create project"},
	}
	check := (&PreExecutionValidator{}).buildSelectorCheck(stage, stage.NodeID)
	if check.Passed || !check.Required || check.RiskLevel != "critical" {
		t.Fatalf("unproven runtime-adaptive primary selector was not blocked: %+v", check)
	}
}

func TestPreExecutionSelectorCheckAcceptsEvidenceBoundRuntimeAdaptivePrimary(t *testing.T) {
	evidence := model.EvidenceRef{ID: "ev_create", Kind: model.EvidenceKindBrowserScan}
	candidate := model.SelectorCandidate{
		Kind: "testid", Value: "create-project", EvidenceID: evidence.ID, SourceKind: "page_scan", SourceDigest: "sha256:page",
		ObservedRole: "button", ObservedAccessibleName: "Create project", ObservedAt: time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC), EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 0.9,
	}
	stage := model.StageApprovalStage{
		NodeID:          "node_create",
		RuntimeAdaptive: true,
		Interaction: model.BrowserAgentInteraction{
			Kind: model.GraphActionClick,
			Target: model.ActionTarget{
				Selector:             `[data-testid='create-project']`,
				SelectorAlternatives: []model.SelectorCandidate{candidate},
			},
		},
	}
	check := (&PreExecutionValidator{}).buildSelectorCheck(stage, stage.NodeID)
	if !check.Passed || check.Target != stage.Interaction.Target.Selector || check.RiskLevel != "low" {
		t.Fatalf("evidence-bound runtime-adaptive selector was rejected: %+v", check)
	}
}
