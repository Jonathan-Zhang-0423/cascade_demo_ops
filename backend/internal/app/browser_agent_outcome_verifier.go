package app

import (
	"context"

	"cascade-demoops/backend/internal/model"
)

// OutcomeVerifier is the future Validation Agent integration point. It
// receives redacted runtime events rather than Playwright or page objects.
type OutcomeVerifier interface {
	ValidateBeforeExecution(context.Context, model.BrowserAgentValidationContext) (model.ValidationReport, error)
	ValidateStageEvents(context.Context, model.BrowserAgentValidationContext, []model.StageExecutionEvent) (model.ValidationReport, error)
	ValidatePostExecution(context.Context, model.BrowserAgentValidationContext, model.RecordingResultPackage, []model.StageExecutionEvent) (model.ValidationReport, error)
}

func validationContextFromPackage(pkg *model.ClientExecutionPackage) model.BrowserAgentValidationContext {
	context := model.BrowserAgentValidationContext{}
	if pkg == nil {
		return context
	}
	context.SourcePackageID = pkg.PackageID
	context.ProjectContextSummary = pkg.ProjectContextSummary
	context.ProductMapSummary = pkg.ProductMapSummary
	context.CredentialGrants = append([]model.CredentialGrant(nil), pkg.CredentialGrants...)
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
	if pkg.RecordingRunSpec.AllowedDomains != nil {
		context.AllowedDomains = append([]string{}, pkg.RecordingRunSpec.AllowedDomains...)
	}
	if bundle.ScriptOutline != nil {
		context.ForbiddenActions = append([]string{}, bundle.ScriptOutline.ForbiddenActions...)
	}
	return context
}
