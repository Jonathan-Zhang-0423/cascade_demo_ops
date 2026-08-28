package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"cascade-demoops/backend/internal/direct"
	"cascade-demoops/backend/internal/model"
)

// DirectHTTPServer exposes the App↔Gateway Direct API v1. It is deliberately
// separate from the legacy Exchange handler so production callers cannot
// silently fall back between the two protocols.
type DirectHTTPServer struct {
	service        *Service
	gateway        *direct.Gateway
	bootstrapToken string
	workerToken    string
	workerHandler  http.Handler
	// dataPort is set on handlers mounted on a concrete lease port. It is
	// server-owned and is never derived from the client Host header.
	dataPort       int
	workerMu       sync.RWMutex
	workerMode     string
	workerLastSeen time.Time
	workerRuns     *directWorkerRunRegistry
}

type directWorkerRunRegistry struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

const directWorkerHeartbeatStaleAfter = 5 * time.Second

func NewDirectHTTPServer(service *Service, baseHost, bootstrapToken, workerToken string) *DirectHTTPServer {
	return NewDirectHTTPServerWithGateway(service, direct.NewGateway(baseHost, 40*time.Minute), bootstrapToken, workerToken)
}

func NewDirectHTTPServerWithGateway(service *Service, gateway *direct.Gateway, bootstrapToken, workerToken string) *DirectHTTPServer {
	if gateway == nil {
		gateway = direct.NewGateway("", 40*time.Minute)
	}
	s := &DirectHTTPServer{service: service, gateway: gateway, bootstrapToken: bootstrapToken, workerToken: workerToken,
		workerRuns: &directWorkerRunRegistry{cancels: map[string]context.CancelFunc{}}}
	s.workerHandler = s.workerAPI()
	return s
}

func (s *DirectHTTPServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/direct/health", s.handleDirectHealth)
	mux.HandleFunc("POST /v1/direct/leases", s.handleDirectLease)
	mux.HandleFunc("POST /v1/direct/leases/release", s.handleDirectLeaseRelease)
	mux.HandleFunc("POST /v1/direct/packages", s.handleDirectPackage)
	mux.HandleFunc("POST /v1/direct/jobs/{job_id}/credentials", s.handleDirectCredentials)
	mux.HandleFunc("GET /v1/direct/jobs/{job_id}", s.handleDirectJob)
	mux.HandleFunc("POST /v1/direct/jobs/{job_id}/cancel", s.handleDirectJobCancel)
	mux.HandleFunc("GET /v1/direct/jobs/{job_id}/package", s.handleDirectSourcePackage)
	mux.HandleFunc("GET /v1/direct/jobs/{job_id}/result", s.handleDirectResult)
	mux.HandleFunc("POST /v1/direct/jobs/{job_id}/ack", s.handleDirectAck)
	mux.HandleFunc("GET /v1/direct/jobs/{job_id}/artifacts/{artifact_id}", s.handleDirectArtifactWhole)
	mux.HandleFunc("GET /v1/direct/jobs/{job_id}/artifacts/{artifact_id}/chunks/{chunk_index}", s.handleDirectArtifactChunk)
	mux.Handle("/v1/worker/", s.workerHandler)
	return mux
}

func (s *DirectHTTPServer) ControlHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/direct/health", s.handleDirectHealth)
	mux.HandleFunc("POST /v1/direct/leases", s.handleDirectLease)
	mux.HandleFunc("POST /v1/direct/leases/release", s.handleDirectLeaseRelease)
	return mux
}

func (s *DirectHTTPServer) DataHandler() http.Handler {
	return s.dataHandler(0)
}

// DataHandlerForPort returns a data-plane handler bound to the actual
// listener port. Production listeners must use this method so a spoofed Host
// header cannot select another lease.
func (s *DirectHTTPServer) DataHandlerForPort(port int) http.Handler {
	return s.dataHandler(port)
}

func (s *DirectHTTPServer) dataHandler(port int) http.Handler {
	bound := *s
	bound.dataPort = port
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/direct/packages", bound.handleDirectPackage)
	mux.HandleFunc("POST /v1/direct/jobs/{job_id}/credentials", bound.handleDirectCredentials)
	mux.HandleFunc("GET /v1/direct/jobs/{job_id}", bound.handleDirectJob)
	mux.HandleFunc("POST /v1/direct/jobs/{job_id}/cancel", bound.handleDirectJobCancel)
	mux.HandleFunc("GET /v1/direct/jobs/{job_id}/package", bound.handleDirectSourcePackage)
	mux.HandleFunc("GET /v1/direct/jobs/{job_id}/result", bound.handleDirectResult)
	mux.HandleFunc("POST /v1/direct/jobs/{job_id}/ack", bound.handleDirectAck)
	mux.HandleFunc("GET /v1/direct/jobs/{job_id}/artifacts/{artifact_id}", bound.handleDirectArtifactWhole)
	mux.HandleFunc("GET /v1/direct/jobs/{job_id}/artifacts/{artifact_id}/chunks/{chunk_index}", bound.handleDirectArtifactChunk)
	return mux
}

func (s *DirectHTTPServer) WorkerHandler() http.Handler { return s.workerHandler }

func (s *DirectHTTPServer) requireBootstrap(r *http.Request) bool {
	return s != nil && s.bootstrapToken != "" && strings.TrimSpace(r.Header.Get("Authorization")) == "Bearer "+s.bootstrapToken
}

func (s *DirectHTTPServer) handleDirectHealth(w http.ResponseWriter, r *http.Request) {
	if !s.requireBootstrap(r) {
		s.directError(w, http.StatusUnauthorized, "unauthorized", "bootstrap token is required")
		return
	}
	// server_time must use the RFC3339 time representation expected by the App
	// client; a Unix millisecond number makes json.Decoder reject the complete
	// health response and leaves an otherwise healthy Direct Gateway unreachable.
	// worker_readiness remains a Gateway observability extension outside the
	// shared App protocol type.
	s.directJSON(w, http.StatusOK, map[string]any{
		"protocol_version":                          direct.ProtocolVersion,
		"crypto_suite":                              direct.CryptoSuite,
		"supported_protocol_versions":               []string{direct.ProtocolVersion},
		"supported_package_schema_versions":         []string{model.ClientExecutionPackageSchemaVersion},
		"supported_runtimes":                        []string{model.ExecutableScriptRuntimeBrowserAgentOutlineV1},
		"supported_worker_protocol_versions":        []string{direct.WorkerProtocolVersion},
		"supported_outcome_verifier_rules_versions": []string{direct.OutcomeVerifierRulesVersion},
		"capabilities": map[string]bool{
			"manual_login_checkpoint":         false,
			"gateway_state_persistence":       s.gateway.PersistenceEnabled(),
			"credential_envelope_persistence": false,
		},
		"active_leases":    s.gateway.ActiveLeaseCount(),
		"server_time":      time.Now().UTC(),
		"worker_readiness": s.workerReadiness(),
	})
}

