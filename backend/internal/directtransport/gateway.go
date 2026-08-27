package directtransport

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"cascade-demoops/backend/internal/model"
)

const (
	maxPackageBodyBytes    = 32 << 20
	maxWorkerArtifactBytes = 2 << 30
)

type Config struct {
	ControlAddr           string
	WorkerAddr            string
	DataBindHost          string
	AdvertisedHost        string
	DataPortStart         int
	DataPortEnd           int
	TLSCertificateFile    string
	TLSPrivateKeyFile     string
	AllowInsecureLoopback bool
	BootstrapToken        string
	WorkerToken           string
	SpoolRoot             string
	LeaseTTL              time.Duration
	Now                   func() time.Time
}

type Gateway struct {
	config    Config
	now       func() time.Time
	tlsConfig *tls.Config

	mu            sync.Mutex
	leases        map[string]*leaseRuntime
	jobs          map[string]*jobRecord
	requestNonces map[string]time.Time
	leaseNonces   map[string]time.Time
	controlServer *http.Server
	workerServer  *http.Server
}

type leaseRuntime struct {
	lease        model.DirectPortLease
	listener     net.Listener
	server       *http.Server
	seenMessages map[string]bool
	seenRequests map[string]bool
}

type jobRecord struct {
	Status            model.DirectJobStatus        `json:"status"`
	LeaseID           string                       `json:"lease_id"`
	InstallationID    string                       `json:"installation_id"`
	PackageDigest     string                       `json:"package_digest_sha256"`
	AcceptedAt        time.Time                    `json:"accepted_at"`
	PackagePath       string                       `json:"package_path"`
	ResultPath        string                       `json:"result_path,omitempty"`
	Artifacts         map[string]artifactRecord    `json:"artifacts,omitempty"`
	Credential        *model.DirectCredentialValue `json:"-"`
	CredentialGrantID string                       `json:"-"`
	DeliveryAcked     bool                         `json:"delivery_acked,omitempty"`
	AckedAt           time.Time                    `json:"acked_at,omitempty"`
	AckedArtifactIDs  []string                     `json:"acked_artifact_ids,omitempty"`
}

type persistedJobRecord struct {
	Status              model.DirectJobStatus           `json:"status"`
	LeaseID             string                          `json:"lease_id"`
	InstallationID      string                          `json:"installation_id"`
	PackageDigestSHA256 string                          `json:"package_digest_sha256"`
	AcceptedAt          time.Time                       `json:"accepted_at"`
	HasResult           bool                            `json:"has_result,omitempty"`
	Artifacts           map[string]model.DirectArtifact `json:"artifacts,omitempty"`
	DeliveryAcked       bool                            `json:"delivery_acked,omitempty"`
	AckedAt             time.Time                       `json:"acked_at,omitempty"`
	AckedArtifactIDs    []string                        `json:"acked_artifact_ids,omitempty"`
}

type artifactRecord struct {
	Artifact model.DirectArtifact `json:"artifact"`
	Path     string               `json:"path"`
}

type WorkerJob struct {
	ProtocolVersion string                       `json:"protocol_version"`
	JobID           string                       `json:"job_id"`
	LeaseID         string                       `json:"lease_id"`
	Package         model.ClientExecutionPackage `json:"package"`
}

type WorkerCredential struct {
	JobID      string                      `json:"job_id"`
	PackageID  string                      `json:"package_id"`
	GrantID    string                      `json:"grant_id"`
	Credential model.DirectCredentialValue `json:"credential"`
}

type WorkerStatusUpdate struct {
	Status          string `json:"status"`
	Stage           string `json:"stage"`
	Message         string `json:"message,omitempty"`
	ProgressPercent int    `json:"progress_percent,omitempty"`
	ResultPackageID string `json:"result_package_id,omitempty"`
}

type WorkerReleaseRequest struct {
	Reason string `json:"reason,omitempty"`
}

func NewGateway(config Config) (*Gateway, error) {
	if config.ControlAddr == "" {
		config.ControlAddr = "0.0.0.0:18443"
	}
	if config.WorkerAddr == "" {
		config.WorkerAddr = "127.0.0.1:18444"
	}
	if config.DataBindHost == "" {
		config.DataBindHost = "0.0.0.0"
	}
	if config.DataPortStart == 0 {
		config.DataPortStart = 24000
	}
	if config.DataPortEnd == 0 {
		config.DataPortEnd = 24031
	}
	if config.LeaseTTL == 0 {
		config.LeaseTTL = 30 * time.Minute
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.DataPortStart < 1024 || config.DataPortEnd > 65535 || config.DataPortEnd < config.DataPortStart {
		return nil, errors.New("direct data port range is invalid")
	}
	if len(strings.TrimSpace(config.BootstrapToken)) < 32 || len(strings.TrimSpace(config.WorkerToken)) < 32 {
		return nil, errors.New("bootstrap and worker tokens must each contain at least 32 characters")
	}
	if strings.TrimSpace(config.SpoolRoot) == "" {
		return nil, errors.New("direct transport spool root is required")
	}
	config.AdvertisedHost = strings.TrimSpace(config.AdvertisedHost)
	if config.AdvertisedHost == "" || strings.ContainsAny(config.AdvertisedHost, "/?#") {
		return nil, errors.New("direct transport advertised host is required and must be a DNS name or IP address")
	}
	if err := os.MkdirAll(config.SpoolRoot, 0o700); err != nil {
		return nil, err
	}
	var tlsConfig *tls.Config
	if config.TLSCertificateFile != "" || config.TLSPrivateKeyFile != "" {
		certificate, err := tls.LoadX509KeyPair(config.TLSCertificateFile, config.TLSPrivateKeyFile)
		if err != nil {
			return nil, err
		}
		tlsConfig = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS13}
	} else if !config.AllowInsecureLoopback || !isLoopbackAddress(config.ControlAddr) || !isLoopbackAddress(config.DataBindHost+":"+strconv.Itoa(config.DataPortStart)) {
		return nil, errors.New("TLS certificate and private key are required outside explicit loopback development")
	}
	gateway := &Gateway{config: config, now: config.Now, tlsConfig: tlsConfig, leases: map[string]*leaseRuntime{}, jobs: map[string]*jobRecord{}, requestNonces: map[string]time.Time{}, leaseNonces: map[string]time.Time{}}
	if err := gateway.loadPersistedJobs(); err != nil {
		return nil, err
	}
	return gateway, nil
}

func (g *Gateway) Run(ctx context.Context) error {
	controlListener, err := net.Listen("tcp", g.config.ControlAddr)
	if err != nil {
		return err
	}
	workerListener, err := net.Listen("tcp", g.config.WorkerAddr)
	if err != nil {
		_ = controlListener.Close()
		return err
	}
	if !isLoopbackAddress(workerListener.Addr().String()) {
		_ = controlListener.Close()
		_ = workerListener.Close()
		return errors.New("worker API must bind to a loopback address")
	}
	if g.tlsConfig != nil {
		controlListener = tls.NewListener(controlListener, g.tlsConfig.Clone())
	}
	g.controlServer = &http.Server{Handler: g.ControlHandler(), ReadHeaderTimeout: 5 * time.Second}
	g.workerServer = &http.Server{Handler: g.WorkerHandler(), ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 2)
	go func() { errCh <- g.controlServer.Serve(controlListener) }()
	go func() { errCh <- g.workerServer.Serve(workerListener) }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		g.shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		g.shutdown(context.Background())
		return err
	}
}

func (g *Gateway) shutdown(ctx context.Context) {
	if g.controlServer != nil {
		_ = g.controlServer.Shutdown(ctx)
	}
	if g.workerServer != nil {
		_ = g.workerServer.Shutdown(ctx)
	}
	g.mu.Lock()
	leases := make([]*leaseRuntime, 0, len(g.leases))
	for _, lease := range g.leases {
		leases = append(leases, lease)
	}
	g.mu.Unlock()
	for _, lease := range leases {
		_ = lease.server.Shutdown(ctx)
		_ = lease.listener.Close()
	}
}

// Close releases all control, worker, and dedicated App listeners.
func (g *Gateway) Close(ctx context.Context) {
	g.shutdown(ctx)
}

func (g *Gateway) ControlHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/direct/health", g.bootstrapAuth(g.handleHealth))
	mux.HandleFunc("POST /v1/direct/leases", g.handleLease)
	mux.HandleFunc("POST /v1/direct/leases/release", g.bootstrapAuth(g.handleLeaseRelease))
	return securityHeaders(mux)
}

func (g *Gateway) bootstrapAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !bearerMatches(r, g.config.BootstrapToken) {
			writeError(w, http.StatusUnauthorized, "direct_auth_invalid", "Direct Browser Agent access token is invalid.")
			return
		}
		next(w, r)
	}
}

