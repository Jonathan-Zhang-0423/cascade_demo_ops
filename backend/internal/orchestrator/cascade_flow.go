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
	NodeInputCtx              NodeName = "InputCtx"
	NodeRequirementRead       NodeName = "RequirementRead"
	NodeCodeRead              NodeName = "CodeRead"
	NodePageRead              NodeName = "PageRead"
	NodeProjectIntelligence   NodeName = "ProjectIntelligence"
	NodeMultimodalUnderstand  NodeName = "MultimodalUnderstand"
	NodeProductExplore        NodeName = "ProductExplore"
	NodePageInteractionVerify NodeName = "PageInteractionVerify"
	NodeGraphGenerate         NodeName = "GraphGenerate"
	NodeScriptPackage         NodeName = "ScriptPackage"
	NodeHumanApprove          NodeName = "HumanApprove"
	NodeExecuteRehearse       NodeName = "ExecuteRehearse"
	NodeAssetGenerate         NodeName = "AssetGenerate"
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
	SourceBinding            *model.ProductSourceBindingAssessment  `json:"source_binding,omitempty"`
	ProjectIntelligence      *model.ProjectIntelligencePack         `json:"project_intelligence,omitempty"`
	ScriptReadinessReport    *model.ScriptReadinessReport           `json:"script_readiness_report,omitempty"`
	AgentGraphTrace          *model.AgentGraphTrace                 `json:"agent_graph_trace,omitempty"`
	UnderstandingReport      *model.MultimodalUnderstandingReport   `json:"understanding_report,omitempty"`
	ProductMap               *model.ProductMap                      `json:"product_map,omitempty"`
	VerifiedInteractionPlan  *model.VerifiedInteractionPlan         `json:"verified_interaction_plan,omitempty"`
	MissingEvidenceReport    *model.MissingEvidenceReport           `json:"missing_evidence_report,omitempty"`
	WorkflowGraph            *model.DemoWorkflowGraph               `json:"workflow_graph,omitempty"`
	ScriptDocument           *model.ExecutionScriptDocument         `json:"script_document,omitempty"`
	ScriptMarkdown           string                                 `json:"script_markdown,omitempty"`
	ScriptMarkdownPath       string                                 `json:"script_markdown_path,omitempty"`
	ScriptMarkdownArtifact   *model.ArtifactRef                     `json:"script_markdown_artifact,omitempty"`
	ExecutableScriptBundle   *model.ExecutableRecordingScriptBundle `json:"executable_script_bundle,omitempty"`
	ExecutableScriptArtifact *model.ArtifactRef                     `json:"executable_script_artifact,omitempty"`
	ApprovalMarkdownArtifact *model.ArtifactRef                     `json:"approval_markdown_artifact,omitempty"`
	// ExecutionPackageGeneration is incremented when a terminal Browser Agent
	// validation result requires App re-understanding. It keeps the next
	// package identity distinct from the invalidated approved package while
	// remaining optional for v1 project-state JSON.
	ExecutionPackageGeneration int                   `json:"execution_package_generation,omitempty"`
	Approved                   bool                  `json:"approved"`
	RehearsePassRate           float64               `json:"rehearse_pass_rate"`
	Artifacts                  *GeneratedArtifacts   `json:"artifacts,omitempty"`
	DesktopCloudRun            *DesktopCloudRunState `json:"desktop_cloud_run,omitempty"`
	ErrorMessage               string                `json:"error_message,omitempty"`
	ArchivedAt                 *time.Time            `json:"archived_at,omitempty"`
}

