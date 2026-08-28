package app

import (
	"fmt"
	"strings"

	"cascade-demoops/backend/internal/model"
)

// BuildValidationRunReport creates the single acceptance-facing report from
// the already validated result, immutable source package, and replay index.
// It never repairs or rewrites any input package data.
func BuildValidationRunReport(result model.RecordingResultPackage, pkg model.ClientExecutionPackage, manifest model.ReplayManifest) (model.ValidationRunReport, error) {
	bundleHash, policyHash := executableHashes(pkg)
	if bundleHash == "" || policyHash == "" || manifest.BundleHashSHA256 != bundleHash || manifest.PolicyHashSHA256 != policyHash {
		return model.ValidationRunReport{}, fmt.Errorf("validation run report hash binding mismatch")
	}
	if result.SourcePackageID != "" && result.SourcePackageID != pkg.PackageID {
		return model.ValidationRunReport{}, fmt.Errorf("validation run report package binding mismatch")
	}

	runID := manifest.RunID
	if runID == "" {
		for _, report := range result.ValidationReports {
			if report.RunID != "" {
				runID = report.RunID
				break
			}
		}
	}
	if runID == "" {
		return model.ValidationRunReport{}, fmt.Errorf("validation run report is missing run identity")
	}

	originalUnchanged := true
	for _, report := range result.ValidationReports {
		if report.SourcePackageID != pkg.PackageID || report.SourceBundleHashSHA256 != bundleHash || report.PolicyHashSHA256 != policyHash {
			return model.ValidationRunReport{}, fmt.Errorf("validation run report source identity mismatch")
		}
	}
	formal, origin, appGenerated, transportAuthenticated, formalExchange := packageProvenance(pkg)
	if result.Classification == model.RecordingResultClassificationFailedLoginGate || result.Classification == model.RecordingResultClassificationFixtureWaiver || result.Classification == model.RecordingResultClassificationVisibleManualReview || result.Classification == model.RecordingResultClassificationServerPreflight {
		formal, formalExchange = false, false
	}
	report := model.ValidationRunReport{
		SchemaVersion:            model.ValidationRunReportSchemaVersion,
		ReportID:                 "validation_run_" + safePathSegment(runID),
		RunID:                    runID,
		PackageID:                pkg.PackageID,
		BundleHashSHA256:         bundleHash,
		PolicyHashSHA256:         policyHash,
		Status:                   validationRunStatus(result.Status),
		FinalDecision:            finalValidationDecision(result.ValidationReports),
		OriginalPackageUnchanged: originalUnchanged,
		FormalAppServerSuccess:   formal && originalUnchanged && result.Status != model.RecordingResultStatusFailed,
		SourceOrigin:             origin,
		AppGenerated:             appGenerated,
		TransportAuthenticated:   transportAuthenticated,
		FormalExchange:           formalExchange,
		ReplayManifestID:         manifest.ManifestID,
		CreatedAt:                result.CreatedAt,
	}
	if result.Status == model.RecordingResultStatusFailed && report.FinalDecision == model.ValidationDecisionContinue {
		report.FinalDecision = model.ValidationDecisionStopAndReport
	}
	if report.CreatedAt.IsZero() {
		report.CreatedAt = timeNowUTC()
	}
	report.ReproducibilityConditions = []string{
		"source_package_id=" + pkg.PackageID,
		"bundle_hash_sha256=" + bundleHash,
		"policy_hash_sha256=" + policyHash,
	}
	for _, key := range []string{"server", "chromium", "video_worker"} {
		if value := strings.TrimSpace(manifestVersion(manifest, key)); value != "" {
			report.ReproducibilityConditions = append(report.ReproducibilityConditions, key+"_version="+value)
		}
	}

	for index, step := range result.StepResults {
		stage := model.ValidationRunStageSummary{NodeID: step.NodeID, Order: index + 1, Status: step.Status}
		for _, manifestStage := range manifest.Stages {
			if manifestStage.NodeID != step.NodeID {
				continue
			}
			stage.StageID = manifestStage.StageID
			stage.Order = manifestStage.Order
			stage.Decision = manifestStage.ValidationDecision
			stage.EvidenceArtifactIDs = append([]string{}, manifestStage.EvidenceArtifactIDs...)
			stage.ActionDefinitionEvidenceIDs = append([]string{}, manifestStage.ActionDefinitionEvidenceIDs...)
			stage.BeforeScreenshotArtifactIDs = append([]string{}, manifestStage.BeforeScreenshotArtifactIDs...)
			stage.AfterScreenshotArtifactIDs = append([]string{}, manifestStage.AfterScreenshotArtifactIDs...)
			stage.StageEventIDs = append([]string{}, manifestStage.StageEventIDs...)
			stage.TraceArtifactIDs = append([]string{}, manifestStage.TraceArtifactIDs...)
			stage.SelectorRepairs = append([]model.ReplayManifestSelectorRepair{}, manifestStage.SelectorRepairs...)
			break
		}
		for _, phaseReport := range result.ValidationReports {
			if phaseReport.NodeID != step.NodeID && phaseReport.StageID != stage.StageID {
				continue
			}
			if stage.StageID == "" {
				stage.StageID = phaseReport.StageID
			}
			if stage.Decision == "" || validationDecisionRank(phaseReport.Decision) > validationDecisionRank(stage.Decision) {
				stage.Decision = phaseReport.Decision
			}
			for _, check := range phaseReport.Checks {
				if !check.Passed && check.Code != "" && !containsValidationRunString(stage.FailureCodes, check.Code) {
					stage.FailureCodes = append(stage.FailureCodes, check.Code)
				}
			}
		}
		if stage.NodeID != "" && stage.StageID != "" {
			report.Stages = append(report.Stages, stage)
		}
	}

	for _, phaseReport := range result.ValidationReports {
		for index := range phaseReport.Checks {
			check := phaseReport.Checks[index]
			if check.Code == "" {
				continue
			}
			if check.Severity == "" {
				if check.Passed {
					check.Severity = model.FindingSeverityInfo
				} else {
					check.Severity = model.FindingSeverityBlocking
				}
			}
			if check.ResponsibilityDomain == "" {
				check.ResponsibilityDomain = model.ValidationCheckDomainServer
			}
			annotated := []model.ValidationCheck{check}
			model.AnnotateValidationChecks(annotated)
			check = annotated[0]
			findingEvidence := append([]model.EvidenceRef{}, check.EvidenceRefs...)
			if len(findingEvidence) == 0 {
				findingEvidence = append(findingEvidence, phaseReport.EvidenceRefs...)
			}
			if len(findingEvidence) == 0 && !check.Passed {
				findingEvidence = []model.EvidenceRef{{ID: "validation_report:" + phaseReport.ReportID, Kind: model.EvidenceKindExecutionRun, Summary: "ValidationReport containing the finding"}}
			}
			finding := model.ValidationRunFinding{
				ID: check.ID, Code: check.Code, Category: model.ValidationFailureCategoryForDomain(check.ResponsibilityDomain),
				Domain: check.ResponsibilityDomain, Severity: check.Severity, Passed: check.Passed,
				NodeID: firstNonEmpty(check.NodeID, phaseReport.NodeID), StageID: firstNonEmpty(check.StageID, phaseReport.StageID),
				ArtifactID: firstEvidenceArtifactID(findingEvidence), Summary: firstNonEmpty(check.Summary, check.Impact, check.Code),
				EvidenceRefs: findingEvidence, RecommendedRepair: firstNonEmpty(check.NextStep, check.Suggestion, "Inspect the referenced evidence and correct the responsible contract or runtime condition."),
				ReproducibleWhen: append([]string{}, report.ReproducibilityConditions...),
			}
			for _, ref := range findingEvidence {
				if !containsValidationRunEvidence(report.EvidenceRefs, ref) {
					report.EvidenceRefs = append(report.EvidenceRefs, ref)
				}
			}
			if !check.Passed || check.Severity == model.FindingSeverityWarning {
				report.Findings = append(report.Findings, finding)
				feedback := model.ValidationFeedback{Code: check.Code, Summary: firstNonEmpty(check.Impact, check.Summary, check.Code), NextStep: firstNonEmpty(check.NextStep, check.Suggestion, finding.RecommendedRepair), EvidenceRefs: findingEvidence}
				if check.ResponsibilityDomain == model.ValidationCheckDomainApp {
					report.AppFeedback = append(report.AppFeedback, feedback)
				} else {
					report.ServerFeedback = append(report.ServerFeedback, feedback)
				}
			}
		}
	}
	if err := report.Validate(); err != nil {
		return model.ValidationRunReport{}, err
	}
	return report, nil
}

