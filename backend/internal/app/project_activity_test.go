package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/model"
)

func TestProjectActivityHTTPRoutes(t *testing.T) {
	service := newTestEditorService(t)
	_, err := service.AppendProjectActivity(context.Background(), model.ProjectActivityEvent{
		ProjectID: "project_http", RunID: "run_http", Mode: model.ProjectCanvasModeAct,
		Kind: "plan", Status: model.ProjectActivityRunning, Title: "Writing execution plan",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewDevHTTPServer(service)
	for _, path := range []string{
		"/v1/desktop/projects/project_http/activity-state",
		"/v1/desktop/projects/project_http/activity-events?after=0",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", path, response.Code, response.Body.String())
		}
		var payload struct {
			OK   bool            `json:"ok"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || !payload.OK || len(payload.Data) == 0 {
			t.Fatalf("unexpected activity response for %s: %s", path, response.Body.String())
		}
	}
}

func TestProjectActivityStorePersistsStateAndCursor(t *testing.T) {
	root := t.TempDir()
	store := newProjectActivityStore(root)
	ctx := context.Background()
	first, err := store.append(ctx, model.ProjectActivityEvent{
		ProjectID: "project_1", RunID: "run_1", Mode: model.ProjectCanvasModeBrowser,
		Kind: "recording", Status: model.ProjectActivityRunning, Title: "Recording approved path",
		Browser: &model.ProjectBrowserActivity{URL: "https://example.com/path?token=secret#part", Title: "Safe page"},
		Capture: &model.ProjectCaptureActivity{Kind: "recording", Phase: "active"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Mode != model.ProjectCanvasModeBrowser || first.Current == nil || first.Current.Browser.URL != "https://example.com/path" || !first.Current.Browser.Redacted {
		t.Fatalf("unexpected redacted browser state: %#v", first)
	}
	second, err := store.append(ctx, model.ProjectActivityEvent{
		ProjectID: "project_1", RunID: "run_1", Mode: model.ProjectCanvasModeEditor,
		Kind: "editor_ingestion", Status: model.ProjectActivityRunning, Title: "Editing", EditorSessionID: "editor_1",
	})
	if err != nil {
		t.Fatal(err)
	}

	reloaded := newProjectActivityStore(root)
	state, err := reloaded.state(ctx, "project_1")
	if err != nil {
		t.Fatal(err)
	}
	if state.Mode != model.ProjectCanvasModeEditor || state.EditorSessionID != "editor_1" || state.LastEventID != second.LastEventID {
		t.Fatalf("unexpected reloaded state: %#v", state)
	}
	events, err := reloaded.list(ctx, "project_1", first.LastEventID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ID != second.LastEventID {
		t.Fatalf("cursor returned %#v", events)
	}
}

func TestProjectActivityStoreRedactsSensitiveText(t *testing.T) {
	store := newProjectActivityStore(t.TempDir())
	state, err := store.append(context.Background(), model.ProjectActivityEvent{
		ProjectID: "project_2", Mode: model.ProjectCanvasModeAct, Status: model.ProjectActivityRunning,
		Title: "Using password=super-secret", Detail: "Authorization: Bearer private-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded := state.Current.Title + " " + state.Current.Detail
	if strings.Contains(encoded, "super-secret") || strings.Contains(encoded, "private-token") {
		t.Fatalf("activity leaked sensitive content: %s", encoded)
	}
}
