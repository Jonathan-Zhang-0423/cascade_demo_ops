package app

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"cascade-demoops/backend/internal/agents"
	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/credentialstore"
	"cascade-demoops/backend/internal/driver"
	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/llm"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
	"cascade-demoops/backend/internal/storage"
	"cascade-demoops/backend/internal/store"
)

type Service struct {
	runtime               config.AppRuntimeConfig
	llm                   *llm.Router
	flow                  *orchestrator.CascadeFlow
	states                store.StateStore
	assistantStore        store.AssistantStore
	assistantMu           sync.Mutex
	assistantProgressMu   sync.RWMutex
	assistantProgressSink func(string, orchestrator.ProgressEvent)
	controlPlaneMu        sync.RWMutex
	controlPlaneURL       string
	directTransportMu     sync.RWMutex
	directTransportURL    string
	directIdentityMu      sync.Mutex
	layout                storage.LocalLayout
	exchange              *ExchangeIntakeService
	runningMu             sync.Mutex
	runningTasks          map[string]context.CancelFunc
	editorMu              sync.Mutex
	editorWorker          editorWorker
	editorJobsMu          sync.Mutex
	editorJobs            map[string]editorRenderTask
	outlineRunner         BrowserAgentOutlineRunner
	sourceRefsMu          sync.RWMutex
	sourceRefs            map[string]LocalSourceRef
	approvalMu            sync.Mutex
	approvedBuilds        map[string]approvedBuildCacheEntry
	graphRevisionMu       sync.Mutex
	graphRevisions        map[string]graphRevisionCacheEntry
	exchangeSessionMu     sync.Mutex
	cloudStateMu          sync.Mutex
	storeModelKey         func(string, string) error
	readModelKey          func(string) (string, error)
	deleteModelKey        func(string) error
	storeDirectToken      func(string) error
	readDirectToken       func() (string, error)
	deleteDirectToken     func() error
	storeDirectIdentity   func([]byte) error
	readDirectIdentity    func() ([]byte, error)
	storeDirectLease      func(string, []byte) error
	readDirectLease       func(string) ([]byte, error)
	deleteDirectLease     func(string) error
	readDemoCredential    func(string) (credentialstore.DemoCredential, error)
	verifierMu            sync.RWMutex
	// outcomeVerifier is Server-owned. It never receives a browser/page object
	// and is snapshotted when an Outline run begins.
	outcomeVerifier OutcomeVerifier
	// devVisibleBrowserAgent is intentionally separate from the normal runtime.
	// It exists only for a human-assisted local acceptance login handoff.
	devVisibleBrowserAgent *devVisibleBrowserAgentManager
	// devAppPackageTestWaivers never participates in Exchange. It holds only
	// short-lived, Server-local capabilities for an unchanged App draft.
	devAppPackageTestWaivers *devAppPackageTestWaiverManager
}

func (s *Service) SetAssistantProgressSink(sink func(string, orchestrator.ProgressEvent)) {
	if s == nil {
		return
	}
	s.assistantProgressMu.Lock()
	defer s.assistantProgressMu.Unlock()
	s.assistantProgressSink = sink
}

func (s *Service) emitAssistantProgress(projectID string, event orchestrator.ProgressEvent) {
	if s == nil {
		return
	}
	s.assistantProgressMu.RLock()
	sink := s.assistantProgressSink
	s.assistantProgressMu.RUnlock()
	if sink != nil {
		sink(projectID, event)
	}
}

type editorRenderTask struct {
	JobID  string
	Kind   string
	Cancel context.CancelFunc
}

type editorWorker interface {
	ProbeMedia(context.Context, executor.MediaProbeRequest) (executor.MediaProbeResult, error)
	ValidateEditPlan(context.Context, executor.EditPlanValidationRequest) (model.DemoEditPlanValidationReport, error)
	Render(context.Context, executor.RenderRequest) (executor.RenderResult, error)
}

type ResultArtifactFile struct {
	Artifact model.ArtifactRef
	Path     string
	MimeType string
}

