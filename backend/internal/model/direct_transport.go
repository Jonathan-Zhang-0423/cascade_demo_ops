package model

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	DirectTransportProtocolVersion          = "cascade.browser_agent_direct.v1"
	DirectWorkerProtocolVersion             = "cascade.browser_agent_worker.v1"
	DirectTransportCryptoSuite              = "hkdf-sha256+aes-256-gcm"
	BrowserAgentOutcomeVerifierRulesVersion = "browser-agent-outcome-verifier-rules-v1"
	DirectTransportDirectionUpload          = "app_to_browser_agent"
	DirectTransportDirectionResult          = "browser_agent_to_app"
	DirectTransportMaxClockSkew             = 5 * time.Minute
	// DirectArtifactChunkBytes bounds App and gateway memory while each chunk
	// remains independently authenticated by the lease-derived AEAD key.
	DirectArtifactChunkBytes = 4 << 20
)

type DirectHealthResponse struct {
	ProtocolVersion                       string          `json:"protocol_version"`
	CryptoSuite                           string          `json:"crypto_suite"`
	SupportedProtocolVersions             []string        `json:"supported_protocol_versions"`
	SupportedPackageSchemaVersions        []string        `json:"supported_package_schema_versions"`
	SupportedRuntimes                     []string        `json:"supported_runtimes"`
	SupportedWorkerProtocolVersions       []string        `json:"supported_worker_protocol_versions"`
	SupportedOutcomeVerifierRulesVersions []string        `json:"supported_outcome_verifier_rules_versions"`
	Capabilities                          map[string]bool `json:"capabilities"`
	ActiveLeases                          int             `json:"active_leases"`
	ServerTime                            time.Time       `json:"server_time"`
}

