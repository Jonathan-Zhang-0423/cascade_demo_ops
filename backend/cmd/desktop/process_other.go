//go:build !windows

package main

import "os/exec"

func startAppProcess(name string, args ...string) error {
	return exec.Command(name, args...).Start()
}

func showDesktopErrorDialog(title string, message string) {
}
