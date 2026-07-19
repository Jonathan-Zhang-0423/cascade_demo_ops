package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"

	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/model"
)

type LocalDriver struct {
	NodeBinary string
	WorkerPath string
}

func NewLocalDriver(nodeBinary string, workerPath string) *LocalDriver {
	if nodeBinary == "" {
		nodeBinary = "node"
	}
	return &LocalDriver{NodeBinary: nodeBinary, WorkerPath: workerPath}
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

func (d *LocalDriver) AnalyzeAudio(ctx context.Context, request executor.AudioAnalysisRequest) (executor.AudioAnalysisResult, error) {
	var result executor.AudioAnalysisResult
	err := d.call(ctx, "analyze_audio", request, &result)
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
		_ = cmd.Process.Kill()
		return err
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
