package model

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestDirectLeaseRequestRequiresInstallationKeySignature(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	request := DirectLeaseRequest{ProtocolVersion: DirectTransportProtocolVersion, InstallationID: DirectInstallationID(publicKey), ClientVersion: "test", TimestampUnixMS: now.UnixMilli(), RequestNonce: "signed_nonce", SigningPublicKeyBase64: base64.StdEncoding.EncodeToString(publicKey)}
	request.SignatureBase64 = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, DirectLeaseRequestSigningPayload(request)))
	if err := ValidateDirectLeaseRequest(request, now); err != nil {
		t.Fatalf("valid signed lease request rejected: %v", err)
	}
	for name, mutate := range map[string]func(*DirectLeaseRequest){
		"claimed installation": func(value *DirectLeaseRequest) { value.InstallationID = "direct_install_forged" },
		"timestamp":            func(value *DirectLeaseRequest) { value.TimestampUnixMS++ },
		"nonce":                func(value *DirectLeaseRequest) { value.RequestNonce += "x" },
		"signature": func(value *DirectLeaseRequest) {
			value.SignatureBase64 = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := request
			mutate(&candidate)
			if err := ValidateDirectLeaseRequest(candidate, now); err == nil {
				t.Fatal("tampered installation lease request was accepted")
			}
		})
	}
}

func TestDirectTransportRoundTripAndMetadataBinding(t *testing.T) {
	now := time.Date(2026, 8, 4, 2, 3, 4, 0, time.UTC)
	lease := directTestLease(now)
	input := map[string]any{"package_id": "pkg_real", "runtime": "browser-agent-outline-v1"}
	message, err := EncryptDirectTransportJSON(input, lease, "message_1", "client_execution_package", DirectTransportDirectionUpload, now)
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if err := DecryptDirectTransportJSON(message, lease, "client_execution_package", DirectTransportDirectionUpload, now, &output); err != nil {
		t.Fatal(err)
	}
	if output["package_id"] != "pkg_real" {
		t.Fatalf("unexpected output: %+v", output)
	}

	mutations := []func(*DirectEncryptedMessage){
		func(value *DirectEncryptedMessage) { value.MessageID = "message_2" },
		func(value *DirectEncryptedMessage) { value.MessageType = "recording_result" },
		func(value *DirectEncryptedMessage) { value.Direction = DirectTransportDirectionResult },
		func(value *DirectEncryptedMessage) { value.TimestampUnixMS++ },
		func(value *DirectEncryptedMessage) { value.PlaintextDigestSHA256 = strings.Repeat("0", 64) },
		func(value *DirectEncryptedMessage) { value.CiphertextBase64 = "AAAA" },
	}
	for index, mutate := range mutations {
		candidate := message
		mutate(&candidate)
		if err := DecryptDirectTransportJSON(candidate, lease, candidate.MessageType, candidate.Direction, time.UnixMilli(candidate.TimestampUnixMS), &output); err == nil {
			t.Fatalf("mutation %d was not rejected", index)
		}
	}
}

func TestDirectTransportRejectsExpiredLeaseAndClockSkew(t *testing.T) {
	now := time.Now().UTC()
	lease := directTestLease(now)
	message, err := EncryptDirectTransportJSON(map[string]string{"ok": "yes"}, lease, "message", "status", DirectTransportDirectionResult, now)
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]string
	if err := DecryptDirectTransportJSON(message, lease, "status", DirectTransportDirectionResult, now.Add(DirectTransportMaxClockSkew+time.Second), &output); err == nil {
		t.Fatal("clock-skewed message was accepted")
	}
	lease.ExpiresAt = now.Add(-time.Second)
	if _, err := EncryptDirectTransportJSON(output, lease, "message", "status", DirectTransportDirectionResult, now); err == nil {
		t.Fatal("expired lease was accepted")
	}
}

func TestDirectTransportRequestSignatureBindsPortTimestampAndPath(t *testing.T) {
	now := time.Now().UTC()
	lease := directTestLease(now)
	signature, err := DirectTransportRequestSignature(lease, "POST", "/v1/direct/packages", now.UnixMilli(), "request_nonce", "body_digest")
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyDirectTransportRequestSignature(lease, "POST", "/v1/direct/packages", now.UnixMilli(), "request_nonce", "body_digest", signature, now); err != nil {
		t.Fatal(err)
	}
	for _, changed := range []struct {
		path string
		port int
	}{{"/v1/direct/jobs", lease.DataPort}, {"/v1/direct/packages", lease.DataPort + 1}} {
		candidate := lease
		candidate.DataPort = changed.port
		if err := VerifyDirectTransportRequestSignature(candidate, "POST", changed.path, now.UnixMilli(), "request_nonce", "body_digest", signature, now); err == nil {
			t.Fatal("signature was not bound to path and port")
		}
	}
}

func TestDirectLeaseReleaseRequestRequiresInstallationSignature(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	seed := sha256.Sum256([]byte("release-install"))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	publicKey := privateKey.Public().(ed25519.PublicKey)
	request := DirectLeaseReleaseRequest{ProtocolVersion: DirectTransportProtocolVersion, InstallationID: DirectInstallationID(publicKey), LeaseID: "lease_release", TimestampUnixMS: now.UnixMilli(), RequestNonce: "release_nonce", SigningPublicKeyBase64: base64.StdEncoding.EncodeToString(publicKey)}
	request.SignatureBase64 = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, DirectLeaseReleaseRequestSigningPayload(request)))
	if err := ValidateDirectLeaseReleaseRequest(request, now); err != nil {
		t.Fatal(err)
	}
	request.LeaseID = "lease_other"
	if err := ValidateDirectLeaseReleaseRequest(request, now); err == nil {
		t.Fatal("release signature remained valid after lease binding changed")
	}
}

func directTestLease(now time.Time) DirectPortLease {
	return DirectPortLease{
		ProtocolVersion: DirectTransportProtocolVersion,
		LeaseID:         "lease_test", InstallationID: "install_test", DataURL: "https://browser.example:24443", DataPort: 24443,
		LeaseToken: "test-token-with-more-than-thirty-two-bytes-of-entropy-placeholder",
		IssuedAt:   now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), CryptoSuite: DirectTransportCryptoSuite,
	}
}
