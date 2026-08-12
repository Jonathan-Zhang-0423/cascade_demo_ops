package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func main() {
	certPath := flag.String("cert", "artifacts/direct-local-tls/direct-localhost.crt", "new certificate output path")
	keyPath := flag.String("key", "artifacts/direct-local-tls/direct-localhost.key", "new private-key output path")
	days := flag.Int("days", 30, "certificate validity in days (1-90)")
	flag.Parse()

	if err := generateAndWrite(*certPath, *keyPath, *days, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Do not print key contents or derived secret material. These paths are
	// sufficient for wiring CASCADE_DIRECT_TLS_CERT/TLS_KEY locally.
	fmt.Printf("local Direct TLS certificate created: cert=%s key=%s\n", *certPath, *keyPath)
}

func generateAndWrite(certPath, keyPath string, days int, now time.Time) error {
	if certPath == "" || keyPath == "" || filepath.Clean(certPath) == filepath.Clean(keyPath) {
		return errors.New("distinct certificate and key output paths are required")
	}
	if days < 1 || days > 90 {
		return errors.New("certificate validity must be between 1 and 90 days")
	}
	certPEM, keyPEM, err := localCertificate(now, time.Duration(days)*24*time.Hour)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return err
	}
	if err := writeNewFile(certPath, certPEM, 0o600); err != nil {
		return fmt.Errorf("write certificate: %w", err)
	}
	if err := writeNewFile(keyPath, keyPEM, 0o600); err != nil {
		_ = os.Remove(certPath) // certPath was created by this invocation only.
		return fmt.Errorf("write private key: %w", err)
	}
	return nil
}

func writeNewFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	return file.Close()
}

func localCertificate(now time.Time, validity time.Duration) ([]byte, []byte, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, nil, err
	}
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Cascade Direct local development"},
		NotBefore:    now.Add(-5 * time.Minute),
		NotAfter:     now.Add(validity),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), nil
}
