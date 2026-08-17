package model

import (
	"errors"
	"fmt"
	"time"
)

const ValidationRunReportSchemaVersion = "demoops.validation_run_report.v1"

type ValidationFailureCategory string

const (
	ValidationFailureCategoryAppPackageContract ValidationFailureCategory = "app_package_contract"
	ValidationFailureCategoryServerExecution    ValidationFailureCategory = "server_execution"
	ValidationFailureCategoryBrowserRuntime     ValidationFailureCategory = "browser_runtime"
	ValidationFailureCategoryMediaEvidence      ValidationFailureCategory = "media_evidence"
	ValidationFailureCategoryProviderCandidate  ValidationFailureCategory = "provider_candidate"
	ValidationFailureCategoryEnvironment        ValidationFailureCategory = "environment"
)

type ValidationRunReport struct {
	SchemaVersion             string                      `json:"schema_version"`
	ReportID                  string                      `json:"report_id"`
	RunID                     string                      `json:"run_id"`
	PackageID                 string                      `json:"package_id"`
	BundleHashSHA256          string                      `json:"bundle_hash_sha256"`
	PolicyHashSHA256          string                      `json:"policy_hash_sha256"`
	Status                    string                      `json:"status"`
	FinalDecision             ValidationDecision          `json:"final_decision"`
	OriginalPackageUnchanged  bool                        `json:"original_package_unchanged"`
	FormalAppServerSuccess    bool                        `json:"formal_app_server_success"`
	SourceOrigin              string                      `json:"source_origin,omitempty"`
	AppGenerated              bool                        `json:"app_generated,omitempty"`
	TransportAuthenticated    bool                        `json:"transport_authenticated,omitempty"`
	FormalExchange            bool                        `json:"formal_exchange,omitempty"`
	Stages                    []ValidationRunStageSummary `json:"stages,omitempty"`
	Findings                  []ValidationRunFinding      `json:"findings,omitempty"`
	EvidenceRefs              []EvidenceRef               `json:"evidence_refs,omitempty"`
	ReplayManifestID          string                      `json:"replay_manifest_id,omitempty"`
	ReproducibilityConditions []string                    `json:"reproducibility_conditions,omitempty"`
	AppFeedback               []ValidationFeedback        `json:"app_feedback,omitempty"`
	ServerFeedback            []ValidationFeedback        `json:"server_feedback,omitempty"`
	CreatedAt                 time.Time                   `json:"created_at"`
}

type ValidationRunStageSummary struct {
	NodeID                      string                         `json:"node_id"`
	StageID                     string                         `json:"stage_id"`
	Order                       int                            `json:"order"`
	Status                      string                         `json:"status"`
	Decision                    ValidationDecision             `json:"decision,omitempty"`
	FailureCodes                []string                       `json:"failure_codes,omitempty"`
	EvidenceArtifactIDs         []string                       `json:"evidence_artifact_ids,omitempty"`
	ActionDefinitionEvidenceIDs []string                       `json:"action_definition_evidence_ids,omitempty"`
	BeforeScreenshotArtifactIDs []string                       `json:"before_screenshot_artifact_ids,omitempty"`
	AfterScreenshotArtifactIDs  []string                       `json:"after_screenshot_artifact_ids,omitempty"`
	StageEventIDs               []string                       `json:"stage_event_ids,omitempty"`
	TraceArtifactIDs            []string                       `json:"trace_artifact_ids,omitempty"`
	SelectorRepairs             []ReplayManifestSelectorRepair `json:"selector_repairs,omitempty"`
}

type ValidationRunFinding struct {
	ID                string                    `json:"id"`
	Code              string                    `json:"code"`
	Category          ValidationFailureCategory `json:"category"`
	Domain            ValidationCheckDomain     `json:"responsibility_domain"`
	Severity          FindingSeverity           `json:"severity"`
	Passed            bool                      `json:"passed"`
	NodeID            string                    `json:"node_id,omitempty"`
	StageID           string                    `json:"stage_id,omitempty"`
	ArtifactID        string                    `json:"artifact_id,omitempty"`
	Summary           string                    `json:"summary,omitempty"`
	EvidenceRefs      []EvidenceRef             `json:"evidence_refs,omitempty"`
	ReproducibleWhen  []string                  `json:"reproducible_when,omitempty"`
	RecommendedRepair string                    `json:"recommended_repair,omitempty"`
}

