//go:build js && wasm

package driver

import "os/exec"

// WebAssembly tests never spawn child processes. Keep the driver linkable so
// Node can execute the pure Server policy and orchestration test suite.
func configureProcessTree(_ *exec.Cmd) {}

func terminateProcessTree(_ *exec.Cmd) error { return nil }
