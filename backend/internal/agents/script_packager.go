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
	intelligencePack := firstIntelligencePack(intelligence...)
	steps := scriptStepsFromGraph(graph, intelligencePack)
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
	markdown := renderScriptMarkdown(project, doc, productMap)
	markdown += "\n\n## 生成记录\n\n"
	markdown += "- 审批文档由本地确定性模板生成，未使用模型润色或新增目标。\n"
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
	bundle, err := buildExecutableScriptBundle(project, report, productMap, graph, doc, markdown, now, intelligencePack)
	if err != nil {
		return nil, err
	}
	synchronizeReadinessWithBundleValidation(intelligencePack, NewScriptBundleValidator().ValidateBundle(bundle))
	return &model.ScriptDocumentPackage{
		Document:         doc,
		Markdown:         markdown,
		MarkdownArtifact: doc.MarkdownArtifact,
		ExecutableBundle: bundle,
	}, nil
}

func synchronizeReadinessWithBundleValidation(intelligence *model.ProjectIntelligencePack, validation model.ExecutableScriptValidation) {
	if intelligence == nil || intelligence.ScriptReadinessReport == nil {
		return
	}
	readiness := intelligence.ScriptReadinessReport
	for _, finding := range validation.Findings {
		if finding.Severity != model.FindingSeverityBlocking {
			continue
		}
		if !agentFindingIDExists(readiness.Blockers, finding.ID) {
			readiness.Blockers = append(readiness.Blockers, finding)
		}
		if strings.TrimSpace(finding.SuggestedAction) != "" && !stringValueExists(readiness.RepairSuggestions, finding.SuggestedAction) {
			readiness.RepairSuggestions = append(readiness.RepairSuggestions, finding.SuggestedAction)
		}
		readiness.CanProceed = false
	}
	if !readiness.CanProceed {
		readiness.Summary = "最终执行大纲存在阻断项，请按修复动作补齐后重新生成。"
	}
}

func agentFindingIDExists(findings []model.AgentFinding, id string) bool {
	for _, finding := range findings {
		if finding.ID == id {
			return true
		}
	}
	return false
}

func stringValueExists(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
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
	stagePlan := buildStageApprovalPlan(project, report, graph, doc, intelligence, now)
	outline := buildBrowserAgentScriptOutline(project, graph, doc, stagePlan, intelligence, now)
	retainFormalSelectorCandidates(doc, stagePlan, outline)
	planHash, err := doc.ComputeScriptHash()
	if err != nil {
		return nil, err
	}
	promptPolicy := buildBrowserAgentPromptPolicy(project, graph, doc, outline, now)
	browserAgentContract := buildBrowserAgentContract(project, graph, doc, now)
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
	browserAgentContractHash, err := model.DigestCanonicalJSON(browserAgentContract)
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
			EntryFunction:       "runBrowserAgentOutline",
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
		StageApprovalPlan:    stagePlan,
		ScriptOutline:        outline,
		AgentPromptPolicy:    promptPolicy,
		BrowserAgentContract: browserAgentContract,
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
			BrowserAgentContractHashSHA256: browserAgentContractHash,
			UnderstandingDossierHashSHA256: dossierHash,
			MarkdownHashSHA256:             markdownHash,
			GraphHashSHA256:                doc.Reproducibility.GraphHashSHA256,
			SourceSnapshotDigest:           reportSourceDigest(report),
			GeneratorVersion:               browserAgentOutlineGeneratorVersion,
			DeterministicSeed:              doc.Reproducibility.DeterministicSeed,
			InputFingerprints:              reportInputFingerprints(report),
			BundleHashAlgorithm:            model.ExecutableBundleHashAlgorithmSemanticV2,
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

func retainFormalSelectorCandidates(doc *model.ExecutionScriptDocument, stagePlan *model.StageApprovalPlan, outline *model.BrowserAgentScriptOutline) {
	retain := func(values []model.SelectorCandidate) []model.SelectorCandidate {
		out := make([]model.SelectorCandidate, 0, len(values))
		for _, candidate := range values {
			if model.SelectorCandidateHasFormalProvenance(candidate) {
				out = append(out, candidate)
			}
		}
		return out
	}
	if doc != nil {
		for index := range doc.Steps {
			step := &doc.Steps[index]
			step.PageTarget.SelectorAlternatives = retain(step.PageTarget.SelectorAlternatives)
			step.Action.Target.SelectorAlternatives = retain(step.Action.Target.SelectorAlternatives)
			for validationIndex := range step.Validations {
				validation := &step.Validations[validationIndex]
				validation.Target.SelectorAlternatives = retain(validation.Target.SelectorAlternatives)
			}
		}
	}
	if stagePlan != nil {
		for index := range stagePlan.Stages {
			stagePlan.Stages[index].Interaction.Target.SelectorAlternatives = retain(stagePlan.Stages[index].Interaction.Target.SelectorAlternatives)
		}
	}
	if outline != nil {
		for stageIndex := range outline.Stages {
			stage := &outline.Stages[stageIndex]
			for componentIndex := range stage.Components {
				stage.Components[componentIndex].SelectorAlternatives = retain(stage.Components[componentIndex].SelectorAlternatives)
			}
			for interactionIndex := range stage.Interactions {
				stage.Interactions[interactionIndex].Target.SelectorAlternatives = retain(stage.Interactions[interactionIndex].Target.SelectorAlternatives)
			}
		}
	}
}

func firstIntelligencePack(values ...*model.ProjectIntelligencePack) *model.ProjectIntelligencePack {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

type stageRouteContract struct {
	EntryRoute                       string
	TargetRoute                      string
	TargetRouteTemplate              string
	ExpectedRouteAfterAction         string
	TargetURL                        string
	RuntimeRouteVerificationRequired bool
	CandidateRoutes                  []model.BrowserAgentRouteCandidate
}

func buildStageApprovalPlan(project *model.ProjectContext, report *model.MultimodalUnderstandingReport, graph *model.DemoWorkflowGraph, doc *model.ExecutionScriptDocument, intelligence *model.ProjectIntelligencePack, now time.Time) *model.StageApprovalPlan {
	stages := make([]model.StageApprovalStage, 0, len(doc.Steps))
	previousRoute := routePathFromCandidate(graph.EntryPoint)
	for _, step := range doc.Steps {
		node := scriptPackagerGraphNodeByID(graph, step.NodeID)
		evidence := evidenceForStage(report, intelligence, step, node)
		interaction := browserAgentInteractionFromStep(step, evidence)
		targetContract := targetContractForStep(step, node, evidence)
		routeContract := routeContractForStep(step, node, intelligence, previousRoute)
		questionRefs := investigationQuestionRefsForStage(report, intelligence, step, node)
		durationMS := step.Timing.DurationMS
		stageKind := businessStageKindForNode(node)
		runtimeAdaptive := nodeAllowsRuntimeAdaptiveTarget(node)
		capturePlan := capturePlanForStep(step, stageKind, durationMS)
		stages = append(stages, model.StageApprovalStage{
			ID:                               "stage_" + step.ID,
			Order:                            step.Order,
			NodeID:                           step.NodeID,
			BusinessStageID:                  businessStageIDForNode(node),
			StageKind:                        stageKind,
			RouteState:                       businessRouteStateForNode(node),
			Title:                            firstNonEmpty(step.Title, step.NodeID),
			Objective:                        firstNonEmpty(step.BusinessValue, step.ExpectedOutcome, step.Narrative.Voiceover, step.Title),
			BusinessIntent:                   firstNonEmpty(step.BusinessValue, step.Narrative.Voiceover, step.ExpectedOutcome),
			DurationMS:                       durationMS,
			EntryRoute:                       routeContract.EntryRoute,
			TargetRoute:                      routeContract.TargetRoute,
			TargetRouteTemplate:              routeContract.TargetRouteTemplate,
			ExpectedRouteAfterAction:         routeContract.ExpectedRouteAfterAction,
			RuntimeRouteVerificationRequired: routeContract.RuntimeRouteVerificationRequired,
			RuntimeAdaptive:                  runtimeAdaptive,
			CandidateRoutes:                  routeContract.CandidateRoutes,
			TargetURL:                        routeContract.TargetURL,
			ComponentRefs:                    uniqueStrings(componentRefsForStage(step, node, intelligence)),
			APIRefs:                          uniqueStrings(apiRefsForStage(node, intelligence)),
			StyleRefs:                        uniqueStrings(styleRefsForStage(node, intelligence)),
			DataModelRefs:                    uniqueStrings(dataModelRefsForStage(node, intelligence)),
			InputContent:                     inputContentForStage(step, evidence),
			Interaction:                      interaction,
			TargetContract:                   targetContract,
			InteractionContract:              step.InteractionContract,
			SuccessState:                     firstNonEmpty(step.ExpectedOutcome, validationSummary(step.Validations)),
			WaitConditions:                   waitConditionsForStep(step),
			CapturePoints:                    capturePointsForStep(step),
			CapturePlan:                      capturePlan,
			RiskNotes:                        stageRiskNotes(step, node),
			InvestigationQuestionRefs:        questionRefs,
			EvidenceRefs:                     evidence,
			Confidence:                       confidenceForStage(node, intelligence),
		})
		previousRoute = nextRouteAfterStage(routeContract, previousRoute)
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
			ID:                               "outline_" + stage.ID,
			StageID:                          stage.ID,
			Order:                            stage.Order,
			NodeID:                           stage.NodeID,
			BusinessStageID:                  stage.BusinessStageID,
			StageKind:                        stage.StageKind,
			RouteState:                       stage.RouteState,
			Objective:                        stage.Objective,
			EntryRoute:                       stage.EntryRoute,
			Route:                            stage.EntryRoute,
			TargetRouteTemplate:              stage.TargetRouteTemplate,
			ExpectedRouteAfterAction:         stage.ExpectedRouteAfterAction,
			RuntimeRouteVerificationRequired: stage.RuntimeRouteVerificationRequired,
			RuntimeAdaptive:                  stage.RuntimeAdaptive,
			CandidateRoutes:                  stage.CandidateRoutes,
			URL:                              stage.TargetURL,
			Components:                       components,
			Interactions:                     interactions,
			TargetContract:                   stage.TargetContract,
			InteractionContract:              stage.InteractionContract,
			WaitConditions:                   stage.WaitConditions,
			CapturePoints:                    stage.CapturePoints,
			CapturePlan:                      stage.CapturePlan,
			SuccessState:                     stage.SuccessState,
			DurationMS:                       stage.DurationMS,
			CanModify:                        []string{"selector", "selector_alternatives", "wait_conditions", "retry_strategy", "non_destructive_exploration_path", "capture_timing", "capture_plan.pre_capture_wait_ms", "capture_plan.hold_after_ms"},
			MustPreserve:                     []string{"stage_id", "node_id", "objective", "business_intent", "input_semantics", "success_state", "explicit_duration_requirement", "capture_plan.intent", "capture_plan.min_duration_ms", "capture_plan.required_assets", "safety_policy"},
			InvestigationQuestionRefs:        stage.InvestigationQuestionRefs,
			EvidenceRefs:                     stage.EvidenceRefs,
			Confidence:                       stage.Confidence,
		})
	}
	allowedRoutes := []string{}
	for _, stage := range stagePlan.Stages {
		allowedRoutes = append(allowedRoutes, stage.EntryRoute, stage.TargetRoute, stage.TargetRouteTemplate, stage.ExpectedRouteAfterAction, stage.TargetURL)
		for _, candidate := range stage.CandidateRoutes {
			allowedRoutes = append(allowedRoutes, candidate.Route)
		}
	}
	// The interaction target is the page location actually approved by the
	// App's evidence binding. It can legitimately differ from the semantic
	// entry route (for example a login form rendered at "/" while the stage is
	// named "/login"). Keep those exact targets inside the exploration scope;
	// the runtime guard still enforces allowed origin/domain and forbidden-page
	// policies before it permits the action.
	for _, stage := range stages {
		allowedRoutes = append(allowedRoutes, stage.EntryRoute, stage.Route, stage.TargetRouteTemplate, stage.ExpectedRouteAfterAction, stage.URL)
		for _, interaction := range stage.Interactions {
			allowedRoutes = append(allowedRoutes, interaction.Target.URL)
		}
	}
	// The worker opens a session at the package recording base URL before it
	// executes the first stage. That concrete path is part of the approved
	// navigation scope even when no business interaction targets it directly.
	allowedRoutes = append(allowedRoutes, doc.RecordingRunSpec.BaseURL, project.ProductURL, graph.EntryPoint)
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
		ServerEditableFields: []string{"script_outline.stages[].components[].selector", "script_outline.stages[].components[].selector_alternatives", "script_outline.stages[].wait_conditions", "script_outline.stages[].interactions[].target", "script_outline.stages[].interactions[].wait_conditions", "script_outline.stages[].capture_points", "script_outline.stages[].capture_plan.pre_capture_wait_ms", "script_outline.stages[].capture_plan.hold_after_ms"},
		ImmutableFields:      []string{"stage_approval_plan.stages[].objective", "stage_approval_plan.stages[].business_intent", "stage_approval_plan.stages[].input_content", "stage_approval_plan.stages[].capture_plan.intent", "stage_approval_plan.stages[].capture_plan.min_duration_ms", "stage_approval_plan.safety_policy", "security_policy", "recording_run_spec.allowed_domains", "recording_run_spec.forbidden_pages"},
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
		"Stage 的 capture_plan 定义要采集的素材意图、必须产物和剪辑重点；仅当 min_duration_ms > 0 时才表示用户明确要求的最低时长，你可以微调截图/hold 时机，但不得缩短明确时长或改变素材意图。",
		"Stage 中的 investigation_question_refs 是 App 端代码调查问题与证据缺口摘要；如果引用问题仍有 remaining_gaps，只能在当前产品域内通过页面观察和非破坏探索补证，不得编造代码证据。",
		"investigation_question_refs.next_actions 是 App 端建议的后续补证工具方向；你可以据此加强页面观察和 selector 修正，但不得据此访问控制面路径、执行 shell 或假装已读代码。",
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
			"non_destructive_exploration_path", "retry_strategy", "capture_timing", "capture_plan.pre_capture_wait_ms", "capture_plan.hold_after_ms",
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
			"优先使用 stage.investigation_question_refs 判断 App 端已确认的代码证据范围和仍需运行时补证的缺口。",
			"next_actions 只能作为补证建议；运行时结果必须来自真实页面 observation 或返回 repair_request。",
			"不确定时先继续在产品域内小步探索；仍不确定则停止并回传缺失证据。",
		},
		SafetyBoundaries:    append(append([]string{}, doc.SafetyPolicy.ForbiddenPages...), doc.SafetyPolicy.ForbiddenData...),
		HumanReviewRequired: true,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
}

