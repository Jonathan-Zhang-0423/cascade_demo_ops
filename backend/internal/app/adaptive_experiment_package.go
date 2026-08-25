package app

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"cascade-demoops/backend/internal/agents"
	"cascade-demoops/backend/internal/experiment"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

// prepareAdaptiveExperimentRun compiles the frozen experiment artifacts
// directly into the existing ClientExecutionPackage boundary. It deliberately
// does not enter CascadeFlow: the adaptive experiment has already selected its
// task pack, product specification, observation plan, and interaction plan.
// The returned CascadeState is only a compatibility envelope for the current
// package approval and Direct Transport adapters.
func (s *Service) prepareAdaptiveExperimentRun(ctx context.Context, request experiment.LegExecutionRequest, contracts []model.InteractionContract) (ProductRunPrepareResult, error) {
	if s == nil || s.states == nil {
		return ProductRunPrepareResult{}, errors.New("adaptive experiment package store is unavailable")
	}
	parsed, err := url.Parse(strings.TrimSpace(request.TargetURL))
	if err != nil || parsed.Hostname() == "" {
		return ProductRunPrepareResult{}, errors.New("adaptive experiment target URL is invalid")
	}
	if strings.TrimSpace(request.BuildPrompt) == "" || strings.TrimSpace(request.ProjectName) == "" {
		return ProductRunPrepareResult{}, errors.New("adaptive experiment requires a frozen build prompt and project name")
	}

	now := time.Now().UTC()
	projectID := "proj_" + shortID(request.RunID+"|"+request.LegID)
	project := &model.ProjectContext{
		ID: projectID, SchemaVersion: model.ProjectContextSchemaVersion, Mode: model.AppModeWeb,
		Name: request.ProjectName, ProductURL: request.TargetURL,
		ProductDescription: request.BuildPrompt, TargetAudience: request.ProductSpec.Audience,
		BrandTone:   request.ProductSpec.VisualDirection.Theme,
		MustShow:    interactionSemanticGoals(request.InteractionPlan),
		MustNotShow: append([]string(nil), request.ProductSpec.ForbiddenOutcomes...),
		DemoAccount: &model.DemoAccount{
			UsernameSecretRef: request.CredentialRef,
			PasswordSecretRef: request.CredentialRef,
			Provider:          "local_vault",
			Scope:             "browser_login",
		},
		AccessPolicy: &model.AccessPolicy{
			CredentialVaultRequired: true, SessionIsolation: true, AutoExpireCredentials: true,
			AllowedDomains: []string{parsed.Hostname()}, AuditLogRequired: true,
		},
		Inputs: &model.ProjectInputBundle{
			ProductURLs:          []model.ProductURLInput{{URL: request.TargetURL, Kind: "web_app", Environment: "experiment"}},
			Requirements:         adaptiveExperimentRequirements(request.ProductSpec),
			InteractionContracts: append([]model.InteractionContract(nil), contracts...),
			WorkflowExecution: &model.WorkflowExecutionHints{
				TaskPackID: request.WorkflowTemplateID, RequiresFreshEntity: true,
				EntityName: request.ProjectName, PrimaryInputSemantic: "product_spec",
				DirectExecution: true, RequiresSubmission: true, ObserveAsyncResult: true,
			},
			RawUserPrompt: request.BuildPrompt,
			Metadata: map[string]any{
				"experiment_run_id": request.RunID, "experiment_leg_id": request.LegID,
				"harness_profile": request.HarnessProfile,
			},
		},
		CreatedAt: now, UpdatedAt: now,
	}

	stagePlan, err := agents.NewBusinessStagePlannerAgent().PlanBusinessStages(ctx, project, nil, nil, nil, nil, nil)
	if err != nil {
		return ProductRunPrepareResult{}, err
	}
	intelligence := &model.ProjectIntelligencePack{
		ID: "intel_" + shortID(projectID), ProjectID: projectID,
		SchemaVersion:     model.ProjectIntelligencePackSchemaVersion,
		BusinessStagePlan: stagePlan,
		ScriptReadinessReport: &model.ScriptReadinessReport{
			ID: "readiness_" + shortID(projectID), ProjectID: projectID,
			SchemaVersion: model.ScriptReadinessReportSchemaVersion,
			CanProceed:    true, Summary: "冻结的 Task Pack 和 Interaction Contract 可直接编译。",
			SuggestedStageCount: len(stagePlan.Stages), SuggestedTargetDurationSec: 105,
			BusinessActionCount: stagePlan.CoreBusinessStageCount, CredentialCoverage: request.CredentialRef != "",
			Confidence: 0.9, CreatedAt: now,
		},
		Confidence: 0.9, CreatedAt: now,
	}
	project.ProjectIntelligence = intelligence
	graph, err := agents.NewGraphBuilderAgent().GenerateGraph(ctx, project, nil, nil, intelligence)
	if err != nil {
		return ProductRunPrepareResult{}, err
	}
	script, err := agents.NewScriptPackagerAgent().PackageScript(ctx, project, nil, nil, graph, intelligence)
	if err != nil {
		return ProductRunPrepareResult{}, err
	}
	state := &orchestrator.CascadeState{
		ProjectID: projectID, CurrentNode: orchestrator.NodeScriptPackage,
		Status: orchestrator.FlowStatusAwaitingHuman, ProjectContext: project,
		ProjectIntelligence: intelligence, ScriptReadinessReport: intelligence.ScriptReadinessReport,
		WorkflowGraph: graph, ScriptDocument: script.Document, ScriptMarkdown: script.Markdown,
		ScriptMarkdownArtifact: script.MarkdownArtifact, ApprovalMarkdownArtifact: script.MarkdownArtifact,
		ExecutableScriptBundle: script.ExecutableBundle,
	}
	if err := s.states.Save(ctx, state); err != nil {
		return ProductRunPrepareResult{}, err
	}
	build, err := buildClientExecutionPackageFromState(state, defaultDesktopOrgID, now)
	if err != nil {
		return ProductRunPrepareResult{}, err
	}
	return ProductRunPrepareResult{State: compactStateForPrepareResponse(state, &build), Build: &build}, nil
}

func adaptiveExperimentRequirements(spec experiment.ProductSpec) []model.DemoRequirement {
	values := make([]model.DemoRequirement, 0, len(spec.Requirements)+len(spec.ObservableAcceptance))
	for _, requirement := range spec.Requirements {
		values = append(values, model.DemoRequirement{
			ID: requirement.ID, Kind: "product_requirement", Description: requirement.Statement,
			Required: strings.EqualFold(requirement.Priority, "must"),
		})
	}
	for _, acceptance := range spec.ObservableAcceptance {
		values = append(values, model.DemoRequirement{
			ID: acceptance.ID, Kind: "observable_acceptance", Description: acceptance.Statement,
			Required: acceptance.Required,
		})
	}
	return values
}
