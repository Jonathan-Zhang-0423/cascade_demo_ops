package main

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalCertificateCoversOnlyLocalGatewayNames(t *testing.T) {
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	certPEM, keyPEM, err := localCertificate(now, 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("certificate PEM missing")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
		if err := cert.VerifyHostname(host); err != nil {
			t.Fatalf("local certificate does not cover %s: %v", host, err)
		}
	}
	if err := cert.VerifyHostname("gateway.example"); err == nil {
		t.Fatal("development certificate must not cover public gateway names")
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil || keyBlock.Type != "PRIVATE KEY" {
		t.Fatal("PKCS#8 private key PEM missing")
	}
	if _, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateAndWriteRefusesToOverwriteExistingFiles(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "direct.crt")
	keyPath := filepath.Join(dir, "direct.key")
	if err := generateAndWrite(certPath, keyPath, 30, time.Now()); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := generateAndWrite(certPath, keyPath, 30, time.Now()); err == nil {
		t.Fatal("expected existing outputs to be preserved")
	}
	after, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatal("existing certificate was overwritten")
	}
}
