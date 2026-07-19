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
	if session.Revision != 1 || len(session.ProviderCapabilities) != 1 || session.ProviderCapabilities[0].Provider != "seedance" {
		t.Fatalf("unexpected initial editor session: %+v", session)
	}
	if session.EditPlan.Audio == nil || session.EditPlan.Audio.Mode != "source" || session.EditPlan.Audio.VolumePercent != 100 {
		t.Fatalf("unexpected default audio policy: %+v", session.EditPlan.Audio)
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
	service.editorWorker = &fakeEditorWorker{probeResult: executor.MediaProbeResult{
		Path: sourcePath, FileName: "recording.mp4", SizeBytes: 7, SHA256: "sha256:fixture", MimeType: "video/mp4", DurationMS: 7000,
	}}
	startedAt := time.Date(2026, 7, 18, 4, 0, 0, 0, time.UTC)
	result := model.RecordingResultPackage{
		ResultID: "result_editor_1", SourcePackageID: "package_1", CloudJobID: "job_1",
		SchemaVersion: "demoops.recording_result_package.v1", Status: model.RecordingResultStatusGenerated,
		ExecutionTrace: &model.ExecutionTrace{ID: "trace_1", WorkflowGraphID: "graph_1", StartedAt: startedAt},
		StepResults: []model.StepResult{
			{NodeID: "open_dashboard", Status: "passed", StartedAt: startedAt, DurationMS: 2000, ObservedState: "打开仪表盘"},
			{NodeID: "create_project", Status: "passed", StartedAt: startedAt.Add(2500 * time.Millisecond), DurationMS: 3000, ObservedState: "创建项目"},
		},
		GeneratedAssets: []model.ArtifactRef{{ID: "raw_1", Kind: "raw_recording", URI: localFileURI(sourcePath), SHA256: "sha256:fixture"}},
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
	if got := *session.EditPlan.Shots[1].SourceTimeRangeMS; got != (model.MillisecondRange{2500, 5500}) {
		t.Fatalf("second shot range = %+v", got)
	}
	if session.EditPlan.Shots[0].SourceStepID != "open_dashboard" || len(session.EditPlan.Shots[0].Overlays) != 1 || session.EditPlan.Shots[0].Overlays[0].Text != "打开仪表盘" {
		t.Fatalf("step semantics were not retained: %+v", session.EditPlan.Shots[0])
	}
	if sourceID := session.AssetCatalog.Artifacts[0].Metadata["source_artifact_id"]; sourceID != "raw_1" {
		t.Fatalf("source artifact id = %v", sourceID)
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
	audioAnalysis executor.AudioAnalysisResult
	audioRequest  executor.AudioAnalysisRequest
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

func (f *fakeEditorWorker) AnalyzeAudio(_ context.Context, request executor.AudioAnalysisRequest) (executor.AudioAnalysisResult, error) {
	f.audioRequest = request
	result := f.audioAnalysis
	if result.Path == "" {
		result.Path = request.Path
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
