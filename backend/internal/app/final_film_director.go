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
	Story *finalFilmDirectorStoryDraft `json:"story,omitempty"`
}

type finalFilmDirectorStoryDraft struct {
	TargetDurationMS int `json:"target_duration_ms"`
	Facts            []struct {
		PublicFactID string  `json:"public_fact_id"`
		Speed        float64 `json:"speed"`
		Reason       string  `json:"reason"`
	} `json:"facts"`
	Generated []struct {
		IntentID                string `json:"intent_id"`
		Placement               string `json:"placement"`
		AnchorAfterPublicFactID string `json:"anchor_after_public_fact_id,omitempty"`
		Reason                  string `json:"reason"`
	} `json:"generated"`
	BackgroundMode string   `json:"background_mode"`
	DecisionLog    []string `json:"decision_log"`
}

type finalFilmDirectorSafeEvidence struct {
	SourceDurationMS   int                      `json:"source_duration_ms"`
	WaitRanges         []model.MillisecondRange `json:"wait_ranges,omitempty"`
	DominantColors     []string                 `json:"dominant_colors,omitempty"`
	SourceAudioPresent bool                     `json:"source_audio_present"`
	Facts              []struct {
		PublicFactID            string                 `json:"public_fact_id"`
		Chapter                 string                 `json:"chapter"`
		ApprovedCaptionVariants []string               `json:"approved_caption_variants"`
		VisibleEvidenceRefs     []string               `json:"visible_evidence_refs"`
		SourceRangeMS           model.MillisecondRange `json:"source_range_ms"`
	} `json:"facts"`
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
		Objective string                         `json:"objective,omitempty"`
		Intents   []safeIntent                   `json:"presentation_intents"`
		Evidence  *finalFilmDirectorSafeEvidence `json:"public_evidence,omitempty"`
	}{}
	if request.AutomationProfile == "" {
		input.Objective = request.Baseline.Objective
	}
	if request.AutomationProfile == model.FinalFilmAutomationProfileGuidedDemoV1 {
		safeEvidence, safeErr := compileFinalFilmDirectorSafeEvidence(request.EvidenceDigest)
		if safeErr != nil {
			return model.FinalFilmDirectorPlan{}, safeErr
		}
		input.Evidence = &safeEvidence
	}
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
		systemPrompt = "You are the Director for a factual product demo. Decide the factual pacing and the placement of all decorative shots, and also describe one provider-neutral visual intent per supplied slot. Use only public_fact_id and intent_id from the input. Preserve the supplied factual order. Login, creation, prompt input, submission, result reveal and interaction stay at 1x; only build-wait footage may use 4x-12x. Target 100-110 seconds when source coverage permits and never slow factual UI. Intro must be first, outro last, and the single divider must bridge build waiting to result reveal. Captions are not authored by you and will be copied verbatim from approved variants. Decorative shots must contain no readable text, product UI, logos, numbers or business claims. Do not return provider names, selectors, HTML, schema keys from other systems, source paths, step IDs, or node IDs."
		responseHint = `{"shots":[{"intent_id":"exact supplied id","visual_intent":"stable abstract light and color with no text","transition_logic":"bridge the adjacent public chapters without depicting UI"}],"story":{"target_duration_ms":105000,"facts":[{"public_fact_id":"exact supplied public fact id","speed":1,"reason":"preserve visible action"}],"generated":[{"intent_id":"exact supplied id","placement":"before_first_fact","reason":"opening"}],"background_mode":"light_electronic_music_with_source_interaction_audio","decision_log":["concise material choice"]}}`
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
	if request.AutomationProfile == model.FinalFilmAutomationProfileGuidedDemoV1 {
		story, storyErr := compileAutomatedDirectorStory(draft.Story, request)
		if storyErr != nil {
			return model.FinalFilmDirectorPlan{}, storyErr
		}
		plan.StoryPlan = &story
		plan.AutomationProfile = request.AutomationProfile
		plan.EvidenceDigestID = request.EvidenceDigest.DigestID
	}
	if err := model.ValidateFinalFilmDirectorPlan(plan, request.JobID, request.Constraints, request.Intents); err != nil {
		return model.FinalFilmDirectorPlan{}, err
	}
	return plan, nil
}

