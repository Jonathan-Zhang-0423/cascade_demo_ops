package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBuildReportServerFixtureReadyWithoutLeakingEnvironmentValues(t *testing.T) {
	fixture := repositoryFixture(t)
	runtimeFile := filepath.Join(t.TempDir(), "runtime-ready")
	if err := os.WriteFile(runtimeFile, []byte("test-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := writeTestCertificate(t, time.Unix(0, 0), "fixture.gateway.invalid")
	secrets := map[string]string{
		"CASCADE_DIRECT_PUBLIC_HOST":     "fixture.gateway.invalid:18443",
		"CASCADE_DIRECT_BOOTSTRAP_TOKEN": "bootstrap-secret-must-not-leak",
		"CASCADE_DIRECT_WORKER_TOKEN":    "worker-secret-must-not-leak",
		"CASCADE_DIRECT_TLS_CERT":        certPath,
		"CASCADE_DIRECT_TLS_KEY":         keyPath,
	}
	for name, value := range secrets {
		t.Setenv(name, value)
	}
	r := buildReport(preflightOptions{Fixture: fixture, Worker: runtimeFile, Node: runtimeFile, FFmpeg: runtimeFile, FFprobe: runtimeFile}, time.Unix(0, 0))
	if !r.Ready || r.AppFormalRun {
		t.Fatalf("expected ready Server-only report: %+v", r)
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range secrets {
		if strings.Contains(string(data), secret) {
			t.Fatalf("preflight report leaked environment value %q", secret)
		}
	}
}

func TestBuildReportRejectsMismatchedTLSKeyPair(t *testing.T) {
	fixture := repositoryFixture(t)
	runtimeFile := filepath.Join(t.TempDir(), "runtime-ready")
	if err := os.WriteFile(runtimeFile, []byte("test-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	certPath, _ := writeTestCertificate(t, time.Unix(0, 0), "localhost")
	_, otherKeyPath := writeTestCertificate(t, time.Unix(0, 0), "localhost")
	t.Setenv("CASCADE_DIRECT_PUBLIC_HOST", "localhost:18443")
	t.Setenv("CASCADE_DIRECT_BOOTSTRAP_TOKEN", "configured")
	t.Setenv("CASCADE_DIRECT_WORKER_TOKEN", "configured")
	t.Setenv("CASCADE_DIRECT_TLS_CERT", certPath)
	t.Setenv("CASCADE_DIRECT_TLS_KEY", otherKeyPath)
	r := buildReport(preflightOptions{Fixture: fixture, Worker: runtimeFile, Node: runtimeFile, FFmpeg: runtimeFile, FFprobe: runtimeFile}, time.Unix(0, 0))
	if r.Ready || checkPassed(r, "tls_key_pair") {
		t.Fatalf("mismatched TLS key pair must block readiness: %+v", r)
	}
}

func TestBuildReportRejectsCertificateForDifferentPublicHost(t *testing.T) {
	fixture := repositoryFixture(t)
	runtimeFile := filepath.Join(t.TempDir(), "runtime-ready")
	if err := os.WriteFile(runtimeFile, []byte("test-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := writeTestCertificate(t, time.Unix(0, 0), "localhost")
	t.Setenv("CASCADE_DIRECT_PUBLIC_HOST", "other.example:18443")
	t.Setenv("CASCADE_DIRECT_BOOTSTRAP_TOKEN", "configured")
	t.Setenv("CASCADE_DIRECT_WORKER_TOKEN", "configured")
	t.Setenv("CASCADE_DIRECT_TLS_CERT", certPath)
	t.Setenv("CASCADE_DIRECT_TLS_KEY", keyPath)
	r := buildReport(preflightOptions{Fixture: fixture, Worker: runtimeFile, Node: runtimeFile, FFmpeg: runtimeFile, FFprobe: runtimeFile}, time.Unix(0, 0))
	if r.Ready || checkPassed(r, "tls_certificate_public_host") {
		t.Fatalf("certificate for another host must block readiness: %+v", r)
	}
}

func checkPassed(r report, name string) bool {
	for _, item := range r.Checks {
		if item.Name == name {
			return item.Passed
		}
	}
	return false
}

func writeTestCertificate(t *testing.T, now time.Time, host string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath := filepath.Join(dir, "direct.crt")
	keyPath := filepath.Join(dir, "direct.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func TestBuildReportMissingFFmpegIsNotReady(t *testing.T) {
	fixture := repositoryFixture(t)
	runtimeFile := filepath.Join(t.TempDir(), "runtime-ready")
	if err := os.WriteFile(runtimeFile, []byte("test-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range requiredGatewayEnvironment {
		t.Setenv(name, "configured-for-test")
	}
	missing := filepath.Join(t.TempDir(), "ffmpeg-missing")
	r := buildReport(preflightOptions{Fixture: fixture, Worker: runtimeFile, Node: runtimeFile, FFmpeg: missing, FFprobe: runtimeFile}, time.Unix(0, 0))
	if r.Ready || r.AppFormalRun {
		t.Fatalf("missing FFmpeg must block readiness without becoming an App run: %+v", r)
	}
	found := false
	for _, item := range r.Checks {
		if item.Name == "ffmpeg_ready" {
			found = true
			if item.Passed {
				t.Fatal("missing FFmpeg check unexpectedly passed")
			}
		}
	}
	if !found {
		t.Fatal("ffmpeg readiness check missing")
	}
}

func repositoryFixture(t *testing.T) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test source path")
	}
	return filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "contracts", "exchange", "v1", "client_execution_package.browser_agent_outline.json")
}
