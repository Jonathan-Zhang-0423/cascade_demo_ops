package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"cascade-demoops/backend/internal/model"
)

// MVP state nodes. Keep this order strict until the first product loop is stable.
type NodeName string

const (
	NodeInputCtx             NodeName = "InputCtx"
	NodeRequirementRead      NodeName = "RequirementRead"
	NodeCodeRead             NodeName = "CodeRead"
	NodePageRead             NodeName = "PageRead"
	NodeProjectIntelligence  NodeName = "ProjectIntelligence"
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
	ProjectID                string                                 `json:"project_id"`
	CurrentNode              NodeName                               `json:"current_node"`
	Status                   FlowStatus                             `json:"status"`
	ProjectContext           *model.ProjectContext                  `json:"project_context,omitempty"`
	RequirementBrief         *model.RequirementBrief                `json:"requirement_brief,omitempty"`
	CodeSnapshots            []model.CodeUnderstandingSnapshot      `json:"code_snapshots,omitempty"`
	PageSnapshots            []model.PageUnderstandingSnapshot      `json:"page_snapshots,omitempty"`
	ProjectIntelligence      *model.ProjectIntelligencePack         `json:"project_intelligence,omitempty"`
	ScriptReadinessReport    *model.ScriptReadinessReport           `json:"script_readiness_report,omitempty"`
	AgentGraphTrace          *model.AgentGraphTrace                 `json:"agent_graph_trace,omitempty"`
	UnderstandingReport      *model.MultimodalUnderstandingReport   `json:"understanding_report,omitempty"`
	ProductMap               *model.ProductMap                      `json:"product_map,omitempty"`
	WorkflowGraph            *model.DemoWorkflowGraph               `json:"workflow_graph,omitempty"`
	ScriptDocument           *model.ExecutionScriptDocument         `json:"script_document,omitempty"`
	ScriptMarkdown           string                                 `json:"script_markdown,omitempty"`
	ScriptMarkdownPath       string                                 `json:"script_markdown_path,omitempty"`
	ScriptMarkdownArtifact   *model.ArtifactRef                     `json:"script_markdown_artifact,omitempty"`
	ExecutableScriptBundle   *model.ExecutableRecordingScriptBundle `json:"executable_script_bundle,omitempty"`
	ExecutableScriptArtifact *model.ArtifactRef                     `json:"executable_script_artifact,omitempty"`
	ApprovalMarkdownArtifact *model.ArtifactRef                     `json:"approval_markdown_artifact,omitempty"`
	Approved                 bool                                   `json:"approved"`
	RehearsePassRate         float64                                `json:"rehearse_pass_rate"`
	Artifacts                *GeneratedArtifacts                    `json:"artifacts,omitempty"`
	ErrorMessage             string                                 `json:"error_message,omitempty"`
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

type ProjectIntelligenceAgent interface {
	RunProjectIntelligence(ctx context.Context, project *model.ProjectContext, brief *model.RequirementBrief, codeSnapshots []model.CodeUnderstandingSnapshot, pageSnapshots []model.PageUnderstandingSnapshot) (*model.ProjectIntelligencePack, *model.ScriptReadinessReport, *model.AgentGraphTrace, error)
}

type MultimodalUnderstandingAgent interface {
	BuildUnderstanding(ctx context.Context, project *model.ProjectContext, brief *model.RequirementBrief, codeSnapshots []model.CodeUnderstandingSnapshot, pageSnapshots []model.PageUnderstandingSnapshot, intelligence *model.ProjectIntelligencePack) (*model.MultimodalUnderstandingReport, error)
}

type ProductMapAgent interface {
	ExploreProduct(ctx context.Context, project *model.ProjectContext, report *model.MultimodalUnderstandingReport, intelligence *model.ProjectIntelligencePack) (*model.ProductMap, error)
}

type GraphBuilderAgent interface {
	GenerateGraph(ctx context.Context, project *model.ProjectContext, productMap *model.ProductMap, report *model.MultimodalUnderstandingReport, intelligence *model.ProjectIntelligencePack) (*model.DemoWorkflowGraph, error)
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
	InputContext        InputContextAgent
	RequirementReader   RequirementReaderAgent
	CodeReader          CodeReaderAgent
	PageReader          PageReaderAgent
	ProjectIntelligence ProjectIntelligenceAgent
	Understanding       MultimodalUnderstandingAgent
	ProductMap          ProductMapAgent
	GraphBuilder        GraphBuilderAgent
	ScriptPackager      ScriptPackagerAgent
	QAExecutor          QAExecutorAgent
	AssetGenerator      AssetGeneratorAgent
}

type ProgressLevel string

const (
	ProgressLevelInfo    ProgressLevel = "info"
	ProgressLevelSuccess ProgressLevel = "success"
	ProgressLevelWarning ProgressLevel = "warning"
	ProgressLevelError   ProgressLevel = "error"
)

type ProgressEvent struct {
	Level     ProgressLevel `json:"level"`
	Node      NodeName      `json:"node,omitempty"`
	Message   string        `json:"message"`
	Detail    string        `json:"detail,omitempty"`
	ElapsedMS int64         `json:"elapsed_ms,omitempty"`
}

type progressSinkKey struct{}

type ProgressSink func(ProgressEvent)

func WithProgressSink(ctx context.Context, sink ProgressSink) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, progressSinkKey{}, sink)
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
	if deps.ProjectIntelligence == nil {
		return nil, errors.New("missing ProjectIntelligence agent")
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
	startedAt := time.Now()
	log.Printf("cascade_flow start product_url_present=%t local_repo_present=%t target_audience=%q", input.ProductURL != "", input.LocalRepoPath != "", input.TargetAudience)
	emitProgress(ctx, ProgressEvent{
		Level:   ProgressLevelInfo,
		Message: "开始生成执行包",
		Detail:  "已收到产品 URL、项目目录和演示需求，开始本地理解链路。",
	})

	state.CurrentNode = NodeInputCtx
	nodeStart := logNodeStart(ctx, state.CurrentNode)
	project, err := f.deps.InputContext.BuildProjectContext(ctx, input)
	if err != nil {
		logNodeError(ctx, state.CurrentNode, nodeStart, err)
		return fail(state, err), err
	}
	state.ProjectID = project.ID
	state.ProjectContext = project
	logNodeDone(ctx, state.CurrentNode, nodeStart, "InputContextAgent 完成上下文整理", "project_id="+project.ID)

	state.CurrentNode = NodeRequirementRead
	nodeStart = logNodeStart(ctx, state.CurrentNode)
	brief, err := f.deps.RequirementReader.ReadRequirements(ctx, project)
	if err != nil {
		logNodeError(ctx, state.CurrentNode, nodeStart, err)
		return fail(state, err), err
	}
	state.RequirementBrief = brief
	logNodeDone(ctx, state.CurrentNode, nodeStart, "RequirementReaderAgent 完成需求理解", fmt.Sprintf("scenario=%q use_cases=%d", brief.Scenario, len(brief.UseCases)))

	state.CurrentNode = NodeCodeRead
	nodeStart = logNodeStart(ctx, state.CurrentNode)
	codeSnapshots, err := f.deps.CodeReader.ReadCode(ctx, project, brief)
	if err != nil {
		logNodeError(ctx, state.CurrentNode, nodeStart, err)
		return fail(state, err), err
	}
	state.CodeSnapshots = codeSnapshots
	logNodeDone(ctx, state.CurrentNode, nodeStart, "CodeReaderAgent 完成只读代码摘要", fmt.Sprintf("snapshots=%d files=%d", len(codeSnapshots), codeFileCount(codeSnapshots)))

	state.CurrentNode = NodePageRead
	nodeStart = logNodeStart(ctx, state.CurrentNode)
	pageSnapshots, err := f.deps.PageReader.ReadPages(ctx, project, brief)
	if err != nil {
		logNodeError(ctx, state.CurrentNode, nodeStart, err)
		return fail(state, err), err
	}
	state.PageSnapshots = pageSnapshots
	logNodeDone(ctx, state.CurrentNode, nodeStart, "PageReaderAgent 完成页面材料读取", fmt.Sprintf("pages=%d", len(pageSnapshots)))

	state.CurrentNode = NodeProjectIntelligence
	nodeStart = logNodeStart(ctx, state.CurrentNode)
	intelligence, readiness, trace, err := f.deps.ProjectIntelligence.RunProjectIntelligence(ctx, project, brief, codeSnapshots, pageSnapshots)
	if err != nil {
		logNodeError(ctx, state.CurrentNode, nodeStart, err)
		return fail(state, err), err
	}
	state.ProjectIntelligence = intelligence
	state.ScriptReadinessReport = readiness
	state.AgentGraphTrace = trace
	moduleCount := 0
	featureCount := 0
	surfaceCount := 0
	if intelligence != nil {
		if intelligence.Architecture != nil {
			moduleCount = len(intelligence.Architecture.Modules)
		}
		featureCount = len(intelligence.FeatureCapabilities)
		surfaceCount = len(intelligence.InteractionSurfaces)
	}
	logNodeDone(ctx, state.CurrentNode, nodeStart, "ProjectIntelligenceGraph 完成项目图谱理解", fmt.Sprintf("modules=%d capabilities=%d surfaces=%d", moduleCount, featureCount, surfaceCount))

	state.CurrentNode = NodeMultimodalUnderstand
	nodeStart = logNodeStart(ctx, state.CurrentNode)
	report, err := f.deps.Understanding.BuildUnderstanding(ctx, project, brief, codeSnapshots, pageSnapshots, intelligence)
	if err != nil {
		logNodeError(ctx, state.CurrentNode, nodeStart, err)
		return fail(state, err), err
	}
	state.UnderstandingReport = report
	logNodeDone(ctx, state.CurrentNode, nodeStart, "MultimodalUnderstandingAgent 完成融合理解", fmt.Sprintf("features=%d workflows=%d", len(report.FeatureHypotheses), len(report.WorkflowCandidates)))

	state.CurrentNode = NodeProductExplore
	nodeStart = logNodeStart(ctx, state.CurrentNode)
	productMap, err := f.deps.ProductMap.ExploreProduct(ctx, project, report, intelligence)
	if err != nil {
		logNodeError(ctx, state.CurrentNode, nodeStart, err)
		return fail(state, err), err
	}
	state.ProductMap = productMap
	logNodeDone(ctx, state.CurrentNode, nodeStart, "ProductMapAgent 完成产品地图生成", fmt.Sprintf("pages=%d features=%d workflows=%d", len(productMap.Pages), len(productMap.Features), len(productMap.Workflows)))

	state.CurrentNode = NodeGraphGenerate
	nodeStart = logNodeStart(ctx, state.CurrentNode)
	graph, err := f.deps.GraphBuilder.GenerateGraph(ctx, project, productMap, report, intelligence)
	if err != nil {
		logNodeError(ctx, state.CurrentNode, nodeStart, err)
		return fail(state, err), err
	}
	state.WorkflowGraph = graph
	logNodeDone(ctx, state.CurrentNode, nodeStart, "GraphBuilderAgent 完成执行图生成", fmt.Sprintf("graph_id=%s nodes=%d", graph.ID, len(graph.Nodes)))

	state.CurrentNode = NodeScriptPackage
	nodeStart = logNodeStart(ctx, state.CurrentNode)
	scriptPackage, err := f.deps.ScriptPackager.PackageScript(ctx, project, report, productMap, graph)
	if err != nil {
		logNodeError(ctx, state.CurrentNode, nodeStart, err)
		return fail(state, err), err
	}
	state.ScriptDocument = scriptPackage.Document
	state.ScriptMarkdown = scriptPackage.Markdown
	state.ScriptMarkdownArtifact = scriptPackage.MarkdownArtifact
	if scriptPackage.MarkdownArtifact != nil {
		state.ScriptMarkdownPath = scriptPackage.MarkdownArtifact.URI
	}
	state.ExecutableScriptBundle = scriptPackage.ExecutableBundle
	if scriptPackage.ExecutableBundle != nil {
		state.ExecutableScriptArtifact = scriptPackage.ExecutableBundle.PlaywrightScript.Artifact
		state.ApprovalMarkdownArtifact = scriptPackage.ExecutableBundle.ApprovalMarkdown.Artifact
	}

	state.CurrentNode = NodeHumanApprove
	state.Status = FlowStatusAwaitingHuman
	logNodeDone(ctx, NodeScriptPackage, nodeStart, "ScriptPackagerAgent 完成执行包封装", fmt.Sprintf("steps=%d bundle=%t", len(scriptPackage.Document.Steps), scriptPackage.ExecutableBundle != nil))
	log.Printf("cascade_flow awaiting_human project_id=%s elapsed_ms=%d", state.ProjectID, time.Since(startedAt).Milliseconds())
	emitProgress(ctx, ProgressEvent{
		Level:     ProgressLevelSuccess,
		Node:      NodeHumanApprove,
		Message:   "执行包已生成，等待人工审批",
		Detail:    fmt.Sprintf("project_id=%s", state.ProjectID),
		ElapsedMS: time.Since(startedAt).Milliseconds(),
	})
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

func logNodeStart(ctx context.Context, node NodeName) time.Time {
	startedAt := time.Now()
	log.Printf("cascade_flow node_start node=%s", node)
	emitProgress(ctx, ProgressEvent{
		Level:   ProgressLevelInfo,
		Node:    node,
		Message: fmt.Sprintf("开始执行 %s", node),
	})
	return startedAt
}

func logNodeDone(ctx context.Context, node NodeName, startedAt time.Time, message string, detail string) {
	elapsedMS := time.Since(startedAt).Milliseconds()
	log.Printf("cascade_flow node_done node=%s elapsed_ms=%d %s", node, elapsedMS, detail)
	emitProgress(ctx, ProgressEvent{
		Level:     ProgressLevelSuccess,
		Node:      node,
		Message:   message,
		Detail:    detail,
		ElapsedMS: elapsedMS,
	})
}

func logNodeError(ctx context.Context, node NodeName, startedAt time.Time, err error) {
	elapsedMS := time.Since(startedAt).Milliseconds()
	log.Printf("cascade_flow node_error node=%s elapsed_ms=%d error=%s", node, elapsedMS, err)
	emitProgress(ctx, ProgressEvent{
		Level:     ProgressLevelError,
		Node:      node,
		Message:   fmt.Sprintf("%s 执行失败", node),
		Detail:    err.Error(),
		ElapsedMS: elapsedMS,
	})
}

func emitProgress(ctx context.Context, event ProgressEvent) {
	if ctx == nil {
		return
	}
	sink, ok := ctx.Value(progressSinkKey{}).(ProgressSink)
	if !ok || sink == nil {
		return
	}
	sink(event)
}

func codeFileCount(snapshots []model.CodeUnderstandingSnapshot) int {
	total := 0
	for _, snapshot := range snapshots {
		total += snapshot.FileCount
	}
	return total
}