type ValidationFeedback struct {
	Code         string        `json:"code"`
	Summary      string        `json:"summary"`
	NextStep     string        `json:"next_step,omitempty"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

func (r ValidationRunReport) Validate() error {
	if r.SchemaVersion != ValidationRunReportSchemaVersion {
		return errors.New("unsupported validation run report schema version")
	}
	if anyBlank(r.ReportID, r.RunID, r.PackageID, r.BundleHashSHA256, r.PolicyHashSHA256) || r.CreatedAt.IsZero() {
		return errors.New("validation run report is missing identity fields or created_at")
	}
	if r.Status != "success" && r.Status != "failed" {
		return errors.New("validation run report status must be success or failed")
	}
	if !validValidationDecision(r.FinalDecision) {
		return errors.New("validation run report final_decision is invalid")
	}
	if r.FormalAppServerSuccess && (!r.OriginalPackageUnchanged || !r.AppGenerated || !r.TransportAuthenticated || !r.FormalExchange) {
		return errors.New("formal app-server success requires original_package_unchanged and formal provenance")
	}
	for index, stage := range r.Stages {
		if anyBlank(stage.NodeID, stage.StageID) || stage.Order < 1 {
			return fmt.Errorf("validation run stage %d is missing node/stage identity or order", index)
		}
		if stage.Decision != "" && !validValidationDecision(stage.Decision) {
			return fmt.Errorf("validation run stage %d has invalid decision", index)
		}
		if r.formalExecution() && (len(stage.ActionDefinitionEvidenceIDs) == 0 || len(stage.BeforeScreenshotArtifactIDs) == 0 || len(stage.AfterScreenshotArtifactIDs) == 0 || len(stage.StageEventIDs) == 0 || len(stage.TraceArtifactIDs) == 0) {
			return fmt.Errorf("formal validation run stage %d is missing required action, screenshot, event, or trace evidence", index)
		}
	}
	for index, finding := range r.Findings {
		if anyBlank(finding.ID, finding.Code, string(finding.Category), string(finding.Domain), string(finding.Severity)) {
			return fmt.Errorf("validation finding %d requires id, code, category, responsibility_domain and severity", index)
		}
		if !validValidationFailureCategory(finding.Category) {
			return fmt.Errorf("validation finding %d has unsupported category %q", index, finding.Category)
		}
		if !finding.Passed && len(finding.EvidenceRefs) == 0 && finding.ArtifactID == "" {
			return fmt.Errorf("validation finding %d requires evidence_refs or artifact_id", index)
		}
		if !finding.Passed && (finding.Summary == "" || finding.RecommendedRepair == "" || len(finding.ReproducibleWhen) == 0) {
			return fmt.Errorf("validation finding %d requires summary, recommended_repair and reproducibility conditions", index)
		}
		if err := validateRuntimeContractText(finding.Summary, finding.RecommendedRepair); err != nil {
			return err
		}
	}
	for _, feedback := range append(append([]ValidationFeedback{}, r.AppFeedback...), r.ServerFeedback...) {
		if anyBlank(feedback.Code, feedback.Summary) {
			return errors.New("validation feedback requires code and summary")
		}
		if err := validateRuntimeContractText(feedback.Summary, feedback.NextStep); err != nil {
			return err
		}
	}
	return validateRuntimeEvidenceRefs(r.EvidenceRefs)
}

func (r ValidationRunReport) formalExecution() bool {
	return r.OriginalPackageUnchanged && r.AppGenerated && r.TransportAuthenticated && r.FormalExchange
}

func validValidationFailureCategory(value ValidationFailureCategory) bool {
	switch value {
	case ValidationFailureCategoryAppPackageContract, ValidationFailureCategoryServerExecution,
		ValidationFailureCategoryBrowserRuntime, ValidationFailureCategoryMediaEvidence,
		ValidationFailureCategoryProviderCandidate, ValidationFailureCategoryEnvironment:
		return true
	default:
		return false
	}
}

func ValidationFailureCategoryForDomain(domain ValidationCheckDomain) ValidationFailureCategory {
	switch domain {
	case ValidationCheckDomainApp:
		return ValidationFailureCategoryAppPackageContract
	case ValidationCheckDomainEnvironment:
		return ValidationFailureCategoryEnvironment
	case ValidationCheckDomainMediaDelivery:
		return ValidationFailureCategoryMediaEvidence
	case ValidationCheckDomainBrowserRuntime:
		return ValidationFailureCategoryBrowserRuntime
	case ValidationCheckDomainProviderCandidate:
		return ValidationFailureCategoryProviderCandidate
	case ValidationCheckDomainServer:
		return ValidationFailureCategoryServerExecution
	default:
		return ValidationFailureCategoryServerExecution
	}
}
