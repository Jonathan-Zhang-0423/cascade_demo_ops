package app

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

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
				DirectExecution: true, RequiresSubmission: true, MayRequireExecutionConfirmation: true, ObserveAsyncResult: true,
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
	applyAdaptiveBusinessRuntimePolicy(stagePlan, request.ObservationPlan, request.BuildPrompt)
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

// applyAdaptiveBusinessRuntimePolicy lets the first runtime-discovered
// execution confirmation live for the same idle window as the asynchronous
// business observation. A generated plan may take substantially longer than
// the generic planner's compatibility timeout to expose its confirmation
// control. Only the first continuation receives the long wait: a follow-up is
// conditional on a committed first effect and remains deliberately bounded.
// Direct-execution mode is represented as an idempotent boolean configuration,
// never as a guessed primary-button click.
func applyAdaptiveBusinessRuntimePolicy(stagePlan *model.BusinessStagePlan, observationPlan experiment.ObservationPlan, buildPrompt string) {
	if stagePlan == nil {
		return
	}
	confirmationValue := adaptiveContinuationConfirmationValue(buildPrompt)
	for index := range stagePlan.Stages {
		stage := &stagePlan.Stages[index]
		switch stage.ID {
		case "business_stage_select_build_mode":
			if stage.Action.Parameters == nil {
				stage.Action.Parameters = map[string]string{}
			}
			stage.Action.Parameters["action_recipe"] = "configure_boolean"
			stage.Action.Parameters["desired_checked"] = "false"
			stage.Action.Parameters["allowed_names"] = "计划,规划,Plan,Planning"
			if stage.InteractionContract != nil {
				if stage.InteractionContract.Parameters == nil {
					stage.InteractionContract.Parameters = map[string]any{}
				}
				stage.InteractionContract.Parameters["action_recipe"] = "configure_boolean"
				stage.InteractionContract.Parameters["desired_checked"] = "false"
				stage.InteractionContract.Parameters["allowed_names"] = "计划,规划,Plan,Planning"
			}
		case "business_stage_continue_prepared_execution":
			if stage.Action.Parameters == nil {
				stage.Action.Parameters = map[string]string{}
			}
			if observationPlan.DeferAfterMS > 0 {
				stage.Action.Parameters["target_wait_timeout_ms"] = strconv.Itoa(observationPlan.DeferAfterMS)
			}
			stage.Action.Parameters["continuation_confirmation_value"] = confirmationValue
			if stage.InteractionContract != nil {
				if stage.InteractionContract.Parameters == nil {
					stage.InteractionContract.Parameters = map[string]any{}
				}
				if observationPlan.DeferAfterMS > 0 {
					stage.InteractionContract.Parameters["target_wait_timeout_ms"] = strconv.Itoa(observationPlan.DeferAfterMS)
				}
				stage.InteractionContract.Parameters["continuation_confirmation_value"] = confirmationValue
			}
		}
	}
}

func adaptiveContinuationConfirmationValue(buildPrompt string) string {
	for _, value := range buildPrompt {
		if unicode.Is(unicode.Han, value) {
			return "确认，继续执行。"
		}
	}
	return "Confirm and continue."
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
