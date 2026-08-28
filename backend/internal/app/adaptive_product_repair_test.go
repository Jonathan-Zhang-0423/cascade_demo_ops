package app

import (
	"strings"
	"testing"

	"cascade-demoops/backend/internal/experiment"
	"cascade-demoops/backend/internal/model"
)

func TestAdaptiveProductRepairRequiresAsyncRepairToBecomeIdleBeforeProof(t *testing.T) {
	contracts := []model.InteractionContract{
		{ReplayPolicy: model.InteractionReplayIdempotentWrite, ActionKind: model.GraphActionClick},
		{ReplayPolicy: model.InteractionReplayObserveOnly, ActionKind: model.GraphActionInspect},
		{ReplayPolicy: model.InteractionReplayObserveOnly, ActionKind: model.GraphActionInspect},
	}
	markAdaptiveRepairInteractionContracts(contracts)
	if contracts[0].Parameters != nil || contracts[1].Parameters["require_repair_idle_transition"] != true || contracts[2].Parameters != nil {
		t.Fatalf("repair idle policy was not scoped to the first passive product proof: %+v", contracts)
	}
}

func TestAdaptiveProductRepairPromptRepairsOnlyFirstCausalFailure(t *testing.T) {
	spec := experiment.ProductSpec{VisualDirection: experiment.VisualDirection{Theme: "深色霓虹界面"}, ObservableAcceptance: []experiment.AcceptanceCriterion{
		{ID: "surface", Statement: "完整产品表面可见", Required: true},
		{ID: "terminal", Statement: "终局状态可用", Required: true},
	}}
	plan := experiment.InteractionPlan{Steps: []experiment.InteractionStep{
		{StepID: "directional_moves", SemanticIntent: "使用两个不同方向键执行实际操作", Action: experiment.InteractionAction{Kind: "keyboard_sequence"}},
		{StepID: "terminal_scenes", SemanticIntent: "观察胜利和无可移动演示状态", Action: experiment.InteractionAction{Kind: "activate_state_variants"}},
	}}

	got := adaptiveProductRepairPrompt(spec, plan, []string{"directional_moves", "terminal_scenes"})
	for _, required := range []string{"只修复这个问题", "不要重写页面", "使用两个不同方向键执行实际操作"} {
		if !strings.Contains(got, required) {
			t.Fatalf("causal repair prompt lost %q: %q", required, got)
		}
	}
	if strings.Contains(got, "深色霓虹") || strings.Contains(got, "状态切换入口") || strings.Contains(got, "胜利和无可移动") || len([]rune(got)) > 120 {
		t.Fatalf("functional repair should contain only the causal defect: %q", got)
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

func TestAdaptiveProductRepairPromptCarriesVisualQualityFailure(t *testing.T) {
	spec := experiment.ProductSpec{VisualDirection: experiment.VisualDirection{Theme: "深蓝霓虹界面", Palette: []string{"深蓝", "青色", "紫色"}}}
	got := adaptiveProductRepairPrompt(spec, experiment.InteractionPlan{}, []string{"product_visual_quality"}, 1)
	for _, required := range []string{"只调整视觉样式", "将实际产品界面改为深蓝霓虹界面", "深蓝、青色、紫色", "修复后实际操作确认"} {
		if !strings.Contains(got, required) {
			t.Fatalf("visual product repair lost %q: %q", required, got)
		}
	}
	if strings.Count(got, "深蓝霓虹界面") != 1 || len([]rune(got)) > 120 {
		t.Fatalf("visual repair should state the target once and stay compact: %q", got)
	}
}

func TestAdaptiveProductRepairPromptKeepsCausalOrderWithoutUnexecutedFailures(t *testing.T) {
	spec := experiment.ProductSpec{ObservableAcceptance: []experiment.AcceptanceCriterion{
		{ID: "initial_surface", Statement: "初始业务内容完整渲染并包含可操作状态", Required: true},
		{ID: "advanced_state", Statement: "高级状态可切换", Required: true},
	}}
	plan := experiment.InteractionPlan{Steps: []experiment.InteractionStep{
		{StepID: "surface_ready", SemanticIntent: "确认主要交互区域可见"},
		{StepID: "continued_stability", SemanticIntent: "确认连续操作后稳定"},
	}}

	got := adaptiveProductRepairPrompt(spec, plan, []string{"continued_stability", "surface_ready"})
	if !strings.Contains(got, "确认主要交互区域可见") || strings.Contains(got, "确认连续操作后稳定") {
		t.Fatalf("repair did not isolate the first causal failure in plan order: %q", got)
	}
}

func TestAdaptiveProductRepairPromptEscalatesRepeatedFailureToRuntimeVerification(t *testing.T) {
	spec := experiment.ProductSpec{ObservableAcceptance: []experiment.AcceptanceCriterion{
		{ID: "initial_surface", Statement: "初始业务内容完整渲染并包含可操作状态", Required: true},
	}}
	plan := experiment.InteractionPlan{Steps: []experiment.InteractionStep{
		{StepID: "surface_ready", SemanticIntent: "确认主要交互区域可见"},
	}}

	got := adaptiveProductRepairPrompt(spec, plan, []string{"surface_ready"}, 1)
	for _, required := range []string{"只修复这个问题", "确认主要交互区域可见", "修复后实际操作确认"} {
		if !strings.Contains(got, required) {
			t.Fatalf("repeated repair prompt lost %q: %q", required, got)
		}
	}
	for _, internal := range []string{"Harness", "selector", "schema", "expected_outcome", "observed_state"} {
		if strings.Contains(got, internal) {
			t.Fatalf("repeated repair prompt leaked internal term %q: %q", internal, got)
		}
	}
	if len([]rune(got)) > 120 {
		t.Fatalf("repeated repair prompt is too long: %d %q", len([]rune(got)), got)
	}
	if !strings.HasSuffix(got, "修复后实际操作确认。") || strings.Contains(got, ".。") {
		t.Fatalf("repeated repair prompt lost its public suffix or punctuation: %q", got)
	}
}

func TestAdaptiveProductRepairPromptDoesNotTreatUnexecutedCapabilitiesAsDefects(t *testing.T) {
	spec := experiment.ProductSpec{ObservableAcceptance: []experiment.AcceptanceCriterion{{ID: "initial_surface", Statement: "初始业务内容完整渲染并包含可操作状态", Required: true}}}
	plan := experiment.InteractionPlan{Steps: []experiment.InteractionStep{
		{StepID: "surface_ready", SemanticIntent: "确认主要交互区域可见"},
		{StepID: "directional_moves", SemanticIntent: "使用两个不同方向执行实际操作"},
		{StepID: "undo", SemanticIntent: "撤销并恢复上一状态"},
		{StepID: "terminal_scenes", SemanticIntent: "显示成功和结束演示状态", Action: experiment.InteractionAction{Kind: "activate_state_variants"}},
	}}

	got := adaptiveProductRepairPrompt(spec, plan, []string{"surface_ready", "directional_moves", "undo", "terminal_scenes"}, 1)
	if !strings.Contains(got, "确认主要交互区域可见") {
		t.Fatalf("repair prompt omitted its first causal capability: %q", got)
	}
	for _, unexecuted := range []string{"两个不同方向", "撤销并恢复", "状态切换入口", "成功和结束"} {
		if strings.Contains(got, unexecuted) {
			t.Fatalf("repair prompt treated unexecuted capability %q as a proven defect: %q", unexecuted, got)
		}
	}
	if len([]rune(got)) > 120 {
		t.Fatalf("repeated repair prompt exceeded its bounded public request: %d %q", len([]rune(got)), got)
	}
	for _, internal := range []string{"Harness", "selector", "schema", "expected_outcome", "observed_state"} {
		if strings.Contains(got, internal) {
			t.Fatalf("repeated repair prompt leaked internal term %q: %q", internal, got)
		}
	}
}
