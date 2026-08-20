package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/finalfilm"
	"cascade-demoops/backend/internal/llm"
	"cascade-demoops/backend/internal/model"
)

type finalFilmDirectorPlanner struct{ client llm.Client }

type finalFilmDirectorDraft struct {
	Shots []finalFilmDirectorDraftShot `json:"shots"`
}

type finalFilmDirectorDraftShot struct {
	IntentID        string `json:"intent_id"`
	VisualStyle     string `json:"visual_style"`
	Motion          string `json:"motion"`
	Palette         string `json:"palette"`
	VisualIntent    string `json:"visual_intent,omitempty"`
	TransitionLogic string `json:"transition_logic,omitempty"`
}

func newFinalFilmDirectorPlanner(client llm.Client) finalfilm.DirectorPlanner {
	return &finalFilmDirectorPlanner{client: client}
}

func (p *finalFilmDirectorPlanner) PlanGeneratedShots(ctx context.Context, request finalfilm.DirectorPlanRequest) (model.FinalFilmDirectorPlan, error) {
	if p == nil || p.client == nil {
		return model.FinalFilmDirectorPlan{}, errors.New("Director planning model is unavailable")
	}
	type safeIntent struct {
		IntentID string   `json:"intent_id"`
		Purpose  string   `json:"purpose"`
		Duration int      `json:"duration_sec"`
		Ratio    string   `json:"aspect_ratio"`
		Refs     []string `json:"reference_artifact_ids,omitempty"`
	}
	input := struct {
		Objective string                        `json:"objective"`
		Intents   []safeIntent                  `json:"presentation_intents"`
		Evidence  *model.DirectorEvidenceDigest `json:"evidence,omitempty"`
		Story     *model.DirectorStoryPlan      `json:"story_plan,omitempty"`
	}{Objective: request.Baseline.Objective, Evidence: request.EvidenceDigest, Story: request.StoryPlan}
	for _, intent := range request.Intents {
		input.Intents = append(input.Intents, safeIntent{
			IntentID: intent.IntentID, Purpose: intent.Purpose, Duration: intent.RequestedSlot.PreferredDurationSec,
			Ratio: intent.RequestedSlot.AspectRatio, Refs: append([]string{}, intent.ReferenceAssetRefs...),
		})
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return model.FinalFilmDirectorPlan{}, err
	}
	var draft finalFilmDirectorDraft
	systemPrompt := "You are the presentation-shot planner for a factual product demo. Return exactly one enum-only visual recipe per supplied intent. visual_style must be one of abstract_geometric, cinematic_particles, fluid_gradient, brand_atmosphere. motion must be one of slow_drift, gentle_orbit, soft_reveal, calm_flow. palette must be one of cool_neutral, warm_neutral, monochrome, restrained_brand. Never return free-form prompts or provider/model/API parameters. Preserve every intent_id exactly."
	responseHint := `{"shots":[{"intent_id":"exact supplied id","visual_style":"abstract_geometric","motion":"slow_drift","palette":"cool_neutral"}]}`
	if request.AutomationProfile == model.FinalFilmAutomationProfileGuidedDemoV1 {
		systemPrompt = "You are a provider-neutral director for a factual product demo. For each supplied presentation slot, describe a concise decorative visual_intent and transition_logic derived from the evidence tone and adjacent chapter meaning. Do not recreate UI, assert business facts, invent readable text, include provider names, or omit an intent_id."
		responseHint = `{"shots":[{"intent_id":"exact supplied id","visual_intent":"restrained cinematic opening derived from observed palette","transition_logic":"connect verified setup to the next chapter without depicting UI"}]}`
	}
	trace, err := p.client.GenerateJSON(ctx, config.ModelTaskPlanning, llm.JSONRequest{
		System:     systemPrompt,
		User:       "Choose a safe presentation-only visual recipe for this locked input:\n" + string(payload),
		SchemaName: "demoops.final_film_director_draft.v1", MaxTokens: 3000, Temperature: 0.2,
		ResponseHint: responseHint,
	}, &draft)
	if err != nil {
		return model.FinalFilmDirectorPlan{}, fmt.Errorf("Director generated-shot planning failed: %w", err)
	}
	draftByIntent := map[string]finalFilmDirectorDraftShot{}
	for _, shot := range draft.Shots {
		intentID := strings.TrimSpace(shot.IntentID)
		valid := validFinalFilmDirectorRecipe(shot)
		if request.AutomationProfile == model.FinalFilmAutomationProfileGuidedDemoV1 {
			valid = validAutomatedDirectorIntent(shot)
		}
		if intentID == "" || draftByIntent[intentID].IntentID != "" || !valid {
			return model.FinalFilmDirectorPlan{}, errors.New("Director returned blank or duplicate generated-shot specs")
		}
		draftByIntent[intentID] = shot
	}
	if len(draftByIntent) != len(request.Intents) {
		return model.FinalFilmDirectorPlan{}, errors.New("Director must return exactly one generated-shot spec per presentation intent")
	}
	now := time.Now().UTC()
	traceLabel := "planning"
	if trace != nil && trace.Label() != "" {
		traceLabel = trace.Label()
	}
	planSeed := request.JobID + "\x00" + request.Constraints.ConstraintSetID + "\x00" + traceLabel + "\x00" + now.Format(time.RFC3339Nano)
	planDigest := sha256.Sum256([]byte(planSeed))
	plan := model.FinalFilmDirectorPlan{
		SchemaVersion: model.FinalFilmDirectorPlanSchemaVersion, PlanID: "director_plan_" + hex.EncodeToString(planDigest[:10]),
		JobID: request.JobID, ConstraintSetID: request.Constraints.ConstraintSetID,
		DirectorRunID: "director_run_" + hex.EncodeToString(planDigest[10:20]), GeneratedAt: now,
	}
	for index, intent := range request.Intents {
		recipe, ok := draftByIntent[intent.IntentID]
		if !ok {
			return model.FinalFilmDirectorPlan{}, fmt.Errorf("Director omitted presentation intent %s", intent.IntentID)
		}
		prompt := compileFinalFilmPrompt(intent.Purpose, recipe)
		if request.AutomationProfile == model.FinalFilmAutomationProfileGuidedDemoV1 {
			prompt = compileAutomatedFinalFilmPrompt(intent.Purpose, recipe, request.EvidenceDigest)
		}
		plan.Specs = append(plan.Specs, model.FinalFilmGeneratedSpec{
			SpecID: fmt.Sprintf("generated_spec_%02d_%s", index+1, intent.IntentID), IntentID: intent.IntentID,
			Purpose: intent.Purpose, Prompt: prompt, PromptSHA256: model.FinalFilmPromptSHA256(prompt),
			DurationSec: intent.RequestedSlot.PreferredDurationSec, AspectRatio: intent.RequestedSlot.AspectRatio,
			ReferenceArtifactIDs: append([]string{}, intent.ReferenceAssetRefs...),
			ContentPolicy: model.FinalFilmGeneratedSpecContentPolicy{
				PresentationOnly: true, NoCapturedUIRecreation: true, NoBusinessFactClaims: true,
				NoUnverifiedText: true, RequiresExplicitReview: true,
			},
			FailurePolicy: model.PresentationGenerationFailureContinue,
		})
	}
	if err := model.ValidateFinalFilmDirectorPlan(plan, request.JobID, request.Constraints, request.Intents); err != nil {
		return model.FinalFilmDirectorPlan{}, err
	}
	return plan, nil
}

