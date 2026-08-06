package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"cascade-demoops/backend/internal/app"
	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/store"
)

func main() {
	startedAt := time.Now()
	defaultDesktopRuntimeEnv()
	addr := flag.String("addr", "127.0.0.1:0", "local desktop host address")
	check := flag.Bool("check", false, "initialize the desktop bridge and exit")
	openBrowser := flag.Bool("open", true, "open the desktop app window")
	nativeUI := flag.Bool("native", false, "deprecated legacy native interface (disabled; use the Wails desktop or direct bridge)")
	flag.Parse()

	runtimeConfig, err := config.RuntimeConfigFromEnv()
	must(err, runtimeConfig)
	logger := newDesktopLogger(runtimeConfig)
	defer logger.Close()
	logger.Printf("starting Cascade DemoOps Desktop pid=%d args=%q", os.Getpid(), os.Args)
	stateStore := store.NewFileStateStore(filepath.Join(runtimeConfig.DataRoot, "orchestrator_state"))
	service, err := app.NewService(runtimeConfig, stateStore)
	must(err, runtimeConfig)

	if *check {
		logger.Printf("desktop check completed in %s", time.Since(startedAt))
		writeReady(runtimeConfig, "", "Native desktop runtime is initialized.")
		return
	}
	if *nativeUI {
		logger.Printf("legacy native desktop ui disabled; formal execution requires the Wails direct Browser Agent bridge")
		writeReady(runtimeConfig, "", "legacy_native_disabled: use the Wails desktop or direct Browser Agent bridge")
		return
	}

	webRoot := filepath.Join(runtimeConfig.ResourceRoot, "web")
	if err := ensureWebRoot(webRoot); err != nil {
		must(err, runtimeConfig)
	}
	listener, err := net.Listen("tcp", *addr)
	must(err, runtimeConfig)
	actualURL := "http://" + listener.Addr().String()
	handler := desktopHostHandler(app.NewDevHTTPServer(service).Handler(), webRoot)
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	logger.Printf("desktop host ready url=%s resource_root=%s data_root=%s log=%s", actualURL, runtimeConfig.ResourceRoot, runtimeConfig.DataRoot, logger.Path())
	writeReady(runtimeConfig, actualURL, "Desktop host is serving packaged web assets and local bridge APIs.")
	if *openBrowser {
		if err := openDesktopWindow(actualURL, runtimeConfig); err != nil {
			logger.Printf("open desktop window failed: %v", err)
			_ = openURL(actualURL)
		}
	}
	err = server.Serve(listener)
	if err != nil && err != http.ErrServerClosed {
		must(err, runtimeConfig)
	}
	logger.Printf("desktop host stopped after %s", time.Since(startedAt))
}

func defaultDesktopRuntimeEnv() {
	if strings.TrimSpace(os.Getenv("CASCADE_PROFILE")) == "" {
		_ = os.Setenv("CASCADE_PROFILE", string(config.ProfileDesktop))
	}
	if strings.TrimSpace(os.Getenv("APP_MODE")) == "" {
		_ = os.Setenv("APP_MODE", "desktop")
	}
}

func writeReady(runtimeConfig config.AppRuntimeConfig, url string, note string) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	err := encoder.Encode(map[string]any{
		"app":      "Cascade DemoOps Desktop",
		"profile":  runtimeConfig.Profile,
		"mode":     runtimeConfig.Mode,
		"ui":       "native",
		"database": runtimeConfig.DatabaseDialect,
		"ready":    true,
		"url":      url,
		"note":     note,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func must(err error, runtimeConfig config.AppRuntimeConfig) {
	if err != nil {
		writeDesktopError(runtimeConfig, err)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type desktopLogger struct {
	file *os.File
	path string
}

func newDesktopLogger(runtimeConfig config.AppRuntimeConfig) *desktopLogger {
	logRoot := runtimeConfig.LogRoot
	if logRoot == "" {
		logRoot = filepath.Join(runtimeConfig.DataRoot, "logs")
	}
	_ = os.MkdirAll(logRoot, 0o755)
	path := filepath.Join(logRoot, "desktop-launcher.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return &desktopLogger{path: path}
	}
	return &desktopLogger{file: file, path: path}
}

func (l *desktopLogger) Printf(format string, args ...any) {
	if l == nil || l.file == nil {
		return
	}
	_, _ = fmt.Fprintf(l.file, "%s %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

func (l *desktopLogger) Close() {
	if l != nil && l.file != nil {
		_ = l.file.Close()
	}
}

func (l *desktopLogger) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

func writeDesktopError(runtimeConfig config.AppRuntimeConfig, err error) {
	logRoot := runtimeConfig.LogRoot
	if logRoot == "" {
		logRoot = filepath.Join(runtimeConfig.DataRoot, "logs")
	}
	_ = os.MkdirAll(logRoot, 0o755)
	logPath := filepath.Join(logRoot, "desktop-launcher.log")
	message := fmt.Sprintf("%s ERROR %v\n", time.Now().Format(time.RFC3339), err)
	_ = appendTextFile(logPath, message)
	if runtime.GOOS == "windows" {
		showWindowsError("Cascade DemoOps 启动失败", fmt.Sprintf("本地应用启动失败：\n\n%v\n\n诊断日志：\n%s", err, logPath))
	}
}

func appendTextFile(path string, value string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(value)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
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

func openDesktopWindow(url string, runtimeConfig config.AppRuntimeConfig) error {
	if runtime.GOOS != "windows" {
		return openURL(url)
	}
	userDataDir := filepath.Join(runtimeConfig.CacheRoot, "edge-app-window")
	_ = os.MkdirAll(userDataDir, 0o755)
	args := []string{
		"--app=" + url,
		"--new-window",
		"--disable-features=Translate",
		"--user-data-dir=" + userDataDir,
	}
	for _, candidate := range edgeCandidates() {
		if candidate == "" {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			return startHidden(candidate, args...)
		}
	}
	return fmt.Errorf("Microsoft Edge/WebView2 runtime was not found")
}

func edgeCandidates() []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	candidates := []string{}
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
		root := os.Getenv(env)
		if root == "" {
			continue
		}
		candidates = append(candidates, filepath.Join(root, "Microsoft", "Edge", "Application", "msedge.exe"))
	}
	return candidates
}

func openURL(url string) error {
	switch runtime.GOOS {
	case "windows":
		return startHidden("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		return startAppProcess("open", url)
	default:
		return startAppProcess("xdg-open", url)
	}
}

func startHidden(name string, args ...string) error {
	return startAppProcess(name, args...)
}

func showWindowsError(title string, message string) {
	showDesktopErrorDialog(title, message)
}
