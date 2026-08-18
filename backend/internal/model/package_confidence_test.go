package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestPackageConfidenceUsesWeakestRequiredStage(t *testing.T) {
	pkg := confidenceFixture(t)
	for index := range pkg.ExecutableScriptBundle.PlanJSON.Steps {
		pkg.ExecutableScriptBundle.PlanJSON.Steps[index].NonDestructive = true
	}
	weak := &pkg.ExecutableScriptBundle.PlanJSON.Steps[0]
	weak.Action.Target = ActionTarget{}
	weak.TargetContract = nil
	pkg.ExecutableScriptBundle.StageApprovalPlan.Stages[0].TargetContract = nil
	pkg.ExecutableScriptBundle.ScriptOutline.Stages[0].TargetContract = nil
	summary, err := AssessClientExecutionPackage(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Readiness != PackageReadinessBlocked || len(summary.BlockingReasons) == 0 {
		t.Fatalf("weakest required stage must block the package: %+v", summary)
	}
}

func TestPackageConfidenceDoesNotAwardResultScoreForReusedClickTarget(t *testing.T) {
	pkg := confidenceFixture(t)
	index := -1
	for i := range pkg.ExecutableScriptBundle.PlanJSON.Steps {
		if pkg.ExecutableScriptBundle.PlanJSON.Steps[i].Action.Type == GraphActionClick {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatal("confidence fixture has no click stage")
	}
	step := &pkg.ExecutableScriptBundle.PlanJSON.Steps[index]
	step.Validations = []ValidationSpec{{
		ID: "wrong_result", Kind: "element_visible", Target: step.Action.Target, Expected: true, Required: true,
	}}
	summary, err := AssessClientExecutionPackage(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	var assessment *PackageStageConfidenceAssessment
	for i := range summary.Stages {
		if summary.Stages[i].NodeID == step.NodeID {
			assessment = &summary.Stages[i]
			break
		}
	}
	if assessment == nil || assessment.ResultValidationScore != 0 || summary.DeterministicValidationCoverage >= 1 {
		t.Fatalf("reusing a clicked control must not receive full result-validation confidence: stage=%+v summary=%+v", assessment, summary)
	}
}

func TestPackageConfidenceAssessmentIsDeterministic(t *testing.T) {
	pkg := confidenceFixture(t)
	first, err := AssessClientExecutionPackage(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := AssessClientExecutionPackage(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	if first.AssessmentHash != second.AssessmentHash || first.OverallScore != second.OverallScore {
		t.Fatalf("assessment must be deterministic: first=%+v second=%+v", first, second)
	}
}

func TestPackageConfidenceIgnoresEphemeralPackageTimestamp(t *testing.T) {
	pkg := confidenceFixture(t)
	pkg.CreatedAt = time.Date(2026, 8, 3, 8, 16, 37, 1, time.UTC)
	first, err := AssessClientExecutionPackage(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	pkg.CreatedAt = time.Date(2026, 8, 3, 8, 16, 37, 123456700, time.UTC)
	second, err := AssessClientExecutionPackage(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	if first.AssessmentHash != second.AssessmentHash || first.Readiness != second.Readiness || first.RequirementCoverage != second.RequirementCoverage {
		t.Fatalf("ephemeral package timestamp changed deterministic confidence: first=%+v second=%+v", first, second)
	}
}

func TestPackageRequirementCoverageRequiresNodeValidationAndEvidence(t *testing.T) {
	pkg := confidenceFixture(t)
	node := pkg.WorkflowGraph.Nodes[0]
	node.Validations = []ValidationSpec{{ID: "required", Kind: "element_visible", Required: true}}
	pkg.WorkflowGraph.Requirements = []GraphRequirement{{ID: "must_show", Kind: "must_show", Required: true, NodeRefs: []string{node.ID}, EvidenceRefs: []EvidenceRef{{ID: "requirement_evidence", Kind: EvidenceKindBrowserScan}}}}
	summary, err := AssessClientExecutionPackage(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	if summary.RequirementCoverage != 0 {
		t.Fatalf("node_refs without node validation evidence must not count as covered: %+v", summary)
	}
	node.EvidenceRefs = []EvidenceRef{{ID: "node_evidence", Kind: EvidenceKindBrowserScan}}
	summary, err = AssessClientExecutionPackage(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	if summary.RequirementCoverage != 1 {
		t.Fatalf("validated evidence-backed requirement should be covered: %+v", summary)
	}
}

func TestPackageConfidenceIgnoresSafetyConstraintsForPositiveCoverage(t *testing.T) {
	pkg := confidenceFixture(t)
	pkg.WorkflowGraph.Requirements = []GraphRequirement{
		{ID: "must_not", Kind: "must_not_show", Description: "secret", Required: false},
		{ID: "forbidden", Kind: "forbidden_page", Description: "/billing", Required: false},
	}
	summary, err := AssessClientExecutionPackage(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	if summary.RequirementCoverage != 1 {
		t.Fatalf("safety constraints must not reduce positive requirement coverage: %+v", summary)
	}
}

func TestPackageConfidenceScopesPostProductionRequirementsOutsideBrowserPackage(t *testing.T) {
	pkg := confidenceFixture(t)
	pkg.WorkflowGraph.Requirements = []GraphRequirement{
		{ID: "director", Kind: "must_show", Description: "导演模型制定脚本并由 Seedance 2.5 生成候选，FFmpeg 合成最终 MP4 成片", Required: true},
	}
	summary, err := AssessClientExecutionPackage(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	if summary.RequirementCoverage != 1 {
		t.Fatalf("post-production requirements belong to final-film assessment, not browser-package coverage: %+v", summary)
	}

	pkg.WorkflowGraph.Requirements = []GraphRequirement{
		{ID: "playable", Kind: "must_show", Description: "按左、右、下和旋转键后方块位置或形状变化", Required: true},
	}
	summary, err = AssessClientExecutionPackage(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	if summary.RequirementCoverage != 0 {
		t.Fatalf("an unmapped browser/playability requirement must still block browser-package coverage: %+v", summary)
	}
}

func TestPackageConfidenceAllowsCompleteRuntimeAdaptiveContractWithoutPriorPageEvidence(t *testing.T) {
	pkg := confidenceFixture(t)
	makeConfidenceStageRuntimeAdaptive(t, &pkg, 0)
	node := pkg.WorkflowGraph.Nodes[0]
	node.Validations = []ValidationSpec{{ID: "required", Kind: "url_matches", Target: ActionTarget{URL: "https://app.example.com/dashboard"}, Expected: "/dashboard", Required: true}}
	node.EvidenceRefs = nil
	pkg.WorkflowGraph.Requirements = []GraphRequirement{{ID: "must_show", Kind: "must_show", Required: true, NodeRefs: []string{node.ID}, EvidenceRefs: []EvidenceRef{{ID: "ev_user_requirement", Kind: EvidenceKindUserInput}}}}

	summary, err := AssessClientExecutionPackage(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Readiness == PackageReadinessBlocked || summary.RequirementCoverage != 1 || summary.ExecutionContractCoverage != 1 || summary.RuntimePageEvidenceCoverage >= 1 {
		t.Fatalf("complete runtime contract should be package-ready while runtime evidence remains pending: %+v", summary)
	}
}

func TestPackageConfidenceBlocksIncompleteRuntimeAdaptiveContract(t *testing.T) {
	pkg := confidenceFixture(t)
	makeConfidenceStageRuntimeAdaptive(t, &pkg, 0)
	pkg.ExecutableScriptBundle.StageApprovalPlan.Stages[0].SuccessState = ""
	summary, err := AssessClientExecutionPackage(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Readiness != PackageReadinessBlocked || summary.ExecutionContractCoverage != 0 {
		t.Fatalf("missing runtime-adaptive success state must block: %+v", summary)
	}
}

func makeConfidenceStageRuntimeAdaptive(t *testing.T, pkg *ClientExecutionPackage, index int) {
	t.Helper()
	for i := range pkg.ExecutableScriptBundle.PlanJSON.Steps {
		pkg.ExecutableScriptBundle.PlanJSON.Steps[i].NonDestructive = true
		pkg.ExecutableScriptBundle.StageApprovalPlan.Stages[i].Interaction.NonDestructive = true
		for j := range pkg.ExecutableScriptBundle.ScriptOutline.Stages[i].Interactions {
			pkg.ExecutableScriptBundle.ScriptOutline.Stages[i].Interactions[j].NonDestructive = true
		}
	}
	step := &pkg.ExecutableScriptBundle.PlanJSON.Steps[index]
	stage := &pkg.ExecutableScriptBundle.StageApprovalPlan.Stages[index]
	outline := &pkg.ExecutableScriptBundle.ScriptOutline.Stages[index]
	planEvidence := []EvidenceRef{{ID: "ev_user_requirement", Kind: EvidenceKindUserInput}}
	step.RuntimeAdaptive = true
	step.StageKind = BusinessStageKindSessionSetup
	step.RouteState = BusinessRouteStateWorkspace
	step.NonDestructive = true
	step.EvidenceRefs = planEvidence
	stage.RuntimeAdaptive = true
	stage.StageKind = step.StageKind
	stage.RouteState = step.RouteState
	stage.Interaction.NonDestructive = true
	stage.EvidenceRefs = planEvidence
	stage.Interaction.EvidenceRefs = planEvidence
	stage.CapturePlan = &BrowserAgentCapturePlan{Intent: "record approved state", PrimaryArtifact: "screenshot", RequiredAssets: []string{"viewport_screenshot"}}
	outline.RuntimeAdaptive = true
	outline.StageKind = step.StageKind
	outline.RouteState = step.RouteState
	outline.EvidenceRefs = planEvidence
	outline.CapturePlan = &BrowserAgentCapturePlan{Intent: "record approved state", PrimaryArtifact: "screenshot", RequiredAssets: []string{"viewport_screenshot"}}
	for i := range outline.Interactions {
		outline.Interactions[i].NonDestructive = true
		outline.Interactions[i].EvidenceRefs = planEvidence
	}
	outline.CanModify = append(outline.CanModify, "selector")
	outline.MustPreserve = append(outline.MustPreserve, "success_state", "safety_policy")
	if len(pkg.ExecutableScriptBundle.PlanJSON.SafetyPolicy.AllowedDomains) == 0 {
		pkg.ExecutableScriptBundle.PlanJSON.SafetyPolicy.AllowedDomains = []string{"app.example.com"}
	}
	if len(pkg.ExecutableScriptBundle.ScriptOutline.AllowedExplorationScope.AllowedOrigins) == 0 {
		pkg.ExecutableScriptBundle.ScriptOutline.AllowedExplorationScope.AllowedOrigins = []string{"https://app.example.com"}
	}
}

func confidenceFixture(t *testing.T) ClientExecutionPackage {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("fixture path")
	}
	path := filepath.Join(filepath.Dir(current), "..", "..", "..", "contracts", "exchange", "v1", "client_execution_package.browser_agent_outline.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var pkg ClientExecutionPackage
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatal(err)
	}
	return pkg
}
