package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/llm"
	"cascade-demoops/backend/internal/model"
)

type ProjectUnderstandingTool interface {
	Name() string
	Execute(ctx context.Context, state *ProjectUnderstandingState) (ToolPatch, error)
}

type ToolPatch struct {
	OutputSummary string
	ChangedFields []string
	Confidence    float64
	EvidenceRefs  []model.EvidenceRef
}

type ProjectUnderstandingState struct {
	Project            *model.ProjectContext
	Brief              *model.RequirementBrief
	CodeSnapshots      []model.CodeUnderstandingSnapshot
	PageSnapshots      []model.PageUnderstandingSnapshot
	Pack               *model.ProjectIntelligencePack
	InvestigationTools *ProjectInvestigationToolSuite
}

type ProjectIntelligenceGraph struct {
	llm                llm.Client
	tools              []ProjectUnderstandingTool
	investigationTools *ProjectInvestigationToolSuite
}

func NewProjectIntelligenceGraph() *ProjectIntelligenceGraph {
	return &ProjectIntelligenceGraph{tools: defaultProjectUnderstandingTools(), investigationTools: NewProjectInvestigationToolSuite(nil)}
}

func NewProjectIntelligenceGraphWithLLM(client llm.Client) *ProjectIntelligenceGraph {
	return &ProjectIntelligenceGraph{llm: client, tools: defaultProjectUnderstandingTools(), investigationTools: NewProjectInvestigationToolSuite(client)}
}

func (g *ProjectIntelligenceGraph) RunProjectIntelligence(
	ctx context.Context,
	project *model.ProjectContext,
	brief *model.RequirementBrief,
	codeSnapshots []model.CodeUnderstandingSnapshot,
	pageSnapshots []model.PageUnderstandingSnapshot,
) (*model.ProjectIntelligencePack, *model.ScriptReadinessReport, *model.AgentGraphTrace, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, err
	}
	if project == nil {
		return nil, nil, nil, errors.New("project context is required")
	}
	now := time.Now().UTC()
	pack := &model.ProjectIntelligencePack{
		ID:                 "project_intelligence_" + project.ID,
		ProjectID:          project.ID,
		SchemaVersion:      model.ProjectIntelligencePackSchemaVersion,
		RunIntentScope:     runIntentScopeForProject(project),
		InputFingerprints:  inputFingerprints(project, codeSnapshots, pageSnapshots),
		SourceDigestSHA256: combinedSourceDigest(codeSnapshots),
		SourceBinding:      project.SourceBinding,
		Confidence:         0.74,
		CreatedAt:          now,
	}
	state := &ProjectUnderstandingState{
		Project:            project,
		Brief:              brief,
		CodeSnapshots:      append([]model.CodeUnderstandingSnapshot{}, codeSnapshots...),
		PageSnapshots:      append([]model.PageUnderstandingSnapshot{}, pageSnapshots...),
		Pack:               pack,
		InvestigationTools: g.investigationToolSuite(),
	}
	trace := &model.AgentGraphTrace{
		ID:            "agent_graph_trace_" + project.ID,
		ProjectID:     project.ID,
		SchemaVersion: model.AgentGraphTraceSchemaVersion,
		GraphName:     "ProjectIntelligenceGraph",
		StartedAt:     now,
	}
	tools := g.tools
	if len(tools) == 0 {
		tools = defaultProjectUnderstandingTools()
	}
	for _, tool := range tools {
		tool := tool
		err := traceGraphStep(ctx, trace, safeID("node", tool.Name()), "", tool.Name(), graphToolInputSummary(state), func() (string, float64, []model.EvidenceRef, string, error) {
			patch, err := tool.Execute(ctx, state)
			return patch.OutputSummary, patch.Confidence, patch.EvidenceRefs, "", err
		})
		if err != nil {
			trace.CompletedAt = time.Now().UTC()
			trace.Summary = "ProjectIntelligenceGraph 执行失败：" + err.Error()
			return nil, nil, trace, err
		}
	}
	for _, step := range agentSynthesisTraceSteps(state) {
		step := step
		err := traceGraphStep(ctx, trace, step.nodeID, step.agent, "", step.inputSummary, func() (string, float64, []model.EvidenceRef, string, error) {
			return step.outputSummary, step.confidence, state.Pack.EvidenceRefs, "", nil
		})
		if err != nil {
			trace.CompletedAt = time.Now().UTC()
			trace.Summary = "ProjectIntelligenceGraph 执行失败：" + err.Error()
			return nil, nil, trace, err
		}
	}
	modelTrace, err := g.enhancePackWithLLM(ctx, project, brief, pack)
	if err != nil && !llm.IsDeterministicFallback(err) {
		trace.CompletedAt = time.Now().UTC()
		trace.Summary = "ProjectIntelligenceGraph 模型增强失败：" + err.Error()
		return nil, nil, trace, err
	}
	if modelTrace != nil {
		fallback := modelTrace.FallbackReason
		output := "Kimi 方案规划模型参与项目理解图谱润色。"
		if fallback != "" {
			output = "模型增强不可用，保留确定性 ProjectIntelligencePack。"
		}
		_ = traceGraphStep(ctx, trace, "llm_project_intelligence_refine", "DemoDirectorAgent", "", "summary-only intelligence pack", func() (string, float64, []model.EvidenceRef, string, error) {
			return output, 0.7, nil, fallback, nil
		})
		pack.EvidenceRefs = append(pack.EvidenceRefs, model.EvidenceRef{
			ID:         "ev_model_project_intelligence_" + shortHash(modelTrace.Label()),
			Kind:       model.EvidenceKindUserInput,
			Summary:    "ProjectIntelligenceGraph 模型路由：" + modelTrace.Label(),
			FieldPath:  "model_trace.project_intelligence",
			Confidence: 0.68,
		})
	}
	finalizeProjectIntelligencePack(pack)
	trace.CompletedAt = time.Now().UTC()
	trace.Summary = fmt.Sprintf("项目理解图谱完成：模块=%d，功能=%d，交互面=%d，候选路径=%d。", len(pack.Architecture.Modules), len(pack.FeatureCapabilities), len(pack.InteractionSurfaces), len(pack.DemoScenarioPlans))
	project.ProjectIntelligence = pack
	project.KnowledgeRefs = append(project.KnowledgeRefs, pack.EvidenceRefs...)
	return pack, pack.ScriptReadinessReport, trace, nil
}

func (g *ProjectIntelligenceGraph) investigationToolSuite() *ProjectInvestigationToolSuite {
	if g == nil {
		return NewProjectInvestigationToolSuite(nil)
	}
	if g.investigationTools != nil {
		if g.investigationTools.llm == nil && g.llm != nil {
			g.investigationTools.llm = g.llm
		}
		return g.investigationTools
	}
	g.investigationTools = NewProjectInvestigationToolSuite(g.llm)
	return g.investigationTools
}

type projectUnderstandingToolFunc struct {
	name string
	run  func(ctx context.Context, state *ProjectUnderstandingState) (ToolPatch, error)
}

func (t projectUnderstandingToolFunc) Name() string { return t.name }

func (t projectUnderstandingToolFunc) Execute(ctx context.Context, state *ProjectUnderstandingState) (ToolPatch, error) {
	if err := ctx.Err(); err != nil {
		return ToolPatch{}, err
	}
	return t.run(ctx, state)
}

func defaultProjectUnderstandingTools() []ProjectUnderstandingTool {
	return []ProjectUnderstandingTool{
		projectUnderstandingToolFunc{name: "IntentParseTool", run: runDemoIntentTool},
		projectUnderstandingToolFunc{name: "RepoIndexTool", run: runRepoIndexTool},
		projectUnderstandingToolFunc{name: "RouteDrilldownTool", run: runRouteMapTool},
		projectUnderstandingToolFunc{name: "ComponentDrilldownTool", run: runComponentMapTool},
		projectUnderstandingToolFunc{name: "StyleDrilldownTool", run: runStyleDrilldownTool},
		projectUnderstandingToolFunc{name: "APIBackendDrilldownTool", run: runAPIContractTool},
		projectUnderstandingToolFunc{name: "DataModelDrilldownTool", run: runDataModelTool},
		projectUnderstandingToolFunc{name: "SensitiveSurfaceTool", run: runSensitiveSurfaceTool},
		projectUnderstandingToolFunc{name: "FeatureTraceTool", run: runFeatureTraceTool},
		projectUnderstandingToolFunc{name: "ScriptFeasibilityTool", run: runScriptFeasibilityTool},
	}
}

func runRepoIndexTool(_ context.Context, state *ProjectUnderstandingState) (ToolPatch, error) {
	architecture := buildArchitectureMap(state.Project, state.CodeSnapshots, state.PageSnapshots)
	state.Pack.Architecture = architecture
	state.Pack.EvidenceRefs = append(state.Pack.EvidenceRefs, architecture.EvidenceRefs...)
	return ToolPatch{
		OutputSummary: fmt.Sprintf("识别仓库=%d、框架=%d、模块=%d、入口=%d。", architecture.RepositoryCount, len(architecture.Frameworks), len(architecture.Modules), len(architecture.EntryPointHashes)),
		ChangedFields: []string{"architecture"},
		Confidence:    architecture.Confidence,
		EvidenceRefs:  architecture.EvidenceRefs,
	}, nil
}

func runRouteMapTool(ctx context.Context, state *ProjectUnderstandingState) (ToolPatch, error) {
	if state.Pack.Architecture == nil {
		state.Pack.Architecture = buildArchitectureMap(state.Project, state.CodeSnapshots, state.PageSnapshots)
	}
	drillRefs, drillFiles, err := runProjectUnderstandingFocusedInvestigation(ctx, state, "route_drilldown", []string{"route", "router", "page", "workspace", "dashboard", "project", "new project", "create project"})
	if err != nil {
		return ToolPatch{}, err
	}
	state.Pack.Architecture.RouteTree = routeTreeFromSnapshots(state.CodeSnapshots, state.PageSnapshots)
	state.Pack.EvidenceRefs = append(state.Pack.EvidenceRefs, drillRefs...)
	return ToolPatch{
		OutputSummary: fmt.Sprintf("提取路由/页面路径 %d 个；工具化 route drilldown 读取 %d 个文件。", len(state.Pack.Architecture.RouteTree), drillFiles),
		ChangedFields: []string{"architecture.route_tree"},
		Confidence:    0.72,
		EvidenceRefs:  uniqueEvidenceRefs(append(state.Pack.Architecture.EvidenceRefs, drillRefs...)),
	}, nil
}

func runComponentMapTool(ctx context.Context, state *ProjectUnderstandingState) (ToolPatch, error) {
	drillRefs, drillFiles, err := runProjectUnderstandingFocusedInvestigation(ctx, state, "component_drilldown", []string{"component", "button", "form", "input", "dialog", "modal", "data-testid", "role", "new project", "build mode"})
	if err != nil {
		return ToolPatch{}, err
	}
	surfaces := interactionSurfacesFromSnapshots(state.Project, state.CodeSnapshots, state.PageSnapshots)
	state.Pack.InteractionSurfaces = surfaces
	for _, surface := range surfaces {
		state.Pack.EvidenceRefs = append(state.Pack.EvidenceRefs, surface.EvidenceRefs...)
	}
	state.Pack.EvidenceRefs = append(state.Pack.EvidenceRefs, drillRefs...)
	return ToolPatch{
		OutputSummary: fmt.Sprintf("识别页面交互面 %d 个；工具化 component drilldown 读取 %d 个文件。", len(surfaces), drillFiles),
		ChangedFields: []string{"interaction_surfaces"},
		Confidence:    0.72,
		EvidenceRefs:  uniqueEvidenceRefs(append(state.Pack.EvidenceRefs, drillRefs...)),
	}, nil
}

func runStyleDrilldownTool(_ context.Context, state *ProjectUnderstandingState) (ToolPatch, error) {
	styleRefs := []model.EvidenceRef{}
	styleFiles := 0
	for _, snapshot := range state.CodeSnapshots {
		for _, digest := range snapshot.PathDigests {
			if digest.Kind == "style" {
				styleFiles++
			}
		}
		styleRefs = append(styleRefs, snapshot.EvidenceRefs...)
	}
	return ToolPatch{
		OutputSummary: fmt.Sprintf("需求相关样式/视觉定义文件摘要 %d 个，仅保存 hash 与结构证据。", styleFiles),
		ChangedFields: []string{"architecture.modules.style_refs"},
		Confidence:    0.62,
		EvidenceRefs:  uniqueEvidenceRefs(styleRefs),
	}, nil
}

func runAPIContractTool(ctx context.Context, state *ProjectUnderstandingState) (ToolPatch, error) {
	drillRefs, drillFiles, err := runProjectUnderstandingFocusedInvestigation(ctx, state, "api_backend_drilldown", []string{"api", "handler", "endpoint", "mutation", "create project", "project", "build", "agent", "submit"})
	if err != nil {
		return ToolPatch{}, err
	}
	contracts := apiContractsFromSnapshots(state.CodeSnapshots)
	state.Pack.APIContracts = contracts
	state.Pack.EvidenceRefs = append(state.Pack.EvidenceRefs, drillRefs...)
	return ToolPatch{
		OutputSummary: fmt.Sprintf("提取 API contract 摘要 %d 个；工具化 API drilldown 读取 %d 个文件。", len(contracts), drillFiles),
		ChangedFields: []string{"api_contracts"},
		Confidence:    0.68,
		EvidenceRefs:  uniqueEvidenceRefs(append(state.Pack.EvidenceRefs, drillRefs...)),
	}, nil
}

func runDataModelTool(_ context.Context, state *ProjectUnderstandingState) (ToolPatch, error) {
	dataModels := dataModelSummariesFromSnapshots(state.CodeSnapshots)
	state.Pack.DataModels = dataModels
	return ToolPatch{
		OutputSummary: fmt.Sprintf("提取数据模型摘要 %d 个。", len(dataModels)),
		ChangedFields: []string{"data_models"},
		Confidence:    0.68,
		EvidenceRefs:  state.Pack.EvidenceRefs,
	}, nil
}

func runSensitiveSurfaceTool(_ context.Context, state *ProjectUnderstandingState) (ToolPatch, error) {
	safety := safetyReportFromProjectIntelligence(state.Project, state.CodeSnapshots, state.PageSnapshots)
	state.Pack.SafetyReport = safety
	return ToolPatch{
		OutputSummary: fmt.Sprintf("安全审查发现 %d 项策略提示。", len(safety.PolicyFindings)),
		ChangedFields: []string{"safety_report"},
		Confidence:    0.74,
		EvidenceRefs:  state.Pack.EvidenceRefs,
	}, nil
}

