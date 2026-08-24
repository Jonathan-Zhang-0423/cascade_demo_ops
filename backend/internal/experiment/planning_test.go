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

func TestCompileBuildPromptKeepsTargetBriefConciseAndAcceptanceInternal(t *testing.T) {
	spec := validProductSpecFixture()
	spec.ResponsiveRequirements = []string{"Keep the primary surface usable on a narrow viewport", "A second internal verification detail"}
	userGoal := "Create a polished responsive experience in one sentence."
	prompt, err := CompileBuildPrompt(spec, userGoal)
	if err != nil {
		t.Fatal(err)
	}
	if prompt != userGoal || len([]rune(prompt)) > 240 || strings.Contains(prompt, "\n") {
		t.Fatalf("compiled target brief is not a single concise user sentence: %s", prompt)
	}
	for _, internalDetail := range []string{spec.Audience, spec.Requirements[0].Statement, spec.InteractionRequirements[0].Statement, spec.VisualDirection.Theme, spec.ObservableAcceptance[0].Statement, spec.ForbiddenOutcomes[0], spec.ResponsiveRequirements[1]} {
		if strings.Contains(prompt, internalDetail) {
			t.Fatalf("internal planning or acceptance detail leaked into target brief %q: %s", internalDetail, prompt)
		}
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
