package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"cascade-demoops/backend/internal/app"
	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/store"
)

func main() {
	runtimeConfig, err := config.RuntimeConfigFromEnv()
	must(err)
	stateStore := store.NewFileStateStore(filepath.Join(runtimeConfig.DataRoot, "orchestrator_state"))
	_, err = app.NewDesktopBridge(runtimeConfig, stateStore)
	must(err)

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	must(encoder.Encode(map[string]any{
		"app":      "Cascade DemoOps Desktop",
		"profile":  runtimeConfig.Profile,
		"mode":     runtimeConfig.Mode,
		"database": runtimeConfig.DatabaseDialect,
		"ready":    true,
		"note":     "Desktop bridge is initialized; Wails shell wiring is the next packaging step.",
	}))
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
