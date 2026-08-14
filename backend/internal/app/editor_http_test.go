package app

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/model"
)

func TestEditorHTTPCreateListAndGetSession(t *testing.T) {
	service := newTestEditorService(t)
	service.editorWorker = &fakeEditorWorker{}
	server := NewDevHTTPServer(service)

	created := editorHTTPValue[model.EditorSession](t, server, http.MethodPost, "/v1/editor/sessions", map[string]any{"name": "产品介绍"})
	if created.Name != "产品介绍" || created.SessionID == "" {
		t.Fatalf("unexpected created session: %+v", created)
	}
	loaded := editorHTTPValue[model.EditorSession](t, server, http.MethodGet, "/v1/editor/sessions/"+created.SessionID, nil)
	if loaded.SessionID != created.SessionID {
		t.Fatalf("loaded session mismatch: %+v", loaded)
	}
	listed := editorHTTPValue[[]model.EditorSession](t, server, http.MethodGet, "/v1/editor/sessions", nil)
	if len(listed) != 1 || listed[0].SessionID != created.SessionID {
		t.Fatalf("unexpected session list: %+v", listed)
	}
}

func TestEditorHTTPPresentationCandidateReviewUsesServiceBoundary(t *testing.T) {
	service := newTestEditorService(t)
	service.editorWorker = &fakeEditorWorker{}
	session, err := service.CreateEditorSession(t.Context(), model.EditorCreateSessionRequest{Name: "审核接口"})
	if err != nil {
		t.Fatal(err)
	}
	service.editorMu.Lock()
	stored, err := service.loadEditorSession(session.SessionID)
	if err == nil {
		stored.AssetCatalog.Artifacts = append(stored.AssetCatalog.Artifacts, model.TimelineArtifact{ID: "candidate_http", Kind: "generated_video_candidate", URI: "file:///candidate.mp4", Metadata: map[string]any{"media_eligible": true, "non_authoritative": true, "presentation_only": true, "source_material_policy": "non_authoritative_generated_candidate"}})
		err = service.saveEditorSessionUnlocked(stored)
	}
	service.editorMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	updated := editorHTTPValue[model.EditorSession](t, NewDevHTTPServer(service), http.MethodPost, "/v1/editor/sessions/"+session.SessionID+"/presentation-candidates/review", map[string]any{"expected_revision": session.Revision, "artifact_id": "candidate_http", "approved": true})
	metadata := updated.AssetCatalog.Artifacts[len(updated.AssetCatalog.Artifacts)-1].Metadata
	if metadata["approved_for_demo"] != true || metadata["approval_mode"] != "explicit_user_review" {
		t.Fatalf("HTTP review did not persist explicit approval: %+v", metadata)
	}
}

func TestEditorHTTPCreateSessionFromResultPackage(t *testing.T) {
	service := newTestEditorService(t)
	sourcePath := filepath.Join(t.TempDir(), "recording.mp4")
	if err := os.WriteFile(sourcePath, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	service.editorWorker = &fakeEditorWorker{probeResult: executor.MediaProbeResult{
		Path: sourcePath, FileName: "recording.mp4", SizeBytes: 7, SHA256: "sha256:fixture", MimeType: "video/mp4", DurationMS: 3000,
	}}
	server := NewDevHTTPServer(service)

	created := editorHTTPValue[model.EditorSession](t, server, http.MethodPost, "/v1/editor/sessions/from-result-package", map[string]any{
		"recording_path": sourcePath,
		"result_package": map[string]any{
			"result_id": "result_http", "source_package_id": "package_1", "cloud_job_id": "job_1",
			"schema_version": "demoops.recording_result_package.v1", "status": "generated",
			"generated_assets": []map[string]any{{"id": "raw_http", "kind": "raw_recording", "uri": "s3://bucket/raw.mp4.enc"}},
			"delivery":         map[string]any{"asset_refs": []map[string]any{{"id": "raw_http", "kind": "video", "uri": "s3://bucket/raw.mp4.enc", "sha256": "sha256:encrypted", "encrypted": true}}},
		},
	})
	if created.AssetCatalog.Source.RecordingResultPackageID != "result_http" || len(created.AssetCatalog.Artifacts) != 1 {
		t.Fatalf("unexpected imported HTTP session: %+v", created)
	}
}

func TestEditorHTTPUploadVideo(t *testing.T) {
	service := newTestEditorService(t)
	worker := &fakeEditorWorker{}
	service.editorWorker = worker
	session, err := service.CreateEditorSession(t.Context(), model.EditorCreateSessionRequest{Name: "上传测试"})
	if err != nil {
		t.Fatal(err)
	}
	worker.probeResult = executor.MediaProbeResult{FileName: "managed.mp4", SizeBytes: 7, SHA256: "sha256:upload", MimeType: "video/mp4", DurationMS: 2500}

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, err := form.CreateFormFile("file", "recording.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("fixture")); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/editor/sessions/"+session.SessionID+"/uploads", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	NewDevHTTPServer(service).Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected HTTP %d: %s", response.Code, response.Body.String())
	}
	var bridge BridgeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &bridge); err != nil || !bridge.OK {
		t.Fatalf("unexpected bridge response: %+v err=%v", bridge, err)
	}
}

