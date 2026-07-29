package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
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

func TestAssistantContinueWithWebpageEvidenceRequiresConfirmation(t *testing.T) {
	service := newAssistantTestService(t)
	ctx := context.Background()
	projectID := "assistant-source-binding"
	repoPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoPath, "package.json"), []byte(`{"name":"beta","homepage":"https://beta.example"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	state, createErr := service.CreateProject(ctx, orchestrator.UserInput{
		ProjectID: projectID, Mode: model.AppModeDesktop,
		ProductURL: "https://alpha.example", LocalRepoPath: repoPath,
		ProductDescription: "展示工作台", TargetAudience: "产品团队",
		WebpageScreenshots: []model.WebpageScreenshotInput{verifiedActionScreenshotInputForURL("https://alpha.example")},
	})
	var mismatch *model.ProductSourceMismatchError
	if !errors.As(createErr, &mismatch) || state == nil || state.SourceBinding == nil {
		t.Fatalf("expected a persisted mismatch fixture, state=%+v err=%v", state, createErr)
	}
	session, err := service.CreateAssistantSession(ctx, model.AssistantContext{Surface: model.AssistantSurfaceProjects, ScopeKey: projectID, ProjectID: projectID})
	if err != nil {
		t.Fatal(err)
	}
	proposal := model.AssistantProposal{
		ID: "proposal-page-only", Kind: model.AssistantProposalContinueWithWebpageEvidence,
		Title: "仅使用网页证据继续", BaseVersion: session.Configuration.Version,
		IdempotencyKey: "assistant-page-only", RequiresConfirmation: true, Status: "available",
	}
	session.Messages = append(session.Messages, model.AssistantMessage{ID: "message-page-only", Role: "agent", Kind: "proposal", Proposals: []model.AssistantProposal{proposal}})
	if err := service.assistantStore.Save(ctx, session); err != nil {
		t.Fatal(err)
	}
	before, err := service.GetSourceBinding(ctx, projectID)
	if err != nil || before.EffectiveMode != model.ProductSourceModeBlocked {
		t.Fatalf("proposal creation must not execute page-only decision: %+v err=%v", before, err)
	}
	confirmed, err := service.ConfirmAssistantProposal(ctx, session.ID, proposal.ID, model.AssistantProposalDecisionRequest{BaseVersion: proposal.BaseVersion, IdempotencyKey: "confirm-page-only"})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.GetSourceBinding(ctx, projectID)
	if err != nil || updated.EffectiveMode != model.ProductSourceModePageOnly || updated.Decision != "continue_page_only" {
		t.Fatalf("confirmed proposal did not execute restricted action: %+v err=%v", updated, err)
	}
	got := findAssistantProposal(confirmed, proposal.ID)
	if got == nil || got.Status != "confirmed" || got.ExecutionResult["sourceMode"] != "page_only" {
		t.Fatalf("assistant proposal audit result missing: %+v", got)
	}
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

func TestAssistantExtractsExplicitChineseConfigurationWithoutSideEffects(t *testing.T) {
	service := newAssistantTestService(t)
	ctx := context.Background()
	session, err := service.CreateAssistantSession(ctx, model.AssistantContext{Surface: model.AssistantSurfaceProjects, ScopeKey: "chinese-config"})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := service.SubmitAssistantTurn(ctx, session.ID, model.AssistantTurnRequest{Message: "为 https://example.com 制作一个面向产品团队的 60 秒演示，项目名叫 Example Demo，重点展示审批流程"})
	if err != nil {
		t.Fatal(err)
	}
	if turn.Configuration.ProductURL != "" || turn.Configuration.AnalysisProjectID != "" {
		t.Fatalf("pending proposal mutated or analyzed configuration: %+v", turn.Configuration)
	}
	proposal := &turn.Messages[len(turn.Messages)-1].Proposals[0]
	if proposal.Patch == nil || proposal.Patch.ProjectName == nil || *proposal.Patch.ProjectName != "Example Demo" {
		t.Fatalf("project name was not extracted: %+v", proposal.Patch)
	}
	if proposal.Patch.ProductURL == nil || *proposal.Patch.ProductURL != "https://example.com" || proposal.Patch.TargetAudience == nil || *proposal.Patch.TargetAudience != "产品团队" {
		t.Fatalf("URL or audience was not extracted: %+v", proposal.Patch)
	}
	if proposal.Patch.TargetDurationSec == nil || *proposal.Patch.TargetDurationSec != 60 || proposal.Patch.MustShow == nil || len(*proposal.Patch.MustShow) != 1 {
		t.Fatalf("duration or must-show was not extracted: %+v", proposal.Patch)
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
