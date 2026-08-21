package direct

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ProtocolVersion             = "cascade.browser_agent_direct.v1"
	WorkerProtocolVersion       = "cascade.browser_agent_worker.v1"
	OutcomeVerifierRulesVersion = "browser-agent-outcome-verifier-rules-v1"
	CryptoSuite                 = "hkdf-sha256+aes-256-gcm"
	ControlPort                 = 18443
	WorkerPort                  = 18444
	DataPortMin                 = 24000
	DataPortMax                 = 24031
	MaxClockSkew                = 5 * time.Minute
	MaxChunkSize                = 4 * 1024 * 1024
	MaxArtifactSize             = 256 * 1024 * 1024
)

var ErrReplay = errors.New("direct request nonce has already been used")

type DirectLeaseRequest struct {
	ProtocolVersion        string `json:"protocol_version"`
	InstallationID         string `json:"installation_id"`
	ClientVersion          string `json:"client_version"`
	TimestampUnixMS        int64  `json:"timestamp_unix_ms"`
	RequestNonce           string `json:"request_nonce"`
	SigningPublicKeyBase64 string `json:"signing_public_key_base64"`
	SignatureBase64        string `json:"signature_base64"`
}

type DirectPortLease struct {
	LeaseID        string `json:"lease_id"`
	InstallationID string `json:"installation_id"`
	DataURL        string `json:"data_url"`
	DataPort       int    `json:"data_port"`
	LeaseToken     string `json:"lease_token"`
	IssuedAt       int64  `json:"issued_at"`
	ExpiresAt      int64  `json:"expires_at"`
	CryptoSuite    string `json:"crypto_suite"`
	ServerTime     int64  `json:"server_time"`
}

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
	JobID           string `json:"job_id"`
	PackageID       string `json:"package_id"`
	PackageSHA256   string `json:"package_sha256"`
	Status          string `json:"status"`
	Stage           string `json:"stage,omitempty"`
	CreatedAtUnixMS int64  `json:"created_at_unix_ms"`
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

type DirectJobStatus struct {
	JobID             string     `json:"job_id"`
	PackageID         string     `json:"package_id"`
	Status            string     `json:"status"`
	Stage             string     `json:"stage,omitempty"`
	Progress          int        `json:"progress,omitempty"`
	ResultPackageID   string     `json:"result_package_id,omitempty"`
	ArtifactIDs       []string   `json:"artifact_ids,omitempty"`
	AckedAt           *time.Time `json:"acked_at,omitempty"`
	VerifiedChecksums bool       `json:"verified_checksums,omitempty"`
	UpdatedAtUnixMS   int64      `json:"updated_at_unix_ms"`
}

type DirectCredentialEnvelope struct {
	JobID             string   `json:"job_id"`
	PackageID         string   `json:"package_id"`
	PackageSHA256     string   `json:"package_sha256"`
	GrantID           string   `json:"grant_id"`
	SecretRef         string   `json:"secret_ref"`
	InstallationID    string   `json:"installation_id"`
	LeaseID           string   `json:"lease_id"`
	AllowedDomains    []string `json:"allowed_domains"`
	AllowedOperations []string `json:"allowed_operations"`
	ExpiresAtUnixMS   int64    `json:"expires_at_unix_ms"`
	Secret            string   `json:"secret"`
}

