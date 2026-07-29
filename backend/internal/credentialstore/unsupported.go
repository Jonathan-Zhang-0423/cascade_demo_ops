//go:build !windows

package credentialstore

import "errors"

func storeSecret(string, string, []byte) error {
	return errors.New("OS credential storage is not supported on this platform")
}

func readSecret(string) ([]byte, error) {
	return nil, errors.New("OS credential storage is not supported on this platform")
}

func deleteSecret(string) error {
	return errors.New("OS credential storage is not supported on this platform")
}
