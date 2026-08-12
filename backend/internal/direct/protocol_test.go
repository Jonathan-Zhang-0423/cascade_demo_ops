package direct

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func signedLeaseRequest(t *testing.T, now time.Time) DirectLeaseRequest {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	r := DirectLeaseRequest{ProtocolVersion: ProtocolVersion, InstallationID: InstallationID(pub), ClientVersion: "app-test", TimestampUnixMS: now.UnixMilli(), RequestNonce: "nonce-1", SigningPublicKeyBase64: base64.StdEncoding.EncodeToString(pub)}
	r.SignatureBase64 = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, LeaseRequestSigningBytes(r)))
	return r
}

func TestLeaseRequestIdentitySignatureAndReplay(t *testing.T) {
	now := time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC)
	seen := NewNonceSet()
	request := signedLeaseRequest(t, now)
	if _, err := VerifyLeaseRequest(request, now, seen); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyLeaseRequest(request, now, seen); err != ErrReplay {
		t.Fatalf("replay err = %v", err)
	}
	request.InstallationID = "direct_install_wrong"
	if _, err := VerifyLeaseRequest(request, now, nil); err == nil {
		t.Fatal("expected public-key identity mismatch")
	}
}

func TestEncryptedMessageBindsLeasePortTypeAndDirection(t *testing.T) {
	now := time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC)
	plaintext := []byte(`{"package_id":"pkg-1"}`)
	m, err := EncryptMessage("secret-token", "direct_install_abc", "lease-1", 24000, "message-1", "execution_package", "app_to_browser_agent", plaintext, now)
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.Decrypt("secret-token", "direct_install_abc", 24000)
	if err != nil || !bytes.Equal(got, plaintext) {
		t.Fatalf("decrypt = %q, %v", got, err)
	}
	tampered := m
	tampered.MessageType = "credential_envelope"
	if _, err := tampered.Decrypt("secret-token", "direct_install_abc", 24000); err == nil {
		t.Fatal("expected AAD tamper rejection")
	}
	if _, err := m.Decrypt("secret-token", "direct_install_abc", 24001); err == nil {
		t.Fatal("expected cross-port rejection")
	}
	seen := NewNonceSet()
	if err := ValidateMessageFreshness(m, now, seen); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMessageFreshness(m, now, seen); err != ErrReplay {
		t.Fatalf("message replay err = %v", err)
	}
}

func TestDataRequestSignatureAndReplay(t *testing.T) {
	now := time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC)
	body := []byte(`{"ciphertext":"value"}`)
	path := "/v1/direct/packages"
	req := &http.Request{Method: http.MethodPost, URL: &url.URL{Path: path}, Header: http.Header{}}
	req.Header.Set("X-Cascade-Timestamp", "1786323723000")
	req.Header.Set("X-Cascade-Nonce", "request-1")
	req.Header.Set("X-Cascade-Body-SHA256", HashSHA256(body))
	req.Header.Set("X-Cascade-Signature", SignDataRequest(req.Method, path, now.UnixMilli(), "request-1", HashSHA256(body), "lease-token", "install-1"))
	seen := NewNonceSet()
	if err := VerifyDataRequest(req, body, "lease-token", "install-1", now, seen); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDataRequest(req, body, "lease-token", "install-1", now, seen); err != ErrReplay {
		t.Fatalf("replay err = %v", err)
	}
}

