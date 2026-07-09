package agents

import (
	"encoding/json"
	"testing"

	"cascade-demoops/backend/internal/model"
)

func TestFlexibleStringSliceAcceptsLLMStringAndObjects(t *testing.T) {
	var values flexibleStringSlice
	if err := json.Unmarshal([]byte(`"alpha、beta, gamma"`), &values); err != nil {
		t.Fatal(err)
	}
	if got := stringSlice(values); len(got) != 3 || got[0] != "alpha" || got[1] != "beta" || got[2] != "gamma" {
		t.Fatalf("unexpected split values: %#v", got)
	}

	if err := json.Unmarshal([]byte(`[{"name":"hero"},{"label":"cta"},"docs"]`), &values); err != nil {
		t.Fatal(err)
	}
	if got := stringSlice(values); len(got) != 3 || got[0] != "cta" || got[1] != "docs" || got[2] != "hero" {
		t.Fatalf("unexpected object values: %#v", got)
	}
}

func TestFlexibleDemoUseCasesAcceptsLLMObjects(t *testing.T) {
	var useCases flexibleDemoUseCases
	if err := json.Unmarshal([]byte(`[{"use_case":"产品发布"},{"name":"用户文档"},"sales"]`), &useCases); err != nil {
		t.Fatal(err)
	}
	got := []model.DemoUseCase(useCases)
	want := []model.DemoUseCase{model.DemoUseCaseLaunch, model.DemoUseCaseUserDocumentation, model.DemoUseCaseSales}
	if len(got) != len(want) {
		t.Fatalf("use case count = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("use case[%d] = %s, want %s; all=%#v", i, got[i], want[i], got)
		}
	}
}