func runDemoIntentTool(_ context.Context, state *ProjectUnderstandingState) (ToolPatch, error) {
	intent := demoIntentFromState(state)
	state.Pack.DemoIntent = intent
	state.Pack.EvidenceRefs = append(state.Pack.EvidenceRefs, intent.EvidenceRefs...)
	return ToolPatch{
		OutputSummary: fmt.Sprintf("拆解需求目标 %d 个，业务关键目标 %d 个。", len(intent.Goals), businessIntentGoalCount(intent.Goals)),
		ChangedFields: []string{"demo_intent"},
		Confidence:    intent.Confidence,
		EvidenceRefs:  intent.EvidenceRefs,
	}, nil
}

func runFeatureTraceTool(_ context.Context, state *ProjectUnderstandingState) (ToolPatch, error) {
	trace := featureTraceFromState(state)
	state.Pack.FeatureTrace = trace
	capabilities := featureCapabilitiesFromState(state)
	state.Pack.FeatureCapabilities = capabilities
	state.Pack.EvidenceRefs = append(state.Pack.EvidenceRefs, trace.EvidenceRefs...)
	return ToolPatch{
		OutputSummary: fmt.Sprintf("按需求追踪功能能力 %d 个，目标证据链 %d 条。", len(capabilities), len(trace.Traces)),
		ChangedFields: []string{"feature_capabilities", "feature_trace"},
		Confidence:    trace.Confidence,
		EvidenceRefs:  trace.EvidenceRefs,
	}, nil
}

func runScriptFeasibilityTool(_ context.Context, state *ProjectUnderstandingState) (ToolPatch, error) {
	scenarios := demoScenarioPlansFromState(state)
	readiness := scriptReadinessFromState(state, scenarios)
	state.Pack.DemoScenarioPlans = scenarios
	state.Pack.ScriptReadinessReport = readiness
	return ToolPatch{
		OutputSummary: fmt.Sprintf("生成候选演示路径 %d 条；脚本可继续=%t。", len(scenarios), readiness.CanProceed),
		ChangedFields: []string{"demo_scenario_plans", "script_readiness_report"},
		Confidence:    readiness.Confidence,
		EvidenceRefs:  readiness.EvidenceRefs,
	}, nil
}

func runProjectUnderstandingFocusedInvestigation(ctx context.Context, state *ProjectUnderstandingState, purpose string, terms []string) ([]model.EvidenceRef, int, error) {
	if state == nil || state.Project == nil || state.InvestigationTools == nil {
		return nil, 0, nil
	}
	if state.Project.SourceBinding != nil && state.Project.SourceBinding.EffectiveMode == model.ProductSourceModePageOnly {
		return nil, 0, nil
	}
	roots := localProjectCodeRoots(state.Project)
	if len(roots) == 0 {
		return nil, 0, nil
	}
	budget := focusedProjectUnderstandingBudget()
	brief := focusedInvestigationBrief(state, purpose, terms)
	refs := []model.EvidenceRef{}
	fileCount := 0
	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return refs, fileCount, err
		}
		result, err := state.InvestigationTools.InvestigateLocalPath(ctx, root, budget, state.Project, brief)
		if err != nil {
			refs = append(refs, model.EvidenceRef{
				ID:         "ev_project_investigation_error_" + shortHash(purpose+root+err.Error()),
				Kind:       model.EvidenceKindSourceCode,
				Summary:    "ProjectIntelligenceGraph 工具化调查失败，保留已有摘要：" + purpose,
				FieldPath:  "project_intelligence.investigation." + purpose,
				Confidence: 0.3,
			})
			continue
		}
		if result.Trace != nil {
			refs = append(refs, investigationTraceEvidenceRef(purpose, result.Trace))
		}
		if len(result.SelectedCandidates) == 0 {
			continue
		}
		snapshot := model.CodeUnderstandingSnapshot{
			ID:                 "focused_code_" + purpose + "_" + shortHash(root),
			ProjectID:          state.Project.ID,
			SchemaVersion:      model.MultimodalUnderstandingReportSchemaVersion,
			RepositoryID:       "focused_" + purpose,
			Summary:            "ProjectIntelligenceGraph 工具化 " + purpose + " 摘要；仅保存 hash、路由、组件、selector、API 和数据模型结构。",
			ReadBudget:         &budget,
			InvestigationTrace: result.Trace,
			EvidenceRefs: []model.EvidenceRef{{
				ID:         "ev_project_investigation_" + purpose + "_" + shortHash(root),
				Kind:       model.EvidenceKindSourceCode,
				Summary:    "ProjectIntelligenceGraph 使用 ProjectInvestigationToolSuite 执行 " + purpose,
				FieldPath:  "project_intelligence.investigation." + purpose,
				Confidence: 0.72,
			}},
			CreatedAt: time.Now().UTC(),
		}
		if err := readStructuredCodeSnapshotFromCandidates(ctx, &snapshot, result.SelectedCandidates, budget); err != nil {
			return refs, fileCount, err
		}
		normalizeCodeSnapshotForIntent(&snapshot, state.Project, brief)
		snapshot.Languages = uniqueStrings(snapshot.Languages)
		snapshot.Frameworks = uniqueStrings(snapshot.Frameworks)
		snapshot.EntrypointHashes = uniqueStrings(snapshot.EntrypointHashes)
		state.CodeSnapshots = append(state.CodeSnapshots, snapshot)
		fileCount += snapshot.FileCount
		refs = append(refs, snapshot.EvidenceRefs...)
	}
	return uniqueEvidenceRefs(refs), fileCount, nil
}

func focusedProjectUnderstandingBudget() model.CodeReadBudget {
	defaults := defaultCodeReadBudget()
	return model.CodeReadBudget{
		Mode:                   "project_intelligence_focused_drilldown",
		RepoIndexFileLimit:     3,
		DrilldownRounds:        2,
		FilesPerRound:          5,
		TotalFileLimit:         14,
		MaxFileBytes:           defaults.MaxFileBytes,
		ToolSearchFileLimit:    120,
		ToolSearchBytesPerFile: defaults.ToolSearchBytesPerFile,
		ToolSearchResultLimit:  12,
	}
}

func focusedInvestigationBrief(state *ProjectUnderstandingState, purpose string, terms []string) *model.RequirementBrief {
	brief := &model.RequirementBrief{
		ID:        "focused_brief_" + purpose + "_" + shortHash(strings.Join(terms, "|")),
		ProjectID: state.Project.ID,
		Objective: strings.TrimSpace(strings.Join(append(requirementGoalTexts(state), purpose, strings.Join(terms, " ")), " ")),
		MustShow:  uniqueStrings(append(intentLabelsFromState(state), terms...)),
		CreatedAt: time.Now().UTC(),
	}
	if state.Brief != nil {
		brief.TargetAudience = state.Brief.TargetAudience
		brief.ForbiddenPages = append([]string{}, state.Brief.ForbiddenPages...)
		brief.ForbiddenData = append([]string{}, state.Brief.ForbiddenData...)
		brief.MustNotShow = append([]string{}, state.Brief.MustNotShow...)
	}
	return brief
}

func intentLabelsFromState(state *ProjectUnderstandingState) []string {
	labels := []string{}
	if state != nil && state.Pack != nil && state.Pack.DemoIntent != nil {
		for _, goal := range state.Pack.DemoIntent.Goals {
			labels = append(labels, goal.Label)
			labels = append(labels, goal.TargetKeywords...)
		}
	}
	return uniqueStrings(labels)
}

func localProjectCodeRoots(project *model.ProjectContext) []string {
	roots := []string{}
	if project == nil || project.Inputs == nil {
		return roots
	}
	for _, input := range project.Inputs.Code {
		if strings.TrimSpace(input.LocalPath) != "" {
			roots = append(roots, input.LocalPath)
		}
	}
	for _, repo := range project.Inputs.Repositories {
		if strings.TrimSpace(repo.LocalPath) != "" {
			roots = append(roots, repo.LocalPath)
		}
	}
	return uniqueStrings(roots)
}

func investigationTraceEvidenceRef(purpose string, trace *model.CodeInvestigationTrace) model.EvidenceRef {
	summary := "ProjectInvestigationToolSuite " + purpose
	if trace != nil && trace.Summary != "" {
		summary += "：" + trace.Summary
	}
	return model.EvidenceRef{
		ID:         "ev_project_investigation_trace_" + purpose + "_" + shortHash(summary),
		Kind:       model.EvidenceKindSourceCode,
		Summary:    summary,
		FieldPath:  "project_intelligence.investigation_trace." + purpose,
		Confidence: 0.68,
	}
}

type agentSynthesisStep struct {
	nodeID        string
	agent         string
	inputSummary  string
	outputSummary string
	confidence    float64
}

func agentSynthesisTraceSteps(state *ProjectUnderstandingState) []agentSynthesisStep {
	pack := state.Pack
	return []agentSynthesisStep{
		{
			nodeID:        "architecture_cartographer",
			agent:         "ArchitectureCartographerAgent",
			inputSummary:  "repo index + route map",
			outputSummary: fmt.Sprintf("架构地图包含 %d 个模块和 %d 个路由节点。", len(pack.Architecture.Modules), len(pack.Architecture.RouteTree)),
			confidence:    pack.Architecture.Confidence,
		},
		{
			nodeID:        "feature_miner",
			agent:         "FeatureMinerAgent",
			inputSummary:  "requirement brief + components + routes",
			outputSummary: fmt.Sprintf("功能能力卡片 %d 个，均绑定摘要证据。", len(pack.FeatureCapabilities)),
			confidence:    0.76,
		},
		{
			nodeID:        "ux_flow",
			agent:         "UXFlowAgent",
			inputSummary:  "page snapshots + selectors + actions",
			outputSummary: fmt.Sprintf("交互面 %d 个，包含 selector/wait hint。", len(pack.InteractionSurfaces)),
			confidence:    0.72,
		},
		{
			nodeID:        "api_data",
			agent:         "APIDataAgent",
			inputSummary:  "api contracts + data models",
			outputSummary: fmt.Sprintf("API 摘要 %d 个，数据模型 %d 个。", len(pack.APIContracts), len(pack.DataModels)),
			confidence:    0.7,
		},
		{
			nodeID:        "security_reviewer",
			agent:         "SecurityReviewerAgent",
			inputSummary:  "sensitive fields + forbidden policy",
			outputSummary: fmt.Sprintf("安全策略提示 %d 项，masked fields=%d。", len(pack.SafetyReport.PolicyFindings), len(pack.SafetyReport.MaskedFields)),
			confidence:    0.74,
		},
		{
			nodeID:        "demo_director",
			agent:         "DemoDirectorAgent",
			inputSummary:  "feature capabilities + readiness",
			outputSummary: fmt.Sprintf("候选 workflow %d 条，推荐 %s。", len(pack.DemoScenarioPlans), firstScenarioName(pack.DemoScenarioPlans)),
			confidence:    0.78,
		},
		{
			nodeID:        "script_engineer",
			agent:         "ScriptEngineerAgent",
			inputSummary:  "candidate workflows + interaction surface",
			outputSummary: fmt.Sprintf("建议 stage=%d，目标时长=%d 秒。", pack.ScriptReadinessReport.SuggestedStageCount, pack.ScriptReadinessReport.SuggestedTargetDurationSec),
			confidence:    pack.ScriptReadinessReport.Confidence,
		},
		{
			nodeID:        "critic_verifier",
			agent:         "CriticVerifierAgent",
			inputSummary:  "architecture + workflows + safety + readiness",
			outputSummary: criticOutputSummary(pack.ScriptReadinessReport),
			confidence:    0.78,
		},
	}
}

func buildArchitectureMap(project *model.ProjectContext, snapshots []model.CodeUnderstandingSnapshot, pages []model.PageUnderstandingSnapshot) *model.ProjectArchitectureMap {
	refs := []model.EvidenceRef{}
	languages := []string{}
	frameworks := []string{}
	entryHashes := []string{}
	modules := []model.ProjectModule{}
	repositoryRefs := []string{}
	sourceHashParts := []string{}
	repoCount := 0
	if project != nil && project.Inputs != nil {
		repoCount = len(project.Inputs.Repositories)
		for i, repo := range project.Inputs.Repositories {
			ref := firstNonEmpty(repo.LastSnapshotID, repo.URL, repo.LocalPath, fmt.Sprintf("repo_%d", i+1))
			repositoryRefs = append(repositoryRefs, "repo_"+shortHash(ref))
			if repo.LocalPath != "" {
				sourceHashParts = append(sourceHashParts, repo.LocalPath)
			}
			if repo.URL != "" {
				sourceHashParts = append(sourceHashParts, repo.URL)
			}
		}
	}
	if repoCount == 0 {
		repoCount = len(snapshots)
	}
	for index, snapshot := range snapshots {
		refs = append(refs, snapshot.EvidenceRefs...)
		languages = append(languages, snapshot.Languages...)
		frameworks = append(frameworks, snapshot.Frameworks...)
		entryHashes = append(entryHashes, snapshot.EntrypointHashes...)
		if snapshot.SourceDigestSHA256 != "" {
			sourceHashParts = append(sourceHashParts, snapshot.SourceDigestSHA256)
		}
		routeRefs := make([]string, 0, len(snapshot.Routes))
		for _, route := range snapshot.Routes {
			routeRefs = append(routeRefs, firstNonEmpty(route.ID, safeID("route", route.Path)))
		}
		componentRefs := make([]string, 0, len(snapshot.Components))
		for _, component := range snapshot.Components {
			componentRefs = append(componentRefs, firstNonEmpty(component.ID, safeID("component", component.Name)))
		}
		apiRefs := make([]string, 0, len(snapshot.APIEndpoints))
		for _, endpoint := range snapshot.APIEndpoints {
			apiRefs = append(apiRefs, firstNonEmpty(endpoint.ID, safeID("api", endpoint.Path)))
		}
		modelRefs := make([]string, 0, len(snapshot.DataModels))
		for _, dataModel := range snapshot.DataModels {
			modelRefs = append(modelRefs, firstNonEmpty(dataModel.ID, safeID("model", dataModel.Name)))
		}
		sourceHashes := make([]string, 0, len(snapshot.PathDigests))
		for _, digest := range snapshot.PathDigests {
			if digest.PathHashSHA256 != "" {
				sourceHashes = append(sourceHashes, digest.PathHashSHA256)
			}
		}
		modules = append(modules, model.ProjectModule{
			ID:               "module_" + firstNonEmpty(snapshot.RepositoryID, snapshot.ID, fmt.Sprintf("%d", index+1)),
			Name:             firstNonEmpty(snapshot.RepositoryID, snapshot.ID, fmt.Sprintf("代码模块 %d", index+1)),
			Kind:             moduleKind(snapshot.Frameworks, snapshot.Languages),
			Responsibility:   moduleResponsibility(snapshot),
			RepositoryRefID:  firstNonEmpty(snapshot.RepositoryID, "repo_"+shortHash(snapshot.SourceDigestSHA256)),
			FileCount:        snapshot.FileCount,
			SourcePathHashes: limitStrings(uniqueStrings(sourceHashes), 40),
			EntryPointHashes: append([]string{}, snapshot.EntrypointHashes...),
			RouteRefs:        uniqueStrings(routeRefs),
			ComponentRefs:    uniqueStrings(componentRefs),
			APIRefs:          uniqueStrings(apiRefs),
			DataModelRefs:    uniqueStrings(modelRefs),
			EvidenceRefs:     snapshot.EvidenceRefs,
			Confidence:       0.72,
		})
	}
	frameworks = uniqueStrings(frameworks)
	languages = uniqueStrings(languages)
	return &model.ProjectArchitectureMap{
		ID:                "architecture_" + project.ID,
		ProjectID:         project.ID,
		SchemaVersion:     model.ProjectIntelligencePackSchemaVersion,
		RepositoryCount:   repoCount,
		RepositoryRefIDs:  uniqueStrings(repositoryRefs),
		WorkspaceRootHash: hashString(strings.Join(sourceHashParts, "|")),
		PackageManagers:   packageManagersFromFrameworks(frameworks, languages),
		Frameworks:        frameworks,
		Languages:         languages,
		RuntimeTargets:    runtimeTargetsFromFrameworks(frameworks, languages),
		EntryPointHashes:  uniqueStrings(entryHashes),
		Modules:           modules,
		RouteTree:         routeTreeFromSnapshots(snapshots, pages),
		Summary:           architectureSummary(frameworks, languages, modules),
		EvidenceRefs:      uniqueEvidenceRefs(refs),
		Confidence:        architectureConfidence(snapshots),
	}
}

