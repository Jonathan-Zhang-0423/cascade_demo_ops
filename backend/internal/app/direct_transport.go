package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

const (
	directTransportSettingsFileName = "browser_agent_direct.json"
	directTransportStateName        = "browser_agent_direct_v1"
)

type DirectTransportSettingsRequest struct {
	ControlURL  string `json:"control_url"`
	AccessToken string `json:"access_token"`
}

type directTransportSettingsFile struct {
	ControlURL string    `json:"control_url"`
	SavedAt    time.Time `json:"saved_at"`
}

type directInstallationIdentity struct {
	PublicKeyBase64  string `json:"public_key_base64"`
	PrivateKeyBase64 string `json:"private_key_base64"`
}

type DirectTransportRuntimeView struct {
	Configured                            bool            `json:"configured"`
	Reachable                             bool            `json:"reachable"`
	TokenConfigured                       bool            `json:"token_configured"`
	ProtocolVersion                       string          `json:"protocol_version,omitempty"`
	CryptoSuite                           string          `json:"crypto_suite,omitempty"`
	ControlURLHost                        string          `json:"control_url_host,omitempty"`
	ControlURLPath                        string          `json:"control_url_path,omitempty"`
	InstallationIDSuffix                  string          `json:"installation_id_suffix,omitempty"`
	Transport                             string          `json:"transport"`
	ErrorClass                            string          `json:"error_class,omitempty"`
	SupportedProtocolVersions             []string        `json:"supported_protocol_versions,omitempty"`
	SupportedPackageSchemaVersions        []string        `json:"supported_package_schema_versions,omitempty"`
	SupportedRuntimes                     []string        `json:"supported_runtimes,omitempty"`
	SupportedWorkerProtocolVersions       []string        `json:"supported_worker_protocol_versions,omitempty"`
	SupportedOutcomeVerifierRulesVersions []string        `json:"supported_outcome_verifier_rules_versions,omitempty"`
	Capabilities                          map[string]bool `json:"capabilities,omitempty"`
}

type DirectTransportUploadRequest struct {
	OrgID                       string `json:"org_id,omitempty"`
	PackageDigestSHA256         string `json:"package_digest_sha256"`
	ApprovalSubjectDigestSHA256 string `json:"approval_subject_digest_sha256"`
	ConfidenceAssessmentHash    string `json:"confidence_assessment_hash"`
	RiskConfirmed               bool   `json:"risk_confirmed"`
	IdempotencyKey              string `json:"idempotency_key"`
}

type DirectTransportUploadResult struct {
	Build             ClientExecutionPackageBuild    `json:"build"`
	Lease             DirectTransportLeaseView       `json:"lease"`
	Receipt           model.DirectPackageReceipt     `json:"receipt"`
	CredentialReceipt *model.DirectCredentialReceipt `json:"credential_receipt,omitempty"`
}

