package directtransport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestFormalResultArtifactContentsValidateStructuredBindings(t *testing.T) {
	source, result, uploaded := formalResultContentFixture(t)
	if err := validateFormalResultArtifactContents(result, source, result.CloudJobID, uploaded); err != nil {
		t.Fatalf("valid formal result content was rejected: %v", err)
	}
}

func TestFormalResultArtifactContentsRejectManifestFromAnotherPackage(t *testing.T) {
	source, result, uploaded := formalResultContentFixture(t)
	record := uploaded["artifact_replay"]
	data, err := os.ReadFile(record.Path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest model.ReplayManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.PackageID = "pkg_other"
	rewriteStructuredArtifact(t, &record, manifest)
	uploaded["artifact_replay"] = record
	if err := validateFormalResultArtifactContents(result, source, result.CloudJobID, uploaded); err == nil {
		t.Fatal("replay manifest from another package was accepted")
	}
}

func TestFormalResultArtifactContentsRejectIncompleteReplayIndex(t *testing.T) {
	source, result, uploaded := formalResultContentFixture(t)
	record := uploaded["artifact_replay"]
	data, err := os.ReadFile(record.Path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest model.ReplayManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.RawRecordingURI = ""
	rewriteStructuredArtifact(t, &record, manifest)
	uploaded["artifact_replay"] = record
	if err := validateFormalResultArtifactContents(result, source, result.CloudJobID, uploaded); err == nil {
		t.Fatal("Replay Manifest that omitted a requested raw recording was accepted")
	}
}

func TestFormalResultArtifactContentsRejectInvalidStageEventLog(t *testing.T) {
	source, result, uploaded := formalResultContentFixture(t)
	record := uploaded["artifact_events"]
	if err := os.WriteFile(record.Path, []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	record.Artifact.SizeBytes = int64(len("not-json\n"))
	record.Artifact.SHA256 = model.SHA256Hex([]byte("not-json\n"))
	uploaded["artifact_events"] = record
	if err := validateFormalResultArtifactContents(result, source, result.CloudJobID, uploaded); err == nil {
		t.Fatal("invalid StageEventLog bytes were accepted")
	}
}

func TestFormalResultArtifactContentsRejectValidationReportHashMismatch(t *testing.T) {
	source, result, uploaded := formalResultContentFixture(t)
	result.ValidationReports[0].SourceBundleHashSHA256 = "wrong_bundle"
	if err := validateFormalResultArtifactContents(result, source, result.CloudJobID, uploaded); err == nil {
		t.Fatal("ValidationReport with a foreign bundle hash was accepted")
	}
}

func TestFormalResultArtifactContentsRejectValidationRunReportForAnotherReplayManifest(t *testing.T) {
	source, result, uploaded := formalResultContentFixture(t)
	result.ValidationRunReport.ReplayManifestID = "manifest_other"
	if err := validateFormalResultArtifactContents(result, source, result.CloudJobID, uploaded); err == nil {
		t.Fatal("ValidationRunReport bound to another ReplayManifest was accepted")
	}
}

func TestValidateDualDeliveryArtifactsRejectsManifestWithSwappedProfiles(t *testing.T) {
	source := model.ClientExecutionPackage{RecordingRunSpec: model.RecordingRunSpec{Outputs: model.RecordingOutputRequest{FinalVideo: true}}}
	preferences := model.DefaultMediaDeliveryPreferences()
	source.ProjectContextSummary.MediaDeliveryPreferences = &preferences
	root := t.TempDir()
	uploaded := map[string]artifactRecord{}
	add := func(id, kind, mime string, data []byte) model.ArtifactRef {
		path := filepath.Join(root, id)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		artifact := model.DirectArtifact{ArtifactID: id, Kind: kind, MimeType: mime, FileName: id, SHA256: model.SHA256Hex(data), SizeBytes: int64(len(data))}
		uploaded[id] = artifactRecord{Artifact: artifact, Path: path}
		return model.ArtifactRef{ID: id, Kind: kind, MimeType: mime, SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes}
	}
	master := add("master", "final_video_final_master_2k", "video/mp4", []byte("master"))
	delivery := add("delivery", "final_video_final_delivery_1080p", "video/mp4", []byte("delivery"))
	hash := model.SHA256Hex([]byte("manifest-entry"))
	document := dualDeliveryManifest{
		SchemaVersion: "demoops.deliverables_manifest.v1", Status: "complete",
		RequiredProfiles: []dualDeliveryProfile{
			{ID: model.MediaOutputProfileMaster2K, Width: 2560, Height: 1440, FPS: 30, Format: "mp4"},
			{ID: model.MediaOutputProfileDelivery1080, Width: 1920, Height: 1080, FPS: 30, Format: "mp4"},
		},
		Deliverables: []dualDeliveryManifestItem{
			{ID: model.MediaOutputProfileMaster2K, Status: "complete", Profile: dualDeliveryProfile{ID: model.MediaOutputProfileMaster2K, Width: 2560, Height: 1440, FPS: 30, Format: "mp4"}, SHA256: hash, SizeBytes: 1},
			{ID: model.MediaOutputProfileDelivery1080, Status: "complete", Profile: dualDeliveryProfile{ID: model.MediaOutputProfileDelivery1080, Width: 1920, Height: 1080, FPS: 30, Format: "mp4"}, SHA256: hash, SizeBytes: 1},
		},
	}
	manifestData, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	manifest := add("manifest", "deliverables_manifest", "application/json", manifestData)
	result := model.RecordingResultPackage{GeneratedAssets: []model.ArtifactRef{master, delivery, manifest}}
	if err := validateDualDeliveryArtifacts(result, source, uploaded); err != nil {
		t.Fatalf("valid dual delivery manifest was rejected: %v", err)
	}
	document.Deliverables[0].Profile = dualDeliveryProfile{ID: model.MediaOutputProfileMaster2K, Width: 1920, Height: 1080, FPS: 30, Format: "mp4"}
	record := uploaded[manifest.ID]
	rewriteStructuredArtifact(t, &record, document)
	uploaded[manifest.ID] = record
	if err := validateDualDeliveryArtifacts(result, source, uploaded); err == nil {
		t.Fatal("dual delivery manifest with swapped profile labels was accepted")
	}
}

func formalResultContentFixture(t *testing.T) (model.ClientExecutionPackage, model.RecordingResultPackage, map[string]artifactRecord) {
	t.Helper()
	source := loadDirectPackageFixture(t)
	bundle := source.ExecutableScriptBundle
	jobID := "job_content_validation"
	now := time.Now().UTC()
	stageByNode := map[string]string{}
	for _, stage := range bundle.StageApprovalPlan.Stages {
		stageByNode[stage.NodeID] = stage.ID
	}
	steps := make([]model.StepResult, 0, len(bundle.PlanJSON.Steps))
	reports := make([]model.ValidationReport, 0, len(bundle.PlanJSON.Steps))
	for index, planStep := range bundle.PlanJSON.Steps {
		steps = append(steps, model.StepResult{NodeID: planStep.NodeID, Status: "passed", DurationMS: 10 + index})
		reports = append(reports, model.ValidationReport{
			SchemaVersion: model.ValidationReportSchemaVersion, ReportID: "report_" + strconv.Itoa(index+1), RunID: source.RecordingRunSpec.RunID,
			SourcePackageID: source.PackageID, SourceBundleHashSHA256: bundle.Reproducibility.BundleHashSHA256, PolicyHashSHA256: bundle.Reproducibility.BrowserAgentContractHashSHA256,
			Phase: model.ValidationPhaseRuntimeStage, NodeID: planStep.NodeID, StageID: stageByNode[planStep.NodeID], Decision: model.ValidationDecisionContinue,
			PassRate: 1, OverallConfidence: 1, EvidenceQuality: model.RuntimeObservationActualBrowser,
			EvidenceRefs: []model.EvidenceRef{{ID: "evidence_" + strconv.Itoa(index+1), ArtifactID: "artifact_screenshot"}}, CreatedAt: now,
		})
	}

	root := t.TempDir()
	uploaded := map[string]artifactRecord{}
	addArtifact := func(id, kind, mime string, data []byte) model.ArtifactRef {
		path := filepath.Join(root, id)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		artifact := model.DirectArtifact{ArtifactID: id, Kind: kind, MimeType: mime, FileName: id, SHA256: model.SHA256Hex(data), SizeBytes: int64(len(data))}
		uploaded[id] = artifactRecord{Artifact: artifact, Path: path}
		return model.ArtifactRef{ID: id, Kind: kind, URI: directResultArtifactURI(jobID, id), MimeType: mime, SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes}
	}
	video := addArtifact("artifact_video", "demo_video", "video/mp4", []byte("video"))
	raw := addArtifact("artifact_raw", "raw_recording", "video/webm", []byte("raw"))
	trace := addArtifact("artifact_trace", "browser_trace", "application/zip", []byte("trace"))
	screenshot := addArtifact("artifact_screenshot", "screenshot", "image/png", []byte("screenshot"))
	catalog := addArtifact("artifact_catalog", "asset_timeline_catalog", "application/json", []byte("{}\n"))
	editPlan := addArtifact("artifact_edit_plan", "demo_edit_plan", "application/json", []byte("{}\n"))

	eventData := make([]byte, 0, 4096)
	sequence := int64(0)
	for _, step := range steps {
		for _, eventType := range []model.StageExecutionEventType{model.StageExecutionEventStageStarted, model.StageExecutionEventOutcomeObserved, model.StageExecutionEventStageCompleted} {
			sequence++
			event := model.StageExecutionEvent{
				SchemaVersion: model.StageExecutionEventSchemaVersion, EventID: "event_" + strconv.FormatInt(sequence, 10), RunID: source.RecordingRunSpec.RunID,
				SourcePackageID: source.PackageID, SourceBundleHashSHA256: bundle.Reproducibility.BundleHashSHA256, PolicyHashSHA256: bundle.Reproducibility.BrowserAgentContractHashSHA256,
				NodeID: step.NodeID, StageID: stageByNode[step.NodeID], Attempt: 1, Sequence: sequence, EventType: eventType, OccurredAt: now.Add(time.Duration(sequence) * time.Millisecond),
			}
			if eventType == model.StageExecutionEventOutcomeObserved {
				event.Observation = &model.RuntimeObservation{Source: model.RuntimeObservationActualBrowser, URL: "https://example.com/project/1", Title: "Completed"}
				event.EvidenceRefs = []model.EvidenceRef{{ID: "runtime_" + step.NodeID, ArtifactID: screenshot.ID}}
			}
			encoded, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			eventData = append(eventData, encoded...)
			eventData = append(eventData, '\n')
		}
	}
	events := addArtifact("artifact_events", "browser_agent_stage_event_log", "application/x-ndjson", eventData)

	result := model.RecordingResultPackage{
		SchemaVersion: model.RecordingResultPackageSchemaVersion, ResultID: "result_content_validation", SourcePackageID: source.PackageID, CloudJobID: jobID,
		Status: model.RecordingResultStatusGenerated, StepResults: steps, ValidationReports: reports,
		ExecutionTrace:  &model.ExecutionTrace{ID: "trace_content", WorkflowGraphID: source.WorkflowGraph.ID, GraphVersion: source.WorkflowGraph.Version, PassRate: 1, StepResults: steps, Artifacts: []model.ArtifactRef{trace, screenshot}},
		GeneratedAssets: []model.ArtifactRef{video, raw, trace, screenshot, catalog, editPlan}, StageEventLogRef: &events,
		Delivery: model.ResultDelivery{RecipientKind: model.ResultRecipientAppInstallation, RecipientKeyID: "direct-lease", EncryptionAlg: model.DirectTransportCryptoSuite, AckRequired: true,
			ResultPackageRef: model.PackageArtifactDescriptor{ID: "result_content_validation", Kind: model.ArtifactKindRecordingResultPackage, URI: "direct://jobs/" + urlEscape(jobID) + "/result", Encrypted: true, Sensitive: true, RecipientKeyID: "direct-lease"}},
	}

	manifest := model.ReplayManifest{
		SchemaVersion: model.ReplayManifestSchemaVersion, ManifestID: "manifest_content", CreatedAt: now,
		RunID: source.RecordingRunSpec.RunID, PackageID: source.PackageID, BundleHashSHA256: bundle.Reproducibility.BundleHashSHA256, PolicyHashSHA256: bundle.Reproducibility.BrowserAgentContractHashSHA256,
		Status: "success", FinalDecision: model.ValidationDecisionContinue, MP4URI: video.URI, RawRecordingURI: raw.URI, BrowserTraceURI: trace.URI,
		StageEventLogURI: events.URI, ManifestURI: directResultArtifactURI(jobID, "artifact_replay"), ProtocolRuntime: model.DirectTransportProtocolVersion,
		ExecutionBundleRuntime: model.ExecutableScriptRuntimeBrowserAgentOutlineV1,
	}
	for index, step := range steps {
		manifest.Stages = append(manifest.Stages, model.ReplayManifestStage{NodeID: step.NodeID, StageID: stageByNode[step.NodeID], Order: index + 1, Status: step.Status, ValidationDecision: model.ValidationDecisionContinue, EvidenceArtifactIDs: []string{screenshot.ID}})
	}
	for _, report := range reports {
		manifest.ValidationReports = append(manifest.ValidationReports, model.ReplayManifestValidationRef{ReportID: report.ReportID, Phase: report.Phase, Decision: report.Decision})
	}
	result.ValidationRunReport = &model.ValidationRunReport{
		SchemaVersion: model.ValidationRunReportSchemaVersion, ReportID: "validation_run_content", CreatedAt: now,
		RunID: source.RecordingRunSpec.RunID, PackageID: source.PackageID,
		BundleHashSHA256: bundle.Reproducibility.BundleHashSHA256, PolicyHashSHA256: bundle.Reproducibility.BrowserAgentContractHashSHA256,
		Status: "success", FinalDecision: model.ValidationDecisionContinue, OriginalPackageUnchanged: true,
		SourceOrigin: "server_controlled_fixture", ReplayManifestID: manifest.ManifestID,
		ReproducibilityConditions: []string{"same package and policy hashes"},
	}
	for _, stage := range manifest.Stages {
		result.ValidationRunReport.Stages = append(result.ValidationRunReport.Stages, model.ValidationRunStageSummary{
			NodeID: stage.NodeID, StageID: stage.StageID, Order: stage.Order, Status: stage.Status,
			Decision: stage.ValidationDecision, EvidenceArtifactIDs: append([]string(nil), stage.EvidenceArtifactIDs...),
		})
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	replay := addArtifact("artifact_replay", "replay_manifest", "application/json", append(manifestData, '\n'))
	result.GeneratedAssets = append(result.GeneratedAssets, replay)
	for _, ref := range append([]model.ArtifactRef{}, result.GeneratedAssets...) {
		result.Delivery.AssetRefs = append(result.Delivery.AssetRefs, model.PackageArtifactDescriptor{ID: ref.ID, Kind: ref.Kind, URI: ref.URI, MimeType: ref.MimeType, SHA256: ref.SHA256, SizeBytes: ref.SizeBytes, Encrypted: true, Sensitive: true, RecipientKeyID: "direct-lease"})
	}
	result.Delivery.AssetRefs = append(result.Delivery.AssetRefs, model.PackageArtifactDescriptor{ID: events.ID, Kind: events.Kind, URI: events.URI, MimeType: events.MimeType, SHA256: events.SHA256, SizeBytes: events.SizeBytes, Encrypted: true, Sensitive: true, RecipientKeyID: "direct-lease"})
	return source, result, uploaded
}

func directResultArtifactURI(jobID, artifactID string) string {
	return "direct://jobs/" + urlEscape(jobID) + "/artifacts/" + urlEscape(artifactID)
}

func urlEscape(value string) string {
	// IDs in these fixtures are already URL-safe; keeping the helper explicit
	// makes the expected Direct URI shape clear without importing App code.
	return value
}

func rewriteStructuredArtifact(t *testing.T, record *artifactRecord, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(record.Path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	record.Artifact.SHA256 = model.SHA256Hex(data)
	record.Artifact.SizeBytes = int64(len(data))
}