func (s *DirectHTTPServer) handleDirectLease(w http.ResponseWriter, r *http.Request) {
	if !s.requireBootstrap(r) {
		s.directError(w, http.StatusUnauthorized, "unauthorized", "bootstrap token is required")
		return
	}
	var request direct.DirectLeaseRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&request); err != nil {
		s.directError(w, http.StatusBadRequest, "invalid_request", "invalid lease request")
		return
	}
	lease, err := s.gateway.Allocate(request)
	if err != nil {
		s.directError(w, directStatus(err), directCode(err), err.Error())
		return
	}
	// The Gateway keeps compact Unix-millisecond timestamps internally, while
	// Direct API v1 uses RFC3339 timestamps on the App-facing wire contract.
	s.directJSON(w, http.StatusCreated, directLeaseWireView(lease))
}

func (s *DirectHTTPServer) handleDirectLeaseRelease(w http.ResponseWriter, r *http.Request) {
	if !s.requireBootstrap(r) {
		s.directError(w, http.StatusUnauthorized, "unauthorized", "bootstrap token is required")
		return
	}
	var request direct.DirectLeaseRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&request); err != nil {
		s.directError(w, http.StatusBadRequest, "invalid_request", "invalid lease request")
		return
	}
	if err := s.gateway.Release(request); err != nil {
		s.directError(w, directStatus(err), directCode(err), err.Error())
		return
	}
	s.directJSON(w, http.StatusOK, map[string]any{"released": true})
}

func (s *DirectHTTPServer) directMessage(r *http.Request) (direct.DirectEncryptedMessage, []byte, *direct.Lease, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 16*1024*1024))
	if err != nil {
		return direct.DirectEncryptedMessage{}, nil, nil, err
	}
	var message direct.DirectEncryptedMessage
	if err := json.Unmarshal(body, &message); err != nil {
		return message, nil, nil, errors.New("invalid encrypted message")
	}
	lease, err := s.gateway.Lease(message.LeaseID)
	if err != nil {
		return message, nil, nil, err
	}
	if s.dataPort != 0 && lease.DataPort != s.dataPort {
		return message, nil, nil, errors.New("direct request used a data port not assigned to this lease")
	}
	// DataHandler() is retained for in-process tests and diagnostics. It may
	// inspect Host only in that non-production unbound mode.
	if s.dataPort == 0 {
		if localPort := requestLocalPort(r); localPort != 0 && localPort != lease.DataPort {
			return message, nil, nil, errors.New("direct request used a data port not assigned to this lease")
		}
	}
	if err := direct.VerifyDataRequest(r, body, lease.LeaseToken, lease.InstallationID, lease.LeaseID, lease.DataPort, time.Now(), s.gateway.Nonces()); err != nil {
		return message, nil, nil, err
	}
	if err := direct.ValidateMessageFreshness(message, time.Now(), s.gateway.Nonces()); err != nil {
		return message, nil, nil, err
	}
	plaintext, err := message.Decrypt(lease.LeaseToken, lease.InstallationID, lease.DataPort)
	return message, plaintext, lease, err
}

func (s *DirectHTTPServer) directReadRequest(r *http.Request) (*direct.Lease, error) {
	port := s.dataPort
	if port == 0 {
		port = requestLocalPort(r)
	}
	if port < direct.DataPortMin || port > direct.DataPortMax {
		return nil, errors.New("direct read request did not use a lease data port")
	}
	lease, err := s.gateway.LeaseByPort(port)
	if err != nil {
		return nil, err
	}
	if err := direct.VerifyDataRequest(r, nil, lease.LeaseToken, lease.InstallationID, lease.LeaseID, lease.DataPort, time.Now(), s.gateway.Nonces()); err != nil {
		return nil, err
	}
	return lease, nil
}

func (s *DirectHTTPServer) encryptedResponse(w http.ResponseWriter, lease *direct.Lease, messageType, messageID string, value any) {
	plain, err := json.Marshal(value)
	if err != nil {
		s.directError(w, http.StatusInternalServerError, "encode_failed", "failed to encode response")
		return
	}
	message, err := direct.EncryptMessage(lease.LeaseToken, lease.InstallationID, lease.LeaseID, lease.DataPort, messageID, messageType, "browser_agent_to_app", plain, time.Now())
	if err != nil {
		s.directError(w, http.StatusInternalServerError, "encrypt_failed", "failed to encrypt response")
		return
	}
	s.directJSON(w, http.StatusOK, message)
}

func (s *DirectHTTPServer) handleDirectPackage(w http.ResponseWriter, r *http.Request) {
	message, plain, lease, err := s.directMessage(r)
	if err != nil {
		s.directError(w, directStatus(err), directCode(err), err.Error())
		return
	}
	if message.Direction != "app_to_browser_agent" || message.MessageType != "client_execution_package" {
		s.directError(w, http.StatusBadRequest, "invalid_message_type", "execution package message metadata is invalid")
		return
	}
	var pkg model.ClientExecutionPackage
	if err := json.Unmarshal(plain, &pkg); err != nil {
		s.directError(w, http.StatusBadRequest, "invalid_package", "invalid client execution package")
		return
	}
	if pkg.ExecutableScriptBundle == nil || pkg.ExecutableScriptBundle.ScriptManifest.Runtime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 {
		s.directError(w, http.StatusBadRequest, "unsupported_runtime", "only browser-agent-outline-v1 is accepted")
		return
	}
	if err := model.ValidateClientExecutionPackageForDirectInstallation(&pkg, lease.InstallationID); err != nil {
		s.directError(w, http.StatusBadRequest, model.DirectPackageValidationCode(err), err.Error())
		return
	}
	if err := validateDirectPackageCredentialRefs(&pkg); err != nil {
		s.directError(w, http.StatusBadRequest, "credential_grant_mismatch", err.Error())
		return
	}
	receipt, err := s.gateway.CreateJobWithSourceDigestAndCredentialRequirement(lease.LeaseID, lease.InstallationID, pkg.PackageID, direct.HashSHA256(plain), pkg.Reproducibility.PackageHashSHA256, plain, len(pkg.CredentialGrants) > 0)
	if err != nil {
		s.directError(w, directStatus(err), directCode(err), err.Error())
		return
	}
	s.encryptedResponse(w, lease, "package_receipt", message.MessageID, directPackageReceiptWireView(receipt, pkg.Reproducibility.PackageHashSHA256))
}