func compileFinalFilmDirectorSafeEvidence(digest *model.DirectorEvidenceDigest) (finalFilmDirectorSafeEvidence, error) {
	if digest == nil {
		return finalFilmDirectorSafeEvidence{}, errors.New("automated Director requires a public evidence digest")
	}
	result := finalFilmDirectorSafeEvidence{SourceDurationMS: digest.SourceDurationMS, WaitRanges: append([]model.MillisecondRange{}, digest.WaitRanges...), DominantColors: append([]string{}, digest.VisualStyle.DominantColors...), SourceAudioPresent: digest.Audio.SourceAudioPresent}
	facts := map[string]model.PublicNarrativeFact{}
	for _, fact := range digest.PublicFacts {
		if err := model.ValidatePublicNarrativeFact(fact); err != nil {
			return result, err
		}
		facts[fact.FactID] = fact
	}
	for _, step := range digest.RequiredSteps {
		fact, ok := facts[step.PublicNarrativeFactID]
		if !ok {
			return result, fmt.Errorf("public fact %s is missing", step.PublicNarrativeFactID)
		}
		entry := struct {
			PublicFactID            string                 `json:"public_fact_id"`
			Chapter                 string                 `json:"chapter"`
			ApprovedCaptionVariants []string               `json:"approved_caption_variants"`
			VisibleEvidenceRefs     []string               `json:"visible_evidence_refs"`
			SourceRangeMS           model.MillisecondRange `json:"source_range_ms"`
		}{PublicFactID: fact.FactID, Chapter: fact.Chapter, ApprovedCaptionVariants: append([]string{}, fact.ApprovedCaptionVariants...), VisibleEvidenceRefs: append([]string{}, fact.VisibleEvidenceRefs...), SourceRangeMS: step.SourceRangeMS}
		result.Facts = append(result.Facts, entry)
	}
	return result, nil
}