// NormalizeValidationRunReportStageIndex restores the report's stage index to
// the authoritative ReplayManifest. Older workers could append validation
// evidence IDs that were not artifact IDs, making an otherwise complete run
// impossible to deliver after restart. Findings and report-level evidence are
// preserved; only the duplicated manifest index is reconciled.
func NormalizeValidationRunReportStageIndex(result *model.RecordingResultPackage, manifest model.ReplayManifest) {
	if result == nil || result.ValidationRunReport == nil {
		return
	}
	byNode := make(map[string]model.ReplayManifestStage, len(manifest.Stages))
	for _, stage := range manifest.Stages {
		byNode[stage.NodeID] = stage
	}
	for index := range result.ValidationRunReport.Stages {
		stage := &result.ValidationRunReport.Stages[index]
		authoritative, ok := byNode[stage.NodeID]
		if !ok {
			continue
		}
		stage.StageID = authoritative.StageID
		stage.Order = authoritative.Order
		stage.Status = authoritative.Status
		stage.Decision = authoritative.ValidationDecision
		stage.EvidenceArtifactIDs = append([]string(nil), authoritative.EvidenceArtifactIDs...)
		stage.ActionDefinitionEvidenceIDs = append([]string(nil), authoritative.ActionDefinitionEvidenceIDs...)
		stage.BeforeScreenshotArtifactIDs = append([]string(nil), authoritative.BeforeScreenshotArtifactIDs...)
		stage.AfterScreenshotArtifactIDs = append([]string(nil), authoritative.AfterScreenshotArtifactIDs...)
		stage.StageEventIDs = append([]string(nil), authoritative.StageEventIDs...)
		stage.TraceArtifactIDs = append([]string(nil), authoritative.TraceArtifactIDs...)
		stage.SelectorRepairs = append([]model.ReplayManifestSelectorRepair(nil), authoritative.SelectorRepairs...)
	}
}

