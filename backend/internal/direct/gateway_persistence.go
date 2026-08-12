package direct

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const gatewaySnapshotSchemaVersion = "cascade.browser_agent_direct_gateway_snapshot.v1"

var ErrGatewayPersistence = errors.New("direct_gateway_persistence_failed")

type gatewaySnapshot struct {
	SchemaVersion string                    `json:"schema_version"`
	SavedAt       time.Time                 `json:"saved_at"`
	Leases        map[string]persistedLease `json:"leases"`
	Jobs          map[string]persistedJob   `json:"jobs"`
}

type persistedLease struct {
	DirectPortLease
	PublicKey []byte `json:"public_key"`
	Released  bool   `json:"released"`
}

// persistedJob deliberately excludes Credential. Credential envelopes are
// short-lived secrets and must never be written to Gateway storage.
type persistedJob struct {
	Status              DirectJobStatus     `json:"status"`
	LeaseID             string              `json:"lease_id"`
	InstallationID      string              `json:"installation_id"`
	PackageSHA256       string              `json:"package_sha256"`
	SourcePackageDigest string              `json:"source_package_digest"`
	PackageJSON         []byte              `json:"package_json"`
	ResultJSON          []byte              `json:"result_json,omitempty"`
	Artifacts           map[string]Artifact `json:"artifacts,omitempty"`
	CredentialsRequired bool                `json:"credentials_required"`
	AckedAtUnixMS       int64               `json:"acked_at_unix_ms,omitempty"`
	AckedByInstallID    string              `json:"acked_by_installation_id,omitempty"`
	ReceivedArtifactIDs []string            `json:"received_artifact_ids,omitempty"`
	VerifiedChecksums   bool                `json:"verified_checksums,omitempty"`
	CreatedAtUnixMS     int64               `json:"created_at_unix_ms"`
}

func NewPersistentGateway(baseHost string, ttl time.Duration, statePath string) (*Gateway, error) {
	gateway := NewGateway(baseHost, ttl)
	gateway.statePath = strings.TrimSpace(statePath)
	if gateway.statePath == "" {
		return nil, fmt.Errorf("%w: state path is required", ErrGatewayPersistence)
	}
	if err := gateway.loadSnapshot(); err != nil {
		return nil, err
	}
	return gateway, nil
}

func (g *Gateway) PersistenceEnabled() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.statePath != ""
}

