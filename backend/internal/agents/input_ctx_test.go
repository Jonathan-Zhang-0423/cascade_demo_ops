package agents

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

func TestInputContextPreservesValidatedPresentationGenerationIntent(t *testing.T) {
	intent, err := model.PresentationGenerationIntentDefaults("presentation_intro_001", "intro", []string{"stage_1_screenshot"})
	if err != nil {
		t.Fatal(err)
	}
	project, err := NewInputContextAgent().BuildProjectContext(t.Context(), orchestrator.UserInput{
		Mode: model.AppModeDesktop, ProductURL: "https://product.example", TargetAudience: "普通用户",
		PresentationGenerationIntents: []model.PresentationGenerationIntent{intent},
	})
	if err != nil {
		t.Fatal(err)
	}
	if project.Inputs == nil || len(project.Inputs.PresentationGenerationIntents) != 1 || project.Inputs.PresentationGenerationIntents[0].Capability != model.PresentationVideoCandidateCapability {
		t.Fatalf("presentation intent was not preserved: %+v", project.Inputs)
	}
	invalid := intent
	invalid.Purpose = "result_proof"
	if _, err := NewInputContextAgent().BuildProjectContext(t.Context(), orchestrator.UserInput{Mode: model.AppModeDesktop, ProductURL: "https://product.example", TargetAudience: "普通用户", PresentationGenerationIntents: []model.PresentationGenerationIntent{invalid}}); err == nil {
		t.Fatal("business-proof generation intent should be rejected")
	}
}

func TestInputContextRedactsCredentialsEmbeddedInRequirements(t *testing.T) {
	input := orchestrator.UserInput{
		Mode:               model.AppModeDesktop,
		ProductURL:         "https://cascadeai.cn",
		ProductDescription: "演示登录，账号demo.user@example.test密码fixture-secret，然后新建项目",
		RequirementDocuments: []model.RequirementDocumentInput{{
			ID:    "req_credentials",
			Kind:  "text",
			Title: "账号demo.user@example.test",
			Body:  "password: fixture-secret; email: demo.user@example.test",
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
	for _, forbidden := range []string{"fixture-secret", "demo.user@example.test"} {
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

func TestInputContextPersistsOnlyOpaqueDemoCredentialRef(t *testing.T) {
	input := orchestrator.UserInput{
		Mode: model.AppModeDesktop, ProductURL: "https://product.example/login", ProductDescription: "登录并新建项目",
		TargetAudience: "普通用户", DemoUsername: "demo@example.test", DemoPassword: "fixture-password",
		DemoCredentialRef: "credential://demo/e2e-fixture-ref",
	}

	project, err := NewInputContextAgent().BuildProjectContext(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if project.DemoAccount == nil || project.DemoAccount.UsernameSecretRef != input.DemoCredentialRef || project.DemoAccount.PasswordSecretRef != input.DemoCredentialRef {
		t.Fatalf("project did not retain the opaque vault ref: %+v", project.DemoAccount)
	}
	if len(project.Inputs.Credentials) != 1 || project.Inputs.Credentials[0].SecretRef != input.DemoCredentialRef {
		t.Fatalf("input bundle did not retain the opaque vault ref: %+v", project.Inputs.Credentials)
	}
	payload, err := json.Marshal(project)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), input.DemoUsername) || strings.Contains(string(payload), input.DemoPassword) {
		t.Fatalf("project context persisted plaintext credentials: %s", payload)
	}
}

func TestInputContextBuildsStableStructuredRequirements(t *testing.T) {
	input := orchestrator.UserInput{
		Mode: model.AppModeDesktop, ProductURL: "https://app.example.com", ProductDescription: "演示新建项目",
		TargetAudience: "普通用户", MustShow: []string{"新建项目成功"}, MustNotShow: []string{"管理员页面"},
		ForbiddenPages: []string{"/billing"}, ForbiddenData: []string{"api key"},
	}
	first, err := NewInputContextAgent().BuildProjectContext(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewInputContextAgent().BuildProjectContext(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Inputs.Requirements) < 4 || len(second.Inputs.Requirements) != len(first.Inputs.Requirements) {
		t.Fatalf("structured requirements missing: %+v", first.Inputs.Requirements)
	}
	for index, requirement := range first.Inputs.Requirements {
		if requirement.ID == "" || requirement.ID != second.Inputs.Requirements[index].ID {
			t.Fatalf("requirement ID must be stable: first=%+v second=%+v", first.Inputs.Requirements, second.Inputs.Requirements)
		}
		if requirement.Required != (requirement.Kind == "must_show") {
			t.Fatalf("only must_show is a positive coverage requirement: %+v", requirement)
		}
	}
}