func TestGatewayLeaseJobCredentialArtifactLifecycle(t *testing.T) {
	now := time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC)
	gateway := NewGateway("gateway.example", time.Hour)
	gateway.now = func() time.Time { return now }
	request := signedLeaseRequest(t, now)
	lease, err := gateway.Allocate(request)
	if err != nil {
		t.Fatal(err)
	}
	if lease.DataPort != DataPortMin {
		t.Fatalf("data port = %d", lease.DataPort)
	}
	receipt, err := gateway.CreateJob(lease.LeaseID, lease.InstallationID, "pkg-1", HashSHA256([]byte("pkg")), []byte("pkg"))
	if err != nil {
		t.Fatal(err)
	}
	if err := gateway.StoreCredential(receipt.JobID, lease.LeaseID, DirectCredentialEnvelope{JobID: receipt.JobID, PackageID: "pkg-1", PackageSHA256: HashSHA256([]byte("pkg")), GrantID: "grant-1", SecretRef: "secret://login", InstallationID: lease.InstallationID, LeaseID: lease.LeaseID, AllowedDomains: []string{"example.com"}, AllowedOperations: []string{"login"}, ExpiresAtUnixMS: now.Add(time.Minute).UnixMilli(), Secret: "sensitive"}); err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.ClaimWorker(receipt.JobID); err != nil {
		t.Fatal(err)
	}
	credential, err := gateway.ConsumeCredential(receipt.JobID)
	if err != nil || credential.Secret != "sensitive" {
		t.Fatalf("credential = %+v, %v", credential, err)
	}
	if _, err := gateway.ConsumeCredential(receipt.JobID); err == nil {
		t.Fatal("expected one-time credential consumption")
	}
	data := bytes.Repeat([]byte("a"), MaxChunkSize+7)
	descriptor, err := gateway.AddArtifact(receipt.JobID, "video", "mp4", "video/mp4", data)
	if err != nil || descriptor.ChunkCount != 2 {
		t.Fatalf("descriptor = %+v, %v", descriptor, err)
	}
	chunk, _, err := gateway.ArtifactChunk(receipt.JobID, lease.LeaseID, "video", 1)
	if err != nil || len(chunk) != 7 {
		t.Fatalf("chunk len = %d, %v", len(chunk), err)
	}
	if err := gateway.CompleteJob(receipt.JobID, "result-1", []byte(`{"result_id":"result-1"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestGatewayCredentialRequirementAndExplicitResultAck(t *testing.T) {
	now := time.Date(2026, 8, 10, 2, 0, 0, 0, time.UTC)
	gateway := NewGateway("gateway.example", time.Hour)
	gateway.now = func() time.Time { return now }
	lease, err := gateway.Allocate(signedLeaseRequest(t, now))
	if err != nil {
		t.Fatal(err)
	}
	packageBytes := []byte("approved-package")
	receipt, err := gateway.CreateJobWithSourceDigestAndCredentialRequirement(lease.LeaseID, lease.InstallationID, "pkg-await", HashSHA256(packageBytes), "approved-source", packageBytes, true)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "awaiting_credentials" || len(gateway.QueuedJobIDs()) != 0 {
		t.Fatalf("credential-required job must await credentials: %+v queued=%v", receipt, gateway.QueuedJobIDs())
	}
	credential := DirectCredentialEnvelope{JobID: receipt.JobID, PackageID: receipt.PackageID, PackageSHA256: receipt.PackageSHA256, GrantID: "grant", SecretRef: "vault://login", Secret: "secret", InstallationID: lease.InstallationID, LeaseID: lease.LeaseID, AllowedDomains: []string{"example.com"}, AllowedOperations: []string{"login"}, ExpiresAtUnixMS: now.Add(time.Minute).UnixMilli()}
	if err := gateway.StoreCredential(receipt.JobID, lease.LeaseID, credential); err != nil {
		t.Fatal(err)
	}
	if len(gateway.QueuedJobIDs()) != 1 {
		t.Fatalf("credential upload must queue job: %v", gateway.QueuedJobIDs())
	}
	if _, err := gateway.ClaimWorker(receipt.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.AddArtifact(receipt.JobID, "artifact-video", "demo_video", "video/mp4", []byte("video")); err != nil {
		t.Fatal(err)
	}
	if err := gateway.CompleteJob(receipt.JobID, "result-await", []byte(`{"result_id":"result-await"}`)); err != nil {
		t.Fatal(err)
	}
	ack, err := gateway.AckResult(receipt.JobID, lease.LeaseID, DirectResultAckRequest{ProtocolVersion: ProtocolVersion, InstallationID: lease.InstallationID, JobID: receipt.JobID, ResultPackageID: "result-await", ReceivedArtifactIDs: []string{"artifact-video"}, VerifiedChecksums: true, AckedAt: now})
	if err != nil || ack.ResultPackageID != "result-await" || !ack.VerifiedChecksums || ack.AckedAt.IsZero() {
		t.Fatalf("explicit result ack failed: %+v err=%v", ack, err)
	}
	idempotent, err := gateway.AckResult(receipt.JobID, lease.LeaseID, DirectResultAckRequest{ProtocolVersion: ProtocolVersion, InstallationID: lease.InstallationID, JobID: receipt.JobID, ResultPackageID: "result-await", ReceivedArtifactIDs: []string{"artifact-video"}, VerifiedChecksums: true, AckedAt: now.Add(time.Second)})
	if err != nil || !idempotent.AckedAt.Equal(ack.AckedAt) {
		t.Fatalf("ack must be idempotent: first=%+v second=%+v err=%v", ack, idempotent, err)
	}
}

func TestGatewayPackageUploadIsIdempotentAndRejectsPayloadDrift(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	gateway := NewGateway("gateway.example", time.Hour)
	gateway.now = func() time.Time { return now }
	lease, err := gateway.Allocate(signedLeaseRequest(t, now))
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"package_id":"pkg-idempotent","approved":true}`)
	transportDigest := HashSHA256(payload)
	first, err := gateway.CreateJobWithSourceDigestAndCredentialRequirement(lease.LeaseID, lease.InstallationID, "pkg-idempotent", transportDigest, "final-package-digest", payload, true)
	if err != nil {
		t.Fatal(err)
	}

	retry, err := gateway.CreateJobWithSourceDigestAndCredentialRequirement(lease.LeaseID, lease.InstallationID, "pkg-idempotent", transportDigest, "final-package-digest", append([]byte{}, payload...), true)
	if err != nil {
		t.Fatal(err)
	}
	if retry.JobID != first.JobID || retry.CreatedAtUnixMS != first.CreatedAtUnixMS || retry.Status != first.Status {
		t.Fatalf("idempotent retry created or changed the job: first=%+v retry=%+v", first, retry)
	}

	drifted := []byte(`{"package_id":"pkg-idempotent","approved":false}`)
	if _, err := gateway.CreateJobWithSourceDigestAndCredentialRequirement(lease.LeaseID, lease.InstallationID, "pkg-idempotent", HashSHA256(drifted), "final-package-digest", drifted, true); !errors.Is(err, ErrPackageIdempotencyConflict) {
		t.Fatalf("payload drift error = %v", err)
	}
	if ids := gateway.QueuedJobIDs(); len(ids) != 0 {
		t.Fatalf("credential-required idempotent upload must still have one awaiting job, queued=%v", ids)
	}
}