func (g *Gateway) loadSnapshot() error {
	data, err := os.ReadFile(g.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: read snapshot: %v", ErrGatewayPersistence, err)
	}
	var snapshot gatewaySnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("%w: decode snapshot: %v", ErrGatewayPersistence, err)
	}
	if snapshot.SchemaVersion != gatewaySnapshotSchemaVersion {
		return fmt.Errorf("%w: unsupported snapshot schema %q", ErrGatewayPersistence, snapshot.SchemaVersion)
	}

	now := g.now()
	for id, stored := range snapshot.Leases {
		if id == "" || stored.LeaseID != id || stored.InstallationID == "" || stored.DataPort < DataPortMin || stored.DataPort > DataPortMax || stored.LeaseToken == "" || len(stored.PublicKey) != ed25519.PublicKeySize || InstallationID(ed25519.PublicKey(stored.PublicKey)) != stored.InstallationID {
			return fmt.Errorf("%w: invalid persisted lease", ErrGatewayPersistence)
		}
		lease := &Lease{DirectPortLease: stored.DirectPortLease, PublicKey: ed25519.PublicKey(append([]byte{}, stored.PublicKey...)), Released: stored.Released}
		g.leases[id] = lease
		if !lease.Released && time.UnixMilli(lease.ExpiresAt).After(now) {
			g.byInstall[lease.InstallationID] = id
		}
	}
	for id, stored := range snapshot.Jobs {
		if id == "" || stored.Status.JobID != id || stored.Status.PackageID == "" || stored.InstallationID == "" || stored.PackageSHA256 == "" || stored.SourcePackageDigest == "" || len(stored.PackageJSON) == 0 {
			return fmt.Errorf("%w: invalid persisted job", ErrGatewayPersistence)
		}
		if stored.Status.Status == "canceled" || stored.Status.Status == "expired" || stored.Status.Status == "reunderstanding_required" {
			continue
		}
		job := &Job{
			Status: stored.Status, LeaseID: stored.LeaseID, InstallationID: stored.InstallationID,
			PackageSHA256: stored.PackageSHA256, SourcePackageDigest: stored.SourcePackageDigest,
			PackageJSON: append([]byte{}, stored.PackageJSON...), ResultJSON: append([]byte{}, stored.ResultJSON...),
			Artifacts: cloneArtifacts(stored.Artifacts), CredentialsRequired: stored.CredentialsRequired,
			AckedAtUnixMS: stored.AckedAtUnixMS, AckedByInstallID: stored.AckedByInstallID,
			ReceivedArtifactIDs: append([]string{}, stored.ReceivedArtifactIDs...), VerifiedChecksums: stored.VerifiedChecksums,
			CreatedAtUnixMS: stored.CreatedAtUnixMS,
		}
		if job.Artifacts == nil {
			job.Artifacts = map[string]Artifact{}
		}
		if err := validatePersistedJob(job, g.leases); err != nil {
			return fmt.Errorf("%w: %v", ErrGatewayPersistence, err)
		}
		job.Credential = nil
		job.CredentialUsed = false
		switch job.Status.Status {
		case "running":
			// A process-local Worker claim cannot survive restart. Discard only
			// partial running artifacts so the next claim can upload clean bytes.
			job.Artifacts = map[string]Artifact{}
			job.Status.ArtifactIDs = nil
			job.Status.Progress = 0
			if job.CredentialsRequired {
				job.Status.Status = "awaiting_credentials"
				job.Status.Stage = "awaiting_credentials"
			} else {
				job.Status.Status = "queued"
				job.Status.Stage = "gateway_recovered"
			}
			job.Status.UpdatedAtUnixMS = now.UnixMilli()
		case "queued":
			if job.CredentialsRequired {
				job.Status.Status = "awaiting_credentials"
				job.Status.Stage = "awaiting_credentials"
				job.Status.Progress = 0
				job.Status.UpdatedAtUnixMS = now.UnixMilli()
			}
		case "awaiting_credentials", "completed", "failed":
		default:
			return fmt.Errorf("%w: unsupported persisted job status %q", ErrGatewayPersistence, job.Status.Status)
		}
		g.jobs[id] = job
	}
	return nil
}

func validatePersistedJob(job *Job, leases map[string]*Lease) error {
	lease := leases[job.LeaseID]
	if lease == nil || lease.InstallationID != job.InstallationID {
		return errors.New("persisted job lease binding is invalid")
	}
	if HashSHA256(job.PackageJSON) != job.PackageSHA256 {
		return errors.New("persisted package digest is invalid")
	}
	artifactIDs := make([]string, 0, len(job.Artifacts))
	for id, artifact := range job.Artifacts {
		artifactIDs = append(artifactIDs, id)
		descriptor := artifact.Descriptor
		wantChunks := (len(artifact.Bytes) + MaxChunkSize - 1) / MaxChunkSize
		if id == "" || descriptor.ArtifactID != id || len(artifact.Bytes) == 0 || descriptor.Size != int64(len(artifact.Bytes)) || descriptor.SHA256 != HashSHA256(artifact.Bytes) || descriptor.ChunkSize != MaxChunkSize || descriptor.ChunkCount != wantChunks {
			return errors.New("persisted artifact binding is invalid")
		}
	}
	if !sameArtifactIDs(artifactIDs, job.Status.ArtifactIDs) {
		return errors.New("persisted artifact index is invalid")
	}
	switch job.Status.Status {
	case "completed":
		if len(job.ResultJSON) == 0 || job.Status.ResultPackageID == "" {
			return errors.New("persisted terminal result is incomplete")
		}
	case "failed":
		if (len(job.ResultJSON) == 0) != (job.Status.ResultPackageID == "") {
			return errors.New("persisted failed result binding is incomplete")
		}
	case "queued", "running", "awaiting_credentials":
		if len(job.ResultJSON) != 0 || job.Status.ResultPackageID != "" {
			return errors.New("persisted active job contains a terminal result")
		}
	}
	if job.AckedAtUnixMS != 0 {
		if job.Status.Status != "completed" || job.AckedByInstallID != job.InstallationID || !job.VerifiedChecksums || !sameArtifactIDs(job.ReceivedArtifactIDs, job.Status.ArtifactIDs) {
			return errors.New("persisted result ACK binding is invalid")
		}
	}
	return nil
}

