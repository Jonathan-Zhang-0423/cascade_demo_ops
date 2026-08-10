package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"cascade-demoops/backend/internal/model"
)

// BuildReplayManifestInput holds all data needed to construct a ReplayManifest.
type BuildReplayManifestInput struct {
	Result    model.RecordingResultPackage
	Events    []model.StageExecutionEvent
	Package   model.ClientExecutionPackage
	RunID     string
	EventDir  string
	Waiver    *BrowserAgentTestWaiver
	CreatedAt time.Time
}

// BuildReplayManifest assembles a ReplayManifest from the completed execution
// data, writes it to {EventDir}/replay-manifest.json, sets ManifestURI, and
// returns the manifest. The file is created regardless of whether the run
// succeeded or failed so that failure scenes can always be reconstructed.
func BuildReplayManifest(input BuildReplayManifestInput) (model.ReplayManifest, error) {
	pkg := input.Package
	result := input.Result

	m := model.ReplayManifest{
		SchemaVersion: model.ReplayManifestSchemaVersion,
		ManifestID:    "manifest_" + safePathSegment(input.RunID),
		RunID:         input.RunID,
		PackageID:     pkg.PackageID,
		CreatedAt:     input.CreatedAt,
	}

	// Hashes — derive from bundle reproducibility when available.
	if pkg.ExecutableScriptBundle != nil {
		m.BundleHashSHA256 = pkg.ExecutableScriptBundle.Reproducibility.BundleHashSHA256
		m.PolicyHashSHA256 = pkg.ExecutableScriptBundle.Reproducibility.PlanHashSHA256
	}
	// Fallback: take hashes from first ValidationReport if not on bundle.
	if m.BundleHashSHA256 == "" || m.PolicyHashSHA256 == "" {
		for _, rpt := range result.ValidationReports {
			if m.BundleHashSHA256 == "" && rpt.SourceBundleHashSHA256 != "" {
				m.BundleHashSHA256 = rpt.SourceBundleHashSHA256
			}
			if m.PolicyHashSHA256 == "" && rpt.PolicyHashSHA256 != "" {
				m.PolicyHashSHA256 = rpt.PolicyHashSHA256
			}
			if m.BundleHashSHA256 != "" && m.PolicyHashSHA256 != "" {
				break
			}
		}
	}

	// Runtime name.
	if pkg.ExecutableScriptBundle != nil {
		m.ExecutionBundleRuntime = model.ExecutableScriptRuntimeBrowserAgentOutlineV1
	}
	if versions := result.AuditTrail.RuntimeVersions; len(versions) > 0 {
		m.ServerRuntimeVersion = versions["server"]
		m.BrowserRuntimeVersion = versions["chromium"]
		m.VideoWorkerVersion = versions["video_worker"]
	}

	// Outcome.
	if result.Status == model.RecordingResultStatusGenerated {
		m.Status = "success"
	} else {
		m.Status = "failed"
	}
	if result.FailureDiagnostic != nil {
		m.FailedNodeID = result.FailureDiagnostic.FailedNodeID
	}

	// Final ValidationDecision: take the most restrictive decision across all reports.
	m.FinalDecision = finalValidationDecision(result.ValidationReports)

	// Validation report index.
	for _, rpt := range result.ValidationReports {
		failCount := 0
		for _, c := range rpt.Checks {
			if !c.Passed {
				failCount++
			}
		}
		m.ValidationReports = append(m.ValidationReports, model.ReplayManifestValidationRef{
			ReportID:   rpt.ReportID,
			Phase:      rpt.Phase,
			Decision:   rpt.Decision,
			CheckCount: len(rpt.Checks),
			FailCount:  failCount,
		})
	}

	// Per-stage summary from StepResults.
	for i, step := range result.StepResults {
		stage := model.ReplayManifestStage{
			NodeID: step.NodeID,
			Order:  i + 1,
			Status: step.Status,
		}
		if step.Error != nil {
			stage.FailureCode = step.Error.Code
		}
		// Observed URL / title from stage events.
		for _, ev := range input.Events {
			if ev.NodeID == step.NodeID && ev.Observation != nil && ev.Observation.URL != "" {
				stage.ObservedURL = ev.Observation.URL
				stage.ObservedTitle = ev.Observation.Title
				break
			}
		}
		// Evidence artifact IDs.
		for _, art := range step.Artifacts {
			stage.EvidenceArtifactIDs = append(stage.EvidenceArtifactIDs, art.ID)
		}
		// Waived flag.
		if input.Waiver != nil {
			for _, n := range input.Waiver.AllowedNodes {
				if n.NodeID == step.NodeID {
					stage.Waived = true
					break
				}
			}
		}
		m.Stages = append(m.Stages, stage)
	}

	// Artifact URIs.
	for _, art := range result.GeneratedAssets {
		switch art.Kind {
		case "demo_video", "mp4":
			if m.MP4URI == "" {
				m.MP4URI = art.URI
			}
		case "raw_recording":
			if m.RawRecordingURI == "" {
				m.RawRecordingURI = art.URI
			}
		case "trace", "playwright_trace", "browser_trace":
			if m.BrowserTraceURI == "" {
				m.BrowserTraceURI = art.URI
			}
		}
	}
	if result.StageEventLogRef != nil {
		m.StageEventLogURI = result.StageEventLogRef.URI
	}

	// Waiver context.
	if input.Waiver != nil {
		m.DevTestOnly = input.Waiver.DevTestOnly
		m.WaiverID = input.Waiver.WaiverID
		m.WaiverBlockedReasons = append([]string{}, input.Waiver.BlockedReasons...)
		for _, n := range input.Waiver.AllowedNodes {
			m.WaiverAllowedNodeIDs = append(m.WaiverAllowedNodeIDs, n.NodeID)
		}
	}

	// Aggregate statistics for dashboards and quick triage (P2.1).
	agg := model.AggregateValidation(result.ValidationReports, result.StepResults)
	m.Aggregate = &agg

	// Write to disk.
	if input.EventDir != "" {
		if err := os.MkdirAll(input.EventDir, 0o700); err != nil {
			return model.ReplayManifest{}, fmt.Errorf("replay manifest: create event dir: %w", err)
		}
		manifestPath := filepath.Join(input.EventDir, "replay-manifest.json")
		data, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			return model.ReplayManifest{}, fmt.Errorf("replay manifest: marshal: %w", err)
		}
		if err := os.WriteFile(manifestPath, append(data, '\n'), 0o600); err != nil {
			return model.ReplayManifest{}, fmt.Errorf("replay manifest: write: %w", err)
		}
		m.ManifestURI = localFileURI(manifestPath)
	}

	return m, nil
}

// finalValidationDecision returns the most restrictive ValidationDecision seen
// across all reports: stop_and_report > reunderstanding > repair_allowed > continue.
func finalValidationDecision(reports []model.ValidationReport) model.ValidationDecision {
	decision := model.ValidationDecisionContinue
	for _, rpt := range reports {
		switch rpt.Decision {
		case model.ValidationDecisionStopAndReport:
			return model.ValidationDecisionStopAndReport
		case model.ValidationDecisionReunderstandingRequired:
			if decision != model.ValidationDecisionStopAndReport {
				decision = model.ValidationDecisionReunderstandingRequired
			}
		case model.ValidationDecisionRepairAllowed:
			if decision == model.ValidationDecisionContinue {
				decision = model.ValidationDecisionRepairAllowed
			}
		}
	}
	return decision
}