func (s *DirectHTTPServer) handleDirectCredentials(w http.ResponseWriter, r *http.Request) {
	message, plain, lease, err := s.directMessage(r)
	if err != nil {
		s.directError(w, directStatus(err), directCode(err), err.Error())
		return
	}
	if message.Direction != "app_to_browser_agent" || message.MessageType != "credential_envelope" {
		s.directError(w, http.StatusBadRequest, "invalid_message_type", "credential envelope message metadata is invalid")
		return
	}
	var envelope model.DirectCredentialEnvelope
	if err := json.Unmarshal(plain, &envelope); err != nil {
		s.directError(w, http.StatusBadRequest, "invalid_credential_envelope", "invalid credential envelope")
		return
	}
	job, err := s.gateway.WorkerJob(r.PathValue("job_id"))
	if err != nil {
		s.directError(w, directStatus(err), directCode(err), err.Error())
		return
	}
	internalEnvelope, err := directCredentialEnvelopeFromWire(envelope, job)
	if err != nil {
		s.directError(w, http.StatusBadRequest, "credential_scope_mismatch", err.Error())
		return
	}
	if err := validateDirectCredentialEnvelopeAgainstPackage(job, internalEnvelope); err != nil {
		s.directError(w, http.StatusBadRequest, "credential_scope_mismatch", err.Error())
		return
	}
	if err := s.gateway.StoreCredential(r.PathValue("job_id"), lease.LeaseID, internalEnvelope); err != nil {
		s.directError(w, directStatus(err), directCode(err), err.Error())
		return
	}
	s.encryptedResponse(w, lease, "credential_receipt", message.MessageID, model.DirectCredentialReceipt{
		ProtocolVersion: model.DirectTransportProtocolVersion, JobID: envelope.JobID, PackageID: envelope.PackageID,
		GrantID: envelope.GrantID, SecretRef: envelope.SecretRef, Status: "queued", Stage: "package_received", AcceptedAt: time.Now().UTC(),
	})
}

func directLeaseWireView(lease direct.DirectPortLease) model.DirectPortLease {
	return model.DirectPortLease{ProtocolVersion: model.DirectTransportProtocolVersion, LeaseID: lease.LeaseID, InstallationID: lease.InstallationID,
		DataURL: lease.DataURL, DataPort: lease.DataPort, LeaseToken: lease.LeaseToken, IssuedAt: time.UnixMilli(lease.IssuedAt).UTC(),
		ExpiresAt: time.UnixMilli(lease.ExpiresAt).UTC(), CryptoSuite: lease.CryptoSuite, ServerTime: time.UnixMilli(lease.ServerTime).UTC()}
}

func directPackageReceiptWireView(receipt direct.DirectPackageReceipt, approvedDigest string) model.DirectPackageReceipt {
	return model.DirectPackageReceipt{ProtocolVersion: model.DirectTransportProtocolVersion, JobID: receipt.JobID, PackageID: receipt.PackageID,
		PackageDigest: approvedDigest, Status: receipt.Status, Stage: receipt.Stage, AcceptedAt: time.UnixMilli(receipt.CreatedAtUnixMS).UTC()}
}

func directCredentialEnvelopeFromWire(envelope model.DirectCredentialEnvelope, job *direct.Job) (direct.DirectCredentialEnvelope, error) {
	if job == nil || envelope.PackageDigest != job.SourcePackageDigest {
		return direct.DirectCredentialEnvelope{}, errors.New("credential package_digest does not match the approved package")
	}
	if envelope.Credential.SecretRef != "" && envelope.Credential.SecretRef != envelope.SecretRef {
		return direct.DirectCredentialEnvelope{}, errors.New("credential value secret_ref does not match envelope")
	}
	if envelope.Credential.ExpiresAt.IsZero() || !envelope.Credential.ExpiresAt.Equal(envelope.ExpiresAt) {
		return direct.DirectCredentialEnvelope{}, errors.New("credential expiry does not match envelope")
	}
	return direct.DirectCredentialEnvelope{JobID: envelope.JobID, PackageID: envelope.PackageID, PackageSHA256: job.PackageSHA256,
		GrantID: envelope.GrantID, SecretRef: envelope.SecretRef, InstallationID: envelope.InstallationID, LeaseID: envelope.LeaseID,
		AllowedDomains: append([]string(nil), envelope.AllowedDomains...), AllowedOperations: append([]string(nil), envelope.AllowedOperations...),
		ExpiresAtUnixMS: envelope.ExpiresAt.UnixMilli(), Secret: envelope.Credential.Password}, nil
}

func validateDirectCredentialEnvelopeAgainstPackage(job *direct.Job, envelope direct.DirectCredentialEnvelope) error {
	if job == nil || len(job.PackageJSON) == 0 {
		return errors.New("direct credential package binding is unavailable")
	}
	var pkg model.ClientExecutionPackage
	if err := json.Unmarshal(job.PackageJSON, &pkg); err != nil {
		return errors.New("direct credential package binding is invalid")
	}
	var grant *model.CredentialGrant
	for index := range pkg.CredentialGrants {
		if pkg.CredentialGrants[index].GrantID == envelope.GrantID {
			grant = &pkg.CredentialGrants[index]
			break
		}
	}
	if grant == nil {
		return errors.New("direct credential grant_id was not approved by the App package")
	}
	secretRef := strings.TrimSpace(envelope.SecretRef)
	if secretRef == "" || (secretRef != strings.TrimSpace(grant.CloudSecretRef) && secretRef != strings.TrimSpace(grant.EncryptedSecretAttachmentID)) {
		return errors.New("direct credential secret_ref does not match the approved grant")
	}
	if !directStringScopeAllowed(envelope.AllowedDomains, grant.AllowedDomains) || !directStringScopeAllowed(envelope.AllowedDomains, pkg.RecordingRunSpec.AllowedDomains) {
		return errors.New("direct credential domains exceed the approved package scope")
	}
	if !directStringScopeAllowed(envelope.AllowedOperations, grant.AllowedOperations) {
		return errors.New("direct credential operations exceed the approved grant scope")
	}
	if !grant.ExpiresAt.IsZero() && time.UnixMilli(envelope.ExpiresAtUnixMS).After(grant.ExpiresAt) {
		return errors.New("direct credential expiry exceeds the approved grant expiry")
	}
	return nil
}

func directStringScopeAllowed(values, allowed []string) bool {
	allowedSet := make(map[string]bool, len(allowed))
	for _, value := range allowed {
		if normalized := strings.ToLower(strings.TrimSpace(value)); normalized != "" {
			allowedSet[normalized] = true
		}
	}
	if len(values) == 0 || len(allowedSet) == 0 {
		return false
	}
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "" || !allowedSet[normalized] {
			return false
		}
	}
	return true
}

func (s *DirectHTTPServer) handleDirectJob(w http.ResponseWriter, r *http.Request) {
	s.handleDirectJobMessage(w, r, false)
}

