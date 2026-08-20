package finalfilm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

type ConstraintCompileInput struct {
	SourcePackageID string
	Catalog         model.AssetTimelineCatalog
	BaselinePlan    model.DemoEditPlan
	Intents         []model.PresentationGenerationIntent
	Canvas          model.RenderCanvas
	Now             time.Time
}

func CompileStoryboardConstraints(input ConstraintCompileInput) (model.StoryboardConstraintSet, error) {
	if strings.TrimSpace(input.SourcePackageID) == "" {
		return model.StoryboardConstraintSet{}, errors.New("source_package_id is required")
	}
	if input.Catalog.SchemaVersion != model.AssetTimelineCatalogSchemaVersion || strings.TrimSpace(input.Catalog.CatalogID) == "" {
		return model.StoryboardConstraintSet{}, errors.New("a valid asset timeline catalog is required")
	}
	if input.BaselinePlan.CatalogID != input.Catalog.CatalogID || strings.TrimSpace(input.BaselinePlan.PlanID) == "" {
		return model.StoryboardConstraintSet{}, errors.New("baseline plan must bind the supplied catalog")
	}
	if err := model.ValidatePresentationGenerationIntents(input.Intents); err != nil {
		return model.StoryboardConstraintSet{}, err
	}
	if input.Now.IsZero() {
		input.Now = time.Now().UTC()
	}
	if input.Canvas.Width == 0 {
		input.Canvas = model.RenderCanvas{Width: 1920, Height: 1080, FPS: 30, Format: "mp4"}
	}

	catalogDigest, err := digestJSON(input.Catalog)
	if err != nil {
		return model.StoryboardConstraintSet{}, err
	}
	artifacts := make(map[string]model.TimelineArtifact, len(input.Catalog.Artifacts))
	allowedArtifactIDs := make([]string, 0, len(input.Catalog.Artifacts))
	for _, artifact := range input.Catalog.Artifacts {
		if strings.TrimSpace(artifact.ID) == "" || artifact.Sensitive {
			continue
		}
		artifacts[artifact.ID] = artifact
		allowedArtifactIDs = append(allowedArtifactIDs, artifact.ID)
	}
	sort.Strings(allowedArtifactIDs)

	requiredSteps := append([]model.TimelineStep{}, input.Catalog.Steps...)
	sort.SliceStable(requiredSteps, func(i, j int) bool { return requiredSteps[i].Order < requiredSteps[j].Order })
	requiredOrder := []string{}
	coverage := map[string]model.RequiredStepConstraint{}
	bindings := []model.RequirementBinding{}
	for _, step := range requiredSteps {
		if !step.Required {
			continue
		}
		if strings.TrimSpace(step.StepID) == "" || step.EndMS <= step.StartMS || len(step.Artifacts) == 0 {
			return model.StoryboardConstraintSet{}, fmt.Errorf("required step %q is missing identity, timing, or artifacts", step.StepID)
		}
		for _, artifactID := range step.Artifacts {
			if _, ok := artifacts[artifactID]; !ok {
				return model.StoryboardConstraintSet{}, fmt.Errorf("required step %s references missing, sensitive, or disallowed artifact %s", step.StepID, artifactID)
			}
		}
		order := len(requiredOrder) + 1
		timeRange := model.MillisecondRange{step.StartMS, step.EndMS}
		requiredOrder = append(requiredOrder, step.StepID)
		coverage[step.StepID] = model.RequiredStepConstraint{
			StepID: step.StepID, Order: order, SourceArtifactIDs: append([]string{}, step.Artifacts...),
			SourceTimeRangeMS: &timeRange, ExpectedOutcome: step.ExpectedOutcome, ObservedState: step.ObservedState,
		}
		bindings = append(bindings, model.RequirementBinding{
			RequirementID: "required_step:" + step.StepID, Source: "asset_timeline_catalog.required_step", StepIDs: []string{step.StepID},
		})
	}
	if err := validateBaselinePlanCoverage(input.BaselinePlan, requiredOrder, artifacts); err != nil {
		return model.StoryboardConstraintSet{}, err
	}

	slots := make([]model.PresentationSlotConstraint, 0, len(input.Intents))
	for _, intent := range input.Intents {
		for _, artifactID := range intent.ReferenceAssetRefs {
			if _, ok := artifacts[artifactID]; !ok {
				return model.StoryboardConstraintSet{}, fmt.Errorf("presentation intent %s references missing, sensitive, or disallowed artifact %s", intent.IntentID, artifactID)
			}
		}
		slots = append(slots, model.PresentationSlotConstraint{
			IntentID: intent.IntentID, Purpose: intent.Purpose, PreferredDurationSec: intent.RequestedSlot.PreferredDurationSec,
			AspectRatio: intent.RequestedSlot.AspectRatio, ReferenceArtifactIDs: append([]string{}, intent.ReferenceAssetRefs...), Required: intent.Required,
		})
		bindings = append(bindings, model.RequirementBinding{RequirementID: "presentation_intent:" + intent.IntentID, Source: "app.presentation_generation_intent", IntentIDs: []string{intent.IntentID}})
	}

	setIDSeed := struct {
		SourcePackageID string
		CatalogDigest   string
		PlanID          string
		Intents         []model.PresentationGenerationIntent
	}{input.SourcePackageID, catalogDigest, input.BaselinePlan.PlanID, input.Intents}
	setDigest, err := digestJSON(setIDSeed)
	if err != nil {
		return model.StoryboardConstraintSet{}, err
	}
	set := model.StoryboardConstraintSet{
		SchemaVersion: model.StoryboardConstraintSetSchemaVersion, ConstraintSetID: "constraints_" + setDigest[:20],
		SourcePackageID: input.SourcePackageID, CatalogID: input.Catalog.CatalogID, CatalogDigestSHA256: catalogDigest,
		RequiredStepOrder: requiredOrder, RequiredStepCoverage: coverage, AllowedArtifactIDs: allowedArtifactIDs,
		PresentationSlots: slots, TargetDurationMS: effectiveTargetDuration(input.BaselinePlan), Canvas: input.Canvas,
		FactTrackPolicy:      model.FactTrackPolicy{CapturedMediaOnly: true, PreserveRequiredStepOrder: true, LockSourceArtifactBindings: true, LockSourceTimeRanges: true, RequireValidatedOutcome: true},
		GeneratedTrackPolicy: model.GeneratedTrackPolicy{Optional: true, PresentationOnly: true, RequiresExplicitReview: true, FailurePolicy: model.PresentationGenerationFailureContinue},
		RequirementBindings:  bindings, CreatedAt: input.Now.UTC(),
	}
	if err := model.ValidateStoryboardConstraintSet(set); err != nil {
		return model.StoryboardConstraintSet{}, err
	}
	return set, nil
}