func NewService(runtime config.AppRuntimeConfig, states store.StateStore) (*Service, error) {
	if states == nil {
		states = store.NewMemoryStateStore()
	}
	runtime = hydrateRuntimeWithPlanningModelSettings(runtime)
	llmRouter := llm.NewRouter(runtime)
	flow, err := orchestrator.NewCascadeFlow(orchestrator.Dependencies{
		InputContext:         agents.NewInputContextAgent(),
		RequirementReader:    agents.NewRequirementReaderAgentWithLLM(llmRouter),
		CodeReader:           agents.NewCodeReaderAgentWithLLM(llmRouter, runtime.CacheRoot),
		PageReader:           agents.NewPageReaderAgent(),
		SourceBinding:        agents.NewProductSourceBindingAgent(),
		ProjectIntelligence:  agents.NewProjectIntelligenceGraphWithLLM(llmRouter),
		Understanding:        agents.NewMultimodalUnderstandingAgentWithLLM(llmRouter),
		ProductMap:           agents.NewProductMapAgentWithLLM(llmRouter),
		PageVerifier:         agents.NewPageInteractionVerifierAgentWithRuntime(runtime),
		BusinessStagePlanner: agents.NewBusinessStagePlannerAgent(),
		GraphBuilder:         agents.NewGraphBuilderAgentWithLLM(llmRouter),
		ScriptPackager:       agents.NewScriptPackagerAgentWithLLM(llmRouter),
		QAExecutor:           agents.NewQAExecutorAgent(),
		AssetGenerator:       agents.NewAssetGeneratorAgent(),
	})
	if err != nil {
		return nil, err
	}
	service := &Service{
		runtime:             runtime,
		llm:                 llmRouter,
		flow:                flow,
		states:              states,
		assistantStore:      assistantStoreForRuntime(runtime),
		layout:              storage.NewLocalLayout(runtime.DataRoot, runtime.ArtifactRoot, runtime.CacheRoot, runtime.LogRoot),
		exchange:            newExchangeIntakeService(nil, newFileExchangeSnapshotStore(filepath.Join(runtime.DataRoot, "exchange_state"))),
		runningTasks:        map[string]context.CancelFunc{},
		editorJobs:          map[string]editorRenderTask{},
		outlineRunner:       nil,
		sourceRefs:          map[string]LocalSourceRef{},
		approvedBuilds:      map[string]approvedBuildCacheEntry{},
		graphRevisions:      map[string]graphRevisionCacheEntry{},
		storeModelKey:       credentialstore.StoreModelAPIKey,
		readModelKey:        credentialstore.ReadModelAPIKey,
		deleteModelKey:      credentialstore.DeleteModelAPIKey,
		storeDirectToken:    credentialstore.StoreDirectBrowserAgentToken,
		readDirectToken:     credentialstore.ReadDirectBrowserAgentToken,
		deleteDirectToken:   credentialstore.DeleteDirectBrowserAgentToken,
		storeDirectIdentity: credentialstore.StoreDirectBrowserAgentIdentity,
		readDirectIdentity:  credentialstore.ReadDirectBrowserAgentIdentity,
		storeDirectLease:    credentialstore.StoreDirectBrowserAgentLease,
		readDirectLease:     credentialstore.ReadDirectBrowserAgentLease,
		deleteDirectLease:   credentialstore.DeleteDirectBrowserAgentLease,
		readDemoCredential:  credentialstore.ReadDemoCredential,
	}
	service.controlPlaneURL = loadPersistedControlPlaneURL(runtime.DataRoot)
	service.devVisibleBrowserAgent = newDevVisibleBrowserAgentManager(service)
	service.devAppPackageTestWaivers = newDevAppPackageTestWaiverManager(service)
	service.editorWorker = driver.NewLocalDriver(service.nodeBinaryForExecution(), service.localVideoWorkerPath(), service.videoWorkerEnvironment())
	// Production cloud intake receives only encrypted payload references. Local
	// and test runtimes retain inline payloads solely for deterministic fixtures
	// and never relax the production transport rule.
	service.exchange.SetInlinePayloadAllowed(runtime.Profile != config.ProfileCloud || runtime.Environment != "production")
	service.outlineRunner = localBrowserAgentOutlineRunner{service: service}

	// Initialize and inject the BrowserAgentOutcomeVerifierAdapter
	// This provides business-rule validation (domain checks, selector analysis, repair proposals)
	// on top of the lightweight default verifier's protocol compliance checks.
	validationConfig := &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
		PlaybackValidationEnabled: true,
		PassRateThreshold:         0.8,
		ConfidenceThreshold:       0.7,
		CriticalIssueThreshold:    1,
		EnableRuntimeRepair:       true,
		AutoApplyMinorRepairs:     false,
		MaxRepairAttemptsPerStage: 2,
		ParallelValidationEnabled: false,
		ValidationTimeoutSeconds:  30,
	}
	adapter := orchestrator.NewBrowserAgentOutcomeVerifierAdapter(validationConfig)
	service.SetBrowserAgentOutcomeVerifier(adapter)

	service.loadLocalSourceRefs()
	return service, nil
}

func assistantStoreForRuntime(runtime config.AppRuntimeConfig) store.AssistantStore {
	if strings.TrimSpace(runtime.DataRoot) == "" {
		return store.NewMemoryAssistantStore()
	}
	return store.NewFileAssistantStore(filepath.Join(runtime.DataRoot, "assistant_sessions"))
}

// SetBrowserAgentOutcomeVerifier installs the Server-side Validation Agent
// adapter for future Outline runs. The App upload contract remains unchanged.
func (s *Service) SetBrowserAgentOutcomeVerifier(verifier OutcomeVerifier) {
	if s == nil {
		return
	}
	s.verifierMu.Lock()
	defer s.verifierMu.Unlock()
	s.outcomeVerifier = verifier
}

