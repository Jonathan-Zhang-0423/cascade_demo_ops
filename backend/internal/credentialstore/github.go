package credentialstore

import (
	"errors"
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
