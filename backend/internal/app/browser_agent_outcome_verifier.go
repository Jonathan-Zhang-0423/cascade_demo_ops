package app

import (
	"context"

	"cascade-demoops/backend/internal/model"
)

// BrowserAgentValidationContext is an immutable view of the App-approved
// package. Outcome verifiers may read it but cannot rewrite it or its hashes.
type BrowserAgentValidationContext struct {
	RunID                     string
	SourcePackageID           string
	SourceBundleHashSHA256    string
	EffectivePolicyHashSHA256 string
	WorkflowGraph             *model.DemoWorkflowGraph
	Plan                      *model.ExecutionScriptDocument
	StageApprovalPlan         *model.StageApprovalPlan
	ScriptOutline             *model.BrowserAgentScriptOutline
	BrowserAgentContract      *model.BrowserAgentContract
}

// OutcomeVerifier is the future Validation Agent integration point. It
// receives redacted runtime events rather than Playwright or page objects.
type OutcomeVerifier interface {
	ValidateBeforeExecution(context.Context, BrowserAgentValidationContext) (model.ValidationReport, error)
	ValidateStageEvents(context.Context, BrowserAgentValidationContext, []model.StageExecutionEvent) (model.ValidationReport, error)
	ValidatePostExecution(context.Context, BrowserAgentValidationContext, model.RecordingResultPackage, []model.StageExecutionEvent) (model.ValidationReport, error)
}

func validationContextFromPackage(pkg *model.ClientExecutionPackage) BrowserAgentValidationContext {
	context := BrowserAgentValidationContext{}
	if pkg == nil {
		return context
	}
	context.SourcePackageID = pkg.PackageID
	context.WorkflowGraph = pkg.WorkflowGraph
	if pkg.ExecutableScriptBundle == nil {
		return context
	}
	bundle := pkg.ExecutableScriptBundle
	context.SourceBundleHashSHA256 = bundle.Reproducibility.BundleHashSHA256
	context.EffectivePolicyHashSHA256 = bundle.Reproducibility.BrowserAgentContractHashSHA256
	context.Plan = bundle.PlanJSON
	context.StageApprovalPlan = bundle.StageApprovalPlan
	context.ScriptOutline = bundle.ScriptOutline
	context.BrowserAgentContract = bundle.BrowserAgentContract
	return context
}
