package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/store"
)

func TestEditorSessionImportSaveAndRender(t *testing.T) {
	service := newTestEditorService(t)
	worker := &fakeEditorWorker{}
	service.editorWorker = worker

	session, err := service.CreateEditorSession(t.Context(), model.EditorCreateSessionRequest{Name: "登录演示"})
	if err != nil {
		t.Fatal(err)
	}
	if session.Revision != 1 || len(session.PresentationCapabilities) != 1 || session.PresentationCapabilities[0].Capability != model.PresentationVideoCandidateCapability || len(session.ProviderCapabilities) != 1 || session.ProviderCapabilities[0].Provider != "seedance" {
		t.Fatalf("unexpected initial editor session: %+v", session)
	}
	if session.EditPlan.Audio == nil || session.EditPlan.Audio.Mode != "source" || session.EditPlan.Audio.VolumePercent != 100 {
		t.Fatalf("unexpected default audio policy: %+v", session.EditPlan.Audio)
	}
	if session.FinalProfile.Width != 2560 || session.FinalProfile.Height != 1440 || session.FinalProfile.FPS != 30 || session.FinalProfile.CRF != 18 {
		t.Fatalf("default final editor profile must preserve the canonical 2K evidence-master quality: %+v", session.FinalProfile)
	}

	sourcePath := filepath.Join(t.TempDir(), "recording.mp4")
	if err := os.WriteFile(sourcePath, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	worker.probeResult = executor.MediaProbeResult{
		Path: sourcePath, FileName: "recording.mp4", SizeBytes: 7, SHA256: "sha256:fixture", MimeType: "video/mp4",
		DurationMS: 6000, Width: 1920, Height: 1080, FPS: 30, FFProbeAvailable: true,
	}
	imported, err := service.ImportEditorAsset(t.Context(), session.SessionID, model.EditorImportAssetRequest{Path: sourcePath})
	if err != nil {
		t.Fatal(err)
	}
	if imported.Revision != 2 || len(imported.AssetCatalog.Artifacts) != 1 || len(imported.EditPlan.Shots) != 1 {
		t.Fatalf("unexpected imported session: %+v", imported)
	}
	if got := (*imported.EditPlan.Shots[0].SourceTimeRangeMS)[1]; got != 6000 {
		t.Fatalf("source range end = %d", got)
	}

	start, end := 1000, 4500
	imported.EditPlan.Shots[0].SourceTimeRangeMS = &model.MillisecondRange{start, end}
	imported.EditPlan.Shots[0].Overlays = []model.EditOverlay{{Type: model.EditOverlayCaption, Text: "欢迎使用 Cascade"}}
	saved, err := service.SaveEditorPlan(t.Context(), session.SessionID, model.EditorSavePlanRequest{
		ExpectedRevision: imported.Revision,
		EditPlan:         imported.EditPlan,
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 3 || saved.EditPlan.TargetDurationMS != 3500 {
		t.Fatalf("unexpected saved session: %+v", saved)
	}
	if _, err := service.SaveEditorPlan(t.Context(), session.SessionID, model.EditorSavePlanRequest{ExpectedRevision: 2, EditPlan: saved.EditPlan}); err == nil {
		t.Fatal("expected revision conflict")
	}

	preview, err := service.RenderEditorSession(t.Context(), session.SessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Preview.Status != model.EditorRenderStatusReady || worker.renderRequest.RenderProfile.Mode != "preview" {
		t.Fatalf("unexpected preview result: session=%+v request=%+v", preview.Preview, worker.renderRequest)
	}
	if worker.renderRequest.AssetTimelineCatalog == nil || worker.renderRequest.AssetTimelineCatalog.CatalogID != preview.AssetCatalog.CatalogID {
		t.Fatalf("render request did not preserve asset timeline catalog: %+v", worker.renderRequest.AssetTimelineCatalog)
	}
	if worker.renderRequest.RenderProfile.Width != 1280 || worker.renderRequest.RenderProfile.CRF != 28 {
		t.Fatalf("preview profile not preserved: %+v", worker.renderRequest.RenderProfile)
	}
	loaded, err := service.GetEditorSession(context.Background(), session.SessionID)
	if err != nil || loaded.Preview.VideoPath == "" {
		t.Fatalf("persisted preview missing: session=%+v err=%v", loaded, err)
	}
}

func TestEditorPresentationCandidateRequiresMediaEligibilityAndExplicitUserReview(t *testing.T) {
	service := newTestEditorService(t)
	worker := &fakeEditorWorker{}
	service.editorWorker = worker
	session, err := service.CreateEditorSession(t.Context(), model.EditorCreateSessionRequest{Name: "候选审核"})
	if err != nil {
		t.Fatal(err)
	}
	candidate := model.TimelineArtifact{
		ID: "candidate_1", Kind: "generated_video_candidate", URI: "file:///candidate.mp4", MimeType: "video/mp4", SHA256: "sha", SizeBytes: 10,
		Metadata: map[string]any{"media_eligible": true, "approved_for_demo": false, "presentation_only": true, "non_authoritative": true, "source_material_policy": "non_authoritative_generated_candidate"},
	}
	service.editorMu.Lock()
	stored, err := service.loadEditorSession(session.SessionID)
	if err == nil {
		stored.AssetCatalog.Artifacts = append(stored.AssetCatalog.Artifacts, candidate)
		err = service.saveEditorSessionUnlocked(stored)
	}
	service.editorMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	plan := stored.EditPlan
	plan.Shots = append(plan.Shots, model.DemoEditShot{ID: "candidate_shot", SourceArtifactID: candidate.ID, SourceTimeRangeMS: &model.MillisecondRange{0, 5000}, Operations: []model.EditOperation{{Type: model.EditOperationTrim}}})
	if _, err := service.SaveEditorPlan(t.Context(), session.SessionID, model.EditorSavePlanRequest{ExpectedRevision: stored.Revision, EditPlan: plan}); !errors.Is(err, model.ErrPresentationCandidateExplicitReviewRequired) {
		t.Fatalf("unreviewed candidate should be rejected by plan save, got %v", err)
	}

	reviewed, err := service.ReviewEditorPresentationCandidate(t.Context(), session.SessionID, model.EditorReviewPresentationCandidateRequest{ExpectedRevision: stored.Revision, ArtifactID: candidate.ID, Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	metadata := reviewed.AssetCatalog.Artifacts[len(reviewed.AssetCatalog.Artifacts)-1].Metadata
	if metadata["approved_for_demo"] != true || metadata["approval_mode"] != "explicit_user_review" {
		t.Fatalf("explicit review was not persisted: %+v", metadata)
	}
	if _, err := service.SaveEditorPlan(t.Context(), session.SessionID, model.EditorSavePlanRequest{ExpectedRevision: reviewed.Revision, EditPlan: plan}); err != nil {
		t.Fatalf("explicitly reviewed candidate should be eligible for a manual plan save: %v", err)
	}
}

func TestPresentationCandidateMetadataForEditorRedactsProviderDetails(t *testing.T) {
	metadata := presentationCandidateMetadataForEditor(map[string]any{
		"provider": "private-provider", "model": "private-model", "provider_output_url": "https://private.invalid/out.mp4",
		"endpoint": "https://private.invalid/api", "media_eligible": true, "duration_ms": 5000,
	})
	for _, prohibited := range []string{"provider", "model", "provider_output_url", "endpoint"} {
		if _, ok := metadata[prohibited]; ok {
			t.Fatalf("App-facing candidate metadata leaked %s: %+v", prohibited, metadata)
		}
	}
	if metadata["media_eligible"] != true || metadata["approved_for_demo"] != false || metadata["explicit_review_required"] != true {
		t.Fatalf("safe review metadata missing: %+v", metadata)
	}
}

func TestEditorSessionRejectsDuplicateAsset(t *testing.T) {
	service := newTestEditorService(t)
	worker := &fakeEditorWorker{probeResult: executor.MediaProbeResult{
		Path: filepath.Join(t.TempDir(), "same.mp4"), FileName: "same.mp4", SHA256: "sha256:same", MimeType: "video/mp4", DurationMS: 1000,
	}}
	service.editorWorker = worker
	session, err := service.CreateEditorSession(t.Context(), model.EditorCreateSessionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ImportEditorAsset(t.Context(), session.SessionID, model.EditorImportAssetRequest{Path: worker.probeResult.Path}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ImportEditorAsset(t.Context(), session.SessionID, model.EditorImportAssetRequest{Path: worker.probeResult.Path}); err == nil {
		t.Fatal("expected duplicate asset error")
	}
}

func TestEditorSessionImportsUploadedVideoIntoManagedStorage(t *testing.T) {
	service := newTestEditorService(t)
	worker := &fakeEditorWorker{}
	service.editorWorker = worker
	session, err := service.CreateEditorSession(t.Context(), model.EditorCreateSessionRequest{Name: "上传素材"})
	if err != nil {
		t.Fatal(err)
	}
	worker.probeResult = executor.MediaProbeResult{FileName: "managed.mp4", SizeBytes: 7, SHA256: "sha256:upload", MimeType: "video/mp4", DurationMS: 2500}

	managed, err := service.ImportEditorUpload(t.Context(), session.SessionID, "用户录屏.MP4", strings.NewReader("fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if len(managed.AssetCatalog.Artifacts) != 1 || managed.AssetCatalog.Artifacts[0].Label != "用户录屏.MP4" {
		t.Fatalf("unexpected uploaded asset: %+v", managed.AssetCatalog.Artifacts)
	}
	localPath := managed.AssetCatalog.Artifacts[0].LocalPath
	if filepath.Ext(localPath) != ".mp4" || !pathWithinRoot(localPath, service.editorSessionsRoot()) {
		t.Fatalf("upload is outside managed storage: %s", localPath)
	}
	if payload, readErr := os.ReadFile(localPath); readErr != nil || string(payload) != "fixture" {
		t.Fatalf("uploaded bytes mismatch: %q err=%v", payload, readErr)
	}
}

func TestEditorSessionImportsAudioAsReadOnlyNarrationMaterial(t *testing.T) {
	service := newTestEditorService(t)
	worker := &fakeEditorWorker{}
	service.editorWorker = worker
	session, err := service.CreateEditorSession(t.Context(), model.EditorCreateSessionRequest{Name: "配音素材"})
	if err != nil {
		t.Fatal(err)
	}
	worker.probeResult = executor.MediaProbeResult{FileName: "narration.wav", SizeBytes: 7, SHA256: "sha256:narration", MimeType: "audio/wav", DurationMS: 2500, AudioCodec: "pcm_s16le"}

	imported, err := service.ImportEditorUpload(t.Context(), session.SessionID, "narration.wav", strings.NewReader("fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if len(imported.AssetCatalog.Artifacts) != 1 || len(imported.EditPlan.Shots) != 0 {
		t.Fatalf("audio import must add an asset without adding a video shot: %+v", imported)
	}
	asset := imported.AssetCatalog.Artifacts[0]
	if asset.Kind != "narration_audio" || asset.AssetRole != "narration_audio" || asset.Metadata["presentation_only"] != true {
		t.Fatalf("audio import did not retain narration boundary: %+v", asset)
	}
	if imported.AssetCatalog.Timeline.DurationMS != 0 || imported.AssetCatalog.Timeline.RecordingArtifactID != "" {
		t.Fatalf("audio import must not change the video timeline: %+v", imported.AssetCatalog.Timeline)
	}
	if filepath.Ext(asset.LocalPath) != ".wav" || !pathWithinRoot(asset.LocalPath, service.editorSessionsRoot()) {
		t.Fatalf("audio upload is outside managed storage: %s", asset.LocalPath)
	}
}

func TestEditorStyleTemplatesAndDraftApplyPreserveSourceFacts(t *testing.T) {
	service := newTestEditorService(t)
	worker := &fakeEditorWorker{}
	service.editorWorker = worker

	templates, err := service.ListVideoStyleTemplates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) < 4 || templates[0].TemplateID != "concise_product_demo" || !templates[0].Constraints.ExistingAssetsOnly || !templates[0].Constraints.PreserveRequiredStepOrder {
		t.Fatalf("unexpected built-in templates: %+v", templates)
	}

	sourcePath := filepath.Join(t.TempDir(), "recording.mp4")
	if err := os.WriteFile(sourcePath, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	worker.probeResult = executor.MediaProbeResult{
		Path: sourcePath, FileName: "recording.mp4", SizeBytes: 7, SHA256: "sha256:style-source", MimeType: "video/mp4",
		DurationMS: 9000, Width: 1920, Height: 1080, FPS: 30,
	}
	session, err := service.CreateEditorSession(t.Context(), model.EditorCreateSessionRequest{Name: "风格草案"})
	if err != nil {
		t.Fatal(err)
	}
	session, err = service.ImportEditorAsset(t.Context(), session.SessionID, model.EditorImportAssetRequest{Path: sourcePath})
	if err != nil {
		t.Fatal(err)
	}
	originalShot := session.EditPlan.Shots[0]

	draft, err := service.CreateEditorStyleDraft(t.Context(), session.SessionID, model.EditorStyleDraftRequest{
		ExpectedRevision: session.Revision,
		Prompt:           "做一支节奏快的功能亮点演示",
		TemplateID:       "fast_feature_demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !draft.RequiresConfirmation || draft.Validation == nil || !draft.Validation.Valid || draft.StyleProfile.Source != "platform_template" {
		t.Fatalf("unexpected style draft: %+v", draft)
	}
	if draft.ProposedEditPlan.Shots[0].SourceArtifactID != originalShot.SourceArtifactID || !rangesEqualForEditorTest(draft.ProposedEditPlan.Shots[0].SourceTimeRangeMS, originalShot.SourceTimeRangeMS) {
		t.Fatalf("style draft changed locked source fields: before=%+v after=%+v", originalShot, draft.ProposedEditPlan.Shots[0])
	}
	unchanged, err := service.GetEditorSession(t.Context(), session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Revision != session.Revision || unchanged.EditPlan.PlanID != session.EditPlan.PlanID {
		t.Fatalf("draft creation must not mutate active session: %+v", unchanged)
	}

	applied, err := service.ApplyEditorStyleDraft(t.Context(), session.SessionID, draft.DraftID, model.EditorApplyStyleDraftRequest{ExpectedRevision: session.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Revision != session.Revision+1 || applied.EditPlan.PlanID != draft.ProposedEditPlan.PlanID || applied.EditPlan.GlobalStyle == nil || applied.EditPlan.GlobalStyle.Pacing != "fast_with_result_hold" {
		t.Fatalf("style draft was not applied as expected: %+v", applied)
	}
	if applied.EditPlan.Shots[0].SourceArtifactID != originalShot.SourceArtifactID || !rangesEqualForEditorTest(applied.EditPlan.Shots[0].SourceTimeRangeMS, originalShot.SourceTimeRangeMS) {
		t.Fatalf("style apply changed locked source fields: before=%+v after=%+v", originalShot, applied.EditPlan.Shots[0])
	}
	if applied.EditPlan.Audio == nil || applied.EditPlan.Audio.VolumePercent != 35 {
		t.Fatalf("template audio policy was not applied: %+v", applied.EditPlan.Audio)
	}
	if _, err := service.ApplyEditorStyleDraft(t.Context(), session.SessionID, draft.DraftID, model.EditorApplyStyleDraftRequest{ExpectedRevision: session.Revision}); err == nil {
		t.Fatal("expected stale revision to reject style draft apply")
	}
}

func TestEditorStyleReferenceRequiresRightsAndStaysPendingAnalysis(t *testing.T) {
	service := newTestEditorService(t)
	worker := &fakeEditorWorker{}
	service.editorWorker = worker

	sourcePath := filepath.Join(t.TempDir(), "reference.mp4")
	if err := os.WriteFile(sourcePath, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	worker.probeResult = executor.MediaProbeResult{
		Path: sourcePath, FileName: "reference.mp4", SizeBytes: 7, SHA256: "sha256:style-reference", MimeType: "video/mp4",
		DurationMS: 6000, Width: 1920, Height: 1080, FPS: 30,
	}
	session, err := service.CreateEditorSession(t.Context(), model.EditorCreateSessionRequest{Name: "参考视频"})
	if err != nil {
		t.Fatal(err)
	}
	session, err = service.ImportEditorStyleReference(t.Context(), session.SessionID, model.EditorImportAssetRequest{Path: sourcePath})
	if err != nil {
		t.Fatal(err)
	}
	if len(session.EditPlan.Shots) != 0 || session.AssetCatalog.Timeline.DurationMS != 0 {
		t.Fatalf("style reference must not enter the timeline: %+v", session)
	}
	if session.AssetCatalog.Artifacts[0].Kind != "style_reference_video" || session.AssetCatalog.Artifacts[0].IncludeInDemo {
		t.Fatalf("style reference boundary missing: %+v", session.AssetCatalog.Artifacts[0])
	}
	assetID := session.AssetCatalog.Artifacts[0].ID
	if _, err := service.CreateEditorStyleDraft(t.Context(), session.SessionID, model.EditorStyleDraftRequest{
		ExpectedRevision: session.Revision, ReferenceAssetID: assetID,
	}); err == nil || !strings.Contains(err.Error(), "usage rights") {
		t.Fatalf("expected reference rights rejection, got %v", err)
	}
	draft, err := service.CreateEditorStyleDraft(t.Context(), session.SessionID, model.EditorStyleDraftRequest{
		ExpectedRevision: session.Revision, ReferenceAssetID: assetID, ReferenceRightsConfirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if draft.StyleProfile.AnalysisStatus != "pending_analysis" || draft.StyleProfile.Reference == nil || draft.StyleProfile.Reference.ContentCopied || draft.StyleProfile.Reference.AssetID != assetID {
		t.Fatalf("unexpected pending reference profile: %+v", draft.StyleProfile)
	}
	if !hasStyleWarning(draft.Warnings, "reference_analysis_pending") || !hasStyleWarning(draft.Warnings, "reference_content_not_copied") {
		t.Fatalf("expected reference boundaries in warnings: %+v", draft.Warnings)
	}
}

func rangesEqualForEditorTest(left, right *model.MillisecondRange) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left[0] == right[0] && left[1] == right[1]
}

func hasStyleWarning(values []model.VideoStyleDraftWarning, code string) bool {
	for _, value := range values {
		if value.Code == code {
			return true
		}
	}
	return false
}

func TestEditorSessionRejectsUnsupportedOrOversizedUpload(t *testing.T) {
	service := newTestEditorService(t)
	service.editorWorker = &fakeEditorWorker{}
	session, err := service.CreateEditorSession(t.Context(), model.EditorCreateSessionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ImportEditorUpload(t.Context(), session.SessionID, "payload.exe", strings.NewReader("fixture")); err == nil {
		t.Fatal("expected unsupported upload error")
	}
	oversized := io.LimitReader(zeroReader{}, 17)
	if _, err := service.importEditorUpload(t.Context(), session.SessionID, "too-large.mp4", oversized, 16); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected upload size error, got %v", err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = 0
	}
	return len(buffer), nil
}

func TestEditorSessionFromResultPackageBuildsStepTimeline(t *testing.T) {
	service := newTestEditorService(t)
	sourcePath := filepath.Join(t.TempDir(), "recording.mp4")
	if err := os.WriteFile(sourcePath, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	screenshotPath := filepath.Join(filepath.Dir(sourcePath), "open-dashboard.png")
	if err := os.WriteFile(screenshotPath, []byte("png-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	service.editorWorker = &fakeEditorWorker{probeResult: executor.MediaProbeResult{
		Path: sourcePath, FileName: "recording.mp4", SizeBytes: 7, SHA256: "sha256:fixture", MimeType: "video/mp4", DurationMS: 7000,
	}}
	startedAt := time.Date(2026, 7, 18, 4, 0, 0, 0, time.UTC)
	result := model.RecordingResultPackage{
		ResultID: "result_editor_1", SourcePackageID: "package_1", CloudJobID: "job_1",
		SchemaVersion: "demoops.recording_result_package.v1", Status: model.RecordingResultStatusGenerated,
		ExecutionRuntime: model.ExecutableScriptRuntimeBrowserAgentOutlineV1,
		ValidationReports: []model.ValidationReport{{
			SchemaVersion: model.ValidationReportSchemaVersion, ReportID: "report_1", RunID: "run_1",
			SourcePackageID: "package_1", SourceBundleHashSHA256: "bundle_hash", PolicyHashSHA256: "policy_hash",
			Phase: model.ValidationPhaseRuntimeStage, NodeID: "open_dashboard", StageID: "stage_open_dashboard",
			Decision: model.ValidationDecisionContinue, PassRate: 1, OverallConfidence: .96,
			EvidenceQuality: model.RuntimeObservationActualBrowser, EvidenceRefs: []model.EvidenceRef{{ID: "screenshot_open"}},
			CreatedAt: startedAt.Add(2 * time.Second),
		}},
		PatchLedger: []model.RuntimePatchLedgerEntry{{
			SchemaVersion: model.RuntimePatchLedgerEntrySchemaVersion, EntryID: "patch_1", ProposalID: "proposal_1",
			RunID: "run_1", NodeID: "open_dashboard", StageID: "stage_open_dashboard", Attempt: 1,
			SourceBundleHashSHA256: "bundle_hash", PolicyHashSHA256: "policy_hash",
			Field: "script_outline.stages[0].interactions[0].selector", Before: "#old", After: "#new",
			PolicyDecision: model.ValidationDecisionRepairAllowed, Applied: true, AppliedAt: startedAt.Add(time.Second),
		}},
		StageEventLogRef: &model.ArtifactRef{ID: "event_log_1", Kind: "stage_event_log", URI: "file:///events.jsonl"},
		ExecutionTrace:   &model.ExecutionTrace{ID: "trace_1", WorkflowGraphID: "graph_1", StartedAt: startedAt},
		StepResults: []model.StepResult{
			{NodeID: "open_dashboard", Status: "passed", StartedAt: startedAt, DurationMS: 2000, ObservedState: "打开仪表盘", Artifacts: []model.ArtifactRef{{ID: "screenshot_open", Kind: "screenshot", URI: localFileURI(screenshotPath), MimeType: "image/png", SHA256: "sha256:screenshot", Metadata: map[string]any{"include_in_demo": true}}}},
			{NodeID: "create_project", Status: "passed", StartedAt: startedAt.Add(2500 * time.Millisecond), DurationMS: 3000, ObservedState: "创建项目"},
		},
		GeneratedAssets: []model.ArtifactRef{{ID: "raw_1", Kind: "raw_recording", URI: localFileURI(sourcePath), SHA256: "sha256:fixture"}},
	}
	if path, err := resolveResultReferenceArtifact(&result, model.EditorCreateFromResultPackageRequest{}, result.StepResults[0].Artifacts[0]); err != nil || path != screenshotPath {
		t.Fatalf("screenshot reference did not resolve locally: path=%q err=%v", path, err)
	}

	session, err := service.CreateEditorSessionFromResultPackage(t.Context(), model.EditorCreateFromResultPackageRequest{ResultPackage: &result})
	if err != nil {
		t.Fatal(err)
	}
	if session.AssetCatalog.Source.RecordingResultPackageID != result.ResultID || session.AssetCatalog.Source.ExecutionTraceID != "trace_1" {
		t.Fatalf("result package provenance missing: %+v", session.AssetCatalog.Source)
	}
	if session.AssetCatalog.WorkflowGraphID != "graph_1" || len(session.AssetCatalog.Steps) != 2 || len(session.EditPlan.Shots) != 2 {
		t.Fatalf("unexpected imported timeline: %+v", session.AssetCatalog)
	}
	for _, step := range session.AssetCatalog.Steps {
		if len(step.Artifacts) != 1 || step.Artifacts[0] != session.AssetCatalog.Artifacts[0].ID {
			t.Fatalf("timeline step %s must bind the captured recording, not evidence attachments: %+v", step.StepID, step.Artifacts)
		}
	}
	if got := *session.EditPlan.Shots[1].SourceTimeRangeMS; got != (model.MillisecondRange{2500, 5500}) {
		t.Fatalf("second shot range = %+v", got)
	}
	if session.EditPlan.Shots[0].SourceStepID != "open_dashboard" || len(session.EditPlan.Shots[0].Overlays) != 1 || session.EditPlan.Shots[0].Overlays[0].Text != "打开仪表盘" {
		t.Fatalf("step semantics were not retained: %+v", session.EditPlan.Shots[0])
	}
	if sourceID := session.AssetCatalog.Artifacts[0].Metadata["source_artifact_id"]; sourceID != "raw_1" {
		t.Fatalf("source artifact id = %v", sourceID)
	}
	if len(session.AssetCatalog.Artifacts) != 2 || session.AssetCatalog.Artifacts[1].Kind != "step_screenshot" || session.AssetCatalog.Artifacts[1].SourceStepID != "open_dashboard" {
		t.Fatalf("step screenshot was not registered as a presentation material: %+v", session.AssetCatalog.Artifacts)
	}
	if session.Automation == nil || session.Automation.ExecutionRuntime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 || session.Automation.ValidationState != "verified" || session.Automation.EvidenceBackedReportCount != 1 || session.Automation.AppliedPatchCount != 1 || !session.Automation.StageEventAuditAvailable {
		t.Fatalf("browser-agent automation provenance was not materialized: %+v", session.Automation)
	}
}

func TestEnsureEditorSessionFromResultPackageReusesResultHandoff(t *testing.T) {
	service := newTestEditorService(t)
	sourcePath := filepath.Join(t.TempDir(), "recording.mp4")
	if err := os.WriteFile(sourcePath, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	service.editorWorker = &fakeEditorWorker{probeResult: executor.MediaProbeResult{
		Path: sourcePath, FileName: "recording.mp4", SizeBytes: 7, SHA256: "sha256:fixture", MimeType: "video/mp4", DurationMS: 3000,
	}}
	result := model.RecordingResultPackage{
		ResultID: "result_editor_handoff", SourcePackageID: "package_handoff", CloudJobID: "job_handoff",
		SchemaVersion: "demoops.recording_result_package.v1", Status: model.RecordingResultStatusGenerated,
		GeneratedAssets: []model.ArtifactRef{{ID: "raw_handoff", Kind: "raw_recording", URI: localFileURI(sourcePath), SHA256: "sha256:fixture"}},
	}
	first, created, err := service.EnsureEditorSessionFromResultPackage(t.Context(), model.EditorCreateFromResultPackageRequest{ResultPackage: &result})
	if err != nil || !created {
		t.Fatalf("expected first handoff to create a session: session=%+v created=%v err=%v", first, created, err)
	}
	second, created, err := service.EnsureEditorSessionFromResultPackage(t.Context(), model.EditorCreateFromResultPackageRequest{ResultPackage: &result})
	if err != nil || created || second.SessionID != first.SessionID {
		t.Fatalf("expected repeated handoff to reuse the session: first=%+v second=%+v created=%v err=%v", first, second, created, err)
	}
}

func TestNormalizeEditorFactTrackBindingsDropsEvidenceAttachments(t *testing.T) {
	session := model.EditorSession{AssetCatalog: model.AssetTimelineCatalog{
		Timeline: model.AssetTimelineInfo{RecordingArtifactID: "asset_recording"},
		Steps: []model.TimelineStep{
			{StepID: "required", Required: true, Artifacts: []string{"screenshot", "visual-observation-json"}},
			{StepID: "optional", Required: false, Artifacts: []string{"screenshot"}},
		},
	}}
	if !normalizeEditorFactTrackBindings(&session) {
		t.Fatal("legacy evidence attachment bindings were not repaired")
	}
	if got := session.AssetCatalog.Steps[0].Artifacts; len(got) != 1 || got[0] != "asset_recording" {
		t.Fatalf("required fact-track source binding=%v", got)
	}
	if got := session.AssetCatalog.Steps[1].Artifacts; len(got) != 1 || got[0] != "screenshot" {
		t.Fatalf("optional evidence-only step was unexpectedly rewritten: %v", got)
	}
}

func TestMaterializeEditorSessionFromResultPackageUsesOnlyArtifactRoot(t *testing.T) {
	service := newTestEditorService(t)
	recordingPath := filepath.Join(service.runtime.ArtifactRoot, "exchange", "job_1", "recording.webm")
	if err := os.MkdirAll(filepath.Dir(recordingPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recordingPath, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	service.editorWorker = &fakeEditorWorker{probeResult: executor.MediaProbeResult{
		Path: recordingPath, FileName: "recording.webm", SizeBytes: 7, SHA256: "sha256:fixture", MimeType: "video/webm", DurationMS: 3000,
	}}
	result := model.RecordingResultPackage{
		ResultID: "result_auto_editor", SourcePackageID: "package_auto_editor", CloudJobID: "job_auto_editor",
		SchemaVersion: model.RecordingResultPackageSchemaVersion, Status: model.RecordingResultStatusGenerated,
		GeneratedAssets: []model.ArtifactRef{{ID: "raw_auto_editor", Kind: "raw_recording", URI: localFileURI(recordingPath), SHA256: "sha256:fixture"}},
	}
	materialized, err := service.MaterializeEditorSessionFromResultPackage(t.Context(), result)
	if err != nil || !materialized.Ready || !materialized.Created || materialized.SessionID == "" {
		t.Fatalf("expected a Server-owned recording to enter editor: %+v err=%v", materialized, err)
	}
	failed := result
	failed.Status = model.RecordingResultStatusFailed
	failed.ResultID = "result_auto_editor_failed"
	blocked, err := service.MaterializeEditorSessionFromResultPackage(t.Context(), failed)
	if err != nil || blocked.Ready || blocked.SessionID != "" {
		t.Fatalf("failed results must not create editor sessions: %+v err=%v", blocked, err)
	}
	outside := result
	outside.ResultID = "result_outside_editor"
	outside.GeneratedAssets[0].URI = localFileURI(filepath.Join(t.TempDir(), "outside.webm"))
	notReady, err := service.MaterializeEditorSessionFromResultPackage(t.Context(), outside)
	if err != nil || notReady.Ready {
		t.Fatalf("paths outside artifact root must not be auto-imported: %+v err=%v", notReady, err)
	}
}

func TestEditorSessionFromEncryptedResultRequiresLocalRecording(t *testing.T) {
	service := newTestEditorService(t)
	worker := &fakeEditorWorker{}
	service.editorWorker = worker
	result := model.RecordingResultPackage{
		ResultID: "result_encrypted", SourcePackageID: "package_1", CloudJobID: "job_1",
		SchemaVersion: "demoops.recording_result_package.v1", Status: model.RecordingResultStatusGenerated,
		GeneratedAssets: []model.ArtifactRef{{ID: "raw_encrypted", Kind: "raw_recording", URI: "s3://bucket/raw.webm.enc"}},
		Delivery:        model.ResultDelivery{AssetRefs: []model.PackageArtifactDescriptor{{ID: "raw_encrypted", Kind: "video", URI: "s3://bucket/raw.webm.enc", SHA256: "sha256:encrypted", Encrypted: true}}},
	}

	if _, err := service.CreateEditorSessionFromResultPackage(t.Context(), model.EditorCreateFromResultPackageRequest{ResultPackage: &result}); err == nil || !strings.Contains(err.Error(), "downloaded and decrypted local path") {
		t.Fatalf("expected encrypted recording error, got %v", err)
	}

	sourcePath := filepath.Join(t.TempDir(), "decrypted.webm")
	if err := os.WriteFile(sourcePath, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	worker.probeResult = executor.MediaProbeResult{Path: sourcePath, FileName: "decrypted.webm", SizeBytes: 7, SHA256: "sha256:decrypted", MimeType: "video/webm", DurationMS: 4000}
	session, err := service.CreateEditorSessionFromResultPackage(t.Context(), model.EditorCreateFromResultPackageRequest{ResultPackage: &result, RecordingPath: sourcePath})
	if err != nil {
		t.Fatal(err)
	}
	if len(session.AssetCatalog.Artifacts) != 1 || session.AssetCatalog.Artifacts[0].LocalPath != sourcePath {
		t.Fatalf("decrypted recording was not imported: %+v", session.AssetCatalog.Artifacts)
	}
}

func TestEditorRenderJobCompletesInBackground(t *testing.T) {
	service := newTestEditorService(t)
	worker := &fakeEditorWorker{}
	service.editorWorker = worker
	session := createRenderableEditorSession(t, service, worker)

	started, err := service.StartEditorRender(session.SessionID, false)
	if err != nil {
		t.Fatal(err)
	}
	if started.FinalRender.Status != model.EditorRenderStatusRunning || started.FinalRender.JobID == "" || started.FinalRender.Phase != "queued" {
		t.Fatalf("unexpected initial render job: %+v", started.FinalRender)
	}
	completed := waitForEditorRender(t, service, session.SessionID, false, model.EditorRenderStatusReady)
	if completed.FinalRender.Progress != 100 || completed.FinalRender.Phase != "completed" || completed.FinalRender.VideoPath == "" {
		t.Fatalf("unexpected completed render job: %+v", completed.FinalRender)
	}
}

func TestEditorRenderJobCancellationPropagatesContext(t *testing.T) {
	service := newTestEditorService(t)
	worker := &fakeEditorWorker{renderStarted: make(chan struct{}), blockRender: true}
	service.editorWorker = worker
	session := createRenderableEditorSession(t, service, worker)

	if _, err := service.StartEditorRender(session.SessionID, true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-worker.renderStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("render worker did not start")
	}
	cancelling, err := service.CancelEditorRender(session.SessionID, "preview")
	if err != nil {
		t.Fatal(err)
	}
	if !cancelling.Preview.CancelRequested || cancelling.Preview.Phase != "cancelling" {
		t.Fatalf("cancel request not persisted: %+v", cancelling.Preview)
	}
	cancelled := waitForEditorRender(t, service, session.SessionID, true, model.EditorRenderStatusCancelled)
	if cancelled.Preview.Phase != "cancelled" || cancelled.Status != model.EditorSessionStatusEditing {
		t.Fatalf("unexpected cancelled render: %+v", cancelled.Preview)
	}
}

func createRenderableEditorSession(t *testing.T, service *Service, worker *fakeEditorWorker) model.EditorSession {
	t.Helper()
	sourcePath := filepath.Join(t.TempDir(), "recording.mp4")
	if err := os.WriteFile(sourcePath, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	worker.probeResult = executor.MediaProbeResult{Path: sourcePath, FileName: "recording.mp4", SizeBytes: 7, SHA256: "sha256:fixture", MimeType: "video/mp4", DurationMS: 3000}
	session, err := service.CreateEditorSession(t.Context(), model.EditorCreateSessionRequest{Name: "后台渲染测试"})
	if err != nil {
		t.Fatal(err)
	}
	session, err = service.ImportEditorAsset(t.Context(), session.SessionID, model.EditorImportAssetRequest{Path: sourcePath})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func waitForEditorRender(t *testing.T, service *Service, sessionID string, preview bool, wanted model.EditorRenderStatus) model.EditorSession {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		session, err := service.GetEditorSession(t.Context(), sessionID)
		if err != nil {
			t.Fatal(err)
		}
		if editorRenderState(session, preview).Status == wanted {
			return session
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("render did not reach %s", wanted)
	return model.EditorSession{}
}

func newTestEditorService(t *testing.T) *Service {
	t.Helper()
	root := t.TempDir()
	service, err := NewService(config.AppRuntimeConfig{
		Profile: config.ProfileDev, Environment: "test", Mode: model.AppModeDesktop,
		DatabaseDialect: config.DatabaseSQLite, SQLitePath: filepath.Join(root, "cascade.db"),
		DataRoot: root, ArtifactRoot: filepath.Join(root, "artifacts"), CacheRoot: filepath.Join(root, "cache"), LogRoot: filepath.Join(root, "logs"),
		ResourceRoot: root, DevRepoRoot: root, SidecarPaths: map[string]string{}, LLMMode: config.LLMModeDeterministic,
		ArkMediaMode: config.ArkMediaModeDryRun,
		ModelProviders: map[config.ModelProvider]config.ModelProviderCredential{
			config.ModelProviderSeedance: {Provider: config.ModelProviderSeedance, Enabled: true, DefaultModel: "doubao-seedance-2-0-260128"},
		},
	}, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type fakeEditorWorker struct {
	probeResult   executor.MediaProbeResult
	validation    model.DemoEditPlanValidationReport
	renderRequest executor.RenderRequest
	renderErr     error
	renderStarted chan struct{}
	blockRender   bool
}

func (f *fakeEditorWorker) ProbeMedia(_ context.Context, request executor.MediaProbeRequest) (executor.MediaProbeResult, error) {
	result := f.probeResult
	if result.Path == "" {
		result.Path = request.Path
	}
	if result.FileName == "" {
		result.FileName = filepath.Base(request.Path)
	}
	return result, nil
}

func (f *fakeEditorWorker) ValidateEditPlan(_ context.Context, request executor.EditPlanValidationRequest) (model.DemoEditPlanValidationReport, error) {
	if f.validation.SchemaVersion != "" {
		return f.validation, nil
	}
	return model.DemoEditPlanValidationReport{
		SchemaVersion: model.DemoEditPlanValidationSchemaVersion, Valid: len(request.EditPlan.Shots) > 0,
		CheckedAt: time.Now().UTC(), PlanID: request.EditPlan.PlanID, Errors: []model.DemoEditPlanValidationFinding{}, Warnings: []model.DemoEditPlanValidationFinding{},
	}, nil
}

func (f *fakeEditorWorker) Render(ctx context.Context, request executor.RenderRequest) (executor.RenderResult, error) {
	f.renderRequest = request
	if f.renderStarted != nil {
		close(f.renderStarted)
		f.renderStarted = nil
	}
	if f.blockRender {
		<-ctx.Done()
		return executor.RenderResult{}, ctx.Err()
	}
	if f.renderErr != nil {
		return executor.RenderResult{}, f.renderErr
	}
	if request.RenderProfile == nil {
		return executor.RenderResult{}, errors.New("render profile is required")
	}
	return executor.RenderResult{
		VideoPath: filepath.Join(request.OutputDir, "demo.mp4"), RenderManifestPath: filepath.Join(request.OutputDir, "render_manifest.json"),
	}, nil
}
