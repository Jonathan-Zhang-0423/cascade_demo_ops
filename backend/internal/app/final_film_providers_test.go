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
	if len(descriptors) != 1 || descriptors[0].Provider != media.GeneratedShotProviderMiniMaxH3 || !descriptors[0].Enabled {
		t.Fatalf("unexpected final-film provider registry: %+v", descriptors)
	}

	t.Setenv(media.MiniMaxH3MaxCostMicrosEnv, "")
	registry, err = finalFilmProviderRegistry(config.AppRuntimeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if registry.Descriptors()[0].Enabled {
		t.Fatalf("missing budget cap must disable H3: %+v", registry.Descriptors())
	}
}