type DirectArtifactDescriptor struct {
	ArtifactID string `json:"artifact_id"`
	Kind       string `json:"kind"`
	MimeType   string `json:"mime_type,omitempty"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	ChunkSize  int    `json:"chunk_size"`
	ChunkCount int    `json:"chunk_count"`
}

func HashSHA256(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func InstallationID(publicKey ed25519.PublicKey) string {
	sum := sha256.Sum256(publicKey)
	// Keep the wire identity byte-for-byte compatible with the Server Direct
	// transport. A lease is signed by this key, so both ends must derive the
	// same stable identifier before the Gateway can accept it.
	return "direct_install_" + hex.EncodeToString(sum[:])[:32]
}

func LeaseRequestSigningBytes(r DirectLeaseRequest) []byte {
	return []byte(strings.Join([]string{r.ProtocolVersion, r.InstallationID, r.ClientVersion, strconv.FormatInt(r.TimestampUnixMS, 10), r.RequestNonce, r.SigningPublicKeyBase64}, "\n"))
}

func VerifyLeaseRequest(r DirectLeaseRequest, now time.Time, seen *NonceSet) (ed25519.PublicKey, error) {
	if r.ProtocolVersion != ProtocolVersion || r.InstallationID == "" || r.ClientVersion == "" || r.RequestNonce == "" || r.TimestampUnixMS == 0 {
		return nil, errors.New("invalid direct lease request")
	}
	if delta := now.Sub(time.UnixMilli(r.TimestampUnixMS)); delta > MaxClockSkew || delta < -MaxClockSkew {
		return nil, errors.New("direct lease request timestamp outside allowed clock skew")
	}
	pub, err := base64.StdEncoding.DecodeString(r.SigningPublicKeyBase64)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("invalid direct signing public key")
	}
	key := ed25519.PublicKey(pub)
	if InstallationID(key) != r.InstallationID {
		return nil, errors.New("installation_id does not match signing public key")
	}
	sig, err := base64.StdEncoding.DecodeString(r.SignatureBase64)
	if err != nil || len(sig) != ed25519.SignatureSize || !ed25519.Verify(key, LeaseRequestSigningBytes(r), sig) {
		return nil, errors.New("invalid direct lease signature")
	}
	if seen != nil && !seen.Use(r.InstallationID+":"+r.RequestNonce) {
		return nil, ErrReplay
	}
	return key, nil
}

func SignDataRequest(method, escapedPath string, timestamp int64, nonce, bodyDigest, leaseToken, installationID, leaseID string, port int) string {
	key, err := deriveWireKey(leaseToken, installationID, leaseID, port, timestamp, "app_to_browser_agent", "request-auth", "request-auth")
	if err != nil {
		return ""
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(strings.Join([]string{strings.ToUpper(strings.TrimSpace(method)), strings.TrimSpace(escapedPath), strconv.FormatInt(timestamp, 10), strings.TrimSpace(nonce), strings.ToLower(strings.TrimSpace(bodyDigest))}, "\n")))
	return hex.EncodeToString(mac.Sum(nil))
}

func VerifyDataRequest(req *http.Request, body []byte, leaseToken, installationID, leaseID string, port int, now time.Time, seen *NonceSet) error {
	ts, err := strconv.ParseInt(req.Header.Get("X-Cascade-Timestamp"), 10, 64)
	if err != nil {
		return errors.New("missing direct timestamp")
	}
	if delta := now.Sub(time.UnixMilli(ts)); delta > MaxClockSkew || delta < -MaxClockSkew {
		return errors.New("direct request timestamp outside allowed clock skew")
	}
	nonce := req.Header.Get("X-Cascade-Nonce")
	digest := HashSHA256(body)
	if nonce == "" || !hmac.Equal([]byte(digest), []byte(req.Header.Get("X-Cascade-Body-SHA256"))) {
		return errors.New("direct request body digest mismatch")
	}
	want := SignDataRequest(req.Method, req.URL.EscapedPath(), ts, nonce, digest, leaseToken, installationID, leaseID, port)
	if subtle.ConstantTimeCompare([]byte(strings.ToLower(want)), []byte(strings.ToLower(req.Header.Get("X-Cascade-Signature")))) != 1 {
		return errors.New("invalid direct request signature")
	}
	if seen != nil && !seen.Use(installationID+":"+nonce) {
		return ErrReplay
	}
	return nil
}

func deriveWireKey(leaseToken, installationID, leaseID string, port int, timestamp int64, direction, messageType, messageID string) ([]byte, error) {
	if strings.TrimSpace(leaseToken) == "" || strings.TrimSpace(installationID) == "" || strings.TrimSpace(leaseID) == "" || port < 1 || port > 65535 {
		return nil, errors.New("direct transport key binding is incomplete")
	}
	salt := sha256.Sum256([]byte(strings.Join([]string{ProtocolVersion, installationID, leaseID, strconv.Itoa(port), strconv.FormatInt(timestamp, 10)}, "\x00")))
	info := []byte(strings.Join([]string{direction, messageType, messageID}, "\x00"))
	extract := hmac.New(sha256.New, salt[:])
	extract.Write([]byte(leaseToken))
	prk := extract.Sum(nil)
	var previous, okm []byte
	for counter := byte(1); len(okm) < 32; counter++ {
		expand := hmac.New(sha256.New, prk)
		expand.Write(previous)
		expand.Write(info)
		expand.Write([]byte{counter})
		previous = expand.Sum(nil)
		okm = append(okm, previous...)
	}
	return okm[:32], nil
}

func aad(m DirectEncryptedMessage, port int) []byte {
	return []byte(strings.Join([]string{m.ProtocolVersion, m.LeaseID, strconv.Itoa(port), m.MessageID, m.MessageType, m.Direction, strconv.FormatInt(m.TimestampUnixMS, 10), m.NonceBase64, m.PlaintextDigestSHA256, m.CryptoSuite}, "\n"))
}

func EncryptMessage(leaseToken, installationID, leaseID string, port int, messageID, messageType, direction string, plaintext []byte, now time.Time) (DirectEncryptedMessage, error) {
	ts := now.UnixMilli()
	key, err := deriveWireKey(leaseToken, installationID, leaseID, port, ts, direction, messageType, messageID)
	if err != nil {
		return DirectEncryptedMessage{}, err
	}
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return DirectEncryptedMessage{}, err
	}
	m := DirectEncryptedMessage{ProtocolVersion: ProtocolVersion, LeaseID: leaseID, MessageID: messageID, MessageType: messageType, Direction: direction, TimestampUnixMS: ts, NonceBase64: base64.StdEncoding.EncodeToString(nonce), PlaintextDigestSHA256: HashSHA256(plaintext), CryptoSuite: CryptoSuite}
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	ct := gcm.Seal(nil, nonce, plaintext, aad(m, port))
	m.CiphertextBase64 = base64.StdEncoding.EncodeToString(ct)
	m.CiphertextDigestSHA256 = HashSHA256(ct)
	return m, nil
}

func (m DirectEncryptedMessage) Decrypt(leaseToken, installationID string, port int) ([]byte, error) {
	if m.ProtocolVersion != ProtocolVersion || m.CryptoSuite != CryptoSuite || m.MessageID == "" || m.MessageType == "" || m.Direction == "" {
		return nil, errors.New("invalid direct encrypted message metadata")
	}
	ct, err := base64.StdEncoding.DecodeString(m.CiphertextBase64)
	if err != nil || HashSHA256(ct) != m.CiphertextDigestSHA256 {
		return nil, errors.New("direct ciphertext digest mismatch")
	}
	nonce, err := base64.StdEncoding.DecodeString(m.NonceBase64)
	if err != nil || len(nonce) != 12 {
		return nil, errors.New("invalid direct message nonce")
	}
	key, err := deriveWireKey(leaseToken, installationID, m.LeaseID, port, m.TimestampUnixMS, m.Direction, m.MessageType, m.MessageID)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	pt, err := gcm.Open(nil, nonce, ct, aad(m, port))
	if err != nil || HashSHA256(pt) != m.PlaintextDigestSHA256 {
		return nil, errors.New("direct message authentication failed")
	}
	return pt, nil
}

func ValidateMessageFreshness(m DirectEncryptedMessage, now time.Time, seen *NonceSet) error {
	if m.ProtocolVersion != ProtocolVersion || m.CryptoSuite != CryptoSuite || m.LeaseID == "" || m.MessageID == "" || m.MessageType == "" || m.Direction == "" {
		return errors.New("invalid direct encrypted message metadata")
	}
	if delta := now.Sub(time.UnixMilli(m.TimestampUnixMS)); delta > MaxClockSkew || delta < -MaxClockSkew {
		return errors.New("direct message timestamp outside allowed clock skew")
	}
	if seen != nil && !seen.Use("message:"+m.LeaseID+":"+m.MessageID) {
		return ErrReplay
	}
	return nil
}

type NonceSet struct {
	mu     sync.Mutex
	values map[string]time.Time
}

func NewNonceSet() *NonceSet { return &NonceSet{values: map[string]time.Time{}} }
func (s *NonceSet) Use(value string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.values[value]; ok {
		return false
	}
	s.values[value] = time.Now()
	if len(s.values) > 10000 {
		for k, t := range s.values {
			if time.Since(t) > MaxClockSkew*2 {
				delete(s.values, k)
			}
		}
	}
	return true
}

func JSONMessage(m DirectEncryptedMessage) ([]byte, error) { return json.Marshal(m) }
