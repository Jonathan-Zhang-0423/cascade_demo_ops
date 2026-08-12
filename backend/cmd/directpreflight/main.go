package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

type check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

type report struct {
	SchemaVersion string  `json:"schema_version"`
	GeneratedAt   string  `json:"generated_at"`
	AppFormalRun  bool    `json:"app_formal_run"`
	Checks        []check `json:"checks"`
	Ready         bool    `json:"ready"`
}

type preflightOptions struct {
	Fixture string
	Worker  string
	Node    string
	FFmpeg  string
	FFprobe string
}

var requiredGatewayEnvironment = []string{
	"CASCADE_DIRECT_PUBLIC_HOST",
	"CASCADE_DIRECT_BOOTSTRAP_TOKEN",
	"CASCADE_DIRECT_WORKER_TOKEN",
	"CASCADE_DIRECT_TLS_CERT",
	"CASCADE_DIRECT_TLS_KEY",
}

func main() {
	fixture := flag.String("fixture", "contracts/exchange/v1/client_execution_package.browser_agent_outline.json", "Server-controlled fixture to structurally preflight")
	worker := flag.String("worker", "video-worker/dist/index.js", "video-worker entry path")
	node := flag.String("node", "node", "Node executable")
	ffmpeg := flag.String("ffmpeg", "ffmpeg", "FFmpeg executable")
	ffprobe := flag.String("ffprobe", "ffprobe", "FFprobe executable")
	out := flag.String("output", "artifacts/direct-preflight/latest/preflight.json", "preflight report path")
	flag.Parse()

	r := buildReport(preflightOptions{Fixture: *fixture, Worker: *worker, Node: *node, FFmpeg: *ffmpeg, FFprobe: *ffprobe}, time.Now())
	if err := os.MkdirAll(filepath.Dir(*out), 0o700); err != nil {
		fatal(err)
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(*out, append(data, '\n'), 0o600); err != nil {
		fatal(err)
	}
	fmt.Printf("Direct preflight ready=%t report=%s\n", r.Ready, *out)
	if !r.Ready {
		os.Exit(2)
	}
}

func buildReport(options preflightOptions, now time.Time) report {
	r := report{SchemaVersion: "cascade.browser_agent_direct_preflight.v1", GeneratedAt: now.UTC().Format(time.RFC3339Nano), Checks: []check{}, AppFormalRun: false}
	r.add("fixture_exists", fileExists(options.Fixture), options.Fixture)
	if data, err := os.ReadFile(options.Fixture); err != nil {
		r.add("fixture_readable", false, err.Error())
	} else {
		var pkg model.ClientExecutionPackage
		if err := json.Unmarshal(data, &pkg); err != nil {
			r.add("fixture_json", false, err.Error())
		} else {
			r.add("fixture_runtime_outline_v1", pkg.ExecutableScriptBundle != nil && pkg.ExecutableScriptBundle.ScriptManifest.Runtime == model.ExecutableScriptRuntimeBrowserAgentOutlineV1, "Server fixture only; not App formal evidence")
			if err := model.ValidateClientExecutionPackageForCloudExecution(&pkg); err != nil {
				r.add("fixture_package_validation", false, err.Error())
			} else {
				r.add("fixture_package_validation", true, "structural validation passed")
			}
		}
	}
	r.add("worker_entry_exists", fileExists(options.Worker), options.Worker)
	r.add("node_ready", commandReady(options.Node), options.Node)
	r.add("ffmpeg_ready", commandReady(options.FFmpeg), options.FFmpeg)
	r.add("ffprobe_ready", commandReady(options.FFprobe), options.FFprobe)
	for _, env := range requiredGatewayEnvironment {
		value := strings.TrimSpace(os.Getenv(env))
		r.add("env_"+strings.ToLower(env), value != "", "required for Gateway; value not recorded")
	}
	certPath := strings.TrimSpace(os.Getenv("CASCADE_DIRECT_TLS_CERT"))
	keyPath := strings.TrimSpace(os.Getenv("CASCADE_DIRECT_TLS_KEY"))
	r.add("tls_cert_file", certPath != "" && fileExists(certPath), "TLS certificate path must point to a regular file; value not recorded")
	r.add("tls_key_file", keyPath != "" && fileExists(keyPath), "TLS private-key path must point to a regular file; value not recorded")
	if certPath != "" && keyPath != "" && fileExists(certPath) && fileExists(keyPath) {
		certificate, leaf, err := loadTLSCertificate(certPath, keyPath)
		r.add("tls_key_pair", err == nil && certificate != nil, tlsCheckDetail(err, "certificate and private key match"))
		if err == nil && leaf != nil {
			r.add("tls_certificate_time_valid", !now.Before(leaf.NotBefore) && now.Before(leaf.NotAfter), "certificate must be within its validity window; dates not recorded")
			host := directPublicHostname(strings.TrimSpace(os.Getenv("CASCADE_DIRECT_PUBLIC_HOST")))
			hostErr := error(nil)
			if host == "" {
				hostErr = fmt.Errorf("public host is empty")
			} else {
				hostErr = leaf.VerifyHostname(host)
			}
			r.add("tls_certificate_public_host", hostErr == nil, tlsCheckDetail(hostErr, "certificate covers configured public host; host not recorded"))
		}
	}
	r.Ready = true
	for _, item := range r.Checks {
		if !item.Passed {
			r.Ready = false
		}
	}
	return r
}

func loadTLSCertificate(certPath, keyPath string) (*tls.Certificate, *x509.Certificate, error) {
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, nil, err
	}
	if len(pair.Certificate) == 0 {
		return nil, nil, fmt.Errorf("certificate chain is empty")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, nil, err
	}
	return &pair, leaf, nil
}

func directPublicHostname(value string) string {
	if host, _, err := net.SplitHostPort(value); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(value, "[]")
}

func tlsCheckDetail(err error, success string) string {
	if err == nil {
		return success
	}
	return "TLS validation failed; certificate or private-key contents not recorded"
}

func (r *report) add(name string, passed bool, detail string) {
	r.Checks = append(r.Checks, check{Name: name, Passed: passed, Detail: detail})
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func commandReady(name string) bool {
	if filepath.IsAbs(name) || strings.ContainsAny(name, `/\\`) {
		return fileExists(name)
	}
	_, err := exec.LookPath(name)
	return err == nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
