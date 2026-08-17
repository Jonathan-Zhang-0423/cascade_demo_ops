package app

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/media"
)

func finalFilmProviderRegistry(runtime config.AppRuntimeConfig) (*media.GeneratedShotProviderRegistry, error) {
	registry := media.NewGeneratedShotProviderRegistry()
	client, harnessConfig, configErr := finalFilmMiniMaxH3Client()
	if configErr != nil {
		return registryWithDisabledH3(registry, configErr.Error())
	}
	policy, policyErr := media.LoadMiniMaxH3WorkflowAdmissionPolicy(os.Getenv)
	if policyErr != nil {
		return registryWithDisabledH3(registry, policyErr.Error())
	}
	submitter, err := media.NewMiniMaxH3GovernedSubmitter(client, policy, nil)
	if err != nil {
		return nil, err
	}
	adapter, err := media.NewMiniMaxH3ProviderAdapter(media.MiniMaxH3ProviderAdapterOptions{
		Enabled: true, Client: client, Canceller: client, Submitter: submitter,
		Downloader: media.HTTPMiniMaxH3OutputDownloader{Client: http.DefaultClient},
		Normalizer: media.FFmpegMiniMaxH3MediaNormalizer{FFmpegPath: runtime.FFmpegPath, FFprobePath: runtime.FFprobePath},
		Resolution: "2K", UseContextIR: false,
		GenerationPollAttempts: 180, GenerationPollInterval: 5 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	if harnessConfig.Mode != config.ArkMediaModeReal {
		return registryWithDisabledH3(registry, "MiniMax-H3 workflow requires real mode")
	}
	if err := registry.Register(adapter); err != nil {
		return nil, err
	}
	return registry, nil
}

func finalFilmMiniMaxH3Client() (*media.MiniMaxH3Client, media.MiniMaxH3HarnessConfig, error) {
	settings, err := media.LoadMiniMaxH3HarnessConfig(os.Getenv)
	if err != nil {
		return nil, settings, err
	}
	return media.NewMiniMaxH3Client(media.MiniMaxH3ClientOptions{
		Mode: settings.Mode, APIKey: settings.APIKey, BaseURL: settings.BaseURL,
	}), settings, nil
}

func registryWithDisabledH3(registry *media.GeneratedShotProviderRegistry, reason string) (*media.GeneratedShotProviderRegistry, error) {
	adapter, err := media.NewMiniMaxH3ProviderAdapter(media.MiniMaxH3ProviderAdapterOptions{
		Enabled: false, DisabledReason: fmt.Sprintf("MiniMax-H3 workflow disabled: %s", reason),
	})
	if err != nil {
		return nil, err
	}
	if err := registry.Register(adapter); err != nil {
		return nil, err
	}
	return registry, nil
}