type DirectTransportLeaseView struct {
	LeaseID     string    `json:"lease_id"`
	DataURLHost string    `json:"data_url_host"`
	DataPort    int       `json:"data_port"`
	IssuedAt    time.Time `json:"issued_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	CryptoSuite string    `json:"crypto_suite"`
}

type DirectTransportReleaseResult struct {
	Released bool                     `json:"released"`
	Lease    DirectTransportLeaseView `json:"lease"`
}

type DirectArtifactDownloadRequest struct {
	JobID           string               `json:"job_id"`
	Artifact        model.DirectArtifact `json:"artifact"`
	OutputDirectory string               `json:"output_directory,omitempty"`
}

type DirectResultReviewRequest struct {
	JobID           string                    `json:"job_id"`
	ResultPackageID string                    `json:"result_package_id"`
	Review          model.ResultReviewRequest `json:"review"`
}

// GetDirectEditorMaterialization creates/looks up the local editor handoff
// only after every artifact referenced by the verified direct result has been
// downloaded into the App-managed artifact root and checksum-verified.
func (s *Service) GetDirectEditorMaterialization(ctx context.Context, projectID string) (EditorSessionMaterialization, error) {
	state, err := s.states.Load(ctx, projectID)
	if err != nil || state.DesktopCloudRun == nil || state.DesktopCloudRun.Transport != directTransportStateName || state.DesktopCloudRun.ResultPackage == nil {
		return EditorSessionMaterialization{}, errors.New("direct Browser Agent result is unavailable")
	}
	run := state.DesktopCloudRun
	if !run.ResultDownloaded {
		return EditorSessionMaterialization{Message: "直连结果素材尚未完成 checksum 校验。"}, nil
	}
	result := *run.ResultPackage
	assets := make(map[string]orchestrator.DesktopDownloadedAssetState, len(run.DownloadedAssets))
	for _, asset := range run.DownloadedAssets {
		if !asset.Verified || asset.ArtifactID == "" {
			continue
		}
		assets[asset.ArtifactID] = asset
	}
	refs := append([]model.ArtifactRef{}, result.GeneratedAssets...)
	if result.ExecutionTrace != nil {
		refs = append(refs, result.ExecutionTrace.Artifacts...)
	}
	if result.StageEventLogRef != nil {
		refs = append(refs, *result.StageEventLogRef)
	}
	for _, ref := range refs {
		asset, ok := assets[ref.ID]
		if !ok || asset.FileName == "" {
			return EditorSessionMaterialization{Message: "直连结果仍缺少已校验素材：" + ref.ID}, nil
		}
		path := filepath.Join(s.runtime.ArtifactRoot, "desktop", "direct-downloads", safePathSegment(projectID), safePathSegment(run.CloudJobID), safePathSegment(asset.FileName))
		if !pathWithinRoot(path, filepath.Join(s.runtime.ArtifactRoot, "desktop", "direct-downloads")) {
			return EditorSessionMaterialization{Message: "直连素材路径不在 App 管理目录内。"}, nil
		}
		if size, valid := verifiedDownload(path, asset.SHA256, asset.SizeBytes); !valid || size != asset.SizeBytes {
			return EditorSessionMaterialization{Message: "直连素材 checksum 校验状态无效：" + ref.ID}, nil
		}
		ref.URI = path
		if ref.Metadata == nil {
			ref.Metadata = map[string]any{}
		}
		ref.Metadata["local_path"] = path
		for index := range result.GeneratedAssets {
			if result.GeneratedAssets[index].ID == ref.ID {
				result.GeneratedAssets[index] = ref
			}
		}
		if result.ExecutionTrace != nil {
			for index := range result.ExecutionTrace.Artifacts {
				if result.ExecutionTrace.Artifacts[index].ID == ref.ID {
					result.ExecutionTrace.Artifacts[index] = ref
				}
			}
		}
		if result.StageEventLogRef != nil && result.StageEventLogRef.ID == ref.ID {
			*result.StageEventLogRef = ref
		}
	}
	request, err := s.localEditorHandoffRequest(result)
	if err != nil {
		return EditorSessionMaterialization{Message: "直连结果已下载，但编辑器交接尚不可用：" + err.Error()}, nil
	}
	session, created, err := s.EnsureEditorSessionFromResultPackage(ctx, request)
	if err != nil {
		return EditorSessionMaterialization{}, err
	}
	message := "直连 Browser Agent 素材已校验并登记，可进入本地编辑器。"
	if !created {
		message = "直连 Browser Agent 素材已存在，已复用原编辑会话。"
	}
	return EditorSessionMaterialization{Ready: true, Created: created, SessionID: session.SessionID, Message: message}, nil
}

func (s *Service) SaveDirectTransportSettings(ctx context.Context, request DirectTransportSettingsRequest) (DirectTransportRuntimeView, error) {
	controlURL, err := validateDirectTransportControlURL(request.ControlURL)
	if err != nil {
		return DirectTransportRuntimeView{}, err
	}
	if len(strings.TrimSpace(request.AccessToken)) < 32 {
		return DirectTransportRuntimeView{}, errors.New("direct Browser Agent access token must contain at least 32 characters")
	}
	if err := s.storeDirectToken(request.AccessToken); err != nil {
		return DirectTransportRuntimeView{}, err
	}
	if _, err := s.loadOrCreateDirectInstallationIdentity(); err != nil {
		return DirectTransportRuntimeView{}, err
	}
	payload, err := json.MarshalIndent(directTransportSettingsFile{ControlURL: controlURL, SavedAt: time.Now().UTC()}, "", "  ")
	if err != nil {
		return DirectTransportRuntimeView{}, err
	}
	path := filepath.Join(s.runtime.DataRoot, directTransportSettingsFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return DirectTransportRuntimeView{}, err
	}
	if err := atomicWritePrivateFile(path, payload); err != nil {
		return DirectTransportRuntimeView{}, err
	}
	s.directTransportMu.Lock()
	s.directTransportURL = controlURL
	s.directTransportMu.Unlock()
	return s.DirectTransportStatus(ctx)
}

// ValidateDirectTransportControlURL exposes the same URL policy used by the
// desktop settings flow to narrowly-scoped provisioning tools. Keeping the
// policy in this package prevents an operational importer from accepting a
// URL that the App itself would later reject.
func ValidateDirectTransportControlURL(raw string) (string, error) {
	return validateDirectTransportControlURL(raw)
}

func (s *Service) DirectTransportStatus(ctx context.Context) (DirectTransportRuntimeView, error) {
	controlURL := s.effectiveDirectTransportURL()
	view := directTransportViewForURL(controlURL)
	token, tokenErr := s.readDirectToken()
	view.TokenConfigured = tokenErr == nil
	if identity, err := s.readExistingDirectInstallationIdentity(); err == nil {
		publicKey, _ := base64.StdEncoding.DecodeString(identity.PublicKeyBase64)
		view.InstallationIDSuffix = suffix(model.DirectInstallationID(ed25519.PublicKey(publicKey)), 8)
	}
	if controlURL == "" || tokenErr != nil {
		return view, nil
	}
	client := &http.Client{Timeout: 10 * time.Second}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, controlURL+"/v1/direct/health", nil)
	if err != nil {
		return view, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		view.ErrorClass = "unreachable"
		return view, nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		view.ErrorClass = "http_" + strconv.Itoa(response.StatusCode)
		return view, nil
	}
	var health model.DirectHealthResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&health); err != nil {
		view.ErrorClass = "invalid_protocol"
		return view, nil
	}
	view.ProtocolVersion = health.ProtocolVersion
	view.CryptoSuite = health.CryptoSuite
	view.SupportedProtocolVersions = append([]string(nil), health.SupportedProtocolVersions...)
	view.SupportedPackageSchemaVersions = append([]string(nil), health.SupportedPackageSchemaVersions...)
	view.SupportedRuntimes = append([]string(nil), health.SupportedRuntimes...)
	view.SupportedWorkerProtocolVersions = append([]string(nil), health.SupportedWorkerProtocolVersions...)
	view.SupportedOutcomeVerifierRulesVersions = append([]string(nil), health.SupportedOutcomeVerifierRulesVersions...)
	view.Capabilities = health.Capabilities
	view.Reachable = health.ProtocolVersion == model.DirectTransportProtocolVersion && health.CryptoSuite == model.DirectTransportCryptoSuite &&
		directSliceContains(health.SupportedProtocolVersions, model.DirectTransportProtocolVersion) &&
		directSliceContains(health.SupportedPackageSchemaVersions, model.ClientExecutionPackageSchemaVersion) &&
		directSliceContains(health.SupportedRuntimes, model.ExecutableScriptRuntimeBrowserAgentOutlineV1) &&
		directSliceContains(health.SupportedWorkerProtocolVersions, model.DirectWorkerProtocolVersion) &&
		directSliceContains(health.SupportedOutcomeVerifierRulesVersions, model.BrowserAgentOutcomeVerifierRulesVersion)
	if !view.Reachable {
		view.ErrorClass = "protocol_mismatch"
	}
	return view, nil
}

func directSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (s *Service) UploadDirectExecutionPackage(ctx context.Context, projectID string, request DirectTransportUploadRequest) (DirectTransportUploadResult, error) {
	if strings.TrimSpace(request.PackageDigestSHA256) == "" {
		return DirectTransportUploadResult{}, errors.New("direct Browser Agent upload requires the authoritative package digest")
	}
	approval := CloudUploadInitRequest{OrgID: request.OrgID, ProjectID: projectID, PackageDigestSHA256: request.PackageDigestSHA256, ApprovalSubjectDigestSHA256: request.ApprovalSubjectDigestSHA256, ConfidenceAssessmentHash: request.ConfidenceAssessmentHash, RiskConfirmed: request.RiskConfirmed, IdempotencyKey: request.IdempotencyKey}
	build, err := s.ApproveClientExecutionPackage(ctx, projectID, firstNonEmptyString(request.OrgID, defaultDesktopOrgID), approval)
	if err != nil {
		return DirectTransportUploadResult{}, err
	}
	lease, err := s.acquireDirectLease(ctx, projectID)
	if err != nil {
		return DirectTransportUploadResult{}, err
	}
	cleanLease := true
	defer func() {
		if cleanLease {
			_ = s.releaseDirectLeaseRemote(context.Background(), lease)
			_ = s.deleteDirectLease(projectID)
		}
	}()
	build, err = bindDirectExecutionPackageOrigin(build, lease.InstallationID)
	if err != nil {
		return DirectTransportUploadResult{}, err
	}
	messageID := "package_" + shortID(build.PackageDigestSHA256+request.IdempotencyKey)
	message, err := model.EncryptDirectTransportJSON(build.Package, lease, messageID, "client_execution_package", model.DirectTransportDirectionUpload, time.Now().UTC())
	if err != nil {
		return DirectTransportUploadResult{}, err
	}
	var receipt model.DirectPackageReceipt
	if err := s.directDataRequest(ctx, lease, http.MethodPost, "/v1/direct/packages", message, "package_receipt", &receipt); err != nil {
		return DirectTransportUploadResult{}, err
	}
	if receipt.PackageID != build.Package.PackageID || receipt.PackageDigest != build.PackageDigestSHA256 {
		return DirectTransportUploadResult{}, errors.New("direct Browser Agent receipt does not match the approved package")
	}
	var credentialReceipt *model.DirectCredentialReceipt
	if len(build.Package.CredentialGrants) > 0 {
		value, err := s.uploadDirectCredential(ctx, lease, receipt, build)
		if err != nil {
			return DirectTransportUploadResult{}, err
		}
		credentialReceipt = &value
		receipt.Status = value.Status
		receipt.Stage = value.Stage
	}
	if err := s.persistDirectUpload(ctx, projectID, build.OrgID, lease, receipt); err != nil {
		return DirectTransportUploadResult{}, err
	}
	cleanLease = false
	return DirectTransportUploadResult{Build: build, Lease: directLeaseView(lease), Receipt: receipt, CredentialReceipt: credentialReceipt}, nil
}

func bindDirectExecutionPackageOrigin(build ClientExecutionPackageBuild, installationID string) (ClientExecutionPackageBuild, error) {
	installationID = strings.TrimSpace(installationID)
	if installationID == "" {
		return ClientExecutionPackageBuild{}, errors.New("direct Browser Agent installation binding is required")
	}
	encoded, err := json.Marshal(build.Package)
	if err != nil {
		return ClientExecutionPackageBuild{}, err
	}
	var pkg model.ClientExecutionPackage
	if err := json.Unmarshal(encoded, &pkg); err != nil {
		return ClientExecutionPackageBuild{}, err
	}
	approval := &pkg.SafetyReport.HumanApproval
	if pkg.ProducerInstallationID != "" && pkg.ProducerInstallationID != installationID {
		return ClientExecutionPackageBuild{}, errors.New("unverified_origin: package producer installation does not own the direct lease")
	}
	if approval.ApprovedByInstallationID != "" && approval.ApprovedByInstallationID != installationID {
		return ClientExecutionPackageBuild{}, errors.New("unverified_origin: package approval installation does not own the direct lease")
	}
	pkg.ProducerInstallationID = installationID
	approval.ApprovedByInstallationID = installationID
	approval.ApprovalSchemaVersion = "cascade.user_approval.v1"
	confidence, err := model.AssessClientExecutionPackage(&pkg)
	if err != nil {
		return ClientExecutionPackageBuild{}, err
	}
	if confidence.Readiness == model.PackageReadinessBlocked {
		return ClientExecutionPackageBuild{}, errors.New("direct origin binding made the package confidence assessment blocked")
	}
	pkg.ConfidenceSummary = confidence
	approvalDigest, err := refreshPackageApprovalDigests(&pkg)
	if err != nil {
		return ClientExecutionPackageBuild{}, err
	}
	if err := model.ValidateClientExecutionPackageForCloudExecution(&pkg); err != nil {
		return ClientExecutionPackageBuild{}, err
	}
	digest, err := model.DigestCanonicalJSON(pkg)
	if err != nil {
		return ClientExecutionPackageBuild{}, err
	}
	build.Package = pkg
	build.ApprovalSubjectDigestSHA256 = approvalDigest
	build.PackageDigestSHA256 = digest
	build.SizeReport = packageSizeReportForPackage(pkg)
	// Direct transport does not expose or consume a Legacy Exchange envelope.
	build.Envelope = model.ExchangeEnvelope{}
	build.PayloadRef = model.EncryptedPayloadRef{}
	return build, nil
}

// ReleaseDirectTransportLease retires an installation's dedicated listener
// after a terminal run and verified downloads. It is explicit and idempotent
// from the App perspective; active or unverified runs remain protected.
func (s *Service) ReleaseDirectTransportLease(ctx context.Context, projectID string) (DirectTransportReleaseResult, error) {
	lease, err := s.loadDirectLease(projectID)
	if err != nil {
		return DirectTransportReleaseResult{}, err
	}
	state, err := s.states.Load(ctx, projectID)
	if err != nil || state.DesktopCloudRun == nil || state.DesktopCloudRun.Transport != directTransportStateName {
		return DirectTransportReleaseResult{}, errors.New("direct Browser Agent run state is unavailable")
	}
	run := state.DesktopCloudRun
	if run.Status == "completed" && !run.ResultDownloaded {
		return DirectTransportReleaseResult{}, errors.New("direct Browser Agent artifacts must be checksum-verified before lease release")
	}
	if run.Status != "completed" && run.Status != "failed" {
		return DirectTransportReleaseResult{}, errors.New("direct Browser Agent lease cannot be released while the job is active")
	}
	states, err := s.states.List(ctx)
	if err != nil {
		return DirectTransportReleaseResult{}, err
	}
	for _, candidate := range states {
		if candidate == nil || candidate.ProjectID == projectID || candidate.DesktopCloudRun == nil {
			continue
		}
		other := candidate.DesktopCloudRun
		if other.Transport == directTransportStateName && strings.TrimSpace(other.LeaseID) == lease.LeaseID && other.Status != "completed" && other.Status != "failed" && other.Status != "canceled" && other.Status != "expired" {
			return DirectTransportReleaseResult{}, errors.New("direct Browser Agent lease is shared by another active project")
		}
	}
	if run.Status == "completed" {
		if err := s.ackDirectResultRemote(ctx, projectID, lease, run); err != nil {
			return DirectTransportReleaseResult{}, err
		}
	}
	if err := s.releaseDirectLeaseRemote(ctx, lease); err != nil {
		return DirectTransportReleaseResult{}, err
	}
	if err := s.persistDirectLeaseRelease(ctx, projectID); err != nil {
		return DirectTransportReleaseResult{}, err
	}
	if err := s.deleteDirectLease(projectID); err != nil {
		return DirectTransportReleaseResult{}, err
	}
	return DirectTransportReleaseResult{Released: true, Lease: directLeaseView(lease)}, nil
}

func (s *Service) ackDirectResultRemote(ctx context.Context, projectID string, lease model.DirectPortLease, run *orchestrator.DesktopCloudRunState) error {
	if run == nil || run.CloudJobID == "" || run.ResultPackageID == "" || !run.ResultDownloaded {
		return errors.New("direct Browser Agent result is not ready for ACK")
	}
	ids := make([]string, 0, len(run.DownloadedAssets))
	for _, asset := range run.DownloadedAssets {
		if !asset.Verified || asset.ArtifactID == "" {
			return errors.New("direct Browser Agent ACK requires every artifact checksum")
		}
		ids = append(ids, asset.ArtifactID)
	}
	sort.Strings(ids)
	now := time.Now().UTC()
	request := model.DirectResultAckRequest{ProtocolVersion: model.DirectTransportProtocolVersion, InstallationID: lease.InstallationID, JobID: run.CloudJobID, ResultPackageID: run.ResultPackageID, ReceivedArtifactIDs: ids, VerifiedChecksums: true, AckedAt: now}
	message, err := model.EncryptDirectTransportJSON(request, lease, "ack_"+shortID(run.CloudJobID+"|"+run.ResultPackageID), "result_ack", model.DirectTransportDirectionUpload, now)
	if err != nil {
		return err
	}
	var receipt model.DirectResultAckReceipt
	path := "/v1/direct/jobs/" + url.PathEscape(run.CloudJobID) + "/ack"
	if err := s.directDataRequest(ctx, lease, http.MethodPost, path, message, "result_ack_receipt", &receipt); err != nil {
		return err
	}
	if receipt.JobID != run.CloudJobID || receipt.ResultPackageID != run.ResultPackageID || !receipt.VerifiedChecksums {
		return errors.New("direct Browser Agent ACK receipt binding mismatch")
	}
	return s.persistCloudAck(ctx, projectID, model.ResultPackageAckResponse{ResultPackageID: receipt.ResultPackageID, ReceivedAssetIDs: receipt.ReceivedArtifactIDs, VerifiedChecksums: true, AckedAt: receipt.AckedAt, AckedByInstallID: lease.InstallationID})
}

func (s *Service) releaseDirectLeaseRemote(ctx context.Context, lease model.DirectPortLease) error {
	identity, err := s.readExistingDirectInstallationIdentity()
	if err != nil {
		return err
	}
	nonce, err := model.NewDirectTransportNonce()
	if err != nil {
		return err
	}
	publicKey, err := base64.StdEncoding.DecodeString(identity.PublicKeyBase64)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return errors.New("direct Browser Agent installation public key is invalid")
	}
	privateKey, err := base64.StdEncoding.DecodeString(identity.PrivateKeyBase64)
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return errors.New("direct Browser Agent installation signing key is invalid")
	}
	now := time.Now().UTC()
	request := model.DirectLeaseReleaseRequest{ProtocolVersion: model.DirectTransportProtocolVersion, InstallationID: model.DirectInstallationID(ed25519.PublicKey(publicKey)), LeaseID: lease.LeaseID, TimestampUnixMS: now.UnixMilli(), RequestNonce: nonce, SigningPublicKeyBase64: identity.PublicKeyBase64}
	request.SignatureBase64 = base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.PrivateKey(privateKey), model.DirectLeaseReleaseRequestSigningPayload(request)))
	body, _ := json.Marshal(request)
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.effectiveDirectTransportURL(), "/")+"/v1/direct/leases/release", bytes.NewReader(body))
	if err != nil {
		return err
	}
	token, err := s.readDirectToken()
	if err != nil {
		return errors.New("direct Browser Agent access token is unavailable")
	}
	httpRequest.Header.Set("Authorization", "Bearer "+token)
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(httpRequest)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		releaseErr := readDirectHTTPError(response)
		var transportErr *directTransportHTTPError
		if errors.As(releaseErr, &transportErr) && transportErr.Code == "lease_not_found" {
			return nil
		}
		return errors.New("direct Browser Agent lease release endpoint is unavailable")
	}
	if response.StatusCode != http.StatusOK {
		return readDirectHTTPError(response)
	}
	var result struct {
		Released bool `json:"released"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return err
	}
	if !result.Released {
		return errors.New("direct Browser Agent lease release was not acknowledged")
	}
	return nil
}

