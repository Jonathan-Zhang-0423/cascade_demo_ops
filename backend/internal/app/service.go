package app

import (
	"context"
	"errors"

	"cascade-demoops/backend/internal/agents"
	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
	"cascade-demoops/backend/internal/storage"
	"cascade-demoops/backend/internal/store"
)

type Service struct {
	runtime  config.AppRuntimeConfig
	flow     *orchestrator.CascadeFlow
	states   store.StateStore
	layout   storage.LocalLayout
	exchange *ExchangeIntakeService
}

func NewService(runtime config.AppRuntimeConfig, states store.StateStore) (*Service, error) {
	if states == nil {
		states = store.NewMemoryStateStore()
	}
	flow, err := orchestrator.NewCascadeFlow(orchestrator.Dependencies{
		InputContext:      agents.NewInputContextAgent(),
		RequirementReader: agents.NewRequirementReaderAgent(),
		CodeReader:        agents.NewCodeReaderAgent(),
		PageReader:        agents.NewPageReaderAgent(),
		Understanding:     agents.NewMultimodalUnderstandingAgent(),
		ProductMap:        agents.NewProductMapAgent(),
		GraphBuilder:      agents.NewGraphBuilderAgent(),
		ScriptPackager:    agents.NewScriptPackagerAgent(),
		QAExecutor:        agents.NewQAExecutorAgent(),
		AssetGenerator:    agents.NewAssetGeneratorAgent(),
	})
	if err != nil {
		return nil, err
	}
	return &Service{
		runtime:  runtime,
		flow:     flow,
		states:   states,
		layout:   storage.NewLocalLayout(runtime.DataRoot, runtime.ArtifactRoot, runtime.CacheRoot, runtime.LogRoot),
		exchange: NewExchangeIntakeService(nil),
	}, nil
}

func (s *Service) RuntimeConfig() config.AppRuntimeConfig {
	return s.runtime
}

func (s *Service) LocalLayout() storage.LocalLayout {
	return s.layout
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

func (s *Service) InitExecutionPackage(ctx context.Context, request model.ExecutionPackageInitRequest) (model.ExecutionPackageInitResponse, error) {
	return s.exchange.Init(ctx, request)
}

func (s *Service) UploadExecutionPackage(ctx context.Context, request model.ExecutionPackageUploadRequest, payload model.ClientExecutionPackage) (model.ExecutionPackageUploadResponse, error) {
	return s.exchange.Upload(ctx, request, payload)
}

func (s *Service) GetExecutionPackageStatus(ctx context.Context, orgID string, exchangePackageID string) (model.ExecutionPackageStatusResponse, error) {
	return s.exchange.Status(ctx, orgID, exchangePackageID)
}

func (s *Service) CompleteExecutionPackageWithResult(ctx context.Context, orgID string, exchangePackageID string, result model.RecordingResultPackage) (model.ExecutionPackageStatusResponse, error) {
	return s.exchange.CompleteWithRecordingResult(ctx, orgID, exchangePackageID, result)
}

func (s *Service) GetResultPackage(ctx context.Context, orgID string, resultPackageID string) (model.RecordingResultPackage, error) {
	return s.exchange.GetResultPackage(ctx, orgID, resultPackageID)
}

func (s *Service) AcknowledgeResultPackage(ctx context.Context, orgID string, request model.ResultPackageAckRequest) (model.ResultPackageAckResponse, error) {
	return s.exchange.AckResultPackage(ctx, orgID, request)
}
