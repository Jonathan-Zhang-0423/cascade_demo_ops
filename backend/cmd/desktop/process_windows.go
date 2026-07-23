//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"syscall"
)

func startAppProcess(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Start()
}

func showDesktopErrorDialog(title string, message string) {
	script := fmt.Sprintf(
		`Add-Type -AssemblyName PresentationFramework; [System.Windows.MessageBox]::Show(%q, %q, 'OK', 'Error') | Out-Null`,
		message,
		title,
	)
	_ = startAppProcess("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
}