func (s *Service) uploadDirectCredential(ctx context.Context, lease model.DirectPortLease, receipt model.DirectPackageReceipt, build ClientExecutionPackageBuild) (model.DirectCredentialReceipt, error) {
	if len(build.Package.CredentialGrants) != 1 {
		return model.DirectCredentialReceipt{}, errors.New("direct Browser Agent currently requires exactly one scoped login credential grant")
	}
	grant := build.Package.CredentialGrants[0]
	const prefix = "credential://demo/"
	if !strings.HasPrefix(grant.CloudSecretRef, prefix) || s.readDemoCredential == nil {
		return model.DirectCredentialReceipt{}, errors.New("approved demo credential is unavailable from the local vault")
	}
	credential, err := s.readDemoCredential(strings.TrimPrefix(grant.CloudSecretRef, prefix))
	if err != nil {
		return model.DirectCredentialReceipt{}, errors.New("approved demo credential is unavailable from the local vault")
	}
	now := time.Now().UTC()
	expiresAt := lease.ExpiresAt
	if !grant.ExpiresAt.IsZero() && grant.ExpiresAt.Before(expiresAt) {
		expiresAt = grant.ExpiresAt
	}
	if !now.Before(expiresAt) {
		return model.DirectCredentialReceipt{}, errors.New("approved demo credential grant expired before upload")
	}
	value := model.DirectCredentialValue{SecretRef: grant.CloudSecretRef, Username: credential.Username, Password: credential.Password, ExpiresAt: expiresAt, AllowedDomains: append([]string(nil), grant.AllowedDomains...), AllowedOperations: append([]string(nil), grant.AllowedOperations...)}
	envelope := model.DirectCredentialEnvelope{
		ProtocolVersion: model.DirectTransportProtocolVersion, LeaseID: lease.LeaseID, InstallationID: lease.InstallationID,
		JobID: receipt.JobID, PackageID: build.Package.PackageID, PackageDigest: build.PackageDigestSHA256,
		GrantID: grant.GrantID, SecretRef: grant.CloudSecretRef, IssuedAt: now, ExpiresAt: expiresAt,
		AllowedDomains: append([]string(nil), grant.AllowedDomains...), AllowedOperations: append([]string(nil), grant.AllowedOperations...), Credential: value,
	}
	message, err := model.EncryptDirectTransportJSON(envelope, lease, "credential_"+shortID(receipt.JobID+"|"+grant.GrantID), "credential_envelope", model.DirectTransportDirectionUpload, now)
	credential.Username, credential.Password = "", ""
	value.Username, value.Password = "", ""
	envelope.Credential.Username, envelope.Credential.Password = "", ""
	if err != nil {
		return model.DirectCredentialReceipt{}, err
	}
	var accepted model.DirectCredentialReceipt
	path := "/v1/direct/jobs/" + url.PathEscape(receipt.JobID) + "/credentials"
	if err := s.directDataRequest(ctx, lease, http.MethodPost, path, message, "credential_receipt", &accepted); err != nil {
		return accepted, err
	}
	if accepted.JobID != receipt.JobID || accepted.PackageID != build.Package.PackageID || accepted.GrantID != grant.GrantID || accepted.SecretRef != grant.CloudSecretRef || accepted.Status != "queued" {
		return accepted, errors.New("direct Browser Agent credential receipt binding mismatch")
	}
	return accepted, nil
}