func (g *Gateway) WorkerHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/worker/jobs/claim", g.workerAuth(g.handleWorkerClaim))
	mux.HandleFunc("GET /v1/worker/jobs/{job_id}/control", g.workerAuth(g.handleWorkerControl))
	mux.HandleFunc("PUT /v1/worker/jobs/{job_id}/status", g.workerAuth(g.handleWorkerStatus))
	mux.HandleFunc("PUT /v1/worker/jobs/{job_id}/result", g.workerAuth(g.handleWorkerResult))
	mux.HandleFunc("PUT /v1/worker/jobs/{job_id}/artifacts/{artifact_id}", g.workerAuth(g.handleWorkerArtifact))
	mux.HandleFunc("POST /v1/worker/jobs/{job_id}/credentials/consume", g.workerAuth(g.handleWorkerCredentialConsume))
	mux.HandleFunc("POST /v1/worker/jobs/{job_id}/release", g.workerAuth(g.handleWorkerRelease))
	return securityHeaders(mux)
}

func (g *Gateway) handleHealth(w http.ResponseWriter, _ *http.Request) {
	g.mu.Lock()
	active := len(g.leases)
	g.mu.Unlock()
	writeJSON(w, http.StatusOK, model.DirectHealthResponse{
		ProtocolVersion: model.DirectTransportProtocolVersion, CryptoSuite: model.DirectTransportCryptoSuite,
		SupportedProtocolVersions:             []string{model.DirectTransportProtocolVersion},
		SupportedPackageSchemaVersions:        []string{model.ClientExecutionPackageSchemaVersion},
		SupportedRuntimes:                     []string{model.ExecutableScriptRuntimeBrowserAgentOutlineV1},
		SupportedWorkerProtocolVersions:       []string{model.DirectWorkerProtocolVersion},
		SupportedOutcomeVerifierRulesVersions: []string{model.BrowserAgentOutcomeVerifierRulesVersion},
		Capabilities: map[string]bool{
			"credential_envelope": true, "manual_login_checkpoint": false, "artifact_chunk_resume": true,
			"idempotent_package_upload": true, "explicit_lease_release": true, "formal_result_artifact_gate": true,
		},
		ActiveLeases: active, ServerTime: g.now().UTC(),
	})
}

func (g *Gateway) handleLease(w http.ResponseWriter, r *http.Request) {
	if !bearerMatches(r, g.config.BootstrapToken) {
		writeError(w, http.StatusUnauthorized, "direct_auth_invalid", "Direct Browser Agent access token is invalid.")
		return
	}
	var request model.DirectLeaseRequest
	if err := decodeLimitedJSON(r.Body, 1<<20, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	now := g.now().UTC()
	if err := model.ValidateDirectLeaseRequest(request, now); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_lease_request", err.Error())
		return
	}
	g.mu.Lock()
	g.pruneNoncesLocked(now)
	if _, exists := g.leaseNonces[request.InstallationID+"|"+request.RequestNonce]; exists {
		g.mu.Unlock()
		writeError(w, http.StatusConflict, "replay_detected", "Lease request nonce was already used.")
		return
	}
	g.leaseNonces[request.InstallationID+"|"+request.RequestNonce] = now.Add(model.DirectTransportMaxClockSkew)
	g.mu.Unlock()
	lease, err := g.allocateLease(request.InstallationID, now)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "direct_port_unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, lease)
}

func (g *Gateway) handleLeaseRelease(w http.ResponseWriter, r *http.Request) {
	var request model.DirectLeaseReleaseRequest
	if err := decodeLimitedJSON(r.Body, 1<<20, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	now := g.now().UTC()
	if err := model.ValidateDirectLeaseReleaseRequest(request, now); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_lease_release", err.Error())
		return
	}
	g.mu.Lock()
	g.pruneNoncesLocked(now)
	nonceKey := request.InstallationID + "|release|" + request.RequestNonce
	if _, exists := g.leaseNonces[nonceKey]; exists {
		g.mu.Unlock()
		writeError(w, http.StatusConflict, "replay_detected", "Lease release request nonce was already used.")
		return
	}
	g.leaseNonces[nonceKey] = now.Add(model.DirectTransportMaxClockSkew)
	runtime, ok := g.leases[request.LeaseID]
	if !ok || runtime.lease.InstallationID != request.InstallationID {
		g.mu.Unlock()
		writeError(w, http.StatusNotFound, "lease_not_found", "Dedicated Browser Agent lease was not found.")
		return
	}
	for _, record := range g.jobs {
		if record.InstallationID == request.InstallationID && record.LeaseID == request.LeaseID && !directJobTerminal(record.Status.Status) {
			g.mu.Unlock()
			writeError(w, http.StatusConflict, "lease_has_active_jobs", "Dedicated Browser Agent lease still has active jobs.")
			return
		}
		if record.InstallationID == request.InstallationID && record.LeaseID == request.LeaseID && record.Status.Status == "completed" && !record.DeliveryAcked {
			g.mu.Unlock()
			writeError(w, http.StatusConflict, "result_ack_required", "Completed Browser Agent results must be checksum-ACKed before lease release.")
			return
		}
	}
	delete(g.leases, request.LeaseID)
	_ = runtime.server.Close()
	_ = runtime.listener.Close()
	g.mu.Unlock()
	leaseIDSuffix := request.LeaseID
	if len(leaseIDSuffix) > 8 {
		leaseIDSuffix = leaseIDSuffix[len(leaseIDSuffix)-8:]
	}
	writeJSON(w, http.StatusOK, map[string]any{"protocol_version": model.DirectTransportProtocolVersion, "released": true, "lease_id_suffix": leaseIDSuffix})
}

func directJobTerminal(status string) bool {
	switch strings.TrimSpace(status) {
	case "completed", "failed", "canceled", "expired":
		return true
	default:
		return false
	}
}

func (g *Gateway) allocateLease(installationID string, now time.Time) (model.DirectPortLease, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	// Expired listeners must be retired before allocating a new lease.  The
	// expiry goroutine normally closes them, but a request can arrive in the
	// small scheduling window between the lease deadline and that goroutine.
	// Retiring synchronously prevents an App from being assigned a misleading
	// second port merely because the old listener still owns its socket.
	for leaseID, runtime := range g.leases {
		if now.Before(runtime.lease.ExpiresAt) {
			continue
		}
		delete(g.leases, leaseID)
		_ = runtime.server.Close()
		_ = runtime.listener.Close()
	}
	for _, runtime := range g.leases {
		if runtime.lease.InstallationID == installationID && now.Before(runtime.lease.ExpiresAt) {
			return runtime.lease, nil
		}
	}
	leaseIDToken, err := model.NewDirectTransportToken(32)
	if err != nil {
		return model.DirectPortLease{}, err
	}
	leaseToken, err := model.NewDirectTransportToken(32)
	if err != nil {
		return model.DirectPortLease{}, err
	}
	for port := g.config.DataPortStart; port <= g.config.DataPortEnd; port++ {
		listener, listenErr := net.Listen("tcp", net.JoinHostPort(g.config.DataBindHost, strconv.Itoa(port)))
		if listenErr != nil {
			continue
		}
		scheme := "https"
		if g.tlsConfig == nil {
			scheme = "http"
		} else {
			listener = tls.NewListener(listener, g.tlsConfig.Clone())
		}
		lease := model.DirectPortLease{ProtocolVersion: model.DirectTransportProtocolVersion, LeaseID: "lease_" + leaseIDToken, InstallationID: installationID, DataURL: scheme + "://" + net.JoinHostPort(g.config.AdvertisedHost, strconv.Itoa(port)), DataPort: port, LeaseToken: leaseToken, IssuedAt: now, ExpiresAt: now.Add(g.config.LeaseTTL), CryptoSuite: model.DirectTransportCryptoSuite, ServerTime: now}
		runtime := &leaseRuntime{lease: lease, listener: listener, seenMessages: map[string]bool{}, seenRequests: map[string]bool{}}
		runtime.server = &http.Server{Handler: securityHeaders(g.dataHandler(runtime)), ReadHeaderTimeout: 5 * time.Second}
		g.leases[lease.LeaseID] = runtime
		go func() {
			_ = runtime.server.Serve(listener)
			g.mu.Lock()
			if current, ok := g.leases[lease.LeaseID]; ok && current == runtime {
				delete(g.leases, lease.LeaseID)
			}
			g.mu.Unlock()
		}()
		go func() {
			timer := time.NewTimer(time.Until(lease.ExpiresAt))
			defer timer.Stop()
			<-timer.C
			_ = runtime.server.Close()
		}()
		return lease, nil
	}
	return model.DirectPortLease{}, errors.New("no dedicated data port is available")
}

