//go:build !windows

package main

import "os/exec"

func runInstallerProcess(name string, args ...string) error {
	return exec.Command(name, args...).Start()
}

func showInstallerMessage(title string, message string, isError bool) {
}
