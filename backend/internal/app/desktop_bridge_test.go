package app

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

func TestDesktopBridgeReturnsJSONSafeResponses(t *testing.T) {
	bridge := newTestBridge(t)
	response := bridge.CreateProject(orchestrator.UserInput{
		Mode:               model.AppModeDesktop,
		ProductURL:         "https://app.example.com",
		ProductDescription: "Desktop package smoke test，展示团队邀请成员。",
		TargetAudience:     "seed investor",
		WebpageScreenshots: []model.WebpageScreenshotInput{verifiedActionScreenshotInput()},
	})
	if !response.OK {
		t.Fatalf("CreateProject error: %s", response.Error)
	}
	var state orchestrator.CascadeState
	if err := json.Unmarshal(response.Data, &state); err != nil {
		t.Fatalf("response is not JSON-safe: %v", err)
	}
	if state.WorkflowGraph == nil {
		t.Fatal("expected workflow graph in bridge response")
	}
	if state.UnderstandingReport == nil || state.ScriptDocument == nil || state.ScriptMarkdown == "" || state.ExecutableScriptBundle == nil {
		t.Fatal("expected understanding report, script document, and executable bundle in bridge response")
	}
	reportResponse := bridge.GetUnderstandingReport(state.ProjectID)
	if !reportResponse.OK {
		t.Fatalf("GetUnderstandingReport error: %s", reportResponse.Error)
	}
	var report model.MultimodalUnderstandingReport
	if err := json.Unmarshal(reportResponse.Data, &report); err != nil {
		t.Fatalf("understanding report response is not JSON-safe: %v", err)
	}
	scriptResponse := bridge.GetExecutionScriptDocument(state.ProjectID)
	if !scriptResponse.OK {
		t.Fatalf("GetExecutionScriptDocument error: %s", scriptResponse.Error)
	}
	var script model.ExecutionScriptDocument
	if err := json.Unmarshal(scriptResponse.Data, &script); err != nil {
		t.Fatalf("script document response is not JSON-safe: %v", err)
	}
	markdownResponse := bridge.GetExecutionScriptMarkdown(state.ProjectID)
	if !markdownResponse.OK {
		t.Fatalf("GetExecutionScriptMarkdown error: %s", markdownResponse.Error)
	}
	if !strings.Contains(string(markdownResponse.Data), "执行步骤") {
		t.Fatalf("expected markdown preview in bridge response: %s", markdownResponse.Data)
	}
	bundleResponse := bridge.GetExecutableScriptBundle(state.ProjectID)
	if !bundleResponse.OK {
		t.Fatalf("GetExecutableScriptBundle error: %s", bundleResponse.Error)
	}
	var bundle model.ExecutableRecordingScriptBundle
	if err := json.Unmarshal(bundleResponse.Data, &bundle); err != nil {
		t.Fatalf("bundle response is not JSON-safe: %v", err)
	}
	if bundle.ScriptManifest.Runtime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 ||
		bundle.StageApprovalPlan == nil ||
		bundle.ScriptOutline == nil ||
		bundle.AgentPromptPolicy == nil ||
		bundle.Reproducibility.OutlineHashSHA256 == "" {
		t.Fatalf("expected browser agent outline and hashes in bundle: %+v", bundle)
	}
}

func TestDesktopBridgeArtifactURIResponseIsJSONSafe(t *testing.T) {
	bridge := newTestBridge(t)
	response := bridge.ArtifactURI("project_1", "demo.mp4")
	if !response.OK {
		t.Fatalf("ArtifactURI error: %s", response.Error)
	}
	var payload map[string]string
	if err := json.Unmarshal(response.Data, &payload); err != nil {
		t.Fatalf("response is not JSON-safe: %v", err)
	}
	if payload["uri"] == "" {
		t.Fatal("expected artifact uri")
	}
}