func (g *Gateway) dataHandler(lease *leaseRuntime) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/direct/packages", func(w http.ResponseWriter, r *http.Request) { g.handlePackage(w, r, lease) })
	mux.HandleFunc("POST /v1/direct/jobs/{job_id}/credentials", func(w http.ResponseWriter, r *http.Request) { g.handleCredential(w, r, lease) })
	mux.HandleFunc("POST /v1/direct/jobs/{job_id}/ack", func(w http.ResponseWriter, r *http.Request) { g.handleResultAck(w, r, lease) })
	mux.HandleFunc("POST /v1/direct/jobs/{job_id}/cancel", func(w http.ResponseWriter, r *http.Request) { g.handleJobCancel(w, r, lease) })
	mux.HandleFunc("GET /v1/direct/jobs/{job_id}", func(w http.ResponseWriter, r *http.Request) { g.handleJobStatus(w, r, lease) })
	mux.HandleFunc("GET /v1/direct/jobs/{job_id}/package", func(w http.ResponseWriter, r *http.Request) { g.handleJobPackage(w, r, lease) })
	mux.HandleFunc("GET /v1/direct/jobs/{job_id}/result", func(w http.ResponseWriter, r *http.Request) { g.handleJobResult(w, r, lease) })
	mux.HandleFunc("GET /v1/direct/jobs/{job_id}/artifacts/{artifact_id}", func(w http.ResponseWriter, r *http.Request) { g.handleArtifact(w, r, lease) })
	mux.HandleFunc("GET /v1/direct/jobs/{job_id}/artifacts/{artifact_id}/chunks/{chunk_index}", func(w http.ResponseWriter, r *http.Request) { g.handleArtifactChunk(w, r, lease) })
	return mux
}

func (g *Gateway) handleJobCancel(w http.ResponseWriter, r *http.Request, lease *leaseRuntime) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "cancel_too_large", "Job cancel request exceeds the transport limit.")
		return
	}
	if err := g.authenticateDataRequest(r, lease, body); err != nil {
		writeError(w, http.StatusUnauthorized, "direct_request_invalid", err.Error())
		return
	}
	var message model.DirectEncryptedMessage
	if err := json.Unmarshal(body, &message); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_encrypted_message", "Encrypted job cancel message is invalid.")
		return
	}
	var request model.DirectJobCancelRequest
	if err := model.DecryptDirectTransportJSON(message, lease.lease, "job_cancel", model.DirectTransportDirectionUpload, g.now(), &request); err != nil {
		writeError(w, http.StatusBadRequest, "cancel_decryption_failed", "Job cancel authentication failed.")
		return
	}
	jobID := r.PathValue("job_id")
	if request.ProtocolVersion != model.DirectTransportProtocolVersion || request.InstallationID != lease.lease.InstallationID || request.JobID != jobID {
		writeError(w, http.StatusUnprocessableEntity, "cancel_binding_invalid", "Job cancel does not match the active lease and installation.")
		return
	}
	g.mu.Lock()
	record := g.jobs[jobID]
	if record == nil || record.InstallationID != lease.lease.InstallationID {
		g.mu.Unlock()
		writeError(w, http.StatusNotFound, "job_not_found", "Job was not found.")
		return
	}
	if record.Status.Status != "canceled" {
		if directJobTerminal(record.Status.Status) {
			g.mu.Unlock()
			writeError(w, http.StatusConflict, "job_terminal", "Completed or failed Browser Agent jobs cannot be canceled.")
			return
		}
		record.Status.Status = "canceled"
		record.Status.Stage = "canceled"
		record.Status.Message = "Browser Agent execution was canceled by its owning experiment."
		record.Status.BlockingErrorCode = "experiment_canceled"
		record.Status.UpdatedAt = g.now().UTC()
		record.Credential = nil
		record.CredentialGrantID = ""
		if err := g.persistJob(record); err != nil {
			g.mu.Unlock()
			writeError(w, http.StatusInternalServerError, "job_state_write_failed", "Canceled job state could not be persisted.")
			return
		}
	}
	status := record.Status
	g.mu.Unlock()
	g.writeEncrypted(w, http.StatusOK, lease, "job_cancel_receipt", model.DirectJobCancelReceipt{
		ProtocolVersion: model.DirectTransportProtocolVersion,
		JobID:           jobID,
		Status:          status.Status,
		Stage:           status.Stage,
		CanceledAt:      status.UpdatedAt,
	})
}

func (g *Gateway) handleResultAck(w http.ResponseWriter, r *http.Request, lease *leaseRuntime) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "ack_too_large", "Result ACK exceeds the transport limit.")
		return
	}
	if err := g.authenticateDataRequest(r, lease, body); err != nil {
		writeError(w, http.StatusUnauthorized, "direct_request_invalid", err.Error())
		return
	}
	var message model.DirectEncryptedMessage
	if err := json.Unmarshal(body, &message); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_encrypted_message", "Encrypted ACK message is invalid.")
		return
	}
	var request model.DirectResultAckRequest
	if err := model.DecryptDirectTransportJSON(message, lease.lease, "result_ack", model.DirectTransportDirectionUpload, g.now(), &request); err != nil {
		writeError(w, http.StatusBadRequest, "ack_decryption_failed", "Result ACK authentication failed.")
		return
	}
	jobID := r.PathValue("job_id")
	if request.ProtocolVersion != model.DirectTransportProtocolVersion || request.InstallationID != lease.lease.InstallationID || request.JobID != jobID || !request.VerifiedChecksums || request.AckedAt.IsZero() {
		writeError(w, http.StatusUnprocessableEntity, "ack_binding_invalid", "Result ACK binding or checksum confirmation is invalid.")
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	record := g.jobs[jobID]
	if record == nil || record.InstallationID != lease.lease.InstallationID || record.Status.Status != "completed" || record.Status.ResultPackageID != request.ResultPackageID {
		writeError(w, http.StatusConflict, "ack_not_expected", "Completed result binding was not found for ACK.")
		return
	}
	expected := make([]string, 0, len(record.Artifacts))
	for id := range record.Artifacts {
		expected = append(expected, id)
	}
	sort.Strings(expected)
	received := append([]string(nil), request.ReceivedArtifactIDs...)
	sort.Strings(received)
	if strings.Join(expected, "\x00") != strings.Join(received, "\x00") {
		writeError(w, http.StatusUnprocessableEntity, "ack_artifacts_incomplete", "ACK must include every verified result artifact ID.")
		return
	}
	record.DeliveryAcked = true
	record.AckedAt = request.AckedAt.UTC()
	record.AckedArtifactIDs = received
	if err := g.persistJob(record); err != nil {
		writeError(w, http.StatusInternalServerError, "job_state_write_failed", "Result ACK could not be persisted.")
		return
	}
	receipt := model.DirectResultAckReceipt{ProtocolVersion: model.DirectTransportProtocolVersion, JobID: jobID, ResultPackageID: request.ResultPackageID, ReceivedArtifactIDs: received, VerifiedChecksums: true, AckedAt: record.AckedAt}
	g.writeEncrypted(w, http.StatusOK, lease, "result_ack_receipt", receipt)
}

func (g *Gateway) authenticateDataRequest(r *http.Request, lease *leaseRuntime, body []byte) error {
	now := g.now().UTC()
	if !now.Before(lease.lease.ExpiresAt) {
		return errors.New("direct transport lease expired")
	}
	timestamp, err := strconv.ParseInt(r.Header.Get("X-Cascade-Timestamp"), 10, 64)
	if err != nil {
		return errors.New("invalid request timestamp")
	}
	nonce := strings.TrimSpace(r.Header.Get("X-Cascade-Nonce"))
	if nonce == "" {
		return errors.New("request nonce is required")
	}
	digest := model.SHA256Hex(body)
	if !hmac.Equal([]byte(strings.ToLower(r.Header.Get("X-Cascade-Body-SHA256"))), []byte(digest)) {
		return errors.New("request body digest mismatch")
	}
	if err := model.VerifyDirectTransportRequestSignature(lease.lease, r.Method, r.URL.EscapedPath(), timestamp, nonce, digest, r.Header.Get("X-Cascade-Signature"), now); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	key := lease.lease.LeaseID + "|" + nonce
	if lease.seenRequests[key] {
		return errors.New("request replay detected")
	}
	lease.seenRequests[key] = true
	return nil
}

