package executor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestFormalRecordingResultPackagingStillRejectsNonUploadableAppDraft(t *testing.T) {
	pkg := localTestOutlineDraft(t)
	if _, err := NewRecordingResultPackageFromRecordResult(&pkg, localTestRecordResult(), "run_formal", time.Now().UTC()); err == nil || !strings.Contains(err.Error(), "allowed_to_upload") {
		t.Fatalf("formal packaging must still reject an App draft, got %v", err)
	}
}

func TestLocalTestRecordingResultPackagingPreservesDraftAndIsRenderable(t *testing.T) {
	pkg := localTestOutlineDraft(t)
	before, err := model.DigestCanonicalJSON(pkg)
	if err != nil {
		t.Fatal(err)
	}
	bundleHash := pkg.ExecutableScriptBundle.Reproducibility.BundleHashSHA256
	planHash := pkg.ExecutableScriptBundle.Reproducibility.PlanHashSHA256

	result, err := NewLocalTestRecordingResultPackageFromRecordResult(&pkg, localTestRecordResult(), "run_local", time.Now().UTC(), LocalTestRecordingResultPackageOptions{
		WaiverID: "test_waiver_1", SourceBundleHashSHA256: bundleHash, SourcePlanHashSHA256: planHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := model.DigestCanonicalJSON(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("local result packaging mutated the original App draft")
	}
	if pkg.SafetyReport.AllowedToUpload {
		t.Fatal("local result packaging must not make the App draft uploadable")
	}
	metadata := result.Delivery.ResultPackageRef.Metadata
	if metadata["dev_test_only"] != true || metadata["not_for_exchange_upload"] != true || metadata["test_only_waiver"] != true || metadata["formal_exchange"] != false || metadata["app_generated"] != true || metadata["transport_authenticated"] != false || metadata["waiver_id"] != "test_waiver_1" {
		t.Fatalf("local test markers are incomplete: %+v", metadata)
	}
	if result.Delivery.RecipientKind != "local_test_only" || result.Delivery.AckRequired || result.Delivery.EncryptionAlg != "" || result.Delivery.ResultPackageRef.Encrypted {
		t.Fatalf("local result must not claim formal encrypted delivery: %+v", result.Delivery)
	}
	if err := model.ValidateLocalTestRecordingResultPackageForRender(&result, &pkg); err != nil {
		t.Fatalf("local result should be renderable by the dedicated validator: %v", err)
	}
	if _, err := NewRenderRequestFromRecordingResult(&pkg, &result, t.TempDir()); err != nil {
		t.Fatalf("render request should accept a valid local test result: %v", err)
	}
	if err := model.ValidateRecordingResultPackageForRender(&result, &pkg); err == nil {
		t.Fatal("formal render/Exchange validation must reject a local test result")
	}
}

func TestLocalTestRecordingResultPackagingRejectsWrongModeAndHashes(t *testing.T) {
	pkg := localTestOutlineDraft(t)
	options := LocalTestRecordingResultPackageOptions{
		WaiverID:               "test_waiver_1",
		SourceBundleHashSHA256: pkg.ExecutableScriptBundle.Reproducibility.BundleHashSHA256,
		SourcePlanHashSHA256:   pkg.ExecutableScriptBundle.Reproducibility.PlanHashSHA256,
	}
	pkg.SafetyReport.AllowedToUpload = true
	if _, err := NewLocalTestRecordingResultPackageFromRecordResult(&pkg, localTestRecordResult(), "run_local", time.Now().UTC(), options); err == nil || !strings.Contains(err.Error(), "allowed_to_upload=false") {
		t.Fatalf("local path must reject an uploadable formal package, got %v", err)
	}

	pkg = localTestOutlineDraft(t)
	options.SourceBundleHashSHA256 = "tampered"
	if _, err := NewLocalTestRecordingResultPackageFromRecordResult(&pkg, localTestRecordResult(), "run_local", time.Now().UTC(), options); err == nil || !strings.Contains(err.Error(), "source hashes") {
		t.Fatalf("local path must reject a mismatched waiver hash, got %v", err)
	}
}

func TestLocalTestRecordingResultCannotBeRelabeledForFormalDelivery(t *testing.T) {
	pkg := localTestOutlineDraft(t)
	result, err := NewLocalTestRecordingResultPackageFromRecordResult(&pkg, localTestRecordResult(), "run_local", time.Now().UTC(), LocalTestRecordingResultPackageOptions{
		WaiverID:               "test_waiver_1",
		SourceBundleHashSHA256: pkg.ExecutableScriptBundle.Reproducibility.BundleHashSHA256,
		SourcePlanHashSHA256:   pkg.ExecutableScriptBundle.Reproducibility.PlanHashSHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	result.Delivery.RecipientKind = model.ResultRecipientAppInstallation
	result.Delivery.RecipientKeyID = "app_installation:test"
	result.Delivery.EncryptionAlg = model.CryptoSuiteXChaCha20Poly1305
	result.Delivery.AckRequired = true
	if err := model.ValidateLocalTestRecordingResultPackageForRender(&result, &pkg); err == nil {
		t.Fatal("local result must reject relabeling as formal App delivery")
	}
}

func localTestOutlineDraft(t *testing.T) model.ClientExecutionPackage {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve fixture path")
	}
	path := filepath.Join(filepath.Dir(current), "..", "..", "..", "contracts", "exchange", "v1", "client_execution_package.browser_agent_outline.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var pkg model.ClientExecutionPackage
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatal(err)
	}
	pkg.SafetyReport.AllowedToUpload = false
	if err := model.ValidateClientExecutionPackageForLocalTestWaiver(&pkg); err != nil {
		t.Fatalf("fixture must be a valid local App draft: %v", err)
	}
	return pkg
}

func localTestRecordResult() RecordResult {
	now := time.Now().UTC()
	return RecordResult{
		RecordingPath: "artifacts/local-test/recording.webm",
		GeneratedAssets: []model.ArtifactRef{{
			ID: "local_screenshot_1", Kind: "screenshot", URI: "artifacts/local-test/step-001.png", MimeType: "image/png", SHA256: strings.Repeat("a", 64), SizeBytes: 100, SourceNodeID: "node_login", CreatedAt: now,
		}},
		WorkerID: "local-test-worker", RuntimeVersions: map[string]string{"mode": "local-test-waiver"},
		StartedAt: now.Add(-time.Second), CompletedAt: now,
	}
}
