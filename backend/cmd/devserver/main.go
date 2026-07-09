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
