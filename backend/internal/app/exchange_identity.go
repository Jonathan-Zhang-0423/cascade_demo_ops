package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"cascade-demoops/backend/internal/model"
)

const (
	exchangeBootstrapSchemaVersion = "demoops.exchange_bootstrap.v1"
	exchangeInstallationKeyAlg     = "ed25519"
	exchangeResultKeyAlg           = "x25519-placeholder"
	exchangePairingDevKind         = "dev_pairing"
	exchangePairingBrowserKind     = "browser_login"
	exchangeAuthModeInstallation   = "installation_session"
	exchangeAuthModeDevToken       = "dev_token"
	exchangeAuthModeUnpaired       = "unpaired"
	defaultInstallationSessionTTL  = 12 * time.Hour
	exchangeIdentityFileName       = "exchange_identity.json"
)

type ExchangeIdentityStatus struct {
	ExchangeDiscovered bool   `json:"exchange_discovered"`
	InstallationPaired bool   `json:"installation_paired"`
	SessionValid       bool   `json:"session_valid"`
	ServerKeyID        string `json:"server_key_id,omitempty"`
	InstallIDSuffix    string `json:"install_id_suffix,omitempty"`
	AuthMode           string `json:"auth_mode"`
	BaseURLHost        string `json:"base_url_host,omitempty"`
	BaseURLPath        string `json:"base_url_path,omitempty"`
	Environment        string `json:"environment,omitempty"`
	DevPlaintext       bool   `json:"dev_plaintext,omitempty"`
}

type exchangeIdentityStore struct {
	path string
	mu   sync.Mutex
}

type exchangeIdentityRecord struct {
	InstallID                string    `json:"install_id"`
	DeviceID                 string    `json:"device_id"`
	SigningPublicKeyBase64   string    `json:"signing_public_key_base64"`
	SigningPrivateKeyBase64  string    `json:"signing_private_key_base64"`
	ResultPublicKeyBase64    string    `json:"result_public_key_base64"`
	ResultPrivateKeyBase64   string    `json:"result_private_key_base64"`
	ServerKeyID              string    `json:"server_key_id,omitempty"`
	ServerPublicKeyBase64    string    `json:"server_public_key_base64,omitempty"`
	SessionID                string    `json:"session_id,omitempty"`
	SessionToken             string    `json:"session_token,omitempty"`
	SessionExpiresAt         time.Time `json:"session_expires_at,omitempty"`
	ResultRecipientKeyID     string    `json:"result_recipient_key_id,omitempty"`
	ExchangeBaseURL          string    `json:"exchange_base_url,omitempty"`
	Environment              string    `json:"environment,omitempty"`
	LastBootstrapChallengeID string    `json:"last_bootstrap_challenge_id,omitempty"`
	CreatedAt                time.Time `json:"created_at"`
	UpdatedAt                time.Time `json:"updated_at"`
}

type ExchangeSession struct {
	BaseURL              string
	InstallID            string
	DeviceID             string
	SessionID            string
	SessionToken         string
	SessionExpiresAt     time.Time
	ServerKeyID          string
	ServerPublicKey      string
	SigningKeyID         string
	ResultRecipientKeyID string
	AuthMode             string
	Environment          string
	DevPlaintext         bool
}

func newExchangeIdentityStore(root string) *exchangeIdentityStore {
	return &exchangeIdentityStore{path: filepath.Join(root, exchangeIdentityFileName)}
}

func (s *Service) exchangeIdentityStore() *exchangeIdentityStore {
	return newExchangeIdentityStore(filepath.Join(s.runtime.DataRoot, "exchange_identity"))
}

func (s *Service) ExchangeIdentityStatus(ctx context.Context) ExchangeIdentityStatus {
	session, err := s.currentExchangeSession(ctx)
	if err != nil {
		return ExchangeIdentityStatus{AuthMode: exchangeAuthModeUnpaired}
	}
	return session.status()
}

