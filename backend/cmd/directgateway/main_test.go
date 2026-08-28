package main

import (
	"path/filepath"
	"testing"

	"cascade-demoops/backend/internal/config"
)

func TestValidateControlAddrRequiresDirectTLSControlPort(t *testing.T) {
	for _, value := range []string{":18443", "0.0.0.0:18443", "[::1]:18443"} {
		if err := validateControlAddr(value); err != nil {
			t.Fatalf("expected valid Direct control address %q: %v", value, err)
		}
	}
	for _, value := range []string{":18442", "127.0.0.1:443", "missing-port"} {
		if err := validateControlAddr(value); err == nil {
			t.Fatalf("expected invalid Direct control address %q", value)
		}
	}
}

func TestApplyDevLocalRootsUsesWorkspaceScopedPaths(t *testing.T) {
	t.Setenv("CASCADE_DATA_ROOT", "")
	t.Setenv("SQLITE_PATH", "")
	t.Setenv("CASCADE_ARTIFACT_ROOT", "")
	t.Setenv("CASCADE_CACHE_ROOT", "")
	t.Setenv("CASCADE_LOG_ROOT", "")
	runtime := config.AppRuntimeConfig{Profile: config.ProfileDev}
	applyDevLocalRoots(&runtime, `D:\\Engine-7-8`)
	if runtime.DataRoot != filepath.Join(`D:\\Engine-7-8`, ".cascade-dev", "data") || runtime.ArtifactRoot != filepath.Join(`D:\\Engine-7-8`, ".cascade-dev", "artifacts") {
		t.Fatalf("dev Direct Gateway roots are not workspace scoped: %+v", runtime)
	}
}
