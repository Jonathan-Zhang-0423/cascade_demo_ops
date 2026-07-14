package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/model"
)

func TestExchangeContractFixturesLoadInGo(t *testing.T) {
	clientFixture := readContractFixture(t, "client_execution_package.happy.json")
	assertNoContractLeakage(t, clientFixture)
	var pkg model.ClientExecutionPackage
	if err := json.Unmarshal(clientFixture, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.SchemaVersion != model.ClientExecutionPackageSchemaVersion || pkg.PackageID == "" || pkg.ExecutableScriptBundle == nil {
		t.Fatalf("client fixture missing required contract fields: %+v", pkg)
	}
	if !pkg.SafetyReport.AllowedToUpload || pkg.SafetyReport.UploadMode != "structure_summary_only" {
		t.Fatalf("client fixture must be uploadable as structure summary: %+v", pkg.SafetyReport)
	}
	if pkg.ExecutableScriptBundle.ScriptManifest.EntryFunction != "runCascadeRecording" {
		t.Fatalf("fixture script entry must be runCascadeRecording: %+v", pkg.ExecutableScriptBundle.ScriptManifest)
	}

	generatedFixture := readContractFixture(t, "recording_result.generated.json")
	assertNoContractLeakage(t, generatedFixture)
	var generated model.RecordingResultPackage
	if err := json.Unmarshal(generatedFixture, &generated); err != nil {
		t.Fatal(err)
	}
	if generated.Status != model.RecordingResultStatusGenerated || len(generated.Delivery.AssetRefs) == 0 {
		t.Fatalf("generated result fixture missing delivery asset refs: %+v", generated)
	}
	if err := generated.ValidateDeliverySecurity(); err != nil {
		t.Fatalf("generated result delivery security invalid: %v", err)
	}
	for _, artifact := range generated.Delivery.AssetRefs {
		if !artifact.Encrypted || !artifact.Sensitive || artifact.SHA256 == "" {
			t.Fatalf("generated result artifact must be encrypted/sensitive/checksummed: %+v", artifact)
		}
	}

	failedFixture := readContractFixture(t, "recording_result.failed.json")
	assertNoContractLeakage(t, failedFixture)
	var failed model.RecordingResultPackage
	if err := json.Unmarshal(failedFixture, &failed); err != nil {
		t.Fatal(err)
	}
	if failed.Status != model.RecordingResultStatusFailed || failed.FailureDiagnostic == nil || failed.RepairRequest == nil {
		t.Fatalf("failed result fixture must include diagnostic and repair request: %+v", failed)
	}
	if !failed.FailureDiagnostic.RedactionReport.Applied || failed.FailureDiagnostic.RedactionReport.FullHTMLIncluded {
		t.Fatalf("failed diagnostic must be redacted without full HTML: %+v", failed.FailureDiagnostic.RedactionReport)
	}
	for _, artifact := range append(append([]model.PackageArtifactDescriptor{}, failed.FailureDiagnostic.ScreenshotRefs...), failed.FailureDiagnostic.TraceRefs...) {
		if !artifact.Encrypted || !artifact.Sensitive {
			t.Fatalf("failure artifact must be encrypted and sensitive: %+v", artifact)
		}
	}
}

func TestExchangeUploadContractFixturesPreserveEnvelopePayloadRef(t *testing.T) {
	type uploadFixture struct {
		UploadID   string                    `json:"upload_id"`
		Envelope   model.ExchangeEnvelope    `json:"envelope"`
		PayloadRef model.EncryptedPayloadRef `json:"payload_ref"`
		Payload    json.RawMessage           `json:"payload,omitempty"`
	}
	for _, fileName := range []string{"upload_plaintext_dev.json", "upload_encrypted_metadata_only.json"} {
		t.Run(fileName, func(t *testing.T) {
			data := readContractFixture(t, fileName)
			assertNoContractLeakage(t, data)
			var upload uploadFixture
			if err := json.Unmarshal(data, &upload); err != nil {
				t.Fatal(err)
			}
			if upload.UploadID == "" || upload.Envelope.EnvelopeID == "" {
				t.Fatalf("upload fixture missing identity: %+v", upload)
			}
			if !reflect.DeepEqual(upload.PayloadRef, upload.Envelope.PayloadRef) {
				t.Fatalf("payload_ref must match envelope.payload_ref: outer=%+v inner=%+v", upload.PayloadRef, upload.Envelope.PayloadRef)
			}
			if fileName == "upload_plaintext_dev.json" && len(upload.Payload) == 0 {
				t.Fatal("dev plaintext upload fixture must include payload")
			}
			if fileName == "upload_encrypted_metadata_only.json" && len(bytes.TrimSpace(upload.Payload)) != 0 {
				t.Fatal("encrypted metadata-only upload fixture must not include plaintext payload")
			}
			if !upload.PayloadRef.Encrypted || !upload.PayloadRef.Sensitive {
				t.Fatalf("upload payload_ref must be encrypted and sensitive: %+v", upload.PayloadRef)
			}
		})
	}
}

func TestRepairContractFixtureCarriesLineage(t *testing.T) {
	data := readContractFixture(t, "client_execution_package.repair.json")
	assertNoContractLeakage(t, data)
	var pkg model.ClientExecutionPackage
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.RepairContext == nil || pkg.ExecutableScriptBundle == nil || pkg.ExecutableScriptBundle.RepairLineage == nil {
		t.Fatalf("repair fixture must carry repair_context and repair_lineage: %+v", pkg)
	}
	if pkg.RepairContext.SourceResultID == "" || pkg.ExecutableScriptBundle.RepairLineage.BaseBundleHashSHA256 == "" {
		t.Fatalf("repair fixture lineage is incomplete: context=%+v lineage=%+v", pkg.RepairContext, pkg.ExecutableScriptBundle.RepairLineage)
	}
	if !pkg.SafetyReport.AllowedToUpload || pkg.SafetyReport.HumanApproval.ApprovalID == "" {
		t.Fatalf("repair fixture must be re-approved before upload: %+v", pkg.SafetyReport)
	}
}

func readContractFixture(t *testing.T, fileName string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "..", "contracts", "exchange", "v1", fileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) {
		t.Fatalf("fixture is not valid JSON: %s", fileName)
	}
	return data
}

func assertNoContractLeakage(t *testing.T, data []byte) {
	t.Helper()
	payload := strings.ToLower(string(data))
	for _, forbidden := range []string{"raw-password", "bearer ", "authorization:", "cookie:", "sk-", "begin private key", ".env", strings.Repeat("0", 6)} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("contract fixture leaked forbidden token %q", forbidden)
		}
	}
}