func (s *Service) EnsureExchangeSession(ctx context.Context, orgID string, projectID string) (ExchangeSession, error) {
	baseURL, err := s.discoverExchangeBaseURL()
	if err != nil {
		return ExchangeSession{}, err
	}
	store := s.exchangeIdentityStore()
	record, err := store.loadOrCreate(s.runtime.Environment)
	if err != nil {
		return ExchangeSession{}, err
	}
	token := strings.TrimSpace(s.runtime.CloudExchangeToken)
	if isSessionUsable(record, baseURL, token) {
		return sessionFromRecord(record, baseURL, token), nil
	}

	client := &http.Client{Timeout: 30 * time.Second}
	var discovery model.ExchangeBootstrapDiscoveryResponse
	if useConfiguredExchangeBootstrap(baseURL, s.effectiveControlPlaneBaseURL(), s.runtime.Environment) {
		discovery = localBootstrapDiscovery(baseURL, s.runtime.Environment, time.Now().UTC())
	} else {
		discovery, err = cloudGetPublicJSON[model.ExchangeBootstrapDiscoveryResponse](ctx, client, baseURL+"/.well-known/cascade-exchange")
		if err != nil {
			if !isLocalExchangeBaseURL(baseURL) && !isOptionalExchangeDiscoveryError(err) {
				return ExchangeSession{}, err
			}
			discovery = localBootstrapDiscovery(baseURL, s.runtime.Environment, time.Now().UTC())
		}
	}
	if len(discovery.ServerKeyset) > 0 {
		record.ServerKeyID = discovery.ServerKeyset[0].KeyID
		record.ServerPublicKeyBase64 = discovery.ServerKeyset[0].PublicKey
	}
	record.ExchangeBaseURL = discovery.ExchangeBaseURL
	record.Environment = discovery.Environment
	record.LastBootstrapChallengeID = discovery.Challenge.ChallengeID

	signature, err := signExchangeChallenge(record, discovery.Challenge)
	if err != nil {
		return ExchangeSession{}, err
	}
	register := model.AppInstallationRegisterRequest{
		InstallID:          record.InstallID,
		DeviceID:           record.DeviceID,
		OrgID:              firstNonEmptyString(orgID, defaultDesktopOrgID),
		ProjectID:          projectID,
		AppVersion:         defaultDesktopAppVersion,
		RuntimeProfile:     "desktop-product-run",
		ChallengeID:        discovery.Challenge.ChallengeID,
		ChallengeSignature: signature,
		SigningPublicKey: model.AppInstallationPublicKey{
			KeyID:     installationSigningKeyID(record.InstallID),
			Alg:       exchangeInstallationKeyAlg,
			PublicKey: record.SigningPublicKeyBase64,
			Purpose:   "exchange_envelope_signing",
		},
		ResultPublicKey: model.AppInstallationPublicKey{
			KeyID:     installationResultKeyID(record.InstallID),
			Alg:       exchangeResultKeyAlg,
			PublicKey: record.ResultPublicKeyBase64,
			Purpose:   "result_delivery_encryption",
		},
	}
	if err := validateLocalInstallationRegistration(register, discovery.Challenge); err != nil {
		return ExchangeSession{}, err
	}
	response, err := cloudPostPublicJSON[model.AppInstallationSessionResponse](ctx, client, discovery.ExchangeBaseURL+"/v1/app-installations/register", register)
	if err != nil {
		if isOptionalExchangeDiscoveryError(err) {
			return ExchangeSession{}, newExchangeProtocolError(
				"cloud_auth_unavailable",
				"cloud_exchange.auth",
				"installation_endpoint_missing",
				"服务器尚未部署 App installation 自动配对接口，无法在无手动 token 的情况下上传执行包。",
				"请让云端启用 /v1/app-installations/register，或由 Dev Bridge/服务端预置 legacy exchange 凭据；不要要求最终用户手动填写 token。",
			)
		}
		return ExchangeSession{}, err
	}
	record.SessionID = response.SessionID
	record.SessionToken = response.SessionToken
	record.SessionExpiresAt = response.ExpiresAt
	record.ResultRecipientKeyID = response.ResultRecipientKeyID
	record.ServerKeyID = response.ServerKeyID
	record.UpdatedAt = time.Now().UTC()
	if err := store.save(record); err != nil {
		return ExchangeSession{}, err
	}
	return sessionFromRecord(record, baseURL, token), nil
}