func TestGatewayConcurrentPackageRetriesCreateOneJob(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 30, 0, 0, time.UTC)
	gateway := NewGateway("gateway.example", time.Hour)
	gateway.now = func() time.Time { return now }
	lease, err := gateway.Allocate(signedLeaseRequest(t, now))
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"package_id":"pkg-concurrent"}`)
	digest := HashSHA256(payload)
	const attempts = 12
	receipts := make(chan DirectPackageReceipt, attempts)
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			receipt, createErr := gateway.CreateJobWithSourceDigest(lease.LeaseID, lease.InstallationID, "pkg-concurrent", digest, "final-concurrent-digest", payload)
			if createErr != nil {
				errs <- createErr
				return
			}
			receipts <- receipt
		}()
	}
	wg.Wait()
	close(receipts)
	close(errs)
	for createErr := range errs {
		t.Fatal(createErr)
	}
	jobID := ""
	for receipt := range receipts {
		if jobID == "" {
			jobID = receipt.JobID
		}
		if receipt.JobID != jobID {
			t.Fatalf("concurrent retry created multiple jobs: %q and %q", jobID, receipt.JobID)
		}
	}
	if ids := gateway.QueuedJobIDs(); len(ids) != 1 || ids[0] != jobID {
		t.Fatalf("queued jobs = %v, want one job %q", ids, jobID)
	}
}

func TestGatewayQueuedJobsAndTerminalArtifactGuards(t *testing.T) {
	now := time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC)
	gateway := NewGateway("gateway.example", time.Hour)
	gateway.now = func() time.Time { return now }
	request := signedLeaseRequest(t, now)
	lease, err := gateway.Allocate(request)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := gateway.CreateJob(lease.LeaseID, lease.InstallationID, "pkg-queued", "digest", []byte(`{"runtime":"browser-agent-outline-v1"}`))
	if err != nil {
		t.Fatal(err)
	}
	ids := gateway.QueuedJobIDs()
	if len(ids) != 1 || ids[0] != receipt.JobID {
		t.Fatalf("queued ids = %+v", ids)
	}
	if err := gateway.UpdateJob(receipt.JobID, 10, "too-early"); err == nil {
		t.Fatal("expected queued job progress rejection")
	}
	if _, err := gateway.AddArtifact(receipt.JobID, "artifact-before-claim", "mp4", "video/mp4", []byte("video")); err == nil {
		t.Fatal("expected queued job artifact rejection")
	}
	if _, err := gateway.ClaimWorker(receipt.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.ClaimWorker(receipt.JobID); err == nil {
		t.Fatal("expected duplicate worker claim rejection")
	}
	if _, err := gateway.AddArtifact(receipt.JobID, "artifact-1", "mp4", "video/mp4", []byte("video")); err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.AddArtifact(receipt.JobID, "artifact-1", "mp4", "video/mp4", []byte("video")); err == nil {
		t.Fatal("expected duplicate artifact rejection")
	}
	if err := gateway.CompleteJob(receipt.JobID, "result-1", []byte(`{"result_id":"result-1"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.AddArtifact(receipt.JobID, "artifact-2", "mp4", "video/mp4", []byte("late")); err == nil {
		t.Fatal("expected terminal artifact rejection")
	}
}

