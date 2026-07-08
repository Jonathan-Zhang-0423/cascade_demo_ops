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
	NodeInputCtx             NodeName = "InputCtx"
	NodeRequirementRead      NodeName = "RequirementRead"
	NodeCodeRead             NodeName = "CodeRead"
	NodePageRead             NodeName = "PageRead"
	NodeMultimodalUnderstand NodeName = "MultimodalUnderstand"
	NodeProductExplore       NodeName = "ProductExplore"
	NodeGraphGenerate        NodeName = "GraphGenerate"
	NodeScriptPackage        NodeName = "ScriptPackage"
	NodeHumanApprove         NodeName = "HumanApprove"
	NodeExecuteRehearse      NodeName = "ExecuteRehearse"
	NodeAssetGenerate        NodeName = "AssetGenerate"
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
	ProjectID              string                               `json:"project_id"`
	CurrentNode            NodeName                             `json:"current_node"`
	Status                 FlowStatus                           `json:"status"`
	ProjectContext         *model.ProjectContext                `json:"project_context,omitempty"`
	RequirementBrief       *model.RequirementBrief              `json:"requirement_brief,omitempty"`
	CodeSnapshots          []model.CodeUnderstandingSnapshot    `json:"code_snapshots,omitempty"`
	PageSnapshots          []model.PageUnderstandingSnapshot    `json:"page_snapshots,omitempty"`
	UnderstandingReport    *model.MultimodalUnderstandingReport `json:"understanding_report,omitempty"`
	ProductMap             *model.ProductMap                    `json:"product_map,omitempty"`
	WorkflowGraph          *model.DemoWorkflowGraph             `json:"workflow_graph,omitempty"`
	ScriptDocument         *model.ExecutionScriptDocument       `json:"script_document,omitempty"`
	ScriptMarkdown         string                               `json:"script_markdown,omitempty"`
	ScriptMarkdownPath     string                               `json:"script_markdown_path,omitempty"`
	ScriptMarkdownArtifact *model.ArtifactRef                   `json:"script_markdown_artifact,omitempty"`
	Approved               bool                                 `json:"approved"`
	RehearsePassRate       float64                              `json:"rehearse_pass_rate"`
	Artifacts              *GeneratedArtifacts                  `json:"artifacts,omitempty"`
	ErrorMessage           string                               `json:"error_message,omitempty"`
}

type GeneratedArtifacts struct {
	VideoPath          string   `json:"video_path,omitempty"`
	ScreenshotPaths    []string `json:"screenshot_paths,omitempty"`
	StepByStepDocsPath string   `json:"step_by_step_docs_path,omitempty"`
}

