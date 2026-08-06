//go:build darwin

package credentialstore

import (
	"errors"
	"os/exec"
	"strings"
)

func storeSecret(target, username string, secret []byte) error {
	if strings.TrimSpace(target) == "" || len(secret) == 0 {
		return errors.New("Keychain credential is invalid")
	}
	command := exec.Command("/usr/bin/security", "add-generic-password", "-a", username, "-s", target, "-w", string(secret), "-U")
	if err := command.Run(); err != nil {
		return errors.New("macOS Keychain could not store the credential")
	}
	return nil
}

func readSecret(target string) ([]byte, error) {
	output, err := exec.Command("/usr/bin/security", "find-generic-password", "-s", target, "-w").Output()
	if err != nil {
		return nil, errors.New("credential was not found in macOS Keychain")
	}
	secret := strings.TrimSpace(string(output))
	if secret == "" {
		return nil, errors.New("credential is empty")
	}
	return []byte(secret), nil
}

func deleteSecret(target string) error {
	command := exec.Command("/usr/bin/security", "delete-generic-password", "-s", target)
	if err := command.Run(); err != nil {
		// Deleting a credential that is already absent remains idempotent.
		if _, readErr := readSecret(target); readErr != nil {
			return nil
		}
		return errors.New("macOS Keychain could not delete the credential")
	}
	return nil
}
