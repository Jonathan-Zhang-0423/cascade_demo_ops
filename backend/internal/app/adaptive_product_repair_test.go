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

func TestAdaptiveProductRepairPromptEscalatesMultipleFailedCapabilitiesImmediately(t *testing.T) {
	spec := experiment.ProductSpec{VisualDirection: experiment.VisualDirection{Theme: "深色霓虹界面"}, ObservableAcceptance: []experiment.AcceptanceCriterion{
		{ID: "surface", Statement: "完整产品表面可见", Required: true},
		{ID: "terminal", Statement: "终局状态可用", Required: true},
	}}
	plan := experiment.InteractionPlan{Steps: []experiment.InteractionStep{
		{StepID: "directional_moves", SemanticIntent: "使用两个不同方向键执行实际操作"},
		{StepID: "terminal_scenes", SemanticIntent: "观察胜利和无可移动演示状态"},
	}}

	got := adaptiveProductRepairPrompt(spec, plan, []string{"directional_moves", "terminal_scenes"})
	for _, required := range []string{"请修复当前项目", "使用两个不同方向键", "界面改为深色霓虹界面"} {
		if !strings.Contains(got, required) {
			t.Fatalf("multi-capability repair prompt lost %q: %q", required, got)
		}
	}
	if strings.Contains(got, "胜利和无可移动") || len([]rune(got)) > 240 {
		t.Fatalf("first repair should stay on the first causal defect: %q", got)
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
	for _, required := range []string{"实际产品界面的视觉风格应符合深蓝霓虹界面", "界面改为深蓝霓虹界面", "深蓝、青色、紫色", "预览中运行确认"} {
		if !strings.Contains(got, required) {
			t.Fatalf("visual product repair lost %q: %q", required, got)
		}
	}
}

func TestAdaptiveProductRepairPromptKeepsCausalOrderWhileCoveringRelatedFailures(t *testing.T) {
	spec := experiment.ProductSpec{ObservableAcceptance: []experiment.AcceptanceCriterion{
		{ID: "initial_surface", Statement: "初始业务内容完整渲染并包含可操作状态", Required: true},
		{ID: "advanced_state", Statement: "高级状态可切换", Required: true},
	}}
	plan := experiment.InteractionPlan{Steps: []experiment.InteractionStep{
		{StepID: "surface_ready", SemanticIntent: "确认主要交互区域可见"},
		{StepID: "continued_stability", SemanticIntent: "确认连续操作后稳定"},
	}}

	got := adaptiveProductRepairPrompt(spec, plan, []string{"continued_stability", "surface_ready"})
	if !strings.Contains(got, "初始业务内容完整渲染并包含可操作状态") || strings.Contains(got, "连续操作后稳定") {
		t.Fatalf("first repair did not stay on the first causal public failure: %q", got)
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
	for _, required := range []string{"上轮修复后实际预览仍失败", "检查已加载代码", "预览中运行确认", "初始业务内容完整渲染"} {
		if !strings.Contains(got, required) {
			t.Fatalf("repeated repair prompt lost %q: %q", required, got)
		}
	}
	for _, internal := range []string{"Harness", "selector", "schema", "expected_outcome", "observed_state"} {
		if strings.Contains(got, internal) {
			t.Fatalf("repeated repair prompt leaked internal term %q: %q", internal, got)
		}
	}
	if len([]rune(got)) > 240 {
		t.Fatalf("repeated repair prompt is too long: %d %q", len([]rune(got)), got)
	}
	if !strings.HasSuffix(got, "保留已有内容。") || strings.Contains(got, ".。") {
		t.Fatalf("repeated repair prompt lost its public suffix or punctuation: %q", got)
	}
}

func TestAdaptiveProductRepairPromptEscalatesAllRemainingPublicCapabilities(t *testing.T) {
	spec := experiment.ProductSpec{ObservableAcceptance: []experiment.AcceptanceCriterion{{ID: "initial_surface", Statement: "初始业务内容完整渲染并包含可操作状态", Required: true}}}
	plan := experiment.InteractionPlan{Steps: []experiment.InteractionStep{
		{StepID: "surface_ready", SemanticIntent: "确认主要交互区域可见"},
		{StepID: "directional_moves", SemanticIntent: "使用两个不同方向执行实际操作"},
		{StepID: "undo", SemanticIntent: "撤销并恢复上一状态"},
		{StepID: "terminal_scenes", SemanticIntent: "显示成功和结束演示状态", Action: experiment.InteractionAction{Kind: "activate_state_variants"}},
	}}

	got := adaptiveProductRepairPrompt(spec, plan, []string{"surface_ready", "directional_moves", "undo", "terminal_scenes"}, 1)
	for _, publicCapability := range []string{"初始业务内容完整渲染", "两个不同方向", "撤销并恢复", "提供可见且可操作的状态切换入口", "成功和结束"} {
		if !strings.Contains(got, publicCapability) {
			t.Fatalf("repeated repair prompt omitted public capability %q: %q", publicCapability, got)
		}
	}
	if len([]rune(got)) > 240 {
		t.Fatalf("repeated repair prompt exceeded its bounded public request: %d %q", len([]rune(got)), got)
	}
	for _, internal := range []string{"Harness", "selector", "schema", "expected_outcome", "observed_state"} {
		if strings.Contains(got, internal) {
			t.Fatalf("repeated repair prompt leaked internal term %q: %q", internal, got)
		}
	}
}
