package app

import (
	"context"
	"errors"
	"strings"

	"cascade-demoops/backend/internal/agents"
	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/llm"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
	"cascade-demoops/backend/internal/storage"
	"cascade-demoops/backend/internal/store"
)

type Service struct {
	runtime config.AppRuntimeConfig
	flow    *orchestrator.CascadeFlow
	states  store.StateStore
	layout  storage.LocalLayout
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
		runtime: runtime,
		flow:    flow,
		states:  states,
		layout:  storage.NewLocalLayout(runtime.DataRoot, runtime.ArtifactRoot, runtime.CacheRoot, runtime.LogRoot),
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
