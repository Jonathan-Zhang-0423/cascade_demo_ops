package app

import (
	"context"
	"encoding/json"
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

func TestAutomatedDirectorSafeEvidenceOmitsExecutionIdentifiers(t *testing.T) {
	digest := model.DirectorEvidenceDigest{SourceDurationMS: 120_000, WaitRanges: []model.MillisecondRange{{20_000, 80_000}}, VisualStyle: model.DirectorVisualStyleEvidence{DominantColors: []string{"#112244"}}, Audio: model.DirectorAudioEvidence{SourceAudioPresent: true}}
	for index, chapter := range model.RequiredDemoChapters() {
		factID := "public_" + chapter
		fact := model.PublicNarrativeFact{SchemaVersion: model.PublicNarrativeFactSchemaVersion, FactID: factID, Chapter: chapter, ApprovedCaptionVariants: []string{"公开字幕"}, VisibleEvidenceRefs: []string{"artifact_" + chapter}, SourceKind: "visible_ui"}
		digest.PublicFacts = append(digest.PublicFacts, fact)
		digest.RequiredSteps = append(digest.RequiredSteps, model.DirectorEvidenceStep{StepID: "internal_node_" + chapter, Order: index + 1, Chapter: chapter, PublicNarrativeFactID: factID, SourceRangeMS: model.MillisecondRange{index * 10_000, (index + 1) * 10_000}, ArtifactIDs: []string{"artifact_" + chapter}})
	}
	safe, err := compileFinalFilmDirectorSafeEvidence(&digest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(safe)
	text := string(raw)
	for _, forbidden := range []string{"internal_node_", "step_id", "observed_state", "expected_outcome", "selector"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("Director safe evidence leaked %q: %s", forbidden, text)
		}
	}
}

func TestAutomatedDirectorStoryPreservesPublicOrderAndOnlyCompressesWait(t *testing.T) {
	digest := &model.DirectorEvidenceDigest{DigestID: "digest", SourceDurationMS: 120_000}
	draft := &finalFilmDirectorStoryDraft{TargetDurationMS: 105_000, BackgroundMode: "light_electronic_music_with_source_interaction_audio", DecisionLog: []string{"preserve causal order"}}
	for index, chapter := range model.RequiredDemoChapters() {
		factID := "fact_" + chapter
		digest.PublicFacts = append(digest.PublicFacts, model.PublicNarrativeFact{SchemaVersion: model.PublicNarrativeFactSchemaVersion, FactID: factID, Chapter: chapter, ApprovedCaptionVariants: []string{"公开字幕"}, VisibleEvidenceRefs: []string{"artifact_" + chapter}, SourceKind: "visible_ui"})
		digest.RequiredSteps = append(digest.RequiredSteps, model.DirectorEvidenceStep{StepID: "server_step_" + chapter, Order: index + 1, Chapter: chapter, PublicNarrativeFactID: factID, SourceRangeMS: model.MillisecondRange{index * 12_000, (index + 1) * 12_000}, ArtifactIDs: []string{"artifact_" + chapter}})
		speed := float64(1)
		if chapter == "build_wait" {
			speed = 8
		}
		draft.Facts = append(draft.Facts, struct {
			PublicFactID string  `json:"public_fact_id"`
			Speed        float64 `json:"speed"`
			Reason       string  `json:"reason"`
		}{factID, speed, "preserve visible evidence"})
	}
	purposes := []string{"intro", "section_divider", "outro"}
	intents := []model.PresentationGenerationIntent{}
	for _, purpose := range purposes {
		intent, _ := model.PresentationGenerationIntentDefaults("intent_"+purpose, purpose, nil)
		intents = append(intents, intent)
	}
	draft.Generated = append(draft.Generated,
		struct {
			IntentID                string `json:"intent_id"`
			Placement               string `json:"placement"`
			AnchorAfterPublicFactID string `json:"anchor_after_public_fact_id,omitempty"`
			Reason                  string `json:"reason"`
		}{intents[0].IntentID, "before_first_fact", "", "opening"},
		struct {
			IntentID                string `json:"intent_id"`
			Placement               string `json:"placement"`
			AnchorAfterPublicFactID string `json:"anchor_after_public_fact_id,omitempty"`
			Reason                  string `json:"reason"`
		}{intents[1].IntentID, "after_public_fact", "fact_build_wait", "chapter bridge"},
		struct {
			IntentID                string `json:"intent_id"`
			Placement               string `json:"placement"`
			AnchorAfterPublicFactID string `json:"anchor_after_public_fact_id,omitempty"`
			Reason                  string `json:"reason"`
		}{intents[2].IntentID, "after_last_fact", "", "closing"},
	)
	fallback := &model.DirectorStoryPlan{SkillVersions: map[string]string{"a": "1", "b": "1", "c": "1", "d": "1", "e": "1"}}
	story, err := compileAutomatedDirectorStory(draft, finalfilm.DirectorPlanRequest{Intents: intents, EvidenceDigest: digest, StoryPlan: fallback})
	if err != nil {
		t.Fatal(err)
	}
	for _, segment := range story.Timeline {
		if segment.Kind == "fact" && segment.SourceStepID != "server_step_build_wait" && segment.Speed != 1 {
			t.Fatalf("non-wait fact was altered: %+v", segment)
		}
	}
}