func validFinalFilmDirectorRecipe(shot finalFilmDirectorDraftShot) bool {
	styles := map[string]bool{"abstract_geometric": true, "cinematic_particles": true, "fluid_gradient": true, "brand_atmosphere": true}
	motions := map[string]bool{"slow_drift": true, "gentle_orbit": true, "soft_reveal": true, "calm_flow": true}
	palettes := map[string]bool{"cool_neutral": true, "warm_neutral": true, "monochrome": true, "restrained_brand": true}
	return styles[shot.VisualStyle] && motions[shot.Motion] && palettes[shot.Palette]
}

func compileFinalFilmPrompt(purpose string, recipe finalFilmDirectorDraftShot) string {
	return fmt.Sprintf("Create a %s presentation shot using %s visuals, %s motion, and a %s palette. Decorative cinematic imagery only, clean negative space, consistent lighting, no product UI recreation, no people operating software, no business workflow or factual claims, no numbers, no logos, and no readable text.", purpose, recipe.VisualStyle, recipe.Motion, recipe.Palette)
}

func validAutomatedDirectorIntent(shot finalFilmDirectorDraftShot) bool {
	return len(strings.TrimSpace(shot.VisualIntent)) >= 8 && len(shot.VisualIntent) <= 500 && len(strings.TrimSpace(shot.TransitionLogic)) >= 8 && len(shot.TransitionLogic) <= 500
}

func compileAutomatedFinalFilmPrompt(purpose string, recipe finalFilmDirectorDraftShot, evidence *model.DirectorEvidenceDigest) string {
	palette := "the restrained colors observed in the factual recording"
	if evidence != nil && len(evidence.VisualStyle.DominantColors) > 0 {
		palette = strings.Join(evidence.VisualStyle.DominantColors, ", ")
	}
	return fmt.Sprintf("Create a 4-second %s presentation shot. Visual intent: %s. Transition logic: %s. Use %s with continuous cinematic motion and no static title card. Decorative chapter packaging only: no product UI recreation, no business workflow or factual claims, no numbers, no logos, and no readable text.", purpose, strings.TrimSpace(recipe.VisualIntent), strings.TrimSpace(recipe.TransitionLogic), palette)
}