func (g *Gateway) handlePackage(w http.ResponseWriter, r *http.Request, lease *leaseRuntime) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPackageBodyBytes))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "package_too_large", "Encrypted package exceeds the transport limit.")
		return
	}
	if err := g.authenticateDataRequest(r, lease, body); err != nil {
		writeError(w, http.StatusUnauthorized, "direct_request_invalid", err.Error())
		return
	}
	var message model.DirectEncryptedMessage
	if err := json.Unmarshal(body, &message); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_encrypted_message", "Encrypted message is invalid.")
		return
	}
	messageKey := message.MessageID + "|" + message.NonceBase64
	g.mu.Lock()
	replay := lease.seenMessages[messageKey]
	g.mu.Unlock()
	if replay {
		writeError(w, http.StatusConflict, "replay_detected", "Encrypted message was already accepted.")
		return
	}
	var pkg model.ClientExecutionPackage
	if err := model.DecryptDirectTransportJSON(message, lease.lease, "client_execution_package", model.DirectTransportDirectionUpload, g.now(), &pkg); err != nil {
		writeError(w, http.StatusBadRequest, "package_decryption_failed", err.Error())
		return
	}
	if err := model.ValidateClientExecutionPackageForCloudExecution(&pkg); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "package_validation_failed", err.Error())
		return
	}
	approval := pkg.SafetyReport.HumanApproval
	if pkg.ProducerInstallationID == "" || pkg.ProducerInstallationID != lease.lease.InstallationID || approval.ApprovedByInstallationID != lease.lease.InstallationID || approval.ApprovalSchemaVersion != "cascade.user_approval.v1" {
		writeError(w, http.StatusUnprocessableEntity, "unverified_origin", "Package producer, approval, lease, and installation bindings do not match.")
		return
	}
	if err := model.ValidatePackageApprovalComponentDigests(pkg); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "approval_digest_mismatch", err.Error())
		return
	}
	approvalDigest, err := model.ComputePackageApprovalSubjectDigest(pkg)
	if err != nil || approval.ApprovalSubjectDigestSHA256 == "" || approvalDigest != approval.ApprovalSubjectDigestSHA256 {
		writeError(w, http.StatusUnprocessableEntity, "approval_digest_mismatch", "Unified approval subject digest does not match the uploaded package.")
		return
	}
	if pkg.ExecutableScriptBundle == nil || pkg.ExecutableScriptBundle.ScriptOutline == nil || pkg.ExecutableScriptBundle.ScriptOutline.Runtime != "browser-agent-outline-v1" {
		writeError(w, http.StatusUnprocessableEntity, "unsupported_runtime", "Only browser-agent-outline-v1 packages are accepted.")
		return
	}
	if pkg.ExecutableScriptBundle.BrowserAgentContract == nil || pkg.ExecutableScriptBundle.BrowserAgentContract.OutcomeVerifierRulesVersion != model.BrowserAgentOutcomeVerifierRulesVersion {
		writeError(w, http.StatusUnprocessableEntity, "outcome_verifier_rules_mismatch", "Package outcome verifier rules version is unsupported.")
		return
	}
	canonical, _ := model.CanonicalJSON(pkg)
	packageDigest := model.SHA256Hex(canonical)
	jobID := "job_" + shortHash(lease.lease.InstallationID+"|"+pkg.PackageID+"|"+packageDigest)
	g.mu.Lock()
	existing := g.jobs[jobID]
	if existing != nil && existing.InstallationID == lease.lease.InstallationID && existing.PackageDigest == packageDigest && existing.Status.PackageID == pkg.PackageID {
		lease.seenMessages[messageKey] = true
		status := existing.Status
		g.mu.Unlock()
		receipt := model.DirectPackageReceipt{ProtocolVersion: model.DirectTransportProtocolVersion, JobID: jobID, PackageID: pkg.PackageID, PackageDigest: packageDigest, Status: status.Status, Stage: status.Stage, AcceptedAt: existing.AcceptedAt}
		g.writeEncrypted(w, http.StatusOK, lease, "package_receipt", receipt)
		return
	}
	g.mu.Unlock()
	record, err := g.persistNewJob(jobID, lease.lease.LeaseID, lease.lease.InstallationID, pkg, packageDigest)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "spool_write_failed", "Package could not be written to the Browser Agent spool.")
		return
	}
	g.mu.Lock()
	lease.seenMessages[messageKey] = true
	g.jobs[jobID] = record
	g.mu.Unlock()
	receipt := model.DirectPackageReceipt{ProtocolVersion: model.DirectTransportProtocolVersion, JobID: jobID, PackageID: pkg.PackageID, PackageDigest: packageDigest, Status: record.Status.Status, Stage: record.Status.Stage, AcceptedAt: record.AcceptedAt}
	g.writeEncrypted(w, http.StatusAccepted, lease, "package_receipt", receipt)
}

func (g *Gateway) handleCredential(w http.ResponseWriter, r *http.Request, lease *leaseRuntime) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "credential_message_too_large", "Encrypted credential message exceeds the transport limit.")
		return
	}
	if err := g.authenticateDataRequest(r, lease, body); err != nil {
		writeError(w, http.StatusUnauthorized, "direct_request_invalid", err.Error())
		return
	}
	var message model.DirectEncryptedMessage
	if json.Unmarshal(body, &message) != nil {
		writeError(w, http.StatusBadRequest, "invalid_encrypted_message", "Encrypted credential message is invalid.")
		return
	}
	messageKey := message.MessageID + "|" + message.NonceBase64
	g.mu.Lock()
	if lease.seenMessages[messageKey] {
		g.mu.Unlock()
		writeError(w, http.StatusConflict, "replay_detected", "Encrypted credential message was already accepted.")
		return
	}
	g.mu.Unlock()
	var envelope model.DirectCredentialEnvelope
	if err := model.DecryptDirectTransportJSON(message, lease.lease, "credential_envelope", model.DirectTransportDirectionUpload, g.now(), &envelope); err != nil {
		writeError(w, http.StatusBadRequest, "credential_decryption_failed", "Credential envelope authentication failed.")
		return
	}
	jobID := r.PathValue("job_id")
	g.mu.Lock()
	record, ok := g.jobs[jobID]
	g.mu.Unlock()
	if !ok || record.InstallationID != lease.lease.InstallationID {
		writeError(w, http.StatusNotFound, "job_not_found", "Browser Agent job was not found.")
		return
	}
	if record.Status.Status != "awaiting_credentials" {
		writeError(w, http.StatusConflict, "credential_not_expected", "Job is not waiting for a credential grant.")
		return
	}
	pkg, err := readStoredPackage(record.PackagePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "package_invalid", "Stored package is invalid.")
		return
	}
	grant, err := validateDirectCredentialEnvelope(envelope, lease.lease, record, &pkg, g.now())
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "credential_binding_invalid", err.Error())
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	current := g.jobs[jobID]
	if current == nil || current.Status.Status != "awaiting_credentials" {
		writeError(w, http.StatusConflict, "credential_not_expected", "Job is not waiting for a credential grant.")
		return
	}
	credential := envelope.Credential
	current.Credential = &credential
	current.CredentialGrantID = grant.GrantID
	current.Status.Status = "queued"
	current.Status.Stage = "browser_agent_queue"
	current.Status.Message = "Credential grant is held in memory; package is waiting for Browser Agent claim."
	current.Status.ProgressPercent = 5
	current.Status.UpdatedAt = g.now().UTC()
	lease.seenMessages[messageKey] = true
	if err := g.persistJob(current); err != nil {
		current.Credential = nil
		current.CredentialGrantID = ""
		writeError(w, http.StatusInternalServerError, "job_state_write_failed", "Credential readiness state could not be persisted.")
		return
	}
	receipt := model.DirectCredentialReceipt{ProtocolVersion: model.DirectTransportProtocolVersion, JobID: jobID, PackageID: pkg.PackageID, GrantID: grant.GrantID, SecretRef: grant.CloudSecretRef, Status: "queued", Stage: "browser_agent_queue", AcceptedAt: current.Status.UpdatedAt}
	g.writeEncrypted(w, http.StatusAccepted, lease, "credential_receipt", receipt)
}

func (g *Gateway) handleJobStatus(w http.ResponseWriter, r *http.Request, lease *leaseRuntime) {
	if err := g.authenticateDataRequest(r, lease, nil); err != nil {
		writeError(w, http.StatusUnauthorized, "direct_request_invalid", err.Error())
		return
	}
	record, ok := g.jobForLease(r.PathValue("job_id"), lease.lease.LeaseID)
	if !ok {
		writeError(w, http.StatusNotFound, "job_not_found", "Browser Agent job was not found.")
		return
	}
	g.writeEncrypted(w, http.StatusOK, lease, "job_status", record.Status)
}

func (g *Gateway) handleJobPackage(w http.ResponseWriter, r *http.Request, lease *leaseRuntime) {
	if err := g.authenticateDataRequest(r, lease, nil); err != nil {
		writeError(w, http.StatusUnauthorized, "direct_request_invalid", err.Error())
		return
	}
	record, ok := g.jobForLease(r.PathValue("job_id"), lease.lease.LeaseID)
	if !ok {
		writeError(w, http.StatusNotFound, "job_not_found", "Browser Agent job was not found.")
		return
	}
	pkg, err := readStoredPackage(record.PackagePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "package_invalid", "Stored source package is invalid.")
		return
	}
	g.writeEncrypted(w, http.StatusOK, lease, "source_execution_package", pkg)
}

func (g *Gateway) handleJobResult(w http.ResponseWriter, r *http.Request, lease *leaseRuntime) {
	if err := g.authenticateDataRequest(r, lease, nil); err != nil {
		writeError(w, http.StatusUnauthorized, "direct_request_invalid", err.Error())
		return
	}
	record, ok := g.jobForLease(r.PathValue("job_id"), lease.lease.LeaseID)
	if !ok || record.ResultPath == "" {
		writeError(w, http.StatusNotFound, "result_not_ready", "Browser Agent result is not ready.")
		return
	}
	data, err := os.ReadFile(record.ResultPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "result_read_failed", "Browser Agent result could not be read.")
		return
	}
	var result model.RecordingResultPackage
	if err := json.Unmarshal(data, &result); err != nil {
		writeError(w, http.StatusInternalServerError, "result_invalid", "Browser Agent result is invalid.")
		return
	}
	g.writeEncrypted(w, http.StatusOK, lease, "recording_result", result)
}

