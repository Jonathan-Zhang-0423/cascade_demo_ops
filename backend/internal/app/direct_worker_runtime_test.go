package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestLocalFileURIRoundTripsThroughDirectWorkerParser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recording", "replay-manifest.json")
	uri := localFileURI(path)
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(uri, "file:///") {
		t.Fatalf("Windows file URI treated the drive as an authority: %q", uri)
	}
	parsed, err := directWorkerLocalPath(uri)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(parsed) != filepath.Clean(path) {
		t.Fatalf("file URI round trip changed the local path: want=%q got=%q uri=%q", path, parsed, uri)
	}
}

func TestFinalizeDirectReplayManifestPublishesAuthenticatedArtifactURIs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "replay-manifest.json")
	manifest := model.ReplayManifest{
		SchemaVersion: model.ReplayManifestSchemaVersion, ManifestID: "manifest_run_1", RunID: "run_1", PackageID: "pkg_1",
		BundleHashSHA256: "bundle_hash", PolicyHashSHA256: "policy_hash", Status: "success", FinalDecision: model.ValidationDecisionContinue,
		ManifestURI: localFileURI(path), MP4URI: "file:///tmp/video.mp4", RawRecordingURI: "file:///tmp/raw.webm", BrowserTraceURI: "file:///tmp/trace.zip",
		StageEventLogURI: "file:///tmp/events.jsonl", ExecutionBundleRuntime: model.ExecutableScriptRuntimeBrowserAgentOutlineV1, CreatedAt: time.Now().UTC(),
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	replay := model.ArtifactRef{ID: "artifact_replay", Kind: "replay_manifest", URI: localFileURI(path), MimeType: "application/json", SHA256: "stale", SizeBytes: 1}
	result := model.RecordingResultPackage{
		GeneratedAssets: []model.ArtifactRef{
			replay,
			{ID: "artifact_video", Kind: "demo_video", URI: "file:///tmp/video.mp4"},
			{ID: "artifact_raw", Kind: "raw_recording", URI: "file:///tmp/raw.webm"},
			{ID: "artifact_trace", Kind: "browser_trace", URI: "file:///tmp/trace.zip"},
		},
		StageEventLogRef: &model.ArtifactRef{ID: "artifact_events", Kind: "browser_agent_stage_event_log", URI: "file:///tmp/events.jsonl"},
		Delivery:         model.ResultDelivery{AssetRefs: []model.PackageArtifactDescriptor{{ID: replay.ID, Kind: replay.Kind, URI: replay.URI, SHA256: replay.SHA256, SizeBytes: replay.SizeBytes}}},
	}
	if err := finalizeDirectReplayManifest(&result, "job_1"); err != nil {
		t.Fatal(err)
	}
	finalData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var final model.ReplayManifest
	if err := json.Unmarshal(finalData, &final); err != nil {
		t.Fatal(err)
	}
	if final.ManifestURI != directWorkerArtifactURI("job_1", "artifact_replay") || final.MP4URI != directWorkerArtifactURI("job_1", "artifact_video") || final.RawRecordingURI != directWorkerArtifactURI("job_1", "artifact_raw") || final.BrowserTraceURI != directWorkerArtifactURI("job_1", "artifact_trace") || final.StageEventLogURI != directWorkerArtifactURI("job_1", "artifact_events") {
		t.Fatalf("manifest retained local or mismatched artifact URIs: %+v", final)
	}
	if final.ProtocolRuntime != model.DirectTransportProtocolVersion || result.GeneratedAssets[0].SHA256 != model.SHA256Hex(finalData) || result.GeneratedAssets[0].SizeBytes != int64(len(finalData)) || result.Delivery.AssetRefs[0].SHA256 != result.GeneratedAssets[0].SHA256 {
		t.Fatalf("manifest checksum, size, protocol, or delivery descriptor was not finalized: manifest=%+v result=%+v", final, result)
	}
}

