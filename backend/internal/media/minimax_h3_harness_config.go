package media

import (
	"errors"
	"strings"

	"cascade-demoops/backend/internal/config"
)

// MiniMaxH3HarnessConfig is loaded only by the explicitly invoked server-side
// harness command. It may reuse the generic MiniMax credential when the video
// task route explicitly selects MiniMax H3. The passive sidecar remains
// isolated and disabled by default.
type MiniMaxH3HarnessConfig struct {
	Mode         config.ArkMediaMode `json:"mode"`
	APIKey       string              `json:"-"`
	APIKeySource string              `json:"api_key_source"`
	BaseURL      string              `json:"base_url"`
	Provider     string              `json:"provider"`
	Model        string              `json:"model"`
}

func LoadMiniMaxH3HarnessConfig(getenv func(string) string) (MiniMaxH3HarnessConfig, error) {
	if getenv == nil {
		return MiniMaxH3HarnessConfig{}, errors.New("environment reader is required")
	}
	provider := strings.ToLower(strings.TrimSpace(getenv("CASCADE_VIDEO_PROVIDER")))
	model := strings.ToLower(strings.TrimSpace(getenv("CASCADE_VIDEO_MODEL")))
	if provider != "minimax" && provider != "minimax-h3" {
		return MiniMaxH3HarnessConfig{}, errors.New("CASCADE_VIDEO_PROVIDER must explicitly select minimax or minimax-h3")
	}
	if model != "minimax-h3" {
		return MiniMaxH3HarnessConfig{}, errors.New("CASCADE_VIDEO_MODEL must explicitly select minimax-h3")
	}
	key := strings.TrimSpace(getenv(MiniMaxH3APIKeyEnv))
	keySource := MiniMaxH3APIKeyEnv
	if key == "" {
		key = strings.TrimSpace(getenv("MINIMAX_API_KEY"))
		keySource = "MINIMAX_API_KEY"
	}
	if key == "" {
		return MiniMaxH3HarnessConfig{}, errors.New("MiniMax-H3 API key is missing")
	}
	baseURL := strings.TrimSpace(getenv(MiniMaxH3BaseURLEnv))
	if baseURL == "" {
		baseURL = strings.TrimSpace(getenv("MINIMAX_BASE_URL"))
	}
	if baseURL == "" {
		baseURL = defaultMiniMaxH3BaseURL
	}
	return MiniMaxH3HarnessConfig{
		Mode: config.ArkMediaModeReal, APIKey: key, APIKeySource: keySource,
		BaseURL: baseURL, Provider: provider, Model: MiniMaxH3Model,
	}, nil
}