func (g *Gateway) handleArtifact(w http.ResponseWriter, r *http.Request, lease *leaseRuntime) {
	if err := g.authenticateDataRequest(r, lease, nil); err != nil {
		writeError(w, http.StatusUnauthorized, "direct_request_invalid", err.Error())
		return
	}
	writeError(w, http.StatusGone, "artifact_chunking_required", "Use the authenticated chunk endpoint for Browser Agent artifacts.")
}

func (g *Gateway) handleArtifactChunk(w http.ResponseWriter, r *http.Request, lease *leaseRuntime) {
	if err := g.authenticateDataRequest(r, lease, nil); err != nil {
		writeError(w, http.StatusUnauthorized, "direct_request_invalid", err.Error())
		return
	}
	record, ok := g.jobForLease(r.PathValue("job_id"), lease.lease.LeaseID)
	if !ok {
		writeError(w, http.StatusNotFound, "job_not_found", "Browser Agent job was not found.")
		return
	}
	artifact, ok := record.Artifacts[r.PathValue("artifact_id")]
	if !ok {
		writeError(w, http.StatusNotFound, "artifact_not_found", "Browser Agent artifact was not found.")
		return
	}
	chunkIndex, err := strconv.ParseInt(r.PathValue("chunk_index"), 10, 64)
	if err != nil || chunkIndex < 0 {
		writeError(w, http.StatusBadRequest, "artifact_chunk_invalid", "Artifact chunk index is invalid.")
		return
	}
	offset := chunkIndex * int64(model.DirectArtifactChunkBytes)
	if offset >= artifact.Artifact.SizeBytes {
		writeError(w, http.StatusRequestedRangeNotSatisfiable, "artifact_chunk_out_of_range", "Artifact chunk is outside the file.")
		return
	}
	file, err := os.Open(artifact.Path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "artifact_read_failed", "Browser Agent artifact could not be read.")
		return
	}
	defer file.Close()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		writeError(w, http.StatusInternalServerError, "artifact_read_failed", "Browser Agent artifact chunk could not be read.")
		return
	}
	remaining := artifact.Artifact.SizeBytes - offset
	chunkSize := int64(model.DirectArtifactChunkBytes)
	if remaining < chunkSize {
		chunkSize = remaining
	}
	data := make([]byte, chunkSize)
	if _, err := io.ReadFull(file, data); err != nil {
		writeError(w, http.StatusInternalServerError, "artifact_read_failed", "Browser Agent artifact chunk could not be read.")
		return
	}
	messageType := "artifact_chunk:" + artifact.Artifact.ArtifactID + ":" + strconv.FormatInt(chunkIndex, 10)
	g.writeEncrypted(w, http.StatusOK, lease, messageType, data)
}

func (g *Gateway) writeEncrypted(w http.ResponseWriter, status int, lease *leaseRuntime, messageType string, value any) {
	messageIDToken, err := model.NewDirectTransportToken(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encryption_failed", "Response encryption failed.")
		return
	}
	var message model.DirectEncryptedMessage
	if bytes, ok := value.([]byte); ok {
		message, err = model.EncryptDirectTransportBytes(bytes, lease.lease, "response_"+messageIDToken, messageType, model.DirectTransportDirectionResult, g.now())
	} else {
		message, err = model.EncryptDirectTransportJSON(value, lease.lease, "response_"+messageIDToken, messageType, model.DirectTransportDirectionResult, g.now())
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encryption_failed", "Response encryption failed.")
		return
	}
	writeJSON(w, status, message)
}

func (g *Gateway) persistNewJob(jobID, leaseID, installationID string, pkg model.ClientExecutionPackage, digest string) (*jobRecord, error) {
	dir := filepath.Join(g.config.SpoolRoot, "jobs", safeSegment(jobID))
	if err := os.MkdirAll(filepath.Join(dir, "artifacts"), 0o700); err != nil {
		return nil, err
	}
	packagePath := filepath.Join(dir, "package.json")
	if err := writeJSONFile(packagePath, pkg); err != nil {
		return nil, err
	}
	status := model.DirectJobStatus{ProtocolVersion: model.DirectTransportProtocolVersion, JobID: jobID, PackageID: pkg.PackageID, Status: "queued", Stage: "browser_agent_queue", Message: "Validated package is waiting for Browser Agent claim.", ProgressPercent: 5, UpdatedAt: g.now().UTC()}
	if len(pkg.CredentialGrants) > 0 {
		status.Status = "awaiting_credentials"
		status.Stage = "credential_grant_required"
		status.Message = "Validated package is waiting for its separately encrypted credential grant."
	}
	record := &jobRecord{Status: status, LeaseID: leaseID, InstallationID: installationID, PackageDigest: digest, AcceptedAt: status.UpdatedAt, PackagePath: packagePath, Artifacts: map[string]artifactRecord{}}
	if err := g.persistJob(record); err != nil {
		return nil, err
	}
	return record, nil
}

func (g *Gateway) persistJob(record *jobRecord) error {
	if record == nil || strings.TrimSpace(record.Status.JobID) == "" {
		return errors.New("job record is required")
	}
	decorateDirectJobStatus(&record.Status)
	artifacts := make(map[string]model.DirectArtifact, len(record.Artifacts))
	for id, item := range record.Artifacts {
		artifacts[id] = item.Artifact
	}
	persisted := persistedJobRecord{
		Status: record.Status, LeaseID: record.LeaseID, InstallationID: record.InstallationID,
		PackageDigestSHA256: record.PackageDigest, AcceptedAt: record.AcceptedAt, HasResult: record.ResultPath != "", Artifacts: artifacts,
		DeliveryAcked: record.DeliveryAcked, AckedAt: record.AckedAt, AckedArtifactIDs: append([]string(nil), record.AckedArtifactIDs...),
	}
	dir := filepath.Join(g.config.SpoolRoot, "jobs", safeSegment(record.Status.JobID))
	return writeJSONFile(filepath.Join(dir, "job.json"), persisted)
}

func decorateDirectJobStatus(status *model.DirectJobStatus) {
	if status == nil {
		return
	}
	status.WaitingReason = ""
	status.NextAction = ""
	status.RequiresReapproval = false
	switch status.Status {
	case "awaiting_credentials":
		status.WaitingReason = "credential_envelope_required"
		status.NextAction = "upload_credential_envelope"
	case "awaiting_manual_login":
		status.WaitingReason = "manual_login_checkpoint"
		status.NextAction = "complete_manual_login"
	case "queued":
		status.WaitingReason = "worker_claim_pending"
		status.NextAction = "wait_for_browser_agent"
	case "running":
		status.NextAction = "wait_for_current_stage"
	case "completed":
		status.NextAction = "download_and_verify_artifacts"
	case "failed":
		if status.BlockingErrorCode == "reunderstanding_required" {
			status.NextAction = "regenerate_package_from_structured_issues"
			status.RequiresReapproval = true
			if status.Message == "" {
				status.Message = "Browser Agent validation requires App re-understanding; the original job is terminal."
			}
		} else {
			if status.BlockingErrorCode == "" {
				status.BlockingErrorCode = "browser_agent_execution_failed"
			}
			status.NextAction = "regenerate_and_reapprove_package"
			status.RequiresReapproval = true
		}
	case "canceled", "expired":
		status.NextAction = "regenerate_and_reapprove_package"
		status.RequiresReapproval = true
	}
}

// directResultRequiresReunderstanding uses only the authoritative validation
// decision, never natural-language failure text. A reunderstanding decision
// takes precedence over ordinary failed diagnostics because it defines the
// App's next lifecycle transition.
func directResultRequiresReunderstanding(result model.RecordingResultPackage) bool {
	for _, report := range result.ValidationReports {
		if report.Decision == model.ValidationDecisionReunderstandingRequired {
			return true
		}
	}
	return false
}

func directReunderstandingIssues(result model.RecordingResultPackage) []model.DirectReunderstandingIssue {
	if !directResultRequiresReunderstanding(result) {
		return nil
	}
	issues := make([]model.DirectReunderstandingIssue, 0, 8)
	seen := map[string]struct{}{}
	for _, report := range result.ValidationReports {
		for _, check := range report.Checks {
			if check.Passed || (!check.Required && check.Severity != model.FindingSeverityBlocking && report.Decision != model.ValidationDecisionReunderstandingRequired) {
				continue
			}
			code := strings.TrimSpace(check.Code)
			if code == "" {
				code = "validation_check_failed"
			}
			stageID := firstNonEmptyDirect(check.StageID, report.StageID)
			nodeID := firstNonEmptyDirect(check.NodeID, report.NodeID)
			key := code + "|" + stageID + "|" + nodeID
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			severity := check.Severity
			if severity == "" {
				severity = model.FindingSeverityBlocking
			}
			issue := model.DirectReunderstandingIssue{
				Code: code, StageID: stageID, NodeID: nodeID, Severity: severity, Required: check.Required,
				Summary: sanitizeDirectIssueText(check.Summary), Impact: sanitizeDirectIssueText(check.Impact),
				Suggestion: sanitizeDirectIssueText(check.Suggestion), NextStep: sanitizeDirectIssueText(check.NextStep),
				ResponsibilityDomain: check.ResponsibilityDomain,
				EvidenceIDs:          directEvidenceIDs(check.EvidenceRefs),
			}
			issue.IssueID = model.StableDirectReunderstandingIssueID(issue)
			issues = append(issues, issue)
			if len(issues) >= 32 {
				return issues
			}
		}
	}
	if len(issues) == 0 {
		issue := model.DirectReunderstandingIssue{Code: "reunderstanding_required", Severity: model.FindingSeverityBlocking, Required: true, Summary: "运行时验证要求 App 重新理解并重新审批执行方案。"}
		issue.IssueID = model.StableDirectReunderstandingIssueID(issue)
		issues = append(issues, issue)
	}
	return issues
}

