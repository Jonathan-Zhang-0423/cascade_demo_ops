package experiment

import (
	"context"
	"errors"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/llm"
)

type productSpecLLM struct {
	calls int
	specs []ProductSpec
}

func (f *productSpecLLM) GenerateJSON(_ context.Context, task config.ModelTask, request llm.JSONRequest, target any) (*llm.CallTrace, error) {
	if task != config.ModelTaskPlanning || strings.Contains(strings.ToLower(request.User), "ffmpeg") {
		return nil, errors.New("unexpected planning request")
	}
	index := f.calls
	f.calls++
	if index >= len(f.specs) {
		return nil, errors.New("no fixture")
	}
	*(target.(*ProductSpec)) = f.specs[index]
	return &llm.CallTrace{Provider: config.ModelProviderKimi, Model: "planner-test"}, nil
}
func (f *productSpecLLM) GenerateText(context.Context, config.ModelTask, llm.TextRequest) (string, *llm.CallTrace, error) {
	return "", nil, nil
}
func (f *productSpecLLM) GenerateMultimodal(context.Context, config.ModelTask, llm.MultimodalRequest, any) (*llm.CallTrace, error) {
	return nil, nil
}

func TestProductSpecPlannerAllowsOneQualityRegeneration(t *testing.T) {
	valid := validProductSpecFixture()
	weak := valid
	weak.ObservableAcceptance = nil
	client := &productSpecLLM{specs: []ProductSpec{weak, valid}}
	planner, err := NewProductSpecPlanner(client)
	if err != nil {
		t.Fatal(err)
	}
	result, err := planner.Generate(t.Context(), "Build a polished responsive interactive product")
	if err != nil {
		t.Fatal(err)
	}
	if client.calls != 2 || result.SpecID != valid.SpecID {
		t.Fatalf("expected exactly one regeneration: calls=%d result=%+v", client.calls, result)
	}
}

func TestProductSpecPlannerDropsInvalidOptionalBuildBrief(t *testing.T) {
	valid := validProductSpecFixture()
	valid.BuildBrief = strings.Repeat("过长的内部摘要", 40)
	client := &productSpecLLM{specs: []ProductSpec{valid}}
	planner, err := NewProductSpecPlanner(client)
	if err != nil {
		t.Fatal(err)
	}
	result, err := planner.Generate(t.Context(), "Build a polished responsive interactive product")
	if err != nil {
		t.Fatal(err)
	}
	if client.calls != 1 || result.BuildBrief != "" {
		t.Fatalf("optional build brief should not consume regeneration: calls=%d brief=%q", client.calls, result.BuildBrief)
	}
}

func TestProductSpecPlannerNormalizesEvidenceChannelShapeWithoutRegeneration(t *testing.T) {
	valid := validProductSpecFixture()
	valid.ObservableAcceptance[0].EvidenceKinds = []string{"visual", "VISUAL", " "}
	valid.ObservableAcceptance[1].EvidenceKinds = []string{"aria"}
	client := &productSpecLLM{specs: []ProductSpec{valid}}
	planner, err := NewProductSpecPlanner(client)
	if err != nil {
		t.Fatal(err)
	}
	result, err := planner.Generate(t.Context(), "Build a polished responsive interactive product")
	if err != nil {
		t.Fatal(err)
	}
	if client.calls != 1 {
		t.Fatalf("mechanical evidence normalization consumed regeneration: calls=%d", client.calls)
	}
	if got := strings.Join(result.ObservableAcceptance[0].EvidenceKinds, ","); got != "visual,dom" {
		t.Fatalf("visual-only criterion normalized to %q", got)
	}
	if got := strings.Join(result.ObservableAcceptance[1].EvidenceKinds, ","); got != "aria,visual" {
		t.Fatalf("structural-only criterion normalized to %q", got)
	}
	if err := ValidateProductSpecQuality(result); err != nil {
		t.Fatalf("normalized ProductSpec did not satisfy contract: %v", err)
	}
}

