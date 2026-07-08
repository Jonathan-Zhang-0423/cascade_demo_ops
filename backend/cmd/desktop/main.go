package main

import (
	"encoding/json"
	"fmt"
	"os"

	"cascade-demoops/backend/internal/app"
	"cascade-demoops/backend/internal/config"
)

func main() {
	runtimeConfig, err := config.RuntimeConfigFromEnv()
	must(err)
	_, err = app.NewDesktopBridge(runtimeConfig, nil)
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
