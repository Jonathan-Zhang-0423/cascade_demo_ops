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
		ProductDescription: "Desktop package smoke test",
		TargetAudience:     "seed investor",
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
	if state.UnderstandingReport == nil || state.ScriptDocument == nil || state.ScriptMarkdown == "" {
		t.Fatal("expected understanding report and script document in bridge response")
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
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	response := bridge.RuntimeConfig()
	if !response.OK {
		t.Fatalf("RuntimeConfig error: %s", response.Error)
	}
	payload := string(response.Data)
	if strings.Contains(payload, "secret") || strings.Contains(payload, root) {
		t.Fatalf("runtime config leaked sensitive values or local paths: %s", payload)
	}
	var view RuntimeConfigView
	if err := json.Unmarshal(response.Data, &view); err != nil {
		t.Fatal(err)
	}
	if !view.DatabaseConfigured || !view.NodeRuntimeConfigured || !view.Sidecars["video-worker"] {
		t.Fatalf("unexpected runtime view: %+v", view)
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
