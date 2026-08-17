package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/finalfilm"
	"cascade-demoops/backend/internal/llm"
	"cascade-demoops/backend/internal/model"
)

type fakeFinalFilmDirectorLLM struct {
	shots []finalFilmDirectorDraftShot
}

func (f fakeFinalFilmDirectorLLM) GenerateJSON(_ context.Context, _ config.ModelTask, _ llm.JSONRequest, target any) (*llm.CallTrace, error) {
	draft := target.(*finalFilmDirectorDraft)
	draft.Shots = append([]finalFilmDirectorDraftShot{}, f.shots...)
	return &llm.CallTrace{Provider: config.ModelProviderMinimax, Model: "director-test", Task: config.ModelTaskPlanning, AdapterVersion: "test"}, nil
}

func (fakeFinalFilmDirectorLLM) GenerateText(context.Context, config.ModelTask, llm.TextRequest) (string, *llm.CallTrace, error) {
	return "", nil, nil
}

func (fakeFinalFilmDirectorLLM) GenerateMultimodal(context.Context, config.ModelTask, llm.MultimodalRequest, any) (*llm.CallTrace, error) {
	return nil, nil
}

func TestFinalFilmDirectorPlannerCompilesEnumRecipeIntoServerOwnedSafePrompt(t *testing.T) {
	catalog, baseline := finalFilmHTTPFixture("director_test")
	intent, err := model.PresentationGenerationIntentDefaults("intro_director", "intro", nil)
	if err != nil {
		t.Fatal(err)
	}
	constraints, err := finalfilm.CompileStoryboardConstraints(finalfilm.ConstraintCompileInput{
		SourcePackageID: "package_director", Catalog: catalog, BaselinePlan: baseline,
		Intents: []model.PresentationGenerationIntent{intent}, Canvas: model.RenderCanvas{Width: 1920, Height: 1080, FPS: 30, Format: "mp4"}, Now: time.Unix(100, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	planner := &finalFilmDirectorPlanner{
		client: fakeFinalFilmDirectorLLM{
			shots: []finalFilmDirectorDraftShot{{
				IntentID: intent.IntentID, VisualStyle: "abstract_geometric", Motion: "slow_drift", Palette: "cool_neutral",
			}},
		},
	}
	plan, err := planner.PlanGeneratedShots(context.Background(), finalfilm.DirectorPlanRequest{
		JobID: "job_director", Constraints: constraints, Intents: []model.PresentationGenerationIntent{intent}, Catalog: catalog, Baseline: baseline,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Specs) != 1 || plan.Specs[0].PromptSHA256 != model.FinalFilmPromptSHA256(plan.Specs[0].Prompt) {
		t.Fatalf("unsafe or unbound Director plan: %+v", plan)
	}
	prompt := strings.ToLower(plan.Specs[0].Prompt)
	for _, required := range []string{"no product ui recreation", "no business workflow", "no numbers", "no logos", "no readable text"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("server-owned prompt omitted %q: %s", required, prompt)
		}
	}
}

func TestFinalFilmDirectorPlannerRejectsOutOfVocabularyRecipe(t *testing.T) {
	catalog, baseline := finalFilmHTTPFixture("director_reject")
	intent, _ := model.PresentationGenerationIntentDefaults("intro_director", "intro", nil)
	constraints, _ := finalfilm.CompileStoryboardConstraints(finalfilm.ConstraintCompileInput{
		SourcePackageID: "package_director", Catalog: catalog, BaselinePlan: baseline,
		Intents: []model.PresentationGenerationIntent{intent}, Canvas: model.RenderCanvas{Width: 1920, Height: 1080, FPS: 30, Format: "mp4"}, Now: time.Unix(100, 0),
	})
	planner := &finalFilmDirectorPlanner{
		client: fakeFinalFilmDirectorLLM{
			shots: []finalFilmDirectorDraftShot{{
				IntentID: intent.IntentID, VisualStyle: "recreate_product_ui", Motion: "slow_drift", Palette: "cool_neutral",
			}},
		},
	}
	if _, err := planner.PlanGeneratedShots(context.Background(), finalfilm.DirectorPlanRequest{JobID: "job", Constraints: constraints, Intents: []model.PresentationGenerationIntent{intent}, Catalog: catalog, Baseline: baseline}); err == nil {
		t.Fatal("expected out-of-vocabulary Director recipe to be rejected")
	}
}
