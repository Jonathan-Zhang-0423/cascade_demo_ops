// Command browser-agent-direct-configure provisions the App's direct Browser
// Agent settings without ever accepting a token as an argument or writing it
// to a file. It reads the bootstrap token over an SSH host-key-pinned session
// from the server's root-only gateway.env, then delegates persistence to the
// same App service method used by the Wails UI.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"cascade-demoops/backend/internal/app"
	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/store"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const gatewayBootstrapCommand = "/usr/bin/sudo -n /usr/bin/sed -n 's/^CASCADE_DIRECT_BOOTSTRAP_TOKEN=//p' /etc/cascade-browser-agent/gateway.env"

var safeHost = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)
var safeUser = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9._-]*$`)

type options struct {
	Host       string
	User       string
	Port       int
	PrivateKey string
	KnownHosts string
	ControlURL string
	DataRoot   string
	Timeout    time.Duration
}

type result struct {
	OK             bool   `json:"ok"`
	Configured     bool   `json:"configured"`
	Reachable      bool   `json:"reachable"`
	TokenStored    bool   `json:"token_stored"`
	IdentityStored bool   `json:"installation_identity_stored"`
	ControlURLHost string `json:"control_url_host,omitempty"`
	ErrorClass     string `json:"error_class,omitempty"`
}

func main() {
	opts := options{}
	check := flag.Bool("check", false, "report provisioning-tool security capabilities and exit")
	flag.StringVar(&opts.Host, "ssh-host", "", "SSH server host (never a secret)")
	flag.StringVar(&opts.User, "ssh-user", "ubuntu", "SSH user")
	flag.IntVar(&opts.Port, "ssh-port", 22, "SSH port")
	flag.StringVar(&opts.PrivateKey, "ssh-private-key", "", "Ed25519 private key path")
	flag.StringVar(&opts.KnownHosts, "known-hosts", "", "strict known_hosts path")
	flag.StringVar(&opts.ControlURL, "control-url", "", "HTTPS Browser Agent control URL")
	flag.StringVar(&opts.DataRoot, "data-root", "", "App data root (defaults to the normal user data root)")
	flag.DurationVar(&opts.Timeout, "timeout", 20*time.Second, "SSH and provisioning timeout")
	flag.Parse()
	if *check {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"ready":                    true,
			"token_argument_supported": false,
			"strict_known_hosts":       true,
			"ssh_public_key_only":      true,
			"ssh_host_key_algorithm":   ssh.KeyAlgoED25519,
			"credential_store":         "windows_credential_manager",
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()
	res := result{}
	view, err := provision(ctx, opts)
	if err != nil {
		res.ErrorClass = classifyError(err)
		writeResult(res)
		os.Exit(1)
	}
	res.OK = true
	res.Configured = view.Configured
	res.Reachable = view.Reachable
	res.TokenStored = view.TokenConfigured
	res.IdentityStored = view.InstallationIDSuffix != ""
	res.ControlURLHost = view.ControlURLHost
	writeResult(res)
}

func provision(ctx context.Context, opts options) (app.DirectTransportRuntimeView, error) {
	controlURL, err := validateOptions(opts)
	if err != nil {
		return app.DirectTransportRuntimeView{}, err
	}
	privateKey, err := os.ReadFile(opts.PrivateKey)
	if err != nil {
		return app.DirectTransportRuntimeView{}, errors.New("ssh private key is unavailable")
	}
	signer, err := ssh.ParsePrivateKey(privateKey)
	for i := range privateKey {
		privateKey[i] = 0
	}
	if err != nil || signer == nil || signer.PublicKey().Type() != ssh.KeyAlgoED25519 {
		return app.DirectTransportRuntimeView{}, errors.New("ssh private key is invalid")
	}
	knownHostCallback, err := knownhosts.New(opts.KnownHosts)
	if err != nil {
		return app.DirectTransportRuntimeView{}, errors.New("known hosts parse failed")
	}
	hostKeyCallback := strictKnownHostCallback(knownHostCallback, opts.Host)
	sshConfig := &ssh.ClientConfig{
		User:              opts.User,
		Auth:              []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback:   hostKeyCallback,
		HostKeyAlgorithms: []string{ssh.KeyAlgoED25519},
		Timeout:           opts.Timeout,
	}
	address := net.JoinHostPort(opts.Host, fmt.Sprintf("%d", opts.Port))
	client, err := ssh.Dial("tcp", address, sshConfig)
	if err != nil {
		return app.DirectTransportRuntimeView{}, classifySSHConnectionFailure(err)
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return app.DirectTransportRuntimeView{}, errors.New("ssh session failed")
	}
	defer session.Close()
	stdout, err := session.StdoutPipe()
	if err != nil {
		return app.DirectTransportRuntimeView{}, errors.New("ssh output setup failed")
	}
	// stderr is deliberately discarded: it can contain remote paths or other
	// information not needed by the user-facing result.
	session.Stderr = io.Discard
	if err := session.Start(gatewayBootstrapCommand); err != nil {
		return app.DirectTransportRuntimeView{}, errors.New("bootstrap token command failed")
	}
	secret, readErr := io.ReadAll(io.LimitReader(stdout, 4097))
	waitErr := session.Wait()
	if readErr != nil || waitErr != nil || len(secret) > 4096 {
		for i := range secret {
			secret[i] = 0
		}
		return app.DirectTransportRuntimeView{}, errors.New("bootstrap token retrieval failed")
	}
	token, err := parseBootstrapToken(secret)
	for i := range secret {
		secret[i] = 0
	}
	if err != nil {
		return app.DirectTransportRuntimeView{}, err
	}
	dataRoot := strings.TrimSpace(opts.DataRoot)
	if dataRoot == "" {
		dataRoot = config.DefaultUserDataRoot(config.AppName)
	}
	runtime := config.AppRuntimeConfig{Profile: config.ProfileDesktop, Environment: "production", Mode: model.AppModeDesktop, DataRoot: dataRoot, ArtifactRoot: filepath.Join(dataRoot, "artifacts"), CacheRoot: filepath.Join(dataRoot, "cache"), LogRoot: filepath.Join(dataRoot, "logs"), LLMMode: config.LLMModeDeterministic}
	service, err := app.NewService(runtime, store.NewMemoryStateStore())
	if err != nil {
		return app.DirectTransportRuntimeView{}, errors.New("app service initialization failed")
	}
	view, err := service.SaveDirectTransportSettings(ctx, app.DirectTransportSettingsRequest{ControlURL: controlURL, AccessToken: token})
	token = ""
	if err != nil {
		return app.DirectTransportRuntimeView{}, errors.New("direct settings persistence failed")
	}
	return view, nil
}

func strictKnownHostCallback(base ssh.HostKeyCallback, expectedHost string) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := base(hostname, remote, key)
		if err == nil {
			return nil
		}
		// OpenSSH commonly supplies a bare host for port 22 while x/crypto/ssh
		// may supply host:port. Retry only the equivalent canonical form; never
		// bypass the known_hosts callback or accept an unknown key.
		host, port, splitErr := net.SplitHostPort(hostname)
		if splitErr == nil && port == "22" {
			if normalizedErr := base(net.JoinHostPort(host, port), remote, key); normalizedErr == nil {
				return nil
			}
		}
		if expectedHost != "" && expectedHost != hostname {
			if expectedErr := base(net.JoinHostPort(expectedHost, "22"), remote, key); expectedErr == nil {
				return nil
			}
		}
		return err
	}
}

func classifySSHConnectionFailure(err error) error {
	if err == nil {
		return errors.New("ssh connection failed")
	}
	var hostKeyError *knownhosts.KeyError
	if errors.As(err, &hostKeyError) {
		if len(hostKeyError.Want) == 0 {
			return errors.New("ssh host key unknown")
		}
		return errors.New("ssh host key mismatch")
	}
	var authError *ssh.ServerAuthError
	if errors.As(err, &authError) {
		return errors.New("ssh authentication failed")
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return errors.New("ssh network unavailable")
	}
	return errors.New("ssh connection failed")
}

func validateOptions(opts options) (string, error) {
	if !safeHost.MatchString(strings.TrimSpace(opts.Host)) {
		return "", errors.New("invalid ssh host")
	}
	if !safeUser.MatchString(strings.TrimSpace(opts.User)) {
		return "", errors.New("invalid ssh user")
	}
	if opts.Port < 1 || opts.Port > 65535 {
		return "", errors.New("invalid ssh port")
	}
	if strings.TrimSpace(opts.PrivateKey) == "" || strings.TrimSpace(opts.KnownHosts) == "" {
		return "", errors.New("ssh identity and known hosts are required")
	}
	if opts.Timeout <= 0 || opts.Timeout > 2*time.Minute {
		return "", errors.New("ssh timeout is invalid")
	}
	controlURL, err := app.ValidateDirectTransportControlURL(opts.ControlURL)
	if err != nil {
		return "", err
	}
	return controlURL, nil
}

func parseBootstrapToken(secret []byte) (string, error) {
	token := strings.TrimSpace(string(secret))
	if len(token) < 32 || len(token) > 4096 || strings.ContainsAny(token, "\r\n\x00") {
		return "", errors.New("bootstrap token format is invalid")
	}
	return token, nil
}

func classifyError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "known hosts parse"):
		return "known_hosts_parse_failed"
	case strings.Contains(message, "host key"):
		if strings.Contains(message, "unknown") {
			return "ssh_host_key_unknown"
		}
		return "ssh_host_key_mismatch"
	case strings.Contains(message, "known hosts"):
		return "known_hosts_invalid"
	case strings.Contains(message, "authentication"):
		return "ssh_authentication_failed"
	case strings.Contains(message, "network") || strings.Contains(message, "ssh"):
		return "ssh_unavailable"
	case strings.Contains(message, "control url"):
		return "control_url_invalid"
	case strings.Contains(message, "token"):
		return "bootstrap_token_invalid"
	default:
		return "provisioning_failed"
	}
}

func writeResult(value result) {
	_ = json.NewEncoder(os.Stdout).Encode(value)
}
