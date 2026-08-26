package directtransport

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strings"

	"cascade-demoops/backend/internal/model"
)

const maxStructuredResultArtifactBytes = 16 << 20

// validateFormalResultArtifactContents verifies that the checksummed JSON/JSONL
// artifacts are semantically bound to the same approved package and run. Hash
// equality alone proves byte integrity, but not that those bytes are a valid
// Replay Manifest or StageEventLog for this job.
func validateFormalResultArtifactContents(result model.RecordingResultPackage, source model.ClientExecutionPackage, jobID string, uploaded map[string]artifactRecord) error {
	if err := validateDirectResultURIBindings(result, jobID, uploaded); err != nil {
		return err
	}
	if result.Status == model.RecordingResultStatusFailed {
		return nil
	}
	if err := validateFormalResultExecutionBindings(result, source, uploaded); err != nil {
		return err
	}
	if err := validateDualDeliveryArtifacts(result, source, uploaded); err != nil {
		return err
	}
	if err := validateStageEventLogContents(result, source, uploaded); err != nil {
		return err
	}
	if source.ExecutableScriptBundle != nil && source.ExecutableScriptBundle.ScriptManifest.Runtime == model.ExecutableScriptRuntimeBrowserAgentOutlineV1 {
		manifest, err := validateReplayManifestContents(result, source, jobID, uploaded)
		if err != nil {
			return err
		}
		if err := validateValidationRunReportContents(result, source, manifest, uploaded); err != nil {
			return err
		}
	}
	return nil
}

func validateDualDeliveryArtifacts(result model.RecordingResultPackage, source model.ClientExecutionPackage, uploaded map[string]artifactRecord) error {
	if !source.RecordingRunSpec.Outputs.FinalVideo || !model.RequiresDualMediaDelivery(&source) {
		return nil
	}
	for _, requirement := range []struct {
		kind string
		mime string
		name string
	}{
		{kind: "final_video_final_master_2k", mime: "video/mp4", name: "2K final MP4"},
		{kind: "final_video_final_delivery_1080p", mime: "video/mp4", name: "1080p final MP4"},
		{kind: "deliverables_manifest", mime: "application/json", name: "deliverables manifest"},
	} {
		artifact, ok := findResultArtifactByKind(result, requirement.kind)
		if !ok {
			return fmt.Errorf("result is missing required %s", requirement.name)
		}
		upload, ok := uploaded[artifact.ID]
		if !ok || !strings.EqualFold(strings.TrimSpace(upload.Artifact.MimeType), requirement.mime) {
			return fmt.Errorf("required %s was not uploaded with the expected MIME type", requirement.name)
		}
	}
	manifest, ok := findResultArtifactByKind(result, "deliverables_manifest")
	if !ok {
		return fmt.Errorf("result is missing required deliverables manifest")
	}
	record, ok := uploaded[manifest.ID]
	if !ok {
		return fmt.Errorf("deliverables manifest upload is missing")
	}
	return validateDualDeliveryManifest(record)
}

type dualDeliveryManifest struct {
	SchemaVersion    string                     `json:"schema_version"`
	Status           string                     `json:"status"`
	RequiredProfiles []dualDeliveryProfile      `json:"required_profiles"`
	Deliverables     []dualDeliveryManifestItem `json:"deliverables"`
}

