package app

import (
	"testing"

	"cascade-demoops/backend/internal/model"
)

func TestPackagePreflightDiagnosticsExplainsBlockedPresentationRequirement(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	pkg.WorkflowGraph.Requirements = append(pkg.WorkflowGraph.Requirements, model.GraphRequirement{
		ID: "caption_each_stage", Kind: "must_show", Description: "Add subtitles for every step", Required: true,
	})
	confidence, err := model.AssessClientExecutionPackage(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	pkg.ConfidenceSummary = confidence

	diagnostics := packagePreflightDiagnostics(pkg)
	if diagnostics.FormalExecutionAllowed || diagnostics.AppReadiness != model.PackageReadinessBlocked {
		t.Fatalf("blocked App package must remain ineligible for formal execution: %+v", diagnostics)
	}
	if len(diagnostics.Requirements) == 0 {
		t.Fatal("expected requirement diagnostics")
	}
	var caption *PackageRequirementPreflightIssue
	for index := range diagnostics.Requirements {
		if diagnostics.Requirements[index].RequirementID == "caption_each_stage" {
			caption = &diagnostics.Requirements[index]
			break
		}
	}
	if caption == nil || caption.Coverage != "unmapped" || caption.DiagnosticCategory != "presentation_output_candidate" || caption.RecommendedOwner != "app" {
		t.Fatalf("unexpected caption diagnostic: %+v", caption)
	}
	if len(diagnostics.Stages) != len(pkg.ExecutableScriptBundle.PlanJSON.Steps) {
		t.Fatalf("expected one traceable summary per stage: %+v", diagnostics.Stages)
	}
}

func TestPackagePreflightDiagnosticsDoesNotMakeBlockedPackageRunnable(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	pkg.ConfidenceSummary = &model.PackageConfidenceSummary{Readiness: model.PackageReadinessBlocked, BlockingReasons: []string{"App requirement coverage is incomplete"}}
	diagnostics := packagePreflightDiagnostics(pkg)
	if diagnostics.FormalExecutionAllowed {
		t.Fatal("diagnostics must not turn a blocked App package into an executable package")
	}
	if len(diagnostics.ConfidenceBlockers) != 1 {
		t.Fatalf("expected confidence blocker to remain visible: %+v", diagnostics)
	}
}
