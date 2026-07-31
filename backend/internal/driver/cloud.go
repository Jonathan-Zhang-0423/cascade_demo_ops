package driver

import (
	"context"
	"errors"

	"cascade-demoops/backend/internal/executor"
)

type CloudDriver struct{}

func NewCloudDriver() *CloudDriver { return &CloudDriver{} }

type EnterpriseDriver struct{}

func NewEnterpriseDriver() *EnterpriseDriver { return &EnterpriseDriver{} }

func (d *CloudDriver) Record(ctx context.Context, request executor.RecordRequest) (executor.RecordResult, error) {
	return executor.RecordResult{}, errors.New("cloud worker driver is not implemented in MVP skeleton")
}

func (d *CloudDriver) Render(ctx context.Context, request executor.RenderRequest) (executor.RenderResult, error) {
	return executor.RenderResult{}, errors.New("cloud worker driver is not implemented in MVP skeleton")
}

func (d *EnterpriseDriver) Record(ctx context.Context, request executor.RecordRequest) (executor.RecordResult, error) {
	return executor.RecordResult{}, errors.New("enterprise microVM worker driver is not implemented in MVP skeleton")
}

func (d *EnterpriseDriver) Render(ctx context.Context, request executor.RenderRequest) (executor.RenderResult, error) {
	return executor.RenderResult{}, errors.New("enterprise microVM worker driver is not implemented in MVP skeleton")
}

func (d *CloudDriver) ProbeMedia(ctx context.Context, request executor.MediaProbeRequest) (executor.MediaProbeResult, error) {
	return executor.MediaProbeResult{}, errors.New("cloud worker media probe is not implemented in MVP skeleton")
}

func (d *EnterpriseDriver) ProbeMedia(ctx context.Context, request executor.MediaProbeRequest) (executor.MediaProbeResult, error) {
	return executor.MediaProbeResult{}, errors.New("enterprise microVM media probe is not implemented in MVP skeleton")
}