func TestProductSpecPlannerDemotesUnrequestedPresentationPreferences(t *testing.T) {
	valid := validProductSpecFixture()
	valid.Requirements = append(valid.Requirements, ProductRequirement{ID: "r5", Statement: "Use a deep blue neon palette", Priority: "must"})
	valid.ObservableAcceptance = append(valid.ObservableAcceptance, AcceptanceCriterion{ID: "a5", Statement: "The interface uses a deep blue neon gradient", EvidenceKinds: []string{"visual", "dom"}, Required: true})
	client := &productSpecLLM{specs: []ProductSpec{valid}}
	planner, err := NewProductSpecPlanner(client)
	if err != nil {
		t.Fatal(err)
	}
	result, err := planner.Generate(t.Context(), "Build a polished responsive interactive number game")
	if err != nil {
		t.Fatal(err)
	}
	if result.Requirements[len(result.Requirements)-1].Priority != "should" || result.ObservableAcceptance[len(result.ObservableAcceptance)-1].Required {
		t.Fatalf("model-inferred styling became a blocker: requirement=%+v criterion=%+v", result.Requirements[len(result.Requirements)-1], result.ObservableAcceptance[len(result.ObservableAcceptance)-1])
	}
	if result.VisualDirection.Theme != valid.VisualDirection.Theme {
		t.Fatalf("advisory visual context should be retained: %+v", result.VisualDirection)
	}
}

func TestProductSpecPlannerKeepsExplicitPresentationRequirement(t *testing.T) {
	valid := validProductSpecFixture()
	valid.Requirements = append(valid.Requirements, ProductRequirement{ID: "r5", Statement: "Use a deep blue neon palette", Priority: "must"})
	valid.ObservableAcceptance = append(valid.ObservableAcceptance, AcceptanceCriterion{ID: "a5", Statement: "The interface uses a deep blue neon gradient", EvidenceKinds: []string{"visual", "dom"}, Required: true})
	result := normalizeInferredPresentationPreferences(valid, "Build a responsive number game with a deep blue neon style")
	if result.Requirements[len(result.Requirements)-1].Priority != "must" || !result.ObservableAcceptance[len(result.ObservableAcceptance)-1].Required {
		t.Fatalf("explicit styling requirement was demoted: requirement=%+v criterion=%+v", result.Requirements[len(result.Requirements)-1], result.ObservableAcceptance[len(result.ObservableAcceptance)-1])
	}
}

func TestPresentationPreferenceDetectionDoesNotMatchRequiredAsRed(t *testing.T) {
	if looksLikeSpecificPresentationPreference("This required interaction must remain responsive and readable.") {
		t.Fatal("ordinary requirement text was mistaken for the color red")
	}
}

func TestProductSpecQualityRejectsHarnessLeakAndWeakEvidence(t *testing.T) {
	for name, mutate := range map[string]func(*ProductSpec){
		"harness leak": func(spec *ProductSpec) { spec.Objective = "prepare DemoOps recording" },
		"one channel":  func(spec *ProductSpec) { spec.ObservableAcceptance[0].EvidenceKinds = []string{"visual"} },
	} {
		t.Run(name, func(t *testing.T) {
			spec := validProductSpecFixture()
			mutate(&spec)
			if err := ValidateProductSpecQuality(spec); err == nil {
				t.Fatal("expected quality failure")
			}
		})
	}
}

func TestTaskPackSelectionIgnoresHostRouteAndCopy(t *testing.T) {
	base := WorkflowSelectionSignals{UserGoal: "Build a responsive interactive experience", InteractionStructure: "asynchronous_build", InteractionSurface: "canvas", ResultType: "interactive_product"}
	first, err := SelectTaskPack(base)
	if err != nil {
		t.Fatal(err)
	}
	base.UserGoal = "Create a completely different branded tool"
	base.InteractionSurface = "iframe"
	second, err := SelectTaskPack(base)
	if err != nil {
		t.Fatal(err)
	}
	if first != WorkflowTemplateAsyncProductDemo || second != first {
		t.Fatalf("semantic task pack selection drifted: %q %q", first, second)
	}
}

