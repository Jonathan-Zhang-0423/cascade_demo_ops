package agents

import (
	"testing"

	"cascade-demoops/backend/internal/model"
)

func TestProductArchetypeRoutingUsesInteractionStructure(t *testing.T) {
	tests := []struct {
		name  string
		steps []model.ScriptStep
		want  model.ProductArchetype
	}{
		{"interactive", []model.ScriptStep{{Action: model.ScriptActionInstruction{Type: model.GraphActionPress}}}, model.ProductArchetypeInteractive},
		{"async builder", []model.ScriptStep{
			{StageKind: model.BusinessStageKindBusinessSubmit, Action: model.ScriptActionInstruction{Type: model.GraphActionClick}},
			{StageKind: model.BusinessStageKindObserveProgress, Action: model.ScriptActionInstruction{Type: model.GraphActionWait}},
			{StageKind: model.BusinessStageKindFinalObserve, Action: model.ScriptActionInstruction{Type: model.GraphActionInspect}},
		}, model.ProductArchetypeAsyncBuilder},
		{"crud", []model.ScriptStep{
			{Action: model.ScriptActionInstruction{Type: model.GraphActionFill}},
			{Action: model.ScriptActionInstruction{Type: model.GraphActionSelect}},
			{Action: model.ScriptActionInstruction{Type: model.GraphActionClick}},
		}, model.ProductArchetypeCRUDForm},
		{"dashboard", []model.ScriptStep{
			{Action: model.ScriptActionInstruction{Type: model.GraphActionInspect}},
			{Action: model.ScriptActionInstruction{Type: model.GraphActionWait}},
		}, model.ProductArchetypeDashboard},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := productArchetypeForSteps(test.steps); got != test.want {
				t.Fatalf("got %q want %q", got, test.want)
			}
		})
	}
}

func TestProductArchetypeRoutingIsHostnameIndependent(t *testing.T) {
	base := []model.ScriptStep{
		{PageTarget: model.ScriptPageTarget{URL: "https://first.example/a"}, Action: model.ScriptActionInstruction{Type: model.GraphActionFill}},
		{PageTarget: model.ScriptPageTarget{URL: "https://first.example/b"}, Action: model.ScriptActionInstruction{Type: model.GraphActionSelect}},
		{PageTarget: model.ScriptPageTarget{URL: "https://first.example/c"}, Action: model.ScriptActionInstruction{Type: model.GraphActionClick}},
	}
	want := productArchetypeForSteps(base)
	for index := range base {
		base[index].PageTarget.URL = "https://unrelated.invalid/changed"
	}
	if got := productArchetypeForSteps(base); got != want {
		t.Fatalf("hostname changed routing: got %q want %q", got, want)
	}
}
