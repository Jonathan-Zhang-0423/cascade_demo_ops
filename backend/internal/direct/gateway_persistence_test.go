package direct

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func signedPersistentLeaseRequest(t *testing.T, public ed25519.PublicKey, private ed25519.PrivateKey, now time.Time, nonce string) DirectLeaseRequest {
	t.Helper()
	request := DirectLeaseRequest{
		ProtocolVersion: ProtocolVersion, InstallationID: InstallationID(public), ClientVersion: "app-persistence-test",
		TimestampUnixMS: now.UnixMilli(), RequestNonce: nonce, SigningPublicKeyBase64: base64.StdEncoding.EncodeToString(public),
	}
	request.SignatureBase64 = base64.StdEncoding.EncodeToString(ed25519.Sign(private, LeaseRequestSigningBytes(request)))
	return request
}

func newPersistentTestGateway(t *testing.T, path string, now time.Time) *Gateway {
	t.Helper()
	gateway, err := NewPersistentGateway("gateway.example", time.Hour, path)
	if err != nil {
		t.Fatal(err)
	}
	gateway.now = func() time.Time { return now }
	return gateway
}

func TestPersistentGatewayRecoversRunningOrdinaryJobForReclaim(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	path := filepath.Join(t.TempDir(), "direct-state.json")
	gateway := newPersistentTestGateway(t, path, now)
	lease, err := gateway.Allocate(signedLeaseRequest(t, now))
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"package_id":"pkg-recover"}`)
	receipt, err := gateway.CreateJobWithSourceDigest(lease.LeaseID, lease.InstallationID, "pkg-recover", HashSHA256(payload), "final-recover-digest", payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.ClaimWorker(receipt.JobID); err != nil {
		t.Fatal(err)
	}
	if err := gateway.UpdateJob(receipt.JobID, 45, "recording"); err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.AddArtifact(receipt.JobID, "partial-video", "raw_recording", "video/webm", []byte("partial")); err != nil {
		t.Fatal(err)
	}

	restarted := newPersistentTestGateway(t, path, now.Add(time.Minute))
	job, err := restarted.Job(receipt.JobID, lease.LeaseID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status.Status != "queued" || job.Status.Stage != "gateway_recovered" || job.Status.Progress != 0 {
		t.Fatalf("recovered status = %+v", job.Status)
	}
	if len(job.Artifacts) != 0 || len(job.Status.ArtifactIDs) != 0 {
		t.Fatalf("partial running artifacts must not survive reclaim: %+v", job.Status.ArtifactIDs)
	}
	if ids := restarted.QueuedJobIDs(); len(ids) != 1 || ids[0] != receipt.JobID {
		t.Fatalf("queued jobs = %v", ids)
	}
	if _, err := restarted.ClaimWorker(receipt.JobID); err != nil {
		t.Fatalf("recovered job was not reclaimable: %v", err)
	}
	retry, err := restarted.CreateJobWithSourceDigest(lease.LeaseID, lease.InstallationID, "pkg-recover", HashSHA256(payload), "final-recover-digest", payload)
	if err != nil || retry.JobID != receipt.JobID {
		t.Fatalf("recovered idempotent upload = %+v err=%v", retry, err)
	}
}

func TestPersistentGatewayNeverStoresCredentialAndRequiresFreshEnvelope(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	path := filepath.Join(t.TempDir(), "direct-state.json")
	gateway := newPersistentTestGateway(t, path, now)
	lease, err := gateway.Allocate(signedLeaseRequest(t, now))
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"package_id":"pkg-credential-recover"}`)
	receipt, err := gateway.CreateJobWithSourceDigestAndCredentialRequirement(lease.LeaseID, lease.InstallationID, "pkg-credential-recover", HashSHA256(payload), "final-credential-digest", payload, true)
	if err != nil {
		t.Fatal(err)
	}
	envelope := DirectCredentialEnvelope{
		JobID: receipt.JobID, PackageID: receipt.PackageID, PackageSHA256: receipt.PackageSHA256,
		GrantID: "grant-sensitive", SecretRef: "vault://sensitive-login", Secret: "never-write-this-password",
		InstallationID: lease.InstallationID, LeaseID: lease.LeaseID,
		AllowedDomains: []string{"example.com"}, AllowedOperations: []string{"login"},
		ExpiresAtUnixMS: now.Add(30 * time.Minute).UnixMilli(),
	}
	if err := gateway.StoreCredential(receipt.JobID, lease.LeaseID, envelope); err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.ClaimWorker(receipt.JobID); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{envelope.Secret, envelope.SecretRef, envelope.GrantID} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("gateway snapshot leaked credential value %q", secret)
		}
	}

	restarted := newPersistentTestGateway(t, path, now.Add(time.Minute))
	job, err := restarted.Job(receipt.JobID, lease.LeaseID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status.Status != "awaiting_credentials" || job.Status.Stage != "awaiting_credentials" {
		t.Fatalf("credential job recovered as %+v", job.Status)
	}
	if _, err := restarted.ConsumeCredential(receipt.JobID); err == nil {
		t.Fatal("credential must not survive Gateway restart")
	}
	fresh := envelope
	fresh.GrantID = "grant-fresh"
	fresh.SecretRef = "vault://fresh-login"
	fresh.Secret = "fresh-password"
	fresh.ExpiresAtUnixMS = now.Add(45 * time.Minute).UnixMilli()
	if err := restarted.StoreCredential(receipt.JobID, lease.LeaseID, fresh); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.ClaimWorker(receipt.JobID); err != nil {
		t.Fatalf("fresh credential did not restore claimability: %v", err)
	}
}

