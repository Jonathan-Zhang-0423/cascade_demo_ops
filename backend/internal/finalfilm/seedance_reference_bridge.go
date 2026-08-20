package finalfilm

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

const seedanceReferenceAuditSchemaVersion = "demoops.final_film_seedance_reference_audit.v1"

// SeedanceReferenceAudit retains evidence of the Server-controlled reference
// route without retaining a presigned URL, which is a short-lived credential.
// It is safe to persist in the FinalFilm job and to expose in diagnostics.
type SeedanceReferenceAudit struct {
	SchemaVersion    string   `json:"schema_version"`
	IntentID         string   `json:"intent_id"`
	OperationID      string   `json:"operation_id"`
	Status           string   `json:"status"`
	FailureStage     string   `json:"failure_stage,omitempty"`
	ErrorClass       string   `json:"error_class,omitempty"`
	SourceStepID     string   `json:"source_step_id,omitempty"`
	SourceArtifactID string   `json:"source_artifact_id,omitempty"`
	PlanID           string   `json:"plan_id,omitempty"`
	Publisher        string   `json:"publisher,omitempty"`
	PublicationState string   `json:"publication_state,omitempty"`
	CanCallProvider  bool     `json:"can_call_provider"`
	BlockerCodes     []string `json:"blocker_codes,omitempty"`
	Message          string   `json:"message,omitempty"`
}

// prepareSeedance25Reference is deliberately narrow: a Director must have
// explicitly referenced exactly one raw recording and that recording must map
// to exactly one passed stage. There is no "first recording" fallback.
//
// The returned intent is process-local. Its TOS presigned URL is never put in
// the job, event, provider result, or audit record.
func (s *Service) prepareSeedance25Reference(ctx context.Context, job model.FinalFilmJob, intent media.GeneratedShotIntent, operationID string) (media.GeneratedShotIntent, SeedanceReferenceAudit, error) {
	audit := SeedanceReferenceAudit{SchemaVersion: seedanceReferenceAuditSchemaVersion, IntentID: intent.IntentID, OperationID: operationID, Status: "blocked"}
	// Every failure returned here is persisted into the job's generated-track
	// record. Keep it stable and credential-safe: TOS/FFmpeg provider errors
	// may include a presigned URL or an absolute path.
	fail := func(stage, class, message string, codes ...string) (media.GeneratedShotIntent, SeedanceReferenceAudit, error) {
		audit.FailureStage, audit.ErrorClass = stage, class
		audit.BlockerCodes, audit.Message = append([]string{}, codes...), message
		return intent, audit, errors.New(message)
	}
	if s.assetPublisher == nil {
		return fail("publisher", "publisher_unavailable", "Seedance reference publication requires a configured private TOS publisher", "seedance_reference_publisher_unavailable")
	}
	if strings.TrimSpace(operationID) == "" {
		return fail("publication_plan", "operation_id_missing", "Seedance reference publication operation identity is missing", "seedance_reference_operation_id_missing")
	}

	rawArtifactID, stepID, err := exactPassedRawRecordingReference(job.Catalog, intent)
	if err != nil {
		return fail("evidence_binding", "invalid_reference", "Seedance reference must bind exactly one raw recording from one passed stage", "seedance_reference_evidence_binding_invalid")
	}
	selection, err := executor.SelectSeedanceReferenceWindow(&job.Catalog, stepID)
	if err != nil {
		return fail("window_selection", "selection_blocked", "Seedance reference window could not be selected from the passed stage evidence", "seedance_reference_window_blocked")
	}
	audit.SourceArtifactID, audit.SourceStepID = rawArtifactID, selection.SourceStepID
	reference, err := executor.MaterializeSeedanceReferenceWindow(ctx, &job.Catalog, selection, s.referenceOutputDir(job.JobID, intent.IntentID), s.referenceNormalizer)
	if err != nil {
		return fail("normalization", "normalization_failed", "Seedance reference normalization failed; verify the passed recording and FFmpeg/FFprobe readiness", "seedance_reference_normalization_failed")
	}
	retention := s.referenceRetention
	if strings.TrimSpace(retention.Mode) == "" {
		retention = model.DefaultMediaDeliveryPreferences().TOSRetention
	}
	plan, err := executor.BuildSeedanceReferencePublicationPlan(&job.Catalog, selection, reference, job.SourcePackageID, operationID, retention, s.now().UTC())
	if err != nil {
		return fail("publication_plan", "publication_plan_invalid", "Seedance reference publication plan is invalid; verify the stage binding and media delivery policy", "seedance_reference_publication_plan_invalid")
	}
	audit.PlanID = plan.PlanID
	published, err := s.assetPublisher.PublishArkAssets(ctx, plan, reference)
	if err != nil {
		return fail("publication", "publication_failed", "Seedance reference publication failed; inspect the private TOS diagnostic using the operation ID", "seedance_reference_publication_failed")
	}
	audit.Publisher, audit.PublicationState = published.Publisher, published.Status
	for _, blocker := range published.Blockers {
		if strings.TrimSpace(blocker.Code) != "" {
			audit.BlockerCodes = append(audit.BlockerCodes, blocker.Code)
		}
	}
	if !published.CanUseForRealCall || len(published.Items) != 1 || published.Items[0].ProposedPublicRef == nil || strings.TrimSpace(published.Items[0].ProposedPublicRef.URI) == "" {
		if len(audit.BlockerCodes) == 0 {
			audit.BlockerCodes = []string{"seedance_reference_publication_not_callable"}
		}
		audit.Message = "private TOS publication did not produce a provider-callable reference"
		audit.FailureStage, audit.ErrorClass = "publication", "publication_not_callable"
		return intent, audit, errors.New(audit.Message)
	}

	prepared := intent
	prepared.References = replaceGeneratedShotReference(prepared.References, rawArtifactID, media.GeneratedShotReference{ArtifactID: reference.ID, URI: published.Items[0].ProposedPublicRef.URI, MimeType: "video/mp4", Usage: media.GeneratedShotReferenceGeneral})
	if err := media.ValidateGeneratedShotIntent(prepared); err != nil {
		return fail("provider_input", "provider_input_invalid", "Published Seedance reference did not meet the provider input contract", "seedance_reference_provider_input_invalid")
	}
	audit.Status, audit.CanCallProvider, audit.Message = "ready", true, "passed stage reference was normalized and published to private TOS for this one provider call"
	return prepared, audit, nil
}