func TestCompileBuildPromptKeepsOriginalSentenceAndAcceptanceInternal(t *testing.T) {
	spec := validProductSpecFixture()
	spec.BuildBrief = "Core state, persistence, undo, keyboard and touch input in a polished responsive interface"
	spec.ResponsiveRequirements = []string{"Keep the primary surface usable on a narrow viewport", "A second internal verification detail"}
	userGoal := "Create a polished responsive experience in one sentence."
	prompt, err := CompileBuildPrompt(spec, userGoal, BuildDeliveryPortableSingleHTML)
	if err != nil {
		t.Fatal(err)
	}
	if prompt != userGoal || len([]rune(prompt)) > 240 || strings.Contains(prompt, "\n") {
		t.Fatalf("builder did not receive the original one-sentence intent: %s", prompt)
	}
	for _, internalDetail := range []string{spec.BuildBrief, spec.Audience, spec.ObservableAcceptance[0].Statement, spec.ForbiddenOutcomes[0], spec.ResponsiveRequirements[1], "单个HTML入口"} {
		if strings.Contains(prompt, internalDetail) {
			t.Fatalf("internal planning or acceptance detail leaked into target brief %q: %s", internalDetail, prompt)
		}
	}
}

func TestCompileBuildPromptPreservesSentenceBytes(t *testing.T) {
	spec := validProductSpecFixture()
	withTerminator := "构建一款适合产品演示的精致响应式网页游戏。"
	if prompt, err := CompileBuildPrompt(spec, withTerminator, BuildDeliveryPortableSingleHTML); err != nil || prompt != withTerminator {
		t.Fatalf("existing sentence terminator drifted: prompt=%q err=%v", prompt, err)
	}
	withoutTerminator := strings.TrimSuffix(withTerminator, "。")
	if prompt, err := CompileBuildPrompt(spec, withoutTerminator, BuildDeliveryPortableSingleHTML); err != nil || prompt != withoutTerminator {
		t.Fatalf("one-sentence goal bytes drifted: prompt=%q err=%v", prompt, err)
	}
}

func validProductSpecFixture() ProductSpec {
	return ProductSpec{
		SchemaVersion: ProductSpecSchemaVersion, SpecID: "spec-test", Title: "Interactive product", Objective: "Create a polished responsive experience", Audience: "Product evaluators",
		Requirements:            []ProductRequirement{{ID: "r1", Statement: "Core state", Priority: "must"}, {ID: "r2", Statement: "Persistence", Priority: "must"}, {ID: "r3", Statement: "Boundary state", Priority: "must"}, {ID: "r4", Statement: "Reset state", Priority: "should"}},
		VisualDirection:         VisualDirection{Theme: "dark", Palette: []string{"navy", "cyan"}, Motion: "smooth state transitions"},
		InteractionRequirements: []ProductRequirement{{ID: "i1", Statement: "Keyboard input", Priority: "must"}, {ID: "i2", Statement: "Touch input", Priority: "must"}},
		ObservableAcceptance:    []AcceptanceCriterion{{ID: "a1", Statement: "Primary state visible", EvidenceKinds: []string{"visual", "dom"}, Required: true}, {ID: "a2", Statement: "Input changes state", EvidenceKinds: []string{"region_change", "aria"}, Required: true}, {ID: "a3", Statement: "Responsive state visible", EvidenceKinds: []string{"frame", "route"}, Required: true}, {ID: "a4", Statement: "Reset is observable", EvidenceKinds: []string{"visual", "aria"}, Required: true}},
		ForbiddenOutcomes:       []string{"Do not show an empty result"},
	}
}
