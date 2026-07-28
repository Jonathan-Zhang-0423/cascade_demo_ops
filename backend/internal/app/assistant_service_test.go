package app

import (
	"context"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/store"
)

func newAssistantTestService(t *testing.T) *Service {
	t.Helper()
	runtime := config.AppRuntimeConfig{DataRoot: t.TempDir(), LLMMode: config.LLMModeDeterministic}
	service, err := NewService(runtime, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	service.assistantStore = store.NewMemoryAssistantStore()
	return service
}

func TestAssistantConfigurationPatchRequiresConfirmation(t *testing.T) {
	service := newAssistantTestService(t)
	ctx := context.Background()
	session, err := service.CreateAssistantSession(ctx, model.AssistantContext{Surface: model.AssistantSurfaceProjects, ScopeKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := service.SubmitAssistantTurn(ctx, session.ID, model.AssistantTurnRequest{Message: "产品是 https://example.com", IdempotencyKey: "turn-1"})
	if err != nil {
		t.Fatal(err)
	}
	if turn.Configuration.ProductURL != "" {
		t.Fatal("pending patch must not mutate configuration")
	}
	proposal := findAssistantProposal(turn, turn.Messages[len(turn.Messages)-1].Proposals[0].ID)
	if proposal == nil {
		t.Fatal("configuration proposal missing")
	}
	confirmed, err := service.ConfirmAssistantProposal(ctx, turn.ID, proposal.ID, model.AssistantProposalDecisionRequest{BaseVersion: proposal.BaseVersion, IdempotencyKey: "confirm-1"})
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Configuration.ProductURL != "https://example.com" || confirmed.Configuration.Version != 2 {
		t.Fatalf("unexpected configuration: %+v", confirmed.Configuration)
	}
	again, err := service.ConfirmAssistantProposal(ctx, turn.ID, proposal.ID, model.AssistantProposalDecisionRequest{BaseVersion: proposal.BaseVersion, IdempotencyKey: "confirm-1"})
	if err != nil || again.Configuration.Version != 2 {
		t.Fatalf("idempotent confirmation changed version: %+v %v", again.Configuration, err)
	}
}

func TestAssistantProposalRejectsStaleVersion(t *testing.T) {
	service := newAssistantTestService(t)
	ctx := context.Background()
	session, _ := service.CreateAssistantSession(ctx, model.AssistantContext{Surface: model.AssistantSurfaceProjects, ScopeKey: "stale"})
	turn, _ := service.SubmitAssistantTurn(ctx, session.ID, model.AssistantTurnRequest{Message: "https://example.com"})
	proposal := &turn.Messages[len(turn.Messages)-1].Proposals[0]
	proposal.BaseVersion = 0
	turn.Configuration.Version = 2
	if err := service.assistantStore.Save(ctx, turn); err != nil {
		t.Fatal(err)
	}
	_, err := service.ConfirmAssistantProposal(ctx, turn.ID, proposal.ID, model.AssistantProposalDecisionRequest{BaseVersion: 2})
	if err == nil || !strings.Contains(err.Error(), "version conflict") {
		t.Fatalf("expected version conflict, got %v", err)
	}
}

func TestAssistantRedactsSecretsAndAbsolutePaths(t *testing.T) {
	service := newAssistantTestService(t)
	ctx := context.Background()
	session, _ := service.CreateAssistantSession(ctx, model.AssistantContext{Surface: model.AssistantSurfaceProjects, ScopeKey: "redact"})
	turn, err := service.SubmitAssistantTurn(ctx, session.ID, model.AssistantTurnRequest{Message: `password=super-secret C:\\Users\\Alice\\private-project`})
	if err != nil {
		t.Fatal(err)
	}
	data := turn.Messages[len(turn.Messages)-2].Text
	if strings.Contains(data, "super-secret") || strings.Contains(data, `C:\\Users`) {
		t.Fatalf("sensitive input persisted: %q", data)
	}
}

func TestConfigurationConfirmationDoesNotApproveUpload(t *testing.T) {
	draft := newConfigurationDraft()
	name, productURL, objective, audience := "Demo", "https://example.com", "Show the primary workflow", "buyers"
	sources := []model.ConfigurationSourceRef{{Ref: "source-repo", Kind: "github_repository", Label: "repo", URL: "https://github.com/example/repo"}}
	next, err := applyConfigurationPatch(draft, model.ProjectConfigurationPatch{ProjectName: &name, ProductURL: &productURL, Objective: &objective, TargetAudience: &audience, Sources: &sources})
	if err != nil {
		t.Fatal(err)
	}
	if next.Readiness != "ready" {
		t.Fatalf("draft not ready: %v", next.MissingFields)
	}
	// Upload authorization is intentionally absent from configuration state.
	encoded := strings.ToLower(next.Hash + next.Readiness)
	if strings.Contains(encoded, "upload") || strings.Contains(encoded, "approve") {
		t.Fatal("configuration leaked upload approval state")
	}
}

func TestAssistantConfigurationCarriesAllowedDomainsIntoLocalAnalysis(t *testing.T) {
	service := newAssistantTestService(t)
	draft := newConfigurationDraft()
	draft.AllowedDomains = []string{"app.example.com", "assets.example.com"}

	input, err := service.userInputFromConfiguration(draft)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(input.AllowedDomains, ",") != "app.example.com,assets.example.com" {
		t.Fatalf("allowed domains were not preserved: %v", input.AllowedDomains)
	}
}

func TestSafeSelectionsRejectUnknownLocalSourceRef(t *testing.T) {
	service := newAssistantTestService(t)
	_, err := service.patchForSafeSelections(newConfigurationDraft(), []model.ConfigurationSourceRef{{Ref: "source_forged", Kind: "local_repository", Label: "forged"}}, nil)
	if err == nil {
		t.Fatal("forged local source ref should be rejected")
	}
}

func TestSafeSelectionsAcceptGitHubHTTPSOnly(t *testing.T) {
	service := newAssistantTestService(t)
	valid := []model.ConfigurationSourceRef{{Ref: "github_public", Kind: "github_repository", Label: "repo", URL: "https://github.com/example/repo"}}
	patch, err := service.patchForSafeSelections(newConfigurationDraft(), valid, nil)
	if err != nil || patch.Sources == nil || len(*patch.Sources) != 1 {
		t.Fatalf("valid GitHub source rejected: %v", err)
	}
	invalid := []model.ConfigurationSourceRef{{Ref: "github_bad", Kind: "github_repository", Label: "repo", URL: "http://evil.example/repo"}}
	if _, err := service.patchForSafeSelections(newConfigurationDraft(), invalid, nil); err == nil {
		t.Fatal("non-GitHub source should be rejected")
	}
}
