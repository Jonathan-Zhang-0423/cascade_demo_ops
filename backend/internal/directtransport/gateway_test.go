package directtransport

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

const testBootstrapToken = "bootstrap-token-with-at-least-thirty-two-characters"
const testWorkerToken = "worker-token-with-at-least-thirty-two-characters"

func TestGatewayAllocatesDedicatedPortAndHandsValidatedPackageToWorker(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	port := reserveTestPort(t)
	gateway, err := NewGateway(Config{
		ControlAddr: "127.0.0.1:0", WorkerAddr: "127.0.0.1:0", DataBindHost: "127.0.0.1", AdvertisedHost: "127.0.0.1",
		DataPortStart: port, DataPortEnd: port, AllowInsecureLoopback: true,
		BootstrapToken: testBootstrapToken, WorkerToken: testWorkerToken, SpoolRoot: t.TempDir(), LeaseTTL: time.Hour,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { gateway.shutdown(context.Background()) })

	lease := requestTestLease(t, gateway, now)
	if lease.DataPort != port || lease.DataURL != "http://127.0.0.1:"+strconv.Itoa(port) {
		t.Fatalf("unexpected dedicated lease: %+v", lease)
	}
	pkg := loadDirectPackageFixture(t)
	message, err := model.EncryptDirectTransportJSON(pkg, lease, "message_package_1", "client_execution_package", model.DirectTransportDirectionUpload, now)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, lease.DataURL+"/v1/direct/packages", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	signDirectRequest(t, request, lease, body, now, "request_nonce_1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		payload, _ := io.ReadAll(response.Body)
		t.Fatalf("upload status %d: %s", response.StatusCode, payload)
	}
	var encryptedReceipt model.DirectEncryptedMessage
	if err := json.NewDecoder(response.Body).Decode(&encryptedReceipt); err != nil {
		t.Fatal(err)
	}
	var receipt model.DirectPackageReceipt
	if err := model.DecryptDirectTransportJSON(encryptedReceipt, lease, "package_receipt", model.DirectTransportDirectionResult, now, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.PackageID != pkg.PackageID || receipt.JobID == "" || receipt.Status != "queued" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}

	claimRequest := httptest.NewRequest(http.MethodPost, "/v1/worker/jobs/claim", nil)
	claimRequest.Header.Set("Authorization", "Bearer "+testWorkerToken)
	claimResponse := httptest.NewRecorder()
	gateway.WorkerHandler().ServeHTTP(claimResponse, claimRequest)
	if claimResponse.Code != http.StatusOK {
		t.Fatalf("claim status %d: %s", claimResponse.Code, claimResponse.Body.String())
	}
	var claimed WorkerJob
	if err := json.Unmarshal(claimResponse.Body.Bytes(), &claimed); err != nil {
		t.Fatal(err)
	}
	if claimed.JobID != receipt.JobID || claimed.LeaseID != lease.LeaseID || claimed.Package.PackageID != pkg.PackageID {
		t.Fatalf("worker received wrong job: %+v", claimed)
	}

	spooled := filepath.Join(gateway.config.SpoolRoot, "jobs", safeSegment(receipt.JobID), "package.json")
	if _, err := os.Stat(spooled); err != nil {
		t.Fatalf("validated package was not spooled: %v", err)
	}
}

func TestGatewayRejectsLeaseAndPackageReplay(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	port := reserveTestPort(t)
	gateway, err := NewGateway(Config{ControlAddr: "127.0.0.1:0", WorkerAddr: "127.0.0.1:0", DataBindHost: "127.0.0.1", AdvertisedHost: "127.0.0.1", DataPortStart: port, DataPortEnd: port, AllowInsecureLoopback: true, BootstrapToken: testBootstrapToken, WorkerToken: testWorkerToken, SpoolRoot: t.TempDir(), LeaseTTL: time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { gateway.shutdown(context.Background()) })
	leaseRequest := signedTestLeaseRequest(t, now, "same_nonce", "test")
	leaseBody, _ := json.Marshal(leaseRequest)
	for attempt := 0; attempt < 2; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/v1/direct/leases", bytes.NewReader(leaseBody))
		request.Header.Set("Authorization", "Bearer "+testBootstrapToken)
		response := httptest.NewRecorder()
		gateway.ControlHandler().ServeHTTP(response, request)
		if attempt == 0 && response.Code != http.StatusCreated {
			t.Fatalf("first lease failed: %s", response.Body.String())
		}
		if attempt == 1 && response.Code != http.StatusConflict {
			t.Fatalf("lease replay status %d", response.Code)
		}
	}
}

func TestGatewayRejectsInstallationImpersonationWithSharedBootstrapToken(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	port := reserveTestPort(t)
	gateway, err := NewGateway(Config{ControlAddr: "127.0.0.1:0", WorkerAddr: "127.0.0.1:0", DataBindHost: "127.0.0.1", AdvertisedHost: "127.0.0.1", DataPortStart: port, DataPortEnd: port, AllowInsecureLoopback: true, BootstrapToken: testBootstrapToken, WorkerToken: testWorkerToken, SpoolRoot: t.TempDir(), LeaseTTL: time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	owner := signedTestLeaseRequest(t, now, "owner_nonce", "owner_key")
	attacker := signedTestLeaseRequest(t, now, "attacker_nonce", "attacker_key")
	attacker.InstallationID = owner.InstallationID
	body, _ := json.Marshal(attacker)
	request := httptest.NewRequest(http.MethodPost, "/v1/direct/leases", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testBootstrapToken)
	response := httptest.NewRecorder()
	gateway.ControlHandler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("shared bootstrap token allowed installation impersonation: %d %s", response.Code, response.Body.String())
	}
}

func TestGatewayCredentialIsBoundMemoryOnlyAndConsumedOnce(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	port := reserveTestPort(t)
	root := t.TempDir()
	gateway, err := NewGateway(Config{ControlAddr: "127.0.0.1:0", WorkerAddr: "127.0.0.1:0", DataBindHost: "127.0.0.1", AdvertisedHost: "127.0.0.1", DataPortStart: port, DataPortEnd: port, AllowInsecureLoopback: true, BootstrapToken: testBootstrapToken, WorkerToken: testWorkerToken, SpoolRoot: root, LeaseTTL: time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { gateway.Close(context.Background()) })
	lease := requestTestLease(t, gateway, now)
	pkg := loadDirectPackageFixture(t)
	pkg.CredentialGrants = []model.CredentialGrant{{GrantID: "grant_login", Kind: "username_password", Purpose: "browser_login", CloudSecretRef: "credential://demo/account", ExpiresAt: now.Add(30 * time.Minute), AllowedDomains: []string{"cascadeai.cn"}, AllowedOperations: []string{"fill_username", "fill_password", "submit_login"}, DeleteAfterRun: true}}
	receipt := uploadTestPackage(t, lease, pkg, now, "message_package_credential", "request_package_credential")
	if receipt.Status != "awaiting_credentials" {
		t.Fatalf("package bypassed credential gate: %+v", receipt)
	}
	canonical, _ := model.CanonicalJSON(pkg)
	envelope := model.DirectCredentialEnvelope{ProtocolVersion: model.DirectTransportProtocolVersion, LeaseID: lease.LeaseID, InstallationID: lease.InstallationID, JobID: receipt.JobID, PackageID: pkg.PackageID, PackageDigest: model.SHA256Hex(canonical), GrantID: "grant_login", SecretRef: "credential://demo/account", IssuedAt: now, ExpiresAt: now.Add(20 * time.Minute), AllowedDomains: []string{"cascadeai.cn"}, AllowedOperations: []string{"fill_username", "fill_password", "submit_login"}, Credential: model.DirectCredentialValue{SecretRef: "credential://demo/account", Username: "private-user", Password: "private-password", ExpiresAt: now.Add(20 * time.Minute), AllowedDomains: []string{"cascadeai.cn"}, AllowedOperations: []string{"fill_username", "fill_password", "submit_login"}}}
	credentialMessage, err := model.EncryptDirectTransportJSON(envelope, lease, "message_credential", "credential_envelope", model.DirectTransportDirectionUpload, now)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(credentialMessage)
	request, _ := http.NewRequest(http.MethodPost, lease.DataURL+"/v1/direct/jobs/"+receipt.JobID+"/credentials", bytes.NewReader(body))
	signDirectRequest(t, request, lease, body, now, "request_credential")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		payload, _ := io.ReadAll(response.Body)
		t.Fatalf("credential upload failed: %d %s", response.StatusCode, payload)
	}
	spoolData, err := os.ReadFile(filepath.Join(root, "jobs", safeSegment(receipt.JobID), "job.json"))
	if err != nil {
		t.Fatal(err)
	}
	packageData, err := os.ReadFile(filepath.Join(root, "jobs", safeSegment(receipt.JobID), "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(spoolData, []byte("private-user")) || bytes.Contains(spoolData, []byte("private-password")) || bytes.Contains(packageData, []byte("private-user")) || bytes.Contains(packageData, []byte("private-password")) {
		t.Fatal("plaintext credential reached the persisted spool")
	}
	claimRequest := httptest.NewRequest(http.MethodPost, "/v1/worker/jobs/claim", nil)
	claimRequest.Header.Set("Authorization", "Bearer "+testWorkerToken)
	claimResponse := httptest.NewRecorder()
	gateway.WorkerHandler().ServeHTTP(claimResponse, claimRequest)
	if claimResponse.Code != http.StatusOK {
		t.Fatalf("claim failed: %s", claimResponse.Body.String())
	}
	consume := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/v1/worker/jobs/"+receipt.JobID+"/credentials/consume", nil)
		request.Header.Set("Authorization", "Bearer "+testWorkerToken)
		response := httptest.NewRecorder()
		gateway.WorkerHandler().ServeHTTP(response, request)
		return response
	}
	first := consume()
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), "private-password") {
		t.Fatalf("first consume failed: %d %s", first.Code, first.Body.String())
	}
	if second := consume(); second.Code != http.StatusGone || strings.Contains(second.Body.String(), "private-password") {
		t.Fatalf("credential was not one-time: %d %s", second.Code, second.Body.String())
	}
}

