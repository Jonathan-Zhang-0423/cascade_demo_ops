package orchestrator

import (
	"fmt"
	"log"
	"strings"

	"cascade-demoops/backend/internal/model"
)

// ValidationDiagnostics derives human-readable recommendations, evidence
// citations, and runtime repair hints from the structured validation results
// produced by the Server-Side Stage Validation Agent.
//
// It is pure deterministic logic over in-memory model structs: it performs no
// LLM, network, or browser-sidecar calls. Runtime repair patches it describes
// are restricted by the safety boundary to selector / wait-until /
// wait-condition / capture-timing fields only — never business logic, routes,
// or graph structure.
type ValidationDiagnostics struct {
	cfg *model.ValidationConfig
}

// NewValidationDiagnostics constructs a ValidationDiagnostics. A nil config is
// replaced with a defensive default that materializes the documented
// thresholds (PassRateThreshold 0.9, ConfidenceThreshold 0.5).
func NewValidationDiagnostics(cfg *model.ValidationConfig) *ValidationDiagnostics {
	if cfg == nil {
		cfg = diagDefaultValidationConfig()
		log.Printf("validation_diagnostics: nil config, using defaults pass_rate_threshold=%.2f confidence_threshold=%.2f", cfg.PassRateThreshold, cfg.ConfidenceThreshold)
	}
	return &ValidationDiagnostics{cfg: cfg}
}

// GenerateRecommendation returns a concise (<~6 sentences) human-readable
// recommendation keyed on the global feedback type.
func (vd *ValidationDiagnostics) GenerateRecommendation(stageFeedbacks []model.StageFeedback, globalFeedbackType string) string {
	switch globalFeedbackType {
	case model.ValidationFeedbackReunderstandingRequired:
		return vd.reunderstandingRecommendation(stageFeedbacks)
	case model.ValidationFeedbackFineTune:
		return vd.fineTuneRecommendation(stageFeedbacks)
	case model.ValidationFeedbackContinue:
		return "所有阶段校验通过，执行可继续。"
	default:
		passed, repairable, blocked := 0, 0, 0
		for i := range stageFeedbacks {
			switch stageFeedbacks[i].FeedbackType {
			case model.ValidationFeedbackContinue:
				passed++
			case model.ValidationFeedbackFineTune:
				repairable++
			case model.ValidationFeedbackReunderstandingRequired:
				blocked++
			}
		}
		return fmt.Sprintf(
			"未知的全局反馈类型 %q。阶段汇总：通过 %d，待修复 %d，阻塞 %d。请确认反馈类型后决定后续动作。",
			globalFeedbackType, passed, repairable, blocked,
		)
	}
}

func (vd *ValidationDiagnostics) reunderstandingRecommendation(stageFeedbacks []model.StageFeedback) string {
	type blockedNode struct {
		nodeID string
		reason string
	}
	var nodes []blockedNode
	for i := range stageFeedbacks {
		sf := &stageFeedbacks[i]
		if !sf.Blocked || sf.NodeID == "" {
			continue
		}
		nodes = append(nodes, blockedNode{nodeID: sf.NodeID, reason: sf.BlockReason})
	}

	var b strings.Builder
	b.WriteString("检测到阻塞性问题，建议回滚到理解阶段重新生成执行图。")
	if len(nodes) == 0 {
		b.WriteString("未提供具体的阻塞节点，请复查各阶段反馈中的 blocked 标记与 block_reason。")
		return b.String()
	}
	b.WriteString("阻塞节点：")
	for i, n := range nodes {
		if i > 0 {
			b.WriteString("；")
		}
		b.WriteString(n.nodeID)
		if n.reason != "" {
			b.WriteString("（")
			b.WriteString(diagTruncate(n.reason, 120))
			b.WriteString("）")
		}
	}
	b.WriteString("。这些问题无法通过运行时修复补丁解决，需要重新理解业务与页面结构后再生成执行图。")
	return b.String()
}

