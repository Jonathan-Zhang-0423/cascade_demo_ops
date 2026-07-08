package storage

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalLayoutBuildsProjectArtifactPaths(t *testing.T) {
	layout := NewLocalLayout(filepath.Join("data"), filepath.Join("data", "artifacts"), filepath.Join("data", "cache"), filepath.Join("data", "logs"))
	path := layout.ArtifactPath("project/../one", "demo.mp4")
	if strings.Contains(path, "..") || strings.Contains(path, "/../") {
		t.Fatalf("artifact path should sanitize path traversal: %q", path)
	}
	if !strings.Contains(filepath.ToSlash(path), "artifacts") {
		t.Fatalf("artifact path should live under artifacts root: %q", path)
	}
}

func TestLocalLayoutArtifactURIUsesFileScheme(t *testing.T) {
	layout := NewLocalLayout("data", filepath.Join("data", "artifacts"), filepath.Join("data", "cache"), filepath.Join("data", "logs"))
	uri := layout.ArtifactURI("project_1", "shot.png")
	if !strings.HasPrefix(uri, "file:") {
		t.Fatalf("artifact URI should use file scheme: %q", uri)
	}
	if strings.Contains(uri, "blob") {
		t.Fatalf("artifact URI should not embed binary/blob payload: %q", uri)
	}
}
