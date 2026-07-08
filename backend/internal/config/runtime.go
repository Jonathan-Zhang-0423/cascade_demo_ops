package config

import (
	"encoding/json"
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
	Profile              RuntimeProfile
	Environment          string
	Mode                 model.AppMode
	DatabaseDialect      DatabaseDialect
	DatabaseURL          string
	SQLitePath           string
	DataRoot             string
	ArtifactRoot         string
	CacheRoot            string
	LogRoot              string
	ResourceRoot         string
	ResourceManifestPath string
	DevRepoRoot          string
	SidecarPaths         map[string]string
	NodeBinaryPath       string
}

type DesktopResourceManifest struct {
	App                     string            `json:"app"`
	ResourceContractVersion int               `json:"resource_contract_version"`
	Sidecars                map[string]string `json:"sidecars,omitempty"`
	Runtimes                map[string]string `json:"runtimes,omitempty"`
	Web                     string            `json:"web,omitempty"`
}

func RuntimeConfigFromEnv() (AppRuntimeConfig, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return AppRuntimeConfig{}, err
	}
	return RuntimeConfigFromEnvWithRoot(DiscoverDevRepoRoot(cwd))
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
	resourceRoot := resolveResourceRoot(profile, devRepoRoot, os.Getenv("CASCADE_RESOURCE_ROOT"))
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
		NodeBinaryPath: os.Getenv("NODE_BINARY_PATH"),
	}
	applyDesktopResourceManifest(&cfg)
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

func resolveResourceRoot(profile RuntimeProfile, devRepoRoot string, explicit string) string {
	if explicit != "" {
		return filepath.Clean(explicit)
	}
	return defaultResourceRoot(profile, devRepoRoot)
}

func defaultResourceRoot(profile RuntimeProfile, devRepoRoot string) string {
	if profile == ProfileDesktop {
		exe, err := os.Executable()
		if err == nil && exe != "" {
			return resolveDesktopResourceRootFromExeDir(filepath.Dir(exe), devRepoRoot)
		}
	}
	return devRepoRoot
}

func resolveDesktopResourceRootFromExeDir(exeDir string, devRepoRoot string) string {
	candidates := []string{
		filepath.Join(exeDir, "resources"),
		filepath.Join(exeDir, "..", "resources"),
		filepath.Join(exeDir, "..", "..", "package", "resources"),
		exeDir,
	}
	for _, candidate := range candidates {
		clean := filepath.Clean(candidate)
		if hasDesktopResourceManifest(clean) {
			return clean
		}
	}
	if exeDir != "" {
		return filepath.Clean(filepath.Join(exeDir, "resources"))
	}
	return devRepoRoot
}

func LoadDesktopResourceManifest(resourceRoot string) (DesktopResourceManifest, string, error) {
	path := filepath.Join(resourceRoot, "desktop-runtime.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return DesktopResourceManifest{}, path, err
	}
	var manifest DesktopResourceManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return DesktopResourceManifest{}, path, err
	}
	return manifest, path, nil
}

func DiscoverDevRepoRoot(start string) string {
	dir := filepath.Clean(start)
	for {
		if hasFile(dir, "pnpm-workspace.yaml") && hasFile(filepath.Join(dir, "backend"), "go.mod") {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return filepath.Clean(start)
		}
		dir = parent
	}
}

func applyDesktopResourceManifest(cfg *AppRuntimeConfig) {
	if cfg.Profile != ProfileDesktop || cfg.ResourceRoot == "" {
		return
	}
	manifest, path, err := LoadDesktopResourceManifest(cfg.ResourceRoot)
	if err != nil {
		return
	}
	cfg.ResourceManifestPath = path
	if cfg.SidecarPaths == nil {
		cfg.SidecarPaths = map[string]string{}
	}
	for name, relativePath := range manifest.Sidecars {
		if cfg.SidecarPaths[name] == "" {
			cfg.SidecarPaths[name] = resourcePath(cfg.ResourceRoot, relativePath)
		}
	}
	if cfg.NodeBinaryPath == "" && manifest.Runtimes["node"] != "" {
		cfg.NodeBinaryPath = resourcePath(cfg.ResourceRoot, manifest.Runtimes["node"])
	}
}

func resourcePath(resourceRoot string, value string) string {
	if value == "" || filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(resourceRoot, filepath.FromSlash(value))
}

func hasDesktopResourceManifest(resourceRoot string) bool {
	info, err := os.Stat(filepath.Join(resourceRoot, "desktop-runtime.json"))
	return err == nil && !info.IsDir()
}

func hasFile(dir string, name string) bool {
	info, err := os.Stat(filepath.Join(dir, name))
	return err == nil && !info.IsDir()
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
