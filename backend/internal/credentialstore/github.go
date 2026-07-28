package credentialstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const githubCredentialTarget = "CascadeDemoOps/GitHub"

func StoreGitHubToken(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("GitHub token is required")
	}
	if len(token) > 2048 || strings.ContainsAny(token, "\r\n\x00") {
		return errors.New("GitHub token has an invalid format")
	}
	return storeSecret(githubCredentialTarget, "x-access-token", []byte(token))
}

type DemoCredential struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func StoreDemoCredential(ref, username, password string) error {
	ref = strings.TrimSpace(ref)
	username = strings.TrimSpace(username)
	if ref == "" || strings.ContainsAny(ref, "\\/:\x00\r\n") {
		return errors.New("demo credential ref has an invalid format")
	}
	if username == "" || password == "" {
		return errors.New("demo username and password are required")
	}
	payload, err := json.Marshal(DemoCredential{Username: username, Password: password})
	if err != nil {
		return err
	}
	return storeSecret(fmt.Sprintf("CascadeDemoOps/Demo/%s", ref), username, payload)
}

func ReadDemoCredential(ref string) (DemoCredential, error) {
	payload, err := readSecret(fmt.Sprintf("CascadeDemoOps/Demo/%s", strings.TrimSpace(ref)))
	if err != nil {
		return DemoCredential{}, err
	}
	var credential DemoCredential
	if err := json.Unmarshal(payload, &credential); err != nil {
		return DemoCredential{}, err
	}
	return credential, nil
}

func ReadGitHubToken() (string, error) {
	secret, err := readSecret(githubCredentialTarget)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(secret)), nil
}

func DeleteGitHubToken() error {
	return deleteSecret(githubCredentialTarget)
}

func GitHubTokenConfigured() bool {
	token, err := ReadGitHubToken()
	return err == nil && token != ""
}
