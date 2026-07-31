package credentialstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const githubCredentialTarget = "CascadeDemoOps/GitHub"

func modelCredentialTarget(provider string) (string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	switch provider {
	case "glm", "kimi", "minimax", "deepseek":
		return "CascadeDemoOps/Model/" + provider, nil
	default:
		return "", errors.New("unsupported model provider")
	}
}

func StoreModelAPIKey(provider, apiKey string) error {
	target, err := modelCredentialTarget(provider)
	if err != nil {
		return err
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" || len(apiKey) > 4096 || strings.ContainsAny(apiKey, "\r\n\x00") {
		return errors.New("model API key has an invalid format")
	}
	return storeSecret(target, provider, []byte(apiKey))
}

func ReadModelAPIKey(provider string) (string, error) {
	target, err := modelCredentialTarget(provider)
	if err != nil {
		return "", err
	}
	secret, err := readSecret(target)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(secret)), nil
}

func DeleteModelAPIKey(provider string) error {
	target, err := modelCredentialTarget(provider)
	if err != nil {
		return err
	}
	return deleteSecret(target)
}

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