func (s *Service) currentExchangeSession(ctx context.Context) (ExchangeSession, error) {
	baseURL, err := s.discoverExchangeBaseURL()
	if err != nil {
		return ExchangeSession{}, err
	}
	record, err := s.exchangeIdentityStore().load()
	if err != nil {
		return ExchangeSession{}, err
	}
	if !isSessionUsable(record, baseURL, s.runtime.CloudExchangeToken) {
		return ExchangeSession{}, errors.New("exchange installation session is not paired or has expired")
	}
	return sessionFromRecord(record, baseURL, s.runtime.CloudExchangeToken), nil
}

func (s *Service) discoverExchangeBaseURL() (string, error) {
	if configured := s.effectiveControlPlaneBaseURL(); configured != "" {
		return configured, nil
	}
	if record, err := s.exchangeIdentityStore().load(); err == nil && strings.TrimSpace(record.ExchangeBaseURL) != "" {
		return strings.TrimRight(record.ExchangeBaseURL, "/"), nil
	}
	return "", errors.New("DemoOps execution server is not configured; open App settings and enter the control plane base URL")
}

func useConfiguredExchangeBootstrap(baseURL string, configuredBaseURL string, environment string) bool {
	if strings.TrimSpace(configuredBaseURL) == "" {
		return false
	}
	if isLocalExchangeBaseURL(baseURL) {
		return true
	}
	return environment != "production"
}

func isOptionalExchangeDiscoveryError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, " returned 404:") ||
		strings.Contains(lower, " returned 405:") ||
		strings.Contains(lower, "404 page not found")
}

func localBootstrapDiscovery(baseURL string, environment string, now time.Time) model.ExchangeBootstrapDiscoveryResponse {
	if environment == "" {
		environment = "development"
	}
	requireHTTPS := !strings.Contains(baseURL, "127.0.0.1") && !strings.Contains(baseURL, "localhost")
	return model.ExchangeBootstrapDiscoveryResponse{
		SchemaVersion:   exchangeBootstrapSchemaVersion,
		ExchangeBaseURL: strings.TrimRight(baseURL, "/"),
		ServerKeyset: []model.ExchangeServerPublicKey{{
			KeyID:     defaultServerPublicKeyID,
			Alg:       defaultServerPublicKeyAlg,
			PublicKey: base64DevServerPublicKey(),
			NotBefore: now.Add(-time.Hour),
			ExpiresAt: now.Add(24 * time.Hour),
		}},
		SupportedCryptoSuites: []string{model.CryptoSuiteAES256GCM},
		SupportedCompression:  []string{model.CompressionNone, model.CompressionGzip},
		PairingMethods: []model.ExchangePairingMethod{
			{Kind: exchangePairingDevKind, Label: "联调自动配对", Description: "Dev Bridge 自动生成安装密钥和短期会话。"},
			{Kind: exchangePairingBrowserKind, Label: "浏览器登录确认", Description: "生产桌面端通过系统浏览器完成组织授权。"},
		},
		Challenge: model.ExchangePairingChallenge{
			ChallengeID: "challenge_" + randomHex(12),
			Nonce:       randomHex(24),
			Alg:         exchangeInstallationKeyAlg,
			ExpiresAt:   now.Add(10 * time.Minute),
		},
		ExpiresAt:   now.Add(10 * time.Minute),
		Environment: environment,
		Terms: model.ExchangeBootstrapTerms{
			RequireHTTPS: requireHTTPS,
			DevPlaintext: environment != "production",
		},
	}
}

