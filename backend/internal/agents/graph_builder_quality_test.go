package agents

import (
	"context"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/model"
)

func TestGraphBuilderPrefersExecutableBusinessSelector(t *testing.T) {
	project := graphQualityProject()
	productMap := graphQualityProductMap(
		model.UIActionRef{ID: "generic", Label: "Open area", Kind: "click", Selector: "main"},
		model.UIActionRef{ID: "create", Label: "Create campaign", Kind: "click", Selector: "[data-testid='create-campaign']"},
	)

	graph, err := NewGraphBuilderAgent().GenerateGraph(context.Background(), project, productMap, graphQualityReport(project), nil)
	if err != nil {
		t.Fatal(err)
	}
	node := graphNodeByID(graph, "highlight_primary_value")
	if node == nil {
		t.Fatal("missing highlight_primary_value node")
	}
	if node.ActionSpec == nil || node.ActionSpec.Type != model.GraphActionClick {
		t.Fatalf("expected executable click node, got %+v", node.ActionSpec)
	}
	if got := node.ActionSpec.Target.Selector; got != "[data-testid='create-campaign']" {
		t.Fatalf("expected data-testid selector, got %q", got)
	}
	if node.DurationHintMS < 10000 {
		t.Fatalf("expected natural stage duration, got %d", node.DurationHintMS)
	}
}

func TestGraphBuilderDowngradesGenericBusinessSelector(t *testing.T) {
	project := graphQualityProject()
	productMap := graphQualityProductMap(
		model.UIActionRef{ID: "generic", Label: "Click main", Kind: "click", Selector: "main"},
	)

	graph, err := NewGraphBuilderAgent().GenerateGraph(context.Background(), project, productMap, graphQualityReport(project), nil)
	if err != nil {
		t.Fatal(err)
	}
	node := graphNodeByID(graph, "highlight_primary_value")
	if node == nil || node.ActionSpec == nil {
		t.Fatalf("missing generated node: %+v", graph.Nodes)
	}
	if node.ActionSpec.Type != model.GraphActionInspect {
		t.Fatalf("generic selector must be downgraded to inspect, got %+v", node.ActionSpec)
	}
	for _, validation := range node.Validations {
		if validation.Severity == "blocking" || validation.Required {
			t.Fatalf("generic inspect node must not carry blocking validation: %+v", validation)
		}
	}
}

func TestGraphBuilderAvoidsLoginWhenBusinessActionExists(t *testing.T) {
	project := graphQualityProject()
	productMap := graphQualityProductMap(
		model.UIActionRef{ID: "login", Label: "Login", Kind: "click", Selector: "[data-testid='login-submit']"},
		model.UIActionRef{ID: "invite", Label: "Invite teammate", Kind: "click", Selector: "[data-testid='invite-user']"},
	)

	graph, err := NewGraphBuilderAgent().GenerateGraph(context.Background(), project, productMap, graphQualityReport(project), nil)
	if err != nil {
		t.Fatal(err)
	}
	node := graphNodeByID(graph, "highlight_primary_value")
	if node == nil || node.ActionSpec == nil {
		t.Fatalf("missing generated node: %+v", graph.Nodes)
	}
	if strings.Contains(strings.ToLower(node.Title+" "+node.ActionSpec.Target.Selector), "login") {
		t.Fatalf("primary value node should not repeat login when a business action exists: %+v", node)
	}
}

func TestGraphBuilderSkipsChromeToggleWhenBusinessActionExists(t *testing.T) {
	project := graphQualityProject()
	productMap := graphQualityProductMap(
		model.UIActionRef{ID: "sidebar", Label: "button sidebar toggle", Kind: "click", Selector: "[data-testid='button-sidebar-toggle']"},
		model.UIActionRef{ID: "new_project", Label: "New project", Kind: "click", Selector: "[data-testid='new-project']"},
	)

	graph, err := NewGraphBuilderAgent().GenerateGraph(context.Background(), project, productMap, graphQualityReport(project), nil)
	if err != nil {
		t.Fatal(err)
	}
	node := graphNodeByID(graph, "highlight_primary_value")
	if node == nil || node.ActionSpec == nil {
		t.Fatalf("missing generated node: %+v", graph.Nodes)
	}
	if got := node.ActionSpec.Target.Selector; got != "[data-testid='new-project']" {
		t.Fatalf("expected business selector instead of chrome toggle, got %q", got)
	}
}

func TestGraphBuilderDowngradesChromeToggleOnlyAction(t *testing.T) {
	project := graphQualityProject()
	productMap := graphQualityProductMap(
		model.UIActionRef{ID: "sidebar", Label: "button sidebar toggle", Kind: "click", Selector: "[data-testid='button-sidebar-toggle']"},
	)

	graph, err := NewGraphBuilderAgent().GenerateGraph(context.Background(), project, productMap, graphQualityReport(project), nil)
	if err != nil {
		t.Fatal(err)
	}
	node := graphNodeByID(graph, "highlight_primary_value")
	if node == nil || node.ActionSpec == nil {
		t.Fatalf("missing generated node: %+v", graph.Nodes)
	}
	if node.ActionSpec.Type != model.GraphActionInspect {
		t.Fatalf("chrome-only action should be downgraded to inspect, got %+v", node.ActionSpec)
	}
}

func graphQualityProject() *model.ProjectContext {
	return &model.ProjectContext{
		ID:             "project_graph_quality",
		SchemaVersion:  model.ProjectContextSchemaVersion,
		Mode:           model.AppModeDesktop,
		ProductURL:     "https://app.example.com/dashboard",
		TargetAudience: "product team",
		AccessPolicy:   &model.AccessPolicy{AllowedDomains: []string{"app.example.com"}},
	}
}

func graphQualityReport(project *model.ProjectContext) *model.MultimodalUnderstandingReport {
	return &model.MultimodalUnderstandingReport{
		ID:            "report_graph_quality",
		ProjectID:     project.ID,
		SchemaVersion: model.MultimodalUnderstandingReportSchemaVersion,
		Summary:       "quality fixture",
	}
}

func graphQualityProductMap(actions ...model.UIActionRef) *model.ProductMap {
	return &model.ProductMap{
		ID:        "map_graph_quality",
		ProjectID: "project_graph_quality",
		Version:   1,
		Summary:   "quality fixture",
		Pages: []*model.ProductPage{{
			ID:             "page_dashboard",
			URL:            "https://app.example.com/dashboard",
			Title:          "Dashboard",
			Purpose:        "Show the main workflow.",
			PrimaryActions: actions,
		}},
		Features: []*model.Feature{{
			ID:        "feature_campaign",
			Name:      "Campaign workflow",
			UserValue: "Create and inspect a campaign workflow.",
		}},
	}
}

func graphNodeByID(graph *model.DemoWorkflowGraph, id string) *model.GraphNode {
	if graph == nil {
		return nil
	}
	for _, node := range graph.Nodes {
		if node != nil && node.ID == id {
			return node
		}
	}
	return nil
}
