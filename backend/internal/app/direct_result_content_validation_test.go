package app

import (
	"testing"

	"cascade-demoops/backend/internal/model"
)

func TestDirectArtifactViewportMatchesWhenDimensionsAreDeclared(t *testing.T) {
	artifact := model.ArtifactRef{Metadata: map[string]any{"width": 1280, "height": 720}}
	if !directArtifactViewportMatches(artifact, model.BrowserGeometryViewport{Width: 1280, Height: 720, DPR: 1}) {
		t.Fatal("matching screenshot dimensions must be accepted")
	}
	if directArtifactViewportMatches(artifact, model.BrowserGeometryViewport{Width: 1440, Height: 720, DPR: 1}) {
		t.Fatal("mismatched screenshot dimensions must be rejected")
	}
}
