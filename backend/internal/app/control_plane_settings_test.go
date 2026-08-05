package app

import (
	"bytes"
	"encoding/json"
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

func TestValidateControlPlaneBaseURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "https", value: " https://control.demoops.test/api/ ", want: "https://control.demoops.test/api"},
		{name: "loopback IPv4", value: "http://127.0.0.1:4317/", want: "http://127.0.0.1:4317"},
		{name: "loopback hostname", value: "http://localhost:4317", want: "http://localhost:4317"},
		{name: "public plaintext", value: "http://control.demoops.test", wantErr: true},
		{name: "credentials", value: "https://user:secret@control.demoops.test", wantErr: true},
		{name: "query", value: "https://control.demoops.test?token=secret", wantErr: true},
		{name: "fragment", value: "https://control.demoops.test/#settings", wantErr: true},
		{name: "missing host", value: "https://", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := validateControlPlaneBaseURL(test.value)
			if test.wantErr {
				if err == nil {
					t.Fatalf("validateControlPlaneBaseURL(%q) unexpectedly succeeded with %q", test.value, got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("validateControlPlaneBaseURL(%q) = %q, %v; want %q", test.value, got, err, test.want)
			}
		})
	}
}

func TestControlPlaneSettingsPersistReplaceAndClearServerSession(t *testing.T) {
	root := t.TempDir()
	service := newControlPlaneTestService(t, root, "https://managed.demoops.test")
	identityStore := service.exchangeIdentityStore()
	record, err := identityStore.loadOrCreate("test")
	if err != nil {
		t.Fatal(err)
	}
	installID := record.InstallID
	signingKey := record.SigningPrivateKeyBase64
	record.ServerKeyID = "server_old"
	record.ServerPublicKeyBase64 = "old-public-key"
	record.SessionID = "session_old"
	record.SessionToken = "secret-session-token"
	record.SessionExpiresAt = time.Now().UTC().Add(time.Hour)
	record.ExchangeBaseURL = "https://old.demoops.test"
	if err := identityStore.save(record); err != nil {
		t.Fatal(err)
	}

	if err := service.SaveControlPlaneBaseURL("https://first.demoops.test/api/"); err != nil {
		t.Fatal(err)
	}
	if err := service.SaveControlPlaneBaseURL("http://127.0.0.1:4317/"); err != nil {
		t.Fatalf("replace existing Windows settings file: %v", err)
	}
	if got := service.RuntimeConfig().CloudExchangeBaseURL; got != "http://127.0.0.1:4317" {
		t.Fatalf("runtime control plane = %q", got)
	}

	restarted := newControlPlaneTestService(t, root, "https://managed.demoops.test")
	if got := restarted.RuntimeConfig().CloudExchangeBaseURL; got != "http://127.0.0.1:4317" {
		t.Fatalf("reloaded control plane = %q", got)
	}
	cleared, err := restarted.exchangeIdentityStore().load()
	if err != nil {
		t.Fatal(err)
	}
	if cleared.InstallID != installID || cleared.SigningPrivateKeyBase64 != signingKey {
		t.Fatal("changing the server must preserve the installation signing identity")
	}
	if cleared.ServerKeyID != "" || cleared.SessionID != "" || cleared.SessionToken != "" || cleared.ExchangeBaseURL != "" {
		t.Fatalf("old server session was not cleared: %+v", cleared)
	}
}