// DesktopCloudRunState is the restart-safe App view of the remote execution.
// It stores only protocol metadata and safe managed-file names, never local paths.
type DesktopCloudRunState struct {
	SchemaVersion  string     `json:"schema_version"`
	Transport      string     `json:"transport,omitempty"`
	OrgID          string     `json:"org_id,omitempty"`
	LeaseID        string     `json:"lease_id,omitempty"`
	DataPort       int        `json:"data_port,omitempty"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at,omitempty"`
	UploadID       string     `json:"upload_id,omitempty"`
	// PackageID is the authoritative Browser Agent package identity. The
	// exchange_package_id field below remains only as a v1 persisted-state
	// compatibility key for older projects.
	PackageID                   string                             `json:"package_id,omitempty"`
	ExchangePackageID           string                             `json:"exchange_package_id,omitempty"`
	CloudJobID                  string                             `json:"cloud_job_id,omitempty"`
	Status                      string                             `json:"status,omitempty"`
	Stage                       string                             `json:"stage,omitempty"`
	Message                     string                             `json:"message,omitempty"`
	WaitingReason               string                             `json:"waiting_reason,omitempty"`
	BlockingErrorCode           string                             `json:"blocking_error_code,omitempty"`
	NextAction                  string                             `json:"next_action,omitempty"`
	RequiresReapproval          bool                               `json:"requires_reapproval,omitempty"`
	ReunderstandingIssues       []model.DirectReunderstandingIssue `json:"reunderstanding_issues,omitempty"`
	ProgressPercent             int                                `json:"progress_percent,omitempty"`
	LastEventID                 string                             `json:"last_event_id,omitempty"`
	StageHistory                []model.ExecutionStageEvent        `json:"stage_history,omitempty"`
	FailureSummary              *model.ExecutionFailureSummary     `json:"failure_summary,omitempty"`
	Error                       *model.AgentError                  `json:"error,omitempty"`
	ResultPackageID             string                             `json:"result_package_id,omitempty"`
	ResultPackage               *model.RecordingResultPackage      `json:"result_package,omitempty"`
	DiagnosticDigestSHA256      string                             `json:"diagnostic_digest_sha256,omitempty"`
	ResultDownloaded            bool                               `json:"result_downloaded,omitempty"`
	AckedAt                     *time.Time                         `json:"acked_at,omitempty"`
	DownloadedAssets            []DesktopDownloadedAssetState      `json:"downloaded_assets,omitempty"`
	DirectArtifacts             []model.DirectArtifact             `json:"direct_artifacts,omitempty"`
	ResultReview                *DesktopResultReviewState          `json:"result_review,omitempty"`
	PackageDigestSHA256         string                             `json:"package_digest_sha256,omitempty"`
	GraphDigestSHA256           string                             `json:"graph_digest_sha256,omitempty"`
	ApprovalSubjectDigestSHA256 string                             `json:"approval_subject_digest_sha256,omitempty"`
	ConfidenceAssessmentHash    string                             `json:"confidence_assessment_hash,omitempty"`
	BundleHashSHA256            string                             `json:"bundle_hash_sha256,omitempty"`
	PlanHashSHA256              string                             `json:"plan_hash_sha256,omitempty"`
	LastRepairSourceID          string                             `json:"last_repair_source_result_id,omitempty"`
	RepairHistory               []DesktopDirectRepairAuditState    `json:"repair_history,omitempty"`
	UpdatedAt                   time.Time                          `json:"updated_at"`
}

type DesktopDirectRepairAuditState struct {
	SourceResultID        string                             `json:"source_result_id"`
	SourcePackageID       string                             `json:"source_package_id"`
	SourceJobID           string                             `json:"source_job_id"`
	RepairRequestID       string                             `json:"repair_request_id,omitempty"`
	IdempotencyKey        string                             `json:"idempotency_key,omitempty"`
	RequestDigestSHA256   string                             `json:"request_digest_sha256,omitempty"`
	NewPackageID          string                             `json:"new_package_id,omitempty"`
	ResultPackage         *model.RecordingResultPackage      `json:"result_package,omitempty"`
	DownloadedAssets      []DesktopDownloadedAssetState      `json:"downloaded_assets,omitempty"`
	ReunderstandingIssues []model.DirectReunderstandingIssue `json:"reunderstanding_issues,omitempty"`
	CreatedAt             time.Time                          `json:"created_at"`
}

type DesktopDownloadedAssetState struct {
	ArtifactID string `json:"artifact_id"`
	Kind       string `json:"kind,omitempty"`
	Role       string `json:"role,omitempty"`
	FileName   string `json:"file_name"`
	SHA256     string `json:"sha256"`
	MimeType   string `json:"mime_type,omitempty"`
	SizeBytes  int64  `json:"size_bytes,omitempty"`
	Verified   bool   `json:"verified"`
}