func validateLocalInstallationRegistration(request model.AppInstallationRegisterRequest, challenge model.ExchangePairingChallenge) error {
	if request.InstallID == "" || request.SigningPublicKey.PublicKey == "" || request.ChallengeSignature == "" {
		return errors.New("installation registration requires install_id, signing public key, and challenge signature")
	}
	if request.ChallengeID != challenge.ChallengeID {
		return errors.New("installation registration challenge mismatch")
	}
	publicKey, err := base64.StdEncoding.DecodeString(request.SigningPublicKey.PublicKey)
	if err != nil {
		return err
	}
	signature, err := base64.StdEncoding.DecodeString(request.ChallengeSignature)
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), []byte(challengeSigningPayload(request.InstallID, challenge)), signature) {
		return errors.New("installation challenge signature verification failed")
	}
	return nil
}

func localInstallationSession(record exchangeIdentityRecord, request model.AppInstallationRegisterRequest, discovery model.ExchangeBootstrapDiscoveryResponse, now time.Time) model.AppInstallationSessionResponse {
	return model.AppInstallationSessionResponse{
		InstallID:            record.InstallID,
		SessionID:            "sess_" + randomHex(16),
		SessionToken:         "cassess_" + randomHex(32),
		ExpiresAt:            now.Add(defaultInstallationSessionTTL),
		OrgID:                firstNonEmptyString(request.OrgID, defaultDesktopOrgID),
		ProjectID:            request.ProjectID,
		ServerKeyID:          discovery.ServerKeyset[0].KeyID,
		ResultRecipientKeyID: installationResultKeyID(record.InstallID),
		AuthMode:             exchangeAuthModeInstallation,
	}
}

func (s *exchangeIdentityStore) loadOrCreate(environment string) (exchangeIdentityRecord, error) {
	record, err := s.load()
	if err == nil {
		return record, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return exchangeIdentityRecord{}, err
	}
	now := time.Now().UTC()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return exchangeIdentityRecord{}, err
	}
	resultPublic, resultPrivate := randomKeyBytes(32), randomKeyBytes(32)
	record = exchangeIdentityRecord{
		InstallID:               "install_" + randomHex(16),
		DeviceID:                "device_" + shortHash(runtime.GOOS+"-"+runtime.GOARCH+"-"+randomHex(8)),
		SigningPublicKeyBase64:  base64.StdEncoding.EncodeToString(publicKey),
		SigningPrivateKeyBase64: base64.StdEncoding.EncodeToString(privateKey),
		ResultPublicKeyBase64:   base64.StdEncoding.EncodeToString(resultPublic),
		ResultPrivateKeyBase64:  base64.StdEncoding.EncodeToString(resultPrivate),
		Environment:             environment,
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	if err := s.save(record); err != nil {
		return exchangeIdentityRecord{}, err
	}
	return record, nil
}

func (s *exchangeIdentityStore) load() (exchangeIdentityRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		return exchangeIdentityRecord{}, err
	}
	var record exchangeIdentityRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return exchangeIdentityRecord{}, err
	}
	if record.InstallID == "" || record.SigningPrivateKeyBase64 == "" {
		return exchangeIdentityRecord{}, errors.New("exchange identity is incomplete")
	}
	return record, nil
}

