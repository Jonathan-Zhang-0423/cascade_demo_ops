package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
