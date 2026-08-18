package app

import (
	"strings"

	"cascade-demoops/backend/internal/model"
)

// PackagePreflightDiagnostics is an immutable, Server-owned explanation of an
// App-produced package. It is diagnostic only: neither the package payload nor
// its approval, confidence, or hash is changed while creating this report.
type PackagePreflightDiagnostics struct {
	FormalExecutionAllowed bool                               `json:"formal_execution_allowed"`
	AppReadiness           model.PackageReadiness             `json:"app_readiness,omitempty"`
	ConfidenceBlockers     []string                           `json:"confidence_blockers,omitempty"`
	Requirements           []PackageRequirementPreflightIssue `json:"requirements,omitempty"`
	Stages                 []PackageStagePreflightSummary     `json:"stages,omitempty"`
}

type PackageRequirementPreflightIssue struct {
	RequirementID      string   `json:"requirement_id"`
	Kind               string   `json:"kind,omitempty"`
	Description        string   `json:"description,omitempty"`
	Required           bool     `json:"required"`
	NodeRefs           []string `json:"node_refs,omitempty"`
	EvidenceCount      int      `json:"evidence_count"`
	Coverage           string   `json:"coverage"`
	DiagnosticCategory string   `json:"diagnostic_category"`
	ClassificationNote string   `json:"classification_note,omitempty"`
	RecommendedOwner   string   `json:"recommended_owner,omitempty"`
	RecommendedAction  string   `json:"recommended_action,omitempty"`
}

type PackageStagePreflightSummary struct {
	NodeID                   string `json:"node_id"`
	StageKind                string `json:"stage_kind,omitempty"`
	RequiredValidationCount  int    `json:"required_validation_count"`
	EvidenceCount            int    `json:"evidence_count"`
	RuntimeDiscoveryRequired bool   `json:"runtime_discovery_required"`
}

func packagePreflightDiagnostics(pkg model.ClientExecutionPackage) *PackagePreflightDiagnostics {
	diagnostics := &PackagePreflightDiagnostics{
		FormalExecutionAllowed: false,
		ConfidenceBlockers:     []string{},
		Requirements:           []PackageRequirementPreflightIssue{},
		Stages:                 []PackageStagePreflightSummary{},
	}
	if pkg.ConfidenceSummary != nil {
		diagnostics.AppReadiness = pkg.ConfidenceSummary.Readiness
		diagnostics.ConfidenceBlockers = append(diagnostics.ConfidenceBlockers, pkg.ConfidenceSummary.BlockingReasons...)
		diagnostics.FormalExecutionAllowed = pkg.ConfidenceSummary.Readiness == model.PackageReadinessReady
	}
	if pkg.WorkflowGraph != nil {
		for _, requirement := range pkg.WorkflowGraph.Requirements {
			if !requirement.Required {
				continue
			}
			issue := PackageRequirementPreflightIssue{
				RequirementID:      requirement.ID,
				Kind:               requirement.Kind,
				Description:        requirement.Description,
				Required:           true,
				NodeRefs:           append([]string{}, requirement.NodeRefs...),
				EvidenceCount:      len(requirement.EvidenceRefs),
				Coverage:           "covered",
				DiagnosticCategory: "page_interaction",
			}
			if len(requirement.NodeRefs) == 0 {
				issue.Coverage = "unmapped"
				issue.RecommendedOwner = "app"
				issue.RecommendedAction = "Map the requirement to one or more semantic browser stages and evidence, or serialize it as an explicit presentation/output constraint."
			}
			if len(requirement.EvidenceRefs) == 0 && issue.Coverage == "covered" {
				issue.Coverage = "evidence_missing"
				issue.RecommendedOwner = "app"
				issue.RecommendedAction = "Add App-produced evidence references for this required browser interaction."
			}
			if presentationRequirementCandidate(requirement) {
				issue.DiagnosticCategory = "presentation_output_candidate"
				issue.ClassificationNote = "Server heuristic only; this field is not a replacement for an App-declared presentation contract."
				if issue.RecommendedOwner == "" {
					issue.RecommendedOwner = "app"
					issue.RecommendedAction = "Confirm whether this is a video presentation/output constraint. If so, emit it in the App package's presentation contract instead of requiring a DOM stage."
				}
			}
			diagnostics.Requirements = append(diagnostics.Requirements, issue)
		}
	}
	if pkg.ExecutableScriptBundle == nil || pkg.ExecutableScriptBundle.PlanJSON == nil {
		return diagnostics
	}
	stages := map[string]model.StageApprovalStage{}
	if pkg.ExecutableScriptBundle.StageApprovalPlan != nil {
		for _, stage := range pkg.ExecutableScriptBundle.StageApprovalPlan.Stages {
			stages[stage.NodeID] = stage
		}
	}
	for _, step := range pkg.ExecutableScriptBundle.PlanJSON.Steps {
		summary := PackageStagePreflightSummary{NodeID: step.NodeID, StageKind: string(step.StageKind), RuntimeDiscoveryRequired: step.RuntimeAdaptive}
		for _, validation := range step.Validations {
			if validation.Required {
				summary.RequiredValidationCount++
			}
		}
		summary.EvidenceCount = len(step.EvidenceRefs)
		if stage, ok := stages[step.NodeID]; ok {
			summary.RuntimeDiscoveryRequired = summary.RuntimeDiscoveryRequired || stage.RuntimeAdaptive || stage.RuntimeRouteVerificationRequired
			summary.EvidenceCount += len(stage.EvidenceRefs)
		}
		diagnostics.Stages = append(diagnostics.Stages, summary)
	}
	return diagnostics
}

func presentationRequirementCandidate(requirement model.GraphRequirement) bool {
	content := strings.ToLower(requirement.Kind + " " + requirement.Description)
	for _, keyword := range []string{"caption", "subtitle", "narration", "voiceover", "presentation", "video output", "字幕", "旁白", "配音", "镜头", "画幅", "分辨率", "成片"} {
		if strings.Contains(content, keyword) {
			return true
		}
	}
	return false
}