func (s *DirectHTTPServer) handleDirectJobCancel(w http.ResponseWriter, r *http.Request) {
	message, plain, lease, err := s.directMessage(r)
	if err != nil {
		s.directError(w, directStatus(err), directCode(err), err.Error())
		return
	}
	if message.Direction != model.DirectTransportDirectionUpload || message.MessageType != "job_cancel" {
		s.directError(w, http.StatusBadRequest, "invalid_message_type", "job cancel message metadata is invalid")
		return
	}
	var request model.DirectJobCancelRequest
	if err := json.Unmarshal(plain, &request); err != nil {
		s.directError(w, http.StatusBadRequest, "invalid_cancel", "invalid job cancel request")
		return
	}
	jobID := r.PathValue("job_id")
	if request.ProtocolVersion != model.DirectTransportProtocolVersion || request.InstallationID != lease.InstallationID || request.JobID != jobID {
		s.directError(w, http.StatusConflict, "cancel_binding_invalid", "job cancel does not match the active lease")
		return
	}
	status, err := s.gateway.CancelJob(jobID, lease.LeaseID)
	if err != nil {
		s.directError(w, directStatus(err), directCode(err), err.Error())
		return
	}
	s.cancelDirectWorkerRun(jobID)
	canceledAt := time.UnixMilli(status.UpdatedAtUnixMS).UTC()
	s.encryptedResponse(w, lease, "job_cancel_receipt", message.MessageID, model.DirectJobCancelReceipt{
		ProtocolVersion: model.DirectTransportProtocolVersion,
		JobID:           jobID,
		Status:          status.Status,
		Stage:           status.Stage,
		CanceledAt:      canceledAt,
	})
}
func (s *DirectHTTPServer) handleDirectResult(w http.ResponseWriter, r *http.Request) {
	s.handleDirectJobMessage(w, r, true)
}

func (s *DirectHTTPServer) handleDirectSourcePackage(w http.ResponseWriter, r *http.Request) {
	lease, err := s.directReadRequest(r)
	if err != nil {
		s.directError(w, directStatus(err), directCode(err), err.Error())
		return
	}
	jobID := r.PathValue("job_id")
	job, err := s.gateway.JobForInstallation(jobID, lease.InstallationID)
	if err != nil {
		s.directError(w, directStatus(err), directCode(err), err.Error())
		return
	}
	if len(job.PackageJSON) == 0 {
		s.directError(w, http.StatusConflict, "source_package_unavailable", "source execution package is unavailable")
		return
	}
	s.encryptedResponse(w, lease, "source_execution_package", "source_package_"+jobID, json.RawMessage(job.PackageJSON))
}

func (s *DirectHTTPServer) handleDirectAck(w http.ResponseWriter, r *http.Request) {
	message, plain, lease, err := s.directMessage(r)
	if err != nil {
		s.directError(w, directStatus(err), directCode(err), err.Error())
		return
	}
	if message.Direction != "app_to_browser_agent" || message.MessageType != "result_ack" {
		s.directError(w, http.StatusBadRequest, "invalid_message_type", "result ack message metadata is invalid")
		return
	}
	var request model.DirectResultAckRequest
	if err := json.Unmarshal(plain, &request); err != nil {
		s.directError(w, http.StatusBadRequest, "invalid_ack", "invalid result ack")
		return
	}
	if request.ProtocolVersion != model.DirectTransportProtocolVersion || request.InstallationID != lease.InstallationID {
		s.directError(w, http.StatusConflict, "ack_binding_invalid", "result ack does not match the active lease")
		return
	}
	receipt, err := s.gateway.AckResult(request.JobID, lease.LeaseID, direct.DirectResultAckRequest{
		ProtocolVersion: request.ProtocolVersion, InstallationID: request.InstallationID, JobID: request.JobID,
		ResultPackageID: request.ResultPackageID, ReceivedArtifactIDs: request.ReceivedArtifactIDs,
		VerifiedChecksums: request.VerifiedChecksums, AckedAt: request.AckedAt,
	})
	if err != nil {
		code := "ack_binding_invalid"
		if strings.Contains(err.Error(), "incomplete") {
			code = "ack_artifacts_incomplete"
		} else if strings.Contains(err.Error(), "completed") {
			code = "result_ack_required"
		}
		s.directError(w, http.StatusConflict, code, err.Error())
		return
	}
	s.encryptedResponse(w, lease, "result_ack_receipt", message.MessageID, model.DirectResultAckReceipt{
		ProtocolVersion: receipt.ProtocolVersion, JobID: receipt.JobID, ResultPackageID: receipt.ResultPackageID,
		ReceivedArtifactIDs: receipt.ReceivedArtifactIDs, VerifiedChecksums: receipt.VerifiedChecksums, AckedAt: receipt.AckedAt,
	})
}

func (s *DirectHTTPServer) handleDirectArtifactWhole(w http.ResponseWriter, r *http.Request) {
	if _, err := s.directReadRequest(r); err != nil {
		s.directError(w, directStatus(err), directCode(err), err.Error())
		return
	}
	s.directError(w, http.StatusGone, "artifact_chunking_required", "whole-artifact download is disabled; use artifact chunks")
}
func (s *DirectHTTPServer) handleDirectJobMessage(w http.ResponseWriter, r *http.Request, result bool) {
	lease, err := s.directReadRequest(r)
	if err != nil {
		s.directError(w, directStatus(err), directCode(err), err.Error())
		return
	}
	jobID := r.PathValue("job_id")
	job, err := s.gateway.JobForInstallation(jobID, lease.InstallationID)
	if err != nil {
		s.directError(w, directStatus(err), directCode(err), err.Error())
		return
	}
	if result {
		if len(job.ResultJSON) == 0 {
			s.directError(w, http.StatusConflict, "result_not_ready", "recording result is not ready")
			return
		}
		s.encryptedResponse(w, lease, "recording_result", "result_"+jobID, json.RawMessage(job.ResultJSON))
		return
	}
	s.encryptedResponse(w, lease, "job_status", "status_"+jobID, directJobStatusWireView(job.Status, job))
}

func directJobStatusWireView(status direct.DirectJobStatus, job *direct.Job) model.DirectJobStatus {
	artifacts := make([]model.DirectArtifact, 0, len(job.Artifacts))
	for _, artifact := range job.Artifacts {
		descriptor := artifact.Descriptor
		artifacts = append(artifacts, model.DirectArtifact{ArtifactID: descriptor.ArtifactID, Kind: descriptor.Kind, MimeType: descriptor.MimeType, SHA256: descriptor.SHA256, SizeBytes: descriptor.Size})
	}
	return model.DirectJobStatus{ProtocolVersion: model.DirectTransportProtocolVersion, JobID: status.JobID, PackageID: status.PackageID,
		Status: status.Status, Stage: status.Stage, ProgressPercent: status.Progress, ResultPackageID: status.ResultPackageID,
		Artifacts: artifacts, UpdatedAt: time.UnixMilli(status.UpdatedAtUnixMS).UTC()}
}

