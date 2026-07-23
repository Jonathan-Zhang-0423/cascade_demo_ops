//go:build !windows

package main

import (
	"cascade-demoops/backend/internal/app"
	"cascade-demoops/backend/internal/config"
)

func supportsNativeDesktopUI() bool {
	return false
}

func runNativeDesktopUI(runtimeConfig config.AppRuntimeConfig, service *app.Service, logger *desktopLogger) error {
	return nil
}
