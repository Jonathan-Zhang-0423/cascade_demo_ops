package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"cascade-demoops/backend/internal/app"
	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/store"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/fallback
var embeddedFrontend embed.FS

func main() {
	check := flag.Bool("check", false, "initialize the Wails desktop runtime and exit")
	flag.Parse()
	defaultDesktopRuntimeEnv()
	cwd, err := os.Getwd()
	must(err)
	repoRoot := config.DiscoverDevRepoRoot(cwd)
	must(config.LoadDotEnvFiles(config.DefaultDotEnvPaths(repoRoot)...))
	runtimeConfig, err := config.RuntimeConfigFromEnvWithRoot(repoRoot)
	must(err)
	frontend := desktopAssets(runtimeConfig, repoRoot)
	bridge, err := app.NewDesktopBridge(runtimeConfig, store.NewFileStateStore(filepath.Join(runtimeConfig.DataRoot, "desktop_state")))
	must(err)
	if *check {
		must(json.NewEncoder(os.Stdout).Encode(map[string]any{
			"app": "Cascade DemoOps Desktop", "profile": runtimeConfig.Profile, "mode": runtimeConfig.Mode,
			"ui": "wails_webview2", "ready": true,
		}))
		return
	}

	must(wails.Run(&options.App{
		Title:       "Cascade DemoOps",
		Width:       1440,
		Height:      920,
		MinWidth:    1180,
		MinHeight:   720,
		AssetServer: &assetserver.Options{Assets: frontend, Handler: bridge.HTTPHandler()},
		OnStartup:   bridge.Startup,
		Bind:        []interface{}{bridge},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			DisableWindowIcon:    false,
		},
	}))
}

func defaultDesktopRuntimeEnv() {
	if strings.TrimSpace(os.Getenv("CASCADE_PROFILE")) == "" {
		_ = os.Setenv("CASCADE_PROFILE", string(config.ProfileDesktop))
	}
	if strings.TrimSpace(os.Getenv("APP_MODE")) == "" {
		_ = os.Setenv("APP_MODE", "desktop")
	}
}

func desktopAssets(runtimeConfig config.AppRuntimeConfig, repoRoot string) fs.FS {
	candidates := []string{
		filepath.Join(runtimeConfig.ResourceRoot, "web"),
		filepath.Join(repoRoot, "frontend", "web", "dist"),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(filepath.Join(candidate, "index.html")); err == nil && !info.IsDir() {
			return os.DirFS(candidate)
		}
	}
	fallback, err := fs.Sub(embeddedFrontend, "frontend/fallback")
	must(err)
	return fallback
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