func (s *Service) browserAgentOutcomeVerifierSnapshot() OutcomeVerifier {
	if s == nil {
		return nil
	}
	s.verifierMu.RLock()
	defer s.verifierMu.RUnlock()
	return s.outcomeVerifier
}

func (s *Service) videoWorkerEnvironment() map[string]string {
	environment := map[string]string{}
	if s != nil && s.runtime.FFmpegPath != "" {
		environment["CASCADE_FFMPEG_PATH"] = s.runtime.FFmpegPath
	}
	if s != nil && s.runtime.FFprobePath != "" {
		environment["CASCADE_FFPROBE_PATH"] = s.runtime.FFprobePath
	}
	return environment
}

func (s *Service) RuntimeConfig() config.AppRuntimeConfig {
	s.controlPlaneMu.RLock()
	defer s.controlPlaneMu.RUnlock()
	runtime := cloneModelRuntime(s.runtime)
	runtime.CloudExchangeBaseURL = firstNonEmptyString(s.controlPlaneURL, runtime.CloudExchangeBaseURL)
	return runtime
}

func (s *Service) LocalLayout() storage.LocalLayout {
	return s.layout
}

func (s *Service) DiagnoseModels(ctx context.Context) []llm.DiagnosticResult {
	if s.llm == nil {
		return nil
	}
	return s.llm.DiagnoseProviders(ctx)
}

func (s *Service) DiagnosePlanningModel(ctx context.Context) llm.DiagnosticResult {
	if s.llm == nil {
		return llm.DiagnosticResult{Task: config.ModelTaskPlanning, Mode: config.LLMModeAuto, ErrorClass: "router_missing", Error: "LLM router is not configured", CheckedAt: time.Now().UTC()}
	}
	return s.llm.DiagnoseTask(ctx, config.ModelTaskPlanning)
}

func (s *Service) CreateProject(ctx context.Context, input orchestrator.UserInput) (*orchestrator.CascadeState, error) {
	state, err := s.flow.Start(ctx, input)
	if err != nil {
		if state != nil && state.ProjectID != "" {
			_ = s.states.Save(ctx, state)
		}
		return state, err
	}
	if err := s.states.Save(ctx, state); err != nil {
		return nil, err
	}
	return state, nil
}

func (s *Service) LoadProject(ctx context.Context, projectID string) (*orchestrator.CascadeState, error) {
	return s.states.Load(ctx, projectID)
}

