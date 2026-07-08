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
	if strings.TrimSpace(input.ProductURL) == "" {
		return nil, errors.New("product URL is required")
	}
	if input.Mode == model.AppModeWeb && strings.TrimSpace(input.GitRepoURL) == "" {
		return nil, errors.New("git repo URL is required in web mode")
	}
	if input.Mode == model.AppModeDesktop && strings.TrimSpace(input.LocalRepoPath) == "" {
		return nil, errors.New("local repo path is required in desktop mode")
	}
	if strings.TrimSpace(input.TargetAudience) == "" {
		input.TargetAudience = "seed investor"
	}

	project := &model.ProjectContext{
		ID:                 fmt.Sprintf("proj_%d", time.Now().UnixNano()),
		Mode:               input.Mode,
		ProductURL:         input.ProductURL,
		GitRepoURL:         input.GitRepoURL,
		LocalRepoPath:      input.LocalRepoPath,
		ProductDescription: input.ProductDescription,
		TargetAudience:     input.TargetAudience,
		MustShow:           input.MustShow,
		MustNotShow:        input.MustNotShow,
		ForbiddenPages:     input.ForbiddenPages,
		ForbiddenData:      input.ForbiddenData,
	}
	if input.DemoUsername != "" || input.DemoPassword != "" {
		project.DemoAccount = &model.DemoAccount{
			UsernameSecretRef: "local-dev/demo_username",
			PasswordSecretRef: "local-dev/demo_password",
		}
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