func TestEditorHTTPFinalMediaDownloadUsesAttachment(t *testing.T) {
	service := newTestEditorService(t)
	outputPath := filepath.Join(service.runtime.ArtifactRoot, "editor", "demo.mp4")
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath, []byte("video fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	service.editorWorker = &fakeEditorWorker{}
	session, err := service.CreateEditorSession(t.Context(), model.EditorCreateSessionRequest{Name: "download"})
	if err != nil {
		t.Fatal(err)
	}
	session.FinalRender = model.EditorRenderState{Status: model.EditorRenderStatusReady, VideoPath: outputPath}
	if err := service.saveEditorSession(session); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/editor/sessions/"+url.PathEscape(session.SessionID)+"/media/final?download=1", nil)
	response := httptest.NewRecorder()
	NewDevHTTPServer(service).Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Disposition") == "" {
		t.Fatalf("expected download attachment, got status=%d headers=%v", response.Code, response.Header())
	}
}

func TestEditorHTTPStyleReferenceStaysOutsideTimeline(t *testing.T) {
	service := newTestEditorService(t)
	worker := &fakeEditorWorker{}
	service.editorWorker = worker
	session, err := service.CreateEditorSession(t.Context(), model.EditorCreateSessionRequest{Name: "参考视频"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "reference.mp4")
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	worker.probeResult = executor.MediaProbeResult{Path: path, FileName: "reference.mp4", SizeBytes: 7, SHA256: "sha256:reference", MimeType: "video/mp4", DurationMS: 2500}

	updated := editorHTTPValue[model.EditorSession](t, NewDevHTTPServer(service), http.MethodPost, "/v1/editor/sessions/"+session.SessionID+"/style-references/assets", map[string]any{"path": path})
	if len(updated.AssetCatalog.Artifacts) != 1 || len(updated.EditPlan.Shots) != 0 || updated.AssetCatalog.Timeline.DurationMS != 0 {
		t.Fatalf("style reference unexpectedly entered timeline: %+v", updated)
	}
	asset := updated.AssetCatalog.Artifacts[0]
	if asset.Kind != "style_reference_video" || asset.IncludeInDemo || asset.AssetRole != "style_reference_only" {
		t.Fatalf("style reference metadata missing: %+v", asset)
	}

	templates := editorHTTPValue[[]model.VideoStyleTemplate](t, NewDevHTTPServer(service), http.MethodGet, "/v1/editor/style-templates", nil)
	if len(templates) == 0 {
		t.Fatal("expected style templates")
	}
	draft := editorHTTPValue[model.EditorStyleDraft](t, NewDevHTTPServer(service), http.MethodPost, "/v1/editor/sessions/"+session.SessionID+"/style-drafts", map[string]any{
		"expected_revision": updated.Revision, "template_id": templates[0].TemplateID, "reference_asset_id": asset.ID, "reference_rights_confirmed": true,
	})
	if draft.StyleProfile.AnalysisStatus != "pending_analysis" || draft.ProposedEditPlan.PlanID == updated.EditPlan.PlanID {
		t.Fatalf("unexpected style draft: %+v", draft)
	}
}

func editorHTTPValue[T any](t *testing.T, server *DevHTTPServer, method, path string, body any) T {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected HTTP %d: %s", response.Code, response.Body.String())
	}
	var bridge BridgeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &bridge); err != nil {
		t.Fatal(err)
	}
	if !bridge.OK {
		t.Fatalf("bridge error: %s", bridge.Error)
	}
	var value T
	if err := json.Unmarshal(bridge.Data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}
