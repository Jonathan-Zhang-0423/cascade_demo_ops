package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/store"
)

func TestLiveDirectLeaseProbe(t *testing.T) {
	if os.Getenv("CASCADE_LIVE_DIRECT_PROBE") != "1" {
		t.Skip("set CASCADE_LIVE_DIRECT_PROBE=1 to run the authorized live direct probe")
	}
	root := config.DefaultUserDataRoot(config.AppName)
	runtime := config.AppRuntimeConfig{Profile: config.ProfileDesktop, Environment: "production", Mode: model.AppModeDesktop, DataRoot: root, ArtifactRoot: filepath.Join(root, "artifacts"), CacheRoot: filepath.Join(root, "cache"), LogRoot: filepath.Join(root, "logs"), LLMMode: config.LLMModeDeterministic}
	service, err := NewService(runtime, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal("service_init")
	}
	lease, err := service.acquireDirectLease(context.Background(), "live_direct_probe")
	if err != nil {
		t.Fatal("lease_acquire:", sanitizeLiveProbeError(err))
	}
	defer service.deleteDirectLease("live_direct_probe")
	var status model.DirectJobStatus
	err = service.directProjectRead(context.Background(), "live_direct_probe", "/v1/direct/jobs/job_live_probe_missing", "job_status", &status)
	if err == nil || !strings.Contains(sanitizeLiveProbeError(err), "job_not_found") {
		t.Fatal("unexpected_authenticated_probe_result:", sanitizeLiveProbeError(err))
	}
	identity, err := service.readExistingDirectInstallationIdentity()
	if err != nil {
		t.Fatal("identity_read")
	}
	publicKey, err := base64.StdEncoding.DecodeString(identity.PublicKeyBase64)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		t.Fatal("identity_public_key")
	}
	privateKey, err := base64.StdEncoding.DecodeString(identity.PrivateKeyBase64)
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		t.Fatal("identity_private_key")
	}
	nonce, err := model.NewDirectTransportNonce()
	if err != nil {
		t.Fatal("release_nonce")
	}
	now := time.Now().UTC()
	release := model.DirectLeaseReleaseRequest{ProtocolVersion: model.DirectTransportProtocolVersion, InstallationID: model.DirectInstallationID(ed25519.PublicKey(publicKey)), LeaseID: lease.LeaseID, TimestampUnixMS: now.UnixMilli(), RequestNonce: nonce, SigningPublicKeyBase64: identity.PublicKeyBase64}
	release.SignatureBase64 = base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.PrivateKey(privateKey), model.DirectLeaseReleaseRequestSigningPayload(release)))
	body, _ := json.Marshal(release)
	request, err := http.NewRequest(http.MethodPost, strings.TrimRight(service.effectiveDirectTransportURL(), "/")+"/v1/direct/leases/release", bytes.NewReader(body))
	if err != nil {
		t.Fatal("release_request")
	}
	token, err := service.readDirectToken()
	if err != nil {
		t.Fatal("token_read")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		t.Fatal("release_call")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNotFound {
		t.Fatal("release_status:", response.StatusCode)
	}
	t.Logf("live_direct_lease_probe ok port=%d authenticated_status=job_not_found release_status=%d", lease.DataPort, response.StatusCode)
}

func sanitizeLiveProbeError(err error) string {
	if err == nil {
		return ""
	}
	value := strings.ReplaceAll(strings.ReplaceAll(err.Error(), "\r", " "), "\n", " ")
	if len(value) > 160 {
		value = value[:160]
	}
	return value
}
