package agents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

type GraphBuilderAgent struct{}

func NewGraphBuilderAgent() *GraphBuilderAgent { return &GraphBuilderAgent{} }

func (a *GraphBuilderAgent) GenerateGraph(ctx context.Context, project *model.ProjectContext, productMap *model.ProductMap) (*model.DemoWorkflowGraph, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if project == nil {
		return nil, errors.New("project context is required")
	}
	graphID := fmt.Sprintf("graph_%d", time.Now().UnixNano())
	featureID, featureValue := primaryFeature(productMap)
	audience := primaryAudience(project)
	entryPoint := graphEntryPoint(project, productMap)
	startAction := "inspect"
	startActionType := model.GraphActionInspect
	startExpectedOutcome := "product context is available"
	if isHTTPURL(entryPoint) {
		startAction = "navigate"
		startActionType = model.GraphActionNavigate
		startExpectedOutcome = "product entry is loaded"
	}
	useCase := model.DemoUseCaseLaunch
	if len(project.Goals) > 0 {
		useCase = project.Goals[0].UseCase
	}

	graph := model.NewDemoWorkflowGraph(graphID, project.ID, entryPoint)
	graph.Status = model.GraphStatusReviewReady
	graph.Name = "Primary product demo"
	graph.Summary = "Executable MVP demo workflow generated from the product map placeholder."
	graph.Intent = &model.WorkflowIntent{
		UseCase:            useCase,
		Audience:           audience,
		Objective:          "Show the core product value in a short, rehearseable demo.",
		ValueProposition:   featureValue,
		PrimaryFeatureRefs: []string{featureID},
		SuccessCriteria:    []string{"product entry loads", "primary value is visible", "demo story completes"},
		CTA:                "Review and approve the workflow graph.",
	}
	graph.Requirements = requirementsFromProject(project)
	graph.States = []*model.GraphState{
		{
			ID:         "state_product_entry",
			Name:       "Product entry loaded",
			Kind:       "page",
			URLPattern: entryPoint,
			DOMHints: []model.SelectorCandidate{
				{Kind: "css", Value: "main", Confidence: 0.6, Source: "mvp_placeholder"},
				{Kind: "css", Value: "body", Confidence: 0.6, Source: "mvp_placeholder"},
			},
			FeatureRefs: []string{featureID},
		},
	}
	graph.Validations = []*model.ValidationSpec{
		{
			ID:        "validate_entry_loaded",
			Kind:      "dom_visible",
			Target:    model.ActionTarget{Selector: "body"},
			Assertion: "body is visible after navigation",
			Expected:  "visible",
			Severity:  "blocking",
			Required:  true,
			RepairPolicy: &model.RepairPolicy{
				AllowSelectorRepair: true,
				AllowDataRepair:     false,
				AllowStepSkip:       false,
				MaxAttempts:         2,
			},
		},
	}
	graph.Narratives = []*model.NarrativeSegment{
		{
			ID:           "narrative_primary_value",
			NodeRefs:     []string{"start", "highlight_primary_value", "close"},
			Title:        "Primary product value",
			Summary:      featureValue,
			Voiceover:    featureValue,
			Tone:         project.BrandTone,
			AudienceLens: project.TargetAudience,
		},
	}
	graph.Nodes = []*model.GraphNode{
		{
			ID:              "start",
			Action:          startAction,
			Selector:        entryPoint,
			ExpectedOutcome: startExpectedOutcome,
			IsScreenshot:    true,
			HasZoom:         false,
			RetryPolicy:     2,
			Type:            model.GraphNodeTypeStart,
			Title:           "Open product entry",
			Goal:            "Load the product in an isolated browser session.",
			FeatureRefs:     []string{featureID},
			ActionSpec: &model.GraphAction{
				Type:      startActionType,
				Target:    model.ActionTarget{URL: entryPoint},
				TimeoutMS: 30000,
				WaitUntil: "networkidle",
			},
			StateAfter: []model.StateAssertion{
				{
					ID:        "state_after_start_body_visible",
					Kind:      "dom_visible",
					Target:    model.ActionTarget{Selector: "body"},
					Operator:  "is_visible",
					Expected:  true,
					Required:  true,
					TimeoutMS: 10000,
				},
			},
			Validations: []model.ValidationSpec{
				{
					ID:        "node_start_loaded",
					Kind:      "page_loaded",
					Target:    model.ActionTarget{URL: entryPoint},
					Assertion: "entry context is available for demo planning",
					Expected:  true,
					Severity:  "blocking",
					Required:  true,
				},
			},
			Narrative: &model.NarrativeCue{
				Title:        "Start from the product",
				Caption:      "Open the product environment.",
				AudienceLens: project.TargetAudience,
			},
			Capture: &model.CaptureSpec{Screenshot: true, Video: true, AssetRole: "opening_context"},
			FailurePolicy: &model.NodeFailurePolicy{
				RetryAttempts: 2,
				RepairPolicy:  &model.RepairPolicy{AllowSelectorRepair: true, AllowDataRepair: false, AllowStepSkip: false, MaxAttempts: 2},
			},
		},
		{
			ID:              "highlight_primary_value",
			Action:          "inspect",
			Selector:        "main",
			ExpectedOutcome: featureValue,
			IsScreenshot:    true,
			HasZoom:         true,
			RetryPolicy:     2,
			Type:            model.GraphNodeTypeCapture,
			Title:           "Highlight primary value",
			Goal:            "Capture the highest-value product area for this audience.",
			FeatureRefs:     []string{featureID},
			ActionSpec: &model.GraphAction{
				Type:      model.GraphActionInspect,
				Target:    model.ActionTarget{Selector: "main", SelectorAlternatives: []model.SelectorCandidate{{Kind: "css", Value: "[role='main']", Confidence: 0.5}}},
				TimeoutMS: 10000,
			},
			Validations: []model.ValidationSpec{
				{
					ID:        "node_primary_value_visible",
					Kind:      "dom_visible",
					Target:    model.ActionTarget{Selector: "main"},
					Assertion: "main product area is visible",
					Expected:  true,
					Severity:  "blocking",
					Required:  true,
					RepairPolicy: &model.RepairPolicy{
						AllowSelectorRepair: true,
						AllowDataRepair:     false,
						AllowStepSkip:       false,
						MaxAttempts:         2,
					},
				},
			},
			Narrative: &model.NarrativeCue{
				Title:        "Show the value",
				Voiceover:    featureValue,
				Caption:      featureValue,
				Callout:      "Primary value",
				Tone:         project.BrandTone,
				AudienceLens: project.TargetAudience,
			},
			Capture: &model.CaptureSpec{Screenshot: true, Video: true, Zoom: true, Callout: true, FocusSelector: "main", AssetRole: "hero_feature"},
			FailurePolicy: &model.NodeFailurePolicy{
				RetryAttempts: 2,
				RepairPolicy:  &model.RepairPolicy{AllowSelectorRepair: true, AllowDataRepair: true, AllowStepSkip: false, MaxAttempts: 2},
			},
		},
		{
			ID:              "close",
			Action:          "assert",
			Selector:        "body",
			ExpectedOutcome: "demo story is complete",
			IsScreenshot:    false,
			HasZoom:         true,
			RetryPolicy:     1,
			Type:            model.GraphNodeTypeEnd,
			Title:           "Complete demo story",
			Goal:            "Ensure the graph has a clean terminal state for rehearsal and rendering.",
			FeatureRefs:     []string{featureID},
			ActionSpec: &model.GraphAction{
				Type:      model.GraphActionAssert,
				Target:    model.ActionTarget{Selector: "body"},
				TimeoutMS: 5000,
			},
			Validations: []model.ValidationSpec{
				{
					ID:        "node_close_body_present",
					Kind:      "dom_present",
					Target:    model.ActionTarget{Selector: "body"},
					Assertion: "page remains available at the end of the story",
					Expected:  true,
					Severity:  "warning",
					Required:  true,
				},
			},
			Narrative: &model.NarrativeCue{
				Title:        "Wrap the story",
				Caption:      "The demo workflow is ready for rehearsal.",
				AudienceLens: project.TargetAudience,
			},
			Capture:       &model.CaptureSpec{Screenshot: false, Video: true, Zoom: true, AssetRole: "closing_validation"},
			FailurePolicy: &model.NodeFailurePolicy{RetryAttempts: 1},
		},
	}
	graph.Edges = []*model.GraphEdge{
		{ID: "e1", FromNode: "start", ToNode: "highlight_primary_value", Condition: "loaded", ConditionSpec: &model.EdgeCondition{Kind: "validation_passed", PassState: "state_product_entry"}, Priority: 1},
		{ID: "e2", FromNode: "highlight_primary_value", ToNode: "close", Condition: "validated", ConditionSpec: &model.EdgeCondition{Kind: "validation_passed"}, Priority: 1},
	}
	productMapID := ""
	if productMap != nil {
		productMapID = productMap.ID
	}
	graph.Provenance = &model.GraphProvenance{
		CreatedBy:    "GraphBuilderAgent",
		ProductMapID: productMapID,
	}
	graph.Maintenance = &model.MaintenancePolicy{
		UpdateTriggers:   []string{"release_note_changed", "route_changed", "selector_validation_failed"},
		StalenessDays:    30,
		RegressionChecks: []string{"rehearse_primary_workflow"},
	}
	if graph.Assets != nil {
		graph.Assets.Brand = project.BrandKit
		graph.Assets.Provenance = &model.AssetProvenance{WorkflowGraphID: graph.ID, GraphVersion: graph.Version, GeneratedBy: "GraphBuilderAgent"}
	}
	return graph, nil
}

