//go:build windows

package driver

import (
	"fmt"
	"os/exec"
)

func configureProcessTree(_ *exec.Cmd) {}

func terminateProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	// taskkill /T follows the Node worker's descendants and also terminates FFmpeg.
	kill := exec.Command("taskkill.exe", "/PID", fmt.Sprint(cmd.Process.Pid), "/T", "/F")
	kill.Stdout = nil
	kill.Stderr = nil
	if err := kill.Run(); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
