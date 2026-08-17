package media

import "testing"

func TestLoadMiniMaxH3WorkflowAdmissionPolicyRequiresVerifiedPriceAndCap(t *testing.T) {
	values := map[string]string{
		MiniMaxH3CostMicrosPerSecondEnv: "130000",
		MiniMaxH3MaxCostMicrosEnv:       "10000000",
	}
	policy, err := LoadMiniMaxH3WorkflowAdmissionPolicy(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	if policy.EstimatedCostMicrosPerOutputSecond != 130000 || policy.MaxEstimatedCostMicrosPerWindow != 10000000 || policy.MaxConcurrent != 1 {
		t.Fatalf("unexpected workflow policy: %+v", policy)
	}
	delete(values, MiniMaxH3CostMicrosPerSecondEnv)
	if _, err := LoadMiniMaxH3WorkflowAdmissionPolicy(func(key string) string { return values[key] }); err == nil {
		t.Fatal("expected missing verified price to disable the workflow")
	}
}