func directEvidenceIDs(refs []model.EvidenceRef) []string {
	ids := make([]string, 0, len(refs))
	seen := map[string]struct{}{}
	for _, ref := range refs {
		id := strings.TrimSpace(ref.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func sanitizeDirectIssueText(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\r", " "), "\n", " "))
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	for _, token := range []string{"bearer ", "authorization:", "authorization=", "cookie:", "cookie=", "password", "密码", "api key", "apikey", "secret", "token", "私钥", "private key", "sk-"} {
		if strings.Contains(lower, token) {
			return "已脱敏的结构化诊断信息"
		}
	}
	runes := []rune(value)
	if len(runes) > 512 {
		return string(runes[:512])
	}
	return value
}

func firstNonEmptyDirect(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func applyDirectResultStatus(status *model.DirectJobStatus, result model.RecordingResultPackage) {
	if status == nil {
		return
	}
	status.ResultPackageID = result.ResultID
	status.ReunderstandingIssues = nil
	if result.Status == model.RecordingResultStatusFailed {
		status.Status = "failed"
		status.Stage = "failed"
		if directResultRequiresReunderstanding(result) {
			status.BlockingErrorCode = "reunderstanding_required"
			status.Message = "Browser Agent validation requires App re-understanding; the original job is terminal."
			status.ReunderstandingIssues = directReunderstandingIssues(result)
		} else {
			status.Message = "Browser Agent returned a validated failure result package."
			if result.FailureDiagnostic != nil {
				status.BlockingErrorCode = result.FailureDiagnostic.Error.Code
			}
		}
	} else {
		status.Status = "completed"
		status.Stage = "completed"
		status.Message = "Browser Agent returned a validated result package."
		status.BlockingErrorCode = ""
	}
	status.ProgressPercent = 100
	decorateDirectJobStatus(status)
}

func (g *Gateway) loadPersistedJobs() error {
	root := filepath.Join(g.config.SpoolRoot, "jobs")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(root, 0o700)
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		data, readErr := os.ReadFile(filepath.Join(dir, "job.json"))
		if readErr != nil {
			continue
		}
		var persisted persistedJobRecord
		if json.Unmarshal(data, &persisted) != nil || persisted.Status.JobID == "" || persisted.InstallationID == "" {
			continue
		}
		packagePath := filepath.Join(dir, "package.json")
		if _, statErr := os.Stat(packagePath); statErr != nil {
			continue
		}
		record := &jobRecord{
			Status: persisted.Status, LeaseID: persisted.LeaseID, InstallationID: persisted.InstallationID,
			PackageDigest: persisted.PackageDigestSHA256, AcceptedAt: persisted.AcceptedAt, PackagePath: packagePath, Artifacts: map[string]artifactRecord{},
			DeliveryAcked: persisted.DeliveryAcked, AckedAt: persisted.AckedAt, AckedArtifactIDs: append([]string(nil), persisted.AckedArtifactIDs...),
		}
		if record.AcceptedAt.IsZero() {
			record.AcceptedAt = record.Status.UpdatedAt
		}
		if persisted.HasResult {
			resultPath := filepath.Join(dir, "result.json")
			if _, statErr := os.Stat(resultPath); statErr == nil {
				record.ResultPath = resultPath
			}
		}
		for id, artifact := range persisted.Artifacts {
			path := filepath.Join(dir, "artifacts", safeSegment(id)+"-"+safeSegment(artifact.FileName))
			if info, statErr := os.Stat(path); statErr == nil && info.Mode().IsRegular() && info.Size() == artifact.SizeBytes {
				record.Artifacts[id] = artifactRecord{Artifact: artifact, Path: path}
			}
		}
		requiresCredential := packageRequiresCredentials(packagePath)
		if requiresCredential && (record.Status.Status == "running" || record.Status.Status == "queued") {
			record.Status.Status = "awaiting_credentials"
			record.Status.Stage = "credential_reupload_required"
			record.Status.Message = "Gateway restarted; the in-memory credential grant must be uploaded again."
			record.Status.ProgressPercent = 5
			record.Status.UpdatedAt = g.now().UTC()
			_ = g.persistJob(record)
		} else if record.Status.Status == "running" {
			record.Status.Status = "queued"
			record.Status.Stage = "recovered_after_restart"
			record.Status.Message = "Gateway restarted; job is available for Browser Agent reclaim."
			record.Status.ProgressPercent = 5
			record.Status.UpdatedAt = g.now().UTC()
			_ = g.persistJob(record)
		}
		g.jobs[record.Status.JobID] = record
	}
	return nil
}

func (g *Gateway) workerAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !bearerMatches(r, g.config.WorkerToken) {
			writeError(w, http.StatusUnauthorized, "worker_auth_invalid", "Worker token is invalid.")
			return
		}
		next(w, r)
	}
}

func (g *Gateway) handleWorkerControl(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	record := g.jobs[r.PathValue("job_id")]
	if record == nil {
		g.mu.Unlock()
		writeError(w, http.StatusNotFound, "job_not_found", "Job was not found.")
		return
	}
	status := record.Status.Status
	jobID := record.Status.JobID
	g.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"protocol_version": model.DirectWorkerProtocolVersion,
		"job_id":           jobID,
		"status":           status,
		"cancel_requested": status == "canceled",
	})
}

func (g *Gateway) handleWorkerClaim(w http.ResponseWriter, _ *http.Request) {
	g.mu.Lock()
	ids := make([]string, 0)
	for id, record := range g.jobs {
		if record.Status.Status == "queued" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		g.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	record := g.jobs[ids[0]]
	record.Status.Status = "running"
	record.Status.Stage = "claimed"
	record.Status.Message = "Browser Agent claimed the package."
	record.Status.ProgressPercent = 10
	record.Status.UpdatedAt = g.now().UTC()
	jobID := ids[0]
	if err := g.persistJob(record); err != nil {
		record.Status.Status = "queued"
		g.mu.Unlock()
		writeError(w, http.StatusInternalServerError, "job_state_write_failed", "Claimed job state could not be persisted.")
		return
	}
	g.mu.Unlock()
	data, err := os.ReadFile(record.PackagePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "package_read_failed", "Package could not be read.")
		return
	}
	var pkg model.ClientExecutionPackage
	if err := json.Unmarshal(data, &pkg); err != nil {
		writeError(w, http.StatusInternalServerError, "package_invalid", "Stored package is invalid.")
		return
	}
	writeJSON(w, http.StatusOK, WorkerJob{ProtocolVersion: model.DirectWorkerProtocolVersion, JobID: jobID, LeaseID: record.LeaseID, Package: pkg})
}

func (g *Gateway) handleWorkerCredentialConsume(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	record, ok := g.jobs[r.PathValue("job_id")]
	if !ok {
		writeError(w, http.StatusNotFound, "job_not_found", "Job was not found.")
		return
	}
	if record.Status.Status != "running" {
		writeError(w, http.StatusConflict, "job_not_claimed", "Credential may be consumed only by the worker that claimed the running job.")
		return
	}
	if record.Credential == nil || record.CredentialGrantID == "" {
		record.Status.Status = "awaiting_credentials"
		record.Status.Stage = "credential_reupload_required"
		record.Status.Message = "The in-memory credential grant is unavailable and must be uploaded again."
		record.Status.ProgressPercent = 5
		record.Status.UpdatedAt = g.now().UTC()
		_ = g.persistJob(record)
		writeError(w, http.StatusGone, "credential_grant_unavailable", "The in-memory credential grant is unavailable and must be uploaded again.")
		return
	}
	if !g.now().UTC().Before(record.Credential.ExpiresAt) {
		record.Credential = nil
		record.CredentialGrantID = ""
		record.Status.Status = "awaiting_credentials"
		record.Status.Stage = "credential_reupload_required"
		record.Status.Message = "The in-memory credential grant expired and must be uploaded again."
		record.Status.ProgressPercent = 5
		record.Status.UpdatedAt = g.now().UTC()
		_ = g.persistJob(record)
		writeError(w, http.StatusGone, "credential_grant_expired", "The in-memory credential grant expired.")
		return
	}
	value := WorkerCredential{JobID: record.Status.JobID, PackageID: record.Status.PackageID, GrantID: record.CredentialGrantID, Credential: *record.Credential}
	record.Credential = nil
	record.CredentialGrantID = ""
	writeJSON(w, http.StatusOK, value)
}

