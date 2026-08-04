package media

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"cascade-demoops/backend/internal/config"
)

func TestMiniMaxH3SidecarDefaultsDisabledAndIgnoresGenericKeys(t *testing.T) {
	values := map[string]string{
		"CASCADE_ARK_MEDIA_MODE": "real",
		"MINIMAX_API_KEY":        "generic-minimax-key-must-not-enable-h3",
		"SEEDANCE_API_KEY":       "seedance-key-must-not-enable-h3",
	}
	client, settings, err := NewMiniMaxH3Sidecar(func(key string) string { return values[key] }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if client != nil || settings.Mode != config.ArkMediaModeDisabled || settings.APIKey != "" {
		t.Fatalf("sidecar must remain disabled and isolated: client=%v settings=%+v", client, settings)
	}
}

func TestMiniMaxH3SidecarDryRunMakesNoProviderCall(t *testing.T) {
	var providerCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	values := map[string]string{
		MiniMaxH3ModeEnv:    "dry_run",
		MiniMaxH3APIKeyEnv:  "dedicated-h3-key",
		MiniMaxH3BaseURLEnv: server.URL,
	}
	client, settings, err := NewMiniMaxH3Sidecar(func(key string) string { return values[key] }, server.Client())
	if err != nil || client == nil || settings.Mode != config.ArkMediaModeDryRun {
		t.Fatalf("client=%v settings=%+v err=%v", client, settings, err)
	}
	result, err := client.CreateContentGenerationTask(t.Context(), ContentGenerationTaskRequest{
		Content:    []ContentPart{{Type: "text", Text: "Create a presentation-only transition."}},
		Resolution: "2K",
		Duration:   5,
		Ratio:      "16:9",
	})
	if err != nil || result.Request == nil {
		t.Fatalf("dry-run result=%+v err=%v", result, err)
	}
	if got := providerCalls.Load(); got != 0 {
		t.Fatalf("provider HTTP calls = %d, want 0", got)
	}
}

func TestMiniMaxH3SidecarRejectsUnsupportedMode(t *testing.T) {
	_, _, err := NewMiniMaxH3Sidecar(func(key string) string {
		if key == MiniMaxH3ModeEnv {
			return "surprise"
		}
		return ""
	}, nil)
	if err == nil {
		t.Fatal("unsupported dedicated H3 mode must be rejected")
	}
}
