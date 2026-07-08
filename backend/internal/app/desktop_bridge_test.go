package app

import (
	"encoding/json"
	"path/filepath"
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