type dualDeliveryProfile struct {
	ID     string `json:"id"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	FPS    int    `json:"fps"`
	Format string `json:"format"`
}

type dualDeliveryManifestItem struct {
	ID        string              `json:"id"`
	Status    string              `json:"status"`
	Profile   dualDeliveryProfile `json:"profile"`
	SHA256    string              `json:"sha256"`
	SizeBytes int64               `json:"size_bytes"`
}

func validateDualDeliveryManifest(record artifactRecord) error {
	data, err := readStructuredResultArtifact(record)
	if err != nil {
		return fmt.Errorf("deliverables manifest cannot be read: %w", err)
	}
	var manifest dualDeliveryManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("deliverables manifest contains invalid JSON: %w", err)
	}
	if manifest.SchemaVersion != "demoops.deliverables_manifest.v1" || !strings.EqualFold(strings.TrimSpace(manifest.Status), "complete") {
		return fmt.Errorf("deliverables manifest schema or completion status is invalid")
	}
	expected := requiredDualDeliveryManifestProfiles()
	if err := validateDualDeliveryProfileSet(manifest.RequiredProfiles, expected, "required_profiles"); err != nil {
		return err
	}
	if len(manifest.Deliverables) != len(expected) {
		return fmt.Errorf("deliverables manifest must contain exactly %d deliverables", len(expected))
	}
	seen := map[string]bool{}
	for _, item := range manifest.Deliverables {
		if seen[item.ID] {
			return fmt.Errorf("deliverables manifest contains duplicate deliverable %q", safeSegment(item.ID))
		}
		seen[item.ID] = true
		if !strings.EqualFold(strings.TrimSpace(item.Status), "complete") || len(strings.TrimSpace(item.SHA256)) != 64 || item.SizeBytes <= 0 {
			return fmt.Errorf("deliverables manifest deliverable %q is incomplete or lacks checksum metadata", safeSegment(item.ID))
		}
		if err := validateOneDualDeliveryProfile(item.Profile, item.ID, expected, "deliverable"); err != nil {
			return err
		}
	}
	for id := range expected {
		if !seen[id] {
			return fmt.Errorf("deliverables manifest is missing required deliverable %q", id)
		}
	}
	return nil
}

func requiredDualDeliveryManifestProfiles() map[string]dualDeliveryProfile {
	return map[string]dualDeliveryProfile{
		model.MediaOutputProfileMaster2K:     {ID: model.MediaOutputProfileMaster2K, Width: 2560, Height: 1440, FPS: 30, Format: "mp4"},
		model.MediaOutputProfileDelivery1080: {ID: model.MediaOutputProfileDelivery1080, Width: 1920, Height: 1080, FPS: 30, Format: "mp4"},
	}
}

func validateDualDeliveryProfileSet(profiles []dualDeliveryProfile, expected map[string]dualDeliveryProfile, field string) error {
	if len(profiles) != len(expected) {
		return fmt.Errorf("deliverables manifest %s must contain exactly %d profiles", field, len(expected))
	}
	seen := map[string]bool{}
	for _, profile := range profiles {
		if seen[profile.ID] {
			return fmt.Errorf("deliverables manifest %s contains duplicate profile %q", field, safeSegment(profile.ID))
		}
		seen[profile.ID] = true
		if err := validateOneDualDeliveryProfile(profile, profile.ID, expected, field); err != nil {
			return err
		}
	}
	return nil
}

func validateOneDualDeliveryProfile(profile dualDeliveryProfile, id string, expected map[string]dualDeliveryProfile, field string) error {
	want, ok := expected[id]
	if !ok || profile.ID != id || profile.Width != want.Width || profile.Height != want.Height || profile.FPS != want.FPS || !strings.EqualFold(strings.TrimSpace(profile.Format), want.Format) {
		return fmt.Errorf("deliverables manifest %s profile is invalid for %q", field, safeSegment(id))
	}
	return nil
}

func validateDirectResultURIBindings(result model.RecordingResultPackage, jobID string, uploaded map[string]artifactRecord) error {
	expectedURI := func(id string) string {
		return "direct://jobs/" + url.PathEscape(jobID) + "/artifacts/" + url.PathEscape(id)
	}
	for _, ref := range collectDirectResultArtifactRefs(result) {
		if ref.ID == "" {
			continue
		}
		record, ok := uploaded[ref.ID]
		if !ok {
			continue // the checksum/size validator reports the missing upload
		}
		if ref.URI != expectedURI(ref.ID) {
			return fmt.Errorf("artifact %s URI is not bound to the authenticated Direct job", safeSegment(ref.ID))
		}
		if (ref.Kind != "" && !strings.EqualFold(strings.TrimSpace(ref.Kind), strings.TrimSpace(record.Artifact.Kind))) || (ref.MimeType != "" && !strings.EqualFold(strings.TrimSpace(ref.MimeType), strings.TrimSpace(record.Artifact.MimeType))) {
			return fmt.Errorf("artifact %s kind or mime type does not match its uploaded bytes", safeSegment(ref.ID))
		}
	}
	for _, descriptor := range collectDirectResultDescriptors(result) {
		if descriptor.ID == "" {
			continue
		}
		record, ok := uploaded[descriptor.ID]
		if !ok {
			continue
		}
		if descriptor.URI != expectedURI(descriptor.ID) {
			return fmt.Errorf("delivery asset %s URI is not bound to the authenticated Direct job", safeSegment(descriptor.ID))
		}
		if (descriptor.Kind != "" && !strings.EqualFold(strings.TrimSpace(descriptor.Kind), strings.TrimSpace(record.Artifact.Kind))) || (descriptor.MimeType != "" && !strings.EqualFold(strings.TrimSpace(descriptor.MimeType), strings.TrimSpace(record.Artifact.MimeType))) {
			return fmt.Errorf("delivery asset %s kind or mime type does not match its uploaded bytes", safeSegment(descriptor.ID))
		}
	}
	if result.Delivery.ResultPackageRef.URI != "direct://jobs/"+url.PathEscape(jobID)+"/result" {
		return fmt.Errorf("result package URI is not bound to the authenticated Direct job")
	}
	return nil
}

func collectDirectResultArtifactRefs(result model.RecordingResultPackage) []model.ArtifactRef {
	refs := append([]model.ArtifactRef(nil), result.GeneratedAssets...)
	for _, step := range result.StepResults {
		refs = append(refs, step.Artifacts...)
	}
	if result.ExecutionTrace != nil {
		refs = append(refs, result.ExecutionTrace.Artifacts...)
		for _, step := range result.ExecutionTrace.StepResults {
			refs = append(refs, step.Artifacts...)
		}
	}
	if result.StageEventLogRef != nil {
		refs = append(refs, *result.StageEventLogRef)
	}
	return refs
}

func collectDirectResultDescriptors(result model.RecordingResultPackage) []model.PackageArtifactDescriptor {
	refs := append([]model.PackageArtifactDescriptor(nil), result.Delivery.AssetRefs...)
	if result.FailureDiagnostic == nil {
		return refs
	}
	refs = append(refs, result.FailureDiagnostic.ScreenshotRefs...)
	refs = append(refs, result.FailureDiagnostic.TraceRefs...)
	if result.FailureDiagnostic.DOMSnapshotRef != nil {
		refs = append(refs, *result.FailureDiagnostic.DOMSnapshotRef)
	}
	if result.FailureDiagnostic.AccessibilitySnapshotRef != nil {
		refs = append(refs, *result.FailureDiagnostic.AccessibilitySnapshotRef)
	}
	return refs
}

func validateFormalResultExecutionBindings(result model.RecordingResultPackage, source model.ClientExecutionPackage, uploaded map[string]artifactRecord) error {
	bundle := source.ExecutableScriptBundle
	if bundle == nil || bundle.PlanJSON == nil || bundle.StageApprovalPlan == nil {
		return fmt.Errorf("source Browser Agent package is missing its approved execution plan")
	}
	expectedRunID := source.RecordingRunSpec.RunID
	expectedBundleHash := bundle.Reproducibility.BundleHashSHA256
	expectedPolicyHash := bundle.Reproducibility.BrowserAgentContractHashSHA256
	stageByNode := make(map[string]string, len(bundle.StageApprovalPlan.Stages))
	for _, stage := range bundle.StageApprovalPlan.Stages {
		stageByNode[stage.NodeID] = stage.ID
	}
	planNodes := make(map[string]bool, len(bundle.PlanJSON.Steps))
	for _, step := range bundle.PlanJSON.Steps {
		planNodes[step.NodeID] = true
	}
	seenSteps := make(map[string]bool, len(result.StepResults))
	for _, step := range result.StepResults {
		if !planNodes[step.NodeID] || seenSteps[step.NodeID] {
			return fmt.Errorf("result StepResults do not match the approved plan nodes")
		}
		if !strings.EqualFold(strings.TrimSpace(step.Status), "passed") {
			return fmt.Errorf("formal completed result contains a non-passed step %s", safeSegment(step.NodeID))
		}
		seenSteps[step.NodeID] = true
	}
	for nodeID := range planNodes {
		if !seenSteps[nodeID] {
			return fmt.Errorf("formal completed result is missing StepResult for node %s", safeSegment(nodeID))
		}
	}
	for _, report := range result.ValidationReports {
		if report.RunID != expectedRunID || report.SourcePackageID != source.PackageID || report.SourceBundleHashSHA256 != expectedBundleHash || report.PolicyHashSHA256 != expectedPolicyHash {
			return fmt.Errorf("validation report %s is not bound to the approved package hashes and run", safeSegment(report.ReportID))
		}
		if report.Phase == model.ValidationPhaseRuntimeStage && (!planNodes[report.NodeID] || stageByNode[report.NodeID] != report.StageID) {
			return fmt.Errorf("validation report %s stage binding is not in the approved plan", safeSegment(report.ReportID))
		}
		for _, evidence := range report.EvidenceRefs {
			if evidence.ArtifactID != "" {
				if _, ok := uploaded[evidence.ArtifactID]; !ok {
					return fmt.Errorf("validation report %s references an artifact that was not uploaded", safeSegment(report.ReportID))
				}
			}
		}
	}
	return nil
}

func validateStageEventLogContents(result model.RecordingResultPackage, source model.ClientExecutionPackage, uploaded map[string]artifactRecord) error {
	if result.StageEventLogRef == nil {
		return fmt.Errorf("stage event log reference is missing")
	}
	record, ok := uploaded[result.StageEventLogRef.ID]
	if !ok {
		return fmt.Errorf("stage event log upload is missing")
	}
	if record.Path == "" || record.Artifact.SizeBytes <= 0 || record.Artifact.SizeBytes > maxStructuredResultArtifactBytes {
		return fmt.Errorf("stage event log size or path is invalid")
	}
	file, err := os.Open(record.Path)
	if err != nil {
		return fmt.Errorf("stage event log cannot be read: %w", err)
	}
	defer file.Close()

	bundle := source.ExecutableScriptBundle
	stageByNode := make(map[string]string, len(bundle.StageApprovalPlan.Stages))
	for _, stage := range bundle.StageApprovalPlan.Stages {
		stageByNode[stage.NodeID] = stage.ID
	}
	expectedRunID := source.RecordingRunSpec.RunID
	expectedBundleHash := bundle.Reproducibility.BundleHashSHA256
	expectedPolicyHash := bundle.Reproducibility.BrowserAgentContractHashSHA256
	seenEventIDs := map[string]bool{}
	outcomeEvidence := map[string]bool{}
	completedStages := map[string]bool{}
	lastSequence := int64(0)
	count := 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), maxStructuredResultArtifactBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event model.StageExecutionEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return fmt.Errorf("stage event log contains invalid JSON: %w", err)
		}
		if err := event.Validate(); err != nil {
			return fmt.Errorf("stage event log contains an invalid event: %w", err)
		}
		if event.RunID != expectedRunID || event.SourcePackageID != source.PackageID || event.SourceBundleHashSHA256 != expectedBundleHash || event.PolicyHashSHA256 != expectedPolicyHash || stageByNode[event.NodeID] != event.StageID {
			return fmt.Errorf("stage event %s is not bound to the approved package and stage", safeSegment(event.EventID))
		}
		if seenEventIDs[event.EventID] || event.Sequence <= lastSequence {
			return fmt.Errorf("stage event log event identity or sequence is invalid")
		}
		seenEventIDs[event.EventID] = true
		lastSequence = event.Sequence
		count++
		if event.EventType == model.StageExecutionEventOutcomeObserved && event.Observation != nil && event.Observation.Source != model.RuntimeObservationDerivedPlan && event.Observation.Source != model.RuntimeObservationInsufficient {
			for _, evidence := range event.EvidenceRefs {
				if evidence.ArtifactID != "" {
					if _, exists := uploaded[evidence.ArtifactID]; exists {
						outcomeEvidence[event.NodeID] = true
					}
				}
			}
		}
		if event.EventType == model.StageExecutionEventStageCompleted {
			completedStages[event.NodeID] = true
			if model.RuntimeObservationRecordsOptionalCapability(event.Observation) {
				for _, evidence := range event.EvidenceRefs {
					if evidence.ArtifactID != "" {
						if _, exists := uploaded[evidence.ArtifactID]; exists {
							outcomeEvidence[event.NodeID] = true
						}
					}
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("stage event log cannot be parsed: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("stage event log is empty")
	}
	for _, step := range result.StepResults {
		if !outcomeEvidence[step.NodeID] || !completedStages[step.NodeID] {
			return fmt.Errorf("completed stage %s is missing real outcome evidence or its terminal event", safeSegment(step.NodeID))
		}
	}
	return nil
}

func validateReplayManifestContents(result model.RecordingResultPackage, source model.ClientExecutionPackage, jobID string, uploaded map[string]artifactRecord) (model.ReplayManifest, error) {
	manifestRef, ok := findResultArtifactByKind(result, "replay_manifest")
	if !ok {
		return model.ReplayManifest{}, fmt.Errorf("replay manifest reference is missing")
	}
	record, ok := uploaded[manifestRef.ID]
	if !ok {
		return model.ReplayManifest{}, fmt.Errorf("replay manifest upload is missing")
	}
	data, err := readStructuredResultArtifact(record)
	if err != nil {
		return model.ReplayManifest{}, fmt.Errorf("replay manifest cannot be read: %w", err)
	}
	var manifest model.ReplayManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return model.ReplayManifest{}, fmt.Errorf("replay manifest contains invalid JSON: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return model.ReplayManifest{}, err
	}
	bundle := source.ExecutableScriptBundle
	if manifest.DevTestOnly || manifest.WaiverID != "" || len(manifest.WaiverAllowedNodeIDs) > 0 || len(manifest.WaiverBlockedReasons) > 0 {
		return model.ReplayManifest{}, fmt.Errorf("formal replay manifest contains test-waiver fields")
	}
	if manifest.RunID != source.RecordingRunSpec.RunID || manifest.PackageID != source.PackageID || manifest.BundleHashSHA256 != bundle.Reproducibility.BundleHashSHA256 || manifest.PolicyHashSHA256 != bundle.Reproducibility.BrowserAgentContractHashSHA256 {
		return model.ReplayManifest{}, fmt.Errorf("replay manifest is not bound to the approved package hashes and run")
	}
	if manifest.Status != "success" || manifest.ExecutionBundleRuntime != bundle.ScriptManifest.Runtime || manifest.ProtocolRuntime != model.DirectTransportProtocolVersion {
		return model.ReplayManifest{}, fmt.Errorf("replay manifest runtime or success status is invalid")
	}
	expectedManifestURI := "direct://jobs/" + url.PathEscape(jobID) + "/artifacts/" + url.PathEscape(manifestRef.ID)
	if manifest.ManifestURI != expectedManifestURI || result.StageEventLogRef == nil || manifest.StageEventLogURI != result.StageEventLogRef.URI {
		return model.ReplayManifest{}, fmt.Errorf("replay manifest artifact URIs are not bound to this Direct job")
	}
	for _, binding := range []struct {
		uri      string
		kinds    []string
		required bool
	}{
		{manifest.RawRecordingURI, []string{"raw_recording"}, source.RecordingRunSpec.Outputs.RawRecording},
		{manifest.MP4URI, []string{"demo_video", "mp4"}, source.RecordingRunSpec.Outputs.FinalVideo},
		{manifest.BrowserTraceURI, []string{"browser_trace", "execution_trace", "playwright_trace", "trace"}, source.RecordingRunSpec.Outputs.Trace},
	} {
		if binding.uri == "" {
			if binding.required {
				return model.ReplayManifest{}, fmt.Errorf("replay manifest is missing a requested result artifact URI")
			}
			continue
		}
		artifact, exists := findResultArtifactByKind(result, binding.kinds...)
		if !exists || binding.uri != artifact.URI {
			return model.ReplayManifest{}, fmt.Errorf("replay manifest references an artifact outside the result package")
		}
	}
	if manifest.FinalDecision != mostRestrictiveResultDecision(result.ValidationReports) {
		return model.ReplayManifest{}, fmt.Errorf("replay manifest final decision does not match ValidationReports")
	}
	if len(manifest.ValidationReports) != len(result.ValidationReports) || len(manifest.Stages) != len(result.StepResults) {
		return model.ReplayManifest{}, fmt.Errorf("replay manifest report or stage index is incomplete")
	}
	stageByNode := make(map[string]string, len(bundle.StageApprovalPlan.Stages))
	for _, stage := range bundle.StageApprovalPlan.Stages {
		stageByNode[stage.NodeID] = stage.ID
	}
	for index, stage := range manifest.Stages {
		step := result.StepResults[index]
		if stage.NodeID != step.NodeID || stage.StageID != stageByNode[step.NodeID] || stage.Order != index+1 || stage.Status != step.Status {
			return model.ReplayManifest{}, fmt.Errorf("replay manifest stage index does not match StepResults")
		}
		stageReports := make([]model.ValidationReport, 0, 1)
		for _, report := range result.ValidationReports {
			if report.NodeID == stage.NodeID || report.StageID == stage.StageID {
				stageReports = append(stageReports, report)
			}
		}
		if len(stageReports) > 0 && stage.ValidationDecision != mostRestrictiveResultDecision(stageReports) {
			return model.ReplayManifest{}, fmt.Errorf("replay manifest stage decision does not match ValidationReports")
		}
		if len(stage.EvidenceArtifactIDs) == 0 {
			return model.ReplayManifest{}, fmt.Errorf("replay manifest stage is missing outcome evidence artifacts")
		}
		for _, artifactID := range stage.EvidenceArtifactIDs {
			if _, exists := uploaded[artifactID]; !exists {
				return model.ReplayManifest{}, fmt.Errorf("replay manifest stage references an artifact that was not uploaded")
			}
		}
	}
	for index, ref := range manifest.ValidationReports {
		report := result.ValidationReports[index]
		failed := 0
		for _, check := range report.Checks {
			if !check.Passed {
				failed++
			}
		}
		if ref.ReportID != report.ReportID || ref.Phase != report.Phase || ref.Decision != report.Decision || ref.CheckCount != len(report.Checks) || ref.FailCount != failed {
			return model.ReplayManifest{}, fmt.Errorf("replay manifest validation index does not match ValidationReports")
		}
	}
	return manifest, nil
}

// validateValidationRunReportContents binds the inline report to the exact
// uploaded ReplayManifest. This prevents a structurally valid report from a
// different run, package, or stage timeline being delivered with this result.
func validateValidationRunReportContents(result model.RecordingResultPackage, source model.ClientExecutionPackage, manifest model.ReplayManifest, uploaded map[string]artifactRecord) error {
	report := result.ValidationRunReport
	if report == nil {
		return fmt.Errorf("formal outline result is missing validation run report")
	}
	if err := report.Validate(); err != nil {
		return fmt.Errorf("validation run report is invalid: %w", err)
	}
	bundle := source.ExecutableScriptBundle
	if report.RunID != manifest.RunID || report.PackageID != manifest.PackageID || report.BundleHashSHA256 != bundle.Reproducibility.BundleHashSHA256 || report.PolicyHashSHA256 != bundle.Reproducibility.BrowserAgentContractHashSHA256 {
		return fmt.Errorf("validation run report is not bound to the approved package hashes and run")
	}
	if report.ReplayManifestID != manifest.ManifestID || report.Status != manifest.Status || report.FinalDecision != manifest.FinalDecision {
		return fmt.Errorf("validation run report does not match the uploaded replay manifest outcome")
	}
	if len(report.Stages) != len(manifest.Stages) {
		return fmt.Errorf("validation run report stage index does not match the uploaded replay manifest")
	}
	for index, stage := range report.Stages {
		manifestStage := manifest.Stages[index]
		if stage.NodeID != manifestStage.NodeID || stage.StageID != manifestStage.StageID || stage.Order != manifestStage.Order || stage.Status != manifestStage.Status || stage.Decision != manifestStage.ValidationDecision ||
			!reflect.DeepEqual(stage.EvidenceArtifactIDs, manifestStage.EvidenceArtifactIDs) ||
			!reflect.DeepEqual(stage.ActionDefinitionEvidenceIDs, manifestStage.ActionDefinitionEvidenceIDs) ||
			!reflect.DeepEqual(stage.BeforeScreenshotArtifactIDs, manifestStage.BeforeScreenshotArtifactIDs) ||
			!reflect.DeepEqual(stage.AfterScreenshotArtifactIDs, manifestStage.AfterScreenshotArtifactIDs) ||
			!reflect.DeepEqual(stage.StageEventIDs, manifestStage.StageEventIDs) ||
			!reflect.DeepEqual(stage.TraceArtifactIDs, manifestStage.TraceArtifactIDs) ||
			!reflect.DeepEqual(stage.SelectorRepairs, manifestStage.SelectorRepairs) {
			return fmt.Errorf("validation run report stage index does not match the uploaded replay manifest")
		}
		for _, artifactID := range append(append(append([]string{}, stage.EvidenceArtifactIDs...), stage.BeforeScreenshotArtifactIDs...), append(stage.AfterScreenshotArtifactIDs, stage.TraceArtifactIDs...)...) {
			if _, ok := uploaded[artifactID]; !ok {
				return fmt.Errorf("validation run report stage references an artifact that was not uploaded")
			}
		}
	}
	for _, evidence := range append(report.EvidenceRefs, reportFindingEvidenceRefs(report.Findings)...) {
		if evidence.ArtifactID != "" {
			if _, ok := uploaded[evidence.ArtifactID]; !ok {
				return fmt.Errorf("validation run report references an artifact that was not uploaded")
			}
		}
	}
	return nil
}

func reportFindingEvidenceRefs(findings []model.ValidationRunFinding) []model.EvidenceRef {
	refs := make([]model.EvidenceRef, 0)
	for _, finding := range findings {
		refs = append(refs, finding.EvidenceRefs...)
	}
	return refs
}

func findResultArtifactByKind(result model.RecordingResultPackage, kinds ...string) (model.ArtifactRef, bool) {
	refs := append([]model.ArtifactRef(nil), result.GeneratedAssets...)
	if result.ExecutionTrace != nil {
		refs = append(refs, result.ExecutionTrace.Artifacts...)
	}
	for _, descriptor := range result.Delivery.AssetRefs {
		refs = append(refs, model.ArtifactRef{ID: descriptor.ID, Kind: descriptor.Kind, URI: descriptor.URI, MimeType: descriptor.MimeType, SHA256: descriptor.SHA256, SizeBytes: descriptor.SizeBytes})
	}
	for _, ref := range refs {
		for _, kind := range kinds {
			if strings.EqualFold(strings.TrimSpace(ref.Kind), kind) {
				return ref, true
			}
		}
	}
	return model.ArtifactRef{}, false
}

func readStructuredResultArtifact(record artifactRecord) ([]byte, error) {
	if record.Path == "" || record.Artifact.SizeBytes <= 0 || record.Artifact.SizeBytes > maxStructuredResultArtifactBytes {
		return nil, fmt.Errorf("structured artifact size or path is invalid")
	}
	data, err := os.ReadFile(record.Path)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != record.Artifact.SizeBytes {
		return nil, fmt.Errorf("structured artifact size changed after upload")
	}
	return data, nil
}

func mostRestrictiveResultDecision(reports []model.ValidationReport) model.ValidationDecision {
	rank := map[model.ValidationDecision]int{
		model.ValidationDecisionContinue:                0,
		model.ValidationDecisionRepairAllowed:           1,
		model.ValidationDecisionReunderstandingRequired: 2,
		model.ValidationDecisionStopAndReport:           3,
	}
	decision := model.ValidationDecisionContinue
	for _, report := range reports {
		if rank[report.Decision] > rank[decision] {
			decision = report.Decision
		}
	}
	return decision
}