func routeTreeFromSnapshots(snapshots []model.CodeUnderstandingSnapshot, pages []model.PageUnderstandingSnapshot) []model.ArchitectureRouteNode {
	nodes := []model.ArchitectureRouteNode{}
	seen := map[string]bool{}
	for _, snapshot := range snapshots {
		for _, route := range snapshot.Routes {
			path := strings.TrimSpace(route.Path)
			if path == "" || seen[path] || !browserRouteAllowedForCode(path) {
				continue
			}
			seen[path] = true
			nodes = append(nodes, model.ArchitectureRouteNode{
				ID:            firstNonEmpty(route.ID, safeID("route", path)),
				Path:          path,
				Name:          firstNonEmpty(route.Name, routeNameFromPath(path)),
				ParentPath:    parentRoutePath(path),
				ComponentRefs: append([]string{}, route.ComponentRefs...),
				AuthRequired:  route.AuthRequired,
				EvidenceRefs:  route.EvidenceRefs,
				Confidence:    route.Confidence,
			})
		}
	}
	for _, page := range pages {
		path := pathFromURL(page.URL)
		if path == "" || seen[path] || !browserRouteAllowedForCode(path) {
			continue
		}
		seen[path] = true
		nodes = append(nodes, model.ArchitectureRouteNode{
			ID:           firstNonEmpty(page.ID, safeID("route", path)),
			Path:         path,
			Name:         firstNonEmpty(page.Title, page.PageRole, routeNameFromPath(path)),
			ParentPath:   parentRoutePath(path),
			EvidenceRefs: page.EvidenceRefs,
			Confidence:   page.Confidence,
		})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Path < nodes[j].Path })
	return nodes
}

func interactionSurfacesFromSnapshots(project *model.ProjectContext, snapshots []model.CodeUnderstandingSnapshot, pages []model.PageUnderstandingSnapshot) []model.InteractionSurface {
	surfaces := []model.InteractionSurface{}
	codeSelectors := selectorsFromCodeSnapshots(snapshots)
	for i, page := range pages {
		actions := []model.UIActionRef{}
		selectors := append([]model.SelectorCandidate{}, page.StableSelectors...)
		for _, action := range page.Actions {
			actionID := firstNonEmpty(action.ID, safeID("action", action.Label+action.SelectorHint))
			actions = append(actions, model.UIActionRef{
				ID:           actionID,
				Label:        action.Label,
				Kind:         firstNonEmpty(action.Kind, "inspect"),
				Selector:     action.SelectorHint,
				TargetRoute:  action.TargetURL,
				EvidenceRefs: action.EvidenceRefs,
			})
			if action.SelectorHint != "" {
				selectors = append(selectors, model.SelectorCandidate{
					Kind:           "css",
					Value:          action.SelectorHint,
					Confidence:     action.Confidence,
					StabilityScore: 0.7,
					Source:         "page_reader",
					EvidenceRefs:   action.EvidenceRefs,
				})
			}
		}
		for selectorIndex, selector := range limitSelectorCandidates(codeSelectors, 12) {
			if selector.Value == "" {
				continue
			}
			actionID := fmt.Sprintf("action_code_selector_%d", selectorIndex+1)
			actions = append(actions, model.UIActionRef{
				ID:           actionID,
				Label:        labelFromSelector(selector.Value),
				Kind:         actionKindFromSelector(selector.Value),
				Selector:     selector.Value,
				EvidenceRefs: selector.EvidenceRefs,
			})
			selectors = append(selectors, selector)
		}
		surfaces = append(surfaces, model.InteractionSurface{
			ID:              firstNonEmpty(page.ID, fmt.Sprintf("surface_page_%d", i+1)),
			PageID:          page.ID,
			URL:             page.URL,
			Title:           firstNonEmpty(page.Title, "产品页面"),
			PageRole:        firstNonEmpty(page.PageRole, "product_page"),
			Actions:         actions,
			StableSelectors: uniqueSelectorCandidates(selectors),
			States:          page.States,
			WaitHints:       waitHintsForSurface(page.URL, selectors, page.States),
			FeatureRefs:     featureRefsFromPageActions(page.Actions),
			RiskFindings:    page.RiskFindings,
			EvidenceRefs:    page.EvidenceRefs,
			Confidence:      page.Confidence,
		})
	}
	if len(surfaces) == 0 {
		selectors := codeSelectors
		actions := []model.UIActionRef{}
		for i, selector := range limitSelectorCandidates(selectors, 12) {
			label := labelFromSelector(selector.Value)
			actions = append(actions, model.UIActionRef{
				ID:       fmt.Sprintf("action_selector_%d", i+1),
				Label:    label,
				Kind:     actionKindFromSelector(selector.Value),
				Selector: selector.Value,
			})
		}
		surfaces = append(surfaces, model.InteractionSurface{
			ID:              "surface_code_summary",
			URL:             firstNonEmpty(project.ProductURL, "input://product_context"),
			Title:           "代码摘要交互面",
			PageRole:        "inspect_only",
			Actions:         actions,
			StableSelectors: selectors,
			States:          []string{"structure_summary_available"},
			WaitHints:       []string{"等待 body/main 可见", "截图前保持页面状态稳定"},
			EvidenceRefs:    codeEvidenceRefs(snapshots),
			Confidence:      0.62,
		})
	}
	return surfaces
}

func labelFromSelector(selector string) string {
	value := strings.Trim(selector, "[]'")
	value = strings.NewReplacer("data-testid=", "", "\"", "", "'", "", "_", " ", "-", " ").Replace(value)
	value = strings.TrimSpace(value)
	if value == "" {
		return "检查稳定选择器"
	}
	return value
}

func actionKindFromSelector(selector string) string {
	lower := strings.ToLower(selector)
	if selectorLooksReadOnlySurface(lower) {
		return "inspect"
	}
	switch {
	case containsAny(lower, "email", "username", "password", "input", "search", "name", "message", "phone"):
		return "fill"
	case containsAny(lower, "button", "btn", "submit", "confirm", "next", "start", "create", "add", "invite", "open", "new", "login", "signin", "sign-in"):
		return "click"
	default:
		return "inspect"
	}
}

func selectorLooksReadOnlySurface(selector string) bool {
	lower := strings.ToLower(selector)
	if lower == "" {
		return false
	}
	if containsAny(lower,
		"input[", "textarea[", "select[", "button[", "[role=\"button\"", "[role='button'",
		"[role=\"textbox\"", "[role='textbox'", "placeholder=", "name=", "aria-label", "aria-labelledby",
	) {
		return false
	}
	if containsAny(lower,
		"display", "readonly", "read-only", "read_only", "label", "caption", "title",
		"avatar", "profile", "user-email", "user_email", "user-name", "user_name",
		"email-display", "name-display", "current-user", "account-email", "account-name",
	) {
		return true
	}
	if containsAny(lower, "status", "badge", "summary", "stat", "metric", "count") &&
		!containsAny(lower, "button", "btn", "create", "new", "start", "run", "build", "generate", "submit") {
		return true
	}
	return false
}

func containsAny(value string, tokens ...string) bool {
	for _, token := range tokens {
		if strings.Contains(value, token) {
			return true
		}
	}
	return false
}

func apiContractsFromSnapshots(snapshots []model.CodeUnderstandingSnapshot) []model.APIContractSummary {
	contracts := []model.APIContractSummary{}
	for _, snapshot := range snapshots {
		sensitive := sensitiveFieldNames(snapshot.SensitiveFields)
		for _, endpoint := range snapshot.APIEndpoints {
			path := strings.TrimSpace(endpoint.Path)
			if path == "" || !apiPathAllowedForCode(path) {
				continue
			}
			contracts = append(contracts, model.APIContractSummary{
				ID:                 firstNonEmpty(endpoint.ID, safeID("api", path)),
				Method:             firstNonEmpty(endpoint.Method, inferMethodFromPath(path)),
				Path:               path,
				Purpose:            "用于支撑页面数据加载或业务动作的 API 摘要。",
				AuthRequired:       apiLikelyRequiresAuth(path, sensitive),
				RequestFields:      apiFieldHints(path),
				ResponseFields:     []string{"status", "data", "message"},
				SensitiveFields:    sensitive,
				FilePathHashSHA256: endpoint.FilePathHashSHA256,
				EvidenceRefs:       endpoint.EvidenceRefs,
				Confidence:         endpoint.Confidence,
			})
		}
	}
	return dedupeAPIContracts(contracts)
}

func dataModelSummariesFromSnapshots(snapshots []model.CodeUnderstandingSnapshot) []model.ProjectDataModelSummary {
	out := []model.ProjectDataModelSummary{}
	for _, snapshot := range snapshots {
		sensitive := sensitiveFieldNames(snapshot.SensitiveFields)
		for _, dataModel := range snapshot.DataModels {
			out = append(out, model.ProjectDataModelSummary{
				ID:                   firstNonEmpty(dataModel.ID, safeID("model", dataModel.Name)),
				Name:                 dataModel.Name,
				Kind:                 firstNonEmpty(dataModel.Kind, "domain_model"),
				Fields:               append([]model.DataField{}, dataModel.Fields...),
				SensitiveFields:      sensitiveModelFields(dataModel, sensitive),
				SourcePathHashSHA256: dataModel.SourcePathHashSHA256,
				EvidenceRefs:         dataModel.EvidenceRefs,
				Confidence:           dataModel.Confidence,
			})
		}
	}
	return dedupeDataModels(out)
}

func safetyReportFromProjectIntelligence(project *model.ProjectContext, snapshots []model.CodeUnderstandingSnapshot, pages []model.PageUnderstandingSnapshot) *model.SafetyReport {
	findings := []model.AgentFinding{}
	maskedFields := append([]string{}, project.ForbiddenData...)
	for _, snapshot := range snapshots {
		for _, sensitive := range snapshot.SensitiveFields {
			maskedFields = append(maskedFields, sensitive.Name)
			findings = append(findings, model.AgentFinding{
				ID:              "finding_project_intel_sensitive_" + shortHash(sensitive.Name+sensitive.Kind),
				Kind:            "sensitive_surface",
				Severity:        model.FindingSeverityWarning,
				Title:           "敏感字段需打码",
				Summary:         "代码结构摘要中发现敏感字段：" + sensitive.Name,
				Rationale:       sensitive.Reason,
				SuggestedAction: "将相关字段加入 redaction policy，并避免在旁白/截图中暴露原值。",
				EvidenceRefs:    sensitive.EvidenceRefs,
				Confidence:      0.74,
			})
		}
	}
	for _, page := range pages {
		findings = append(findings, page.RiskFindings...)
	}
	for _, page := range project.ForbiddenPages {
		findings = append(findings, model.AgentFinding{
			ID:              "finding_forbidden_page_" + shortHash(page),
			Kind:            "forbidden_page",
			Severity:        model.FindingSeverityWarning,
			Summary:         "禁止访问页面：" + page,
			SuggestedAction: "GraphBuilderAgent 生成脚本时必须避开该路径。",
			Confidence:      0.9,
		})
	}
	return &model.SafetyReport{
		AllowedToProceed: true,
		PolicyFindings:   findings,
		MaskedFields:     uniqueStrings(maskedFields),
		Notes:            []string{"ProjectIntelligenceGraph 只保存结构摘要、hash、selector 和证据引用。"},
	}
}

