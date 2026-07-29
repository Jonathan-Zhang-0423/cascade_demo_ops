package model

import (
	"errors"
	"testing"
)

func TestValidateSourceBindingRejectsPageOnlySourceEvidence(t *testing.T) {
	pkg := sampleClientExecutionPackage(t)
	pkg.SourceBindingSummary = &SourceBindingSummary{SchemaVersion: ProductSourceBindingAssessmentSchemaVersion, Status: ProductSourceBindingUnverified, EffectiveMode: ProductSourceModePageOnly, AssessmentHash: "sha256-assessment", SourceCount: 1}
	pkg.EvidenceBundle.EvidenceRefs = append(pkg.EvidenceBundle.EvidenceRefs, EvidenceRef{ID: "source", Kind: EvidenceKindSourceCode})
	if err := validateSourceBindingSummary(&pkg); err == nil {
		t.Fatal("expected page-only source evidence leakage to be rejected")
	}
}

func TestValidateSourceBindingRejectsInjectedCodeSelectorWithoutEvidenceRef(t *testing.T) {
	pkg := sampleClientExecutionPackage(t)
	pkg.SourceBindingSummary = &SourceBindingSummary{SchemaVersion: ProductSourceBindingAssessmentSchemaVersion, Status: ProductSourceBindingUnverified, EffectiveMode: ProductSourceModePageOnly, AssessmentHash: "sha256-assessment", SourceCount: 1}
	pkg.ExecutableScriptBundle.PlanJSON.Steps[0].Action.Target.Source = "code_reader"
	pkg.ExecutableScriptBundle.PlanJSON.Steps[0].Action.Target.Selector = "[data-testid='foreign-product']"
	err := validateSourceBindingSummary(&pkg)
	var leakage *SourceEvidenceLeakageError
	if !errors.As(err, &leakage) {
		t.Fatalf("expected typed source evidence leakage, got %T: %v", err, err)
	}
}

func TestValidateSourceBindingRejectsLegacySourceEvidenceWithoutAssessment(t *testing.T) {
	pkg := sampleClientExecutionPackage(t)
	pkg.SourceBindingSummary = nil
	pkg.ExecutableScriptBundle.PlanJSON.Steps[0].Action.Target.Source = "source_code"
	if err := validateSourceBindingSummary(&pkg); err == nil {
		t.Fatal("legacy package with source-derived execution evidence must declare source_binding_summary")
	}
}
