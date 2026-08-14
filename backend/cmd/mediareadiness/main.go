// Command mediareadiness performs a local, non-network media configuration check.
// It never uploads assets, contacts a model provider, or starts a browser.
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
	report := media.MediaReadinessFromEnv(nil, nil)
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
	fmt.Fprintln(os.Stderr, "media readiness failed:", err)
	os.Exit(1)
}