func demoIntentFromState(state *ProjectUnderstandingState) *model.DemoIntentSpec {
	now := time.Now().UTC()
	project := state.Project
	brief := state.Brief
	objective := "生成可审批、可执行的产品演示路径。"
	audience := ""
	if project != nil {
		objective = firstNonEmpty(objectiveFromBrief(brief), project.ProductDescription, objective)
		audience = project.TargetAudience
	}
	if brief != nil {
		audience = firstNonEmpty(brief.TargetAudience, audience)
	}
	goals := []model.DemoIntentGoal{}
	if hasProjectCredential(project) || intentTextContains(state, "登录", "login", "signin", "sign in") {
		goals = append(goals, model.DemoIntentGoal{
			ID:               "intent_login",
			Label:            "登录并进入工作台",
			Kind:             "auth",
			Required:         true,
			BusinessCritical: false,
			TargetKeywords:   []string{"login", "signin", "sign-in", "登录", "邮箱", "email", "password", "密码"},
			PreferredAction:  "fill",
			SuccessState:     "进入已登录工作台或目标页面",
			Confidence:       0.78,
		})
	}
	for _, rawGoal := range requirementGoalTexts(state) {
		goal := intentGoalFromText(rawGoal)
		if goal.Label == "" {
			continue
		}
		if containsIntentGoal(goals, goal.ID, goal.Label) {
			continue
		}
		goals = append(goals, goal)
	}
	if !hasBusinessIntentGoal(goals) {
		label := firstNonEmpty(scenarioFromBrief(brief), objective, "核心业务流程")
		goals = append(goals, model.DemoIntentGoal{
			ID:               "intent_primary_business",
			Label:            label,
			Kind:             "business_action",
			Required:         true,
			BusinessCritical: true,
			TargetKeywords:   intentKeywordsForText(label),
			PreferredAction:  "click",
			SuccessState:     firstNonEmpty(primaryOutcomeFromBrief(brief), "目标业务状态可见"),
			Confidence:       0.66,
		})
	}
	forbidden := []string{}
	if project != nil {
		forbidden = append(forbidden, project.MustNotShow...)
		forbidden = append(forbidden, project.ForbiddenPages...)
		forbidden = append(forbidden, project.ForbiddenData...)
	}
	if brief != nil {
		forbidden = append(forbidden, brief.MustNotShow...)
		forbidden = append(forbidden, brief.ForbiddenPages...)
		forbidden = append(forbidden, brief.ForbiddenData...)
	}
	return &model.DemoIntentSpec{
		ID:              "demo_intent_" + project.ID,
		ProjectID:       project.ID,
		SchemaVersion:   model.ProjectIntelligencePackSchemaVersion,
		Objective:       objective,
		TargetAudience:  audience,
		Goals:           goals,
		ForbiddenTopics: uniqueStrings(forbidden),
		EvidenceRefs:    requirementEvidenceRefs(brief),
		Confidence:      intentConfidence(goals),
		CreatedAt:       now,
	}
}

func featureTraceFromState(state *ProjectUnderstandingState) *model.FeatureTraceResult {
	now := time.Now().UTC()
	if state.Pack.DemoIntent == nil {
		state.Pack.DemoIntent = demoIntentFromState(state)
	}
	probes := interactionProbesFromState(state)
	traces := make([]model.FeatureGoalTrace, 0, len(state.Pack.DemoIntent.Goals))
	evidenceRefs := []model.EvidenceRef{}
	for _, goal := range state.Pack.DemoIntent.Goals {
		trace := traceGoalToEvidence(goal, state, probes)
		traces = append(traces, trace)
		evidenceRefs = append(evidenceRefs, trace.EvidenceRefs...)
		for _, probe := range trace.SelectorEvidence {
			evidenceRefs = append(evidenceRefs, probe.EvidenceRefs...)
		}
	}
	return &model.FeatureTraceResult{
		ID:            "feature_trace_" + state.Project.ID,
		ProjectID:     state.Project.ID,
		IntentID:      state.Pack.DemoIntent.ID,
		SchemaVersion: model.ProjectIntelligencePackSchemaVersion,
		Traces:        traces,
		EvidenceRefs:  uniqueEvidenceRefs(evidenceRefs),
		Confidence:    featureTraceConfidence(traces),
		CreatedAt:     now,
	}
}

func traceGoalToEvidence(goal model.DemoIntentGoal, state *ProjectUnderstandingState, probes []model.InteractionProbe) model.FeatureGoalTrace {
	trace := model.FeatureGoalTrace{
		IntentGoalID: goal.ID,
		IntentLabel:  goal.Label,
		Confidence:   0.5,
	}
	keywords := goal.TargetKeywords
	if len(keywords) == 0 {
		keywords = intentKeywordsForText(goal.Label)
	}
	for _, route := range state.Pack.Architecture.RouteTree {
		if !isURLAllowedByRunScope(state.Pack.RunIntentScope, route.Path) || isControlPlaneSignal(state.Pack.RunIntentScope, route.Path, route.Name) {
			continue
		}
		if keywordMatchScore(keywords, route.Path, route.Name) > 0 {
			trace.MatchedRouteRefs = append(trace.MatchedRouteRefs, route.ID)
			trace.EvidenceRefs = append(trace.EvidenceRefs, route.EvidenceRefs...)
		}
	}
	for _, module := range state.Pack.Architecture.Modules {
		if keywordMatchScore(keywords, module.Name, module.Responsibility, strings.Join(module.ComponentRefs, " ")) > 0 {
			trace.MatchedComponents = append(trace.MatchedComponents, module.ComponentRefs...)
			trace.EvidenceRefs = append(trace.EvidenceRefs, module.EvidenceRefs...)
		}
	}
	for _, api := range state.Pack.APIContracts {
		if isControlPlaneSignal(state.Pack.RunIntentScope, api.Path, api.Purpose) {
			continue
		}
		if keywordMatchScore(keywords, api.Path, api.Purpose, strings.Join(api.RequestFields, " "), strings.Join(api.ResponseFields, " ")) > 0 {
			trace.MatchedAPIRefs = append(trace.MatchedAPIRefs, api.ID)
			trace.EvidenceRefs = append(trace.EvidenceRefs, api.EvidenceRefs...)
		}
	}
	for _, dataModel := range state.Pack.DataModels {
		if keywordMatchScore(keywords, dataModel.Name, strings.Join(dataFieldNames(dataModel.Fields), " ")) > 0 {
			trace.MatchedDataModels = append(trace.MatchedDataModels, dataModel.ID)
			trace.EvidenceRefs = append(trace.EvidenceRefs, dataModel.EvidenceRefs...)
		}
	}
	for _, probe := range probes {
		if !isURLAllowedByRunScope(state.Pack.RunIntentScope, probe.URL) || isControlPlaneSignal(state.Pack.RunIntentScope, probe.URL, probe.Label, probe.Selector) {
			continue
		}
		score := interactionProbeGoalScore(goal, probe)
		if score <= 0 {
			continue
		}
		probe.IntentGoalID = goal.ID
		probe.Score = score
		if goal.BusinessCritical && (probe.IsChrome || !probe.IsBusiness) {
			continue
		}
		trace.SelectorEvidence = append(trace.SelectorEvidence, probe)
	}
	sort.Slice(trace.SelectorEvidence, func(i, j int) bool {
		return trace.SelectorEvidence[i].Score > trace.SelectorEvidence[j].Score
	})
	if len(trace.SelectorEvidence) > 6 {
		trace.SelectorEvidence = trace.SelectorEvidence[:6]
	}
	if len(trace.MatchedRouteRefs) == 0 {
		trace.MissingEvidence = append(trace.MissingEvidence, "route")
	}
	if goal.BusinessCritical && len(trace.SelectorEvidence) == 0 {
		trace.MissingEvidence = append(trace.MissingEvidence, "selector")
	}
	trace.MatchedRouteRefs = limitStrings(uniqueStrings(trace.MatchedRouteRefs), 8)
	trace.MatchedComponents = limitStrings(uniqueStrings(trace.MatchedComponents), 8)
	trace.MatchedAPIRefs = limitStrings(uniqueStrings(trace.MatchedAPIRefs), 8)
	trace.MatchedDataModels = limitStrings(uniqueStrings(trace.MatchedDataModels), 8)
	trace.EvidenceRefs = uniqueEvidenceRefs(trace.EvidenceRefs)
	trace.Confidence = goalTraceConfidence(trace)
	return trace
}

func interactionProbesFromState(state *ProjectUnderstandingState) []model.InteractionProbe {
	probes := []model.InteractionProbe{}
	for _, page := range state.PageSnapshots {
		pageDigest, _ := model.DigestCanonicalJSON(page)
		observedAt := page.CapturedAt
		if observedAt.IsZero() {
			observedAt = page.CreatedAt
		}
		for _, action := range page.Actions {
			selector := strings.TrimSpace(action.SelectorHint)
			if selector == "" {
				continue
			}
			actionURL := firstNonEmpty(action.TargetURL, page.URL)
			if !isURLAllowedByRunScope(state.Pack.RunIntentScope, actionURL) || isControlPlaneSignal(state.Pack.RunIntentScope, actionURL, action.Label, selector) {
				continue
			}
			kind := firstNonEmpty(action.Kind, actionKindFromSelector(selector))
			evidenceRefs := uniqueEvidenceRefs(append(action.EvidenceRefs, page.EvidenceRefs...))
			label := firstNonEmpty(action.Label, labelFromSelector(selector))
			probes = append(probes, model.InteractionProbe{
				ID:             firstNonEmpty(action.ID, "probe_page_"+shortHash(page.ID+selector)),
				Label:          label,
				Kind:           kind,
				Selector:       selector,
				URL:            actionURL,
				RouteRef:       safeID("route", pathFromURL(actionURL)),
				Source:         "page_reader",
				IsBusiness:     isBusinessAction(graphActionTypeFromKind(kind, selector)),
				IsChrome:       actionLooksLikeChromeControl(action.Label, selector),
				SelectorScore:  selectorQualityScore(selector),
				WaitConditions: waitHintsForSurface(page.URL, []model.SelectorCandidate{{Kind: "css", Value: selector}}, page.States),
				EvidenceRefs:   evidenceRefs,
				Alternatives:   selectorProvenanceCandidates(selector, kind, label, "page_scan", pageDigest, actionURL, observedAt, evidenceRefs, action.Confidence),
			})
		}
		for _, selector := range page.StableSelectors {
			if selector.Value == "" {
				continue
			}
			if !isURLAllowedByRunScope(state.Pack.RunIntentScope, page.URL) || isControlPlaneSignal(state.Pack.RunIntentScope, page.URL, selector.Value) {
				continue
			}
			kind := actionKindFromSelector(selector.Value)
			evidenceRefs := uniqueEvidenceRefs(append(selector.EvidenceRefs, page.EvidenceRefs...))
			label := labelFromSelector(selector.Value)
			probes = append(probes, model.InteractionProbe{
				ID:             "probe_page_selector_" + shortHash(page.ID+selector.Value),
				Label:          label,
				Kind:           kind,
				Selector:       selector.Value,
				URL:            page.URL,
				RouteRef:       safeID("route", pathFromURL(page.URL)),
				Source:         "page_reader",
				IsBusiness:     isBusinessAction(graphActionTypeFromKind(kind, selector.Value)),
				IsChrome:       actionLooksLikeChromeControl(labelFromSelector(selector.Value), selector.Value),
				SelectorScore:  selectorQualityScore(selector.Value),
				WaitConditions: waitHintsForSurface(page.URL, []model.SelectorCandidate{selector}, page.States),
				EvidenceRefs:   evidenceRefs,
				Alternatives:   selectorProvenanceCandidates(selector.Value, kind, label, "page_scan", pageDigest, page.URL, observedAt, evidenceRefs, selector.Confidence),
			})
		}
	}
	componentByPathHash := map[string]model.ComponentInsight{}
	for _, snapshot := range state.CodeSnapshots {
		observedAt := snapshot.CreatedAt
		for _, component := range snapshot.Components {
			if component.FilePathHashSHA256 != "" {
				componentByPathHash[component.FilePathHashSHA256] = component
			}
			for _, selector := range component.SelectorHints {
				if selector == "" {
					continue
				}
				if isControlPlaneSignal(state.Pack.RunIntentScope, component.Name, strings.Join(component.ActionLabels, " "), selector) {
					continue
				}
				kind := actionKindFromSelector(selector)
				label := firstNonEmpty(firstString(component.ActionLabels, ""), labelFromSelector(selector), component.Name)
				probes = append(probes, model.InteractionProbe{
					ID:            "probe_component_" + shortHash(component.ID+selector),
					Label:         label,
					Kind:          kind,
					Selector:      selector,
					ComponentRef:  component.ID,
					Source:        "code_reader",
					IsBusiness:    isBusinessAction(graphActionTypeFromKind(kind, selector)),
					IsChrome:      actionLooksLikeChromeControl(component.Name+" "+strings.Join(component.ActionLabels, " "), selector),
					SelectorScore: selectorQualityScore(selector),
					EvidenceRefs:  component.EvidenceRefs,
					Alternatives:  selectorProvenanceCandidates(selector, kind, label, "source_scan", snapshot.SourceDigestSHA256, "", observedAt, component.EvidenceRefs, component.Confidence),
				})
			}
		}
		for _, selector := range snapshot.Selectors {
			if selector.Value == "" {
				continue
			}
			if isControlPlaneSignal(state.Pack.RunIntentScope, selector.Value) {
				continue
			}
			component := componentByPathHash[selector.FilePathHashSHA256]
			kind := actionKindFromSelector(selector.Value)
			label := labelFromSelector(selector.Value)
			if component.Name != "" {
				label = firstNonEmpty(firstString(component.ActionLabels, ""), label, component.Name)
			}
			probes = append(probes, model.InteractionProbe{
				ID:            "probe_code_selector_" + shortHash(selector.FilePathHashSHA256+selector.Value),
				Label:         label,
				Kind:          kind,
				Selector:      selector.Value,
				ComponentRef:  component.ID,
				Source:        "code_reader",
				IsBusiness:    isBusinessAction(graphActionTypeFromKind(kind, selector.Value)),
				IsChrome:      actionLooksLikeChromeControl(label, selector.Value),
				SelectorScore: selectorQualityScore(selector.Value),
				EvidenceRefs:  uniqueEvidenceRefs(append(selector.EvidenceRefs, component.EvidenceRefs...)),
				Alternatives:  selectorProvenanceCandidates(selector.Value, kind, label, "source_scan", snapshot.SourceDigestSHA256, "", observedAt, uniqueEvidenceRefs(append(selector.EvidenceRefs, component.EvidenceRefs...)), selector.Confidence),
			})
		}
	}
	return dedupeInteractionProbes(probes)
}

func selectorProvenanceCandidates(selector, actionKind, label, sourceKind, sourceDigest, observedURL string, observedAt time.Time, evidenceRefs []model.EvidenceRef, confidence float64) []model.SelectorCandidate {
	evidenceID := ""
	for _, ref := range evidenceRefs {
		if strings.TrimSpace(ref.ID) != "" {
			evidenceID = ref.ID
			break
		}
	}
	name := selectorAccessibleName(label, selector)
	if strings.TrimSpace(selector) == "" || evidenceID == "" || strings.TrimSpace(sourceDigest) == "" || observedAt.IsZero() || name == "" {
		return nil
	}
	kind, value := selectorCandidateIdentity(selector)
	if confidence <= 0 {
		confidence = 0.78
	}
	candidate := model.SelectorCandidate{
		Kind: kind, Value: value, Confidence: confidence, StabilityScore: float64(selectorQualityScore(selector)) / 100,
		Source: sourceKind, EvidenceID: evidenceID, SourceKind: sourceKind, SourceDigest: sourceDigest,
		ObservedRole: observedRoleForAction(actionKind, selector), ObservedAccessibleName: name, ObservedAt: &observedAt,
		LastValidatedAt: observedAt, EvidenceRefs: evidenceRefs,
	}
	if sourceKind == "page_scan" {
		candidate.ObservedURL = observedURL
		candidate.ObservedRouteTemplate = pathFromURL(observedURL)
		candidate.EvidenceDigestSHA256 = sourceDigest
	}
	if !model.SelectorCandidateHasFormalProvenance(candidate) {
		return nil
	}
	return []model.SelectorCandidate{candidate}
}

