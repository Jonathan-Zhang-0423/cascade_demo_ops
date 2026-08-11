package app

import (
	"encoding/json"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/model"
)

func TestPrepareDirectWorkerResultRewritesEveryDiagnosticArtifact(t *testing.T) {
	artifact := model.ArtifactRef{ID: "failure-shot", Kind: "screenshot", URI: "file:///var/lib/cascade/jobs/failure.png", MimeType: "image/png", SHA256: "abc", SizeBytes: 42, Metadata: map[string]any{"local_path": "/var/lib/cascade/jobs/failure.png"}}
	descriptor := model.PackageArtifactDescriptor{ID: artifact.ID, Kind: artifact.Kind, URI: artifact.URI, MimeType: artifact.MimeType, SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes, Encrypted: true, Sensitive: true, RecipientKeyID: "local-dev/result-key", Metadata: map[string]any{"dev_local_artifact": true, "local_path": "/var/lib/cascade/jobs/failure.png"}}
	result := model.RecordingResultPackage{
		GeneratedAssets:   []model.ArtifactRef{artifact},
		FailureDiagnostic: &model.ScriptFailureDiagnostic{ScreenshotRefs: []model.PackageArtifactDescriptor{descriptor}, TraceRefs: []model.PackageArtifactDescriptor{descriptor}},
		Delivery:          model.ResultDelivery{ResultPackageRef: model.PackageArtifactDescriptor{ID: "result", Kind: "result_package", URI: "file:///var/lib/cascade/jobs/result.json", RecipientKeyID: "local-dev/result-key", Metadata: map[string]any{"dev_local_artifact": true}}, AssetRefs: []model.PackageArtifactDescriptor{descriptor}},
	}
	prepareDirectWorkerResult(&result, "job_direct_1", []DirectWorkerArtifactFile{{Artifact: artifact, Path: `C:\temp\failure.png`}})

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, forbidden := range []string{"dev_local_artifact", "local-dev/result-key", "file:///var/", "/var/lib/", "local_path"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("formal direct result leaked %q: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, "direct://jobs/job_direct_1/artifacts/failure-shot") || !strings.Contains(text, "direct-lease") {
		t.Fatalf("formal direct result did not bind artifacts to the direct lease: %s", text)
	}
}
