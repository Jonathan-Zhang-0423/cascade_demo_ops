package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"cascade-demoops/backend/internal/app"
	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/store"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:4317", "local bind address for the desktop dev bridge")
	flag.Parse()

	if err := app.EnsureLocalDevAddress(*addr); err != nil {
		must(err)
	}

	cwd, err := os.Getwd()
	must(err)
	devRepoRoot := config.DiscoverDevRepoRoot(cwd)
	must(config.LoadDotEnvFiles(config.DefaultDotEnvPaths(devRepoRoot)...))
	runtimeConfig, err := config.RuntimeConfigFromEnvWithRoot(devRepoRoot)
	must(err)
	applyDevLocalRoots(&runtimeConfig, devRepoRoot)
	stateStore := store.NewFileStateStore(filepath.Join(runtimeConfig.DataRoot, "dev_http_state"))
	service, err := app.NewService(runtimeConfig, stateStore)
	must(err)
	server := &http.Server{
		Addr:              *addr,
		Handler:           app.NewDevHTTPServer(service).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	fmt.Fprintf(os.Stdout, "Cascade DemoOps dev bridge listening on http://%s\n", *addr)
	must(server.ListenAndServe())
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func applyDevLocalRoots(runtimeConfig *config.AppRuntimeConfig, devRepoRoot string) {
	if runtimeConfig == nil || devRepoRoot == "" || runtimeConfig.Profile != config.ProfileDev {
		return
	}
	root := filepath.Join(devRepoRoot, ".cascade-dev")
	if os.Getenv("CASCADE_DATA_ROOT") == "" {
		runtimeConfig.DataRoot = filepath.Join(root, "data")
	}
	if os.Getenv("SQLITE_PATH") == "" {
		runtimeConfig.SQLitePath = filepath.Join(runtimeConfig.DataRoot, "cascade_demoops.db")
	}
	if os.Getenv("CASCADE_ARTIFACT_ROOT") == "" {
		runtimeConfig.ArtifactRoot = filepath.Join(root, "artifacts")
	}
	if os.Getenv("CASCADE_CACHE_ROOT") == "" {
		runtimeConfig.CacheRoot = filepath.Join(root, "cache")
	}
	if os.Getenv("CASCADE_LOG_ROOT") == "" {
		runtimeConfig.LogRoot = filepath.Join(root, "logs")
	}
}
