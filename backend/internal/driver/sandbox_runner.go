package driver

import (
	"fmt"

	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/model"
)

type SandboxRunnerConfig struct {
	Profile    string
	NodeBinary string
	WorkerPath string
}

func NewSandboxRunner(config SandboxRunnerConfig) (executor.Service, error) {
	profile := config.Profile
	if profile == "" {
		profile = model.SandboxProfileDev
	}
	switch profile {
	case model.SandboxProfileDev:
		return NewLocalDriver(config.NodeBinary, config.WorkerPath), nil
	case model.SandboxProfileMVPCloud:
		return NewCloudDriver(), nil
	case model.SandboxProfileEnterprise:
		return NewEnterpriseDriver(), nil
	default:
		return nil, fmt.Errorf("unsupported sandbox runner profile %q", profile)
	}
}
