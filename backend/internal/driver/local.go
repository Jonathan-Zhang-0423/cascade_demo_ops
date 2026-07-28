package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/model"
)

type LocalDriver struct {
	NodeBinary  string
	WorkerPath  string
	Environment map[string]string
}

func NewLocalDriver(nodeBinary string, workerPath string, environment ...map[string]string) *LocalDriver {
	if nodeBinary == "" {
		nodeBinary = "node"
	}
	return &LocalDriver{NodeBinary: nodeBinary, WorkerPath: workerPath, Environment: firstEnvironment(environment)}
}

func (d *LocalDriver) Record(ctx context.Context, request executor.RecordRequest) (executor.RecordResult, error) {
	var result executor.RecordResult
	err := d.call(ctx, "record", request, &result)
	return result, err
}

func (d *LocalDriver) Render(ctx context.Context, request executor.RenderRequest) (executor.RenderResult, error) {
	var result executor.RenderResult
	err := d.call(ctx, "render", request, &result)
	return result, err
}

func (d *LocalDriver) ProbeMedia(ctx context.Context, request executor.MediaProbeRequest) (executor.MediaProbeResult, error) {
	var result executor.MediaProbeResult
	err := d.call(ctx, "probe_media", request, &result)
	return result, err
}

func (d *LocalDriver) ValidateEditPlan(ctx context.Context, request executor.EditPlanValidationRequest) (model.DemoEditPlanValidationReport, error) {
	var result model.DemoEditPlanValidationReport
	err := d.call(ctx, "validate_edit_plan", request, &result)
	return result, err
}

func (d *LocalDriver) call(ctx context.Context, method string, params any, result any) error {
	if d.WorkerPath == "" {
		return errors.New("node worker path is required")
	}
	cmd := exec.Command(d.NodeBinary, d.WorkerPath)
	cmd.Env = childProcessEnvironment(d.Environment)
	configureProcessTree(cmd)
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
		_ = stdin.Close()
		waitErr := cmd.Wait()
		return fmt.Errorf("write node worker request: %w; process=%v; stderr=%s", err, waitErr, stderr.String())
	}
	_ = stdin.Close()

	var response rpcResponse
	decodeDone := make(chan error, 1)
	go func() { decodeDone <- json.NewDecoder(stdout).Decode(&response) }()
	select {
	case <-ctx.Done():
		_ = terminateProcessTree(cmd)
		_ = cmd.Wait()
		return ctx.Err()
	case err := <-decodeDone:
		if err != nil {
			_ = terminateProcessTree(cmd)
			_ = cmd.Wait()
			return fmt.Errorf("decode node worker response: %w; stderr=%s", err, stderr.String())
		}
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("node worker failed: %w; stderr=%s", err, stderr.String())
	}
	if response.Error != nil {
		return errors.New(response.Error.Message)
	}
	return json.Unmarshal(response.Result, result)
}

func firstEnvironment(values []map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	return values[0]
}

func childProcessEnvironment(overrides map[string]string) []string {
	if len(overrides) == 0 {
		return nil
	}
	environment := os.Environ()
	for name, value := range overrides {
		if strings.TrimSpace(name) == "" || value == "" {
			continue
		}
		prefix := name + "="
		filtered := environment[:0]
		for _, existing := range environment {
			key, _, found := strings.Cut(existing, "=")
			if !found || !strings.EqualFold(key, name) {
				filtered = append(filtered, existing)
			}
		}
		environment = append(filtered, prefix+value)
	}
	return environment
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
