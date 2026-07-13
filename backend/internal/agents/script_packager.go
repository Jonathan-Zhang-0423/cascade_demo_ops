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
			BlockingReasons:                    scriptBlockingReasons(project, report),
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
	bundle, err := buildExecutableScriptBundle(project, report, graph, doc, markdown, now)
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
	graph *model.DemoWorkflowGraph,
	doc *model.ExecutionScriptDocument,
	markdown string,
	now time.Time,
) (*model.ExecutableRecordingScriptBundle, error) {
	source, err := NewScriptCodeGenerator().Generate(doc)
	if err != nil {
		return nil, err
	}
	planHash, err := doc.ComputeScriptHash()
	if err != nil {
		return nil, err
	}
	scriptHash := hashString(source)
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
			Language:            "typescript",
			Runtime:             "playwright-restricted-sandbox",
			EntryFunction:       "runCascadeRecording",
			Generator:           scriptCodeGeneratorName,
			GeneratorVersion:    scriptCodeGeneratorVersion,
			DependencyAllowlist: []string{},
			ContextAPIs:         []string{"ctx.page", "ctx.secrets", "ctx.capture", "ctx.assert", "ctx.log"},
			StepNodeIDs:         scriptStepNodeIDs(doc),
		},
		PlanJSON: doc,
		PlaywrightScript: model.ExecutableScriptSource{
			InlineSource: source,
			Artifact: &model.ArtifactRef{
				ID:        "artifact_" + doc.ID + "_playwright_ts",
				Kind:      "playwright_recording_script",
				URI:       "cascade://projects/" + project.ID + "/scripts/" + doc.ID + ".ts",
				MimeType:  "text/typescript",
				Label:     doc.Title + " 可执行脚本",
				SHA256:    scriptHash,
				SizeBytes: int64(len([]byte(source))),
				CreatedAt: now,
				Sensitive: false,
			},
			MimeType:  "text/typescript",
			SHA256:    scriptHash,
			SizeBytes: int64(len([]byte(source))),
			Encrypted: false,
		},
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
			PlanHashSHA256:       planHash,
			ScriptHashSHA256:     scriptHash,
			MarkdownHashSHA256:   markdownHash,
			GraphHashSHA256:      doc.Reproducibility.GraphHashSHA256,
			SourceSnapshotDigest: reportSourceDigest(report),
			GeneratorVersion:     scriptCodeGeneratorVersion,
			DeterministicSeed:    doc.Reproducibility.DeterministicSeed,
			InputFingerprints:    reportInputFingerprints(report),
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

type approvalMarkdownLLMOutput struct {
	Markdown string `json:"markdown"`
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
		capture := captureSpecForNode(node)
		narrative := narrativeForNode(node)
		action := scriptActionForNode(node)
		target := scriptPageTargetForNode(node)
		validations := append([]model.ValidationSpec{}, node.Validations...)
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
			Timing:          model.NodeTimingHint{NodeID: node.ID, DurationMS: durationMS, HoldAfterMS: 500},
			Narrative:       narrative,
			EvidenceRefs:    node.EvidenceRefs,
			Blocking:        isBlockingStep(node),
		})
		elapsedMS += durationMS
		_ = elapsedMS
	}
	return steps
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
		timingHints = append(timingHints, model.NodeTimingHint{NodeID: node.ID, DurationMS: durationMS, HoldAfterMS: 500})
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
	for _, validation := range node.Validations {
		if validation.Required && validation.Severity == "blocking" {
			return true
		}
	}
	return node.Type == model.GraphNodeTypeStart || node.Type == model.GraphNodeTypeAction || node.Type == model.GraphNodeTypeCapture
}

func scriptSafetyPolicy(project *model.ProjectContext, graph *model.DemoWorkflowGraph) model.ScriptSafetyPolicy {
	piiHandling := "mask_in_artifacts"
	if project.SecurityPolicy != nil {
		piiHandling = firstNonEmpty(project.SecurityPolicy.PIIHandling, piiHandling)
	}
	return model.ScriptSafetyPolicy{
		AllowedDomains: allowedDomains(project, graph),
		ForbiddenPages: append([]string{}, project.ForbiddenPages...),
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

func scriptBlockingReasons(project *model.ProjectContext, report *model.MultimodalUnderstandingReport) []string {
	reasons := []string{"上传前必须完成人工审批。", "需要确认仅上传代码结构摘要，不上传完整源码。", "需要复核打码选择器和禁止访问数据。"}
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
