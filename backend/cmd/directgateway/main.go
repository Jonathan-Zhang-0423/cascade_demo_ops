package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"cascade-demoops/backend/internal/app"
	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/direct"
	"cascade-demoops/backend/internal/store"
)

func main() {
	cwd, err := os.Getwd()
	must(err)
	repoRoot := config.DiscoverDevRepoRoot(cwd)
	// Direct commands must resolve the same ignored local configuration as the
	// media preflights. Without this, a valid .env was silently ignored unless
	// every variable had also been exported into the current shell.
	must(config.LoadDotEnvFiles(config.DefaultDotEnvPaths(repoRoot)...))
	controlAddr := flag.String("control-addr", fmt.Sprintf(":%d", direct.ControlPort), "public TLS control/data router address")
	workerAddr := flag.String("worker-addr", fmt.Sprintf("127.0.0.1:%d", direct.WorkerPort), "loopback Worker address")
	baseHost := flag.String("public-host", os.Getenv("CASCADE_DIRECT_PUBLIC_HOST"), "approved public Gateway hostname")
	cert := flag.String("tls-cert", os.Getenv("CASCADE_DIRECT_TLS_CERT"), "TLS certificate path")
	key := flag.String("tls-key", os.Getenv("CASCADE_DIRECT_TLS_KEY"), "TLS private key path")
	embeddedWorker := flag.Bool("embedded-worker", false, "run the Worker scheduler in this process (local convenience only)")
	statePath := flag.String("state-path", os.Getenv("CASCADE_DIRECT_STATE_PATH"), "persistent Direct Gateway snapshot path")
	flag.Parse()
	bootstrap := os.Getenv("CASCADE_DIRECT_BOOTSTRAP_TOKEN")
	workerToken := os.Getenv("CASCADE_DIRECT_WORKER_TOKEN")
	if bootstrap == "" || workerToken == "" || *baseHost == "" || *cert == "" || *key == "" {
		fatal(errors.New("CASCADE_DIRECT_PUBLIC_HOST, CASCADE_DIRECT_BOOTSTRAP_TOKEN, CASCADE_DIRECT_WORKER_TOKEN, CASCADE_DIRECT_TLS_CERT and CASCADE_DIRECT_TLS_KEY are required"))
	}
	if *workerAddr != fmt.Sprintf("127.0.0.1:%d", direct.WorkerPort) {
		fatal(fmt.Errorf("worker address must remain 127.0.0.1:%d", direct.WorkerPort))
	}
	if err := validateControlAddr(*controlAddr); err != nil {
		fatal(err)
	}
	runtime, err := config.RuntimeConfigFromEnvWithRoot(repoRoot)
	must(err)
	applyDevLocalRoots(&runtime, repoRoot)
	service, err := app.NewService(runtime, store.NewFileStateStore(filepath.Join(runtime.DataRoot, "direct_gateway_state")))
	must(err)
	if *statePath == "" {
		*statePath = filepath.Join(runtime.DataRoot, "direct_gateway_state", "direct-v1-snapshot.json")
	}
	gateway, err := direct.NewPersistentGateway(*baseHost, 30*time.Minute, *statePath)
	must(err)
	directServer := app.NewDirectHTTPServerWithGateway(service, gateway, bootstrap, workerToken)
	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()
	if *embeddedWorker {
		go func() {
			if err := directServer.RunDirectWorkerScheduler(workerCtx, 500*time.Millisecond, 1); err != nil && !errors.Is(err, context.Canceled) {
				fmt.Fprintln(os.Stderr, "embedded direct worker scheduler stopped:", err)
			}
		}()
	}
	publicServer := &http.Server{Addr: *controlAddr, Handler: directServer.ControlHandler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 64 * 1024}
	workerServer := &http.Server{Addr: *workerAddr, Handler: directServer.WorkerHandler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 32 * 1024}
	dataServers := make([]*http.Server, 0, direct.DataPortMax-direct.DataPortMin+1)
	errCh := make(chan error, direct.DataPortMax-direct.DataPortMin+3)
	go func() { errCh <- publicServer.ListenAndServeTLS(*cert, *key) }()
	go func() { errCh <- workerServer.ListenAndServe() }()
	for port := direct.DataPortMin; port <= direct.DataPortMax; port++ {
		server := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: directServer.DataHandler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 64 * 1024}
		dataServers = append(dataServers, server)
		port := port
		server.Handler = directServer.DataHandlerForPort(port)
		go func(value *http.Server) { errCh <- value.ListenAndServeTLS(*cert, *key) }(server)
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case <-stop:
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	workerCancel()
	_ = publicServer.Shutdown(ctx)
	_ = workerServer.Shutdown(ctx)
	for _, server := range dataServers {
		_ = server.Shutdown(ctx)
	}
}

func validateControlAddr(value string) error {
	_, port, err := net.SplitHostPort(value)
	if err != nil {
		return fmt.Errorf("control address must include Direct TLS port %d", direct.ControlPort)
	}
	if port != fmt.Sprintf("%d", direct.ControlPort) {
		return fmt.Errorf("control address must use Direct TLS port %d", direct.ControlPort)
	}
	return nil
}

// Keep the local Direct Gateway in the same workspace-scoped dev roots as the
// desktop dev bridge. Otherwise the Gateway process may inherit the default
// per-user roaming artifact path, which is often unavailable to a
// non-interactive local Worker and prevents stage-event audit capture.
func applyDevLocalRoots(runtime *config.AppRuntimeConfig, repoRoot string) {
	if runtime == nil || repoRoot == "" || runtime.Profile != config.ProfileDev {
		return
	}
	root := filepath.Join(repoRoot, ".cascade-dev")
	if os.Getenv("CASCADE_DATA_ROOT") == "" {
		runtime.DataRoot = filepath.Join(root, "data")
	}
	if os.Getenv("SQLITE_PATH") == "" {
		runtime.SQLitePath = filepath.Join(runtime.DataRoot, "cascade_demoops.db")
	}
	if os.Getenv("CASCADE_ARTIFACT_ROOT") == "" {
		runtime.ArtifactRoot = filepath.Join(root, "artifacts")
	}
	if os.Getenv("CASCADE_CACHE_ROOT") == "" {
		runtime.CacheRoot = filepath.Join(root, "cache")
	}
	if os.Getenv("CASCADE_LOG_ROOT") == "" {
		runtime.LogRoot = filepath.Join(root, "logs")
	}
}

func must(err error) {
	if err != nil {
		fatal(err)
	}
}
func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
