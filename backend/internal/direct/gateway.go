package direct

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrLeaseNotFound              = errors.New("direct lease was not found")
	ErrLeaseExpired               = errors.New("direct lease has expired")
	ErrLeaseHasActiveJobs         = errors.New("lease_has_active_jobs")
	ErrNoDataPorts                = errors.New("direct data port range is exhausted")
	ErrJobNotFound                = errors.New("direct job was not found")
	ErrResultAckRequired          = errors.New("result_ack_required")
	ErrPackageIdempotencyConflict = errors.New("package_idempotency_conflict")
)

type Lease struct {
	DirectPortLease
	PublicKey ed25519.PublicKey
	Released  bool
}

type Job struct {
	Status         DirectJobStatus
	LeaseID        string
	InstallationID string
	// PackageSHA256 binds the exact plaintext uploaded over Direct transport.
	PackageSHA256 string
	// SourcePackageDigest binds the App-approved package reproducibility hash
	// carried into RecordingResultPackage.AuditTrail.
	SourcePackageDigest string
	PackageJSON         []byte
	ResultJSON          []byte
	Artifacts           map[string]Artifact
	Credential          *DirectCredentialEnvelope
	CredentialUsed      bool
	CredentialsRequired bool
	AckedAtUnixMS       int64
	AckedByInstallID    string
	ReceivedArtifactIDs []string
	VerifiedChecksums   bool
	CreatedAtUnixMS     int64
}

type Artifact struct {
	Descriptor DirectArtifactDescriptor
	Bytes      []byte
}

type Gateway struct {
	mu        sync.Mutex
	now       func() time.Time
	baseHost  string
	ttl       time.Duration
	leases    map[string]*Lease
	byInstall map[string]string
	jobs      map[string]*Job
	nonces    *NonceSet
	statePath string
}

func NewGateway(baseHost string, ttl time.Duration) *Gateway {
	if ttl <= 0 {
		ttl = 40 * time.Minute
	}
	return &Gateway{now: time.Now, baseHost: strings.TrimSpace(baseHost), ttl: ttl, leases: map[string]*Lease{}, byInstall: map[string]string{}, jobs: map[string]*Job{}, nonces: NewNonceSet()}
}