func (s *DirectHTTPServer) handleDirectArtifactChunk(w http.ResponseWriter, r *http.Request) {
	lease, err := s.directReadRequest(r)
	if err != nil {
		s.directError(w, directStatus(err), directCode(err), err.Error())
		return
	}
	index, err := strconv.Atoi(r.PathValue("chunk_index"))
	if err != nil {
		s.directError(w, http.StatusBadRequest, "invalid_chunk_index", "invalid chunk index")
		return
	}
	chunk, descriptor, err := s.gateway.ArtifactChunk(r.PathValue("job_id"), lease.LeaseID, r.PathValue("artifact_id"), index)
	if err != nil {
		s.directError(w, directStatus(err), directCode(err), err.Error())
		return
	}
	s.encryptedResponse(w, lease, fmt.Sprintf("artifact_chunk:%s:%d", descriptor.ArtifactID, index), fmt.Sprintf("chunk_%s_%d", descriptor.ArtifactID, index), map[string]any{"descriptor": descriptor, "chunk_index": index, "bytes_base64": base64.StdEncoding.EncodeToString(chunk)})
}

func (s *DirectHTTPServer) workerAPI() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/worker/jobs/queued", s.handleWorkerQueued)
	mux.HandleFunc("GET /v1/worker/readiness", s.handleWorkerReadiness)
	mux.HandleFunc("POST /v1/worker/heartbeat", s.handleWorkerHeartbeat)
	mux.HandleFunc("POST /v1/worker/jobs/{job_id}/claim", s.handleWorkerClaim)
	mux.HandleFunc("POST /v1/worker/jobs/claim", s.handleWorkerClaimNext)
	mux.HandleFunc("POST /v1/worker/jobs/{job_id}/run", s.handleWorkerRun)
	mux.HandleFunc("POST /v1/worker/jobs/{job_id}/credentials/consume", s.handleWorkerCredential)
	mux.HandleFunc("POST /v1/worker/jobs/{job_id}/progress", s.handleWorkerProgress)
	mux.HandleFunc("PUT /v1/worker/jobs/{job_id}/status", s.handleWorkerProgress)
	mux.HandleFunc("POST /v1/worker/jobs/{job_id}/artifacts", s.handleWorkerArtifact)
	mux.HandleFunc("PUT /v1/worker/jobs/{job_id}/artifacts/{artifact_id}", s.handleWorkerArtifact)
	mux.HandleFunc("POST /v1/worker/jobs/{job_id}/result", s.handleWorkerResult)
	mux.HandleFunc("PUT /v1/worker/jobs/{job_id}/result", s.handleWorkerResult)
	mux.HandleFunc("POST /v1/worker/jobs/{job_id}/release", s.handleWorkerRelease)
	mux.HandleFunc("GET /v1/worker/jobs/{job_id}/control", s.handleWorkerControl)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackRequest(r) {
			s.directError(w, http.StatusForbidden, "loopback_required", "worker API requires loopback")
			return
		}
		if s.workerToken == "" || r.Header.Get("Authorization") != "Bearer "+s.workerToken {
			s.directError(w, http.StatusUnauthorized, "unauthorized", "worker token is required")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *DirectHTTPServer) handleWorkerControl(w http.ResponseWriter, r *http.Request) {
	job, err := s.gateway.WorkerJob(r.PathValue("job_id"))
	if err != nil {
		s.directError(w, directStatus(err), directCode(err), err.Error())
		return
	}
	s.directJSON(w, http.StatusOK, map[string]any{
		"protocol_version": direct.WorkerProtocolVersion,
		"job_id":           job.Status.JobID,
		"status":           job.Status.Status,
		"cancel_requested": job.Status.Status == "canceled",
	})
}

func (s *DirectHTTPServer) handleWorkerQueued(w http.ResponseWriter, r *http.Request) {
	ids := s.gateway.QueuedJobIDs()
	s.directJSON(w, http.StatusOK, map[string]any{"job_ids": ids})
}

func (s *DirectHTTPServer) handleWorkerReadiness(w http.ResponseWriter, r *http.Request) {
	s.directJSON(w, http.StatusOK, s.workerReadiness())
}

func (s *DirectHTTPServer) handleWorkerHeartbeat(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Mode            string `json:"mode"`
		ProtocolVersion string `json:"protocol_version"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&request); err != nil && !errors.Is(err, io.EOF) {
			s.directError(w, http.StatusBadRequest, "invalid_request", "invalid worker heartbeat")
			return
		}
	}
	mode := strings.TrimSpace(request.Mode)
	if mode == "" {
		mode = "external"
	}
	if mode != "external" && mode != "embedded" {
		s.directError(w, http.StatusBadRequest, "invalid_worker_mode", "worker mode must be external or embedded")
		return
	}
	if request.ProtocolVersion != "" && request.ProtocolVersion != direct.WorkerProtocolVersion {
		s.directError(w, http.StatusConflict, "protocol_mismatch", "worker protocol version is not supported")
		return
	}
	s.markWorkerHeartbeat(mode)
	s.directJSON(w, http.StatusOK, map[string]any{"accepted": true, "worker_readiness": s.workerReadiness()})
}

func (s *DirectHTTPServer) markWorkerHeartbeat(mode string) {
	if s == nil {
		return
	}
	s.workerMu.Lock()
	s.workerMode = mode
	s.workerLastSeen = time.Now().UTC()
	s.workerMu.Unlock()
}

func (s *DirectHTTPServer) workerReadiness() map[string]any {
	if s == nil {
		return map[string]any{"ready": false, "mode": "unavailable"}
	}
	s.workerMu.RLock()
	mode := s.workerMode
	lastSeen := s.workerLastSeen
	s.workerMu.RUnlock()
	now := time.Now().UTC()
	ready := !lastSeen.IsZero() && now.Sub(lastSeen) <= directWorkerHeartbeatStaleAfter
	result := map[string]any{
		"ready":             ready,
		"mode":              mode,
		"stale_after_ms":    directWorkerHeartbeatStaleAfter.Milliseconds(),
		"queued_job_count":  len(s.gateway.QueuedJobIDs()),
		"last_seen_unix_ms": int64(0),
		"heartbeat_age_ms":  int64(0),
	}
	if !lastSeen.IsZero() {
		result["last_seen_unix_ms"] = lastSeen.UnixMilli()
		age := now.Sub(lastSeen).Milliseconds()
		if age < 0 {
			age = 0
		}
		result["heartbeat_age_ms"] = age
	}
	return result
}
func (s *DirectHTTPServer) handleWorkerClaim(w http.ResponseWriter, r *http.Request) {
	data, err := s.gateway.ClaimWorker(r.PathValue("job_id"))
	if err != nil {
		s.directError(w, directStatus(err), directCode(err), err.Error())
		return
	}
	s.directJSON(w, http.StatusOK, map[string]any{"protocol_version": direct.WorkerProtocolVersion, "job_id": r.PathValue("job_id"), "package_json": json.RawMessage(data)})
}

func (s *DirectHTTPServer) handleWorkerClaimNext(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ProtocolVersion string `json:"protocol_version"`
		JobID           string `json:"job_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&request); err != nil {
		s.directError(w, http.StatusBadRequest, "invalid_request", "invalid worker claim")
		return
	}
	if request.ProtocolVersion != direct.WorkerProtocolVersion {
		s.directError(w, http.StatusConflict, "protocol_mismatch", "worker protocol version is not supported")
		return
	}
	data, err := s.gateway.ClaimWorker(strings.TrimSpace(request.JobID))
	if err != nil {
		s.directError(w, directStatus(err), directCode(err), err.Error())
		return
	}
	s.directJSON(w, http.StatusOK, map[string]any{"protocol_version": direct.WorkerProtocolVersion, "job_id": request.JobID, "package_json": json.RawMessage(data)})
}