func selectorAccessibleName(label, selector string) string {
	name := strings.TrimSpace(label)
	if testID := testIDFromSelector(selector); testID != "" {
		name = strings.TrimSpace(strings.ReplaceAll(name, testID, ""))
	}
	name = strings.Trim(name, " -_:/|[]()")
	return name
}

func observedRoleForAction(actionKind, selector string) string {
	lower := strings.ToLower(selector)
	switch graphActionTypeFromKind(actionKind, selector) {
	case model.GraphActionFill:
		return "textbox"
	case model.GraphActionSelect:
		return "combobox"
	case model.GraphActionUpload:
		return "button"
	}
	if strings.Contains(lower, "a[") || strings.HasPrefix(strings.TrimSpace(lower), "a:") || strings.Contains(lower, "role=\"link\"") {
		return "link"
	}
	return "button"
}

func featureCapabilitiesFromState(state *ProjectUnderstandingState) []model.FeatureCapability {
	capabilities := []model.FeatureCapability{}
	brief := state.Brief
	objective := "展示产品核心价值路径"
	if brief != nil {
		objective = firstNonEmpty(brief.Objective, objective)
	}
	traceRoutes, traceComponents, traceAPIs, traceModels, traceActions, traceEvidence := featureTraceSupport(state.Pack.FeatureTrace)
	capabilities = append(capabilities, model.FeatureCapability{
		ID:                   "capability_primary_value",
		Name:                 firstNonEmpty(scenarioFromBrief(brief), "核心产品价值"),
		Kind:                 "hero",
		UserValue:            objective,
		BusinessValue:        firstNonEmpty(primaryOutcomeFromBrief(brief), objective),
		Priority:             "hero",
		SupportingRouteRefs:  limitStrings(traceRoutes, 8),
		SupportingPageRefs:   surfaceIDs(intentRelevantSurfaces(state.Pack.InteractionSurfaces, traceActions), 4),
		SupportingComponents: limitStrings(traceComponents, 10),
		SupportingAPIs:       limitStrings(traceAPIs, 8),
		SupportingDataModels: limitStrings(traceModels, 8),
		KeyActions:           keyActionsFromTraceOrBrief(brief, traceActions, state.Pack.InteractionSurfaces),
		Risks:                riskNotesFromSafety(state.Pack.SafetyReport),
		EvidenceRefs:         uniqueEvidenceRefs(append(traceEvidence, state.Pack.EvidenceRefs...)),
		DemoValueScore:       0.9,
		Confidence:           maxFloat(0.68, featureTraceConfidenceValue(state.Pack.FeatureTrace)),
	})
	for _, module := range state.Pack.Architecture.Modules {
		if len(capabilities) >= 8 {
			break
		}
		if len(module.ComponentRefs) == 0 && len(module.RouteRefs) == 0 {
			continue
		}
		if !moduleSupportsIntent(module, state.Pack.FeatureTrace, state) {
			continue
		}
		capabilities = append(capabilities, model.FeatureCapability{
			ID:                   "capability_module_" + shortHash(module.ID),
			Name:                 firstNonEmpty(module.Name, "项目模块"),
			Kind:                 module.Kind,
			UserValue:            "该模块可作为演示中可解释的产品能力支撑。",
			BusinessValue:        module.Responsibility,
			Priority:             "supporting",
			SupportingRouteRefs:  limitStrings(module.RouteRefs, 6),
			SupportingComponents: limitStrings(module.ComponentRefs, 8),
			SupportingAPIs:       limitStrings(module.APIRefs, 6),
			SupportingDataModels: limitStrings(module.DataModelRefs, 6),
			KeyActions:           []string{"inspect", "capture", "validate"},
			EvidenceRefs:         module.EvidenceRefs,
			DemoValueScore:       0.7,
			Confidence:           module.Confidence,
		})
	}
	return capabilities
}

func featureTraceSupport(trace *model.FeatureTraceResult) ([]string, []string, []string, []string, []model.InteractionProbe, []model.EvidenceRef) {
	routeRefs := []string{}
	componentRefs := []string{}
	apiRefs := []string{}
	modelRefs := []string{}
	actions := []model.InteractionProbe{}
	evidence := []model.EvidenceRef{}
	if trace == nil {
		return routeRefs, componentRefs, apiRefs, modelRefs, actions, evidence
	}
	for _, item := range trace.Traces {
		routeRefs = append(routeRefs, item.MatchedRouteRefs...)
		componentRefs = append(componentRefs, item.MatchedComponents...)
		apiRefs = append(apiRefs, item.MatchedAPIRefs...)
		modelRefs = append(modelRefs, item.MatchedDataModels...)
		evidence = append(evidence, item.EvidenceRefs...)
		for _, probe := range item.SelectorEvidence {
			if probe.IsChrome || selectorLooksReadOnlySurface(probe.Selector) || selectorLooksGeneric(probe.Selector) {
				continue
			}
			actions = append(actions, probe)
			evidence = append(evidence, probe.EvidenceRefs...)
		}
	}
	return uniqueStrings(routeRefs), uniqueStrings(componentRefs), uniqueStrings(apiRefs), uniqueStrings(modelRefs), dedupeInteractionProbes(actions), uniqueEvidenceRefs(evidence)
}

func intentRelevantSurfaces(surfaces []model.InteractionSurface, actions []model.InteractionProbe) []model.InteractionSurface {
	if len(actions) == 0 {
		return nil
	}
	selectors := map[string]bool{}
	for _, action := range actions {
		if action.Selector != "" {
			selectors[normalizeSelector(action.Selector)] = true
		}
	}
	out := []model.InteractionSurface{}
	for _, surface := range surfaces {
		for _, action := range surface.Actions {
			if selectors[normalizeSelector(action.Selector)] {
				out = append(out, surface)
				break
			}
		}
	}
	return out
}

func keyActionsFromTraceOrBrief(brief *model.RequirementBrief, actions []model.InteractionProbe, surfaces []model.InteractionSurface) []string {
	values := []string{}
	for _, action := range actions {
		if action.IsBusiness && !action.IsChrome {
			values = append(values, firstNonEmpty(action.Label, action.Kind, labelFromSelector(action.Selector)))
		}
	}
	if len(values) == 0 && brief != nil {
		values = append(values, brief.MustShow...)
	}
	if len(values) == 0 {
		values = append(values, keyActionsFromBriefAndSurfaces(brief, surfaces)...)
	}
	return limitStrings(uniqueStrings(values), 12)
}

func moduleSupportsIntent(module model.ProjectModule, trace *model.FeatureTraceResult, state *ProjectUnderstandingState) bool {
	if trace != nil {
		for _, item := range trace.Traces {
			if intersectsStrings(module.RouteRefs, item.MatchedRouteRefs) ||
				intersectsStrings(module.ComponentRefs, item.MatchedComponents) ||
				intersectsStrings(module.APIRefs, item.MatchedAPIRefs) ||
				intersectsStrings(module.DataModelRefs, item.MatchedDataModels) {
				return true
			}
		}
	}
	keywords := intentKeywordsForText(strings.Join(requirementGoalTexts(state), " "))
	return keywordMatchScore(keywords, module.Name, module.Responsibility, strings.Join(module.ComponentRefs, " "), strings.Join(module.RouteRefs, " ")) > 0
}

func intersectsStrings(left []string, right []string) bool {
	seen := map[string]bool{}
	for _, value := range left {
		seen[value] = true
	}
	for _, value := range right {
		if seen[value] {
			return true
		}
	}
	return false
}

func featureTraceConfidenceValue(trace *model.FeatureTraceResult) float64 {
	if trace != nil && trace.Confidence > 0 {
		return trace.Confidence
	}
	return 0.68
}

func demoScenarioPlansFromState(state *ProjectUnderstandingState) []model.DemoScenarioPlan {
	brief := state.Brief
	useCase := model.DemoUseCaseLaunch
	if brief != nil && len(brief.UseCases) > 0 {
		useCase = brief.UseCases[0]
	} else if state.Project != nil && len(state.Project.Goals) > 0 {
		useCase = state.Project.Goals[0].UseCase
	}
	featureRefs := capabilityIDs(state.Pack.FeatureCapabilities, 5)
	pageRefs := surfaceIDs(state.Pack.InteractionSurfaces, 5)
	routeRefs := routeIDs(state.Pack.Architecture.RouteTree, 6)
	objective := firstNonEmpty(objectiveFromBrief(brief), state.Project.ProductDescription, "生成可审批、可执行的产品演示路径。")
	value := firstNonEmpty(primaryOutcomeFromBrief(brief), objective)
	stageCount := suggestedStageCount(state.Pack)
	duration := maxInt(stageCount*12, 60)
	plans := []model.DemoScenarioPlan{
		{
			ID:                   "scenario_primary_script_ready",
			Name:                 firstNonEmpty(scenarioFromBrief(brief), "产品演示") + "主线演示",
			UseCase:              useCase,
			AudienceID:           "audience_primary",
			Objective:            objective,
			ValueProposition:     value,
			FeatureRefs:          featureRefs,
			PageRefs:             pageRefs,
			RouteRefs:            routeRefs,
			EstimatedSteps:       stageCount,
			EstimatedDurationSec: duration,
			NarrativeBeats:       narrativeBeatsFromPack(state.Pack),
			RiskNotes:            riskNotesFromSafety(state.Pack.SafetyReport),
			EvidenceRefs:         state.Pack.EvidenceRefs,
			Feasibility:          0.78,
			ValueScore:           0.88,
			Confidence:           0.78,
		},
		{
			ID:                   "scenario_inspect_only_fallback",
			Name:                 "结构摘要检查型演示",
			UseCase:              useCase,
			AudienceID:           "audience_primary",
			Objective:            "在 URL 或截图不足时，先用代码结构摘要解释功能能力和可执行性。",
			ValueProposition:     "保障缺少页面材料时仍能生成可审批的 inspect-only 脚本包。",
			FeatureRefs:          featureRefs[:minInt(len(featureRefs), 3)],
			RouteRefs:            routeRefs[:minInt(len(routeRefs), 4)],
			EstimatedSteps:       4,
			EstimatedDurationSec: 45,
			NarrativeBeats:       []string{"说明输入材料", "解释架构和功能能力", "标记缺失页面材料", "输出可审批脚本包"},
			RiskNotes:            []string{"无可访问 URL 时不能进入真实浏览器录制，只能生成检查型脚本。"},
			EvidenceRefs:         state.Pack.EvidenceRefs,
			Feasibility:          0.66,
			ValueScore:           0.62,
			Confidence:           0.66,
		},
	}
	if len(state.Pack.FeatureCapabilities) >= 3 {
		plans = append(plans, model.DemoScenarioPlan{
			ID:                   "scenario_feature_deep_dive",
			Name:                 "核心能力深挖演示",
			UseCase:              useCase,
			AudienceID:           "audience_primary",
			Objective:            "围绕最有价值的功能能力生成多 stage 演示。",
			ValueProposition:     value,
			FeatureRefs:          featureRefs,
			PageRefs:             pageRefs,
			RouteRefs:            routeRefs,
			EstimatedSteps:       maxInt(stageCount, 5),
			EstimatedDurationSec: maxInt(duration, 75),
			NarrativeBeats:       append(narrativeBeatsFromPack(state.Pack), "展示最终业务结果"),
			RiskNotes:            riskNotesFromSafety(state.Pack.SafetyReport),
			EvidenceRefs:         state.Pack.EvidenceRefs,
			Feasibility:          0.74,
			ValueScore:           0.82,
			Confidence:           0.72,
		})
	}
	return plans
}