func validateBaselinePlanCoverage(plan model.DemoEditPlan, requiredOrder []string, artifacts map[string]model.TimelineArtifact) error {
	seenRequired := []string{}
	seen := map[string]bool{}
	for index, shot := range plan.Shots {
		artifact, ok := artifacts[shot.SourceArtifactID]
		if !ok {
			return fmt.Errorf("baseline plan shot %d references missing, sensitive, or disallowed artifact %s", index, shot.SourceArtifactID)
		}
		if shot.SourceStepID == "" {
			continue
		}
		if artifact.Kind == "generated_video_candidate" || strings.HasPrefix(artifact.Kind, "generated_") {
			return fmt.Errorf("baseline fact shot %s cannot use generated artifact %s", shot.ID, artifact.ID)
		}
		for _, required := range requiredOrder {
			if shot.SourceStepID == required && !seen[required] {
				seen[required] = true
				seenRequired = append(seenRequired, required)
			}
		}
	}
	if len(seenRequired) != len(requiredOrder) {
		return fmt.Errorf("baseline plan does not cover every required step: got %v want %v", seenRequired, requiredOrder)
	}
	for index := range requiredOrder {
		if seenRequired[index] != requiredOrder[index] {
			return fmt.Errorf("baseline plan changes required step order: got %v want %v", seenRequired, requiredOrder)
		}
	}
	return nil
}

func effectiveTargetDuration(plan model.DemoEditPlan) int {
	if plan.TargetDurationMS > 0 {
		return plan.TargetDurationMS
	}
	total := 0
	for _, shot := range plan.Shots {
		if shot.OutputDurationMS > 0 {
			total += shot.OutputDurationMS
		} else if shot.SourceTimeRangeMS != nil {
			speed := 1.0
			for index := len(shot.Operations) - 1; index >= 0; index-- {
				if shot.Operations[index].Type == model.EditOperationSpeed && shot.Operations[index].Speed != nil {
					speed = math.Max(0.5, math.Min(12, *shot.Operations[index].Speed))
					break
				}
			}
			total += int(math.Round(float64(shot.SourceTimeRangeMS[1]-shot.SourceTimeRangeMS[0]) / speed))
		}
	}
	return total
}

func digestJSON(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}