func primaryFeature(productMap *model.ProductMap) (string, string) {
	if productMap != nil && len(productMap.Features) > 0 && productMap.Features[0] != nil {
		feature := productMap.Features[0]
		featureID := feature.ID
		if featureID == "" {
			featureID = "feature_primary_workflow"
		}
		if feature.UserValue != "" {
			return featureID, feature.UserValue
		}
		return featureID, feature.Name
	}
	return "feature_primary_workflow", "Shows the core product value in a short executable demo path."
}

func graphEntryPoint(project *model.ProjectContext, productMap *model.ProductMap) string {
	if productMap != nil && len(productMap.Pages) > 0 && productMap.Pages[0] != nil && productMap.Pages[0].URL != "" {
		return productMap.Pages[0].URL
	}
	if project.ProductURL != "" {
		return project.ProductURL
	}
	if project.Inputs != nil {
		if len(project.Inputs.WebpageScreenshots) > 0 {
			screenshot := project.Inputs.WebpageScreenshots[0]
			if screenshot.URL != "" {
				return screenshot.URL
			}
			return "input://webpage_screenshots/" + screenshot.ID
		}
		if len(project.Inputs.Code) > 0 {
			return "input://code/" + project.Inputs.Code[0].ID
		}
		if len(project.Inputs.RequirementDocuments) > 0 {
			return "input://requirements/" + project.Inputs.RequirementDocuments[0].ID
		}
	}
	return "input://product_context"
}

