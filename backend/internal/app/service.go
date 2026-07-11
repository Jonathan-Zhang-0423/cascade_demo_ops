package app

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"cascade-demoops/backend/internal/agents"
	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/llm"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
	"cascade-demoops/backend/internal/storage"
	"cascade-demoops/backend/internal/store"
)

type Service struct {
	runtime      config.AppRuntimeConfig
	llm          *llm.Router
	flow         *orchestrator.CascadeFlow
	states       store.StateStore
	layout       storage.LocalLayout
	exchange     *ExchangeIntakeService
	runningMu    sync.Mutex
	runningTasks map[string]context.CancelFunc
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
	llmRouter := llm.NewRouter(runtime)
	flow, err := orchestrator.NewCascadeFlow(orchestrator.Dependencies{
		InputContext:      agents.NewInputContextAgent(),
		RequirementReader: agents.NewRequirementReaderAgentWithLLM(llmRouter),
		CodeReader:        agents.NewCodeReaderAgentWithLLM(llmRouter),
		PageReader:        agents.NewPageReaderAgent(),
		Understanding:     agents.NewMultimodalUnderstandingAgentWithLLM(llmRouter),
		ProductMap:        agents.NewProductMapAgentWithLLM(llmRouter),
		GraphBuilder:      agents.NewGraphBuilderAgentWithLLM(llmRouter),
		ScriptPackager:    agents.NewScriptPackagerAgentWithLLM(llmRouter),
		QAExecutor:        agents.NewQAExecutorAgent(),
		AssetGenerator:    agents.NewAssetGeneratorAgent(),
	})
	if err != nil {
		return nil, err
	}
	return &Service{
		runtime:      runtime,
		llm:          llmRouter,
		flow:         flow,
		states:       states,
		layout:       storage.NewLocalLayout(runtime.DataRoot, runtime.ArtifactRoot, runtime.CacheRoot, runtime.LogRoot),
		exchange:     newExchangeIntakeService(nil, newFileExchangeSnapshotStore(filepath.Join(runtime.DataRoot, "exchange_state"))),
		runningTasks: map[string]context.CancelFunc{},
	}, nil
}

func (s *Service) RuntimeConfig() config.AppRuntimeConfig {
	return s.runtime
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

func (s *Service) CreateProject(ctx context.Context, input orchestrator.UserInput) (*orchestrator.CascadeState, error) {
	state, err := s.flow.Start(ctx, input)
	if err != nil {
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

func (s *Service) GenerateExecutionPackage(ctx context.Context, input orchestrator.UserInput) (*orchestrator.CascadeState, error) {
	return s.CreateProject(ctx, input)
}

func (s *Service) RegenerateExecutionPackage(ctx context.Context, projectID string) (*orchestrator.CascadeState, error) {
	state, err := s.states.Load(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if state.ProjectContext == nil {
		return nil, errors.New("project context is missing")
	}
	return s.CreateProject(ctx, userInputFromProjectContext(state.ProjectContext))
}

func (s *Service) SaveProjectInput(ctx context.Context, projectID string, inputs model.ProjectInputBundle) (*model.ProjectContext, error) {
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

func userInputFromProjectContext(project *model.ProjectContext) orchestrator.UserInput {
	input := orchestrator.UserInput{
		Mode:               project.Mode,
		ProductURL:         project.ProductURL,
		GitRepoURL:         project.GitRepoURL,
		LocalRepoPath:      project.LocalRepoPath,
		ProductDescription: project.ProductDescription,
		TargetAudience:     project.TargetAudience,
		BrandTone:          project.BrandTone,
		MustShow:           append([]string{}, project.MustShow...),
		MustNotShow:        append([]string{}, project.MustNotShow...),
		ForbiddenPages:     append([]string{}, project.ForbiddenPages...),
		ForbiddenData:      append([]string{}, project.ForbiddenData...),
	}
	if project.Inputs != nil {
		input.Code = append([]model.CodeInput{}, project.Inputs.Code...)
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
	}
	return input
}

func (s *Service) InitExecutionPackage(ctx context.Context, request model.ExecutionPackageInitRequest) (model.ExecutionPackageInitResponse, error) {
	return s.exchange.Init(ctx, request)
}

func (s *Service) UploadExecutionPackage(ctx context.Context, request model.ExecutionPackageUploadRequest, payload model.ClientExecutionPackage) (model.ExecutionPackageUploadResponse, error) {
	return s.exchange.Upload(ctx, request, payload)
}

func (s *Service) GetExecutionPackageStatus(ctx context.Context, orgID string, exchangePackageID string) (model.ExecutionPackageStatusResponse, error) {
	return s.exchange.Status(ctx, orgID, exchangePackageID)
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
	return s.exchange.CompleteWithRecordingResult(ctx, orgID, exchangePackageID, result)
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
