package media

import (
	"errors"
	"net/http"
	"strings"

	"cascade-demoops/backend/internal/config"
)

const (
	MiniMaxH3ModeEnv    = "CASCADE_MINIMAX_H3_MODE"
	MiniMaxH3APIKeyEnv  = "MINIMAX_H3_API_KEY"
	MiniMaxH3BaseURLEnv = "MINIMAX_H3_BASE_URL"
)

// MiniMaxH3SidecarConfig is deliberately separate from CASCADE_ARK_MEDIA_MODE
// and the generic MINIMAX_API_KEY used by text/understanding models. The
// current acceptance route never constructs this sidecar.
type MiniMaxH3SidecarConfig struct {
	Mode    config.ArkMediaMode
	APIKey  string
	BaseURL string
}

func LoadMiniMaxH3SidecarConfig(getenv func(string) string) (MiniMaxH3SidecarConfig, error) {
	if getenv == nil {
		return MiniMaxH3SidecarConfig{}, errors.New("environment reader is required")
	}
	mode := config.ArkMediaMode(strings.TrimSpace(getenv(MiniMaxH3ModeEnv)))
	if mode == "" {
		mode = config.ArkMediaModeDisabled
	}
	switch mode {
	case config.ArkMediaModeDisabled, config.ArkMediaModeDryRun, config.ArkMediaModeReal:
	default:
		return MiniMaxH3SidecarConfig{}, errors.New("unsupported CASCADE_MINIMAX_H3_MODE")
	}
	return MiniMaxH3SidecarConfig{
		Mode:    mode,
		APIKey:  strings.TrimSpace(getenv(MiniMaxH3APIKeyEnv)),
		BaseURL: strings.TrimSpace(getenv(MiniMaxH3BaseURLEnv)),
	}, nil
}

// NewMiniMaxH3Sidecar returns nil while the dedicated H3 mode is disabled.
// It does not read CASCADE_ARK_MEDIA_MODE, MINIMAX_API_KEY, or Seedance keys.
func NewMiniMaxH3Sidecar(getenv func(string) string, httpClient *http.Client) (*MiniMaxH3Client, MiniMaxH3SidecarConfig, error) {
	settings, err := LoadMiniMaxH3SidecarConfig(getenv)
	if err != nil {
		return nil, settings, err
	}
	if settings.Mode == config.ArkMediaModeDisabled {
		return nil, settings, nil
	}
	return NewMiniMaxH3Client(MiniMaxH3ClientOptions{
		Mode:       settings.Mode,
		APIKey:     settings.APIKey,
		BaseURL:    settings.BaseURL,
		HTTPClient: httpClient,
	}), settings, nil
}
