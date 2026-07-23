//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"syscall"
)

func runInstallerProcess(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Start()
}

func showInstallerMessage(title string, message string, isError bool) {
	icon := "Information"
	if isError {
		icon = "Error"
	}
	script := fmt.Sprintf(
		`Add-Type -AssemblyName PresentationFramework; [System.Windows.MessageBox]::Show(%q, %q, 'OK', %q) | Out-Null`,
		message,
		title,
		icon,
	)
	_ = runInstallerProcess("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
}
