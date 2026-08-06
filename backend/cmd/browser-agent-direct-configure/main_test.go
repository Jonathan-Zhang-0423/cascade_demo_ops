package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestValidateOptionsRejectsInjectedHostAndNonHTTPSControl(t *testing.T) {
	base := options{Host: "server.example", User: "ubuntu", Port: 22, PrivateKey: "identity", KnownHosts: "known_hosts", ControlURL: "https://app.example:18443", Timeout: 20 * time.Second}
	if got, err := validateOptions(base); err != nil || got != base.ControlURL {
		t.Fatalf("valid options rejected: got=%q err=%v", got, err)
	}
	injected := base
	injected.Host = "server.example;whoami"
	if _, err := validateOptions(injected); err == nil {
		t.Fatal("injected SSH host was accepted")
	}
	insecure := base
	insecure.ControlURL = "http://app.example:18443"
	if _, err := validateOptions(insecure); err == nil {
		t.Fatal("non-loopback HTTP control URL was accepted")
	}
}

func TestParseBootstrapTokenRejectsShortDuplicateAndOversizedValues(t *testing.T) {
	valid := strings.Repeat("A", 48)
	if got, err := parseBootstrapToken([]byte(valid + "\n")); err != nil || got != valid {
		t.Fatalf("valid token rejected: got=%q err=%v", got, err)
	}
	for _, raw := range []string{"short", valid + "\n" + valid, strings.Repeat("B", 4097)} {
		if _, err := parseBootstrapToken([]byte(raw)); err == nil {
			t.Fatalf("invalid token accepted (length=%d)", len(raw))
		}
	}
}

func TestResultAndErrorsNeverContainToken(t *testing.T) {
	token := strings.Repeat("sensitive-token-", 4)
	encoded, err := json.Marshal(result{OK: false, ErrorClass: classifyError(assertError(token))})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), token) {
		t.Fatal("redacted result leaked token")
	}
	if got := classifyError(assertError("bootstrap token " + token + " invalid")); got != "bootstrap_token_invalid" {
		t.Fatalf("unexpected error class: %s", got)
	}
}

type assertError string

func (e assertError) Error() string { return string(e) }

func TestGatewayBootstrapCommandIsFixedAndNarrow(t *testing.T) {
	if strings.Count(gatewayBootstrapCommand, "CASCADE_DIRECT_BOOTSTRAP_TOKEN") != 1 || !strings.Contains(gatewayBootstrapCommand, "/etc/cascade-browser-agent/gateway.env") {
		t.Fatalf("unexpected gateway bootstrap command: %q", gatewayBootstrapCommand)
	}
	if strings.ContainsAny(gatewayBootstrapCommand, "\r\n") {
		t.Fatal("gateway bootstrap command must remain a single fixed command")
	}
}