func TestDevHTTPControlPlaneSettingsUpdatesRuntimeHealth(t *testing.T) {
	var registerHits int
	discovery := localBootstrapDiscovery("", "test", time.Now().UTC())
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/cascade-exchange" {
			discovery.ExchangeBaseURL = "http://" + r.Host
			_ = json.NewEncoder(w).Encode(discovery)
			return
		}
		if r.URL.Path != "/v1/app-installations/register" {
			http.NotFound(w, r)
			return
		}
		registerHits++
		var request model.AppInstallationRegisterRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.ChallengeID != discovery.Challenge.ChallengeID {
			t.Fatalf("registration challenge = %q, want %q", request.ChallengeID, discovery.Challenge.ChallengeID)
		}
		_ = json.NewEncoder(w).Encode(model.AppInstallationSessionResponse{
			InstallID: request.InstallID, SessionID: "session_control_plane", SessionToken: "cassess_control_plane",
			ExpiresAt: time.Now().UTC().Add(time.Hour), OrgID: request.OrgID, ServerKeyID: defaultServerPublicKeyID,
			ResultRecipientKeyID: installationResultKeyID(request.InstallID), AuthMode: exchangeAuthModeInstallation,
		})
	}))
	defer controlPlane.Close()
	server := newTestDevHTTPServer(t)
	body := bytes.NewBufferString(`{"base_url":"` + controlPlane.URL + `/"}`)
	request := httptest.NewRequest(http.MethodPut, "/v1/desktop/control-plane", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}
	var bridge BridgeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &bridge); err != nil {
		t.Fatal(err)
	}
	if !bridge.OK {
		t.Fatalf("bridge error: %s", bridge.Error)
	}
	var runtime RuntimeConfigView
	if err := json.Unmarshal(bridge.Data, &runtime); err != nil {
		t.Fatal(err)
	}
	if !runtime.CloudExchange.Configured || !runtime.CloudExchange.SessionValid || runtime.CloudExchange.AuthMode != exchangeAuthModeInstallation || registerHits != 1 {
		t.Fatalf("runtime health did not expose configured control plane: %+v", runtime.CloudExchange)
	}
	settingsData, err := json.Marshal(controlPlaneSettingsFile{BaseURL: controlPlane.URL})
	if err != nil || strings.Contains(string(settingsData), "token") || strings.Contains(string(settingsData), "password") {
		t.Fatalf("control-plane settings must only contain the safe base URL: %s, %v", settingsData, err)
	}
}

func TestDevHTTPControlPlaneSettingsPersistsAddressWhenHandshakeFails(t *testing.T) {
	controlPlane := httptest.NewServer(http.NotFoundHandler())
	defer controlPlane.Close()
	server := newTestDevHTTPServer(t)

	body := bytes.NewBufferString(`{"base_url":"` + controlPlane.URL + `/"}`)
	request := httptest.NewRequest(http.MethodPut, "/v1/desktop/control-plane", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}
	var failed BridgeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &failed); err != nil {
		t.Fatal(err)
	}
	if failed.OK || failed.Error == "" {
		t.Fatalf("handshake failure was not returned: %+v", failed)
	}

	healthRequest := httptest.NewRequest(http.MethodGet, "/v1/desktop/runtime-health", nil)
	healthResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(healthResponse, healthRequest)
	if healthResponse.Code != http.StatusOK {
		t.Fatalf("runtime health status %d: %s", healthResponse.Code, healthResponse.Body.String())
	}
	var healthBridge BridgeResponse
	if err := json.Unmarshal(healthResponse.Body.Bytes(), &healthBridge); err != nil {
		t.Fatal(err)
	}
	var runtime RuntimeConfigView
	if err := json.Unmarshal(healthBridge.Data, &runtime); err != nil {
		t.Fatal(err)
	}
	if !runtime.CloudExchange.Configured || runtime.CloudExchange.SessionValid || runtime.CloudExchange.BaseURLHost == "" {
		t.Fatalf("saved but disconnected control plane was not exposed correctly: %+v", runtime.CloudExchange)
	}
	serialized := strings.ToLower(response.Body.String() + healthResponse.Body.String())
	if strings.Contains(serialized, `"password"`) || strings.Contains(serialized, `"session_token"`) {
		t.Fatalf("control-plane responses leaked credential-shaped fields: %s", serialized)
	}
}

func newControlPlaneTestService(t *testing.T, root, configuredURL string) *Service {
	t.Helper()
	service, err := NewService(config.AppRuntimeConfig{
		Profile:              config.ProfileDev,
		Environment:          "test",
		Mode:                 model.AppModeDesktop,
		DatabaseDialect:      config.DatabaseSQLite,
		SQLitePath:           filepath.Join(root, "cascade_demoops.db"),
		DataRoot:             root,
		ArtifactRoot:         filepath.Join(root, "artifacts"),
		CacheRoot:            filepath.Join(root, "cache"),
		LogRoot:              filepath.Join(root, "logs"),
		ResourceRoot:         root,
		DevRepoRoot:          root,
		CloudExchangeBaseURL: configuredURL,
		LLMMode:              config.LLMModeDeterministic,
		SidecarPaths:         map[string]string{},
		ModelTaskRoutes:      map[config.ModelTask]config.ModelTaskRoute{},
	}, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	return service
}
