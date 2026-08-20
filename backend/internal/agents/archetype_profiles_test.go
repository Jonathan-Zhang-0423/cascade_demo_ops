package agents

import (
	"testing"

	"cascade-demoops/backend/internal/model"
)

func TestProductArchetypeProfilesUseOnlyObservedInteractionStructure(t *testing.T) {
	step := func(action model.GraphActionType, kind model.BusinessStageKind, outcome string) model.ScriptStep {
		value := model.ScriptStep{Action: model.ScriptActionInstruction{Type: action}, StageKind: kind}
		if outcome != "" {
			value.Validations = []model.ValidationSpec{{Kind: outcome, Required: true}}
		}
		return value
	}
	tests := []struct {
		name  string
		steps []model.ScriptStep
		want  model.ProductArchetype
	}{
		{"async", []model.ScriptStep{step(model.GraphActionClick, model.BusinessStageKindBusinessSubmit, "network_settled"), step(model.GraphActionWait, model.BusinessStageKindObserveProgress, "dom_changed"), step(model.GraphActionInspect, model.BusinessStageKindFinalObserve, "dom_changed")}, model.ProductArchetypeAsyncBuilder},
		{"crud", []model.ScriptStep{step(model.GraphActionFill, model.BusinessStageKindBusinessInput, "value_equals"), step(model.GraphActionSelect, model.BusinessStageKindBusinessInput, "value_equals"), step(model.GraphActionClick, model.BusinessStageKindBusinessSubmit, "dom_changed")}, model.ProductArchetypeCRUDForm},
		{"dashboard", []model.ScriptStep{step(model.GraphActionInspect, model.BusinessStageKindFinalObserve, "dom_changed"), step(model.GraphActionWait, model.BusinessStageKindFinalObserve, "network_settled")}, model.ProductArchetypeDashboard},
		{"canvas", []model.ScriptStep{step(model.GraphActionClick, model.BusinessStageKindBusinessAction, "visual_region_changed"), step(model.GraphActionClick, model.BusinessStageKindBusinessAction, "visual_region_changed"), step(model.GraphActionInspect, model.BusinessStageKindFinalObserve, "visual_region_changed")}, model.ProductArchetypeCanvasEditor},
		{"interactive", []model.ScriptStep{step(model.GraphActionInspect, model.BusinessStageKindFinalObserve, "interactive_surface_visible"), step(model.GraphActionPress, model.BusinessStageKindFinalObserve, "frame_surface_changed")}, model.ProductArchetypeInteractive},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := productArchetypeForSteps(test.steps); got != test.want {
				t.Fatalf("archetype=%q want %q", got, test.want)
			}
		})
	}
}