func (g *Gateway) handleWorkerRelease(w http.ResponseWriter, r *http.Request) {
	var request WorkerReleaseRequest
	if err := decodeLimitedJSON(r.Body, 1<<20, &request); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_release", "Worker release request is invalid.")
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	record, ok := g.jobs[r.PathValue("job_id")]
	if !ok {
		writeError(w, http.StatusNotFound, "job_not_found", "Job was not found.")
		return
	}
	if record.Status.Status != "running" {
		writeError(w, http.StatusConflict, "job_not_claimed", "Only a claimed running job may be released.")
		return
	}
	record.Credential = nil
	record.CredentialGrantID = ""
	if packageRequiresCredentials(record.PackagePath) {
		record.Status.Status = "awaiting_credentials"
		record.Status.Stage = "credential_reupload_required"
		record.Status.Message = "Worker released the job; its in-memory credential grant must be uploaded again."
	} else {
		record.Status.Status = "queued"
		record.Status.Stage = "worker_released"
		record.Status.Message = "Worker released the job; it is available for another claim."
	}
	record.Status.ProgressPercent = 5
	record.Status.UpdatedAt = g.now().UTC()
	if err := g.persistJob(record); err != nil {
		writeError(w, http.StatusInternalServerError, "job_state_write_failed", "Released job state could not be persisted.")
		return
	}
	writeJSON(w, http.StatusOK, record.Status)
}

func (g *Gateway) handleWorkerStatus(w http.ResponseWriter, r *http.Request) {
	var update WorkerStatusUpdate
	if err := decodeLimitedJSON(r.Body, 1<<20, &update); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_status", err.Error())
		return
	}
	update.Status = strings.TrimSpace(update.Status)
	update.Stage = strings.TrimSpace(update.Stage)
	if update.Status != "running" || update.Stage == "" || strings.TrimSpace(update.ResultPackageID) != "" || update.ProgressPercent < 1 || update.ProgressPercent >= 100 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_status_transition", "Worker progress updates must remain running, omit result_package_id, and use progress 1-99.")
		return
	}
	g.mu.Lock()
	record, ok := g.jobs[r.PathValue("job_id")]
	if ok && record.Status.Status != "running" {
		g.mu.Unlock()
		writeError(w, http.StatusConflict, "job_not_claimed", "Worker may update only a claimed running job.")
		return
	}
	if ok {
		record.Status.Status = update.Status
		record.Status.Stage = update.Stage
		record.Status.Message = update.Message
		record.Status.ProgressPercent = update.ProgressPercent
		record.Status.ResultPackageID = ""
		record.Status.UpdatedAt = g.now().UTC()
	}
	if !ok {
		g.mu.Unlock()
		writeError(w, http.StatusNotFound, "job_not_found", "Job was not found.")
		return
	}
	if err := g.persistJob(record); err != nil {
		g.mu.Unlock()
		writeError(w, http.StatusInternalServerError, "job_state_write_failed", "Job status could not be persisted.")
		return
	}
	status := record.Status
	g.mu.Unlock()
	writeJSON(w, http.StatusOK, status)
}

func (g *Gateway) handleWorkerResult(w http.ResponseWriter, r *http.Request) {
	var result model.RecordingResultPackage
	if err := decodeLimitedJSON(r.Body, maxPackageBodyBytes, &result); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_result", err.Error())
		return
	}
	g.mu.Lock()
	record, ok := g.jobs[r.PathValue("job_id")]
	g.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "job_not_found", "Job was not found.")
		return
	}
	resultRecovery := r.Header.Get("X-Result-Recovery") == "finalization-result-v1"
	if record.Status.Status != "running" && !(resultRecovery && record.Status.Status == "awaiting_credentials") {
		writeError(w, http.StatusConflict, "job_not_claimed", "Worker may return a result only for a claimed running job.")
		return
	}
	if result.SourcePackageID != record.Status.PackageID || result.CloudJobID != record.Status.JobID || strings.TrimSpace(result.ResultID) == "" {
		writeError(w, http.StatusUnprocessableEntity, "result_binding_mismatch", "Result does not belong to the claimed package.")
		return
	}
	packageData, err := os.ReadFile(record.PackagePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "package_read_failed", "Source package could not be read for result validation.")
		return
	}
	var sourcePackage model.ClientExecutionPackage
	if err := json.Unmarshal(packageData, &sourcePackage); err != nil {
		writeError(w, http.StatusInternalServerError, "package_invalid", "Source package is invalid.")
		return
	}
	if err := result.ValidateStatusContract(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "result_status_invalid", err.Error())
		return
	}
	if result.Status != model.RecordingResultStatusFailed {
		if directResultRequiresReunderstanding(result) {
			writeError(w, http.StatusUnprocessableEntity, "result_validation_failed", "reunderstanding_required results must terminate the job as a failed diagnostic package.")
			return
		}
		if err := model.ValidateRecordingResultPackageForRender(&result, &sourcePackage); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "result_validation_failed", err.Error())
			return
		}
	}
	if err := validateResultArtifactsAgainstUploads(result, record.Artifacts); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "result_artifact_binding_invalid", err.Error())
		return
	}
	if err := model.ValidateFormalRecordingResultArtifacts(&result, &sourcePackage); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "result_artifact_completeness_failed", err.Error())
		return
	}
	if err := validateFormalResultArtifactContents(result, sourcePackage, record.Status.JobID, record.Artifacts); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "result_artifact_content_invalid", err.Error())
		return
	}
	if err := validateDirectResultSanitization(result); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "result_contains_local_runtime_data", err.Error())
		return
	}
	path := filepath.Join(g.config.SpoolRoot, "jobs", safeSegment(record.Status.JobID), "result.json")
	if err := writeJSONFile(path, result); err != nil {
		writeError(w, http.StatusInternalServerError, "result_write_failed", "Result could not be stored.")
		return
	}
	g.mu.Lock()
	record.ResultPath = path
	applyDirectResultStatus(&record.Status, result)
	record.Status.UpdatedAt = g.now().UTC()
	if err := g.persistJob(record); err != nil {
		g.mu.Unlock()
		writeError(w, http.StatusInternalServerError, "job_state_write_failed", "Result state could not be persisted.")
		return
	}
	g.mu.Unlock()
	writeJSON(w, http.StatusOK, record.Status)
}

func validateDirectResultSanitization(result model.RecordingResultPackage) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	text := strings.ToLower(string(encoded))
	for _, forbidden := range []string{"dev_local_artifact", "local-dev/result-key", "file://", "/var/lib/", "/home/", "/root/"} {
		if strings.Contains(text, forbidden) {
			return fmt.Errorf("formal direct result contains forbidden local runtime marker")
		}
	}
	return nil
}

func validateResultArtifactsAgainstUploads(result model.RecordingResultPackage, uploaded map[string]artifactRecord) error {
	refs := append([]model.ArtifactRef(nil), result.GeneratedAssets...)
	for _, step := range result.StepResults {
		refs = append(refs, step.Artifacts...)
	}
	if result.ExecutionTrace != nil {
		refs = append(refs, result.ExecutionTrace.Artifacts...)
		for _, step := range result.ExecutionTrace.StepResults {
			refs = append(refs, step.Artifacts...)
		}
	}
	if result.StageEventLogRef != nil {
		refs = append(refs, *result.StageEventLogRef)
	}
	if result.FailureDiagnostic != nil {
		for _, descriptor := range append(append([]model.PackageArtifactDescriptor{}, result.FailureDiagnostic.ScreenshotRefs...), result.FailureDiagnostic.TraceRefs...) {
			refs = append(refs, model.ArtifactRef{ID: descriptor.ID, Kind: descriptor.Kind, URI: descriptor.URI, MimeType: descriptor.MimeType, SHA256: descriptor.SHA256, SizeBytes: descriptor.SizeBytes})
		}
		for _, descriptor := range []*model.PackageArtifactDescriptor{result.FailureDiagnostic.DOMSnapshotRef, result.FailureDiagnostic.AccessibilitySnapshotRef} {
			if descriptor != nil {
				refs = append(refs, model.ArtifactRef{ID: descriptor.ID, Kind: descriptor.Kind, URI: descriptor.URI, MimeType: descriptor.MimeType, SHA256: descriptor.SHA256, SizeBytes: descriptor.SizeBytes})
			}
		}
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		if ref.ID == "" || seen[ref.ID] {
			continue
		}
		seen[ref.ID] = true
		artifact, ok := uploaded[ref.ID]
		if !ok || !hmac.Equal([]byte(strings.ToLower(ref.SHA256)), []byte(strings.ToLower(artifact.Artifact.SHA256))) || ref.SizeBytes != artifact.Artifact.SizeBytes {
			return fmt.Errorf("result artifact %s is missing or does not match its uploaded bytes", safeSegment(ref.ID))
		}
	}
	for _, delivery := range result.Delivery.AssetRefs {
		artifact, ok := uploaded[delivery.ID]
		if !ok || delivery.SHA256 != artifact.Artifact.SHA256 || delivery.SizeBytes != artifact.Artifact.SizeBytes {
			return fmt.Errorf("delivery asset %s is missing or does not match its uploaded bytes", safeSegment(delivery.ID))
		}
		seen[delivery.ID] = true
	}
	for id := range uploaded {
		if !seen[id] {
			return fmt.Errorf("uploaded artifact %s is not referenced by the result package", safeSegment(id))
		}
	}
	return nil
}