// handleWorkerRun is intentionally loopback-only and Worker-token protected.
// It is a narrow adapter for the local Worker process; task execution remains
// in the existing Server Browser Agent runner and renderer.
func (s *DirectHTTPServer) handleWorkerRun(w http.ResponseWriter, r *http.Request) {
	if err := s.RunDirectJob(r.Context(), r.PathValue("job_id")); err != nil {
		s.directError(w, directStatus(err), "direct_worker_run_failed", err.Error())
		return
	}
	s.directJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "job_id": r.PathValue("job_id")})
}
func (s *DirectHTTPServer) handleWorkerCredential(w http.ResponseWriter, r *http.Request) {
	value, err := s.gateway.ConsumeCredential(r.PathValue("job_id"))
	if err != nil {
		s.directError(w, directStatus(err), directCode(err), err.Error())
		return
	}
	s.directJSON(w, http.StatusOK, value)
}
func (s *DirectHTTPServer) handleWorkerProgress(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Progress int    `json:"progress"`
		Stage    string `json:"stage"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		s.directError(w, http.StatusBadRequest, "invalid_request", "invalid progress")
		return
	}
	if err := s.gateway.UpdateJob(r.PathValue("job_id"), request.Progress, request.Stage); err != nil {
		s.directError(w, directStatus(err), directCode(err), err.Error())
		return
	}
	s.directJSON(w, http.StatusOK, map[string]any{"accepted": true})
}
func (s *DirectHTTPServer) handleWorkerArtifact(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ArtifactID  string `json:"artifact_id"`
		Kind        string `json:"kind"`
		MimeType    string `json:"mime_type"`
		BytesBase64 string `json:"bytes_base64"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		s.directError(w, http.StatusBadRequest, "invalid_request", "invalid artifact")
		return
	}
	data, err := base64.StdEncoding.DecodeString(request.BytesBase64)
	if err != nil {
		s.directError(w, http.StatusBadRequest, "invalid_artifact", "invalid artifact bytes")
		return
	}
	pathArtifactID := strings.TrimSpace(r.PathValue("artifact_id"))
	if pathArtifactID != "" {
		if request.ArtifactID != "" && request.ArtifactID != pathArtifactID {
			s.directError(w, http.StatusBadRequest, "result_artifact_binding_invalid", "artifact path and body identity do not match")
			return
		}
		request.ArtifactID = pathArtifactID
	}
	descriptor, err := s.gateway.AddArtifact(r.PathValue("job_id"), request.ArtifactID, request.Kind, request.MimeType, data)
	if err != nil {
		s.directError(w, directStatus(err), directCode(err), err.Error())
		return
	}
	s.directJSON(w, http.StatusOK, descriptor)
}

