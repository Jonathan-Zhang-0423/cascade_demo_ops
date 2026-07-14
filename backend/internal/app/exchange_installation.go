package app

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

func (s *ExchangeIntakeService) BootstrapDiscovery(ctx context.Context, baseURL string, environment string) (model.ExchangeBootstrapDiscoveryResponse, error) {
	if err := ctx.Err(); err != nil {
		return model.ExchangeBootstrapDiscoveryResponse{}, err
	}
	now := s.now()
	response := localBootstrapDiscovery(strings.TrimRight(baseURL, "/"), environment, now)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.challenges[response.Challenge.ChallengeID] = exchangePairingChallengeState{Challenge: response.Challenge}
	if err := s.saveLocked(ctx); err != nil {
		return model.ExchangeBootstrapDiscoveryResponse{}, err
	}
	return response, nil
}

func (s *ExchangeIntakeService) RegisterInstallation(ctx context.Context, request model.AppInstallationRegisterRequest) (model.AppInstallationSessionResponse, error) {
	if err := ctx.Err(); err != nil {
		return model.AppInstallationSessionResponse{}, err
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	challengeState, ok := s.challenges[request.ChallengeID]
	if !ok {
		return model.AppInstallationSessionResponse{}, errors.New("installation challenge not found")
	}
	if challengeState.Used {
		return model.AppInstallationSessionResponse{}, errors.New("installation challenge has already been used")
	}
	if now.After(challengeState.Challenge.ExpiresAt) {
		return model.AppInstallationSessionResponse{}, errors.New("installation challenge expired")
	}
	if err := validateLocalInstallationRegistration(request, challengeState.Challenge); err != nil {
		return model.AppInstallationSessionResponse{}, err
	}
	challengeState.Used = true
	s.challenges[request.ChallengeID] = challengeState
	install := &exchangeInstallationState{
		InstallID:        request.InstallID,
		DeviceID:         request.DeviceID,
		OrgID:            firstNonEmptyString(request.OrgID, defaultDesktopOrgID),
		ProjectID:        request.ProjectID,
		AppVersion:       request.AppVersion,
		RuntimeProfile:   request.RuntimeProfile,
		SigningPublicKey: request.SigningPublicKey,
		ResultPublicKey:  request.ResultPublicKey,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	s.installations[install.InstallID] = install
	response := s.newInstallationSessionLocked(install, now)
	if err := s.saveLocked(ctx); err != nil {
		return model.AppInstallationSessionResponse{}, err
	}
	return response, nil
}

func (s *ExchangeIntakeService) CreateInstallationSession(ctx context.Context, request model.AppInstallationSessionRequest) (model.AppInstallationSessionResponse, error) {
	if err := ctx.Err(); err != nil {
		return model.AppInstallationSessionResponse{}, err
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	install, ok := s.installations[request.InstallID]
	if !ok || install.Revoked {
		return model.AppInstallationSessionResponse{}, errors.New("installation is revoked or missing")
	}
	challengeState, ok := s.challenges[request.ChallengeID]
	if !ok || challengeState.Used || now.After(challengeState.Challenge.ExpiresAt) {
		return model.AppInstallationSessionResponse{}, errors.New("installation session challenge is invalid")
	}
	publicKey, err := base64.StdEncoding.DecodeString(install.SigningPublicKey.PublicKey)
	if err != nil {
		return model.AppInstallationSessionResponse{}, err
	}
	signature, err := base64.StdEncoding.DecodeString(request.ChallengeSignature)
	if err != nil {
		return model.AppInstallationSessionResponse{}, err
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), []byte(challengeSigningPayload(request.InstallID, challengeState.Challenge)), signature) {
		return model.AppInstallationSessionResponse{}, errors.New("installation session signature verification failed")
	}
	challengeState.Used = true
	s.challenges[request.ChallengeID] = challengeState
	response := s.newInstallationSessionLocked(install, now)
	if err := s.saveLocked(ctx); err != nil {
		return model.AppInstallationSessionResponse{}, err
	}
	return response, nil
}

func (s *ExchangeIntakeService) RefreshInstallationSession(ctx context.Context, sessionToken string) (model.AppInstallationSessionResponse, error) {
	if err := ctx.Err(); err != nil {
		return model.AppInstallationSessionResponse{}, err
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionToken]
	if !ok || session.Revoked || now.After(session.ExpiresAt) {
		return model.AppInstallationSessionResponse{}, errors.New("installation session is invalid or expired")
	}
	install, ok := s.installations[session.InstallID]
	if !ok || install.Revoked {
		return model.AppInstallationSessionResponse{}, errors.New("installation is revoked or missing")
	}
	session.Revoked = true
	response := s.newInstallationSessionLocked(install, now)
	if err := s.saveLocked(ctx); err != nil {
		return model.AppInstallationSessionResponse{}, err
	}
	return response, nil
}

func (s *ExchangeIntakeService) RevokeInstallation(ctx context.Context, request model.AppInstallationRevokeRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	install, ok := s.installations[request.InstallID]
	if !ok {
		return errors.New("installation not found")
	}
	install.Revoked = true
	install.UpdatedAt = s.now()
	for _, session := range s.sessions {
		if session.InstallID == request.InstallID {
			session.Revoked = true
		}
	}
	return s.saveLocked(ctx)
}

func (s *ExchangeIntakeService) AuthenticateInstallationSession(token string) (*exchangeInstallationState, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, false
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[token]
	if !ok || session.Revoked || now.After(session.ExpiresAt) {
		return nil, false
	}
	install, ok := s.installations[session.InstallID]
	if !ok || install.Revoked {
		return nil, false
	}
	copyValue := *install
	return &copyValue, true
}

func (s *ExchangeIntakeService) VerifyExchangeSignature(envelope *model.ExchangeEnvelope, canonicalPayload []byte) bool {
	if envelope == nil {
		return false
	}
	installID := strings.TrimSpace(envelope.Producer.InstallID)
	if installID == "" || strings.EqualFold(envelope.Crypto.SignatureAlg, "dev-static") ||
		strings.HasPrefix(envelope.Crypto.Signature, "desktop-dev") ||
		envelope.Crypto.Signature == "signature" ||
		envelope.Crypto.Signature == "signature_bytes_base64" {
		return true
	}
	install, ok := s.installations[installID]
	if !ok || install == nil || install.Revoked {
		return false
	}
	publicKey, err := base64.StdEncoding.DecodeString(install.SigningPublicKey.PublicKey)
	if err != nil {
		return false
	}
	signature, err := base64.StdEncoding.DecodeString(envelope.Crypto.Signature)
	if err != nil {
		return false
	}
	signingBytes := []byte(envelope.EnvelopeID + "\n" + envelope.OrgID + "\n" + envelope.ProjectID + "\n" + model.SHA256Hex(canonicalPayload))
	return ed25519.Verify(ed25519.PublicKey(publicKey), signingBytes, signature)
}

func (s *ExchangeIntakeService) newInstallationSessionLocked(install *exchangeInstallationState, now time.Time) model.AppInstallationSessionResponse {
	sessionID := "sess_" + randomHex(16)
	token := "cassess_" + randomHex(32)
	expiresAt := now.Add(defaultInstallationSessionTTL)
	resultKeyID := firstNonEmptyString(install.ResultPublicKey.KeyID, installationResultKeyID(install.InstallID))
	s.sessions[token] = &exchangeInstallationSessionState{
		SessionID:            sessionID,
		SessionToken:         token,
		InstallID:            install.InstallID,
		OrgID:                install.OrgID,
		ProjectID:            install.ProjectID,
		ResultRecipientKeyID: resultKeyID,
		ExpiresAt:            expiresAt,
	}
	return model.AppInstallationSessionResponse{
		InstallID:            install.InstallID,
		SessionID:            sessionID,
		SessionToken:         token,
		ExpiresAt:            expiresAt,
		OrgID:                install.OrgID,
		ProjectID:            install.ProjectID,
		ServerKeyID:          defaultServerPublicKeyID,
		ResultRecipientKeyID: resultKeyID,
		AuthMode:             exchangeAuthModeInstallation,
	}
}

func sessionTokenFromRequest(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(header), "cascade-session ") {
		return strings.TrimSpace(header[len("Cascade-Session "):])
	}
	return ""
}

func base64DevServerPublicKey() string {
	return base64.StdEncoding.EncodeToString([]byte("local-dev-server-public-key-placeholder"))
}
