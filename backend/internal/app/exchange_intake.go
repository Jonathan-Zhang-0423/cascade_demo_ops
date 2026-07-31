package app

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cascade-demoops/backend/internal/model"
)

const (
	defaultUploadTTL           = time.Hour
	defaultMaxEnvelopeBytes    = 2 << 20
	defaultMaxAttachmentBytes  = 512 << 20
	defaultServerPublicKeyID   = "local-dev/server-public-key"
	defaultServerPublicKeyAlg  = "x25519"
	defaultCascadeExecutionIP  = "127.0.0.1"
	defaultCloudJobIDPrefix    = "job"
	defaultExchangeIDPrefix    = "xpkg"
	defaultUploadIDPrefix      = "upload"
	defaultResultPackagePrefix = "result_pkg"
	defaultExecutionTimeoutSec = 30 * 60
)

var exchangeIDSequence uint64

type ExchangeIntakeService struct {
	mu                 sync.Mutex
	verifier           model.ExchangeSignatureVerifier
	persistence        exchangeSnapshotStore
	allowInlinePayload bool
	now                func() time.Time
	uploads            map[string]exchangeUploadSession
	packages           map[string]*exchangePackageState
	packageByIdem      map[string]string
	resultByID         map[string]*recordingResultState
	resultByPackage    map[string]string
	seenNonces         map[string]bool
	installations      map[string]*exchangeInstallationState
	sessions           map[string]*exchangeInstallationSessionState
	challenges         map[string]exchangePairingChallengeState
}

type exchangeUploadSession struct {
	UploadID    string
	OrgID       string
	ProjectID   string
	PackageKind model.ExchangePackageKind
	Producer    model.ExchangeProducer
	ExpiresAt   time.Time
}

type exchangePackageState struct {
	ExchangePackageID string
	CloudJobID        string
	// AuthenticatedInstallID is populated only by the installation-session
	// transport path. A package cannot self-assert this provenance in JSON.
	AuthenticatedInstallID string
	Status                 model.ExchangePackageStatus
	Stage                  string
	FailedStage            string
	Message                string
	ProgressPercent        int
	StageHistory           []model.ExecutionStageEvent
	Envelope               model.ExchangeEnvelope
	Payload                model.ClientExecutionPackage
	CreatedAt              time.Time
	UpdatedAt              time.Time
	Error                  *model.AgentError
}

type recordingResultState struct {
	ResultPackageID   string
	ExchangePackageID string
	Result            model.RecordingResultPackage
	Retention         model.RetentionSpec
	Reviews           []model.ResultReviewRecord
	Revisions         []model.ResultRevisionRecord
}

