package driver

import (
	"context"
	"errors"

	"cascade-demoops/backend/internal/executor"
)

type CloudDriver struct{}

func NewCloudDriver() *CloudDriver { return &CloudDriver{} }

func (d *CloudDriver) Record(ctx context.Context, request executor.RecordRequest) (executor.RecordResult, error) {
	return executor.RecordResult{}, errors.New("cloud worker driver is not implemented in MVP skeleton")
}

func (d *CloudDriver) Render(ctx context.Context, request executor.RenderRequest) (executor.RenderResult, error) {
	return executor.RenderResult{}, errors.New("cloud worker driver is not implemented in MVP skeleton")
}