func TestGatewayDeduplicatesApprovedPackageAcrossReplacementLease(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	port := reserveTestPort(t)
	gateway, err := NewGateway(Config{ControlAddr: "127.0.0.1:0", WorkerAddr: "127.0.0.1:0", DataBindHost: "127.0.0.1", AdvertisedHost: "127.0.0.1", DataPortStart: port, DataPortEnd: port + 10, AllowInsecureLoopback: true, BootstrapToken: testBootstrapToken, WorkerToken: testWorkerToken, SpoolRoot: t.TempDir(), LeaseTTL: time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { gateway.Close(context.Background()) })
	pkg := loadDirectPackageFixture(t)
	firstLease := requestTestLeaseForInstallation(t, gateway, now, "install_dedupe", "lease_dedupe_1")
	firstReceipt := uploadTestPackage(t, firstLease, pkg, now, "message_dedupe_1", "request_dedupe_1")
	secondLease := requestTestLeaseForInstallation(t, gateway, now, "install_dedupe", "lease_dedupe_2")
	if secondLease.LeaseID != firstLease.LeaseID || secondLease.DataPort != firstLease.DataPort || secondLease.LeaseToken != firstLease.LeaseToken || len(gateway.leases) != 1 {
		t.Fatalf("same App installation did not reuse its one active listener: first=%+v second=%+v leases=%d", firstLease, secondLease, len(gateway.leases))
	}
	secondReceipt := uploadTestPackage(t, secondLease, pkg, now, "message_dedupe_2", "request_dedupe_2")
	if firstReceipt.JobID != secondReceipt.JobID || len(gateway.jobs) != 1 {
		t.Fatalf("same installation/package created duplicate jobs: first=%+v second=%+v jobs=%d", firstReceipt, secondReceipt, len(gateway.jobs))
	}
}

func TestGatewayAllocatesDifferentActivePortsToDifferentAppInstallations(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	port := reserveTestPort(t)
	gateway, err := NewGateway(Config{ControlAddr: "127.0.0.1:0", WorkerAddr: "127.0.0.1:0", DataBindHost: "127.0.0.1", AdvertisedHost: "127.0.0.1", DataPortStart: port, DataPortEnd: port + 20, AllowInsecureLoopback: true, BootstrapToken: testBootstrapToken, WorkerToken: testWorkerToken, SpoolRoot: t.TempDir(), LeaseTTL: time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { gateway.Close(context.Background()) })
	first := requestTestLeaseForInstallation(t, gateway, now, "install_one", "lease_one")
	second := requestTestLeaseForInstallation(t, gateway, now, "install_two", "lease_two")
	if first.InstallationID == second.InstallationID || first.LeaseID == second.LeaseID || first.DataPort == second.DataPort || len(gateway.leases) != 2 {
		t.Fatalf("different App installations did not receive isolated active listeners: first=%+v second=%+v leases=%d", first, second, len(gateway.leases))
	}
}

func TestGatewayRetiresExpiredListenerBeforeReallocatingDedicatedPort(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	port := reserveTestPort(t)
	current := now
	gateway, err := NewGateway(Config{
		ControlAddr: "127.0.0.1:0", WorkerAddr: "127.0.0.1:0", DataBindHost: "127.0.0.1", AdvertisedHost: "127.0.0.1",
		DataPortStart: port, DataPortEnd: port, AllowInsecureLoopback: true,
		BootstrapToken: testBootstrapToken, WorkerToken: testWorkerToken, SpoolRoot: t.TempDir(), LeaseTTL: time.Minute,
		Now: func() time.Time { return current },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { gateway.Close(context.Background()) })
	first := requestTestLeaseForInstallation(t, gateway, current, "install_expired_one", "lease_expired_one")
	current = first.ExpiresAt.Add(time.Millisecond)
	second := requestTestLeaseForInstallation(t, gateway, current, "install_expired_two", "lease_expired_two")
	if second.DataPort != port || second.InstallationID == first.InstallationID || len(gateway.leases) != 1 {
		t.Fatalf("expired listener was not retired and port was not reused: first=%+v second=%+v leases=%d", first, second, len(gateway.leases))
	}
}

func TestGatewayReleasesDedicatedLeaseWithInstallationSignature(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	port := reserveTestPort(t)
	gateway, err := NewGateway(Config{ControlAddr: "127.0.0.1:0", WorkerAddr: "127.0.0.1:0", DataBindHost: "127.0.0.1", AdvertisedHost: "127.0.0.1", DataPortStart: port, DataPortEnd: port, AllowInsecureLoopback: true, BootstrapToken: testBootstrapToken, WorkerToken: testWorkerToken, SpoolRoot: t.TempDir(), LeaseTTL: time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { gateway.Close(context.Background()) })
	lease := requestTestLeaseForInstallation(t, gateway, now, "install_release", "release_nonce_1")
	request := signedTestLeaseReleaseRequest(t, now, lease, "release_request_1", "install_release")
	body, _ := json.Marshal(request)
	httpRequest := httptest.NewRequest(http.MethodPost, "/v1/direct/leases/release", bytes.NewReader(body))
	httpRequest.Header.Set("Authorization", "Bearer "+testBootstrapToken)
	response := httptest.NewRecorder()
	gateway.ControlHandler().ServeHTTP(response, httpRequest)
	if response.Code != http.StatusOK || len(gateway.leases) != 0 {
		t.Fatalf("dedicated lease was not released: status=%d body=%s leases=%d", response.Code, response.Body.String(), len(gateway.leases))
	}
}

func TestGatewayRejectsLeaseReleaseWhileInstallationHasActiveJob(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	port := reserveTestPort(t)
	gateway, err := NewGateway(Config{ControlAddr: "127.0.0.1:0", WorkerAddr: "127.0.0.1:0", DataBindHost: "127.0.0.1", AdvertisedHost: "127.0.0.1", DataPortStart: port, DataPortEnd: port, AllowInsecureLoopback: true, BootstrapToken: testBootstrapToken, WorkerToken: testWorkerToken, SpoolRoot: t.TempDir(), LeaseTTL: time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { gateway.Close(context.Background()) })
	lease := requestTestLeaseForInstallation(t, gateway, now, "install_active_release", "active_release_nonce")
	pkg := loadDirectPackageFixture(t)
	uploadTestPackage(t, lease, pkg, now, "active_release_package", "active_release_upload")
	release := signedTestLeaseReleaseRequest(t, now, lease, "active_release_request", "install_active_release")
	body, _ := json.Marshal(release)
	request := httptest.NewRequest(http.MethodPost, "/v1/direct/leases/release", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testBootstrapToken)
	response := httptest.NewRecorder()
	gateway.ControlHandler().ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "lease_has_active_jobs") || len(gateway.leases) != 1 {
		t.Fatalf("active installation lease was not protected: status=%d body=%s leases=%d", response.Code, response.Body.String(), len(gateway.leases))
	}
}

func TestGatewayRejectsWorkerStatusThatBypassesValidatedResult(t *testing.T) {
	port := reserveTestPort(t)
	gateway, err := NewGateway(Config{ControlAddr: "127.0.0.1:0", WorkerAddr: "127.0.0.1:0", DataBindHost: "127.0.0.1", AdvertisedHost: "127.0.0.1", DataPortStart: port, DataPortEnd: port, AllowInsecureLoopback: true, BootstrapToken: testBootstrapToken, WorkerToken: testWorkerToken, SpoolRoot: t.TempDir(), LeaseTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	pkg := loadDirectPackageFixture(t)
	canonical, _ := model.CanonicalJSON(pkg)
	record, err := gateway.persistNewJob("job_status_gate", "lease_status_gate", "install_status_gate", pkg, model.SHA256Hex(canonical))
	if err != nil {
		t.Fatal(err)
	}
	gateway.jobs[record.Status.JobID] = record
	for _, update := range []WorkerStatusUpdate{
		{Status: "completed", Stage: "completed", ProgressPercent: 100, ResultPackageID: "forged_result"},
		{Status: "running", Stage: "runtime", ProgressPercent: 50, ResultPackageID: "forged_result"},
		{Status: "failed", Stage: "failed", ProgressPercent: 90},
	} {
		body, _ := json.Marshal(update)
		request := httptest.NewRequest(http.MethodPut, "/v1/worker/jobs/job_status_gate/status", bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+testWorkerToken)
		response := httptest.NewRecorder()
		gateway.WorkerHandler().ServeHTTP(response, request)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("unsafe status update was accepted (%d): %s", response.Code, response.Body.String())
		}
	}
	if gateway.jobs["job_status_gate"].Status.Status != "queued" || gateway.jobs["job_status_gate"].Status.ResultPackageID != "" {
		t.Fatalf("rejected status update mutated the job: %+v", gateway.jobs["job_status_gate"].Status)
	}
}

func uploadTestPackage(t *testing.T, lease model.DirectPortLease, pkg model.ClientExecutionPackage, now time.Time, messageID, requestNonce string) model.DirectPackageReceipt {
	t.Helper()
	message, err := model.EncryptDirectTransportJSON(pkg, lease, messageID, "client_execution_package", model.DirectTransportDirectionUpload, now)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(message)
	request, err := http.NewRequest(http.MethodPost, lease.DataURL+"/v1/direct/packages", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	signDirectRequest(t, request, lease, body, now, requestNonce)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted && response.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(response.Body)
		t.Fatalf("package upload failed %d: %s", response.StatusCode, payload)
	}
	var encrypted model.DirectEncryptedMessage
	if err := json.NewDecoder(response.Body).Decode(&encrypted); err != nil {
		t.Fatal(err)
	}
	var receipt model.DirectPackageReceipt
	if err := model.DecryptDirectTransportJSON(encrypted, lease, "package_receipt", model.DirectTransportDirectionResult, now, &receipt); err != nil {
		t.Fatal(err)
	}
	return receipt
}

func TestGatewayRecoversRunningJobAndRebindsOnlySameInstallation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	root := t.TempDir()
	port := reserveTestPort(t)
	config := Config{ControlAddr: "127.0.0.1:0", WorkerAddr: "127.0.0.1:0", DataBindHost: "127.0.0.1", AdvertisedHost: "127.0.0.1", DataPortStart: port, DataPortEnd: port + 10, AllowInsecureLoopback: true, BootstrapToken: testBootstrapToken, WorkerToken: testWorkerToken, SpoolRoot: root, LeaseTTL: time.Hour, Now: func() time.Time { return now }}
	first, err := NewGateway(config)
	if err != nil {
		t.Fatal(err)
	}
	pkg := loadDirectPackageFixture(t)
	canonical, _ := model.CanonicalJSON(pkg)
	record, err := first.persistNewJob("job_restart", "lease_before_restart", testDirectInstallationID("install_same"), pkg, model.SHA256Hex(canonical))
	if err != nil {
		t.Fatal(err)
	}
	first.jobs[record.Status.JobID] = record
	claim := httptest.NewRequest(http.MethodPost, "/v1/worker/jobs/claim", nil)
	claim.Header.Set("Authorization", "Bearer "+testWorkerToken)
	claimResponse := httptest.NewRecorder()
	first.WorkerHandler().ServeHTTP(claimResponse, claim)
	if claimResponse.Code != http.StatusOK {
		t.Fatalf("initial claim failed: %s", claimResponse.Body.String())
	}
	first.Close(context.Background())

	restarted, err := NewGateway(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.Close(context.Background()) })
	recovered := restarted.jobs["job_restart"]
	if recovered == nil || recovered.Status.Status != "queued" || recovered.Status.Stage != "recovered_after_restart" {
		t.Fatalf("running job was not recovered for reclaim: %+v", recovered)
	}
	sameLease := requestTestLeaseForInstallation(t, restarted, now, "install_same", "restart_same_nonce")
	if _, ok := restarted.jobForLease("job_restart", sameLease.LeaseID); !ok {
		t.Fatal("same App installation could not continue its persisted job on a replacement lease")
	}
	otherLease := requestTestLeaseForInstallation(t, restarted, now, "install_other", "restart_other_nonce")
	if _, ok := restarted.jobForLease("job_restart", otherLease.LeaseID); ok {
		t.Fatal("different App installation accessed a persisted job")
	}
}

func TestGatewayWorkerReleaseRestoresCredentialGateOrQueue(t *testing.T) {
	for _, test := range []struct {
		name       string
		credential bool
		wantStatus string
		wantStage  string
	}{
		{name: "credential job", credential: true, wantStatus: "awaiting_credentials", wantStage: "credential_reupload_required"},
		{name: "credentialless job", credential: false, wantStatus: "queued", wantStage: "worker_released"},
	} {
		t.Run(test.name, func(t *testing.T) {
			port := reserveTestPort(t)
			gateway, err := NewGateway(Config{ControlAddr: "127.0.0.1:0", WorkerAddr: "127.0.0.1:0", DataBindHost: "127.0.0.1", AdvertisedHost: "127.0.0.1", DataPortStart: port, DataPortEnd: port, AllowInsecureLoopback: true, BootstrapToken: testBootstrapToken, WorkerToken: testWorkerToken, SpoolRoot: t.TempDir(), LeaseTTL: time.Hour})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { gateway.Close(context.Background()) })
			pkg := loadDirectPackageFixture(t)
			if test.credential {
				pkg.CredentialGrants = []model.CredentialGrant{{GrantID: "grant_login", Kind: "username_password", Purpose: "browser_login", CloudSecretRef: "credential://demo/account", ExpiresAt: time.Now().Add(time.Hour), AllowedDomains: []string{"example.com"}, AllowedOperations: []string{"fill_username", "fill_password", "submit_login"}, DeleteAfterRun: true}}
			}
			canonical, _ := model.CanonicalJSON(pkg)
			record, err := gateway.persistNewJob("job_release", "lease_release", "install_release", pkg, model.SHA256Hex(canonical))
			if err != nil {
				t.Fatal(err)
			}
			record.Status.Status = "queued"
			gateway.jobs[record.Status.JobID] = record
			claim := httptest.NewRequest(http.MethodPost, "/v1/worker/jobs/claim", nil)
			claim.Header.Set("Authorization", "Bearer "+testWorkerToken)
			claimResponse := httptest.NewRecorder()
			gateway.WorkerHandler().ServeHTTP(claimResponse, claim)
			if claimResponse.Code != http.StatusOK {
				t.Fatalf("claim failed: %s", claimResponse.Body.String())
			}
			release := httptest.NewRequest(http.MethodPost, "/v1/worker/jobs/job_release/release", bytes.NewReader([]byte(`{"reason":"worker_failed"}`)))
			release.Header.Set("Authorization", "Bearer "+testWorkerToken)
			releaseResponse := httptest.NewRecorder()
			gateway.WorkerHandler().ServeHTTP(releaseResponse, release)
			if releaseResponse.Code != http.StatusOK {
				t.Fatalf("release failed: %s", releaseResponse.Body.String())
			}
			status := gateway.jobs["job_release"].Status
			if status.Status != test.wantStatus || status.Stage != test.wantStage {
				t.Fatalf("unexpected released job state: %+v", status)
			}
		})
	}
}

func requestTestLease(t *testing.T, gateway *Gateway, now time.Time) model.DirectPortLease {
	t.Helper()
	return requestTestLeaseForInstallation(t, gateway, now, "install_test", "lease_nonce_1")
}

func requestTestLeaseForInstallation(t *testing.T, gateway *Gateway, now time.Time, installationID, nonce string) model.DirectPortLease {
	t.Helper()
	payload := signedTestLeaseRequest(t, now, nonce, installationID)
	body, _ := json.Marshal(payload)
	request := httptest.NewRequest(http.MethodPost, "/v1/direct/leases", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testBootstrapToken)
	response := httptest.NewRecorder()
	gateway.ControlHandler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("lease status %d: %s", response.Code, response.Body.String())
	}
	var lease model.DirectPortLease
	if err := json.Unmarshal(response.Body.Bytes(), &lease); err != nil {
		t.Fatal(err)
	}
	return lease
}

func signedTestLeaseRequest(t *testing.T, now time.Time, nonce, keySeed string) model.DirectLeaseRequest {
	t.Helper()
	seed := sha256.Sum256([]byte("direct-test-installation-key|" + keySeed))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	publicKey := privateKey.Public().(ed25519.PublicKey)
	payload := model.DirectLeaseRequest{ProtocolVersion: model.DirectTransportProtocolVersion, InstallationID: model.DirectInstallationID(publicKey), ClientVersion: "test", TimestampUnixMS: now.UnixMilli(), RequestNonce: nonce, SigningPublicKeyBase64: base64.StdEncoding.EncodeToString(publicKey)}
	payload.SignatureBase64 = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, model.DirectLeaseRequestSigningPayload(payload)))
	return payload
}