func containsValidationRunEvidence(values []model.EvidenceRef, target model.EvidenceRef) bool {
	for _, value := range values {
		if value.ID != "" && value.ID == target.ID {
			return true
		}
		if value.ID == "" && target.ID == "" && value.ArtifactID != "" && value.ArtifactID == target.ArtifactID {
			return true
		}
	}
	return false
}

func executableHashes(pkg model.ClientExecutionPackage) (string, string) {
	if pkg.ExecutableScriptBundle == nil {
		return "", ""
	}
	return pkg.ExecutableScriptBundle.Reproducibility.BundleHashSHA256, pkg.ExecutableScriptBundle.Reproducibility.BrowserAgentContractHashSHA256
}

func packageProvenance(pkg model.ClientExecutionPackage) (bool, string, bool, bool, bool) {
	origin, _ := pkg.Metadata["origin"].(string)
	appGenerated, _ := pkg.Metadata["app_generated"].(bool)
	transportAuthenticated, _ := pkg.Metadata["transport_authenticated"].(bool)
	formalExchange, _ := pkg.Metadata["formal_exchange"].(bool)
	return origin == "app_formal_exchange" && appGenerated && transportAuthenticated && formalExchange, origin, appGenerated, transportAuthenticated, formalExchange
}

func validationRunStatus(status model.RecordingResultStatus) string {
	if status == model.RecordingResultStatusGenerated || status == model.RecordingResultStatusDelivered || status == model.RecordingResultStatusAcked {
		return "success"
	}
	return "failed"
}

func manifestVersion(manifest model.ReplayManifest, key string) string {
	switch key {
	case "server":
		return manifest.ServerRuntimeVersion
	case "chromium":
		return manifest.BrowserRuntimeVersion
	case "video_worker":
		return manifest.VideoWorkerVersion
	default:
		return ""
	}
}

func firstEvidenceArtifactID(refs []model.EvidenceRef) string {
	for _, ref := range refs {
		if ref.ArtifactID != "" {
			return ref.ArtifactID
		}
	}
	return ""
}

func containsValidationRunString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func validationDecisionRank(value model.ValidationDecision) int {
	switch value {
	case model.ValidationDecisionStopAndReport:
		return 4
	case model.ValidationDecisionReunderstandingRequired:
		return 3
	case model.ValidationDecisionRepairAllowed:
		return 2
	default:
		return 1
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
