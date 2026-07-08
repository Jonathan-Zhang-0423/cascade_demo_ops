package orchestrator

import (
	"context"
	"errors"
	"fmt"

	"cascade-demoops/backend/internal/model"
)

// MVP state nodes. Keep this order strict until the first product loop is stable.
type NodeName string

const (
	NodeInputCtx        NodeName = "InputCtx"
	NodeProductExplore  NodeName = "ProductExplore"
	NodeGraphGenerate   NodeName = "GraphGenerate"
	NodeHumanApprove    NodeName = "HumanApprove"
	NodeExecuteRehearse NodeName = "ExecuteRehearse"
	NodeAssetGenerate   NodeName = "AssetGenerate"
)

type FlowStatus string

const (
	FlowStatusCreated       FlowStatus = "created"
	FlowStatusRunning       FlowStatus = "running"
	FlowStatusAwaitingHuman FlowStatus = "awaiting_human_approval"
	FlowStatusCompleted     FlowStatus = "completed"
	FlowStatusFailed        FlowStatus = "failed"
)

type CascadeState struct {
	ProjectID        string                   `json:"project_id"`
	CurrentNode      NodeName                 `json:"current_node"`
	Status           FlowStatus               `json:"status"`
	ProjectContext   *model.ProjectContext    `json:"project_context,omitempty"`
	ProductMap       *model.ProductMap        `json:"product_map,omitempty"`
	WorkflowGraph    *model.DemoWorkflowGraph `json:"workflow_graph,omitempty"`
	Approved         bool                     `json:"approved"`
	RehearsePassRate float64                  `json:"rehearse_pass_rate"`
	Artifacts        *GeneratedArtifacts      `json:"artifacts,omitempty"`
	ErrorMessage     string                   `json:"error_message,omitempty"`
}

type GeneratedArtifacts struct {
	VideoPath          string   `json:"video_path,omitempty"`
	ScreenshotPaths    []string `json:"screenshot_paths,omitempty"`
	StepByStepDocsPath string   `json:"step_by_step_docs_path,omitempty"`
}

type UserInput struct {
	Mode                   model.AppMode `json:"mode"`
	ProductURL             string        `json:"product_url"`
	GitRepoURL             string        `json:"git_repo_url,omitempty"`
	LocalRepoPath          string        `json:"local_repo_path,omitempty"`
	ProductDescription     string        `json:"product_description,omitempty"`
	TargetAudience         string        `json:"target_audience"`
	BrandTone              string        `json:"brand_tone,omitempty"`
	MustShow               []string      `json:"must_show,omitempty"`
	MustNotShow            []string      `json:"must_not_show,omitempty"`
	ForbiddenPages         []string      `json:"forbidden_pages,omitempty"`
	ForbiddenData          []string      `json:"forbidden_data,omitempty"`
	DemoUsername           string        `json:"demo_username,omitempty"`
	DemoPassword           string        `json:"demo_password,omitempty"`
	SSHHost                string        `json:"ssh_host,omitempty"`
	SSHPort                int           `json:"ssh_port,omitempty"`
	SSHUsername            string        `json:"ssh_username,omitempty"`
	SSHPrivateKeySecretRef string        `json:"ssh_private_key_secret_ref,omitempty"`
	SSHPasswordSecretRef   string        `json:"ssh_password_secret_ref,omitempty"`
	SSHAllowedPaths        []string      `json:"ssh_allowed_paths,omitempty"`
	SSHAllowedCommands     []string      `json:"ssh_allowed_commands,omitempty"`
}

type RehearsalResult struct {
	PassRate       float64  `json:"pass_rate"`
	RecordingPaths []string `json:"recording_paths,omitempty"`
	ScreenshotRefs []string `json:"screenshot_refs,omitempty"`
	FailureNotes   []string `json:"failure_notes,omitempty"`
}

type InputContextAgent interface {
	BuildProjectContext(ctx context.Context, input UserInput) (*model.ProjectContext, error)
}

type ProductMapAgent interface {
	ExploreProduct(ctx context.Context, project *model.ProjectContext) (*model.ProductMap, error)
}

type GraphBuilderAgent interface {
	GenerateGraph(ctx context.Context, project *model.ProjectContext, productMap *model.ProductMap) (*model.DemoWorkflowGraph, error)
}

