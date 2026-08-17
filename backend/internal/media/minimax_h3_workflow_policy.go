package media

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	MiniMaxH3CostMicrosPerSecondEnv = "CASCADE_MINIMAX_H3_COST_MICROS_PER_OUTPUT_SECOND"
	MiniMaxH3MaxCostMicrosEnv       = "CASCADE_MINIMAX_H3_MAX_COST_MICROS_PER_WINDOW"
)

// LoadMiniMaxH3WorkflowAdmissionPolicy reads Server-owned cost and quota
// controls. The verified vendor rate is configuration, never a code default,
// so a stale or absent price disables real workflow execution fail-closed.
func LoadMiniMaxH3WorkflowAdmissionPolicy(getenv func(string) string) (MiniMaxH3AdmissionPolicy, error) {
	if getenv == nil {
		return MiniMaxH3AdmissionPolicy{}, errors.New("environment reader is required")
	}
	costPerSecond, err := positiveInt64Env(getenv, MiniMaxH3CostMicrosPerSecondEnv)
	if err != nil {
		return MiniMaxH3AdmissionPolicy{}, err
	}
	maxCost, err := positiveInt64Env(getenv, MiniMaxH3MaxCostMicrosEnv)
	if err != nil {
		return MiniMaxH3AdmissionPolicy{}, err
	}
	return MiniMaxH3AdmissionPolicy{
		MaxConcurrent: 1, Window: time.Hour, MaxRequestsPerWindow: 4, MaxOutputSecondsPerWindow: 60,
		EstimatedCostMicrosPerOutputSecond: costPerSecond, MaxEstimatedCostMicrosPerWindow: maxCost,
		ProviderRequestTimeout: 45 * time.Second, IdempotencyTTL: 24 * time.Hour,
	}, nil
}

func positiveInt64Env(getenv func(string) string, key string) (int64, error) {
	value := strings.TrimSpace(getenv(key))
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, errors.New(key + " must be a positive integer")
	}
	return parsed, nil
}
