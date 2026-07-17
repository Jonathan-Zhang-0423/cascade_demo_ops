package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
}

func (f *fakeEditorWorker) ProbeMedia(_ context.Context, _ executor.MediaProbeRequest) (executor.MediaProbeResult, error) {
	return f.probeResult, nil
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

func (f *fakeEditorWorker) Render(_ context.Context, request executor.RenderRequest) (executor.RenderResult, error) {
	f.renderRequest = request
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
