package sidecar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"cascade-demoops/backend/internal/config"
)

const VideoWorkerName = "video-worker"

type HealthCheck struct {
	Method    string `json:"method"`
	TimeoutMS int    `json:"timeout_ms"`
}

type SidecarSpec struct {
	Name       string            `json:"name"`
	Command    string            `json:"command"`
	Args       []string          `json:"args,omitempty"`
	WorkingDir string            `json:"working_dir,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Health     *HealthCheck      `json:"health_check,omitempty"`
}

type Manager struct {
	runtime    config.AppRuntimeConfig
	nodeBinary string
}

func NewManager(runtime config.AppRuntimeConfig) *Manager {
	nodeBinary := runtime.NodeBinaryPath
	if nodeBinary == "" {
		nodeBinary = defaultNodeBinary(runtime.ResourceRoot)
	}
	return &Manager{runtime: runtime, nodeBinary: nodeBinary}
}

func (m *Manager) VideoWorkerSpec() SidecarSpec {
	workerPath := m.runtime.SidecarPaths[VideoWorkerName]
	if workerPath == "" {
		workerPath = m.defaultVideoWorkerPath()
	}
	return SidecarSpec{
		Name:       VideoWorkerName,
		Command:    m.nodeBinary,
		Args:       []string{workerPath},
		WorkingDir: filepath.Dir(workerPath),
		Env:        map[string]string{"CASCADE_RUNTIME_PROFILE": string(m.runtime.Profile)},
		Health:     &HealthCheck{Method: "health", TimeoutMS: 5000},
	}
}

func (m *Manager) CallJSONRPC(ctx context.Context, spec SidecarSpec, method string, params any, result any) error {
	if spec.Command == "" {
		return errors.New("sidecar command is required")
	}
	if len(spec.Args) == 0 {
		return errors.New("sidecar args are required")
	}

	cmd := exec.CommandContext(ctx, spec.Command, spec.Args...)
	cmd.Dir = spec.WorkingDir
	cmd.Env = os.Environ()
	for key, value := range spec.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}

	request := rpcRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: params}
	if err := json.NewEncoder(stdin).Encode(request); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	_ = stdin.Close()

	var response rpcResponse
	if err := json.NewDecoder(stdout).Decode(&response); err != nil {
		_ = cmd.Wait()
		return fmt.Errorf("decode sidecar response: %w; stderr=%s", err, stderr.String())
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("sidecar failed: %w; stderr=%s", err, stderr.String())
	}
	if response.Error != nil {
		return errors.New(response.Error.Message)
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(response.Result, result)
}

func (m *Manager) defaultVideoWorkerPath() string {
	if m.runtime.Profile == config.ProfileDesktop {
		return filepath.Join(m.runtime.ResourceRoot, "sidecars", VideoWorkerName, "dist", "index.js")
	}
	return filepath.Join(m.runtime.DevRepoRoot, "video-worker", "dist", "index.js")
}

func defaultNodeBinary(resourceRoot string) string {
	if resourceRoot != "" {
		candidate := filepath.Join(resourceRoot, "runtimes", "node", nodeExecutableName())
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return "node"
}

func nodeExecutableName() string {
	if runtime.GOOS == "windows" {
		return "node.exe"
	}
	return "node"
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