func (vd *ValidationDiagnostics) fineTuneRecommendation(stageFeedbacks []model.StageFeedback) string {
	type repairableStage struct {
		nodeID string
		kinds  []string
	}
	var stages []repairableStage
	for i := range stageFeedbacks {
		sf := &stageFeedbacks[i]
		if !sf.RequiresRepair || sf.NodeID == "" {
			continue
		}
		stages = append(stages, repairableStage{nodeID: sf.NodeID, kinds: vd.repairKindsForStage(sf)})
	}

	var b strings.Builder
	b.WriteString("检测到可修复的轻微问题，建议应用运行时修复补丁后继续执行。")
	if len(stages) == 0 {
		b.WriteString("未提供具体可修复节点，请复查各阶段反馈中的 requires_repair 标记。")
		return b.String()
	}
	b.WriteString("可修复节点：")
	for i, s := range stages {
		if i > 0 {
			b.WriteString("；")
		}
		b.WriteString(s.nodeID)
		if len(s.kinds) > 0 {
			b.WriteString("（建议修复类型：")
			b.WriteString(strings.Join(s.kinds, "、"))
			b.WriteString("）")
		}
	}
	b.WriteString("。修复仅涉及 selector / wait / 时序字段，不会改动业务逻辑、路由或图结构。")
	return b.String()
}

// repairKindsForStage collects distinct repair Kind labels from a stage's
// applied repairs and per-result repair details.
func (vd *ValidationDiagnostics) repairKindsForStage(sf *model.StageFeedback) []string {
	seen := make(map[string]bool)
	var kinds []string
	add := func(k string) {
		if k == "" || seen[k] {
			return
		}
		seen[k] = true
		kinds = append(kinds, k)
	}
	for i := range sf.RepairsApplied {
		add(sf.RepairsApplied[i].Kind)
	}
	for i := range sf.ValidationResults {
		if sf.ValidationResults[i].RepairDetails != nil {
			add(sf.ValidationResults[i].RepairDetails.Kind)
		}
	}
	return kinds
}

// CollectEvidenceReferences gathers every EvidenceRef cited by the stage
// feedbacks (both StageFeedback.EvidenceRefs and each ValidationResult's
// EvidenceRefs) plus artifact-backed evidence from the validation context
// (ExecutionTrace.Artifacts and each StepResult.Artifacts, converted to
// EvidenceRef via the documented ArtifactID link). Results are deduplicated by
// a stable key and the returned slice is always non-nil.
func (vd *ValidationDiagnostics) CollectEvidenceReferences(stageFeedbacks []model.StageFeedback, validationContext *model.ValidationContext) []model.EvidenceRef {
	out := make([]model.EvidenceRef, 0)
	seen := make(map[string]bool)
	add := func(ref model.EvidenceRef) {
		key := diagEvidenceKey(ref)
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, ref)
	}

	// 1. EvidenceRefs cited directly by stage feedbacks and their results.
	for i := range stageFeedbacks {
		sf := &stageFeedbacks[i]
		for _, ref := range sf.EvidenceRefs {
			add(ref)
		}
		for j := range sf.ValidationResults {
			for _, ref := range sf.ValidationResults[j].EvidenceRefs {
				add(ref)
			}
		}
	}

	// 2. Artifact-backed evidence from the execution context. ArtifactRef and
	//    EvidenceRef are not directly Go-convertible, but the type map
	//    documents EvidenceRef.ArtifactID as the artifact link and EvidenceKind
	//    is `type EvidenceKind string`, so a manual field mapping is the
	//    documented conversion path. Artifacts without a stable ID are skipped.
	if validationContext != nil {
		if validationContext.ExecutionTrace != nil {
			for _, art := range validationContext.ExecutionTrace.Artifacts {
				if ref, ok := diagArtifactToEvidenceRef(art); ok {
					add(ref)
				}
			}
			for s := range validationContext.ExecutionTrace.StepResults {
				for _, art := range validationContext.ExecutionTrace.StepResults[s].Artifacts {
					if ref, ok := diagArtifactToEvidenceRef(art); ok {
						add(ref)
					}
				}
			}
		}
		for s := range validationContext.StepResults {
			for _, art := range validationContext.StepResults[s].Artifacts {
				if ref, ok := diagArtifactToEvidenceRef(art); ok {
					add(ref)
				}
			}
		}
	}

	return out
}

