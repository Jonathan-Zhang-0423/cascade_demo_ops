package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/model"
)

func TestDevVisibleTargetURLAcceptsExplicitLoopbackURL(t *testing.T) {
	target, err := devVisibleTargetURL("http://127.0.0.1:5000/app")
	if err != nil {
		t.Fatal(err)
	}
	if devVisibleOrigin(target) != "http://127.0.0.1:5000" || target.Path != "/app" {
		t.Fatalf("unexpected target: %+v", target)
	}
}

func TestDevVisiblePackageBindingRejectsAValidPackageForAnotherOrigin(t *testing.T) {
	var pkg model.ClientExecutionPackage
	if err := json.Unmarshal(readContractFixture(t, "client_execution_package.browser_agent_outline.json"), &pkg); err != nil {
		t.Fatal(err)
	}
	_, err := validateDevVisiblePackageBinding(pkg, "http://127.0.0.1:5000")
	if err == nil || !strings.Contains(err.Error(), "base_url does not match") {
		t.Fatalf("expected valid fixture to be rejected for a different visible origin, got %v", err)
	}
}

func TestDevVisibleOriginScopeRejectsExtraOrigin(t *testing.T) {
	if !visibleOriginsRestricted([]string{"http://127.0.0.1:5000"}, "http://127.0.0.1:5000") {
		t.Fatal("expected the exact visible origin to be accepted")
	}
	if visibleOriginsRestricted([]string{"http://127.0.0.1:5000", "http://127.0.0.1:5001"}, "http://127.0.0.1:5000") {
		t.Fatal("expected an additional origin to be rejected")
	}
}

func TestDevVisibleTargetURLRejectsNonLocalAndSensitiveURLs(t *testing.T) {
	for _, value := range []string{
		"https://127.0.0.1:5000/app",
		"http://localhost:5000/app",
		"http://127.0.0.1/app",
		"http://user:password@127.0.0.1:5000/app",
		"http://127.0.0.1:5000/app?token=not-allowed",
	} {
		if _, err := devVisibleTargetURL(value); err == nil {
			t.Fatalf("expected target to be rejected: %s", value)
		}
	}
}

func TestDevVisiblePrepareRequiresAcknowledgementBeforeWorkerStarts(t *testing.T) {
	server := newTestDevHTTPServer(t)
	request := httptest.NewRequest(http.MethodPost, "/v1/desktop/dev-visible-browser-agent/prepare", strings.NewReader(`{"target_url":"http://127.0.0.1:5000/app","dev_test_ack":false}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "dev_test_ack=true") {
		t.Fatalf("expected explicit local-test acknowledgement error, got %d: %s", response.Code, response.Body.String())
	}
}

func TestDevVisiblePrepareRejectsExternalTargetBeforeWorkerStarts(t *testing.T) {
	server := newTestDevHTTPServer(t)
	request := httptest.NewRequest(http.MethodPost, "/v1/desktop/dev-visible-browser-agent/prepare", strings.NewReader(`{"target_url":"https://example.com/app","dev_test_ack":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "127.0.0.1") {
		t.Fatalf("expected external target rejection, got %d: %s", response.Code, response.Body.String())
	}
}

func TestDevVisibleStatusRouteDoesNotOpenOrExecuteABrowser(t *testing.T) {
	server := newTestDevHTTPServer(t)
	request := httptest.NewRequest(http.MethodGet, "/v1/desktop/dev-visible-browser-agent/not-a-session", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "session was not found") {
		t.Fatalf("expected a read-only missing-session response, got %d: %s", response.Code, response.Body.String())
	}
}
