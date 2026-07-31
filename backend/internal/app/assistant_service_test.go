package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestAssistantBuildsLocalDraftFromRealSourceAndBrowserScan(t *testing.T) {
	testPage := httptest.NewServer(http.HandlerFunc(controlledBusinessFixtureHandler))
	defer testPage.Close()

	repoPath := t.TempDir()
	packageJSON := map[string]any{
		"name":     "controlled-builder",
		"homepage": testPage.URL,
		"scripts":  map[string]string{"start": "vite"},
	}
	encodedPackage, err := json.Marshal(packageJSON)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "package.json"), encodedPackage, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "App.tsx"), []byte(`export function App() { return <button data-testid="start-build">Start build</button> }`), 0o600); err != nil {
		t.Fatal(err)
	}

	dataRoot := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	runtime := config.AppRuntimeConfig{
		DataRoot: dataRoot, CacheRoot: filepath.Join(dataRoot, "cache"),
		LLMMode: config.LLMModeDeterministic, DevRepoRoot: repoRoot,
	}
	service, err := NewService(runtime, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	service.assistantStore = store.NewMemoryAssistantStore()
	if !fileExists(service.localVideoWorkerPath()) || !commandReady(service.nodeBinaryForExecution()) {
		t.Skip("real local video-worker runtime is unavailable")
	}
	sourceRef, err := service.RegisterLocalSource("local_repository", repoPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	session, err := service.CreateAssistantSession(ctx, model.AssistantContext{Surface: model.AssistantSurfaceProjects, ScopeKey: "real-local-draft"})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := service.SubmitAssistantTurn(ctx, session.ID, model.AssistantTurnRequest{
		Message:        "为 " + testPage.URL + " 制作一个面向产品团队的 60 秒演示，项目名叫 Controlled Builder，重点展示创建项目并启动构建",
		IdempotencyKey: "real-local-turn",
	})
	if err != nil {
		t.Fatal(err)
	}
	patchProposal := findAssistantProposalByKind(turn, model.AssistantProposalConfigurationPatch)
	if patchProposal == nil {
		t.Fatalf("configuration patch proposal is missing: %+v", turn.NextAction)
	}
	configured, err := service.ConfirmAssistantProposal(ctx, turn.ID, patchProposal.ID, model.AssistantProposalDecisionRequest{
		BaseVersion: patchProposal.BaseVersion, IdempotencyKey: "real-local-patch-confirm",
	})
	if err != nil {
		t.Fatal(err)
	}
	sourceProposal := findAssistantProposalByKind(configured, model.AssistantProposalSelectProjectSource)
	if sourceProposal == nil {
		t.Fatalf("project source proposal is missing: %+v", configured.NextAction)
	}
	configured, err = service.CompleteAssistantClientAction(ctx, configured.ID, sourceProposal.ID, model.AssistantClientActionResultRequest{
		BaseVersion: sourceProposal.BaseVersion, IdempotencyKey: "real-local-source-confirm",
		SelectedSources: []model.ConfigurationSourceRef{{Ref: sourceRef.Ref, Kind: sourceRef.Kind, Label: sourceRef.Label}},
	})
	if err != nil {
		t.Fatal(err)
	}
	confirmProposal := findAssistantProposalByKind(configured, model.AssistantProposalConfirmConfiguration)
	if confirmProposal == nil {
		t.Fatalf("configuration confirmation proposal is missing: %+v", configured.NextAction)
	}
	confirmed, err := service.ConfirmAssistantProposal(ctx, configured.ID, confirmProposal.ID, model.AssistantProposalDecisionRequest{
		BaseVersion: confirmProposal.BaseVersion, IdempotencyKey: "real-local-analysis-confirm",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !confirmed.Configuration.Confirmed || confirmed.Configuration.AnalysisProjectID == "" || confirmed.ActiveWorkstation != model.AssistantWorkstationApproval {
		t.Fatalf("local analysis did not reach package approval: config=%+v workstation=%s", confirmed.Configuration, confirmed.ActiveWorkstation)
	}
	if confirmed.NextAction.Kind != "review_local_draft" || confirmed.NextAction.RequiresUserAction {
		t.Fatalf("analysis completion left a stale confirmation action: %+v", confirmed.NextAction)
	}
	latest := confirmed.Messages[len(confirmed.Messages)-1]
	if latest.Kind != "status" || !strings.Contains(latest.Text, "三合一执行包草稿已生成") {
		t.Fatalf("analysis completion status message is missing: %+v", latest)
	}
	for _, message := range confirmed.Messages {
		for _, candidate := range message.Proposals {
			if candidate.Status == "available" && (candidate.Kind == model.AssistantProposalConfirmConfiguration || candidate.Kind == model.AssistantProposalStartLocalAnalysis) {
				t.Fatalf("analysis completion retained a stale confirmation proposal: %+v", candidate)
			}
		}
	}

	state, err := service.LoadProject(ctx, confirmed.Configuration.AnalysisProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if state.SourceBinding == nil || state.SourceBinding.Status != model.ProductSourceBindingMatched || state.SourceBinding.EffectiveMode != model.ProductSourceModeMixed {
		t.Fatalf("source and webpage were not matched: %+v", state.SourceBinding)
	}
	if state.VerifiedInteractionPlan == nil || state.VerifiedInteractionPlan.BrowserScanID == "" || state.VerifiedInteractionPlan.BusinessActionCount == 0 {
		t.Fatalf("real browser scan did not verify a business action: plan=%+v missing=%+v", state.VerifiedInteractionPlan, state.MissingEvidenceReport)
	}
	if state.ExecutableScriptBundle == nil || len(state.ExecutableScriptBundle.PlanJSON.Steps) == 0 || len(state.ExecutableScriptBundle.StageApprovalPlan.Stages) == 0 || len(state.ExecutableScriptBundle.ScriptOutline.Stages) == 0 {
		t.Fatalf("three-in-one execution draft is incomplete: %+v", state.ExecutableScriptBundle)
	}

	build, err := service.BuildClientExecutionPackage(ctx, state.ProjectID, defaultDesktopOrgID)
	if err != nil {
		t.Fatal(err)
	}
	if build.BuildStatus != "draft" || !build.Package.ApprovedAt.IsZero() || build.Package.SafetyReport.AllowedToUpload || build.Package.SafetyReport.HumanApproval.ApprovalID != "" || build.Envelope.EnvelopeID != "" || build.PayloadRef.Kind != "" {
		t.Fatalf("configuration confirmation incorrectly approved or uploaded the package: %+v", build)
	}

	safeSession, err := json.Marshal(confirmed)
	if err != nil {
		t.Fatal(err)
	}
	safeBuild, err := json.Marshal(build)
	if err != nil {
		t.Fatal(err)
	}
	for label, payload := range map[string][]byte{"assistant session": safeSession, "execution package": safeBuild} {
		serialized := string(payload)
		if strings.Contains(serialized, repoPath) || strings.Contains(serialized, `export function App`) {
			t.Fatalf("%s leaked a local path or source content", label)
		}
	}
}

func findAssistantProposalByKind(session *model.AssistantSession, kind model.AssistantProposalKind) *model.AssistantProposal {
	if session == nil {
		return nil
	}
	for messageIndex := len(session.Messages) - 1; messageIndex >= 0; messageIndex-- {
		for proposalIndex := range session.Messages[messageIndex].Proposals {
			proposal := &session.Messages[messageIndex].Proposals[proposalIndex]
			if proposal.Kind == kind && proposal.Status == "available" {
				return proposal
			}
		}
	}
	return nil
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

func TestAssistantUsesRealLLMToProposeConfiguration(t *testing.T) {
	var requestSeen bool
	var projectedConfigurationSeen bool
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestSeen = true
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer test-assistant-key" {
			t.Fatalf("unexpected model request: path=%s auth=%s", r.URL.Path, r.Header.Get("Authorization"))
		}
		var payload struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		for _, message := range payload.Messages {
			if strings.Contains(message.Content, "credential://") || strings.Contains(message.Content, "source-secret-ref") || strings.Contains(message.Content, "github.com/private") || strings.Contains(message.Content, "private-repo") {
				t.Fatalf("assistant LLM payload leaked an opaque or private source ref: %s", message.Content)
			}
			if strings.Contains(message.Content, `"connected":true`) && strings.Contains(message.Content, `"label":"source_1"`) {
				projectedConfigurationSeen = true
			}
			if strings.Contains(message.Content, `"ref":""`) {
				t.Fatalf("assistant LLM payload represented a connected source as an empty ref: %s", message.Content)
			}
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"reply\":\"我理解了这支演示的受众和重点。\",\"hasPatch\":true,\"patch\":{\"projectName\":\"智能配置演示\",\"productURL\":\"https://product.example\",\"objective\":\"让运营负责人理解审批效率\",\"targetAudience\":\"运营负责人\",\"targetDurationSec\":75,\"mustShow\":[\"审批时间线\"],\"mustNotShow\":[\"内部调试信息\"]},\"missingFields\":[\"sources\"],\"suggestedActions\":[]}"}}],"usage":{"prompt_tokens":80,"completion_tokens":35}}`))
	}))
	defer modelServer.Close()

	runtime := config.AppRuntimeConfig{
		DataRoot: t.TempDir(), LLMMode: config.LLMModeReal, ModelAdapterVersion: config.ModelAdapterVersion,
		ModelProviders: map[config.ModelProvider]config.ModelProviderCredential{
			config.ModelProviderKimi: {Provider: config.ModelProviderKimi, APIKey: "test-assistant-key", BaseURL: modelServer.URL, DefaultModel: "kimi-assistant-test", Enabled: true},
		},
		ModelTaskRoutes: map[config.ModelTask]config.ModelTaskRoute{
			config.ModelTaskPlanning: {Task: config.ModelTaskPlanning, Provider: config.ModelProviderKimi, Model: "kimi-assistant-test"},
		},
	}
	service, err := NewService(runtime, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	service.assistantStore = store.NewMemoryAssistantStore()
	session, err := service.CreateAssistantSession(t.Context(), model.AssistantContext{Surface: model.AssistantSurfaceProjects, ScopeKey: "real-llm"})
	if err != nil {
		t.Fatal(err)
	}
	session.Configuration.Sources = []model.ConfigurationSourceRef{{Ref: "source-secret-ref", Kind: "github_repository", Label: "private-repo", URL: "https://github.com/private/repository"}}
	session.Configuration.CredentialRefs = []string{"credential://demo/secret-ref"}
	refreshConfigurationMetadata(&session.Configuration)
	if err := service.assistantStore.Save(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	turn, err := service.SubmitAssistantTurn(t.Context(), session.ID, model.AssistantTurnRequest{Message: "做一支给运营负责人看的演示，重点是让他们看到审批能变快", IdempotencyKey: "real-llm-turn"})
	if err != nil {
		t.Fatal(err)
	}
	if !requestSeen {
		t.Fatal("assistant did not call the configured real LLM")
	}
	if !projectedConfigurationSeen {
		t.Fatal("assistant did not tell the model that the redacted source is already connected")
	}
	last := turn.Messages[len(turn.Messages)-1]
	if last.GenerationSource != "llm" || last.ModelProvider != "kimi" || last.ModelName != "kimi-assistant-test" || last.FallbackReason != "" {
		t.Fatalf("real LLM provenance is missing: %+v", last)
	}
	proposal := findAssistantProposalByKind(turn, model.AssistantProposalConfigurationPatch)
	if proposal == nil || proposal.Patch == nil || proposal.Patch.ProjectName == nil || *proposal.Patch.ProjectName != "智能配置演示" || proposal.Patch.MustNotShow == nil {
		t.Fatalf("real LLM configuration patch is incomplete: %+v", proposal)
	}
	if turn.Configuration.ProjectName != "" {
		t.Fatal("real LLM proposal mutated configuration before confirmation")
	}
}

func TestAssistantFiltersModelActionsAgainstConfigurationState(t *testing.T) {
	service := newAssistantTestService(t)
	session, err := service.CreateAssistantSession(t.Context(), model.AssistantContext{Surface: model.AssistantSurfaceProjects, ScopeKey: "model-action-gate"})
	if err != nil {
		t.Fatal(err)
	}
	response := assistantModelResponse{SuggestedActions: []assistantSuggestedAction{
		{Kind: model.AssistantProposalStartLocalAnalysis, Title: "立即分析"},
		{Kind: model.AssistantProposalContinueWithWebpageEvidence, Title: "仅网页继续"},
		{Kind: model.AssistantProposalSelectLocalProject, Title: "选择本地目录"},
	}}
	workflow := service.assistantWorkflowProjection(t.Context(), *session)
	proposals := service.proposalsFromModelResponse(session, workflow, response, time.Now().UTC())
	if len(proposals) != 1 || proposals[0].Kind != model.AssistantProposalSelectLocalProject {
		t.Fatalf("model exposed actions that are invalid for an incomplete configuration: %+v", proposals)
	}

	session.Configuration.ProjectName = "Ready"
	session.Configuration.ProductURL = "https://product.example"
	session.Configuration.Objective = "展示完整流程"
	session.Configuration.TargetAudience = "产品团队"
	session.Configuration.Sources = []model.ConfigurationSourceRef{{Kind: "github_repository", URL: "https://github.com/example/product", Label: "product"}}
	refreshConfigurationMetadata(&session.Configuration)
	workflow = service.assistantWorkflowProjection(t.Context(), *session)
	proposals = service.proposalsFromModelResponse(session, workflow, assistantModelResponse{SuggestedActions: []assistantSuggestedAction{{Kind: model.AssistantProposalStartLocalAnalysis, Title: "开始本地分析"}}}, time.Now().UTC())
	if len(proposals) != 1 || proposals[0].Kind != model.AssistantProposalStartLocalAnalysis {
		t.Fatalf("ready configuration did not allow the bounded analysis action: %+v", proposals)
	}
}

func TestLocalSourceRefsPersistAcrossServiceRestart(t *testing.T) {
	dataRoot := t.TempDir()
	repoPath := t.TempDir()
	runtime := config.AppRuntimeConfig{DataRoot: dataRoot}
	first, err := NewService(runtime, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	registered, err := first.RegisterLocalSource("local_repository", repoPath)
	if err != nil {
		t.Fatal(err)
	}

	restarted, err := NewService(runtime, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	resolved, ok := restarted.ResolveLocalSourceRef(registered.Ref)
	if !ok || resolved.Path != repoPath || resolved.Kind != "local_repository" {
		t.Fatalf("local source ref was not restored after restart: ok=%v ref=%+v", ok, resolved)
	}
}

func TestAssistantWorkflowProjectionUnlocksOnlyReachedWorkstations(t *testing.T) {
	service := newAssistantTestService(t)
	ctx := t.Context()
	projectID := "workflow-projection"
	state := &orchestrator.CascadeState{
		ProjectID: projectID, Status: orchestrator.FlowStatusAwaitingHuman,
		ProjectIntelligence:    &model.ProjectIntelligencePack{},
		ExecutableScriptBundle: &model.ExecutableRecordingScriptBundle{},
	}
	if err := service.states.Save(ctx, state); err != nil {
		t.Fatal(err)
	}
	session, err := service.CreateAssistantSession(ctx, model.AssistantContext{Surface: model.AssistantSurfaceProjects, ScopeKey: "workflow-projection", ProjectID: projectID})
	if err != nil {
		t.Fatal(err)
	}
	workflow := service.assistantWorkflowProjection(ctx, *session)
	if workflow.Stage != "package_approval" || workflow.RecommendedWorkstation != model.AssistantWorkstationApproval {
		t.Fatalf("unexpected workflow projection: %+v", workflow)
	}
	for _, expected := range []model.AssistantWorkstation{model.AssistantWorkstationOverview, model.AssistantWorkstationEvidence, model.AssistantWorkstationPlan, model.AssistantWorkstationApproval} {
		if !assistantWorkstationAvailable(workflow, expected) {
			t.Fatalf("reached workstation %s was not exposed: %+v", expected, workflow)
		}
	}
	if assistantWorkstationAvailable(workflow, model.AssistantWorkstationExecution) || assistantWorkstationAvailable(workflow, model.AssistantWorkstationAssets) {
		t.Fatalf("future workstations leaked into workflow projection: %+v", workflow)
	}
}

func TestAssistantWorkflowNavigationHasDeterministicFallbackAndNoSideEffect(t *testing.T) {
	service := newAssistantTestService(t)
	ctx := t.Context()
	projectID := "workflow-navigation"
	state := &orchestrator.CascadeState{
		ProjectID: projectID, Status: orchestrator.FlowStatusAwaitingHuman,
		ProjectIntelligence:    &model.ProjectIntelligencePack{},
		ExecutableScriptBundle: &model.ExecutableRecordingScriptBundle{},
	}
	if err := service.states.Save(ctx, state); err != nil {
		t.Fatal(err)
	}
	session, err := service.CreateAssistantSession(ctx, model.AssistantContext{Surface: model.AssistantSurfaceProjects, ScopeKey: "workflow-navigation", ProjectID: projectID})
	if err != nil {
		t.Fatal(err)
	}
	workflow := service.assistantWorkflowProjection(ctx, *session)
	actions := deterministicWorkflowActions("打开上传审批工作台", workflow)
	if len(actions) != 1 || actions[0].Kind != model.AssistantProposalOpenWorkstation || actions[0].TargetWorkstation != model.AssistantWorkstationApproval {
		t.Fatalf("deterministic workflow navigation missing: %+v", actions)
	}
	if got := deterministicWorkflowActions("查看服务器执行进度", workflow); len(got) != 0 {
		t.Fatalf("navigation exposed an unreached workstation: %+v", got)
	}
	response := service.generateAssistantResponse(ctx, session.Configuration, workflow, "打开上传审批工作台")
	if response.HasPatch || !configurationPatchEmpty(response.Patch) || len(response.SuggestedActions) != 1 || response.SuggestedActions[0].Kind != model.AssistantProposalOpenWorkstation {
		t.Fatalf("workflow navigation was misclassified as a configuration patch: %+v", response)
	}

	proposal := newAssistantProposal(model.AssistantProposalOpenWorkstation, actions[0].Title, actions[0].Description, session.Configuration.Version, time.Now().UTC(), nil, actions[0].TargetWorkstation)
	session.Messages = append(session.Messages, model.AssistantMessage{ID: "workflow-nav", Role: "agent", Kind: "proposal", Proposals: []model.AssistantProposal{proposal}})
	if err := service.assistantStore.Save(ctx, session); err != nil {
		t.Fatal(err)
	}
	confirmed, err := service.ConfirmAssistantProposal(ctx, session.ID, proposal.ID, model.AssistantProposalDecisionRequest{BaseVersion: proposal.BaseVersion, IdempotencyKey: "workflow-nav-confirm"})
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.ActiveWorkstation != model.AssistantWorkstationApproval || confirmed.Configuration.Version != session.Configuration.Version {
		t.Fatalf("navigation did more than switch workstation: %+v", confirmed)
	}
	reloaded, err := service.LoadProject(ctx, projectID)
	if err != nil || reloaded.Approved || reloaded.DesktopCloudRun != nil {
		t.Fatalf("navigation caused an upload or approval side effect: state=%+v err=%v", reloaded, err)
	}
}

func TestAssistantConfigurationChangeInvalidatesPriorAnalysisContext(t *testing.T) {
	service := newAssistantTestService(t)
	ctx := t.Context()
	projectID := "stale-analysis-context"
	state := &orchestrator.CascadeState{ProjectID: projectID, Status: orchestrator.FlowStatusAwaitingHuman, ExecutableScriptBundle: &model.ExecutableRecordingScriptBundle{}}
	if err := service.states.Save(ctx, state); err != nil {
		t.Fatal(err)
	}
	session, err := service.CreateAssistantSession(ctx, model.AssistantContext{Surface: model.AssistantSurfaceProjects, ScopeKey: "stale-analysis-context", ProjectID: projectID})
	if err != nil {
		t.Fatal(err)
	}
	name := "Changed configuration"
	proposal := newAssistantProposal(model.AssistantProposalConfigurationPatch, "修改配置", "", session.Configuration.Version, time.Now().UTC(), &model.ProjectConfigurationPatch{ProjectName: &name}, model.AssistantWorkstationOverview)
	session.Messages = append(session.Messages, model.AssistantMessage{ID: "change-config", Role: "agent", Kind: "proposal", Proposals: []model.AssistantProposal{proposal}})
	if err := service.assistantStore.Save(ctx, session); err != nil {
		t.Fatal(err)
	}
	updated, err := service.ConfirmAssistantProposal(ctx, session.ID, proposal.ID, model.AssistantProposalDecisionRequest{BaseVersion: proposal.BaseVersion, IdempotencyKey: "change-config-confirm"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Context.ProjectID != "" || updated.Configuration.AnalysisProjectID != "" || updated.Configuration.Confirmed || updated.ActiveWorkstation != model.AssistantWorkstationOverview {
		t.Fatalf("configuration change retained stale analysis authority: context=%+v config=%+v workstation=%s", updated.Context, updated.Configuration, updated.ActiveWorkstation)
	}
	workflow := service.assistantWorkflowProjection(ctx, *updated)
	if workflow.ProjectAttached || workflow.Stage != "configuration" || assistantWorkstationAvailable(workflow, model.AssistantWorkstationApproval) {
		t.Fatalf("stale project remained visible to assistant workflow: %+v", workflow)
	}
}

func TestAssistantNormalizesDuplicatePendingActions(t *testing.T) {
	session := &model.AssistantSession{
		ActiveWorkstation: model.AssistantWorkstationOverview,
		Configuration:     newConfigurationDraft(),
		Messages: []model.AssistantMessage{
			{ID: "older", Proposals: []model.AssistantProposal{{ID: "confirm-old", Kind: model.AssistantProposalConfirmConfiguration, BaseVersion: 1, Status: "available"}}},
			{ID: "newer", Proposals: []model.AssistantProposal{{ID: "confirm-new", Kind: model.AssistantProposalConfirmConfiguration, BaseVersion: 1, Status: "available"}}},
		},
	}
	normalizeAssistantSession(session)
	if got := findAssistantProposal(session, "confirm-old"); got == nil || got.Status != "dismissed" || got.ExecutionResult["duplicate"] != true {
		t.Fatalf("older duplicate proposal remained active: %+v", got)
	}
	if got := findAssistantProposal(session, "confirm-new"); got == nil || got.Status != "available" {
		t.Fatalf("latest proposal was not retained: %+v", got)
	}
}

func TestManualConfigurationUsesSamePendingProposalGate(t *testing.T) {
	service := newAssistantTestService(t)
	session, err := service.CreateAssistantSession(t.Context(), model.AssistantContext{Surface: model.AssistantSurfaceProjects, ScopeKey: "manual-configuration"})
	if err != nil {
		t.Fatal(err)
	}
	name, productURL, objective, audience, brandTone, duration := "手动兜底演示", "https://manual.example", "展示人工配置也能推进", "产品团队", "简洁可信", 45
	mustShow, mustNotShow := []string{"核心审批流程"}, []string{"内部日志"}
	proposed, err := service.ProposeAssistantConfigurationPatch(t.Context(), session.ID, model.AssistantConfigurationProposalRequest{
		BaseVersion: session.Configuration.Version, IdempotencyKey: "manual-proposal-1",
		Patch: model.ProjectConfigurationPatch{ProjectName: &name, ProductURL: &productURL, Objective: &objective, TargetAudience: &audience, TargetDurationSec: &duration, MustShow: &mustShow, MustNotShow: &mustNotShow, BrandTone: &brandTone},
	})
	if err != nil {
		t.Fatal(err)
	}
	if proposed.Configuration.ProjectName != "" || proposed.Configuration.Version != session.Configuration.Version {
		t.Fatalf("manual form bypassed pending proposal gate: %+v", proposed.Configuration)
	}
	proposal := findAssistantProposalByKind(proposed, model.AssistantProposalConfigurationPatch)
	if proposal == nil || proposed.Messages[len(proposed.Messages)-1].GenerationSource != "manual" {
		t.Fatalf("manual proposal audit metadata is missing: %+v", proposed.Messages[len(proposed.Messages)-1])
	}
	confirmed, err := service.ConfirmAssistantProposal(t.Context(), proposed.ID, proposal.ID, model.AssistantProposalDecisionRequest{BaseVersion: proposal.BaseVersion, IdempotencyKey: "manual-confirm-1"})
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Configuration.ProjectName != name || confirmed.Configuration.ProductURL != productURL || confirmed.Configuration.TargetDurationSec != duration || confirmed.Configuration.Version != session.Configuration.Version+1 {
		t.Fatalf("confirmed manual configuration was not applied: %+v", confirmed.Configuration)
	}
	if confirmed.Configuration.Confirmed || confirmed.Configuration.AnalysisProjectID != "" {
		t.Fatal("manual field confirmation must not start analysis")
	}
}

func TestAssistantRestoresPersistedProjectAtAuthoritativeWorkstation(t *testing.T) {
	service := newAssistantTestService(t)
	ctx := context.Background()
	repoPath := t.TempDir()
	projectID := "persisted-assistant-project"
	state := &orchestrator.CascadeState{
		ProjectID: projectID,
		ProjectContext: &model.ProjectContext{
			ID: projectID, Mode: model.AppModeDesktop, Name: "Persisted Demo",
			ProductURL: "https://product.example", ProductDescription: "展示审批流程", TargetAudience: "销售团队",
			LocalRepoPath: repoPath, Inputs: &model.ProjectInputBundle{Repositories: []model.RepositoryInput{{LocalPath: repoPath, Provider: "local", ReadOnly: true}}},
		},
		ExecutableScriptBundle: &model.ExecutableRecordingScriptBundle{},
	}
	if err := service.states.Save(ctx, state); err != nil {
		t.Fatal(err)
	}

	session, err := service.CreateAssistantSession(ctx, model.AssistantContext{Surface: model.AssistantSurfaceProjects, ScopeKey: "persisted-project", ProjectID: projectID})
	if err != nil {
		t.Fatal(err)
	}
	if !session.Configuration.Confirmed || session.Configuration.AnalysisProjectID != projectID {
		t.Fatalf("persisted generated project was treated as an unconfirmed draft: %+v", session.Configuration)
	}
	if session.ActiveWorkstation != model.AssistantWorkstationApproval || session.NextAction.Kind != "review_local_draft" || session.NextAction.RequiresUserAction {
		t.Fatalf("persisted project did not resume at approval: workstation=%s next=%+v", session.ActiveWorkstation, session.NextAction)
	}
	if len(session.Configuration.Sources) != 1 || session.Configuration.Sources[0].Ref == "" || session.Configuration.Sources[0].Label != filepath.Base(repoPath) {
		t.Fatalf("local source was not restored as an opaque reference: %+v", session.Configuration.Sources)
	}
	if session.Configuration.Sources[0].URL != "" || strings.Contains(session.Configuration.Sources[0].Label, repoPath) {
		t.Fatalf("local source path leaked into restored configuration: %+v", session.Configuration.Sources[0])
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

func TestAssistantRoutesExplicitSafeActionsWithoutOverwritingObjective(t *testing.T) {
	service := newAssistantTestService(t)
	ctx := context.Background()
	session, err := service.CreateAssistantSession(ctx, model.AssistantContext{Surface: model.AssistantSurfaceProjects, ScopeKey: "safe-action"})
	if err != nil {
		t.Fatal(err)
	}
	session.Configuration.Objective = "展示审批流程"
	refreshConfigurationMetadata(&session.Configuration)
	if err := service.assistantStore.Save(ctx, session); err != nil {
		t.Fatal(err)
	}
	turn, err := service.SubmitAssistantTurn(ctx, session.ID, model.AssistantTurnRequest{Message: "帮我连接 GitHub 仓库"})
	if err != nil {
		t.Fatal(err)
	}
	if turn.Configuration.Objective != "展示审批流程" {
		t.Fatalf("safe action request overwrote objective: %q", turn.Configuration.Objective)
	}
	proposals := turn.Messages[len(turn.Messages)-1].Proposals
	if len(proposals) != 1 || proposals[0].Kind != model.AssistantProposalConnectGitHub || proposals[0].Patch != nil {
		t.Fatalf("expected one restricted GitHub action, got %+v", proposals)
	}
}

func TestAssistantRoutesDesktopPickerActionsDeterministically(t *testing.T) {
	draft := newConfigurationDraft()
	response := assistantFallbackResponse(draft, "选择本地项目目录，并添加需求文档、品牌素材和测试账号")
	want := map[model.AssistantProposalKind]bool{
		model.AssistantProposalSelectLocalProject:        true,
		model.AssistantProposalAttachRequirementDocument: true,
		model.AssistantProposalAttachBrandAsset:          true,
		model.AssistantProposalStoreDemoCredential:       true,
	}
	for _, action := range response.SuggestedActions {
		delete(want, action.Kind)
	}
	if len(want) != 0 {
		t.Fatalf("missing deterministic safe actions: %v", want)
	}
	if response.HasPatch || response.Patch.Objective != nil {
		t.Fatalf("picker request must not become objective patch: %+v", response.Patch)
	}
}

func TestAssistantSerializesPatchBeforeSourceActions(t *testing.T) {
	service := newAssistantTestService(t)
	ctx := context.Background()
	session, _ := service.CreateAssistantSession(ctx, model.AssistantContext{Surface: model.AssistantSurfaceProjects, ScopeKey: "serialized-actions"})
	turn, err := service.SubmitAssistantTurn(ctx, session.ID, model.AssistantTurnRequest{Message: "为 https://example.com 制作面向产品团队的演示，项目名叫 Serialized，并连接 GitHub"})
	if err != nil {
		t.Fatal(err)
	}
	proposals := turn.Messages[len(turn.Messages)-1].Proposals
	if len(proposals) != 1 || proposals[0].Kind != model.AssistantProposalConfigurationPatch {
		t.Fatalf("patch turn must not expose version-conflicting client actions: %+v", proposals)
	}
	confirmed, err := service.ConfirmAssistantProposal(ctx, turn.ID, proposals[0].ID, model.AssistantProposalDecisionRequest{BaseVersion: proposals[0].BaseVersion, IdempotencyKey: "serialized-confirm"})
	if err != nil {
		t.Fatal(err)
	}
	followUp := confirmed.Messages[len(confirmed.Messages)-1]
	if confirmed.Status != "awaiting_confirmation" || len(followUp.Proposals) != 1 || followUp.Proposals[0].Kind != model.AssistantProposalSelectProjectSource {
		t.Fatalf("source follow-up proposals missing or session status wrong: status=%s proposals=%+v", confirmed.Status, followUp.Proposals)
	}
	for _, proposal := range followUp.Proposals {
		if proposal.BaseVersion != confirmed.Configuration.Version {
			t.Fatalf("follow-up proposal version is stale: %+v", proposal)
		}
	}
}

func TestAssistantCompletesProjectSourceInOneAtomicClientAction(t *testing.T) {
	service := newAssistantTestService(t)
	ctx := context.Background()
	repoPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoPath, "package.json"), []byte(`{"name":"example","homepage":"https://example.com"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ref, err := service.RegisterLocalSource("local_repository", repoPath)
	if err != nil {
		t.Fatal(err)
	}
	session, _ := service.CreateAssistantSession(ctx, model.AssistantContext{Surface: model.AssistantSurfaceProjects, ScopeKey: "atomic-source"})
	name, productURL, objective, audience := "Example", "https://example.com", "展示审批流程", "产品团队"
	next, err := applyConfigurationPatch(session.Configuration, model.ProjectConfigurationPatch{ProjectName: &name, ProductURL: &productURL, Objective: &objective, TargetAudience: &audience})
	if err != nil {
		t.Fatal(err)
	}
	session.Configuration = next
	message := configurationFollowUpMessage(next, time.Now().UTC())
	session.Messages = append(session.Messages, *message)
	refreshAssistantPendingStatus(session)
	refreshAssistantNextAction(session)
	if err := service.assistantStore.Save(ctx, session); err != nil {
		t.Fatal(err)
	}
	proposal := message.Proposals[0]
	completed, err := service.CompleteAssistantClientAction(ctx, session.ID, proposal.ID, model.AssistantClientActionResultRequest{
		BaseVersion: proposal.BaseVersion, IdempotencyKey: "atomic-source-complete",
		SelectedSources: []model.ConfigurationSourceRef{{Ref: ref.Ref, Kind: ref.Kind, Label: ref.Label}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if completed.Configuration.Readiness != "ready" || completed.Configuration.Version != next.Version+1 || len(completed.Configuration.Sources) != 1 {
		t.Fatalf("client action did not atomically update configuration: %+v", completed.Configuration)
	}
	if completed.NextAction.Kind != string(model.AssistantProposalConfirmConfiguration) || completed.NextAction.ProposalID == "" {
		t.Fatalf("server did not return one authoritative next action: %+v", completed.NextAction)
	}
	got := findAssistantProposal(completed, proposal.ID)
	if got == nil || got.Status != "confirmed" || got.ExecutionResult["clientActionCompleted"] != true {
		t.Fatalf("client action audit result missing: %+v", got)
	}
}

func TestAssistantOpensPersistedProjectWhenAnalysisGateBlocks(t *testing.T) {
	service := newAssistantTestService(t)
	ctx := context.Background()
	repoPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoPath, "package.json"), []byte(`{"name":"different-product","homepage":"https://different.example"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ref, err := service.RegisterLocalSource("local_repository", repoPath)
	if err != nil {
		t.Fatal(err)
	}
	session, _ := service.CreateAssistantSession(ctx, model.AssistantContext{Surface: model.AssistantSurfaceProjects, ScopeKey: "blocked-analysis"})
	name, productURL, objective, audience := "Blocked Demo", "https://target.example", "展示审批流程", "产品团队"
	sources := []model.ConfigurationSourceRef{{Ref: ref.Ref, Kind: ref.Kind, Label: ref.Label}}
	next, err := applyConfigurationPatch(session.Configuration, model.ProjectConfigurationPatch{ProjectName: &name, ProductURL: &productURL, Objective: &objective, TargetAudience: &audience, Sources: &sources})
	if err != nil {
		t.Fatal(err)
	}
	session.Configuration = next
	proposal := newAssistantProposal(model.AssistantProposalConfirmConfiguration, "确认并分析", "", next.Version, time.Now().UTC(), nil, model.AssistantWorkstationEvidence)
	session.Messages = append(session.Messages, model.AssistantMessage{ID: "blocked-proposal", Role: "agent", Kind: "proposal", Proposals: []model.AssistantProposal{proposal}})
	if err := service.assistantStore.Save(ctx, session); err != nil {
		t.Fatal(err)
	}
	confirmed, err := service.ConfirmAssistantProposal(ctx, session.ID, proposal.ID, model.AssistantProposalDecisionRequest{BaseVersion: proposal.BaseVersion, IdempotencyKey: "blocked-analysis-confirm"})
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Configuration.AnalysisProjectID == "" || !confirmed.Configuration.Confirmed || confirmed.ActiveWorkstation != model.AssistantWorkstationEvidence {
		t.Fatalf("repairable blocked project was not opened: %+v", confirmed.Configuration)
	}
	got := findAssistantProposal(confirmed, proposal.ID)
	if got == nil || got.ExecutionResult["analysisBlocked"] != true || got.ExecutionResult["uploadApproved"] != false {
		t.Fatalf("blocked analysis audit result missing: %+v", got)
	}
	state, loadErr := service.LoadProject(ctx, confirmed.Configuration.AnalysisProjectID)
	if loadErr != nil || state.SourceBinding == nil || state.SourceBinding.EffectiveMode != model.ProductSourceModeBlocked {
		t.Fatalf("blocked source binding state was not persisted: state=%+v err=%v", state, loadErr)
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