func buildBrowserAgentContract(project *model.ProjectContext, graph *model.DemoWorkflowGraph, doc *model.ExecutionScriptDocument, now time.Time) *model.BrowserAgentContract {
	maskSelectors := append([]string{}, doc.SafetyPolicy.Redactions.MaskSelectors...)
	maskSelectors = append(maskSelectors, "input[type=password]", "[data-sensitive=true]")
	return &model.BrowserAgentContract{
		ID:                          "browser_agent_contract_" + doc.ID,
		ProjectID:                   project.ID,
		WorkflowGraphID:             graph.ID,
		SchemaVersion:               model.BrowserAgentContractSchemaVersion,
		Mode:                        "suggest_only",
		OutcomeVerifierRulesVersion: model.BrowserAgentOutcomeVerifierRulesVersion,
		BusinessAuthority: model.BrowserAgentBusinessAuthority{
			Source:                             "workflow_graph_plan_json_and_validations",
			ScriptMayChangeBusinessIntent:      false,
			RuntimePageMayChangeBusinessIntent: false,
			AgentMayChangeBusinessIntent:       false,
		},
		RepairPolicy: model.BrowserAgentRepairPolicy{
			AllowedRepairKinds: []string{"selector_strategy", "selector_alternative", "semantic_target", "wait_condition", "frame_target"},
			EditableFields: []string{
				"action.target.selector",
				"action.target.selector_alternatives",
				"action.target.test_id",
				"action.target.role",
				"action.target.text",
				"action.target.label",
				"action.target.frame",
				"action.timeout_ms",
				"action.wait_until",
			},
			ImmutableFields: []string{
				"node_id",
				"action.type",
				"action.value",
				"action.input_ref",
				"action.secret_ref",
				"page_target.url",
				"expected_outcome",
				"validations",
				"blocking",
			},
			MaxRepairAttempts:      2,
			MaxPatchOperations:     3,
			MaxAgentRuntimeMS:      30000,
			MinAutoApplyConfidence: 0.9,
			SameNodeOnly:           true,
			SameActionTypeOnly:     true,
			SameDomainOnly:         true,
			StepInsertAllowed:      false,
			StepDeleteAllowed:      false,
			StepReorderAllowed:     false,
			StepSkipAllowed:        false,
		},
		ObservationPolicy: model.BrowserAgentObservationPolicy{
			AllowedFields: []string{
				"url_without_query",
				"page_title",
				"accessibility_snapshot",
				"interactive_elements",
				"element_attributes",
				"redacted_screenshot",
				"redacted_console_summary",
				"redacted_network_summary",
			},
			FullHTMLAllowed:        false,
			CookiesAllowed:         false,
			AuthorizationAllowed:   false,
			StorageContentAllowed:  false,
			InputValuesAllowed:     false,
			SourceCodeAllowed:      false,
			MaxInteractiveElements: 200,
			MaxTextLength:          12000,
			MaskSelectors:          uniqueStrings(maskSelectors),
		},
		ModelPolicy: model.BrowserAgentModelPolicy{
			ProviderRef:               "approved_browser_agent_model",
			DeploymentMode:            "approved_cloud",
			CustomerDataExportAllowed: false,
			TrainingUsageAllowed:      false,
			RetentionAllowed:          false,
			DataResidency:             "CN",
			RequestTimeoutMS:          15000,
		},
		ConflictPolicy: model.BrowserAgentConflictPolicy{
			OnGraphPlanConflict:          "reject_package",
			OnTargetContractConflict:     "stop_and_report",
			OnRuntimeIntentConflict:      "stop_and_report",
			OnAmbiguousTarget:            "require_approval",
			OnOutcomeVerificationFailure: "retry_within_limit",
			OnRepairLimitExceeded:        "return_to_customer_app",
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func buildProjectUnderstandingDossier(project *model.ProjectContext, report *model.MultimodalUnderstandingReport, productMap *model.ProductMap, graph *model.DemoWorkflowGraph, intelligence *model.ProjectIntelligencePack, now time.Time) *model.ProjectUnderstandingDossier {
	intentText := projectIntentText(project, intelligence)
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
			dossier.ComponentEvidence = append(dossier.ComponentEvidence, model.DossierEvidence{ID: component.ID, Kind: component.Kind, Label: component.Name, ComponentRef: component.ID, FilePathHashSHA256: hashString(component.FilePath), Summary: dossierEvidenceSummary(component.Selectors, intentText), EvidenceRefs: component.EvidenceRefs, Confidence: 0.7})
			for _, action := range component.Actions {
				if !actionAllowedForIntentEvidenceForIntent(action.Label, action.Selector, intentText) {
					continue
				}
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
				dossier.ComponentEvidence = append(dossier.ComponentEvidence, model.DossierEvidence{ID: component.ID, Kind: component.Kind, Label: component.Name, FilePathHashSHA256: component.FilePathHashSHA256, Summary: dossierEvidenceSummary(append(component.SelectorHints, component.ActionLabels...), intentText), EvidenceRefs: component.EvidenceRefs, Confidence: component.Confidence})
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

func dossierEvidenceSummary(values []string, intentText string) string {
	tokens := []string{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			token := strings.TrimSpace(part)
			if token == "" || !actionAllowedForIntentEvidenceForIntent(token, token, intentText) {
				continue
			}
			tokens = append(tokens, token)
		}
	}
	return strings.Join(uniqueStrings(tokens), ", ")
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

func businessStageIDForNode(node *model.GraphNode) string {
	if node == nil || node.Metadata == nil {
		return ""
	}
	if value, ok := node.Metadata["business_stage_id"].(string); ok {
		return value
	}
	return ""
}

func businessStageKindForNode(node *model.GraphNode) model.BusinessStageKind {
	if node == nil || node.Metadata == nil {
		return ""
	}
	if value, ok := node.Metadata["business_stage_kind"].(string); ok {
		return model.BusinessStageKind(value)
	}
	return ""
}

func businessRouteStateForNode(node *model.GraphNode) model.BusinessRouteState {
	if node == nil || node.Metadata == nil {
		return ""
	}
	if value, ok := node.Metadata["business_route_state"].(string); ok {
		return model.BusinessRouteState(value)
	}
	return ""
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
		NonDestructive: step.NonDestructive,
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
		if route := routePathFromCandidate(value); route != "" {
			return route
		}
	}
	if node != nil {
		if route := routePathFromCandidate(node.PageRef); route != "" {
			return route
		}
	}
	return ""
}

func routePathFromCandidate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if isHTTPURL(value) {
		parsed, err := url.Parse(value)
		if err != nil {
			return ""
		}
		if parsed.Path == "" {
			return "/"
		}
		return pathFromURL(value)
	}
	if strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") && !strings.ContainsAny(value, "[]'\"<>") {
		if value == "/" {
			return value
		}
		return strings.TrimRight(value, "/")
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

func routeContractForStep(step model.ScriptStep, node *model.GraphNode, intelligence *model.ProjectIntelligencePack, previousRoute string) stageRouteContract {
	return routeContractForNode(node, step.Action, step.PageTarget, intelligence, previousRoute)
}

func routeContractForNode(node *model.GraphNode, action model.ScriptActionInstruction, target model.ScriptPageTarget, intelligence *model.ProjectIntelligencePack, previousRoute string) stageRouteContract {
	contract := stageRouteContract{
		EntryRoute: firstNonEmpty(previousRoute, "/"),
		TargetURL:  firstNonEmpty(target.URL, action.Target.URL),
	}
	if node != nil {
		if stageEntry, ok := node.Metadata["business_stage_entry_route"].(string); ok && strings.TrimSpace(stageEntry) != "" {
			contract.EntryRoute = stageEntry
		}
		if expected, ok := node.Metadata["expected_route_after_action"].(string); ok && strings.TrimSpace(expected) != "" {
			contract.ExpectedRouteAfterAction = expected
		}
	}
	observedValues := []any{action.Target.Selector, action.Target.SelectorAlternatives, target.Selector, target.SelectorAlternatives}
	if node != nil && node.ActionSpec != nil {
		observedValues = append(observedValues, node.ActionSpec.Target.Selector, node.ActionSpec.Target.SelectorAlternatives)
	}
	if observedRoute := observedPageScanRoute(observedValues...); observedRoute != "" {
		contract.EntryRoute = observedRoute
	}
	if contract.TargetURL == "" && node != nil {
		contract.TargetURL = urlIfHTTP(node.PageRef)
	}
	semanticText := routeSemanticText(node, action, target)
	route := routePathFromCandidate(firstNonEmpty(target.PageRef, action.Target.URL, target.URL))
	if route == "" && node != nil {
		route = routePathFromCandidate(node.PageRef)
	}
	candidates := routeCandidatesForStage(semanticText, intelligence, 4)
	if (route == "" || route == "/") && routeIsUserLoginStage(semanticText) {
		route = userLoginRouteTemplate(intelligence)
	}
	if (route == "" || route == "/") && (routeCreationStageShouldRemainInWorkspace(semanticText) || routeIsBuildModeSelectionStage(semanticText)) {
		route = appWorkspaceRouteTemplate(intelligence)
	}
	if routeShouldContinueOnProjectDetail(semanticText, contract.EntryRoute) {
		route = contract.EntryRoute
	}
	if (route == "" || route == "/") && len(candidates) > 0 && candidates[0].Route != "/" {
		route = candidates[0].Route
	}
	if !routeCandidateAllowed(route) || routeLooksLikeSourcePath(route) {
		route = ""
	}
	if route == "" {
		route = routeFallbackForStage(semanticText, contract.EntryRoute, intelligence)
	}
	contract.TargetRoute = route
	contract.TargetRouteTemplate = routeTemplateForStage(route, semanticText, intelligence)
	if routeTemplateDynamic(contract.TargetRouteTemplate) && routePathFromCandidate(contract.TargetURL) == routePathFromCandidate(contract.EntryRoute) {
		contract.TargetURL = ""
	}
	if contract.ExpectedRouteAfterAction == "" {
		contract.ExpectedRouteAfterAction = expectedRouteAfterActionForStage(semanticText, contract.EntryRoute, contract.TargetRouteTemplate, intelligence)
	}
	contract.CandidateRoutes = candidates
	contract.RuntimeRouteVerificationRequired = true
	if routeExactInArchitecture(contract.TargetRouteTemplate, intelligence) && contract.ExpectedRouteAfterAction == "" {
		contract.RuntimeRouteVerificationRequired = false
	}
	if contract.TargetRouteTemplate != "" {
		if routeTemplateDynamic(contract.TargetRouteTemplate) && routePathFromCandidate(contract.TargetURL) == "/" && contract.TargetRouteTemplate != "/" {
			contract.TargetURL = ""
		}
		if inferredURL := routeURLFromTemplate(contract.TargetRouteTemplate, intelligence); inferredURL != "" &&
			(contract.TargetURL == "" || (routePathFromCandidate(contract.TargetURL) == "/" && contract.TargetRouteTemplate != "/")) {
			contract.TargetURL = inferredURL
		}
	}
	if contract.TargetRoute == "" {
		contract.TargetRoute = contract.EntryRoute
	}
	if contract.TargetRouteTemplate == "" {
		contract.TargetRouteTemplate = contract.TargetRoute
	}
	return contract
}

func observedPageScanRoute(primaryValues ...any) string {
	primaries := map[string]bool{}
	candidates := []model.SelectorCandidate{}
	for _, value := range primaryValues {
		switch typed := value.(type) {
		case string:
			if normalized := strings.ToLower(strings.TrimSpace(typed)); normalized != "" {
				primaries[normalized] = true
			}
		case []model.SelectorCandidate:
			candidates = append(candidates, typed...)
		}
	}
	for _, candidate := range candidates {
		if candidate.SourceKind != "page_scan" || !selectorCandidateMatchesPrimaryValue(candidate, primaries) {
			continue
		}
		if route := normalizeRouteTemplate(firstNonEmpty(candidate.ObservedRouteTemplate, routePathFromCandidate(candidate.ObservedURL))); route != "" {
			return route
		}
	}
	return ""
}

func selectorCandidateMatchesPrimaryValue(candidate model.SelectorCandidate, primaries map[string]bool) bool {
	value := strings.ToLower(strings.TrimSpace(candidate.Value))
	if primaries[value] {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(candidate.Kind), "testid") {
		for primary := range primaries {
			if strings.Contains(strings.ReplaceAll(strings.ReplaceAll(primary, "\"", "'"), " ", ""), "data-testid='"+value+"'") {
				return true
			}
		}
	}
	return false
}

func routeSemanticText(node *model.GraphNode, action model.ScriptActionInstruction, target model.ScriptPageTarget) string {
	parts := []string{
		string(action.Type),
		target.URL,
		target.Selector,
		target.PageRef,
		action.Target.URL,
		action.Target.Selector,
		action.Target.Label,
		action.Target.Text,
		action.Target.TestID,
		action.Value,
	}
	if node != nil {
		parts = append(parts, node.ID, node.Title, node.Goal, node.Description, node.ExpectedOutcome, node.PageRef, node.Selector)
		if node.Narrative != nil {
			parts = append(parts, node.Narrative.Title, node.Narrative.Caption, node.Narrative.Voiceover, node.Narrative.Callout)
		}
	}
	return strings.Join(parts, " ")
}

func routeIsUserLoginStage(value string) bool {
	return containsAnyNormalized(value, "登录", "登陆", "登入", "login", "signin", "sign in")
}

func routeIsAdminIntent(value string) bool {
	return containsAnyNormalized(value, "admin", "管理员", "后台", "管理端")
}

func routeIsProjectCreationStage(value string) bool {
	return containsAnyNormalized(value, "新建项目", "创建项目", "新增项目", "项目名称", "项目名", "project name", "new project", "create project")
}

func routeIsNewProjectEntryStage(value string) bool {
	return containsAnyNormalized(value, "新建项目", "创建项目", "new project", "create project") &&
		!containsAnyNormalized(value, "项目名称", "项目名", "填写", "输入", "project name")
}

func routeCreationStageShouldRemainInWorkspace(value string) bool {
	return routeIsNewProjectEntryStage(value) || containsAnyNormalized(value, "项目名称", "项目名", "填写", "输入", "project name")
}

func routeIsBuildModeSelectionStage(value string) bool {
	return containsAnyNormalized(value, "选择构建模式", "构建模式", "build mode") &&
		!containsAnyNormalized(value, "启动", "开始", "实际构建", "等待", "观察", "agent", "智能体", "generate", "run")
}

func routeIsProjectBuildStage(value string) bool {
	return !routeIsBuildModeSelectionStage(value) &&
		containsAnyNormalized(value, "开始构建", "启动构建", "实际构建", "等待构建", "观察构建", "agent", "智能体", "build", "builder", "generate", "run")
}

func routeShouldContinueOnProjectDetail(semanticText string, previousRoute string) bool {
	return routeTemplateDynamic(previousRoute) && routeIsProjectBuildStage(semanticText)
}

func routeCandidatesForStage(semanticText string, intelligence *model.ProjectIntelligencePack, limit int) []model.BrowserAgentRouteCandidate {
	if intelligence == nil || intelligence.Architecture == nil || limit <= 0 {
		return nil
	}
	type scoredRoute struct {
		route model.BrowserAgentRouteCandidate
		score int
	}
	scored := []scoredRoute{}
	keywords := routeKeywordsForStage(semanticText)
	for _, route := range intelligence.Architecture.RouteTree {
		path := normalizeRouteTemplate(route.Path)
		if !routeCandidateAllowed(path) {
			continue
		}
		score := routeMatchScore(path, route.Name, semanticText, keywords)
		if score <= 0 {
			continue
		}
		scored = append(scored, scoredRoute{
			route: model.BrowserAgentRouteCandidate{
				Route:          path,
				RouteID:        route.ID,
				Name:           route.Name,
				Source:         "code_route_tree",
				MatchedKeyword: bestMatchedKeyword(path+" "+route.Name, keywords),
				AuthRequired:   route.AuthRequired,
				RuntimeDynamic: routeTemplateDynamic(path),
				EvidenceRefs:   route.EvidenceRefs,
				Confidence:     minFloat64(0.92, 0.55+float64(score)*0.08),
			},
			score: score,
		})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return len(scored[i].route.Route) > len(scored[j].route.Route)
		}
		return scored[i].score > scored[j].score
	})
	out := []model.BrowserAgentRouteCandidate{}
	seen := map[string]bool{}
	for _, item := range scored {
		if seen[item.route.Route] {
			continue
		}
		seen[item.route.Route] = true
		out = append(out, item.route)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func routeKeywordsForStage(semanticText string) []string {
	normalized := normalizeIntentText(semanticText)
	keywords := []string{}
	if routeIsUserLoginStage(normalized) {
		keywords = append(keywords, "login", "登录", "signin")
	}
	if routeIsProjectCreationStage(normalized) || routeIsBuildModeSelectionStage(normalized) {
		keywords = append(keywords, "app", "project", "projects", "new", "create", "workspace", "应用", "项目")
	}
	if routeIsProjectBuildStage(normalized) {
		keywords = append(keywords, "project", "build", "builder", "generate", "agent", "构建")
	}
	if len(keywords) == 0 {
		keywords = append(keywords, intentKeywordsForText(semanticText)...)
	}
	return uniqueStrings(keywords)
}

func routeMatchScore(path string, name string, semanticText string, keywords []string) int {
	score := 0
	search := normalizeIntentText(path + " " + name)
	stageText := normalizeIntentText(semanticText)
	userLogin := routeIsUserLoginStage(stageText)
	adminIntent := routeIsAdminIntent(stageText)
	projectCreation := routeIsProjectCreationStage(stageText)
	newProjectEntry := routeIsNewProjectEntryStage(stageText)
	buildModeSelection := routeIsBuildModeSelectionStage(stageText)
	projectBuild := routeIsProjectBuildStage(stageText)
	for _, keyword := range keywords {
		keyword = normalizeIntentText(keyword)
		if keyword == "" {
			continue
		}
		if strings.Contains(search, keyword) {
			score += 3
		}
	}
	switch {
	case userLogin && strings.Contains(search, "login"):
		score += 10
	case (projectCreation || buildModeSelection) && containsAnyNormalized(search, "app", "project", "workspace"):
		score += 7
	case projectBuild && containsAnyNormalized(search, "build", "builder", "project"):
		score += 7
	}
	if userLogin && path == "/login" {
		score += 8
	}
	if userLogin && strings.Contains(path, "/admin") && !adminIntent {
		score -= 14
	}
	if strings.Contains(path, "/admin") && !adminIntent {
		score -= 4
	}
	if strings.Contains(path, "?") {
		score -= 4
	}
	if path == "/" {
		score -= 2
	}
	if strings.Contains(search, "invite") && !containsAnyNormalized(stageText, "invite", "邀请") {
		score -= 10
	}
	if strings.Contains(search, "buildersquare") && !containsAnyNormalized(stageText, "buildersquare", "广场", "发布", "浏览") {
		score -= 8
	}
	if path == "/app" && (newProjectEntry || buildModeSelection || (projectCreation && !projectBuild)) {
		score += 8
	}
	if routeTemplateDynamic(path) && strings.Contains(path, "project") && (projectBuild || (projectCreation && !newProjectEntry)) {
		score += 10
	}
	if routeLooksLikeSourcePath(path) {
		score -= 100
	}
	return score
}

func routeFallbackForStage(semanticText string, previousRoute string, intelligence *model.ProjectIntelligencePack) string {
	normalized := normalizeIntentText(semanticText)
	if routeIsUserLoginStage(normalized) {
		if routeIsAdminIntent(normalized) {
			return firstExistingRouteTemplate([]string{"/admin/login", "/login", "/signin", "/auth"}, intelligence, "/login")
		}
		return userLoginRouteTemplate(intelligence)
	}
	if routeShouldContinueOnProjectDetail(normalized, previousRoute) {
		return previousRoute
	}
	if routeIsProjectBuildStage(normalized) {
		return projectBuildRouteTemplate(intelligence, dynamicProjectRouteTemplate(intelligence, previousRoute))
	}
	if routeIsBuildModeSelectionStage(normalized) {
		return appWorkspaceRouteTemplate(intelligence)
	}
	if routeIsProjectCreationStage(normalized) {
		if routeIsNewProjectEntryStage(normalized) {
			return appWorkspaceRouteTemplate(intelligence)
		}
		return appWorkspaceRouteTemplate(intelligence)
	}
	return firstNonEmpty(previousRoute, "/")
}

func expectedRouteAfterActionForStage(semanticText string, entryRoute string, targetRoute string, intelligence *model.ProjectIntelligencePack) string {
	normalized := normalizeIntentText(semanticText)
	switch {
	case routeIsUserLoginStage(normalized):
		return firstExistingRouteTemplate([]string{"/app", "/dashboard", "/workspace"}, intelligence, "/app")
	case routeIsProjectBuildStage(normalized):
		return projectBuildRouteTemplate(intelligence, dynamicProjectRouteTemplate(intelligence, firstNonEmpty(targetRoute, entryRoute)))
	case routeIsBuildModeSelectionStage(normalized):
		return appWorkspaceRouteTemplate(intelligence)
	case routeIsProjectCreationStage(normalized):
		if routeCreationStageShouldRemainInWorkspace(normalized) {
			return appWorkspaceRouteTemplate(intelligence)
		}
		return dynamicProjectRouteTemplate(intelligence, firstNonEmpty(targetRoute, entryRoute))
	}
	if routeTemplateDynamic(targetRoute) {
		return targetRoute
	}
	return ""
}

func firstExistingRouteTemplate(candidates []string, intelligence *model.ProjectIntelligencePack, fallback string) string {
	for _, candidate := range candidates {
		if routeExactInArchitecture(candidate, intelligence) {
			return candidate
		}
	}
	return fallback
}

func firstExistingDynamicRouteTemplate(candidates []string, intelligence *model.ProjectIntelligencePack, fallback string) string {
	if intelligence != nil && intelligence.Architecture != nil {
		for _, candidate := range candidates {
			for _, item := range intelligence.Architecture.RouteTree {
				path := normalizeRouteTemplate(item.Path)
				if !routeCandidateAllowed(path) || !routeTemplateDynamic(path) {
					continue
				}
				if routeTemplateEquivalent(path, candidate) {
					return path
				}
			}
		}
	}
	for _, candidate := range candidates {
		if routeExactInArchitecture(candidate, intelligence) {
			return normalizeRouteTemplate(candidate)
		}
	}
	return fallback
}

func routeTemplateEquivalent(left string, right string) bool {
	left = normalizeRouteTemplate(left)
	right = normalizeRouteTemplate(right)
	if left == "" || right == "" {
		return false
	}
	if left == right {
		return true
	}
	return routeTemplateSignature(left) == routeTemplateSignature(right)
}

func routeTemplateSignature(route string) string {
	route = normalizeRouteTemplate(route)
	parts := strings.Split(route, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, ":") || strings.HasPrefix(part, "*") {
			parts[i] = ":param"
		}
	}
	return strings.Join(parts, "/")
}

func userLoginRouteTemplate(intelligence *model.ProjectIntelligencePack) string {
	return firstExistingRouteTemplate([]string{"/login", "/signin", "/auth", "/admin/login", "/"}, intelligence, "/login")
}

func appWorkspaceRouteTemplate(intelligence *model.ProjectIntelligencePack) string {
	return firstExistingRouteTemplate([]string{"/app", "/dashboard", "/workspace", "/"}, intelligence, "/app")
}

func dynamicProjectRouteTemplate(intelligence *model.ProjectIntelligencePack, fallback string) string {
	return bestObservedDynamicRouteTemplate(intelligence, fallback, []string{"project", "workspace", "detail", "项目", "详情"})
}

func projectBuildRouteTemplate(intelligence *model.ProjectIntelligencePack, fallback string) string {
	return bestObservedDynamicRouteTemplate(intelligence, fallback, []string{"build", "progress", "log", "project", "构建", "进度", "日志"})
}

// bestObservedDynamicRouteTemplate selects only from the route tree produced
// by project understanding. It deliberately has no remembered product route
// shapes: two sites with different nesting or parameter names receive the
// same semantic scoring behavior.
func bestObservedDynamicRouteTemplate(intelligence *model.ProjectIntelligencePack, fallback string, keywords []string) string {
	if intelligence == nil || intelligence.Architecture == nil {
		return fallback
	}
	bestRoute, bestScore := "", 0
	for _, item := range intelligence.Architecture.RouteTree {
		candidate := normalizeRouteTemplate(item.Path)
		if !routeCandidateAllowed(candidate) || !routeTemplateDynamic(candidate) {
			continue
		}
		score := semanticArchitectureRouteScore(candidate, item, keywords)
		if score > bestScore {
			bestRoute, bestScore = candidate, score
		}
	}
	if bestRoute != "" {
		return bestRoute
	}
	return fallback
}

func routeExactInArchitecture(route string, intelligence *model.ProjectIntelligencePack) bool {
	route = normalizeRouteTemplate(route)
	if route == "" || intelligence == nil || intelligence.Architecture == nil {
		return false
	}
	for _, item := range intelligence.Architecture.RouteTree {
		if normalizeRouteTemplate(item.Path) == route {
			return true
		}
	}
	return false
}

func normalizeRouteTemplate(route string) string {
	route = strings.TrimSpace(route)
	if route == "" {
		return ""
	}
	if strings.Contains(route, "://") {
		route = pathFromURL(route)
	}
	if route == "" {
		return ""
	}
	if !strings.HasPrefix(route, "/") {
		return ""
	}
	if i := strings.Index(route, "?"); i >= 0 {
		route = route[:i]
	}
	route = strings.TrimRight(route, "/")
	if route == "" {
		return "/"
	}
	route = strings.ReplaceAll(route, "[id]", "{id}")
	segments := strings.Split(route, "/")
	for index, segment := range segments {
		if strings.HasPrefix(segment, ":") && len(segment) > 1 && !strings.ContainsAny(segment[1:], ":{}[]*") {
			segments[index] = "{" + segment[1:] + "}"
		}
	}
	return strings.Join(segments, "/")
}

func routeCandidateAllowed(route string) bool {
	if route == "" {
		return false
	}
	lower := strings.ToLower(route)
	if containsAny(lower, "/aigc", "/.well-known", "/v1", "execution-packages", "app-installations", "debug", "result-packages") {
		return false
	}
	if strings.Contains(lower, "callback") || strings.Contains(lower, "error=") || strings.Contains(lower, "bind_") {
		return false
	}
	if routeLooksLikeSourcePath(lower) {
		return false
	}
	return true
}

func routeLooksLikeSourcePath(route string) bool {
	lower := strings.ToLower(strings.TrimSpace(route))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "*") || strings.Contains(lower, "\\") {
		return true
	}
	if containsAny(lower, "/src/", "/components/", "/styles/", "/tests/", "/fixtures/", "/node_modules/", "/dist/", "/build/") {
		return true
	}
	for _, suffix := range []string{".tsx", ".ts", ".jsx", ".js", ".css", ".scss", ".json", ".md", ".html", ".svg", ".png", ".jpg", ".jpeg", ".webp"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

func routeTemplateForStage(route string, semanticText string, intelligence *model.ProjectIntelligencePack) string {
	route = normalizeRouteTemplate(route)
	if route == "" {
		return ""
	}
	normalized := normalizeIntentText(semanticText)
	if routeTemplateDynamic(route) {
		return route
	}
	if routeIsProjectBuildStage(normalized) {
		if strings.Contains(route, "project") {
			return projectBuildRouteTemplate(intelligence, dynamicProjectRouteTemplate(intelligence, route))
		}
		return route
	}
	if routeIsProjectCreationStage(normalized) && !routeIsNewProjectEntryStage(normalized) && strings.Contains(route, "project") {
		return dynamicProjectRouteTemplate(intelligence, route)
	}
	return route
}

func routeTemplateDynamic(route string) bool {
	for _, segment := range strings.Split(strings.Trim(route, "/"), "/") {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") && len(segment) > 2 {
			return true
		}
	}
	return false
}

func routeURLFromTemplate(route string, intelligence *model.ProjectIntelligencePack) string {
	origin := ""
	if intelligence != nil && intelligence.RunIntentScope != nil {
		origin = intelligence.RunIntentScope.ProductOrigin
		if origin == "" {
			origin = baseURL(intelligence.RunIntentScope.ProductURL)
		}
	}
	if origin == "" {
		return ""
	}
	if route == "" {
		return origin
	}
	if routeTemplateDynamic(route) {
		return ""
	}
	if route == "/" {
		return origin
	}
	return strings.TrimRight(origin, "/") + route
}

func nextRouteAfterStage(contract stageRouteContract, previousRoute string) string {
	return firstNonEmpty(contract.ExpectedRouteAfterAction, contract.TargetRouteTemplate, contract.TargetRoute, previousRoute, "/")
}

func bestMatchedKeyword(value string, keywords []string) string {
	value = normalizeIntentText(value)
	for _, keyword := range keywords {
		keyword = normalizeIntentText(keyword)
		if keyword != "" && strings.Contains(value, keyword) {
			return keyword
		}
	}
	return ""
}

func minFloat64(left float64, right float64) float64 {
	if left < right {
		return left
	}
	return right
}

func componentTargetsForStep(step model.ScriptStep, stage model.StageApprovalStage, intelligence *model.ProjectIntelligencePack) []model.BrowserAgentComponentTarget {
	_ = intelligence
	target := step.Action.Target
	if target.Selector == "" {
		target.Selector = step.PageTarget.Selector
	}
	components := []model.BrowserAgentComponentTarget{{
		ComponentRef: firstNonEmpty(target.ComponentRef, firstString(stage.ComponentRefs, "")),
		RouteRef:     stage.TargetRoute,
		Source:       target.Source,
		Role:         target.Role,
		// Accessible/name fields must contain user-facing semantics only. A
		// data-testid is emitted separately as TestID/Selector and must never be
		// concatenated into the accessible name contract.
		Name:                 firstNonEmpty(target.Label, target.Text),
		Text:                 target.Text,
		Label:                target.Label,
		TestID:               target.TestID,
		Selector:             target.Selector,
		SelectorAlternatives: append(append([]model.SelectorCandidate{}, target.SelectorAlternatives...), step.PageTarget.SelectorAlternatives...),
		EvidenceRefs:         mergeStageEvidenceRefs(stage.EvidenceRefs, target.EvidenceRefs),
		Confidence:           stage.Confidence,
	}}
	// Keep only action and outcome components that belong to this stage.  The
	// previous implementation copied every verified interaction into every
	// stage, which both bloated the package and allowed an unrelated component
	// selector to conflict with the approved action target.
	for _, validation := range step.Validations {
		result := validation.Target
		if result.Selector == "" && result.TestID == "" && result.Role == "" && result.Label == "" && result.Text == "" {
			continue
		}
		components = append(components, model.BrowserAgentComponentTarget{
			ComponentRef:         result.ComponentRef,
			RouteRef:             stage.TargetRoute,
			Source:               result.Source,
			Role:                 result.Role,
			Name:                 firstNonEmpty(result.Label, result.Text),
			Text:                 result.Text,
			Label:                result.Label,
			TestID:               result.TestID,
			Selector:             result.Selector,
			SelectorAlternatives: append([]model.SelectorCandidate{}, result.SelectorAlternatives...),
			EvidenceRefs:         mergeStageEvidenceRefs(validation.EvidenceRefs, result.EvidenceRefs),
			Confidence:           stage.Confidence,
		})
	}
	return uniqueBrowserAgentComponentTargets(components)
}

func uniqueBrowserAgentComponentTargets(values []model.BrowserAgentComponentTarget) []model.BrowserAgentComponentTarget {
	seen := map[string]bool{}
	out := make([]model.BrowserAgentComponentTarget, 0, len(values))
	for _, value := range values {
		key := strings.ToLower(strings.TrimSpace(value.ComponentRef + "|" + value.Selector + "|" + value.TestID + "|" + value.Role + "|" + value.Name))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
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

func capturePlanForStep(step model.ScriptStep, stageKind model.BusinessStageKind, durationMS int) *model.BrowserAgentCapturePlan {
	if durationMS <= 0 {
		durationMS = step.Timing.DurationMS
	}
	requiredAssets := []string{}
	if step.Capture.Video {
		requiredAssets = append(requiredAssets, "stage_video")
	}
	if step.Capture.Screenshot {
		if step.Capture.Scope == model.CaptureScopeFullPage || step.Capture.FullPage {
			requiredAssets = append(requiredAssets, "full_page_screenshot")
		} else {
			requiredAssets = append(requiredAssets, "viewport_screenshot")
		}
	}
	if step.Capture.Callout {
		requiredAssets = append(requiredAssets, "annotated_callout")
	}
	if step.Capture.Zoom {
		requiredAssets = append(requiredAssets, "interaction_closeup")
	}
	if len(requiredAssets) == 0 {
		requiredAssets = append(requiredAssets, "redacted_trace_observation")
	}
	primary := firstString(requiredAssets, "stage_video")
	shotType := captureShotTypeForStage(step, stageKind)
	holdAfterMS := stageHoldAfterMS(durationMS)
	return &model.BrowserAgentCapturePlan{
		Intent:           captureIntentForStage(step, stageKind, durationMS),
		ShotType:         shotType,
		PrimaryArtifact:  primary,
		RequiredAssets:   uniqueStrings(requiredAssets),
		MinDurationMS:    durationMS,
		PreCaptureWaitMS: 1000,
		HoldAfterMS:      holdAfterMS,
		ClipSuggestion:   captureClipSuggestionForStage(step, stageKind, durationMS),
		Notes:            captureNotesForStage(step, stageKind),
	}
}

func captureShotTypeForStage(step model.ScriptStep, stageKind model.BusinessStageKind) string {
	semantic := step.Title + " " + step.BusinessValue + " " + step.ExpectedOutcome + " " + step.Action.Value
	switch stageKind {
	case model.BusinessStageKindSessionSetup:
		return "session_setup"
	case model.BusinessStageKindBusinessInput:
		return "form_input"
	case model.BusinessStageKindModeSelection:
		return "mode_selection"
	case model.BusinessStageKindBusinessSubmit:
		return "submit_and_transition"
	case model.BusinessStageKindObserveProgress:
		return "continuous_progress_observation"
	case model.BusinessStageKindFinalObserve:
		return "final_state_summary"
	}
	if routeIsUserLoginStage(semantic) {
		return "session_setup"
	}
	if routeIsBuildModeSelectionStage(semantic) {
		return "mode_selection"
	}
	if routeIsProjectBuildStage(semantic) && (step.Action.Type == model.GraphActionWait || step.Action.Type == model.GraphActionInspect) {
		return "continuous_progress_observation"
	}
	switch step.Action.Type {
	case model.GraphActionNavigate:
		return "navigation_context"
	case model.GraphActionFill:
		return "form_input"
	case model.GraphActionClick, model.GraphActionSelect, model.GraphActionUpload:
		return "business_action"
	case model.GraphActionWait, model.GraphActionInspect:
		return "observation"
	default:
		return "stage_evidence"
	}
}

func captureIntentForStage(step model.ScriptStep, stageKind model.BusinessStageKind, durationMS int) string {
	target := firstNonEmpty(step.Action.Target.Label, step.Action.Target.Text, step.Action.Value, step.Title)
	semantic := step.Title + " " + step.BusinessValue + " " + step.ExpectedOutcome + " " + step.Action.Value
	switch stageKind {
	case model.BusinessStageKindSessionSetup:
		return "展示登录授权过程和进入工作台后的稳定页面状态。"
	case model.BusinessStageKindBusinessInput:
		if step.Action.Value != "" {
			return "展示按用户需求填写内容：" + RedactSensitiveUserText(step.Action.Value)
		}
		return "展示业务表单输入：" + target
	case model.BusinessStageKindModeSelection:
		return "展示选择业务模式：" + firstNonEmpty(target, "构建模式")
	case model.BusinessStageKindBusinessSubmit:
		return "展示提交或启动业务动作，并捕获进入下一页面/状态的过渡。"
	case model.BusinessStageKindObserveProgress:
		return continuousCaptureIntent(durationMS)
	case model.BusinessStageKindFinalObserve:
		return "展示最终稳定结果和可交付状态。"
	}
	if routeIsUserLoginStage(semantic) {
		return "展示登录授权过程和进入工作台后的稳定页面状态。"
	}
	if routeIsBuildModeSelectionStage(semantic) {
		return "展示选择业务模式：" + firstNonEmpty(target, "构建模式")
	}
	if step.Action.Type == model.GraphActionWait || (step.Action.Type == model.GraphActionInspect && containsAnyNormalized(step.Title+" "+step.ExpectedOutcome, "观察", "等待", "progress", "log")) {
		return continuousCaptureIntent(durationMS)
	}
	return "展示该阶段关键业务动作、页面反馈和稳定结果。"
}

func captureClipSuggestionForStage(step model.ScriptStep, stageKind model.BusinessStageKind, durationMS int) string {
	semantic := step.Title + " " + step.BusinessValue + " " + step.ExpectedOutcome
	if stageKind == model.BusinessStageKindSessionSetup || routeIsUserLoginStage(semantic) {
		return "保留登录前表单、提交动作和进入工作台后三段，不展示明文密码。"
	}
	if stageKind == model.BusinessStageKindObserveProgress || step.Action.Type == model.GraphActionWait {
		return "从进入目标状态后保留连续录屏，剪辑时突出页面进度、日志、预览或状态变化；如用户指定了时长，不得压缩低于该时长。"
	}
	if stageKind == model.BusinessStageKindBusinessInput {
		return "保留输入前字段上下文、填充完成状态和下一步入口。"
	}
	if stageKind == model.BusinessStageKindBusinessSubmit {
		return "保留点击/提交瞬间、页面跳转或状态变化以及首个稳定结果。"
	}
	return "保留行动前观察、动作瞬间和动作后稳定状态。"
}

func captureNotesForStage(step model.ScriptStep, stageKind model.BusinessStageKind) []string {
	notes := []string{"进入页面后至少等待 1 秒并确认渲染稳定再截图。"}
	if step.Action.SecretRef != "" || stageKind == model.BusinessStageKindSessionSetup || routeIsUserLoginStage(step.Title+" "+step.BusinessValue+" "+step.ExpectedOutcome) {
		notes = append(notes, "凭据、密码框、token 和账号敏感信息必须按 redaction policy 打码。")
	}
	if step.Capture.FocusSelector != "" {
		notes = append(notes, "优先围绕 focus_selector 取景，但不得裁掉业务结果区域。")
	}
	if stageKind == model.BusinessStageKindObserveProgress {
		notes = append(notes, "观察阶段只允许等待、滚动、截图和非破坏性查看，不允许新增业务点击。")
	}
	return uniqueStrings(notes)
}

func continuousCaptureIntent(durationMS int) string {
	if durationMS > 0 {
		return fmt.Sprintf("连续展示业务执行过程，至少保留 %d 秒页面变化。", durationMS/1000)
	}
	return "连续展示业务执行过程，直到页面出现可解释的进度、日志、预览或结果状态。"
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

func investigationQuestionRefsForStage(report *model.MultimodalUnderstandingReport, intelligence *model.ProjectIntelligencePack, step model.ScriptStep, node *model.GraphNode) []model.InvestigationQuestionRef {
	questions := codeInvestigationQuestionsForPackage(report)
	if len(questions) == 0 {
		return nil
	}
	stageKind := businessStageKindForNode(node)
	preferred := preferredInvestigationQuestionIDsForStage(stageKind, step, node)
	refs := []model.InvestigationQuestionRef{}
	add := func(question model.CodeInvestigationQuestion) {
		if question.ID == "" || investigationQuestionRefExists(refs, question.ID) {
			return
		}
		refs = append(refs, model.InvestigationQuestionRef{
			ID:              question.ID,
			IntentLabel:     question.IntentLabel,
			Status:          question.Status,
			EvidenceSummary: question.EvidenceSummary,
			RemainingGaps:   limitStrings(question.RemainingGaps, 4),
			NextActions:     limitInvestigationNextActions(question.NextActions, 3),
			ToolCallIDs:     limitStrings(question.ToolCallIDs, 4),
			Confidence:      question.Confidence,
		})
	}
	for _, id := range preferred {
		for _, question := range questions {
			if question.ID == id {
				add(question)
			}
		}
	}
	if len(refs) >= 2 {
		return refs
	}
	stageText := investigationStageMatchText(step, node, intelligence)
	type scoredQuestion struct {
		question model.CodeInvestigationQuestion
		score    int
	}
	scored := []scoredQuestion{}
	for _, question := range questions {
		score := keywordMatchScore(question.QueryTerms, stageText)
		if score == 0 {
			score = keywordMatchScore(intentKeywordsForText(question.IntentLabel+" "+question.Question), stageText)
		}
		if score > 0 {
			scored = append(scored, scoredQuestion{question: question, score: score})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].question.ID < scored[j].question.ID
		}
		return scored[i].score > scored[j].score
	})
	for _, item := range scored {
		if len(refs) >= 2 {
			break
		}
		add(item.question)
	}
	return refs
}

func codeInvestigationQuestionsForPackage(report *model.MultimodalUnderstandingReport) []model.CodeInvestigationQuestion {
	if report == nil {
		return nil
	}
	questions := []model.CodeInvestigationQuestion{}
	seen := map[string]bool{}
	for _, snapshot := range report.CodeSnapshots {
		if snapshot.InvestigationTrace == nil {
			continue
		}
		for _, question := range snapshot.InvestigationTrace.Questions {
			if question.ID == "" || seen[question.ID] {
				continue
			}
			seen[question.ID] = true
			questions = append(questions, question)
		}
	}
	return questions
}

func preferredInvestigationQuestionIDsForStage(kind model.BusinessStageKind, step model.ScriptStep, node *model.GraphNode) []string {
	switch kind {
	case model.BusinessStageKindSessionSetup:
		return []string{"question_session_setup"}
	case model.BusinessStageKindBusinessInput, model.BusinessStageKindBusinessAction:
		if containsAnyNormalized(investigationStageMatchText(step, node, nil), "新建项目", "创建项目", "新增项目", "项目名称", "项目名", "new project", "create project", "project") {
			return []string{"question_project_creation"}
		}
	case model.BusinessStageKindModeSelection, model.BusinessStageKindBusinessSubmit, model.BusinessStageKindObserveProgress, model.BusinessStageKindFinalObserve:
		return []string{"question_build_mode_agent", "question_project_creation"}
	}
	return nil
}

func investigationStageMatchText(step model.ScriptStep, node *model.GraphNode, intelligence *model.ProjectIntelligencePack) string {
	parts := []string{
		step.ID,
		step.NodeID,
		step.Title,
		step.BusinessValue,
		step.ExpectedOutcome,
		step.Narrative.Voiceover,
		step.Action.Target.Selector,
		step.Action.Target.Label,
		step.Action.Target.Text,
		step.Action.Value,
	}
	if node != nil {
		parts = append(parts, node.ID, node.Title, node.Goal, node.Description, node.Action, node.Selector, node.InputData, node.ExpectedOutcome)
		if node.ActionSpec != nil {
			parts = append(parts, node.ActionSpec.Value, node.ActionSpec.Target.Selector, node.ActionSpec.Target.Label, node.ActionSpec.Target.Text, node.ActionSpec.Target.TestID, node.ActionSpec.Target.ComponentRef)
		}
		if node.Metadata != nil {
			for _, key := range []string{"stage_kind", "business_stage_id", "route_state"} {
				if value, ok := node.Metadata[key].(string); ok {
					parts = append(parts, value)
				}
			}
		}
	}
	if intelligence != nil && intelligence.BusinessStagePlan != nil {
		for _, stage := range intelligence.BusinessStagePlan.Stages {
			if node != nil && (stage.ID == businessStageIDForNode(node) || stage.ID == node.ID) {
				parts = append(parts, stage.Title, stage.Objective, stage.UserIntent, stage.Action.Label, stage.Action.InputSemantic, stage.Action.InputValue, string(stage.Kind), string(stage.RouteState))
			}
		}
	}
	return strings.Join(parts, " ")
}

func investigationQuestionRefExists(refs []model.InvestigationQuestionRef, id string) bool {
	for _, ref := range refs {
		if ref.ID == id {
			return true
		}
	}
	return false
}

func limitInvestigationNextActions(values []model.CodeInvestigationNextAction, limit int) []model.CodeInvestigationNextAction {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return values[:limit]
}

func uncertaintyReportForBundle(project *model.ProjectContext, intelligence *model.ProjectIntelligencePack, graph *model.DemoWorkflowGraph) []model.StageUncertainty {
	items := []model.StageUncertainty{}
	if intelligence != nil && intelligence.MissingEvidenceReport != nil {
		blockingItemFound := false
		for _, item := range intelligence.MissingEvidenceReport.Items {
			blocks := missingEvidenceItemBlocks(intelligence.MissingEvidenceReport, item)
			blockingItemFound = blockingItemFound || blocks
			items = append(items, model.StageUncertainty{
				ID:              item.ID,
				Kind:            item.MissingKind,
				Summary:         item.Message,
				Blocking:        blocks,
				SuggestedAction: item.SuggestedAction,
				EvidenceRefs:    item.EvidenceRefs,
			})
		}
		if intelligence.MissingEvidenceReport.Blocking && !blockingItemFound {
			summary := strings.TrimSpace(intelligence.MissingEvidenceReport.Summary)
			if summary == "" {
				summary = "关键执行证据缺失。"
			}
			items = append(items, model.StageUncertainty{ID: "uncertain_missing_evidence_" + shortHash(project.ID), Kind: "missing_evidence", Summary: summary, Blocking: true, SuggestedAction: "补充关键业务动作和结果证据后重新生成执行包。", EvidenceRefs: intelligence.MissingEvidenceReport.EvidenceRefs})
		}
	}
	if graph == nil || len(graph.Nodes) == 0 {
		items = append(items, model.StageUncertainty{ID: "uncertain_graph_empty_" + shortHash(project.ID), Kind: "workflow_graph", Summary: "执行图为空，无法生成 stage 大纲。", Blocking: true})
	}
	if project != nil && project.DemoAccount == nil && graph != nil {
		for _, node := range graph.Nodes {
			if businessStageKindForNode(node) != model.BusinessStageKindSessionSetup {
				continue
			}
			items = append(items, model.StageUncertainty{
				ID:              "human_checkpoint_session_" + shortHash(node.ID),
				NodeID:          node.ID,
				Kind:            "human_checkpoint",
				Summary:         "登录阶段没有可执行的 credential secret_ref，不能自动声称已进入工作台。",
				Blocking:        true,
				SuggestedAction: "由用户人工完成登录并确认工作台页面，或通过安全卡片保存演示凭据后重新生成。",
			})
		}
	}
	return items
}

func missingEvidenceItemBlocks(report *model.MissingEvidenceReport, item model.MissingEvidenceItem) bool {
	if report == nil || !report.Blocking {
		return false
	}
	severity := strings.ToLower(strings.TrimSpace(item.Severity))
	return severity == "blocking" || severity == "error" || severity == "critical"
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
	if projectName := intentProjectName(intentText); projectName != "" && !containsAnyNormalized(combined, projectName) {
		findings = append(findings, fmt.Sprintf("需求要求项目名称为%s，但执行脚本没有输入或选择该项目名称", projectName))
	}
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
	if containsAnyNormalized(intentText, "新建项目", "创建项目", "新增项目", "项目名称", "项目名", "构建模式") &&
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
	return requiredObservationDurationMS(intentText)
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

func scriptStepsFromGraph(graph *model.DemoWorkflowGraph, intelligence *model.ProjectIntelligencePack) []model.ScriptStep {
	steps := make([]model.ScriptStep, 0, len(graph.Nodes))
	elapsedMS := 0
	previousRoute := routePathFromCandidate(graph.EntryPoint)
	for i, node := range graph.Nodes {
		if node == nil {
			continue
		}
		durationMS := node.DurationHintMS
		capture := captureSpecForNode(node)
		narrative := narrativeForNode(node)
		action := scriptActionForNode(node)
		target := scriptPageTargetForNode(node)
		validations := append([]model.ValidationSpec{}, node.Validations...)
		routeContract := routeContractForNode(node, action, target, intelligence, previousRoute)
		if routeContract.EntryRoute != "" {
			target.URL = routeContract.EntryRoute
		}
		if routeContract.TargetRouteTemplate != "" && routeTemplateDynamic(routeContract.TargetRouteTemplate) && routePathFromCandidate(routeContract.TargetURL) == routePathFromCandidate(routeContract.EntryRoute) {
			routeContract.TargetURL = ""
		}
		if routeContract.TargetRoute != "" {
			target.PageRef = routeContract.TargetRoute
		}
		if routeContract.TargetURL != "" {
			if action.Target.URL == "" || routeContract.TargetURL == "" {
				action.Target.URL = routeContract.TargetURL
			}
		}
		selector := bestSelectorForScriptStep(action, target)
		if selector != "" {
			action.Target.Selector = selector
			target.Selector = selector
			if capture.FocusSelector == "" && !selectorLooksGeneric(selector) {
				capture.FocusSelector = selector
			}
		}
		if businessActionNeedsExecutableSelector(action.Type) && !nodeAllowsRuntimeAdaptiveTarget(node) && (!selectorUsableForBusinessAction(selector) || !nodeVerifiedForBusinessAction(node)) {
			action.Type = model.GraphActionInspect
			action.Target.Selector = ""
			target.Selector = ""
			capture.FocusSelector = ""
			validations = softenBlockingValidations(validations)
		}
		validations = ensureRequiredValidationsForStep(node, action, target, validations)
		contractValidations := append([]model.ValidationSpec{}, validations...)
		validations = v1WireCompatibleValidations(validations)
		targetContract := targetContractForNode(node, action, target, validations)
		nonDestructive := targetContract != nil && !targetContract.Destructive
		if node.Metadata != nil {
			if approved, ok := node.Metadata["non_destructive"].(bool); ok {
				nonDestructive = approved
			}
		}
		step := model.ScriptStep{
			ID:              fmt.Sprintf("step_%02d_%s", i+1, node.ID),
			Order:           i + 1,
			NodeID:          node.ID,
			StageKind:       businessStageKindForNode(node),
			RouteState:      businessRouteStateForNode(node),
			NonDestructive:  nonDestructive,
			RuntimeAdaptive: nodeAllowsRuntimeAdaptiveTarget(node),
			Title:           firstNonEmpty(node.Title, "执行步骤"),
			BusinessValue:   firstNonEmpty(node.Goal, node.Description),
			PageTarget:      target,
			Action:          action,
			TargetContract:  targetContract,
			ExpectedOutcome: node.ExpectedOutcome,
			Validations:     validations,
			Capture:         capture,
			Timing:          model.NodeTimingHint{NodeID: node.ID, DurationMS: durationMS, HoldAfterMS: stageHoldAfterMS(durationMS)},
			Narrative:       narrative,
			EvidenceRefs:    node.EvidenceRefs,
			Blocking:        isBlockingScriptStep(node, action.Type, validations),
		}
		step.InteractionContract = interactionContractForStep(step, node, targetContract, evidenceForInteractionContract(node, action, contractValidations), contractValidations)
		steps = append(steps, step)
		if routeContract.ExpectedRouteAfterAction != "" {
			previousRoute = routeContract.ExpectedRouteAfterAction
		} else if routeContract.TargetRoute != "" {
			previousRoute = routeContract.TargetRoute
		}
		elapsedMS += durationMS
		_ = elapsedMS
	}
	archetype := productArchetypeForSteps(steps)
	for index := range steps {
		if steps[index].InteractionContract != nil {
			steps[index].InteractionContract.Archetype = archetype
		}
	}
	return steps
}

func evidenceForInteractionContract(node *model.GraphNode, action model.ScriptActionInstruction, validations []model.ValidationSpec) []model.EvidenceRef {
	refs := append([]model.EvidenceRef{}, node.EvidenceRefs...)
	refs = append(refs, action.Target.EvidenceRefs...)
	for _, validation := range validations {
		refs = append(refs, validation.EvidenceRefs...)
		refs = append(refs, validation.Target.EvidenceRefs...)
	}
	return uniqueEvidenceRefs(refs)
}

func interactionContractForStep(step model.ScriptStep, node *model.GraphNode, target *model.BrowserAgentTargetContract, evidence []model.EvidenceRef, contractValidations []model.ValidationSpec) *model.InteractionContract {
	if node == nil || target == nil || target.Destructive || !step.NonDestructive || len(evidence) == 0 {
		return nil
	}
	predicates := make([]model.InteractionPredicate, 0, len(contractValidations))
	for _, validation := range contractValidations {
		if !validation.Required || !validationKindAllowedForBrowserAgent(validation.Kind) {
			continue
		}
		kind := strings.TrimSpace(validation.Kind)
		if kind == "playable_surface_visible" {
			kind = "interactive_surface_visible"
		} else if kind == "page_changed" && step.Action.Type == model.GraphActionPress {
			kind = "frame_surface_changed"
		} else if kind == "page_changed" && step.StageKind == model.BusinessStageKindBusinessSubmit {
			kind = "dom_changed"
		}
		predicates = append(predicates, model.InteractionPredicate{
			ID: validation.ID, Kind: kind, Target: validation.Target, Expected: validation.Expected,
			Required: true, TimeoutMS: validation.TimeoutMS, EvidenceRefs: uniqueEvidenceRefs(validation.EvidenceRefs),
		})
	}
	if len(predicates) == 0 {
		return nil
	}
	preconditions := make([]model.InteractionPredicate, 0, len(step.Action.Preconditions))
	for _, assertion := range step.Action.Preconditions {
		if !validationKindAllowedForBrowserAgent(assertion.Kind) {
			continue
		}
		preconditions = append(preconditions, model.InteractionPredicate{
			ID:   firstNonEmpty(assertion.ID, "precondition_"+shortHash(step.NodeID+"|"+assertion.Kind)),
			Kind: assertion.Kind, Target: assertion.Target, Expected: assertion.Expected,
			Required: assertion.Required, TimeoutMS: assertion.TimeoutMS, EvidenceRefs: uniqueEvidenceRefs(assertion.EvidenceRefs),
		})
	}
	contract := &model.InteractionContract{
		SchemaVersion: model.InteractionContractSchemaVersion,
		ContractID:    "interaction_" + shortHash(step.NodeID+"|"+string(step.Action.Type)+"|"+target.SemanticID),
		SemanticGoal:  firstNonEmpty(step.BusinessValue, step.ExpectedOutcome, step.Title),
		Archetype:     model.ProductArchetypeUnknown, ActionKind: step.Action.Type, ReplayPolicy: interactionReplayPolicy(step.Action.Type), TargetSemanticID: target.SemanticID, ActionTarget: step.Action.Target,
		Preconditions: preconditions, ExpectedTransitions: predicates, EvidenceRefs: evidence, NonDestructive: true,
	}
	if err := model.ValidateInteractionContract(*contract); err != nil {
		return nil
	}
	return contract
}

// v1WireCompatibleValidations keeps the immutable client package consumable by
// already deployed browser-agent-outcome-verifier-rules-v1 servers. Richer
// observer intent remains available in InteractionContract; this transport
// projection never changes the approved action, target, input, or evidence.
func v1WireCompatibleValidations(validations []model.ValidationSpec) []model.ValidationSpec {
	result := append([]model.ValidationSpec{}, validations...)
	for index := range result {
		switch strings.TrimSpace(result[index].Kind) {
		case "state_changed", "dom_changed", "aria_changed", "network_settled", "visual_region_changed", "frame_surface_changed":
			result[index].Kind = "page_changed"
		case "interactive_surface_visible":
			result[index].Kind = "playable_surface_visible"
		}
	}
	return result
}

func interactionReplayPolicy(action model.GraphActionType) model.InteractionReplayPolicy {
	switch action {
	case model.GraphActionWait, model.GraphActionInspect, model.GraphActionAssert:
		return model.InteractionReplayObserveOnly
	case model.GraphActionFill, model.GraphActionSelect:
		return model.InteractionReplayIdempotentWrite
	default:
		return model.InteractionReplayOnceEffect
	}
}

// productArchetypeForSteps classifies observed interaction structure only. It
// deliberately ignores hostname, paths, selectors, and brand copy.
func productArchetypeForSteps(steps []model.ScriptStep) model.ProductArchetype {
	return routeProductArchetype(steps)
}

func nodeVerifiedForBusinessAction(node *model.GraphNode) bool {
	if node == nil {
		return false
	}
	// Business-stage nodes are synthesized from the App's approved planning
	// layer and therefore do not carry the legacy verified_interaction_id.
	// Accept them only when the exact executable selector is independently
	// bound to complete, live page-scan provenance.
	if node.ActionSpec != nil {
		selector := firstNonEmpty(node.ActionSpec.Target.Selector, node.Selector)
		for _, candidate := range node.ActionSpec.Target.SelectorAlternatives {
			if strings.EqualFold(strings.TrimSpace(candidate.SourceKind), "page_scan") &&
				model.SelectorCandidateHasFormalProvenance(candidate) &&
				selectorCandidateMatchesValue(candidate, selector) {
				return true
			}
		}
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

func nodeAllowsRuntimeAdaptiveTarget(node *model.GraphNode) bool {
	if node == nil || node.Metadata == nil {
		return false
	}
	if nonDestructive, ok := node.Metadata["non_destructive"].(bool); !ok || !nonDestructive {
		return false
	}
	if status, _ := node.Metadata["verification_status"].(string); status == "runtime_adaptive" || status == "business_stage_plan" {
		return node.Metadata["runtime_adaptive"] == true
	}
	return node.Metadata["runtime_adaptive"] == true
}

func ensureRequiredValidationsForStep(node *model.GraphNode, action model.ScriptActionInstruction, target model.ScriptPageTarget, validations []model.ValidationSpec) []model.ValidationSpec {
	out := append([]model.ValidationSpec{}, validations...)
	if hasRequiredValidation(out) || node == nil {
		return out
	}
	switch action.Type {
	case model.GraphActionNavigate:
		if url := firstNonEmpty(action.Target.URL, target.URL); url != "" {
			out = append(out, model.ValidationSpec{
				ID:        "validate_url_" + node.ID,
				Kind:      "url_matches",
				Assertion: "导航后 URL 应保持在目标产品路由内",
				Expected:  url,
				Severity:  "blocking",
				Required:  true,
			})
		}
	case model.GraphActionClick, model.GraphActionFill, model.GraphActionSelect, model.GraphActionUpload, model.GraphActionAPICall:
		validationTarget := action.Target
		if validationTarget.Selector == "" && target.Selector != "" {
			validationTarget.Selector = target.Selector
		}
		kind := "element_visible"
		expected := any(true)
		if validationTarget.Selector == "" && validationTarget.TestID == "" && validationTarget.Role == "" && validationTarget.Text == "" && validationTarget.Label == "" {
			kind = "text_contains"
			validationTarget.Text = firstNonEmpty(node.ExpectedOutcome, node.Title, node.Goal)
		}
		out = append(out, model.ValidationSpec{
			ID:        "validate_business_" + node.ID,
			Kind:      kind,
			Target:    validationTarget,
			Assertion: firstNonEmpty(node.ExpectedOutcome, "关键业务控件可见并完成操作"),
			Expected:  expected,
			Severity:  "blocking",
			Required:  true,
		})
	case model.GraphActionPress:
		out = append(out, model.ValidationSpec{
			ID: "validate_keyboard_change_" + node.ID, Kind: "page_changed",
			Target: action.Target, Assertion: firstNonEmpty(node.ExpectedOutcome, "键盘操作后页面画面必须发生变化"),
			Expected: true, Severity: "blocking", Required: true,
		})
	case model.GraphActionWait, model.GraphActionInspect:
		stageKind := businessStageKindForNode(node)
		if stageKind != model.BusinessStageKindSessionSetup && stageKind != model.BusinessStageKindObserveProgress && stageKind != model.BusinessStageKindFinalObserve {
			break
		}
		validationTarget := action.Target
		if validationTarget.URL == "" {
			validationTarget.URL = target.URL
		}
		kind := "url_matches"
		expected := any(validationTarget.URL)
		if validationTarget.URL == "" {
			kind = "text_contains"
			expected = firstNonEmpty(node.ExpectedOutcome, node.Title, node.Goal)
		}
		out = append(out, model.ValidationSpec{
			ID:        "validate_observation_" + node.ID,
			Kind:      kind,
			Target:    validationTarget,
			Assertion: firstNonEmpty(node.ExpectedOutcome, "观察阶段必须验证页面状态"),
			Expected:  expected,
			Severity:  "blocking",
			Required:  true,
		})
	}
	return out
}

func hasRequiredValidation(validations []model.ValidationSpec) bool {
	for _, validation := range validations {
		if validation.Required && validationKindAllowedForBrowserAgent(validation.Kind) {
			return true
		}
	}
	return false
}

func validationKindAllowedForBrowserAgent(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "url_matches", "element_visible", "element_hidden", "text_contains", "attribute_equals", "value_equals", "checked_equals", "element_count", "page_title_contains", "page_changed", "state_changed", "dom_changed", "aria_changed", "network_settled", "visual_region_changed", "frame_surface_changed", "interactive_surface_visible", "playable_surface_visible":
		return true
	default:
		return false
	}
}

func targetContractForStep(step model.ScriptStep, node *model.GraphNode, evidence []model.EvidenceRef) *model.BrowserAgentTargetContract {
	contract := targetContractForNode(node, step.Action, step.PageTarget, step.Validations)
	if contract == nil {
		return nil
	}
	contract.EvidenceRefs = uniqueEvidenceRefs(append(contract.EvidenceRefs, evidence...))
	return contract
}

func targetContractForNode(node *model.GraphNode, action model.ScriptActionInstruction, pageTarget model.ScriptPageTarget, validations []model.ValidationSpec) *model.BrowserAgentTargetContract {
	if node == nil {
		return nil
	}
	target := action.Target
	semanticSeed := strings.Join([]string{
		node.ID,
		string(action.Type),
		firstNonEmpty(target.ComponentRef, node.PageRef, pageTarget.PageRef),
		firstNonEmpty(target.TestID, target.Role, target.Label, target.Text, target.Selector, node.Title),
	}, "|")
	allowedNames := accessibleNamesForActionTarget(target)
	if node.Metadata != nil && node.Metadata["runtime_adaptive"] == true && action.Type == model.GraphActionClick {
		if raw, ok := action.Parameters["approved_accessible_names"].(string); ok {
			for _, name := range strings.Split(raw, "|") {
				if name = strings.TrimSpace(name); name != "" {
					allowedNames = append(allowedNames, name)
				}
			}
		}
	}
	if len(allowedNames) == 0 && (action.Type == model.GraphActionNavigate || action.Type == model.GraphActionWait || action.Type == model.GraphActionInspect) {
		allowedNames = uniqueStrings(nonEmptyStrings(target.Label, target.Text, node.Title))
	}
	allowedRoles := []string{}
	if target.Role != "" {
		allowedRoles = append(allowedRoles, target.Role)
	} else if action.Type == model.GraphActionClick {
		allowedRoles = append(allowedRoles, "button", "link")
	} else if action.Type == model.GraphActionFill || action.Type == model.GraphActionSelect || action.Type == model.GraphActionUpload {
		allowedRoles = append(allowedRoles, "textbox", "combobox", "radio", "checkbox")
	}
	for _, validation := range validations {
		if validation.Target.Role != "" {
			allowedRoles = append(allowedRoles, validation.Target.Role)
		}
	}
	return &model.BrowserAgentTargetContract{
		SemanticID:     "semantic_" + shortHash(semanticSeed),
		Purpose:        firstNonEmpty(node.Goal, node.Description, node.ExpectedOutcome, node.Title),
		AllowedRoles:   uniqueStrings(allowedRoles),
		AllowedNames:   limitStrings(uniqueStringsStable(allowedNames), 12),
		ForbiddenNames: []string{"删除", "移除", "支付", "账单", "API Key", "Delete", "Remove", "Pay", "Billing"},
		ComponentRef:   firstNonEmpty(target.ComponentRef, firstString(node.FeatureRefs, ""), node.PageRef),
		Destructive:    destructiveActionText(node.Title + " " + node.Goal + " " + node.Description + " " + target.Label + " " + target.Text),
		EvidenceRefs:   uniqueEvidenceRefs(append(append([]model.EvidenceRef{}, node.EvidenceRefs...), target.EvidenceRefs...)),
		Confidence:     0.76,
	}
}

func uniqueStringsStable(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func accessibleNamesForActionTarget(target model.ActionTarget) []string {
	testIDs := nonEmptyStrings(target.TestID)
	for _, candidate := range target.SelectorAlternatives {
		if strings.EqualFold(strings.TrimSpace(candidate.Kind), "testid") {
			testIDs = append(testIDs, candidate.Value)
		}
	}
	names := []string{}
	for _, candidate := range target.SelectorAlternatives {
		if name := strings.TrimSpace(candidate.ObservedAccessibleName); name != "" {
			names = append(names, name)
		}
	}
	for _, value := range []string{target.Label, target.Text} {
		value = stripTestIDFromAccessibleName(value, testIDs)
		if value != "" {
			names = append(names, value)
		}
	}
	return uniqueStrings(names)
}

func stripTestIDFromAccessibleName(value string, testIDs []string) string {
	value = strings.TrimSpace(value)
	for _, testID := range testIDs {
		testID = strings.TrimSpace(testID)
		if testID == "" {
			continue
		}
		if strings.EqualFold(value, testID) {
			return ""
		}
		if len(value) > len(testID) && strings.EqualFold(value[len(value)-len(testID):], testID) {
			prefix := strings.TrimSpace(value[:len(value)-len(testID)])
			if prefix != value {
				return prefix
			}
		}
	}
	return value
}

func destructiveActionText(value string) bool {
	lower := strings.ToLower(value)
	return containsAny(lower, "delete", "remove", "pay", "billing", "api key", "删除", "移除", "支付", "账单", "密钥")
}

func nonEmptyStrings(values ...string) []string {
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func stageHoldAfterMS(durationMS int) int {
	if durationMS <= 0 {
		return 0
	}
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
	targetDuration := 0
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
		timingHints = append(timingHints, model.NodeTimingHint{NodeID: node.ID, DurationMS: durationMS, HoldAfterMS: stageHoldAfterMS(durationMS)})
		if node.Capture != nil && (node.Capture.Video || node.Capture.Screenshot) && durationMS > 0 {
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
	if targetDuration <= 0 {
		targetDuration = (startMS + 999) / 1000
	}
	maxDuration := 0
	if targetDuration > 0 {
		maxDuration = targetDuration + 30
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
			MaxDurationSec:    maxDuration,
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
		action := model.ScriptActionInstruction{
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
		if businessStageKindForNode(node) == model.BusinessStageKindSessionSetup && node.Metadata != nil {
			// Formal login is a brokered session operation, not a single DOM fill.
			// Keep the opaque ref in the approved package while the Worker consumes
			// the structured username/password grant under the session_setup scope.
			action.Type = model.GraphActionInspect
			action.Value = ""
			if secretRef, ok := node.Metadata["demo_password_secret_ref"].(string); ok {
				action.SecretRef = secretRef
			}
			if usernameRef, ok := node.Metadata["demo_username_secret_ref"].(string); ok {
				action.InputRef = usernameRef
			}
			action.Target.Label = firstNonEmpty(action.Target.Label, "登录表单")
		}
		return action
	}
	actionType := graphActionTypeFromKind(node.Action, node.Selector)
	return model.ScriptActionInstruction{
		Type:   actionType,
		Target: model.ActionTarget{URL: urlIfHTTP(node.Selector), Selector: selectorIfNotURL(node.Selector)},
		Value:  node.InputData,
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
		if actionType == model.GraphActionAssert && !selectorUsableForBlockingAssertion(selector) {
			report.BlockingAssertRisk++
		}
		if actionType == model.GraphActionPress && node.ActionSpec != nil && len(approvedKeyboardKeys(node.ActionSpec.Parameters)) > 0 && graphNodeHasPageChangedValidation(node) {
			report.ExecutableActionCount++
		} else if isBusinessAction(actionType) && selectorUsableForBusinessAction(selector) && nodeVerifiedForBusinessAction(node) {
			report.ExecutableActionCount++
		} else if isBusinessAction(actionType) {
			report.Warnings = append(report.Warnings, "业务动作缺少稳定且已验证的 selector，上传前应补充页面扫描、截图标注或 data-testid/role/name 证据。")
		}
	}
	report.ObservationOnly = report.ExecutableActionCount == 0
	if report.ObservationOnly {
		report.Blockers = append(report.Blockers, "当前执行图没有 click/fill/select/upload/press/api_call 等已验证真实业务动作，不能自动上传录制。请确认需求目标能在产品页面中找到可见、可用、可解释的控件或键盘验证证据。")
	}
	if report.GenericSelectorCount > 0 {
		report.Warnings = append(report.Warnings, "检测到 body/main/section/div 等泛 selector，业务动作不会使用这些 selector 作为 blocking 目标。")
	}
	if report.LoginActionCount > 1 {
		report.Warnings = append(report.Warnings, "检测到重复登录动作，脚本应只登录一次并复用已登录会话。")
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
	case model.GraphActionClick, model.GraphActionFill, model.GraphActionSelect, model.GraphActionUpload, model.GraphActionPress, model.GraphActionAPICall:
		return true
	default:
		return false
	}
}

func graphNodeHasPageChangedValidation(node *model.GraphNode) bool {
	if node == nil {
		return false
	}
	for _, validation := range node.Validations {
		if validation.Required && validation.Kind == "page_changed" && validation.Expected == true {
			return true
		}
	}
	return false
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

func renderScriptMarkdown(project *model.ProjectContext, doc *model.ExecutionScriptDocument, productMap *model.ProductMap) string {
	var builder strings.Builder
	rawRequirement := ""
	targetAudience := "中国客户"
	if project != nil {
		rawRequirement = strings.TrimSpace(project.ProductDescription)
		targetAudience = firstNonEmpty(project.TargetAudience, targetAudience)
	}
	summary := firstNonEmpty(rawRequirement, doc.Summary)
	builder.WriteString("# " + firstNonEmpty(doc.Title, "演示执行脚本文档") + "\n\n")
	builder.WriteString("## 摘要\n\n")
	builder.WriteString(summary + "\n\n")
	builder.WriteString("## 演示目标\n\n")
	builder.WriteString("- 目标：" + summary + "\n")
	builder.WriteString("- 受众：" + targetAudience + "\n")
	builder.WriteString("- 价值主张：严格按用户需求展示已审批的业务阶段、输入内容、页面结果和素材采集目标。\n\n")
	builder.WriteString("## 生成依据\n\n")
	if productMap != nil && productMap.Summary != "" {
		builder.WriteString("- 产品理解：已读取本地项目结构、需求相关 route/component/API/selector 摘要，并用于生成 stage 证据链。\n")
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
	if doc.RecordingRunSpec.Timeline.TargetDurationSec > 0 {
		builder.WriteString("- 目标时长：" + fmt.Sprintf("%d 秒\n", doc.RecordingRunSpec.Timeline.TargetDurationSec))
	} else {
		builder.WriteString("- 目标时长：未指定固定总时长，按用户明确 stage 要求、页面状态和 Browser Agent 运行时观察控制节奏。\n")
	}
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
		builder.WriteString("- 页面路由：" + firstNonEmpty(step.PageTarget.PageRef, routePathFromCandidate(firstNonEmpty(step.PageTarget.URL, step.Action.Target.URL)), "运行时确认") + "\n")
		builder.WriteString("- 运行时 URL：" + firstNonEmpty(step.PageTarget.URL, step.Action.Target.URL, "由 Browser Agent 在产品域内探索确认") + "\n")
		if selector := firstNonEmpty(step.Action.Target.Selector, step.PageTarget.Selector); selector != "" {
			builder.WriteString("- 目标控件：" + selector + "\n")
		}
		builder.WriteString("- 预期：" + step.ExpectedOutcome + "\n")
		builder.WriteString("- 录制：" + captureSummary(step.Capture) + "\n")
		builder.WriteString("- 素材意图：" + capturePlanSummary(capturePlanForStep(step, "", step.Timing.DurationMS)) + "\n")
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

func capturePlanSummary(plan *model.BrowserAgentCapturePlan) string {
	if plan == nil {
		return "按 stage 结果采集素材。"
	}
	assets := strings.Join(plan.RequiredAssets, "/")
	if assets == "" {
		assets = plan.PrimaryArtifact
	}
	duration := ""
	if plan.MinDurationMS > 0 {
		duration = fmt.Sprintf("；最低 %d 秒", plan.MinDurationMS/1000)
	}
	return firstNonEmpty(plan.Intent, "按 stage 结果采集素材。") + duration + "；资产：" + firstNonEmpty(assets, "trace")
}
