package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"cascade-demoops/backend/internal/app"
	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/store"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "local desktop host address")
	check := flag.Bool("check", false, "initialize the desktop bridge and exit")
	openBrowser := flag.Bool("open", true, "open the desktop app URL in the system browser")
	flag.Parse()

	runtimeConfig, err := config.RuntimeConfigFromEnv()
	must(err)
	stateStore := store.NewFileStateStore(filepath.Join(runtimeConfig.DataRoot, "orchestrator_state"))
	service, err := app.NewService(runtimeConfig, stateStore)
	must(err)

	if *check {
		writeReady(runtimeConfig, "", "Desktop bridge is initialized.")
		return
	}

	webRoot := filepath.Join(runtimeConfig.ResourceRoot, "web")
	if err := ensureWebRoot(webRoot); err != nil {
		must(err)
	}
	listener, err := net.Listen("tcp", *addr)
	must(err)
	actualURL := "http://" + listener.Addr().String()
	handler := desktopHostHandler(app.NewDevHTTPServer(service).Handler(), webRoot)
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	writeReady(runtimeConfig, actualURL, "Desktop host is serving packaged web assets and local bridge APIs.")
	if *openBrowser {
		_ = openURL(actualURL)
	}
	must(server.Serve(listener))
}

func writeReady(runtimeConfig config.AppRuntimeConfig, url string, note string) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	must(encoder.Encode(map[string]any{
		"app":      "Cascade DemoOps Desktop",
		"profile":  runtimeConfig.Profile,
		"mode":     runtimeConfig.Mode,
		"database": runtimeConfig.DatabaseDialect,
		"ready":    true,
		"url":      url,
		"note":     note,
	}))
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func ensureWebRoot(webRoot string) error {
	indexPath := filepath.Join(webRoot, "index.html")
	info, err := os.Stat(indexPath)
	if err != nil {
		return fmt.Errorf("packaged web assets are missing: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("packaged web index is a directory: %s", indexPath)
	}
	return nil
}

func desktopHostHandler(api http.Handler, webRoot string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isBridgePath(r.URL.Path) {
			api.ServeHTTP(w, r)
			return
		}
		serveDesktopWeb(w, r, webRoot)
	})
}

func isBridgePath(path string) bool {
	return path == "/.well-known/cascade-exchange" ||
		strings.HasPrefix(path, "/v1/") ||
		strings.HasPrefix(path, "/aigc/")
}

func serveDesktopWeb(w http.ResponseWriter, r *http.Request, webRoot string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	cleanPath := strings.TrimPrefix(filepath.Clean("/"+r.URL.Path), string(filepath.Separator))
	candidate := filepath.Join(webRoot, filepath.FromSlash(cleanPath))
	if cleanPath != "" && pathWithin(candidate, webRoot) {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			http.ServeFile(w, r, candidate)
			return
		}
	}
	http.ServeFile(w, r, filepath.Join(webRoot, "index.html"))
}

func pathWithin(path string, root string) bool {
	absPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return false
	}
	absRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return rel == "." || rel != "" && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func openURL(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
