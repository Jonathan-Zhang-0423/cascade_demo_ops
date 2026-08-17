package app

import (
	"testing"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/media"
)

func TestFinalFilmProviderRegistryEnablesH3OnlyWithExplicitRouteKeyAndBudget(t *testing.T) {
	t.Setenv("CASCADE_VIDEO_PROVIDER", "minimax-h3")
	t.Setenv("CASCADE_VIDEO_MODEL", "minimax-h3")
	t.Setenv("MINIMAX_H3_API_KEY", "test-only-key")
	t.Setenv(media.MiniMaxH3CostMicrosPerSecondEnv, "130000")
	t.Setenv(media.MiniMaxH3MaxCostMicrosEnv, "10000000")
	registry, err := finalFilmProviderRegistry(config.AppRuntimeConfig{FFmpegPath: "ffmpeg", FFprobePath: "ffprobe"})
	if err != nil {
		t.Fatal(err)
	}
	descriptors := registry.Descriptors()
	h3 := finalFilmProviderDescriptor(descriptors, media.GeneratedShotProviderMiniMaxH3)
	seedance := finalFilmProviderDescriptor(descriptors, media.GeneratedShotProviderSeedance20)
	if len(descriptors) != 2 || h3 == nil || !h3.Enabled || seedance == nil || seedance.Enabled {
		t.Fatalf("unexpected final-film provider registry: %+v", descriptors)
	}

	t.Setenv(media.MiniMaxH3MaxCostMicrosEnv, "")
	registry, err = finalFilmProviderRegistry(config.AppRuntimeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if descriptor := finalFilmProviderDescriptor(registry.Descriptors(), media.GeneratedShotProviderMiniMaxH3); descriptor == nil || descriptor.Enabled {
		t.Fatalf("missing budget cap must disable H3: %+v", registry.Descriptors())
	}
}

func TestFinalFilmProviderRegistryEnablesSeedanceOnlyWithExplicitRealRoute(t *testing.T) {
	t.Setenv("CASCADE_SEEDANCE_FINAL_FILM_ENABLED", "true")
	runtime := config.AppRuntimeConfig{
		ArkMediaMode: config.ArkMediaModeReal, FFmpegPath: "ffmpeg", FFprobePath: "ffprobe",
		ModelProviders: map[config.ModelProvider]config.ModelProviderCredential{
			config.ModelProviderSeedance: {Provider: config.ModelProviderSeedance, APIKey: "test-only-key", BaseURL: "https://ark.example.test/api/v3", DefaultModel: media.Seedance20ServerModel, Enabled: true},
		},
		ModelTaskRoutes: map[config.ModelTask]config.ModelTaskRoute{
			config.ModelTaskVideoOperation: {Task: config.ModelTaskVideoOperation, Provider: config.ModelProviderSeedance, Model: media.Seedance20ServerModel},
		},
	}
	registry, err := finalFilmProviderRegistry(runtime)
	if err != nil {
		t.Fatal(err)
	}
	descriptor := finalFilmProviderDescriptor(registry.Descriptors(), media.GeneratedShotProviderSeedance20)
	if descriptor == nil || !descriptor.Enabled {
		t.Fatalf("explicit Seedance final-film route was not enabled: %+v", registry.Descriptors())
	}

	t.Setenv("CASCADE_SEEDANCE_FINAL_FILM_ENABLED", "false")
	registry, err = finalFilmProviderRegistry(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor := finalFilmProviderDescriptor(registry.Descriptors(), media.GeneratedShotProviderSeedance20); descriptor == nil || descriptor.Enabled {
		t.Fatalf("Seedance key and route enabled execution without explicit final-film opt-in: %+v", registry.Descriptors())
	}
}

func finalFilmProviderDescriptor(descriptors []media.GeneratedShotProviderDescriptor, provider string) *media.GeneratedShotProviderDescriptor {
	for index := range descriptors {
		if descriptors[index].Provider == provider {
			return &descriptors[index]
		}
	}
	return nil
}