func (s *exchangeIdentityStore) save(record exchangeIdentityRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

func signExchangeChallenge(record exchangeIdentityRecord, challenge model.ExchangePairingChallenge) (string, error) {
	privateKey, err := base64.StdEncoding.DecodeString(record.SigningPrivateKeyBase64)
	if err != nil {
		return "", err
	}
	signature := ed25519.Sign(ed25519.PrivateKey(privateKey), []byte(challengeSigningPayload(record.InstallID, challenge)))
	return base64.StdEncoding.EncodeToString(signature), nil
}

func signCanonicalPayload(record exchangeIdentityRecord, envelope model.ExchangeEnvelope, canonicalPayload []byte) (string, error) {
	privateKey, err := base64.StdEncoding.DecodeString(record.SigningPrivateKeyBase64)
	if err != nil {
		return "", err
	}
	signingBytes := []byte(envelope.EnvelopeID + "\n" + envelope.OrgID + "\n" + envelope.ProjectID + "\n" + model.SHA256Hex(canonicalPayload))
	signature := ed25519.Sign(ed25519.PrivateKey(privateKey), signingBytes)
	return base64.StdEncoding.EncodeToString(signature), nil
}

func challengeSigningPayload(installID string, challenge model.ExchangePairingChallenge) string {
	return installID + "\n" + challenge.ChallengeID + "\n" + challenge.Nonce + "\n" + challenge.Alg
}

func isSessionUsable(record exchangeIdentityRecord, baseURL string, fallbackToken string) bool {
	if strings.TrimSpace(fallbackToken) != "" {
		return true
	}
	return record.InstallID != "" &&
		record.SessionToken != "" &&
		record.ExchangeBaseURL == strings.TrimRight(baseURL, "/") &&
		time.Now().UTC().Before(record.SessionExpiresAt.Add(-time.Minute))
}

func sessionFromRecord(record exchangeIdentityRecord, baseURL string, fallbackToken string) ExchangeSession {
	authMode := exchangeAuthModeInstallation
	token := record.SessionToken
	if strings.TrimSpace(fallbackToken) != "" {
		authMode = exchangeAuthModeDevToken
		token = strings.TrimSpace(fallbackToken)
	}
	return ExchangeSession{
		BaseURL:              strings.TrimRight(baseURL, "/"),
		InstallID:            record.InstallID,
		DeviceID:             record.DeviceID,
		SessionID:            record.SessionID,
		SessionToken:         token,
		SessionExpiresAt:     record.SessionExpiresAt,
		ServerKeyID:          firstNonEmptyString(record.ServerKeyID, defaultServerPublicKeyID),
		ServerPublicKey:      record.ServerPublicKeyBase64,
		SigningKeyID:         installationSigningKeyID(record.InstallID),
		ResultRecipientKeyID: firstNonEmptyString(record.ResultRecipientKeyID, installationResultKeyID(record.InstallID)),
		AuthMode:             authMode,
		Environment:          record.Environment,
		DevPlaintext:         record.Environment != "production",
	}
}

func (s ExchangeSession) status() ExchangeIdentityStatus {
	host, path := "", ""
	if parsed, err := url.Parse(s.BaseURL); err == nil {
		host = parsed.Host
		path = parsed.Path
	}
	return ExchangeIdentityStatus{
		ExchangeDiscovered: s.BaseURL != "",
		InstallationPaired: s.InstallID != "",
		SessionValid:       s.SessionToken != "",
		ServerKeyID:        s.ServerKeyID,
		InstallIDSuffix:    suffix(s.InstallID, 8),
		AuthMode:           s.AuthMode,
		BaseURLHost:        host,
		BaseURLPath:        path,
		Environment:        s.Environment,
		DevPlaintext:       s.DevPlaintext,
	}
}

func installationSigningKeyID(installID string) string {
	return installID + "/signing/v1"
}

func installationResultKeyID(installID string) string {
	return installID + "/result/v1"
}

func randomKeyBytes(size int) []byte {
	out := make([]byte, size)
	if _, err := rand.Read(out); err != nil {
		panic(fmt.Sprintf("crypto rand failed: %v", err))
	}
	return out
}

func randomHex(size int) string {
	return hex.EncodeToString(randomKeyBytes(size))
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
}

func suffix(value string, count int) string {
	if len(value) <= count {
		return value
	}
	return value[len(value)-count:]
}

func cloudGetPublicJSON[T any](ctx context.Context, client *http.Client, endpoint string) (T, error) {
	var zero T
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return zero, err
	}
	return cloudDoJSON[T](client, req)
}

func cloudPostPublicJSON[T any](ctx context.Context, client *http.Client, endpoint string, body any) (T, error) {
	var zero T
	payload, err := json.Marshal(body)
	if err != nil {
		return zero, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return zero, err
	}
	req.Header.Set("Content-Type", "application/json")
	return cloudDoJSON[T](client, req)
}

func isLocalExchangeBaseURL(baseURL string) bool {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	return host == "127.0.0.1" || host == "localhost"
}