func (s *Service) referenceOutputDir(jobID, intentID string) string {
	return fmt.Sprintf("%s/%s/generated/reference-inputs/%s", s.outputRoot, safePathComponent(jobID), safePathComponent(intentID))
}

func exactPassedRawRecordingReference(catalog model.AssetTimelineCatalog, intent media.GeneratedShotIntent) (string, string, error) {
	rawByID := map[string]struct{}{}
	for _, artifact := range catalog.Artifacts {
		if strings.EqualFold(strings.TrimSpace(artifact.Kind), "raw_recording") {
			rawByID[artifact.ID] = struct{}{}
		}
	}
	var rawID string
	for _, ref := range intent.References {
		if _, ok := rawByID[ref.ArtifactID]; ok {
			uri := strings.TrimSpace(ref.URI)
			if !strings.EqualFold(uri, "asset://"+ref.ArtifactID) && !strings.HasPrefix(strings.ToLower(uri), "https://") {
				return "", "", errors.New("raw recording references must use the Server-owned asset:// binding or an already-published HTTPS asset")
			}
			if rawID != "" && rawID != ref.ArtifactID {
				return "", "", errors.New("Seedance request references more than one raw recording; one explicit passed-stage reference is required")
			}
			rawID = ref.ArtifactID
		}
	}
	if rawID == "" {
		return "", "", errors.New("Seedance request must explicitly reference one raw_recording artifact; no automatic recording fallback is allowed")
	}
	matched := ""
	for _, step := range catalog.Steps {
		if !strings.EqualFold(strings.TrimSpace(step.Status), "passed") || !containsString(step.Artifacts, rawID) {
			continue
		}
		if matched != "" {
			return "", "", errors.New("Seedance raw recording reference maps to multiple passed stages; Server requires an unambiguous stage binding")
		}
		matched = step.StepID
	}
	if matched == "" {
		return "", "", errors.New("Seedance raw recording reference is not attached to a passed stage")
	}
	return rawID, matched, nil
}

func replaceGeneratedShotReference(refs []media.GeneratedShotReference, oldID string, replacement media.GeneratedShotReference) []media.GeneratedShotReference {
	result := append([]media.GeneratedShotReference{}, refs...)
	for index := range result {
		if result[index].ArtifactID == oldID {
			result[index] = replacement
		}
	}
	return result
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func safePathComponent(value string) string {
	value = strings.TrimSpace(value)
	var result strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			result.WriteRune(r)
		}
	}
	if result.Len() == 0 {
		return "unknown"
	}
	return result.String()
}