func (s *DirectHTTPServer) handleWorkerRelease(w http.ResponseWriter, r *http.Request) {
	status, err := s.gateway.ReleaseWorkerClaim(r.PathValue("job_id"))
	if err != nil {
		s.directError(w, directStatus(err), "job_not_claimed", err.Error())
		return
	}
	s.directJSON(w, http.StatusOK, map[string]any{"protocol_version": direct.WorkerProtocolVersion, "job": status})
}
func (s *DirectHTTPServer) handleWorkerResult(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ResultPackageID string          `json:"result_package_id"`
		ResultJSON      json.RawMessage `json:"result_json"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		s.directError(w, http.StatusBadRequest, "invalid_request", "invalid result")
		return
	}
	// Worker requests are already authenticated and loopback-only; retrieve the
	// job without exposing the lease token to the Worker.
	job, err := s.gateway.WorkerJob(r.PathValue("job_id"))
	if err != nil {
		s.directError(w, directStatus(err), directCode(err), err.Error())
		return
	}
	if err := validateDirectWorkerResult(job, request.ResultPackageID, request.ResultJSON); err != nil {
		_ = s.gateway.FailJob(r.PathValue("job_id"), "result_validation_failed")
		s.directError(w, http.StatusBadRequest, directResultContractErrorCode(err), err.Error())
		return
	}
	var committedResult model.RecordingResultPackage
	if err := json.Unmarshal(request.ResultJSON, &committedResult); err != nil {
		s.directError(w, http.StatusBadRequest, "result_artifact_content_invalid", "invalid recording result package")
		return
	}
	if err := s.gateway.CommitJobResult(r.PathValue("job_id"), request.ResultPackageID, request.ResultJSON, directResultTerminalStatus(committedResult)); err != nil {
		s.directError(w, directStatus(err), directCode(err), err.Error())
		return
	}
	s.directJSON(w, http.StatusOK, map[string]any{"accepted": true})
}

func directResultTerminalStatus(result model.RecordingResultPackage) string {
	if result.Status == model.RecordingResultStatusFailed {
		return "failed"
	}
	return "completed"
}

func validateDirectWorkerResult(job *direct.Job, resultPackageID string, data []byte) error {
	if job == nil || resultPackageID == "" || len(data) == 0 {
		return newDirectResultContractError("result_artifact_completeness_failed", "result package is required")
	}
	var source model.ClientExecutionPackage
	if len(job.PackageJSON) == 0 || json.Unmarshal(job.PackageJSON, &source) != nil {
		return newDirectResultContractError("result_artifact_content_invalid", "direct source package is unavailable")
	}
	var result model.RecordingResultPackage
	if err := json.Unmarshal(data, &result); err != nil {
		return newDirectResultContractError("result_artifact_content_invalid", "invalid recording result package")
	}
	if result.ResultID != resultPackageID {
		return newDirectResultContractError("result_artifact_content_invalid", "recording result identity does not match direct job")
	}
	if err := result.ValidateDirectConsistency(job.Status.PackageID, job.SourcePackageDigest, job.Status.JobID); err != nil {
		return newDirectResultContractError("result_artifact_content_invalid", "%v", err)
	}
	for _, report := range result.ValidationReports {
		if report.SourcePackageID != job.Status.PackageID {
			return newDirectResultContractError("result_artifact_content_invalid", "validation report source package mismatch")
		}
	}
	for _, ref := range directResultArtifactRefs(result) {
		if strings.TrimSpace(ref.ID) == "" || strings.TrimSpace(ref.SHA256) == "" || ref.SizeBytes <= 0 {
			return newDirectResultContractError("result_artifact_completeness_failed", "direct result artifact identity, digest, and size are required")
		}
		artifact, ok := job.Artifacts[ref.ID]
		if !ok || artifact.Descriptor.SHA256 != ref.SHA256 || artifact.Descriptor.Size != ref.SizeBytes {
			return newDirectResultContractError("result_artifact_binding_invalid", "artifact %q digest or size does not match uploaded bytes", ref.ID)
		}
	}
	if err := validateDirectResultURIs(job.Status.JobID, result); err != nil {
		return err
	}
	if result.Status == model.RecordingResultStatusFailed {
		if err := validateDirectFailedResult(source, result); err != nil {
			return err
		}
		return validateDirectStructuredArtifacts(job, source, result)
	}
	if err := validateDirectSuccessfulResult(source, result); err != nil {
		return err
	}
	return validateDirectStructuredArtifacts(job, source, result)
}

func validateDirectFailedResult(source model.ClientExecutionPackage, result model.RecordingResultPackage) error {
	if result.FailureDiagnostic == nil || result.RepairRequest == nil || !result.RepairRequest.ApprovalRequired {
		return newDirectResultContractError("result_artifact_completeness_failed", "direct failed result requires a diagnostic and approval-gated repair request")
	}
	if !result.FailureDiagnostic.RedactionReport.Applied || result.FailureDiagnostic.RedactionReport.FullHTMLIncluded {
		return newDirectResultContractError("result_artifact_content_invalid", "direct failed result diagnostic is not safely redacted")
	}
	if directHasArtifact(directResultArtifactRefs(result), []string{"demo_video"}, "") {
		return newDirectResultContractError("result_artifact_content_invalid", "direct failed result must not contain a final demo video")
	}
	if !directInfrastructureFailureCode(result.FailureDiagnostic.Error.Code) && (len(result.FailureDiagnostic.ScreenshotRefs) == 0 || len(result.FailureDiagnostic.TraceRefs) == 0) {
		return newDirectResultContractError("result_artifact_completeness_failed", "direct failed browser execution requires screenshot and trace evidence")
	}
	if source.ExecutableScriptBundle == nil || result.RepairRequest.FailedBundleHashSHA256 != source.ExecutableScriptBundle.Reproducibility.BundleHashSHA256 || result.RepairRequest.FailedPlanHashSHA256 != source.ExecutableScriptBundle.Reproducibility.PlanHashSHA256 {
		return newDirectResultContractError("result_artifact_content_invalid", "direct failed repair request does not bind the approved bundle and plan")
	}
	return nil
}

func validateDirectSuccessfulResult(source model.ClientExecutionPackage, result model.RecordingResultPackage) error {
	if result.Status != model.RecordingResultStatusGenerated && result.Status != model.RecordingResultStatusDelivered && result.Status != model.RecordingResultStatusAcked {
		return fmt.Errorf("direct successful result has unsupported status %q", result.Status)
	}
	if source.ExecutableScriptBundle == nil || source.ExecutableScriptBundle.StageApprovalPlan == nil {
		return errors.New("direct source package is missing stage approval plan")
	}
	if result.ExecutionRuntime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 || result.ExecutionRuntime != source.ExecutableScriptBundle.ScriptManifest.Runtime {
		return errors.New("direct successful result runtime does not match approved browser-agent runtime")
	}
	if result.ExecutionTrace == nil {
		return errors.New("direct successful result requires execution_trace")
	}
	if result.StageEventLogRef == nil {
		return errors.New("direct successful result requires stage_event_log_ref")
	}
	if !result.VerificationReport.ReproducibilityMatch || result.VerificationReport.PassRate < 1 || len(result.VerificationReport.FailedNodeIDs) > 0 {
		return errors.New("direct successful result verification report is incomplete")
	}

	steps := map[string]model.StepResult{}
	for _, step := range result.StepResults {
		steps[step.NodeID] = step
	}
	runtimeReports := map[string]bool{}
	preExecutionSeen := false
	postExecutionSeen := false
	for _, report := range result.ValidationReports {
		if report.Decision != model.ValidationDecisionContinue {
			return fmt.Errorf("direct successful result contains non-continuing validation decision %q", report.Decision)
		}
		for _, check := range report.Checks {
			if check.Required && !check.Passed {
				return fmt.Errorf("direct successful result contains failed required validation %q", check.ID)
			}
		}
		switch report.Phase {
		case model.ValidationPhasePreExecution:
			preExecutionSeen = true
		case model.ValidationPhaseRuntimeStage:
			runtimeReports[report.NodeID] = true
		case model.ValidationPhasePostExecution:
			postExecutionSeen = true
		}
	}
	if !preExecutionSeen || !postExecutionSeen {
		return errors.New("direct successful result requires pre_execution and post_execution validation reports")
	}
	for _, stage := range source.ExecutableScriptBundle.StageApprovalPlan.Stages {
		step, ok := steps[stage.NodeID]
		if !ok || step.Status != "passed" || !directObservedStateIsRuntimeDerived(step.ObservedState) {
			return fmt.Errorf("direct successful result is missing a passed runtime-derived step for node %q", stage.NodeID)
		}
		if !runtimeReports[stage.NodeID] {
			return fmt.Errorf("direct successful result is missing runtime validation for node %q", stage.NodeID)
		}
	}

	refs := directResultArtifactRefs(result)
	if source.RecordingRunSpec.Outputs.RawRecording && !directHasArtifact(refs, []string{"raw_recording"}, "") {
		return errors.New("direct successful result is missing requested raw recording")
	}
	if source.RecordingRunSpec.Outputs.Trace && !directHasArtifact(refs, []string{"browser_trace", "execution_trace"}, "") {
		return errors.New("direct successful result is missing requested browser trace")
	}
	if source.RecordingRunSpec.Outputs.ScreenshotPack && !directHasArtifact(refs, []string{"screenshot", "step_screenshot", "webpage_screenshot"}, "") {
		return errors.New("direct successful result is missing requested screenshots")
	}
	if source.RecordingRunSpec.Outputs.FinalVideo && !directHasArtifact(refs, []string{"demo_video"}, "video/mp4") {
		return errors.New("direct successful result is missing requested final MP4")
	}
	if source.RecordingRunSpec.Outputs.FinalVideo && model.RequiresDualMediaDelivery(&source) {
		if !directHasArtifact(refs, []string{"final_video_final_master_2k"}, "video/mp4") {
			return errors.New("direct successful result is missing requested 2K final MP4")
		}
		if !directHasArtifact(refs, []string{"final_video_final_delivery_1080p"}, "video/mp4") {
			return errors.New("direct successful result is missing requested 1080p final MP4")
		}
		if !directHasArtifact(refs, []string{"deliverables_manifest"}, "application/json") {
			return errors.New("direct successful result is missing deliverables manifest")
		}
	}
	if source.RecordingRunSpec.Outputs.StepByStepDocs && !directHasArtifact(refs, []string{"step_by_step_docs"}, "") {
		return errors.New("direct successful result is missing requested step-by-step documentation")
	}

	checksums := map[string]model.ContentDigest{}
	for _, checksum := range result.VerificationReport.OutputChecksums {
		checksums[checksum.ID] = checksum
	}
	for _, ref := range refs {
		checksum, ok := checksums[ref.ID]
		if !ok || checksum.SHA256 != ref.SHA256 || checksum.SizeBytes != ref.SizeBytes {
			return fmt.Errorf("direct successful result checksum is missing or inconsistent for artifact %q", ref.ID)
		}
	}
	return nil
}

func directObservedStateIsRuntimeDerived(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(value, "source=actual_browser_observation") ||
		strings.HasPrefix(value, "source=browser_assertion") ||
		strings.HasPrefix(value, "source=artifact_observation")
}

func directHasArtifact(refs []model.ArtifactRef, kinds []string, mimeType string) bool {
	for _, ref := range refs {
		for _, kind := range kinds {
			if strings.EqualFold(ref.Kind, kind) && (mimeType == "" || strings.EqualFold(ref.MimeType, mimeType)) {
				return true
			}
		}
	}
	return false
}

func directResultArtifactRefs(result model.RecordingResultPackage) []model.ArtifactRef {
	refs := make([]model.ArtifactRef, 0, len(result.GeneratedAssets)+1)
	seen := map[string]bool{}
	appendRef := func(ref model.ArtifactRef) {
		if strings.TrimSpace(ref.ID) == "" || seen[ref.ID] {
			return
		}
		seen[ref.ID] = true
		refs = append(refs, ref)
	}
	for _, ref := range result.GeneratedAssets {
		appendRef(ref)
	}
	if result.ExecutionTrace != nil {
		for _, ref := range result.ExecutionTrace.Artifacts {
			appendRef(ref)
		}
	}
	for _, step := range result.StepResults {
		for _, ref := range step.Artifacts {
			appendRef(ref)
		}
	}
	if result.StageEventLogRef != nil {
		appendRef(*result.StageEventLogRef)
	}
	for _, descriptor := range result.Delivery.AssetRefs {
		appendRef(model.ArtifactRef{ID: descriptor.ID, Kind: descriptor.Kind, URI: descriptor.URI, MimeType: descriptor.MimeType, SHA256: descriptor.SHA256, SizeBytes: descriptor.SizeBytes, Sensitive: descriptor.Sensitive, Metadata: descriptor.Metadata})
	}
	if result.FailureDiagnostic != nil {
		for _, descriptor := range directDiagnosticArtifacts(result.FailureDiagnostic) {
			appendRef(model.ArtifactRef{ID: descriptor.ID, Kind: descriptor.Kind, URI: descriptor.URI, MimeType: descriptor.MimeType, SHA256: descriptor.SHA256, SizeBytes: descriptor.SizeBytes, Sensitive: descriptor.Sensitive, Metadata: descriptor.Metadata})
		}
	}
	return refs
}

func directDiagnosticArtifacts(diagnostic *model.ScriptFailureDiagnostic) []model.PackageArtifactDescriptor {
	if diagnostic == nil {
		return nil
	}
	artifacts := append([]model.PackageArtifactDescriptor{}, diagnostic.ScreenshotRefs...)
	artifacts = append(artifacts, diagnostic.TraceRefs...)
	if diagnostic.DOMSnapshotRef != nil {
		artifacts = append(artifacts, *diagnostic.DOMSnapshotRef)
	}
	if diagnostic.AccessibilitySnapshotRef != nil {
		artifacts = append(artifacts, *diagnostic.AccessibilitySnapshotRef)
	}
	return artifacts
}

func synchronizeDirectResultChecksums(result *model.RecordingResultPackage) {
	if result == nil {
		return
	}
	refs := directResultArtifactRefs(*result)
	checksums := make([]model.ContentDigest, 0, len(refs))
	for _, ref := range refs {
		checksums = append(checksums, model.ContentDigest{ID: ref.ID, Kind: ref.Kind, SHA256: ref.SHA256, SizeBytes: ref.SizeBytes})
	}
	result.VerificationReport.OutputChecksums = checksums
}

func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func requestLocalPort(r *http.Request) int {
	host := r.Host
	if strings.Contains(host, ":") {
		_, value, err := net.SplitHostPort(host)
		if err == nil {
			port, _ := strconv.Atoi(value)
			return port
		}
	}
	return 0
}
func (s *DirectHTTPServer) directJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func (s *DirectHTTPServer) directError(w http.ResponseWriter, status int, code, message string) {
	s.directJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func directStatus(err error) int {
	switch {
	case errors.Is(err, direct.ErrLeaseHasActiveJobs):
		return http.StatusConflict
	case errors.Is(err, direct.ErrResultAckRequired):
		return http.StatusConflict
	case errors.Is(err, direct.ErrPackageIdempotencyConflict):
		return http.StatusConflict
	case errors.Is(err, direct.ErrGatewayPersistence):
		return http.StatusInternalServerError
	case errors.Is(err, direct.ErrLeaseNotFound), errors.Is(err, direct.ErrJobNotFound):
		return http.StatusNotFound
	case errors.Is(err, direct.ErrReplay):
		return http.StatusConflict
	}
	return http.StatusBadRequest
}
func directCode(err error) string {
	if errors.Is(err, direct.ErrLeaseHasActiveJobs) {
		return "lease_has_active_jobs"
	}
	if errors.Is(err, direct.ErrReplay) {
		return "replay_detected"
	}
	if errors.Is(err, direct.ErrJobNotFound) {
		return "job_not_found"
	}
	if errors.Is(err, direct.ErrLeaseNotFound) {
		return "lease_not_found"
	}
	if errors.Is(err, direct.ErrLeaseExpired) {
		return "lease_expired"
	}
	if errors.Is(err, direct.ErrResultAckRequired) {
		return "result_ack_required"
	}
	if errors.Is(err, direct.ErrPackageIdempotencyConflict) {
		return "package_idempotency_conflict"
	}
	if errors.Is(err, direct.ErrGatewayPersistence) {
		return "gateway_state_unavailable"
	}
	return "direct_request_rejected"
}
