package credentialstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const githubCredentialTarget = "CascadeDemoOps/GitHub"
const directBrowserAgentCredentialTarget = "CascadeDemoOps/BrowserAgentDirect/AccessToken"
const directBrowserAgentIdentityTarget = "CascadeDemoOps/BrowserAgentDirect/InstallationKey"
const githubOAuthCredentialTarget = "CascadeDemoOps/GitHubOAuth"

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

func StoreDirectBrowserAgentToken(token string) error {
	token = strings.TrimSpace(token)
	if len(token) < 32 || len(token) > 4096 || strings.ContainsAny(token, "\r\n\x00") {
		return errors.New("Browser Agent access token has an invalid format")
	}
	return storeSecret(directBrowserAgentCredentialTarget, "browser-agent-direct", []byte(token))
}

func ReadDirectBrowserAgentToken() (string, error) {
	secret, err := readSecret(directBrowserAgentCredentialTarget)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(secret)), nil
}

func DeleteDirectBrowserAgentToken() error {
	return deleteSecret(directBrowserAgentCredentialTarget)
}

func StoreDirectBrowserAgentIdentity(identityJSON []byte) error {
	if len(identityJSON) == 0 || len(identityJSON) > 8192 {
		return errors.New("Browser Agent installation identity has an invalid format")
	}
	return storeSecret(directBrowserAgentIdentityTarget, "browser-agent-direct-installation", identityJSON)
}

func ReadDirectBrowserAgentIdentity() ([]byte, error) {
	return readSecret(directBrowserAgentIdentityTarget)
}

func StoreDirectBrowserAgentLease(projectID string, leaseJSON []byte) error {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" || len(projectID) > 256 || strings.ContainsAny(projectID, "\\/:\x00\r\n") {
		return errors.New("project id has an invalid format")
	}
	if len(leaseJSON) == 0 || len(leaseJSON) > 65536 {
		return errors.New("Browser Agent lease has an invalid format")
	}
	return storeSecret("CascadeDemoOps/BrowserAgentDirect/Lease/"+projectID, projectID, leaseJSON)
}

func ReadDirectBrowserAgentLease(projectID string) ([]byte, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, errors.New("project id is required")
	}
	return readSecret("CascadeDemoOps/BrowserAgentDirect/Lease/" + projectID)
}

func DeleteDirectBrowserAgentLease(projectID string) error {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return errors.New("project id is required")
	}
	return deleteSecret("CascadeDemoOps/BrowserAgentDirect/Lease/" + projectID)
}

func GitHubTokenConfigured() bool {
	token, err := ReadGitHubToken()
	return err == nil && token != ""
}

func StoreGitHubOAuthToken(token string) error {
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 4096 || strings.ContainsAny(token, "\r\n\x00") {
		return errors.New("GitHub OAuth token has an invalid format")
	}
	return storeSecret(githubOAuthCredentialTarget, "oauth", []byte(token))
}

func DeleteGitHubOAuthToken() error {
	return deleteSecret(githubOAuthCredentialTarget)
}