func compileAutomatedDirectorStory(draft *finalFilmDirectorStoryDraft, request finalfilm.DirectorPlanRequest) (model.DirectorStoryPlan, error) {
	if draft == nil || request.EvidenceDigest == nil {
		return model.DirectorStoryPlan{}, errors.New("Director omitted the required story decision")
	}
	if draft.TargetDurationMS < 90_000 || draft.TargetDurationMS > 120_000 {
		return model.DirectorStoryPlan{}, errors.New("Director target duration is outside 90-120 seconds")
	}
	factDrafts := map[string]struct {
		Speed  float64
		Reason string
	}{}
	for _, item := range draft.Facts {
		id := strings.TrimSpace(item.PublicFactID)
		if id == "" || factDrafts[id].Speed != 0 {
			return model.DirectorStoryPlan{}, errors.New("Director fact decision is blank or duplicated")
		}
		factDrafts[id] = struct {
			Speed  float64
			Reason string
		}{item.Speed, strings.TrimSpace(item.Reason)}
	}
	factByID := map[string]model.PublicNarrativeFact{}
	for _, fact := range request.EvidenceDigest.PublicFacts {
		factByID[fact.FactID] = fact
	}
	story := model.DirectorStoryPlan{SchemaVersion: model.DirectorStoryPlanSchemaVersion, TargetDurationMS: draft.TargetDurationMS, SkillVersions: copyFinalFilmSkillVersions(request.StoryPlan), AudioPlan: model.DirectorAudioPlan{TargetLUFS: -16, TruePeakDB: -1, PreserveSource: true, BackgroundMode: "light_electronic_music_with_source_interaction_audio"}}
	if strings.TrimSpace(draft.BackgroundMode) != "" {
		story.AudioPlan.BackgroundMode = strings.TrimSpace(draft.BackgroundMode)
	}
	for index, step := range request.EvidenceDigest.RequiredSteps {
		decision, ok := factDrafts[step.PublicNarrativeFactID]
		fact, factOK := factByID[step.PublicNarrativeFactID]
		if !ok || !factOK {
			return model.DirectorStoryPlan{}, fmt.Errorf("Director omitted public fact %s", step.PublicNarrativeFactID)
		}
		speed := decision.Speed
		if fact.Chapter == "build_wait" {
			if speed < 4 || speed > 12 {
				return model.DirectorStoryPlan{}, errors.New("build-wait speed must remain within 4-12x")
			}
		} else if speed != 1 {
			return model.DirectorStoryPlan{}, fmt.Errorf("chapter %s must remain at 1x", fact.Chapter)
		}
		duration := int(float64(step.SourceRangeMS[1]-step.SourceRangeMS[0]) / speed)
		caption := fact.ApprovedCaptionVariants[0]
		story.Timeline = append(story.Timeline, model.DirectorTimelineSegment{SegmentID: fmt.Sprintf("fact_%03d", index+1), Kind: "fact", SourceArtifactID: firstString(step.ArtifactIDs), SourceStepID: step.StepID, SourceRangeMS: &step.SourceRangeMS, Placement: "fact_order", Speed: speed, OutputDurationMS: maxDirectorInt(1, duration), Caption: caption})
		story.Beats = append(story.Beats, model.DirectorStoryBeat{BeatID: fmt.Sprintf("beat_%03d", index+1), Purpose: fact.Chapter, StepIDs: []string{step.StepID}, Caption: caption, DurationMS: maxDirectorInt(1, duration)})
		story.DecisionLog = append(story.DecisionLog, fact.Chapter+": "+decision.Reason)
	}
	generated := map[string]struct{ Placement, AnchorFact, Reason string }{}
	for _, item := range draft.Generated {
		generated[item.IntentID] = struct{ Placement, AnchorFact, Reason string }{item.Placement, item.AnchorAfterPublicFactID, item.Reason}
	}
	for index, intent := range request.Intents {
		decision, ok := generated[intent.IntentID]
		if !ok {
			return model.DirectorStoryPlan{}, fmt.Errorf("Director omitted generated placement %s", intent.IntentID)
		}
		placement, anchorStep := normalizeGeneratedPlacement(intent.Purpose, decision.Placement, decision.AnchorFact, request.EvidenceDigest)
		if placement == "" {
			return model.DirectorStoryPlan{}, fmt.Errorf("invalid generated placement for %s", intent.IntentID)
		}
		story.Timeline = append(story.Timeline, model.DirectorTimelineSegment{SegmentID: fmt.Sprintf("generated_%02d", index+1), Kind: "generated_presentation", IntentID: intent.IntentID, Placement: placement, AnchorAfterStepID: anchorStep, Speed: 1, OutputDurationMS: intent.RequestedSlot.PreferredDurationSec * 1000})
		story.DecisionLog = append(story.DecisionLog, intent.Purpose+": "+strings.TrimSpace(decision.Reason))
	}
	story.DecisionLog = append(story.DecisionLog, draft.DecisionLog...)
	if err := model.ValidateDirectorStoryPlan(story, model.FinalFilmDurationRange{MinMS: 90_000, MaxMS: 120_000}); err != nil {
		return model.DirectorStoryPlan{}, err
	}
	return story, nil
}

func copyFinalFilmSkillVersions(fallback *model.DirectorStoryPlan) map[string]string {
	result := map[string]string{}
	if fallback != nil {
		for key, value := range fallback.SkillVersions {
			result[key] = value
		}
	}
	return result
}

func normalizeGeneratedPlacement(purpose, placement, anchorFact string, digest *model.DirectorEvidenceDigest) (string, string) {
	// Slot placement is a server-owned story invariant. The Director decides
	// the adjacent visual idea and records its reason, but an omitted synonym or
	// anchor must not invalidate an otherwise sound plan. Canonicalize the three
	// fixed guided-demo slots against public chapter semantics.
	switch purpose {
	case "intro":
		return "before_first_required_step", ""
	case "outro":
		return "after_last_required_step", ""
	case "section_divider":
		if placement == "after_public_fact" && strings.TrimSpace(anchorFact) != "" {
			for _, step := range digest.RequiredSteps {
				if step.PublicNarrativeFactID == anchorFact && step.Chapter == "build_wait" {
					return "between_sections", step.StepID
				}
			}
		}
		for _, step := range digest.RequiredSteps {
			if step.Chapter == "build_wait" {
				return "between_sections", step.StepID
			}
		}
	}
	return "", ""
}

func firstString(values []string) string {
	if len(values) > 0 {
		return values[0]
	}
	return ""
}
func maxDirectorInt(left, right int) int {
	if left > right {
		return left
	}
	return right
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
