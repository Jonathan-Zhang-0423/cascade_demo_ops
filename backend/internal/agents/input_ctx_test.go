package agents

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

func TestInputContextRedactsCredentialsEmbeddedInRequirements(t *testing.T) {
	input := orchestrator.UserInput{
		Mode:               model.AppModeDesktop,
		ProductURL:         "https://cascadeai.cn",
		ProductDescription: "演示登录，账号yikai.xu@cascadeai.co密码000000，然后新建项目",
		RequirementDocuments: []model.RequirementDocumentInput{{
			ID:    "req_credentials",
			Kind:  "text",
			Title: "账号yikai.xu@cascadeai.co",
			Body:  "password: 000000; email: yikai.xu@cascadeai.co",
		}},
		TargetAudience: "运营",
	}

	project, err := NewInputContextAgent().BuildProjectContext(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(project)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, forbidden := range []string{"000000", "yikai.xu@cascadeai.co"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("project context leaked credential %q: %s", forbidden, text)
		}
	}
	for _, expected := range []string{"secret_ref:local-dev/demo_username", "secret_ref:local-dev/demo_password"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected redacted credential ref %q in project context: %s", expected, text)
		}
	}
}

func TestInputContextKeepsGitHubAndLocalReposAsParallelSources(t *testing.T) {
	input := orchestrator.UserInput{
		Mode:               model.AppModeDesktop,
		ProductURL:         "https://app.example.com",
		GitRepoURL:         "https://github.com/acme/demo-app",
		LocalRepoPath:      "C:\\Users\\demo\\project",
		ProductDescription: "演示新建项目",
		TargetAudience:     "运营",
	}

	project, err := NewInputContextAgent().BuildProjectContext(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if project.GitRepoURL != input.GitRepoURL || project.LocalRepoPath != input.LocalRepoPath {
		t.Fatalf("project context lost parallel repo inputs: %+v", project)
	}
	if len(project.Inputs.Repositories) != 2 || len(project.Inputs.Code) != 2 {
		t.Fatalf("expected GitHub and local code sources, got repositories=%+v code=%+v", project.Inputs.Repositories, project.Inputs.Code)
	}
	if project.Inputs.Repositories[0].Provider != "github" || project.Inputs.Repositories[0].Primary {
		t.Fatalf("expected GitHub source to be parallel non-primary when local path also exists: %+v", project.Inputs.Repositories[0])
	}
	if project.Inputs.Repositories[1].Provider != "local" || project.Inputs.Repositories[1].Primary {
		t.Fatalf("expected local source to be parallel non-primary when GitHub URL also exists: %+v", project.Inputs.Repositories[1])
	}
}