func randomID(prefix string, size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

func (g *Gateway) Allocate(request DirectLeaseRequest) (DirectPortLease, error) {
	now := g.now()
	pub, err := VerifyLeaseRequest(request, now, g.nonces)
	if err != nil {
		return DirectPortLease{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if id := g.byInstall[request.InstallationID]; id != "" {
		if existing := g.leases[id]; existing != nil && !existing.Released && time.UnixMilli(existing.ExpiresAt).After(now) {
			beforeTime, beforeURL := existing.ServerTime, existing.DataURL
			existing.ServerTime = now.UnixMilli()
			existing.DataURL = directDataURL(g.baseHost, existing.DataPort)
			if err := g.persistLocked(); err != nil {
				existing.ServerTime, existing.DataURL = beforeTime, beforeURL
				return DirectPortLease{}, err
			}
			return existing.DirectPortLease, nil
		}
	}
	used := map[int]bool{}
	for _, lease := range g.leases {
		if !lease.Released && time.UnixMilli(lease.ExpiresAt).After(now) {
			used[lease.DataPort] = true
		}
	}
	port := 0
	for candidate := DataPortMin; candidate <= DataPortMax; candidate++ {
		if !used[candidate] {
			port = candidate
			break
		}
	}
	if port == 0 {
		return DirectPortLease{}, ErrNoDataPorts
	}
	leaseID, err := randomID("lease_", 18)
	if err != nil {
		return DirectPortLease{}, err
	}
	token, err := randomID("direct_token_", 32)
	if err != nil {
		return DirectPortLease{}, err
	}
	value := DirectPortLease{LeaseID: leaseID, InstallationID: request.InstallationID, DataURL: directDataURL(g.baseHost, port), DataPort: port, LeaseToken: token, IssuedAt: now.UnixMilli(), ExpiresAt: now.Add(g.ttl).UnixMilli(), CryptoSuite: CryptoSuite, ServerTime: now.UnixMilli()}
	g.leases[leaseID] = &Lease{DirectPortLease: value, PublicKey: pub}
	g.byInstall[request.InstallationID] = leaseID
	reboundJobs := map[string]string{}
	for _, job := range g.jobs {
		if job == nil || job.InstallationID != request.InstallationID || terminalStatus(job.Status.Status) {
			continue
		}
		reboundJobs[job.Status.JobID] = job.LeaseID
		job.LeaseID = leaseID
	}
	if err := g.persistLocked(); err != nil {
		for jobID, previousLeaseID := range reboundJobs {
			if job := g.jobs[jobID]; job != nil {
				job.LeaseID = previousLeaseID
			}
		}
		delete(g.leases, leaseID)
		delete(g.byInstall, request.InstallationID)
		return DirectPortLease{}, err
	}
	return value, nil
}

// directDataHostname removes the fixed TLS control port before a dedicated
// data-plane port is appended. CASCADE_DIRECT_PUBLIC_HOST is allowed to carry
// the control address (for example 127.0.0.1:18443), but a lease data URL must
// contain exactly one port in the installation range.
func directDataHostname(value string) string {
	value = strings.TrimSpace(value)
	if host, _, err := net.SplitHostPort(value); err == nil {
		return host
	}
	return value
}

func directDataURL(baseHost string, port int) string {
	host := directDataHostname(baseHost)
	if host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("https://%s:%d", host, port)
}

func (g *Gateway) Lease(id string) (*Lease, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	lease := g.leases[id]
	if lease == nil || lease.Released {
		return nil, ErrLeaseNotFound
	}
	if time.UnixMilli(lease.ExpiresAt).Before(g.now()) {
		return nil, ErrLeaseExpired
	}
	copy := *lease
	copy.LeaseToken = lease.LeaseToken
	return &copy, nil
}

func (g *Gateway) LeaseByPort(port int) (*Lease, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	for _, lease := range g.leases {
		if lease.DataPort == port && !lease.Released && time.UnixMilli(lease.ExpiresAt).After(now) {
			copy := *lease
			return &copy, nil
		}
	}
	return nil, ErrLeaseNotFound
}

func (g *Gateway) Release(request DirectLeaseRequest) error {
	_, err := VerifyLeaseRequest(request, g.now(), g.nonces)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	id := g.byInstall[request.InstallationID]
	lease := g.leases[id]
	if lease == nil || lease.Released {
		return ErrLeaseNotFound
	}
	for _, job := range g.jobs {
		if job.LeaseID == id && !terminalStatus(job.Status.Status) {
			return ErrLeaseHasActiveJobs
		}
		if job.LeaseID == id && job.Status.Status == "completed" && job.AckedAtUnixMS == 0 {
			return ErrResultAckRequired
		}
	}
	lease.Released = true
	delete(g.byInstall, request.InstallationID)
	if err := g.persistLocked(); err != nil {
		lease.Released = false
		g.byInstall[request.InstallationID] = id
		return err
	}
	return nil
}

func terminalStatus(status string) bool {
	switch status {
	case "completed", "failed", "canceled", "expired":
		return true
	}
	return false
}

func (g *Gateway) ActiveLeaseCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	n := 0
	for _, lease := range g.leases {
		if !lease.Released && time.UnixMilli(lease.ExpiresAt).After(now) {
			n++
		}
	}
	return n
}

func (g *Gateway) CreateJob(leaseID, installationID, packageID, packageDigest string, packageJSON []byte) (DirectPackageReceipt, error) {
	return g.CreateJobWithSourceDigest(leaseID, installationID, packageID, packageDigest, packageDigest, packageJSON)
}

func (g *Gateway) CreateJobWithSourceDigest(leaseID, installationID, packageID, transportDigest, sourcePackageDigest string, packageJSON []byte) (DirectPackageReceipt, error) {
	return g.CreateJobWithSourceDigestAndCredentialRequirement(leaseID, installationID, packageID, transportDigest, sourcePackageDigest, packageJSON, false)
}

func (g *Gateway) CreateJobWithSourceDigestAndCredentialRequirement(leaseID, installationID, packageID, transportDigest, sourcePackageDigest string, packageJSON []byte, credentialsRequired bool) (DirectPackageReceipt, error) {
	now := g.now()
	if strings.TrimSpace(packageID) == "" || strings.TrimSpace(transportDigest) == "" || strings.TrimSpace(sourcePackageDigest) == "" || len(packageJSON) == 0 {
		return DirectPackageReceipt{}, errors.New("direct package digests are required")
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	lease := g.leases[leaseID]
	if lease == nil || lease.Released || lease.InstallationID != installationID {
		return DirectPackageReceipt{}, ErrLeaseNotFound
	}
	if time.UnixMilli(lease.ExpiresAt).Before(now) {
		return DirectPackageReceipt{}, ErrLeaseExpired
	}

	// The protocol freezes installation + package ID + final package digest as
	// the upload idempotency key. The transport digest and bytes are checked as
	// the payload identity so a reused key can never silently accept drift.
	for _, existing := range g.jobs {
		if existing == nil || existing.InstallationID != installationID || existing.Status.PackageID != packageID || existing.SourcePackageDigest != sourcePackageDigest {
			continue
		}
		if existing.PackageSHA256 != transportDigest || !bytes.Equal(existing.PackageJSON, packageJSON) || existing.CredentialsRequired != credentialsRequired {
			return DirectPackageReceipt{}, ErrPackageIdempotencyConflict
		}
		return DirectPackageReceipt{
			JobID: existing.Status.JobID, PackageID: existing.Status.PackageID,
			PackageSHA256: existing.PackageSHA256, Status: existing.Status.Status,
			Stage: existing.Status.Stage, CreatedAtUnixMS: existing.CreatedAtUnixMS,
		}, nil
	}

	jobID, err := randomID("direct_job_", 18)
	if err != nil {
		return DirectPackageReceipt{}, err
	}
	status, stage := "queued", "package_received"
	if credentialsRequired {
		status, stage = "awaiting_credentials", "awaiting_credentials"
	}
	receipt := DirectPackageReceipt{JobID: jobID, PackageID: packageID, PackageSHA256: transportDigest, Status: status, Stage: stage, CreatedAtUnixMS: now.UnixMilli()}
	g.jobs[jobID] = &Job{Status: DirectJobStatus{JobID: jobID, PackageID: packageID, Status: receipt.Status, Stage: receipt.Stage, UpdatedAtUnixMS: now.UnixMilli()}, LeaseID: leaseID, InstallationID: installationID, PackageSHA256: transportDigest, SourcePackageDigest: sourcePackageDigest, PackageJSON: append([]byte{}, packageJSON...), Artifacts: map[string]Artifact{}, CredentialsRequired: credentialsRequired, CreatedAtUnixMS: receipt.CreatedAtUnixMS}
	if err := g.persistLocked(); err != nil {
		delete(g.jobs, jobID)
		return DirectPackageReceipt{}, err
	}
	return receipt, nil
}

func (g *Gateway) Job(jobID, leaseID string) (*Job, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	job := g.jobs[jobID]
	if job == nil || job.LeaseID != leaseID {
		return nil, ErrJobNotFound
	}
	copy := *job
	copy.PackageJSON = append([]byte{}, job.PackageJSON...)
	copy.ResultJSON = append([]byte{}, job.ResultJSON...)
	return &copy, nil
}

// JobForInstallation lets a renewed lease read jobs that were created by the
// same installation. Terminal jobs are intentionally not rebound when a lease
// is renewed, but their source package and result remain valid recovery input.
// Callers must authenticate the current lease before using this method.
func (g *Gateway) JobForInstallation(jobID, installationID string) (*Job, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	job := g.jobs[jobID]
	if job == nil || strings.TrimSpace(installationID) == "" || job.InstallationID != installationID {
		return nil, ErrJobNotFound
	}
	copy := *job
	copy.PackageJSON = append([]byte{}, job.PackageJSON...)
	copy.ResultJSON = append([]byte{}, job.ResultJSON...)
	return &copy, nil
}

func (g *Gateway) WorkerJob(jobID string) (*Job, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	job := g.jobs[jobID]
	if job == nil {
		return nil, ErrJobNotFound
	}
	copy := *job
	copy.PackageJSON = append([]byte{}, job.PackageJSON...)
	copy.ResultJSON = append([]byte{}, job.ResultJSON...)
	copy.Artifacts = make(map[string]Artifact, len(job.Artifacts))
	for id, artifact := range job.Artifacts {
		artifact.Bytes = append([]byte{}, artifact.Bytes...)
		copy.Artifacts[id] = artifact
	}
	return &copy, nil
}

// QueuedJobIDs returns a stable snapshot for the loopback Worker scheduler.
// ClaimWorker remains the atomic state transition and is the final duplicate
// execution guard.
func (g *Gateway) QueuedJobIDs() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	ids := make([]string, 0)
	for id, job := range g.jobs {
		if job != nil && job.Status.Status == "queued" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func (g *Gateway) StoreCredential(jobID, leaseID string, envelope DirectCredentialEnvelope) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	job := g.jobs[jobID]
	if job == nil || job.LeaseID != leaseID {
		return ErrJobNotFound
	}
	if envelope.JobID != jobID || envelope.PackageID != job.Status.PackageID || envelope.PackageSHA256 != job.PackageSHA256 || envelope.LeaseID != leaseID || envelope.InstallationID != job.InstallationID || envelope.GrantID == "" || envelope.SecretRef == "" || envelope.Secret == "" || len(envelope.AllowedDomains) == 0 || len(envelope.AllowedOperations) == 0 || time.UnixMilli(envelope.ExpiresAtUnixMS).Before(g.now()) {
		return errors.New("credential envelope binding mismatch")
	}
	if job.Status.Status != "awaiting_credentials" && job.Status.Status != "queued" {
		return errors.New("credential envelope can only be attached before worker claim")
	}
	before := cloneJob(job)
	copy := envelope
	job.Credential = &copy
	job.CredentialUsed = false
	if job.Status.Status == "awaiting_credentials" {
		job.Status.Status = "queued"
		job.Status.Stage = "package_received"
		job.Status.UpdatedAtUnixMS = g.now().UnixMilli()
	}
	if err := g.persistLocked(); err != nil {
		g.jobs[jobID] = before
		return err
	}
	return nil
}

func (g *Gateway) ClaimWorker(jobID string) ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	job := g.jobs[jobID]
	if job == nil {
		return nil, ErrJobNotFound
	}
	if job.Status.Status != "queued" {
		return nil, errors.New("direct job is not claimable")
	}
	if lease, ok := g.leases[job.LeaseID]; !ok || lease.Released || time.UnixMilli(lease.ExpiresAt).Before(g.now()) {
		return nil, ErrLeaseExpired
	}
	before := cloneJob(job)
	job.Status.Status = "running"
	job.Status.Stage = "worker_claimed"
	job.Status.Progress = 1
	job.Status.UpdatedAtUnixMS = g.now().UnixMilli()
	if err := g.persistLocked(); err != nil {
		g.jobs[jobID] = before
		return nil, err
	}
	return append([]byte{}, job.PackageJSON...), nil
}

func (g *Gateway) ReleaseWorkerClaim(jobID string) (DirectJobStatus, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	job := g.jobs[jobID]
	if job == nil {
		return DirectJobStatus{}, ErrJobNotFound
	}
	if job.Status.Status != "running" || len(job.ResultJSON) > 0 {
		return DirectJobStatus{}, errors.New("job_not_claimed")
	}
	before := cloneJob(job)
	if job.CredentialsRequired {
		job.Credential = nil
		job.CredentialUsed = false
		job.Status.Status = "awaiting_credentials"
		job.Status.Stage = "awaiting_credentials"
	} else {
		job.Status.Status = "queued"
		job.Status.Stage = "package_received"
	}
	job.Status.Progress = 0
	job.Status.UpdatedAtUnixMS = g.now().UnixMilli()
	if err := g.persistLocked(); err != nil {
		g.jobs[jobID] = before
		return DirectJobStatus{}, err
	}
	return job.Status, nil
}

func (g *Gateway) ConsumeCredential(jobID string) (DirectCredentialEnvelope, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	job := g.jobs[jobID]
	if job == nil || job.Credential == nil || job.CredentialUsed {
		return DirectCredentialEnvelope{}, errors.New("credential envelope unavailable")
	}
	if job.Status.Status != "running" {
		return DirectCredentialEnvelope{}, errors.New("credential envelope can only be consumed by a running job")
	}
	if time.UnixMilli(job.Credential.ExpiresAtUnixMS).Before(g.now()) {
		job.CredentialUsed = true
		job.Credential = nil
		return DirectCredentialEnvelope{}, errors.New("credential envelope has expired")
	}
	value := *job.Credential
	job.CredentialUsed = true
	job.Credential = nil
	return value, nil
}

func (g *Gateway) UpdateJob(jobID string, progress int, stage string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	job := g.jobs[jobID]
	if job == nil {
		return ErrJobNotFound
	}
	if job.Status.Status != "running" || progress < 1 || progress > 99 || strings.TrimSpace(stage) == "" {
		return errors.New("invalid direct job progress")
	}
	before := cloneJob(job)
	job.Status.Progress = progress
	job.Status.Stage = stage
	job.Status.UpdatedAtUnixMS = g.now().UnixMilli()
	if err := g.persistLocked(); err != nil {
		g.jobs[jobID] = before
		return err
	}
	return nil
}

// CancelJob moves a queued or running job to a durable terminal state before
// the execution context is interrupted. Repeating the same cancellation is
// idempotent; completed and failed results remain immutable.
func (g *Gateway) CancelJob(jobID, leaseID string) (DirectJobStatus, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	job := g.jobs[jobID]
	if job == nil || job.LeaseID != leaseID {
		return DirectJobStatus{}, ErrJobNotFound
	}
	if job.Status.Status == "canceled" {
		return job.Status, nil
	}
	if terminalStatus(job.Status.Status) {
		return DirectJobStatus{}, errors.New("terminal direct job cannot be canceled")
	}
	before := cloneJob(job)
	job.Status.Status = "canceled"
	job.Status.Stage = "canceled"
	job.Status.UpdatedAtUnixMS = g.now().UnixMilli()
	job.Credential = nil
	if err := g.persistLocked(); err != nil {
		g.jobs[jobID] = before
		return DirectJobStatus{}, err
	}
	return job.Status, nil
}

func (g *Gateway) AddArtifact(jobID, artifactID, kind, mime string, data []byte) (DirectArtifactDescriptor, error) {
	if artifactID == "" || len(data) == 0 || len(data) > MaxArtifactSize {
		return DirectArtifactDescriptor{}, errors.New("artifact id and bytes are required")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	job := g.jobs[jobID]
	if job == nil {
		return DirectArtifactDescriptor{}, ErrJobNotFound
	}
	if job.Status.Status != "running" {
		return DirectArtifactDescriptor{}, errors.New("artifacts can only be added to a running job")
	}
	if _, exists := job.Artifacts[artifactID]; exists {
		return DirectArtifactDescriptor{}, errors.New("artifact already exists")
	}
	before := cloneJob(job)
	chunks := (len(data) + MaxChunkSize - 1) / MaxChunkSize
	d := DirectArtifactDescriptor{ArtifactID: artifactID, Kind: kind, MimeType: mime, Size: int64(len(data)), SHA256: HashSHA256(data), ChunkSize: MaxChunkSize, ChunkCount: chunks}
	job.Artifacts[artifactID] = Artifact{Descriptor: d, Bytes: append([]byte{}, data...)}
	job.Status.ArtifactIDs = append(job.Status.ArtifactIDs, artifactID)
	if err := g.persistLocked(); err != nil {
		g.jobs[jobID] = before
		return DirectArtifactDescriptor{}, err
	}
	return d, nil
}

func (g *Gateway) ArtifactChunk(jobID, leaseID, artifactID string, index int) ([]byte, DirectArtifactDescriptor, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	job := g.jobs[jobID]
	if job == nil || job.LeaseID != leaseID {
		return nil, DirectArtifactDescriptor{}, ErrJobNotFound
	}
	artifact, ok := job.Artifacts[artifactID]
	if !ok || index < 0 || index >= artifact.Descriptor.ChunkCount {
		return nil, DirectArtifactDescriptor{}, errors.New("artifact chunk was not found")
	}
	start := index * MaxChunkSize
	end := start + MaxChunkSize
	if end > len(artifact.Bytes) {
		end = len(artifact.Bytes)
	}
	return append([]byte{}, artifact.Bytes[start:end]...), artifact.Descriptor, nil
}

func (g *Gateway) CompleteJob(jobID, resultPackageID string, result []byte) error {
	return g.CommitJobResult(jobID, resultPackageID, result, "completed")
}

// CommitJobResult stores an authoritative Worker result and moves the job to
// the matching terminal state. A failed result is still retrievable by the App
// and must not be confused with an infrastructure failure that has no result.
func (g *Gateway) CommitJobResult(jobID, resultPackageID string, result []byte, status string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	job := g.jobs[jobID]
	if job == nil {
		return ErrJobNotFound
	}
	if (status != "completed" && status != "failed") || len(result) == 0 || resultPackageID == "" || terminalStatus(job.Status.Status) || job.Status.Status != "running" {
		return errors.New("result package is required")
	}
	before := cloneJob(job)
	job.ResultJSON = append([]byte{}, result...)
	job.Status.Status = status
	if status == "failed" {
		job.Status.Stage = "failed_result_ready"
	} else {
		job.Status.Stage = "result_ready"
	}
	job.Status.Progress = 100
	job.Status.ResultPackageID = resultPackageID
	job.Status.UpdatedAtUnixMS = g.now().UnixMilli()
	job.Credential = nil
	if err := g.persistLocked(); err != nil {
		g.jobs[jobID] = before
		return err
	}
	return nil
}

func (g *Gateway) AckResult(jobID, leaseID string, request DirectResultAckRequest) (DirectResultAckReceipt, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	job := g.jobs[jobID]
	if job == nil || job.LeaseID != leaseID {
		return DirectResultAckReceipt{}, ErrJobNotFound
	}
	if job.Status.Status != "completed" || len(job.ResultJSON) == 0 {
		return DirectResultAckReceipt{}, errors.New("result_ack_requires_completed_result")
	}
	if request.ProtocolVersion != ProtocolVersion || request.InstallationID != job.InstallationID || request.JobID != jobID || request.ResultPackageID != job.Status.ResultPackageID || !request.VerifiedChecksums || request.AckedAt.IsZero() {
		return DirectResultAckReceipt{}, errors.New("ack_binding_invalid")
	}
	expected := append([]string{}, job.Status.ArtifactIDs...)
	actual := uniqueArtifactIDs(request.ReceivedArtifactIDs)
	if len(actual) != len(expected) || !sameArtifactIDs(actual, expected) {
		return DirectResultAckReceipt{}, errors.New("ack_artifacts_incomplete")
	}
	if job.AckedAtUnixMS != 0 {
		if job.AckedByInstallID != request.InstallationID || !sameArtifactIDs(job.ReceivedArtifactIDs, actual) {
			return DirectResultAckReceipt{}, errors.New("ack_binding_invalid")
		}
		return DirectResultAckReceipt{ProtocolVersion: ProtocolVersion, JobID: jobID, ResultPackageID: job.Status.ResultPackageID, ReceivedArtifactIDs: append([]string{}, job.ReceivedArtifactIDs...), VerifiedChecksums: job.VerifiedChecksums, AckedAt: time.UnixMilli(job.AckedAtUnixMS).UTC()}, nil
	}
	before := cloneJob(job)
	ackedAt := g.now().UnixMilli()
	job.AckedAtUnixMS, job.AckedByInstallID = ackedAt, request.InstallationID
	job.ReceivedArtifactIDs, job.VerifiedChecksums = actual, true
	ackedAtTime := time.UnixMilli(ackedAt).UTC()
	job.Status.AckedAt, job.Status.VerifiedChecksums = &ackedAtTime, true
	if err := g.persistLocked(); err != nil {
		g.jobs[jobID] = before
		return DirectResultAckReceipt{}, err
	}
	return DirectResultAckReceipt{ProtocolVersion: ProtocolVersion, JobID: jobID, ResultPackageID: job.Status.ResultPackageID, ReceivedArtifactIDs: append([]string{}, actual...), VerifiedChecksums: true, AckedAt: ackedAtTime}, nil
}

func uniqueArtifactIDs(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func sameArtifactIDs(left, right []string) bool {
	return strings.Join(uniqueArtifactIDs(left), "\n") == strings.Join(uniqueArtifactIDs(right), "\n")
}

func (g *Gateway) FailJob(jobID, stage string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	job := g.jobs[jobID]
	if job == nil {
		return ErrJobNotFound
	}
	if terminalStatus(job.Status.Status) {
		return errors.New("terminal direct job cannot be failed")
	}
	before := cloneJob(job)
	job.Status.Status = "failed"
	job.Status.Stage = stage
	job.Status.UpdatedAtUnixMS = g.now().UnixMilli()
	job.Credential = nil
	if err := g.persistLocked(); err != nil {
		g.jobs[jobID] = before
		return err
	}
	return nil
}

func (g *Gateway) Nonces() *NonceSet { return g.nonces }