func TestPrepareDirectWorkerResultRewritesEveryDiagnosticArtifact(t *testing.T) {
	artifact := model.ArtifactRef{ID: "failure-shot", Kind: "screenshot", URI: "file:///var/lib/cascade/jobs/failure.png", MimeType: "image/png", SHA256: "abc", SizeBytes: 42, Metadata: map[string]any{"local_path": "/var/lib/cascade/jobs/failure.png", "render_manifest_path": "/var/lib/cascade/jobs/render_manifest.json"}}
	descriptor := model.PackageArtifactDescriptor{ID: artifact.ID, Kind: "generic_image", URI: artifact.URI, MimeType: artifact.MimeType, SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes, Encrypted: true, Sensitive: true, RecipientKeyID: "local-dev/result-key", Metadata: map[string]any{"dev_local_artifact": true, "local_path": "/var/lib/cascade/jobs/failure.png"}}
	result := model.RecordingResultPackage{
		GeneratedAssets:   []model.ArtifactRef{artifact},
		FailureDiagnostic: &model.ScriptFailureDiagnostic{ScreenshotRefs: []model.PackageArtifactDescriptor{descriptor}, TraceRefs: []model.PackageArtifactDescriptor{descriptor}},
		Delivery:          model.ResultDelivery{ResultPackageRef: model.PackageArtifactDescriptor{ID: "result", Kind: "result_package", URI: "file:///var/lib/cascade/jobs/result.json", RecipientKeyID: "local-dev/result-key", Metadata: map[string]any{"dev_local_artifact": true}}, AssetRefs: []model.PackageArtifactDescriptor{descriptor}},
	}
	prepareDirectWorkerResult(&result, "job_direct_1", []DirectWorkerArtifactFile{{Artifact: artifact, Path: `C:\temp\failure.png`}})
	SanitizeDirectWorkerResultMetadata(&result)
	if got := result.Delivery.AssetRefs[0].Kind; got != artifact.Kind {
		t.Fatalf("delivery descriptor kind was not normalized to the uploaded artifact identity: got %q want %q", got, artifact.Kind)
	}
	result.Delivery.AssetRefs[0].Kind = "generic_image"
	NormalizeDirectWorkerResultDescriptors(&result)
	if got := result.Delivery.AssetRefs[0].Kind; got != artifact.Kind {
		t.Fatalf("persisted delivery descriptor kind was not reconciled: got %q want %q", got, artifact.Kind)
	}
	result.ValidationRunReport = &model.ValidationRunReport{EvidenceRefs: []model.EvidenceRef{{ID: "evidence-1", ArtifactID: "evidence_" + artifact.ID}}}
	NormalizeDirectWorkerEvidenceArtifactIDs(&result)
	if got := result.ValidationRunReport.EvidenceRefs[0].ArtifactID; got != artifact.ID {
		t.Fatalf("semantic evidence ID was not reconciled to the uploaded artifact ID: got %q want %q", got, artifact.ID)
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, forbidden := range []string{"dev_local_artifact", "local-dev/result-key", "file:///var/", "/var/lib/", "local_path", "render_manifest_path"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("formal direct result leaked %q: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, "direct://jobs/job_direct_1/artifacts/failure-shot") || !strings.Contains(text, "direct-lease") {
		t.Fatalf("formal direct result did not bind artifacts to the direct lease: %s", text)
	}
}

func TestDirectWorkerArtifactFilesIncludesStepOnlyEvidenceAndNormalizesBytes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "step-evidence.png")
	data := []byte("actual browser evidence")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	stale := model.ArtifactRef{ID: "step-evidence", Kind: "screenshot", URI: localFileURI(path), MimeType: "image/png", SHA256: "stale", SizeBytes: 1}
	result := model.RecordingResultPackage{StepResults: []model.StepResult{{NodeID: "node", Artifacts: []model.ArtifactRef{stale}}}}
	files, err := directWorkerArtifactFiles(result, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Artifact.ID != stale.ID || files[0].Artifact.SHA256 != model.SHA256Hex(data) || files[0].Artifact.SizeBytes != int64(len(data)) {
		t.Fatalf("step-only evidence was not bound to its actual uploaded bytes: %+v", files)
	}
	prepareDirectWorkerResult(&result, "job_1", files)
	got := result.StepResults[0].Artifacts[0]
	if got.URI != directWorkerArtifactURI("job_1", stale.ID) || got.SHA256 != model.SHA256Hex(data) || got.SizeBytes != int64(len(data)) {
		t.Fatalf("prepared result did not reuse normalized artifact identity: %+v", got)
	}
}
