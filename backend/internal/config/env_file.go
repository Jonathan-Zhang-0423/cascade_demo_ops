package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// LoadDotEnvFiles loads simple KEY=value files without overriding existing env.
// It is intentionally small so dev builds can use local secrets without adding
// a dotenv dependency or exposing values through runtime health.
func LoadDotEnvFiles(paths ...string) error {
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if err := loadDotEnvFile(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func DefaultDotEnvPaths(devRepoRoot string) []string {
	if strings.TrimSpace(devRepoRoot) == "" {
		return nil
	}
	return []string{
		filepath.Join(devRepoRoot, ".env"),
		filepath.Join(devRepoRoot, ".env.local"),
		filepath.Join(devRepoRoot, "backend", ".env"),
		filepath.Join(devRepoRoot, "backend", ".env.local"),
	}
}

func loadDotEnvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" || os.Getenv(key) != "" {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	return scanner.Err()
}