func scriptReadinessFromState(state *ProjectUnderstandingState, scenarios []model.DemoScenarioPlan) *model.ScriptReadinessReport {
	blockers := []model.AgentFinding{}
	warnings := []model.AgentFinding{}
	missingInputs := []string{}
	hasCode := codeFileCountFromSnapshots(state.CodeSnapshots) > 0 || len(state.CodeSnapshots) > 0
	hasPage := len(state.PageSnapshots) > 0
	hasURL := strings.TrimSpace(state.Project.ProductURL) != ""
	if !hasURL {
		missingInputs = append(missingInputs, "product_url")
		warnings = append(warnings, model.AgentFinding{
			ID:              "readiness_missing_product_url",
			Kind:            "missing_product_url",
			Severity:        model.FindingSeverityWarning,
			Summary:         "未提供可访问产品 URL，将降级生成 inspect-only 或截图驱动脚本。",
			SuggestedAction: "补充测试环境 URL 可以生成真实浏览器导航步骤。",
			Confidence:      0.8,
		})
	}
	if !hasCode {
		missingInputs = append(missingInputs, "local_repo_path")
		warnings = append(warnings, model.AgentFinding{
			ID:              "readiness_missing_code_summary",
			Kind:            "missing_code_summary",
			Severity:        model.FindingSeverityWarning,
			Summary:         "代码结构摘要不足，功能能力与 selector 证据会偏弱。",
			SuggestedAction: "提供本地项目根目录以便 CodeReaderAgent 只读扫描。",
			Confidence:      0.76,
		})
	}
	if !hasPage {
		missingInputs = append(missingInputs, "page_snapshot")
		warnings = append(warnings, model.AgentFinding{
			ID:              "readiness_missing_page_snapshot",
			Kind:            "missing_page_snapshot",
			Severity:        model.FindingSeverityWarning,
			Summary:         "缺少页面截图/DOM 摘要，视觉叙事和等待条件需要保守生成。",
			SuggestedAction: "后续接入 Playwright 深度扫描或手动上传截图。",
			Confidence:      0.72,
		})
	}
	if !hasURL && !hasCode && !hasPage {
		blockers = append(blockers, model.AgentFinding{
			ID:              "readiness_no_executable_evidence",
			Kind:            "no_executable_evidence",
			Severity:        model.FindingSeverityBlocking,
			Summary:         "缺少 URL、代码摘要和页面材料，无法生成可信执行路径。",
			SuggestedAction: "至少补充一个产品 URL、本地项目根目录或页面截图。",
			Confidence:      0.92,
		})
	}
	codeInvestigation := codeInvestigationReadinessFromSnapshots(state.CodeSnapshots)
	if hasCode && !codeInvestigation.HasQuality {
		warnings = append(warnings, model.AgentFinding{
			ID:              "readiness_code_investigation_quality_missing",
			Kind:            "code_investigation_quality_missing",
			Severity:        model.FindingSeverityWarning,
			Summary:         "代码摘要缺少工具化调查质量记录，无法确认是否按需求小步读取代码。",
			SuggestedAction: "重新运行 CodeReaderAgent，让它通过 grep/shell_run/read_window 等工具链按需求补证据。",
			Confidence:      0.78,
		})
	}
	if codeInvestigation.HasQuality && !codeInvestigation.ToolDriven {
		warnings = append(warnings, model.AgentFinding{
			ID:              "readiness_code_investigation_not_tool_driven",
			Kind:            "code_investigation_not_tool_driven",
			Severity:        model.FindingSeverityWarning,
			Summary:         "代码调查没有体现工具化小步 drilldown，项目理解可能仍停留在宽泛摘要。",
			SuggestedAction: "让 planner 使用 grep_text、shell_run、read_window、find_references 等工具围绕需求目标继续调查。",
			Confidence:      0.82,
		})
	}
	if codeInvestigation.OverreadRisk == "high" {
		blockers = append(blockers, model.AgentFinding{
			ID:              "readiness_code_investigation_overread",
			Kind:            "code_investigation_overread",
			Severity:        model.FindingSeverityBlocking,
			Summary:         "代码调查读取范围过大，已阻止生成执行包，避免产生臃肿或低置信度脚本大纲。",
			Rationale:       codeInvestigation.Summary,
			SuggestedAction: "按需求关键词重新做小步工具调查，优先读取 route/component/API/style 相关文件，不要扫描全仓。",
			Confidence:      0.9,
		})
	}
	if codeInvestigation.OpenQuestionCount > 0 || len(codeInvestigation.Gaps) > 0 {
		warnings = append(warnings, model.AgentFinding{
			ID:              "readiness_code_investigation_open_questions",
			Kind:            "code_investigation_open_questions",
			Severity:        model.FindingSeverityWarning,
			Summary:         fmt.Sprintf("代码调查仍有 %d 个未解问题，脚本大纲需要在这些位置保留不确定项。", codeInvestigation.OpenQuestionCount),
			Rationale:       strings.Join(limitStrings(codeInvestigation.Gaps, 4), "；"),
			SuggestedAction: "继续围绕未解问题做 read_window、follow_imports 或 find_api_handlers，而不是扩大扫描范围。",
			Confidence:      0.78,
		})
	}
	selectorStats := selectorReadinessStats(state.Pack)
	selectorCoverage := selectorStats.Coverage
	if selectorCoverage < 0.35 {
		warnings = append(warnings, model.AgentFinding{
			ID:              "readiness_low_selector_coverage",
			Kind:            "low_selector_coverage",
			Severity:        model.FindingSeverityWarning,
			Summary:         "稳定 selector 覆盖不足，云端执行时更容易需要修复闭环。",
			SuggestedAction: "优先补充 data-testid、role/text selector 或页面可访问性摘要。",
			Confidence:      0.78,
		})
	}
	if selectorStats.BusinessActionCount == 0 {
		warnings = append(warnings, model.AgentFinding{
			ID:              "readiness_no_stable_business_action",
			Kind:            "no_stable_business_action",
			Severity:        model.FindingSeverityWarning,
			Summary:         "尚未识别到带稳定 selector 的业务动作，脚本会优先生成观察节点并等待补充页面证据。",
			SuggestedAction: "补充 DOM/页面扫描或 data-testid、role/name、按钮文案等稳定 selector。",
			Confidence:      0.82,
		})
	}
	if selectorStats.GenericSelectorCount > 0 {
		warnings = append(warnings, model.AgentFinding{
			ID:              "readiness_generic_selectors",
			Kind:            "generic_selectors",
			Severity:        model.FindingSeverityWarning,
			Summary:         "识别到 body/main/section/div 等泛 selector，业务动作会自动降级，避免云端录制 selector_timeout。",
			SuggestedAction: "为关键按钮、输入框和状态区域补充更稳定的 selector。",
			Confidence:      0.8,
		})
	}
	if selectorStats.LoginActionCount > 1 {
		warnings = append(warnings, model.AgentFinding{
			ID:              "readiness_login_duplication",
			Kind:            "login_duplication",
			Severity:        model.FindingSeverityWarning,
			Summary:         "候选动作中出现多次登录相关动作，生成脚本时应只登录一次并复用会话。",
			SuggestedAction: "把登录作为前置 session stage，后续演示节点不要重复跳回登录页。",
			Confidence:      0.78,
		})
	}
	credentialCoverage := !routesNeedAuth(state.Pack.Architecture.RouteTree) || hasProjectCredential(state.Project)
	if !credentialCoverage {
		warnings = append(warnings, model.AgentFinding{
			ID:              "readiness_auth_without_credential",
			Kind:            "credential_scope_missing",
			Severity:        model.FindingSeverityWarning,
			Summary:         "部分路由可能需要登录，但当前未声明 credential secret_ref。",
			SuggestedAction: "审批上传前补充凭据授权范围，禁止在脚本中写入明文账号密码。",
			Confidence:      0.74,
		})
	}
	recommended := firstScenario(scenarios)
	stageCount := maxInt(4, recommended.EstimatedSteps)
	targetDuration := explicitTotalStageDurationSec(state.Pack.BusinessStagePlan)
	if targetDuration == 0 && recommended.EstimatedDurationSec > 0 {
		targetDuration = recommended.EstimatedDurationSec
	}
	canProceed := len(blockers) == 0
	summary := "脚本生成可继续，需在审批页复核安全策略和凭据范围。"
	if !canProceed {
		summary = "脚本生成存在阻塞，需要补充输入材料。"
	}
	return &model.ScriptReadinessReport{
		ID:                            "script_readiness_" + state.Project.ID,
		ProjectID:                     state.Project.ID,
		SchemaVersion:                 model.ScriptReadinessReportSchemaVersion,
		CanProceed:                    canProceed,
		Summary:                       summary,
		Blockers:                      blockers,
		Warnings:                      warnings,
		MissingInputs:                 uniqueStrings(missingInputs),
		RepairSuggestions:             readinessRepairSuggestions(blockers, warnings),
		RecommendedScenarioID:         recommended.ID,
		RecommendedScenarioName:       recommended.Name,
		SuggestedStageCount:           stageCount,
		SuggestedTargetDurationSec:    targetDuration,
		SelectorCoverage:              selectorCoverage,
		BusinessActionCount:           selectorStats.BusinessActionCount,
		GenericSelectorCount:          selectorStats.GenericSelectorCount,
		LoginActionCount:              selectorStats.LoginActionCount,
		LoginDuplication:              selectorStats.LoginActionCount > 1,
		MinStageDurationMS:            explicitMinimumStageDurationMS(state.Pack.BusinessStagePlan),
		BlockingAssertionRiskCount:    selectorStats.BlockingAssertionRiskCount,
		CodeInvestigationToolDriven:   codeInvestigation.ToolDriven,
		CodeInvestigationOverreadRisk: codeInvestigation.OverreadRisk,
		CodeInvestigationSpecializedToolCallCount: codeInvestigation.SpecializedToolCallCount,
		CodeInvestigationOpenQuestionCount:        codeInvestigation.OpenQuestionCount,
		CodeInvestigationSummary:                  codeInvestigation.Summary,
		CodeInvestigationGaps:                     codeInvestigation.Gaps,
		CredentialCoverage:                        credentialCoverage,
		EvidenceRefs:                              state.Pack.EvidenceRefs,
		Confidence:                                0.78,
		CreatedAt:                                 time.Now().UTC(),
	}
}

type codeInvestigationReadinessMetrics struct {
	HasQuality               bool
	ToolDriven               bool
	OverreadRisk             string
	SpecializedToolCallCount int
	OpenQuestionCount        int
	Gaps                     []string
	Summary                  string
}

func codeInvestigationReadinessFromSnapshots(snapshots []model.CodeUnderstandingSnapshot) codeInvestigationReadinessMetrics {
	metrics := codeInvestigationReadinessMetrics{}
	risks := []string{}
	summaries := []string{}
	for _, snapshot := range snapshots {
		quality := snapshot.InvestigationQuality
		if quality == nil {
			continue
		}
		metrics.HasQuality = true
		metrics.ToolDriven = metrics.ToolDriven || quality.ToolDriven
		metrics.SpecializedToolCallCount += quality.SpecializedToolCallCount
		metrics.OpenQuestionCount += quality.OpenQuestionCount
		metrics.Gaps = append(metrics.Gaps, quality.RemainingGaps...)
		if strings.TrimSpace(quality.OverreadRisk) != "" {
			risks = append(risks, quality.OverreadRisk)
		}
		if strings.TrimSpace(quality.Summary) != "" {
			summaries = append(summaries, quality.Summary)
		}
	}
	metrics.OverreadRisk = worstCodeInvestigationOverreadRisk(risks)
	metrics.Gaps = limitStrings(uniqueStrings(metrics.Gaps), 8)
	if len(summaries) > 0 {
		metrics.Summary = strings.Join(limitStrings(uniqueStrings(summaries), 2), " ")
	} else if metrics.HasQuality {
		metrics.Summary = fmt.Sprintf("代码调查质量：专用工具 %d 次，未解问题 %d 个，过读风险=%s。", metrics.SpecializedToolCallCount, metrics.OpenQuestionCount, firstNonEmpty(metrics.OverreadRisk, "unknown"))
	}
	return metrics
}

func worstCodeInvestigationOverreadRisk(risks []string) string {
	seen := map[string]bool{}
	for _, risk := range risks {
		seen[strings.ToLower(strings.TrimSpace(risk))] = true
	}
	switch {
	case seen["high"]:
		return "high"
	case seen["medium"]:
		return "medium"
	case seen["low"]:
		return "low"
	case seen["unknown"]:
		return "unknown"
	default:
		return ""
	}
}

type projectIntelligenceLLMOutput struct {
	Summary   string `json:"summary"`
	Scenarios []struct {
		ID               string              `json:"id"`
		Name             string              `json:"name"`
		Objective        string              `json:"objective"`
		ValueProposition string              `json:"value_proposition"`
		NarrativeBeats   flexibleStringSlice `json:"narrative_beats"`
		RiskNotes        flexibleStringSlice `json:"risk_notes"`
		Confidence       float64             `json:"confidence"`
	} `json:"scenarios"`
	ReadinessNotes flexibleStringSlice `json:"readiness_notes"`
	Confidence     float64             `json:"confidence"`
}

func (o *projectIntelligenceLLMOutput) UnmarshalJSON(data []byte) error {
	type alias projectIntelligenceLLMOutput
	var single alias
	if err := json.Unmarshal(data, &single); err == nil {
		*o = projectIntelligenceLLMOutput(single)
		o.normalize()
		return nil
	}
	var items []alias
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}
	merged := projectIntelligenceLLMOutput{}
	for _, item := range items {
		patch := projectIntelligenceLLMOutput(item)
		if merged.Summary == "" {
			merged.Summary = patch.Summary
		}
		merged.Scenarios = append(merged.Scenarios, patch.Scenarios...)
		merged.ReadinessNotes = append(merged.ReadinessNotes, patch.ReadinessNotes...)
		if patch.Confidence > merged.Confidence {
			merged.Confidence = patch.Confidence
		}
	}
	merged.normalize()
	*o = merged
	return nil
}

func (o *projectIntelligenceLLMOutput) normalize() {
	o.ReadinessNotes = flexibleStringSlice(uniqueStrings([]string(o.ReadinessNotes)))
}

func (g *ProjectIntelligenceGraph) enhancePackWithLLM(ctx context.Context, project *model.ProjectContext, brief *model.RequirementBrief, pack *model.ProjectIntelligencePack) (*llm.CallTrace, error) {
	if g == nil || g.llm == nil || project == nil || pack == nil {
		return nil, nil
	}
	payload := map[string]any{
		"target_audience": project.TargetAudience,
		"brief":           brief,
		"architecture": map[string]any{
			"summary":       pack.Architecture.Summary,
			"frameworks":    pack.Architecture.Frameworks,
			"languages":     pack.Architecture.Languages,
			"module_count":  len(pack.Architecture.Modules),
			"route_count":   len(pack.Architecture.RouteTree),
			"source_digest": pack.SourceDigestSHA256,
		},
		"feature_capabilities": compactCapabilities(pack.FeatureCapabilities),
		"interaction_surfaces": compactSurfaces(pack.InteractionSurfaces),
		"api_count":            len(pack.APIContracts),
		"data_model_count":     len(pack.DataModels),
		"script_readiness":     pack.ScriptReadinessReport,
	}
	data, _ := json.Marshal(payload)
	var output projectIntelligenceLLMOutput
	trace, err := g.llm.GenerateJSON(ctx, config.ModelTaskPlanning, llm.JSONRequest{
		System:       "你是 Cascade DemoOps 的项目智能理解 graph critic。请只基于结构摘要、hash、selector 和 evidence ref 润色项目理解摘要与候选演示路径。禁止输出源码、绝对路径、密钥或完整 HTML。",
		User:         string(data),
		SchemaName:   "ProjectIntelligencePatch",
		ResponseHint: "返回字段：summary, scenarios[{id,name,objective,value_proposition,narrative_beats,risk_notes,confidence}], readiness_notes, confidence。",
		MaxTokens:    1800,
		Temperature:  0.18,
	}, &output)
	if err != nil {
		return trace, err
	}
	if output.Summary != "" && pack.Architecture != nil {
		pack.Architecture.Summary = output.Summary
	}
	for _, patch := range output.Scenarios {
		for i := range pack.DemoScenarioPlans {
			if patch.ID != "" && pack.DemoScenarioPlans[i].ID != patch.ID {
				continue
			}
			if patch.Name != "" {
				pack.DemoScenarioPlans[i].Name = patch.Name
			}
			if patch.Objective != "" {
				pack.DemoScenarioPlans[i].Objective = patch.Objective
			}
			if patch.ValueProposition != "" {
				pack.DemoScenarioPlans[i].ValueProposition = patch.ValueProposition
			}
			pack.DemoScenarioPlans[i].NarrativeBeats = uniqueStrings(append(pack.DemoScenarioPlans[i].NarrativeBeats, stringSlice(patch.NarrativeBeats)...))
			pack.DemoScenarioPlans[i].RiskNotes = uniqueStrings(append(pack.DemoScenarioPlans[i].RiskNotes, stringSlice(patch.RiskNotes)...))
			if patch.Confidence > 0 {
				pack.DemoScenarioPlans[i].Confidence = patch.Confidence
			}
			break
		}
	}
	if pack.ScriptReadinessReport != nil {
		pack.ScriptReadinessReport.RepairSuggestions = uniqueStrings(append(pack.ScriptReadinessReport.RepairSuggestions, stringSlice(output.ReadinessNotes)...))
	}
	if output.Confidence > 0 {
		pack.Confidence = output.Confidence
	}
	return trace, nil
}

