package model

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const PackageConfidenceAlgorithmVersion = "demoops.package_confidence.v1"

type PackageReadiness string

const (
	PackageReadinessBlocked        PackageReadiness = "blocked"
	PackageReadinessReviewRequired PackageReadiness = "review_required"
	PackageReadinessReady          PackageReadiness = "ready"
)

type PackageStageConfidenceAssessment struct {
	NodeID                string            `json:"node_id"`
	StageKind             BusinessStageKind `json:"stage_kind,omitempty"`
	OverallScore          float64           `json:"overall_score"`
	BusinessIntentScore   float64           `json:"business_intent_score"`
	ResultValidationScore float64           `json:"result_validation_score"`
	EvidenceQualityScore  float64           `json:"evidence_quality_score"`
	TargetSelectorScore   float64           `json:"target_selector_score"`
	SafetySourceScore     float64           `json:"safety_source_score"`
	BlockingReasons       []string          `json:"blocking_reasons,omitempty"`
	Warnings              []string          `json:"warnings,omitempty"`
}

type PackageConfidenceSummary struct {
	OverallScore                    float64                            `json:"overall_score"`
	Readiness                       PackageReadiness                   `json:"readiness"`
	Stages                          []PackageStageConfidenceAssessment `json:"stages"`
	RequirementCoverage             float64                            `json:"requirement_coverage"`
	DeterministicValidationCoverage float64                            `json:"deterministic_validation_coverage"`
	RuntimePageEvidenceCoverage     float64                            `json:"runtime_page_evidence_coverage"`
	SelectorQuality                 float64                            `json:"selector_quality"`
	SourceBindingStatus             ProductSourceBindingStatus         `json:"source_binding_status,omitempty"`
	SourceBindingMode               ProductSourceEffectiveMode         `json:"source_binding_mode,omitempty"`
	BlockingReasons                 []string                           `json:"blocking_reasons,omitempty"`
	Warnings                        []string                           `json:"warnings,omitempty"`
	AlgorithmVersion                string                             `json:"algorithm_version"`
	AssessmentHash                  string                             `json:"assessment_hash"`
}