func signedTestLeaseReleaseRequest(t *testing.T, now time.Time, lease model.DirectPortLease, nonce, keySeed string) model.DirectLeaseReleaseRequest {
	t.Helper()
	seed := sha256.Sum256([]byte("direct-test-installation-key|" + keySeed))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	publicKey := privateKey.Public().(ed25519.PublicKey)
	request := model.DirectLeaseReleaseRequest{ProtocolVersion: model.DirectTransportProtocolVersion, InstallationID: model.DirectInstallationID(publicKey), LeaseID: lease.LeaseID, TimestampUnixMS: now.UnixMilli(), RequestNonce: nonce, SigningPublicKeyBase64: base64.StdEncoding.EncodeToString(publicKey)}
	request.SignatureBase64 = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, model.DirectLeaseReleaseRequestSigningPayload(request)))
	return request
}

func testDirectInstallationID(keySeed string) string {
	seed := sha256.Sum256([]byte("direct-test-installation-key|" + keySeed))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	return model.DirectInstallationID(privateKey.Public().(ed25519.PublicKey))
}
func signDirectRequest(t *testing.T, request *http.Request, lease model.DirectPortLease, body []byte, now time.Time, nonce string) {
	t.Helper()
	digest := model.SHA256Hex(body)
	signature, err := model.DirectTransportRequestSignature(lease, request.Method, request.URL.EscapedPath(), now.UnixMilli(), nonce, digest)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Cascade-Timestamp", fmt.Sprintf("%d", now.UnixMilli()))
	request.Header.Set("X-Cascade-Nonce", nonce)
	request.Header.Set("X-Cascade-Body-SHA256", digest)
	request.Header.Set("X-Cascade-Signature", signature)
	request.Header.Set("Content-Type", "application/json")
}
func reserveTestPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}
func loadDirectPackageFixture(t *testing.T) model.ClientExecutionPackage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "exchange", "v1", "client_execution_package.browser_agent_outline.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg model.ClientExecutionPackage
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatal(err)
	}
	// Most gateway transport tests exercise packages that do not need a secret.
	// Credential-gated behavior has dedicated tests below.
	pkg.CredentialGrants = nil
	if err := model.ValidateClientExecutionPackageForCloudExecution(&pkg); err != nil {
		t.Fatalf("fixture invalid: %v", err)
	}
	return pkg
}