// DirectPortLease is issued by the fixed TLS control endpoint. DataURL points
// at a listener dedicated to one App installation for the lease lifetime.
// LeaseToken is returned once and must only be kept in the App process/vault.
type DirectPortLease struct {
	ProtocolVersion string    `json:"protocol_version"`
	LeaseID         string    `json:"lease_id"`
	InstallationID  string    `json:"installation_id"`
	DataURL         string    `json:"data_url"`
	DataPort        int       `json:"data_port"`
	LeaseToken      string    `json:"lease_token"`
	IssuedAt        time.Time `json:"issued_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	CryptoSuite     string    `json:"crypto_suite"`
	ServerTime      time.Time `json:"server_time"`
}

type DirectLeaseRequest struct {
	ProtocolVersion        string `json:"protocol_version"`
	InstallationID         string `json:"installation_id"`
	ClientVersion          string `json:"client_version,omitempty"`
	TimestampUnixMS        int64  `json:"timestamp_unix_ms"`
	RequestNonce           string `json:"request_nonce"`
	SigningPublicKeyBase64 string `json:"signing_public_key_base64"`
	SignatureBase64        string `json:"signature_base64"`
}

// DirectLeaseReleaseRequest lets an App retire its dedicated listener when a
// run and all verified downloads are finished. It uses the same installation
// signing key as lease allocation, so possession of the bootstrap token alone
// cannot release another installation's listener.
type DirectLeaseReleaseRequest struct {
	ProtocolVersion        string `json:"protocol_version"`
	InstallationID         string `json:"installation_id"`
	LeaseID                string `json:"lease_id"`
	TimestampUnixMS        int64  `json:"timestamp_unix_ms"`
	RequestNonce           string `json:"request_nonce"`
	SigningPublicKeyBase64 string `json:"signing_public_key_base64"`
	SignatureBase64        string `json:"signature_base64"`
}

// DirectEncryptedMessage is the only App-facing data-port payload. The
// timestamp and all routing metadata are authenticated as AES-GCM AAD.
type DirectEncryptedMessage struct {
	ProtocolVersion        string `json:"protocol_version"`
	LeaseID                string `json:"lease_id"`
	MessageID              string `json:"message_id"`
	MessageType            string `json:"message_type"`
	Direction              string `json:"direction"`
	TimestampUnixMS        int64  `json:"timestamp_unix_ms"`
	NonceBase64            string `json:"nonce_base64"`
	PlaintextDigestSHA256  string `json:"plaintext_digest_sha256"`
	CiphertextDigestSHA256 string `json:"ciphertext_digest_sha256"`
	CiphertextBase64       string `json:"ciphertext_base64"`
	CryptoSuite            string `json:"crypto_suite"`
}

type DirectPackageReceipt struct {
	ProtocolVersion string    `json:"protocol_version"`
	JobID           string    `json:"job_id"`
	PackageID       string    `json:"package_id"`
	PackageDigest   string    `json:"package_digest_sha256"`
	Status          string    `json:"status"`
	Stage           string    `json:"stage"`
	AcceptedAt      time.Time `json:"accepted_at"`
}

// DirectCredentialEnvelope is encrypted as a DirectEncryptedMessage and is
// never written to the gateway spool. Every field binds the credential to one
// approved package, job, installation, lease, scope, and expiry.
type DirectCredentialEnvelope struct {
	ProtocolVersion   string                `json:"protocol_version"`
	LeaseID           string                `json:"lease_id"`
	InstallationID    string                `json:"installation_id"`
	JobID             string                `json:"job_id"`
	PackageID         string                `json:"package_id"`
	PackageDigest     string                `json:"package_digest_sha256"`
	GrantID           string                `json:"grant_id"`
	SecretRef         string                `json:"secret_ref"`
	IssuedAt          time.Time             `json:"issued_at"`
	ExpiresAt         time.Time             `json:"expires_at"`
	AllowedDomains    []string              `json:"allowed_domains"`
	AllowedOperations []string              `json:"allowed_operations"`
	Credential        DirectCredentialValue `json:"credential"`
}

// DirectCredentialValue exists only in App, gateway memory, the authenticated
// loopback Worker response, and the active Browser Agent process.
type DirectCredentialValue struct {
	SecretRef         string    `json:"secret_ref"`
	Username          string    `json:"username"`
	Password          string    `json:"password"`
	ExpiresAt         time.Time `json:"expires_at"`
	AllowedDomains    []string  `json:"allowed_domains"`
	AllowedOperations []string  `json:"allowed_operations"`
}

type DirectCredentialReceipt struct {
	ProtocolVersion string    `json:"protocol_version"`
	JobID           string    `json:"job_id"`
	PackageID       string    `json:"package_id"`
	GrantID         string    `json:"grant_id"`
	SecretRef       string    `json:"secret_ref"`
	Status          string    `json:"status"`
	Stage           string    `json:"stage"`
	AcceptedAt      time.Time `json:"accepted_at"`
}

type DirectJobStatus struct {
	ProtocolVersion    string           `json:"protocol_version"`
	JobID              string           `json:"job_id"`
	PackageID          string           `json:"package_id"`
	Status             string           `json:"status"`
	Stage              string           `json:"stage"`
	Message            string           `json:"message,omitempty"`
	WaitingReason      string           `json:"waiting_reason,omitempty"`
	BlockingErrorCode  string           `json:"blocking_error_code,omitempty"`
	NextAction         string           `json:"next_action,omitempty"`
	RequiresReapproval bool             `json:"requires_reapproval,omitempty"`
	ProgressPercent    int              `json:"progress_percent,omitempty"`
	ResultPackageID    string           `json:"result_package_id,omitempty"`
	Artifacts          []DirectArtifact `json:"artifacts,omitempty"`
	// ReunderstandingIssues is populated only when the authoritative outcome
	// verifier decision requires the App to regenerate its business plan. It
	// intentionally contains structured, redacted fields rather than raw page
	// content, browser output, or credentials.
	ReunderstandingIssues []DirectReunderstandingIssue `json:"reunderstanding_issues,omitempty"`
	UpdatedAt             time.Time                    `json:"updated_at"`
}

// DirectJobCancelRequest binds an operator cancellation to the active Direct
// lease and job. The reason is diagnostic metadata only; it must not contain
// credentials or browser evidence payloads.
type DirectJobCancelRequest struct {
	ProtocolVersion string `json:"protocol_version"`
	InstallationID  string `json:"installation_id"`
	JobID           string `json:"job_id"`
	Reason          string `json:"reason,omitempty"`
}

type DirectJobCancelReceipt struct {
	ProtocolVersion string    `json:"protocol_version"`
	JobID           string    `json:"job_id"`
	Status          string    `json:"status"`
	Stage           string    `json:"stage"`
	CanceledAt      time.Time `json:"canceled_at"`
}

// DirectReunderstandingIssue is the stable Direct API representation of one
// validation problem that the App must use when re-understanding a failed run.
// EvidenceIDs are opaque references only; the Gateway never returns evidence
// payloads or browser/page objects in job status.
type DirectReunderstandingIssue struct {
	IssueID              string                `json:"issue_id,omitempty"`
	Code                 string                `json:"code"`
	StageID              string                `json:"stage_id,omitempty"`
	NodeID               string                `json:"node_id,omitempty"`
	Severity             FindingSeverity       `json:"severity,omitempty"`
	Required             bool                  `json:"required,omitempty"`
	Summary              string                `json:"summary,omitempty"`
	Impact               string                `json:"impact,omitempty"`
	Suggestion           string                `json:"suggestion,omitempty"`
	NextStep             string                `json:"next_step,omitempty"`
	ResponsibilityDomain ValidationCheckDomain `json:"responsibility_domain,omitempty"`
	EvidenceIDs          []string              `json:"evidence_ids,omitempty"`
}

// StableDirectReunderstandingIssueID binds selection to the structured issue
// identity without exposing validation text or browser evidence payloads.
func StableDirectReunderstandingIssueID(issue DirectReunderstandingIssue) string {
	seed := strings.Join([]string{
		strings.TrimSpace(issue.Code),
		strings.TrimSpace(issue.StageID),
		strings.TrimSpace(issue.NodeID),
		strings.TrimSpace(string(issue.ResponsibilityDomain)),
	}, "|")
	sum := sha256.Sum256([]byte(seed))
	return "issue_" + hex.EncodeToString(sum[:12])
}

type DirectResultAckRequest struct {
	ProtocolVersion     string    `json:"protocol_version"`
	InstallationID      string    `json:"installation_id"`
	JobID               string    `json:"job_id"`
	ResultPackageID     string    `json:"result_package_id"`
	ReceivedArtifactIDs []string  `json:"received_artifact_ids"`
	VerifiedChecksums   bool      `json:"verified_checksums"`
	AckedAt             time.Time `json:"acked_at"`
}

type DirectResultAckReceipt struct {
	ProtocolVersion     string    `json:"protocol_version"`
	JobID               string    `json:"job_id"`
	ResultPackageID     string    `json:"result_package_id"`
	ReceivedArtifactIDs []string  `json:"received_artifact_ids"`
	VerifiedChecksums   bool      `json:"verified_checksums"`
	AckedAt             time.Time `json:"acked_at"`
}

type DirectArtifact struct {
	ArtifactID string `json:"artifact_id"`
	Role       string `json:"role,omitempty"`
	Kind       string `json:"kind,omitempty"`
	FileName   string `json:"file_name"`
	MimeType   string `json:"mime_type,omitempty"`
	SHA256     string `json:"sha256"`
	SizeBytes  int64  `json:"size_bytes"`
}

func NewDirectTransportToken(byteCount int) (string, error) {
	if byteCount < 32 {
		return "", errors.New("direct transport tokens must contain at least 256 bits")
	}
	value := make([]byte, byteCount)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func NewDirectTransportNonce() (string, error) {
	value := make([]byte, 24)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func EncryptDirectTransportJSON(value any, lease DirectPortLease, messageID, messageType, direction string, now time.Time) (DirectEncryptedMessage, error) {
	plain, err := CanonicalJSON(value)
	if err != nil {
		return DirectEncryptedMessage{}, err
	}
	return EncryptDirectTransportBytes(plain, lease, messageID, messageType, direction, now)
}

func EncryptDirectTransportBytes(plain []byte, lease DirectPortLease, messageID, messageType, direction string, now time.Time) (DirectEncryptedMessage, error) {
	if err := validateDirectLeaseForCrypto(lease, now); err != nil {
		return DirectEncryptedMessage{}, err
	}
	messageID = strings.TrimSpace(messageID)
	messageType = strings.TrimSpace(messageType)
	direction = strings.TrimSpace(direction)
	if messageID == "" || messageType == "" || !validDirectDirection(direction) {
		return DirectEncryptedMessage{}, errors.New("direct transport message id, type, and valid direction are required")
	}
	now = now.UTC()
	message := DirectEncryptedMessage{
		ProtocolVersion: DirectTransportProtocolVersion,
		LeaseID:         lease.LeaseID, MessageID: messageID, MessageType: messageType,
		Direction: direction, TimestampUnixMS: now.UnixMilli(),
		PlaintextDigestSHA256: SHA256Hex(plain), CryptoSuite: DirectTransportCryptoSuite,
	}
	key, err := deriveDirectTransportKey(lease, message)
	if err != nil {
		return DirectEncryptedMessage{}, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return DirectEncryptedMessage{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return DirectEncryptedMessage{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return DirectEncryptedMessage{}, err
	}
	message.NonceBase64 = base64.RawStdEncoding.EncodeToString(nonce)
	aad := directTransportAAD(message, lease.DataPort)
	ciphertext := aead.Seal(nil, nonce, plain, aad)
	message.CiphertextBase64 = base64.StdEncoding.EncodeToString(ciphertext)
	message.CiphertextDigestSHA256 = SHA256Hex(ciphertext)
	return message, nil
}

func DecryptDirectTransportJSON(message DirectEncryptedMessage, lease DirectPortLease, expectedType, expectedDirection string, now time.Time, target any) error {
	if target == nil {
		return errors.New("direct transport target is required")
	}
	plain, err := DecryptDirectTransportBytes(message, lease, expectedType, expectedDirection, now)
	if err != nil {
		return err
	}
	return json.Unmarshal(plain, target)
}

func DecryptDirectTransportBytes(message DirectEncryptedMessage, lease DirectPortLease, expectedType, expectedDirection string, now time.Time) ([]byte, error) {
	if err := validateDirectLeaseForCrypto(lease, now); err != nil {
		return nil, err
	}
	if message.ProtocolVersion != DirectTransportProtocolVersion || message.CryptoSuite != DirectTransportCryptoSuite {
		return nil, errors.New("unsupported direct transport protocol or crypto suite")
	}
	if message.LeaseID != lease.LeaseID || message.MessageType != expectedType || message.Direction != expectedDirection {
		return nil, errors.New("direct transport message binding mismatch")
	}
	messageTime := time.UnixMilli(message.TimestampUnixMS)
	if delta := now.UTC().Sub(messageTime); delta > DirectTransportMaxClockSkew || delta < -DirectTransportMaxClockSkew {
		return nil, errors.New("direct transport message timestamp is outside the allowed clock skew")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(message.CiphertextBase64)
	if err != nil {
		return nil, errors.New("invalid direct transport ciphertext encoding")
	}
	if !hmac.Equal([]byte(strings.ToLower(message.CiphertextDigestSHA256)), []byte(SHA256Hex(ciphertext))) {
		return nil, errors.New("direct transport ciphertext digest mismatch")
	}
	nonce, err := base64.RawStdEncoding.DecodeString(message.NonceBase64)
	if err != nil {
		return nil, errors.New("invalid direct transport nonce encoding")
	}
	key, err := deriveDirectTransportKey(lease, message)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != aead.NonceSize() {
		return nil, errors.New("invalid direct transport nonce size")
	}
	plain, err := aead.Open(nil, nonce, ciphertext, directTransportAAD(message, lease.DataPort))
	if err != nil {
		return nil, errors.New("direct transport authentication failed")
	}
	if !hmac.Equal([]byte(strings.ToLower(message.PlaintextDigestSHA256)), []byte(SHA256Hex(plain))) {
		return nil, errors.New("direct transport plaintext digest mismatch")
	}
	return plain, nil
}

func DirectTransportRequestSignature(lease DirectPortLease, method, path string, timestampUnixMS int64, nonce, bodyDigest string) (string, error) {
	message := DirectEncryptedMessage{LeaseID: lease.LeaseID, MessageID: "request-auth", MessageType: "request-auth", Direction: DirectTransportDirectionUpload, TimestampUnixMS: timestampUnixMS}
	key, err := deriveDirectTransportKey(lease, message)
	if err != nil {
		return "", err
	}
	canonical := strings.Join([]string{strings.ToUpper(strings.TrimSpace(method)), strings.TrimSpace(path), strconv.FormatInt(timestampUnixMS, 10), strings.TrimSpace(nonce), strings.ToLower(strings.TrimSpace(bodyDigest))}, "\n")
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func VerifyDirectTransportRequestSignature(lease DirectPortLease, method, path string, timestampUnixMS int64, nonce, bodyDigest, signature string, now time.Time) error {
	requestTime := time.UnixMilli(timestampUnixMS)
	if delta := now.UTC().Sub(requestTime); delta > DirectTransportMaxClockSkew || delta < -DirectTransportMaxClockSkew {
		return errors.New("direct transport request timestamp is outside the allowed clock skew")
	}
	expected, err := DirectTransportRequestSignature(lease, method, path, timestampUnixMS, nonce, bodyDigest)
	if err != nil {
		return err
	}
	provided, err := hex.DecodeString(strings.TrimSpace(signature))
	if err != nil || !hmac.Equal(provided, mustDecodeHex(expected)) {
		return errors.New("direct transport request signature is invalid")
	}
	return nil
}

func validateDirectLeaseForCrypto(lease DirectPortLease, now time.Time) error {
	if lease.ProtocolVersion != DirectTransportProtocolVersion || lease.CryptoSuite != DirectTransportCryptoSuite {
		return errors.New("unsupported direct transport lease")
	}
	if strings.TrimSpace(lease.LeaseID) == "" || strings.TrimSpace(lease.InstallationID) == "" || lease.DataPort < 1 || lease.DataPort > 65535 {
		return errors.New("direct transport lease binding is incomplete")
	}
	if strings.TrimSpace(lease.LeaseToken) == "" {
		return errors.New("direct transport lease token is required")
	}
	if !lease.ExpiresAt.IsZero() && !now.UTC().Before(lease.ExpiresAt) {
		return errors.New("direct transport lease has expired")
	}
	return nil
}

func deriveDirectTransportKey(lease DirectPortLease, message DirectEncryptedMessage) ([]byte, error) {
	if strings.TrimSpace(lease.LeaseToken) == "" {
		return nil, errors.New("direct transport lease token is required")
	}
	salt := sha256.Sum256([]byte(strings.Join([]string{DirectTransportProtocolVersion, lease.InstallationID, lease.LeaseID, strconv.Itoa(lease.DataPort), strconv.FormatInt(message.TimestampUnixMS, 10)}, "\x00")))
	info := []byte(strings.Join([]string{message.Direction, message.MessageType, message.MessageID}, "\x00"))
	return hkdfSHA256([]byte(lease.LeaseToken), salt[:], info, 32), nil
}

func hkdfSHA256(secret, salt, info []byte, length int) []byte {
	extract := hmac.New(sha256.New, salt)
	_, _ = extract.Write(secret)
	prk := extract.Sum(nil)
	result := make([]byte, 0, length)
	previous := []byte(nil)
	for counter := byte(1); len(result) < length; counter++ {
		expand := hmac.New(sha256.New, prk)
		_, _ = expand.Write(previous)
		_, _ = expand.Write(info)
		_, _ = expand.Write([]byte{counter})
		previous = expand.Sum(nil)
		result = append(result, previous...)
	}
	return result[:length]
}

func directTransportAAD(message DirectEncryptedMessage, port int) []byte {
	return []byte(strings.Join([]string{
		message.ProtocolVersion, message.LeaseID, strconv.Itoa(port), message.MessageID,
		message.MessageType, message.Direction, strconv.FormatInt(message.TimestampUnixMS, 10),
		message.NonceBase64, message.PlaintextDigestSHA256, message.CryptoSuite,
	}, "\n"))
}

func validDirectDirection(value string) bool {
	return value == DirectTransportDirectionUpload || value == DirectTransportDirectionResult
}

func mustDecodeHex(value string) []byte {
	decoded, _ := hex.DecodeString(value)
	return decoded
}

func ValidateDirectLeaseRequest(request DirectLeaseRequest, now time.Time) error {
	if request.ProtocolVersion != DirectTransportProtocolVersion {
		return errors.New("unsupported direct transport protocol")
	}
	if strings.TrimSpace(request.InstallationID) == "" || len(request.InstallationID) > 128 || strings.ContainsAny(request.InstallationID, "\r\n\x00") {
		return errors.New("installation_id has an invalid format")
	}
	if strings.TrimSpace(request.RequestNonce) == "" || len(request.RequestNonce) > 256 {
		return errors.New("request_nonce has an invalid format")
	}
	publicKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(request.SigningPublicKeyBase64))
	if err != nil || len(publicKey) != ed25519.PublicKeySize || DirectInstallationID(ed25519.PublicKey(publicKey)) != request.InstallationID {
		return errors.New("lease installation public key binding is invalid")
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(request.SignatureBase64))
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(publicKey), DirectLeaseRequestSigningPayload(request), signature) {
		return errors.New("lease installation signature is invalid")
	}
	requestTime := time.UnixMilli(request.TimestampUnixMS)
	if delta := now.UTC().Sub(requestTime); delta > DirectTransportMaxClockSkew || delta < -DirectTransportMaxClockSkew {
		return fmt.Errorf("lease request timestamp is outside the allowed clock skew")
	}
	return nil
}

func DirectInstallationID(publicKey ed25519.PublicKey) string {
	return "direct_install_" + SHA256Hex(publicKey)[:32]
}

func DirectLeaseRequestSigningPayload(request DirectLeaseRequest) []byte {
	return []byte(strings.Join([]string{
		request.ProtocolVersion, request.InstallationID, request.ClientVersion,
		strconv.FormatInt(request.TimestampUnixMS, 10), request.RequestNonce,
		request.SigningPublicKeyBase64,
	}, "\n"))
}

func DirectLeaseReleaseRequestSigningPayload(request DirectLeaseReleaseRequest) []byte {
	return []byte(strings.Join([]string{
		"release_lease", request.ProtocolVersion, request.InstallationID, request.LeaseID,
		strconv.FormatInt(request.TimestampUnixMS, 10), request.RequestNonce,
		request.SigningPublicKeyBase64,
	}, "\n"))
}

func ValidateDirectLeaseReleaseRequest(request DirectLeaseReleaseRequest, now time.Time) error {
	if request.ProtocolVersion != DirectTransportProtocolVersion {
		return errors.New("unsupported direct transport protocol")
	}
	if strings.TrimSpace(request.InstallationID) == "" || strings.TrimSpace(request.LeaseID) == "" || len(request.InstallationID) > 128 || len(request.LeaseID) > 256 || strings.ContainsAny(request.InstallationID+request.LeaseID, "\r\n\x00") {
		return errors.New("lease release binding is invalid")
	}
	if strings.TrimSpace(request.RequestNonce) == "" || len(request.RequestNonce) > 256 {
		return errors.New("request_nonce has an invalid format")
	}
	publicKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(request.SigningPublicKeyBase64))
	if err != nil || len(publicKey) != ed25519.PublicKeySize || DirectInstallationID(ed25519.PublicKey(publicKey)) != request.InstallationID {
		return errors.New("lease release public key binding is invalid")
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(request.SignatureBase64))
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(publicKey), DirectLeaseReleaseRequestSigningPayload(request), signature) {
		return errors.New("lease release signature is invalid")
	}
	requestTime := time.UnixMilli(request.TimestampUnixMS)
	if delta := now.UTC().Sub(requestTime); delta > DirectTransportMaxClockSkew || delta < -DirectTransportMaxClockSkew {
		return errors.New("lease release timestamp is outside the allowed clock skew")
	}
	return nil
}
