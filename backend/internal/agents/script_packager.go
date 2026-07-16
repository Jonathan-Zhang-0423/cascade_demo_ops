package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/llm"
	"cascade-demoops/backend/internal/model"
)

type ScriptPackagerAgent struct {
	llm llm.Client
}

const (
	browserAgentOutlineGeneratorName    = "cascade_browser_agent_outline_packager"
	browserAgentOutlineGeneratorVersion = "0.1.0"
)

func NewScriptPackagerAgent() *ScriptPackagerAgent { return &ScriptPackagerAgent{} }

func NewScriptPackagerAgentWithLLM(client llm.Client) *ScriptPackagerAgent {
	return &ScriptPackagerAgent{llm: client}
}

func (a *ScriptPackagerAgent) PackageScript(
	ctx context.Context,
	project *model.ProjectContext,
	report *model.MultimodalUnderstandingReport,
	productMap *model.ProductMap,
	graph *model.DemoWorkflowGraph,
	intelligence ...*model.ProjectIntelligencePack,
) (*model.ScriptDocumentPackage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if project == nil {
		return nil, errors.New("project context is required")
	}
	if graph == nil {
		return nil, errors.New("workflow graph is required")
	}
	if len(graph.Nodes) == 0 {
		return nil, errors.New("workflow graph must contain at least one node")
	}
	now := time.Now().UTC()
	quality := scriptQualityFromGraph(graph)
	graphHash, err := model.DigestCanonicalJSON(graph)
	if err != nil {
		return nil, err
	}
	runSpec := recordingRunSpecFromGraph(project, graph)
	steps := scriptStepsFromGraph(graph)
	doc := &model.ExecutionScriptDocument{
		ID:               "script_" + graph.ID,
		ProjectID:        project.ID,
		WorkflowGraphID:  graph.ID,
		GraphVersion:     graph.Version,
		SchemaVersion:    model.ExecutionScriptDocumentSchemaVersion,
		Status:           model.ScriptDocumentStatusReviewReady,
		Title:            firstNonEmpty(graph.Name, "演示执行脚本文档"),
		Summary:          firstNonEmpty(graph.Summary, "基于多模态理解结果封装的可审批执行脚本。"),
		Language:         "zh-CN",
		WorkflowGraph:    graph,
		RecordingRunSpec: runSpec,
		Steps:            steps,
		SafetyPolicy:     scriptSafetyPolicy(project, graph),
		Reproducibility: model.ReproducibilitySpec{
			GraphHashSHA256:       graphHash,
			InputFingerprints:     reportInputFingerprints(report),
			BrowserRuntimePins:    map[string]string{"browser": firstNonEmpty(runSpec.Browser.Engine, "chromium"), "version_policy": runSpec.Browser.VersionPolicy},
			SourceSnapshotDigest:  reportSourceDigest(report),
			DeterministicSeed:     "script_seed_" + shortHash(project.ID+"|"+graph.ID),
			CreatedWithAppVersion: "0.1.0",
		},
		ApprovalChecklist: model.ScriptApprovalChecklist{
			HumanApprovalRequired:              true,
			SourceSummaryOnly:                  true,
			CredentialScopeReviewRequired:      hasCredentials(project),
			RedactionsReviewRequired:           true,
			IPAllowlistAcknowledgementRequired: true,
			BlockingReasons:                    scriptBlockingReasons(project, report, quality),
		},
		EvidenceRefs: reportEvidenceRefs(report),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	scriptHash, err := doc.ComputeScriptHash()
	if err != nil {
		return nil, err
	}
	doc.Reproducibility.ScriptHashSHA256 = scriptHash
	markdown := renderScriptMarkdown(doc, productMap)
	markdown, trace, err := a.enhanceMarkdownWithLLM(ctx, project, report, productMap, graph, doc, markdown)
	if err != nil {
		markdown += "\n\n## 模型生成记录\n\n"
		if trace != nil {
			markdown += "- 审批文档润色模型：" + trace.Label() + "\n"
		}
		markdown += "- 审批文档润色失败，已使用本地确定性审批文档继续打包。\n"
		err = nil
	} else if trace != nil && trace.FallbackReason == "" {
		markdown += "\n\n## 模型生成记录\n\n"
		markdown += "- 审批文档润色模型：" + trace.Label() + "\n"
	}
	doc.MarkdownArtifact = &model.ArtifactRef{
		ID:        "artifact_" + doc.ID + "_markdown",
		Kind:      "execution_script_markdown",
		URI:       "cascade://projects/" + project.ID + "/scripts/" + doc.ID + ".md",
		MimeType:  "text/markdown",
		Label:     doc.Title,
		SHA256:    hashString(markdown),
		CreatedAt: now,
		Sensitive: false,
	}
	bundle, err := buildExecutableScriptBundle(project, report, productMap, graph, doc, markdown, now, firstIntelligencePack(intelligence...))
	if err != nil {
		return nil, err
	}
	return &model.ScriptDocumentPackage{
		Document:         doc,
		Markdown:         markdown,
		MarkdownArtifact: doc.MarkdownArtifact,
		ExecutableBundle: bundle,
	}, nil
}

func buildExecutableScriptBundle(
	project *model.ProjectContext,
	report *model.MultimodalUnderstandingReport,
	productMap *model.ProductMap,
	graph *model.DemoWorkflowGraph,
	doc *model.ExecutionScriptDocument,
	markdown string,
	now time.Time,
	intelligence *model.ProjectIntelligencePack,
) (*model.ExecutableRecordingScriptBundle, error) {
	planHash, err := doc.ComputeScriptHash()
	if err != nil {
		return nil, err
	}
	stagePlan := buildStageApprovalPlan(project, report, graph, doc, intelligence, now)
	outline := buildBrowserAgentScriptOutline(project, graph, doc, stagePlan, intelligence, now)
	promptPolicy := buildBrowserAgentPromptPolicy(project, graph, doc, outline, now)
	dossier := buildProjectUnderstandingDossier(project, report, productMap, graph, intelligence, now)
	auditText := outlineAuditText(stagePlan, outline, promptPolicy)
	if findings := auditScriptIntentCoverage(project, doc, auditText); len(findings) > 0 {
		return nil, fmt.Errorf("script_intent_mismatch: %s", strings.Join(findings, "；"))
	}
	stagePlanHash, err := model.DigestCanonicalJSON(stagePlan)
	if err != nil {
		return nil, err
	}
	outlineHash, err := model.DigestCanonicalJSON(outline)
	if err != nil {
		return nil, err
	}
	promptPolicyHash, err := model.DigestCanonicalJSON(promptPolicy)
	if err != nil {
		return nil, err
	}
	dossierHash, err := model.DigestCanonicalJSON(dossier)
	if err != nil {
		return nil, err
	}
	markdownHash := hashString(markdown)
	bundle := &model.ExecutableRecordingScriptBundle{
		ID:              "bundle_" + doc.ID,
		ProjectID:       project.ID,
		WorkflowGraphID: graph.ID,
		SchemaVersion:   model.ExecutableRecordingScriptBundleSchemaVersion,
		Status:          model.ExecutableScriptBundleStatusReviewReady,
		ScriptManifest: model.ExecutableScriptManifest{
			ScriptID:            "recording_" + graph.ID,
			Version:             1,
			Language:            "browser-agent-outline",
			Runtime:             model.ExecutableScriptRuntimeBrowserAgentOutlineV1,
			EntryFunction:       "runCascadeRecording",
			Generator:           browserAgentOutlineGeneratorName,
			GeneratorVersion:    browserAgentOutlineGeneratorVersion,
			DependencyAllowlist: []string{},
			ContextAPIs:         []string{"browser_agent.page", "browser_agent.secrets", "browser_agent.capture", "browser_agent.assert", "browser_agent.log"},
			StepNodeIDs:         scriptStepNodeIDs(doc),
		},
		PlanJSON: doc,
		PlaywrightScript: model.ExecutableScriptSource{
			MimeType:  "application/x.browser-agent-outline+json",
			SHA256:    "",
			SizeBytes: 0,
			Encrypted: false,
		},
		StageApprovalPlan: stagePlan,
		ScriptOutline:     outline,
		AgentPromptPolicy: promptPolicy,
		UnderstandingDossierRef: &model.ArtifactRef{
			ID:        "artifact_" + doc.ID + "_understanding_dossier",
			Kind:      "project_understanding_dossier",
			URI:       "cascade://projects/" + project.ID + "/scripts/" + doc.ID + ".dossier.json",
			MimeType:  "application/json",
			Label:     doc.Title + " 项目理解包",
			SHA256:    dossierHash,
			CreatedAt: now,
			Sensitive: false,
		},
		UnderstandingDossier: dossier,
		ApprovalMarkdown: model.ApprovalMarkdownDocument{
			InlineMarkdown: markdown,
			Artifact:       doc.MarkdownArtifact,
			MimeType:       "text/markdown",
			SHA256:         markdownHash,
			SizeBytes:      int64(len([]byte(markdown))),
		},
		SecurityPolicy: model.ExecutableScriptSecurityPolicy{
			AllowedDomains:       append([]string{}, doc.SafetyPolicy.AllowedDomains...),
			ForbiddenPages:       append([]string{}, doc.SafetyPolicy.ForbiddenPages...),
			ForbiddenData:        append([]string{}, doc.SafetyPolicy.ForbiddenData...),
			Redactions:           doc.SafetyPolicy.Redactions,
			SecretRefs:           scriptSecretRefs(doc),
			AllowedContextAPIs:   []string{"ctx.page", "ctx.secrets", "ctx.capture", "ctx.assert", "ctx.log"},
			AllowedPageMethods:   []string{"goto", "click", "fill", "selectOption", "setInputFiles", "waitForTimeout", "waitForLoadState", "locator"},
			ForbiddenImports:     []string{"fs", "node:fs", "child_process", "node:child_process", "http", "https", "net", "tls"},
			ForbiddenIdentifiers: []string{"import", "require", "eval", "Function", "process", "global", "globalThis", "window", "document", "fetch", "XMLHttpRequest", "WebSocket"},
			NetworkPolicy:        "allowed_domains_only_via_ctx_page",
			FileSystemPolicy:     "no_direct_fs_access",
		},
		Reproducibility: model.ExecutableScriptReproducibility{
			PlanHashSHA256:                 planHash,
			StagePlanHashSHA256:            stagePlanHash,
			OutlineHashSHA256:              outlineHash,
			PromptPolicyHashSHA256:         promptPolicyHash,
			UnderstandingDossierHashSHA256: dossierHash,
			MarkdownHashSHA256:             markdownHash,
			GraphHashSHA256:                doc.Reproducibility.GraphHashSHA256,
			SourceSnapshotDigest:           reportSourceDigest(report),
			GeneratorVersion:               browserAgentOutlineGeneratorVersion,
			DeterministicSeed:              doc.Reproducibility.DeterministicSeed,
			InputFingerprints:              reportInputFingerprints(report),
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	bundleHash, err := bundle.ComputeBundleHash()
	if err != nil {
		return nil, err
	}
	bundle.Reproducibility.BundleHashSHA256 = bundleHash
	validation := NewScriptBundleValidator().ValidateBundle(bundle)
	bundle.Validation = &validation
	if !validation.Valid {
		bundle.Status = model.ExecutableScriptBundleStatusRejected
		return nil, ensureValidBundle(bundle)
	}
	return bundle, nil
}

func firstIntelligencePack(values ...*model.ProjectIntelligencePack) *model.ProjectIntelligencePack {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func buildStageApprovalPlan(project *model.ProjectContext, report *model.MultimodalUnderstandingReport, graph *model.DemoWorkflowGraph, doc *model.ExecutionScriptDocument, intelligence *model.ProjectIntelligencePack, now time.Time) *model.StageApprovalPlan {
	stages := make([]model.StageApprovalStage, 0, len(doc.Steps))
	for _, step := range doc.Steps {
		node := scriptPackagerGraphNodeByID(graph, step.NodeID)
		evidence := evidenceForStage(report, intelligence, step, node)
		interaction := browserAgentInteractionFromStep(step, evidence)
		durationMS := step.Timing.DurationMS
		if durationMS <= 0 {
			durationMS = 10000
		}
		durationMS = maxInt(durationMS, 10000)
		stages = append(stages, model.StageApprovalStage{
			ID:             "stage_" + step.ID,
			Order:          step.Order,
			NodeID:         step.NodeID,
			Title:          firstNonEmpty(step.Title, step.NodeID),
			Objective:      firstNonEmpty(step.BusinessValue, step.ExpectedOutcome, step.Narrative.Voiceover, step.Title),
			BusinessIntent: firstNonEmpty(step.BusinessValue, step.Narrative.Voiceover, step.ExpectedOutcome),
			DurationMS:     durationMS,
			TargetRoute:    routeForScriptStep(step, node),
			TargetURL:      firstNonEmpty(step.PageTarget.URL, step.Action.Target.URL),
			ComponentRefs:  uniqueStrings(componentRefsForStage(step, node, intelligence)),
			APIRefs:        uniqueStrings(apiRefsForStage(node, intelligence)),
			StyleRefs:      uniqueStrings(styleRefsForStage(node, intelligence)),
			DataModelRefs:  uniqueStrings(dataModelRefsForStage(node, intelligence)),
			InputContent:   inputContentForStage(step, evidence),
			Interaction:    interaction,
			SuccessState:   firstNonEmpty(step.ExpectedOutcome, validationSummary(step.Validations)),
			WaitConditions: waitConditionsForStep(step),
			CapturePoints:  capturePointsForStep(step),
			RiskNotes:      stageRiskNotes(step, node),
			EvidenceRefs:   evidence,
			Confidence:     confidenceForStage(node, intelligence),
		})
	}
	return &model.StageApprovalPlan{
		ID:                "stage_plan_" + doc.ID,
		ProjectID:         project.ID,
		WorkflowGraphID:   graph.ID,
		SchemaVersion:     model.StageApprovalPlanSchemaVersion,
		Title:             firstNonEmpty(doc.Title, graph.Name, "演示 Stage 审批计划"),
		Summary:           firstNonEmpty(doc.Summary, graph.Summary),
		Runtime:           model.ExecutableScriptRuntimeBrowserAgentOutlineV1,
		Language:          "zh-CN",
		Stages:            stages,
		SafetyPolicy:      doc.SafetyPolicy,
		UncertaintyReport: uncertaintyReportForBundle(project, intelligence, graph),
		EvidenceRefs:      evidenceForDocument(report, intelligence, doc),
		Confidence:        confidenceForDossier(report, intelligence),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func buildBrowserAgentScriptOutline(project *model.ProjectContext, graph *model.DemoWorkflowGraph, doc *model.ExecutionScriptDocument, stagePlan *model.StageApprovalPlan, intelligence *model.ProjectIntelligencePack, now time.Time) *model.BrowserAgentScriptOutline {
	stages := make([]model.BrowserAgentOutlineStage, 0, len(stagePlan.Stages))
	for _, stage := range stagePlan.Stages {
		step := scriptStepByNodeID(doc, stage.NodeID)
		components := []model.BrowserAgentComponentTarget{}
		if step != nil {
			components = append(components, componentTargetsForStep(*step, stage, intelligence)...)
		}
		interactions := []model.BrowserAgentInteraction{stage.Interaction}
		stages = append(stages, model.BrowserAgentOutlineStage{
			ID:             "outline_" + stage.ID,
			StageID:        stage.ID,
			Order:          stage.Order,
			NodeID:         stage.NodeID,
			Objective:      stage.Objective,
			Route:          stage.TargetRoute,
			URL:            stage.TargetURL,
			Components:     components,
			Interactions:   interactions,
			WaitConditions: stage.WaitConditions,
			CapturePoints:  stage.CapturePoints,
			SuccessState:   stage.SuccessState,
			DurationMS:     stage.DurationMS,
			CanModify:      []string{"selector", "selector_alternatives", "wait_conditions", "retry_strategy", "non_destructive_exploration_path", "capture_timing"},
			MustPreserve:   []string{"stage_id", "node_id", "objective", "business_intent", "input_semantics", "success_state", "duration_floor", "safety_policy"},
			EvidenceRefs:   stage.EvidenceRefs,
			Confidence:     stage.Confidence,
		})
	}
	allowedRoutes := []string{}
	for _, stage := range stagePlan.Stages {
		allowedRoutes = append(allowedRoutes, stage.TargetRoute, stage.TargetURL)
	}
	allowedOrigins := []string{}
	for _, domain := range doc.SafetyPolicy.AllowedDomains {
		if strings.Contains(domain, "://") {
			allowedOrigins = append(allowedOrigins, baseURL(domain))
		} else if domain != "" {
			allowedOrigins = append(allowedOrigins, "https://"+domain)
		}
	}
	if origin := baseURL(firstNonEmpty(project.ProductURL, graph.EntryPoint, doc.RecordingRunSpec.BaseURL)); origin != "" {
		allowedOrigins = append(allowedOrigins, origin)
	}
	return &model.BrowserAgentScriptOutline{
		ID:              "outline_" + doc.ID,
		ProjectID:       project.ID,
		WorkflowGraphID: graph.ID,
		SchemaVersion:   model.BrowserAgentScriptOutlineSchemaVersion,
		Runtime:         model.ExecutableScriptRuntimeBrowserAgentOutlineV1,
		BaseURL:         firstNonEmpty(doc.RecordingRunSpec.BaseURL, baseURL(project.ProductURL)),
		ProductOrigin:   baseURL(firstNonEmpty(project.ProductURL, graph.EntryPoint, doc.RecordingRunSpec.BaseURL)),
		Summary:         "服务器 browser agent 按此大纲在产品域内自适应探索、修正 selector 与等待策略，并保持用户意图与安全边界不变。",
		Stages:          stages,
		AllowedExplorationScope: model.BrowserAgentExplorationScope{
			AllowedOrigins:        uniqueStrings(allowedOrigins),
			AllowedRoutes:         uniqueStrings(allowedRoutes),
			ForbiddenPathPrefixes: uniqueStrings(append(append([]string{}, doc.SafetyPolicy.ForbiddenPages...), defaultControlPlaneForbiddenPaths()...)),
			ForbiddenKeywords:     []string{"delete", "remove", "pay", "billing", "api key", "token", "删除", "支付", "账单", "密钥"},
			MaxDepth:              3,
			AllowNonDestructive:   true,
		},
		ForbiddenActions:     []string{"delete", "remove", "payment", "billing_change", "api_key_read", "raw_secret_exfiltration", "full_source_upload"},
		ServerEditableFields: []string{"script_outline.stages[].components[].selector", "script_outline.stages[].components[].selector_alternatives", "script_outline.stages[].wait_conditions", "script_outline.stages[].interactions[].target", "script_outline.stages[].interactions[].wait_conditions", "script_outline.stages[].capture_points"},
		ImmutableFields:      []string{"stage_approval_plan.stages[].objective", "stage_approval_plan.stages[].business_intent", "stage_approval_plan.stages[].input_content", "stage_approval_plan.safety_policy", "security_policy", "recording_run_spec.allowed_domains", "recording_run_spec.forbidden_pages"},
		UncertaintyReport:    uncertaintyReportForBundle(project, intelligence, graph),
		EvidenceRefs:         evidenceForDocument(nil, intelligence, doc),
		Confidence:           confidenceForDossier(nil, intelligence),
		CreatedAt:            now,
		UpdatedAt:            now,
	}
}

func buildBrowserAgentPromptPolicy(project *model.ProjectContext, graph *model.DemoWorkflowGraph, doc *model.ExecutionScriptDocument, outline *model.BrowserAgentScriptOutline, now time.Time) *model.BrowserAgentPromptPolicy {
	_ = graph
	_ = outline
	systemPrompt := strings.Join([]string{
		"你是 Cascade 云端 Browser Agent，负责把 App 端审批过的 StageApprovalPlan 和 BrowserAgentScriptOutline 转成可执行网页操作。",
		"你可以在 allowed origins 内做非破坏性探索，修正 selector、等待条件、轻量导航路径和截图时机。",
		"你不能修改用户需求意图、stage 顺序、stage 目标、填充内容语义、凭据 secret_ref、安全策略、禁止页面、打码规则和 allowed domains。",
		"如果代码/页面证据不足以确认某个业务动作，必须返回 failure diagnostic 和 repair_request，不得编造 route、组件、API 或成功状态。",
		"所有凭据只能通过 secret_ref 使用，不得写入日志、trace、截图元数据、错误响应或输出 artifact。",
	}, "\n")
	return &model.BrowserAgentPromptPolicy{
		ID:              "prompt_policy_" + doc.ID,
		ProjectID:       project.ID,
		WorkflowGraphID: doc.WorkflowGraphID,
		SchemaVersion:   model.BrowserAgentPromptPolicySchemaVersion,
		Runtime:         model.ExecutableScriptRuntimeBrowserAgentOutlineV1,
		SystemPrompt:    systemPrompt,
		ImmutableFields: []string{
			"user_intent", "stage_order", "stage_objective", "business_intent", "input_semantics",
			"success_state", "credential_secret_ref", "allowed_domains", "forbidden_pages",
			"forbidden_data", "redaction_policy", "human_approval_record",
		},
		EditableFields: []string{
			"selector", "selector_alternatives", "role_name_locator", "wait_conditions",
			"non_destructive_exploration_path", "retry_strategy", "capture_timing",
			"diagnostic_detail", "repair_hints",
		},
		ForbiddenChanges: []string{
			"不得新增用户没有审批的业务 stage。",
			"不得把登录、侧边栏、头像、主题切换等 chrome 控件当成核心业务 stage，除非用户需求明确要求。",
			"不得访问 /aigc、/.well-known、/v1/execution-packages、/v1/app-installations、debug、result 等控制面路径。",
			"不得读取或回传 raw secret、cookie、Authorization、本地源码或完整 HTML。",
		},
		RepairPolicy: []string{
			"selector、role/name、等待条件可以由服务器自适应修复。",
			"填充内容语义、stage 目标、安全边界不可修复为其他含义。",
			"连续失败后返回结构化 failure_diagnostic，而不是自动扩大权限。",
		},
		EvidencePolicy: []string{
			"每个动作必须追溯到 StageApprovalPlan、ProjectUnderstandingDossier 或页面运行时证据。",
			"不确定时先继续在产品域内小步探索；仍不确定则停止并回传缺失证据。",
		},
		SafetyBoundaries:    append(append([]string{}, doc.SafetyPolicy.ForbiddenPages...), doc.SafetyPolicy.ForbiddenData...),
		HumanReviewRequired: true,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
}

func buildProjectUnderstandingDossier(project *model.ProjectContext, report *model.MultimodalUnderstandingReport, productMap *model.ProductMap, graph *model.DemoWorkflowGraph, intelligence *model.ProjectIntelligencePack, now time.Time) *model.ProjectUnderstandingDossier {
	dossier := &model.ProjectUnderstandingDossier{
		ID:                   "dossier_" + graph.ID,
		ProjectID:            project.ID,
		WorkflowGraphID:      graph.ID,
		SchemaVersion:        model.ProjectUnderstandingDossierSchemaVersion,
		Summary:              firstNonEmpty(project.ProductDescription, graph.Summary),
		RequirementObjective: firstNonEmpty(project.ProductDescription, graph.Summary),
		SourceDigestSHA256:   reportSourceDigest(report),
		InputFingerprints:    reportInputFingerprints(report),
		EvidenceRefs:         evidenceForDocument(report, intelligence, nil),
		Confidence:           confidenceForDossier(report, intelligence),
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if intelligence != nil && intelligence.Architecture != nil {
		dossier.ArchitectureSummary = intelligence.Architecture.Summary
		for _, route := range intelligence.Architecture.RouteTree {
			dossier.RouteEvidence = append(dossier.RouteEvidence, model.DossierEvidence{
				ID:           route.ID,
				Kind:         "route",
				Label:        firstNonEmpty(route.Name, route.Path),
				Route:        route.Path,
				Summary:      "需求相关路由候选：" + route.Path,
				EvidenceRefs: route.EvidenceRefs,
				Confidence:   route.Confidence,
			})
		}
		for _, module := range intelligence.Architecture.Modules {
			for _, componentRef := range module.ComponentRefs {
				dossier.ComponentEvidence = append(dossier.ComponentEvidence, model.DossierEvidence{
					ID:           "component_" + shortHash(module.ID+componentRef),
					Kind:         "component_ref",
					Label:        componentRef,
					ComponentRef: componentRef,
					Summary:      module.Responsibility,
					EvidenceRefs: module.EvidenceRefs,
					Confidence:   module.Confidence,
				})
			}
			for _, apiRef := range module.APIRefs {
				dossier.APIEvidence = append(dossier.APIEvidence, model.DossierEvidence{
					ID:           "api_" + shortHash(module.ID+apiRef),
					Kind:         "api_ref",
					Label:        apiRef,
					Summary:      module.Responsibility,
					EvidenceRefs: module.EvidenceRefs,
					Confidence:   module.Confidence,
				})
			}
			for _, modelRef := range module.DataModelRefs {
				dossier.DataModelEvidence = append(dossier.DataModelEvidence, model.DossierEvidence{
					ID:           "model_" + shortHash(module.ID+modelRef),
					Kind:         "data_model_ref",
					Label:        modelRef,
					Summary:      module.Responsibility,
					EvidenceRefs: module.EvidenceRefs,
					Confidence:   module.Confidence,
				})
			}
		}
	}
	if productMap != nil {
		if dossier.ArchitectureSummary == "" {
			dossier.ArchitectureSummary = productMap.Summary
		}
		for _, route := range productMap.Routes {
			if route == nil {
				continue
			}
			dossier.RouteEvidence = append(dossier.RouteEvidence, model.DossierEvidence{ID: route.ID, Kind: "route", Label: firstNonEmpty(route.Name, route.Path), Route: route.Path, Summary: "产品地图路由：" + route.Path, EvidenceRefs: route.EvidenceRefs, Confidence: 0.7})
		}
		for _, component := range productMap.Components {
			if component == nil {
				continue
			}
			dossier.ComponentEvidence = append(dossier.ComponentEvidence, model.DossierEvidence{ID: component.ID, Kind: component.Kind, Label: component.Name, ComponentRef: component.ID, FilePathHashSHA256: hashString(component.FilePath), Summary: strings.Join(component.Selectors, ", "), EvidenceRefs: component.EvidenceRefs, Confidence: 0.7})
			for _, action := range component.Actions {
				dossier.InteractionEvidence = append(dossier.InteractionEvidence, model.DossierEvidence{ID: firstNonEmpty(action.ID, "interaction_"+shortHash(component.ID+action.Label+action.Selector)), Kind: action.Kind, Label: action.Label, ComponentRef: component.ID, Summary: action.Selector, EvidenceRefs: action.EvidenceRefs, Confidence: 0.7})
			}
		}
		for _, dataModel := range productMap.DataModels {
			if dataModel == nil {
				continue
			}
			dossier.DataModelEvidence = append(dossier.DataModelEvidence, model.DossierEvidence{ID: dataModel.ID, Kind: dataModel.Kind, Label: dataModel.Name, SourcePathHashSHA256: hashString(dataModel.SourcePath), Summary: dataFieldSummary(dataModel.Fields), EvidenceRefs: dataModel.EvidenceRefs, Confidence: 0.65})
		}
	}
	if report != nil {
		for _, snapshot := range report.CodeSnapshots {
			for _, route := range snapshot.Routes {
				dossier.RouteEvidence = append(dossier.RouteEvidence, model.DossierEvidence{ID: route.ID, Kind: "code_route", Label: firstNonEmpty(route.Name, route.Path), Route: route.Path, SourcePathHashSHA256: route.SourcePathHash, EvidenceRefs: route.EvidenceRefs, Confidence: route.Confidence})
			}
			for _, component := range snapshot.Components {
				dossier.ComponentEvidence = append(dossier.ComponentEvidence, model.DossierEvidence{ID: component.ID, Kind: component.Kind, Label: component.Name, FilePathHashSHA256: component.FilePathHashSHA256, Summary: strings.Join(append(component.SelectorHints, component.ActionLabels...), ", "), EvidenceRefs: component.EvidenceRefs, Confidence: component.Confidence})
			}
			for _, endpoint := range snapshot.APIEndpoints {
				dossier.APIEvidence = append(dossier.APIEvidence, model.DossierEvidence{ID: endpoint.ID, Kind: endpoint.Method, Label: endpoint.Path, FilePathHashSHA256: endpoint.FilePathHashSHA256, Summary: endpoint.Path, EvidenceRefs: endpoint.EvidenceRefs, Confidence: endpoint.Confidence})
			}
			for _, dataModel := range snapshot.DataModels {
				dossier.DataModelEvidence = append(dossier.DataModelEvidence, model.DossierEvidence{ID: dataModel.ID, Kind: dataModel.Kind, Label: dataModel.Name, SourcePathHashSHA256: dataModel.SourcePathHashSHA256, Summary: dataFieldSummary(dataModel.Fields), EvidenceRefs: dataModel.EvidenceRefs, Confidence: dataModel.Confidence})
			}
			for _, sensitive := range snapshot.SensitiveFields {
				dossier.SecurityEvidence = append(dossier.SecurityEvidence, model.DossierEvidence{ID: "sensitive_" + shortHash(sensitive.Name+sensitive.Kind), Kind: sensitive.Kind, Label: sensitive.Name, Summary: sensitive.Reason, EvidenceRefs: sensitive.EvidenceRefs, Confidence: 0.75})
			}
		}
	}
	dossier.UncertaintyReport = uncertaintyReportForBundle(project, intelligence, graph)
	return dossier
}

func outlineAuditText(values ...any) string {
	data, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	return string(data)
}

func scriptPackagerGraphNodeByID(graph *model.DemoWorkflowGraph, nodeID string) *model.GraphNode {
	if graph == nil {
		return nil
	}
	for _, node := range graph.Nodes {
		if node != nil && node.ID == nodeID {
			return node
		}
	}
	return nil
}

func scriptStepByNodeID(doc *model.ExecutionScriptDocument, nodeID string) *model.ScriptStep {
	if doc == nil {
		return nil
	}
	for i := range doc.Steps {
		if doc.Steps[i].NodeID == nodeID {
			return &doc.Steps[i]
		}
	}
	return nil
}

func browserAgentInteractionFromStep(step model.ScriptStep, evidence []model.EvidenceRef) model.BrowserAgentInteraction {
	target := step.Action.Target
	if target.Selector == "" {
		target.Selector = step.PageTarget.Selector
	}
	if target.URL == "" {
		target.URL = step.PageTarget.URL
	}
	if len(target.SelectorAlternatives) == 0 {
		target.SelectorAlternatives = append(target.SelectorAlternatives, step.PageTarget.SelectorAlternatives...)
	}
	return model.BrowserAgentInteraction{
		Kind:           step.Action.Type,
		Target:         target,
		Value:          RedactSensitiveUserText(step.Action.Value),
		InputRef:       step.Action.InputRef,
		SecretRef:      step.Action.SecretRef,
		Parameters:     step.Action.Parameters,
		WaitUntil:      step.Action.WaitUntil,
		WaitConditions: waitConditionsForStep(step),
		NonDestructive: !isBusinessAction(step.Action.Type),
		SelectorPolicy: "server_may_repair_within_stage_evidence",
		EvidenceRefs:   evidence,
	}
}

func inputContentForStage(step model.ScriptStep, evidence []model.EvidenceRef) []model.StageInputContent {
	if step.Action.Value == "" && step.Action.InputRef == "" && step.Action.SecretRef == "" {
		return nil
	}
	value := RedactSensitiveUserText(step.Action.Value)
	if step.Action.SecretRef != "" {
		value = ""
	}
	return []model.StageInputContent{{
		Kind:         string(step.Action.Type),
		Label:        firstNonEmpty(step.Action.Target.Label, step.Action.Target.Text, step.Title),
		Value:        value,
		InputRef:     step.Action.InputRef,
		SecretRef:    step.Action.SecretRef,
		Editable:     false,
		EvidenceRefs: evidence,
	}}
}

func routeForScriptStep(step model.ScriptStep, node *model.GraphNode) string {
	for _, value := range []string{step.PageTarget.URL, step.Action.Target.URL} {
		if route := pathFromURL(value); route != "" {
			return route
		}
	}
	if node != nil {
		if route := pathFromURL(node.Selector); route != "" {
			return route
		}
		if node.PageRef != "" {
			return node.PageRef
		}
	}
	return ""
}

func componentRefsForStage(step model.ScriptStep, node *model.GraphNode, intelligence *model.ProjectIntelligencePack) []string {
	refs := []string{step.Action.Target.ComponentRef}
	if node != nil {
		refs = append(refs, node.FeatureRefs...)
		if node.ActionSpec != nil {
			refs = append(refs, node.ActionSpec.Target.ComponentRef)
		}
	}
	if intelligence != nil && intelligence.FeatureTrace != nil {
		for _, trace := range intelligence.FeatureTrace.Traces {
			refs = append(refs, trace.MatchedComponents...)
		}
	}
	return refs
}

func apiRefsForStage(node *model.GraphNode, intelligence *model.ProjectIntelligencePack) []string {
	refs := []string{}
	if node != nil {
		if value, ok := node.Metadata["api_ref"].(string); ok {
			refs = append(refs, value)
		}
	}
	if intelligence != nil && intelligence.FeatureTrace != nil {
		for _, trace := range intelligence.FeatureTrace.Traces {
			refs = append(refs, trace.MatchedAPIRefs...)
		}
	}
	return refs
}

func styleRefsForStage(node *model.GraphNode, intelligence *model.ProjectIntelligencePack) []string {
	refs := []string{}
	if node != nil {
		if value, ok := node.Metadata["style_ref"].(string); ok {
			refs = append(refs, value)
		}
	}
	if intelligence != nil && intelligence.Architecture != nil {
		for _, module := range intelligence.Architecture.Modules {
			if strings.Contains(strings.ToLower(module.Kind+" "+module.Responsibility+" "+module.Name), "style") {
				refs = append(refs, module.ID)
			}
		}
	}
	return refs
}

func dataModelRefsForStage(node *model.GraphNode, intelligence *model.ProjectIntelligencePack) []string {
	refs := []string{}
	if node != nil {
		if value, ok := node.Metadata["data_model_ref"].(string); ok {
			refs = append(refs, value)
		}
	}
	if intelligence != nil && intelligence.FeatureTrace != nil {
		for _, trace := range intelligence.FeatureTrace.Traces {
			refs = append(refs, trace.MatchedDataModels...)
		}
	}
	return refs
}

func componentTargetsForStep(step model.ScriptStep, stage model.StageApprovalStage, intelligence *model.ProjectIntelligencePack) []model.BrowserAgentComponentTarget {
	target := step.Action.Target
	if target.Selector == "" {
		target.Selector = step.PageTarget.Selector
	}
	components := []model.BrowserAgentComponentTarget{{
		ComponentRef:         firstNonEmpty(target.ComponentRef, firstString(stage.ComponentRefs, "")),
		RouteRef:             stage.TargetRoute,
		Role:                 target.Role,
		Name:                 firstNonEmpty(target.Label, target.Text),
		Text:                 target.Text,
		Label:                target.Label,
		TestID:               target.TestID,
		Selector:             target.Selector,
		SelectorAlternatives: append(append([]model.SelectorCandidate{}, target.SelectorAlternatives...), step.PageTarget.SelectorAlternatives...),
		EvidenceRefs:         mergeStageEvidenceRefs(stage.EvidenceRefs, target.EvidenceRefs),
		Confidence:           stage.Confidence,
	}}
	if intelligence != nil && intelligence.VerifiedInteraction != nil {
		for _, action := range intelligence.VerifiedInteraction.Actions {
			if action.IntentGoalID == "" && action.ID == "" {
				continue
			}
			if action.ComponentRef == "" && action.Selector == "" && len(action.Alternatives) == 0 {
				continue
			}
			components = append(components, model.BrowserAgentComponentTarget{
				ComponentRef:         action.ComponentRef,
				RouteRef:             action.RouteRef,
				Label:                action.Label,
				Selector:             action.Selector,
				SelectorAlternatives: action.Alternatives,
				EvidenceRefs:         action.EvidenceRefs,
				Confidence:           stage.Confidence,
			})
		}
	}
	return components
}

func waitConditionsForStep(step model.ScriptStep) []string {
	conditions := []string{}
	if step.Action.WaitUntil != "" {
		conditions = append(conditions, "load_state:"+step.Action.WaitUntil)
	}
	for _, precondition := range step.Action.Preconditions {
		conditions = append(conditions, firstNonEmpty(precondition.Kind, precondition.Operator))
	}
	for _, validation := range step.Validations {
		conditions = append(conditions, firstNonEmpty(validation.Assertion, validation.Kind))
	}
	if step.ExpectedOutcome != "" {
		conditions = append(conditions, "success:"+step.ExpectedOutcome)
	}
	conditions = append(conditions, "wait_after_entry_at_least_1000ms", "wait_for_render_stable_before_capture")
	return uniqueStrings(conditions)
}

func capturePointsForStep(step model.ScriptStep) []string {
	points := []string{}
	if step.Capture.Video {
		points = append(points, "record_stage_video")
	}
	if step.Capture.Screenshot {
		points = append(points, "capture_stage_screenshot_after_render_stable")
	}
	if step.Capture.FocusSelector != "" {
		points = append(points, "focus:"+step.Capture.FocusSelector)
	}
	if step.Capture.Zoom {
		points = append(points, "zoom_primary_interaction")
	}
	if step.Capture.Callout {
		points = append(points, "mark_callout")
	}
	return uniqueStrings(points)
}

func stageRiskNotes(step model.ScriptStep, node *model.GraphNode) []string {
	notes := []string{}
	if step.Action.SecretRef != "" {
		notes = append(notes, "凭据只允许通过 secret_ref 注入。")
	}
	if selectorLooksGeneric(step.Action.Target.Selector) || selectorLooksGeneric(step.PageTarget.Selector) {
		notes = append(notes, "当前 selector 偏泛化，服务器只能在同 stage 证据范围内修复。")
	}
	if node != nil && node.Sensitive {
		notes = append(notes, "该节点标记为 sensitive，截图/trace 必须打码。")
	}
	return notes
}

func evidenceForStage(report *model.MultimodalUnderstandingReport, intelligence *model.ProjectIntelligencePack, step model.ScriptStep, node *model.GraphNode) []model.EvidenceRef {
	refs := []model.EvidenceRef{}
	refs = append(refs, step.EvidenceRefs...)
	if node != nil {
		refs = append(refs, node.EvidenceRefs...)
	}
	if len(refs) == 0 {
		refs = append(refs, evidenceForDocument(report, intelligence, nil)...)
	}
	if len(refs) == 0 {
		refs = append(refs, model.EvidenceRef{ID: "ev_stage_" + shortHash(step.NodeID+step.Title), Kind: model.EvidenceKindRequirementDoc, Summary: "Stage 来自用户需求和本地结构化计划。", Confidence: 0.55})
	}
	return uniqueEvidenceRefs(refs)
}

func evidenceForDocument(report *model.MultimodalUnderstandingReport, intelligence *model.ProjectIntelligencePack, doc *model.ExecutionScriptDocument) []model.EvidenceRef {
	refs := []model.EvidenceRef{}
	if report != nil {
		refs = append(refs, report.EvidenceRefs...)
		if report.RequirementBrief != nil {
			refs = append(refs, report.RequirementBrief.EvidenceRefs...)
		}
		for _, snapshot := range report.CodeSnapshots {
			refs = append(refs, snapshot.EvidenceRefs...)
		}
		for _, snapshot := range report.PageSnapshots {
			refs = append(refs, snapshot.EvidenceRefs...)
		}
	}
	if intelligence != nil {
		refs = append(refs, intelligence.EvidenceRefs...)
	}
	if doc != nil {
		refs = append(refs, doc.EvidenceRefs...)
	}
	return uniqueEvidenceRefs(refs)
}

func mergeStageEvidenceRefs(groups ...[]model.EvidenceRef) []model.EvidenceRef {
	refs := []model.EvidenceRef{}
	for _, group := range groups {
		refs = append(refs, group...)
	}
	return uniqueEvidenceRefs(refs)
}

func uncertaintyReportForBundle(project *model.ProjectContext, intelligence *model.ProjectIntelligencePack, graph *model.DemoWorkflowGraph) []model.StageUncertainty {
	items := []model.StageUncertainty{}
	if intelligence != nil && intelligence.MissingEvidenceReport != nil {
		for _, item := range intelligence.MissingEvidenceReport.Items {
			items = append(items, model.StageUncertainty{
				ID:              item.ID,
				Kind:            item.MissingKind,
				Summary:         item.Message,
				Blocking:        item.Severity == "blocking" || intelligence.MissingEvidenceReport.Blocking,
				SuggestedAction: item.SuggestedAction,
				EvidenceRefs:    item.EvidenceRefs,
			})
		}
	}
	if graph == nil || len(graph.Nodes) == 0 {
		items = append(items, model.StageUncertainty{ID: "uncertain_graph_empty_" + shortHash(project.ID), Kind: "workflow_graph", Summary: "执行图为空，无法生成 stage 大纲。", Blocking: true})
	}
	return items
}

func confidenceForStage(node *model.GraphNode, intelligence *model.ProjectIntelligencePack) float64 {
	confidence := 0.72
	if node != nil && nodeVerifiedForBusinessAction(node) {
		confidence = 0.86
	}
	if intelligence != nil && intelligence.Confidence > 0 {
		confidence = (confidence + intelligence.Confidence) / 2
	}
	return confidence
}

func confidenceForDossier(report *model.MultimodalUnderstandingReport, intelligence *model.ProjectIntelligencePack) float64 {
	values := []float64{}
	if report != nil && report.Confidence > 0 {
		values = append(values, report.Confidence)
	}
	if intelligence != nil && intelligence.Confidence > 0 {
		values = append(values, intelligence.Confidence)
	}
	if len(values) == 0 {
		return 0.72
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func validationSummary(validations []model.ValidationSpec) string {
	parts := []string{}
	for _, validation := range validations {
		parts = append(parts, firstNonEmpty(validation.Assertion, validation.Kind))
	}
	return strings.Join(uniqueStrings(parts), "；")
}

func dataFieldSummary(fields []model.DataField) string {
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, field.Name)
	}
	return strings.Join(uniqueStrings(names), ", ")
}

func auditScriptIntentCoverage(project *model.ProjectContext, doc *model.ExecutionScriptDocument, source string) []string {
	if project == nil || doc == nil {
		return nil
	}
	intentText := normalizeIntentText(strings.Join([]string{
		project.ProductDescription,
		project.TargetAudience,
		strings.Join(project.MustShow, " "),
		strings.Join(project.ForbiddenPages, " "),
	}, " "))
	if intentText == "" {
		return nil
	}
	planText := normalizeIntentText(scriptDocumentSemanticText(doc))
	sourceText := normalizeIntentText(source)
	combined := planText + " " + sourceText
	findings := []string{}
	requireText := func(trigger []string, required []string, message string) {
		if !containsAnyNormalized(intentText, trigger...) {
			return
		}
		if !containsAnyNormalized(combined, required...) {
			findings = append(findings, message)
		}
	}
	requireText(
		[]string{"登录", "登陆", "登入", "login", "sign in", "signin"},
		[]string{"cascadeensuredemologin", "demo_username", "demo_password", "登录", "login", "signin"},
		"需求要求演示登录，但执行脚本没有明确登录动作或本地凭据 secret_ref",
	)
	requireText(
		[]string{"新建项目", "创建项目", "新增项目", "new project", "create project"},
		[]string{"新建项目", "创建项目", "新增项目", "new project", "create project", "project name"},
		"需求要求新建项目，但执行脚本没有新建项目动作",
	)
	requireText(
		[]string{"俄罗斯方块", "tetris"},
		[]string{"俄罗斯方块", "tetris"},
		"需求要求项目内容为俄罗斯方块，但执行脚本没有输入或选择俄罗斯方块",
	)
	requireText(
		[]string{"构建模式", "build mode", "builder mode", "构建"},
		[]string{"构建模式", "build mode", "builder mode", "构建"},
		"需求要求选择构建模式，但执行脚本没有构建模式动作",
	)
	if containsAnyNormalized(intentText, "agent", "智能体", "实际构建", "开始构建", "run build", "build") &&
		!containsAnyNormalized(combined, "agent", "智能体", "实际构建", "开始构建", "生成", "构建", "run build", "start build") {
		findings = append(findings, "需求要求 agent 实际构建演示，但执行脚本没有启动或观察构建动作")
	}
	if requiredWait := requiredLongWaitMS(intentText); requiredWait > 0 && !scriptHasWaitAtLeast(doc, sourceText, requiredWait) {
		findings = append(findings, fmt.Sprintf("需求要求等待至少 %d 秒，但执行脚本没有对应的长等待 stage", requiredWait/1000))
	}
	if containsAnyNormalized(intentText, "新建项目", "创建项目", "新增项目", "俄罗斯方块", "构建模式") &&
		containsAnyNormalized(combined, "user-email-display", "user-name-display") {
		findings = append(findings, "执行脚本选择了账号展示字段作为业务动作，偏离新建项目/构建需求")
	}
	return uniqueStrings(findings)
}

func scriptDocumentSemanticText(doc *model.ExecutionScriptDocument) string {
	if doc == nil {
		return ""
	}
	parts := []string{doc.Title, doc.Summary}
	if doc.WorkflowGraph != nil {
		parts = append(parts, doc.WorkflowGraph.Name, doc.WorkflowGraph.Summary, doc.WorkflowGraph.EntryPoint)
		if doc.WorkflowGraph.Intent != nil {
			parts = append(parts, doc.WorkflowGraph.Intent.Objective, doc.WorkflowGraph.Intent.ValueProposition)
			parts = append(parts, doc.WorkflowGraph.Intent.SuccessCriteria...)
		}
		for _, requirement := range doc.WorkflowGraph.Requirements {
			parts = append(parts, requirement.Description)
		}
	}
	for _, step := range doc.Steps {
		parts = append(parts,
			step.ID,
			step.NodeID,
			step.Title,
			step.BusinessValue,
			step.ExpectedOutcome,
			step.PageTarget.URL,
			step.PageTarget.Selector,
			step.Action.Value,
			step.Action.InputRef,
			step.Action.SecretRef,
			step.Action.Target.Selector,
			step.Action.Target.Label,
			step.Action.Target.Text,
			step.Action.Target.TestID,
			step.Narrative.Title,
			step.Narrative.Caption,
			step.Narrative.Voiceover,
			step.Narrative.Callout,
		)
		for _, candidate := range step.Action.Target.SelectorAlternatives {
			parts = append(parts, candidate.Value)
		}
		for _, candidate := range step.PageTarget.SelectorAlternatives {
			parts = append(parts, candidate.Value)
		}
		for _, validation := range step.Validations {
			parts = append(parts, validation.Assertion, fmt.Sprint(validation.Expected), validation.Target.Selector, validation.Target.Label, validation.Target.Text)
		}
	}
	return strings.Join(parts, " ")
}

func normalizeIntentText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "（", "(")
	value = strings.ReplaceAll(value, "）", ")")
	value = strings.ReplaceAll(value, "　", " ")
	value = strings.Join(strings.Fields(value), " ")
	return value
}

func containsAnyNormalized(value string, needles ...string) bool {
	value = normalizeIntentText(value)
	for _, needle := range needles {
		if needle = normalizeIntentText(needle); needle != "" && strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func requiredLongWaitMS(intentText string) int {
	intentText = normalizeIntentText(intentText)
	for _, token := range []string{"60s", "60 s", "60秒", "60 秒", "一分钟", "1分钟", "1 分钟"} {
		if strings.Contains(intentText, token) {
			return 60000
		}
	}
	return 0
}

func scriptHasWaitAtLeast(doc *model.ExecutionScriptDocument, sourceText string, waitMS int) bool {
	if doc != nil {
		for _, step := range doc.Steps {
			if step.Timing.DurationMS >= waitMS ||
				step.Timing.HoldAfterMS >= waitMS ||
				(step.Action.Type == model.GraphActionWait && step.Action.TimeoutMS >= waitMS) {
				return true
			}
		}
	}
	for _, marker := range []string{
		fmt.Sprintf("waitfortimeout(%d", waitMS),
		fmt.Sprintf("durationms: %d", waitMS),
		fmt.Sprintf("durationms=%d", waitMS),
	} {
		if strings.Contains(sourceText, normalizeIntentText(marker)) {
			return true
		}
	}
	return false
}

type approvalMarkdownLLMOutput struct {
	Markdown string `json:"markdown"`
}

func (o *approvalMarkdownLLMOutput) UnmarshalJSON(data []byte) error {
	type alias approvalMarkdownLLMOutput
	var single alias
	if err := json.Unmarshal(data, &single); err == nil {
		*o = approvalMarkdownLLMOutput(single)
		return nil
	}
	var items []alias
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}
	for _, item := range items {
		if item.Markdown != "" {
			o.Markdown = item.Markdown
			return nil
		}
	}
	return nil
}

func (a *ScriptPackagerAgent) enhanceMarkdownWithLLM(
	ctx context.Context,
	project *model.ProjectContext,
	report *model.MultimodalUnderstandingReport,
	productMap *model.ProductMap,
	graph *model.DemoWorkflowGraph,
	doc *model.ExecutionScriptDocument,
	current string,
) (string, *llm.CallTrace, error) {
	if a.llm == nil || doc == nil {
		return current, nil, nil
	}
	payload := map[string]any{
		"target_audience": project.TargetAudience,
		"report_summary":  "",
		"product_map":     productMap,
		"graph":           graphPatchInput(graph),
		"script_steps":    doc.Steps,
		"safety_policy":   doc.SafetyPolicy,
		"hashes": map[string]string{
			"graph_hash": doc.Reproducibility.GraphHashSHA256,
			"plan_hash":  doc.Reproducibility.ScriptHashSHA256,
		},
		"current_markdown": current,
	}
	if report != nil {
		payload["report_summary"] = report.Summary
	}
	data, _ := json.Marshal(payload)
	var output approvalMarkdownLLMOutput
	trace, err := a.llm.GenerateJSON(ctx, config.ModelTaskPlanning, llm.JSONRequest{
		System:       "你是 Cascade DemoOps 的中文审批文档 agent。请润色执行包审批文档，让中国产品/运营用户能理解演示目标、生成依据、执行路径、安全边界和审批清单。不要新增与 JSON plan 不一致的步骤，不要输出源码、密钥或完整代码内容。",
		User:         string(data),
		SchemaName:   "ApprovalMarkdownPatch",
		ResponseHint: "返回字段：markdown。markdown 必须包含：演示目标、生成依据、执行路径、每步动作、录制策略、安全/打码策略、凭据使用范围、云端执行边界、审批清单。",
		MaxTokens:    2600,
		Temperature:  0.25,
	}, &output)
	if err != nil {
		return current, trace, err
	}
	if strings.TrimSpace(output.Markdown) == "" {
		return current, trace, nil
	}
	return output.Markdown, trace, nil
}

func scriptStepsFromGraph(graph *model.DemoWorkflowGraph) []model.ScriptStep {
	steps := make([]model.ScriptStep, 0, len(graph.Nodes))
	elapsedMS := 0
	for i, node := range graph.Nodes {
		if node == nil {
			continue
		}
		durationMS := node.DurationHintMS
		if durationMS <= 0 {
			durationMS = 3000
		}
		durationMS = maxInt(durationMS, 10000)
		capture := captureSpecForNode(node)
		narrative := narrativeForNode(node)
		action := scriptActionForNode(node)
		target := scriptPageTargetForNode(node)
		validations := append([]model.ValidationSpec{}, node.Validations...)
		selector := bestSelectorForScriptStep(action, target)
		if selector != "" {
			action.Target.Selector = selector
			target.Selector = selector
			if capture.FocusSelector == "" && !selectorLooksGeneric(selector) {
				capture.FocusSelector = selector
			}
		}
		if businessActionNeedsExecutableSelector(action.Type) && (!selectorUsableForBusinessAction(selector) || !nodeVerifiedForBusinessAction(node)) {
			action.Type = model.GraphActionInspect
			action.Target.Selector = ""
			target.Selector = ""
			capture.FocusSelector = ""
			validations = softenBlockingValidations(validations)
		}
		if len(validations) == 0 && node.ExpectedOutcome != "" {
			validations = append(validations, model.ValidationSpec{
				ID:        "validate_" + node.ID,
				Kind:      "expected_outcome",
				Target:    action.Target,
				Assertion: node.ExpectedOutcome,
				Expected:  true,
				Severity:  "blocking",
				Required:  true,
			})
		}
		steps = append(steps, model.ScriptStep{
			ID:              fmt.Sprintf("step_%02d_%s", i+1, node.ID),
			Order:           i + 1,
			NodeID:          node.ID,
			Title:           firstNonEmpty(node.Title, "执行步骤"),
			BusinessValue:   firstNonEmpty(node.Goal, node.Description),
			PageTarget:      target,
			Action:          action,
			ExpectedOutcome: node.ExpectedOutcome,
			Validations:     validations,
			Capture:         capture,
			Timing:          model.NodeTimingHint{NodeID: node.ID, DurationMS: durationMS, HoldAfterMS: stageHoldAfterMS(durationMS)},
			Narrative:       narrative,
			EvidenceRefs:    node.EvidenceRefs,
			Blocking:        isBlockingScriptStep(node, action.Type, validations),
		})
		elapsedMS += durationMS
		_ = elapsedMS
	}
	return steps
}

func nodeVerifiedForBusinessAction(node *model.GraphNode) bool {
	if node == nil {
		return false
	}
	if node.Metadata == nil {
		return false
	}
	status, _ := node.Metadata["verification_status"].(string)
	if status != "verified" {
		if status != "runtime_adaptive" && status != "sidecar_unavailable" {
			return false
		}
	}
	if _, ok := node.Metadata["verified_interaction_id"].(string); ok {
		return true
	}
	if _, ok := node.Metadata["verified_interaction_id"]; ok {
		return true
	}
	return false
}

func stageHoldAfterMS(durationMS int) int {
	if durationMS >= 10000 {
		return minInt(durationMS/2, 6000)
	}
	return minInt(maxInt(durationMS, 1500), 5000)
}

func scriptStepNodeIDs(doc *model.ExecutionScriptDocument) []string {
	if doc == nil {
		return []string{}
	}
	nodeIDs := make([]string, 0, len(doc.Steps))
	for _, step := range doc.Steps {
		nodeIDs = append(nodeIDs, step.NodeID)
	}
	return nodeIDs
}

func scriptSecretRefs(doc *model.ExecutionScriptDocument) []string {
	if doc == nil {
		return []string{}
	}
	refs := []string{}
	for _, step := range doc.Steps {
		if step.Action.SecretRef != "" {
			refs = append(refs, step.Action.SecretRef)
		}
	}
	if doc.WorkflowGraph != nil {
		for _, variable := range doc.WorkflowGraph.Variables {
			if variable.SecretRef != "" {
				refs = append(refs, variable.SecretRef)
			}
		}
		for _, record := range doc.WorkflowGraph.TestData {
			for _, ref := range record.SecretRefs {
				if ref != "" {
					refs = append(refs, ref)
				}
			}
		}
	}
	return uniqueStrings(refs)
}

func recordingRunSpecFromGraph(project *model.ProjectContext, graph *model.DemoWorkflowGraph) model.RecordingRunSpec {
	browser := model.BrowserRunSpec{Engine: "chromium", VersionPolicy: "stable-pinned", Headless: true}
	targetDuration := 60
	if graph.Execution != nil {
		browser.Engine = firstNonEmpty(graph.Execution.Browser, browser.Engine)
		browser.Headless = graph.Execution.Headless
		browser.Viewports = graph.Execution.Viewports
	}
	if len(browser.Viewports) == 0 {
		browser.Viewports = []model.ViewportSpec{{Name: "desktop", Width: 1440, Height: 900, Device: "desktop"}}
	}
	if graph.Assets != nil && graph.Assets.TargetDurationSec > 0 {
		targetDuration = graph.Assets.TargetDurationSec
	}
	outputs := model.RecordingOutputRequest{
		RawRecording:     true,
		FinalVideo:       graph.Assets == nil || graph.Assets.DemoVideo60s,
		ScreenshotPack:   graph.Assets == nil || graph.Assets.ScreenshotPack,
		StepByStepDocs:   graph.Assets == nil || graph.Assets.StepByStepDocs,
		Trace:            true,
		OutputFormats:    []string{"mp4", "markdown", "png", "json"},
		ResolutionWidth:  1920,
		ResolutionHeight: 1080,
	}
	captureWindows := []model.CaptureWindow{}
	timingHints := []model.NodeTimingHint{}
	startMS := 0
	for _, node := range graph.Nodes {
		if node == nil {
			continue
		}
		durationMS := node.DurationHintMS
		if durationMS <= 0 {
			durationMS = 3000
		}
		durationMS = maxInt(durationMS, 10000)
		timingHints = append(timingHints, model.NodeTimingHint{NodeID: node.ID, DurationMS: durationMS, HoldAfterMS: stageHoldAfterMS(durationMS)})
		if node.Capture != nil && (node.Capture.Video || node.Capture.Screenshot) {
			captureWindows = append(captureWindows, model.CaptureWindow{
				ID:         "capture_" + node.ID,
				NodeID:     node.ID,
				StartMS:    startMS,
				DurationMS: durationMS,
				Role:       firstNonEmpty(node.Capture.AssetRole, string(node.Type)),
			})
		}
		startMS += durationMS
	}
	return model.RecordingRunSpec{
		RunID:          "run_" + graph.ID,
		BaseURL:        firstNonEmpty(baseURL(graph.EntryPoint), project.ProductURL, graph.EntryPoint),
		AllowedDomains: allowedDomains(project, graph),
		AuthFlowRef:    authFlowRef(project),
		Timezone:       "Asia/Shanghai",
		Locale:         "zh-CN",
		Browser:        browser,
		Timeline: model.RecordingTimeline{
			TargetDurationSec: targetDuration,
			MaxDurationSec:    targetDuration + 30,
			CaptureWindows:    captureWindows,
			NodeTimingHints:   timingHints,
		},
		Outputs:       outputs,
		Redactions:    redactionPolicyForProject(project, graph),
		FailurePolicy: recordingFailurePolicy(graph),
		Environment:   map[string]string{"profile": string(project.Mode), "script_schema": model.ExecutionScriptDocumentSchemaVersion},
	}
}

func scriptActionForNode(node *model.GraphNode) model.ScriptActionInstruction {
	if node.ActionSpec != nil {
		return model.ScriptActionInstruction{
			Type:          node.ActionSpec.Type,
			Target:        node.ActionSpec.Target,
			Value:         node.ActionSpec.Value,
			InputRef:      node.ActionSpec.InputRef,
			SecretRef:     node.ActionSpec.SecretRef,
			Parameters:    node.ActionSpec.Parameters,
			TimeoutMS:     node.ActionSpec.TimeoutMS,
			WaitUntil:     node.ActionSpec.WaitUntil,
			Preconditions: node.ActionSpec.Preconditions,
		}
	}
	actionType := graphActionTypeFromKind(node.Action, node.Selector)
	return model.ScriptActionInstruction{
		Type:      actionType,
		Target:    model.ActionTarget{URL: urlIfHTTP(node.Selector), Selector: selectorIfNotURL(node.Selector)},
		Value:     node.InputData,
		TimeoutMS: 10000,
	}
}

func scriptPageTargetForNode(node *model.GraphNode) model.ScriptPageTarget {
	target := model.ScriptPageTarget{PageRef: node.PageRef}
	if node.ActionSpec != nil {
		target.URL = node.ActionSpec.Target.URL
		target.Selector = node.ActionSpec.Target.Selector
		target.SelectorAlternatives = node.ActionSpec.Target.SelectorAlternatives
	}
	if target.URL == "" {
		target.URL = urlIfHTTP(node.Selector)
	}
	if target.Selector == "" {
		target.Selector = selectorIfNotURL(node.Selector)
	}
	return target
}

func captureSpecForNode(node *model.GraphNode) model.CaptureSpec {
	if node.Capture != nil {
		return *node.Capture
	}
	return model.CaptureSpec{
		Screenshot: node.IsScreenshot,
		Video:      true,
		Zoom:       node.HasZoom,
		Callout:    node.HasZoom,
		AssetRole:  string(node.Type),
	}
}

func narrativeForNode(node *model.GraphNode) model.NarrativeCue {
	if node.Narrative != nil {
		return *node.Narrative
	}
	return model.NarrativeCue{
		Title:     firstNonEmpty(node.Title, "执行步骤"),
		Voiceover: firstNonEmpty(node.Goal, node.ExpectedOutcome),
		Caption:   node.ExpectedOutcome,
	}
}

func isBlockingStep(node *model.GraphNode) bool {
	if node.FailurePolicy != nil && node.FailurePolicy.HumanReviewRequired {
		return true
	}
	actionType := graphActionTypeFromKind(node.Action, node.Selector)
	if node.ActionSpec != nil {
		actionType = node.ActionSpec.Type
	}
	for _, validation := range node.Validations {
		if validation.Required && validation.Severity == "blocking" {
			return true
		}
	}
	if actionType == model.GraphActionInspect || actionType == model.GraphActionWait {
		return false
	}
	return node.Type == model.GraphNodeTypeStart || node.Type == model.GraphNodeTypeAction || node.Type == model.GraphNodeTypeCapture
}

func isBlockingScriptStep(node *model.GraphNode, actionType model.GraphActionType, validations []model.ValidationSpec) bool {
	if node != nil && node.FailurePolicy != nil && node.FailurePolicy.HumanReviewRequired {
		return true
	}
	for _, validation := range validations {
		if validation.Required && validation.Severity == "blocking" {
			return true
		}
	}
	if actionType == model.GraphActionInspect || actionType == model.GraphActionWait {
		return false
	}
	return actionType == model.GraphActionNavigate || isBusinessAction(actionType) || actionType == model.GraphActionAPICall
}

type scriptQualityReport struct {
	ExecutableActionCount int
	GenericSelectorCount  int
	LoginActionCount      int
	ShortStageCount       int
	BlockingAssertRisk    int
	ObservationOnly       bool
	Warnings              []string
	Blockers              []string
}

func scriptQualityFromGraph(graph *model.DemoWorkflowGraph) scriptQualityReport {
	report := scriptQualityReport{}
	if graph == nil {
		report.ObservationOnly = true
		report.Blockers = append(report.Blockers, "执行图缺失，不能进入自动录制。")
		return report
	}
	for _, node := range graph.Nodes {
		if node == nil {
			continue
		}
		actionType := graphActionTypeFromKind(node.Action, node.Selector)
		if node.ActionSpec != nil {
			actionType = node.ActionSpec.Type
		}
		selector := graphNodeSelector(node)
		if selectorLooksGeneric(selector) {
			report.GenericSelectorCount++
		}
		if looksLikeLoginAction(node.Title, node.Action, selector) {
			report.LoginActionCount++
		}
		if node.DurationHintMS > 0 && node.DurationHintMS < 10000 {
			report.ShortStageCount++
		}
		if actionType == model.GraphActionAssert && !selectorUsableForBlockingAssertion(selector) {
			report.BlockingAssertRisk++
		}
		if isBusinessAction(actionType) && selectorUsableForBusinessAction(selector) && nodeVerifiedForBusinessAction(node) {
			report.ExecutableActionCount++
		} else if isBusinessAction(actionType) {
			report.Warnings = append(report.Warnings, "业务动作缺少稳定且已验证的 selector，上传前应补充页面扫描、截图标注或 data-testid/role/name 证据。")
		}
	}
	report.ObservationOnly = report.ExecutableActionCount == 0
	if report.ObservationOnly {
		report.Blockers = append(report.Blockers, "当前执行图没有 click/fill/select/upload/api_call 等已验证真实业务动作，不能自动上传录制。请确认需求目标能在产品页面中找到可见、可用、可解释的控件证据。")
	}
	if report.GenericSelectorCount > 0 {
		report.Warnings = append(report.Warnings, "检测到 body/main/section/div 等泛 selector，业务动作不会使用这些 selector 作为 blocking 目标。")
	}
	if report.LoginActionCount > 1 {
		report.Warnings = append(report.Warnings, "检测到重复登录动作，脚本应只登录一次并复用已登录会话。")
	}
	if report.ShortStageCount > 0 {
		report.Warnings = append(report.Warnings, "检测到低于 10 秒的 stage，打包时会拉长为更自然的录制节奏。")
	}
	if report.BlockingAssertRisk > 0 {
		report.Warnings = append(report.Warnings, "检测到泛 selector 上的 blocking assert 风险，打包时会降级为非阻塞观察。")
	}
	return report
}

func graphNodeSelector(node *model.GraphNode) string {
	if node == nil {
		return ""
	}
	values := []string{node.Selector}
	if node.ActionSpec != nil {
		values = append(values, node.ActionSpec.Target.Selector, actionTargetTestIDSelector(node.ActionSpec.Target))
		values = append(values, bestSelectorCandidate(node.ActionSpec.Target.SelectorAlternatives))
	}
	if node.Capture != nil {
		values = append(values, node.Capture.FocusSelector)
	}
	if selector := bestSelectorValue(values...); selector != "" {
		return selector
	}
	return firstNonEmpty(values...)
}

func bestSelectorForScriptStep(action model.ScriptActionInstruction, target model.ScriptPageTarget) string {
	return bestSelectorValue(
		actionTargetTestIDSelector(action.Target),
		bestSelectorCandidate(action.Target.SelectorAlternatives),
		bestSelectorCandidate(target.SelectorAlternatives),
		action.Target.Selector,
		target.Selector,
	)
}

func softenBlockingValidations(validations []model.ValidationSpec) []model.ValidationSpec {
	out := make([]model.ValidationSpec, 0, len(validations))
	for _, validation := range validations {
		if validation.Severity == "blocking" || validation.Required {
			validation.Severity = "warning"
			validation.Required = false
		}
		out = append(out, validation)
	}
	return out
}

func isBusinessAction(action model.GraphActionType) bool {
	switch action {
	case model.GraphActionClick, model.GraphActionFill, model.GraphActionSelect, model.GraphActionUpload, model.GraphActionAPICall:
		return true
	default:
		return false
	}
}

func scriptSafetyPolicy(project *model.ProjectContext, graph *model.DemoWorkflowGraph) model.ScriptSafetyPolicy {
	piiHandling := "mask_in_artifacts"
	if project.SecurityPolicy != nil {
		piiHandling = firstNonEmpty(project.SecurityPolicy.PIIHandling, piiHandling)
	}
	return model.ScriptSafetyPolicy{
		AllowedDomains: allowedDomains(project, graph),
		ForbiddenPages: uniqueStrings(append(append([]string{}, project.ForbiddenPages...), defaultControlPlaneForbiddenPaths()...)),
		ForbiddenData:  append([]string{}, project.ForbiddenData...),
		Redactions:     redactionPolicyForProject(project, graph),
		PIIHandling:    piiHandling,
	}
}

func redactionPolicyForProject(project *model.ProjectContext, graph *model.DemoWorkflowGraph) model.RedactionPolicy {
	selectors := maskSelectorsFromProject(project)
	for _, node := range graph.Nodes {
		if node != nil && node.Capture != nil {
			selectors = append(selectors, node.Capture.MaskSelectors...)
		}
	}
	return model.RedactionPolicy{
		MaskSelectors:        uniqueStrings(selectors),
		TextPatterns:         []string{"[A-Z0-9._%+-]+@[A-Z0-9.-]+", "api[_-]?key", "token"},
		VideoMaskPolicy:      "mask_before_persist",
		ScreenshotMaskPolicy: "mask_before_persist",
	}
}

func recordingFailurePolicy(graph *model.DemoWorkflowGraph) model.RecordingFailurePolicy {
	policy := model.RecordingFailurePolicy{
		RetryAttempts:             2,
		SelectorRepairAllowed:     true,
		DataRepairAllowed:         false,
		MaxRepairAttempts:         1,
		HumanEscalationConditions: []string{"auth_failed", "forbidden_page_detected", "raw_secret_detected"},
	}
	if graph.Execution != nil && graph.Execution.FailurePolicy != nil {
		policy.RetryAttempts = graph.Execution.FailurePolicy.MaxAttempts
		policy.SelectorRepairAllowed = graph.Execution.FailurePolicy.AllowSelectorRepair
		policy.DataRepairAllowed = graph.Execution.FailurePolicy.AllowDataRepair
		policy.MaxRepairAttempts = graph.Execution.FailurePolicy.MaxAttempts
		policy.HumanEscalationConditions = append(policy.HumanEscalationConditions, graph.Execution.FailurePolicy.EscalateToHumanOn...)
	}
	return policy
}

func allowedDomains(project *model.ProjectContext, graph *model.DemoWorkflowGraph) []string {
	domains := []string{}
	if project.AccessPolicy != nil {
		domains = append(domains, project.AccessPolicy.AllowedDomains...)
	}
	for _, value := range []string{project.ProductURL, graph.EntryPoint} {
		if host := urlHost(value); host != "" {
			domains = append(domains, host)
		}
	}
	return uniqueStrings(domains)
}

func authFlowRef(project *model.ProjectContext) string {
	if project.Inputs == nil || len(project.Inputs.Credentials) == 0 {
		return ""
	}
	return project.Inputs.Credentials[0].ID
}

func baseURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func urlIfHTTP(value string) string {
	if isHTTPURL(value) {
		return value
	}
	return ""
}

func selectorIfNotURL(value string) string {
	if isHTTPURL(value) {
		return ""
	}
	return value
}

func reportInputFingerprints(report *model.MultimodalUnderstandingReport) map[string]string {
	if report == nil {
		return map[string]string{}
	}
	return report.InputFingerprints
}

func reportSourceDigest(report *model.MultimodalUnderstandingReport) string {
	if report == nil {
		return ""
	}
	return report.SourceDigestSHA256
}

func scriptBlockingReasons(project *model.ProjectContext, report *model.MultimodalUnderstandingReport, quality scriptQualityReport) []string {
	reasons := []string{"上传前必须完成人工审批。", "需要确认仅上传代码结构摘要，不上传完整源码。", "需要复核打码选择器和禁止访问数据。"}
	reasons = append(reasons, quality.Blockers...)
	reasons = append(reasons, quality.Warnings...)
	if hasCredentials(project) {
		reasons = append(reasons, "需要复核凭据授权范围和过期时间。")
	}
	if report != nil && report.SafetyReport != nil {
		for _, finding := range report.SafetyReport.PolicyFindings {
			if finding.Severity == model.FindingSeverityBlocking {
				reasons = append(reasons, finding.Summary)
			}
		}
	}
	return uniqueStrings(reasons)
}

func hasCredentials(project *model.ProjectContext) bool {
	return project != nil && project.Inputs != nil && len(project.Inputs.Credentials) > 0
}

func renderScriptMarkdown(doc *model.ExecutionScriptDocument, productMap *model.ProductMap) string {
	var builder strings.Builder
	builder.WriteString("# " + firstNonEmpty(doc.Title, "演示执行脚本文档") + "\n\n")
	builder.WriteString("## 摘要\n\n")
	builder.WriteString(doc.Summary + "\n\n")
	builder.WriteString("## 演示目标\n\n")
	if doc.WorkflowGraph != nil && doc.WorkflowGraph.Intent != nil {
		builder.WriteString("- 目标：" + firstNonEmpty(doc.WorkflowGraph.Intent.Objective, doc.Summary) + "\n")
		builder.WriteString("- 受众：" + firstNonEmpty(audienceName(doc.WorkflowGraph.Intent.Audience), "中国客户") + "\n")
		builder.WriteString("- 价值主张：" + firstNonEmpty(doc.WorkflowGraph.Intent.ValueProposition, "展示产品核心路径和可验证结果") + "\n\n")
	} else {
		builder.WriteString("- 目标：" + doc.Summary + "\n")
		builder.WriteString("- 受众：中国客户\n\n")
	}
	builder.WriteString("## 生成依据\n\n")
	if productMap != nil && productMap.Summary != "" {
		builder.WriteString("- 产品理解：" + productMap.Summary + "\n")
	}
	builder.WriteString("- 需求、代码结构摘要、页面/截图证据已融合为可审计计划。\n")
	builder.WriteString("- 本地代码只用于生成结构摘要和稳定选择器，不上传完整源码。\n")
	builder.WriteString("- Graph 摘要：" + doc.Reproducibility.GraphHashSHA256 + "\n")
	builder.WriteString("- Plan 摘要：" + doc.Reproducibility.ScriptHashSHA256 + "\n\n")

	builder.WriteString("## 安全策略\n\n")
	builder.WriteString("- 允许域名：" + strings.Join(doc.SafetyPolicy.AllowedDomains, ", ") + "\n")
	builder.WriteString("- 禁止页面：" + strings.Join(doc.SafetyPolicy.ForbiddenPages, ", ") + "\n")
	builder.WriteString("- 禁止数据：" + strings.Join(doc.SafetyPolicy.ForbiddenData, ", ") + "\n")
	builder.WriteString("- 打码选择器：" + strings.Join(doc.SafetyPolicy.Redactions.MaskSelectors, ", ") + "\n\n")

	builder.WriteString("## 录制策略\n\n")
	builder.WriteString("- 目标时长：" + fmt.Sprintf("%d 秒\n", doc.RecordingRunSpec.Timeline.TargetDurationSec))
	builder.WriteString("- 浏览器：" + firstNonEmpty(doc.RecordingRunSpec.Browser.Engine, "chromium") + " / " + firstNonEmpty(doc.RecordingRunSpec.Browser.VersionPolicy, "stable-pinned") + "\n")
	builder.WriteString("- 语言与时区：" + firstNonEmpty(doc.RecordingRunSpec.Locale, "zh-CN") + " / " + firstNonEmpty(doc.RecordingRunSpec.Timezone, "Asia/Shanghai") + "\n")
	builder.WriteString("- 输出资产：" + outputSummary(doc.RecordingRunSpec.Outputs) + "\n\n")

	builder.WriteString("## 凭据与云端边界\n\n")
	if len(scriptSecretRefs(doc)) > 0 {
		builder.WriteString("- 凭据只通过 secret_ref 读取，不在脚本或文档中展示明文。\n")
		builder.WriteString("- 凭据引用：" + strings.Join(scriptSecretRefs(doc), ", ") + "\n")
	} else {
		builder.WriteString("- 当前脚本未声明凭据引用。\n")
	}
	builder.WriteString("- 云端只允许在 allowed domains 内执行页面访问。\n")
	builder.WriteString("- 云端执行前仍需校验 TS 脚本、JSON plan、hash、禁止 API 和打码策略。\n\n")

	builder.WriteString("## 执行步骤\n\n")
	for _, step := range doc.Steps {
		builder.WriteString(fmt.Sprintf("### %d. %s\n\n", step.Order, firstNonEmpty(step.Title, step.NodeID)))
		builder.WriteString("- 节点：" + step.NodeID + "\n")
		builder.WriteString("- 动作：" + string(step.Action.Type) + "\n")
		builder.WriteString("- 目标：" + firstNonEmpty(step.PageTarget.URL, step.PageTarget.Selector, "页面上下文") + "\n")
		builder.WriteString("- 预期：" + step.ExpectedOutcome + "\n")
		builder.WriteString("- 录制：" + captureSummary(step.Capture) + "\n")
		builder.WriteString("- 旁白：" + firstNonEmpty(step.Narrative.Voiceover, step.Narrative.Caption, step.BusinessValue) + "\n\n")
	}

	builder.WriteString("## 审批清单\n\n")
	for _, reason := range doc.ApprovalChecklist.BlockingReasons {
		builder.WriteString("- [ ] " + reason + "\n")
	}
	return builder.String()
}

func audienceName(audience *model.AudienceProfile) string {
	if audience == nil {
		return ""
	}
	return audience.Name
}

func outputSummary(outputs model.RecordingOutputRequest) string {
	parts := []string{}
	if outputs.RawRecording {
		parts = append(parts, "原始录屏")
	}
	if outputs.FinalVideo {
		parts = append(parts, "最终视频")
	}
	if outputs.StepByStepDocs {
		parts = append(parts, "步骤文档")
	}
	if outputs.ScreenshotPack {
		parts = append(parts, "截图包")
	}
	if outputs.Trace {
		parts = append(parts, "执行轨迹")
	}
	if len(parts) == 0 {
		return "仅验证执行"
	}
	return strings.Join(parts, " / ")
}

func captureSummary(capture model.CaptureSpec) string {
	parts := []string{}
	if capture.Video {
		parts = append(parts, "录屏")
	}
	if capture.Screenshot {
		parts = append(parts, "截图")
	}
	if capture.Zoom {
		parts = append(parts, "特写")
	}
	if capture.Callout {
		parts = append(parts, "标注")
	}
	if len(parts) == 0 {
		return "仅执行验证"
	}
	return strings.Join(parts, " / ")
}
