package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/store"
)

func TestEnsureExchangeSessionSkipsWellKnownForConfiguredDevBaseURL(t *testing.T) {
	t.Parallel()
	wellKnownHits := 0
	registerHits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/aigc/.well-known/cascade-exchange":
			wellKnownHits++
			http.Error(w, "404 page not found", http.StatusNotFound)
		case "/aigc/v1/app-installations/register":
			registerHits++
			var request model.AppInstallationRegisterRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode register request: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			response := model.AppInstallationSessionResponse{
				InstallID:            request.InstallID,
				SessionID:            "sess_test",
				SessionToken:         "cassess_test",
				ExpiresAt:            time.Now().UTC().Add(time.Hour),
				OrgID:                request.OrgID,
				ProjectID:            request.ProjectID,
				ServerKeyID:          defaultServerPublicKeyID,
				ResultRecipientKeyID: installationResultKeyID(request.InstallID),
				AuthMode:             exchangeAuthModeInstallation,
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
		default:
			t.Errorf("unexpected exchange request path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	service, err := NewService(config.AppRuntimeConfig{
		Profile:              config.ProfileDev,
		Environment:          "development",
		Mode:                 model.AppModeDesktop,
		DatabaseDialect:      config.DatabaseSQLite,
		SQLitePath:           filepath.Join(root, "cascade_demoops.db"),
		DataRoot:             root,
		ArtifactRoot:         filepath.Join(root, "artifacts"),
		CacheRoot:            filepath.Join(root, "cache"),
		LogRoot:              filepath.Join(root, "logs"),
		ResourceRoot:         root,
		DevRepoRoot:          root,
		CloudExchangeBaseURL: server.URL + "/aigc",
		LLMMode:              config.LLMModeDeterministic,
	}, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}

	session, err := service.EnsureExchangeSession(context.Background(), "https://cascadeai.cn", "org_1", "project_1")
	if err != nil {
		t.Fatal(err)
	}
	if wellKnownHits != 0 {
		t.Fatalf("configured dev exchange base URL should not probe well-known endpoint, hits=%d", wellKnownHits)
	}
	if registerHits != 1 {
		t.Fatalf("expected one installation register request, got %d", registerHits)
	}
	if session.BaseURL != server.URL+"/aigc" || session.SessionToken != "cassess_test" {
		t.Fatalf("unexpected exchange session: %+v", session)
	}
}