// GenerateRepairHint converts validation issues into ScriptRepairHint records.
// Per the safety boundary, only issues whose inferred kind is runtime-repairable
// (selector / wait_strategy / timing) yield hints; route / state / generic
// issues are classified but skipped because they would require touching business
// logic, routes, or graph structure. For selector issues, SelectorCandidates are
// populated from the workflow graph node's ActionSpec.Target.SelectorAlternatives
// when available. The returned slice is always non-nil.
func (vd *ValidationDiagnostics) GenerateRepairHints(criticalIssues []model.ValidationResult, majorIssues []model.ValidationResult, validationContext *model.ValidationContext) []model.ScriptRepairHint {
	hints := make([]model.ScriptRepairHint, 0)
	var graph *model.DemoWorkflowGraph
	if validationContext != nil {
		graph = validationContext.WorkflowGraph
	}

	emit := func(issue *model.ValidationResult) {
		if issue == nil || issue.Type == model.ValidationResultTypePassed {
			return
		}
		kind, repairable := diagInferRepairKind(*issue)
		if !repairable {
			return
		}
		hint := model.ScriptRepairHint{
			Kind:            kind,
			Summary:         diagIssueSummary(issue),
			NodeID:          issue.NodeID,
			SuggestedAction: diagSuggestedActionForKind(kind),
			Confidence:      diagRepairConfidence(issue.Confidence, vd.defaultConfidence()),
			EvidenceRefs:    append([]model.EvidenceRef(nil), issue.EvidenceRefs...),
		}
		if kind == "selector" {
			hint.SelectorCandidates = diagSelectorCandidatesForNode(graph, issue.NodeID)
		}
		hints = append(hints, hint)
	}

	for i := range criticalIssues {
		emit(&criticalIssues[i])
	}
	for i := range majorIssues {
		emit(&majorIssues[i])
	}
	return hints
}

// defaultConfidence is the fallback repair confidence for issues that do not
// carry their own. It uses the configured ConfidenceThreshold as a floor so
// that repairs are only proposed with at least the threshold confidence.
func (vd *ValidationDiagnostics) defaultConfidence() float64 {
	if vd == nil || vd.cfg == nil {
		return 0.6
	}
	if vd.cfg.ConfidenceThreshold > 0.6 {
		return vd.cfg.ConfidenceThreshold
	}
	return 0.6
}

// diagDefaultValidationConfig returns a defensive default config materializing
// the documented thresholds (validation.go declares no defaults itself).
func diagDefaultValidationConfig() *model.ValidationConfig {
	return &model.ValidationConfig{
		PreExecutionEnabled:       true,
		RealTimeBatchEnabled:      true,
		PostExecutionBatchEnabled: true,
		PlaybackValidationEnabled: true,
		PassRateThreshold:         0.9,
		ConfidenceThreshold:       0.5,
		CriticalIssueThreshold:    0,
		EnableRuntimeRepair:       true,
		MaxRepairAttemptsPerStage: 1,
	}
}

// diagNodeByID is the canonical linear-scan node lookup (mirrors
// scriptPackagerGraphNodeByID in internal/agents/script_packager.go). There is
// no node map or lookup method on *DemoWorkflowGraph.
func diagNodeByID(graph *model.DemoWorkflowGraph, nodeID string) *model.GraphNode {
	if graph == nil || nodeID == "" {
		return nil
	}
	for _, node := range graph.Nodes {
		if node != nil && node.ID == nodeID {
			return node
		}
	}
	return nil
}

// diagSelectorCandidatesForNode returns a defensive copy of the selector
// alternatives declared on the node's structured action target, following the
// graphNodeSelector precedence's nested source. Returns nil when the node or
// its alternatives are unavailable.
func diagSelectorCandidatesForNode(graph *model.DemoWorkflowGraph, nodeID string) []model.SelectorCandidate {
	node := diagNodeByID(graph, nodeID)
	if node == nil || node.ActionSpec == nil {
		return nil
	}
	alts := node.ActionSpec.Target.SelectorAlternatives
	if len(alts) == 0 {
		return nil
	}
	out := make([]model.SelectorCandidate, len(alts))
	copy(out, alts)
	return out
}

