package app

import (
	"encoding/json"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/credentialstore"
	"cascade-demoops/backend/internal/orchestrator"
	"cascade-demoops/backend/internal/store"
)

func newCredentialHydrationService(t *testing.T) *Service {
	t.Helper()
	service, err := NewService(config.AppRuntimeConfig{DataRoot: t.TempDir(), LLMMode: config.LLMModeDeterministic}, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	service.readDemoCredential = func(ref string) (credentialstore.DemoCredential, error) {
		if ref != "fixture-ref" {
			return credentialstore.DemoCredential{}, errCredentialNotFound
		}
		return credentialstore.DemoCredential{Username: "demo@example.test", Password: "fixture-password"}, nil
	}
	return service
}

var errCredentialNotFound = &credentialUnavailableError{}

type credentialUnavailableError struct{}

func (*credentialUnavailableError) Error() string { return "not found" }

func TestHydrateDemoCredentialInputResolvesOpaqueRefWithoutPersistence(t *testing.T) {
	service := newCredentialHydrationService(t)
	input := orchestrator.UserInput{DemoCredentialRef: "credential://demo/fixture-ref"}
	hydrated, err := service.hydrateDemoCredentialInput(input)
	if err != nil {
		t.Fatal(err)
	}
	if hydrated.DemoUsername != "demo@example.test" || hydrated.DemoPassword != "fixture-password" {
		t.Fatalf("unexpected hydrated credentials: %+v", hydrated)
	}
	serialized, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), "fixture-password") || strings.Contains(string(serialized), "demo@example.test") {
		t.Fatal("opaque input unexpectedly contains plaintext credentials")
	}
	if hydrated.DemoCredentialRef != input.DemoCredentialRef {
		t.Fatal("credential ref was not preserved")
	}
}

func TestHydrateDemoCredentialInputFailsClosedAndRejectsMismatch(t *testing.T) {
	service := newCredentialHydrationService(t)
	for _, input := range []orchestrator.UserInput{
		{DemoCredentialRef: "credential://demo/missing"},
		{DemoCredentialRef: "credential://demo/bad/ref"},
		{DemoCredentialRef: "credential://demo/fixture-ref", DemoPassword: "wrong"},
	} {
		if _, err := service.hydrateDemoCredentialInput(input); err == nil {
			t.Fatalf("expected credential hydration failure for %+v", input)
		} else if strings.Contains(err.Error(), "fixture-password") || strings.Contains(err.Error(), "demo@example.test") {
			t.Fatalf("credential details leaked in error: %v", err)
		}
	}
}