func (g *Gateway) persistLocked() error {
	if g == nil || g.statePath == "" {
		return nil
	}
	snapshot := gatewaySnapshot{
		SchemaVersion: gatewaySnapshotSchemaVersion,
		SavedAt:       g.now().UTC(),
		Leases:        make(map[string]persistedLease, len(g.leases)),
		Jobs:          make(map[string]persistedJob, len(g.jobs)),
	}
	for id, lease := range g.leases {
		if lease == nil {
			continue
		}
		snapshot.Leases[id] = persistedLease{DirectPortLease: lease.DirectPortLease, PublicKey: append([]byte{}, lease.PublicKey...), Released: lease.Released}
	}
	for id, job := range g.jobs {
		if job == nil {
			continue
		}
		snapshot.Jobs[id] = persistedJob{
			Status: job.Status, LeaseID: job.LeaseID, InstallationID: job.InstallationID,
			PackageSHA256: job.PackageSHA256, SourcePackageDigest: job.SourcePackageDigest,
			PackageJSON: append([]byte{}, job.PackageJSON...), ResultJSON: append([]byte{}, job.ResultJSON...),
			Artifacts: cloneArtifacts(job.Artifacts), CredentialsRequired: job.CredentialsRequired,
			AckedAtUnixMS: job.AckedAtUnixMS, AckedByInstallID: job.AckedByInstallID,
			ReceivedArtifactIDs: append([]string{}, job.ReceivedArtifactIDs...), VerifiedChecksums: job.VerifiedChecksums,
			CreatedAtUnixMS: job.CreatedAtUnixMS,
		}
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("%w: encode snapshot: %v", ErrGatewayPersistence, err)
	}
	dir := filepath.Dir(g.statePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("%w: create snapshot directory: %v", ErrGatewayPersistence, err)
	}
	temp, err := os.CreateTemp(dir, ".direct-gateway-*.tmp")
	if err != nil {
		return fmt.Errorf("%w: create snapshot temp file: %v", ErrGatewayPersistence, err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("%w: protect snapshot temp file: %v", ErrGatewayPersistence, err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("%w: write snapshot: %v", ErrGatewayPersistence, err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("%w: sync snapshot: %v", ErrGatewayPersistence, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("%w: close snapshot: %v", ErrGatewayPersistence, err)
	}
	if err := os.Rename(tempPath, g.statePath); err != nil {
		return fmt.Errorf("%w: replace snapshot: %v", ErrGatewayPersistence, err)
	}
	return nil
}

func cloneArtifacts(values map[string]Artifact) map[string]Artifact {
	if values == nil {
		return nil
	}
	result := make(map[string]Artifact, len(values))
	for id, artifact := range values {
		artifact.Bytes = append([]byte{}, artifact.Bytes...)
		result[id] = artifact
	}
	return result
}

func cloneJob(job *Job) *Job {
	if job == nil {
		return nil
	}
	copy := *job
	copy.PackageJSON = append([]byte{}, job.PackageJSON...)
	copy.ResultJSON = append([]byte{}, job.ResultJSON...)
	copy.Artifacts = cloneArtifacts(job.Artifacts)
	copy.ReceivedArtifactIDs = append([]string{}, job.ReceivedArtifactIDs...)
	copy.Status.ArtifactIDs = append([]string{}, job.Status.ArtifactIDs...)
	if job.Status.AckedAt != nil {
		ackedAt := *job.Status.AckedAt
		copy.Status.AckedAt = &ackedAt
	}
	if job.Credential != nil {
		credential := *job.Credential
		credential.AllowedDomains = append([]string{}, job.Credential.AllowedDomains...)
		credential.AllowedOperations = append([]string{}, job.Credential.AllowedOperations...)
		copy.Credential = &credential
	}
	return &copy
}