func isHTTPURL(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(normalized, "http://") || strings.HasPrefix(normalized, "https://")
}

func primaryAudience(project *model.ProjectContext) *model.AudienceProfile {
	if project != nil && len(project.Audiences) > 0 {
		return &project.Audiences[0]
	}
	return &model.AudienceProfile{ID: "audience_primary", Name: "primary audience"}
}

func requirementsFromProject(project *model.ProjectContext) []model.GraphRequirement {
	requirements := make([]model.GraphRequirement, 0, len(project.MustShow)+len(project.MustNotShow)+len(project.ForbiddenPages)+len(project.ForbiddenData))
	for i, item := range project.MustShow {
		requirements = append(requirements, model.GraphRequirement{
			ID:          fmt.Sprintf("must_show_%d", i+1),
			Kind:        "must_show",
			Description: item,
			Required:    true,
		})
	}
	for i, item := range project.MustNotShow {
		requirements = append(requirements, model.GraphRequirement{
			ID:          fmt.Sprintf("must_not_show_%d", i+1),
			Kind:        "must_not_show",
			Description: item,
			Required:    true,
		})
	}
	for i, item := range project.ForbiddenPages {
		requirements = append(requirements, model.GraphRequirement{
			ID:          fmt.Sprintf("forbidden_page_%d", i+1),
			Kind:        "forbidden_page",
			Description: item,
			Required:    true,
		})
	}
	for i, item := range project.ForbiddenData {
		requirements = append(requirements, model.GraphRequirement{
			ID:          fmt.Sprintf("forbidden_data_%d", i+1),
			Kind:        "forbidden_data",
			Description: item,
			Required:    true,
		})
	}
	return requirements
}