type exchangeInstallationState struct {
	InstallID        string
	DeviceID         string
	OrgID            string
	ProjectID        string
	AppVersion       string
	RuntimeProfile   string
	SigningPublicKey model.AppInstallationPublicKey
	ResultPublicKey  model.AppInstallationPublicKey
	Revoked          bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type exchangeInstallationSessionState struct {
	SessionID            string
	SessionToken         string
	InstallID            string
	OrgID                string
	ProjectID            string
	ResultRecipientKeyID string
	ExpiresAt            time.Time
	Revoked              bool
}

type exchangePairingChallengeState struct {
	Challenge model.ExchangePairingChallenge
	Used      bool
}

func NewExchangeIntakeService(verifier model.ExchangeSignatureVerifier) *ExchangeIntakeService {
	return newExchangeIntakeService(verifier, nil)
}

func newExchangeIntakeService(verifier model.ExchangeSignatureVerifier, persistence exchangeSnapshotStore) *ExchangeIntakeService {
	service := &ExchangeIntakeService{
		verifier:           verifier,
		persistence:        persistence,
		allowInlinePayload: true,
		now:                func() time.Time { return time.Now().UTC() },
		uploads:            map[string]exchangeUploadSession{},
		packages:           map[string]*exchangePackageState{},
		packageByIdem:      map[string]string{},
		resultByID:         map[string]*recordingResultState{},
		resultByPackage:    map[string]string{},
		seenNonces:         map[string]bool{},
		installations:      map[string]*exchangeInstallationState{},
		sessions:           map[string]*exchangeInstallationSessionState{},
		challenges:         map[string]exchangePairingChallengeState{},
	}
	if verifier == nil {
		service.verifier = service
	}
	if persistence != nil {
		_ = service.LoadSnapshot(context.Background())
	}
	return service
}

// SetInlinePayloadAllowed is a transport boundary, not an App business-policy
// switch. Cloud profiles must receive only encrypted payload references.
func (s *ExchangeIntakeService) SetInlinePayloadAllowed(allowed bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.allowInlinePayload = allowed
}

func (s *ExchangeIntakeService) Init(ctx context.Context, request model.ExecutionPackageInitRequest) (model.ExecutionPackageInitResponse, error) {
	if err := ctx.Err(); err != nil {
		return model.ExecutionPackageInitResponse{}, err
	}
	if request.OrgID == "" || request.ProjectID == "" {
		return model.ExecutionPackageInitResponse{}, errors.New("org_id and project_id are required")
	}
	if request.PackageKind == "" {
		request.PackageKind = model.ExchangePackageKindClientExecution
	}
	if request.PackageKind != model.ExchangePackageKindClientExecution {
		return model.ExecutionPackageInitResponse{}, fmt.Errorf("unsupported package_kind %q", request.PackageKind)
	}

	now := s.now()
	response := model.ExecutionPackageInitResponse{
		UploadID:           newExchangeID(defaultUploadIDPrefix, now),
		ServerPublicKeyID:  defaultServerPublicKeyID,
		ServerPublicKeyAlg: defaultServerPublicKeyAlg,
		ServerPublicKeys: []model.ExchangeServerPublicKey{{
			KeyID:     defaultServerPublicKeyID,
			Alg:       defaultServerPublicKeyAlg,
			PublicKey: base64DevServerPublicKey(),
			NotBefore: now.Add(-time.Hour),
			ExpiresAt: now.Add(24 * time.Hour),
		}},
		KeyWrappingModes:      []string{model.KeyWrappingModeServerKMS, model.KeyWrappingModeServerPublicKey, model.KeyWrappingModeCustomerKMS},
		SupportedCryptoSuites: []string{model.CryptoSuiteXChaCha20Poly1305, model.CryptoSuiteAES256GCM},
		SupportedCompression:  []string{model.CompressionGzip, model.CompressionNone},
		RequiredSignatureAlg:  exchangeInstallationKeyAlg,
		InstallationRequired:  true,
		SessionExpiresAt:      now.Add(defaultInstallationSessionTTL),
		ResultRecipientKeyID:  installationResultKeyID(firstNonEmptyString(request.Producer.InstallID, defaultDesktopInstallID)),
		CascadeExecutionIPs:   []string{defaultCascadeExecutionIP},
		MaxEnvelopeBytes:      defaultMaxEnvelopeBytes,
		MaxAttachmentBytes:    defaultMaxAttachmentBytes,
		ExpiresAt:             now.Add(defaultUploadTTL),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.uploads[response.UploadID] = exchangeUploadSession{
		UploadID:    response.UploadID,
		OrgID:       request.OrgID,
		ProjectID:   request.ProjectID,
		PackageKind: request.PackageKind,
		Producer:    request.Producer,
		ExpiresAt:   response.ExpiresAt,
	}
	if err := s.saveLocked(ctx); err != nil {
		return model.ExecutionPackageInitResponse{}, err
	}
	return response, nil
}

func (s *ExchangeIntakeService) Upload(ctx context.Context, request model.ExecutionPackageUploadRequest, payload model.ClientExecutionPackage) (model.ExecutionPackageUploadResponse, error) {
	return s.upload(ctx, request, payload, "")
}

// UploadFromInstallation binds an upload to the App installation already
// authenticated by the HTTP transport. It is the production path for an App
// package that may later be counted as end-to-end acceptance evidence.
func (s *ExchangeIntakeService) UploadFromInstallation(ctx context.Context, request model.ExecutionPackageUploadRequest, payload model.ClientExecutionPackage, installID string) (model.ExecutionPackageUploadResponse, error) {
	return s.upload(ctx, request, payload, strings.TrimSpace(installID))
}

func (s *ExchangeIntakeService) upload(ctx context.Context, request model.ExecutionPackageUploadRequest, payload model.ClientExecutionPackage, authenticatedInstallID string) (model.ExecutionPackageUploadResponse, error) {
	if err := ctx.Err(); err != nil {
		return model.ExecutionPackageUploadResponse{}, err
	}
	if request.UploadID == "" {
		return model.ExecutionPackageUploadResponse{}, newExchangeProtocolError("upload_request_invalid", "upload_id", "required", "upload_id is required", "Call /v1/execution-packages/init first and reuse the returned upload_id.")
	}

	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.uploads[request.UploadID]
	if !ok {
		return model.ExecutionPackageUploadResponse{}, newExchangeProtocolError("upload_session_not_found", "upload_id", "not_found", "upload session not found", "Call /v1/execution-packages/init again and upload with the fresh upload_id.")
	}
	if now.After(session.ExpiresAt) {
		return model.ExecutionPackageUploadResponse{}, newExchangeProtocolError("upload_session_expired", "upload_id", "expired", "upload session expired", "Call /v1/execution-packages/init again; upload sessions expire after the negotiated window.")
	}
	if request.Envelope.OrgID != session.OrgID || request.Envelope.ProjectID != session.ProjectID || request.Envelope.PackageKind != session.PackageKind {
		return model.ExecutionPackageUploadResponse{}, newExchangeProtocolError("upload_session_mismatch", "envelope.identity", "mismatch", "upload request envelope does not match initialized session", "Use the same org_id, project_id, and package_kind that were used during init.")
	}
	if authenticatedInstallID != "" && (session.Producer.InstallID != authenticatedInstallID || request.Envelope.Producer.InstallID != authenticatedInstallID) {
		return model.ExecutionPackageUploadResponse{}, newExchangeProtocolError("installation_identity_mismatch", "envelope.producer.install_id", "mismatch", "authenticated App installation does not match the initialized upload or envelope", "Create a new upload session with the same paired App installation and sign the matching envelope.")
	}
	if request.PayloadRef.Kind == "" {
		request.PayloadRef = request.Envelope.PayloadRef
	}
	if !reflect.DeepEqual(request.PayloadRef, request.Envelope.PayloadRef) {
		return model.ExecutionPackageUploadResponse{}, newExchangeProtocolError("upload_payload_ref_mismatch", "payload_ref", "mismatch", "upload request payload_ref does not match exchange envelope", "Send one payload_ref value and keep it identical to envelope.payload_ref.")
	}
	if !s.allowInlinePayload && payload.PackageID != "" {
		return model.ExecutionPackageUploadResponse{}, newExchangeProtocolError("production_inline_payload_forbidden", "payload", "forbidden", "production Server only accepts an encrypted payload_ref; inline payload is not allowed", "Upload ciphertext as payload_ref and let the isolated worker decrypt it after Intake.")
	}
	if existingID := s.packageByIdem[idempotencyKey(request.Envelope.OrgID, request.Envelope.IdempotencyKey)]; existingID != "" {
		existing := s.packages[existingID]
		return model.ExecutionPackageUploadResponse{ExchangePackageID: existing.ExchangePackageID, CloudJobID: existing.CloudJobID, Status: existing.Status}, nil
	}
	if payload.PackageID == "" {
		if err := request.Envelope.ValidateMetadataForEncryptedUpload(now, s.seenNonces); err != nil {
			return model.ExecutionPackageUploadResponse{}, wrapExchangeValidationError("encrypted_payload_metadata_invalid", err)
		}
		return s.acceptEnvelopeOnlyPackageLocked(request.Envelope, now)
	}
	if err := model.ValidateClientExecutionPackageIntake(&request.Envelope, &payload, now, s.seenNonces, s.verifier); err != nil {
		return model.ExecutionPackageUploadResponse{}, wrapExchangeValidationError("client_execution_package_invalid", err)
	}

	exchangePackageID := newExchangeID(defaultExchangeIDPrefix, now)
	cloudJobID := newExchangeID(defaultCloudJobIDPrefix, now)
	state := &exchangePackageState{
		ExchangePackageID:      exchangePackageID,
		CloudJobID:             cloudJobID,
		AuthenticatedInstallID: authenticatedInstallID,
		Status:                 model.ExchangePackageStatusAccepted,
		Envelope:               request.Envelope,
		Payload:                payload,
		CreatedAt:              now,
		UpdatedAt:              now,
	}
	setPackageStageLocked(state, model.ExchangePackageStatusAccepted, "accepted", "Execution package accepted. Call the dev run endpoint to start recording and rendering.", 10, now)
	s.packages[exchangePackageID] = state
	s.packageByIdem[idempotencyKey(request.Envelope.OrgID, request.Envelope.IdempotencyKey)] = exchangePackageID
	delete(s.uploads, request.UploadID)
	if err := s.saveLocked(ctx); err != nil {
		return model.ExecutionPackageUploadResponse{}, err
	}

	return model.ExecutionPackageUploadResponse{ExchangePackageID: exchangePackageID, CloudJobID: cloudJobID, Status: state.Status}, nil
}

// ValidateUpload checks an App package with the same Intake rules as Upload,
// without allocating an upload session, persisting a payload, or starting work.
// A copied nonce set keeps this preflight side-effect free.
func (s *ExchangeIntakeService) ValidateUpload(ctx context.Context, request model.ExecutionPackageUploadRequest, payload model.ClientExecutionPackage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if request.PayloadRef.Kind == "" {
		request.PayloadRef = request.Envelope.PayloadRef
	}
	if !reflect.DeepEqual(request.PayloadRef, request.Envelope.PayloadRef) {
		return newExchangeProtocolError("upload_payload_ref_mismatch", "payload_ref", "mismatch", "upload request payload_ref does not match exchange envelope", "Send one payload_ref value and keep it identical to envelope.payload_ref.")
	}
	s.mu.Lock()
	allowInlinePayload := s.allowInlinePayload
	seenNonces := make(map[string]bool, len(s.seenNonces))
	for nonce, seen := range s.seenNonces {
		seenNonces[nonce] = seen
	}
	now := s.now()
	verifier := s.verifier
	s.mu.Unlock()
	if !allowInlinePayload && payload.PackageID != "" {
		return newExchangeProtocolError("production_inline_payload_forbidden", "payload", "forbidden", "production Server only accepts an encrypted payload_ref; inline payload is not allowed", "Upload ciphertext as payload_ref and let the isolated worker decrypt it after Intake.")
	}
	if err := model.ValidateClientExecutionPackageIntake(&request.Envelope, &payload, now, seenNonces, verifier); err != nil {
		return wrapExchangeValidationError("client_execution_package_invalid", err)
	}
	return nil
}

func (s *ExchangeIntakeService) acceptEnvelopeOnlyPackageLocked(envelope model.ExchangeEnvelope, now time.Time) (model.ExecutionPackageUploadResponse, error) {
	exchangePackageID := newExchangeID(defaultExchangeIDPrefix, now)
	cloudJobID := newExchangeID(defaultCloudJobIDPrefix, now)
	state := &exchangePackageState{
		ExchangePackageID: exchangePackageID,
		CloudJobID:        cloudJobID,
		Status:            model.ExchangePackageStatusAccepted,
		Envelope:          envelope,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	setPackageStageLocked(state, model.ExchangePackageStatusAccepted, "accepted", "Encrypted execution package accepted. Isolated worker decryption is required before execution.", 10, now)
	s.packages[exchangePackageID] = state
	s.packageByIdem[idempotencyKey(envelope.OrgID, envelope.IdempotencyKey)] = exchangePackageID
	return model.ExecutionPackageUploadResponse{ExchangePackageID: exchangePackageID, CloudJobID: cloudJobID, Status: state.Status}, nil
}

func (s *ExchangeIntakeService) Status(ctx context.Context, orgID string, exchangePackageID string) (model.ExecutionPackageStatusResponse, error) {
	if err := ctx.Err(); err != nil {
		return model.ExecutionPackageStatusResponse{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.packageStateLocked(orgID, exchangePackageID)
	if err != nil {
		return model.ExecutionPackageStatusResponse{}, err
	}
	return s.statusResponseLocked(state), nil
}

func (s *ExchangeIntakeService) ListExecutionPackages(ctx context.Context, orgID string) (model.ExecutionPackageListResponse, error) {
	if err := ctx.Err(); err != nil {
		return model.ExecutionPackageListResponse{}, err
	}
	if strings.TrimSpace(orgID) == "" {
		return model.ExecutionPackageListResponse{}, errors.New("org_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	items := make([]model.ExecutionPackageListItem, 0, len(s.packages))
	for _, state := range s.packages {
		if state == nil || state.Envelope.OrgID != orgID {
			continue
		}
		status := s.statusResponseLocked(state)
		items = append(items, model.ExecutionPackageListItem{
			ExchangePackageID: status.ExchangePackageID,
			CloudJobID:        status.CloudJobID,
			OrgID:             state.Envelope.OrgID,
			ProjectID:         state.Envelope.ProjectID,
			PackageID:         state.Payload.PackageID,
			Status:            status.Status,
			Stage:             status.Stage,
			Message:           status.Message,
			ProgressPercent:   status.ProgressPercent,
			ResultPackageID:   status.ResultPackageID,
			ResultSummary:     status.ResultSummary,
			FailureSummary:    status.FailureSummary,
			CreatedAt:         state.CreatedAt,
			UpdatedAt:         status.UpdatedAt,
		})
	}
	sort.SliceStable(items, func(left, right int) bool {
		return items[left].UpdatedAt.After(items[right].UpdatedAt)
	})
	return model.ExecutionPackageListResponse{Items: items}, nil
}

func (s *ExchangeIntakeService) PayloadSnapshot(ctx context.Context, orgID string, exchangePackageID string) (model.ClientExecutionPackage, error) {
	if err := ctx.Err(); err != nil {
		return model.ClientExecutionPackage{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.packageStateLocked(orgID, exchangePackageID)
	if err != nil {
		return model.ClientExecutionPackage{}, err
	}
	return state.Payload, nil
}

func (s *ExchangeIntakeService) CompleteWithRecordingResult(ctx context.Context, orgID string, exchangePackageID string, result model.RecordingResultPackage) (model.ExecutionPackageStatusResponse, error) {
	if err := ctx.Err(); err != nil {
		return model.ExecutionPackageStatusResponse{}, err
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.packageStateLocked(orgID, exchangePackageID)
	if err != nil {
		return model.ExecutionPackageStatusResponse{}, err
	}
	if isTerminalExchangeStatus(state.Status) {
		return s.statusResponseLocked(state), nil
	}
	if result.Status == model.RecordingResultStatusFailed {
		if err := result.ValidateStatusContract(); err != nil {
			setPackageStageLocked(state, model.ExchangePackageStatusFailed, "failed", "Failed recording result package was rejected by cloud-side validation.", 100, now)
			state.Error = &model.AgentError{Code: "failed_recording_result_invalid", Message: err.Error()}
			return s.statusResponseLocked(state), err
		}
	} else {
		if err := model.ValidateRecordingResultPackageForRender(&result, &state.Payload); err != nil {
			setPackageStageLocked(state, model.ExchangePackageStatusFailed, "failed", "Recording result package was rejected by cloud-side validation.", 100, now)
			state.Error = &model.AgentError{Code: "recording_result_invalid", Message: err.Error()}
			return s.statusResponseLocked(state), err
		}
	}

	resultPackageID := newExchangeID(defaultResultPackagePrefix, now)
	retention := resultRetention(result, now)
	s.resultByID[resultPackageID] = &recordingResultState{
		ResultPackageID:   resultPackageID,
		ExchangePackageID: state.ExchangePackageID,
		Result:            result,
		Retention:         retention,
	}
	s.resultByPackage[state.ExchangePackageID] = resultPackageID
	if result.Status == model.RecordingResultStatusFailed {
		setPackageStageLocked(state, model.ExchangePackageStatusFailed, "failed", "Recording failed. Failure diagnostics are ready in the result package.", 100, now)
		state.FailedStage = "running_script"
		if result.FailureDiagnostic != nil {
			state.Error = &result.FailureDiagnostic.Error
		}
	} else {
		setPackageStageLocked(state, model.ExchangePackageStatusCompleted, "completed", "Recording, directing, deterministic rendering, and quality validation completed. Result package is ready.", 100, now)
	}
	if err := s.saveLocked(ctx); err != nil {
		return model.ExecutionPackageStatusResponse{}, err
	}

	return s.statusResponseLocked(state), nil
}

func (s *ExchangeIntakeService) GetResultPackage(ctx context.Context, orgID string, resultPackageID string) (model.RecordingResultPackage, error) {
	if err := ctx.Err(); err != nil {
		return model.RecordingResultPackage{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	resultState, ok := s.resultByID[resultPackageID]
	if !ok {
		return model.RecordingResultPackage{}, errors.New("result package not found")
	}
	state, err := s.packageStateLocked(orgID, resultState.ExchangePackageID)
	if err != nil {
		return model.RecordingResultPackage{}, err
	}
	if state.Envelope.Policy.DeletePayloadAfterRun {
		state.Payload = model.ClientExecutionPackage{}
	}
	markResultDeliveredLocked(resultState, "", s.now())
	if err := s.saveLocked(ctx); err != nil {
		return model.RecordingResultPackage{}, err
	}
	return resultState.Result, nil
}

// ResultSnapshot reads a result without advancing delivery state. It is used
// for status decoration, where a polling request must never imply delivery.
func (s *ExchangeIntakeService) ResultSnapshot(ctx context.Context, orgID string, resultPackageID string) (model.RecordingResultPackage, error) {
	if err := ctx.Err(); err != nil {
		return model.RecordingResultPackage{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	resultState, ok := s.resultByID[resultPackageID]
	if !ok {
		return model.RecordingResultPackage{}, errors.New("result package not found")
	}
	if _, err := s.packageStateLocked(orgID, resultState.ExchangePackageID); err != nil {
		return model.RecordingResultPackage{}, err
	}
	return resultState.Result, nil
}

func (s *ExchangeIntakeService) ListResultPackages(ctx context.Context, orgID string) (model.ResultPackageListResponse, error) {
	if err := ctx.Err(); err != nil {
		return model.ResultPackageListResponse{}, err
	}
	if strings.TrimSpace(orgID) == "" {
		return model.ResultPackageListResponse{}, errors.New("org_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	items := make([]model.ResultPackageListItem, 0, len(s.resultByID))
	for _, resultState := range s.resultByID {
		if resultState == nil {
			continue
		}
		state, ok := s.packages[resultState.ExchangePackageID]
		if !ok || state == nil || state.Envelope.OrgID != orgID {
			continue
		}
		result := resultState.Result
		item := model.ResultPackageListItem{
			ResultPackageID:   resultState.ResultPackageID,
			ResultID:          result.ResultID,
			ExchangePackageID: resultState.ExchangePackageID,
			SourcePackageID:   result.SourcePackageID,
			CloudJobID:        result.CloudJobID,
			OrgID:             state.Envelope.OrgID,
			ProjectID:         state.Envelope.ProjectID,
			Status:            result.Status,
			DeliveryStatus:    resultDeliveryStatus(result),
			ResultSummary:     summarizeRecordingResult(result),
			CreatedAt:         result.CreatedAt,
			ExpiresAt:         result.Delivery.ExpiresAt,
			AckRequired:       result.Delivery.AckRequired,
			DeliveredAt:       result.Delivery.DeliveredAt,
			AckedAt:           result.Delivery.AckedAt,
			AckedByInstallID:  result.Delivery.AckedByInstallID,
		}
		if result.FailureDiagnostic != nil {
			item.FailureSummary = failureSummary(state, result.FailureDiagnostic)
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(left, right int) bool {
		return items[left].CreatedAt.After(items[right].CreatedAt)
	})
	return model.ResultPackageListResponse{Items: items}, nil
}

func (s *ExchangeIntakeService) GetResultArtifact(ctx context.Context, orgID string, resultPackageID string, artifactID string) (model.ArtifactRef, error) {
	if err := ctx.Err(); err != nil {
		return model.ArtifactRef{}, err
	}
	if strings.TrimSpace(artifactID) == "" {
		return model.ArtifactRef{}, errors.New("artifact_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	resultState, ok := s.resultByID[resultPackageID]
	if !ok {
		return model.ArtifactRef{}, errors.New("result package not found")
	}
	if _, err := s.packageStateLocked(orgID, resultState.ExchangePackageID); err != nil {
		return model.ArtifactRef{}, err
	}
	for _, artifact := range uniqueStatusArtifacts(append(append([]model.ArtifactRef{}, resultState.Result.GeneratedAssets...), traceArtifacts(resultState.Result.ExecutionTrace)...)) {
		if artifact.ID == artifactID {
			return artifact, nil
		}
	}
	return model.ArtifactRef{}, errors.New("result artifact not found")
}

func (s *ExchangeIntakeService) MarkResultArtifactDelivered(ctx context.Context, orgID string, resultPackageID string, artifactID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	resultState, ok := s.resultByID[resultPackageID]
	if !ok {
		return errors.New("result package not found")
	}
	if _, err := s.packageStateLocked(orgID, resultState.ExchangePackageID); err != nil {
		return err
	}
	markResultDeliveredLocked(resultState, artifactID, s.now())
	return s.saveLocked(ctx)
}

func (s *ExchangeIntakeService) AckResultPackage(ctx context.Context, orgID string, request model.ResultPackageAckRequest) (model.ResultPackageAckResponse, error) {
	if err := ctx.Err(); err != nil {
		return model.ResultPackageAckResponse{}, err
	}
	if request.ResultPackageID == "" {
		return model.ResultPackageAckResponse{}, errors.New("result_package_id is required")
	}
	if !request.VerifiedChecksums {
		return model.ResultPackageAckResponse{}, errors.New("result package ack requires verified_checksums=true")
	}
	if len(request.ChecksumMismatchIDs) > 0 {
		return model.ResultPackageAckResponse{}, errors.New("result package ack rejected because checksum_mismatch_ids is not empty")
	}
	if request.AckedAt.IsZero() {
		request.AckedAt = s.now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	resultState, ok := s.resultByID[request.ResultPackageID]
	if !ok {
		return model.ResultPackageAckResponse{}, errors.New("result package not found")
	}
	state, err := s.packageStateLocked(orgID, resultState.ExchangePackageID)
	if err != nil {
		return model.ResultPackageAckResponse{}, err
	}
	if len(request.ReceivedAssetIDs) > 0 {
		if err := validateReceivedAssets(resultState.Result, request.ReceivedAssetIDs); err != nil {
			return model.ResultPackageAckResponse{}, err
		}
	}
	resultState.Result.Status = model.RecordingResultStatusAcked
	markResultDeliveredLocked(resultState, "", request.AckedAt)
	resultState.Result.Delivery.AckedAt = request.AckedAt
	resultState.Result.Delivery.AckedByInstallID = request.AckedByInstallID
	resultState.Result.Delivery.ReceivedAssetIDs = uniqueStrings(request.ReceivedAssetIDs)
	resultState.Result.Delivery.VerifiedChecksums = request.VerifiedChecksums
	state.UpdatedAt = request.AckedAt
	if err := s.saveLocked(ctx); err != nil {
		return model.ResultPackageAckResponse{}, err
	}
	return model.ResultPackageAckResponse{
		ResultPackageID:   request.ResultPackageID,
		Status:            model.RecordingResultStatusAcked,
		DeliveryStatus:    model.ResultDeliveryStatusAcked,
		Retention:         resultState.Retention,
		AckedAt:           request.AckedAt,
		AckedByInstallID:  request.AckedByInstallID,
		ReceivedAssetIDs:  append([]string{}, resultState.Result.Delivery.ReceivedAssetIDs...),
		VerifiedChecksums: request.VerifiedChecksums,
	}, nil
}

func (s *ExchangeIntakeService) ReviewResultPackage(ctx context.Context, orgID string, resultPackageID string, request model.ResultReviewRequest) (model.ResultReviewRecord, error) {
	if err := ctx.Err(); err != nil {
		return model.ResultReviewRecord{}, err
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return model.ResultReviewRecord{}, errors.New("result review idempotency_key is required")
	}
	switch request.Decision {
	case model.ResultReviewApproved, model.ResultReviewReeditRequested, model.ResultReviewRerecordRequested:
	default:
		return model.ResultReviewRecord{}, errors.New("unsupported result review decision")
	}
	if request.ReviewedAt.IsZero() {
		request.ReviewedAt = s.now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	resultState, err := s.resultStateLocked(orgID, resultPackageID)
	if err != nil {
		return model.ResultReviewRecord{}, err
	}
	for _, review := range resultState.Reviews {
		if review.IdempotencyKey == request.IdempotencyKey {
			return review, nil
		}
	}
	record := model.ResultReviewRecord{
		ReviewID: newExchangeID("review", request.ReviewedAt), ResultPackageID: resultPackageID,
		IdempotencyKey: request.IdempotencyKey, ReviewerInstallID: request.ReviewerInstallID,
		Decision: request.Decision, Summary: strings.TrimSpace(request.Summary),
		Annotations: append([]model.ResultReviewAnnotation{}, request.Annotations...), ReviewedAt: request.ReviewedAt,
	}
	resultState.Reviews = append(resultState.Reviews, record)
	if err := s.saveLocked(ctx); err != nil {
		return model.ResultReviewRecord{}, err
	}
	return record, nil
}

func (s *ExchangeIntakeService) RequestResultRevision(ctx context.Context, orgID string, resultPackageID string, request model.ResultRevisionRequest) (model.ResultRevisionRecord, error) {
	if err := ctx.Err(); err != nil {
		return model.ResultRevisionRecord{}, err
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return model.ResultRevisionRecord{}, errors.New("result revision idempotency_key is required")
	}
	if request.RequestedAt.IsZero() {
		request.RequestedAt = s.now()
	}
	resolved, err := resolveRevisionAction(request)
	if err != nil {
		return model.ResultRevisionRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	resultState, err := s.resultStateLocked(orgID, resultPackageID)
	if err != nil {
		return model.ResultRevisionRecord{}, err
	}
	for _, revision := range resultState.Revisions {
		if revision.IdempotencyKey == request.IdempotencyKey {
			return revision, nil
		}
	}
	record := model.ResultRevisionRecord{
		RevisionID: newExchangeID("revision", request.RequestedAt), ResultPackageID: resultPackageID,
		IdempotencyKey: request.IdempotencyKey, RequestedByInstallID: request.RequestedByInstallID,
		RequestedAction: request.RequestedAction, ResolvedAction: resolved, Status: "queued",
		Summary: strings.TrimSpace(request.Summary), Issues: append([]model.ResultRevisionIssue{}, request.Issues...), RequestedAt: request.RequestedAt,
	}
	resultState.Revisions = append(resultState.Revisions, record)
	if err := s.saveLocked(ctx); err != nil {
		return model.ResultRevisionRecord{}, err
	}
	return record, nil
}

func resolveRevisionAction(request model.ResultRevisionRequest) (model.ResultRevisionAction, error) {
	switch request.RequestedAction {
	case model.ResultRevisionReedit, model.ResultRevisionRerecord:
		return request.RequestedAction, nil
	case "", model.ResultRevisionAuto:
		for _, issue := range request.Issues {
			switch strings.ToLower(strings.TrimSpace(issue.Kind)) {
			case "missing_material", "missing_step", "recording_failure", "wrong_page", "interaction_failure":
				return model.ResultRevisionRerecord, nil
			}
		}
		return model.ResultRevisionReedit, nil
	default:
		return "", errors.New("unsupported result revision action")
	}
}

func (s *ExchangeIntakeService) resultStateLocked(orgID string, resultPackageID string) (*recordingResultState, error) {
	resultState, ok := s.resultByID[resultPackageID]
	if !ok {
		return nil, errors.New("result package not found")
	}
	if _, err := s.packageStateLocked(orgID, resultState.ExchangePackageID); err != nil {
		return nil, err
	}
	return resultState, nil
}

func validateReceivedAssets(result model.RecordingResultPackage, receivedAssetIDs []string) error {
	known := map[string]bool{}
	for _, asset := range result.Delivery.AssetRefs {
		if asset.ID != "" {
			known[asset.ID] = true
		}
	}
	for _, asset := range result.GeneratedAssets {
		if asset.ID != "" {
			known[asset.ID] = true
		}
	}
	for _, asset := range traceArtifacts(result.ExecutionTrace) {
		if asset.ID != "" {
			known[asset.ID] = true
		}
	}
	for _, id := range receivedAssetIDs {
		if strings.TrimSpace(id) == "" {
			return errors.New("received_asset_ids contains an empty asset id")
		}
		if !known[id] {
			return fmt.Errorf("received asset id %q is not part of result package", id)
		}
	}
	return nil
}

func markResultDeliveredLocked(resultState *recordingResultState, artifactID string, deliveredAt time.Time) {
	if resultState == nil {
		return
	}
	if deliveredAt.IsZero() {
		deliveredAt = time.Now().UTC()
	}
	if resultState.Result.Delivery.DeliveredAt.IsZero() {
		resultState.Result.Delivery.DeliveredAt = deliveredAt
	}
	if strings.TrimSpace(artifactID) != "" {
		resultState.Result.Delivery.DownloadedAssetIDs = appendUniqueString(resultState.Result.Delivery.DownloadedAssetIDs, artifactID)
	}
}

func (s *ExchangeIntakeService) LoadSnapshot(ctx context.Context) error {
	if s == nil || s.persistence == nil {
		return nil
	}
	snapshot, err := s.persistence.Load(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applySnapshotLocked(snapshot)
	if s.failInterruptedExecutionsLocked(s.now()) {
		return s.saveLocked(ctx)
	}
	return nil
}

func (s *ExchangeIntakeService) saveLocked(ctx context.Context) error {
	if s == nil || s.persistence == nil {
		return nil
	}
	return s.persistence.Save(ctx, s.snapshotLocked())
}

func (s *ExchangeIntakeService) snapshotLocked() exchangeSnapshot {
	return exchangeSnapshot{
		Uploads:         cloneUploadSessions(s.uploads),
		Packages:        clonePackageStates(s.packages),
		PackageByIdem:   cloneStringMap(s.packageByIdem),
		ResultByID:      cloneRecordingResultStates(s.resultByID),
		ResultByPackage: cloneStringMap(s.resultByPackage),
		SeenNonces:      cloneBoolMap(s.seenNonces),
		Installations:   cloneInstallationStates(s.installations),
		Sessions:        cloneInstallationSessionStates(s.sessions),
		Challenges:      clonePairingChallengeStates(s.challenges),
	}
}

func (s *ExchangeIntakeService) applySnapshotLocked(snapshot exchangeSnapshot) {
	s.uploads = cloneUploadSessions(snapshot.Uploads)
	s.packages = clonePackageStates(snapshot.Packages)
	s.packageByIdem = cloneStringMap(snapshot.PackageByIdem)
	s.resultByID = cloneRecordingResultStates(snapshot.ResultByID)
	s.resultByPackage = cloneStringMap(snapshot.ResultByPackage)
	s.seenNonces = cloneBoolMap(snapshot.SeenNonces)
	s.installations = cloneInstallationStates(snapshot.Installations)
	s.sessions = cloneInstallationSessionStates(snapshot.Sessions)
	s.challenges = clonePairingChallengeStates(snapshot.Challenges)
	if s.uploads == nil {
		s.uploads = map[string]exchangeUploadSession{}
	}
	if s.packages == nil {
		s.packages = map[string]*exchangePackageState{}
	}
	if s.packageByIdem == nil {
		s.packageByIdem = map[string]string{}
	}
	if s.resultByID == nil {
		s.resultByID = map[string]*recordingResultState{}
	}
	if s.resultByPackage == nil {
		s.resultByPackage = map[string]string{}
	}
	if s.seenNonces == nil {
		s.seenNonces = map[string]bool{}
	}
	if s.installations == nil {
		s.installations = map[string]*exchangeInstallationState{}
	}
	if s.sessions == nil {
		s.sessions = map[string]*exchangeInstallationSessionState{}
	}
	if s.challenges == nil {
		s.challenges = map[string]exchangePairingChallengeState{}
	}
}

func (s *ExchangeIntakeService) failInterruptedExecutionsLocked(now time.Time) bool {
	changed := false
	for _, state := range s.packages {
		if state == nil {
			continue
		}
		if state.Status != model.ExchangePackageStatusRunning && state.Status != model.ExchangePackageStatusQueued {
			continue
		}
		stage := state.Stage
		if stage == "" {
			stage = string(state.Status)
		}
		markPackageFailedLocked(state, "interrupted_by_restart", "Execution was interrupted by service restart before completion.", stage, now)
		changed = true
	}
	return changed
}

func (s *ExchangeIntakeService) packageState(orgID string, exchangePackageID string) (*exchangePackageState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.packageStateLocked(orgID, exchangePackageID)
}

func (s *ExchangeIntakeService) packageStateLocked(orgID string, exchangePackageID string) (*exchangePackageState, error) {
	if exchangePackageID == "" {
		return nil, errors.New("exchange_package_id is required")
	}
	state, ok := s.packages[exchangePackageID]
	if !ok {
		return nil, errors.New("exchange package not found")
	}
	if state.Envelope.OrgID != orgID {
		return nil, errors.New("exchange package org mismatch")
	}
	return state, nil
}

func resultRetention(result model.RecordingResultPackage, now time.Time) model.RetentionSpec {
	if !result.Delivery.ExpiresAt.IsZero() {
		return model.RetentionSpec{ExpiresAt: result.Delivery.ExpiresAt, Reason: "recording_result_delivery"}
	}
	return model.RetentionSpec{ExpiresAt: now.Add(24 * time.Hour), Reason: "default_recording_result_retention"}
}

func (s *ExchangeIntakeService) statusResponseLocked(state *exchangePackageState) model.ExecutionPackageStatusResponse {
	stage, message, progress := exchangeStatusView(state)
	response := model.ExecutionPackageStatusResponse{
		ExchangePackageID: state.ExchangePackageID,
		CloudJobID:        state.CloudJobID,
		Status:            state.Status,
		Stage:             stage,
		Message:           message,
		ProgressPercent:   progress,
		StageHistory:      append([]model.ExecutionStageEvent{}, state.StageHistory...),
		Error:             state.Error,
		UpdatedAt:         state.UpdatedAt,
	}
	if state.Error != nil {
		response.FailureSummary = failureSummary(state, nil)
	}
	if resultID := s.resultByPackage[state.ExchangePackageID]; resultID != "" {
		response.ResultPackageID = resultID
		if resultState := s.resultByID[resultID]; resultState != nil {
			response.ResultSummary = summarizeRecordingResult(resultState.Result)
			if response.ResultSummary != nil {
				response.ResultSummary.Acceptance = summarizeExecutionAcceptance(state, resultState.Result)
			}
			if resultState.Result.FailureDiagnostic != nil {
				response.FailureSummary = failureSummary(state, resultState.Result.FailureDiagnostic)
			}
		}
	}
	return response
}

func setPackageStageLocked(state *exchangePackageState, status model.ExchangePackageStatus, stage string, message string, progress int, updatedAt time.Time) {
	if state == nil {
		return
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	state.Status = status
	state.Stage = stage
	state.Message = message
	state.ProgressPercent = progress
	state.UpdatedAt = updatedAt
	state.StageHistory = append(state.StageHistory, model.ExecutionStageEvent{
		EventID:         fmt.Sprintf("%s:%d", state.ExchangePackageID, len(state.StageHistory)+1),
		Stage:           stage,
		Status:          status,
		Message:         message,
		ProgressPercent: progress,
		UpdatedAt:       updatedAt,
	})
}

func failureSummary(state *exchangePackageState, diagnostic *model.ScriptFailureDiagnostic) *model.ExecutionFailureSummary {
	if state == nil && diagnostic == nil {
		return nil
	}
	summary := &model.ExecutionFailureSummary{}
	if state != nil {
		summary.FailedStage = state.FailedStage
		if summary.FailedStage == "" {
			summary.FailedStage = state.Stage
		}
		if state.Error != nil {
			summary.Code = state.Error.Code
			summary.Message = state.Error.Message
			summary.Retryable = state.Error.Retryable
		}
	}
	if diagnostic != nil {
		summary.FailedNodeID = diagnostic.FailedNodeID
		summary.CurrentURL = diagnostic.CurrentURL
		summary.PageTitle = diagnostic.PageTitle
		if len(diagnostic.ScreenshotRefs) > 0 {
			summary.FailureScreenshotURI = diagnostic.ScreenshotRefs[0].URI
		}
		if len(diagnostic.TraceRefs) > 0 {
			summary.FailureTraceURI = diagnostic.TraceRefs[0].URI
		}
		if summary.Code == "" {
			summary.Code = diagnostic.Error.Code
		}
		if summary.Message == "" {
			summary.Message = diagnostic.Error.Message
		}
		if !summary.Retryable {
			summary.Retryable = diagnostic.Error.Retryable
		}
	}
	if summary.Code == "" && summary.Message == "" && summary.FailedStage == "" && summary.FailedNodeID == "" {
		return nil
	}
	return summary
}

func exchangeStatusView(state *exchangePackageState) (string, string, int) {
	if state.Stage != "" || state.Message != "" || state.ProgressPercent > 0 {
		return state.Stage, state.Message, state.ProgressPercent
	}
	switch state.Status {
	case model.ExchangePackageStatusAccepted:
		return "accepted", "Execution package accepted. Call the dev run endpoint to start recording and rendering.", 10
	case model.ExchangePackageStatusRunning:
		return "recording_rendering", "Recording and rendering are running.", 50
	case model.ExchangePackageStatusCompleted:
		return "completed", "Recording and rendering completed. Result package is ready.", 100
	case model.ExchangePackageStatusFailed:
		return "failed", "Execution failed.", 100
	default:
		return string(state.Status), "", 0
	}
}

func summarizeRecordingResult(result model.RecordingResultPackage) *model.ExecutionResultSummary {
	steps := result.StepResults
	if len(steps) == 0 && result.ExecutionTrace != nil {
		steps = result.ExecutionTrace.StepResults
	}
	assets := uniqueStatusArtifacts(append(append([]model.ArtifactRef{}, result.GeneratedAssets...), traceArtifacts(result.ExecutionTrace)...))
	summary := &model.ExecutionResultSummary{
		ResultID:            result.ResultID,
		ResultStatus:        result.Status,
		DeliveryStatus:      resultDeliveryStatus(result),
		PassRate:            result.VerificationReport.PassRate,
		StepCount:           len(steps),
		GeneratedAssetCount: len(assets),
		AckRequired:         result.Delivery.AckRequired,
		DeliveredAt:         result.Delivery.DeliveredAt,
		AckedAt:             result.Delivery.AckedAt,
		ExpiresAt:           result.Delivery.ExpiresAt,
		Validation:          summarizeExecutionValidation(result),
	}
	if summary.PassRate == 0 && result.ExecutionTrace != nil {
		summary.PassRate = result.ExecutionTrace.PassRate
	}
	for _, step := range steps {
		switch {
		case strings.EqualFold(step.Status, "passed"):
			summary.PassedStepCount++
		case strings.EqualFold(step.Status, "failed"):
			summary.FailedStepCount++
		}
	}
	for _, asset := range assets {
		if deliverable := executionDeliverableFromArtifact(asset); deliverable.URI != "" {
			summary.Deliverables = append(summary.Deliverables, deliverable)
		}
		switch strings.ToLower(asset.Kind) {
		case "demo_video":
			summary.DemoVideoCount++
			if summary.PrimaryDemoVideoURI == "" {
				summary.PrimaryDemoVideoURI = asset.URI
			}
		case "raw_recording":
			summary.RawRecordingCount++
			if summary.RawRecordingURI == "" {
				summary.RawRecordingURI = asset.URI
			}
		case "screenshot", "webpage_screenshot":
			summary.ScreenshotCount++
		case "browser_trace", "execution_trace":
			summary.TraceCount++
		}
	}
	return summary
}

func summarizeExecutionValidation(result model.RecordingResultPackage) *model.ExecutionValidationSummary {
	if result.ExecutionRuntime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 &&
		len(result.ValidationReports) == 0 && result.StageEventLogRef == nil {
		return nil
	}

	summary := &model.ExecutionValidationSummary{
		Runtime:                result.ExecutionRuntime,
		Status:                 "incomplete",
		ValidationReportCount:  len(result.ValidationReports),
		StageEventLogAvailable: result.StageEventLogRef != nil,
	}
	for _, report := range result.ValidationReports {
		switch report.Phase {
		case model.ValidationPhasePreExecution:
			summary.PreExecutionReportCount++
		case model.ValidationPhaseRuntimeStage:
			summary.RuntimeStageReportCount++
		case model.ValidationPhasePostExecution:
			summary.PostExecutionReportCount++
		}
		summary.LatestDecision = report.Decision
	}
	for _, step := range result.StepResults {
		if strings.TrimSpace(step.ObservedState) != "" &&
			!strings.Contains(strings.ToLower(step.ObservedState), string(model.RuntimeObservationDerivedPlan)) {
			summary.RealObservedStepCount++
		}
	}
	if result.Status == model.RecordingResultStatusFailed {
		summary.Status = "failed"
		return summary
	}
	if summary.PreExecutionReportCount == 1 && summary.PostExecutionReportCount == 1 &&
		summary.RuntimeStageReportCount == len(result.StepResults) &&
		summary.RealObservedStepCount == len(result.StepResults) &&
		summary.StageEventLogAvailable && summary.LatestDecision == model.ValidationDecisionContinue {
		summary.Status = "complete"
	}
	return summary
}

func summarizeExecutionAcceptance(state *exchangePackageState, result model.RecordingResultPackage) *model.ExecutionAcceptanceSummary {
	if state == nil || result.ExecutionRuntime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 {
		return nil
	}
	producer := strings.TrimSpace(state.Envelope.Producer.RuntimeProfile)
	origin := "unverified_client_upload"
	appGenerated := false
	if strings.HasPrefix(producer, "server_") || strings.HasPrefix(producer, "server-") ||
		strings.HasPrefix(strings.TrimSpace(stringMetadata(state.Payload.Metadata, "producer")), "server_") {
		origin = "server_controlled_fixture"
	} else if state.AuthenticatedInstallID != "" && state.AuthenticatedInstallID == state.Envelope.Producer.InstallID {
		origin = "app_formal_exchange"
		appGenerated = true
	}
	validation := summarizeExecutionValidation(result)
	strictEvidence := validation != nil && validation.Status == "complete"
	finalMP4 := false
	for _, asset := range result.GeneratedAssets {
		if asset.Kind == "demo_video" && strings.EqualFold(asset.MimeType, "video/mp4") {
			finalMP4 = true
			break
		}
	}
	status := "incomplete"
	if result.Status == model.RecordingResultStatusFailed {
		status = "failed"
	} else if appGenerated && strictEvidence && finalMP4 {
		status = "ready_for_app_e2e_acceptance"
	} else if origin == "server_controlled_fixture" && strictEvidence && finalMP4 {
		status = "server_fixture_only"
	} else if strictEvidence && finalMP4 {
		status = "unverified_origin"
	}
	return &model.ExecutionAcceptanceSummary{
		Origin: origin, AppGenerated: appGenerated, FormalExchange: true,
		StrictEvidenceComplete: strictEvidence, FinalMP4Available: finalMP4, Status: status,
	}
}

func executionDeliverableFromArtifact(asset model.ArtifactRef) model.ExecutionDeliverable {
	role := stringMetadata(asset.Metadata, "asset_role")
	return model.ExecutionDeliverable{
		ID:            asset.ID,
		Kind:          asset.Kind,
		Role:          role,
		URI:           asset.URI,
		MimeType:      asset.MimeType,
		SHA256:        asset.SHA256,
		SizeBytes:     asset.SizeBytes,
		SourceNodeID:  asset.SourceNodeID,
		IncludeInDemo: boolMetadata(asset.Metadata, "include_in_demo"),
		Sensitive:     asset.Sensitive,
	}
}

func resultDeliveryStatus(result model.RecordingResultPackage) model.ResultDeliveryStatus {
	if !result.Delivery.AckedAt.IsZero() || result.Status == model.RecordingResultStatusAcked {
		return model.ResultDeliveryStatusAcked
	}
	if !result.Delivery.DeliveredAt.IsZero() || result.Status == model.RecordingResultStatusDelivered {
		return model.ResultDeliveryStatusDelivered
	}
	return model.ResultDeliveryStatusReady
}

func traceArtifacts(trace *model.ExecutionTrace) []model.ArtifactRef {
	if trace == nil {
		return nil
	}
	return trace.Artifacts
}

func uniqueStatusArtifacts(artifacts []model.ArtifactRef) []model.ArtifactRef {
	seen := map[string]bool{}
	out := []model.ArtifactRef{}
	for _, artifact := range artifacts {
		key := artifact.ID
		if key == "" {
			key = artifact.URI
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, artifact)
	}
	return out
}

func stringMetadata(metadata map[string]any, key string) string {
	if value, ok := metadata[key].(string); ok {
		return value
	}
	return ""
}

func boolMetadata(metadata map[string]any, key string) bool {
	if value, ok := metadata[key].(bool); ok {
		return value
	}
	return false
}

func uniqueStrings(values []string) []string {
	out := []string{}
	for _, value := range values {
		out = appendUniqueString(out, value)
	}
	return out
}

func appendUniqueString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func idempotencyKey(orgID string, key string) string {
	return orgID + "\x00" + key
}

func newExchangeID(prefix string, now time.Time) string {
	return fmt.Sprintf("%s_%d_%d", prefix, now.UnixNano(), atomic.AddUint64(&exchangeIDSequence, 1))
}

func cloneUploadSessions(values map[string]exchangeUploadSession) map[string]exchangeUploadSession {
	if values == nil {
		return nil
	}
	out := make(map[string]exchangeUploadSession, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func clonePackageStates(values map[string]*exchangePackageState) map[string]*exchangePackageState {
	if values == nil {
		return nil
	}
	out := make(map[string]*exchangePackageState, len(values))
	for key, value := range values {
		if value == nil {
			continue
		}
		copyValue := *value
		copyValue.StageHistory = append([]model.ExecutionStageEvent{}, value.StageHistory...)
		out[key] = &copyValue
	}
	return out
}

func cloneRecordingResultStates(values map[string]*recordingResultState) map[string]*recordingResultState {
	if values == nil {
		return nil
	}
	out := make(map[string]*recordingResultState, len(values))
	for key, value := range values {
		if value == nil {
			continue
		}
		copyValue := *value
		copyValue.Reviews = append([]model.ResultReviewRecord{}, value.Reviews...)
		copyValue.Revisions = append([]model.ResultRevisionRecord{}, value.Revisions...)
		out[key] = &copyValue
	}
	return out
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneBoolMap(values map[string]bool) map[string]bool {
	if values == nil {
		return nil
	}
	out := make(map[string]bool, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneInstallationStates(values map[string]*exchangeInstallationState) map[string]*exchangeInstallationState {
	if values == nil {
		return nil
	}
	out := make(map[string]*exchangeInstallationState, len(values))
	for key, value := range values {
		if value == nil {
			continue
		}
		copyValue := *value
		out[key] = &copyValue
	}
	return out
}

func cloneInstallationSessionStates(values map[string]*exchangeInstallationSessionState) map[string]*exchangeInstallationSessionState {
	if values == nil {
		return nil
	}
	out := make(map[string]*exchangeInstallationSessionState, len(values))
	for key, value := range values {
		if value == nil {
			continue
		}
		copyValue := *value
		out[key] = &copyValue
	}
	return out
}

func clonePairingChallengeStates(values map[string]exchangePairingChallengeState) map[string]exchangePairingChallengeState {
	if values == nil {
		return nil
	}
	out := make(map[string]exchangePairingChallengeState, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
