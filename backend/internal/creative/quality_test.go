package creative

import (
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestProjectQualityKeepsProviderFailureAsDegradation(t *testing.T) {
	projection := ProjectQuality(model.FinalFilmOutputValidation{Status: "passed", DeliveryStatus: "complete", QualityTier: "enterprise", CheckedAt: time.Unix(1, 0)}, 0, nil)
	if projection.DeliveryStatus != "complete" || projection.QualityTier != "degraded" || len(projection.BlockingFailures) != 0 || len(projection.Degradations) == 0 {
		t.Fatalf("provider failure should project as degradation: %+v", projection)
	}
}

func TestProjectQualitySurfacesMissingRequiredAction(t *testing.T) {
	projection := ProjectQuality(model.FinalFilmOutputValidation{Status: "passed", DeliveryStatus: "complete", QualityTier: "enterprise", CheckedAt: time.Unix(1, 0)}, 1, []model.CreativeActionCoverage{{ActionID: "submit", Action: "click", Required: true, Status: "missing"}})
	if projection.DeliveryStatus != "incomplete" || projection.QualityTier != "blocked" || len(projection.BlockingFailures) != 1 {
		t.Fatalf("required action must be surfaced as blocking: %+v", projection)
	}
}