func TestGatewayStoresAuthoritativeFailedResult(t *testing.T) {
	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	gateway := NewGateway("gateway.example", time.Hour)
	gateway.now = func() time.Time { return now }
	lease, err := gateway.Allocate(signedLeaseRequest(t, now))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := gateway.CreateJob(lease.LeaseID, lease.InstallationID, "pkg-failed", "transport-digest", []byte(`{"package_id":"pkg-failed"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.ClaimWorker(receipt.JobID); err != nil {
		t.Fatal(err)
	}
	if err := gateway.CommitJobResult(receipt.JobID, "result-failed", []byte(`{"status":"failed"}`), "failed"); err != nil {
		t.Fatal(err)
	}
	job, err := gateway.Job(receipt.JobID, lease.LeaseID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status.Status != "failed" || job.Status.Stage != "failed_result_ready" || job.Status.ResultPackageID != "result-failed" || len(job.ResultJSON) == 0 {
		t.Fatalf("failed result was not preserved as an authoritative terminal result: %+v", job.Status)
	}
}

func TestCredentialEnvelopeRequiresApprovedScopeFields(t *testing.T) {
	now := time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC)
	gateway := NewGateway("gateway.example", time.Hour)
	gateway.now = func() time.Time { return now }
	request := signedLeaseRequest(t, now)
	lease, err := gateway.Allocate(request)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := gateway.CreateJob(lease.LeaseID, lease.InstallationID, "pkg-cred", "digest", []byte("pkg"))
	if err != nil {
		t.Fatal(err)
	}
	envelope := DirectCredentialEnvelope{JobID: receipt.JobID, PackageID: receipt.PackageID, PackageSHA256: receipt.PackageSHA256, SecretRef: "secret://ref", Secret: "secret", InstallationID: lease.InstallationID, LeaseID: lease.LeaseID, ExpiresAtUnixMS: now.Add(time.Minute).UnixMilli()}
	if err := gateway.StoreCredential(receipt.JobID, lease.LeaseID, envelope); err == nil {
		t.Fatal("expected missing grant and approved scope rejection")
	}
}

func TestCredentialEnvelopeRequiresRunningJobAndIsRecheckedAtConsumption(t *testing.T) {
	clock := time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC)
	gateway := NewGateway("gateway.example", time.Hour)
	gateway.now = func() time.Time { return clock }
	lease, err := gateway.Allocate(signedLeaseRequest(t, clock))
	if err != nil {
		t.Fatal(err)
	}
	packageBytes := []byte("pkg")
	receipt, err := gateway.CreateJob(lease.LeaseID, lease.InstallationID, "pkg-cred-expiry", HashSHA256(packageBytes), packageBytes)
	if err != nil {
		t.Fatal(err)
	}
	envelope := DirectCredentialEnvelope{
		JobID: receipt.JobID, PackageID: receipt.PackageID, PackageSHA256: receipt.PackageSHA256,
		GrantID: "grant-1", SecretRef: "secret://login", Secret: "sensitive",
		InstallationID: lease.InstallationID, LeaseID: lease.LeaseID,
		AllowedDomains: []string{"example.com"}, AllowedOperations: []string{"login"},
		ExpiresAtUnixMS: clock.Add(time.Minute).UnixMilli(),
	}
	if err := gateway.StoreCredential(receipt.JobID, lease.LeaseID, envelope); err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.ConsumeCredential(receipt.JobID); err == nil || !strings.Contains(err.Error(), "running job") {
		t.Fatalf("expected pre-claim consumption rejection, got %v", err)
	}
	if _, err := gateway.ClaimWorker(receipt.JobID); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(2 * time.Minute)
	if _, err := gateway.ConsumeCredential(receipt.JobID); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected consumption-time expiry rejection, got %v", err)
	}
	if _, err := gateway.ConsumeCredential(receipt.JobID); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("expired credential must be destroyed, got %v", err)
	}
}

func TestGatewayKeepsTransportAndApprovedSourceDigestsSeparate(t *testing.T) {
	now := time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC)
	gateway := NewGateway("gateway.example", time.Hour)
	gateway.now = func() time.Time { return now }
	request := signedLeaseRequest(t, now)
	lease, err := gateway.Allocate(request)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := gateway.CreateJobWithSourceDigest(lease.LeaseID, lease.InstallationID, "pkg-digest", "transport-digest", "approved-source-digest", []byte("package"))
	if err != nil {
		t.Fatal(err)
	}
	job, err := gateway.WorkerJob(receipt.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.PackageSHA256 != "transport-digest" || job.SourcePackageDigest != "approved-source-digest" {
		t.Fatalf("digest binding = transport=%q source=%q", job.PackageSHA256, job.SourcePackageDigest)
	}
}