type DesktopResultReviewState struct {
	Decision          string    `json:"decision"`
	ReviewID          string    `json:"review_id,omitempty"`
	IdempotencyKey    string    `json:"idempotency_key,omitempty"`
	ReviewerInstallID string    `json:"reviewer_install_id,omitempty"`
	RevisionID        string    `json:"revision_id,omitempty"`
	RevisionAction    string    `json:"revision_action,omitempty"`
	RevisionStatus    string    `json:"revision_status,omitempty"`
	Summary           string    `json:"summary,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type GeneratedArtifacts struct {
	VideoPath          string   `json:"video_path,omitempty"`
	ScreenshotPaths    []string `json:"screenshot_paths,omitempty"`
	StepByStepDocsPath string   `json:"step_by_step_docs_path,omitempty"`
}

type UserInput struct {
	ProjectID                     string                               `json:"project_id,omitempty"`
	Mode                          model.AppMode                        `json:"mode"`
	ProductURL                    string                               `json:"product_url"`
	GitRepoURL                    string                               `json:"git_repo_url,omitempty"`
	LocalRepoPath                 string                               `json:"local_repo_path,omitempty"`
	ProductDescription            string                               `json:"product_description,omitempty"`
	TargetDurationSec             int                                  `json:"target_duration_sec,omitempty"`
	Code                          []model.CodeInput                    `json:"code,omitempty"`
	RequirementDocuments          []model.RequirementDocumentInput     `json:"requirement_documents,omitempty"`
	WebpageScreenshots            []model.WebpageScreenshotInput       `json:"webpage_screenshots,omitempty"`
	TargetAudience                string                               `json:"target_audience"`
	BrandTone                     string                               `json:"brand_tone,omitempty"`
	MustShow                      []string                             `json:"must_show,omitempty"`
	MustNotShow                   []string                             `json:"must_not_show,omitempty"`
	Requirements                  []model.DemoRequirement              `json:"requirements,omitempty"`
	PresentationGenerationIntents []model.PresentationGenerationIntent `json:"presentation_generation_intents,omitempty"`
	ForbiddenPages                []string                             `json:"forbidden_pages,omitempty"`
	ForbiddenData                 []string                             `json:"forbidden_data,omitempty"`
	AllowedDomains                []string                             `json:"allowed_domains,omitempty"`
	DemoUsername                  string                               `json:"demo_username,omitempty"`
	DemoPassword                  string                               `json:"demo_password,omitempty"`
	DemoCredentialRef             string                               `json:"demo_credential_ref,omitempty"`
	SSHHost                       string                               `json:"ssh_host,omitempty"`
	SSHPort                       int                                  `json:"ssh_port,omitempty"`
	SSHUsername                   string                               `json:"ssh_username,omitempty"`
	SSHPrivateKeySecretRef        string                               `json:"ssh_private_key_secret_ref,omitempty"`
	SSHPasswordSecretRef          string                               `json:"ssh_password_secret_ref,omitempty"`
	SSHAllowedPaths               []string                             `json:"ssh_allowed_paths,omitempty"`
	SSHAllowedCommands            []string                             `json:"ssh_allowed_commands,omitempty"`
	SourceBindingDecision         string                               `json:"source_binding_decision,omitempty"`
	SourceBindingHash             string                               `json:"source_binding_hash,omitempty"`
}

type PageVerificationCredentials struct {
	DemoUsername string
	DemoPassword string
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

type SourceBindingAgent interface {
	Assess(project *model.ProjectContext, codeSnapshots []model.CodeUnderstandingSnapshot, pageSnapshots []model.PageUnderstandingSnapshot, now time.Time) (*model.ProductSourceBindingAssessment, []model.CodeUnderstandingSnapshot, error)
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

type PageInteractionVerifierAgent interface {
	VerifyInteractions(ctx context.Context, project *model.ProjectContext, brief *model.RequirementBrief, report *model.MultimodalUnderstandingReport, productMap *model.ProductMap, intelligence *model.ProjectIntelligencePack, credentials PageVerificationCredentials) (*model.VerifiedInteractionPlan, *model.MissingEvidenceReport, error)
}

type BusinessStagePlannerAgent interface {
	PlanBusinessStages(ctx context.Context, project *model.ProjectContext, brief *model.RequirementBrief, report *model.MultimodalUnderstandingReport, productMap *model.ProductMap, intelligence *model.ProjectIntelligencePack, verifiedPlan *model.VerifiedInteractionPlan) (*model.BusinessStagePlan, error)
}

type GraphBuilderAgent interface {
	GenerateGraph(ctx context.Context, project *model.ProjectContext, productMap *model.ProductMap, report *model.MultimodalUnderstandingReport, intelligence *model.ProjectIntelligencePack) (*model.DemoWorkflowGraph, error)
}

type ScriptPackagerAgent interface {
	PackageScript(ctx context.Context, project *model.ProjectContext, report *model.MultimodalUnderstandingReport, productMap *model.ProductMap, graph *model.DemoWorkflowGraph, intelligence ...*model.ProjectIntelligencePack) (*model.ScriptDocumentPackage, error)
}

type QAExecutorAgent interface {
	ExecuteAndRehearse(ctx context.Context, graph *model.DemoWorkflowGraph) (RehearsalResult, error)
}

type AssetGeneratorAgent interface {
	GenerateAssets(ctx context.Context, graph *model.DemoWorkflowGraph, result RehearsalResult) (*GeneratedArtifacts, error)
}

type Dependencies struct {
	InputContext         InputContextAgent
	RequirementReader    RequirementReaderAgent
	CodeReader           CodeReaderAgent
	PageReader           PageReaderAgent
	SourceBinding        SourceBindingAgent
	ProjectIntelligence  ProjectIntelligenceAgent
	Understanding        MultimodalUnderstandingAgent
	ProductMap           ProductMapAgent
	PageVerifier         PageInteractionVerifierAgent
	BusinessStagePlanner BusinessStagePlannerAgent
	GraphBuilder         GraphBuilderAgent
	ScriptPackager       ScriptPackagerAgent
	QAExecutor           QAExecutorAgent
	AssetGenerator       AssetGeneratorAgent
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
	if deps.SourceBinding == nil {
		return nil, errors.New("missing SourceBinding agent")
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
	if deps.PageVerifier == nil {
		return nil, errors.New("missing PageInteractionVerifier agent")
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

	assessment, effectiveCodeSnapshots, err := f.deps.SourceBinding.Assess(project, codeSnapshots, pageSnapshots, time.Now().UTC())
	state.SourceBinding = assessment
	project.SourceBinding = assessment
	if err != nil {
		logNodeError(ctx, NodeProjectIntelligence, nodeStart, err)
		return fail(state, err), err
	}

	state.CurrentNode = NodeProjectIntelligence
	nodeStart = logNodeStart(ctx, state.CurrentNode)
	intelligence, readiness, trace, err := f.deps.ProjectIntelligence.RunProjectIntelligence(ctx, project, brief, effectiveCodeSnapshots, pageSnapshots)
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
	report, err := f.deps.Understanding.BuildUnderstanding(ctx, project, brief, effectiveCodeSnapshots, pageSnapshots, intelligence)
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
	if f.deps.BusinessStagePlanner != nil {
		preScanStagePlan, planErr := f.deps.BusinessStagePlanner.PlanBusinessStages(ctx, project, brief, report, productMap, intelligence, nil)
		if planErr != nil {
			logNodeError(ctx, NodePageInteractionVerify, nodeStart, planErr)
			return fail(state, planErr), planErr
		}
		if intelligence != nil {
			intelligence.BusinessStagePlan = preScanStagePlan
		}
	}

	state.CurrentNode = NodePageInteractionVerify
	nodeStart = logNodeStart(ctx, state.CurrentNode)
	verifiedPlan, missingReport, err := f.deps.PageVerifier.VerifyInteractions(ctx, project, brief, report, productMap, intelligence, PageVerificationCredentials{
		DemoUsername: input.DemoUsername,
		DemoPassword: input.DemoPassword,
	})
	if err != nil {
		logNodeError(ctx, state.CurrentNode, nodeStart, err)
		return fail(state, err), err
	}
	state.VerifiedInteractionPlan = verifiedPlan
	state.MissingEvidenceReport = missingReport
	if intelligence != nil {
		intelligence.VerifiedInteraction = verifiedPlan
		intelligence.MissingEvidenceReport = missingReport
	}
	var businessStagePlan *model.BusinessStagePlan
	if f.deps.BusinessStagePlanner != nil {
		businessStagePlan, err = f.deps.BusinessStagePlanner.PlanBusinessStages(ctx, project, brief, report, productMap, intelligence, verifiedPlan)
		if err != nil {
			logNodeError(ctx, state.CurrentNode, nodeStart, err)
			return fail(state, err), err
		}
		if intelligence != nil {
			intelligence.BusinessStagePlan = businessStagePlan
		}
	}
	if missingReport != nil && missingReport.Blocking && !businessStagePlanUsable(businessStagePlan) {
		markReadinessBlockedByMissingEvidence(readiness, missingReport)
		summary := missingReport.Summary
		if summary == "" {
			summary = "没有页面验证过的业务动作"
		}
		err := fmt.Errorf("missing verified interaction evidence: %s", summary)
		logNodeError(ctx, state.CurrentNode, nodeStart, err)
		return fail(state, err), err
	}
	verifiedCount := 0
	if verifiedPlan != nil {
		verifiedCount = len(verifiedPlan.Actions)
	}
	logNodeDone(ctx, state.CurrentNode, nodeStart, "PageInteractionVerifierAgent 完成页面交互预验证", fmt.Sprintf("verified_actions=%d missing=%t", verifiedCount, missingReport != nil))

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
	scriptPackage, err := f.deps.ScriptPackager.PackageScript(ctx, project, report, productMap, graph, intelligence)
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

func businessStagePlanUsable(plan *model.BusinessStagePlan) bool {
	if plan == nil || len(plan.Stages) == 0 {
		return false
	}
	coreCount := 0
	for _, stage := range plan.Stages {
		switch stage.Kind {
		case model.BusinessStageKindBusinessAction, model.BusinessStageKindBusinessInput, model.BusinessStageKindModeSelection, model.BusinessStageKindBusinessSubmit:
			coreCount++
			if !businessStageHasVerifiedPageEvidence(stage) {
				return false
			}
		}
	}
	return coreCount > 0
}

func businessStageHasVerifiedPageEvidence(stage model.BusinessStage) bool {
	for _, target := range stage.Targets {
		if !target.IsVerified {
			continue
		}
		refs := append([]model.EvidenceRef{}, target.EvidenceRefs...)
		for _, candidate := range target.Alternatives {
			refs = append(refs, candidate.EvidenceRefs...)
		}
		for _, ref := range refs {
			switch ref.Kind {
			case model.EvidenceKindBrowserScan, model.EvidenceKindBrowserTrace, model.EvidenceKindWebScreenshot:
				return true
			}
		}
	}
	return false
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

// RepackageReviewedGraph persists a reviewed graph revision and rebuilds all
// digest-bound package documents without entering the legacy rehearsal path.
func (f *CascadeFlow) RepackageReviewedGraph(ctx context.Context, state *CascadeState, graph *model.DemoWorkflowGraph) (*CascadeState, error) {
	if state == nil || state.ProjectContext == nil {
		return nil, errors.New("project context is missing")
	}
	if graph == nil {
		return nil, errors.New("workflow graph is missing")
	}
	pkg, err := f.deps.ScriptPackager.PackageScript(ctx, state.ProjectContext, state.UnderstandingReport, state.ProductMap, graph, state.ProjectIntelligence)
	if err != nil {
		return state, err
	}
	state.WorkflowGraph = graph
	state.ScriptDocument = pkg.Document
	state.ScriptMarkdown = pkg.Markdown
	state.ScriptMarkdownArtifact = pkg.MarkdownArtifact
	state.ScriptMarkdownPath = ""
	if pkg.MarkdownArtifact != nil {
		state.ScriptMarkdownPath = pkg.MarkdownArtifact.URI
	}
	state.ExecutableScriptBundle = pkg.ExecutableBundle
	state.ExecutableScriptArtifact = nil
	state.ApprovalMarkdownArtifact = nil
	if pkg.ExecutableBundle != nil {
		state.ExecutableScriptArtifact = pkg.ExecutableBundle.PlaywrightScript.Artifact
		state.ApprovalMarkdownArtifact = pkg.ExecutableBundle.ApprovalMarkdown.Artifact
	}
	state.CurrentNode = NodeHumanApprove
	state.Status = FlowStatusAwaitingHuman
	state.Approved = false
	state.RehearsePassRate = 0
	state.Artifacts = nil
	state.DesktopCloudRun = nil
	state.ErrorMessage = ""
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

func markReadinessBlockedByMissingEvidence(readiness *model.ScriptReadinessReport, report *model.MissingEvidenceReport) {
	if readiness == nil || report == nil {
		return
	}
	readiness.CanProceed = false
	readiness.Summary = "页面交互验证存在阻塞，需要补齐证据后再生成脚本。"
	readiness.Blockers = append(readiness.Blockers, model.AgentFinding{
		ID:              "readiness_missing_verified_interaction",
		Kind:            "missing_verified_interaction",
		Severity:        model.FindingSeverityBlocking,
		Summary:         report.Summary,
		SuggestedAction: "补充真实页面预扫描、截图标注或稳定 selector 后重新生成执行包。",
		Confidence:      0.9,
	})
	for _, item := range report.Items {
		if item.SuggestedAction != "" {
			readiness.RepairSuggestions = append(readiness.RepairSuggestions, item.SuggestedAction)
		}
	}
}

func codeFileCount(snapshots []model.CodeUnderstandingSnapshot) int {
	total := 0
	for _, snapshot := range snapshots {
		total += snapshot.FileCount
	}
	return total
}
