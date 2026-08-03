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
	addr := flag.String("addr", "127.0.0.1:4317", "loopback address behind the DemoOps HTTPS reverse proxy")
	flag.Parse()
	if err := app.EnsureControlPlaneAddress(*addr); err != nil {
		must(err)
	}
	cwd, err := os.Getwd()
	must(err)
	repoRoot := config.DiscoverDevRepoRoot(cwd)
	runtimeConfig, err := config.RuntimeConfigFromEnvWithRoot(repoRoot)
	must(err)
	if runtimeConfig.Profile != config.ProfileCloud {
		must(fmt.Errorf("CASCADE_PROFILE=cloud is required for the control plane"))
	}
	stateStore := store.NewFileStateStore(filepath.Join(runtimeConfig.DataRoot, "control_plane_state"))
	service, err := app.NewService(runtimeConfig, stateStore)
	must(err)
	server := &http.Server{
		Addr:              *addr,
		Handler:           app.NewControlPlaneHTTPServer(service).ControlPlaneHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	fmt.Fprintf(os.Stdout, "Cascade DemoOps control plane listening on http://%s\n", *addr)
	must(server.ListenAndServe())
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
