package creative

import (
	"fmt"
	"strings"

	"cascade-demoops/backend/internal/model"
)

// CreativeQualityProjection maps existing FinalFilm/Renderer validation into
// the creative delivery summary. It is not a second renderer or review state
// machine; FinalFilm remains the authority for media validity and approval.
type CreativeQualityProjection struct {
	SchemaVersion    string   `json:"schema_version"`
	CheckedAt        string   `json:"checked_at"`
	DeliveryStatus   string   `json:"delivery_status"`
	QualityTier      string   `json:"quality_tier"`
	BlockingFailures []string `json:"blocking_failures,omitempty"`
	Degradations     []string `json:"degradations,omitempty"`
}

const QualityProjectionSchemaVersion = "demoops.creative_quality_projection.v2"

func ProjectQuality(validation model.FinalFilmOutputValidation, generatedCandidates int, coverage []model.CreativeActionCoverage) CreativeQualityProjection {
	projection := CreativeQualityProjection{
		SchemaVersion:    QualityProjectionSchemaVersion,
		CheckedAt:        validation.CheckedAt.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		DeliveryStatus:   validation.DeliveryStatus,
		QualityTier:      validation.QualityTier,
		BlockingFailures: append([]string{}, validation.BlockingFailures...),
		Degradations:     append([]string{}, validation.Degradations...),
	}
	if validation.Status != "passed" {
		projection.DeliveryStatus = "incomplete"
		projection.QualityTier = "blocked"
		if strings.TrimSpace(validation.Error) != "" {
			projection.BlockingFailures = append(projection.BlockingFailures, validation.Error)
		} else {
			projection.BlockingFailures = append(projection.BlockingFailures, "final_film_output_validation_failed")
		}
	}
	for _, action := range coverage {
		if action.Required && action.Status != "verified" {
			projection.DeliveryStatus = "incomplete"
			projection.QualityTier = "blocked"
			projection.BlockingFailures = append(projection.BlockingFailures, fmt.Sprintf("required_action_missing:%s", action.ActionID))
		}
	}
	if generatedCandidates == 0 {
		projection.Degradations = append(projection.Degradations, "generated_candidates_unavailable_using_fact_baseline")
		if projection.DeliveryStatus == "" {
			projection.DeliveryStatus = "complete"
		}
		if projection.QualityTier == "" || projection.QualityTier == "enterprise" {
			projection.QualityTier = "degraded"
		}
	}
	if projection.DeliveryStatus == "" {
		projection.DeliveryStatus = "complete"
	}
	if projection.QualityTier == "" {
		projection.QualityTier = "enterprise"
	}
	return projection
}