type QAExecutorAgent interface {
	ExecuteAndRehearse(ctx context.Context, graph *model.DemoWorkflowGraph) (RehearsalResult, error)
}

type AssetGeneratorAgent interface {
	GenerateAssets(ctx context.Context, graph *model.DemoWorkflowGraph, result RehearsalResult) (*GeneratedArtifacts, error)
}

type Dependencies struct {
	InputContext   InputContextAgent
	ProductMap     ProductMapAgent
	GraphBuilder   GraphBuilderAgent
	QAExecutor     QAExecutorAgent
	AssetGenerator AssetGeneratorAgent
}

type CascadeFlow struct {
	deps Dependencies
}

func NewCascadeFlow(deps Dependencies) (*CascadeFlow, error) {
	if deps.InputContext == nil {
		return nil, errors.New("missing InputContext agent")
	}
	if deps.ProductMap == nil {
		return nil, errors.New("missing ProductMap agent")
	}
	if deps.GraphBuilder == nil {
		return nil, errors.New("missing GraphBuilder agent")
	}
	if deps.QAExecutor == nil {
		return nil, errors.New("missing QAExecutor agent")
	}
	if deps.AssetGenerator == nil {
		return nil, errors.New("missing AssetGenerator agent")
	}
	return &CascadeFlow{deps: deps}, nil
}

// Start runs the first three nodes and intentionally stops at HumanApprove.
// The frontend should render/edit the graph, then call ApproveAndContinue.
func (f *CascadeFlow) Start(ctx context.Context, input UserInput) (*CascadeState, error) {
	state := &CascadeState{Status: FlowStatusRunning}

	state.CurrentNode = NodeInputCtx
	project, err := f.deps.InputContext.BuildProjectContext(ctx, input)
	if err != nil {
		return fail(state, err), err
	}
	state.ProjectID = project.ID
	state.ProjectContext = project

	state.CurrentNode = NodeProductExplore
	productMap, err := f.deps.ProductMap.ExploreProduct(ctx, project)
	if err != nil {
		return fail(state, err), err
	}
	state.ProductMap = productMap

	state.CurrentNode = NodeGraphGenerate
	graph, err := f.deps.GraphBuilder.GenerateGraph(ctx, project, productMap)
	if err != nil {
		return fail(state, err), err
	}
	state.WorkflowGraph = graph

	state.CurrentNode = NodeHumanApprove
	state.Status = FlowStatusAwaitingHuman
	return state, nil
}

// ApproveAndContinue resumes the flow after a human has reviewed and possibly
// edited the DemoWorkflowGraph in the frontend graph editor.
func (f *CascadeFlow) ApproveAndContinue(ctx context.Context, state *CascadeState, approvedGraph *model.DemoWorkflowGraph) (*CascadeState, error) {
	if state == nil {
		return nil, errors.New("state is nil")
	}
	if state.CurrentNode != NodeHumanApprove || state.Status != FlowStatusAwaitingHuman {
		err := fmt.Errorf("flow is not awaiting human approval: node=%s status=%s", state.CurrentNode, state.Status)
		return fail(state, err), err
	}
	if approvedGraph == nil {
		err := errors.New("approved graph is nil")
		return fail(state, err), err
	}

	state.WorkflowGraph = approvedGraph
	state.Approved = true
	state.Status = FlowStatusRunning

	state.CurrentNode = NodeExecuteRehearse
	rehearsal, err := f.deps.QAExecutor.ExecuteAndRehearse(ctx, approvedGraph)
	if err != nil {
		return fail(state, err), err
	}
	state.RehearsePassRate = rehearsal.PassRate
	if rehearsal.PassRate < 0.90 {
		err := fmt.Errorf("rehearsal pass rate %.2f below required 0.90", rehearsal.PassRate)
		return fail(state, err), err
	}

	state.CurrentNode = NodeAssetGenerate
	artifacts, err := f.deps.AssetGenerator.GenerateAssets(ctx, approvedGraph, rehearsal)
	if err != nil {
		return fail(state, err), err
	}
	state.Artifacts = artifacts
	state.Status = FlowStatusCompleted
	return state, nil
}

func fail(state *CascadeState, err error) *CascadeState {
	if state == nil {
		state = &CascadeState{}
	}
	state.Status = FlowStatusFailed
	state.ErrorMessage = err.Error()
	return state
}