func TestPersistentGatewayKeepsCompletedResultArtifactAndAck(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	path := filepath.Join(t.TempDir(), "direct-state.json")
	gateway := newPersistentTestGateway(t, path, now)
	lease, err := gateway.Allocate(signedLeaseRequest(t, now))
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"package_id":"pkg-complete-recover"}`)
	receipt, err := gateway.CreateJob(lease.LeaseID, lease.InstallationID, "pkg-complete-recover", HashSHA256(payload), payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.ClaimWorker(receipt.JobID); err != nil {
		t.Fatal(err)
	}
	artifactBytes := []byte("final-mp4-bytes")
	if _, err := gateway.AddArtifact(receipt.JobID, "final-video", "demo_video", "video/mp4", artifactBytes); err != nil {
		t.Fatal(err)
	}
	if err := gateway.CompleteJob(receipt.JobID, "result-recovered", []byte(`{"result_id":"result-recovered"}`)); err != nil {
		t.Fatal(err)
	}
	ack, err := gateway.AckResult(receipt.JobID, lease.LeaseID, DirectResultAckRequest{
		ProtocolVersion: ProtocolVersion, InstallationID: lease.InstallationID, JobID: receipt.JobID,
		ResultPackageID: "result-recovered", ReceivedArtifactIDs: []string{"final-video"},
		VerifiedChecksums: true, AckedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	restarted := newPersistentTestGateway(t, path, now.Add(time.Minute))
	chunk, descriptor, err := restarted.ArtifactChunk(receipt.JobID, lease.LeaseID, "final-video", 0)
	if err != nil || string(chunk) != string(artifactBytes) || descriptor.SHA256 != HashSHA256(artifactBytes) {
		t.Fatalf("restored artifact = %q descriptor=%+v err=%v", chunk, descriptor, err)
	}
	idempotent, err := restarted.AckResult(receipt.JobID, lease.LeaseID, DirectResultAckRequest{
		ProtocolVersion: ProtocolVersion, InstallationID: lease.InstallationID, JobID: receipt.JobID,
		ResultPackageID: "result-recovered", ReceivedArtifactIDs: []string{"final-video"},
		VerifiedChecksums: true, AckedAt: now.Add(time.Minute),
	})
	if err != nil || !idempotent.AckedAt.Equal(ack.AckedAt) {
		t.Fatalf("restored ACK = %+v err=%v", idempotent, err)
	}
}

func TestPersistentGatewayKeepsInfrastructureFailureWithoutInventingResult(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	path := filepath.Join(t.TempDir(), "direct-state.json")
	gateway := newPersistentTestGateway(t, path, now)
	lease, err := gateway.Allocate(signedLeaseRequest(t, now))
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"package_id":"pkg-infra-failure"}`)
	receipt, err := gateway.CreateJob(lease.LeaseID, lease.InstallationID, "pkg-infra-failure", HashSHA256(payload), payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.ClaimWorker(receipt.JobID); err != nil {
		t.Fatal(err)
	}
	if err := gateway.FailJob(receipt.JobID, "browser_start_failed"); err != nil {
		t.Fatal(err)
	}

	restarted := newPersistentTestGateway(t, path, now.Add(time.Minute))
	job, err := restarted.Job(receipt.JobID, lease.LeaseID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status.Status != "failed" || job.Status.Stage != "browser_start_failed" || len(job.ResultJSON) != 0 || job.Status.ResultPackageID != "" {
		t.Fatalf("restored infrastructure failure = %+v result=%s", job.Status, job.ResultJSON)
	}
}

func TestPersistentGatewayRebindsRecoverableJobToRenewedLease(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	path := filepath.Join(t.TempDir(), "direct-state.json")
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	gateway := newPersistentTestGateway(t, path, now)
	lease, err := gateway.Allocate(signedPersistentLeaseRequest(t, public, private, now, "lease-before-restart"))
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"package_id":"pkg-renew-lease"}`)
	receipt, err := gateway.CreateJob(lease.LeaseID, lease.InstallationID, "pkg-renew-lease", HashSHA256(payload), payload)
	if err != nil {
		t.Fatal(err)
	}

	restartTime := now.Add(2 * time.Hour)
	restarted := newPersistentTestGateway(t, path, restartTime)
	if _, err := restarted.ClaimWorker(receipt.JobID); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("expired lease claim error = %v", err)
	}
	renewed, err := restarted.Allocate(signedPersistentLeaseRequest(t, public, private, restartTime, "lease-after-restart"))
	if err != nil {
		t.Fatal(err)
	}
	if renewed.LeaseID == lease.LeaseID {
		t.Fatal("expired lease must be replaced")
	}
	job, err := restarted.Job(receipt.JobID, renewed.LeaseID)
	if err != nil || job.LeaseID != renewed.LeaseID {
		t.Fatalf("renewed lease binding = %+v err=%v", job, err)
	}
	if _, err := restarted.ClaimWorker(receipt.JobID); err != nil {
		t.Fatalf("renewed lease did not restore claimability: %v", err)
	}
}

func TestPersistentGatewayRejectsCorruptSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "direct-state.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":"wrong","jobs":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewPersistentGateway("gateway.example", time.Hour, path); !errors.Is(err, ErrGatewayPersistence) {
		t.Fatalf("corrupt snapshot error = %v", err)
	}
}

func TestPersistentGatewayRejectsTamperedPackageBytes(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	path := filepath.Join(t.TempDir(), "direct-state.json")
	gateway := newPersistentTestGateway(t, path, now)
	lease, err := gateway.Allocate(signedLeaseRequest(t, now))
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"package_id":"pkg-tamper"}`)
	if _, err := gateway.CreateJob(lease.LeaseID, lease.InstallationID, "pkg-tamper", HashSHA256(payload), payload); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot gatewaySnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	for id, job := range snapshot.Jobs {
		job.PackageJSON = []byte(`{"package_id":"pkg-tampered"}`)
		snapshot.Jobs[id] = job
	}
	data, err = json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewPersistentGateway("gateway.example", time.Hour, path); !errors.Is(err, ErrGatewayPersistence) {
		t.Fatalf("tampered snapshot error = %v", err)
	}
}

func TestPersistentGatewayRollsBackJobWhenSnapshotCannotBeWritten(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	gateway := NewGateway("gateway.example", time.Hour)
	gateway.now = func() time.Time { return now }
	lease, err := gateway.Allocate(signedLeaseRequest(t, now))
	if err != nil {
		t.Fatal(err)
	}
	stateDirectory := filepath.Join(t.TempDir(), "state-is-a-directory")
	if err := os.MkdirAll(stateDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	gateway.statePath = stateDirectory
	payload := []byte(`{"package_id":"pkg-write-failure"}`)
	if _, err := gateway.CreateJob(lease.LeaseID, lease.InstallationID, "pkg-write-failure", HashSHA256(payload), payload); !errors.Is(err, ErrGatewayPersistence) {
		t.Fatalf("snapshot write error = %v", err)
	}
	if len(gateway.jobs) != 0 {
		t.Fatalf("failed persistence must roll back job creation: %+v", gateway.jobs)
	}
}