func traceGraphStep(
	ctx context.Context,
	trace *model.AgentGraphTrace,
	nodeID string,
	agent string,
	tool string,
	inputSummary string,
	work func() (string, float64, []model.EvidenceRef, string, error),
) error {
	started := time.Now().UTC()
	output, confidence, evidenceRefs, fallback, err := work()
	completed := time.Now().UTC()
	status := "completed"
	if err != nil {
		status = "failed"
		output = err.Error()
	}
	if ctxErr := ctx.Err(); ctxErr != nil && err == nil {
		err = ctxErr
		status = "failed"
		output = ctxErr.Error()
	}
	trace.Steps = append(trace.Steps, model.AgentGraphTraceStep{
		ID:             "trace_step_" + shortHash(nodeID+agent+tool+started.String()),
		NodeID:         nodeID,
		Agent:          agent,
		Tool:           tool,
		Status:         status,
		InputSummary:   inputSummary,
		OutputSummary:  output,
		ElapsedMS:      completed.Sub(started).Milliseconds(),
		Confidence:     confidence,
		FallbackReason: fallback,
		EvidenceRefs:   uniqueEvidenceRefs(evidenceRefs),
		StartedAt:      started,
		CompletedAt:    completed,
	})
	return err
}

func finalizeProjectIntelligencePack(pack *model.ProjectIntelligencePack) {
	if pack == nil {
		return
	}
	pack.EvidenceRefs = uniqueEvidenceRefs(pack.EvidenceRefs)
	if pack.Architecture == nil {
		return
	}
	if pack.Confidence <= 0 {
		pack.Confidence = pack.Architecture.Confidence
	}
}

func graphToolInputSummary(state *ProjectUnderstandingState) string {
	return fmt.Sprintf("brief=%t code_snapshots=%d code_files=%d code_tools=%d page_snapshots=%d", state.Brief != nil, len(state.CodeSnapshots), codeFileCountFromSnapshots(state.CodeSnapshots), codeInvestigationToolCallCount(state.CodeSnapshots), len(state.PageSnapshots))
}

func codeInvestigationToolCallCount(snapshots []model.CodeUnderstandingSnapshot) int {
	count := 0
	for _, snapshot := range snapshots {
		if snapshot.InvestigationTrace != nil {
			count += len(snapshot.InvestigationTrace.ToolCalls)
		}
	}
	return count
}

func architectureSummary(frameworks []string, languages []string, modules []model.ProjectModule) string {
	parts := []string{}
	if len(frameworks) > 0 {
		parts = append(parts, "框架："+strings.Join(frameworks, "、"))
	}
	if len(languages) > 0 {
		parts = append(parts, "语言："+strings.Join(languages, "、"))
	}
	parts = append(parts, fmt.Sprintf("模块边界：%d 个", len(modules)))
	return strings.Join(parts, "；")
}

func architectureConfidence(snapshots []model.CodeUnderstandingSnapshot) float64 {
	if len(snapshots) == 0 {
		return 0.54
	}
	files := codeFileCountFromSnapshots(snapshots)
	switch {
	case files > 100:
		return 0.82
	case files > 20:
		return 0.76
	default:
		return 0.68
	}
}

func moduleKind(frameworks []string, languages []string) string {
	values := strings.ToLower(strings.Join(append(frameworks, languages...), "|"))
	switch {
	case strings.Contains(values, "react"), strings.Contains(values, "vue"), strings.Contains(values, "svelte"), strings.Contains(values, "vite"), strings.Contains(values, "next"):
		return "frontend"
	case strings.Contains(values, "gin"), strings.Contains(values, "fiber"), strings.Contains(values, "echo"), strings.Contains(values, "go"):
		return "backend"
	default:
		return "mixed"
	}
}

func moduleResponsibility(snapshot model.CodeUnderstandingSnapshot) string {
	parts := []string{}
	if len(snapshot.Routes) > 0 {
		parts = append(parts, fmt.Sprintf("路由 %d 个", len(snapshot.Routes)))
	}
	if len(snapshot.Components) > 0 {
		parts = append(parts, fmt.Sprintf("组件 %d 个", len(snapshot.Components)))
	}
	if len(snapshot.APIEndpoints) > 0 {
		parts = append(parts, fmt.Sprintf("API %d 个", len(snapshot.APIEndpoints)))
	}
	styleFiles := 0
	for _, digest := range snapshot.PathDigests {
		if digest.Kind == "style" {
			styleFiles++
		}
	}
	if styleFiles > 0 {
		parts = append(parts, fmt.Sprintf("样式定义 %d 个", styleFiles))
	}
	if len(parts) == 0 {
		return firstNonEmpty(snapshot.Summary, "项目结构摘要模块。")
	}
	return "负责" + strings.Join(parts, "、") + "的摘要理解。"
}

func packageManagersFromFrameworks(frameworks []string, languages []string) []string {
	joined := strings.ToLower(strings.Join(append(frameworks, languages...), "|"))
	managers := []string{}
	if strings.Contains(joined, "react") || strings.Contains(joined, "next") || strings.Contains(joined, "vite") || strings.Contains(joined, "vue") || strings.Contains(joined, "svelte") {
		managers = append(managers, "npm/pnpm")
	}
	if strings.Contains(joined, "go") || strings.Contains(joined, "gin") || strings.Contains(joined, "fiber") || strings.Contains(joined, "echo") {
		managers = append(managers, "go modules")
	}
	if len(managers) == 0 {
		managers = append(managers, "unknown")
	}
	return uniqueStrings(managers)
}

func runtimeTargetsFromFrameworks(frameworks []string, languages []string) []string {
	joined := strings.ToLower(strings.Join(append(frameworks, languages...), "|"))
	targets := []string{}
	if strings.Contains(joined, "react") || strings.Contains(joined, "next") || strings.Contains(joined, "vite") || strings.Contains(joined, "vue") || strings.Contains(joined, "svelte") {
		targets = append(targets, "browser")
	}
	if strings.Contains(joined, "go") || strings.Contains(joined, "gin") || strings.Contains(joined, "fiber") || strings.Contains(joined, "echo") {
		targets = append(targets, "server")
	}
	if strings.Contains(joined, "wails") {
		targets = append(targets, "desktop")
	}
	if len(targets) == 0 {
		targets = append(targets, "unknown")
	}
	return uniqueStrings(targets)
}

func pathFromURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Path == "" {
		return ""
	}
	if parsed.Path == "/" {
		return "/"
	}
	return strings.TrimRight(parsed.Path, "/")
}

func routeNameFromPath(path string) string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return "首页"
	}
	parts := strings.Split(trimmed, "/")
	return parts[len(parts)-1]
}

func parentRoutePath(path string) string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" || !strings.Contains(trimmed, "/") {
		return ""
	}
	parts := strings.Split(trimmed, "/")
	return "/" + strings.Join(parts[:len(parts)-1], "/")
}

func selectorsFromCodeSnapshots(snapshots []model.CodeUnderstandingSnapshot) []model.SelectorCandidate {
	candidates := []model.SelectorCandidate{}
	for _, snapshot := range snapshots {
		for _, selector := range snapshot.Selectors {
			if selector.Value == "" ||
				selectorLooksGeneric(selector.Value) ||
				selectorLooksReadOnlySurface(selector.Value) ||
				selectorLooksLikeChromeControl(selector.Value) {
				continue
			}
			candidates = append(candidates, model.SelectorCandidate{
				Kind:           selector.Kind,
				Value:          selector.Value,
				Confidence:     selector.Confidence,
				StabilityScore: selector.StabilityScore,
				Source:         "code_reader",
				EvidenceRefs:   selector.EvidenceRefs,
			})
		}
	}
	return uniqueSelectorCandidates(candidates)
}