func (s *Service) GetDirectExecutionStatus(ctx context.Context, projectID, jobID string) (model.DirectJobStatus, error) {
	var status model.DirectJobStatus
	if err := s.directProjectRead(ctx, projectID, "/v1/direct/jobs/"+url.PathEscape(jobID), "job_status", &status); err != nil {
		return status, err
	}
	if status.JobID != jobID {
		return status, errors.New("direct Browser Agent status job binding mismatch")
	}
	if err := s.persistDirectStatus(ctx, projectID, status); err != nil {
		return status, err
	}
	return status, nil
}

func (s *Service) GetDirectResult(ctx context.Context, projectID, jobID string) (model.RecordingResultPackage, error) {
	var result model.RecordingResultPackage
	if err := s.directProjectRead(ctx, projectID, "/v1/direct/jobs/"+url.PathEscape(jobID)+"/result", "recording_result", &result); err != nil {
		return result, err
	}
	if result.CloudJobID != jobID {
		return result, errors.New("direct Browser Agent result job binding mismatch")
	}
	state, err := s.states.Load(ctx, projectID)
	if err != nil {
		return result, err
	}
	build, err := buildClientExecutionPackageFromState(state, defaultDesktopOrgID, time.Now().UTC())
	if err != nil {
		return result, err
	}
	if err := result.ValidateStatusContract(); err != nil {
		return result, err
	}
	if result.Status != model.RecordingResultStatusFailed {
		if err := model.ValidateRecordingResultPackageForRender(&result, &build.Package); err != nil {
			return result, err
		}
	}
	if err := model.ValidateFormalRecordingResultArtifacts(&result, &build.Package); err != nil {
		return result, err
	}
	if err := s.persistCloudResult(ctx, projectID, defaultDesktopOrgID, result); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Service) DownloadDirectArtifact(ctx context.Context, projectID string, request DirectArtifactDownloadRequest) (CloudDeliverableDownloadResult, error) {
	if strings.TrimSpace(request.JobID) == "" || strings.TrimSpace(request.Artifact.ArtifactID) == "" {
		return CloudDeliverableDownloadResult{}, errors.New("job_id and artifact are required")
	}
	lease, err := s.directLeaseForRequest(ctx, projectID)
	if err != nil {
		return CloudDeliverableDownloadResult{}, err
	}
	root := request.OutputDirectory
	if strings.TrimSpace(root) == "" {
		root = filepath.Join(s.runtime.ArtifactRoot, "desktop", "direct-downloads", safePathSegment(projectID), safePathSegment(request.JobID))
	}
	absoluteRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return CloudDeliverableDownloadResult{}, err
	}
	managedRoot, err := filepath.Abs(filepath.Clean(s.runtime.ArtifactRoot))
	if err != nil {
		return CloudDeliverableDownloadResult{}, err
	}
	if !pathWithinRoot(absoluteRoot, managedRoot) {
		return CloudDeliverableDownloadResult{}, errors.New("direct artifact output directory is outside the App-managed artifact root")
	}
	fileName := safePathSegment(request.Artifact.FileName)
	if fileName == "" {
		fileName = safePathSegment(request.Artifact.ArtifactID)
	}
	if err := os.MkdirAll(absoluteRoot, 0o700); err != nil {
		return CloudDeliverableDownloadResult{}, err
	}
	path := filepath.Join(absoluteRoot, fileName)
	if !pathWithinRoot(path, absoluteRoot) {
		return CloudDeliverableDownloadResult{}, errors.New("artifact file name is outside output directory")
	}
	expected := strings.ToLower(strings.TrimSpace(request.Artifact.SHA256))
	if size, ok := verifiedDownload(path, expected, request.Artifact.SizeBytes); ok {
		return CloudDeliverableDownloadResult{ArtifactID: request.Artifact.ArtifactID, Kind: request.Artifact.Kind, Role: request.Artifact.Role, LocalPath: path, SHA256: expected, ExpectedSHA256: expected, MimeType: request.Artifact.MimeType, SizeBytes: size, ChecksumVerified: true}, nil
	}
	partial := path + ".part"
	file, err := os.OpenFile(partial, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return CloudDeliverableDownloadResult{}, err
	}
	hasher := sha256.New()
	written := int64(0)
	chunkCount := (request.Artifact.SizeBytes + int64(model.DirectArtifactChunkBytes) - 1) / int64(model.DirectArtifactChunkBytes)
	for index := int64(0); index < chunkCount; index++ {
		chunkPath := "/v1/direct/jobs/" + url.PathEscape(request.JobID) + "/artifacts/" + url.PathEscape(request.Artifact.ArtifactID) + "/chunks/" + strconv.FormatInt(index, 10)
		messageType := "artifact_chunk:" + request.Artifact.ArtifactID + ":" + strconv.FormatInt(index, 10)
		var chunk []byte
		if err := s.directDataRequest(ctx, lease, http.MethodGet, chunkPath, nil, messageType, &chunk); err != nil {
			if shouldRenewDirectLease(err) {
				lease, err = s.acquireDirectLease(ctx, projectID)
				if err == nil {
					err = s.directDataRequest(ctx, lease, http.MethodGet, chunkPath, nil, messageType, &chunk)
				}
			}
			if err != nil {
				_ = file.Close()
				_ = os.Remove(partial)
				return CloudDeliverableDownloadResult{}, err
			}
		}
		expectedChunkSize := int64(model.DirectArtifactChunkBytes)
		if remaining := request.Artifact.SizeBytes - written; remaining < expectedChunkSize {
			expectedChunkSize = remaining
		}
		if int64(len(chunk)) != expectedChunkSize {
			_ = file.Close()
			_ = os.Remove(partial)
			return CloudDeliverableDownloadResult{}, errors.New("direct Browser Agent artifact chunk size mismatch")
		}
		if _, err := io.MultiWriter(file, hasher).Write(chunk); err != nil {
			_ = file.Close()
			_ = os.Remove(partial)
			return CloudDeliverableDownloadResult{}, err
		}
		written += int64(len(chunk))
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(partial)
		return CloudDeliverableDownloadResult{}, err
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	if digest != expected || written != request.Artifact.SizeBytes {
		_ = os.Remove(partial)
		return CloudDeliverableDownloadResult{}, errors.New("direct Browser Agent artifact checksum or size mismatch")
	}
	if err := os.Rename(partial, path); err != nil {
		_ = os.Remove(partial)
		return CloudDeliverableDownloadResult{}, err
	}
	if err := writeVerifiedDownloadMarker(path, expected); err != nil {
		return CloudDeliverableDownloadResult{}, err
	}
	result := CloudDeliverableDownloadResult{ArtifactID: request.Artifact.ArtifactID, Kind: request.Artifact.Kind, Role: request.Artifact.Role, LocalPath: path, SHA256: digest, ExpectedSHA256: expected, MimeType: request.Artifact.MimeType, SizeBytes: written, ChecksumVerified: true}
	_ = s.persistCloudDownload(ctx, projectID, "", result)
	return result, nil
}

