package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"cascade-demoops/backend/internal/finalfilm"
	"cascade-demoops/backend/internal/model"
)

func TestFinalFilmHTTPCreatesAndRendersFactTrackBeforeGenerationApproval(t *testing.T) {
	service := newTestEditorService(t)
	worker := &fakeEditorWorker{}
	service.editorWorker = worker
	finalFilmService, err := finalfilm.NewService(finalfilm.ServiceOptions{
		Store: finalfilm.NewFileStore(filepath.Join(service.runtime.DataRoot, "final-film-http-test")), Renderer: worker,
		OutputRoot: filepath.Join(service.runtime.ArtifactRoot, "final-film-http-test"),
	})
	if err != nil {
		t.Fatal(err)
	}
	service.finalFilm = finalFilmService

	session, err := service.CreateEditorSession(context.Background(), model.EditorCreateSessionRequest{Name: "最终成片"})
	if err != nil {
		t.Fatal(err)
	}
	catalog, plan := finalFilmHTTPFixture(session.SessionID)
	service.editorMu.Lock()
	stored, err := service.loadEditorSession(session.SessionID)
	if err == nil {
		stored.AssetCatalog = catalog
		stored.EditPlan = plan
		stored.Revision = 2
		stored.FinalProfile = model.EditorRenderProfile{Mode: "final", Width: 1920, Height: 1080, FPS: 30, Format: "mp4", Preset: "medium", CRF: 18}
		err = service.saveEditorSessionUnlocked(stored)
	}
	service.editorMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	server := NewDevHTTPServer(service)
	job := editorHTTPValue[model.FinalFilmJob](t, server, "POST", "/v1/final-film/jobs", map[string]any{
		"editor_session_id": session.SessionID, "expected_revision": 2,
		"presentation_generation_intents": []map[string]any{{
			"intent_id": "intro_http", "capability": "presentation_video_candidate", "purpose": "intro", "required": false,
			"reference_asset_refs": []string{"recording_http"},
			"requested_slot":       map[string]any{"preferred_duration_sec": 5, "aspect_ratio": "16:9"},
			"content_policy":       map[string]any{"presentation_only": true, "may_represent_business_step": false, "may_replace_captured_ui": false, "requires_explicit_review": true},
			"failure_policy":       "continue_without_generated_candidate",
		}},
	})
	if job.State != model.FinalFilmJobBaselineReady || job.GenerationAuthorized {
		t.Fatalf("unexpected created job: %+v", job)
	}

	job = editorHTTPValue[model.FinalFilmJob](t, server, "POST", "/v1/final-film/jobs/"+job.JobID+"/render", nil)
	if job.State != model.FinalFilmJobAwaitingGenerationApproval || job.BaselineRender.VideoPath == "" {
		t.Fatalf("baseline was not rendered first: %+v", job)
	}
	if worker.renderRequest.ModelExecution == nil || worker.renderRequest.ModelExecution.Invoked || worker.renderRequest.ModelExecution.ProviderOutputAdopted {
		t.Fatalf("baseline render model audit is unsafe: %+v", worker.renderRequest.ModelExecution)
	}

	events := editorHTTPValue[[]model.FinalFilmEvent](t, server, "GET", "/v1/final-film/jobs/"+job.JobID+"/events", nil)
	if len(events) != 3 || events[2].State != model.FinalFilmJobAwaitingGenerationApproval {
		t.Fatalf("unexpected final film events: %+v", events)
	}
	job = editorHTTPValue[model.FinalFilmJob](t, server, "POST", "/v1/final-film/jobs/"+job.JobID+"/generation-approval", map[string]any{
		"expected_revision": job.Revision, "approved": false, "reason": "用户选择只使用真实录屏",
	})
	if job.State != model.FinalFilmJobCompletedWithoutGenerated || job.FinalRender.VideoPath != job.BaselineRender.VideoPath {
		t.Fatalf("generation rejection did not preserve baseline delivery: %+v", job)
	}
}

func finalFilmHTTPFixture(sessionID string) (model.AssetTimelineCatalog, model.DemoEditPlan) {
	rangeMS := model.MillisecondRange{0, 3000}
	catalog := model.AssetTimelineCatalog{
		SchemaVersion: model.AssetTimelineCatalogSchemaVersion, CatalogID: "catalog_" + sessionID, WorkflowGraphID: "graph_http", GraphVersion: 1, RunID: "run_http",
		Source:      model.AssetTimelineSource{GeneratedAt: time.Now().UTC()},
		Constraints: model.AssetTimelineConstraints{SourceMaterialOnly: true, ProhibitNewImageOrVideoGeneration: true, ScriptIsPrimaryStoryline: true, AllowedEditOperations: append([]model.EditOperationType{}, model.DemoEditAllowedOperations...), ProhibitedPlanKeys: append([]string{}, model.DemoEditProhibitedPlanKeys...)},
		Timeline:    model.AssetTimelineInfo{DurationMS: 3000, RecordingArtifactID: "recording_http"},
		Steps:       []model.TimelineStep{{StepID: "step_http", Order: 1, Action: "show", Status: "passed", Required: true, StartMS: 0, EndMS: 3000, DurationMS: 3000, ExpectedOutcome: "visible", ObservedState: "visible", Artifacts: []string{"recording_http"}}},
		Artifacts:   []model.TimelineArtifact{{ID: "recording_http", Kind: "browser_recording", URI: "file:///recording.mp4", MimeType: "video/mp4", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SizeBytes: 100, IncludeInDemo: true, DurationMS: 3000, LocalPath: filepath.Clean("C:/fixtures/recording.mp4")}},
	}
	plan := model.DemoEditPlan{
		SchemaVersion: model.DemoEditPlanSchemaVersion, PlanID: "plan_http", CatalogID: catalog.CatalogID,
		SourceAuthority: model.DemoEditSourceAuthorityServerLocalEditor, ModelRole: model.DemoEditModelRolePresentationOptimizerOnly,
		SourceMaterialPolicy: model.DemoEditSourceMaterialPolicyExistingAssetsOnly, ScriptOrderPolicy: model.DemoEditScriptOrderPolicyPreserveRequiredStepOrder,
		LockedFields: append([]string{}, model.DemoEditRequiredLockedFields...), ModelEditableFields: append([]string{}, model.DemoEditAllowedModelEditableFields...), TargetDurationMS: 3000,
		Shots: []model.DemoEditShot{{ID: "fact_http", SourceArtifactID: "recording_http", SourceStepID: "step_http", SourceTimeRangeMS: &rangeMS, Purpose: "事实步骤"}},
	}
	return catalog, plan
}
