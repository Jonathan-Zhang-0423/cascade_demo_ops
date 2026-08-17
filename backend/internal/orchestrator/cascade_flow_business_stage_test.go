package orchestrator

import (
	"testing"

	"cascade-demoops/backend/internal/model"
)

func TestBusinessStagePlanUsableFailsClosedWithoutVerifiedPageEvidence(t *testing.T) {
	plan := &model.BusinessStagePlan{
		CoreBusinessStageCount: 1,
		Stages: []model.BusinessStage{{
			ID:   "business_stage_start_agent_build",
			Kind: model.BusinessStageKindBusinessSubmit,
			Targets: []model.BusinessTargetCandidate{{
				Label: "启动 agent 构建", IsVerified: false,
			}},
		}},
	}
	if businessStagePlanUsable(plan) {
		t.Fatal("a generic model-generated business stage must not bypass a blocking missing-evidence report")
	}
	plan.Stages[0].Targets[0].IsVerified = true
	plan.Stages[0].Targets[0].EvidenceRefs = []model.EvidenceRef{{ID: "ev_build_button", Kind: model.EvidenceKindBrowserScan}}
	if !businessStagePlanUsable(plan) {
		t.Fatal("a verified page-evidence-bound business stage should remain usable")
	}
}