func uniqueSelectorCandidates(values []model.SelectorCandidate) []model.SelectorCandidate {
	seen := map[string]bool{}
	out := make([]model.SelectorCandidate, 0, len(values))
	for _, value := range values {
		key := value.Kind + "|" + value.Value
		if strings.TrimSpace(value.Value) == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func limitSelectorCandidates(values []model.SelectorCandidate, limit int) []model.SelectorCandidate {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

func waitHintsForSurface(pageURL string, selectors []model.SelectorCandidate, states []string) []string {
	hints := []string{"等待 body 可见", "截图前保持页面状态稳定"}
	if isHTTPURL(pageURL) {
		hints = append(hints, "导航后等待 domcontentloaded，并确认主要区域可见")
	}
	if len(selectors) > 0 {
		hints = append(hints, "优先等待稳定 selector："+selectors[0].Value)
	}
	if len(states) > 0 {
		hints = append(hints, "确认页面状态："+strings.Join(states, "、"))
	}
	return uniqueStrings(hints)
}

func featureRefsFromPageActions(actions []model.PageActionInsight) []string {
	refs := []string{}
	for _, action := range actions {
		if action.FeatureRef != "" {
			refs = append(refs, action.FeatureRef)
		}
	}
	return uniqueStrings(refs)
}

func codeEvidenceRefs(snapshots []model.CodeUnderstandingSnapshot) []model.EvidenceRef {
	refs := []model.EvidenceRef{}
	for _, snapshot := range snapshots {
		refs = append(refs, snapshot.EvidenceRefs...)
	}
	return uniqueEvidenceRefs(refs)
}

func sensitiveFieldNames(values []model.SensitiveFieldFinding) []string {
	names := []string{}
	for _, value := range values {
		names = append(names, value.Name)
	}
	return uniqueStrings(names)
}

func inferMethodFromPath(path string) string {
	lower := strings.ToLower(path)
	switch {
	case strings.Contains(lower, "create"), strings.Contains(lower, "invite"), strings.Contains(lower, "submit"):
		return "POST"
	case strings.Contains(lower, "delete"), strings.Contains(lower, "remove"):
		return "DELETE"
	default:
		return "GET"
	}
}

func apiLikelyRequiresAuth(path string, sensitive []string) bool {
	lower := strings.ToLower(path)
	return strings.Contains(lower, "user") || strings.Contains(lower, "team") || strings.Contains(lower, "account") || strings.Contains(lower, "project") || len(sensitive) > 0
}

func apiFieldHints(path string) []string {
	lower := strings.ToLower(path)
	fields := []string{"id"}
	for _, token := range []string{"email", "team", "project", "invite", "message", "password"} {
		if strings.Contains(lower, token) {
			fields = append(fields, token)
		}
	}
	return uniqueStrings(fields)
}

func dedupeAPIContracts(values []model.APIContractSummary) []model.APIContractSummary {
	seen := map[string]bool{}
	out := []model.APIContractSummary{}
	for _, value := range values {
		key := strings.ToUpper(value.Method) + " " + value.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func sensitiveModelFields(dataModel model.DataModelInsight, fallback []string) []string {
	fields := append([]string{}, fallback...)
	for _, field := range dataModel.Fields {
		if field.Sensitive {
			fields = append(fields, field.Name)
		}
	}
	return uniqueStrings(fields)
}

func dedupeDataModels(values []model.ProjectDataModelSummary) []model.ProjectDataModelSummary {
	seen := map[string]bool{}
	out := []model.ProjectDataModelSummary{}
	for _, value := range values {
		if value.Name == "" || seen[value.ID] {
			continue
		}
		seen[value.ID] = true
		out = append(out, value)
	}
	return out
}

func routeIDs(routes []model.ArchitectureRouteNode, limit int) []string {
	out := []string{}
	for _, route := range routes {
		out = append(out, route.ID)
	}
	return limitStrings(uniqueStrings(out), limit)
}

func surfaceIDs(surfaces []model.InteractionSurface, limit int) []string {
	out := []string{}
	for _, surface := range surfaces {
		out = append(out, surface.ID)
	}
	return limitStrings(uniqueStrings(out), limit)
}

func moduleComponentRefs(modules []model.ProjectModule, limit int) []string {
	out := []string{}
	for _, module := range modules {
		out = append(out, module.ComponentRefs...)
	}
	return limitStrings(uniqueStrings(out), limit)
}

func apiIDs(values []model.APIContractSummary, limit int) []string {
	out := []string{}
	for _, value := range values {
		out = append(out, value.ID)
	}
	return limitStrings(uniqueStrings(out), limit)
}

func dataModelIDs(values []model.ProjectDataModelSummary, limit int) []string {
	out := []string{}
	for _, value := range values {
		out = append(out, value.ID)
	}
	return limitStrings(uniqueStrings(out), limit)
}

func capabilityIDs(values []model.FeatureCapability, limit int) []string {
	out := []string{}
	for _, value := range values {
		out = append(out, value.ID)
	}
	return limitStrings(uniqueStrings(out), limit)
}

func keyActionsFromBriefAndSurfaces(brief *model.RequirementBrief, surfaces []model.InteractionSurface) []string {
	actions := []string{}
	if brief != nil {
		actions = append(actions, brief.MustShow...)
	}
	for _, surface := range surfaces {
		for _, action := range surface.Actions {
			actions = append(actions, firstNonEmpty(action.Label, action.Kind))
		}
	}
	if len(actions) == 0 {
		actions = append(actions, "navigate", "inspect", "capture", "validate")
	}
	return limitStrings(uniqueStrings(actions), 12)
}

func riskNotesFromSafety(safety *model.SafetyReport) []string {
	if safety == nil {
		return nil
	}
	out := append([]string{}, safety.Notes...)
	for _, finding := range safety.PolicyFindings {
		out = append(out, finding.Summary)
	}
	return limitStrings(uniqueStrings(out), 10)
}

func narrativeBeatsFromPack(pack *model.ProjectIntelligencePack) []string {
	beats := []string{"打开产品入口并确认页面稳定", "说明业务目标和目标受众"}
	for _, capability := range pack.FeatureCapabilities {
		if capability.Name != "" {
			beats = append(beats, "展示："+capability.Name)
		}
		if len(beats) >= 5 {
			break
		}
	}
	beats = append(beats, "截图/录屏证明核心结果", "收束到审批和安全边界")
	return uniqueStrings(beats)
}

func suggestedStageCount(pack *model.ProjectIntelligencePack) int {
	count := 4
	if len(pack.FeatureCapabilities) >= 2 {
		count++
	}
	if len(pack.InteractionSurfaces) >= 2 {
		count++
	}
	if count > 7 {
		return 7
	}
	return count
}

func firstScenario(plans []model.DemoScenarioPlan) model.DemoScenarioPlan {
	if len(plans) == 0 {
		return model.DemoScenarioPlan{ID: "scenario_primary", Name: "默认演示路径", EstimatedSteps: 4, EstimatedDurationSec: 60}
	}
	return plans[0]
}

func firstScenarioName(plans []model.DemoScenarioPlan) string {
	return firstScenario(plans).Name
}

func selectorCoverage(pack *model.ProjectIntelligencePack) float64 {
	return selectorReadinessStats(pack).Coverage
}

type selectorReadinessMetrics struct {
	Coverage                   float64
	BusinessActionCount        int
	GenericSelectorCount       int
	LoginActionCount           int
	BlockingAssertionRiskCount int
}

func selectorReadinessStats(pack *model.ProjectIntelligencePack) selectorReadinessMetrics {
	metrics := selectorReadinessMetrics{}
	if pack == nil || len(pack.InteractionSurfaces) == 0 {
		return metrics
	}
	actionCount := 0
	for _, surface := range pack.InteractionSurfaces {
		for _, selector := range surface.StableSelectors {
			if selectorLooksGeneric(selector.Value) {
				metrics.GenericSelectorCount++
			}
		}
		for _, action := range surface.Actions {
			actionCount++
			actionType := graphActionTypeFromKind(action.Kind, action.Selector)
			if looksLikeLoginAction(action.Label, action.Selector) {
				metrics.LoginActionCount++
			}
			if selectorLooksGeneric(action.Selector) {
				metrics.GenericSelectorCount++
			}
			if actionType == model.GraphActionAssert && !selectorUsableForBlockingAssertion(action.Selector) {
				metrics.BlockingAssertionRiskCount++
			}
			if isBusinessAction(actionType) && selectorUsableForBusinessAction(action.Selector) {
				metrics.BusinessActionCount++
			}
		}
	}
	if actionCount == 0 {
		if len(pack.InteractionSurfaces) > 0 {
			for _, surface := range pack.InteractionSurfaces {
				for _, selector := range surface.StableSelectors {
					if selectorUsableForBusinessAction(selector.Value) {
						metrics.Coverage = 1
						return metrics
					}
				}
			}
		}
		return metrics
	}
	metrics.Coverage = float64(metrics.BusinessActionCount) / float64(actionCount)
	if metrics.Coverage > 1 {
		metrics.Coverage = 1
	}
	return metrics
}

func routesNeedAuth(routes []model.ArchitectureRouteNode) bool {
	for _, route := range routes {
		if route.AuthRequired {
			return true
		}
	}
	return false
}

func hasProjectCredential(project *model.ProjectContext) bool {
	return project != nil && project.Inputs != nil && len(project.Inputs.Credentials) > 0
}

func readinessRepairSuggestions(blockers []model.AgentFinding, warnings []model.AgentFinding) []string {
	suggestions := []string{}
	for _, finding := range append(blockers, warnings...) {
		if finding.SuggestedAction != "" {
			suggestions = append(suggestions, finding.SuggestedAction)
		}
	}
	if len(suggestions) == 0 {
		suggestions = append(suggestions, "审批前复核 allowed domains、凭据范围和打码选择器。")
	}
	return uniqueStrings(suggestions)
}

func explicitMinimumStageDurationMS(plan *model.BusinessStagePlan) int {
	if plan == nil {
		return 0
	}
	minDuration := 0
	for _, stage := range plan.Stages {
		if stage.DurationMS <= 0 {
			continue
		}
		if minDuration == 0 || stage.DurationMS < minDuration {
			minDuration = stage.DurationMS
		}
	}
	return minDuration
}

func explicitTotalStageDurationSec(plan *model.BusinessStagePlan) int {
	if plan == nil {
		return 0
	}
	totalMS := 0
	for _, stage := range plan.Stages {
		if stage.DurationMS > 0 {
			totalMS += stage.DurationMS
		}
	}
	if totalMS <= 0 {
		return 0
	}
	return (totalMS + 999) / 1000
}

func criticOutputSummary(readiness *model.ScriptReadinessReport) string {
	if readiness == nil {
		return "缺少脚本可行性报告，需回退到保守脚本。"
	}
	if !readiness.CanProceed {
		return fmt.Sprintf("发现 %d 个阻塞，需补齐输入后重跑相关节点。", len(readiness.Blockers))
	}
	return fmt.Sprintf("通过一致性审查：warnings=%d，selector coverage=%.0f%%。", len(readiness.Warnings), readiness.SelectorCoverage*100)
}

func businessIntentGoalCount(goals []model.DemoIntentGoal) int {
	count := 0
	for _, goal := range goals {
		if goal.BusinessCritical {
			count++
		}
	}
	return count
}

func intentTextContains(state *ProjectUnderstandingState, tokens ...string) bool {
	return keywordMatchScore(tokens, strings.Join(requirementGoalTexts(state), " ")) > 0
}

func requirementGoalTexts(state *ProjectUnderstandingState) []string {
	values := []string{}
	if state == nil {
		return values
	}
	if state.Project != nil {
		values = append(values, state.Project.ProductDescription)
		values = append(values, state.Project.MustShow...)
	}
	if state.Brief != nil {
		values = append(values, state.Brief.Scenario, state.Brief.Objective, state.Brief.PrimaryOutcome)
		values = append(values, state.Brief.MustShow...)
	}
	return uniqueStrings(values)
}

func intentGoalFromText(text string) model.DemoIntentGoal {
	label := strings.TrimSpace(text)
	if label == "" {
		return model.DemoIntentGoal{}
	}
	keywords := intentKeywordsForText(label)
	kind := "business_action"
	preferredAction := "click"
	success := "目标业务状态可见"
	lower := strings.ToLower(label)
	switch {
	case containsAny(lower, "登录", "login", "signin", "sign in", "密码", "password"):
		kind = "auth"
		preferredAction = "fill"
		success = "进入已登录状态"
	case containsAny(lower, "搜索", "search", "筛选", "filter"):
		preferredAction = "fill"
		success = "搜索或筛选结果可见"
	case containsAny(lower, "填写", "输入", "描述需求", "需求描述", "fill", "enter", "type", "prompt"):
		preferredAction = "fill"
		success = "输入内容已填写并可供下一步提交"
	case containsAny(lower, "上传", "upload", "导入", "import"):
		preferredAction = "upload"
		success = "上传结果或导入状态可见"
	case containsAny(lower, "查看", "展示", "说明", "inspect", "observe"):
		preferredAction = "inspect"
		success = "目标区域可见"
	}
	return model.DemoIntentGoal{
		ID:               "intent_" + shortHash(label),
		Label:            label,
		Kind:             kind,
		Required:         true,
		BusinessCritical: kind != "auth" && preferredAction != "inspect",
		TargetKeywords:   keywords,
		PreferredAction:  preferredAction,
		SuccessState:     success,
		Confidence:       0.72,
	}
}

func containsIntentGoal(goals []model.DemoIntentGoal, id string, label string) bool {
	for _, goal := range goals {
		if goal.ID == id || strings.EqualFold(goal.Label, label) {
			return true
		}
	}
	return false
}

func hasBusinessIntentGoal(goals []model.DemoIntentGoal) bool {
	for _, goal := range goals {
		if goal.BusinessCritical {
			return true
		}
	}
	return false
}

func intentKeywordsForText(text string) []string {
	lower := strings.ToLower(text)
	keywords := []string{}
	dictionary := []string{
		"新建", "创建", "项目", "工程", "create", "new", "project",
		"填写", "输入", "选择", "启动", "开始", "观察", "fill", "enter", "select", "start", "observe",
		"邀请", "成员", "团队", "invite", "member", "team",
		"生成", "构建", "执行", "运行", "generate", "build", "run", "agent",
		"上传", "导入", "upload", "import",
		"搜索", "筛选", "search", "filter",
		"登录", "邮箱", "密码", "login", "signin", "email", "password",
		"成功", "完成", "状态", "success", "complete", "done",
	}
	for _, token := range dictionary {
		if strings.Contains(lower, strings.ToLower(token)) {
			keywords = append(keywords, token)
		}
	}
	semanticAliases := []struct {
		matches []string
		aliases []string
	}{
		{[]string{"项目", "工程", "project"}, []string{"项目", "project"}},
		{[]string{"填写", "输入", "描述需求", "需求描述", "fill", "enter", "type", "prompt"}, []string{"填写", "输入", "fill", "input", "enter", "idea", "prompt", "description"}},
		{[]string{"新建", "创建", "新增", "create", "new"}, []string{"新建", "创建", "create", "new"}},
		{[]string{"生成", "构建", "generate", "build"}, []string{"生成", "构建", "generate", "build"}},
	}
	for _, group := range semanticAliases {
		if containsAny(lower, group.matches...) {
			keywords = append(keywords, group.aliases...)
		}
	}
	for _, token := range strings.FieldsFunc(lower, func(r rune) bool {
		return r == ' ' || r == ',' || r == ';' || r == '，' || r == '。' || r == '/' || r == '-' || r == '_' || r == ':'
	}) {
		token = strings.TrimSpace(token)
		if len([]rune(token)) >= 2 && !containsAny(token, "http", "https") {
			keywords = append(keywords, token)
		}
	}
	if len(keywords) == 0 && strings.TrimSpace(text) != "" {
		keywords = append(keywords, strings.TrimSpace(text))
	}
	return limitStrings(uniqueStrings(keywords), 16)
}

func requirementEvidenceRefs(brief *model.RequirementBrief) []model.EvidenceRef {
	if brief == nil {
		return nil
	}
	return brief.EvidenceRefs
}

func intentConfidence(goals []model.DemoIntentGoal) float64 {
	if len(goals) == 0 {
		return 0.4
	}
	if hasBusinessIntentGoal(goals) {
		return 0.78
	}
	return 0.62
}

func keywordMatchScore(keywords []string, values ...string) int {
	if len(keywords) == 0 {
		return 0
	}
	haystack := strings.ToLower(strings.Join(values, " "))
	if strings.TrimSpace(haystack) == "" {
		return 0
	}
	score := 0
	for _, keyword := range keywords {
		keyword = strings.ToLower(strings.TrimSpace(keyword))
		if keyword == "" {
			continue
		}
		if strings.Contains(haystack, keyword) {
			score++
		}
	}
	return score
}

func dataFieldNames(fields []model.DataField) []string {
	names := []string{}
	for _, field := range fields {
		names = append(names, field.Name)
	}
	return uniqueStrings(names)
}

func interactionProbeGoalScore(goal model.DemoIntentGoal, probe model.InteractionProbe) float64 {
	keywords := goal.TargetKeywords
	if len(keywords) == 0 {
		keywords = intentKeywordsForText(goal.Label)
	}
	match := keywordMatchScore(keywords, probe.Label, probe.Selector, probe.ComponentRef, probe.RouteRef, probe.URL)
	if match == 0 {
		return 0
	}
	score := float64(match*20) + float64(probe.SelectorScore)
	if probe.Source == "page_reader" {
		score += 20
	}
	if probe.IsBusiness {
		score += 30
	}
	if goal.PreferredAction != "" && graphActionTypeFromKind(probe.Kind, probe.Selector) == graphActionTypeFromKind(goal.PreferredAction, probe.Selector) {
		score += 15
	}
	if looksLikeLoginAction(probe.Label, probe.Selector) && goal.Kind != "auth" {
		score -= 80
	}
	if probe.IsChrome {
		score -= 120
	}
	if selectorLooksGeneric(probe.Selector) {
		score -= 60
	}
	return score
}

func featureTraceConfidence(traces []model.FeatureGoalTrace) float64 {
	if len(traces) == 0 {
		return 0.42
	}
	total := 0.0
	for _, trace := range traces {
		total += trace.Confidence
	}
	return total / float64(len(traces))
}

func goalTraceConfidence(trace model.FeatureGoalTrace) float64 {
	score := 0.45
	if len(trace.MatchedRouteRefs) > 0 {
		score += 0.12
	}
	if len(trace.MatchedComponents) > 0 {
		score += 0.1
	}
	if len(trace.MatchedAPIRefs) > 0 {
		score += 0.06
	}
	if len(trace.SelectorEvidence) > 0 {
		score += 0.2
	}
	if len(trace.MissingEvidence) > 0 {
		score -= 0.12
	}
	if score < 0.1 {
		return 0.1
	}
	if score > 0.95 {
		return 0.95
	}
	return score
}

func dedupeInteractionProbes(values []model.InteractionProbe) []model.InteractionProbe {
	seen := map[string]bool{}
	out := make([]model.InteractionProbe, 0, len(values))
	for _, value := range values {
		key := value.Source + "|" + value.Selector + "|" + value.Label
		if strings.TrimSpace(value.Selector) == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func codeFileCountFromSnapshots(snapshots []model.CodeUnderstandingSnapshot) int {
	total := 0
	for _, snapshot := range snapshots {
		total += snapshot.FileCount
	}
	return total
}

func uniqueEvidenceRefs(values []model.EvidenceRef) []model.EvidenceRef {
	seen := map[string]bool{}
	out := []model.EvidenceRef{}
	for _, value := range values {
		key := value.ID
		if key == "" {
			key = string(value.Kind) + "|" + value.Summary + "|" + value.FieldPath
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func limitStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return values[:limit]
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func objectiveFromBrief(brief *model.RequirementBrief) string {
	if brief == nil {
		return ""
	}
	return brief.Objective
}

func primaryOutcomeFromBrief(brief *model.RequirementBrief) string {
	if brief == nil {
		return ""
	}
	return brief.PrimaryOutcome
}

func scenarioFromBrief(brief *model.RequirementBrief) string {
	if brief == nil {
		return ""
	}
	return brief.Scenario
}

func compactCapabilities(values []model.FeatureCapability) []map[string]any {
	out := make([]map[string]any, 0, minInt(len(values), 12))
	for _, value := range values {
		if len(out) >= 12 {
			break
		}
		out = append(out, map[string]any{
			"id":          value.ID,
			"name":        value.Name,
			"kind":        value.Kind,
			"user_value":  value.UserValue,
			"key_actions": value.KeyActions,
			"risks":       value.Risks,
			"confidence":  value.Confidence,
		})
	}
	return out
}

func compactSurfaces(values []model.InteractionSurface) []map[string]any {
	out := make([]map[string]any, 0, minInt(len(values), 12))
	for _, value := range values {
		if len(out) >= 12 {
			break
		}
		out = append(out, map[string]any{
			"id":             value.ID,
			"url_present":    value.URL != "",
			"title":          value.Title,
			"page_role":      value.PageRole,
			"action_count":   len(value.Actions),
			"selector_count": len(value.StableSelectors),
			"states":         value.States,
			"wait_hints":     value.WaitHints,
			"risk_count":     len(value.RiskFindings),
			"confidence":     value.Confidence,
		})
	}
	return out
}