func (s *Service) ReviewDirectResult(ctx context.Context, projectID string, request DirectResultReviewRequest) (model.ResultReviewRecord, error) {
	if strings.TrimSpace(projectID) == "" || strings.TrimSpace(request.JobID) == "" || strings.TrimSpace(request.ResultPackageID) == "" || strings.TrimSpace(request.Review.IdempotencyKey) == "" {
		return model.ResultReviewRecord{}, errors.New("project_id, job_id, result_package_id, and review idempotency key are required")
	}
	switch request.Review.Decision {
	case model.ResultReviewApproved, model.ResultReviewReeditRequested, model.ResultReviewRerecordRequested:
	default:
		return model.ResultReviewRecord{}, errors.New("result review decision is invalid")
	}
	state, err := s.states.Load(ctx, projectID)
	if err != nil || state.DesktopCloudRun == nil {
		return model.ResultReviewRecord{}, errors.New("direct Browser Agent run state is unavailable")
	}
	run := state.DesktopCloudRun
	if run.Transport != directTransportStateName || run.CloudJobID != request.JobID || run.ResultPackageID != request.ResultPackageID || !run.ResultDownloaded {
		return model.ResultReviewRecord{}, errors.New("direct result review binding or verified download requirement failed")
	}
	if existing := run.ResultReview; existing != nil && existing.IdempotencyKey == request.Review.IdempotencyKey {
		if existing.Decision != string(request.Review.Decision) || existing.Summary != strings.TrimSpace(request.Review.Summary) {
			return model.ResultReviewRecord{}, errors.New("direct result review idempotency key conflicts with an existing review")
		}
		return model.ResultReviewRecord{ReviewID: existing.ReviewID, ResultPackageID: request.ResultPackageID, IdempotencyKey: existing.IdempotencyKey, ReviewerInstallID: existing.ReviewerInstallID, Decision: model.ResultReviewDecision(existing.Decision), Summary: existing.Summary, ReviewedAt: existing.UpdatedAt}, nil
	}
	now := request.Review.ReviewedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	identity, err := s.loadOrCreateDirectInstallationIdentity()
	if err != nil {
		return model.ResultReviewRecord{}, err
	}
	publicKey, err := base64.StdEncoding.DecodeString(identity.PublicKeyBase64)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return model.ResultReviewRecord{}, errors.New("direct Browser Agent installation public key is invalid")
	}
	reviewerInstallID := model.DirectInstallationID(ed25519.PublicKey(publicKey))
	record := model.ResultReviewRecord{
		ReviewID:        "direct_review_" + shortHash(projectID+"|"+request.JobID+"|"+request.ResultPackageID+"|"+request.Review.IdempotencyKey),
		ResultPackageID: request.ResultPackageID, IdempotencyKey: request.Review.IdempotencyKey,
		ReviewerInstallID: reviewerInstallID, Decision: request.Review.Decision, Summary: strings.TrimSpace(request.Review.Summary),
		Annotations: append([]model.ResultReviewAnnotation(nil), request.Review.Annotations...), ReviewedAt: now,
	}
	if err := s.persistCloudReview(ctx, projectID, record); err != nil {
		return model.ResultReviewRecord{}, err
	}
	return record, nil
}

