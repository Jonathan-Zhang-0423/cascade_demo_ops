package app

import (
	"strings"
	"testing"

	"cascade-demoops/backend/internal/experiment"
)

func TestAdaptiveProductRepairPromptUsesOnlyEarliestFailedCapability(t *testing.T) {
	spec := experiment.ProductSpec{ObservableAcceptance: []experiment.AcceptanceCriterion{
		{ID: "surface", Statement: "完整产品表面可见", Required: true},
		{ID: "terminal", Statement: "终局状态可用", Required: true},
	}}
	plan := experiment.InteractionPlan{Steps: []experiment.InteractionStep{
		{StepID: "directional_moves", SemanticIntent: "使用两个不同方向键执行实际操作"},
		{StepID: "terminal_scenes", SemanticIntent: "观察胜利和无可移动演示状态"},
	}}

	got := adaptiveProductRepairPrompt(spec, plan, []string{"directional_moves", "terminal_scenes"})
	want := "请修复当前项目：使用两个不同方向键执行实际操作必须能实际工作。保留已有内容，直接更新当前项目。"
	if got != want {
		t.Fatalf("repair prompt = %q, want %q", got, want)
	}
	if strings.Contains(got, "终局") || len([]rune(got)) > 140 {
		t.Fatalf("repair prompt was not root-cause scoped: %q", got)
	}
}

func TestAdaptiveProductRepairPromptFallsBackToOneRequiredCriterion(t *testing.T) {
	spec := experiment.ProductSpec{ObservableAcceptance: []experiment.AcceptanceCriterion{
		{ID: "first", Statement: "首个必需条件", Required: true},
		{ID: "second", Statement: "第二个必需条件", Required: true},
	}}
	got := adaptiveProductRepairPrompt(spec, experiment.InteractionPlan{}, []string{"unknown"})
	if !strings.Contains(got, "首个必需条件") || strings.Contains(got, "第二个必需条件") {
		t.Fatalf("fallback prompt was not bounded to one criterion: %q", got)
	}
}