// AssessClientExecutionPackage derives confidence exclusively from the compact
// package that will be approved and uploaded; model self-reported scores are ignored.
func AssessClientExecutionPackage(pkg *ClientExecutionPackage) (*PackageConfidenceSummary, error) {
	if pkg == nil || pkg.ExecutableScriptBundle == nil || pkg.ExecutableScriptBundle.PlanJSON == nil {
		return nil, fmt.Errorf("execution package is missing plan_json")
	}
	bundle := pkg.ExecutableScriptBundle
	approved := map[string]StageApprovalStage{}
	if bundle.StageApprovalPlan != nil {
		for _, stage := range bundle.StageApprovalPlan.Stages {
			approved[stage.NodeID] = stage
		}
	}
	outlines := map[string]BrowserAgentOutlineStage{}
	if bundle.ScriptOutline != nil {
		for _, stage := range bundle.ScriptOutline.Stages {
			outlines[stage.NodeID] = stage
		}
	}
	summary := &PackageConfidenceSummary{AlgorithmVersion: PackageConfidenceAlgorithmVersion, Readiness: PackageReadinessReady}
	if pkg.SourceBindingSummary != nil {
		summary.SourceBindingStatus = pkg.SourceBindingSummary.Status
		summary.SourceBindingMode = pkg.SourceBindingSummary.EffectiveMode
	}
	requiredRequirements, coveredRequirements := 0, 0
	if pkg.WorkflowGraph != nil {
		for _, requirement := range pkg.WorkflowGraph.Requirements {
			if !requirement.Required {
				continue
			}
			requiredRequirements++
			if len(requirement.NodeRefs) > 0 {
				coveredRequirements++
			}
		}
	}
	if requiredRequirements == 0 {
		summary.RequirementCoverage = 1
	} else {
		summary.RequirementCoverage = ratio(coveredRequirements, requiredRequirements)
	}
	validationNeeded, validationCovered, runtimeNeeded, runtimeCovered := 0, 0, 0, 0
	selectorTotal := 0.0
	for _, step := range bundle.PlanJSON.Steps {
		stage := approved[step.NodeID]
		outline := outlines[step.NodeID]
		a := PackageStageConfidenceAssessment{NodeID: step.NodeID, StageKind: step.StageKind}
		if strings.TrimSpace(stage.Objective) != "" && strings.TrimSpace(stage.BusinessIntent) != "" {
			a.BusinessIntentScore = 1
		} else if strings.TrimSpace(stage.Objective) != "" || strings.TrimSpace(step.BusinessValue) != "" {
			a.BusinessIntentScore = .6
		}
		needsValidation := stepRequiresBrowserAgentValidation(step)
		if needsValidation {
			validationNeeded++
			if stepHasDeterministicBrowserAgentValidation(step) {
				a.ResultValidationScore = 1
				validationCovered++
			} else {
				a.BlockingReasons = append(a.BlockingReasons, "缺少必填确定性结果验证")
			}
		} else {
			a.ResultValidationScore = 1
		}
		refs := stageConfidenceEvidenceRefs(step, stage, outline)
		a.EvidenceQualityScore = bestEvidenceQuality(refs, pkg.SourceBindingSummary)
		if runtimeEvidenceStage(step.StageKind) {
			runtimeNeeded++
			if confidenceRefsHaveRuntimePageEvidence(refs) {
				runtimeCovered++
			} else {
				a.BlockingReasons = append(a.BlockingReasons, "运行时页面状态缺少真实页面证据")
			}
		}
		a.TargetSelectorScore = confidenceTargetScore(step, stage, outline)
		selectorTotal += a.TargetSelectorScore
		a.SafetySourceScore = confidenceSafetyScore(pkg, step, stage)
		if a.BusinessIntentScore < .6 {
			a.BlockingReasons = append(a.BlockingReasons, "业务意图未绑定")
		}
		if a.SafetySourceScore < 1 {
			a.BlockingReasons = append(a.BlockingReasons, "安全域、来源或非破坏性约束不完整")
		}
		a.OverallScore = roundConfidence(.25*a.BusinessIntentScore + .25*a.ResultValidationScore + .20*a.EvidenceQualityScore + .20*a.TargetSelectorScore + .10*a.SafetySourceScore)
		if a.OverallScore < .65 {
			a.BlockingReasons = append(a.BlockingReasons, "stage 确信度低于 0.65")
		}
		if len(a.BlockingReasons) == 0 && (a.OverallScore < .8 || a.TargetSelectorScore < .8) {
			a.Warnings = append(a.Warnings, "需要人工复核或由 BrowserAgent 受限补全技术细节")
		}
		summary.Stages = append(summary.Stages, a)
	}
	if len(summary.Stages) == 0 {
		summary.BlockingReasons = append(summary.BlockingReasons, "执行包没有 stage")
		summary.OverallScore = 0
	} else {
		summary.OverallScore = 1
		for _, stage := range summary.Stages {
			if stage.OverallScore < summary.OverallScore {
				summary.OverallScore = stage.OverallScore
			}
			for _, reason := range stage.BlockingReasons {
				summary.BlockingReasons = append(summary.BlockingReasons, stage.NodeID+": "+reason)
			}
			for _, warning := range stage.Warnings {
				summary.Warnings = append(summary.Warnings, stage.NodeID+": "+warning)
			}
		}
	}
	if summary.RequirementCoverage < 1 {
		summary.BlockingReasons = append(summary.BlockingReasons, "关键需求未完整映射到 stage 和证据")
	}
	if validationNeeded == 0 {
		summary.DeterministicValidationCoverage = 1
	} else {
		summary.DeterministicValidationCoverage = ratio(validationCovered, validationNeeded)
	}
	if runtimeNeeded == 0 {
		summary.RuntimePageEvidenceCoverage = 1
	} else {
		summary.RuntimePageEvidenceCoverage = ratio(runtimeCovered, runtimeNeeded)
	}
	if len(summary.Stages) > 0 {
		summary.SelectorQuality = roundConfidence(selectorTotal / float64(len(summary.Stages)))
	}
	if pkg.SourceBindingSummary != nil && (pkg.SourceBindingSummary.EffectiveMode == ProductSourceModeBlocked || pkg.SourceBindingSummary.Status == ProductSourceBindingMismatched) {
		summary.BlockingReasons = append(summary.BlockingReasons, "网页与源码来源不匹配")
	}
	if size := confidencePayloadSize(pkg); size > 96*1024 {
		summary.Warnings = append(summary.Warnings, fmt.Sprintf("执行包超过 96 KiB 软预算：%d bytes", size))
	}
	if len(summary.BlockingReasons) > 0 || summary.OverallScore < .65 {
		summary.Readiness = PackageReadinessBlocked
	} else if summary.OverallScore < .8 || len(summary.Warnings) > 0 {
		summary.Readiness = PackageReadinessReviewRequired
	}
	sort.Strings(summary.BlockingReasons)
	sort.Strings(summary.Warnings)
	summary.AssessmentHash = ""
	hash, err := DigestCanonicalJSON(summary)
	if err != nil {
		return nil, err
	}
	summary.AssessmentHash = hash
	return summary, nil
}