type UserInput struct {
	Mode                   model.AppMode                    `json:"mode"`
	ProductURL             string                           `json:"product_url"`
	GitRepoURL             string                           `json:"git_repo_url,omitempty"`
	LocalRepoPath          string                           `json:"local_repo_path,omitempty"`
	ProductDescription     string                           `json:"product_description,omitempty"`
	Code                   []model.CodeInput                `json:"code,omitempty"`
	RequirementDocuments   []model.RequirementDocumentInput `json:"requirement_documents,omitempty"`
	WebpageScreenshots     []model.WebpageScreenshotInput   `json:"webpage_screenshots,omitempty"`
	TargetAudience         string                           `json:"target_audience"`
	BrandTone              string                           `json:"brand_tone,omitempty"`
	MustShow               []string                         `json:"must_show,omitempty"`
	MustNotShow            []string                         `json:"must_not_show,omitempty"`
	ForbiddenPages         []string                         `json:"forbidden_pages,omitempty"`
	ForbiddenData          []string                         `json:"forbidden_data,omitempty"`
	DemoUsername           string                           `json:"demo_username,omitempty"`
	DemoPassword           string                           `json:"demo_password,omitempty"`
	SSHHost                string                           `json:"ssh_host,omitempty"`
	SSHPort                int                              `json:"ssh_port,omitempty"`
	SSHUsername            string                           `json:"ssh_username,omitempty"`
	SSHPrivateKeySecretRef string                           `json:"ssh_private_key_secret_ref,omitempty"`
	SSHPasswordSecretRef   string                           `json:"ssh_password_secret_ref,omitempty"`
	SSHAllowedPaths        []string                         `json:"ssh_allowed_paths,omitempty"`
	SSHAllowedCommands     []string                         `json:"ssh_allowed_commands,omitempty"`
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

type RequirementReaderAgent interface {
	ReadRequirements(ctx context.Context, project *model.ProjectContext) (*model.RequirementBrief, error)
}

type CodeReaderAgent interface {
	ReadCode(ctx context.Context, project *model.ProjectContext, brief *model.RequirementBrief) ([]model.CodeUnderstandingSnapshot, error)
}

type PageReaderAgent interface {
	ReadPages(ctx context.Context, project *model.ProjectContext, brief *model.RequirementBrief) ([]model.PageUnderstandingSnapshot, error)
}

type MultimodalUnderstandingAgent interface {
	BuildUnderstanding(ctx context.Context, project *model.ProjectContext, brief *model.RequirementBrief, codeSnapshots []model.CodeUnderstandingSnapshot, pageSnapshots []model.PageUnderstandingSnapshot) (*model.MultimodalUnderstandingReport, error)
}

type ProductMapAgent interface {
	ExploreProduct(ctx context.Context, project *model.ProjectContext, report *model.MultimodalUnderstandingReport) (*model.ProductMap, error)
}

type GraphBuilderAgent interface {
	GenerateGraph(ctx context.Context, project *model.ProjectContext, productMap *model.ProductMap, report *model.MultimodalUnderstandingReport) (*model.DemoWorkflowGraph, error)
}

type ScriptPackagerAgent interface {
	PackageScript(ctx context.Context, project *model.ProjectContext, report *model.MultimodalUnderstandingReport, productMap *model.ProductMap, graph *model.DemoWorkflowGraph) (*model.ScriptDocumentPackage, error)
}

type QAExecutorAgent interface {
	ExecuteAndRehearse(ctx context.Context, graph *model.DemoWorkflowGraph) (RehearsalResult, error)
}

type AssetGeneratorAgent interface {
	GenerateAssets(ctx context.Context, graph *model.DemoWorkflowGraph, result RehearsalResult) (*GeneratedArtifacts, error)
}

type Dependencies struct {
	InputContext      InputContextAgent
	RequirementReader RequirementReaderAgent
	CodeReader        CodeReaderAgent
	PageReader        PageReaderAgent
	Understanding     MultimodalUnderstandingAgent
	ProductMap        ProductMapAgent
	GraphBuilder      GraphBuilderAgent
	ScriptPackager    ScriptPackagerAgent
	QAExecutor        QAExecutorAgent
	AssetGenerator    AssetGeneratorAgent
}

type CascadeFlow struct {
	deps Dependencies
}

func NewCascadeFlow(deps Dependencies) (*CascadeFlow, error) {
	if deps.InputContext == nil {
		return nil, errors.New("missing InputContext agent")
	}
	if deps.RequirementReader == nil {
		return nil, errors.New("missing RequirementReader agent")
	}
	if deps.CodeReader == nil {
		return nil, errors.New("missing CodeReader agent")
	}
	if deps.PageReader == nil {
		return nil, errors.New("missing PageReader agent")
	}
	if deps.Understanding == nil {
		return nil, errors.New("missing MultimodalUnderstanding agent")
	}
	if deps.ProductMap == nil {
		return nil, errors.New("missing ProductMap agent")
	}
	if deps.GraphBuilder == nil {
		return nil, errors.New("missing GraphBuilder agent")
	}
	if deps.ScriptPackager == nil {
		return nil, errors.New("missing ScriptPackager agent")
	}
	if deps.QAExecutor == nil {
		return nil, errors.New("missing QAExecutor agent")
	}
	if deps.AssetGenerator == nil {
		return nil, errors.New("missing AssetGenerator agent")
	}
	return &CascadeFlow{deps: deps}, nil
}

// Start runs local understanding, graph generation, and script packaging, then
// intentionally stops at HumanApprove.
// The frontend should render/edit the graph and script, then call ApproveAndContinue.
func (f *CascadeFlow) Start(ctx context.Context, input UserInput) (*CascadeState, error) {
	state := &CascadeState{Status: FlowStatusRunning}

	state.CurrentNode = NodeInputCtx
	project, err := f.deps.InputContext.BuildProjectContext(ctx, input)
	if err != nil {
		return fail(state, err), err
	}
	state.ProjectID = project.ID
	state.ProjectContext = project

	state.CurrentNode = NodeRequirementRead
	brief, err := f.deps.RequirementReader.ReadRequirements(ctx, project)
	if err != nil {
		return fail(state, err), err
	}
	state.RequirementBrief = brief

	state.CurrentNode = NodeCodeRead
	codeSnapshots, err := f.deps.CodeReader.ReadCode(ctx, project, brief)
	if err != nil {
		return fail(state, err), err
	}
	state.CodeSnapshots = codeSnapshots

	state.CurrentNode = NodePageRead
	pageSnapshots, err := f.deps.PageReader.ReadPages(ctx, project, brief)
	if err != nil {
		return fail(state, err), err
	}
	state.PageSnapshots = pageSnapshots

	state.CurrentNode = NodeMultimodalUnderstand
	report, err := f.deps.Understanding.BuildUnderstanding(ctx, project, brief, codeSnapshots, pageSnapshots)
	if err != nil {
		return fail(state, err), err
	}
	state.UnderstandingReport = report

	state.CurrentNode = NodeProductExplore
	productMap, err := f.deps.ProductMap.ExploreProduct(ctx, project, report)
	if err != nil {
		return fail(state, err), err
	}
	state.ProductMap = productMap

	state.CurrentNode = NodeGraphGenerate
	graph, err := f.deps.GraphBuilder.GenerateGraph(ctx, project, productMap, report)
	if err != nil {
		return fail(state, err), err
	}
	state.WorkflowGraph = graph

	state.CurrentNode = NodeScriptPackage
	scriptPackage, err := f.deps.ScriptPackager.PackageScript(ctx, project, report, productMap, graph)
	if err != nil {
		return fail(state, err), err
	}
	state.ScriptDocument = scriptPackage.Document
	state.ScriptMarkdown = scriptPackage.Markdown
	state.ScriptMarkdownArtifact = scriptPackage.MarkdownArtifact
	if scriptPackage.MarkdownArtifact != nil {
		state.ScriptMarkdownPath = scriptPackage.MarkdownArtifact.URI
	}

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
