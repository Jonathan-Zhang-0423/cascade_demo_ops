package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	createdAt := input.CreatedAt
	if createdAt.IsZero() {
		createdAt = timeNowUTC()
	}

	m := model.ReplayManifest{
		SchemaVersion: model.ReplayManifestSchemaVersion,
		ManifestID:    "manifest_" + safePathSegment(input.RunID),
		RunID:         input.RunID,
		PackageID:     pkg.PackageID,
		CreatedAt:     createdAt,
	}

	// Hashes — derive from bundle reproducibility when available.
	if pkg.ExecutableScriptBundle != nil {
		m.BundleHashSHA256 = pkg.ExecutableScriptBundle.Reproducibility.BundleHashSHA256
		m.PolicyHashSHA256 = pkg.ExecutableScriptBundle.Reproducibility.BrowserAgentContractHashSHA256
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
		m.ProtocolRuntime = pkg.ExecutableScriptBundle.ScriptManifest.Runtime
		m.ExecutionBundleRuntime = pkg.ExecutableScriptBundle.ScriptManifest.Runtime
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
	if result.Status == model.RecordingResultStatusFailed && m.FinalDecision == model.ValidationDecisionContinue {
		m.FinalDecision = model.ValidationDecisionStopAndReport
	}

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

	stageByNode := map[string]model.StageApprovalStage{}
	if pkg.ExecutableScriptBundle != nil && pkg.ExecutableScriptBundle.StageApprovalPlan != nil {
		for _, stage := range pkg.ExecutableScriptBundle.StageApprovalPlan.Stages {
			stageByNode[stage.NodeID] = stage
		}
	}
	decisionByNode := map[string]model.ValidationDecision{}
	for _, report := range result.ValidationReports {
		if report.NodeID != "" {
			decisionByNode[report.NodeID] = report.Decision
		}
	}

	// Per-stage summary from StepResults.
	for i, step := range result.StepResults {
		stage := model.ReplayManifestStage{
			NodeID:             step.NodeID,
			Order:              i + 1,
			Status:             step.Status,
			ValidationDecision: decisionByNode[step.NodeID],
		}
		if approved, ok := stageByNode[step.NodeID]; ok {
			stage.StageID = approved.ID
			stage.Order = approved.Order
		}
		if step.Error != nil {
			stage.FailureCode = step.Error.Code
		}
		// Bind the stage ID and most recent observed URL/title from the same
		// immutable event stream used by StageEventLog.
		for _, ev := range input.Events {
			if ev.NodeID != step.NodeID {
				continue
			}
			if stage.StageID == "" {
				stage.StageID = ev.StageID
			}
			if ev.Observation != nil {
				if ev.Observation.URL != "" {
					stage.ObservedURL = ev.Observation.URL
				}
				if ev.Observation.Title != "" {
					stage.ObservedTitle = ev.Observation.Title
				}
			}
			for _, ref := range ev.EvidenceRefs {
				if ref.ArtifactID != "" {
					stage.EvidenceArtifactIDs = appendUniqueReplayString(stage.EvidenceArtifactIDs, ref.ArtifactID)
				}
			}
		}
		// Carry the most restrictive stage decision and the first structured
		// responsibility domain explaining a failed check.
		stageReports := []model.ValidationReport{}
		for _, report := range result.ValidationReports {
			if report.NodeID != step.NodeID && (stage.StageID == "" || report.StageID != stage.StageID) {
				continue
			}
			stageReports = append(stageReports, report)
			if stage.FailureDomain == "" {
				for _, check := range report.Checks {
					if !check.Passed && check.ResponsibilityDomain != "" {
						stage.FailureDomain = check.ResponsibilityDomain
						break
					}
				}
			}
		}
		if len(stageReports) > 0 {
			stage.ValidationDecision = finalValidationDecision(stageReports)
		}
		// Evidence artifact IDs.
		for _, art := range step.Artifacts {
			stage.EvidenceArtifactIDs = appendUniqueReplayString(stage.EvidenceArtifactIDs, art.ID)
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
	artifacts := append([]model.ArtifactRef{}, result.GeneratedAssets...)
	if result.ExecutionTrace != nil {
		artifacts = append(artifacts, result.ExecutionTrace.Artifacts...)
	}
	for _, art := range artifacts {
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

	// Set the URI before serializing so the manifest is self-describing. The
	// local file URI is rewritten to the authenticated Direct artifact URI in
	// the result package before it crosses the Gateway boundary.
	manifestPath := ""
	if input.EventDir != "" {
		if err := os.MkdirAll(input.EventDir, 0o700); err != nil {
			return model.ReplayManifest{}, fmt.Errorf("replay manifest: create event dir: %w", err)
		}
		manifestPath = filepath.Join(input.EventDir, "replay-manifest.json")
		m.ManifestURI = localFileURI(manifestPath)
	}
	if err := m.Validate(); err != nil {
		return model.ReplayManifest{}, fmt.Errorf("replay manifest: validate: %w", err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return model.ReplayManifest{}, fmt.Errorf("replay manifest: marshal: %w", err)
	}
	if manifestPath != "" {
		if err := os.WriteFile(manifestPath, append(data, '\n'), 0o600); err != nil {
			return model.ReplayManifest{}, fmt.Errorf("replay manifest: write: %w", err)
		}
	}

	return m, nil
}

func appendUniqueReplayString(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// AttachReplayManifestArtifact makes the manifest a first-class result
// artifact. It is deliberately shared by the formal Direct runner and the
// local visible runner so both paths expose the same artifact identity,
// checksum, size and editor-facing delivery descriptor.
func AttachReplayManifestArtifact(result *model.RecordingResultPackage, manifest model.ReplayManifest) error {
	if result == nil || strings.TrimSpace(manifest.ManifestURI) == "" {
		return fmt.Errorf("replay manifest artifact requires a persisted manifest URI")
	}
	path, err := directWorkerLocalPath(manifest.ManifestURI)
	if err != nil {
		return fmt.Errorf("replay manifest artifact path %q: %w", manifest.ManifestURI, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("replay manifest artifact read: %w", err)
	}
	artifactID := "replay_manifest_" + safePathSegment(manifest.RunID)
	localOnly := result.Delivery.RecipientKind == "local_test_only"
	metadata := map[string]any{
		"asset_role":      "replay_manifest",
		"role":            "replay_manifest",
		"include_in_demo": false,
	}
	encrypted := !localOnly
	recipientKeyID := result.Delivery.RecipientKeyID
	if localOnly {
		metadata["dev_test_only"] = true
		metadata["not_for_exchange_upload"] = true
		metadata["test_only_waiver"] = true
		if result.Delivery.ResultPackageRef.Metadata != nil {
			if waiverID, ok := result.Delivery.ResultPackageRef.Metadata["waiver_id"]; ok {
				metadata["waiver_id"] = waiverID
			}
		}
		recipientKeyID = ""
	}
	artifact := model.ArtifactRef{
		ID: artifactID, Kind: "replay_manifest", URI: manifest.ManifestURI,
		MimeType: "application/json", SHA256: model.SHA256Hex(data), SizeBytes: int64(len(data)),
		Sensitive: encrypted, Metadata: metadata,
	}
	result.GeneratedAssets = upsertReplayManifestArtifact(result.GeneratedAssets, artifact)
	result.Delivery.AssetRefs = upsertReplayManifestDescriptor(result.Delivery.AssetRefs, model.PackageArtifactDescriptor{
		ID: artifact.ID, Role: "replay_manifest", Kind: artifact.Kind, URI: artifact.URI,
		MimeType: artifact.MimeType, SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes,
		Encrypted: encrypted, Sensitive: encrypted, RecipientKeyID: recipientKeyID, Metadata: metadata,
	})
	return nil
}

func upsertReplayManifestArtifact(values []model.ArtifactRef, artifact model.ArtifactRef) []model.ArtifactRef {
	out := make([]model.ArtifactRef, 0, len(values)+1)
	for _, value := range values {
		if value.ID == artifact.ID || value.Kind == artifact.Kind {
			continue
		}
		out = append(out, value)
	}
	return append(out, artifact)
}

func upsertReplayManifestDescriptor(values []model.PackageArtifactDescriptor, descriptor model.PackageArtifactDescriptor) []model.PackageArtifactDescriptor {
	out := make([]model.PackageArtifactDescriptor, 0, len(values)+1)
	for _, value := range values {
		if value.ID == descriptor.ID || value.Kind == descriptor.Kind {
			continue
		}
		out = append(out, value)
	}
	return append(out, descriptor)
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