func (s *Service) directLeaseForRequest(ctx context.Context, projectID string) (model.DirectPortLease, error) {
	if state, stateErr := s.states.Load(ctx, projectID); stateErr == nil && state.DesktopCloudRun != nil {
		run := state.DesktopCloudRun
		if run.Transport == directTransportStateName && run.Stage == "lease_released" {
			return model.DirectPortLease{}, errors.New("direct Browser Agent lease was released for this run")
		}
	}
	lease, err := s.loadDirectLease(projectID)
	if err == nil {
		return lease, nil
	}
	// Leases are intentionally short-lived and listeners disappear on gateway
	// restart. The same installation may obtain a new port and continue its
	// persisted jobs; no package approval or upload is repeated here.
	return s.acquireDirectLease(ctx, projectID)
}

func (s *Service) directProjectRead(ctx context.Context, projectID, path, responseType string, target any) error {
	lease, err := s.directLeaseForRequest(ctx, projectID)
	if err != nil {
		return err
	}
	firstErr := s.directDataRequest(ctx, lease, http.MethodGet, path, nil, responseType, target)
	if firstErr == nil {
		return nil
	}
	if !shouldRenewDirectLease(firstErr) {
		return firstErr
	}
	lease, err = s.acquireDirectLease(ctx, projectID)
	if err != nil {
		return err
	}
	return s.directDataRequest(ctx, lease, http.MethodGet, path, nil, responseType, target)
}

type directTransportHTTPError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *directTransportHTTPError) Error() string {
	if e.Code != "" {
		return e.Code + ": " + e.Message
	}
	return fmt.Sprintf("direct Browser Agent returned HTTP %d", e.StatusCode)
}

func shouldRenewDirectLease(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var responseErr *directTransportHTTPError
	if errors.As(err, &responseErr) {
		return responseErr.StatusCode == http.StatusUnauthorized && responseErr.Code == "direct_request_invalid"
	}
	var networkErr *url.Error
	return errors.As(err, &networkErr)
}