func ValidatePackageConfidenceSummary(pkg *ClientExecutionPackage) error {
	if pkg == nil || pkg.ConfidenceSummary == nil || pkg.ConfidenceSummary.AlgorithmVersion == "" {
		return nil
	}
	actual, err := AssessClientExecutionPackage(pkg)
	if err != nil {
		return err
	}
	configured := pkg.ConfidenceSummary
	if configured.AlgorithmVersion != PackageConfidenceAlgorithmVersion || configured.AssessmentHash != actual.AssessmentHash || configured.Readiness != actual.Readiness || configured.OverallScore != actual.OverallScore {
		return fmt.Errorf("package confidence summary does not match deterministic intake assessment")
	}
	if actual.Readiness == PackageReadinessBlocked {
		return fmt.Errorf("package confidence assessment is blocked")
	}
	return nil
}

func ComputePackageApprovalSubjectDigest(pkg ClientExecutionPackage) (string, error) {
	safety := PackageSafetyReport{
		UploadMode: pkg.SafetyReport.UploadMode, ForbiddenPages: pkg.SafetyReport.ForbiddenPages,
		ForbiddenData: pkg.SafetyReport.ForbiddenData, RedactionSelectors: pkg.SafetyReport.RedactionSelectors,
		PIIHandling: pkg.SafetyReport.PIIHandling, DataResidency: pkg.SafetyReport.DataResidency,
		PolicyFindings: pkg.SafetyReport.PolicyFindings,
	}
	return DigestCanonicalJSON(struct {
		ProducerInstallationID string                           `json:"producer_installation_id,omitempty"`
		Project                ProjectContextSummary            `json:"project"`
		Source                 *SourceBindingSummary            `json:"source,omitempty"`
		Run                    RecordingRunSpec                 `json:"run"`
		Bundle                 *ExecutableRecordingScriptBundle `json:"bundle"`
		Safety                 PackageSafetyReport              `json:"safety"`
		Confidence             *PackageConfidenceSummary        `json:"confidence"`
	}{pkg.ProducerInstallationID, pkg.ProjectContextSummary, pkg.SourceBindingSummary, pkg.RecordingRunSpec, pkg.ExecutableScriptBundle, safety, pkg.ConfidenceSummary})
}

