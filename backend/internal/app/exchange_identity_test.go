package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/store"
)

func TestEnsureExchangeSessionUsesServerIssuedChallengeForConfiguredDevBaseURL(t *testing.T) {
	wellKnownHits := 0
	registerHits := 0
	discovery := localBootstrapDiscovery("", "development", time.Now().UTC())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/aigc/.well-known/cascade-exchange":
			wellKnownHits++
			discovery.ExchangeBaseURL = "http://" + r.Host + "/aigc"
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(discovery)
		case "/aigc/v1/app-installations/register":
			registerHits++
			var request model.AppInstallationRegisterRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode register request: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			if request.ChallengeID != discovery.Challenge.ChallengeID {
				t.Errorf("registration challenge = %q, want server challenge %q", request.ChallengeID, discovery.Challenge.ChallengeID)
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
		CloudExchangeToken:   "local-fallback-token",
		LLMMode:              config.LLMModeDeterministic,
	}, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}

	session, err := service.EnsureExchangeSession(context.Background(), "org_1", "project_1")
	if err != nil {
		t.Fatal(err)
	}
	if wellKnownHits != 1 {
		t.Fatalf("configured dev exchange must fetch one server-issued challenge, hits=%d", wellKnownHits)
	}
	if registerHits != 1 {
		t.Fatalf("expected one installation register request, got %d", registerHits)
	}
	if session.BaseURL != server.URL+"/aigc" || session.SessionToken != "cassess_test" {
		t.Fatalf("unexpected exchange session: %+v", session)
	}
	if session.AuthMode != exchangeAuthModeInstallation {
		t.Fatalf("local exchange must prefer an installation session, got auth mode %q", session.AuthMode)
	}
}

func TestEnsureExchangeSessionUsesRemoteDiscoveryChallengeForConfiguredHTTPS(t *testing.T) {
	oldTransport := http.DefaultTransport
	wellKnownHits := 0
	registerHits := 0
	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	discovery := localBootstrapDiscovery("https://control.demoops.test", "staging", now)
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		response := func(status int, value any) *http.Response {
			data, _ := json.Marshal(value)
			return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(data))), Request: r}
		}
		switch r.URL.String() {
		case "https://control.demoops.test/.well-known/cascade-exchange":
			wellKnownHits++
			return response(http.StatusOK, discovery), nil
		case "https://control.demoops.test/v1/app-installations/register":
			registerHits++
			var request model.AppInstallationRegisterRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.ChallengeID != discovery.Challenge.ChallengeID {
				t.Fatalf("register challenge = %q, want remote %q", request.ChallengeID, discovery.Challenge.ChallengeID)
			}
			return response(http.StatusOK, model.AppInstallationSessionResponse{
				InstallID: request.InstallID, SessionID: "sess_remote", SessionToken: "cassess_remote",
				ExpiresAt: now.Add(time.Hour), OrgID: request.OrgID, ProjectID: request.ProjectID,
				ServerKeyID: defaultServerPublicKeyID, ResultRecipientKeyID: installationResultKeyID(request.InstallID), AuthMode: exchangeAuthModeInstallation,
			}), nil
		default:
			t.Fatalf("unexpected exchange request URL: %s", r.URL.String())
			return nil, nil
		}
	})
	defer func() { http.DefaultTransport = oldTransport }()

	root := t.TempDir()
	service, err := NewService(config.AppRuntimeConfig{
		Profile: config.ProfileDev, Environment: "development", Mode: model.AppModeDesktop,
		DatabaseDialect: config.DatabaseSQLite, SQLitePath: filepath.Join(root, "cascade_demoops.db"),
		DataRoot: root, ArtifactRoot: filepath.Join(root, "artifacts"), CacheRoot: filepath.Join(root, "cache"),
		LogRoot: filepath.Join(root, "logs"), ResourceRoot: root, DevRepoRoot: root,
		CloudExchangeBaseURL: "https://control.demoops.test", LLMMode: config.LLMModeDeterministic,
	}, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.EnsureExchangeSession(context.Background(), "org_1", "project_1")
	if err != nil {
		t.Fatal(err)
	}
	if wellKnownHits != 1 || registerHits != 1 || session.SessionToken != "cassess_remote" {
		t.Fatalf("unexpected remote pairing: well_known=%d register=%d session=%+v", wellKnownHits, registerHits, session)
	}
}

func TestEnsureExchangeSessionKeepsBearerTokenForExternalLegacyExchange(t *testing.T) {
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
		CloudExchangeBaseURL: "https://control.demoops.test",
		CloudExchangeToken:   "legacy-bearer-token",
		LLMMode:              config.LLMModeDeterministic,
	}, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}

	session, err := service.EnsureExchangeSession(context.Background(), "org_1", "project_1")
	if err != nil {
		t.Fatal(err)
	}
	if session.AuthMode != exchangeAuthModeDevToken || session.SessionToken != "legacy-bearer-token" {
		t.Fatalf("external legacy exchange must retain bearer token mode: %+v", session)
	}
}

func TestEnsureExchangeSessionReportsCloudAuthUnavailableWhenRegisterMissing(t *testing.T) {
	oldTransport := http.DefaultTransport
	discovery := localBootstrapDiscovery("https://control.demoops.test", "staging", time.Now().UTC())
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() == "https://control.demoops.test/.well-known/cascade-exchange" {
			data, _ := json.Marshal(discovery)
			return &http.Response{
				StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(string(data))), Request: r,
			}, nil
		}
		if r.URL.String() != "https://control.demoops.test/v1/app-installations/register" {
			t.Fatalf("unexpected exchange request URL: %s", r.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Status:     "404 Not Found",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("404 page not found")),
			Request:    r,
		}, nil
	})
	defer func() { http.DefaultTransport = oldTransport }()

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
		CloudExchangeBaseURL: "https://control.demoops.test",
		LLMMode:              config.LLMModeDeterministic,
	}, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.EnsureExchangeSession(context.Background(), "org_1", "project_1")
	if err == nil {
		t.Fatal("expected cloud auth unavailable error")
	}
	if code := bridgeErrorCode(err); code != "cloud_auth_unavailable" {
		t.Fatalf("expected cloud_auth_unavailable, got %q: %v", code, err)
	}
}

func TestEnsureExchangeSessionRequiresIndependentControlPlaneConfiguration(t *testing.T) {
	root := t.TempDir()
	service, err := NewService(config.AppRuntimeConfig{
		Profile:         config.ProfileDev,
		Environment:     "development",
		Mode:            model.AppModeDesktop,
		DatabaseDialect: config.DatabaseSQLite,
		SQLitePath:      filepath.Join(root, "cascade_demoops.db"),
		DataRoot:        root,
		ArtifactRoot:    filepath.Join(root, "artifacts"),
		CacheRoot:       filepath.Join(root, "cache"),
		LogRoot:         filepath.Join(root, "logs"),
		ResourceRoot:    root,
		DevRepoRoot:     root,
		LLMMode:         config.LLMModeDeterministic,
	}, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.EnsureExchangeSession(context.Background(), "org_1", "project_1")
	if err == nil || !strings.Contains(err.Error(), "execution server is not configured") {
		t.Fatalf("expected an explicit unconfigured control plane error, got %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