// diagInferRepairKind classifies an issue into a repair kind and reports
// whether that kind is runtime-repairable. Only selector / wait_strategy /
// timing are repairable; route / state / generic are not, per the safety
// boundary.
func diagInferRepairKind(issue model.ValidationResult) (string, bool) {
	hay := strings.ToLower(issue.Title + " " + issue.Description + " " + issue.ValidationRule)
	switch {
	case diagContainsAny(hay, "selector", "css", "xpath", "testid", "test_id", "role", "locator", "元素定位"):
		return "selector", true
	case diagContainsAny(hay, "wait_until", "wait until", "wait_strategy", "wait condition", "wait_condition", "timeout", "polling", "等待"):
		return "wait_strategy", true
	case diagContainsAny(hay, "timing", "capture", "delay", "stabiliz", "pre_capture", "hold_after", "时序", "采集"):
		return "timing", true
	case diagContainsAny(hay, "route", "url", "page", "navigation", "redirect", "路由"):
		return "route", false
	case diagContainsAny(hay, "state", "assertion", "expected status", "success state", "dom", "visibility", "visible", "enabled", "状态"):
		return "state", false
	default:
		return "generic", false
	}
}

// diagContainsAny reports whether haystack contains any of the needles
// (case-sensitive; callers lower-case haystack and needles as needed).
func diagContainsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if n != "" && strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}

// diagIssueSummary derives a concise hint summary from an issue's text fields.
func diagIssueSummary(issue *model.ValidationResult) string {
	if issue.Title != "" {
		return diagTruncate(issue.Title, 200)
	}
	if issue.Description != "" {
		return diagTruncate(issue.Description, 200)
	}
	if issue.NodeID != "" {
		return fmt.Sprintf("节点 %s 存在需修复的校验问题", issue.NodeID)
	}
	return "存在需修复的校验问题"
}

// diagSuggestedActionForKind returns a deterministic suggested action for each
// runtime-repairable kind.
func diagSuggestedActionForKind(kind string) string {
	switch kind {
	case "selector":
		return "替换不稳定 selector：优先使用 selector_candidates 中稳定性更高的候选项，或补充 testid/role 维度的备选 selector。"
	case "wait_strategy":
		return "调整等待策略：适当延长 timeout，或改用更精确的 wait_until 条件（如网络空闲、目标元素可见）。"
	case "timing":
		return "调整采集时序：增加 pre_capture 等待或 stabilization 稳定窗口后再截图/录制。"
	default:
		return "请人工确认后修复。"
	}
}

// diagRepairConfidence normalizes an issue-level confidence into a hint
// confidence, falling back to defaultConfidence when unset and clamping to the
// required <=0.9 ceiling.
func diagRepairConfidence(issueConf float64, defaultConf float64) float64 {
	c := issueConf
	if c <= 0 {
		c = defaultConf
	}
	if c > 0.9 {
		c = 0.9
	}
	if c < 0 {
		c = 0
	}
	return c
}

// diagEvidenceKey returns a stable dedup key for an EvidenceRef. Prefers the
// ref ID; falls back to a composite of its other fields so anonymous refs still
// dedupe. Returns "" when the ref carries no identity at all.
func diagEvidenceKey(ref model.EvidenceRef) string {
	if ref.ID != "" {
		return "ref:" + ref.ID
	}
	parts := []string{string(ref.Kind), ref.Summary, ref.FieldPath, ref.ArtifactID}
	joined := strings.Join(parts, "|")
	if strings.Trim(joined, "|") == "" {
		return ""
	}
	return "ref:" + joined
}

// diagArtifactToEvidenceRef performs the documented manual conversion of an
// ArtifactRef into an EvidenceRef by mapping the artifact ID onto
// EvidenceRef.ArtifactID and the artifact Kind (string) onto EvidenceKind
// (defined as `type EvidenceKind string`). Returns ok=false when the artifact
// lacks the stable ID required for deduplication.
func diagArtifactToEvidenceRef(artifact model.ArtifactRef) (model.EvidenceRef, bool) {
	if artifact.ID == "" {
		return model.EvidenceRef{}, false
	}
	summary := artifact.Label
	if summary == "" {
		summary = artifact.Kind
	}
	return model.EvidenceRef{
		ID:         "artifact:" + artifact.ID,
		Kind:       model.EvidenceKind(artifact.Kind),
		Summary:    summary,
		ArtifactID: artifact.ID,
	}, true
}

// diagTruncate caps s to maxRunes runes, appending an ellipsis when truncated.
func diagTruncate(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "..."
}
