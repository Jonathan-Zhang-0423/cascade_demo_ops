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
	if err := model.ValidatePresentationGenerationIntents(input.PresentationGenerationIntents); err != nil {
		return nil, err
	}
	for _, contract := range input.InteractionContracts {
		if err := model.ValidateInteractionContract(contract); err != nil {
			return nil, fmt.Errorf("invalid interaction contract %q: %w", contract.ContractID, err)
		}
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
	productDescription := RedactSensitiveUserText(input.ProductDescription)
	mustShow := RedactSensitiveUserTexts(input.MustShow)
	mustNotShow := RedactSensitiveUserTexts(input.MustNotShow)
	requirementDocuments := redactRequirementDocuments(input.RequirementDocuments)

	now := time.Now().UTC()
	targetDurationSec := input.TargetDurationSec
	if targetDurationSec <= 0 {
		targetDurationSec = 60
	}
	projectID := strings.TrimSpace(input.ProjectID)
	if projectID == "" {
		projectID = fmt.Sprintf("proj_%d", time.Now().UnixNano())
	}
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
		ProductDescription: productDescription,
		TargetAudience:     input.TargetAudience,
		BrandTone:          input.BrandTone,
		MustShow:           mustShow,
		MustNotShow:        mustNotShow,
		ForbiddenPages:     uniqueStrings(append(append([]string{}, input.ForbiddenPages...), defaultControlPlaneForbiddenPaths()...)),
		ForbiddenData:      input.ForbiddenData,
		Inputs: &model.ProjectInputBundle{
			ProductURLs:                   productURLInputs(input),
			Code:                          codeInputs(input),
			Repositories:                  repositoryInputs(input),
			RequirementDocuments:          requirementDocuments,
			WebpageScreenshots:            input.WebpageScreenshots,
			KnowledgeSources:              knowledgeSourcesFromInputs(input),
			Requirements:                  structuredDemoRequirements(input.Requirements, mustShow, mustNotShow, input.ForbiddenPages, input.ForbiddenData),
			InteractionContracts:          append([]model.InteractionContract{}, input.InteractionContracts...),
			PresentationGenerationIntents: append([]model.PresentationGenerationIntent{}, input.PresentationGenerationIntents...),
			RawUserPrompt:                 productDescription,
			Scenarios: []model.DemoScenario{
				{
					ID:              "scenario_primary",
					UseCase:         useCase,
					AudienceID:      audience.ID,
					Objective:       productDescription,
					DurationSeconds: targetDurationSec,
					Priority:        1,
					MustShow:        mustShow,
					MustAvoid:       append([]string{}, mustNotShow...),
				},
			},
		},
		Goals: []model.DemoGoal{
			{
				ID:               "goal_primary",
				UseCase:          useCase,
				AudienceID:       audience.ID,
				ValueProposition: productDescription,
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
			AllowedDomains:          append([]string{}, input.AllowedDomains...),
			AllowedCommands:         input.SSHAllowedCommands,
			AllowedPaths:            input.SSHAllowedPaths,
			AuditLogRequired:        true,
		},
		SecurityPolicy: &model.SecurityPolicy{
			ForbiddenPages:       uniqueStrings(append(append([]string{}, input.ForbiddenPages...), defaultControlPlaneForbiddenPaths()...)),
			ForbiddenData:        input.ForbiddenData,
			PIIHandling:          "mask_in_artifacts",
			NetworkCapturePolicy: "metadata_only_by_default",
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if input.SourceBindingDecision != "" || input.SourceBindingHash != "" {
		project.SourceBinding = &model.ProductSourceBindingAssessment{
			Decision: input.SourceBindingDecision, AssessmentHash: input.SourceBindingHash,
		}
	}
	if input.DemoUsername != "" || input.DemoPassword != "" || input.DemoCredentialRef != "" {
		credentialRef := strings.TrimSpace(input.DemoCredentialRef)
		if credentialRef == "" {
			credentialRef = "local-dev/demo_account"
		}
		project.DemoAccount = &model.DemoAccount{
			UsernameSecretRef: credentialRef,
			PasswordSecretRef: credentialRef,
			Provider:          "demo_account",
			Scope:             "browser_login",
		}
		project.Inputs.Credentials = append(project.Inputs.Credentials, model.CredentialInput{
			ID:            "demo_account",
			Kind:          "username_password",
			SecretRef:     credentialRef,
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

func structuredDemoRequirements(existing []model.DemoRequirement, mustShow, mustNotShow, forbiddenPages, forbiddenData []string) []model.DemoRequirement {
	out := make([]model.DemoRequirement, 0, len(existing)+len(mustShow)+len(mustNotShow)+len(forbiddenPages)+len(forbiddenData))
	seen := map[string]bool{}
	add := func(value model.DemoRequirement) {
		value.Kind = strings.TrimSpace(value.Kind)
		value.Description = RedactSensitiveUserText(value.Description)
		if value.Kind == "" || value.Description == "" {
			return
		}
		key := value.Kind + "\x00" + normalizeIntentText(value.Description)
		if seen[key] {
			return
		}
		seen[key] = true
		if strings.TrimSpace(value.ID) == "" {
			digest := strings.TrimPrefix(model.SHA256Hex([]byte(key)), "sha256:")
			value.ID = "requirement_" + value.Kind + "_" + digest[:12]
		}
		value.Required = value.Kind == "must_show"
		if len(value.EvidenceRefs) == 0 {
			value.EvidenceRefs = []model.EvidenceRef{{
				ID:         "ev_" + value.ID,
				Kind:       model.EvidenceKindUserInput,
				Summary:    "用户明确提交的结构化演示要求",
				FieldPath:  "project_input_bundle.requirements." + value.ID,
				Confidence: 1,
			}}
		}
		out = append(out, value)
	}
	for _, value := range existing {
		add(value)
	}
	for _, item := range mustShow {
		add(model.DemoRequirement{Kind: "must_show", Description: item, Required: true})
	}
	for _, item := range mustNotShow {
		add(model.DemoRequirement{Kind: "must_not_show", Description: item})
	}
	for _, item := range forbiddenPages {
		add(model.DemoRequirement{Kind: "forbidden_page", Description: item})
	}
	for _, item := range forbiddenData {
		add(model.DemoRequirement{Kind: "forbidden_data", Description: item})
	}
	return out
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
			Provider: repositoryProviderForURL(input.GitRepoURL),
			ReadOnly: true,
			Primary:  false,
		})
	}
	if input.LocalRepoPath != "" {
		repositories = append(repositories, model.RepositoryInput{
			LocalPath: input.LocalRepoPath,
			Provider:  "local",
			ReadOnly:  true,
			Primary:   false,
		})
	}
	if len(repositories) == 1 {
		repositories[0].Primary = true
	}
	return repositories
}

func repositoryProviderForURL(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if strings.Contains(normalized, "github.com") {
		return "github"
	}
	if strings.HasPrefix(normalized, "git@github.com:") {
		return "github"
	}
	return "git"
}

func redactRequirementDocuments(documents []model.RequirementDocumentInput) []model.RequirementDocumentInput {
	if len(documents) == 0 {
		return documents
	}
	out := make([]model.RequirementDocumentInput, 0, len(documents))
	for _, document := range documents {
		document.Title = RedactSensitiveUserText(document.Title)
		document.Body = RedactSensitiveUserText(document.Body)
		out = append(out, document)
	}
	return out
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
			Title:    RedactSensitiveUserText(doc.Title),
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
