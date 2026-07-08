package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"cascade-demoops/backend/internal/orchestrator"
)

func TestFileStateStorePersistsAcrossInstances(t *testing.T) {
	root := t.TempDir()
	first := NewFileStateStore(root)
	state := &orchestrator.CascadeState{
		ProjectID:   "project_1",
		CurrentNode: orchestrator.NodeHumanApprove,
		Status:      orchestrator.FlowStatusAwaitingHuman,
	}
	if err := first.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}

	second := NewFileStateStore(root)
	loaded, err := second.Load(context.Background(), "project_1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ProjectID != state.ProjectID || loaded.CurrentNode != state.CurrentNode || loaded.Status != state.Status {
		t.Fatalf("loaded state = %+v, want %+v", loaded, state)
	}
}

func TestFileStateStoreHashesProjectIDIntoFileName(t *testing.T) {
	root := t.TempDir()
	store := NewFileStateStore(root)
	state := &orchestrator.CascadeState{ProjectID: "../project/one", Status: orchestrator.FlowStatusCreated}
	if err := store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "..", "project", "one.json")); err == nil {
		t.Fatal("state store wrote outside the configured root")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("state files = %d, want 1", len(entries))
	}
}

func TestFileStateStoreRequiresExplicitRoot(t *testing.T) {
	store := NewFileStateStore("")
	if err := store.Save(context.Background(), &orchestrator.CascadeState{ProjectID: "project_1"}); err == nil {
		t.Fatal("expected root validation error")
	}
	if _, err := store.Load(context.Background(), "project_1"); err == nil {
		t.Fatal("expected root validation error")
	}
}
