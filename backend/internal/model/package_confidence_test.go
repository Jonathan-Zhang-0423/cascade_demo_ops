package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
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
