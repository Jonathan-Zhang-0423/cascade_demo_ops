package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/store"
)

func TestAccountHTTPRoutes(t *testing.T) {
	service, err := NewService(config.AppRuntimeConfig{Profile: config.ProfileDev, Environment: "development", DataRoot: t.TempDir()}, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	service.accounts.store = store.NewMemoryAccountStore()
	handler := NewDevHTTPServer(service).Handler()

	request := httptest.NewRequest(http.MethodGet, "/v1/desktop/account/session", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"authenticated":true`) {
		t.Fatalf("unexpected session response: %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPut, "/v1/desktop/account/profile", strings.NewReader(`{"display_name":"Jonathan Zhang"}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"display_name":"Jonathan Zhang"`) {
		t.Fatalf("unexpected profile response: %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/desktop/account/verification/email/start", strings.NewReader(`{"destination":"jonathan@example.com"}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var verificationPayload struct {
		OK   bool `json:"ok"`
		Data struct {
			DevelopmentCode string `json:"development_code"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &verificationPayload); err != nil || !verificationPayload.OK || verificationPayload.Data.DevelopmentCode == "" {
		t.Fatalf("unexpected verification start response: %s", response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/v1/desktop/account/verification/email", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"pending"`) || strings.Contains(response.Body.String(), `development_code`) {
		t.Fatalf("unexpected pending verification state: %d %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/v1/desktop/account/verification/email/confirm", strings.NewReader(`{"code":"`+verificationPayload.Data.DevelopmentCode+`"}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"email_verification":"verified"`) {
		t.Fatalf("unexpected verification confirmation response: %d %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/v1/desktop/account/verification/email", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"verified"`) {
		t.Fatalf("unexpected verified state: %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/desktop/account/password/set", strings.NewReader(`{"password":"a sufficiently long password"}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"authenticated":false`) || strings.Contains(response.Body.String(), "sufficiently") {
		t.Fatalf("unexpected password setup response: %d %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/v1/desktop/account/session/login", strings.NewReader(`{"identifier":"jonathan@example.com","password":"a sufficiently long password"}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"authenticated":true`) || strings.Contains(response.Body.String(), "password_hash") {
		t.Fatalf("unexpected password login response: %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/desktop/account/plan", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var payload BridgeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || !payload.OK || len(payload.Data) == 0 {
		t.Fatalf("unexpected plan response: %s", response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/desktop/account/logout", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"authenticated":false`) {
		t.Fatalf("unexpected logout response: %d %s", response.Code, response.Body.String())
	}
}
