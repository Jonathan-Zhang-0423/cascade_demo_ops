package app

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/media"
)

func finalFilmProviderRegistry(runtime config.AppRuntimeConfig) (*media.GeneratedShotProviderRegistry, error) {
	registry := media.NewGeneratedShotProviderRegistry()
	if err := registerFinalFilmH3(registry, runtime); err != nil {
		return nil, err
	}
	if err := registerFinalFilmSeedance25(registry, runtime); err != nil {
		return nil, err
	}
	return registry, nil
}

// finalFilmAssetPublisher is intentionally absent when private TOS is not
// fully configured. The FinalFilm Seedance bridge then records a concrete
// preparation failure and keeps the fact-track baseline deliverable.
func finalFilmAssetPublisher() media.AssetPublisher {
	config, configured := media.TOSAssetPublisherConfigFromEnv(os.Getenv)
	if !configured {
		return nil
	}
	publisher, err := media.NewTOSAssetPublisher(config, time.Now)
	if err != nil {
		return nil
	}
	return publisher
}

func registerFinalFilmH3(registry *media.GeneratedShotProviderRegistry, runtime config.AppRuntimeConfig) error {
	client, harnessConfig, configErr := finalFilmMiniMaxH3Client()
	if configErr != nil {
		return registerDisabledH3(registry, configErr.Error())
	}
	policy, policyErr := media.LoadMiniMaxH3WorkflowAdmissionPolicy(os.Getenv)
	if policyErr != nil {
		return registerDisabledH3(registry, policyErr.Error())
	}
	submitter, err := media.NewMiniMaxH3GovernedSubmitter(client, policy, nil)
	if err != nil {
		return err
	}
	adapter, err := media.NewMiniMaxH3ProviderAdapter(media.MiniMaxH3ProviderAdapterOptions{
		Enabled: true, Client: client, Canceller: client, Submitter: submitter,
		Downloader: media.HTTPMiniMaxH3OutputDownloader{Client: http.DefaultClient},
		Normalizer: media.FFmpegMiniMaxH3MediaNormalizer{FFmpegPath: runtime.FFmpegPath, FFprobePath: runtime.FFprobePath},
		Resolution: "2K", UseContextIR: false,
		GenerationPollAttempts: 180, GenerationPollInterval: 5 * time.Second,
	})
	if err != nil {
		return err
	}
	if harnessConfig.Mode != config.ArkMediaModeReal {
		return registerDisabledH3(registry, "MiniMax-H3 workflow requires real mode")
	}
	return registry.Register(adapter)
}

func registerFinalFilmSeedance25(registry *media.GeneratedShotProviderRegistry, runtime config.AppRuntimeConfig) error {
	credential := runtime.ModelProviders[config.ModelProviderSeedance]
	enabled := runtime.ArkMediaMode == config.ArkMediaModeReal && credential.Enabled && strings.EqualFold(strings.TrimSpace(os.Getenv("CASCADE_SEEDANCE_FINAL_FILM_ENABLED")), "true") && credential.DefaultModel == media.Seedance25ServerModel
	reason := "Seedance 2.5 final-film adapter requires real Ark mode, configured credentials, explicit CASCADE_SEEDANCE_FINAL_FILM_ENABLED=true, and SEEDANCE_MODEL=doubao-seedance-2-5-260628"
	options := media.Seedance25ProviderAdapterOptions{Enabled: enabled, DisabledReason: reason}
	if enabled {
		policy, policyErr := media.Seedance25AdmissionPolicyFromEnv(os.Getenv)
		if policyErr != nil {
			return policyErr
		}
		admission, admissionErr := media.NewSeedance25AdmissionGate(policy, nil)
		if admissionErr != nil {
			return admissionErr
		}
		options.Client = media.NewClient(runtime, http.DefaultClient)
		options.Downloader = media.HTTPMiniMaxH3OutputDownloader{Client: http.DefaultClient}
		options.Normalizer = media.FFmpegMiniMaxH3MediaNormalizer{FFmpegPath: runtime.FFmpegPath, FFprobePath: runtime.FFprobePath}
		options.PollAttempts = 180
		options.PollInterval = 5 * time.Second
		options.Admission = admission
	}
	adapter, err := media.NewSeedance25ProviderAdapter(options)
	if err != nil {
		return err
	}
	return registry.Register(adapter)
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

func registerDisabledH3(registry *media.GeneratedShotProviderRegistry, reason string) error {
	adapter, err := media.NewMiniMaxH3ProviderAdapter(media.MiniMaxH3ProviderAdapterOptions{
		Enabled: false, DisabledReason: fmt.Sprintf("MiniMax-H3 workflow disabled: %s", reason),
	})
	if err != nil {
		return err
	}
	return registry.Register(adapter)
}