func (g *Gateway) handleWorkerArtifact(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("job_id")
	artifactID := safeSegment(r.PathValue("artifact_id"))
	recovery := r.Header.Get("X-Artifact-Recovery") == "stage-event-log-v1"
	g.mu.Lock()
	record, ok := g.jobs[jobID]
	g.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "job_not_found", "Job was not found.")
		return
	}
	if recovery {
		expectedID := "stage_event_log_" + safeSegment(jobID)
		if record.Status.Status != "awaiting_credentials" || artifactID != expectedID || r.Header.Get("X-Artifact-Kind") != "browser_agent_stage_event_log" || r.Header.Get("Content-Type") != "application/x-ndjson" {
			writeError(w, http.StatusConflict, "job_not_awaiting_recovery", "Only the bounded stage-event log may be recovered for an interrupted credential-gated job.")
			return
		}
	} else if record.Status.Status != "running" {
		writeError(w, http.StatusConflict, "job_not_claimed", "Worker may upload artifacts only for a claimed running job.")
		return
	}
	expected := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Artifact-SHA256")))
	if len(expected) != sha256.Size*2 {
		writeError(w, http.StatusUnprocessableEntity, "artifact_checksum_mismatch", "Artifact checksum is missing or invalid.")
		return
	}
	fileName := safeSegment(r.URL.Query().Get("file_name"))
	if fileName == "" {
		fileName = artifactID
	}
	path := filepath.Join(g.config.SpoolRoot, "jobs", safeSegment(jobID), "artifacts", artifactID+"-"+fileName)
	temporary := path + ".part"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "artifact_write_failed", "Artifact could not be stored.")
		return
	}
	hasher := sha256.New()
	uploadLimit := int64(maxWorkerArtifactBytes)
	if recovery {
		uploadLimit = 1 << 20
	}
	written, copyErr := io.Copy(io.MultiWriter(file, hasher), io.LimitReader(r.Body, uploadLimit+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written > uploadLimit {
		_ = os.Remove(temporary)
		writeError(w, http.StatusRequestEntityTooLarge, "artifact_too_large", "Artifact exceeds the transport limit.")
		return
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(actual)) {
		_ = os.Remove(temporary)
		writeError(w, http.StatusUnprocessableEntity, "artifact_checksum_mismatch", "Artifact checksum is missing or invalid.")
		return
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		writeError(w, http.StatusInternalServerError, "artifact_write_failed", "Artifact could not be finalized.")
		return
	}
	artifact := model.DirectArtifact{ArtifactID: artifactID, Role: r.Header.Get("X-Artifact-Role"), Kind: r.Header.Get("X-Artifact-Kind"), FileName: fileName, MimeType: r.Header.Get("Content-Type"), SHA256: actual, SizeBytes: written}
	g.mu.Lock()
	record.Artifacts[artifactID] = artifactRecord{Artifact: artifact, Path: path}
	record.Status.Artifacts = appendOrReplaceArtifact(record.Status.Artifacts, artifact)
	record.Status.UpdatedAt = g.now().UTC()
	if err := g.persistJob(record); err != nil {
		g.mu.Unlock()
		writeError(w, http.StatusInternalServerError, "job_state_write_failed", "Artifact state could not be persisted.")
		return
	}
	g.mu.Unlock()
	writeJSON(w, http.StatusCreated, artifact)
}

func (g *Gateway) jobForLease(jobID, leaseID string) (*jobRecord, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	record, ok := g.jobs[jobID]
	if !ok {
		return nil, false
	}
	lease, leaseOK := g.leases[leaseID]
	if !leaseOK || record.InstallationID == "" || record.InstallationID != lease.lease.InstallationID {
		return nil, false
	}
	copyRecord := *record
	copyRecord.Status.Artifacts = append([]model.DirectArtifact(nil), record.Status.Artifacts...)
	copyRecord.Artifacts = make(map[string]artifactRecord, len(record.Artifacts))
	for id, artifact := range record.Artifacts {
		copyRecord.Artifacts[id] = artifact
	}
	return &copyRecord, true
}

func readStoredPackage(path string) (model.ClientExecutionPackage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return model.ClientExecutionPackage{}, err
	}
	var pkg model.ClientExecutionPackage
	if err := json.Unmarshal(data, &pkg); err != nil {
		return model.ClientExecutionPackage{}, err
	}
	return pkg, nil
}

func packageRequiresCredentials(path string) bool {
	pkg, err := readStoredPackage(path)
	return err == nil && len(pkg.CredentialGrants) > 0
}

func validateDirectCredentialEnvelope(envelope model.DirectCredentialEnvelope, lease model.DirectPortLease, record *jobRecord, pkg *model.ClientExecutionPackage, now time.Time) (model.CredentialGrant, error) {
	if record == nil || pkg == nil || envelope.ProtocolVersion != model.DirectTransportProtocolVersion || envelope.LeaseID != lease.LeaseID || envelope.InstallationID != lease.InstallationID {
		return model.CredentialGrant{}, errors.New("credential lease or installation binding mismatch")
	}
	if envelope.JobID != record.Status.JobID || envelope.PackageID != pkg.PackageID || envelope.PackageDigest != record.PackageDigest {
		return model.CredentialGrant{}, errors.New("credential job or package binding mismatch")
	}
	if envelope.IssuedAt.IsZero() || envelope.ExpiresAt.IsZero() || envelope.IssuedAt.After(now.Add(model.DirectTransportMaxClockSkew)) || !now.Before(envelope.ExpiresAt) || envelope.ExpiresAt.After(lease.ExpiresAt) {
		return model.CredentialGrant{}, errors.New("credential grant time binding is invalid")
	}
	var grant *model.CredentialGrant
	for index := range pkg.CredentialGrants {
		candidate := &pkg.CredentialGrants[index]
		if candidate.GrantID == envelope.GrantID && candidate.CloudSecretRef == envelope.SecretRef {
			grant = candidate
			break
		}
	}
	if grant == nil || !grant.ExpiresAt.IsZero() && envelope.ExpiresAt.After(grant.ExpiresAt) {
		return model.CredentialGrant{}, errors.New("credential grant is not approved by the package")
	}
	if envelope.Credential.SecretRef != envelope.SecretRef || strings.TrimSpace(envelope.Credential.Username) == "" || envelope.Credential.Password == "" || envelope.Credential.ExpiresAt != envelope.ExpiresAt {
		return model.CredentialGrant{}, errors.New("credential value binding is invalid")
	}
	if !equalStringSets(envelope.AllowedDomains, grant.AllowedDomains) || !equalStringSets(envelope.AllowedOperations, grant.AllowedOperations) || !equalStringSets(envelope.Credential.AllowedDomains, grant.AllowedDomains) || !equalStringSets(envelope.Credential.AllowedOperations, grant.AllowedOperations) {
		return model.CredentialGrant{}, errors.New("credential scope exceeds the approved package grant")
	}
	return *grant, nil
}

func equalStringSets(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	a, b := append([]string(nil), left...), append([]string(nil), right...)
	for index := range a {
		a[index] = strings.ToLower(strings.TrimSpace(a[index]))
	}
	for index := range b {
		b[index] = strings.ToLower(strings.TrimSpace(b[index]))
	}
	sort.Strings(a)
	sort.Strings(b)
	for index := range a {
		if a[index] == "" || a[index] != b[index] {
			return false
		}
	}
	return true
}

func (g *Gateway) pruneNoncesLocked(now time.Time) {
	for key, expiry := range g.leaseNonces {
		if !now.Before(expiry) {
			delete(g.leaseNonces, key)
		}
	}
	for key, expiry := range g.requestNonces {
		if !now.Before(expiry) {
			delete(g.requestNonces, key)
		}
	}
}

func decodeLimitedJSON(reader io.Reader, max int64, target any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, max))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
func bearerMatches(r *http.Request, expected string) bool {
	provided := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	return provided != "" && hmac.Equal([]byte(provided), []byte(expected))
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}
func isLoopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
}
func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}
func safeSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return url.PathEscape(filepath.Base(value))
}
func appendOrReplaceArtifact(values []model.DirectArtifact, value model.DirectArtifact) []model.DirectArtifact {
	for i := range values {
		if values[i].ArtifactID == value.ArtifactID {
			values[i] = value
			return values
		}
	}
	return append(values, value)
}