func ComputeApprovalSubjectDigestsSHA256(pkg ClientExecutionPackage) (ApprovalSubjectDigestsSHA256, error) {
	bundle := pkg.ExecutableScriptBundle
	if bundle == nil || bundle.PlanJSON == nil || bundle.StageApprovalPlan == nil || bundle.ScriptOutline == nil || bundle.BrowserAgentContract == nil || bundle.AgentPromptPolicy == nil {
		return ApprovalSubjectDigestsSHA256{}, fmt.Errorf("approval subject objects are incomplete")
	}
	plan, err := bundle.PlanJSON.ComputeScriptHash()
	if err != nil {
		return ApprovalSubjectDigestsSHA256{}, err
	}
	stagePlan, err := DigestCanonicalJSON(bundle.StageApprovalPlan)
	if err != nil {
		return ApprovalSubjectDigestsSHA256{}, err
	}
	outline, err := DigestCanonicalJSON(bundle.ScriptOutline)
	if err != nil {
		return ApprovalSubjectDigestsSHA256{}, err
	}
	contract, err := DigestCanonicalJSON(bundle.BrowserAgentContract)
	if err != nil {
		return ApprovalSubjectDigestsSHA256{}, err
	}
	prompt, err := DigestCanonicalJSON(bundle.AgentPromptPolicy)
	if err != nil {
		return ApprovalSubjectDigestsSHA256{}, err
	}
	markdown := strings.TrimSpace(bundle.ApprovalMarkdown.SHA256)
	if markdown == "" {
		return ApprovalSubjectDigestsSHA256{}, fmt.Errorf("approval markdown digest is required")
	}
	return ApprovalSubjectDigestsSHA256{
		PlanJSON: plan, StageApprovalPlan: stagePlan, ScriptOutline: outline,
		BrowserAgentContract: contract, AgentPromptPolicy: prompt, ApprovalMarkdown: markdown,
	}, nil
}

func stageConfidenceEvidenceRefs(step ScriptStep, stage StageApprovalStage, outline BrowserAgentOutlineStage) []EvidenceRef {
	refs := append([]EvidenceRef{}, step.EvidenceRefs...)
	refs = append(refs, step.Action.Target.EvidenceRefs...)
	refs = append(refs, stage.EvidenceRefs...)
	refs = append(refs, stage.Interaction.EvidenceRefs...)
	refs = append(refs, stage.Interaction.Target.EvidenceRefs...)
	refs = append(refs, outline.EvidenceRefs...)
	if stage.TargetContract != nil {
		refs = append(refs, stage.TargetContract.EvidenceRefs...)
	}
	for _, c := range outline.Components {
		refs = append(refs, c.EvidenceRefs...)
	}
	return refs
}

func bestEvidenceQuality(refs []EvidenceRef, binding *SourceBindingSummary) float64 {
	best := 0.0
	for _, ref := range refs {
		quality := 0.0
		switch ref.Kind {
		case EvidenceKindBrowserTrace:
			quality = 1
		case EvidenceKindBrowserScan:
			if !strings.Contains(strings.ToLower(ref.ID+" "+ref.Summary), "url input") && !strings.Contains(ref.Summary, "产品 URL 输入") {
				quality = .95
			} else {
				quality = .3
			}
		case EvidenceKindWebScreenshot, EvidenceKindScreenshotOCR, EvidenceKindVisionFinding:
			quality = .82
		case EvidenceKindSourceCode, EvidenceKindCodeSnapshot, EvidenceKindRepoSnapshot:
			if binding != nil && binding.Status == ProductSourceBindingMatched && binding.EffectiveMode == ProductSourceModeMixed {
				quality = .7
			}
		case EvidenceKindRequirementDoc:
			quality = .6
		case EvidenceKindUserInput:
			quality = .45
		}
		if ref.Confidence > 0 {
			quality *= math.Min(1, ref.Confidence)
		}
		if quality > best {
			best = quality
		}
	}
	return roundConfidence(best)
}

