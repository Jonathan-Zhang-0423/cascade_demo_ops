package agents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

type InputContextAgent struct{}

func NewInputContextAgent() *InputContextAgent { return &InputContextAgent{} }

func (a *InputContextAgent) BuildProjectContext(ctx context.Context, input orchestrator.UserInput) (*model.ProjectContext, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if input.Mode == "" {
		input.Mode = model.AppModeDesktop
	}
	if input.Mode != model.AppModeWeb && input.Mode != model.AppModeDesktop {
		return nil, fmt.Errorf("unsupported app mode: %s", input.Mode)
	}
	hasProductURL := strings.TrimSpace(input.ProductURL) != ""
	hasCode := strings.TrimSpace(input.GitRepoURL) != "" || strings.TrimSpace(input.LocalRepoPath) != "" || len(input.Code) > 0
	hasRequirements := strings.TrimSpace(input.ProductDescription) != "" || len(input.RequirementDocuments) > 0
	hasScreenshots := len(input.WebpageScreenshots) > 0
	if !hasProductURL && !hasCode && !hasRequirements && !hasScreenshots {
		return nil, errors.New("at least one product URL, code input, requirement document, webpage screenshot, or product description is required")
	}
	if strings.TrimSpace(input.TargetAudience) == "" {
		input.TargetAudience = "seed investor"
	}

	now := time.Now().UTC()
	projectID := fmt.Sprintf("proj_%d", time.Now().UnixNano())
	audience := model.AudienceProfile{
		ID:            "audience_primary",
		Name:          input.TargetAudience,
		Segment:       input.TargetAudience,
		PreferredTone: input.BrandTone,
	}
	useCase := defaultUseCaseForAudience(input.TargetAudience)
	project := &model.ProjectContext{
		ID:                 projectID,
		SchemaVersion:      model.ProjectContextSchemaVersion,
		Mode:               input.Mode,
		ProductURL:         input.ProductURL,
		GitRepoURL:         input.GitRepoURL,
		LocalRepoPath:      input.LocalRepoPath,
		ProductDescription: input.ProductDescription,
		TargetAudience:     input.TargetAudience,
		BrandTone:          input.BrandTone,
		MustShow:           input.MustShow,
		MustNotShow:        input.MustNotShow,
		ForbiddenPages:     input.ForbiddenPages,
		ForbiddenData:      input.ForbiddenData,
		Inputs: &model.ProjectInputBundle{
			ProductURLs:          productURLInputs(input),
			Code:                 codeInputs(input),
			Repositories:         repositoryInputs(input),
			RequirementDocuments: input.RequirementDocuments,
			WebpageScreenshots:   input.WebpageScreenshots,
			KnowledgeSources:     knowledgeSourcesFromInputs(input),
			Scenarios: []model.DemoScenario{
				{
					ID:              "scenario_primary",
					UseCase:         useCase,
					AudienceID:      audience.ID,
					Objective:       input.ProductDescription,
					DurationSeconds: 60,
					Priority:        1,
					MustShow:        input.MustShow,
					MustAvoid:       append([]string{}, input.MustNotShow...),
				},
			},
		},
		Goals: []model.DemoGoal{
			{
				ID:               "goal_primary",
				UseCase:          useCase,
				AudienceID:       audience.ID,
				ValueProposition: input.ProductDescription,
				SuccessCriteria:  []string{"graph can be approved", "rehearsal pass rate is at least 90%", "assets can be traced to graph nodes"},
				Priority:         1,
			},
		},
		Audiences: []model.AudienceProfile{audience},
		BrandKit: &model.BrandKit{
			ID:           "brand_default",
			VoiceAndTone: input.BrandTone,
		},
		AccessPolicy: &model.AccessPolicy{
			CredentialVaultRequired: true,
			SessionIsolation:        true,
			AutoExpireCredentials:   true,
			DefaultCredentialTTLSec: int((24 * time.Hour).Seconds()),
			AllowedCommands:         input.SSHAllowedCommands,
			AllowedPaths:            input.SSHAllowedPaths,
			AuditLogRequired:        true,
		},
		SecurityPolicy: &model.SecurityPolicy{
			ForbiddenPages:       input.ForbiddenPages,
			ForbiddenData:        input.ForbiddenData,
			PIIHandling:          "mask_in_artifacts",
			NetworkCapturePolicy: "metadata_only_by_default",
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if input.DemoUsername != "" || input.DemoPassword != "" {
		project.DemoAccount = &model.DemoAccount{
			UsernameSecretRef: "local-dev/demo_username",
			PasswordSecretRef: "local-dev/demo_password",
			Provider:          "demo_account",
			Scope:             "browser_login",
		}
		project.Inputs.Credentials = append(project.Inputs.Credentials, model.CredentialInput{
			ID:            "demo_account",
			Kind:          "username_password",
			SecretRef:     "local-dev/demo_account",
			Scope:         "browser_login",
			RequiredFor:   []string{"browser_scan", "workflow_rehearsal"},
			SessionPolicy: "isolated_ephemeral_session",
		})
	}
	if input.SSHHost != "" {
		port := input.SSHPort
		if port == 0 {
			port = 22
		}
		project.ServerAccess = &model.ServerAccess{
			Host:                input.SSHHost,
			Port:                port,
			UsernameSecretRef:   "local-dev/ssh_username",
			PrivateKeySecretRef: input.SSHPrivateKeySecretRef,
			PasswordSecretRef:   input.SSHPasswordSecretRef,
			AllowedPaths:        input.SSHAllowedPaths,
			AllowedCommands:     input.SSHAllowedCommands,
		}
	}
	return project, nil
}

func defaultUseCaseForAudience(audience string) model.DemoUseCase {
	normalized := strings.ToLower(audience)
	switch {
	case strings.Contains(normalized, "investor"):
		return model.DemoUseCaseInvestor
	case strings.Contains(normalized, "sales"):
		return model.DemoUseCaseSales
	case strings.Contains(normalized, "support"):
		return model.DemoUseCaseSupport
	case strings.Contains(normalized, "onboarding"):
		return model.DemoUseCaseOnboarding
	default:
		return model.DemoUseCaseLaunch
	}
}

func repositoryInputs(input orchestrator.UserInput) []model.RepositoryInput {
	repositories := make([]model.RepositoryInput, 0, 2)
	if input.GitRepoURL != "" {
		repositories = append(repositories, model.RepositoryInput{
			URL:      input.GitRepoURL,
			Provider: "git",
			ReadOnly: true,
			Primary:  true,
		})
	}
	if input.LocalRepoPath != "" {
		repositories = append(repositories, model.RepositoryInput{
			LocalPath: input.LocalRepoPath,
			Provider:  "local",
			ReadOnly:  true,
			Primary:   input.GitRepoURL == "",
		})
	}
	return repositories
}

func productURLInputs(input orchestrator.UserInput) []model.ProductURLInput {
	if strings.TrimSpace(input.ProductURL) == "" {
		return nil
	}
	return []model.ProductURLInput{
		{URL: input.ProductURL, Kind: "product", Environment: string(input.Mode)},
	}
}

func codeInputs(input orchestrator.UserInput) []model.CodeInput {
	code := append([]model.CodeInput{}, input.Code...)
	if input.GitRepoURL != "" {
		code = append(code, model.CodeInput{
			ID:       "code_git_repo",
			Kind:     "git_repository",
			URI:      input.GitRepoURL,
			ReadOnly: true,
		})
	}
	if input.LocalRepoPath != "" {
		code = append(code, model.CodeInput{
			ID:        "code_local_repo",
			Kind:      "local_repository",
			LocalPath: input.LocalRepoPath,
			ReadOnly:  true,
		})
	}
	for i := range code {
		if code[i].ID == "" {
			code[i].ID = fmt.Sprintf("code_%d", i+1)
		}
		if code[i].Kind == "" {
			code[i].Kind = "source_code"
		}
		code[i].ReadOnly = true
	}
	return code
}

func knowledgeSourcesFromInputs(input orchestrator.UserInput) []model.KnowledgeSource {
	sources := make([]model.KnowledgeSource, 0, len(input.RequirementDocuments)+len(input.WebpageScreenshots))
	for _, doc := range input.RequirementDocuments {
		source := model.KnowledgeSource{
			ID:       doc.ID,
			Kind:     model.EvidenceKindRequirementDoc,
			URI:      doc.URI,
			Title:    doc.Title,
			Required: true,
		}
		if source.ID == "" {
			source.ID = fmt.Sprintf("requirement_doc_%d", len(sources)+1)
		}
		sources = append(sources, source)
	}
	for _, screenshot := range input.WebpageScreenshots {
		source := model.KnowledgeSource{
			ID:       screenshot.ID,
			Kind:     model.EvidenceKindWebScreenshot,
			URI:      screenshot.Artifact.URI,
			Title:    screenshot.Title,
			Required: true,
		}
		if source.ID == "" {
			source.ID = fmt.Sprintf("webpage_screenshot_%d", len(sources)+1)
		}
		sources = append(sources, source)
	}
	return sources
}
