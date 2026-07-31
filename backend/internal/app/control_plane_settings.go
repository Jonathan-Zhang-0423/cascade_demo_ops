package app

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const controlPlaneSettingsFileName = "control_plane.json"

type ControlPlaneSettingsRequest struct {
	BaseURL string `json:"base_url"`
}

type controlPlaneSettingsFile struct {
	BaseURL string    `json:"base_url"`
	SavedAt time.Time `json:"saved_at"`
}

func (s *Service) SaveControlPlaneBaseURL(raw string) error {
	baseURL, err := validateControlPlaneBaseURL(raw)
	if err != nil {
		return err
	}
	s.controlPlaneMu.Lock()
	defer s.controlPlaneMu.Unlock()
	path := filepath.Join(s.runtime.DataRoot, controlPlaneSettingsFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(controlPlaneSettingsFile{BaseURL: baseURL, SavedAt: time.Now().UTC()}, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, payload, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		// Windows does not replace an existing file with os.Rename.
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			_ = os.Remove(temporary)
			return removeErr
		}
		if retryErr := os.Rename(temporary, path); retryErr != nil {
			_ = os.Remove(temporary)
			return retryErr
		}
	}
	s.controlPlaneURL = baseURL
	return s.clearExchangeServerSession()
}

func (s *Service) effectiveControlPlaneBaseURL() string {
	s.controlPlaneMu.RLock()
	persisted := strings.TrimRight(strings.TrimSpace(s.controlPlaneURL), "/")
	s.controlPlaneMu.RUnlock()
	if persisted != "" {
		return persisted
	}
	return strings.TrimRight(strings.TrimSpace(s.runtime.CloudExchangeBaseURL), "/")
}

func loadPersistedControlPlaneURL(dataRoot string) string {
	data, err := os.ReadFile(filepath.Join(dataRoot, controlPlaneSettingsFileName))
	if err != nil {
		return ""
	}
	var settings controlPlaneSettingsFile
	if json.Unmarshal(data, &settings) != nil {
		return ""
	}
	baseURL, err := validateControlPlaneBaseURL(settings.BaseURL)
	if err != nil {
		return ""
	}
	return baseURL
}

func validateControlPlaneBaseURL(raw string) (string, error) {
	value := strings.TrimRight(strings.TrimSpace(raw), "/")
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("control plane must be a server base URL without credentials, query, or fragment")
	}
	local := parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost"
	if parsed.Scheme != "https" && !(local && parsed.Scheme == "http") {
		return "", errors.New("control plane requires HTTPS; loopback HTTP is allowed only for SSH forwarding")
	}
	return value, nil
}

func (s *Service) clearExchangeServerSession() error {
	store := s.exchangeIdentityStore()
	record, err := store.load()
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	record.ServerKeyID = ""
	record.ServerPublicKeyBase64 = ""
	record.SessionID = ""
	record.SessionToken = ""
	record.SessionExpiresAt = time.Time{}
	record.ResultRecipientKeyID = ""
	record.ExchangeBaseURL = ""
	record.LastBootstrapChallengeID = ""
	record.UpdatedAt = time.Now().UTC()
	return store.save(record)
}