func confidenceTargetScore(step ScriptStep, stage StageApprovalStage, outline BrowserAgentOutlineStage) float64 {
	if step.Action.Type == GraphActionNavigate {
		if step.Action.Target.URL != "" {
			return 1
		}
		return .4
	}
	targets := []ActionTarget{step.Action.Target, stage.Interaction.Target}
	for _, interaction := range outline.Interactions {
		targets = append(targets, interaction.Target)
	}
	best := 0.0
	for _, target := range targets {
		score := 0.0
		switch {
		case target.TestID != "":
			score = 1
		case target.Role != "" && (target.Text != "" || target.Label != ""):
			score = .9
		case target.Label != "":
			score = .86
		case target.Text != "":
			score = .72
		case target.Selector != "":
			score = selectorKindScore("css", target.Selector)
		}
		for _, candidate := range target.SelectorAlternatives {
			candidateScore := selectorKindScore(candidate.Kind, candidate.Value)
			if candidate.Confidence > 0 {
				candidateScore *= math.Min(1, candidate.Confidence)
			}
			if candidate.StabilityScore > 0 {
				candidateScore = (candidateScore + math.Min(1, candidate.StabilityScore)) / 2
			}
			if candidateScore > score {
				score = candidateScore
			}
		}
		if score > best {
			best = score
		}
	}
	if best == 0 && stage.TargetContract != nil && stage.TargetContract.SemanticID != "" {
		best = .65
	}
	return roundConfidence(best)
}

func selectorKindScore(kind, value string) float64 {
	text := strings.ToLower(kind + " " + value)
	switch {
	case strings.Contains(text, "testid") || strings.Contains(text, "data-testid"):
		return 1
	case strings.Contains(text, "role"):
		return .9
	case strings.Contains(text, "label"):
		return .86
	case strings.Contains(text, "aria-"):
		return .82
	case strings.Contains(text, "text"):
		return .72
	default:
		return .58
	}
}
func confidenceSafetyScore(pkg *ClientExecutionPackage, step ScriptStep, stage StageApprovalStage) float64 {
	if len(pkg.RecordingRunSpec.AllowedDomains) == 0 || stage.TargetContract == nil || stage.TargetContract.SemanticID == "" {
		return 0
	}
	if step.Action.Type != GraphActionNavigate && step.Action.Type != GraphActionInspect && !step.NonDestructive {
		return 0
	}
	if pkg.SourceBindingSummary != nil && pkg.SourceBindingSummary.EffectiveMode == ProductSourceModeBlocked {
		return 0
	}
	return 1
}
func runtimeEvidenceStage(kind BusinessStageKind) bool {
	return kind == BusinessStageKindSessionSetup || kind == BusinessStageKindObserveProgress || kind == BusinessStageKindFinalObserve
}
func confidenceRefsHaveRuntimePageEvidence(refs []EvidenceRef) bool {
	for _, ref := range refs {
		if ref.Kind == EvidenceKindBrowserTrace || ref.Kind == EvidenceKindWebScreenshot || ref.Kind == EvidenceKindScreenshotOCR || ref.Kind == EvidenceKindVisionFinding {
			return true
		}
		if ref.Kind == EvidenceKindBrowserScan && !strings.Contains(strings.ToLower(ref.ID+" "+ref.Summary), "url input") && !strings.Contains(ref.Summary, "产品 URL 输入") {
			return true
		}
	}
	return false
}
func ratio(a, b int) float64 {
	if b == 0 {
		return 1
	}
	return roundConfidence(float64(a) / float64(b))
}
func roundConfidence(v float64) float64 { return math.Round(math.Max(0, math.Min(1, v))*1000) / 1000 }
func confidencePayloadSize(pkg *ClientExecutionPackage) int {
	copy := *pkg
	copy.ConfidenceSummary = nil
	copy.ApprovedAt = time.Time{}
	copy.SafetyReport.AllowedToUpload = false
	copy.SafetyReport.HumanApproval = UserApprovalRecord{}
	data, _ := json.Marshal(copy)
	return len(data)
}