func TestDesktopBridgeRuntimeConfigIsRedacted(t *testing.T) {
	root := t.TempDir()
	bridge, err := NewDesktopBridge(config.AppRuntimeConfig{
		Profile:         config.ProfileCloud,
		Environment:     "test",
		Mode:            model.AppModeWeb,
		DatabaseDialect: config.DatabasePostgres,
		DatabaseURL:     "postgres://user:secret@example/db",
		SQLitePath:      filepath.Join(root, "cascade_demoops.db"),
		DataRoot:        root,
		ArtifactRoot:    filepath.Join(root, "artifacts"),
		CacheRoot:       filepath.Join(root, "cache"),
		LogRoot:         filepath.Join(root, "logs"),
		ResourceRoot:    filepath.Join(root, "resources"),
		DevRepoRoot:     root,
		SidecarPaths:    map[string]string{"video-worker": filepath.Join(root, "worker", "index.js")},
		NodeBinaryPath:  filepath.Join(root, "node"),
		ArkMediaMode:    config.ArkMediaModeDryRun,
		ModelProviders: map[config.ModelProvider]config.ModelProviderCredential{
			config.ModelProviderGLM: {
				Provider:        config.ModelProviderGLM,
				APIKey:          "glm-secret-key",
				APIKeyEnv:       "GLM_API_KEY",
				BaseURL:         "https://glm.example",
				BaseURLEnv:      "GLM_BASE_URL",
				DefaultModel:    "glm-5.2",
				DefaultModelEnv: "GLM_MODEL",
				Enabled:         true,
			},
		},
		ModelTaskRoutes: map[config.ModelTask]config.ModelTaskRoute{
			config.ModelTaskCodeReading: {
				Task:             config.ModelTaskCodeReading,
				Provider:         config.ModelProviderGLM,
				Model:            "glm-5.2",
				ProviderOverride: "CASCADE_CODE_READING_PROVIDER",
				ModelOverride:    "CASCADE_CODE_READING_MODEL",
			},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	response := bridge.RuntimeConfig()
	if !response.OK {
		t.Fatalf("RuntimeConfig error: %s", response.Error)
	}
	payload := string(response.Data)
	if strings.Contains(payload, "secret") || strings.Contains(payload, "glm-secret-key") || strings.Contains(payload, "https://glm.example") || strings.Contains(payload, root) {
		t.Fatalf("runtime config leaked sensitive values or local paths: %s", payload)
	}
	var view RuntimeConfigView
	if err := json.Unmarshal(response.Data, &view); err != nil {
		t.Fatal(err)
	}
	if !view.DatabaseConfigured || !view.NodeRuntimeConfigured || !view.Sidecars["video-worker"] {
		t.Fatalf("unexpected runtime view: %+v", view)
	}
	if !view.ModelProviders["glm"].Configured || view.ModelProviders["glm"].APIKeyEnv != "GLM_API_KEY" {
		t.Fatalf("expected redacted glm provider state: %+v", view.ModelProviders)
	}
	if view.ArkMediaMode != config.ArkMediaModeDryRun {
		t.Fatalf("expected redacted ark media mode in runtime view: %+v", view)
	}
	if view.ModelTaskRoutes["code_reading"].Provider != "glm" || view.ModelTaskRoutes["code_reading"].Model != "glm-5.2" {
		t.Fatalf("expected code reading model route in runtime view: %+v", view.ModelTaskRoutes)
	}
}

func newTestBridge(t *testing.T) *DesktopBridge {
	t.Helper()
	root := t.TempDir()
	bridge, err := NewDesktopBridge(config.AppRuntimeConfig{
		Profile:         config.ProfileDesktop,
		Environment:     "test",
		Mode:            model.AppModeDesktop,
		DatabaseDialect: config.DatabaseSQLite,
		SQLitePath:      filepath.Join(root, "cascade_demoops.db"),
		DataRoot:        root,
		ArtifactRoot:    filepath.Join(root, "artifacts"),
		CacheRoot:       filepath.Join(root, "cache"),
		LogRoot:         filepath.Join(root, "logs"),
		ResourceRoot:    root,
		DevRepoRoot:     root,
		SidecarPaths:    map[string]string{},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return bridge
}
