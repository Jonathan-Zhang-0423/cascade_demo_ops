package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"cascade-demoops/backend/internal/model"
)

const AppName = "CascadeDemoOps"

type RuntimeProfile string

const (
	ProfileDev     RuntimeProfile = "dev"
	ProfileDesktop RuntimeProfile = "desktop"
	ProfileCloud   RuntimeProfile = "cloud"
)

type DatabaseDialect string

const (
	DatabasePostgres DatabaseDialect = "postgres"
	DatabaseSQLite   DatabaseDialect = "sqlite"
)

type AppRuntimeConfig struct {
	Profile         RuntimeProfile
	Environment     string
	Mode            model.AppMode
	DatabaseDialect DatabaseDialect
	DatabaseURL     string
	SQLitePath      string
	DataRoot        string
	ArtifactRoot    string
	CacheRoot       string
	LogRoot         string
	ResourceRoot    string
	DevRepoRoot     string
	SidecarPaths    map[string]string
}

func RuntimeConfigFromEnv() (AppRuntimeConfig, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return AppRuntimeConfig{}, err
	}
	return RuntimeConfigFromEnvWithRoot(cwd)
}

func RuntimeConfigFromEnvWithRoot(devRepoRoot string) (AppRuntimeConfig, error) {
	profile := RuntimeProfile(envOrDefault("CASCADE_PROFILE", string(ProfileDev)))
	if !validProfile(profile) {
		return AppRuntimeConfig{}, errors.New("unsupported CASCADE_PROFILE")
	}

	mode := model.AppMode(envOrDefault("APP_MODE", string(model.AppModeDesktop)))
	if mode != model.AppModeDesktop && mode != model.AppModeWeb {
		return AppRuntimeConfig{}, errors.New("unsupported APP_MODE")
	}

	dataRoot := envOrDefault("CASCADE_DATA_ROOT", DefaultUserDataRoot(AppName))
	resourceRoot := envOrDefault("CASCADE_RESOURCE_ROOT", defaultResourceRoot(profile, devRepoRoot))
	dialect := DatabaseDialect(os.Getenv("DATABASE_DIALECT"))
	if dialect == "" {
		dialect = defaultDatabaseDialect(profile)
	}
	if dialect != DatabasePostgres && dialect != DatabaseSQLite {
		return AppRuntimeConfig{}, errors.New("unsupported DATABASE_DIALECT")
	}

	sqlitePath := envOrDefault("SQLITE_PATH", filepath.Join(dataRoot, "cascade_demoops.db"))
	cfg := AppRuntimeConfig{
		Profile:         profile,
		Environment:     envOrDefault("APP_ENV", defaultEnvironment(profile)),
		Mode:            mode,
		DatabaseDialect: dialect,
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		SQLitePath:      sqlitePath,
		DataRoot:        dataRoot,
		ArtifactRoot:    envOrDefault("CASCADE_ARTIFACT_ROOT", filepath.Join(dataRoot, "artifacts")),
		CacheRoot:       envOrDefault("CASCADE_CACHE_ROOT", filepath.Join(dataRoot, "cache")),
		LogRoot:         envOrDefault("CASCADE_LOG_ROOT", filepath.Join(dataRoot, "logs")),
		ResourceRoot:    resourceRoot,
		DevRepoRoot:     devRepoRoot,
		SidecarPaths: map[string]string{
			"video-worker": os.Getenv("NODE_WORKER_PATH"),
		},
	}
	return cfg, nil
}

func DefaultUserDataRoot(appName string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", appName)
	}
	return DefaultUserDataRootFor(runtime.GOOS, home, appName)
}

func DefaultUserDataRootFor(goos string, homeDir string, appName string) string {
	switch strings.ToLower(goos) {
	case "windows":
		return filepath.Join(homeDir, "AppData", "Roaming", appName)
	case "darwin":
		return filepath.Join(homeDir, "Library", "Application Support", appName)
	default:
		return filepath.Join(homeDir, ".config", appName)
	}
}

func defaultDatabaseDialect(profile RuntimeProfile) DatabaseDialect {
	if profile == ProfileCloud {
		return DatabasePostgres
	}
	return DatabaseSQLite
}

func defaultEnvironment(profile RuntimeProfile) string {
	if profile == ProfileCloud {
		return "production"
	}
	return "development"
}

func defaultResourceRoot(profile RuntimeProfile, devRepoRoot string) string {
	if profile == ProfileDesktop {
		exe, err := os.Executable()
		if err == nil && exe != "" {
			return filepath.Dir(exe)
		}
	}
	return devRepoRoot
}

func validProfile(profile RuntimeProfile) bool {
	switch profile {
	case ProfileDev, ProfileDesktop, ProfileCloud:
		return true
	default:
		return false
	}
}

func envOrDefault(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
