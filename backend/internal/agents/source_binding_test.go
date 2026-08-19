package agents

import (
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestAssessProductSourceBindingMatchedByDeploymentOrigin(t *testing.T) {
	page := model.PageUnderstandingSnapshot{ProductIdentitySignals: []model.ProductIdentitySignal{{Kind: "deployment_origin", Strength: "strong", ValueSHA256: hashString("https://product.example")}}}
	code := model.CodeUnderstandingSnapshot{ID: "code", SourceDigestSHA256: "digest", ProductIdentitySignals: []model.ProductIdentitySignal{{Kind: "deployment_origin", Strength: "strong", ValueSHA256: hashString("https://product.example")}}}
	assessment, effective, err := AssessProductSourceBinding(&model.ProjectContext{}, []model.CodeUnderstandingSnapshot{code}, []model.PageUnderstandingSnapshot{page}, time.Now())
	if err != nil || assessment.Status != model.ProductSourceBindingMatched || assessment.EffectiveMode != model.ProductSourceModeMixed || len(effective) != 1 {
		t.Fatalf("expected matched mixed assessment, assessment=%+v effective=%d err=%v", assessment, len(effective), err)
	}
}

func TestAssessProductSourceBindingMismatchBlocksBeforeFusion(t *testing.T) {
	page := model.PageUnderstandingSnapshot{ProductIdentitySignals: []model.ProductIdentitySignal{
		{Kind: "deployment_origin", Strength: "strong", ValueSHA256: hashString("https://alpha.example")},
		{Kind: "product_name", Strength: "medium", ValueSHA256: hashString("alpha")},
	}}
	code := model.CodeUnderstandingSnapshot{ID: "code", SourceDigestSHA256: "digest", ProductIdentitySignals: []model.ProductIdentitySignal{
		{Kind: "deployment_origin", Strength: "strong", ValueSHA256: hashString("https://beta.example")},
		{Kind: "product_name", Strength: "medium", ValueSHA256: hashString("beta")},
	}}
	assessment, effective, err := AssessProductSourceBinding(&model.ProjectContext{}, []model.CodeUnderstandingSnapshot{code}, []model.PageUnderstandingSnapshot{page}, time.Now())
	if err == nil || assessment.Status != model.ProductSourceBindingMismatched || assessment.EffectiveMode != model.ProductSourceModeBlocked || len(effective) != 0 {
		t.Fatalf("expected blocking mismatch, assessment=%+v effective=%d err=%v", assessment, len(effective), err)
	}
	if _, ok := err.(*model.ProductSourceMismatchError); !ok {
		t.Fatalf("expected ProductSourceMismatchError, got %T", err)
	}

	project := &model.ProjectContext{SourceBinding: &model.ProductSourceBindingAssessment{Decision: "continue_page_only", AssessmentHash: assessment.AssessmentHash}}
	next, effective, err := AssessProductSourceBinding(project, []model.CodeUnderstandingSnapshot{code}, []model.PageUnderstandingSnapshot{page}, time.Now())
	if err != nil || next.EffectiveMode != model.ProductSourceModePageOnly || len(effective) != 0 {
		t.Fatalf("expected explicit page-only continuation, assessment=%+v effective=%d err=%v", next, len(effective), err)
	}
}

func TestAssessProductSourceBindingUnverifiedDefaultsToPageOnly(t *testing.T) {
	page := model.PageUnderstandingSnapshot{ProductIdentitySignals: []model.ProductIdentitySignal{{Kind: "deployment_origin", Strength: "strong", ValueSHA256: hashString("https://alpha.example")}}}
	code := model.CodeUnderstandingSnapshot{ID: "code", SourceDigestSHA256: "digest", ProductIdentitySignals: []model.ProductIdentitySignal{{Kind: "product_name", Strength: "medium", ValueSHA256: hashString("alpha")}}}
	assessment, effective, err := AssessProductSourceBinding(&model.ProjectContext{}, []model.CodeUnderstandingSnapshot{code}, []model.PageUnderstandingSnapshot{page}, time.Now())
	if err != nil || assessment.Status != model.ProductSourceBindingUnverified || assessment.EffectiveMode != model.ProductSourceModePageOnly || len(effective) != 0 {
		t.Fatalf("expected unverified page-only assessment, assessment=%+v effective=%d err=%v", assessment, len(effective), err)
	}
}

func TestAssessProductSourceBindingAllowsExplicitConfirmationOnlyWhenUnverified(t *testing.T) {
	page := model.PageUnderstandingSnapshot{ProductIdentitySignals: []model.ProductIdentitySignal{{Kind: "deployment_origin", Strength: "strong", ValueSHA256: hashString("https://alpha.example")}}}
	code := model.CodeUnderstandingSnapshot{ID: "code", SourceDigestSHA256: "digest", ProductIdentitySignals: []model.ProductIdentitySignal{{Kind: "product_name", Strength: "medium", ValueSHA256: hashString("alpha-app")}}}
	initial, _, err := AssessProductSourceBinding(&model.ProjectContext{}, []model.CodeUnderstandingSnapshot{code}, []model.PageUnderstandingSnapshot{page}, time.Now())
	if err != nil || initial.Status != model.ProductSourceBindingUnverified {
		t.Fatalf("expected unverified assessment before confirmation: %+v err=%v", initial, err)
	}
	project := &model.ProjectContext{SourceBinding: &model.ProductSourceBindingAssessment{Decision: "confirm_mixed", AssessmentHash: "validated-persisted-assessment"}}
	confirmed, effective, err := AssessProductSourceBinding(project, []model.CodeUnderstandingSnapshot{code}, []model.PageUnderstandingSnapshot{page}, time.Now())
	if err != nil || confirmed.Status != model.ProductSourceBindingConfirmed || confirmed.EffectiveMode != model.ProductSourceModeMixed || confirmed.Decision != "confirm_mixed" || len(effective) != 1 {
		t.Fatalf("explicit unverified binding confirmation was not honored: assessment=%+v effective=%d err=%v", confirmed, len(effective), err)
	}
}

func TestAssessProductSourceBindingDoesNotConfirmDetectedMismatch(t *testing.T) {
	page := model.PageUnderstandingSnapshot{ProductIdentitySignals: []model.ProductIdentitySignal{
		{Kind: "deployment_origin", Strength: "strong", ValueSHA256: hashString("https://alpha.example")},
		{Kind: "product_name", Strength: "medium", ValueSHA256: hashString("alpha")},
	}}
	code := model.CodeUnderstandingSnapshot{ID: "code", SourceDigestSHA256: "digest", ProductIdentitySignals: []model.ProductIdentitySignal{
		{Kind: "deployment_origin", Strength: "strong", ValueSHA256: hashString("https://beta.example")},
		{Kind: "product_name", Strength: "medium", ValueSHA256: hashString("beta")},
	}}
	initial, _, _ := AssessProductSourceBinding(&model.ProjectContext{}, []model.CodeUnderstandingSnapshot{code}, []model.PageUnderstandingSnapshot{page}, time.Now())
	project := &model.ProjectContext{SourceBinding: &model.ProductSourceBindingAssessment{Decision: "confirm_mixed", AssessmentHash: initial.AssessmentHash}}
	confirmed, effective, err := AssessProductSourceBinding(project, []model.CodeUnderstandingSnapshot{code}, []model.PageUnderstandingSnapshot{page}, time.Now())
	if err == nil || confirmed.Status != model.ProductSourceBindingMismatched || confirmed.EffectiveMode != model.ProductSourceModeBlocked || len(effective) != 0 {
		t.Fatalf("detected mismatch was incorrectly overridable: assessment=%+v effective=%d err=%v", confirmed, len(effective), err)
	}
}

func TestAssessProductSourceBindingMultipleSourcesBlocksOnAnyMismatch(t *testing.T) {
	page := model.PageUnderstandingSnapshot{ProductIdentitySignals: []model.ProductIdentitySignal{
		{Kind: "deployment_origin", Strength: "strong", ValueSHA256: hashString("https://alpha.example")},
		{Kind: "product_name", Strength: "medium", ValueSHA256: hashString("alpha")},
	}}
	matched := model.CodeUnderstandingSnapshot{ID: "matched", SourceDigestSHA256: "digest-a", ProductIdentitySignals: []model.ProductIdentitySignal{{Kind: "deployment_origin", Strength: "strong", ValueSHA256: hashString("https://alpha.example")}}}
	foreign := model.CodeUnderstandingSnapshot{ID: "foreign", SourceDigestSHA256: "digest-b", ProductIdentitySignals: []model.ProductIdentitySignal{
		{Kind: "deployment_origin", Strength: "strong", ValueSHA256: hashString("https://beta.example")},
		{Kind: "product_name", Strength: "medium", ValueSHA256: hashString("beta")},
	}}
	assessment, effective, err := AssessProductSourceBinding(&model.ProjectContext{}, []model.CodeUnderstandingSnapshot{matched, foreign}, []model.PageUnderstandingSnapshot{page}, time.Now())
	if err == nil || assessment.Status != model.ProductSourceBindingMismatched || len(effective) != 0 {
		t.Fatalf("one conflicting source must block the entire mixed analysis: assessment=%+v effective=%d err=%v", assessment, len(effective), err)
	}
}

func TestProductSourceBindingAssessmentHashChangesWithInputDigest(t *testing.T) {
	page := model.PageUnderstandingSnapshot{ProductIdentitySignals: []model.ProductIdentitySignal{{Kind: "deployment_origin", ValueSHA256: hashString("https://alpha.example")}}}
	code := model.CodeUnderstandingSnapshot{ID: "code", SourceDigestSHA256: "digest-a", ProductIdentitySignals: []model.ProductIdentitySignal{{Kind: "product_name", ValueSHA256: hashString("alpha")}}}
	first, _, _ := AssessProductSourceBinding(&model.ProjectContext{}, []model.CodeUnderstandingSnapshot{code}, []model.PageUnderstandingSnapshot{page}, time.Now())
	code.SourceDigestSHA256 = "digest-b"
	second, _, _ := AssessProductSourceBinding(&model.ProjectContext{}, []model.CodeUnderstandingSnapshot{code}, []model.PageUnderstandingSnapshot{page}, time.Now())
	if first.AssessmentHash == second.AssessmentHash {
		t.Fatal("source content digest change must invalidate the previous assessment hash")
	}
}

func TestProductSourceBindingAssessmentHashChangesWithProductURL(t *testing.T) {
	page := model.PageUnderstandingSnapshot{ProductIdentitySignals: []model.ProductIdentitySignal{{Kind: "deployment_origin", ValueSHA256: hashString("https://alpha.example")}}}
	code := model.CodeUnderstandingSnapshot{ID: "code", SourceDigestSHA256: "digest", SourceRefHashSHA256: "source-ref", ProductIdentitySignals: []model.ProductIdentitySignal{{Kind: "product_name", ValueSHA256: hashString("alpha")}}}
	first, _, _ := AssessProductSourceBinding(&model.ProjectContext{ProductURL: "https://alpha.example/one"}, []model.CodeUnderstandingSnapshot{code}, []model.PageUnderstandingSnapshot{page}, time.Now())
	second, _, _ := AssessProductSourceBinding(&model.ProjectContext{ProductURL: "https://alpha.example/two"}, []model.CodeUnderstandingSnapshot{code}, []model.PageUnderstandingSnapshot{page}, time.Now())
	if first.AssessmentHash == second.AssessmentHash {
		t.Fatal("product URL change must invalidate the previous assessment hash")
	}
}

func TestProductSourceBindingAssessmentHashChangesWithSourceReference(t *testing.T) {
	page := model.PageUnderstandingSnapshot{ProductIdentitySignals: []model.ProductIdentitySignal{{Kind: "deployment_origin", ValueSHA256: hashString("https://alpha.example")}}}
	code := model.CodeUnderstandingSnapshot{ID: "code", SourceDigestSHA256: "same-content", SourceRefHashSHA256: "source-a", ProductIdentitySignals: []model.ProductIdentitySignal{{Kind: "product_name", ValueSHA256: hashString("alpha")}}}
	first, _, _ := AssessProductSourceBinding(&model.ProjectContext{ProductURL: "https://alpha.example"}, []model.CodeUnderstandingSnapshot{code}, []model.PageUnderstandingSnapshot{page}, time.Now())
	code.SourceRefHashSHA256 = "source-b"
	second, _, _ := AssessProductSourceBinding(&model.ProjectContext{ProductURL: "https://alpha.example"}, []model.CodeUnderstandingSnapshot{code}, []model.PageUnderstandingSnapshot{page}, time.Now())
	if first.AssessmentHash == second.AssessmentHash {
		t.Fatal("source reference change must invalidate the previous assessment hash even when content is identical")
	}
}
