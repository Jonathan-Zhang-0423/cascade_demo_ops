// Command seedance25readiness performs a local-only Seedance 2.5 admission
// check. It never contacts Ark, TOS, a callback endpoint, or the App.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/media"
)

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	repoRoot := config.DiscoverDevRepoRoot(cwd)
	if err := config.LoadDotEnvFiles(config.DefaultDotEnvPaths(repoRoot)...); err != nil {
		fatal(err)
	}
	report := media.Seedance25ReadinessFromEnv(nil, nil)
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fatal(err)
	}
	fmt.Println(string(data))
	if !report.Ready {
		os.Exit(2)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "seedance25readiness failed:", err)
	os.Exit(1)
}
