package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/direct"
	"cascade-demoops/backend/internal/model"
)

func TestNormalizeServerControlledFixtureIsCredentialFreeAndValid(t *testing.T) {
	path := filepath.Join("..", "..", "..", "contracts", "exchange", "v1", "client_execution_package.browser_agent_outline.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var pkg model.ClientExecutionPackage
	if err := json.Unmarshal(original, &pkg); err != nil {
		t.Fatal(err)
	}
	normalized, pkg, err := normalizeServerControlledFixture(original, pkg, "direct_install_fixture_test")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.CredentialGrants) != 0 || pkg.ProducerInstallationID != "direct_install_fixture_test" || pkg.Reproducibility.PackageHashSHA256 != direct.HashSHA256(original) || pkg.SafetyReport.HumanApproval.ApprovalSubjectDigestSHA256 == "" {
		t.Fatalf("normalized fixture = %+v", pkg)
	}
	if len(normalized) == 0 {
		t.Fatal("normalized fixture was empty")
	}
	if err := model.ValidateClientExecutionPackageForDirectExecution(&pkg); err != nil {
		t.Fatalf("normalized Server fixture must pass Direct validation: %v", err)
	}
}

func TestRecoverySmokeStateRoundTrip(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true} //nolint:gosec -- local test fixture
	client := &smokeClient{
		httpClient: &http.Client{Transport: transport}, controlURL: "https://127.0.0.1:18443", bootstrap: "must-not-persist-bootstrap",
		publicKey: public, privateKey: private,
		lease: direct.DirectPortLease{LeaseID: "lease-test", InstallationID: direct.InstallationID(public), DataURL: "https://127.0.0.1:24000", DataPort: direct.DataPortMin, LeaseToken: "lease-token", CryptoSuite: direct.CryptoSuite},
	}
	path := filepath.Join(t.TempDir(), "recovery-state.json")
	if err := writeRecoverySmokeState(path, client, direct.DirectPackageReceipt{JobID: "job-test", PackageID: "pkg-test"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), client.bootstrap) {
		t.Fatal("recovery state leaked bootstrap token")
	}
	restored, state, err := readRecoverySmokeState(path, "new-bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	if state.JobID != "job-test" || restored.lease.LeaseID != client.lease.LeaseID || restored.bootstrap != "new-bootstrap" || !restored.httpClient.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify {
		t.Fatalf("restored state = %+v client=%+v", state, restored)
	}
}