func (s *Service) acquireDirectLease(ctx context.Context, projectID string) (model.DirectPortLease, error) {
	controlURL := s.effectiveDirectTransportURL()
	if controlURL == "" {
		return model.DirectPortLease{}, errors.New("direct Browser Agent server is not configured")
	}
	token, err := s.readDirectToken()
	if err != nil {
		return model.DirectPortLease{}, errors.New("direct Browser Agent access token is unavailable")
	}
	identity, err := s.loadOrCreateDirectInstallationIdentity()
	if err != nil {
		return model.DirectPortLease{}, err
	}
	nonce, err := model.NewDirectTransportNonce()
	if err != nil {
		return model.DirectPortLease{}, err
	}
	publicKey, err := base64.StdEncoding.DecodeString(identity.PublicKeyBase64)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return model.DirectPortLease{}, errors.New("direct Browser Agent installation public key is invalid")
	}
	privateKey, err := base64.StdEncoding.DecodeString(identity.PrivateKeyBase64)
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return model.DirectPortLease{}, errors.New("direct Browser Agent installation signing key is invalid")
	}
	directInstallID := model.DirectInstallationID(ed25519.PublicKey(publicKey))
	payload := model.DirectLeaseRequest{ProtocolVersion: model.DirectTransportProtocolVersion, InstallationID: directInstallID, ClientVersion: firstNonEmptyString(s.runtime.AppVersion, defaultDesktopAppVersion), TimestampUnixMS: time.Now().UTC().UnixMilli(), RequestNonce: nonce, SigningPublicKeyBase64: identity.PublicKeyBase64}
	payload.SignatureBase64 = base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.PrivateKey(privateKey), model.DirectLeaseRequestSigningPayload(payload)))
	body, _ := json.Marshal(payload)
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, controlURL+"/v1/direct/leases", bytes.NewReader(body))
	if err != nil {
		return model.DirectPortLease{}, err
	}
	httpRequest.Header.Set("Authorization", "Bearer "+token)
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(httpRequest)
	if err != nil {
		return model.DirectPortLease{}, fmt.Errorf("direct Browser Agent lease failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return model.DirectPortLease{}, readDirectHTTPError(response)
	}
	var lease model.DirectPortLease
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&lease); err != nil {
		return lease, err
	}
	if lease.InstallationID != directInstallID {
		return lease, errors.New("direct Browser Agent lease installation binding mismatch")
	}
	if err := validateDirectDataURL(controlURL, lease); err != nil {
		return lease, err
	}
	encoded, err := json.Marshal(lease)
	if err != nil {
		return lease, err
	}
	if err := s.storeDirectLease(projectID, encoded); err != nil {
		return lease, err
	}
	// A gateway restart or lease expiry can replace the listener while the
	// approved job remains active. Persist the new lease metadata immediately so
	// the App UI and restart state never advertise the retired port.
	if state, stateErr := s.states.Load(ctx, projectID); stateErr == nil && state.DesktopCloudRun != nil && state.DesktopCloudRun.Transport == directTransportStateName && state.DesktopCloudRun.Stage != "lease_released" {
		_ = s.updateDesktopCloudRun(ctx, projectID, func(run *orchestrator.DesktopCloudRunState) {
			run.LeaseID = lease.LeaseID
			run.DataPort = lease.DataPort
			expires := lease.ExpiresAt
			run.LeaseExpiresAt = &expires
			run.Message = "Browser Agent 安全执行会话已恢复。"
		})
	}
	return lease, nil
}

func (s *Service) loadOrCreateDirectInstallationIdentity() (directInstallationIdentity, error) {
	s.directIdentityMu.Lock()
	defer s.directIdentityMu.Unlock()
	if identity, err := s.readExistingDirectInstallationIdentity(); err == nil {
		return identity, nil
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return directInstallationIdentity{}, err
	}
	identity := directInstallationIdentity{PublicKeyBase64: base64.StdEncoding.EncodeToString(publicKey), PrivateKeyBase64: base64.StdEncoding.EncodeToString(privateKey)}
	data, err := json.Marshal(identity)
	if err != nil {
		return directInstallationIdentity{}, err
	}
	if err := s.storeDirectIdentity(data); err != nil {
		return directInstallationIdentity{}, err
	}
	return identity, nil
}

func (s *Service) readExistingDirectInstallationIdentity() (directInstallationIdentity, error) {
	data, err := s.readDirectIdentity()
	if err != nil {
		return directInstallationIdentity{}, err
	}
	var identity directInstallationIdentity
	if json.Unmarshal(data, &identity) != nil {
		return directInstallationIdentity{}, errors.New("direct Browser Agent installation identity is invalid")
	}
	publicKey, publicErr := base64.StdEncoding.DecodeString(identity.PublicKeyBase64)
	privateKey, privateErr := base64.StdEncoding.DecodeString(identity.PrivateKeyBase64)
	if publicErr != nil || privateErr != nil || len(publicKey) != ed25519.PublicKeySize || len(privateKey) != ed25519.PrivateKeySize || !ed25519.PrivateKey(privateKey).Public().(ed25519.PublicKey).Equal(ed25519.PublicKey(publicKey)) {
		return directInstallationIdentity{}, errors.New("direct Browser Agent installation identity is invalid")
	}
	return identity, nil
}

func (s *Service) directDataRequest(ctx context.Context, lease model.DirectPortLease, method, path string, input any, responseType string, target any) error {
	var body []byte
	var err error
	if input != nil {
		body, err = json.Marshal(input)
		if err != nil {
			return err
		}
	}
	timestamp := time.Now().UTC().UnixMilli()
	nonce, err := model.NewDirectTransportNonce()
	if err != nil {
		return err
	}
	digest := model.SHA256Hex(body)
	signature, err := model.DirectTransportRequestSignature(lease, method, path, timestamp, nonce, digest)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(lease.DataURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Cascade-Timestamp", strconv.FormatInt(timestamp, 10))
	request.Header.Set("X-Cascade-Nonce", nonce)
	request.Header.Set("X-Cascade-Body-SHA256", digest)
	request.Header.Set("X-Cascade-Signature", signature)
	response, err := (&http.Client{Timeout: 60 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return readDirectHTTPError(response)
	}
	var encrypted model.DirectEncryptedMessage
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<20)).Decode(&encrypted); err != nil {
		return err
	}
	if bytesTarget, ok := target.(*[]byte); ok {
		plain, err := model.DecryptDirectTransportBytes(encrypted, lease, responseType, model.DirectTransportDirectionResult, time.Now().UTC())
		if err == nil {
			*bytesTarget = plain
		}
		return err
	}
	return model.DecryptDirectTransportJSON(encrypted, lease, responseType, model.DirectTransportDirectionResult, time.Now().UTC(), target)
}

func (s *Service) loadDirectLease(projectID string) (model.DirectPortLease, error) {
	data, err := s.readDirectLease(projectID)
	if err != nil {
		return model.DirectPortLease{}, errors.New("direct Browser Agent lease is unavailable; upload again to obtain a new dedicated port")
	}
	var lease model.DirectPortLease
	if err := json.Unmarshal(data, &lease); err != nil {
		return lease, err
	}
	if !time.Now().UTC().Before(lease.ExpiresAt) {
		_ = s.deleteDirectLease(projectID)
		return lease, errors.New("direct Browser Agent lease expired; upload again to obtain a new dedicated port")
	}
	return lease, nil
}

