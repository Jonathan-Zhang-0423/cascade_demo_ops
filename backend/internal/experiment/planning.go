package experiment

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/llm"
)

type WorkflowSelectionSignals struct {
	UserGoal             string `json:"user_goal"`
	InteractionStructure string `json:"interaction_structure"`
	InteractionSurface   string `json:"interaction_surface"`
	ResultType           string `json:"result_type"`
}

func SelectTaskPack(signals WorkflowSelectionSignals) (string, error) {
	values := []string{signals.UserGoal, signals.InteractionStructure, signals.InteractionSurface, signals.ResultType}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return "", errors.New("task pack selection requires all four semantic signals")
		}
	}
	structure := strings.ToLower(strings.TrimSpace(signals.InteractionStructure))
	result := strings.ToLower(strings.TrimSpace(signals.ResultType))
	if (structure == "asynchronous_build" || structure == "async_builder") && (result == "interactive_product" || result == "interactive_surface") {
		return WorkflowTemplateAsyncProductDemo, nil
	}
	return "", errors.New("no compatible task pack for the observed semantic structure")
}

type ProductSpecPlanner struct{ client llm.Client }

func NewProductSpecPlanner(client llm.Client) (*ProductSpecPlanner, error) {
	if client == nil {
		return nil, errors.New("product specification planner is unavailable")
	}
	return &ProductSpecPlanner{client: client}, nil
}

func (p *ProductSpecPlanner) Generate(ctx context.Context, shortGoal string) (ProductSpec, error) {
	shortGoal = strings.TrimSpace(shortGoal)
	if shortGoal == "" || len(shortGoal) > 1000 {
		return ProductSpec{}, errors.New("short product goal is required and must be bounded")
	}
	request := llm.JSONRequest{
		System:     "You compile a product request into a provider-neutral ProductSpecArtifact. Return structured product requirements plus build_brief: one concise clause of at most 140 characters containing only the product capabilities, visual direction, and interactions that the builder must implement. Keep detailed acceptance criteria internal to the artifact. Do not mention the execution harness, recording, screenshots, visual polling, audit, credentials, video generation, media providers, or post-production. Acceptance criteria must be observable through visual plus DOM/ARIA/route/network evidence and must not contain selectors or hostnames.",
		User:       "Compile this short product goal into demoops.product_spec_artifact.v1:\n" + shortGoal,
		SchemaName: ProductSpecSchemaVersion, MaxTokens: 3000, Temperature: 0.2,
		ResponseHint: `{"schema_version":"demoops.product_spec_artifact.v1","spec_id":"product_spec_generated","title":"...","objective":"...","audience":"...","build_brief":"one concise builder-facing product clause","requirements":[{"id":"requirement_1","statement":"...","priority":"must"}],"visual_direction":{"theme":"...","palette":["..."],"motion":"..."},"interaction_requirements":[{"id":"interaction_1","statement":"...","priority":"must"}],"responsive_requirements":["..."],"observable_acceptance":[{"id":"criterion_1","statement":"...","evidence_kinds":["visual","dom"],"required":true}],"forbidden_outcomes":["..."]}`,
	}
	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		var spec ProductSpec
		_, err := p.client.GenerateJSON(ctx, config.ModelTaskPlanning, request, &spec)
		if err == nil {
			err = ValidateProductSpecQuality(spec)
		}
		if err == nil {
			return spec, nil
		}
		lastErr = err
		request.User = fmt.Sprintf("The previous draft failed the deterministic ProductSpec quality gate: %s\nRegenerate the same product goal once and correct only those structural issues:\n%s", boundedPlanningError(err), shortGoal)
	}
	return ProductSpec{}, fmt.Errorf("product specification failed after one regeneration: %w", lastErr)
}

func ValidateProductSpecQuality(spec ProductSpec) error {
	if err := ValidateProductSpec(spec); err != nil {
		return fmt.Errorf("product specification contract failed: %w", err)
	}
	seen := map[string]bool{}
	for _, criterion := range spec.ObservableAcceptance {
		if strings.TrimSpace(criterion.ID) == "" || strings.TrimSpace(criterion.Statement) == "" || seen[criterion.ID] || len(criterion.EvidenceKinds) < 2 {
			return errors.New("product acceptance criteria must be unique and use independent evidence channels")
		}
		visual, independent := false, false
		for _, kind := range criterion.EvidenceKinds {
			switch strings.ToLower(strings.TrimSpace(kind)) {
			case "visual", "frame", "region_change":
				visual = true
			case "dom", "aria", "route", "network":
				independent = true
			}
		}
		if criterion.Required && (!visual || !independent) {
			return errors.New("required product acceptance criterion lacks visual and independent structural evidence")
		}
		seen[criterion.ID] = true
	}
	serialized := strings.ToLower(fmt.Sprintf("%s %s %+v %+v %+v %+v %+v %+v", spec.Objective, spec.BuildBrief, spec.Requirements, spec.VisualDirection, spec.InteractionRequirements, spec.ResponsiveRequirements, spec.ObservableAcceptance, spec.ForbiddenOutcomes))
	for _, forbidden := range []string{"demoops", "h3", "seedance", "ffmpeg", "visual polling", "screenshot polling", "credential", "selector", "hostname"} {
		if strings.Contains(serialized, forbidden) {
			return fmt.Errorf("product specification leaks harness term %q", forbidden)
		}
	}
	return nil
}

func boundedPlanningError(err error) string {
	if err == nil {
		return "unknown quality failure"
	}
	value := strings.ReplaceAll(strings.ReplaceAll(err.Error(), "\r", " "), "\n", " ")
	if len(value) > 300 {
		value = value[:300]
	}
	return value
}