type SourceBindingDecisionRequest struct {
	Decision       string `json:"decision"`
	AssessmentHash string `json:"assessment_hash"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (s *Service) GetSourceBinding(ctx context.Context, projectID string) (*model.ProductSourceBindingAssessment, error) {
	state, err := s.states.Load(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if state.SourceBinding == nil {
		return nil, errors.New("source binding assessment is missing")
	}
	return state.SourceBinding, nil
}

func (s *Service) DecideSourceBinding(ctx context.Context, projectID string, request SourceBindingDecisionRequest) (*orchestrator.CascadeState, error) {
	if request.Decision != "continue_page_only" {
		return nil, errors.New("unsupported source binding decision")
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return nil, errors.New("idempotency_key is required")
	}
	state, err := s.states.Load(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if state.SourceBinding == nil || state.SourceBinding.AssessmentHash == "" {
		return nil, errors.New("source binding assessment is missing")
	}
	if request.AssessmentHash == "" || request.AssessmentHash != state.SourceBinding.AssessmentHash {
		return nil, &SourceBindingStaleError{}
	}
	if state.SourceBinding.Decision == request.Decision && state.SourceBinding.EffectiveMode == model.ProductSourceModePageOnly {
		return state, nil
	}
	if state.ProjectContext == nil {
		return nil, errors.New("project context is missing")
	}
	input, err := userInputFromProjectContext(state.ProjectContext)
	if err != nil {
		return nil, err
	}
	input.ProjectID = projectID
	input.SourceBindingDecision = request.Decision
	input.SourceBindingHash = request.AssessmentHash
	return s.CreateProject(ctx, input)
}

type SourceBindingStaleError struct{}

func (e *SourceBindingStaleError) Error() string { return "source binding assessment is stale" }

type ProjectSummary struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	ProductURL          string    `json:"product_url,omitempty"`
	Stage               string    `json:"stage"`
	Status              string    `json:"status"`
	AssetCount          int       `json:"asset_count"`
	GeneratedAssetCount int       `json:"generated_asset_count"`
	CreatedAt           time.Time `json:"created_at,omitempty"`
	UpdatedAt           time.Time `json:"updated_at,omitempty"`
}

func (s *Service) ListProjects(ctx context.Context) ([]ProjectSummary, error) {
	states, err := s.states.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]ProjectSummary, 0, len(states))
	for _, state := range states {
		if state == nil || state.ProjectID == "" || state.ArchivedAt != nil {
			continue
		}
		result = append(result, projectSummaryFromState(state))
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].UpdatedAt.After(result[j].UpdatedAt) })
	return result, nil
}

func (s *Service) ArchiveProject(ctx context.Context, projectID string) error {
	return s.states.Archive(ctx, projectID, time.Now().UTC())
}

func (s *Service) DeleteProject(ctx context.Context, projectID string) error {
	return s.states.Delete(ctx, projectID)
}

func projectSummaryFromState(state *orchestrator.CascadeState) ProjectSummary {
	summary := ProjectSummary{ID: state.ProjectID, Name: "未命名演示", Stage: projectStageFromState(state), Status: projectStatusFromState(state)}
	if state.ProjectContext != nil {
		if state.ProjectContext.Name != "" {
			summary.Name = state.ProjectContext.Name
		}
		summary.ProductURL = state.ProjectContext.ProductURL
		summary.CreatedAt = state.ProjectContext.CreatedAt
		summary.UpdatedAt = state.ProjectContext.UpdatedAt
	}
	if summary.UpdatedAt.IsZero() {
		summary.UpdatedAt = summary.CreatedAt
	}
	if state.Artifacts != nil {
		if state.Artifacts.VideoPath != "" {
			summary.AssetCount++
			summary.GeneratedAssetCount++
		}
		if state.Artifacts.StepByStepDocsPath != "" {
			summary.AssetCount++
			summary.GeneratedAssetCount++
		}
		summary.AssetCount += len(state.Artifacts.ScreenshotPaths)
		summary.GeneratedAssetCount += len(state.Artifacts.ScreenshotPaths)
	}
	return summary
}

func projectStageFromState(state *orchestrator.CascadeState) string {
	switch state.CurrentNode {
	case orchestrator.NodeInputCtx:
		return "setup"
	case orchestrator.NodeRequirementRead, orchestrator.NodeCodeRead, orchestrator.NodePageRead, orchestrator.NodeProjectIntelligence, orchestrator.NodeMultimodalUnderstand, orchestrator.NodeProductExplore, orchestrator.NodePageInteractionVerify:
		return "understanding"
	case orchestrator.NodeGraphGenerate:
		return "plan_review"
	case orchestrator.NodeScriptPackage, orchestrator.NodeHumanApprove:
		return "package_approval"
	case orchestrator.NodeExecuteRehearse:
		return "cloud_run"
	case orchestrator.NodeAssetGenerate:
		return "result_review"
	default:
		return "inputs"
	}
}

func projectStatusFromState(state *orchestrator.CascadeState) string {
	switch state.Status {
	case orchestrator.FlowStatusAwaitingHuman:
		return "awaiting_approval"
	case orchestrator.FlowStatusRunning:
		return "cloud_running"
	case orchestrator.FlowStatusCompleted:
		return "asset_ready"
	case orchestrator.FlowStatusFailed:
		return "script_repair_required"
	default:
		return "draft"
	}
}

func (s *Service) GenerateExecutionPackage(ctx context.Context, input orchestrator.UserInput) (*orchestrator.CascadeState, error) {
	state, err := s.CreateProject(ctx, input)
	if err != nil {
		return state, err
	}
	if stateHasNoCoreBusinessAction(state) {
		return state, errors.New("missing verified interaction evidence: execution package has no real business action")
	}
	return state, nil
}

func (s *Service) RegenerateExecutionPackage(ctx context.Context, projectID string) (*orchestrator.CascadeState, error) {
	state, err := s.states.Load(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if state.ProjectContext == nil {
		return nil, errors.New("project context is missing")
	}
	input, err := userInputFromProjectContext(state.ProjectContext)
	if err != nil {
		return nil, err
	}
	return s.CreateProject(ctx, input)
}

func stateHasNoCoreBusinessAction(state *orchestrator.CascadeState) bool {
	if state == nil {
		return true
	}
	if state.ProjectIntelligence != nil && state.ProjectIntelligence.BusinessStagePlan != nil {
		return state.ProjectIntelligence.BusinessStagePlan.CoreBusinessStageCount == 0
	}
	return !scriptDocumentHasBusinessAction(state.ScriptDocument)
}

func (s *Service) SaveProjectInput(ctx context.Context, projectID string, inputs model.ProjectInputBundle) (*model.ProjectContext, error) {
	if err := model.ValidatePresentationGenerationIntents(inputs.PresentationGenerationIntents); err != nil {
		return nil, err
	}
	state, err := s.states.Load(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if state.ProjectContext == nil {
		return nil, errors.New("project context is missing")
	}
	state.ProjectContext.Inputs = &inputs
	if err := s.states.Save(ctx, state); err != nil {
		return nil, err
	}
	return state.ProjectContext, nil
}

func (s *Service) GetWorkflowGraph(ctx context.Context, projectID string) (*model.DemoWorkflowGraph, error) {
	state, err := s.states.Load(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if state.WorkflowGraph == nil {
		return nil, errors.New("workflow graph is missing")
	}
	return state.WorkflowGraph, nil
}

func (s *Service) GetUnderstandingReport(ctx context.Context, projectID string) (*model.MultimodalUnderstandingReport, error) {
	state, err := s.states.Load(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if state.UnderstandingReport == nil {
		return nil, errors.New("understanding report is missing")
	}
	return state.UnderstandingReport, nil
}

func (s *Service) GetExecutionScriptDocument(ctx context.Context, projectID string) (*model.ExecutionScriptDocument, error) {
	state, err := s.states.Load(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if state.ScriptDocument == nil {
		return nil, errors.New("execution script document is missing")
	}
	return state.ScriptDocument, nil
}

func (s *Service) GetExecutionScriptMarkdown(ctx context.Context, projectID string) (string, *model.ArtifactRef, error) {
	state, err := s.states.Load(ctx, projectID)
	if err != nil {
		return "", nil, err
	}
	if state.ScriptMarkdown == "" {
		return "", nil, errors.New("execution script markdown is missing")
	}
	return state.ScriptMarkdown, state.ScriptMarkdownArtifact, nil
}

func (s *Service) GetExecutableScriptBundle(ctx context.Context, projectID string) (*model.ExecutableRecordingScriptBundle, error) {
	state, err := s.states.Load(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if state.ExecutableScriptBundle == nil {
		return nil, errors.New("executable script bundle is missing")
	}
	return state.ExecutableScriptBundle, nil
}

func (s *Service) ApproveWorkflowGraph(ctx context.Context, projectID string, graph *model.DemoWorkflowGraph) (*orchestrator.CascadeState, error) {
	state, err := s.states.Load(ctx, projectID)
	if err != nil {
		return nil, err
	}
	next, err := s.flow.ApproveAndContinue(ctx, state, graph)
	if err != nil {
		return next, err
	}
	if err := s.states.Save(ctx, next); err != nil {
		return nil, err
	}
	return next, nil
}

func (s *Service) RunRehearsal(ctx context.Context, projectID string) (*orchestrator.CascadeState, error) {
	graph, err := s.GetWorkflowGraph(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return s.ApproveWorkflowGraph(ctx, projectID, graph)
}

func (s *Service) ArtifactURI(projectID string, fileName string) string {
	return s.layout.ArtifactURI(projectID, fileName)
}

func userInputFromProjectContext(project *model.ProjectContext) (orchestrator.UserInput, error) {
	input := orchestrator.UserInput{
		ProjectID:          project.ID,
		Mode:               project.Mode,
		ProductURL:         project.ProductURL,
		GitRepoURL:         project.GitRepoURL,
		LocalRepoPath:      project.LocalRepoPath,
		ProductDescription: project.ProductDescription,
		TargetDurationSec:  projectTargetDuration(project),
		TargetAudience:     project.TargetAudience,
		BrandTone:          project.BrandTone,
		MustShow:           append([]string{}, project.MustShow...),
		MustNotShow:        append([]string{}, project.MustNotShow...),
		ForbiddenPages:     append([]string{}, project.ForbiddenPages...),
		ForbiddenData:      append([]string{}, project.ForbiddenData...),
	}
	if project.SourceBinding != nil {
		input.SourceBindingDecision = project.SourceBinding.Decision
		input.SourceBindingHash = project.SourceBinding.AssessmentHash
	}
	if project.AccessPolicy != nil {
		input.AllowedDomains = append([]string{}, project.AccessPolicy.AllowedDomains...)
	}
	if project.Inputs != nil {
		input.Requirements = append([]model.DemoRequirement{}, project.Inputs.Requirements...)
		input.PresentationGenerationIntents = append([]model.PresentationGenerationIntent{}, project.Inputs.PresentationGenerationIntents...)
		input.Code = codeInputsForProjectRerun(project)
		input.RequirementDocuments = append([]model.RequirementDocumentInput{}, project.Inputs.RequirementDocuments...)
		input.WebpageScreenshots = append([]model.WebpageScreenshotInput{}, project.Inputs.WebpageScreenshots...)
		if input.ProductURL == "" && len(project.Inputs.ProductURLs) > 0 {
			input.ProductURL = project.Inputs.ProductURLs[0].URL
		}
		if input.LocalRepoPath == "" || input.GitRepoURL == "" {
			for _, repo := range project.Inputs.Repositories {
				if input.LocalRepoPath == "" && strings.TrimSpace(repo.LocalPath) != "" {
					input.LocalRepoPath = repo.LocalPath
				}
				if input.GitRepoURL == "" && strings.TrimSpace(repo.URL) != "" {
					input.GitRepoURL = repo.URL
				}
			}
		}
		if input.ProductDescription == "" {
			input.ProductDescription = project.Inputs.RawUserPrompt
		}
		for _, value := range project.Inputs.Credentials {
			const prefix = "credential://demo/"
			if !strings.HasPrefix(value.SecretRef, prefix) {
				continue
			}
			credential, err := credentialstore.ReadDemoCredential(strings.TrimPrefix(value.SecretRef, prefix))
			if err != nil {
				return input, errors.New("demo credential ref is unavailable")
			}
			input.DemoUsername = credential.Username
			input.DemoPassword = credential.Password
			input.DemoCredentialRef = value.SecretRef
			break
		}
	}
	return input, nil
}

func codeInputsForProjectRerun(project *model.ProjectContext) []model.CodeInput {
	if project == nil || project.Inputs == nil {
		return nil
	}
	out := make([]model.CodeInput, 0, len(project.Inputs.Code))
	for _, code := range project.Inputs.Code {
		if project.LocalRepoPath != "" && code.ID == "code_local_repo" {
			continue
		}
		if project.GitRepoURL != "" && code.ID == "code_git_repo" {
			continue
		}
		out = append(out, code)
	}
	return out
}

func projectTargetDuration(project *model.ProjectContext) int {
	if project != nil && project.Inputs != nil && len(project.Inputs.Scenarios) > 0 {
		return project.Inputs.Scenarios[0].DurationSeconds
	}
	return 60
}

func (s *Service) InitExecutionPackage(ctx context.Context, request model.ExecutionPackageInitRequest) (model.ExecutionPackageInitResponse, error) {
	return s.exchange.Init(ctx, request)
}

func (s *Service) UploadExecutionPackage(ctx context.Context, request model.ExecutionPackageUploadRequest, payload model.ClientExecutionPackage) (model.ExecutionPackageUploadResponse, error) {
	return s.exchange.Upload(ctx, request, payload)
}

// UploadExecutionPackageFromHTTP carries only the transport-authenticated
// installation identity. The package body remains the source of business data.
func (s *Service) UploadExecutionPackageFromHTTP(ctx context.Context, request model.ExecutionPackageUploadRequest, payload model.ClientExecutionPackage, installID string) (model.ExecutionPackageUploadResponse, error) {
	if strings.TrimSpace(installID) == "" {
		return s.exchange.Upload(ctx, request, payload)
	}
	return s.exchange.UploadFromInstallation(ctx, request, payload, installID)
}

// ValidateExecutionPackage applies the same Intake rules as Upload without
// creating an upload session, persisting the payload, or starting a browser.
func (s *Service) ValidateExecutionPackage(ctx context.Context, request model.ExecutionPackageUploadRequest, payload model.ClientExecutionPackage) (CloudPackagePreflightResult, error) {
	if err := s.exchange.ValidateUpload(ctx, request, payload); err != nil {
		return CloudPackagePreflightResult{}, err
	}
	return executionPackagePreflightResult(payload), nil
}

func executionPackagePreflightResult(pkg model.ClientExecutionPackage) CloudPackagePreflightResult {
	result := CloudPackagePreflightResult{
		Valid: true, PackageID: pkg.PackageID, AllowedDomains: append([]string{}, pkg.RecordingRunSpec.AllowedDomains...),
		Warnings: []string{},
		Message:  "Server Intake 校验通过：尚未上传、尚未启动浏览器、尚未读取任何客户页面。",
	}
	if bundle := pkg.ExecutableScriptBundle; bundle != nil {
		result.Runtime = bundle.ScriptManifest.Runtime
		if bundle.PlanJSON != nil {
			result.StageCount = len(bundle.PlanJSON.Steps)
			for _, step := range bundle.PlanJSON.Steps {
				for _, validation := range step.Validations {
					if validation.Required {
						result.RequiredChecks++
					}
				}
			}
		}
		if bundle.ScriptManifest.Runtime == model.ExecutableScriptRuntimeBrowserAgentOutlineV1 {
			readiness := browserAgentReadiness(&pkg)
			result.Readiness = &readiness
			if !readiness.CanRun {
				result.Message = "Server Intake structural validation passed, but Browser Agent readiness is blocked before any browser session can start."
			}
		}
	}
	return result
}

func (s *Service) GetExecutionPackageStatus(ctx context.Context, orgID string, exchangePackageID string) (model.ExecutionPackageStatusResponse, error) {
	status, err := s.exchange.Status(ctx, orgID, exchangePackageID)
	if err != nil || status.ResultSummary == nil || status.ResultSummary.Acceptance == nil || status.ResultPackageID == "" {
		return status, err
	}
	result, snapshotErr := s.exchange.ResultSnapshot(ctx, orgID, status.ResultPackageID)
	if snapshotErr != nil {
		return status, nil
	}
	materialization, materializationErr := s.GetEditorSessionMaterialization(ctx, result)
	if materializationErr == nil {
		status.ResultSummary.Acceptance.EditorMaterialized = materialization.Ready
	}
	return status, nil
}

func (s *Service) ListExecutionPackages(ctx context.Context, orgID string) (model.ExecutionPackageListResponse, error) {
	return s.exchange.ListExecutionPackages(ctx, orgID)
}

func (s *Service) CancelExecutionPackage(ctx context.Context, orgID string, exchangePackageID string) (model.ExecutionPackageStatusResponse, error) {
	s.cancelRunningExecution(orgID, exchangePackageID)
	return s.exchange.CancelExecution(ctx, orgID, exchangePackageID, "canceled_by_dev_request")
}

func (s *Service) GetExecutionPackageDebugView(ctx context.Context, orgID string, exchangePackageID string) (ExecutionPackageDebugView, error) {
	return s.GetExecutionPackageDebug(ctx, orgID, exchangePackageID)
}

func (s *Service) CompleteExecutionPackageWithResult(ctx context.Context, orgID string, exchangePackageID string, result model.RecordingResultPackage) (model.ExecutionPackageStatusResponse, error) {
	status, err := s.exchange.CompleteWithRecordingResult(ctx, orgID, exchangePackageID, result)
	if err != nil {
		return model.ExecutionPackageStatusResponse{}, err
	}
	// A completed Server-owned recording should immediately enter the editor
	// inbox. A handoff failure must not roll back the already valid result.
	materialization, _ := s.MaterializeEditorSessionFromResultPackage(ctx, result)
	if status.ResultSummary != nil && status.ResultSummary.Acceptance != nil {
		status.ResultSummary.Acceptance.EditorMaterialized = materialization.Ready
	}
	return status, nil
}

// EditorSessionMaterialization reports whether a successful result is ready
// for the local editor. Remote encrypted artifacts require a future download
// and decryption handoff, so they are deliberately not imported directly.
type EditorSessionMaterialization struct {
	Ready     bool   `json:"ready"`
	Created   bool   `json:"created"`
	SessionID string `json:"session_id,omitempty"`
	Message   string `json:"message"`
}

func (s *Service) MaterializeEditorSessionFromResultPackage(ctx context.Context, result model.RecordingResultPackage) (EditorSessionMaterialization, error) {
	if result.Status == model.RecordingResultStatusFailed {
		return EditorSessionMaterialization{Message: "任务失败，素材保留在失败复盘中，不创建编辑会话。"}, nil
	}
	request, err := s.localEditorHandoffRequest(result)
	if err != nil {
		return EditorSessionMaterialization{Message: "结果已交付，但待编辑素材尚不可用：" + err.Error()}, nil
	}
	session, created, err := s.EnsureEditorSessionFromResultPackage(ctx, request)
	if err != nil {
		return EditorSessionMaterialization{Message: "结果已交付，但创建编辑会话失败：" + err.Error()}, nil
	}
	message := "待编辑素材已登记，可直接进入视频编辑器。"
	if !created {
		message = "待编辑素材已存在，已复用原编辑会话。"
	}
	return EditorSessionMaterialization{Ready: true, Created: created, SessionID: session.SessionID, Message: message}, nil
}

func (s *Service) GetEditorSessionMaterialization(ctx context.Context, result model.RecordingResultPackage) (EditorSessionMaterialization, error) {
	if result.Status == model.RecordingResultStatusFailed {
		return EditorSessionMaterialization{Message: "任务失败，素材保留在失败复盘中，不创建编辑会话。"}, nil
	}
	if strings.TrimSpace(result.ResultID) == "" {
		return EditorSessionMaterialization{Message: "结果包缺少标识，暂不能创建编辑会话。"}, nil
	}
	sessions, err := s.ListEditorSessions(ctx)
	if err != nil {
		return EditorSessionMaterialization{}, err
	}
	for _, session := range sessions {
		if session.AssetCatalog.Source.RecordingResultPackageID == result.ResultID {
			return EditorSessionMaterialization{Ready: true, SessionID: session.SessionID, Message: "待编辑素材已登记，可直接进入视频编辑器。"}, nil
		}
	}
	_, err = s.localEditorHandoffRequest(result)
	if err != nil {
		return EditorSessionMaterialization{Message: "结果已交付，但待编辑素材尚不可用：" + err.Error()}, nil
	}
	return EditorSessionMaterialization{Message: "待编辑素材正在登记。"}, nil
}

func (s *Service) localEditorHandoffRequest(result model.RecordingResultPackage) (model.EditorCreateFromResultPackageRequest, error) {
	request := model.EditorCreateFromResultPackageRequest{ResultPackage: &result, ArtifactPaths: map[string]string{}}
	artifacts := append([]model.ArtifactRef{}, result.GeneratedAssets...)
	if result.ExecutionTrace != nil {
		artifacts = append(artifacts, result.ExecutionTrace.Artifacts...)
	}
	for _, artifact := range artifacts {
		if artifact.ID == "" {
			continue
		}
		path, ok := s.localResultArtifactPath(artifact)
		if !ok {
			continue
		}
		request.ArtifactPaths[artifact.ID] = path
		if isRawRecordingArtifact(artifact) && request.RecordingPath == "" {
			request.RecordingPath = path
			request.RecordingArtifactID = artifact.ID
		}
	}
	if request.RecordingPath == "" {
		return model.EditorCreateFromResultPackageRequest{}, errors.New("未找到位于 Server 素材目录的录屏；远程加密产物需先下载并解密")
	}
	return request, nil
}

func (s *Service) localResultArtifactPath(artifact model.ArtifactRef) (string, bool) {
	value := artifact.URI
	if path, ok := artifact.Metadata["local_path"].(string); ok && strings.TrimSpace(path) != "" {
		value = path
	}
	path, err := localPathFromURI(value)
	if err != nil || !fileExists(path) {
		return "", false
	}
	root, err := filepath.Abs(filepath.Clean(s.runtime.ArtifactRoot))
	if err != nil || !pathWithinRoot(path, root) {
		return "", false
	}
	return path, true
}

func (s *Service) GetResultPackage(ctx context.Context, orgID string, resultPackageID string) (model.RecordingResultPackage, error) {
	return s.exchange.GetResultPackage(ctx, orgID, resultPackageID)
}

func (s *Service) ListResultPackages(ctx context.Context, orgID string) (model.ResultPackageListResponse, error) {
	return s.exchange.ListResultPackages(ctx, orgID)
}

func (s *Service) GetResultArtifactFile(ctx context.Context, orgID string, resultPackageID string, artifactID string) (ResultArtifactFile, error) {
	artifact, err := s.exchange.GetResultArtifact(ctx, orgID, resultPackageID, artifactID)
	if err != nil {
		return ResultArtifactFile{}, err
	}
	filePath, err := s.localArtifactPath(artifact.URI)
	if err != nil {
		return ResultArtifactFile{}, err
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return ResultArtifactFile{}, err
	}
	if info.IsDir() {
		return ResultArtifactFile{}, errors.New("artifact path is a directory")
	}
	if err := s.exchange.MarkResultArtifactDelivered(ctx, orgID, resultPackageID, artifactID); err != nil {
		return ResultArtifactFile{}, err
	}
	return ResultArtifactFile{Artifact: artifact, Path: filePath, MimeType: artifact.MimeType}, nil
}

func (s *Service) AcknowledgeResultPackage(ctx context.Context, orgID string, request model.ResultPackageAckRequest) (model.ResultPackageAckResponse, error) {
	return s.exchange.AckResultPackage(ctx, orgID, request)
}

func (s *Service) ReviewResultPackage(ctx context.Context, orgID string, resultPackageID string, request model.ResultReviewRequest) (model.ResultReviewRecord, error) {
	return s.exchange.ReviewResultPackage(ctx, orgID, resultPackageID, request)
}

func (s *Service) RequestResultRevision(ctx context.Context, orgID string, resultPackageID string, request model.ResultRevisionRequest) (model.ResultRevisionRecord, error) {
	return s.exchange.RequestResultRevision(ctx, orgID, resultPackageID, request)
}

func (s *Service) localArtifactPath(uri string) (string, error) {
	value := strings.TrimSpace(uri)
	if value == "" {
		return "", errors.New("artifact uri is required")
	}
	filePath := value
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" && !isWindowsDriveScheme(parsed.Scheme, value) {
		if parsed.Scheme != "file" {
			return "", errors.New("only local file artifacts are downloadable in dev exchange")
		}
		filePath = parsed.Path
		if parsed.Host != "" {
			if len(parsed.Host) == 2 && parsed.Host[1] == ':' {
				filePath = parsed.Host + parsed.Path
			} else {
				filePath = `\\` + parsed.Host + parsed.Path
			}
		}
		if unescaped, unescapeErr := url.PathUnescape(filePath); unescapeErr == nil {
			filePath = unescaped
		}
		filePath = filepath.FromSlash(filePath)
		if len(filePath) >= 3 && filePath[0] == filepath.Separator && filePath[2] == ':' {
			filePath = filePath[1:]
		}
	}
	cleanPath, err := filepath.Abs(filepath.Clean(filePath))
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(filepath.Clean(s.runtime.ArtifactRoot))
	if err != nil {
		return "", err
	}
	if !pathWithinRoot(cleanPath, root) {
		return "", errors.New("artifact path is outside artifact root")
	}
	return cleanPath, nil
}

func pathWithinRoot(path string, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || rel != "" && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isWindowsDriveScheme(scheme string, value string) bool {
	return len(scheme) == 1 && len(value) >= 2 && value[1] == ':'
}