func (s *Service) persistDirectUpload(ctx context.Context, projectID, orgID string, lease model.DirectPortLease, receipt model.DirectPackageReceipt) error {
	return s.updateDesktopCloudState(ctx, projectID, func(state *orchestrator.CascadeState) {
		if state.DesktopCloudRun == nil {
			state.DesktopCloudRun = &orchestrator.DesktopCloudRunState{SchemaVersion: desktopCloudRunSchemaVersion}
		}
		run := state.DesktopCloudRun
		run.Transport = directTransportStateName
		run.OrgID = orgID
		run.LeaseID = lease.LeaseID
		run.DataPort = lease.DataPort
		expires := lease.ExpiresAt
		run.LeaseExpiresAt = &expires
		run.ExchangePackageID = receipt.PackageID
		run.PackageID = receipt.PackageID
		run.CloudJobID = receipt.JobID
		run.Status = receipt.Status
		run.Stage = receipt.Stage
		run.Message = "执行包已通过短期加密会话上传至 Browser Agent。"
		run.ProgressPercent = 5
		run.WaitingReason = ""
		run.BlockingErrorCode = ""
		run.NextAction = ""
		run.RequiresReapproval = false
		run.ReunderstandingIssues = nil
		run.ResultPackageID = ""
		run.ResultPackage = nil
		run.ResultDownloaded = false
		run.AckedAt = nil
		run.DownloadedAssets = nil
		run.DirectArtifacts = nil
		run.ResultReview = nil
		state.ErrorMessage = ""
	})
}

func (s *Service) persistDirectLeaseRelease(ctx context.Context, projectID string) error {
	return s.updateDesktopCloudRun(ctx, projectID, func(run *orchestrator.DesktopCloudRunState) {
		run.LeaseID = ""
		run.DataPort = 0
		run.LeaseExpiresAt = nil
		run.Message = "直连租约已释放；终态结果和已校验素材仍保留。"
		run.Stage = "lease_released"
	})
}

func (s *Service) persistDirectStatus(ctx context.Context, projectID string, status model.DirectJobStatus) error {
	return s.updateDesktopCloudState(ctx, projectID, func(state *orchestrator.CascadeState) {
		if state.DesktopCloudRun == nil {
			state.DesktopCloudRun = &orchestrator.DesktopCloudRunState{SchemaVersion: desktopCloudRunSchemaVersion}
		}
		run := state.DesktopCloudRun
		wasReunderstanding := run.BlockingErrorCode == "reunderstanding_required"
		run.Transport = directTransportStateName
		run.ExchangePackageID = status.PackageID
		run.PackageID = status.PackageID
		run.CloudJobID = status.JobID
		run.Status = status.Status
		run.Stage = status.Stage
		run.Message = status.Message
		run.WaitingReason = status.WaitingReason
		run.BlockingErrorCode = status.BlockingErrorCode
		run.NextAction = status.NextAction
		run.RequiresReapproval = status.RequiresReapproval
		run.ProgressPercent = status.ProgressPercent
		run.ResultPackageID = status.ResultPackageID
		run.DirectArtifacts = append([]model.DirectArtifact(nil), status.Artifacts...)
		run.ReunderstandingIssues = append([]model.DirectReunderstandingIssue(nil), status.ReunderstandingIssues...)
		if status.BlockingErrorCode == "reunderstanding_required" {
			if !wasReunderstanding {
				state.ExecutionPackageGeneration++
			}
			state.Approved = false
			state.CurrentNode = orchestrator.NodeHumanApprove
			state.Status = orchestrator.FlowStatusAwaitingHuman
			state.ErrorMessage = "Browser Agent 运行事实要求重新理解并重新审批执行方案。"
			s.invalidateApprovedBuildsForProject(projectID)
		}
	})
}

func (s *Service) invalidateApprovedBuildsForProject(projectID string) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return
	}
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	for key := range s.approvedBuilds {
		if strings.HasPrefix(key, projectID+"|") {
			delete(s.approvedBuilds, key)
		}
	}
}

func (s *Service) effectiveDirectTransportURL() string {
	s.directTransportMu.RLock()
	value := strings.TrimRight(strings.TrimSpace(s.directTransportURL), "/")
	s.directTransportMu.RUnlock()
	if value != "" {
		return value
	}
	data, err := os.ReadFile(filepath.Join(s.runtime.DataRoot, directTransportSettingsFileName))
	if err != nil {
		return ""
	}
	var settings directTransportSettingsFile
	if json.Unmarshal(data, &settings) != nil {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(settings.ControlURL), "/")
}
func directTransportViewForURL(raw string) DirectTransportRuntimeView {
	view := DirectTransportRuntimeView{Configured: raw != "", Transport: directTransportStateName}
	if parsed, err := url.Parse(raw); err == nil {
		view.ControlURLHost = parsed.Host
		view.ControlURLPath = parsed.Path
	}
	return view
}
func directLeaseView(lease model.DirectPortLease) DirectTransportLeaseView {
	parsed, _ := url.Parse(lease.DataURL)
	return DirectTransportLeaseView{LeaseID: lease.LeaseID, DataURLHost: parsed.Hostname(), DataPort: lease.DataPort, IssuedAt: lease.IssuedAt, ExpiresAt: lease.ExpiresAt, CryptoSuite: lease.CryptoSuite}
}
func validateDirectTransportControlURL(raw string) (string, error) {
	value := strings.TrimRight(strings.TrimSpace(raw), "/")
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("direct Browser Agent control URL is invalid")
	}
	loopback := parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost"
	if parsed.Scheme != "https" && !(loopback && parsed.Scheme == "http") {
		return "", errors.New("direct Browser Agent requires HTTPS except for loopback development")
	}
	return value, nil
}
func validateDirectDataURL(controlURL string, lease model.DirectPortLease) error {
	parsed, err := url.Parse(lease.DataURL)
	if err != nil || parsed.Hostname() == "" || parsed.Port() != strconv.Itoa(lease.DataPort) || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("direct Browser Agent data URL is invalid")
	}
	control, _ := url.Parse(controlURL)
	if !strings.EqualFold(parsed.Hostname(), control.Hostname()) {
		return errors.New("direct Browser Agent data host does not match the configured control host")
	}
	loopback := parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost"
	if parsed.Scheme != "https" && !(loopback && parsed.Scheme == "http") {
		return errors.New("direct Browser Agent data URL requires HTTPS")
	}
	if lease.ProtocolVersion != model.DirectTransportProtocolVersion || lease.CryptoSuite != model.DirectTransportCryptoSuite || lease.LeaseToken == "" || !time.Now().UTC().Before(lease.ExpiresAt) {
		return errors.New("direct Browser Agent lease is invalid or expired")
	}
	return nil
}
func readDirectHTTPError(response *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(data, &body)
	return &directTransportHTTPError{StatusCode: response.StatusCode, Code: body.Error.Code, Message: body.Error.Message}
}
func atomicWritePrivateFile(path string, data []byte) error {
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(path)
		if retry := os.Rename(temporary, path); retry != nil {
			_ = os.Remove(temporary)
			return retry
		}
	}
	return nil
}
